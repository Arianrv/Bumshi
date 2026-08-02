package proxy

// Upstream request logging.
//
// When a site treats proxied requests differently from a browser's own, the
// question is always "what exactly did we send?" — and every answer reached by
// reading the code instead of the wire has so far been wrong. This records the
// real header set for a chosen host so it can be diffed against what the same
// browser sends directly.
//
// Off unless BUMSHI_LOG_UPSTREAM_HOST names a host substring. It is deliberately
// awkward to enable: this is the one place where browsing would otherwise never
// be written down, so it should be turned on for a diagnosis and turned off
// again. Cookie and Authorization VALUES are never logged. What matters for
// diagnosis is which cookie names are present, how long their values are, and
// whether any name appears twice; the values themselves are live session
// credentials and would add nothing.

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// describeCookies renders a Cookie header as names with value lengths, flagging
// any name that appears more than once. A duplicate is the thing worth looking
// for: it means one logical cookie is stored under two scopes, and sites that
// sign a cookie read the conflict as tampering.
func describeCookies(v string) string {
	counts := map[string]int{}
	names := make([]string, 0, 8)
	sizes := make([]int, 0, 8)
	for _, part := range strings.Split(v, ";") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		name, size := p, 0
		if eq := strings.IndexByte(p, '='); eq >= 0 {
			name = p[:eq]
			size = len(p) - eq - 1
		}
		counts[name]++
		names = append(names, name)
		sizes = append(sizes, size)
	}
	if len(names) == 0 {
		return "(none)"
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n + "(" + strconv.Itoa(sizes[i]) + ")"
		if counts[n] > 1 {
			out[i] += " <<DUPLICATE"
		}
	}
	return strings.Join(out, ", ")
}

// logUpstreamRequest records the exact header set sent upstream for hosts
// matching the configured substring. Headers are sorted so two captures can be
// diffed directly.
func (h *Handler) logUpstreamRequest(out *http.Request) {
	if h.logUpstream == "" || out.URL == nil {
		return
	}
	if !strings.Contains(strings.ToLower(out.URL.Hostname()), h.logUpstream) {
		return
	}

	keys := make([]string, 0, len(out.Header))
	for k := range out.Header {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b strings.Builder
	b.WriteString(out.Method)
	b.WriteByte(' ')
	b.WriteString(out.URL.Scheme)
	b.WriteString("://")
	b.WriteString(out.Host)
	b.WriteString(out.URL.EscapedPath())
	for _, k := range keys {
		for _, v := range out.Header[k] {
			b.WriteString("\n  ")
			b.WriteString(k)
			b.WriteString(": ")
			switch k {
			case "Cookie":
				b.WriteString(describeCookies(v))
			case "Authorization":
				b.WriteString("(" + strconv.Itoa(len(v)) + " bytes)")
			default:
				b.WriteString(v)
			}
		}
	}
	h.logger.Info("upstream request", "detail", b.String())
}
