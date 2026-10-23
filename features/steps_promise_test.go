package features_test

import (
	"fmt"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/book"
)

func registerPromiseSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I make a promise from the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer$`, w.makePromise)
	sc.Step(`^I make a promise from the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer again$`, w.makePromiseAgain)
	sc.Step(`^a promise exists from the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer$`, w.givenPromise)
	sc.Step(`^I confirm the promise for the order "([^"]*)" group (\d+)$`, w.confirm)
	sc.Step(`^the promise for the order "([^"]*)" is confirmed by the order-placed event$`, w.orderPlaced)
	sc.Step(`^the promise is (PROVISIONAL|CONFIRMED|AT_RISK|KEPT|BROKEN|RELEASED|CANCELLED)$`, w.promiseState)
	sc.Step(`^the promise holds the hold expiry "([^"]*)"$`, w.holdExpiry)
	sc.Step(`^the book "([^"]*)" holds (\d+) provisional and (\d+) confirmed$`, w.bookHolds)
	sc.Step(`^the book "([^"]*)" has a share of (\d+)$`, w.bookShare)
	sc.Step(`^the second promise is the first promise$`, w.samePromise)
	sc.Step(`^the sweeper releases the expired holds$`, w.releaseHolds)
	sc.Step(`^the order "([^"]*)" is cancelled$`, w.cancelOrder)
	sc.Step(`^I cancel the promise on request$`, w.cancelPromise)
}

func (w *world) makeFrom(tier string) error {
	o, err := w.offer(tier)
	if err != nil {
		return err
	}
	w.run(func() error {
		res, err := w.svc.MakePromise(w.ctx(), usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: lines("ci-1001", 2)})
		if err == nil {
			w.promiseID = res.Promise.PromiseID
			w.created = res.Created
		}
		return err
	})
	return nil
}

func (w *world) makePromise(tier string) error { return w.makeFrom(tier) }

func (w *world) makePromiseAgain(tier string) error {
	first := w.promiseID
	if err := w.makeFrom(tier); err != nil {
		return err
	}
	w.firstPromise = first
	return nil
}

func (w *world) givenPromise(tier string) error {
	if err := w.makeFrom(tier); err != nil {
		return err
	}
	return w.given("make a promise", func() error { return w.lastErr })
}

func (w *world) confirm(order string, group int) error {
	if w.promiseID == "" {
		return errNoPromise
	}
	w.run(func() error {
		_, err := w.svc.ConfirmPromise(w.ctx(), w.promiseID, order, group)
		return err
	})
	return nil
}

func (w *world) orderPlaced(order string) error {
	if w.promiseID == "" {
		return errNoPromise
	}
	placed := usecases.CustomerOrderPlaced{OrderID: order, Groups: []usecases.OrderGroup{{GroupNo: 1, PromiseID: w.promiseID}}}
	w.run(func() error { return w.svc.OnCustomerOrderPlaced(w.ctx(), w.meta("CustomerOrderPlaced"), placed) })
	return nil
}

func (w *world) promiseState(want string) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if p.State != want {
		return fmt.Errorf("promise is %s, want %s", p.State, want)
	}
	return nil
}

func (w *world) holdExpiry(want string) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if got := p.HoldExpiresAt.UTC().Format("2006-01-02T15:04:05Z"); got != want {
		return fmt.Errorf("hold expires at %s, want %s", got, want)
	}
	return nil
}

func (w *world) bookHolds(id string, provisional, confirmed int) error {
	b, err := w.store.Books().Get(w.ctx(), book.ID(id))
	if err != nil {
		return err
	}
	if b.Provisional() != provisional || b.Confirmed() != confirmed {
		return fmt.Errorf("book %s holds %d provisional and %d confirmed, want %d and %d", id, b.Provisional(), b.Confirmed(), provisional, confirmed)
	}
	return nil
}

func (w *world) bookShare(id string, share int) error {
	b, err := w.store.Books().Get(w.ctx(), book.ID(id))
	if err != nil {
		return err
	}
	if b.Share() != share {
		return fmt.Errorf("book %s share %d, want %d", id, b.Share(), share)
	}
	return nil
}

func (w *world) samePromise() error {
	if w.created || w.firstPromise == "" || w.firstPromise != w.promiseID {
		return fmt.Errorf("replay created=%v first=%q second=%q", w.created, w.firstPromise, w.promiseID)
	}
	return nil
}

func (w *world) releaseHolds() error {
	w.run(func() error {
		_, err := w.svc.ReleaseExpiredHolds(w.ctx(), 10)
		return err
	})
	return nil
}

func (w *world) cancelPromise() error {
	if w.promiseID == "" {
		return errNoPromise
	}
	w.run(func() error {
		_, err := w.svc.CancelPromise(w.ctx(), w.promiseID)
		return err
	})
	return nil
}

func (w *world) cancelOrder(order string) error {
	w.run(func() error {
		return w.svc.OnCustomerOrderCancelled(w.ctx(), w.meta("CustomerOrderCancelled"), usecases.CustomerOrderCancelled{OrderID: order, Reason: "CUSTOMER_REQUEST"})
	})
	return nil
}
