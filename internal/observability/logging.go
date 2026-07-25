package observability

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// RequestLogging is HTTP middleware that assigns (or propagates, via X-Request-Id) a
// correlation id for the request, stashes it in context via WithCorrelationID, echoes it
// back in the response header, and logs one structured line per request on completion.
// Mounted first (outermost) in both command-api and query-api's middleware chain, ahead of
// auth/schema validation, so every request — including rejected ones — gets logged with a
// correlation id.
func RequestLogging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				id = uuid.NewString()
			}
			ctx := WithCorrelationID(r.Context(), id)
			w.Header().Set("X-Request-Id", id)

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r.WithContext(ctx))

			logger.Info("http_request",
				"correlation_id", id,
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}
