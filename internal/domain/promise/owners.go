package promise

import (
	"fmt"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// ChooseOwners returns lines with each line's owner re-chosen by the
// OwnerChoice rule (shared.ChooseOwner, ADR 0017) over stock, the per-owner net
// units of each item at one node. A line that no single owner covers is
// ErrGroupSpansNodes: the units of several owners never add up to one line.
// The input lines are not modified.
func ChooseOwners(lines []shared.Line, stock map[string][]shared.OwnerStock) ([]shared.Line, error) {
	out := make([]shared.Line, len(lines))
	for i, l := range lines {
		owner, ok := shared.ChooseOwner(stock[l.CatalogItemID], l.Quantity)
		if !ok {
			return nil, fmt.Errorf("%w: no single owner can ship %s x%d", ErrGroupSpansNodes, l.CatalogItemID, l.Quantity)
		}
		l.OwnerRef = owner
		out[i] = l
	}
	return out, nil
}

// reown replaces the owners of the promise's lines with those of lines, which
// must hold the same items and quantities in the same order; nil keeps the
// current owners.
func (p *Promise) reown(lines []shared.Line) error {
	if lines == nil {
		return nil
	}
	if !shared.SameItems(p.lines, lines) {
		return shared.Invalid("the re-netted lines must be the promise's items and quantities")
	}
	if err := shared.ValidateLines(lines); err != nil {
		return err
	}
	p.lines = append([]shared.Line(nil), lines...)
	return nil
}
