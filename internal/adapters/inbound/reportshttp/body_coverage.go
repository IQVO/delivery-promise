package reportshttp

import (
	"context"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// The promiseCoverage block of GET /reports/quote-conversion: the offers-based
// fields of ADR 0030 and the request-based fields of ADR 0032.

// promiseCoverage reads the offers and the request facts of the range and
// derives the block.
func (s *Server) promiseCoverage(ctx context.Context, rg report.Range) ([]report.CoverageDay, error) {
	offers, err := s.Reader.PromiseCoverage(ctx, rg)
	if err != nil {
		return nil, err
	}
	requests, err := s.Reader.RequestCoverage(ctx, rg)
	if err != nil {
		return nil, err
	}
	return report.PromiseCoverage(offers, requests), nil
}

type withinBody struct {
	Days  int      `json:"days"`
	Count int      `json:"count"`
	Share *float64 `json:"share"`
}

// regionCoverageBody: requests, answeredWithPromise and answeredWithinDays are
// OFFERS-based (ADR 0030); requestsAnswered, requestsWithPromise,
// requestsWithinDays and requestShare are REQUEST-based (ADR 0032), best effort
// and present from the day the request fact was switched on.
type regionCoverageBody struct {
	Region              string       `json:"region"`
	Requests            int          `json:"requests"`
	AnsweredWithPromise int          `json:"answeredWithPromise"`
	AnsweredWithinDays  []withinBody `json:"answeredWithinDays"`

	RequestsAnswered    int          `json:"requestsAnswered"`
	RequestsWithPromise int          `json:"requestsWithPromise"`
	RequestsWithinDays  []withinBody `json:"requestsWithinDays"`
	RequestShare        *float64     `json:"requestShare"`
}

type coverageDayBody struct {
	Day     string               `json:"day"`
	Regions []regionCoverageBody `json:"regions"`
}

// coverageBody is the wire form of the coverage days; never nil, so an empty
// range answers [] and not null.
func coverageBody(days []report.CoverageDay) []coverageDayBody {
	out := make([]coverageDayBody, 0, len(days))
	for _, d := range days {
		regions := make([]regionCoverageBody, 0, len(d.Regions))
		for _, r := range d.Regions {
			regions = append(regions, regionCoverageBody{Region: r.Region, Requests: r.Requests,
				AnsweredWithPromise: r.AnsweredWithPromise, AnsweredWithinDays: withinBodies(r.Within),
				RequestsAnswered: r.RequestsAnswered, RequestsWithPromise: r.RequestsWithPromise,
				RequestsWithinDays: withinBodies(r.RequestsWithin), RequestShare: r.RequestShare})
		}
		out = append(out, coverageDayBody{Day: report.FormatDay(d.Day), Regions: regions})
	}
	return out
}

func withinBodies(in []report.Within) []withinBody {
	out := make([]withinBody, 0, len(in))
	for _, w := range in {
		out = append(out, withinBody{Days: w.Days, Count: w.Count, Share: w.Share})
	}
	return out
}
