package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var unitOfWorkCases = []named{
	{"a returned nil commits every save", uowCommit},
	{"a returned error rolls every save back and is returned unchanged", uowRollback},
	{"a panic rolls back and propagates", uowPanic},
	{"a nested Do joins the outer unit", uowNested},
	{"the outbox and the inbox commit and roll back with the aggregates", uowOutboxInbox},
}

var errBoom = errors.New("boom")

func twoOffers(t *testing.T, f Fixture, c context.Context) error {
	t.Helper()
	if err := f.Offers.Save(c, NewOffer(t, "off-1")); err != nil {
		return err
	}
	return f.Offers.Save(c, NewOffer(t, "off-2"))
}

func offerStored(f Fixture, id string) bool {
	_, err := f.Offers.Get(ctx, offerID(id))
	return err == nil
}

func uowCommit(t *testing.T, f Fixture) {
	noErr(t, f.UoW.Do(ctx, func(c context.Context) error { return twoOffers(t, f, c) }))
	if !offerStored(f, "off-1") || !offerStored(f, "off-2") {
		t.Fatal("a committed unit lost a save")
	}
}

func uowRollback(t *testing.T, f Fixture) {
	err := f.UoW.Do(ctx, func(c context.Context) error {
		noErr(t, twoOffers(t, f, c))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if offerStored(f, "off-1") || offerStored(f, "off-2") {
		t.Fatal("a failed unit left a save behind")
	}
}

func uowPanic(t *testing.T, f Fixture) {
	defer func() {
		if recover() == nil {
			t.Fatal("the panic was swallowed")
		}
		if offerStored(f, "off-1") {
			t.Fatal("a panicking unit left a save behind")
		}
	}()
	_ = f.UoW.Do(ctx, func(c context.Context) error {
		noErr(t, twoOffers(t, f, c))
		panic("kaboom")
	})
}

func uowNested(t *testing.T, f Fixture) {
	err := f.UoW.Do(ctx, func(outer context.Context) error {
		noErr(t, f.UoW.Do(outer, func(inner context.Context) error { return f.Offers.Save(inner, NewOffer(t, "off-1")) }))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if offerStored(f, "off-1") {
		t.Fatal("the inner unit committed on its own")
	}
}

func uowOutboxInbox(t *testing.T, f Fixture) {
	ev := []shared.Event{mustEvent(t)}
	before := f.OutboxCount()
	err := f.UoW.Do(ctx, func(c context.Context) error {
		first, err := f.Inbox.Begin(c, "evt-1", "type.X", T0)
		noErr(t, err)
		eq(t, "first delivery", first, true)
		noErr(t, f.Outbox.Append(c, ev...))
		return errBoom
	})
	wantErr(t, err, errBoom)
	eq(t, "outbox rolled back", f.OutboxCount(), before)
	noErr(t, f.UoW.Do(ctx, func(c context.Context) error {
		first, err := f.Inbox.Begin(c, "evt-1", "type.X", T0)
		noErr(t, err)
		eq(t, "the rolled-back id is new again", first, true)
		again, err := f.Inbox.Begin(c, "evt-1", "type.X", T0)
		noErr(t, err)
		eq(t, "a second delivery inside the unit", again, false)
		return f.Outbox.Append(c, ev...)
	}))
	eq(t, "outbox committed", f.OutboxCount(), before+1)
	noErr(t, f.UoW.Do(ctx, func(c context.Context) error {
		again, err := f.Inbox.Begin(c, "evt-1", "type.X", T0)
		noErr(t, err)
		eq(t, "a committed id is not new", again, false)
		return nil
	}))
	noErr(t, f.Outbox.Append(ctx))
	eq(t, "appending nothing is a no-op", f.OutboxCount(), before+1)
}
