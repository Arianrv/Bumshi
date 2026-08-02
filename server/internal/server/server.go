// Package server wires configuration, logging, metrics, health, and routing
// into a runnable control-plane service.
//
// It runs up to three listeners, each isolated from the others on purpose:
// the control plane (public routes, fronted by Caddy), a metrics listener that
// must stay bound to localhost, and — when the panel is enabled — an admin
// listener that is also localhost-bound, so the panel never shares an origin
// with the third-party content the proxy serves. All are shut down gracefully
// when the run context is canceled.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bumshi/bumshi/server/internal/admin"
	"github.com/bumshi/bumshi/server/internal/auth"
	"github.com/bumshi/bumshi/server/internal/config"
	"github.com/bumshi/bumshi/server/internal/health"
	"github.com/bumshi/bumshi/server/internal/httpx"
	"github.com/bumshi/bumshi/server/internal/metrics"
	"github.com/bumshi/bumshi/server/internal/proxy"
	"github.com/bumshi/bumshi/server/internal/proxy/fetch"
	"github.com/bumshi/bumshi/server/internal/settings"
	"github.com/bumshi/bumshi/server/internal/webengine"
)

// Server is the composed control-plane service.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	health *health.Checker

	main    *http.Server
	metrics *http.Server
	admin   *http.Server // nil unless the panel is enabled
}

// New constructs a Server from cfg and logger. It builds the routers and HTTP
// servers but does not open any listeners; call Run for that.
func New(cfg config.Config, logger *slog.Logger) *Server {
	reg := metrics.NewRegistry()
	collectors := metrics.NewHTTPCollectors(reg)
	hc := health.New()

	live := settings.New(cfg.ProxyEnabled, cfg.AccessLog)
	// Persisted beside the access roster, so a panel toggle is not silently
	// undone by the next restart.
	live.Persist(settingsPath(cfg.AccessStorePath), logger)

	// One shared access-user roster: the admin panel manages it, the proxy gates
	// on it. It persists to disk so users survive restarts.
	access, err := admin.NewAccessStore(cfg.AccessStorePath)
	if err != nil {
		logger.Error("could not load access-user roster; continuing in memory (fix the file and restart to re-enable persistence)",
			"path", cfg.AccessStorePath, "error", err)
		access, _ = admin.NewAccessStore("")
	}

	proxyHandler, engineHandler := buildProxy(cfg, logger, reg, live, access)
	var authHandler http.Handler
	if engineHandler != nil {
		authHandler = webengine.AuthHandler(cfg.IsProduction())
	}

	mainHandler := httpx.Chain(
		routes(hc, proxyHandler, engineHandler, authHandler),
		httpx.RequestID(),         // outermost: every layer sees the ID
		httpx.Metrics(collectors), // count even panicked requests (recovered below)
		httpx.Recoverer(logger),
		httpx.SecurityHeaders(),
		httpx.AccessLog(logger, live.AccessLog), // innermost; consulted per request
	)

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", reg.Handler())

	errLog := slog.NewLogLogger(logger.Handler(), slog.LevelError)

	// The panel gets its own listener rather than a path on the control plane.
	// On a shared origin, any page a user opens through the proxy can call the
	// panel's API with the deployer's session cookie attached — same-site rules
	// do not help, because it is literally the same site. Bound to localhost by
	// default: reach it over "ssh -L", or give it a hostname of its own.
	var adminSrv *http.Server
	if adminHandler := buildAdmin(cfg, logger, live, access); adminHandler != nil {
		adminMux := http.NewServeMux()
		adminMux.Handle(cfg.AdminPath, adminHandler)
		adminMux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, cfg.AdminPath, http.StatusSeeOther)
		})
		adminSrv = &http.Server{
			Addr:              cfg.AdminAddr,
			Handler:           httpx.Chain(adminMux, httpx.RequestID(), httpx.Recoverer(logger), httpx.SecurityHeaders()),
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			ErrorLog:          errLog,
		}
	}

	return &Server{
		cfg:    cfg,
		logger: logger,
		health: hc,
		main: &http.Server{
			Addr:              cfg.ListenAddr,
			Handler:           mainHandler,
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			ErrorLog:          errLog,
		},
		metrics: &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           metricsMux,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ErrorLog:          errLog,
		},
		admin: adminSrv,
	}
}

// buildProxy constructs the web proxy handler and its runtime-asset handler. It
// is built when the proxy is enabled now, or when the admin panel is enabled and
// could turn it on at runtime; otherwise it returns (nil, nil) so the router
// leaves /p/ and /__bumshi__/ unmounted.
func buildProxy(cfg config.Config, logger *slog.Logger, reg *metrics.Registry, live *settings.Settings, access *admin.AccessStore) (proxyHandler, engineHandler http.Handler) {
	if !cfg.ProxyEnabled && !cfg.AdminEnabled {
		return nil, nil
	}
	proxyHandler = proxy.New(proxy.Options{
		Client:          fetch.NewClient(cfg.ProxyResponseHeaderTimeout, cfg.ProxyForceIPv4),
		Logger:          logger,
		Collectors:      proxy.NewCollectors(reg),
		RewriteMaxBytes: cfg.ProxyRewriteMaxBytes,
		InjectHTML:      webengine.Inject,
		Enabled:         live.ProxyEnabled,
		ForceIPv4:       cfg.ProxyForceIPv4,
		RequireToken:    cfg.ProxyRequireToken,
		Authorized:      access.Authorized,
		SecureCookies:   cfg.IsProduction(),
		SelfHosts:       selfHosts(cfg.PublicURL),
	})
	engineHandler = webengine.Handler()
	return proxyHandler, engineHandler
}

// buildAdmin constructs the admin panel handler when it is enabled, or returns
// nil — leaving the panel unmounted — when it cannot be started safely. A
// missing or malformed password hash is such a case: the alternative was
// printing a generated credential into the logs, where it stays forever.
func buildAdmin(cfg config.Config, logger *slog.Logger, live *settings.Settings, access *admin.AccessStore) http.Handler {
	if !cfg.AdminEnabled {
		return nil
	}
	hash := cfg.AdminPasswordHash
	if hash == "" {
		// A generated password had to be printed to be usable, which wrote a
		// working credential into journald permanently, where it long outlives
		// the session it was meant for. Refusing is the honest alternative: the
		// operator runs one command and sets a hash they control.
		logger.Error("admin panel not started: no BUMSHI_ADMIN_PASSWORD_HASH is set. " +
			"Generate one with `bumshid hash-password` and put it in /etc/bumshi/bumshi.env, then restart")
		return nil
	}
	if _, err := auth.VerifyPassword(hash, ""); err != nil {
		// Checked at startup rather than at the first login attempt, where it
		// surfaced only as "invalid credentials".
		logger.Error("admin panel not started: BUMSHI_ADMIN_PASSWORD_HASH is malformed. "+
			"Regenerate it with `bumshid hash-password`", "error", err)
		return nil
	}
	return admin.New(admin.Options{
		BasePath:     cfg.AdminPath,
		Username:     cfg.AdminUsername,
		PasswordHash: hash,
		PublicURL:    cfg.PublicURL,
		Settings:     live,
		Sessions:     auth.NewSessionStore(admin.AdminSessionTTL),
		Logins:       auth.NewRateLimiter(10, time.Minute),
		Access:       access,
		Logger:       logger,
		StartedAt:    time.Now(),
		// Caddy runs on the same host, so a forwarded header from loopback is
		// the real client's address; anything else is client-controlled and is
		// not believed.
		TrustedProxy: isLoopbackPeer,
	})
}

// isLoopbackPeer reports whether a peer address is the local machine, which is
// where a same-host reverse proxy connects from.
func isLoopbackPeer(peer string) bool {
	ip := net.ParseIP(peer)
	return ip != nil && ip.IsLoopback()
}

// settingsPath puts the live settings file beside the access roster, or returns
// "" when the roster itself is memory-only.
func settingsPath(accessStorePath string) string {
	if accessStorePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(accessStorePath), "settings.json")
}

// selfHosts extracts the hostname from the configured public URL so the proxy
// can refuse to proxy itself even when an intermediary rewrites the Host header.
func selfHosts(publicURL string) []string {
	raw := strings.TrimSpace(publicURL)
	if raw == "" {
		return nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	return []string{u.Hostname()}
}

// Run starts both listeners and blocks until ctx is canceled or a listener
// fails, then performs a graceful shutdown bounded by the configured shutdown
// timeout. It returns the first fatal listener error, if any.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Add(2)

	if s.admin != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.logger.Info("admin listener starting", "addr", s.cfg.AdminAddr, "path", s.cfg.AdminPath)
			if err := s.admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	go func() {
		defer wg.Done()
		s.logger.Info("metrics listener starting", "addr", s.cfg.MetricsAddr)
		if err := s.metrics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		defer wg.Done()
		s.logger.Info("control-plane listener starting", "addr", s.cfg.ListenAddr)
		s.health.SetReady(true)
		if err := s.main.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
		s.logger.Info("shutdown signal received")
	case runErr = <-errCh:
		s.logger.Error("listener failed", "error", runErr)
	}

	s.health.SetReady(false)
	s.shutdown()
	wg.Wait()
	return runErr
}

func (s *Server) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.main.Shutdown(ctx); err != nil {
		s.logger.Error("control-plane shutdown error", "error", err)
	}
	if err := s.metrics.Shutdown(ctx); err != nil {
		s.logger.Error("metrics shutdown error", "error", err)
	}
	if s.admin != nil {
		if err := s.admin.Shutdown(ctx); err != nil {
			s.logger.Error("admin shutdown error", "error", err)
		}
	}
}
