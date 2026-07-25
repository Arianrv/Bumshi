package config

import (
	"log/slog"
	"testing"
	"time"
)

// knownKeys are cleared before each test so the host environment cannot leak
// into the result.
var knownKeys = []string{
	"ENV", "LISTEN_ADDR", "METRICS_ADDR", "LOG_LEVEL", "LOG_FORMAT", "ACCESS_LOG",
	"READ_TIMEOUT", "READ_HEADER_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT", "SHUTDOWN_TIMEOUT",
	"PROXY_ENABLED", "PROXY_REWRITE_MAX_BYTES", "PROXY_RESPONSE_HEADER_TIMEOUT",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range knownKeys {
		t.Setenv(envPrefix+k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != EnvProduction {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvProduction)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("MetricsAddr = %q", cfg.MetricsAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
	if cfg.AccessLog {
		t.Error("AccessLog must default to false")
	}
	if cfg.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %v", cfg.ReadTimeout)
	}
	if !cfg.IsProduction() {
		t.Error("IsProduction should be true by default")
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUMSHI_ENV", "development")
	t.Setenv("BUMSHI_LOG_LEVEL", "debug")
	t.Setenv("BUMSHI_ACCESS_LOG", "true")
	t.Setenv("BUMSHI_READ_TIMEOUT", "5s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q", cfg.Env)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want text (development default)", cfg.LogFormat)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if !cfg.AccessLog {
		t.Error("AccessLog should be true")
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("ReadTimeout = %v, want 5s", cfg.ReadTimeout)
	}
}

func TestLoadInvalidEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUMSHI_ENV", "staging")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid ENV")
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUMSHI_LOG_LEVEL", "verbose")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid LOG_LEVEL")
	}
}

func TestLoadMetricsEqualsListen(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUMSHI_LISTEN_ADDR", "127.0.0.1:9000")
	t.Setenv("BUMSHI_METRICS_ADDR", "127.0.0.1:9000")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when metrics and listen addresses match")
	}
}

func TestProxyDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProxyEnabled {
		t.Error("ProxyEnabled must default to false")
	}
	if cfg.ProxyRewriteMaxBytes != 8<<20 {
		t.Errorf("ProxyRewriteMaxBytes = %d, want 8MiB", cfg.ProxyRewriteMaxBytes)
	}
}

func TestProxyEnabledValidation(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUMSHI_PROXY_ENABLED", "true")
	t.Setenv("BUMSHI_PROXY_REWRITE_MAX_BYTES", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-positive PROXY_REWRITE_MAX_BYTES when proxy enabled")
	}
}

func TestLoadBadDurationFallsBackToDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUMSHI_WRITE_TIMEOUT", "not-a-duration")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WriteTimeout != 30*time.Second {
		t.Errorf("WriteTimeout = %v, want default 30s", cfg.WriteTimeout)
	}
}
