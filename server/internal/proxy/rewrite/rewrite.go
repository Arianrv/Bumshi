// Package rewrite performs dependency-free URL rewriting of HTML and CSS
// response bodies so that navigations and subresource loads are routed back
// through the proxy.
//
// HTML is scanned as markup (see scan.go), not matched with regular
// expressions: only attribute values inside real start tags and CSS inside
// <style> blocks and style="" attributes are rewritten. Text nodes, comments
// and <script> bodies are copied byte-for-byte, so inline JavaScript and
// embedded JSON survive untouched.
//
// Scope: this layer covers the URLs present in the markup the server sends.
// URLs a page builds at runtime in JavaScript are handled by the browser
// runtime in internal/webengine (service worker + in-page hooks), which is the
// robust way to cover fully dynamic sites.
package rewrite

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/bumshi/bumshi/server/internal/proxy/link"
)

var (
	// CSS url() with optional quotes.
	cssURLRe = regexp.MustCompile(`(?i)url\(\s*("[^"]*"|'[^']*'|[^)'"]+)\s*\)`)
	// @import with a bare string target (the url() form is handled above).
	cssImportRe = regexp.MustCompile(`(?i)@import\s+("[^"]*"|'[^']*')`)
)

// Classify reports whether a body of the given content-type should be rewritten,
// and if so whether it is HTML or CSS.
func Classify(contentType string) (html, css bool) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(ct, "text/html"), strings.HasPrefix(ct, "application/xhtml+xml"):
		return true, false
	case strings.HasPrefix(ct, "text/css"):
		return false, true
	default:
		return false, false
	}
}

// HTML rewrites the URL-bearing attributes and embedded CSS of an HTML document.
// References resolve against the document's base URL, which is target unless the
// document carries a <base href>.
func HTML(target *url.URL, body []byte) []byte {
	src := string(body)
	r := &htmlScanner{src: src, base: documentBase(src, target)}
	r.out.Grow(len(src) + len(src)/8)
	r.run()
	return []byte(r.out.String())
}

// CSS rewrites url() and @import references in a stylesheet relative to base.
func CSS(base *url.URL, body []byte) []byte {
	return []byte(rewriteCSS(base, string(body)))
}

// rewriteCSS rewrites every reference in a block of CSS. Values that do not
// change are returned exactly as written, so the stylesheet is not reformatted.
func rewriteCSS(base *url.URL, css string) string {
	out := cssURLRe.ReplaceAllStringFunc(css, func(m string) string {
		g := cssURLRe.FindStringSubmatch(m)
		if g == nil {
			return m
		}
		quote, val := splitQuoted(g[1])
		next := link.Resolve(base, val)
		if next == val {
			return m
		}
		if quote == "" {
			quote = `"`
		}
		return "url(" + quote + next + quote + ")"
	})

	return cssImportRe.ReplaceAllStringFunc(out, func(m string) string {
		g := cssImportRe.FindStringSubmatch(m)
		if g == nil {
			return m
		}
		quote, val := splitQuoted(g[1])
		next := link.Resolve(base, val)
		if next == val {
			return m
		}
		// g[1] is the trailing quoted string of the match.
		return m[:len(m)-len(g[1])] + quote + next + quote
	})
}

// splitQuoted separates an optional surrounding quote from a value.
func splitQuoted(raw string) (quote, value string) {
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		return string(raw[0]), raw[1 : len(raw)-1]
	}
	return "", raw
}
