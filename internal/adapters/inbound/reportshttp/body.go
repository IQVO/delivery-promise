package reportshttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// Wire bodies (camelCase, apis/openapi.yaml tag `reports`).

type brokenByCauseBody struct {
	Late     int `json:"late"`
	Lost     int `json:"lost"`
	Returned int `json:"returned"`
}

type latenessBody struct {
	P50 *float64 `json:"p50"`
	P90 *float64 `json:"p90"`
}

type reliabilityDayBody struct {
	Day           string            `json:"day"`
	Tier          string            `json:"tier"`
	Basis         string            `json:"basis"`
	Judged        int               `json:"judged"`
	Kept          int               `json:"kept"`
	Broken        int               `json:"broken"`
	KeptRate      float64           `json:"keptRate"`
	BrokenByCause brokenByCauseBody `json:"brokenByCause"`
	LateByMinutes latenessBody      `json:"lateByMinutes"`
}

type reliabilityReportBody struct {
	From string               `json:"from"`
	To   string               `json:"to"`
	Days []reliabilityDayBody `json:"days"`
}

func reliabilityBody(rg report.Range, rows []report.ReliabilityRow) reliabilityReportBody {
	out := reliabilityReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]reliabilityDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, reliabilityDayBody{
			Day: report.FormatDay(r.Day), Tier: r.Tier, Basis: r.Basis, Judged: r.Judged, Kept: r.Kept, Broken: r.Broken,
			KeptRate:      r.KeptRate,
			BrokenByCause: brokenByCauseBody{Late: r.BrokenLate, Lost: r.BrokenLost, Returned: r.BrokenReturned},
			LateByMinutes: latenessBody{P50: r.LateByP50, P90: r.LateByP90},
		})
	}
	return out
}

type windowMinutesBody struct {
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P90  float64 `json:"p90"`
}

type precisionDayBody struct {
	Day             string            `json:"day"`
	Tier            string            `json:"tier"`
	PromisesMade    int               `json:"promisesMade"`
	WindowMinutes   windowMinutesBody `json:"windowMinutes"`
	ExactDayShare   float64           `json:"exactDayShare"`
	ZoneUnknown     int               `json:"zoneUnknown"`
	RevisedShare    float64           `json:"revisedShare"`
	UnkeepableCount int               `json:"unkeepableCount"`
}

type precisionReportBody struct {
	From string             `json:"from"`
	To   string             `json:"to"`
	Days []precisionDayBody `json:"days"`
}

func precisionBody(rg report.Range, rows []report.PrecisionRow) precisionReportBody {
	out := precisionReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]precisionDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, precisionDayBody{
			Day: report.FormatDay(r.Day), Tier: r.Tier, PromisesMade: r.PromisesMade,
			WindowMinutes: windowMinutesBody{Mean: r.WindowMean, P50: r.WindowP50, P90: r.WindowP90},
			ExactDayShare: r.ExactDayShare, ZoneUnknown: r.ZoneUnknown, RevisedShare: r.RevisedShare, UnkeepableCount: r.Unkeepable,
		})
	}
	return out
}

type conversionDayBody struct {
	Day          string   `json:"day"`
	Tier         string   `json:"tier"`
	OffersQuoted int      `json:"offersQuoted"`
	PromisesMade int      `json:"promisesMade"`
	Conversion   *float64 `json:"conversion"`
}

type conversionReportBody struct {
	From string              `json:"from"`
	To   string              `json:"to"`
	Days []conversionDayBody `json:"days"`
}

func conversionBody(rg report.Range, rows []report.ConversionRow) conversionReportBody {
	out := conversionReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]conversionDayBody, 0, len(rows))}
	for _, r := range rows {
		out.Days = append(out.Days, conversionDayBody{
			Day: report.FormatDay(r.Day), Tier: r.Tier, OffersQuoted: r.OffersQuoted, PromisesMade: r.PromisesMade, Conversion: r.Conversion,
		})
	}
	return out
}

type freshnessBody struct {
	AsOf       *time.Time `json:"asOf"`
	LagSeconds *float64   `json:"lagSeconds"`
}

// problemBaseURI prefixes every problem slug, the same `type` form as cmd/api
// (apis/openapi.yaml Problem.type: https://retailnetwork.example/problems/<slug>).
const problemBaseURI = "https://retailnetwork.example/problems/"

// problem is one RFC 9457 problem type: its status, slug and title.
type problem struct {
	status int
	slug   string
	title  string
}

var (
	invalidRange          = problem{http.StatusBadRequest, "invalid-report-range", "The report range is invalid"}
	notFound              = problem{http.StatusNotFound, "not-found", "Resource not found"}
	methodNotAllowed      = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable"}
	internalError         = problem{http.StatusInternalServerError, "internal-error", "Internal error"}
)

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemBody{Type: problemBaseURI + p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath()})
}

// logSafe strips line breaks so a logged value cannot forge a log line.
var logSafe = strings.NewReplacer("\n", " ", "\r", " ")

// writeInternal logs err and answers a 500 whose detail never leaks it.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "report query failed", "error", logSafe.Replace(err.Error()), "path", logSafe.Replace(r.URL.Path))
	writeProblem(w, r, internalError, "an unexpected error occurred")
}
