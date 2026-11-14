package features_test

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

const promiseStates = `PROVISIONAL|CONFIRMED|AT_RISK|KEPT|BROKEN|RELEASED|CANCELLED`

func (r *rules) registerLifecycle(sc *godog.ScenarioContext) {
	sc.Step(`^the promise is remembered as "([^"]*)"$`, r.rememberPromise)
	sc.Step(`^the promise "([^"]*)" becomes the scenario's promise$`, r.adoptPromise)
	sc.Step(`^the remembered promise "([^"]*)" is (`+promiseStates+`)$`, r.rememberedState)
	sc.Step(`^the order "([^"]*)" is placed with the promise "([^"]*)" as group (\d+) and the promise "([^"]*)" as group (\d+)$`, r.placeTwoGroups)
	sc.Step(`^the order "([^"]*)" is cancelled for the groups "([^"]*)" with the reason "([^"]*)"$`, r.cancelGroups)
	sc.Step(`^the promise was cancelled with the reason "([^"]*)"$`, r.cancelReason)
}

func (r *rules) rememberPromise(label string) error {
	if r.w.promiseID == "" {
		return errNoPromise
	}
	r.promises[label] = r.w.promiseID
	return nil
}

func (r *rules) adoptPromise(label string) error {
	id, ok := r.promises[label]
	if !ok {
		return fmt.Errorf("the scenario has not remembered a promise as %q", label)
	}
	r.w.promiseID = id
	return nil
}

func (r *rules) rememberedState(label, want string) error {
	id, ok := r.promises[label]
	if !ok {
		return fmt.Errorf("the scenario has not remembered a promise as %q", label)
	}
	p, err := r.w.svc.GetPromise(r.w.ctx(), id)
	if err != nil {
		return err
	}
	if p.State != want {
		return fmt.Errorf("promise %s is %s, want %s", label, p.State, want)
	}
	return nil
}

// promiseRef resolves a remembered promise label, or takes the text as a
// literal promise id (a scenario may name a promise nobody ever made).
func (r *rules) promiseRef(label string) string {
	if id, ok := r.promises[label]; ok {
		return id
	}
	return label
}

func (r *rules) placeTwoGroups(order, first string, g1 int, second string, g2 int) error {
	placed := usecases.CustomerOrderPlaced{OrderID: order, Groups: []usecases.OrderGroup{
		{GroupNo: g1, PromiseID: r.promiseRef(first)}, {GroupNo: g2, PromiseID: r.promiseRef(second)},
	}}
	r.w.run(func() error { return r.w.svc.OnCustomerOrderPlaced(r.w.ctx(), r.w.meta("CustomerOrderPlaced"), placed) })
	return nil
}

func (r *rules) cancelGroups(order, groups, reason string) error {
	var nos []int
	for _, g := range strings.Split(groups, ",") {
		if g = strings.TrimSpace(g); g != "" {
			n, err := strconv.Atoi(g)
			if err != nil {
				return fmt.Errorf("group %q is not a number", g)
			}
			nos = append(nos, n)
		}
	}
	r.w.run(func() error {
		return r.w.svc.OnCustomerOrderCancelled(r.w.ctx(), r.w.meta("CustomerOrderCancelled"), usecases.CustomerOrderCancelled{OrderID: order, Reason: reason, GroupNos: nos})
	})
	return nil
}

func (r *rules) cancelReason(want string) error {
	p, err := r.w.promise()
	if err != nil {
		return err
	}
	if p.CancelReason != want {
		return fmt.Errorf("the promise was cancelled with %q, want %q", p.CancelReason, want)
	}
	return nil
}

func (r *rules) registerJudgement(sc *godog.ScenarioContext) {
	sc.Step(`^the package "([^"]*)" is lost at "([^"]*)"$`, r.lostAt)
	sc.Step(`^the sweeper flags the promises predicted late$`, r.flagLate)
	sc.Step(`^the sweep flagged (\d+) promises?$`, r.flagged)
	sc.Step(`^the load of the departure "([^"]*)" arrived at "([^"]*)"$`, r.loadArrived)
}

func (r *rules) lostAt(tracking, at string) error {
	t, err := parseInstant(at)
	if err != nil {
		return err
	}
	r.w.run(func() error {
		return r.w.svc.OnPackageLost(r.w.ctx(), r.w.meta("PackageLost"), usecases.PackageLost{TrackingNumber: tracking, DeclaredAt: t})
	})
	return nil
}

// swept is how many promises the last late-risk pass flagged.
func (r *rules) flagLate() error {
	r.w.run(func() error {
		n, err := r.w.svc.FlagPredictedLate(r.w.ctx(), 10)
		r.swept = n
		return err
	})
	return r.w.lastErr
}

func (r *rules) flagged(want int) error {
	if r.swept != want {
		return fmt.Errorf("the late-risk sweep flagged %d promises, want %d", r.swept, want)
	}
	return nil
}

func (r *rules) loadArrived(departure, at string) error {
	t, err := parseInstant(at)
	if err != nil {
		return err
	}
	return r.w.svc.OnLoadArrived(r.w.ctx(), r.w.meta("LoadArrived"), usecases.LoadArrived{
		LoadID: "load-" + departure, DepartureID: departure, DestinationNodeID: "N-SAU1", ArrivedAt: t,
	})
}
