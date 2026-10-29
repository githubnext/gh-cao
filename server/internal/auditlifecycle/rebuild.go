// Package auditlifecycle detects missing administrative rebuild evidence without
// changing history, consulting replaceable status, or inferring execution results.
package auditlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var scanLog = logger.New("cao:auditlifecycle")

const (
	DefaultGrace = time.Hour
	// MinimumGrace exceeds the rebuild's 30-minute lease and 25-minute work
	// deadline, including bounded terminal evidence cleanup.
	MinimumGrace = 35 * time.Minute
	MaxEvents    = 100000
)

// Reader deliberately excludes append, maintenance, and unbounded verification.
type Reader interface {
	ListAudit(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error)
}

type Options struct {
	AfterSequence int64
	MaxEvents     int
	Grace         time.Duration
	Now           time.Time
}

func (o Options) Validate() error {
	if o.AfterSequence < 0 || o.MaxEvents < 1 || o.MaxEvents > MaxEvents ||
		o.Grace < MinimumGrace || o.Grace > 7*24*time.Hour || o.Now.IsZero() {
		return errors.New("audit lifecycle scan requires after >= 0, max-events 1-100000, grace 35m-168h, and an observation time")
	}
	return nil
}

// Result is an observation of retained rows, not an execution or integrity
// attestation. A bounded scan cannot prove the absence of future appends.
type Result struct {
	Status          string    `json:"status"`
	ObservedAt      time.Time `json:"observed_at"`
	GraceSeconds    int64     `json:"grace_seconds"`
	AfterSequence   int64     `json:"after_sequence"`
	FirstSequence   int64     `json:"first_sequence"`
	LastSequence    int64     `json:"last_sequence"`
	Scanned         int       `json:"scanned"`
	TailReached     bool      `json:"tail_reached"`
	HistoryComplete bool      `json:"history_complete"`
	Completed       int       `json:"completed"`
	Pending         int       `json:"pending"`
	MissingTerminal int       `json:"missing_terminal"`
	Issues          []string  `json:"issues"`
	Findings        []Finding `json:"findings"`
}

type Finding struct {
	RebuildID      string    `json:"rebuild_id"`
	FirstSequence  int64     `json:"first_sequence"`
	LastSequence   int64     `json:"last_sequence"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	Classification string    `json:"classification"`
	TerminalState  string    `json:"terminal_state,omitempty"`
	Issues         []string  `json:"issues"`
}

type lifecycle struct {
	Finding
	admitted, started, completed int
	terminal                     [sha256.Size]byte
}

// Scan reads at most MaxEvents plus one lookahead row, retaining only bounded
// per-ID summaries. It never advances a persistent cursor: a later scan must
// revisit unresolved admissions, including when a previous scan hit its cap.
func Scan(ctx context.Context, reader Reader, options Options) (Result, error) {
	result := Result{
		Status: "incomplete", ObservedAt: options.Now.UTC(), GraceSeconds: int64(options.Grace / time.Second),
		AfterSequence: options.AfterSequence, Issues: []string{}, Findings: []Finding{},
	}
	if err := options.Validate(); err != nil {
		return result, err
	}
	ids := map[string]*lifecycle{}
	cursor := options.AfterSequence
	for {
		if err := ctx.Err(); err != nil {
			addIssue(&result.Issues, "read_failed")
			return result, err
		}
		limit := min(operational.AuditListLimit, options.MaxEvents-result.Scanned+1)
		events, err := reader.ListAudit(ctx, operational.AuditFilter{AfterSequence: cursor, Limit: limit})
		if err != nil {
			addIssue(&result.Issues, "read_failed")
			return result, errors.New("audit lifecycle scan could not read retained events")
		}
		if len(events) > limit {
			addIssue(&result.Issues, "invalid_page")
			return result, errors.New("audit lifecycle reader exceeded the requested page bound")
		}
		for _, event := range events {
			if event.Sequence <= cursor {
				addIssue(&result.Issues, "invalid_sequence")
				return result, errors.New("audit lifecycle reader returned non-increasing sequences")
			}
			if event.Sequence != cursor+1 {
				if result.Scanned == 0 && cursor == 0 {
					addIssue(&result.Issues, "retained_prefix")
				} else {
					addIssue(&result.Issues, "history_gap")
				}
			}
			if result.Scanned == options.MaxEvents {
				addIssue(&result.Issues, "event_limit")
				break
			}
			if result.Scanned == 0 {
				result.FirstSequence = event.Sequence
			}
			cursor = event.Sequence
			result.LastSequence = cursor
			result.Scanned++
			observe(&result, ids, event)
		}
		if slices.Contains(result.Issues, "event_limit") {
			break
		}
		if len(events) < limit {
			result.TailReached = true
			break
		}
	}
	result.HistoryComplete = result.TailReached && options.AfterSequence == 0 &&
		!slices.Contains(result.Issues, "history_gap") && !slices.Contains(result.Issues, "retained_prefix")
	if options.AfterSequence > 0 {
		addIssue(&result.Issues, "selected_suffix")
	}
	for _, entry := range ids {
		classify(&result, entry, options)
		if entry.admitted == 0 {
			addIssue(&entry.Issues, "admission_not_observed")
		}
		if entry.completed == 0 || len(entry.Issues) > 0 {
			slices.Sort(entry.Issues)
			result.Findings = append(result.Findings, entry.Finding)
		}
	}
	slices.SortFunc(result.Findings, func(a, b Finding) int {
		if a.FirstSequence < b.FirstSequence {
			return -1
		}
		if a.FirstSequence > b.FirstSequence {
			return 1
		}
		return 0
	})
	slices.Sort(result.Issues)
	result.Status = resultStatus(result)
	scanLog.Printf("audit lifecycle scan finished: status=%s scanned=%d findings=%d tail_reached=%t", result.Status, result.Scanned, len(result.Findings), result.TailReached)
	return result, nil
}

// classify assigns a lifecycle its classification and updates the result tallies.
func classify(result *Result, entry *lifecycle, options Options) {
	switch {
	case entry.completed > 0:
		entry.Classification = "terminal_observed"
		result.Completed++
	case !result.TailReached || slices.Contains(result.Issues, "history_gap"):
		entry.Classification = "unknown"
	case entry.FirstSeenAt.IsZero() || slices.Contains(entry.Issues, "clock_uncertain"):
		entry.Classification = "unknown"
	case options.Now.Sub(entry.FirstSeenAt) < options.Grace:
		// Recent evidence may still be active; status and lease expiry do
		// not prove execution liveness or termination.
		entry.Classification = "pending"
		result.Pending++
	default:
		entry.Classification = "missing_terminal"
		result.MissingTerminal++
	}
}

// resultStatus reports "incomplete" for partial history or scan issues,
// "attention" for missing terminals or finding issues, and "ok" otherwise.
func resultStatus(result Result) string {
	if !result.HistoryComplete || len(result.Issues) > 0 {
		return "incomplete"
	}
	if result.MissingTerminal > 0 {
		return "attention"
	}
	for _, finding := range result.Findings {
		if len(finding.Issues) > 0 {
			return "attention"
		}
	}
	return "ok"
}

func observe(result *Result, ids map[string]*lifecycle, event operational.AuditEvent) {
	switch event.Event {
	case "admin.rebuild.admitted", "admin.rebuild.started", "admin.rebuild.completed", "admin.rebuild.status_failed":
	default:
		return
	}
	id := event.Attributes["rebuild_id"]
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 32 || len(id) != 64 {
		addIssue(&result.Issues, "invalid_rebuild_id")
		return
	}
	entry := ids[id]
	if entry == nil {
		entry = &lifecycle{Finding: Finding{
			RebuildID: id, FirstSequence: event.Sequence, FirstSeenAt: event.OccurredAt, Issues: []string{},
		}}
		ids[id] = entry
	}
	entry.LastSequence = event.Sequence
	if event.OccurredAt.IsZero() {
		addIssue(&entry.Issues, "invalid_timestamp")
		entry.FirstSeenAt = time.Time{}
	}
	if event.OccurredAt.After(result.ObservedAt.Add(5 * time.Minute)) {
		addIssue(&entry.Issues, "clock_uncertain")
	}
	switch event.Event {
	case "admin.rebuild.admitted":
		entry.admitted++
		if entry.admitted > 1 {
			addIssue(&entry.Issues, "duplicate_admission")
		}
		if entry.started > 0 || entry.completed > 0 {
			addIssue(&entry.Issues, "unexpected_order")
		}
	case "admin.rebuild.started":
		entry.started++
		if entry.started > 1 {
			addIssue(&entry.Issues, "duplicate_start")
		}
		if entry.completed > 0 {
			addIssue(&entry.Issues, "unexpected_order")
		}
	case "admin.rebuild.completed":
		state := event.Attributes["state"]
		valid := state == "succeeded" && event.Outcome == "success" ||
			(state == "failed" || state == "canceled") && event.Outcome == "failure"
		if !valid {
			addIssue(&entry.Issues, "invalid_terminal")
			return
		}
		// Compare without returning arbitrary record attributes in diagnostics.
		terminal := sha256.Sum256([]byte(state + "\x00" + event.Attributes["revision"] + "\x00" + event.Attributes["data_revision"]))
		if entry.completed > 0 {
			addIssue(&entry.Issues, "duplicate_terminal")
			if entry.terminal != terminal {
				addIssue(&entry.Issues, "conflicting_terminal")
				entry.TerminalState = ""
			}
		} else {
			entry.TerminalState = state
			entry.terminal = terminal
		}
		entry.completed++
	case "admin.rebuild.status_failed":
		addIssue(&entry.Issues, "status_write_failed")
	}
}

func addIssue(issues *[]string, issue string) {
	if !slices.Contains(*issues, issue) {
		*issues = append(*issues, issue)
	}
}
