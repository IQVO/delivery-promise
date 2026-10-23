//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/outbox"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

var errBroker = errors.New("broker down")

func releasedEvents(ids ...string) []shared.Event {
	var out []shared.Event
	for _, id := range ids {
		out = append(out, promise.PromiseReleased{Header: shared.Header{Version: 2}, PromiseID: promise.ID(id), Reason: "HOLD_EXPIRED"})
	}
	return out
}

func TestDrainPublishesInOrderAndMarksRows(t *testing.T) {
	pool := pgtest.NewPool(t)
	ob := postgres.NewOutbox(pool, kafka.NewEncoder())
	ctx := context.Background()
	if err := ob.Append(ctx, releasedEvents("prm-1", "prm-2", "prm-3")...); err != nil {
		t.Fatal(err)
	}
	var sent []string
	n, err := ob.Drain(ctx, 2, 0, func(_ context.Context, m outbox.Message) error { sent = append(sent, m.Subject); return nil })
	if err != nil || n != 2 || len(sent) != 2 || sent[0] != "prm-1" || sent[1] != "prm-2" {
		t.Fatalf("first pass: n=%d err=%v sent=%v", n, err, sent)
	}
	n, err = ob.Drain(ctx, 10, 0, func(_ context.Context, m outbox.Message) error { sent = append(sent, m.Subject); return nil })
	if err != nil || n != 1 || sent[2] != "prm-3" {
		t.Fatalf("second pass: n=%d err=%v sent=%v", n, err, sent)
	}
	pgtest.WaitAllPublished(t, pool)
}

func TestDrainStopsAtTheFirstFailureAndRecordsIt(t *testing.T) {
	pool := pgtest.NewPool(t)
	ob := postgres.NewOutbox(pool, kafka.NewEncoder())
	ctx := context.Background()
	if err := ob.Append(ctx, releasedEvents("prm-1", "prm-2")...); err != nil {
		t.Fatal(err)
	}
	calls := 0
	n, err := ob.Drain(ctx, 10, 0, func(context.Context, outbox.Message) error {
		calls++
		return errBroker
	})
	var sf *outbox.SendFailure
	if !errors.As(err, &sf) || sf.Attempts != 1 || !errors.Is(err, errBroker) || n != 0 || calls != 1 {
		t.Fatalf("n=%d calls=%d err=%v", n, calls, err)
	}
	attempts, lastErr, published := pgtest.OutboxRowState(t, pool, sf.RowID)
	if attempts != 1 || lastErr != "broker down" || published {
		t.Fatalf("row state = %d %q %v", attempts, lastErr, published)
	}
}

func TestARowThatReachedTheAttemptCapIsSkippedAndLaterRowsOvertakeIt(t *testing.T) {
	pool := pgtest.NewPool(t)
	ob := postgres.NewOutbox(pool, kafka.NewEncoder())
	ctx := context.Background()
	if err := ob.Append(ctx, releasedEvents("poison")...); err != nil {
		t.Fatal(err)
	}
	fail := func(context.Context, outbox.Message) error { return errBroker }
	for range 2 {
		_, _ = ob.Drain(ctx, 10, 2, fail)
	}
	if err := ob.Append(ctx, releasedEvents("good")...); err != nil {
		t.Fatal(err)
	}
	var sent []string
	n, err := ob.Drain(ctx, 10, 2, func(_ context.Context, m outbox.Message) error { sent = append(sent, m.Subject); return nil })
	if err != nil || n != 1 || len(sent) != 1 || sent[0] != "good" {
		t.Fatalf("n=%d err=%v sent=%v: the poison row must be skipped", n, err, sent)
	}
}

func TestAppendInsideAFailedUnitOfWorkLeavesNoRow(t *testing.T) {
	pool := pgtest.NewPool(t)
	ob := postgres.NewOutbox(pool, kafka.NewEncoder())
	uow := postgres.NewUnitOfWork(pool)
	ctx := context.Background()
	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := ob.Append(ctx, releasedEvents("prm-1")...); err != nil {
			return err
		}
		return errBroker
	})
	if !errors.Is(err, errBroker) {
		t.Fatalf("err = %v", err)
	}
	if n, _ := ob.Count(ctx); n != 0 {
		t.Fatalf("%d rows survived a rolled-back unit", n)
	}
}

type badEncoder struct{}

func (badEncoder) Encode(shared.Event) (outbox.Message, error) { return outbox.Message{}, errBroker }

func TestAnEventThatDoesNotEncodeFailsTheAppend(t *testing.T) {
	pool := pgtest.NewPool(t)
	ob := postgres.NewOutbox(pool, badEncoder{})
	if err := ob.Append(context.Background(), releasedEvents("prm-1")...); !errors.Is(err, errBroker) {
		t.Fatalf("err = %v", err)
	}
}
