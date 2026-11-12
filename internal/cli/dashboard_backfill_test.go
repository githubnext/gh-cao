package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDashboardSevenDayWindow(t *testing.T) {
	cmd := dashboardCommand("test", nil, nil)
	window, err := cmd.Flags().GetDuration("window")
	if err != nil || window != 7*24*time.Hour {
		t.Fatalf("default evidence window = %v, %v", window, err)
	}
	options := dashboardOptions{repositories: []string{"octo/api"}, window: window, count: 100}
	if _, _, err := resolveDashboardOptions(cmd, &options, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardHistoryPagesResumeWithoutLosingSameSecondRuns(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cutoff := now.Add(-dashboardMaximumWindow)
	cursor := dashboardHistoryRange(dashboardHistoryCursor{}, cutoff, now)
	calls := 0
	gh := func(_ context.Context, args ...string) (string, string, error) {
		location, err := url.Parse(args[len(args)-1])
		if err != nil {
			t.Fatal(err)
		}
		page, _ := strconv.Atoi(location.Query().Get("page"))
		if page != calls+1 || location.Query().Get("created") != cutoff.Format(time.RFC3339)+".."+now.Format(time.RFC3339) {
			t.Fatalf("history range moved during pagination: %v", args)
		}
		rows := []map[string]any{}
		for i := 0; i < min(2, 3-calls*2); i++ {
			rows = append(rows, map[string]any{"id": 100 + calls*2 + i, "workflow_id": 42,
				"created_at": now.Add(-5 * 24 * time.Hour).Format(time.RFC3339), "run_attempt": 1})
		}
		calls++
		content, err := json.Marshal(map[string]any{"total_count": 3, "workflow_runs": rows})
		return string(content), "", err
	}
	first, next, err := collectDashboardHistory(t.Context(), "octo/api", cursor, 2, gh)
	if err != nil || len(first) != 2 || next.Page != 2 || next.End != cursor.End {
		t.Fatalf("first page checkpoint: %+v %v", next, err)
	}
	if resumed := dashboardHistoryRange(next, cutoff.Add(time.Minute), now.Add(time.Minute)); resumed != next {
		t.Fatal("moving retention cutoff restarted a partially scanned page range")
	}
	partial := dashboardIngestion{Cursors: map[string]dashboardHistoryCursor{"octo/api": next}}
	partial.summarize([]string{"octo/api"}, cutoff.Add(time.Minute), now.Add(time.Minute), dashboardMaximumWindow)
	if partial.ScannedHours != 0 {
		t.Fatal("elapsed wall time was reported as newly scanned history")
	}
	second, next, err := collectDashboardHistory(t.Context(), "octo/api", next, 2, gh)
	if err != nil || len(second) != 1 || !next.End.Before(cutoff) || first[0]["createdAt"] != second[0]["createdAt"] {
		t.Fatalf("same-second page or history frontier lost: %+v %v", next, err)
	}
	state := dashboardIngestion{Cursors: map[string]dashboardHistoryCursor{"octo/api": next}, Status: "waiting"}
	state.summarize([]string{"octo/api"}, cutoff, now, dashboardMaximumWindow)
	if state.Status != "watching" || state.ScannedHours != 168 || state.TargetHours != 168 {
		t.Fatalf("discovery progress is not window coverage: %+v", state)
	}
}

func TestDashboardQuotaPauseIsVisibleThroughCanonicalQuery(t *testing.T) {
	cache := t.TempDir()
	calls := 0
	gh := func(_ context.Context, args ...string) (string, string, error) {
		calls++
		if args[len(args)-1] != "rate_limit" {
			t.Fatalf("low quota admitted remote acquisition: %v", args)
		}
		content, err := json.Marshal(map[string]any{"resources": map[string]any{"core": dashboardQuota{
			Limit: 5000, Used: 4001, Remaining: 999, Reset: time.Now().Add(time.Hour).Unix(),
		}}})
		return string(content), "", err
	}
	logs := func(context.Context, dashboardLogRequest) error {
		t.Fatal("quota pause must not download artifacts")
		return nil
	}
	_, err := collectDashboard(t.Context(), []string{"octo/api"}, cache,
		dashboardOptions{window: dashboardMaximumWindow, count: 100, refresh: time.Minute}, gh, logs)
	if err != nil || calls != 1 {
		t.Fatalf("quota pause did not retain a queryable snapshot: %v", err)
	}
	state, err := readDashboardIngestion(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	record, err := dashboardIngestionRecord(state)
	if err != nil {
		t.Fatal(err)
	}
	dsn := nativeTestSnapshot(t, map[string][]map[string]any{"transactions": {record}})
	db, err := openNativeSnapshot(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	result, err := catalog.runQuery(t.Context(), db, "local-ingestion-progress", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	rows := result["rows"].([]map[string]any)
	if len(rows) != 1 || rows[0]["status"] != "paused" || nativeNumber(rows[0]["api-remaining"]) != 999 ||
		nativeNumber(rows[0]["target-hours"]) != 168 || rows[0]["next-attempt-at"] != rows[0]["api-reset-at"] {
		t.Fatalf("quota pause or progress was lost at canonical query boundary: %v", rows)
	}
}

func TestDashboardHistoryPublicationRetentionAndAttempts(t *testing.T) {
	cache := t.TempDir()
	now := time.Now().UTC()
	options := dashboardOptions{window: dashboardMaximumWindow, count: 100}
	metadata := dashboardObservationMetadata([]string{"octo/api"}, options, now, nil)
	old := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-6 * 24 * time.Hour).Format(time.RFC3339)
	batch := map[string][]map[string]any{
		"transactions": {metadata},
		"runs": {
			{"id": "run:old", "createdAt": old, "attempt": 1},
			{"id": "run:recent", "createdAt": recent, "attempt": 1, "resolvedModel": "observed"},
		},
		"tools": {
			{"id": "tool:old", "runId": "run:old"},
			{"id": "tool:recent", "runId": "run:recent"},
		},
	}
	if err := publishDashboardSnapshot(t.Context(), cache, batch); err != nil {
		t.Fatal(err)
	}
	batch["runs"] = []map[string]any{{"id": "run:recent", "createdAt": recent, "attempt": 1, "status": "completed", "outputTokens": nil}}
	delete(batch, "tools")
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	runs, err := readDashboardCachedCollection(t.Context(), cache, "runs")
	if err != nil || len(runs) != 1 || runs[0]["resolvedModel"] != "observed" {
		t.Fatalf("retention or same-attempt sparse merge lost evidence: %v %v", runs, err)
	}
	if value, present := runs[0]["outputTokens"]; !present || value != nil {
		t.Fatal("explicit null was converted into a missing field during sparse enrichment")
	}
	tools, err := readDashboardCachedCollection(t.Context(), cache, "tools")
	if err != nil || len(tools) != 1 || tools[0]["id"] != "tool:recent" {
		t.Fatalf("run-linked retention removed unrelated evidence: %v %v", tools, err)
	}
	batch["runs"] = []map[string]any{{"id": "run:recent", "createdAt": recent, "attempt": 2, "status": "in_progress"}}
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	runs, err = readDashboardCachedCollection(t.Context(), cache, "runs")
	if err != nil || runs[0]["resolvedModel"] != nil {
		t.Fatalf("prior-attempt run evidence survived: %v %v", runs, err)
	}
	tools, err = readDashboardCachedCollection(t.Context(), cache, "tools")
	if err != nil || len(tools) != 0 {
		t.Fatalf("prior-attempt dependent evidence survived: %v %v", tools, err)
	}
	batch["runs"] = []map[string]any{{"id": "run:recent", "createdAt": recent, "attempt": 1, "resolvedModel": "stale"}}
	batch["tools"] = []map[string]any{{"id": "tool:stale", "runId": "run:recent"}}
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	runs, err = readDashboardCachedCollection(t.Context(), cache, "runs")
	if err != nil || runs[0]["attempt"] != float64(2) || runs[0]["resolvedModel"] != nil {
		t.Fatalf("cached earlier attempt replaced newer retained evidence: %v %v", runs, err)
	}
	tools, err = readDashboardCachedCollection(t.Context(), cache, "tools")
	if err != nil || len(tools) != 0 {
		t.Fatalf("stale cached children were attached to a newer attempt: %v %v", tools, err)
	}
}

func TestDashboardHistorySplitsSearchCeilingAndRejectsIncompletePages(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cursor := dashboardHistoryRange(dashboardHistoryCursor{}, now.Add(-dashboardMaximumWindow), now)
	response := `{"total_count":1200,"workflow_runs":[]}`
	gh := func(context.Context, ...string) (string, string, error) { return response, "", nil }
	rows, next, err := collectDashboardHistory(t.Context(), "octo/api", cursor, 100, gh)
	if err != nil || len(rows) != 0 || !next.Start.After(cursor.Start) || next.End != cursor.End || next.Page != 1 {
		t.Fatalf("search ceiling falsely advanced history coverage: %+v %v", next, err)
	}
	response = `{"total_count":20,"workflow_runs":[]}`
	if _, _, err := collectDashboardHistory(t.Context(), "octo/api", cursor, 100, gh); err == nil {
		t.Fatal("incomplete page advanced the frontier")
	}
	response = `{"total_count":1200,"workflow_runs":[]}`
	cursor.Start = cursor.End
	if _, _, err := collectDashboardHistory(t.Context(), "octo/api", cursor, 100, gh); err == nil {
		t.Fatal("unrepresentable single-second history was silently skipped")
	}
}

func TestDashboardQuotaReservesHeadroomAndSharedPassCeiling(t *testing.T) {
	for _, used := range []int{200, 4000} {
		t.Run(strconv.Itoa(used), func(t *testing.T) {
			current, remote := used, 0
			gh := func(_ context.Context, args ...string) (string, string, error) {
				if args[len(args)-1] == "rate_limit" {
					content, err := json.Marshal(map[string]any{"resources": map[string]any{"core": dashboardQuota{
						Limit: 5000, Used: current, Remaining: 5000 - current, Reset: time.Now().Add(time.Hour).Unix(),
					}}})
					return string(content), "", err
				}
				remote++
				return "[]", "", nil
			}
			state := dashboardIngestion{}
			budget, err := newDashboardAcquisition(t.Context(), gh, &state)
			if used == 4000 {
				if !errors.Is(err, errDashboardQuota) || remote != 0 || state.Reserve != 1000 {
					t.Fatalf("quota reserve was not protected: %+v %v", state, err)
				}
				return
			}
			if err != nil || budget.ceiling != 300 {
				t.Fatalf("pass ceiling was not bounded: %+v %v", budget, err)
			}
			current = 300
			if _, _, err := budget.exec(t.Context(), "run", "list", "--limit", "100"); !errors.Is(err, errDashboardQuota) || remote != 0 {
				t.Fatal("discovery consumed points past the shared pass ceiling")
			}
			current = 299
			if err := budget.check(t.Context(), 2); !errors.Is(err, errDashboardQuota) {
				t.Fatal("multi-page read was admitted without enough headroom")
			}
		})
	}
}

func TestDashboardHistoryPersistsAcrossPassesAndFailures(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second).Add(-time.Second)
	cache := t.TempDir()
	base := dashboardTestGH(t, now)
	page := 0
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		if args[0] != "api" || !strings.Contains(args[len(args)-1], "/actions/runs?") {
			return base(ctx, args...)
		}
		page++
		rows := []map[string]any{{
			"id": 900 + page, "workflow_id": 42, "run_attempt": 1, "status": "completed", "conclusion": "success",
			"created_at":     now.Add(-5 * 24 * time.Hour).Format(time.RFC3339),
			"run_started_at": now.Add(-5 * 24 * time.Hour).Format(time.RFC3339),
		}}
		content, err := json.Marshal(map[string]any{"total_count": 2, "workflow_runs": rows})
		return string(content), "", err
	}
	options := dashboardOptions{window: dashboardMaximumWindow, count: 1, refresh: time.Minute}
	logs := func(context.Context, dashboardLogRequest) error { return nil }
	for i := 0; i < 2; i++ {
		if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache, options, gh, logs); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := readDashboardCachedCollection(t.Context(), cache, "runs")
	if err != nil || len(runs) != 3 {
		t.Fatalf("earlier history page was replaced: %v %v", runs, err)
	}
	state, err := readDashboardIngestion(t.Context(), cache)
	if err != nil || !state.Cursors["octo/api"].Done {
		t.Fatalf("history checkpoint was not persisted: %+v %v", state, err)
	}
	fail := func(context.Context, dashboardLogRequest) error { return errors.New("acquisition failed") }
	if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache, options, base, fail); err == nil {
		t.Fatal("failed acquisition was hidden")
	}
	failed, err := readDashboardIngestion(t.Context(), cache)
	if err != nil || failed.Status != "failed" || !reflect.DeepEqual(failed.Cursors, state.Cursors) {
		t.Fatalf("failure lost status or advanced history: %+v %v", failed, err)
	}
	after, err := readDashboardCachedCollection(t.Context(), cache, "runs")
	if err != nil || !reflect.DeepEqual(runs, after) {
		t.Fatalf("failure changed retained evidence: %v", err)
	}

}

func readDashboardCachedCollection(ctx context.Context, cache, collection string) ([]map[string]any, error) {
	batch, err := readDashboardArtifactBatch(ctx, cache)
	return batch[collection], err
}

func TestDashboardSeedPassPublishesRecentRunsWithoutAdvancingHistory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second).Add(-time.Second)
	cache := t.TempDir()
	base := dashboardTestGH(t, now)
	listed := ""
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		if args[0] == "api" && strings.Contains(args[len(args)-1], "/actions/runs?") {
			t.Fatalf("seed pass advanced history discovery: %v", args)
		}
		if args[0] == "run" {
			listed = args[slices.Index(args, "--limit")+1]
		}
		return base(ctx, args...)
	}
	logs := func(context.Context, dashboardLogRequest) error {
		t.Fatal("seed pass downloaded artifacts")
		return nil
	}
	options := dashboardSeedOptions(dashboardOptions{window: dashboardMaximumWindow, count: 200, refresh: time.Minute})
	if _, err := collectDashboard(t.Context(), []string{"octo/api", "octo/web"}, cache, options, gh, logs); err != nil {
		t.Fatal(err)
	}
	if listed != strconv.Itoa(dashboardSeedRunCount) {
		t.Fatalf("seed run listing limit = %q", listed)
	}
	runs, err := readDashboardCachedCollection(t.Context(), cache, "runs")
	if err != nil || len(runs) == 0 {
		t.Fatalf("seed pass published no runs: %v %v", runs, err)
	}
	state, err := readDashboardIngestion(t.Context(), cache)
	if err != nil || len(state.Cursors) != 0 || state.NextRepo != 0 {
		t.Fatalf("seed pass advanced durable discovery state: %+v %v", state, err)
	}
}
