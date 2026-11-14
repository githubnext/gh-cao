package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

var errDashboardQuota = errors.New("dashboard acquisition paused to preserve GitHub API quota")

type dashboardHistoryCursor struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Through time.Time `json:"through"`
	Page    int       `json:"page"`
	Done    bool      `json:"done"`
}

type dashboardIngestion struct {
	ID           string                            `json:"id"`
	Kind         string                            `json:"kind"`
	CreatedAt    string                            `json:"createdAt"`
	Status       string                            `json:"ingestionStatus"`
	Message      string                            `json:"ingestionMessage"`
	Repository   string                            `json:"ingestionRepository"`
	ScannedHours float64                           `json:"scannedHours"`
	TargetHours  float64                           `json:"targetHours"`
	Remaining    *int                              `json:"apiRemaining,omitempty"`
	Reserve      int                               `json:"apiReserve,omitempty"`
	ResetAt      string                            `json:"apiResetAt,omitempty"`
	NextAt       string                            `json:"nextAttemptAt,omitempty"`
	NextRepo     int                               `json:"nextRepository"`
	Cursors      map[string]dashboardHistoryCursor `json:"cursors"`
}

type dashboardQuota struct {
	Limit     int   `json:"limit"`
	Used      int   `json:"used"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"`
}

// dashboardPassRequests bounds the core API requests one pass may issue itself.
const dashboardPassRequests = 500

type dashboardAcquisition struct {
	gh      ghExecutor
	ceiling int
	reset   int64
	spent   int
	state   *dashboardIngestion
}

func newDashboardAcquisition(ctx context.Context, gh ghExecutor, state *dashboardIngestion) (*dashboardAcquisition, error) {
	budget := &dashboardAcquisition{gh: gh, state: state}
	quota, err := budget.quota(ctx)
	if err != nil {
		return nil, err
	}
	// All selected repositories and log collectors share this pass ceiling.
	budget.ceiling = min(quota.Limit-state.Reserve, quota.Used+dashboardPassRequests)
	budget.reset = quota.Reset
	if quota.Used >= budget.ceiling {
		return nil, errDashboardQuota
	}
	return budget, nil
}

func (b *dashboardAcquisition) quota(ctx context.Context) (dashboardQuota, error) {
	raw, _, err := b.gh(ctx, "api", "--hostname", "github.com", "rate_limit")
	if err != nil {
		return dashboardQuota{}, errors.New("cannot read GitHub API quota; acquisition paused")
	}
	var response struct {
		Resources struct {
			Core dashboardQuota `json:"core"`
		} `json:"resources"`
	}
	if json.Unmarshal([]byte(raw), &response) != nil {
		return dashboardQuota{}, errors.New("invalid GitHub API quota response")
	}
	quota := response.Resources.Core
	if quota.Limit <= 0 || quota.Used < 0 || quota.Remaining < 0 ||
		quota.Used+quota.Remaining != quota.Limit || quota.Reset <= time.Now().Unix() {
		return dashboardQuota{}, errors.New("invalid or expired GitHub API quota observation")
	}
	b.state.Remaining = &quota.Remaining
	b.state.Reserve = max(1000, (quota.Limit+4)/5)
	b.state.ResetAt = time.Unix(quota.Reset, 0).UTC().Format(time.RFC3339)
	return quota, nil
}

// check admits requests against this pass's own spending and the global
// reserve. Other consumers sharing the token must not exhaust the pass budget.
func (b *dashboardAcquisition) check(ctx context.Context, requests int) error {
	quota, err := b.quota(ctx)
	if err != nil {
		return err
	}
	if quota.Reset != b.reset {
		b.reset, b.spent = quota.Reset, 0
	}
	b.ceiling = min(quota.Limit-b.state.Reserve, quota.Used+dashboardPassRequests-b.spent)
	if quota.Used+requests > b.ceiling || quota.Remaining-requests < b.state.Reserve {
		return errDashboardQuota
	}
	return nil
}

func (b *dashboardAcquisition) exec(ctx context.Context, args ...string) (string, string, error) {
	// gh workflow/run list can paginate internally. Reserve their maximum reads.
	requests := 1
	for index, arg := range args {
		if arg == "--limit" && index+1 < len(args) {
			count, err := strconv.Atoi(args[index+1])
			if err != nil {
				return "", "", err
			}
			requests = (count + 99) / 100
		}
	}
	if err := b.check(ctx, requests); err != nil {
		return "", "", err
	}
	b.spent += requests
	return b.gh(ctx, args...)
}

func dashboardHistoryRange(cursor dashboardHistoryCursor, cutoff, now time.Time) dashboardHistoryCursor {
	if cursor.End.IsZero() || cursor.Done || cursor.End.Before(cutoff) {
		cursor = dashboardHistoryCursor{End: now.Truncate(time.Second), Page: 1}
	}
	if cursor.Through.IsZero() {
		cursor.Through = cursor.End
	}
	if cursor.Start.IsZero() {
		cursor.Start = cutoff.Truncate(time.Second)
		cursor.Page = 1
	}
	return cursor
}

// collectDashboardHistory reads the whole current interval in one pass. After
// splitting, an interval holds at most GitHub's 1000 search results, so this
// is at most ten 100-run pages. Pages already read survive a later quota pause.
func collectDashboardHistory(ctx context.Context, repository string, cursor dashboardHistoryCursor, count int, gh ghExecutor) ([]map[string]any, dashboardHistoryCursor, error) {
	size := min(count, 100)
	var runs []map[string]any
	for progressed := false; ; progressed = true {
		page, next, err := collectDashboardHistoryPage(ctx, repository, cursor, size, gh)
		if err != nil {
			if progressed && errors.Is(err, errDashboardQuota) {
				return runs, cursor, nil
			}
			return nil, cursor, err
		}
		runs = append(runs, page...)
		cursor = next
		if cursor.Page == 1 {
			return runs, cursor, nil
		}
	}
}

// Each page uses a fixed, closed interval; advancing by page, not timestamp,
// preserves runs sharing the same creation second. Split GitHub's 1000-row
// filtered-search ceiling before advancing the durable frontier.
func collectDashboardHistoryPage(ctx context.Context, repository string, cursor dashboardHistoryCursor, size int, gh ghExecutor) ([]map[string]any, dashboardHistoryCursor, error) {
	var response struct {
		Total *int             `json:"total_count"`
		Runs  []map[string]any `json:"workflow_runs"`
	}
	// Splitting costs one request, so narrow within the pass instead of
	// spending a whole refresh interval on each halving.
	for split := false; ; split = true {
		query := url.Values{
			"created":  {cursor.Start.Format(time.RFC3339) + ".." + cursor.End.Format(time.RFC3339)},
			"per_page": {strconv.Itoa(size)}, "page": {strconv.Itoa(cursor.Page)},
		}
		raw, _, err := gh(ctx, "api", "--hostname", "github.com", "repos/"+repository+"/actions/runs?"+query.Encode())
		if err != nil {
			if split && errors.Is(err, errDashboardQuota) {
				return nil, cursor, nil
			}
			return nil, cursor, err
		}
		response.Total, response.Runs = nil, nil
		if json.Unmarshal([]byte(raw), &response) != nil || response.Total == nil || *response.Total < 0 ||
			response.Runs == nil || len(response.Runs) > size {
			return nil, cursor, errors.New("invalid historical Actions run page")
		}
		if *response.Total <= 1000 {
			break
		}
		if cursor.End.Sub(cursor.Start) < time.Second {
			return nil, cursor, errors.New("more than 1000 runs in one creation second; historical discovery is incomplete")
		}
		cursor.Start = cursor.Start.Add((cursor.End.Sub(cursor.Start) / 2).Truncate(time.Second) + time.Second)
		cursor.Page = 1
	}
	expected := min(size, max(0, *response.Total-(cursor.Page-1)*size))
	if len(response.Runs) != expected {
		return nil, cursor, errors.New("historical Actions run page is incomplete; checkpoint retained")
	}
	runs := make([]map[string]any, 0, len(response.Runs))
	for _, row := range response.Runs {
		run := map[string]any{}
		for from, to := range map[string]string{
			"id": "databaseId", "workflow_id": "workflowDatabaseId", "name": "workflowName",
			"display_title": "displayTitle", "status": "status", "conclusion": "conclusion",
			"event": "event", "head_branch": "headBranch", "head_sha": "headSha", "run_attempt": "attempt",
			"created_at": "createdAt", "run_started_at": "startedAt", "updated_at": "updatedAt", "html_url": "url",
		} {
			if value, ok := row[from]; ok {
				run[to] = value
			}
		}
		created, err := time.Parse(time.RFC3339, nativeText(run["createdAt"]))
		id, idErr := strconv.ParseInt(nativeScalar(run["databaseId"]), 10, 64)
		if err != nil || created.Before(cursor.Start) || created.After(cursor.End) ||
			idErr != nil || id < 1 || id == 1<<63-1 {
			return nil, cursor, errors.New("historical Actions run is outside the requested interval or has an invalid identity")
		}
		runs = append(runs, run)
	}
	if len(runs) < size || cursor.Page*size >= *response.Total {
		cursor.End = cursor.Start.Add(-time.Second)
		cursor.Start = time.Time{}
		cursor.Page = 1
	} else {
		cursor.Page++
	}
	return runs, cursor, nil
}

func (s *dashboardIngestion) summarize(repositories []string, cutoff, now time.Time, window time.Duration) {
	s.ID, s.Kind = "local-dashboard-ingestion", "local-dashboard-ingestion"
	s.CreatedAt = now.Format(time.RFC3339Nano)
	s.TargetHours = window.Hours() * float64(len(repositories))
	s.ScannedHours = 0
	done := true
	for _, repository := range repositories {
		cursor := s.Cursors[repository]
		if !cursor.End.IsZero() && !cursor.Through.IsZero() {
			s.ScannedHours += min(window.Hours(), max(0, cursor.Through.Sub(cursor.End).Hours()))
		}
		if !cursor.Done && !cursor.End.IsZero() && cursor.End.Before(cutoff) {
			cursor.Done = true
			s.Cursors[repository] = cursor
		}
		done = done && cursor.Done
	}
	if s.Status == "waiting" && done {
		s.Status = "watching"
		s.Message = "History discovery reached the requested window. Refresh continues; artifact and upstream coverage remain partial."
	}
}

func dashboardPauseMessage(err error) string {
	if errors.Is(err, errDashboardQuota) {
		return "Acquisition paused at the shared pass ceiling or GitHub quota reserve; saved history will resume on the next eligible refresh."
	}
	return fmt.Sprintf("Collection failed; previously published evidence and history checkpoint retained: %v", err)
}
