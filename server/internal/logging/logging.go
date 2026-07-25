// Package logging builds the application's structured logger from configuration.
package logging

import (
	"io"
	"log/slog"

	"github.com/bumshi/bumshi/server/internal/config"
)

// New returns a slog.Logger that writes to w using the format and level from
// cfg. Source positions are attached only at debug level to keep production
// logs compact.
func New(w io.Writer, cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     cfg.LogLevel,
		AddSource: cfg.LogLevel <= slog.LevelDebug,
	}

	var h slog.Handler
	if cfg.LogFormat == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}
