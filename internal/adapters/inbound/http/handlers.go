package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
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
	res, err := s.API.ListPromisesByOrder(r.Context(), r.URL.Query().Get("orderId"))
	respond(w, r, http.StatusOK, res, err)
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

// handleCancel serves POST /promises/{promiseId}:cancel. The route has no body.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	res, err := s.API.CancelPromise(r.Context(), chi.URLParam(r, "promiseId"))
	respond(w, r, http.StatusOK, res, err)
}

func (s *Server) handleListBooks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.API.ListPromiseBooks(r.Context(), q.Get("nodeId"), q.Get("date"))
	respond(w, r, http.StatusOK, res, err)
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
