package memory_test

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/repocontract"
)

// Fixture wires one fresh Store into the contract suite.
func Fixture(t *testing.T) repocontract.Fixture {
	t.Helper()
	s := memory.NewStore()
	return repocontract.Fixture{
		UoW: s.UnitOfWork(), Offers: s.Offers(), Promises: s.Promises(), Books: s.Books(), Policies: s.Policies(),
		Network: s.Network(), Shipments: s.Shipments(), Outbox: s.Outbox(), Inbox: s.Inbox(),
		OutboxCount: func() int { return s.Counts().OutboxEvents },
	}
}

func TestMemoryDoublesSatisfyTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, Fixture)
}
