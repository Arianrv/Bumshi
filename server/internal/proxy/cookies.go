package proxy

// Cookie namespacing.
//
// Every proxied site shares one browser origin, so left alone the browser hands
// site B every cookie site A set, and the proxy forwards them upstream. A user
// signed into Google would have their session cookie sent to every site they
// then visited through the proxy.
//
// Cookies are therefore stored in the browser under a name that encodes the
// scope they belong to, and unpacked again for each upstream request:
//
//	upstream  Set-Cookie: SID=x; Domain=.google.com
//	browser   Set-Cookie: b_<hash(google.com)>d_SID=x; Path=/; SameSite=Lax
//	upstream  Cookie: SID=x        — but only for hosts inside that scope
//
// The letter after the hash records how the scope matches: "d" for a Domain
// cookie (the request host must equal the scope or be a subdomain of it) and
// "h" for a host-only cookie (exact host match). Because a request's candidate
// scopes can be enumerated from its host, the mapping needs no server state:
// nothing about the user's sessions is stored here, and the jar stays on the
// user's own device.
//
// Two limits, both deliberate:
//
//   - Path scope is not preserved; a cookie is offered for every path on a
//     matching host. Sites that rely on two same-named cookies at different
//     paths will see the wrong one. This is rare, and encoding paths into
//     cookie names costs more than it buys.
//   - This stops the SERVER from ever sending one site's cookies to another,
//     which is absolute. It does not stop a malicious page on the shared origin
//     from reading the raw browser jar. There is no hard in-browser boundary
//     while every site shares one hostname.

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// cookieNamePrefix marks a cookie as one the proxy packed. It is deliberately
// short: it is repeated on every cookie in every request header.
const cookieNamePrefix = "b_"

// maxCookieScopeLabels bounds how far up the domain tree a Domain attribute may
// reach. Without a public-suffix list this is the practical guard against a site
// claiming Domain=co.uk; RFC 6265 requires at least a suffix match of the
// request host, which is enforced separately.
const minScopeLabels = 2

// fnv1a64 is FNV-1a over the scope host. It is a labelling function, not a
// security primitive: it only has to be compact, deterministic and identical to
// the copy in the browser runtime (see client.js). Any change here must be
// mirrored there, or every user's stored cookies become unreadable at once.
func fnv1a64(s string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// cookiePrefix builds the name prefix for a scope. domainMatch selects between
// subdomain-inclusive ("d") and exact-host ("h") matching.
func cookiePrefix(scope string, domainMatch bool) string {
	kind := "h"
	if domainMatch {
		kind = "d"
	}
	return cookieNamePrefix + strconv.FormatUint(fnv1a64(strings.ToLower(scope)), 16) + kind + "_"
}

// cookieScope decides which scope an upstream Set-Cookie belongs to: the Domain
// attribute when it is present and legitimate, otherwise the request host alone.
//
// A Domain is legitimate only when the request host is inside it and it has at
// least two labels, so a site cannot claim a cookie for a whole suffix and have
// it offered to unrelated hosts.
func cookieScope(domainAttr, requestHost string) (scope string, domainMatch bool) {
	host := strings.ToLower(strings.TrimSuffix(requestHost, "."))
	d := strings.ToLower(strings.TrimSpace(domainAttr))
	d = strings.TrimPrefix(d, ".")
	d = strings.TrimSuffix(d, ".")
	if d == "" || strings.Count(d, ".") < minScopeLabels-1 {
		return host, false
	}
	if d != host && !strings.HasSuffix(host, "."+d) {
		return host, false // not a suffix of the request host: ignore it
	}
	return d, true
}

// scopePrefixes returns every cookie-name prefix whose cookies belong on a
// request to host: the host-only prefix, plus a Domain prefix for the host and
// each of its parent domains down to the two-label limit.
func scopePrefixes(host string) []string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "" {
		return nil
	}
	out := []string{cookiePrefix(h, false), cookiePrefix(h, true)}
	for rest := h; ; {
		dot := strings.IndexByte(rest, '.')
		if dot < 0 {
			break
		}
		rest = rest[dot+1:]
		if strings.Count(rest, ".") < minScopeLabels-1 {
			break
		}
		out = append(out, cookiePrefix(rest, true))
	}
	return out
}

// packSetCookie rewrites an upstream Set-Cookie for storage in the browser under
// the proxy origin. It reports false when the header cannot be parsed, in which
// case the cookie is dropped rather than passed through unscoped.
//
// secure forces the Secure attribute, which is correct whenever the proxy is
// reached over HTTPS: the cookie is being stored against the proxy's origin, not
// the target's, so the target's own view of transport security does not apply.
func packSetCookie(raw string, target *url.URL, secure bool) (string, bool) {
	parsed := (&http.Response{Header: http.Header{"Set-Cookie": {raw}}}).Cookies()
	if len(parsed) != 1 {
		return "", false
	}
	c := parsed[0]
	if c.Name == "" {
		return "", false
	}

	scope, domainMatch := cookieScope(c.Domain, target.Hostname())
	out := &http.Cookie{
		Name:     cookiePrefix(scope, domainMatch) + c.Name,
		Value:    c.Value,
		Path:     "/",
		Secure:   secure || c.Secure,
		HttpOnly: c.HttpOnly,
		// Everything is same-site once it is on the proxy origin, so Lax is both
		// sufficient and the safer choice: it keeps the jar from riding along on
		// cross-site requests that some other page makes to the proxy.
		SameSite: http.SameSiteLaxMode,
		Expires:  c.Expires,
		MaxAge:   c.MaxAge,
	}
	s := out.String()
	if s == "" {
		return "", false
	}
	return s, true
}

// unpackCookies builds the Cookie header for an upstream request from the jar
// the browser sent, selecting only the cookies scoped to the target host and
// restoring their original names.
//
// Anything without a recognised prefix is dropped. That is what keeps the
// bumshi_access token — and any cookie a page set outside the runtime shim —
// from ever reaching a target site.
func unpackCookies(r *http.Request, targetHost string) string {
	prefixes := scopePrefixes(targetHost)
	if len(prefixes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range r.Cookies() {
		name, ok := stripCookiePrefix(c.Name, prefixes)
		if !ok {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(c.Value)
	}
	return b.String()
}

func stripCookiePrefix(name string, prefixes []string) (string, bool) {
	if !strings.HasPrefix(name, cookieNamePrefix) {
		return "", false
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return name[len(p):], true
		}
	}
	return "", false
}
