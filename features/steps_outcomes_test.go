package features_test

import (
	"errors"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// errorsByName maps the model's Err names to the sentinels the domain raises.
var errorsByName = map[string]error{
	"ErrPromiseCapacityExhausted": book.ErrPromiseCapacityExhausted,
	"ErrOfferNotQuotable":         offer.ErrOfferNotQuotable,
	"ErrPromiseHoldExpired":       promise.ErrPromiseHoldExpired,
	"ErrPromiseClosed":            promise.ErrPromiseClosed,
	"ErrGroupSpansNodes":          promise.ErrGroupSpansNodes,
	"ErrIllegalTransition":        shared.ErrIllegalTransition,
	"ErrValidation":               shared.ErrValidation,
	"ErrNotFound":                 ports.ErrNotFound,
}

func registerOutcomeSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the command succeeds$`, w.commandSucceeds)
	sc.Step(`^the command is refused with "([^"]*)"$`, w.commandRefused)
	sc.Step(`^the last command published the event "([^"]*)" (\d+) times?$`, w.publishedTimes)
	sc.Step(`^the last command published no events$`, w.publishedNothing)
	sc.Step(`^the promise has (\d+) revisions? with the reason "([^"]*)"$`, w.revisions)
	sc.Step(`^the promise ships from the pickup "([^"]*)"$`, w.shipsFrom)
	sc.Step(`^the judgement cause is "([^"]*)"$`, w.judgementCause)
	sc.Step(`^the promise was late by (\d+) minutes$`, w.lateBy)
}

func (w *world) commandSucceeds() error {
	if !w.ran {
		return errNoCommand
	}
	if w.lastErr != nil {
		return fmt.Errorf("the command failed: %w", w.lastErr)
	}
	return nil
}

func (w *world) commandRefused(name string) error {
	if !w.ran {
		return errNoCommand
	}
	want, ok := errorsByName[name]
	if !ok {
		return fmt.Errorf("the scenario names an unknown error %q", name)
	}
	if !errors.Is(w.lastErr, want) {
		return fmt.Errorf("the command returned %v, want %s", w.lastErr, name)
	}
	return nil
}

func (w *world) publishedTimes(name string, want int) error {
	n := 0
	for _, got := range w.published() {
		if got == name {
			n++
		}
	}
	if n != want {
		return fmt.Errorf("%s was published %d times, want %d (published %v)", name, n, want, w.published())
	}
	return nil
}

func (w *world) publishedNothing() error {
	if got := w.published(); len(got) != 0 {
		return fmt.Errorf("the command published %v", got)
	}
	return nil
}

func (w *world) revisions(n int, reason string) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if len(p.Revisions) != n || (n > 0 && p.Revisions[0].Reason != reason) {
		return fmt.Errorf("promise has %d revisions (%+v), want %d with reason %s", len(p.Revisions), p.Revisions, n, reason)
	}
	return nil
}

func (w *world) shipsFrom(pickup string) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if p.WitnessPath.PickupDepartureID != pickup {
		return fmt.Errorf("promise ships from %s, want %s", p.WitnessPath.PickupDepartureID, pickup)
	}
	return nil
}

func (w *world) judgementCause(cause string) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if p.Judgement == nil || p.Judgement.Cause != cause {
		return fmt.Errorf("judgement = %+v, want cause %s", p.Judgement, cause)
	}
	return nil
}

func (w *world) lateBy(minutes int) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if p.Judgement == nil || p.Judgement.LateByMinutes == nil || *p.Judgement.LateByMinutes != minutes {
		return fmt.Errorf("judgement = %+v, want late by %d", p.Judgement, minutes)
	}
	return nil
}
