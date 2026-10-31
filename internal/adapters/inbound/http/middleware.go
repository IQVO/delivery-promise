package http

import (
	"log/slog"
	"net/http"
)

// Recoverer turns a handler panic into the internal-error problem (RFC 9457)
// instead of a dropped connection; the request logger records it as a 500.
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
