package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	ghaw "github.com/github/gh-aw/pkg/cli"
)

func TestDashboardRemoteCollectionDoesNotDiscoverLocalWorkflows(t *testing.T) {
	t.Chdir(t.TempDir())
	ids := dashboardObservedWorkflowIDs([]dashboardWorkflow{
		{ID: 1, Path: ".github/workflows/first.lock.yml"},
		{ID: 2, Path: ".github/workflows/second.lock.yml"},
		{ID: 3, Path: ".github/workflows/ordinary.yml"},
	}, []map[string]any{
		{"workflowDatabaseId": 2}, {"workflowDatabaseId": 1},
		{"workflowDatabaseId": 3}, {"workflowDatabaseId": 2},
	})
	if !slices.Equal(ids, []string{"2", "1"}) {
		t.Fatalf("unexpected remote workflow scope: %v", ids)
	}
	cache := filepath.Join(t.TempDir(), "cached.jsonl")
	analysis := `{"schema_version":5,"kind":"run","run":{"run_id":123}}`
	writeTestFile(t, cache, analysis+"\n"+`{"kind":"workflow_runs","payload":[]}`+"\n")
	calls, count := 0, 0
	err := downloadDashboardLogs(t.Context(), dashboardLogRequest{
		Repository: "octo/api", Cache: cache, Directory: filepath.Dir(cache),
		WorkflowIDs: ids, Count: 5, ManagedCache: true,
	}, func(ctx context.Context, options ghaw.LogsDownloadOptions) error {
		if options.WorkflowName != ids[calls] || options.RepoOverride != "github.com/octo/api" ||
			!options.Audit || options.Parse || !options.SuppressRender || options.MaxStorageMB != 256 || !options.PruneOlderRuns ||
			!slices.Equal(options.ArtifactSets, []string{"all"}) {
			t.Fatalf("remote rich collection contract was lost: %+v", options)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Minute {
			t.Fatal("workflow targets do not share a bounded collection lifetime")
		}
		content, err := os.ReadFile(cache)
		if err != nil || strings.TrimSpace(string(content)) != analysis {
			t.Fatalf("discovery must be fresh while cached analysis survives: %s, %v", content, err)
		}
		calls++
		count += options.Count
		return nil
	})
	if err != nil || calls != 2 || count != 5 {
		t.Fatalf("shared rich acquisition budget changed: calls=%d count=%d err=%v", calls, count, err)
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("remote collection populated the empty working folder: %v, %v", entries, err)
	}
}

func TestDashboardListingRefreshPreservesInvalidCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cached.jsonl")
	content := "{\"kind\":\"workflow_runs\"}\ninvalid\n"
	writeTestFile(t, cache, content)
	if err := refreshDashboardLogListings(cache); err == nil {
		t.Fatal("invalid cached evidence was silently discarded")
	}

	actual, err := os.ReadFile(cache)
	if err != nil || string(actual) != content {
		t.Fatalf("failed refresh replaced the original cache: %v", err)
	}
}

func TestDashboardCollectionBudgetDoesNotMaskErrors(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(strconv.FormatBool(failed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := errors.New("invalid acquired evidence")
				err := downloadDashboardLogs(t.Context(), dashboardLogRequest{
					Cache: filepath.Join(t.TempDir(), "cached.jsonl"), WorkflowIDs: []string{"1"}, Count: 1,
				}, func(ctx context.Context, _ ghaw.LogsDownloadOptions) error {
					time.Sleep(3 * time.Minute)
					if failed {
						return cause
					}
					return ctx.Err()
				})
				if failed && !errors.Is(err, cause) || !failed && err != nil {
					t.Fatalf("acquisition limit masked an error or rejected bounded partial evidence: %v", err)
				}
			})
		})
	}
}

func TestDashboardLogsShareQuotaCeilingAndHistoryBounds(t *testing.T) {
	calls := 0
	limited := false
	request := dashboardLogRequest{
		Repository: "octo/api", Cache: filepath.Join(t.TempDir(), "cache.jsonl"),
		WorkflowIDs: []string{"1", "2"}, WorkflowCounts: map[string]int{"1": 3, "2": 1}, Count: 4,
		StartDate: "2026-11-05T00:00:00Z", EndDate: "2026-11-06T00:00:00Z",
		BeforeRunID: 101, AfterRunID: 90, MaxAPIUsed: 350, Limited: &limited,
		CheckQuota: func(context.Context, int) error {
			if calls > 0 {
				return errDashboardQuota
			}
			return nil
		},
	}
	err := downloadDashboardLogs(t.Context(), request, func(_ context.Context, options ghaw.LogsDownloadOptions) error {
		calls++
		if options.MaxGitHubAPIRateLimit != 350 || options.Count != 3 ||
			options.BeforeRunID != 101 || options.AfterRunID != 90 ||
			options.StartDate != request.StartDate || options.EndDate != request.EndDate {
			t.Fatalf("collector widened shared quota or historical scope: %+v", options)
		}
		return nil
	})
	if err != nil || calls != 1 || !limited {
		t.Fatalf("quota limit did not preserve partial bounded acquisition: calls=%d limited=%v err=%v", calls, limited, err)
	}
}
