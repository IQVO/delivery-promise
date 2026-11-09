package usecases_test

import (
	"context"

	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

// The late-risk additions to the PromiseRepository fault wrapper of faults_test.go
// (ADR 0021); like every wrapped port call they can be failed by index.

func (fw *fPromiseRepository) ListAtRiskPage(ctx context.Context, offset, limit int) ([]promise.ID, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListAtRiskPage(ctx, offset, limit)
}

func (fw *fPromiseRepository) ListConfirmed(ctx context.Context, after promise.ID, limit int) ([]promise.ID, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListConfirmed(ctx, after, limit)
}
