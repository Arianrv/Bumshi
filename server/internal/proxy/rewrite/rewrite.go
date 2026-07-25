// Package rewrite performs best-effort, dependency-free URL rewriting of HTML
// and CSS response bodies so that navigations and subresource loads are routed
// back through the proxy.
//
// Scope and honesty: this is a v1 aimed at static and lightly-dynamic pages. It
// rewrites the common URL-bearing HTML attributes, srcset, and CSS url(). It
// deliberately does NOT attempt to rewrite JavaScript — URLs constructed at
// runtime are handled by the client service-worker runtime in a later milestone,
// which is the robust way to cover fully dynamic sites. Keeping this layer
// simple avoids the brittle JS-rewriting that plagues ad-hoc proxies.
package rewrite

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/bumshi/bumshi/server/internal/proxy/link"
)

var (
	// URL-bearing HTML attributes with quoted or unquoted values.
	attrRe = regexp.MustCompile(`(?i)\b(href|src|poster|formaction|action|data-src|data-href)\s*=\s*("[^"]*"|'[^']*'|[^\s"'<>` + "`" + `]+)`)
	// srcset requires quoting because it contains commas and spaces.
	srcsetRe = regexp.MustCompile(`(?i)\bsrcset\s*=\s*("[^"]*"|'[^']*')`)
	// CSS url() with optional quotes.
	cssURLRe = regexp.MustCompile(`(?i)url\(\s*("[^"]*"|'[^']*'|[^)'"]+)\s*\)`)
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

// HTML rewrites URL-bearing attributes, srcset, and embedded CSS url() in an
// HTML document relative to base.
func HTML(base *url.URL, body []byte) []byte {
	s := string(body)
	s = attrRe.ReplaceAllStringFunc(s, func(m string) string { return rewriteAttr(base, m) })
	s = srcsetRe.ReplaceAllStringFunc(s, func(m string) string { return rewriteSrcset(base, m) })
	s = cssURLRe.ReplaceAllStringFunc(s, func(m string) string { return rewriteCSSURL(base, m) })
	return []byte(s)
}

// CSS rewrites url() references in a stylesheet relative to base.
func CSS(base *url.URL, body []byte) []byte {
	out := cssURLRe.ReplaceAllStringFunc(string(body), func(m string) string { return rewriteCSSURL(base, m) })
	return []byte(out)
}

func rewriteAttr(base *url.URL, m string) string {
	g := attrRe.FindStringSubmatch(m)
	if g == nil {
		return m
	}
	name, raw := g[1], g[2]
	quote, val := splitQuoted(raw)
	nv := link.Resolve(base, val)
	if nv == val {
		return m
	}
	if quote == "" {
		quote = `"`
	}
	return name + "=" + quote + nv + quote
}

func rewriteSrcset(base *url.URL, m string) string {
	g := srcsetRe.FindStringSubmatch(m)
	if g == nil {
		return m
	}
	quote, val := splitQuoted(g[1])
	parts := strings.Split(val, ",")
	for i, p := range parts {
		fields := strings.Fields(strings.TrimSpace(p))
		if len(fields) == 0 {
			continue
		}
		fields[0] = link.Resolve(base, fields[0])
		parts[i] = strings.Join(fields, " ")
	}
	return "srcset=" + quote + strings.Join(parts, ", ") + quote
}

func rewriteCSSURL(base *url.URL, m string) string {
	g := cssURLRe.FindStringSubmatch(m)
	if g == nil {
		return m
	}
	quote, val := splitQuoted(g[1])
	nv := link.Resolve(base, val)
	if quote == "" {
		quote = `"`
	}
	return "url(" + quote + nv + quote + ")"
}

// splitQuoted separates an optional surrounding quote from a value.
func splitQuoted(raw string) (quote, value string) {
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		return string(raw[0]), raw[1 : len(raw)-1]
	}
	return "", raw
}
