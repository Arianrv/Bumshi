package webengine

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServesAssetsAsJavaScript(t *testing.T) {
	h := Handler()
	for _, name := range []string{"codec.js", "rewriter.js", "client.js", "sw.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", Prefix+name, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("%s: content-type %q", name, ct)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", name)
		}
	}
}

func TestServiceWorkerAllowedHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", Prefix+"sw.js", nil))
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed = %q, want /", got)
	}
}

func TestUnknownAssetIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", Prefix+"nope.js", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", Prefix+"..%2fwebengine.go", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestInjectAfterHead(t *testing.T) {
	in := []byte(`<!doctype html><html><head><title>x</title></head><body>hi</body></html>`)
	out := Inject(in, "")
	if !bytes.Contains(out, bootstrap) {
		t.Fatal("bootstrap not injected")
	}
	headIdx := bytes.Index(out, []byte("<head>"))
	scriptIdx := bytes.Index(out, bootstrap)
	titleIdx := bytes.Index(out, []byte("<title>"))
	if headIdx >= scriptIdx || scriptIdx >= titleIdx {
		t.Errorf("bootstrap not placed right after <head> (head=%d script=%d title=%d)", headIdx, scriptIdx, titleIdx)
	}
}

func TestInjectIsNotFooledByHeaderElement(t *testing.T) {
	// A bare "<head" prefix search also matches <header>. In a fragment with no
	// real <head> that placed the bootstrap in the middle of the body, after the
	// page's own scripts had already run.
	in := []byte(`<div><header>top</header><p>body</p></div>`)
	out := Inject(in, "")
	if !bytes.HasPrefix(out, bootstrap) {
		t.Errorf("bootstrap should be prepended when there is no <head>:\n%s", out)
	}
	if !bytes.Contains(out, []byte("<header>top</header>")) {
		t.Errorf("<header> was damaged:\n%s", out)
	}
}

func TestInjectHandlesHeadWithAttributes(t *testing.T) {
	in := []byte(`<html><head lang="en"><title>x</title></head><body>hi</body></html>`)
	out := Inject(in, "")
	headIdx := bytes.Index(out, []byte(`<head lang="en">`))
	scriptIdx := bytes.Index(out, bootstrap)
	titleIdx := bytes.Index(out, []byte("<title>"))
	if headIdx < 0 || headIdx >= scriptIdx || scriptIdx >= titleIdx {
		t.Errorf("bootstrap misplaced (head=%d script=%d title=%d):\n%s", headIdx, scriptIdx, titleIdx, out)
	}
}

func TestInjectAppliesCSPNonce(t *testing.T) {
	out := Inject([]byte(`<html><head></head><body></body></html>`), "NONCE123")
	if !bytes.Contains(out, []byte(`nonce="NONCE123"`)) {
		t.Errorf("nonce not applied to the injected scripts:\n%s", out)
	}
	if n := bytes.Count(out, []byte(`nonce="NONCE123"`)); n != len(bootstrapAssets) {
		t.Errorf("nonce on %d scripts, want %d:\n%s", n, len(bootstrapAssets), out)
	}
}

func TestInjectWithoutNonceEmitsPlainScripts(t *testing.T) {
	out := Inject([]byte(`<html><head></head></html>`), "")
	if bytes.Contains(out, []byte("nonce=")) {
		t.Errorf("no nonce should be emitted when the response has no CSP:\n%s", out)
	}
}

func TestAuthHandlerSetsCookieAndRedirects(t *testing.T) {
	rec := httptest.NewRecorder()
	AuthHandler(true).ServeHTTP(rec, httptest.NewRequest("GET", AuthPath+"?t=the-token", nil))

	if rec.Code != 303 {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	sc := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"bumshi_access=the-token", "HttpOnly", "Secure", "Path=/"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie missing %q: %s", want, sc)
		}
	}
}

func TestAuthHandlerRejectsMissingToken(t *testing.T) {
	rec := httptest.NewRecorder()
	AuthHandler(true).ServeHTTP(rec, httptest.NewRequest("GET", AuthPath, nil))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("no cookie should be set without a token")
	}
}

func TestInjectWithoutHeadPrepends(t *testing.T) {
	in := []byte(`<div>no head here</div>`)
	out := Inject(in, "")
	if !bytes.HasPrefix(out, bootstrap) {
		t.Error("bootstrap should be prepended when there is no <head>")
	}
}
