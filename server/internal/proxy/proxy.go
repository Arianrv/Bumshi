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
	InjectHTML func(body []byte, nonce string) []byte
	// Enabled, if set, gates the proxy at request time so the admin panel can
	// toggle it live. When it returns false, requests get 404. Nil means always
	// enabled.
	Enabled func() bool
	// ForceIPv4 makes the WebSocket tunnel dial upstream over IPv4 only. It
	// mirrors the fetch client's setting (see fetch.NewClient) so both transports
	// behave consistently.
	ForceIPv4 bool
	// RequireToken gates every proxied request behind a valid, unexpired access
	// token supplied as the bumshi_access cookie. Off unless explicitly enabled.
	RequireToken bool
	// Authorized validates an access token (see admin.AccessStore.Authorized).
	// Only consulted when RequireToken is set.
	Authorized func(token string) bool
	// SecureCookies marks packed cookies Secure. True whenever users reach the
	// proxy over HTTPS, which is every real deployment.
	SecureCookies bool
	// SelfHosts are additional hostnames that belong to this deployment (the
	// public URL, typically). A target pointing at one of them is a
	// double-wrapped proxy link and is refused. The request's own Host header is
	// always checked; this covers deployments where an intermediary rewrites it.
	SelfHosts []string
	// LogUpstreamHost, when non-empty, logs the full header set of every
	// upstream request whose host contains it. Diagnostic only; see debug.go.
	LogUpstreamHost string
}

// Handler is the proxy HTTP handler. Mount it under link.Prefix ("/p/").
type Handler struct {
	client       *http.Client
	logger       *slog.Logger
	col          *Collectors
	rewriteMax   int64
	injectHTML   func(body []byte, nonce string) []byte
	enabled      func() bool
	forceIPv4    bool
	requireToken bool
	authorized   func(string) bool
	secure       bool
	selfHosts    []string
	logUpstream  string
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
		client:       opts.Client,
		logger:       logger,
		col:          opts.Collectors,
		rewriteMax:   max,
		injectHTML:   opts.InjectHTML,
		enabled:      opts.Enabled,
		forceIPv4:    opts.ForceIPv4,
		requireToken: opts.RequireToken,
		authorized:   opts.Authorized,
		secure:       opts.SecureCookies,
		selfHosts:    normalizeHosts(opts.SelfHosts),
		logUpstream:  strings.ToLower(opts.LogUpstreamHost),
	}
}

// normalizeHosts lower-cases and de-ports a list of hostnames, dropping empties.
func normalizeHosts(in []string) []string {
	out := make([]string, 0, len(in))
	for _, h := range in {
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		}
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// ServeHTTP decodes the target from the request path and forwards the request,
// dispatching WebSocket upgrades to the tunnel path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.enabled != nil && !h.enabled() {
		http.NotFound(w, r)
		return
	}
	// Access gating: when enabled, every proxied request must carry a valid,
	// unexpired access token. The token never reaches the target site: the
	// upstream Cookie header is rebuilt from scratch out of the namespaced jar
	// (see setRequestIdentity), and anything without a recognised prefix — the
	// access token included — is left behind.
	if h.requireToken {
		if h.authorized == nil || !h.authorized(accessToken(r)) {
			h.count("unauthorized")
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
	}
	target, err := link.DecodeRequest(r.URL.EscapedPath(), r.URL.RawQuery)
	if err != nil {
		h.count("bad_request")
		http.Error(w, "invalid proxy target", http.StatusBadRequest)
		return
	}
	// Refuse to proxy our own origin. A decoded target pointing back at this
	// host is a nested "/p/<enc(/p/...)>" self-reference (e.g. a client that
	// double-wrapped an already-proxied link); fetching it would recurse into
	// the proxy and spin until the context is canceled.
	if h.isSelf(target.Hostname(), r.Host) {
		h.count("bad_request")
		http.Error(w, "refusing to proxy this proxy", http.StatusBadRequest)
		return
	}
	// Proxied traffic is long-lived by design: video, multi-gigabyte downloads,
	// server-sent events and WebSocket tunnels all outlive the control plane's
	// Read/Write timeouts. Those timeouts exist to protect the small
	// control-plane endpoints; on this path they are cleared and cancellation
	// comes from the client's request context instead. This must happen before
	// the WebSocket branch, because deadlines armed by the HTTP server stay on
	// the connection after it is hijacked.
	//
	// Errors are ignored on purpose: a ResponseWriter that cannot carry
	// deadlines (an httptest recorder, say) simply keeps the default behaviour.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	if isWebSocketUpgrade(r) {
		h.serveWebSocket(w, r, target, rc)
		return
	}
	h.serveHTTP(w, r, target, rc)
}

// isSelf reports whether targetHost belongs to this deployment: the host the
// request arrived on, or any configured SelfHost. Such a target is a nested
// "/p/<enc(/p/...)>" self-reference — a client that double-wrapped an
// already-proxied link — and fetching it would recurse into the proxy and spin
// until the context is canceled.
func (h *Handler) isSelf(targetHost, reqHost string) bool {
	if targetHost == "" {
		return false
	}
	if host, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = host
	}
	if strings.EqualFold(targetHost, reqHost) {
		return true
	}
	for _, self := range h.selfHosts {
		if strings.EqualFold(targetHost, self) {
			return true
		}
	}
	return false
}

// accessCookie is the cookie the client app sets to carry its access token.
const accessCookie = "bumshi_access"

// accessToken returns the access token from the request's bumshi_access cookie.
func accessToken(r *http.Request) string {
	if c, err := r.Cookie(accessCookie); err == nil {
		return c.Value
	}
	return ""
}

func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request, target *url.URL, rc *http.ResponseController) {
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		h.count("bad_request")
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	copyRequestHeaders(outReq.Header, r.Header)
	setRequestIdentity(outReq, r, target)
	outReq.Host = target.Host
	// Carry the body length across explicitly. Go derives Content-Length from
	// this field and ignores any header of that name, so without it every
	// proxied POST goes out chunked — which some origins reject outright, and
	// which is itself a tell, since a browser sends Content-Length for an
	// ordinary form submission. A -1 (genuinely unknown) stays chunked.
	outReq.ContentLength = r.ContentLength
	h.logUpstreamRequest(outReq)

	start := time.Now()
	resp, err := h.client.Do(outReq)
	if h.col != nil {
		h.col.Upstream.Observe(time.Since(start).Seconds())
	}
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
	defer func() { _ = resp.Body.Close() }()

	nonce := ""
	if resp.Header.Get("Content-Security-Policy") != "" || resp.Header.Get("Content-Security-Policy-Report-Only") != "" {
		nonce = newCSPNonce()
	}

	html, css := rewrite.Classify(resp.Header.Get("Content-Type"))
	if html || css {
		// Headers are staged inside serveRewritten, only once the body has been
		// read: staging them here would leave the upstream's Set-Cookie and
		// friends attached to the 502 page if the read fails.
		h.serveRewritten(w, r, resp, target, html, nonce, rc)
		return
	}
	// This body is streamed through untouched, so the upstream Content-Length
	// still describes it exactly (Go has already removed it if it decompressed
	// on our behalf). Keeping it is what lets a <video> element seek and show a
	// duration before the file has finished arriving.
	h.copyResponseHeaders(w.Header(), resp.Header, target, nonce, !resp.Uncompressed)

	w.WriteHeader(resp.StatusCode)
	// Push the headers out ahead of the first body byte. Server-sent events and
	// long-polling deliver nothing for a while, and the client must not be kept
	// waiting for the server's output buffer to fill.
	_ = rc.Flush()
	_, _ = io.Copy(flushWriter{w: w, rc: rc}, resp.Body)
	h.count("ok")
}

// flushWriter writes through to w and flushes after every chunk, so streamed
// responses — video, large downloads, server-sent events, long-polling — reach
// the client as they arrive rather than sitting in the server's output buffer.
type flushWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if n > 0 {
		_ = fw.rc.Flush()
	}
	return n, err
}

// serveRewritten buffers a text body, rewrites its URLs, and writes it back.
//
// Reading one byte past the limit is what distinguishes "this is the whole
// document" from "this is the first N bytes of a larger one". A body that
// exceeds the limit is NOT truncated — a document cut off mid-tag renders
// broken and the failure is baffling. Instead the prefix is emitted unrewritten
// (with the runtime bootstrap still injected, since that lands in the first few
// hundred bytes) and the remainder streams through, leaving the service worker
// and in-page hooks to rewrite that page's requests at fetch time.
func (h *Handler) serveRewritten(w http.ResponseWriter, r *http.Request, resp *http.Response, target *url.URL, html bool, nonce string, rc *http.ResponseController) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, h.rewriteMax+1))
	if err != nil {
		h.count("upstream_error")
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	if int64(len(body)) > h.rewriteMax {
		h.logger.WarnContext(r.Context(), "text body exceeds the rewrite limit; serving it unrewritten",
			"host", target.Host, "limit_bytes", h.rewriteMax)
		if html && h.injectHTML != nil && shouldInject(r) {
			body = h.injectHTML(body, nonce)
		}
		// The bootstrap was injected into the buffered prefix, so the body no
		// longer matches the upstream length: false.
		h.copyResponseHeaders(w.Header(), resp.Header, target, nonce, false)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		_ = rc.Flush()
		_, _ = io.Copy(flushWriter{w: w, rc: rc}, resp.Body)
		h.count("ok_unrewritten")
		return
	}

	if html {
		body = rewrite.HTML(target, body)
		if h.injectHTML != nil && shouldInject(r) {
			body = h.injectHTML(body, nonce)
		}
	} else {
		body = rewrite.CSS(target, body)
	}
	// Rewritten body: its own length is set below, so the upstream one is wrong.
	h.copyResponseHeaders(w.Header(), resp.Header, target, nonce, false)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
	h.count("ok")
}

// shouldInject reports whether the runtime bootstrap belongs in this response.
//
// The bootstrap is for documents. Injecting it into an HTML fragment that a
// page fetches and drops into innerHTML corrupts that fragment, so honour the
// browser's own Sec-Fetch-Dest signal: inject for documents and frames, never
// for fetch/XHR. An absent header means a client that does not send it (or a
// non-browser), where assuming "document" preserves the previous behaviour.
func shouldInject(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "", "document", "iframe", "frame", "embed", "object":
		return true
	default:
		return false
	}
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

// copyRequestHeaders copies the allowed client headers upstream. Anything not
// named in forwardedRequestHeaders is dropped, including headers this code has
// never heard of.
func copyRequestHeaders(dst, src http.Header) {
	for k, vv := range src {
		ck := http.CanonicalHeaderKey(k)
		if !forwardedRequestHeaders[ck] {
			continue
		}
		dst[ck] = append([]string(nil), vv...)
	}
}

// setRequestIdentity fills in the three headers that describe who is asking:
// Cookie, Origin and Referer. All three arrive describing the PROXY, and all
// three must leave describing the TARGET.
//
// Dropping Origin and Referer outright — the previous behaviour — quietly broke
// sign-in across a large part of the web: Django rejects any POST over HTTPS
// with no Referer, and most CSRF middleware rejects a missing or foreign Origin.
// Sending the proxy's own values instead would both leak the proxy URL and fail
// the same checks. Reconstructing the target's own values is what a browser
// talking to that site directly would send, so it discloses nothing new.
func setRequestIdentity(out, in *http.Request, target *url.URL) {
	if cookies := unpackCookies(in, target.Hostname()); cookies != "" {
		out.Header.Set("Cookie", cookies)
	} else {
		out.Header.Del("Cookie")
	}

	// Origin is only sent by browsers on non-GET/HEAD and on CORS requests;
	// mirror that rather than inventing one.
	if in.Header.Get("Origin") != "" {
		out.Header.Set("Origin", target.Scheme+"://"+target.Host)
	}

	// The incoming Referer points at a proxy URL whose token decodes to the page
	// the user actually came from. Forward that; if it does not decode, send
	// nothing rather than guessing.
	referer := ""
	if ref := in.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil {
			if real, err := link.Decode(u.EscapedPath()); err == nil {
				referer = real.String()
				out.Header.Set("Referer", referer)
			}
		}
	}

	// Sec-Fetch-Site describes the same relationship the Referer does, and the
	// browser computed it against the PROXY origin — where every target is
	// "same-origin", because they all are. Forwarding it unchanged alongside a
	// real Referer emits combinations that cannot occur in a browser: a request
	// to gstatic.com, refered from google.com, asserting same-origin. That
	// contradiction is trivial for an anti-abuse system to spot, and we only
	// started emitting it once the Referer became real.
	if in.Header.Get("Sec-Fetch-Site") != "" {
		out.Header.Set("Sec-Fetch-Site", secFetchSite(referer, target))
	}
}

// secFetchSite recomputes the request's site relationship in the target's terms.
//
// Registrable-domain comparison is a last-two-labels heuristic: without a public
// suffix list "bbc.co.uk" and "news.co.uk" look related. The cost of that is a
// "same-site" where a browser would say "cross-site" between two sites under the
// same two-label suffix, which is a far smaller error than the impossible value
// it replaces.
func secFetchSite(referer string, target *url.URL) string {
	if referer == "" {
		return "none"
	}
	ref, err := url.Parse(referer)
	if err != nil || ref.Host == "" {
		return "cross-site"
	}
	if strings.EqualFold(ref.Scheme, target.Scheme) && strings.EqualFold(ref.Host, target.Host) {
		return "same-origin"
	}
	if registrableDomain(ref.Hostname()) == registrableDomain(target.Hostname()) {
		return "same-site"
	}
	return "cross-site"
}

// registrableDomain returns a host's last two labels, lower-cased.
func registrableDomain(host string) string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	parts := strings.Split(h, ".")
	if len(parts) < 2 {
		return h
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// forwardedRequestHeaders is the closed set of client headers copied verbatim
// to the target. Everything else is dropped.
//
// It is an ALLOWLIST, and the direction matters more than the contents. A
// denylist can only exclude what somebody thought to name, and this service
// sits behind whatever edge the operator puts in front of it — a CDN adds
// headers nobody in this repository chose.
//
// That is not hypothetical. With Cloudflare in front, every inbound request
// carries CF-Connecting-IP, which is Cloudflare's copy of the real visitor's
// address, next to CF-IPCountry, CF-Ray, CF-Visitor, CDN-Loop and Via. The
// denylist this replaced named X-Forwarded-For, X-Real-Ip and Forwarded but
// nothing from that family, so all of it went upstream and every site the user
// visited learned their home IP and country. For users behind national
// censorship that is the single disclosure this service exists to prevent, and
// the package doc above promised it could not happen.
//
// It is also read as an explicit relay declaration: a request arriving from a
// datacenter IP that asserts a residential client in another country is an
// immediate anti-abuse challenge. On Google it is the "unusual traffic"
// interstitial, whose reCAPTCHA cannot then be solved from a proxied origin.
//
// Four absences are deliberate:
//
//   - Cookie, Origin and Referer are rebuilt to describe the target rather than
//     the proxy (see setRequestIdentity), so the raw values must not survive.
//   - Accept-Encoding is left to Go, so compression is transparent and text
//     bodies stay rewritable.
//   - X-Requested-With is dropped because Android WebView fills it with the host
//     app's package name: forwarding it announces "com.bumshi.browser" to every
//     site, links a user's traffic together, and reads as an automated client.
//   - Content-Length is not copied because Go derives it from
//     Request.ContentLength; serveHTTP sets that field directly instead.
//
// Adding to this list is a deliberate act. Before adding a header, ask whether
// a browser talking to that site directly would send it.
var forwardedRequestHeaders = headerSet(
	// Content negotiation.
	"Accept", "Accept-Language", "Content-Type",

	// Caching and ranged requests (video seeking, resumable downloads).
	"Cache-Control", "Pragma", "Range",
	"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "If-Range",

	// The browser's identity, and the fetch metadata that has to stay
	// consistent with it. Sec-Fetch-Site is overwritten in setRequestIdentity;
	// the rest describe the request in terms proxying does not change.
	"User-Agent", "Upgrade-Insecure-Requests",
	"Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Fetch-Site", "Sec-Fetch-User",
	"Sec-CH-UA", "Sec-CH-UA-Mobile", "Sec-CH-UA-Platform", "Sec-CH-UA-Platform-Version",
	"Sec-CH-UA-Arch", "Sec-CH-UA-Bitness", "Sec-CH-UA-Model",
	"Sec-CH-UA-Full-Version", "Sec-CH-UA-Full-Version-List", "Sec-CH-UA-WoW64",

	// Preferences a browser volunteers. Dropping them is itself an anomaly:
	// a real Chrome always sends them.
	"Priority", "DNT", "Sec-GPC", "Sec-CH-Prefers-Color-Scheme",

	// HTTP authentication, for sites that use it.
	"Authorization",
)

// headerSet builds a lookup set with canonicalised keys.
//
// Canonicalising here rather than trusting the literals is what makes the list
// safe to edit: Go's canonical form is "Sec-Ch-Ua", not "Sec-CH-UA", and "Dnt",
// not "DNT". A hand-written key in the wrong case would never match, and
// because the set is an allowlist the failure is silent — the header is simply
// dropped, and the only symptom is a site behaving oddly months later.
func headerSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[http.CanonicalHeaderKey(n)] = true
	}
	return m
}

// controlPlaneHeaders are response headers the service's own middleware sets on
// every response (see internal/httpx.SecurityHeaders). They are right for our
// own endpoints and wrong for third-party content: "X-Frame-Options: DENY"
// blocks every legitimate iframe inside a proxied page — embeds, captchas,
// payment frames — and "X-Content-Type-Options: nosniff" breaks the many sites
// that serve CSS or JS with a sloppy Content-Type. They are cleared here so the
// upstream's own values, copied below, are the ones the browser sees.
var controlPlaneHeaders = []string{
	"X-Frame-Options",
	"X-Content-Type-Options",
	"Referrer-Policy",
	"Cross-Origin-Opener-Policy",
	"Cross-Origin-Embedder-Policy",
	"Cross-Origin-Resource-Policy",
}

// copyResponseHeaders stages the upstream response headers on dst.
//
// keepLength says whether the body reaches the client byte-for-byte, in which
// case the upstream Content-Length is still true and worth forwarding. It is
// false wherever the body is rewritten or only partly buffered.
func (h *Handler) copyResponseHeaders(dst, src http.Header, base *url.URL, nonce string, keepLength bool) {
	for _, k := range controlPlaneHeaders {
		dst.Del(k)
	}
	for k, vv := range src {
		ck := http.CanonicalHeaderKey(k)
		switch ck {
		case "Content-Length":
			// Forwarded only for an untouched body. Length matters most for the
			// case that needs it most: without it the response is framed as
			// chunked, and a media element cannot report a duration or seek
			// until the whole file has arrived — progressive <video> playback
			// degrades into download-then-play.
			//
			// Go deletes this header itself when it transparently decompressed
			// the body, so a value surviving here describes the bytes we are
			// about to write.
			if !keepLength {
				continue
			}
		case "Content-Encoding":
			// Go decompressed transparently, or we rewrote the body.
			continue
		case "Location", "Content-Location":
			dst.Set(ck, link.Resolve(base, src.Get(ck)))
			continue
		case "Set-Cookie":
			for _, c := range vv {
				if packed, ok := packSetCookie(c, base, h.secure); ok {
					dst.Add("Set-Cookie", packed)
				}
			}
			continue
		case "Content-Security-Policy", "Content-Security-Policy-Report-Only":
			// Translated onto the proxy origin rather than dropped: discarding it
			// would leave one XSS anywhere able to compromise every site the user
			// browses through this shared origin. See csp.go.
			if policy := rewriteCSP(src.Get(ck), nonce); policy != "" {
				dst.Set(ck, policy)
			}
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
