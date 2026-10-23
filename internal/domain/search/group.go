package search

import (
	"slices"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// GroupLines is ShipmentGrouping: prefer one node for the whole cart; split
// only when no single node covers every line, then repeatedly take the node
// covering the most remaining lines (ties by node id). covers maps a node id to
// the item ids it can ship in the asked quantity. It returns the groups in
// order, or false when some line is covered by no node.
func GroupLines(lines []shared.Line, covers map[string]map[string]bool) ([][]shared.Line, bool) {
	ids := make([]string, 0, len(covers))
	for id := range covers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	remaining := slices.Clone(lines)
	var groups [][]shared.Line
	for len(remaining) > 0 {
		bestID, best := "", 0
		for _, id := range ids {
			if n := countCovered(remaining, covers[id]); n > best {
				bestID, best = id, n
			}
		}
		if best == 0 {
			return nil, false
		}
		var take, rest []shared.Line
		for _, l := range remaining {
			if covers[bestID][l.CatalogItemID] {
				take = append(take, l)
			} else {
				rest = append(rest, l)
			}
		}
		groups = append(groups, take)
		remaining = rest
	}
	return groups, true
}

func countCovered(lines []shared.Line, covered map[string]bool) int {
	n := 0
	for _, l := range lines {
		if covered[l.CatalogItemID] {
			n++
		}
	}
	return n
}

// Handicap is the ScorecardHandicap: it returns the effective handling minutes
// and the number of feasible pickups to skip. Grade A, PROVISIONAL and no
// scorecard add nothing; B multiplies the handling by 1.25 (rounded up); C
// skips one more feasible pickup, D two.
func Handicap(g Grade, handling int) (effective, skip int) {
	switch g {
	case GradeB:
		return (handling*5 + 3) / 4, 0
	case GradeC:
		return handling, 1
	case GradeD:
		return handling, 2
	default:
		return handling, 0
	}
}
