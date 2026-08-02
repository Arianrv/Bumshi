// Package webengine serves the browser-side proxy runtime (service worker and
// client hooks) and injects its bootstrap into proxied HTML documents.
//
// The runtime lives in assets/*.js and is embedded into the binary. It is
// served under Prefix ("/__bumshi__/"); the service worker is served with the
// Service-Worker-Allowed header so it can claim the whole origin even though it
// is hosted under a sub-path.
package webengine

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// Prefix is the path under which the runtime assets are served.
const Prefix = "/__bumshi__/"

//go:embed assets/codec.js assets/rewriter.js assets/client.js assets/sw.js
var assetFS embed.FS

// modTime is fixed at process start so conditional requests are stable.
var modTime = time.Now()

// bootstrap is the script block injected into proxied HTML. Order matters:
// codec and rewriter define the shared helpers that client.js relies on, and
// client.js must run before the page's own scripts.
var bootstrap = []byte(`<script src="/__bumshi__/codec.js"></script>` +
	`<script src="/__bumshi__/rewriter.js"></script>` +
	`<script src="/__bumshi__/client.js"></script>`)

// Handler serves the embedded runtime assets under Prefix.
func Handler() http.Handler {
	sub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		// Unreachable: the embedded directory always exists.
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, Prefix)
		if name == "" || strings.ContainsAny(name, "/\\") {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		// Revalidate on every load (ServeContent answers with 304 when unchanged)
		// so a redeployed runtime takes effect immediately, without users having
		// to clear cached assets or an old service worker.
		h.Set("Cache-Control", "no-cache")
		if name == "sw.js" {
			// Allow a worker hosted under /__bumshi__/ to control scope "/".
			h.Set("Service-Worker-Allowed", "/")
		}
		http.ServeContent(w, r, name, modTime, bytes.NewReader(data))
	})
}

// Inject inserts the bootstrap script block into an HTML document, immediately
// after the opening <head> tag when present, otherwise at the very start.
func Inject(body []byte) []byte {
	if idx := headOpenTag(body); idx >= 0 {
		if gt := bytes.IndexByte(body[idx:], '>'); gt >= 0 {
			pos := idx + gt + 1
			out := make([]byte, 0, len(body)+len(bootstrap))
			out = append(out, body[:pos]...)
			out = append(out, bootstrap...)
			out = append(out, body[pos:]...)
			return out
		}
	}
	return append(append([]byte(nil), bootstrap...), body...)
}

// headOpenTag returns the index of the document's opening <head> tag, or -1.
//
// The tag name must actually end there: a bare prefix search also matches
// <header>, which in a document with no <head> would place the bootstrap in the
// middle of the body, after the page's own scripts have already run.
func headOpenTag(body []byte) int {
	lower := bytes.ToLower(body)
	for from := 0; ; {
		i := bytes.Index(lower[from:], []byte("<head"))
		if i < 0 {
			return -1
		}
		i += from
		end := i + len("<head")
		if end == len(body) {
			return -1
		}
		switch body[end] {
		case '>', '/', ' ', '\t', '\n', '\f', '\r':
			return i
		}
		from = end
	}
}
