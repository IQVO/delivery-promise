//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/delivery-promise/internal/adapters/inbound/mcp"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// writeTables are every table an estimate must leave untouched (ADR 0019).
var writeTables = []string{"delivery_offers", "outbox_events", "promises", "promise_books", "idempotency_keys", "promise_policies"}

func tableCounts(t *testing.T, pool *pgtest.Pool) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range writeTables {
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

func sameCounts(t *testing.T, stage string, before, after map[string]int) {
	t.Helper()
	for _, table := range writeTables {
		if before[table] != after[table] {
			t.Errorf("%s: %s rows %d -> %d, an estimate stores nothing", stage, table, before[table], after[table])
		}
	}
}

// On real Postgres, the estimate use case (through the service cmd/mcp wires,
// at the worked example's instant) and the estimate_delivery tool answer
// without writing a row anywhere: no offer, no outbox event, no book, no hold.
func TestEstimatesAgainstPostgresWriteNothing(t *testing.T) {
	url := pgtest.URL(t)
	ctx := context.Background()
	seedThroughWriteUseCases(t, url)
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	before := tableCounts(t, pool)
	if before["delivery_offers"] == 0 || before["outbox_events"] == 0 {
		t.Fatalf("the seed wrote no offer or event: %v", before)
	}

	now := time.Date(2026, 10, 22, 14, 5, 0, 0, time.FixedZone("CDT", -5*3600)).UTC()
	svc := buildServiceWithClock(pool, quietLogger(), fixedClock{now})
	res, err := svc.EstimateDeliveries(ctx, usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: []shared.Line{
		{CatalogItemID: "ci-1001", Quantity: 2}, {CatalogItemID: "ci-unknown", Quantity: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Estimates) != 2 || len(res.Estimates[0].Options) == 0 || res.Estimates[0].Options[0].Tier != "NEXT_DAY" {
		t.Fatalf("estimate = %+v", res)
	}
	if c := res.Estimates[1].Constraints; len(c) != 1 || c[0].Code != "ITEM_QUANTITY_NOT_AVAILABLE" || c[0].Tier != "" {
		t.Fatalf("unknown item constraints = %+v", c)
	}
	sameCounts(t, "use case", before, tableCounts(t, pool))

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)
	callEstimateTool(t, deps)
	sameCounts(t, "estimate_delivery tool", before, tableCounts(t, pool))
}

func callEstimateTool(t *testing.T, deps inboundmcp.Deps) {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "estimate_delivery", Arguments: map[string]any{
		"destinationPostalPrefix": "787", "items": []any{map[string]any{"catalogItemId": "ci-1001", "quantity": 1}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("estimate_delivery: err=%v res=%+v", err, res)
	}
}
