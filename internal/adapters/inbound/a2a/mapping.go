package a2a

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// problemTypeBase is the namespace of the REST adapter's RFC 9457 `type` URIs
// (internal/adapters/inbound/http/errors.go): an answer's problem type is the
// same URI a REST caller of the same operation would see.
const problemTypeBase = "https://retailnetwork.example/problems/"

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business answers,
// not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func newProblem(status int, slug, title, detail string) *problem {
	return &problem{Type: problemTypeBase + slug, Title: title, Detail: detail, Status: status}
}

func problemMalformedRequest(detail string) *problem {
	return newProblem(http.StatusBadRequest, "malformed-request", "Malformed skill request", detail)
}

func problemUnknownSkill(skill string) *problem {
	return newProblem(http.StatusBadRequest, "unknown-skill", "Unknown skill",
		fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill))
}

func problemInvalidArgument(detail string) *problem {
	return newProblem(http.StatusBadRequest, "invalid-argument", "Invalid skill argument", detail)
}

// problemFromUseCase maps a use-case error to a problem answer with the SAME
// classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a READ can raise: not-found is 404,
// validation-failed is 422. Anything else is an internal failure, still
// answered as a problem (500, REST's internal-error) so the calling agent
// always receives a structured result; the cause is logged, never returned,
// so infrastructure details (DSNs, SQL) cannot reach the caller.
func problemFromUseCase(err error) *problem {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return newProblem(http.StatusNotFound, "not-found", "Not found", err.Error())
	case errors.Is(err, shared.ErrValidation):
		return newProblem(http.StatusUnprocessableEntity, "validation-failed", "Validation failed", err.Error())
	default:
		slog.Error("a2a skill failed", "error", err)
		return newProblem(http.StatusInternalServerError, "internal-error", "Internal server error",
			"the skill failed; see the server logs")
	}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill result) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string    `json:"skill"`
	Data    *skillDTO `json:"data,omitempty"`
	Problem *problem  `json:"problem,omitempty"`
}

// skillDTO is the union of the skill results; each skill fills its own field.
// The payloads are the application views the REST adapter returns, so all
// three surfaces speak the identical camelCase wire shapes of
// apis/openapi.yaml. Domain structs never cross the message boundary.
type skillDTO struct {
	PromiseStatus *promiseStatus `json:"promiseStatus,omitempty"`
	BookCapacity  *bookCapacity  `json:"bookCapacity,omitempty"`
}

// promiseStatus is the promise_status result: the promises behind an order
// (one per shipment group) or the single promise asked for.
type promiseStatus struct {
	OrderID  string          `json:"orderId,omitempty"`
	Promises []views.Promise `json:"promises"`
}

func (s promiseStatus) summary() string {
	if s.OrderID == "" {
		p := s.Promises[0]
		return fmt.Sprintf("promise %s is %s%s", p.PromiseID, p.State, judgementSuffix(p))
	}
	if len(s.Promises) == 0 {
		return fmt.Sprintf("order %s has no promise", s.OrderID)
	}
	counts := map[string]int{}
	order := []string{}
	for _, p := range s.Promises {
		if counts[p.State] == 0 {
			order = append(order, p.State)
		}
		counts[p.State]++
	}
	out := fmt.Sprintf("order %s has %d promise(s):", s.OrderID, len(s.Promises))
	for _, state := range order {
		out += fmt.Sprintf(" %d %s", counts[state], state)
	}
	return out
}

func judgementSuffix(p views.Promise) string {
	if p.Judgement == nil {
		return ""
	}
	return fmt.Sprintf(" (judged %s)", p.Judgement.Outcome)
}

// bookCapacity is the book_capacity result.
type bookCapacity struct {
	NodeID string       `json:"nodeId"`
	Date   string       `json:"date"`
	Books  []views.Book `json:"books"`
}

func (b bookCapacity) summary() string {
	if len(b.Books) == 0 {
		return fmt.Sprintf("node %s has no promise book on %s", b.NodeID, b.Date)
	}
	out := fmt.Sprintf("node %s has %d promise book(s) on %s:", b.NodeID, len(b.Books), b.Date)
	for _, bk := range b.Books {
		out += fmt.Sprintf(" %s %s share %d, provisional %d, confirmed %d;",
			bk.BookID, bk.State, bk.Capacity.Share, bk.Capacity.Provisional, bk.Capacity.Confirmed)
	}
	return out[:len(out)-1]
}
