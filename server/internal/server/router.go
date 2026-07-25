package server

import (
	"encoding/json"
	"net/http"

	"github.com/bumshi/bumshi/server/internal/health"
	"github.com/bumshi/bumshi/server/internal/proxy/link"
	"github.com/bumshi/bumshi/server/internal/version"
	"github.com/bumshi/bumshi/server/internal/webengine"
)

// routes builds the control-plane router. The generic web proxy is mounted under
// link.Prefix, its browser runtime under webengine.Prefix, and the admin panel
// under adminPath — each only when its handler is non-nil. The YouTube and
// Telegram modules follow in later milestones.
func routes(hc *health.Checker, proxyHandler, engineHandler, adminHandler http.Handler, adminPath string) http.Handler {
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
	if adminHandler != nil && adminPath != "" {
		mux.Handle(adminPath, adminHandler)
	}
	return mux
}

func handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(version.Get())
}
