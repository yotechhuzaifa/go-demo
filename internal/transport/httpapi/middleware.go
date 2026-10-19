package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// requestIDKey is an unexported struct type used as a context key.
//
// Using a private type prevents collisions with keys defined in other packages,
// which is the recommended context-key pattern.
type requestIDKey struct{}

// requestIDFromContext returns the request ID stored by the middleware.
func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// withRequestID assigns a traceable ID to every request and echoes it in the
// X-Request-ID response header. The same ID appears in access logs, making it
// possible to correlate a client report with a specific server log line.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reuse a valid client-provided ID for distributed tracing; otherwise
		// generate a fresh one. Validation prevents log injection and absurdly
		// long header values.
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = newRequestID()
		}

		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validRequestID accepts a short, log-safe identifier.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, char := range id {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.':
		default:
			return false
		}
	}
	return true
}

// newRequestID generates 16 random hexadecimal characters.
func newRequestID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		// crypto/rand failing is extraordinarily rare. Falling back to a
		// timestamp still provides a useful, unique-ish ID for logs.
		return "req-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(random[:])
}

// withLogging records one structured access-log line per request.
func withLogging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)

			status := recorder.status
			if status == 0 {
				// net/http defaults to 200 when a handler writes nothing.
				status = http.StatusOK
			}

			attributes := []any{
				"request_id", requestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", recorder.bytes,
				"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
				"remote_addr", r.RemoteAddr,
			}

			// Server errors deserve error-level visibility; everything else is
			// routine traffic.
			if status >= http.StatusInternalServerError {
				logger.ErrorContext(r.Context(), "http request", attributes...)
				return
			}
			logger.InfoContext(r.Context(), "http request", attributes...)
		})
	}
}

// statusRecorder captures the status code and response size for logging.
//
// Wrapping a ResponseWriter hides optional interfaces such as http.Flusher.
// Unwrap() lets http.ResponseController reach the original writer when needed.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader records the first status code. Repeated calls are ignored, just
// like the behavior of net/http's own writer.
func (r *statusRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Write records response size and ensures an implicit 200 is captured.
func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(body)
	r.bytes += written
	return written, err
}

// Written reports whether a status has been sent. The recovery middleware uses
// this to avoid writing a second response body.
func (r *statusRecorder) Written() bool {
	return r.status != 0
}

// Unwrap exposes the wrapped writer to http.ResponseController.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// withRecovery converts a panic into a logged 500 response instead of allowing
// the connection to be torn down with no useful diagnostic.
func withRecovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				// http.ErrAbortHandler is a special signal used by net/http to
				// abort a response intentionally. Re-panic so the server keeps
				// its standard behavior for that sentinel.
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}

				stack := make([]byte, 64<<10) // 64 KiB is enough for a stack trace.
				stack = stack[:runtime.Stack(stack, false)]

				logger.ErrorContext(r.Context(), "panic recovered",
					"request_id", requestIDFromContext(r.Context()),
					"panic", recovered,
					"stack", string(stack),
				)

				// If the handler already started writing, the status line is on
				// the wire and cannot be changed. Logging above is the best we
				// can do in that case.
				if recorder, ok := w.(interface{ Written() bool }); ok && recorder.Written() {
					return
				}

				writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// chain applies middleware so that the first argument is the outermost layer.
//
// Example:
//
//	chain(handler, withRequestID, withLogging, withRecovery)
//
// A request flows through request ID -> logging -> recovery -> handler, and the
// response flows back in the opposite direction.
func chain(handler http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}
