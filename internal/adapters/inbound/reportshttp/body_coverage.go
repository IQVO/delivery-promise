package reportshttp

import "github.com/claudioed/delivery-promise/internal/analytics/report"

// The promiseCoverage block of GET /reports/quote-conversion (ADR 0030).

type withinBody struct {
	Days  int      `json:"days"`
	Count int      `json:"count"`
	Share *float64 `json:"share"`
}

type regionCoverageBody struct {
	Region              string       `json:"region"`
	Requests            int          `json:"requests"`
	AnsweredWithPromise int          `json:"answeredWithPromise"`
	AnsweredWithinDays  []withinBody `json:"answeredWithinDays"`
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
			within := make([]withinBody, 0, len(r.Within))
			for _, w := range r.Within {
				within = append(within, withinBody{Days: w.Days, Count: w.Count, Share: w.Share})
			}
			regions = append(regions, regionCoverageBody{Region: r.Region, Requests: r.Requests,
				AnsweredWithPromise: r.AnsweredWithPromise, AnsweredWithinDays: within})
		}
		out = append(out, coverageDayBody{Day: report.FormatDay(d.Day), Regions: regions})
	}
	return out
}
