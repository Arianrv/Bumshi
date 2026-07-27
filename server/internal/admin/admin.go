// Package admin implements the server-side admin panel: authentication (session
// cookies + CSRF), a small JSON API, and an embedded dark UI modeled on the
// 3x-ui layout. It is deployer-only and served under a configurable base path.
// It is never part of the client app.
package admin

import (
	"bytes"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bumshi/bumshi/server/internal/auth"
	"github.com/bumshi/bumshi/server/internal/settings"
	"github.com/bumshi/bumshi/server/internal/version"
)

// AdminSessionTTL is how long an admin session (and its cookie) stays valid.
const AdminSessionTTL = 8 * time.Hour

const (
	sessionCookie = "bumshi_admin_session"
	csrfCookie    = "bumshi_admin_csrf"
	csrfHeader    = "X-CSRF-Token"
	maxBodyBytes  = 16 << 10
)

//go:embed assets/login.html assets/app.html assets/style.css assets/app.js assets/i18n.js assets/theme.js
var assetFS embed.FS

// Options configures the admin Handler.
type Options struct {
	BasePath     string // must start and end with '/', e.g. "/admin/"
	Username     string
	PasswordHash string
	Secure       bool   // set the Secure flag on cookies (true behind HTTPS)
	PublicURL    string // base URL end users connect to (for connection links)
	Settings     *settings.Settings
	Sessions     *auth.SessionStore
	Logins       *auth.RateLimiter
	Access       *AccessStore
	Logger       *slog.Logger
	StartedAt    time.Time
}

// Handler is the admin panel HTTP handler. Mount it under Options.BasePath.
type Handler struct {
	opts Options
	mux  *http.ServeMux
}

// New builds an admin Handler.
func New(o Options) *Handler {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	h := &Handler{opts: o}
	b := o.BasePath
	m := http.NewServeMux()
	m.HandleFunc("GET "+b, h.serveApp)
	m.HandleFunc("GET "+b+"login", h.serveLogin)
	m.HandleFunc("POST "+b+"login", h.handleLogin)
	m.HandleFunc("POST "+b+"logout", h.handleLogout)
	m.HandleFunc("GET "+b+"assets/{file}", h.serveAsset)
	m.HandleFunc("GET "+b+"api/status", h.auth(h.apiStatus))
	m.HandleFunc("GET "+b+"api/settings", h.auth(h.apiGetSettings))
	m.HandleFunc("POST "+b+"api/settings", h.authCSRF(h.apiPutSettings))
	m.HandleFunc("GET "+b+"api/access-users", h.auth(h.apiListUsers))
	m.HandleFunc("POST "+b+"api/access-users", h.authCSRF(h.apiCreateUser))
	m.HandleFunc("POST "+b+"api/access-users/delete", h.authCSRF(h.apiDeleteUser))
	h.mux = m
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

// --- auth middleware ---

func (h *Handler) currentSession(r *http.Request) (auth.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, false
	}
	return h.opts.Sessions.Get(c.Value)
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.currentSession(r); !ok {
			writeJSON(w, http.StatusUnauthorized, errBody("unauthorized"))
			return
		}
		next(w, r)
	}
}

func (h *Handler) authCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := h.currentSession(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, errBody("unauthorized"))
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(sess.CSRF)) != 1 {
			writeJSON(w, http.StatusForbidden, errBody("invalid CSRF token"))
			return
		}
		next(w, r)
	}
}

// --- pages & assets ---

func (h *Handler) serveApp(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.currentSession(r); !ok {
		http.Redirect(w, r, h.opts.BasePath+"login", http.StatusSeeOther)
		return
	}
	h.writeAsset(w, r, "app.html", "text/html; charset=utf-8")
}

func (h *Handler) serveLogin(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.currentSession(r); ok {
		http.Redirect(w, r, h.opts.BasePath, http.StatusSeeOther)
		return
	}
	h.writeAsset(w, r, "login.html", "text/html; charset=utf-8")
}

func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if strings.ContainsAny(file, "/\\") {
		http.NotFound(w, r)
		return
	}
	var ct string
	switch {
	case strings.HasSuffix(file, ".css"):
		ct = "text/css; charset=utf-8"
	case strings.HasSuffix(file, ".js"):
		ct = "text/javascript; charset=utf-8"
	default:
		http.NotFound(w, r)
		return
	}
	h.writeAsset(w, r, file, ct)
}

func (h *Handler) writeAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	data, err := assetFS.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Templating: the base path is injected so the assets work under any mount.
	if strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".js") {
		data = bytes.ReplaceAll(data, []byte("__BASE__"), []byte(h.opts.BasePath))
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// --- login / logout ---

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !h.opts.Logins.Allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, errBody("too many attempts, please wait"))
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid request"))
		return
	}

	userOK := subtle.ConstantTimeCompare([]byte(body.Username), []byte(h.opts.Username)) == 1
	passOK, _ := auth.VerifyPassword(h.opts.PasswordHash, body.Password)
	if !userOK || !passOK {
		h.opts.Logger.Warn("failed admin login", "ip", ip)
		writeJSON(w, http.StatusUnauthorized, errBody("invalid credentials"))
		return
	}

	token, csrf, err := h.opts.Sessions.Create(h.opts.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("internal error"))
		return
	}
	h.opts.Logins.Reset(ip)
	h.setCookie(w, sessionCookie, token, true)
	h.setCookie(w, csrfCookie, csrf, false) // readable by JS (double-submit)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect": h.opts.BasePath})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		h.opts.Sessions.Delete(c.Value)
	}
	h.clearCookie(w, sessionCookie)
	h.clearCookie(w, csrfCookie)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "redirect": h.opts.BasePath + "login"})
}

// --- API ---

func (h *Handler) apiStatus(w http.ResponseWriter, _ *http.Request) {
	info := version.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       info.Version,
		"commit":        info.Commit,
		"go":            info.GoVersion,
		"uptimeSeconds": int(time.Since(h.opts.StartedAt).Seconds()),
		"settings":      h.opts.Settings.Snapshot(),
		"sessions":      h.opts.Sessions.Count(),
		"accessUsers":   len(h.opts.Access.List()),
	})
}

func (h *Handler) apiGetSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.opts.Settings.Snapshot())
}

func (h *Handler) apiPutSettings(w http.ResponseWriter, r *http.Request) {
	var snap settings.Snapshot
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&snap); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid request"))
		return
	}
	h.opts.Settings.Apply(snap)
	h.opts.Logger.Info("admin updated settings", "proxy_enabled", snap.ProxyEnabled, "access_log", snap.AccessLog)
	writeJSON(w, http.StatusOK, h.opts.Settings.Snapshot())
}

type userView struct {
	AccessUser
	Link    string `json:"link"`
	Expired bool   `json:"expired"`
}

func newUserView(u AccessUser, link string) userView {
	return userView{AccessUser: u, Link: link, Expired: u.Expired()}
}

func (h *Handler) apiListUsers(w http.ResponseWriter, _ *http.Request) {
	users := h.opts.Access.List()
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, newUserView(u, h.connectionLink(u)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) apiCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label       string `json:"label"`
		ExpiresDays int    `json:"expiresDays"` // 0 = never expires
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&body)
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "user"
	}
	var expires *time.Time
	if body.ExpiresDays > 0 {
		t := time.Now().UTC().Add(time.Duration(body.ExpiresDays) * 24 * time.Hour)
		expires = &t
	}
	u, err := h.opts.Access.Create(label, expires)
	if err != nil {
		// An empty token means secure randomness failed (fatal); otherwise the
		// user exists in memory but the roster couldn't be written to disk —
		// usable now, just not persisted, so surface it in the logs and continue.
		if u.Token == "" {
			writeJSON(w, http.StatusInternalServerError, errBody("internal error"))
			return
		}
		h.opts.Logger.Error("access user created but not persisted to disk", "error", err)
	}
	writeJSON(w, http.StatusOK, newUserView(u, h.connectionLink(u)))
}

func (h *Handler) apiDeleteUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, errBody("invalid request"))
		return
	}
	if err := h.opts.Access.Delete(body.ID); err != nil {
		h.opts.Logger.Error("access-user deletion not persisted to disk", "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// connectionLink builds the bumshi:// link the app consumes to configure itself.
func (h *Handler) connectionLink(u AccessUser) string {
	payload, _ := json.Marshal(map[string]string{
		"url":   h.opts.PublicURL,
		"token": u.Token,
		"label": u.Label,
	})
	return "bumshi://connect#" + base64.RawURLEncoding.EncodeToString(payload)
}

// --- cookies & helpers ---

func (h *Handler) setCookie(w http.ResponseWriter, name, value string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     h.opts.BasePath,
		MaxAge:   int(AdminSessionTTL.Seconds()),
		HttpOnly: httpOnly,
		Secure:   h.opts.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     h.opts.BasePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.opts.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
