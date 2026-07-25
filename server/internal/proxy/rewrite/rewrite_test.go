package rewrite

import (
	"net/url"
	"strings"
	"testing"

	"github.com/bumshi/bumshi/server/internal/proxy/link"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u
}

func TestClassify(t *testing.T) {
	if h, _ := Classify("text/html; charset=utf-8"); !h {
		t.Error("text/html should classify as HTML")
	}
	if _, c := Classify("text/css"); !c {
		t.Error("text/css should classify as CSS")
	}
	if h, c := Classify("application/json"); h || c {
		t.Error("json should not be rewritten")
	}
}

func TestHTMLRewritesURLs(t *testing.T) {
	base := mustURL(t, "https://example.com/dir/page.html")
	in := []byte(`<a href="/about">a</a><img src="img.png"><script src="https://cdn.example.net/a.js"></script>`)
	out := string(HTML(base, in))

	wants := []string{
		link.EncodeString("https://example.com/about"),
		link.EncodeString("https://example.com/dir/img.png"),
		link.EncodeString("https://cdn.example.net/a.js"),
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestHTMLLeavesNonNavigational(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	in := []byte(`<a href="javascript:void(0)">x</a><img src="data:image/png;base64,AAAA">`)
	out := string(HTML(base, in))
	if !strings.Contains(out, "javascript:void(0)") || !strings.Contains(out, "data:image/png;base64,AAAA") {
		t.Errorf("non-navigational refs were altered:\n%s", out)
	}
}

func TestSrcsetRewrite(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	in := []byte(`<img srcset="a.png 1x, b.png 2x">`)
	out := string(HTML(base, in))
	if !strings.Contains(out, link.EncodeString("https://example.com/a.png")) ||
		!strings.Contains(out, link.EncodeString("https://example.com/b.png")) {
		t.Errorf("srcset not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "1x") || !strings.Contains(out, "2x") {
		t.Errorf("srcset descriptors lost:\n%s", out)
	}
}

func TestCSSRewrite(t *testing.T) {
	base := mustURL(t, "https://example.com/css/main.css")
	in := []byte(`body{background:url("../img/bg.png")} @font-face{src:url(font.woff2)}`)
	out := string(CSS(base, in))
	if !strings.Contains(out, link.EncodeString("https://example.com/img/bg.png")) {
		t.Errorf("css url not rewritten:\n%s", out)
	}
	if !strings.Contains(out, link.EncodeString("https://example.com/css/font.woff2")) {
		t.Errorf("unquoted css url not rewritten:\n%s", out)
	}
}
