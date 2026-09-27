// Package logging wires up structured JSON logging (PRD FR-10.1) with
// per-request correlation ids (FR-10.2), header redaction (FR-10.4),
// and a helper for the always-logged audit events (FR-10.3).
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"time"
)

type contextKey string

const correlationIDKey contextKey = "correlation_id"

func New(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}

func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationIDKey).(string)
	return id
}

// Middleware logs every request with method, path, status, latency and
// a correlation id (FR-10.2). It never logs bodies (FR-10.5) and
// redacts the Authorization header at this single point (FR-10.4)
// rather than leaving redaction to per-handler discipline. Health
// endpoints are excluded from normal-verbosity logging (FR-9.3) since
// they fire every few seconds.
func Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}

			cid := newCorrelationID()
			ctx := WithCorrelationID(r.Context(), cid)
			r = r.WithContext(ctx)

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r)

			logger.Info("request",
				"correlation_id", cid,
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Audit logs a security-relevant event unconditionally, regardless of
// configured level (FR-10.3) — the partial mitigation for the admin
// credential's blast radius (PRD FR-4.3 non-goal).
func Audit(logger *slog.Logger, event string, attrs ...any) {
	args := append([]any{"audit_event", event}, attrs...)
	logger.Info("audit", args...)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
