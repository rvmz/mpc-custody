// Package observability provides metrics, tracing, and request instrumentation.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

type traceContextKey struct{}

// Trace stores the trace and span identifiers attached to a request.
type Trace struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

// WithTrace adds trace metadata to a context.
func WithTrace(ctx context.Context, trace Trace) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceFromContext returns trace metadata from a context.
func TraceFromContext(ctx context.Context) Trace {
	trace, ok := ctx.Value(traceContextKey{}).(Trace)
	if !ok {
		return Trace{}
	}
	return trace
}

// Middleware records request metrics and attaches simple W3C-compatible trace identifiers.
func Middleware(metrics *Metrics, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			trace := traceFromHeader(r.Header.Get("traceparent"))
			if trace.TraceID == "" {
				trace = Trace{TraceID: randomHex(16), SpanID: randomHex(8)}
			}

			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			w.Header().Set("traceparent", "00-"+trace.TraceID+"-"+trace.SpanID+"-01")

			next.ServeHTTP(recorder, r.WithContext(WithTrace(r.Context(), trace)))

			labels := map[string]string{
				"method": r.Method,
				"path":   routeLabel(r.URL.Path),
				"status": http.StatusText(recorder.status),
			}
			metrics.Inc("custody_http_requests_total", labels)
			metrics.ObserveDuration("custody_http_request_duration_seconds", labels, time.Since(start))

			logger.Info("request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"trace_id", trace.TraceID,
				"span_id", trace.SpanID,
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func traceFromHeader(header string) Trace {
	if len(header) != 55 {
		return Trace{}
	}
	return Trace{
		TraceID: header[3:35],
		SpanID:  header[36:52],
	}
}

func randomHex(size int) string {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(raw)
}

func routeLabel(path string) string {
	switch {
	case path == "/healthz" || path == "/readyz" || path == "/metrics":
		return path
	case path == "/v1/wallets":
		return "/v1/wallets"
	case path == "/v1/transactions":
		return "/v1/transactions"
	default:
		return "/v1/transactions/{id}"
	}
}
