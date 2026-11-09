package shared

// OperatorOwned is the ownerRef of an operator-owned listing: the empty string,
// never NULL in a key (ADR 0017).
const OperatorOwned = ""

// OwnerStock is the net units one owner can ship of one item at one node:
// the owner's listing figure minus the provisional and confirmed holds taken
// against that same (node, item, ownerRef) key (ADR 0015, ADR 0017).
type OwnerStock struct {
	OwnerRef  string
	Available int
}

// ChooseOwner is the OwnerChoice rule of ADR 0017, shared verbatim with
// order-sourcing. A line of qty units is covered by ONE owner, never by a sum
// across owners:
//
//   - operator-owned ("") first, if it covers;
//   - otherwise the covering seller owner with the highest available (after
//     netting);
//   - ties go to the lowest ownerRef in byte order;
//   - no covering owner: ok is false, the node is not a candidate for the line.
//
// stock holds one entry per owner; the order of the entries does not matter.
func ChooseOwner(stock []OwnerStock, qty int) (ownerRef string, ok bool) {
	best, found := OwnerStock{}, false
	for _, s := range stock {
		if s.Available < qty {
			continue
		}
		if s.OwnerRef == OperatorOwned {
			return OperatorOwned, true
		}
		if !found || s.Available > best.Available || s.Available == best.Available && s.OwnerRef < best.OwnerRef {
			best, found = s, true
		}
	}
	return best.OwnerRef, found
}
