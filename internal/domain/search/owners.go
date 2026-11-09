package search

import "github.com/claudioed/delivery-promise/internal/domain/shared"

// ownerOf applies the OwnerChoice rule (shared.ChooseOwner, ADR 0017) to one
// line at one node: the single owner whose net units cover the line. A nil
// Stock knows no listing.
func (s *searcher) ownerOf(nodeID string, l shared.Line) (string, bool) {
	if s.in.Stock == nil {
		return "", false
	}
	return shared.ChooseOwner(s.in.Stock(nodeID, l.CatalogItemID), l.Quantity)
}

// owned stamps on each ranked candidate the group's lines with the owner that
// covers each of them at the candidate's node. Only nodes that cover every line
// reach a candidate, so every line has an owner.
func (s *searcher) owned(cs []Candidate, lines []shared.Line) []Candidate {
	for i := range cs {
		out := make([]shared.Line, len(lines))
		for j, l := range lines {
			l.OwnerRef, _ = s.ownerOf(cs[i].NodeID, l)
			out[j] = l
		}
		cs[i].Lines = out
	}
	return cs
}
