package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u
}

// jar packs a Set-Cookie the way the proxy would, then hands it back as the
// browser would on a later request to host, and reports what reaches upstream.
func jar(t *testing.T, setCookies []string, from string, host string) string {
	t.Helper()
	target := mustParse(t, from)
	req := httptest.NewRequest("GET", "/p/token", nil)
	for _, sc := range setCookies {
		packed, ok := packSetCookie(sc, target, true)
		if !ok {
			t.Fatalf("packSetCookie(%q) failed", sc)
		}
		for _, c := range (&http.Response{Header: http.Header{"Set-Cookie": {packed}}}).Cookies() {
			req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
	}
	return unpackCookies(req, host)
}

func TestCookieStaysWithinItsHost(t *testing.T) {
	got := jar(t, []string{"SID=secret"}, "https://www.youtube.com/watch", "www.youtube.com")
	if got != "SID=secret" {
		t.Errorf("same host: got %q, want SID=secret", got)
	}
	// The whole point: another site must never be offered it.
	if got := jar(t, []string{"SID=secret"}, "https://www.youtube.com/watch", "evil.example"); got != "" {
		t.Errorf("cookie leaked to another site: %q", got)
	}
	// A host-only cookie does not reach subdomains either.
	if got := jar(t, []string{"SID=secret"}, "https://youtube.com/", "www.youtube.com"); got != "" {
		t.Errorf("host-only cookie leaked to a subdomain: %q", got)
	}
}

func TestDomainCookieReachesSubdomains(t *testing.T) {
	// This is what makes signing in to Google work: accounts.google.com sets a
	// Domain=.google.com cookie that www.google.com and mail.google.com must see.
	set := []string{"SID=secret; Domain=.google.com; Path=/"}
	for _, host := range []string{"google.com", "www.google.com", "mail.google.com", "a.b.google.com"} {
		if got := jar(t, set, "https://accounts.google.com/signin", host); got != "SID=secret" {
			t.Errorf("host %s: got %q, want SID=secret", host, got)
		}
	}
	for _, host := range []string{"google.com.evil.example", "notgoogle.com", "evil.example"} {
		if got := jar(t, set, "https://accounts.google.com/signin", host); got != "" {
			t.Errorf("host %s should not receive the cookie, got %q", host, got)
		}
	}
}

func TestDomainAttributeMustCoverTheRequestHost(t *testing.T) {
	// A site claiming a Domain it does not belong to falls back to host-only,
	// so it cannot have its cookie offered to somebody else's hosts.
	set := []string{"SID=x; Domain=.google.com"}
	if got := jar(t, set, "https://evil.example/", "www.google.com"); got != "" {
		t.Errorf("foreign Domain was honoured: %q", got)
	}
	if got := jar(t, set, "https://evil.example/", "evil.example"); got != "SID=x" {
		t.Errorf("should fall back to host-only: got %q", got)
	}
}

func TestPublicSuffixishDomainIsRefused(t *testing.T) {
	// Domain=com would otherwise scope a cookie to every .com host.
	set := []string{"SID=x; Domain=com"}
	if got := jar(t, set, "https://a.com/", "b.com"); got != "" {
		t.Errorf("single-label Domain was honoured: %q", got)
	}
	if got := jar(t, set, "https://a.com/", "a.com"); got != "SID=x" {
		t.Errorf("should fall back to host-only: got %q", got)
	}
}

func TestAccessTokenAndStrayCookiesNeverReachUpstream(t *testing.T) {
	req := httptest.NewRequest("GET", "/p/token", nil)
	req.AddCookie(&http.Cookie{Name: accessCookie, Value: "the-users-access-token"})
	req.AddCookie(&http.Cookie{Name: "somethingElse", Value: "v"})

	if got := unpackCookies(req, "www.youtube.com"); got != "" {
		t.Errorf("unprefixed cookies must be dropped, got %q", got)
	}
}

func TestPackedCookiePreservesAttributes(t *testing.T) {
	target := mustParse(t, "https://example.com/")
	packed, ok := packSetCookie("SID=v; HttpOnly; Max-Age=600; Domain=.example.com", target, true)
	if !ok {
		t.Fatal("packSetCookie failed")
	}
	for _, want := range []string{"HttpOnly", "Max-Age=600", "Secure", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(packed, want) {
			t.Errorf("packed cookie missing %q: %s", want, packed)
		}
	}
	if strings.Contains(packed, "Domain=") {
		t.Errorf("Domain must not survive onto the proxy origin: %s", packed)
	}
	if !strings.HasPrefix(packed, cookieNamePrefix) {
		t.Errorf("packed cookie is not namespaced: %s", packed)
	}
}

func TestOriginalCookieNameIsRestored(t *testing.T) {
	// Including names the browser treats specially, which lose their meaning on
	// the proxy origin but must arrive intact upstream.
	for _, name := range []string{"__Host-SID", "__Secure-ID", "a.b_c-d"} {
		got := jar(t, []string{name + "=v"}, "https://example.com/", "example.com")
		if want := name + "=v"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestScopePrefixesStopAtTwoLabels(t *testing.T) {
	got := scopePrefixes("a.b.example.com")
	// host-only + host domain + b.example.com + example.com, and never "com".
	if len(got) != 4 {
		t.Fatalf("scopePrefixes = %d entries, want 4: %v", len(got), got)
	}
	if strings.Contains(strings.Join(got, ","), cookiePrefix("com", true)) {
		t.Error("a single-label suffix must never be a scope")
	}
}
