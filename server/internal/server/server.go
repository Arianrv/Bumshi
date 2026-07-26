// Package server wires configuration, logging, metrics, health, and routing
// into a runnable control-plane service.
//
// It runs two listeners: the control plane (public routes, fronted by Caddy)
// and a separate metrics listener that must stay bound to localhost. Both are
// shut down gracefully when the run context is canceled.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
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
}

// New constructs a Server from cfg and logger. It builds the routers and HTTP
// servers but does not open any listeners; call Run for that.
func New(cfg config.Config, logger *slog.Logger) *Server {
	reg := metrics.NewRegistry()
	collectors := metrics.NewHTTPCollectors(reg)
	hc := health.New()

	live := settings.New(cfg.ProxyEnabled, cfg.AccessLog)
	proxyHandler, engineHandler := buildProxy(cfg, logger, reg, live)
	adminHandler := buildAdmin(cfg, logger, live)

	mainHandler := httpx.Chain(
		routes(hc, proxyHandler, engineHandler, adminHandler, cfg.AdminPath),
		httpx.RequestID(),          // outermost: every layer sees the ID
		httpx.Metrics(collectors),  // count even panicked requests (recovered below)
		httpx.Recoverer(logger),
		httpx.SecurityHeaders(),
		httpx.AccessLog(logger, live.AccessLog), // innermost; consulted per request
	)

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", reg.Handler())

	errLog := slog.NewLogLogger(logger.Handler(), slog.LevelError)

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
	}
}

// buildProxy constructs the web proxy handler and its runtime-asset handler. It
// is built when the proxy is enabled now, or when the admin panel is enabled and
// could turn it on at runtime; otherwise it returns (nil, nil) so the router
// leaves /p/ and /__bumshi__/ unmounted.
func buildProxy(cfg config.Config, logger *slog.Logger, reg *metrics.Registry, live *settings.Settings) (proxyHandler, engineHandler http.Handler) {
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
	})
	engineHandler = webengine.Handler()
	return proxyHandler, engineHandler
}

// buildAdmin constructs the admin panel handler when it is enabled, or returns
// nil so the router leaves the admin path unmounted. When no password hash is
// configured, a random password is generated and printed once to the logs.
func buildAdmin(cfg config.Config, logger *slog.Logger, live *settings.Settings) http.Handler {
	if !cfg.AdminEnabled {
		return nil
	}
	hash := cfg.AdminPasswordHash
	if hash == "" {
		password, generated, err := randomAdminPassword()
		if err != nil {
			logger.Error("failed to generate admin password; admin panel disabled", "error", err)
			return nil
		}
		hash = generated
		logger.Warn("no BUMSHI_ADMIN_PASSWORD_HASH set — generated a temporary admin password (shown once)",
			"username", cfg.AdminUsername, "password", password)
	}
	return admin.New(admin.Options{
		BasePath:     cfg.AdminPath,
		Username:     cfg.AdminUsername,
		PasswordHash: hash,
		Secure:       cfg.IsProduction(),
		PublicURL:    cfg.PublicURL,
		Settings:     live,
		Sessions:     auth.NewSessionStore(admin.AdminSessionTTL),
		Logins:       auth.NewRateLimiter(10, time.Minute),
		Access:       admin.NewAccessStore(),
		Logger:       logger,
		StartedAt:    time.Now(),
	})
}

func randomAdminPassword() (password, hash string, err error) {
	b := make([]byte, 12)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	password = base64.RawURLEncoding.EncodeToString(b)
	hash, err = auth.HashPassword(password)
	return password, hash, err
}

// Run starts both listeners and blocks until ctx is canceled or a listener
// fails, then performs a graceful shutdown bounded by the configured shutdown
// timeout. It returns the first fatal listener error, if any.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

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
}
