package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/bumshi/bumshi/server/internal/proxy/link"
)

func TestTelemetrySinksAreRecognised(t *testing.T) {
	blocked := []string{
		"https://www.google.com/gen_204?atyp=i&ct=slh",
		"https://www.google.com/client_204?cad=x",
		"https://www.google.com/httpservice/retry/jserror",
		"https://play.google.com/log?format=json",
		"https://www.google-analytics.com/g/collect?v=2",
		"https://www.facebook.com/tr?id=1",
		"https://o123.ingest.sentry.io/api/456/envelope/",
	}
	for _, raw := range blocked {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if !isTelemetrySink(u) {
			t.Errorf("%s should be treated as a beacon", raw)
		}
	}

	// Content must never be mistaken for telemetry. The scripts especially:
	// blocking a library a page waits on is how ad blockers produce blank
	// pages, and "block sinks, not scripts" is the rule that prevents it.
	allowed := []string{
		"https://www.google.com/search?q=weather",
		"https://www.google.com/",
		"https://www.google.com/xjs/_/js/k=xjs.hm.en.js",
		"https://www.google-analytics.com/analytics.js",
		"https://www.googletagmanager.com/gtag/js?id=G-X",
		"https://www.facebook.com/tracking-cookies-explained",
		"https://notgoogle.com/gen_204",
		"https://evil-play.google.com.attacker.net/log",
	}
	for _, raw := range allowed {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if isTelemetrySink(u) {
			t.Errorf("%s is content and must be forwarded", raw)
		}
	}
}

func TestTelemetryIsAnsweredWithoutTouchingUpstream(t *testing.T) {
	var reached bool
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer upstream.Close()

	h := newTestHandler(upstream.Client())
	rec := httptest.NewRecorder()
	// A beacon shaped like Google's, pointed at a host the rules match.
	req := httptest.NewRequest("POST", link.EncodeString("https://www.google.com/gen_204"), nil)
	req.Header.Set("Origin", "https://proxy.example")
	h.ServeHTTP(rec, req)

	if reached {
		t.Error("the beacon was forwarded upstream; the whole point is that it is not")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (what the real endpoint returns on success)", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("a credentialed beacon needs the CORS headers its real endpoint would send")
	}
}

// TestTelemetryBlockingDefaultsOnAndCanBeDisabled checks the toggle itself.
//
// It deliberately does not drive a request through the handler: the only hosts
// the rules match are real ones, and a test that reaches google.com to prove a
// negative is a test that fails whenever CI has no network.
func TestTelemetryBlockingDefaultsOnAndCanBeDisabled(t *testing.T) {
	if h := New(Options{Logger: discard()}); !h.blockTelemetry {
		t.Error("must default on: the traffic it removes is traffic no user asked for")
	}
	off := false
	if h := New(Options{Logger: discard(), BlockTelemetry: &off}); h.blockTelemetry {
		t.Error("an explicit false must be honoured")
	}
	on := true
	if h := New(Options{Logger: discard(), BlockTelemetry: &on}); !h.blockTelemetry {
		t.Error("an explicit true must be honoured")
	}
}
