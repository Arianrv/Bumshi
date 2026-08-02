package httpx

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bumshi/bumshi/server/internal/metrics"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRequestIDGenerated(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}), RequestID())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if seen == "" {
		t.Error("request ID not present in context")
	}
	if got := rec.Header().Get("X-Request-ID"); got == "" || got != seen {
		t.Errorf("response header %q does not match context %q", got, seen)
	}
}

func TestRequestIDReusedWhenValid(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), RequestID())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "abc-123_XYZ")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "abc-123_XYZ" {
		t.Errorf("valid client ID not reused: got %q", got)
	}
}

func TestRequestIDRejectsInvalid(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), RequestID())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "bad id!") // contains space and '!'
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got == "bad id!" {
		t.Error("invalid client ID should have been replaced")
	}
}

func TestRecovererReturns500(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}), Recoverer(discardLogger()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), SecurityHeaders())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options: DENY")
	}
}

func TestMetricsMiddlewareRecordsStatus(t *testing.T) {
	reg := metrics.NewRegistry()
	c := metrics.NewHTTPCollectors(reg)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), Metrics(c))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	out := httptest.NewRecorder()
	reg.Handler().ServeHTTP(out, httptest.NewRequest("GET", "/metrics", nil))
	body := out.Body.String()
	if !contains(body, `bumshi_http_requests_total{method="GET",code="418"} 1`) {
		t.Errorf("request not counted with status 418:\n%s", body)
	}
}

// fullChain mirrors the middleware stack server.New composes, so these tests
// fail if any layer breaks a capability the proxy depends on.
func fullChain(h http.Handler) http.Handler {
	return Chain(h,
		RequestID(),
		Metrics(metrics.NewHTTPCollectors(metrics.NewRegistry())),
		Recoverer(discardLogger()),
		SecurityHeaders(),
		AccessLog(discardLogger(), func() bool { return true }),
	)
}

func TestChainPreservesHijack(t *testing.T) {
	// The proxy's WebSocket tunnel hijacks the connection. Middleware wraps the
	// ResponseWriter in a type that embeds the http.ResponseWriter *interface*,
	// so only Header/Write/WriteHeader are promoted and a `w.(http.Hijacker)`
	// assertion can never succeed — every upgrade returned 500. Hijacking must
	// go through ResponseController, which walks the Unwrap chain.
	var hijackErr error
	done := make(chan struct{})
	srv := httptest.NewServer(fullChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		c, _, err := http.NewResponseController(w).Hijack()
		hijackErr = err
		if err == nil {
			_ = c.Close()
		}
	})))
	defer srv.Close()

	// The handler closes the hijacked connection, so the client sees an error;
	// that is expected and not what this test asserts.
	resp, err := http.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	<-done

	if hijackErr != nil {
		t.Fatalf("Hijack through the middleware chain: %v", hijackErr)
	}
}

func TestChainPreservesFlush(t *testing.T) {
	// Streaming responses (video, downloads, server-sent events) must be
	// flushable through the same chain, or they sit in the output buffer.
	var flushErr error
	done := make(chan struct{})
	srv := httptest.NewServer(fullChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		_, _ = w.Write([]byte("chunk"))
		flushErr = http.NewResponseController(w).Flush()
	})))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	<-done

	if flushErr != nil {
		t.Fatalf("Flush through the middleware chain: %v", flushErr)
	}
}

func TestAccessLogRedactsTheTokenBearingPath(t *testing.T) {
	// The auth endpoint carries an access token in its query string. Even with
	// access logging deliberately switched on, it must not reach disk.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		AccessLog(logger, func() bool { return true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/__bumshi__/auth?t=super-secret", nil))

	out := buf.String()
	if strings.Contains(out, "super-secret") {
		t.Errorf("access token was logged: %s", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("path was not redacted: %s", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
