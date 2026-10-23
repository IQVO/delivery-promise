package features_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

func registerRiskSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the assignment "([^"]*)" of the order "([^"]*)" is locked and shipped with the packages "([^"]*)"$`, w.lockAndShip)
	sc.Step(`^the package "([^"]*)" is delivered at "([^"]*)"$`, w.delivered)
	sc.Step(`^the package "([^"]*)" is lost$`, w.lost)
	sc.Step(`^the package "([^"]*)" is returned$`, w.returned)
	sc.Step(`^the sweeper re-promises the at-risk promises$`, w.reprice)
	sc.Step(`^the order-placed event for the promise is delivered twice$`, w.deliveredTwice)
	sc.Step(`^the second delivery changed nothing$`, w.nothingChanged)
}

func (w *world) lockAndShip(assignment, order, trackings string) error {
	if w.promiseID == "" {
		return errNoPromise
	}
	ctx := w.ctx()
	locked := usecases.FulfillmentLocked{AssignmentID: assignment, OrderID: order, GroupNo: 1, PromiseID: w.promiseID, NodeID: "N-AUS3"}
	if err := w.svc.OnFulfillmentLocked(ctx, w.meta("FulfillmentLocked"), locked); err != nil {
		return err
	}
	shipped := usecases.FulfillmentShipped{AssignmentID: assignment, TrackingNumbers: strings.Split(trackings, ",")}
	return w.svc.OnFulfillmentShipped(ctx, w.meta("FulfillmentShipped"), shipped)
}

func (w *world) delivered(tracking, at string) error {
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return err
	}
	w.run(func() error {
		return w.svc.OnPackageDelivered(w.ctx(), w.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: tracking, DeliveredAt: when})
	})
	return nil
}

func (w *world) lost(tracking string) error {
	w.run(func() error {
		return w.svc.OnPackageLost(w.ctx(), w.meta("PackageLost"), usecases.PackageLost{TrackingNumber: tracking, DeclaredAt: at(23, 10, 0)})
	})
	return nil
}

func (w *world) returned(tracking string) error {
	w.run(func() error {
		return w.svc.OnPackageReturned(w.ctx(), w.meta("PackageReturned"), usecases.PackageReturned{TrackingNumber: tracking, ReturnedAt: at(23, 10, 0)})
	})
	return nil
}

func (w *world) reprice() error {
	w.run(func() error {
		_, err := w.svc.RepriceAtRisk(w.ctx(), 10)
		return err
	})
	return nil
}

func (w *world) deliveredTwice() error {
	if w.promiseID == "" {
		return errNoPromise
	}
	m := w.meta("CustomerOrderPlaced")
	placed := usecases.CustomerOrderPlaced{OrderID: "ord-1", Groups: []usecases.OrderGroup{{GroupNo: 1, PromiseID: w.promiseID}}}
	if err := w.svc.OnCustomerOrderPlaced(w.ctx(), m, placed); err != nil {
		return err
	}
	w.counts = w.store.Counts()
	w.outboxLen = len(w.store.Outbox().Events())
	w.run(func() error { return w.svc.OnCustomerOrderPlaced(w.ctx(), m, placed) })
	return nil
}

func (w *world) nothingChanged() error {
	if w.lastErr != nil {
		return fmt.Errorf("the redelivery failed: %w", w.lastErr)
	}
	if got := w.store.Counts(); got != w.counts {
		return fmt.Errorf("the redelivery changed the store: %+v -> %+v", w.counts, got)
	}
	return nil
}
