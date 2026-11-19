package report

import "time"

// RequestBucket is what the store counts of the request facts (ADR 0031) for
// one UTC day, one region (never empty), whether a promise was answered and the
// shortest promised days (nil: none): the requests answered.
type RequestBucket struct {
	Day          time.Time
	Region       string
	Answered     bool
	ShortestDays *int
	Count        int
}

// addRequests counts the requests of one bucket: all are answered, those with a
// promise are counted as such, and those with shortest promised days within a
// limit are counted for every limit they fit.
func (t *tally) addRequests(b RequestBucket) {
	t.reqAnswered += b.Count
	if b.Answered {
		t.reqPromised += b.Count
	}
	if b.ShortestDays == nil {
		return
	}
	for i, limit := range CoverageDays() {
		if *b.ShortestDays <= limit {
			t.reqWithin[i] += b.Count
		}
	}
}
