package proxy

// Content-Security-Policy rewriting.
//
// The policy a site sends describes its own origins, which no longer exist from
// the browser's point of view: every resource now arrives from the proxy. Left
// untouched, the policy blocks the whole page. Dropped entirely — the previous
// behaviour — one XSS anywhere becomes total compromise of every site the user
// browses, because they all share this origin.
//
// So the policy is translated rather than discarded. Host and scheme sources
// collapse to 'self', since that is genuinely where everything comes from now,
// while keywords, nonces and hashes are preserved: the site's decisions about
// inline script, eval, framing and form targets survive intact.
//
// Honest limit: under a shared origin, 'self' is a much weaker statement than
// the site intended — it now means "any site the user has proxied", not "this
// site". What survives is the part of CSP that is not origin-based, which is
// most of what actually stops XSS in practice.

import (
	"crypto/rand"
	"encoding/base64"
	"slices"
	"strings"
)

// cspSourceKeywords are source expressions that carry meaning on their own and
// must survive translation unchanged.
var cspSourceKeywords = []string{
	"'self'",
	"'none'",
	"'unsafe-inline'",
	"'unsafe-eval'",
	"'unsafe-hashes'",
	"'wasm-unsafe-eval'",
	"'strict-dynamic'",
	"'report-sample'",
	"'inline-speculation-rules'",
}

// cspVerbatimDirectives take no source list, so their values pass through.
var cspVerbatimDirectives = []string{
	"upgrade-insecure-requests",
	"block-all-mixed-content",
	"sandbox",
	"require-trusted-types-for",
	"trusted-types",
}

// cspDroppedDirectives are reporting endpoints. They point at the target site's
// collector, which the browser can no longer reach directly, and forwarding
// reports through the proxy would tell that site about the user's session for
// no benefit to either side.
var cspDroppedDirectives = []string{
	"report-uri",
	"report-to",
}

// cspScriptDirectives must admit the injected runtime, or the page loads with
// no proxy hooks at all and every dynamic request escapes.
var cspScriptDirectives = []string{
	"script-src",
	"script-src-elem",
	"default-src",
}

// newCSPNonce returns a fresh nonce for one response.
func newCSPNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b[:])
}

// rewriteCSP translates an upstream policy for the proxy origin and grants the
// injected runtime permission to run via nonce. It returns "" when the policy is
// empty, in which case no header should be sent.
func rewriteCSP(policy, nonce string) string {
	var out []string

	for _, raw := range strings.Split(policy, ";") {
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if slices.Contains(cspDroppedDirectives, name) {
			continue
		}
		if slices.Contains(cspVerbatimDirectives, name) {
			out = append(out, strings.Join(fields, " "))
			continue
		}

		sources := translateCSPSources(fields[1:])
		if slices.Contains(cspScriptDirectives, name) && nonce != "" {
			// 'none' means "nothing may run"; the runtime still has to, so the
			// nonce replaces it rather than joining it (a list containing 'none'
			// alongside anything else is invalid).
			sources = removeCSPSource(sources, "'none'")
			sources = append(sources, "'nonce-"+nonce+"'")
		}
		if len(sources) == 0 {
			sources = []string{"'none'"}
		}
		out = append(out, name+" "+strings.Join(sources, " "))
	}

	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "; ")
}

// translateCSPSources maps a source list onto the proxy origin: every host or
// scheme source becomes 'self', because that is where the proxy serves it from.
// Keywords, nonces and hashes are meaningful independently of origin and are
// kept as they are.
func translateCSPSources(sources []string) []string {
	var out []string
	seen := make(map[string]bool, len(sources))
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range sources {
		lower := strings.ToLower(s)
		switch {
		case slices.Contains(cspSourceKeywords, lower):
			add(lower)
		case strings.HasPrefix(lower, "'nonce-"), strings.HasPrefix(lower, "'sha256-"),
			strings.HasPrefix(lower, "'sha384-"), strings.HasPrefix(lower, "'sha512-"):
			add(s) // case-sensitive payload
		case lower == "data:", lower == "blob:", lower == "filesystem:", lower == "mediastream:":
			add(lower) // not fetched through the proxy, so still valid as written
		default:
			add("'self'")
		}
	}
	return out
}

func removeCSPSource(sources []string, drop string) []string {
	out := sources[:0]
	for _, s := range sources {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}
