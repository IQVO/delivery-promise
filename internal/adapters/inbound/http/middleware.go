package http

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder remembers the status code the handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	return s.ResponseWriter.Write(b)
}

// Recoverer turns a handler panic into the internal-error problem (RFC 9457)
// instead of a dropped connection; the panic is logged by AccessLog as a 500.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity on purpose
				panic(rec)
			}
			slog.ErrorContext(r.Context(), "handler panicked", "panic", rec, "method", r.Method, "path", r.URL.Path)
			writeProblem(w, r, internalProblem, "an unexpected error occurred")
		}()
		next.ServeHTTP(w, r)
	})
}

// AccessLog logs one line per request (method, path, status, duration). Install
// it OUTSIDE Recoverer so a recovered panic is logged as the 500 it became.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			level := slog.LevelInfo
			if rec.status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			logger.Log(r.Context(), level, "http request",
				"method", r.Method, "path", r.URL.EscapedPath(), "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds())
		})
	}
}
