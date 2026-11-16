package http

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestWrongMethodOnTheAPIRouterCarriesAllow guards RFC 9110 section 15.5.6 on
// the main router, which registers no custom 405 handler and so relies on
// chi's default one to set Allow. Registering a custom handler there would
// silently drop the header. chi emits one Allow line per method, so the lines
// are sorted and joined, because chi lists them in map-iteration order.
func TestWrongMethodOnTheAPIRouterCarriesAllow(t *testing.T) {
	h := NewRouter(&Server{API: &fakeAPI{}}, nil, "")
	for _, c := range []struct{ method, path, want string }{
		{http.MethodGet, "/delivery-estimates", "POST"},
		{http.MethodDelete, "/healthz", "GET"},
		{http.MethodPut, "/promises", "GET, POST"},
	} {
		rec := do(h, c.method, c.path, ``)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", c.method, c.path, rec.Code)
			continue
		}
		if got := allowOf(rec.Header()); got != c.want {
			t.Errorf("%s %s: Allow = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

// allowOf is the Allow methods of h, sorted and comma-joined.
func allowOf(h http.Header) string {
	values := slices.Clone(h.Values("Allow"))
	slices.Sort(values)
	return strings.Join(values, ", ")
}
