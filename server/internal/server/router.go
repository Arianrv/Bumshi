package server

import (
	"encoding/json"
	"net/http"

	"github.com/bumshi/bumshi/server/internal/health"
	"github.com/bumshi/bumshi/server/internal/proxy/link"
	"github.com/bumshi/bumshi/server/internal/version"
	"github.com/bumshi/bumshi/server/internal/webengine"
)

// routes builds the control-plane router: the generic web proxy under
// link.Prefix and its browser runtime under webengine.Prefix, each only when its
// handler is non-nil.
//
// The admin panel is deliberately absent. It runs on its own listener (see
// config.AdminAddr) so it never shares an origin with proxied content.
func routes(hc *health.Checker, proxyHandler, engineHandler, authHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", hc.Live)
	mux.HandleFunc("GET /readyz", hc.Ready)
	mux.HandleFunc("GET /version", handleVersion)
	if proxyHandler != nil {
		mux.Handle(link.Prefix, proxyHandler)
	}
	if engineHandler != nil {
		mux.Handle(webengine.Prefix, engineHandler)
	}
	if authHandler != nil {
		// More specific than the asset prefix above, so it wins.
		mux.Handle("GET "+webengine.AuthPath, authHandler)
	}
	return mux
}

func handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(version.Get())
}
