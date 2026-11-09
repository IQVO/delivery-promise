//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// calibrationMigration is the migration this file proves (ADR 0020).
const calibrationMigration = 6

var ct0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// TestCalibrationMigrationCreatesAndDropsTheTable proves migration 0006 on a
// fresh database: the table exists after the set climbs, the step back drops
// it, and the step up again restores it empty (rebuildable from nothing).
func TestCalibrationMigrationCreatesAndDropsTheTable(t *testing.T) {
	ctx := context.Background()
	url := pgtest.EmptyURL(t)
	mustDo(t, "up", postgres.RunMigrations(url))
	pool := pgtest.OpenPool(t, url)
	tables := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name = 'rm_promise_calibration'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if tables() != 1 {
		t.Fatal("0006 up must create rm_promise_calibration")
	}
	mustDo(t, "down to 0005", postgres.MigrateTo(url, calibrationMigration-1))
	if tables() != 0 {
		t.Fatal("0006 down must drop rm_promise_calibration")
	}
	mustDo(t, "up again", postgres.RunMigrations(url))
	got, err := postgres.NewPromiseCalibrationView(pool).Segments(ctx, 28, ct0)
	mustDo(t, "segments", err)
	if len(got) != 0 {
		t.Fatalf("a re-created table is empty, got %v", got)
	}
}

// TestConcurrentJudgementsOfOneSegmentAllLand loads every writer first, then
// releases them together: the row lock makes the appends follow one another, so
// no observation overwrites another.
func TestConcurrentJudgementsOfOneSegmentAllLand(t *testing.T) {
	pool := pgtest.NewPool(t)
	view, uow := postgres.NewPromiseCalibrationView(pool), postgres.NewUnitOfWork(pool)
	key := readmodel.CalibrationKey{Tier: shared.TierNextDay, Basis: shared.BasisMeasured, NodeGrade: search.GradeC}
	const writers = 16
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- uow.Do(context.Background(), func(ctx context.Context) error {
				return view.RecordJudgement(ctx, key, readmodel.CalibrationObservation{LateByMinutes: i, ObservedAt: ct0.Add(time.Duration(i) * time.Minute)})
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent judgement failed: %v", err)
		}
	}
	got, err := view.Segments(context.Background(), 28, ct0.Add(time.Hour))
	mustDo(t, "segments", err)
	if len(got[key]) != writers {
		t.Fatalf("observations = %d, want %d: a concurrent append was lost", len(got[key]), writers)
	}
}
