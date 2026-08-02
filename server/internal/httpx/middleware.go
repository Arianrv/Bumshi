// Package httpx provides composable HTTP middleware for the control service.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/bumshi/bumshi/server/internal/metrics"
)

const requestIDHeader = "X-Request-ID"

type ctxKey int

const requestIDKey ctxKey = iota

// Middleware wraps an http.Handler to add behavior.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so the first listed runs outermost (closest to the
// network) and the last runs innermost (closest to the handler).
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestIDFrom returns the request ID stored in ctx, or "" if none is set.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// RequestID ensures every request carries a stable identifier. A well-formed
// client-supplied X-Request-ID is reused; otherwise a new one is generated. The
// value is echoed in the response header and stored in the request context.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set(requestIDHeader, id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func validRequestID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// Recoverer converts panics into a 500 response and logs them with a stack
// trace, keeping the server alive.
func Recoverer(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					"panic", rec,
					"request_id", RequestIDFrom(r.Context()),
					"stack", string(debug.Stack()),
				)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets conservative security headers on every response.
// Transport security (HSTS) is applied by Caddy at the TLS edge.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			next.ServeHTTP(w, r)
		})
	}
}

// Metrics records request counts, in-flight gauge, and latency histogram.
func Metrics(c *metrics.HTTPCollectors) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			c.InFlight.Inc()
			rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				c.InFlight.Dec()
				c.Requests.Inc(r.Method, strconv.Itoa(rr.status))
				c.Duration.Observe(time.Since(start).Seconds(), r.Method)
			}()
			next.ServeHTTP(rr, r)
		})
	}
}

// AccessLog logs one structured line per request when enabled() reports true.
// It is DISABLED by default and must remain disabled in public releases: it is
// a development aid. The enabled func is consulted per
// request so the admin panel can toggle it live. Even when enabled it logs only
// coarse operational fields (method, path, status, size, duration) and never
// bodies or headers. A nil enabled func means "never log".
func AccessLog(logger *slog.Logger, enabled func() bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if enabled == nil || !enabled() {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rr, r)
			logger.InfoContext(r.Context(), "request",
				"request_id", RequestIDFrom(r.Context()),
				"method", r.Method,
				"path", redactPath(r.URL.Path),
				"status", rr.status,
				"bytes", rr.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// redactedPaths carry a secret in the URL and must never be written to a log.
var redactedPaths = []string{"/__bumshi__/auth"}

// redactPath replaces a secret-bearing path with a placeholder.
func redactPath(path string) string {
	for _, p := range redactedPaths {
		if path == p {
			return p + " (redacted)"
		}
	}
	return path
}

// responseRecorder captures the status code and byte count of a response while
// transparently delegating to the underlying ResponseWriter.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (rr *responseRecorder) WriteHeader(code int) {
	if rr.wrote {
		return
	}
	rr.status = code
	rr.wrote = true
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	if !rr.wrote {
		rr.WriteHeader(http.StatusOK)
	}
	n, err := rr.ResponseWriter.Write(b)
	rr.bytes += n
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController (Go 1.20+),
// preserving optional interfaces such as Flusher and Hijacker.
func (rr *responseRecorder) Unwrap() http.ResponseWriter {
	return rr.ResponseWriter
}
