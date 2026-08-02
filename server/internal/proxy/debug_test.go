package proxy

import "testing"

func TestDescribeCookiesFlagsDuplicates(t *testing.T) {
	// The case this exists for: one logical cookie stored under two scopes is
	// unpacked twice onto the same request. Sites that sign a cookie read the
	// conflict as tampering, and the only visible symptom is in this header.
	got := describeCookies("NID=aaa; SOCS=bb; NID=cccc")
	want := "NID(3) <<DUPLICATE, SOCS(2), NID(4) <<DUPLICATE"
	if got != want {
		t.Errorf("describeCookies() = %q, want %q", got, want)
	}
}

func TestDescribeCookiesNeverLogsValues(t *testing.T) {
	got := describeCookies("session=super-secret-token-value")
	if got != "session(24)" {
		t.Errorf("describeCookies() = %q, want %q", got, "session(24)")
	}
}

func TestDescribeCookiesEdgeCases(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "(none)"},
		{"   ", "(none)"},
		{"lone", "lone(0)"},
		{"a=1;;b=2", "a(1), b(1)"},
		{"a=", "a(0)"},
	} {
		if got := describeCookies(tc.in); got != tc.want {
			t.Errorf("describeCookies(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
