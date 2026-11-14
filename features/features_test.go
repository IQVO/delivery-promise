// Package features_test hosts the godog (Cucumber for Go) acceptance suite of
// delivery-promise. Every scenario drives the application's use cases, the
// same ones the REST adapter and the Kafka consumer call, over the in-memory
// doubles (test doubles: the shipped binary runs on Postgres). `make bdd` runs
// it with `go test ./... -run TestFeatures`; the runner lives here, not at the
// module root, because the root holds no Go file.
package features_test

import (
	"context"
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// TestFeatures runs every Gherkin feature in this directory. Strict makes an
// undefined or pending step fail the suite instead of passing silently.
//
// Scenarios tagged @known-bug describe the documented behaviour of a rule the
// code does not honour yet: they are excluded by default and run alone with
// BDD_TAGS=@known-bug, where they must fail.
func TestFeatures(t *testing.T) {
	tags := os.Getenv("BDD_TAGS")
	if tags == "" {
		tags = "~@known-bug"
	}
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			Strict:   true,
			Tags:     tags,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// register the steps of every feature area.
func initializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.reset()
		return ctx, nil
	})
	registerNetworkSteps(sc, w)
	registerQuoteSteps(sc, w)
	registerPromiseSteps(sc, w)
	registerRiskSteps(sc, w)
	registerOutcomeSteps(sc, w)
	registerFactSteps(sc, w)
	registerRestSteps(sc, w)
	registerCalibrationSteps(sc, w)
	registerConsolidatedSteps(sc, w)
	registerRulesSteps(sc, w)
}
