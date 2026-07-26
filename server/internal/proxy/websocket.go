package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bumshi/bumshi/server/internal/proxy/fetch"
	"github.com/bumshi/bumshi/server/internal/proxy/ssrfguard"
)

// wsForwardHeaders are the request headers copied verbatim into the upstream
// WebSocket handshake.
var wsForwardHeaders = []string{
	"Upgrade", "Connection",
	"Sec-Websocket-Key", "Sec-Websocket-Version",
	"Sec-Websocket-Protocol", "Sec-Websocket-Extensions",
	"User-Agent", "Cookie", "Accept-Language",
}

// wsUpgradeResponseHeaders are the upstream 101 headers echoed back to the
// client to complete the handshake.
var wsUpgradeResponseHeaders = []string{
	"Upgrade", "Connection",
	"Sec-Websocket-Accept", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions",
}

// isWebSocketUpgrade reports whether r is a WebSocket upgrade request.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		headerContainsToken(r.Header, "Connection", "upgrade")
}

func headerContainsToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// serveWebSocket transparently tunnels a WebSocket connection to target. After
// relaying the handshake it copies bytes in both directions without parsing
// frames, so any WebSocket subprotocol works.
func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request, target *url.URL) {
	dialCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	upstream, err := h.dialUpstream(dialCtx, target)
	if err != nil {
		if errors.Is(err, ssrfguard.ErrBlockedAddress) {
			h.count("blocked")
			http.Error(w, "destination not permitted", http.StatusForbidden)
			return
		}
		h.count("upstream_error")
		h.logger.WarnContext(r.Context(), "websocket dial failed", "host", target.Host, "error", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	upResp, upstreamBR, err := h.upstreamHandshake(upstream, target, r)
	if err != nil {
		h.count("upstream_error")
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	if upResp.StatusCode != http.StatusSwitchingProtocols {
		h.count("upstream_error")
		http.Error(w, "upstream did not upgrade", http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		h.count("upstream_error")
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	if err := writeClientHandshake(clientRW, upResp.Header); err != nil {
		return
	}

	h.count("ok")
	tunnel(clientConn, clientRW, upstream, upstreamBR)
}

func (h *Handler) dialUpstream(ctx context.Context, target *url.URL) (net.Conn, error) {
	dialer := fetch.Dialer()
	network := fetch.DialNetwork(h.forceIPv4)
	hostport := canonicalHostPort(target)
	if target.Scheme == "https" {
		td := &tls.Dialer{
			NetDialer: dialer,
			Config:    &tls.Config{ServerName: target.Hostname(), MinVersion: tls.VersionTLS12},
		}
		return td.DialContext(ctx, network, hostport)
	}
	return dialer.DialContext(ctx, network, hostport)
}

func (h *Handler) upstreamHandshake(upstream net.Conn, target *url.URL, r *http.Request) (*http.Response, *bufio.Reader, error) {
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Host = target.Host
	for _, k := range wsForwardHeaders {
		if v := r.Header.Values(k); len(v) > 0 {
			req.Header[http.CanonicalHeaderKey(k)] = v
		}
	}
	// Present the target's own origin upstream rather than the proxy's.
	req.Header.Set("Origin", target.Scheme+"://"+target.Host)

	_ = upstream.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err := req.Write(upstream); err != nil {
		return nil, nil, err
	}
	_ = upstream.SetWriteDeadline(time.Time{})

	br := bufio.NewReader(upstream)
	_ = upstream.SetReadDeadline(time.Now().Add(15 * time.Second))
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, nil, err
	}
	_ = upstream.SetReadDeadline(time.Time{})
	return resp, br, nil
}

func writeClientHandshake(rw *bufio.ReadWriter, upstreamHeader http.Header) error {
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	for _, k := range wsUpgradeResponseHeaders {
		for _, v := range upstreamHeader.Values(k) {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\r\n")
	if _, err := rw.WriteString(b.String()); err != nil {
		return err
	}
	return rw.Flush()
}

// tunnel copies bytes in both directions until either side closes.
func tunnel(clientConn net.Conn, clientRW *bufio.ReadWriter, upstream net.Conn, upstreamBR *bufio.Reader) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, clientRW) // client -> upstream (includes buffered bytes)
		_ = upstream.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, upstreamBR) // upstream -> client (includes buffered bytes)
		_ = clientConn.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

func canonicalHostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return net.JoinHostPort(u.Hostname(), "443")
	}
	return net.JoinHostPort(u.Hostname(), "80")
}
