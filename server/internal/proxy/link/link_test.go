package link

import (
	"net/url"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	raw := "https://example.com/watch?v=abc&x=1#frag"
	path := EncodeString(raw)
	u, err := Decode(path)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if u.String() != raw {
		t.Errorf("round trip = %q, want %q", u.String(), raw)
	}
}

func TestDecodeErrors(t *testing.T) {
	if _, err := Decode(Prefix); err == nil {
		t.Error("empty token should error")
	}
	if _, err := Decode(Prefix + "!!!not-base64!!!"); err == nil {
		t.Error("bad base64 should error")
	}
	// ftp scheme is not allowed.
	if _, err := Decode(EncodeString("ftp://example.com/x")); err == nil {
		t.Error("non-http(s) scheme should error")
	}
}

func TestDecodeIgnoresTrailingSegments(t *testing.T) {
	path := EncodeString("https://example.com/") + "/style.css"
	if _, err := Decode(path); err != nil {
		t.Errorf("trailing segment should be ignored: %v", err)
	}
}

func TestDecodeRequestAppliesFormQuery(t *testing.T) {
	// A GET form whose action is a proxy link submits to "/p/<token>?q=...".
	// HTML form submission replaces the action URL's own query, so must we.
	u, err := DecodeRequest(EncodeString("https://example.com/search?old=1"), "q=new&page=2")
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if got, want := u.String(), "https://example.com/search?q=new&page=2"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}

func TestDecodeRequestKeepsEncodedQuery(t *testing.T) {
	u, err := DecodeRequest(EncodeString("https://example.com/search?old=1"), "")
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if got, want := u.String(), "https://example.com/search?old=1"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}

func TestDecodeRequestPropagatesDecodeErrors(t *testing.T) {
	if _, err := DecodeRequest(Prefix+"!!!", "q=1"); err == nil {
		t.Error("bad token should error even with a query present")
	}
}

func TestResolveRelative(t *testing.T) {
	base, _ := url.Parse("https://example.com/dir/page.html")

	got := Resolve(base, "img.png")
	want := EncodeString("https://example.com/dir/img.png")
	if got != want {
		t.Errorf("relative resolve = %q, want %q", got, want)
	}

	got = Resolve(base, "/root")
	want = EncodeString("https://example.com/root")
	if got != want {
		t.Errorf("absolute-path resolve = %q, want %q", got, want)
	}
}

func TestResolveLeavesNonNavigational(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	for _, ref := range []string{"#top", "data:image/png;base64,AAAA", "javascript:void(0)", "mailto:a@b.c"} {
		if got := Resolve(base, ref); got != ref {
			t.Errorf("Resolve(%q) = %q, want unchanged", ref, got)
		}
	}
}
