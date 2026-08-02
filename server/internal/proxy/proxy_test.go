package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bumshi/bumshi/server/internal/metrics"
	"github.com/bumshi/bumshi/server/internal/proxy/link"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestHandler(client *http.Client) *Handler {
	return New(Options{
		Client:     client,
		Logger:     discard(),
		Collectors: NewCollectors(metrics.NewRegistry()),
	})
}

func TestForwardsAndRewritesHTML(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The proxy must not leak the client's IP or forwarding headers.
		if r.Header.Get("X-Forwarded-For") != "" {
			t.Error("X-Forwarded-For was forwarded upstream")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Set-Cookie", "sid=1; Domain=tracker.example; Path=/deep")
		io.WriteString(w, `<a href="/next">next</a>`)
	}))
	defer upstream.Close()

	h := newTestHandler(upstream.Client())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/page"), nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if want := link.EncodeString(upstream.URL + "/next"); !strings.Contains(body, want) {
		t.Errorf("link not rewritten to %q:\n%s", want, body)
	}
	cookie := strings.ToLower(rec.Header().Get("Set-Cookie"))
	if strings.Contains(cookie, "domain=") {
		t.Errorf("Set-Cookie Domain not stripped: %q", cookie)
	}
	if !strings.Contains(cookie, "path=/") {
		t.Errorf("Set-Cookie Path not normalized: %q", cookie)
	}
}

func TestStreamsNonHTMLUnchanged(t *testing.T) {
	payload := "\x00\x01binary-body\xff"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, payload)
	}))
	defer upstream.Close()

	h := newTestHandler(upstream.Client())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/file.bin"), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != payload {
		t.Errorf("body altered: got %q want %q", rec.Body.String(), payload)
	}
}

func TestRewritesRedirectLocation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/somewhere-else", http.StatusFound)
	}))
	defer upstream.Close()

	// Mirror the production client, which does not follow redirects so the
	// proxy can rewrite the Location header itself.
	client := upstream.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	h := newTestHandler(client)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/start"), nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if want := link.EncodeString(upstream.URL + "/somewhere-else"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

func TestBadTargetReturns400(t *testing.T) {
	h := newTestHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/p/!!!not-base64!!!", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestForwardsRequestQueryString(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
	}))
	defer upstream.Close()

	// A GET form whose action is a proxy link submits to "/p/<token>?q=...".
	// Dropping that query made every search box on every proxied site return
	// the unfiltered page.
	h := newTestHandler(upstream.Client())
	req := httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/search")+"?q=hello&page=2", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if want := "q=hello&page=2"; got != want {
		t.Errorf("upstream query = %q, want %q", got, want)
	}
}

func TestKeepsEncodedQueryWhenRequestHasNone(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
	}))
	defer upstream.Close()

	h := newTestHandler(upstream.Client())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/x?a=1"), nil))

	if want := "a=1"; got != want {
		t.Errorf("upstream query = %q, want %q", got, want)
	}
}

func TestControlPlaneSecurityHeadersNotImposedOnProxiedContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<p>hi</p>")
	}))
	defer upstream.Close()

	h := newTestHandler(upstream.Client())
	rec := httptest.NewRecorder()
	// Pre-set what httpx.SecurityHeaders puts on every response. Left in place,
	// X-Frame-Options: DENY blocks every legitimate iframe inside a proxied page.
	rec.Header().Set("X-Frame-Options", "DENY")
	rec.Header().Set("X-Content-Type-Options", "nosniff")
	rec.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	h.ServeHTTP(rec, httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/page"), nil))

	for _, k := range []string{"X-Frame-Options", "X-Content-Type-Options", "Cross-Origin-Opener-Policy"} {
		if v := rec.Header().Get(k); v != "" {
			t.Errorf("%s = %q, want it cleared for proxied content", k, v)
		}
	}
}

func TestUpstreamSecurityHeadersArePreserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		io.WriteString(w, "<p>hi</p>")
	}))
	defer upstream.Close()

	h := newTestHandler(upstream.Client())
	rec := httptest.NewRecorder()
	rec.Header().Set("X-Frame-Options", "DENY")
	h.ServeHTTP(rec, httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/page"), nil))

	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want the upstream's SAMEORIGIN", got)
	}
}

func TestStreamedResponsesAreFlushed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: hello\n\n")
	}))
	defer upstream.Close()

	h := newTestHandler(upstream.Client())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", link.EncodeString(upstream.URL+"/events"), nil))

	if !rec.Flushed {
		t.Error("streamed response was never flushed; SSE and long-polling would stall")
	}
	if got := rec.Body.String(); got != "data: hello\n\n" {
		t.Errorf("body = %q", got)
	}
}

func TestWebSocketToPrivateAddressBlocked(t *testing.T) {
	h := newTestHandler(http.DefaultClient)
	req := httptest.NewRequest("GET", link.EncodeString("http://127.0.0.1:9/socket"), nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (SSRF-blocked)", rec.Code)
	}
}
