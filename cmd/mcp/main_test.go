package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/delivery-promise/internal/adapters/inbound/mcp"
	"github.com/claudioed/delivery-promise/internal/application/views"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestServer serves the real MCP server (no tool can reach a database:
// a Reads stub is enough for the transport tests) behind the real router.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: stubReads{}}))))
	t.Cleanup(srv.Close)
	return srv
}

// stubReads answers nothing: the transport tests never call a tool.
type stubReads struct{}

func (stubReads) GetOffer(context.Context, string) (views.Offer, error) { return views.Offer{}, nil }

func (stubReads) ListPromisesByOrder(context.Context, string) (views.PromiseList, error) {
	return views.PromiseList{}, nil
}

func (stubReads) GetPromise(context.Context, string) (views.Promise, error) {
	return views.Promise{}, nil
}

func (stubReads) ListPromiseBooks(context.Context, string, string) (views.BookList, error) {
	return views.BookList{}, nil
}

func (stubReads) CurrentPolicy(context.Context) (views.Policy, error) {
	return views.Policy{}, nil
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

// Both mount points speak Streamable HTTP, with no credentials, and a real
// client lists the five read-only tools.
func TestStreamableHTTP_RootAndMCPPathsServeTools(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/", "/mcp"} {
		t.Run(path, func(t *testing.T) {
			client := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
			session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL + path}, nil)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer session.Close()
			tools, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatalf("list tools: %v", err)
			}
			if len(tools.Tools) != 5 {
				t.Fatalf("tools = %d, want 5", len(tools.Tools))
			}
			for _, tool := range tools.Tools {
				if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
					t.Fatalf("tool %q is not read-only", tool.Name)
				}
			}
		})
	}
}

// The binary reads no API key: requests carrying (or lacking) any
// Authorization header behave identically.
func TestNoAuthIsRequired(t *testing.T) {
	srv := newTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	for _, auth := range []string{"", "Bearer nonsense"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initialize with Authorization=%q = %d, want 200", auth, resp.StatusCode)
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

func TestGetenv(t *testing.T) {
	t.Setenv("DP_MCP_TEST_SET", "v")
	t.Setenv("DP_MCP_TEST_EMPTY", "")
	if got := getenv("DP_MCP_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("DP_MCP_TEST_EMPTY", "d"); got != "d" {
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
