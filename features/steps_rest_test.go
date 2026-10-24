package features_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// The REST steps send real HTTP requests to the router the shipped binary
// serves (apis/openapi.yaml), over the same in-memory doubles the use-case
// steps use, and assert on the decoded JSON answer. Bodies may reference the
// scenario's own ids through {offerId:<TIER>} and {promiseId} placeholders, so
// one generic step serves every operation.
func registerRestSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a (GET|POST) request is sent to "([^"]*)"$`, w.restCall)
	sc.Step(`^a (GET|POST) request with the JSON body is sent to "([^"]*)":$`, w.restCallBody)
	sc.Step(`^the response status is (\d+)$`, w.restStatus)
	sc.Step(`^the response problem is "([a-z-]+)"$`, w.restProblem)
	sc.Step(`^the response field "([^"]*)" is "([^"]*)"$`, w.restFieldIs)
	sc.Step(`^the response field "([^"]*)" is (-?\d+)$`, w.restFieldIsInt)
	sc.Step(`^the response field "([^"]*)" is absent$`, w.restFieldAbsent)
	sc.Step(`^the response list "([^"]*)" has (\d+) items?$`, w.restListLen)
	sc.Step(`^the promise of the response becomes the scenario's promise$`, w.rememberPromise)
}

// expand substitutes the scenario's ids into a request body.
func (w *world) expand(body string) (string, error) {
	for _, tier := range []string{"NEXT_DAY", "TWO_DAY", "SAME_DAY", "STANDARD"} {
		o, err := w.offer(tier)
		if err == nil {
			body = strings.ReplaceAll(body, "{offerId:"+tier+"}", o.OfferID)
		}
	}
	if w.promiseID != "" {
		body = strings.ReplaceAll(body, "{promiseId}", w.promiseID)
	}
	if strings.Contains(body, "{offerId:") || strings.Contains(body, "{promiseId}") {
		return "", fmt.Errorf("the body references an id the scenario has not made: %s", body)
	}
	return body, nil
}

func (w *world) restCall(method, path string) error {
	return w.send(method, w.expandPath(path), "")
}

func (w *world) restCallBody(method, path, docstring string) error {
	body, err := w.expand(docstring)
	if err != nil {
		return err
	}
	return w.send(method, w.expandPath(path), body)
}

// expandPath substitutes the scenario's ids into a request path.
func (w *world) expandPath(path string) string {
	if w.promiseID != "" {
		return strings.ReplaceAll(path, "{promiseId}", w.promiseID)
	}
	return path
}

func (w *world) send(method, path, body string) error {
	// Snapshot the outbox like run() does, so the published-event steps work
	// for a REST call too.
	w.outboxLen = len(w.store.Outbox().Events())
	w.counts = w.store.Counts()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	w.lastRes = rec
	return nil
}

// rememberPromise adopts the promiseId of the last answer, so later steps and
// bodies can reference it as {promiseId}.
func (w *world) rememberPromise() error {
	doc, err := w.decoded()
	if err != nil {
		return err
	}
	id, ok := jsonAt(doc, "promiseId")
	s, isStr := id.(string)
	if !ok || !isStr || s == "" {
		return fmt.Errorf("the answer has no promiseId (%s)", w.lastRes.Body)
	}
	w.promiseID = s
	return nil
}

func (w *world) restStatus(want int) error {
	if w.lastRes == nil {
		return errNoCommand
	}
	if w.lastRes.Code != want {
		return fmt.Errorf("the response status is %d, want %d (%s)", w.lastRes.Code, want, w.lastRes.Body)
	}
	return nil
}

// restProblem asserts the RFC 9457 type of the answer ends in the slug the
// openapi names for the error.
func (w *world) restProblem(slug string) error {
	if w.lastRes == nil {
		return errNoCommand
	}
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.lastRes.Body.Bytes(), &p); err != nil {
		return fmt.Errorf("the answer is not a problem: %w (%s)", err, w.lastRes.Body)
	}
	if !strings.HasSuffix(p.Type, "/"+slug) {
		return fmt.Errorf("the problem type is %s, want slug %s (%s)", p.Type, slug, w.lastRes.Body)
	}
	return nil
}

// at walks a dotted path (offers.0.tier) into decoded JSON.
func jsonAt(doc any, path string) (any, bool) {
	cur := doc
	if path == "" {
		return cur, true
	}
	for _, seg := range strings.Split(path, ".") {
		if idx, err := strconv.Atoi(seg); err == nil {
			list, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(list) {
				return nil, false
			}
			cur = list[idx]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, present := obj[seg]
		if !present {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func (w *world) decoded() (any, error) {
	if w.lastRes == nil {
		return nil, errNoCommand
	}
	var doc any
	if err := json.Unmarshal(w.lastRes.Body.Bytes(), &doc); err != nil {
		return nil, fmt.Errorf("the answer is not JSON: %w (%s)", err, w.lastRes.Body)
	}
	return doc, nil
}

func (w *world) restFieldIs(path, want string) error {
	doc, err := w.decoded()
	if err != nil {
		return err
	}
	if w.promiseID != "" {
		want = strings.ReplaceAll(want, "{promiseId}", w.promiseID)
	}
	got, ok := jsonAt(doc, path)
	if !ok {
		return fmt.Errorf("the answer has no field %q (%s)", path, w.lastRes.Body)
	}
	if fmt.Sprint(got) != want {
		return fmt.Errorf("the field %q is %v, want %q", path, got, want)
	}
	return nil
}

func (w *world) restFieldIsInt(path string, want int) error {
	return w.restFieldIs(path, strconv.Itoa(want))
}

func (w *world) restFieldAbsent(path string) error {
	doc, err := w.decoded()
	if err != nil {
		return err
	}
	if got, ok := jsonAt(doc, path); ok && got != nil {
		return fmt.Errorf("the field %q is %v, want it absent", path, got)
	}
	return nil
}

func (w *world) restListLen(path string, want int) error {
	doc, err := w.decoded()
	if err != nil {
		return err
	}
	got, ok := jsonAt(doc, path)
	if !ok {
		return fmt.Errorf("the answer has no list %q (%s)", path, w.lastRes.Body)
	}
	list, ok := got.([]any)
	if !ok {
		return fmt.Errorf("the field %q is %v, want a list", path, got)
	}
	if len(list) != want {
		return fmt.Errorf("the list %q has %d items, want %d", path, len(list), want)
	}
	return nil
}
