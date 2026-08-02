package rewrite

// HTML scanning.
//
// This file exists because a regular-expression sweep over a whole document
// cannot tell markup from text. The previous rewriter did exactly that, and so:
//
//   - rewrote `el.href = "/login"` inside a <script> block, breaking the script;
//   - matched `href=\` inside escaped JSON such as "<a href=\"/x\"",
//     producing a JavaScript syntax error;
//   - matched the tail of unrelated attributes: `data-action`, `data-track-src`;
//   - rewrote `url(...)` occurring in JavaScript string literals.
//
// The scanner below only ever rewrites values inside real start tags and CSS
// inside <style> blocks and style="" attributes. Text nodes, comments,
// doctypes and <script> bodies are copied through byte-for-byte. Attribute
// values that do not change are spliced from the original bytes, so the
// document is never reformatted.

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/bumshi/bumshi/server/internal/proxy/link"
)

// rawText elements have CDATA content: markup inside them is not markup, so the
// scanner skips to the matching end tag instead of looking for the next '<'.
// <style> is in the set but its body is rewritten as CSS; the rest are copied
// verbatim.
var rawText = map[string]bool{
	"script":   true,
	"style":    true,
	"textarea": true,
	"title":    true,
	"xmp":      true,
	"iframe":   true,
	"noembed":  true,
	"noframes": true,
	"noscript": true,
}

// urlAttrs maps a URL-bearing attribute name to the elements on which it is a
// URL. A nil element set means "any element". Restricting the rest is
// deliberate: matching on the attribute name alone rewrites application data
// such as <my-widget data="{...}"> or a framework's own cite/background props.
var urlAttrs = map[string]map[string]bool{
	"href":       nil,
	"src":        nil,
	"data-src":   nil,
	"data-href":  nil,
	"action":     {"form": true},
	"formaction": {"button": true, "input": true},
	"poster":     {"video": true},
	"data":       {"object": true},
	"cite":       {"blockquote": true, "q": true, "del": true, "ins": true},
	"background": {"body": true, "table": true, "td": true, "th": true},
	"longdesc":   {"img": true, "iframe": true, "frame": true},
	"manifest":   {"html": true},
	"profile":    {"head": true},
	"codebase":   {"object": true, "applet": true},
}

// srcsetAttrs hold a comma-separated candidate list rather than a single URL.
var srcsetAttrs = map[string]bool{"srcset": true, "imagesrcset": true, "data-srcset": true}

// headContent is the set of elements that may appear before <body>. The first
// start tag outside it implicitly opens the body, which bounds the <base>
// pre-scan (see documentBase).
var headContent = map[string]bool{
	"html": true, "head": true, "base": true, "basefont": true, "bgsound": true,
	"link": true, "meta": true, "noscript": true, "script": true, "style": true,
	"template": true, "title": true,
}

func isURLAttr(tag, name string) bool {
	tags, ok := urlAttrs[name]
	if !ok {
		return false
	}
	return tags == nil || tags[tag]
}

// --- tokens ---

// htmlAttr records an attribute's byte spans in the source so an unchanged
// value can be spliced back verbatim.
type htmlAttr struct {
	name             string // lower-cased
	start, end       int    // full attribute span, [start,end)
	nameEnd          int    // end of the name as written, for original casing
	valStart, valEnd int    // value span, excluding any quotes
	quote            byte   // '"', '\'' or 0 when unquoted/absent
	hasValue         bool
}

// htmlTag is a parsed start tag. start..end covers "<name ...>" inclusive.
type htmlTag struct {
	name        string // lower-cased
	start, end  int
	attrs       []htmlAttr
	selfClosing bool
}

// parseStartTag parses the start tag beginning at s[i] == '<'. It follows the
// HTML5 tokenizer's tag-open, attribute-name and attribute-value states closely
// enough for rewriting; in particular quoting is respected, so a '>' inside an
// attribute value never terminates the tag. A truncated document yields a tag
// ending at len(s), which the caller emits as-is.
func parseStartTag(s string, i int) (htmlTag, bool) {
	t := htmlTag{start: i}
	j := i + 1
	nameStart := j
	for j < len(s) && !isTagNameEnd(s[j]) {
		j++
	}
	if j == nameStart {
		return t, false
	}
	t.name = strings.ToLower(s[nameStart:j])

	for {
		for j < len(s) && isHTMLSpace(s[j]) {
			j++
		}
		if j >= len(s) {
			t.end = len(s)
			return t, true
		}
		switch s[j] {
		case '>':
			t.end = j + 1
			return t, true
		case '/':
			if j+1 < len(s) && s[j+1] == '>' {
				t.selfClosing = true
				t.end = j + 2
				return t, true
			}
			j++
			continue
		}

		a := htmlAttr{start: j}
		nameFrom := j
		for j < len(s) && !isAttrNameEnd(s[j]) {
			j++
		}
		a.name = strings.ToLower(s[nameFrom:j])
		a.nameEnd = j

		k := j
		for k < len(s) && isHTMLSpace(s[k]) {
			k++
		}
		if k < len(s) && s[k] == '=' {
			k++
			for k < len(s) && isHTMLSpace(s[k]) {
				k++
			}
			a.hasValue = true
			if k < len(s) && (s[k] == '"' || s[k] == '\'') {
				a.quote = s[k]
				k++
				a.valStart = k
				for k < len(s) && s[k] != a.quote {
					k++
				}
				a.valEnd = k
				if k < len(s) {
					k++ // closing quote
				}
			} else {
				a.valStart = k
				for k < len(s) && !isUnquotedValueEnd(s[k]) {
					k++
				}
				a.valEnd = k
			}
			j = k
		} else {
			a.valStart, a.valEnd = j, j
		}
		a.end = j
		if a.name != "" {
			t.attrs = append(t.attrs, a)
		}
	}
}

// findEndTag returns the index of the appropriate end tag for a raw-text
// element, or len(s) when the document is truncated. The tag name must be
// followed by whitespace, '/' or '>', matching the HTML5 rule, so a stray
// "</scriptish" inside a script body does not terminate it.
func findEndTag(s, name string, from int) int {
	for i := from; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			return len(s)
		}
		p := i + j
		if p+1 < len(s) && s[p+1] == '/' && hasTagNameAt(s, p+2, name) {
			return p
		}
		i = p + 1
	}
	return len(s)
}

func hasTagNameAt(s string, pos int, name string) bool {
	if pos+len(name) > len(s) || !strings.EqualFold(s[pos:pos+len(name)], name) {
		return false
	}
	if pos+len(name) == len(s) {
		return true
	}
	switch s[pos+len(name)] {
	case ' ', '\t', '\n', '\f', '\r', '/', '>':
		return true
	}
	return false
}

// --- scanner ---

type htmlScanner struct {
	src  string
	base *url.URL
	out  strings.Builder
}

func (r *htmlScanner) run() {
	s := r.src
	i := 0
	for i < len(s) {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			r.out.WriteString(s[i:])
			return
		}
		lt += i
		r.out.WriteString(s[i:lt])
		i = lt

		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				r.out.WriteString(s[i:])
				return
			}
			end = i + 4 + end + 3
			r.out.WriteString(s[i:end])
			i = end

		case strings.HasPrefix(s[i:], "<!"), strings.HasPrefix(s[i:], "<?"), strings.HasPrefix(s[i:], "</"):
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				r.out.WriteString(s[i:])
				return
			}
			end = i + end + 1
			r.out.WriteString(s[i:end])
			i = end

		case i+1 < len(s) && isASCIILetter(s[i+1]):
			t, ok := parseStartTag(s, i)
			if !ok {
				r.out.WriteByte('<')
				i++
				continue
			}
			r.writeStartTag(t)
			i = t.end
			if t.name == "plaintext" {
				// Obsolete, but once open it never closes: the rest of the
				// document is text.
				r.out.WriteString(s[i:])
				return
			}
			if rawText[t.name] && !t.selfClosing {
				end := findEndTag(s, t.name, i)
				if t.name == "style" {
					r.out.WriteString(rewriteCSS(r.base, s[i:end]))
				} else {
					r.out.WriteString(s[i:end])
				}
				i = end
			}

		default:
			r.out.WriteByte('<')
			i++
		}
	}
}

// writeStartTag emits a start tag, replacing only the attribute values that
// actually change. Everything else — including whitespace and attribute
// ordering — is copied from the source byte-for-byte.
func (r *htmlScanner) writeStartTag(t htmlTag) {
	s := r.src
	refresh := r.isRefreshMeta(t)
	dropIntegrity := r.rewritesOwnBody(t)
	cur := t.start

	for _, a := range t.attrs {
		// Subresource Integrity pins the bytes of the fetched resource. The
		// proxy rewrites stylesheet bodies, so their hashes no longer match and
		// the browser refuses the sheet outright — the page loads unstyled.
		// Script bodies are never rewritten, so their integrity attributes are
		// left intact and keep protecting them.
		if dropIntegrity && a.name == "integrity" {
			r.out.WriteString(s[cur:a.start])
			cur = a.end
			continue
		}
		// <base href> is dropped rather than rewritten. Every URL this package
		// emits is already an absolute proxy path, so the element has no work
		// left to do — and either alternative is wrong: leaving the real site's
		// base sends every reference we missed straight past the proxy, and
		// rewriting it to "/p/<token>" breaks the browser's sibling resolution
		// for relative references. The href is still honoured: documentBase
		// resolved it and it is the base for this whole document.
		if t.name == "base" && a.name == "href" {
			r.out.WriteString(s[cur:a.start])
			cur = a.end
			continue
		}
		if !a.hasValue {
			continue
		}
		value, changed := r.newValue(t.name, a, refresh)
		if !changed {
			continue
		}
		if a.quote == 0 {
			// Rewrite the whole attribute so the new value can be quoted: it
			// may contain characters an unquoted value cannot hold.
			r.out.WriteString(s[cur:a.start])
			r.out.WriteString(s[a.start:a.nameEnd]) // original casing
			r.out.WriteString(`="`)
			r.out.WriteString(escapeAttrValue(value, '"'))
			r.out.WriteByte('"')
			cur = a.end
			continue
		}
		r.out.WriteString(s[cur:a.valStart])
		r.out.WriteString(escapeAttrValue(value, a.quote))
		cur = a.valEnd
	}
	r.out.WriteString(s[cur:t.end])
}

// newValue returns the rewritten value for an attribute and whether it differs
// from the source. The source value is character-reference-decoded first: a
// literal "?a=1&amp;b=2" must be resolved as "?a=1&b=2" or the ampersand is
// encoded into the proxy token doubly escaped.
func (r *htmlScanner) newValue(tag string, a htmlAttr, refreshMeta bool) (string, bool) {
	raw := r.src[a.valStart:a.valEnd]
	val := unescapeHTML(raw)

	var next string
	switch {
	case srcsetAttrs[a.name]:
		next = rewriteSrcset(r.base, val)
	case a.name == "ping" && tag == "a":
		next = rewriteURLList(r.base, val)
	case a.name == "style":
		next = rewriteCSS(r.base, val)
	case refreshMeta && a.name == "content":
		next = rewriteMetaRefresh(r.base, val)
	case isURLAttr(tag, a.name):
		next = link.Resolve(r.base, val)
	default:
		return "", false
	}
	return next, next != val
}

// rewritesOwnBody reports whether the proxy will modify the bytes of the
// resource this element points at — true only for stylesheets, whose url()
// references are rewritten (see rewriteCSS).
func (r *htmlScanner) rewritesOwnBody(t htmlTag) bool {
	if t.name != "link" {
		return false
	}
	for _, a := range t.attrs {
		if !a.hasValue {
			continue
		}
		value := strings.ToLower(unescapeHTML(r.src[a.valStart:a.valEnd]))
		switch a.name {
		case "rel":
			for _, token := range strings.Fields(value) {
				if token == "stylesheet" {
					return true
				}
			}
		case "as":
			if strings.TrimSpace(value) == "style" { // <link rel=preload as=style>
				return true
			}
		}
	}
	return false
}

func (r *htmlScanner) isRefreshMeta(t htmlTag) bool {
	if t.name != "meta" {
		return false
	}
	for _, a := range t.attrs {
		if a.name != "http-equiv" || !a.hasValue {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(unescapeHTML(r.src[a.valStart:a.valEnd])), "refresh") {
			return true
		}
	}
	return false
}

// --- value rewriters ---

// rewriteSrcset rewrites the URLs in a srcset value following the HTML "parse a
// srcset attribute" algorithm: a candidate's URL runs to the next ASCII
// whitespace, so the commas inside a data: URI are not candidate separators.
func rewriteSrcset(base *url.URL, v string) string {
	var b strings.Builder
	b.Grow(len(v) + 32)
	first := true
	for i := 0; i < len(v); {
		for i < len(v) && (isHTMLSpace(v[i]) || v[i] == ',') {
			i++
		}
		if i >= len(v) {
			break
		}
		from := i
		for i < len(v) && !isHTMLSpace(v[i]) {
			i++
		}
		candidate, descriptor := v[from:i], ""
		if strings.HasSuffix(candidate, ",") {
			candidate = strings.TrimRight(candidate, ",")
		} else {
			descFrom := i
			for i < len(v) && v[i] != ',' {
				i++
			}
			descriptor = strings.TrimSpace(v[descFrom:i])
		}
		if candidate == "" {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(link.Resolve(base, candidate))
		if descriptor != "" {
			b.WriteByte(' ')
			b.WriteString(descriptor)
		}
	}
	if first {
		return v
	}
	return b.String()
}

// rewriteURLList rewrites a whitespace-separated list of URLs (the ping
// attribute).
func rewriteURLList(base *url.URL, v string) string {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return v
	}
	for i, f := range fields {
		fields[i] = link.Resolve(base, f)
	}
	return strings.Join(fields, " ")
}

// rewriteMetaRefresh rewrites the URL in a <meta http-equiv="refresh"> content
// value ("5; url=https://elsewhere/"). Left alone, the browser performs the
// redirect itself and leaves the proxy entirely.
func rewriteMetaRefresh(base *url.URL, v string) string {
	semi := strings.IndexByte(v, ';')
	if semi < 0 {
		return v
	}
	rest := v[semi+1:]
	trimmed := strings.TrimLeft(rest, " \t\n\f\r")
	lead := rest[:len(rest)-len(trimmed)]
	if len(trimmed) < 4 || !strings.EqualFold(trimmed[:3], "url") {
		return v
	}
	k := 3
	for k < len(trimmed) && isHTMLSpace(trimmed[k]) {
		k++
	}
	if k >= len(trimmed) || trimmed[k] != '=' {
		return v
	}
	k++
	for k < len(trimmed) && isHTMLSpace(trimmed[k]) {
		k++
	}
	ref := trimmed[k:]
	if len(ref) > 0 && (ref[0] == '"' || ref[0] == '\'') {
		quote := ref[0]
		ref = ref[1:]
		if j := strings.IndexByte(ref, quote); j >= 0 {
			ref = ref[:j]
		}
	} else {
		ref = strings.TrimRight(ref, " \t\n\f\r")
	}
	next := link.Resolve(base, ref)
	if next == ref {
		return v
	}
	return v[:semi+1] + lead + "url=" + next
}

// documentBase returns the URL relative references in the document resolve
// against: the href of the first <base> element when there is one (the HTML
// "document base URL"), otherwise the document's own URL. The scan stops at
// </head> or <body>, where <base> is no longer valid.
func documentBase(s string, target *url.URL) *url.URL {
	for i := 0; i < len(s); {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			return target
		}
		i += lt

		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				return target
			}
			i += 4 + end + 3
			continue
		}
		if strings.HasPrefix(s[i:], "</") {
			if hasTagNameAt(s, i+2, "head") {
				return target
			}
			i++
			continue
		}
		if i+1 >= len(s) || !isASCIILetter(s[i+1]) {
			i++
			continue
		}

		t, ok := parseStartTag(s, i)
		if !ok {
			i++
			continue
		}
		if t.name == "base" {
			for _, a := range t.attrs {
				if a.name != "href" || !a.hasValue {
					continue
				}
				ref := strings.TrimSpace(unescapeHTML(s[a.valStart:a.valEnd]))
				if ref == "" {
					continue
				}
				u, err := url.Parse(ref)
				if err != nil {
					continue
				}
				if abs := target.ResolveReference(u); abs.Scheme == "http" || abs.Scheme == "https" {
					return abs
				}
			}
		} else if !headContent[t.name] {
			// The first element that cannot appear in <head> implicitly opens
			// the body, past which <base> is no longer meaningful. Stopping here
			// keeps this pre-scan proportional to the head, not the document.
			return target
		}
		i = t.end
		if rawText[t.name] && !t.selfClosing {
			i = findEndTag(s, t.name, i)
		}
	}
	return target
}

// --- character references ---

// unescapeHTML decodes the character references that legally appear inside
// attribute values. It handles the named references that occur in URLs plus
// numeric references; anything else is left as written.
func unescapeHTML(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi > 1 && semi <= 12 {
			if r, ok := decodeEntity(s[i+1 : i+semi]); ok {
				b.WriteRune(r)
				i += semi + 1
				continue
			}
		}
		b.WriteByte('&')
		i++
	}
	return b.String()
}

func decodeEntity(e string) (rune, bool) {
	switch strings.ToLower(e) {
	case "amp":
		return '&', true
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "quot":
		return '"', true
	case "apos":
		return '\'', true
	case "nbsp":
		return ' ', true
	}
	if len(e) > 1 && e[0] == '#' {
		digits, bits := e[1:], 10
		if digits[0] == 'x' || digits[0] == 'X' {
			digits, bits = digits[1:], 16
		}
		if n, err := strconv.ParseInt(digits, bits, 32); err == nil && n > 0 && n <= 0x10FFFF {
			return rune(n), true
		}
	}
	return 0, false
}

// escapeAttrValue re-encodes a rewritten value for the quoting context it is
// spliced into, so a value containing the delimiter cannot terminate the tag.
func escapeAttrValue(v string, quote byte) string {
	v = strings.ReplaceAll(v, "&", "&amp;")
	switch quote {
	case '"':
		return strings.ReplaceAll(v, `"`, "&quot;")
	case '\'':
		return strings.ReplaceAll(v, "'", "&#39;")
	}
	return v
}

// --- byte classes ---

func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isTagNameEnd(c byte) bool { return isHTMLSpace(c) || c == '/' || c == '>' }

func isAttrNameEnd(c byte) bool { return isTagNameEnd(c) || c == '=' }

func isUnquotedValueEnd(c byte) bool { return isHTMLSpace(c) || c == '>' }
