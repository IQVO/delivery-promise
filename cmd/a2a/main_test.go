package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/claudioed/delivery-promise/internal/adapters/inbound/a2a"
	"github.com/claudioed/delivery-promise/internal/application/views"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const testAgentURL = "http://delivery-promise-a2a.warehouse-systems.svc.cluster.local:8093/"

// stubReads answers a fixed promise and nothing else: the transport tests
// never need a database.
type stubReads struct{}

func (stubReads) GetPromise(_ context.Context, id string) (views.Promise, error) {
	return views.Promise{PromiseID: id, State: "CONFIRMED"}, nil
}

func (stubReads) ListPromisesByOrder(context.Context, string) (views.PromiseList, error) {
	return views.PromiseList{}, nil
}

func (stubReads) ListPromiseBooks(context.Context, string, string) (views.BookList, error) {
	return views.BookList{}, nil
}

func testCard() *a2a.AgentCard {
	return inbounda2a.Card(inbounda2a.Config{AgentURL: testAgentURL, Version: "test"})
}

// testRouter is the real router over the real A2A handler on a stub Reads.
func testRouter(logger *slog.Logger) http.Handler {
	deps := inbounda2a.Deps{Reads: stubReads{}}
	return newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), testCard(), logger, "delivery-promise-a2a")
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(testRouter(quietLogger()))
	t.Cleanup(srv.Close)
	return srv
}

func TestRouter_HealthzIsOpenAndServesOK(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
		t.Fatalf(`GET /healthz = %d %q, want 200 {"status":"ok"}`, resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

// The card is served at the well-known path, carries the configured URL and
// declares no security scheme.
func TestRouter_ServesTheAgentCard(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET agent card = %d, want 200", resp.StatusCode)
	}
	var card a2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatalf("card is not JSON: %v", err)
	}
	if card.Name != "delivery-promise" || len(card.Skills) != 2 || card.SecuritySchemes != nil {
		t.Fatalf("card = %q, %d skills, schemes %v", card.Name, len(card.Skills), card.SecuritySchemes)
	}
	if len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].URL != testAgentURL {
		t.Fatalf("interfaces = %+v, want the configured agent URL", card.SupportedInterfaces)
	}
}

// Both mount points answer a real SendMessage over the SDK client, with no
// credentials, and a real client lists the two read skills from the card.
func TestJSONRPC_RootAndA2APathsAnswerSkills(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/", "/a2a"} {
		t.Run(path, func(t *testing.T) {
			card := inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + path, Version: "t"})
			client, err := a2aclient.NewFromCard(context.Background(), card)
			if err != nil {
				t.Fatalf("NewFromCard: %v", err)
			}
			msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{
				"skill": "promise_status", "arguments": map[string]any{"promiseId": "prm-1"},
			}))
			resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
			if err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
			reply, ok := resp.(*a2a.Message)
			if !ok || len(reply.Parts) != 2 {
				t.Fatalf("reply = %T %+v, want a message with a data and a text part", resp, resp)
			}
			if text, _ := reply.Parts[1].Content.(a2a.Text); !strings.Contains(string(text), "prm-1 is CONFIRMED") {
				t.Fatalf("summary = %q", text)
			}
		})
	}
}

// The binary reads no credential: an Authorization header changes nothing.
func TestNoAuthIsRequired(t *testing.T) {
	srv := newTestServer(t)
	for _, auth := range []string{"", "Bearer nonsense"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/.well-known/agent-card.json", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("agent card with Authorization=%q = %d, want 200", auth, resp.StatusCode)
		}
	}
}

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback.
func TestBuildDeps_MissingDatabaseFailsBoot(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want an error naming DATABASE_URL", err)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

// Every mandatory variable is refused at boot, naming itself, before anything
// opens.
func TestValidateBootConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://u@h/db", "A2A_AGENT_URL": testAgentURL}
	with := func(k, v string) func(string) string {
		env := map[string]string{}
		for key, val := range base {
			env[key] = val
		}
		env[k] = v
		return func(name string) string { return env[name] }
	}
	cases := []struct {
		name string
		env  func(string) string
		want string // substring of the error, "" = accepted
	}{
		{"valid", with("A2A_ADDR", ""), ""},
		{"valid https", with("A2A_AGENT_URL", "https://gw.example/api/a2a/delivery-promise/"), ""},
		{"no database", with("DATABASE_URL", ""), "DATABASE_URL is required"},
		{"no agent url", with("A2A_AGENT_URL", ""), "A2A_AGENT_URL is required"},
		{"relative agent url", with("A2A_AGENT_URL", "/a2a"), "A2A_AGENT_URL is required"},
		{"host without scheme", with("A2A_AGENT_URL", "delivery-promise-a2a:8093"), "A2A_AGENT_URL is required"},
		{"non-http scheme", with("A2A_AGENT_URL", "ftp://h/"), "A2A_AGENT_URL is required"},
		{"scheme without host", with("A2A_AGENT_URL", "http://"), "A2A_AGENT_URL is required"},
		{"malformed", with("A2A_AGENT_URL", "http://[::1"), "A2A_AGENT_URL is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateBootConfig(c.env)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("validateBootConfig = %v, want accepted", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("validateBootConfig = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// run refuses to boot BEFORE it opens telemetry, the database or the
// listener when the agent URL is missing (the database URL is deliberately
// unreachable: reaching it would hang the retries, not return this error).
func TestRun_RefusesWithoutAgentURLBeforeOpeningAnything(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u@127.0.0.1:1/db")
	t.Setenv("A2A_AGENT_URL", "")
	t.Setenv("A2A_ADDR", "127.0.0.1:0")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "A2A_AGENT_URL is required") {
		t.Fatalf("run() = %v, want the A2A_AGENT_URL boot error", err)
	}
	if errors.Is(err, errNoDatabase) {
		t.Fatalf("run() reported the database: %v", err)
	}
}

// *usecases.Service (what buildService returns) is the production Reads.
func TestBuildService_IsTheReadsImplementation(t *testing.T) {
	var _ inbounda2a.Reads = buildService(nil, quietLogger())
}

func TestGetenv(t *testing.T) {
	t.Setenv("DP_A2A_TEST_SET", "v")
	t.Setenv("DP_A2A_TEST_EMPTY", "")
	if got := getenv("DP_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("DP_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

// newLogger accepts the fleet's level words; an unknown word means info.
func TestNewLogger(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "warning", "error", "nonsense"} {
		if newLogger(level) == nil {
			t.Fatalf("newLogger(%q) = nil", level)
		}
	}
}
