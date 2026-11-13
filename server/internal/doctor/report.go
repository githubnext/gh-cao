// Package doctor is the systematic diagnostic check-up for a CAO server
// deployment.
//
// The doctor is read-only. It inspects the selected operational backend and canonical Postgres data, the
// query definitions, the optional collection profile, and the process
// environment, and reports what it found. It never writes to operational state or Postgres, never
// contacts GitHub, and never reads or reports secret material: a credential is
// reported as configured or absent, never by value.
//
// The report is produced once and then rendered, so the text and JSON
// renderings describe exactly the same observations. Check identifiers are
// stable and machine-readable so an agent can key on them; the text rendering
// carries the same identifiers so a human and an agent read one artifact.
package doctor

import (
	"sort"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var reportLog = logger.New("cao:doctor:report")

// Status is one check's outcome.
//
// The four values are ordered by severity, so the worst outcome in a report is
// the report's own status.
type Status string

const (
	// StatusPass means the check observed a healthy state.
	StatusPass Status = "pass"
	// StatusSkip means the check did not apply to this deployment, for
	// example a collection check on an Actions-profile server.
	StatusSkip Status = "skip"
	// StatusWarn means the check observed something an operator should look
	// at, but the system is serving.
	StatusWarn Status = "warn"
	// StatusFail means the check observed a condition that breaks or will
	// break the deployment.
	StatusFail Status = "fail"
)

func (s Status) severity() int {
	switch s {
	case StatusFail:
		return 3
	case StatusWarn:
		return 2
	case StatusSkip:
		return 1
	default:
		return 0
	}
}

// Detail is one observed fact. Details are an ordered slice rather than a map
// so that both renderings present facts in the order the check authored them
// and two runs over the same state produce identical output.
type Detail struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Check is one diagnostic result.
type Check struct {
	// ID is the stable machine-readable identifier, "area.check".
	ID string `json:"id"`
	// Area groups related checks in the rendering.
	Area string `json:"area"`
	// Title is the short human-readable name.
	Title string `json:"title"`
	// Status is the outcome.
	Status Status `json:"status"`
	// Summary is one sentence describing what was observed.
	Summary string `json:"summary"`
	// Details are the supporting facts.
	Details []Detail `json:"details,omitempty"`
	// Remedy tells the reader what to do about a warn or fail. It is omitted
	// for a passing check, because a passing check needs no action.
	Remedy string `json:"remedy,omitempty"`
	// DurationMS is how long the check took, so a slow dependency is visible.
	DurationMS int64 `json:"durationMs"`
}

// Summary counts outcomes across a report.
type Summary struct {
	Total      int    `json:"total"`
	Pass       int    `json:"pass"`
	Warn       int    `json:"warn"`
	Fail       int    `json:"fail"`
	Skip       int    `json:"skip"`
	Status     Status `json:"status"`
	DurationMS int64  `json:"durationMs"`
}

// Report is a complete check-up.
type Report struct {
	// SchemaVersion versions this report's shape, so an agent consuming the
	// JSON can detect a change rather than silently misread it.
	SchemaVersion int `json:"schemaVersion"`
	// Tool identifies the producer.
	Tool string `json:"tool"`
	// Version is the server build version.
	Version string `json:"version"`
	// GeneratedAt is when the check-up ran.
	GeneratedAt string `json:"generatedAt"`
	// Profile is the acquisition profile the environment selects.
	Profile string `json:"profile"`
	// Namespace is the operational namespace inspected.
	Namespace string `json:"namespace"`
	// Deep reports whether expensive probes ran.
	Deep bool `json:"deep"`
	// Checks are ordered by area and then by identifier.
	Checks []Check `json:"checks"`
	// Summary aggregates the checks.
	Summary Summary `json:"summary"`
}

// ReportSchemaVersion versions the JSON rendering.
const ReportSchemaVersion = 2

// areaOrder renders areas from the outside in: the process, then its data
// store, then the data, then what reads the data, then what writes it. A check
// in an unknown area sorts last, so adding an area cannot silently reorder the
// report.
var areaOrder = []string{"runtime", "operational", "data", "query", "collect"}

func areaRank(area string) int {
	for index, name := range areaOrder {
		if name == area {
			return index
		}
	}
	return len(areaOrder)
}

// sortChecks orders checks by area and then by identifier, so the rendering is
// deterministic regardless of the order checks completed in.
func sortChecks(checks []Check) {
	sort.SliceStable(checks, func(left, right int) bool {
		leftRank, rightRank := areaRank(checks[left].Area), areaRank(checks[right].Area)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if checks[left].Area != checks[right].Area {
			return checks[left].Area < checks[right].Area
		}
		return checks[left].ID < checks[right].ID
	})
}

// tallyStatuses counts each check's outcome and resolves the worst status
// among them, skipping StatusSkip because a skipped check is not a problem
// even though it outranks pass for rendering purposes. It is split out from
// summarize so the counting and worst-status resolution can be exercised
// directly, independent of duration handling.
func tallyStatuses(checks []Check) (pass, warn, fail, skip int, worst Status) {
	worst = StatusPass
	for _, check := range checks {
		switch check.Status {
		case StatusPass:
			pass++
		case StatusWarn:
			warn++
		case StatusFail:
			fail++
		case StatusSkip:
			skip++
		}
		if check.Status != StatusSkip && check.Status.severity() > worst.severity() {
			worst = check.Status
		}
	}
	return pass, warn, fail, skip, worst
}

// summarize counts outcomes and resolves the report's overall status to the
// worst check it contains.
func summarize(checks []Check, elapsed time.Duration) Summary {
	pass, warn, fail, skip, worst := tallyStatuses(checks)
	summary := Summary{
		Total:      len(checks),
		Pass:       pass,
		Warn:       warn,
		Fail:       fail,
		Skip:       skip,
		Status:     worst,
		DurationMS: elapsed.Milliseconds(),
	}
	reportLog.Printf("summarized checks total=%d status=%s duration_ms=%d", summary.Total, summary.Status, summary.DurationMS)
	return summary
}

// Failed reports whether the check-up found a breaking condition. With strict
// set, a warning counts as a failure, which is what a gate in continuous
// integration wants.
func (r Report) Failed(strict bool) bool {
	if r.Summary.Fail > 0 {
		return true
	}
	return strict && r.Summary.Warn > 0
}

// Check finds one check by identifier, so a caller can assert on a specific
// observation without scanning the slice.
func (r Report) Check(id string) (Check, bool) {
	for _, check := range r.Checks {
		if check.ID == id {
			return check, true
		}
	}
	return Check{}, false
}

func detail(name string, value string) Detail {
	return Detail{Name: name, Value: strings.TrimSpace(value)}
}
