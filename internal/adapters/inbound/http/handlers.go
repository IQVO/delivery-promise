package http

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/http/page"
	"github.com/claudioed/delivery-promise/internal/application/views"
)

func (s *Server) handleQuote(w http.ResponseWriter, r *http.Request) {
	var req quoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.API.QuoteDeliveryOffers(r.Context(), req.command())
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) handleGetOffer(w http.ResponseWriter, r *http.Request) {
	res, err := s.API.GetOffer(r.Context(), chi.URLParam(r, "offerId"))
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) handleMakePromise(w http.ResponseWriter, r *http.Request) {
	var req makePromiseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.API.MakePromise(r.Context(), req.command())
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK // a replay of an earlier request
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, res.Promise)
}

func (s *Server) handleListPromises(w http.ResponseWriter, r *http.Request) {
	limit, after, err := pageParams(r, "orderId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.API.ListPromisesByOrder(r.Context(), r.URL.Query().Get("orderId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	// One order has a handful of promises (one per shipment group): the use case reads them
	// all, ordered by groupNo, and the keyset (groupNo, promiseId) slices the page.
	writeJSON(w, http.StatusOK, paginate(res.Items, limit, after, func(p views.Promise) page.Position {
		return page.Position{Sort: fmt.Sprintf("%010d", p.GroupNo), ID: p.PromiseID}
	}))
}

func (s *Server) handleGetPromise(w http.ResponseWriter, r *http.Request) {
	res, err := s.API.GetPromise(r.Context(), chi.URLParam(r, "promiseId"))
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	var req confirmRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.API.ConfirmPromise(r.Context(), chi.URLParam(r, "promiseId"), req.OrderID, req.GroupNo)
	respond(w, r, http.StatusOK, res, err)
}

// handleCancel serves POST /promises/{promiseId}/cancellations. The route has no body.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	res, err := s.API.CancelPromise(r.Context(), chi.URLParam(r, "promiseId"))
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) handleListBooks(w http.ResponseWriter, r *http.Request) {
	limit, after, err := pageParams(r, "nodeId", "date")
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	res, err := s.API.ListPromiseBooks(r.Context(), q.Get("nodeId"), q.Get("date"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	// One node-day has a few books (one per pickup cutoff): keyset (cutoffAt, bookId).
	writeJSON(w, http.StatusOK, paginate(res.Items, limit, after, func(b views.Book) page.Position {
		return page.Position{Sort: b.CutoffAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), ID: b.BookID}
	}))
}

func (s *Server) handlePublishPolicy(w http.ResponseWriter, r *http.Request) {
	var req publishPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	in, err := req.input()
	if err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.API.PublishPromisePolicy(r.Context(), in)
	respond(w, r, http.StatusCreated, res, err)
}

func (s *Server) handleCurrentPolicy(w http.ResponseWriter, r *http.Request) {
	res, err := s.API.CurrentPolicy(r.Context())
	respond(w, r, http.StatusOK, res, err)
}

// respond writes v with status, or the problem of err.
func respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

// pageParams validates the query of a collection GET: only the declared filters plus limit
// and cursor are accepted, limit is 1-500 and the cursor decodes.
func pageParams(r *http.Request, filters ...string) (int, *page.Position, error) {
	if name := page.Unknown(r, filters...); name != "" {
		return 0, nil, fmt.Errorf("%w: %q", errUnknownQuery, name)
	}
	limit, err := page.Limit(r)
	if err != nil {
		return 0, nil, err
	}
	after, err := page.After(r)
	if err != nil {
		return 0, nil, err
	}
	return limit, after, nil
}

// paginate orders rows by their keyset (sort key, id), drops everything up to and including
// the cursor position and returns one page. The rows are already bounded by the mandatory
// parent filter of the collection, so the keyset is applied over that bounded set.
func paginate[T any](rows []T, limit int, after *page.Position, at func(T) page.Position) page.Page[T] {
	sorted := append([]T(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return before(at(sorted[i]), at(sorted[j])) })
	if after != nil {
		i := sort.Search(len(sorted), func(i int) bool { return before(*after, at(sorted[i])) })
		sorted = sorted[i:]
	}
	if len(sorted) > page.FetchSize(limit) {
		sorted = sorted[:page.FetchSize(limit)]
	}
	return page.Of(sorted, limit, at)
}

func before(a, b page.Position) bool {
	if c := strings.Compare(a.Sort, b.Sort); c != 0 {
		return c < 0
	}
	return a.ID < b.ID
}
