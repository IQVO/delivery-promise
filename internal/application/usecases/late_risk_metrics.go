package usecases

import "context"

// noLateRiskMetrics is the LateRiskMetrics of a Deps without one.
type noLateRiskMetrics struct{}

func (noLateRiskMetrics) PredictedLateFlagged(context.Context)    {}
func (noLateRiskMetrics) DepartureArrivalIgnored(context.Context) {}
