package features_test

import (
	"fmt"
	"reflect"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/book"
)

// The steps of this file drive the Kafka-ARRIVED risk facts the consumer
// projects (BookingMoved, BookingFailed, PackageInductionDeferred,
// PackageExceptionRaised, FulfillmentUnsourceable), the two clock-driven
// sweepers (ExpireOffers, CloseBooks) and the payload of the published events.
func registerFactSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the booking of the assignment "([^"]*)" is moved from the departure "([^"]*)"$`, w.bookingMoved)
	sc.Step(`^the booking of the assignment "([^"]*)" on the departure "([^"]*)" fails$`, w.bookingFailed)
	sc.Step(`^the induction of the package "([^"]*)" is deferred$`, w.inductionDeferred)
	sc.Step(`^a package of the assignment "([^"]*)" raises an exception$`, w.packageException)
	sc.Step(`^the fulfilment of the promise is unsourceable at the node "([^"]*)"$`, w.unsourceable)
	sc.Step(`^the sweeper expires the quoted offers$`, w.expireOffers)
	sc.Step(`^the sweeper closes the due books$`, w.closeBooks)
	sc.Step(`^the book "([^"]*)" is (OPEN|CLOSED)$`, w.bookState)
	sc.Step(`^the promise is at risk with the reason "([A-Z_]+)"$`, w.atRiskReason)
	sc.Step(`^the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer is NOMINAL from the node "([^"]*)"$`, w.offerNominal)
	sc.Step(`^the last command published a "([A-Za-z]+)" event whose "([A-Za-z]+)" is "([^"]*)"$`, w.eventField)
	sc.Step(`^the promise "([^"]*)" is listed for the order "([^"]*)" in group (\d+)$`, w.listedPromise)
	sc.Step(`^I list the promises of the order "([^"]*)"$`, w.listPromises)
}

func (w *world) bookingMoved(assignment, from string) error {
	w.run(func() error {
		return w.svc.OnBookingMoved(w.ctx(), w.meta("BookingMoved"), usecases.BookingMoved{AssignmentID: assignment, FromDepartureID: from})
	})
	return w.lastErr
}

func (w *world) bookingFailed(assignment, departure string) error {
	w.run(func() error {
		return w.svc.OnBookingFailed(w.ctx(), w.meta("BookingFailed"), usecases.BookingFailed{AssignmentID: assignment, DepartureID: departure})
	})
	return w.lastErr
}

func (w *world) inductionDeferred(tracking string) error {
	w.run(func() error {
		return w.svc.OnInductionDeferred(w.ctx(), w.meta("PackageInductionDeferred"), usecases.InductionDeferred{TrackingNumber: tracking})
	})
	return w.lastErr
}

func (w *world) packageException(assignment string) error {
	w.run(func() error {
		return w.svc.OnPackageException(w.ctx(), w.meta("PackageExceptionRaised"), usecases.PackageException{TrackingNumber: "TRK-1", AssignmentID: assignment})
	})
	return w.lastErr
}

func (w *world) unsourceable(node string) error {
	if w.promiseID == "" {
		return errNoPromise
	}
	w.run(func() error {
		return w.svc.OnFulfillmentUnsourceable(w.ctx(), w.meta("FulfillmentUnsourceable"), usecases.FulfillmentUnsourceable{
			AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: w.promiseID, ExcludedNodes: usecases.NodeRefs{node},
		})
	})
	return w.lastErr
}

func (w *world) expireOffers() error {
	w.run(func() error {
		_, err := w.svc.ExpireOffers(w.ctx(), 10)
		return err
	})
	return nil
}

func (w *world) closeBooks() error {
	w.run(func() error {
		_, err := w.svc.CloseBooks(w.ctx(), 10)
		return err
	})
	return nil
}

func (w *world) bookState(id, want string) error {
	b, err := w.store.Books().Get(w.ctx(), book.ID(id))
	if err != nil {
		return err
	}
	if got := string(b.State()); got != want {
		return fmt.Errorf("book %s is %s, want %s", id, got, want)
	}
	return nil
}

func (w *world) atRiskReason(want string) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if p.State != "AT_RISK" || p.AtRiskReason != want {
		return fmt.Errorf("promise is %s at-risk %q, want reason %s", p.State, p.AtRiskReason, want)
	}
	return nil
}

// offerNominal asserts the E8 shape of a NOMINAL witness path: the node is
// named, but no pickup departure, no wave and no legs are.
func (w *world) offerNominal(tier, node string) error {
	o, err := w.offer(tier)
	if err != nil {
		return err
	}
	wp := o.WitnessPath
	if o.Basis != "NOMINAL" || wp.NodeID != node || wp.PickupDepartureID != "" || wp.DeliveryWaveID != "" || len(wp.Legs) != 0 {
		return fmt.Errorf("%s offer basis %s witness %+v, want NOMINAL from %s with no departure, wave or legs", tier, o.Basis, wp, node)
	}
	return nil
}

// eventField asserts a key field of one event the last command published, so a
// scenario pins the payload (the reason of a risk, the cause of a break, the
// version of a policy), not just the event name.
func (w *world) eventField(name, field, want string) error {
	for _, e := range w.store.Outbox().Events()[w.outboxLen:] {
		if e.EventName() != name {
			continue
		}
		v := reflect.ValueOf(e)
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		f := v.FieldByName(field)
		if !f.IsValid() {
			return fmt.Errorf("%s has no field %q", name, field)
		}
		got := fmt.Sprint(f.Interface())
		if t, ok := f.Interface().(time.Time); ok {
			got = t.Format(time.RFC3339)
		}
		if got != want {
			return fmt.Errorf("%s.%s = %q, want %q", name, field, got, want)
		}
		return nil
	}
	return fmt.Errorf("the last command published no %s (published %v)", name, w.published())
}

// listedPromise asserts the query behind GET /promises at the use-case level:
// the order's promises, one per group, found by promise id, never by position.
func (w *world) listedPromise(wantID, order string, group int) error {
	if wantID == "{promiseId}" {
		wantID = w.promiseID
	}
	res, err := w.svc.ListPromisesByOrder(w.ctx(), order)
	if err != nil {
		return err
	}
	for _, p := range res.Items {
		if p.PromiseID == wantID {
			if p.GroupNo != group {
				return fmt.Errorf("promise %s is group %d, want %d", wantID, p.GroupNo, group)
			}
			return nil
		}
	}
	ids := make([]string, len(res.Items))
	for i, p := range res.Items {
		ids[i] = p.PromiseID
	}
	return fmt.Errorf("order %s lists %v, want %s among them", order, ids, wantID)
}

// listPromises runs the query behind GET /promises as the scenario's command,
// so its refusals (an invalid orderId) can be asserted like any other.
func (w *world) listPromises(order string) error {
	w.run(func() error {
		_, err := w.svc.ListPromisesByOrder(w.ctx(), order)
		return err
	})
	return nil
}
