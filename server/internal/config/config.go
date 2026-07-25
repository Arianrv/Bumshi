// Package config loads and validates the control service's runtime
// configuration from the environment. Every setting has a safe default so the
// binary can start with no configuration at all, and every value is validated
// before use.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// envPrefix namespaces all environment variables read by this service.
const envPrefix = "BUMSHI_"

// Environment identifies the deployment mode.
type Environment string

const (
	// EnvDevelopment enables developer-friendly defaults (text logs, etc.).
	EnvDevelopment Environment = "development"
	// EnvProduction is the default, hardened mode.
	EnvProduction Environment = "production"
)

// Config holds all runtime configuration for the control service. Each field
// maps to a BUMSHI_* environment variable documented in Load.
type Config struct {
	Env         Environment
	ListenAddr  string // control-plane listener; keep bound to localhost behind Caddy
	MetricsAddr string // Prometheus metrics listener; never expose publicly

	LogLevel  slog.Level
	LogFormat string // "json" or "text"

	// AccessLog controls per-request access logging.
	//
	// Privacy policy (see DESIGN §8.4): user-traffic logging is OFF by default
	// and must remain off in public releases. It exists only as a development
	// aid and is gated behind this single flag.
	AccessLog bool

	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration

	// ProxyEnabled mounts the web proxy engine under /p/. It is off by default
	// so the control plane can run on its own during early rollout.
	ProxyEnabled bool
	// ProxyRewriteMaxBytes caps the size of HTML/CSS bodies that are buffered
	// for URL rewriting; larger text bodies are truncated at this limit.
	ProxyRewriteMaxBytes int64
	// ProxyResponseHeaderTimeout bounds how long the proxy waits for an
	// upstream's response headers (the body may then stream indefinitely).
	ProxyResponseHeaderTimeout time.Duration

	// AdminEnabled mounts the deployer-only admin panel.
	AdminEnabled bool
	// AdminPath is the panel's base path (normalized to start and end with '/').
	AdminPath string
	// AdminUsername is the admin login name.
	AdminUsername string
	// AdminPasswordHash is a PBKDF2 hash produced by `bumshid hash-password`.
	// When empty and the panel is enabled, a random password is generated at
	// startup and printed once to the logs.
	AdminPasswordHash string
	// PublicURL is the base URL end users connect to (used in connection links).
	PublicURL string
}

// Load reads configuration from the environment, applies defaults, and
// validates the result. It never reads from disk and never panics.
func Load() (Config, error) {
	cfg := Config{
		Env:         Environment(getString("ENV", string(EnvProduction))),
		ListenAddr:  getString("LISTEN_ADDR", "127.0.0.1:8080"),
		MetricsAddr: getString("METRICS_ADDR", "127.0.0.1:9090"),
		LogFormat:   getString("LOG_FORMAT", ""),
		AccessLog:   getBool("ACCESS_LOG", false),

		ReadTimeout:       getDuration("READ_TIMEOUT", 15*time.Second),
		ReadHeaderTimeout: getDuration("READ_HEADER_TIMEOUT", 10*time.Second),
		WriteTimeout:      getDuration("WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       getDuration("IDLE_TIMEOUT", 120*time.Second),
		ShutdownTimeout:   getDuration("SHUTDOWN_TIMEOUT", 20*time.Second),

		ProxyEnabled:               getBool("PROXY_ENABLED", false),
		ProxyRewriteMaxBytes:       getInt64("PROXY_REWRITE_MAX_BYTES", 8<<20),
		ProxyResponseHeaderTimeout: getDuration("PROXY_RESPONSE_HEADER_TIMEOUT", 30*time.Second),

		AdminEnabled:      getBool("ADMIN_ENABLED", false),
		AdminPath:         getString("ADMIN_PATH", "/admin/"),
		AdminUsername:     getString("ADMIN_USERNAME", "admin"),
		AdminPasswordHash: getString("ADMIN_PASSWORD_HASH", ""),
		PublicURL:         getString("PUBLIC_URL", ""),
	}

	cfg.AdminPath = normalizePath(cfg.AdminPath)

	level, err := parseLevel(getString("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level

	// The default log format depends on the environment: human-friendly text
	// while developing, structured JSON in production.
	if cfg.LogFormat == "" {
		if cfg.Env == EnvDevelopment {
			cfg.LogFormat = "text"
		} else {
			cfg.LogFormat = "json"
		}
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// IsProduction reports whether the service runs in production mode.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }

func (c Config) validate() error {
	switch c.Env {
	case EnvDevelopment, EnvProduction:
	default:
		return fmt.Errorf("invalid %sENV %q: want %q or %q", envPrefix, c.Env, EnvDevelopment, EnvProduction)
	}
	if c.ListenAddr == "" {
		return fmt.Errorf("%sLISTEN_ADDR must not be empty", envPrefix)
	}
	if c.MetricsAddr == "" {
		return fmt.Errorf("%sMETRICS_ADDR must not be empty", envPrefix)
	}
	if c.MetricsAddr == c.ListenAddr {
		return fmt.Errorf("%sMETRICS_ADDR and %sLISTEN_ADDR must differ (both %q)", envPrefix, envPrefix, c.ListenAddr)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("invalid %sLOG_FORMAT %q: want \"json\" or \"text\"", envPrefix, c.LogFormat)
	}
	if c.ReadTimeout < 0 || c.ReadHeaderTimeout < 0 || c.WriteTimeout < 0 || c.IdleTimeout < 0 {
		return fmt.Errorf("timeouts must not be negative")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("%sSHUTDOWN_TIMEOUT must be positive", envPrefix)
	}
	if c.ProxyEnabled {
		if c.ProxyRewriteMaxBytes <= 0 {
			return fmt.Errorf("%sPROXY_REWRITE_MAX_BYTES must be positive", envPrefix)
		}
		if c.ProxyResponseHeaderTimeout <= 0 {
			return fmt.Errorf("%sPROXY_RESPONSE_HEADER_TIMEOUT must be positive", envPrefix)
		}
	}
	if c.AdminEnabled {
		if !strings.HasPrefix(c.AdminPath, "/") || !strings.HasSuffix(c.AdminPath, "/") {
			return fmt.Errorf("%sADMIN_PATH must start and end with '/'", envPrefix)
		}
		if c.AdminUsername == "" {
			return fmt.Errorf("%sADMIN_USERNAME must not be empty", envPrefix)
		}
	}
	return nil
}

// normalizePath ensures a URL path base starts and ends with a single '/'.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/admin/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func getString(key, def string) string {
	if v, ok := os.LookupEnv(envPrefix + key); ok {
		if tv := strings.TrimSpace(v); tv != "" {
			return tv
		}
	}
	return def
}

func getBool(key string, def bool) bool {
	v, ok := os.LookupEnv(envPrefix + key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func getInt64(key string, def int64) int64 {
	v, ok := os.LookupEnv(envPrefix + key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return def
	}
	return n
}

func getDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(envPrefix + key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return d
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid %sLOG_LEVEL %q", envPrefix, s)
	}
}
