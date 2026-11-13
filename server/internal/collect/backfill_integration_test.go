package collect

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestBackfillRunsEnumerationAndQueuesWork(t *testing.T) {
	store, ctx := integrationStore(t)
	pushedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	enumerator := fakeRepositoryEnumerator{
		installations: []githubapp.Installation{{ID: 7}},
		repositories: map[int64][]githubapp.Repository{
			7: {{FullName: "Octo/API", PushedAt: pushedAt}},
		},
	}
	runEnumerator := &fakeWorkflowRunEnumerator{pages: map[workflowRunPageKey]workflowRunPage{
		{installationID: 7, repository: "octo/api", page: 1}: {
			runs: []githubapp.WorkflowRun{{
				ID: 42, Attempt: 2, CreatedAt: pushedAt.Add(time.Minute),
			}},
		},
	}}
	enrollment := Enrollment{Metadata: store, Leases: store}
	backfill := Backfill{StateStore: store, Metadata: store, Enrollment: enrollment, Queue: Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxLength: 100},
		Lake: Lake{Directory: t.TempDir()}, Enumerator: enumerator,
		RunEnumerator: runEnumerator,
	}

	result, err := backfill.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != "collecting" || result.Installations != 1 ||
		result.Repositories != 1 || result.QueuedRepositories != 1 ||
		result.QueuedRunTasks != 1 || result.EnumerationFailures != 0 ||
		result.LakeReplayed || result.CompletedAt == "" {
		t.Fatalf("backfill result = %+v, want successful single-repository cold start", result)
	}

	state, err := backfill.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state != result {
		t.Fatalf("persisted state = %+v, want run result %+v", state, result)
	}
	enrolled, err := enrollment.Enrolled(ctx, "OCTO/API")
	if err != nil || !enrolled {
		t.Fatalf("repository enrollment = %t, err=%v; want enrolled", enrolled, err)
	}
	if installation, err := enrollment.InstallationFor(ctx, "octo/api"); err != nil || installation != 7 {
		t.Fatalf("repository installation = %d, err=%v; want 7", installation, err)
	}

	leases, err := (Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}).Lease(ctx, "backfill-integration", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("repository task leases = %d, want 1", len(leases))
	}
	if task := leases[0].Task; task.Repository != "octo/api" ||
		task.InstallationID != 7 || task.Reason != "backfill" {
		t.Fatalf("repository task = %+v, want backfill task for octo/api", task)
	}

	if err := store.EnsureQueue(ctx, runTaskStream, "backfill-integration"); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadTasks(ctx, operational.QueueRead{Queue: runTaskStream, Group: "backfill-integration", Consumer: "test", Count: 10, Block: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("historical run task messages = %d, want 1", len(messages))
	}
	var runTask RunTask
	if err := json.Unmarshal([]byte(messages[0].Fields.Task), &runTask); err != nil {
		t.Fatal(err)
	}
	if runTask.Key != "octo/api:42:2" || runTask.Repository != "octo/api" ||
		runTask.InstallationID != 7 || runTask.RunID != 42 || runTask.Attempt != 2 ||
		!runTask.CreatedAt.Equal(pushedAt.Add(time.Minute)) {
		t.Fatalf("historical run task = %+v, want run 42 attempt 2 for octo/api", runTask)
	}

	cursor, err := store.ReadAttribute(ctx, runBackfillCursorKey, "7:octo/api")
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := parseRunBackfillCursor(cursor); err != nil || !parsed.Complete {
		t.Fatalf("historical run cursor = %q, decoded=%+v err=%v; want complete", cursor, parsed, err)
	}
}
