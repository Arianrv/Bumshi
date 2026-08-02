// Package link defines the proxy URL scheme: an absolute target URL is encoded
// into a single path segment under a fixed prefix, and relative references are
// resolved back into proxy links during rewriting.
package link

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

// Prefix is the path prefix under which proxied targets are served.
const Prefix = "/p/"

var enc = base64.RawURLEncoding

// Errors returned by Decode.
var (
	ErrEmpty     = errors.New("link: empty target")
	ErrMalformed = errors.New("link: malformed encoded target")
	ErrScheme    = errors.New("link: only http and https targets are allowed")
)

// nonNavigational are reference prefixes that must never be rewritten into
// proxy links (they are not fetchable http(s) navigations).
var nonNavigational = []string{"#", "data:", "blob:", "mailto:", "tel:", "javascript:", "about:", "ws:", "wss:"}

// Encode returns the proxy path for an absolute target URL.
func Encode(target *url.URL) string {
	return Prefix + enc.EncodeToString([]byte(target.String()))
}

// EncodeString is Encode for a raw absolute URL string.
func EncodeString(raw string) string {
	return Prefix + enc.EncodeToString([]byte(raw))
}

// Decode extracts and validates the absolute target URL from a request path.
// The path may include the Prefix and any trailing "/extra" segments, which are
// ignored — only the first segment (the encoded token) is used.
func Decode(path string) (*url.URL, error) {
	token := strings.TrimPrefix(path, Prefix)
	if i := strings.IndexByte(token, '/'); i >= 0 {
		token = token[:i]
	}
	if token == "" {
		return nil, ErrEmpty
	}
	raw, err := enc.DecodeString(token)
	if err != nil {
		return nil, ErrMalformed
	}
	u, err := url.Parse(string(raw))
	if err != nil {
		return nil, ErrMalformed
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrScheme
	}
	if u.Host == "" {
		return nil, ErrMalformed
	}
	return u, nil
}

// DecodeRequest decodes the target of a proxy request from its escaped path and
// applies the request's own query string.
//
// A query on the proxy request itself — as opposed to one inside the encoded
// token — comes from a GET form whose action is a proxy link: the browser
// submits to "/p/<token>?field=value". The HTML form-submission algorithm
// replaces the action URL's own query with the form data, so this mirrors it
// exactly. Decoding the token alone drops the submission, which makes every
// search box on every proxied site silently return the unfiltered page.
func DecodeRequest(escapedPath, rawQuery string) (*url.URL, error) {
	u, err := Decode(escapedPath)
	if err != nil {
		return nil, err
	}
	if rawQuery != "" {
		u.RawQuery = rawQuery
		u.ForceQuery = false
	}
	return u, nil
}

// Resolve resolves a possibly-relative reference against base and returns the
// proxy path pointing at the resulting absolute URL. References that are not
// http(s) navigations (fragments, data:, javascript:, etc.) are returned
// unchanged.
func Resolve(base *url.URL, ref string) string {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return ref
	}
	low := strings.ToLower(trimmed)
	for _, p := range nonNavigational {
		if strings.HasPrefix(low, p) {
			return ref
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ref
	}
	abs := base.ResolveReference(u)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ref
	}
	return Encode(abs)
}
