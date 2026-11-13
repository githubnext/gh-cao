package doctor

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var doctorLog = logger.New("cao:doctor")

// Doctor runs the check-up.
//
// Every field the checks need is injected rather than read from a package
// global, so a test can drive the whole report deterministically.
type Doctor struct {
	// Store is the owning process's operational state, not a new memory instance.
	Store   operational.Store
	Backend string
	// Postgres is the dashboard entity store used for data and query checks.
	Postgres *postgresx.Store
	// Namespace identifies the selected operational state.
	Namespace string
	// DatabaseQueriesPath is the canonical entity query document.
	DatabaseQueriesPath string
	// Version is the server build version.
	Version string
	// Deep enables probes that read real data rather than only metadata.
	Deep bool
	// Timeout bounds each individual check, so one unresponsive dependency
	// cannot hang the whole check-up.
	Timeout time.Duration
	// Now and Getenv are injected so a test controls time and environment.
	Now    func() time.Time
	Getenv func(string) string
}

func (d Doctor) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

func (d Doctor) getenv(name string) string {
	if d.Getenv != nil {
		return strings.TrimSpace(d.Getenv(name))
	}
	return strings.TrimSpace(os.Getenv(name))
}

func (d Doctor) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return 10 * time.Second
}

// check is one diagnostic function. Returning an error is how a check reports
// that it could not complete; the runner turns that into a failure rather than
// letting it abort the report, because a check-up that stops at the first
// problem is far less useful than one that reports every problem at once.
type check func(context.Context) Check

// Run performs the check-up and returns the report.
//
// Run never returns an error. A check that cannot complete is reported as a
// failed check, which is the diagnostic answer the caller asked for.
func (d Doctor) Run(ctx context.Context) Report {
	wallStarted := time.Now()
	started := d.now()
	profile := d.profile()
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Tool:          "cao-dashboard doctor",
		Version:       d.Version,
		GeneratedAt:   started.UTC().Format(time.RFC3339),
		Profile:       profile.label,
		Namespace:     d.Namespace,
		Deep:          d.Deep,
	}
	checks := []check{
		d.checkBuild,
		d.checkOperational,
		d.checkOperationalCapabilities,
	}
	checks = append(checks,
		d.checkActiveData,
		d.checkSchemaVersion,
		d.checkIntegrity,
		d.checkSources,
		d.checkQueryDefinitions,
		d.checkSourceReads,
		d.checkCollectionProfile,
		d.checkCollectionSettings,
		d.checkCollectionStorage,
		d.checkEnrollment,
		d.checkQueue,
		d.checkBackfill,
		d.checkBudget,
		d.checkTooling,
	)

	for _, run := range checks {
		checkCtx, cancel := context.WithTimeout(ctx, d.timeout())
		begun := time.Now()
		result := run(checkCtx)
		cancel()
		result.DurationMS = time.Since(begun).Milliseconds()
		report.Checks = append(report.Checks, result)
	}
	sortChecks(report.Checks)
	report.Summary = summarize(report.Checks, time.Since(wallStarted))
	doctorLog.Printf("check-up completed profile=%s checks=%d status=%s duration_ms=%d",
		profile.label, report.Summary.Total, report.Summary.Status, report.Summary.DurationMS)
	return report
}

func (d Doctor) postgresUnavailable(id, area, title string) (Check, bool) {
	if d.Postgres != nil {
		return Check{}, false
	}
	return Check{
		ID: id, Area: area, Title: title, Status: StatusFail,
		Summary: "Postgres is not configured, so this check could not run",
		Remedy:  "configure the dashboard Postgres connection",
	}, true
}

// profileSelection is what the environment says this process is.
type profileSelection struct {
	label      string
	collecting bool
	admitOnly  bool
	conflict   bool
}

func (d Doctor) profile() profileSelection {
	appID := d.getenv("CAO_COLLECT_APP_ID")
	source := d.getenv("CAO_SOURCE_DIRECTORY")
	admitOnly := envTruthy(d.getenv("CAO_COLLECT_ADMIT_ONLY"))
	switch {
	case appID != "" && source != "":
		// The two acquisition profiles are alternatives, never layers. A
		// process configured for both would collect and ingest published
		// snapshots into the same namespace.
		return profileSelection{label: "conflicting (collection and published-snapshot ingestion)", conflict: true, collecting: true, admitOnly: admitOnly}
	case appID != "" && admitOnly:
		return profileSelection{label: "collection (admission only)", collecting: true, admitOnly: true}
	case appID != "":
		return profileSelection{label: "collection (webhook-driven)", collecting: true}
	default:
		return profileSelection{label: "actions (published-snapshot ingestion)"}
	}
}

func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// storeUnavailable rejects unavailable or incomplete operational services.
func (d Doctor) storeUnavailable(id, area, title string) (Check, bool) {
	if operational.CheckStore(d.Store) == nil {
		caps := d.Store.Capabilities()
		collection := area == areaCollect
		if collection && caps.Collection.Scope == operational.ScopeUnsupported {
			return skipped(id, area, title, "collection is unsupported by the selected operational backend"), true
		}
		if err := operational.ValidateOperationalServices(caps, d.Store.OperationalServices(), operational.Requirements{
			SingleProcess: true, AllowVolatile: true, Collection: collection,
		}); err != nil {
			return failed(id, area, title, err), true
		}
		return Check{}, false
	}
	return Check{
		ID: id, Area: area, Title: title, Status: StatusFail,
		Summary: "operational state is unavailable, so this check could not run",
		Remedy:  "inspect the selected backend in its owning process; a separate memory instance cannot diagnose live state",
	}, true
}

func failed(id, area, title string, err error) Check {
	return Check{
		ID: id, Area: area, Title: title, Status: StatusFail,
		Summary: "check could not complete: " + err.Error(),
	}
}

func sortedKeys[V any](values map[string]V) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// humanDuration formats an age so staleness reads at a glance.
func humanDuration(value time.Duration) string {
	if value < time.Minute {
		return fmt.Sprintf("%ds", int(value.Seconds()))
	}
	if value < time.Hour {
		return fmt.Sprintf("%dm", int(value.Minutes()))
	}
	if value < 48*time.Hour {
		return fmt.Sprintf("%.1fh", value.Hours())
	}
	return fmt.Sprintf("%.1fd", value.Hours()/24)
}
