package report

import "slices"

// The request-level coverage fact of ADR 0032: the vocabulary and the checks of
// DeliveryRequestAnswered, hand-mirrored from apis/asyncapi.yaml.

// Request kinds of DeliveryRequestAnswered.
const (
	RequestQuote    = "QUOTE"
	RequestEstimate = "ESTIMATE"
)

// RequestKinds is the vocabulary of the kind of request; a kind outside it is
// poison.
func RequestKinds() []string { return []string{RequestQuote, RequestEstimate} }

// IsRequestKind reports whether kind is in RequestKinds.
func IsRequestKind(kind string) bool { return slices.Contains(RequestKinds(), kind) }

// requestMissing names the first missing field of a DeliveryRequestAnswered.
// The region must be present: the fact is only published for a request whose
// destination region is known, and it never holds more than two characters.
func (e Event) requestMissing() string {
	return firstMissing(
		required{"requestId", e.RequestID != ""},
		required{"destinationRegion", e.Region != "" && len([]rune(e.Region)) <= regionWidth()},
		required{"a known kind", IsRequestKind(e.RequestKind)},
		required{"an itemCount of at least 1", e.Items >= 1},
		required{"a non-negative shortestPromisedDays", e.ShortestDays == nil || *e.ShortestDays >= 0},
	)
}
