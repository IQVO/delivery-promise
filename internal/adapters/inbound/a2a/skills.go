package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/delivery-promise/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card).
const (
	skillPromiseStatus = "promise_status"
	skillBookCapacity  = "book_capacity"
)

// skillRequest is the wire contract of every skill call: the first message
// part carries (as a Data part, or a Text part holding the same JSON) the
// skill id and its arguments. Strict on purpose -- an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored: a
// misspelled name like "ordrId" must not silently become an empty argument.
// (encoding/json matches field names case-insensitively, so "orderid" still
// reaches orderId; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// promiseStatusArgs and bookCapacityArgs are the per-skill argument shapes.
type promiseStatusArgs struct {
	OrderID   string `json:"orderId"`
	PromiseID string `json:"promiseId"`
}

type bookCapacityArgs struct {
	NodeID string `json:"nodeId"`
	Date   string `json:"date"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: a message-only agent, so a skill run
// is exactly one request mapped to one answer (docs/adr/0010).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, prob := parseSkillRequest(ec)
	if prob != nil {
		return reply(ec, envelope{Problem: prob}, prob.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, text, prob := run(ctx, deps, req)
	if prob != nil {
		span.SetStatus(codes.Error, prob.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: prob}, prob.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, text)
}

// run dispatches one validated request to its skill and returns the result
// with its one-line summary.
func run(ctx context.Context, deps Deps, req skillRequest) (*skillDTO, string, *problem) {
	switch req.Skill {
	case skillPromiseStatus:
		data, prob := deps.promiseStatus(ctx, req.Arguments)
		if prob != nil {
			return nil, "", prob
		}
		return &skillDTO{PromiseStatus: &data}, data.summary(), nil
	case skillBookCapacity:
		data, prob := deps.bookCapacity(ctx, req.Arguments)
		if prob != nil {
			return nil, "", prob
		}
		return &skillDTO{BookCapacity: &data}, data.summary(), nil
	default:
		return nil, "", problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part. A
// Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered -- not raised -- because
// a malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// promiseStatus is the promise-by-order-or-id read skill: exactly one of
// orderId and promiseId selects the use case (ListPromisesByOrder or
// GetPromise), both unchanged.
func (d Deps) promiseStatus(ctx context.Context, rawArgs json.RawMessage) (promiseStatus, *problem) {
	var args promiseStatusArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return promiseStatus{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	hasOrder, hasPromise := strings.TrimSpace(args.OrderID) != "", strings.TrimSpace(args.PromiseID) != ""
	switch {
	case hasOrder == hasPromise:
		return promiseStatus{}, problemInvalidArgument("exactly one of orderId and promiseId is required")
	case hasOrder:
		list, err := d.Reads.ListPromisesByOrder(ctx, args.OrderID)
		if err != nil {
			return promiseStatus{}, problemFromUseCase(err)
		}
		return promiseStatus{OrderID: args.OrderID, Promises: nonNil(list.Items)}, nil
	default:
		p, err := d.Reads.GetPromise(ctx, args.PromiseID)
		if err != nil {
			return promiseStatus{}, problemFromUseCase(err)
		}
		return promiseStatus{Promises: []views.Promise{p}}, nil
	}
}

// bookCapacity is the node-books-on-a-date read skill (ListPromiseBooks).
func (d Deps) bookCapacity(ctx context.Context, rawArgs json.RawMessage) (bookCapacity, *problem) {
	var args bookCapacityArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return bookCapacity{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	if strings.TrimSpace(args.NodeID) == "" || strings.TrimSpace(args.Date) == "" {
		return bookCapacity{}, problemInvalidArgument("nodeId and date are required")
	}
	list, err := d.Reads.ListPromiseBooks(ctx, args.NodeID, args.Date)
	if err != nil {
		return bookCapacity{}, problemFromUseCase(err)
	}
	items := list.Items
	if items == nil {
		items = []views.Book{}
	}
	return bookCapacity{NodeID: args.NodeID, Date: args.Date, Books: items}, nil
}

func nonNil(items []views.Promise) []views.Promise {
	if items == nil {
		return []views.Promise{}
	}
	return items
}
