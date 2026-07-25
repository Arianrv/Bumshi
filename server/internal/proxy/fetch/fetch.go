// Package fetch builds HTTP clients and dialers hardened for server-side
// retrieval of untrusted, user-supplied URLs.
package fetch

import (
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
func NewClient(responseHeaderTimeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext:           Dialer().DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
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
