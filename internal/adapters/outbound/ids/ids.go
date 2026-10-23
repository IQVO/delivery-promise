// Package ids is the adapter of ports.IDGenerator: "off-" and "prm-" plus a UUID v4.
package ids

import (
	"github.com/google/uuid"

	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

// UUID mints offer and promise ids.
type UUID struct{}

// NewOfferID implements ports.IDGenerator.
func (UUID) NewOfferID() offer.ID { return offer.ID("off-" + uuid.NewString()) }

// NewPromiseID implements ports.IDGenerator.
func (UUID) NewPromiseID() promise.ID { return promise.ID("prm-" + uuid.NewString()) }
