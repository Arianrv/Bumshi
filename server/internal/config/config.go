// Package config loads and validates the control service's runtime
// configuration from the environment. Every setting has a safe default so the
// binary can start with no configuration at all, and every value is validated
// before use.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
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
	// Privacy policy: user-traffic logging is OFF by default
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
	// ProxyForceIPv4 makes the proxy's upstream fetcher and WebSocket tunnel
	// dial over IPv4 only. IPv6 egress is unreliable on some networks (notably
	// from Iran), so this defaults to true; set to false for dual-stack.
	ProxyForceIPv4 bool
	// ProxyRequireToken gates the web proxy behind a valid, unexpired access
	// token (sent by the app as the bumshi_access cookie).
	//
	// With this off the proxy is an OPEN RELAY: anyone who learns the domain can
	// route their traffic through the operator's IP. It stays off by default only
	// so an existing install is not cut off mid-upgrade, and starting that way
	// now requires ProxyAllowOpenRelay to say so out loud.
	ProxyRequireToken bool
	// ProxyAllowOpenRelay acknowledges running the proxy with no access control.
	// Without it, an enabled proxy that does not require a token refuses to
	// start rather than quietly serving the whole internet.
	ProxyAllowOpenRelay bool

	// AdminEnabled mounts the deployer-only admin panel.
	AdminEnabled bool
	// AdminAddr is the panel's own listener. It is deliberately separate from
	// the control-plane listener: sharing an origin with proxied content means
	// any page a user visits through the proxy can call the panel's API with the
	// deployer's session cookie attached. Bound to localhost by default, so the
	// panel is reached over an SSH tunnel unless the operator deliberately
	// exposes it on a hostname of its own.
	AdminAddr string
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
	// AccessStorePath is the JSON file the access-user roster persists to, so
	// users survive restarts. Empty disables persistence (RAM-only).
	AccessStorePath string
}

// Load reads configuration from the environment, applies defaults, and
// validates the result. It never reads from disk and never panics.
func Load() (Config, error) {
	var errs parseErrors
	cfg := Config{
		Env:         Environment(getString("ENV", string(EnvProduction))),
		ListenAddr:  getString("LISTEN_ADDR", "127.0.0.1:8080"),
		MetricsAddr: getString("METRICS_ADDR", "127.0.0.1:9090"),
		LogFormat:   getString("LOG_FORMAT", ""),
		AccessLog:   getBool(&errs, "ACCESS_LOG", false),

		ReadTimeout:       getDuration(&errs, "READ_TIMEOUT", 15*time.Second),
		ReadHeaderTimeout: getDuration(&errs, "READ_HEADER_TIMEOUT", 10*time.Second),
		WriteTimeout:      getDuration(&errs, "WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       getDuration(&errs, "IDLE_TIMEOUT", 120*time.Second),
		ShutdownTimeout:   getDuration(&errs, "SHUTDOWN_TIMEOUT", 20*time.Second),

		ProxyEnabled:               getBool(&errs, "PROXY_ENABLED", false),
		ProxyRewriteMaxBytes:       getInt64(&errs, "PROXY_REWRITE_MAX_BYTES", 8<<20),
		ProxyResponseHeaderTimeout: getDuration(&errs, "PROXY_RESPONSE_HEADER_TIMEOUT", 30*time.Second),
		ProxyForceIPv4:             getBool(&errs, "PROXY_FORCE_IPV4", true),
		ProxyRequireToken:          getBool(&errs, "PROXY_REQUIRE_TOKEN", false),
		ProxyAllowOpenRelay:        getBool(&errs, "PROXY_ALLOW_OPEN_RELAY", false),

		AdminEnabled:      getBool(&errs, "ADMIN_ENABLED", false),
		AdminAddr:         getString("ADMIN_ADDR", "127.0.0.1:8081"),
		AdminPath:         getString("ADMIN_PATH", "/admin/"),
		AdminUsername:     getString("ADMIN_USERNAME", "admin"),
		AdminPasswordHash: getString("ADMIN_PASSWORD_HASH", ""),
		PublicURL:         getString("PUBLIC_URL", ""),
		// getStringAllowEmpty, not getString: the documented way to run
		// RAM-only is an empty value, and getString treats empty as unset and
		// reapplies the default — so that mode was unreachable.
		AccessStorePath: getStringAllowEmpty("ACCESS_STORE_PATH", "/var/lib/bumshi/access.json"),
	}

	if err := errs.err(); err != nil {
		return Config{}, err
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
		if !c.ProxyRequireToken && !c.ProxyAllowOpenRelay {
			return fmt.Errorf(
				"refusing to start an open relay: %sPROXY_ENABLED is on but %sPROXY_REQUIRE_TOKEN is off, "+
					"so anyone who learns this domain can route traffic through your IP. "+
					"Set %sPROXY_REQUIRE_TOKEN=true (create access users in the panel first), "+
					"or set %sPROXY_ALLOW_OPEN_RELAY=true if that is genuinely what you want",
				envPrefix, envPrefix, envPrefix, envPrefix)
		}
	}
	// C7: an unvalidated PublicURL produced connection links carrying
	// {"url":""}, which every client silently refuses — the operator sees a
	// link that simply never works, with nothing to explain why.
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("%sPUBLIC_URL %q must be an absolute URL, e.g. https://proxy.example.com", envPrefix, c.PublicURL)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("%sPUBLIC_URL %q must use http or https", envPrefix, c.PublicURL)
		}
	}
	if c.AdminEnabled && c.PublicURL == "" {
		return fmt.Errorf("%sPUBLIC_URL must be set when the admin panel is enabled: it is what connection links point at", envPrefix)
	}
	if c.AdminEnabled {
		if !strings.HasPrefix(c.AdminPath, "/") || !strings.HasSuffix(c.AdminPath, "/") {
			return fmt.Errorf("%sADMIN_PATH must start and end with '/'", envPrefix)
		}
		if c.AdminUsername == "" {
			return fmt.Errorf("%sADMIN_USERNAME must not be empty", envPrefix)
		}
		if c.AdminAddr == "" {
			return fmt.Errorf("%sADMIN_ADDR must not be empty", envPrefix)
		}
		// Sharing a listener with proxied content would put the panel on the same
		// origin as every site a user visits, which is what lets a hostile page
		// call its API with the deployer's session attached.
		if c.AdminAddr == c.ListenAddr {
			return fmt.Errorf("%sADMIN_ADDR must differ from %sLISTEN_ADDR (both %q): the panel must not share an origin with proxied content", envPrefix, envPrefix, c.AdminAddr)
		}
		if c.AdminAddr == c.MetricsAddr {
			return fmt.Errorf("%sADMIN_ADDR and %sMETRICS_ADDR must differ (both %q)", envPrefix, envPrefix, c.AdminAddr)
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

// Parse errors are collected rather than swallowed. Silently falling back to
// the default meant a typo disabled a security control without a word:
// BUMSHI_PROXY_REQUIRE_TOKEN=ture read as false and left the proxy open to
// anyone who knew the domain. A misconfigured service now refuses to start.
type parseErrors []string

func (e *parseErrors) add(key, value, want string) {
	*e = append(*e, fmt.Sprintf("%s%s=%q is not a valid %s", envPrefix, key, value, want))
}

func (e parseErrors) err() error {
	if len(e) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(e, "\n  - "))
}

// getStringAllowEmpty is getString except that an explicitly empty value is
// honoured rather than treated as unset.
func getStringAllowEmpty(key, def string) string {
	if v, ok := os.LookupEnv(envPrefix + key); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func getBool(errs *parseErrors, key string, def bool) bool {
	v, ok := os.LookupEnv(envPrefix + key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		errs.add(key, v, "boolean (true/false)")
		return def
	}
	return b
}

func getInt64(errs *parseErrors, key string, def int64) int64 {
	v, ok := os.LookupEnv(envPrefix + key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		errs.add(key, v, "integer")
		return def
	}
	return n
}

func getDuration(errs *parseErrors, key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(envPrefix + key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		errs.add(key, v, "duration (e.g. 30s, 2m)")
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
