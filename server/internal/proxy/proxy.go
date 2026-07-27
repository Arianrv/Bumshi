// Package proxy implements Bumshi's server-side web proxy engine: a hardened,
// streaming HTTP and WebSocket forwarder that fetches user-supplied URLs on the
// server and returns them rewritten to route back through the proxy.
//
// Design goals that make this a better foundation than typical ad-hoc engines:
//   - a single static Go binary sharing the service's logging and metrics;
//   - streaming pass-through (low time-to-first-byte, large files and video)
//     instead of buffering whole responses;
//   - SSRF protection built in (see internal/proxy/ssrfguard) — the security
//     hole most ad-hoc proxies leave open;
//   - transparent WebSocket tunneling with no frame parsing;
//   - the client never leaks its IP upstream (privacy: the target sees only the
//     VPS).
//
// Dynamic-JavaScript coverage (URLs built at runtime) is provided by the client
// service-worker runtime in a later milestone; this package is the hardened
// transport core plus best-effort HTML/CSS rewriting.
package proxy

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bumshi/bumshi/server/internal/metrics"
	"github.com/bumshi/bumshi/server/internal/proxy/link"
	"github.com/bumshi/bumshi/server/internal/proxy/rewrite"
	"github.com/bumshi/bumshi/server/internal/proxy/ssrfguard"
)

const defaultRewriteMaxBytes = 8 << 20 // 8 MiB

// Collectors are the proxy engine's Prometheus metrics.
type Collectors struct {
	Requests *metrics.CounterVec   // labelled by outcome
	Upstream *metrics.HistogramVec // upstream response-header latency
}

// NewCollectors registers and returns the proxy metrics.
func NewCollectors(r *metrics.Registry) *Collectors {
	return &Collectors{
		Requests: metrics.NewCounterVec(r, "bumshi_proxy_requests_total", "Proxy requests by outcome.", "outcome"),
		Upstream: metrics.NewHistogramVec(r, "bumshi_proxy_upstream_duration_seconds", "Upstream response latency in seconds.", metrics.DefBuckets),
	}
}

// Options configures a Handler.
type Options struct {
	Client          *http.Client
	Logger          *slog.Logger
	Collectors      *Collectors
	RewriteMaxBytes int64
	// InjectHTML, if set, is applied to rewritten HTML bodies to insert the
	// client runtime bootstrap (see internal/webengine). It may be nil.
	InjectHTML func([]byte) []byte
	// Enabled, if set, gates the proxy at request time so the admin panel can
	// toggle it live. When it returns false, requests get 404. Nil means always
	// enabled.
	Enabled func() bool
	// ForceIPv4 makes the WebSocket tunnel dial upstream over IPv4 only. It
	// mirrors the fetch client's setting (see fetch.NewClient) so both transports
	// behave consistently.
	ForceIPv4 bool
}

// Handler is the proxy HTTP handler. Mount it under link.Prefix ("/p/").
type Handler struct {
	client     *http.Client
	logger     *slog.Logger
	col        *Collectors
	rewriteMax int64
	injectHTML func([]byte) []byte
	enabled    func() bool
	forceIPv4  bool
}

// New builds a Handler from opts.
func New(opts Options) *Handler {
	max := opts.RewriteMaxBytes
	if max <= 0 {
		max = defaultRewriteMaxBytes
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		client:     opts.Client,
		logger:     logger,
		col:        opts.Collectors,
		rewriteMax: max,
		injectHTML: opts.InjectHTML,
		enabled:    opts.Enabled,
		forceIPv4:  opts.ForceIPv4,
	}
}

// ServeHTTP decodes the target from the request path and forwards the request,
// dispatching WebSocket upgrades to the tunnel path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.enabled != nil && !h.enabled() {
		http.NotFound(w, r)
		return
	}
	target, err := link.Decode(r.URL.EscapedPath())
	if err != nil {
		h.count("bad_request")
		http.Error(w, "invalid proxy target", http.StatusBadRequest)
		return
	}
	// Refuse to proxy our own origin. A decoded target pointing back at this
	// host is a nested "/p/<enc(/p/...)>" self-reference (e.g. a client that
	// double-wrapped an already-proxied link); fetching it would recurse into
	// the proxy and spin until the context is canceled.
	if sameHost(target.Hostname(), r.Host) {
		h.count("bad_request")
		http.Error(w, "refusing to proxy this proxy", http.StatusBadRequest)
		return
	}
	if isWebSocketUpgrade(r) {
		h.serveWebSocket(w, r, target)
		return
	}
	h.serveHTTP(w, r, target)
}

// sameHost reports whether targetHost is the same host the request arrived on
// (ignoring any port on the request's Host header), case-insensitively.
func sameHost(targetHost, reqHost string) bool {
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
	}
	return targetHost != "" && strings.EqualFold(targetHost, reqHost)
}

func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request, target *url.URL) {
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		h.count("bad_request")
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	copyRequestHeaders(outReq.Header, r.Header)
	outReq.Host = target.Host

	start := time.Now()
	resp, err := h.client.Do(outReq)
	h.col.Upstream.Observe(time.Since(start).Seconds())
	if err != nil {
		if errors.Is(err, ssrfguard.ErrBlockedAddress) {
			h.count("blocked")
			http.Error(w, "destination not permitted", http.StatusForbidden)
			return
		}
		h.count("upstream_error")
		h.logger.WarnContext(r.Context(), "upstream fetch failed", "host", target.Host, "error", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	html, css := rewrite.Classify(resp.Header.Get("Content-Type"))
	copyResponseHeaders(w.Header(), resp.Header, target)

	if html || css {
		h.serveRewritten(w, resp, target, html)
		return
	}

	// Stream everything else through unchanged. Content-Length is intentionally
	// dropped in copyResponseHeaders, so the server frames the response itself.
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	h.count("ok")
}

func (h *Handler) serveRewritten(w http.ResponseWriter, resp *http.Response, target *url.URL, html bool) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, h.rewriteMax))
	if err != nil {
		h.count("upstream_error")
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	if html {
		body = rewrite.HTML(target, body)
		if h.injectHTML != nil {
			body = h.injectHTML(body)
		}
	} else {
		body = rewrite.CSS(target, body)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
	h.count("ok")
}

func (h *Handler) count(outcome string) {
	if h.col != nil {
		h.col.Requests.Inc(outcome)
	}
}

// hopByHop are per-connection headers that must not be forwarded in either
// direction (RFC 7230 §6.1).
var hopByHop = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
}

func copyRequestHeaders(dst, src http.Header) {
	for k, vv := range src {
		ck := http.CanonicalHeaderKey(k)
		if hopByHop[ck] || deniedRequestHeader[ck] {
			continue
		}
		dst[ck] = append([]string(nil), vv...)
	}
}

// deniedRequestHeader lists client headers we refuse to forward upstream:
// Accept-Encoding (so Go manages compression and we can rewrite text bodies),
// Referer/Origin (avoid leaking the proxy URL / origin mismatch in v1), and any
// forwarding headers that would disclose the client's IP.
var deniedRequestHeader = map[string]bool{
	"Accept-Encoding":   true,
	"Referer":           true,
	"Origin":            true,
	"X-Forwarded-For":   true,
	"X-Forwarded-Host":  true,
	"X-Forwarded-Proto": true,
	"X-Real-Ip":         true,
	"Forwarded":         true,
}

func copyResponseHeaders(dst, src http.Header, base *url.URL) {
	for k, vv := range src {
		ck := http.CanonicalHeaderKey(k)
		switch ck {
		case "Content-Length", "Content-Encoding":
			// Body may be transformed or was transparently decompressed.
			continue
		case "Location", "Content-Location":
			dst.Set(ck, link.Resolve(base, src.Get(ck)))
			continue
		case "Set-Cookie":
			for _, c := range vv {
				dst.Add("Set-Cookie", rewriteSetCookie(c))
			}
			continue
		case "Content-Security-Policy", "Content-Security-Policy-Report-Only":
			// Would block proxied subresources; dropped in v1.
			continue
		case "Strict-Transport-Security":
			// HSTS is applied by our own edge, not the target's.
			continue
		}
		if hopByHop[ck] {
			continue
		}
		dst[ck] = append([]string(nil), vv...)
	}
}

// rewriteSetCookie scopes an upstream Set-Cookie to the proxy origin by removing
// the Domain attribute and forcing Path=/.
//
// Limitation (v1): cookies from different targets share the proxy origin. Robust
// per-target isolation via a server-side, session-keyed cookie jar is a later
// milestone.
func rewriteSetCookie(c string) string {
	var out []string
	hasPath := false
	for _, part := range strings.Split(c, ";") {
		t := strings.TrimSpace(part)
		low := strings.ToLower(t)
		switch {
		case strings.HasPrefix(low, "domain="):
			continue
		case strings.HasPrefix(low, "path="):
			out = append(out, "Path=/")
			hasPath = true
		default:
			out = append(out, t)
		}
	}
	if !hasPath {
		out = append(out, "Path=/")
	}
	return strings.Join(out, "; ")
}
