// Agentic Standard v2 (AS-3): the MCP surface is complete -- tools plus one
// scoped resource and one operator prompt (docs/adr/0013). These pin
// discovery and reads over the in-memory client, like the reference
// implementation's conformance tests.
package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The promise-books resource template is discoverable via the protocol's
// listing call, so a host can surface it without out-of-band knowledge.
func TestResourceTemplateDiscovery(t *testing.T) {
	h := newHarness(t)
	templates, err := h.session.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	for _, tpl := range templates.ResourceTemplates {
		if strings.Contains(tpl.URITemplate, "{nodeId}") && strings.Contains(tpl.URITemplate, "{date}") &&
			strings.HasPrefix(tpl.URITemplate, "promise://books/") {
			if tpl.MIMEType != "application/json" {
				t.Errorf("template MIMEType = %q, want application/json", tpl.MIMEType)
			}
			return
		}
	}
	t.Fatalf("promise-books template not discoverable: %+v", templates.ResourceTemplates)
}

// Reading one concrete URI returns the bounded JSON of that node's books on
// that date -- the same read model as the list_promise_books tool.
func TestResourceReadReturnsTheBooks(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: "promise://books/N-AUS3/2026-10-22"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(res.Contents))
	}
	c := res.Contents[0]
	if c.URI != "promise://books/N-AUS3/2026-10-22" {
		t.Errorf("uri = %q", c.URI)
	}
	if c.MIMEType != "application/json" {
		t.Errorf("mimeType = %q", c.MIMEType)
	}
	var body struct {
		NodeID string `json:"nodeId"`
		Date   string `json:"date"`
		Items  []struct {
			BookID string `json:"bookId"`
			State  string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(c.Text), &body); err != nil {
		t.Fatalf("resource text is not JSON: %v (%s)", err, c.Text)
	}
	if body.NodeID != "N-AUS3" || body.Date != "2026-10-22" {
		t.Errorf("body = %s/%s, want N-AUS3/2026-10-22", body.NodeID, body.Date)
	}
	if len(body.Items) == 0 {
		t.Fatalf("worked example has books on N-AUS3/2026-10-22, got %s", c.Text)
	}
	if body.Items[0].BookID == "" || body.Items[0].State == "" {
		t.Errorf("first book is not fully shaped: %+v", body.Items[0])
	}
}

// A URI outside the template is a protocol error, and so is a malformed
// one: the model-supplied segments are untrusted.
func TestResourceUnknownOrMalformedURIRejected(t *testing.T) {
	h := newHarness(t)
	for _, uri := range []string{
		"promise://books/N-AUS3",              // missing date
		"promise://books/N-AUS3/2026-10-22/x", // extra segment
		"promise://books//2026-10-22",         // empty nodeId
		"staffing://not-a-template",           // wrong scheme entirely
	} {
		if _, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("reading %q must be rejected", uri)
		}
	}
}

// The operator SOP prompt is listable and gettable, and its text names the
// tools but calls none.
func TestPromptListAndGet(t *testing.T) {
	h := newHarness(t)
	prompts, err := h.session.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	found := false
	for _, p := range prompts.Prompts {
		if p.Name == "check_order_promises" {
			found = true
		}
	}
	if !found {
		t.Fatalf("check_order_promises not listed: %+v", prompts.Prompts)
	}

	res, err := h.session.GetPrompt(t.Context(), &sdk.GetPromptParams{Name: "check_order_promises"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) == 0 || res.Messages[0].Content == nil {
		t.Fatal("prompt carries no message content")
	}
	tc, ok := res.Messages[0].Content.(*sdk.TextContent)
	if !ok {
		t.Fatalf("prompt content = %T, want text", res.Messages[0].Content)
	}
	for _, tool := range []string{"list_promises_by_order", "get_current_policy", "list_promise_books"} {
		if !strings.Contains(tc.Text, tool) {
			t.Errorf("the SOP does not name %s", tool)
		}
	}
	if res.Messages[0].Role != "user" {
		t.Errorf("role = %q, want user", res.Messages[0].Role)
	}
}

// Getting a prompt the server does not have is a protocol error.
func TestUnknownPromptIsRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.session.GetPrompt(t.Context(), &sdk.GetPromptParams{Name: "no_such_prompt"}); err == nil {
		t.Fatal("getting an unknown prompt must be rejected")
	}
}
