// Package fetch builds HTTP clients and dialers hardened for server-side
// retrieval of untrusted, user-supplied URLs.
package fetch

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/bumshi/bumshi/server/internal/proxy/ssrfguard"
)

// NewClient returns an http.Client suitable for fetching arbitrary user-supplied
// URLs safely:
//
//   - the dialer refuses non-public IPs (SSRF protection, incl. DNS-rebinding);
//   - dial, TLS-handshake and response-header times are bounded;
//   - redirects are NOT followed automatically — 3xx responses are returned so
//     the proxy can rewrite the Location header;
//   - idle connections are pooled and capped.
//
// responseHeaderTimeout bounds the wait for the upstream's response headers; the
// body itself may then stream for as long as needed (so large downloads and
// video are not cut off).
//
// When forceIPv4 is set, all upstream connections are dialed over IPv4 only;
// IPv6 egress is unreliable on some networks (notably from Iran).
func NewClient(responseHeaderTimeout time.Duration, forceIPv4 bool) *http.Client {
	base := Dialer()
	dialContext := base.DialContext
	if forceIPv4 {
		dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return base.DialContext(ctx, forceIPv4Network(network), addr)
		}
	}
	transport := &http.Transport{
		DialContext:       dialContext,
		ForceAttemptHTTP2: true,
		// Sized for streaming media, which is what actually stresses this pool.
		//
		// A DASH or HLS player does not make one request per video; it makes a
		// steady stream of small ranged requests, several in flight at once, all
		// to the same CDN host — and every user watching does the same. The
		// previous per-host idle cap of 4 meant that beyond the fourth
		// connection each finished request was closed rather than returned to
		// the pool, so the next segment paid a fresh TCP and TLS handshake. On a
		// link to Iran that is a round trip measured in hundreds of
		// milliseconds, repeated every few seconds of playback: it shows up as
		// stalling that looks like bandwidth but is not.
		//
		// MaxConnsPerHost is deliberately left unset (unlimited). Capping it
		// would block requests rather than open connections, converting a
		// throughput problem into a stall.
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 64,
		// 4 KiB (the default) is a lot of syscalls at video bitrates.
		WriteBufferSize:       64 << 10,
		ReadBufferSize:        64 << 10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Dialer returns a net.Dialer that enforces the SSRF guard. It is used directly
// for WebSocket tunneling, where the connection is managed by hand.
func Dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   ssrfguard.Control,
	}
}

// DialNetwork returns the network to request from a net.Dialer: "tcp4" when
// forceIPv4 is set (IPv6 egress is unreliable from some networks, e.g. Iran),
// otherwise "tcp" for dual-stack. Callers that pass the network explicitly
// (such as the WebSocket tunnel) use this to honour the same setting as the
// HTTP client.
func DialNetwork(forceIPv4 bool) string {
	if forceIPv4 {
		return "tcp4"
	}
	return "tcp"
}

// forceIPv4Network rewrites a dual-stack "tcp" network to IPv4-only "tcp4",
// leaving an explicit "tcp4"/"tcp6" request unchanged.
func forceIPv4Network(network string) string {
	if network == "tcp" {
		return "tcp4"
	}
	return network
}
