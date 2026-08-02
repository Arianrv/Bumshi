package proxy

import (
	"strings"
	"testing"
)

func TestCSPHostSourcesBecomeSelf(t *testing.T) {
	// Every resource now arrives from the proxy, so the site's own hostnames no
	// longer describe anything the browser will see.
	got := rewriteCSP("default-src 'self' https://cdn.example.net *.example.com", "")
	if want := "default-src 'self'"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSPPreservesNonOriginDecisions(t *testing.T) {
	// These are the parts of a policy that still mean what the site intended,
	// and they are most of what actually stops XSS.
	got := rewriteCSP("script-src 'unsafe-eval' 'strict-dynamic' 'sha256-abc123'; sandbox allow-forms; upgrade-insecure-requests", "")
	for _, want := range []string{"'unsafe-eval'", "'strict-dynamic'", "'sha256-abc123'", "sandbox allow-forms", "upgrade-insecure-requests"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestCSPAdmitsTheRuntimeViaNonce(t *testing.T) {
	got := rewriteCSP("script-src 'self'", "NONCE123")
	if !strings.Contains(got, "'nonce-NONCE123'") {
		t.Errorf("runtime would be blocked: %q", got)
	}
}

func TestCSPNoneIsReplacedNotJoined(t *testing.T) {
	// "'none' plus anything else" is an invalid source list, so the nonce has to
	// displace it rather than sit alongside it.
	got := rewriteCSP("script-src 'none'", "NONCE123")
	if strings.Contains(got, "'none'") {
		t.Errorf("'none' must not survive next to a nonce: %q", got)
	}
	if !strings.Contains(got, "'nonce-NONCE123'") {
		t.Errorf("nonce missing: %q", got)
	}
}

func TestCSPDropsReportingEndpoints(t *testing.T) {
	got := rewriteCSP("default-src 'self'; report-uri https://example.com/csp; report-to group", "")
	if strings.Contains(got, "report-") {
		t.Errorf("reporting endpoints should be dropped: %q", got)
	}
}

func TestCSPKeepsNonProxiedSchemes(t *testing.T) {
	// data: and blob: never travel through the proxy, so they still mean exactly
	// what they said.
	got := rewriteCSP("img-src data: blob: https://cdn.example.net", "")
	for _, want := range []string{"data:", "blob:", "'self'"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestCSPEmptyPolicyYieldsNoHeader(t *testing.T) {
	for _, in := range []string{"", "   ", ";;"} {
		if got := rewriteCSP(in, "n"); got != "" {
			t.Errorf("rewriteCSP(%q) = %q, want empty", in, got)
		}
	}
}

func TestCSPFrameAncestorsSurvivesAsSelf(t *testing.T) {
	// Clickjacking protection is worth keeping, scoped to the proxy origin.
	got := rewriteCSP("frame-ancestors https://example.com", "")
	if want := "frame-ancestors 'self'"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSPNonceIsUniquePerResponse(t *testing.T) {
	a, b := newCSPNonce(), newCSPNonce()
	if a == "" || a == b {
		t.Errorf("nonces must be random and distinct: %q %q", a, b)
	}
}
