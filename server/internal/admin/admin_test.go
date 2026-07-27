package admin

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bumshi/bumshi/server/internal/auth"
	"github.com/bumshi/bumshi/server/internal/settings"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testHandler(t *testing.T) *Handler {
	t.Helper()
	hash, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	access, err := NewAccessStore("")
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{
		BasePath:     "/admin/",
		Username:     "admin",
		PasswordHash: hash,
		Settings:     settings.New(false, false),
		Sessions:     auth.NewSessionStore(time.Hour),
		Logins:       auth.NewRateLimiter(20, time.Minute),
		Access:       access,
		Logger:       discard(),
		StartedAt:    time.Now(),
	})
}

func do(h *Handler, method, path, body string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if csrf != "" {
		r.Header.Set(csrfHeader, csrf)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func login(t *testing.T, h *Handler) ([]*http.Cookie, string) {
	t.Helper()
	rec := do(h, "POST", "/admin/login", `{"username":"admin","password":"pw"}`, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	var csrf string
	for _, c := range cookies {
		if c.Name == csrfCookie {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("login did not set a CSRF cookie")
	}
	return cookies, csrf
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	h := testHandler(t)
	rec := do(h, "POST", "/admin/login", `{"username":"admin","password":"nope"}`, nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestApiRequiresAuth(t *testing.T) {
	h := testHandler(t)
	rec := do(h, "GET", "/admin/api/status", "", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLoginThenAccessStatus(t *testing.T) {
	h := testHandler(t)
	cookies, _ := login(t, h)
	rec := do(h, "GET", "/admin/api/status", "", cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestCSRFEnforcedOnMutations(t *testing.T) {
	h := testHandler(t)
	cookies, csrf := login(t, h)

	rec := do(h, "POST", "/admin/api/settings", `{"proxyEnabled":true}`, cookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no-CSRF status = %d, want 403", rec.Code)
	}

	rec = do(h, "POST", "/admin/api/settings", `{"proxyEnabled":true}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("with-CSRF status = %d, want 200", rec.Code)
	}
	if !h.opts.Settings.ProxyEnabled() {
		t.Error("settings change was not applied")
	}
}

func TestAccessUserCreate(t *testing.T) {
	h := testHandler(t)
	cookies, csrf := login(t, h)
	rec := do(h, "POST", "/admin/api/access-users", `{"label":"phone"}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d", rec.Code)
	}
	if got := len(h.opts.Access.List()); got != 1 {
		t.Fatalf("access users = %d, want 1", got)
	}
	if !strings.Contains(rec.Body.String(), "bumshi://connect#") {
		t.Errorf("response missing connection link: %s", rec.Body.String())
	}
}

func TestUnauthenticatedAppRedirects(t *testing.T) {
	h := testHandler(t)
	rec := do(h, "GET", "/admin/", "", nil, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}
