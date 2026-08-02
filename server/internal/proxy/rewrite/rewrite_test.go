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

func TestCSSImportString(t *testing.T) {
	base := mustURL(t, "https://example.com/css/main.css")
	out := string(CSS(base, []byte(`@import "theme.css"; @import url(other.css);`)))
	if !strings.Contains(out, link.EncodeString("https://example.com/css/theme.css")) {
		t.Errorf("@import string not rewritten:\n%s", out)
	}
	if !strings.Contains(out, link.EncodeString("https://example.com/css/other.css")) {
		t.Errorf("@import url() not rewritten:\n%s", out)
	}
}

// --- markup vs. text ---
//
// The previous regex sweep could not tell markup from text and so rewrote
// inline JavaScript, escaped JSON and the tails of unrelated attributes. Each
// document below must come back byte-for-byte identical, or with only the
// intended value changed.

func TestHTMLLeavesScriptBodiesIntact(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	in := `<script>el.href = "/login"; var d={"h":"<a href=\"/x\">"}; s.style="url(/a.png)";</script>`
	if out := string(HTML(base, []byte(in))); out != in {
		t.Errorf("script body was rewritten:\ngot  %s\nwant %s", out, in)
	}
}

func TestHTMLScriptEndTagMustBeExact(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	in := `<script>var s="</scriptish>"; el.href="/a";</script><a href="/b">x</a>`
	out := string(HTML(base, []byte(in)))
	if !strings.Contains(out, `var s="</scriptish>"; el.href="/a";`) {
		t.Errorf("script body ended early:\n%s", out)
	}
	if !strings.Contains(out, link.EncodeString("https://example.com/b")) {
		t.Errorf("markup after the script was not rewritten:\n%s", out)
	}
}

func TestHTMLLeavesUnrelatedAttributesIntact(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	in := `<div data-action="/api/save" data-track-src="abc" my-href="/z" aria-src="/q"></div>`
	if out := string(HTML(base, []byte(in))); out != in {
		t.Errorf("unrelated attributes were rewritten:\ngot  %s\nwant %s", out, in)
	}
}

func TestHTMLLeavesCommentsAndTextIntact(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	in := `<!-- href="/x" --><p>a &lt; b and href = "/y" and url(/z)</p>`
	if out := string(HTML(base, []byte(in))); out != in {
		t.Errorf("text or comment was rewritten:\ngot  %s\nwant %s", out, in)
	}
}

func TestHTMLPreservesTagFormatting(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	in := `<a   id="x"    class='y'   data-n=3 >t</a>`
	if out := string(HTML(base, []byte(in))); out != in {
		t.Errorf("tag was reformatted:\ngot  %s\nwant %s", out, in)
	}
}

func TestHTMLQuotedAngleBracketDoesNotEndTag(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<a title="a > b" href="/x">t</a>`)))
	if !strings.Contains(out, `title="a > b"`) {
		t.Errorf("quoted '>' broke tag parsing:\n%s", out)
	}
	if !strings.Contains(out, link.EncodeString("https://example.com/x")) {
		t.Errorf("href after a quoted '>' was not rewritten:\n%s", out)
	}
}

func TestHTMLDecodesCharacterReferences(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<a href="/s?a=1&amp;b=2">x</a>`)))
	if want := link.EncodeString("https://example.com/s?a=1&b=2"); !strings.Contains(out, want) {
		t.Errorf("&amp; not decoded before resolving; want %q in:\n%s", want, out)
	}
}

func TestHTMLQuotesRewrittenUnquotedValue(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<a href=/plain/path >x</a>`)))
	if want := `href="` + link.EncodeString("https://example.com/plain/path") + `"`; !strings.Contains(out, want) {
		t.Errorf("unquoted value not requoted; want %q in:\n%s", want, out)
	}
}

func TestHTMLStyleAttributeAndBlock(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<style>b{background:url(bg.png)}</style><div style="background:url('x.png')"></div><script>var u="url(bg.png)"</script>`)))
	if !strings.Contains(out, link.EncodeString("https://example.com/bg.png")) {
		t.Errorf("<style> block not rewritten:\n%s", out)
	}
	if !strings.Contains(out, link.EncodeString("https://example.com/x.png")) {
		t.Errorf("style attribute not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `var u="url(bg.png)"`) {
		t.Errorf("url() inside a script was rewritten:\n%s", out)
	}
	if strings.Contains(out, `style=""`) {
		t.Errorf("style attribute quoting broken:\n%s", out)
	}
}

func TestSrcsetKeepsDataURICommas(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<img srcset="data:image/png;base64,AAAA 1x, b.png 2x">`)))
	if !strings.Contains(out, "data:image/png;base64,AAAA 1x") {
		t.Errorf("data: candidate was split on its comma:\n%s", out)
	}
	if want := link.EncodeString("https://example.com/b.png") + " 2x"; !strings.Contains(out, want) {
		t.Errorf("second candidate not rewritten; want %q in:\n%s", want, out)
	}
}

// --- <base href> ---

func TestHTMLBaseSetsResolutionBaseAndIsDropped(t *testing.T) {
	base := mustURL(t, "https://example.com/index.html")
	out := string(HTML(base, []byte(`<head><base href="/app/"><link href="style.css"></head>`)))
	if want := link.EncodeString("https://example.com/app/style.css"); !strings.Contains(out, want) {
		t.Errorf("reference not resolved against <base>; want %q in:\n%s", want, out)
	}
	if strings.Contains(out, "<base href") {
		t.Errorf("<base href> should be dropped:\n%s", out)
	}
	if !strings.Contains(out, "<base") {
		t.Errorf("<base> element itself should remain:\n%s", out)
	}
}

func TestHTMLBaseOnAnotherOrigin(t *testing.T) {
	base := mustURL(t, "https://example.com/index.html")
	out := string(HTML(base, []byte(`<base href="https://cdn.example.net/v2/"><img src="a.png">`)))
	if want := link.EncodeString("https://cdn.example.net/v2/a.png"); !strings.Contains(out, want) {
		t.Errorf("cross-origin <base> not honoured; want %q in:\n%s", want, out)
	}
}

func TestHTMLWithoutBaseUsesDocumentURL(t *testing.T) {
	base := mustURL(t, "https://example.com/dir/page.html")
	out := string(HTML(base, []byte(`<img src="a.png">`)))
	if want := link.EncodeString("https://example.com/dir/a.png"); !strings.Contains(out, want) {
		t.Errorf("document URL not used as base; want %q in:\n%s", want, out)
	}
}

// --- <meta http-equiv="refresh"> ---

func TestMetaRefreshRewritten(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<meta http-equiv="refresh" content="0; url=https://other.example/next">`)))
	if want := link.EncodeString("https://other.example/next"); !strings.Contains(out, want) {
		t.Errorf("meta refresh not rewritten; want %q in:\n%s", want, out)
	}
	if strings.Contains(out, `https://other.example/next"`) {
		t.Errorf("meta refresh still points off-proxy:\n%s", out)
	}
}

func TestMetaRefreshQuotedAndMixedCase(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<meta HTTP-EQUIV="Refresh" content="5;URL='/later'">`)))
	if want := link.EncodeString("https://example.com/later"); !strings.Contains(out, want) {
		t.Errorf("quoted/mixed-case meta refresh not rewritten; want %q in:\n%s", want, out)
	}
}

func TestMetaContentLeftAloneWhenNotRefresh(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	for _, in := range []string{
		`<meta name="description" content="visit https://x.example/y">`,
		`<meta http-equiv="refresh" content="30">`,
	} {
		if out := string(HTML(base, []byte(in))); out != in {
			t.Errorf("meta content altered:\ngot  %s\nwant %s", out, in)
		}
	}
}

// --- structural safety ---

func TestHTMLHandlesTruncatedMarkup(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	for _, in := range []string{`<a href=`, `<a href="/x`, `<!-- unclosed`, `<div`, `<`, `a < b`, `</`, `<script>x`, `<style>a{url(`} {
		// The scanner must terminate and must not drop the trailing bytes.
		if out := string(HTML(base, []byte(in))); out == "" {
			t.Errorf("input %q produced empty output", in)
		}
	}
}

func TestHTMLSelfClosingAndVoidElements(t *testing.T) {
	base := mustURL(t, "https://example.com/")
	out := string(HTML(base, []byte(`<img src="a.png"/><br/><input formaction="/go">`)))
	if !strings.Contains(out, link.EncodeString("https://example.com/a.png")) {
		t.Errorf("self-closing img not rewritten:\n%s", out)
	}
	if !strings.Contains(out, link.EncodeString("https://example.com/go")) {
		t.Errorf("formaction not rewritten:\n%s", out)
	}
}
