package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func nativeEnrollmentFixture(t *testing.T, data *postgresx.Store, queue Queue) Enrollment {
	t.Helper()
	store, ok := queue.Tasks.(operational.Store)
	if !ok {
		t.Fatal("fixture requires the real operational store")
	}
	services := store.OperationalServices()
	return Enrollment{Metadata: services.Collection, Leases: services.Leases, Authority: data.NativeEnrollment()}
}

func nativeScope(t *testing.T, enrollment Enrollment, repository string) collectnative.EnrollmentScope {
	t.Helper()
	scope, err := enrollment.Authority.CurrentScope(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func nativeCanonicalCount(t *testing.T, data *postgresx.Store, source string, expected int) {
	t.Helper()
	results, _, err := data.ExecuteSQLPlan(t.Context(), []query.Definition{{Name: "scope", From: source}}, []string{"scope"})
	if err != nil || len(results["scope"].Rows) != expected {
		t.Fatalf("%s after scope transition: expected %d rows, got %v, %v", source, expected, results, err)
	}
}

func TestNativeEnrollmentFencesQueuedEpochAndErasesCanonicalGraph(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	task := historicalNativeTask()
	if err := enrollment.AddRepositories(t.Context(), task.InstallationID, []string{task.Repository}); err != nil {
		t.Fatal(err)
	}
	first := nativeScope(t, enrollment, task.Repository)
	task.ScopeEpoch = first.Epoch
	collector := collectnative.Collector{Provider: historicalNativeProvider{time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)}, Store: data.Acquisition()}
	if _, err := queue.EnqueueRun(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	worker := RunWorker{Queue: queue, Collector: collector, Consumer: "native"}
	if more, err := worker.Step(t.Context()); !more || err != nil {
		t.Fatalf("enrollment-backed acquisition: %t %v", more, err)
	}
	if count, err := data.Acquisition().Replay(t.Context(), 1); err != nil || count != 1 {
		t.Fatalf("canonical publication: %d %v", count, err)
	}
	nativeCanonicalCount(t, data, "$runs", 1)
	coverage, _, err := data.ExecuteSQLPlan(t.Context(), []query.Definition{{Name: "coverage", From: "$audits"}}, []string{"coverage"})
	if err != nil {
		t.Fatal(err)
	}
	missing := map[string]bool{"audit-summary-unavailable": true, "job-logs-unavailable": true,
		"session-unavailable": true, "agentic-tool-metrics-unavailable": true}
	sawSuccess := false
	for _, row := range coverage["coverage"].Rows {
		code, _ := row["code"].(string)
		if row["type"] == "audit.finding" && code == "workflow_succeeded" {
			sawSuccess = true
			continue
		}
		if !missing[code] || row["type"] != "collection.coverage" || row["status"] != "unavailable" || row["timestamp"] != nil {
			t.Fatalf("missing source was hidden or became a timed success-shaped audit: %v", row)
		}
		delete(missing, code)
	}
	if len(missing) != 0 || !sawSuccess {
		t.Fatalf("native acquisition lost source coverage or the valid success finding: missing=%v success=%t", missing, sawSuccess)
	}
	removed, err := enrollment.RemoveRepositories(t.Context(), task.InstallationID, []string{task.Repository})
	if err != nil || len(removed) != 1 {
		t.Fatalf("authoritative withdrawal: %v %v", removed, err)
	}
	nativeCanonicalCount(t, data, "$runs", 0)
	nativeCanonicalCount(t, data, "$audits", 0)
	nativeCanonicalCount(t, data, "$workflows", 0)
	nativeCanonicalCount(t, data, "$repositories", 0)
	if pending, err := data.Acquisition().Publications(t.Context(), 1); err != nil || len(pending) != 0 {
		t.Fatalf("withdrawal retained pending evidence: %v %v", pending, err)
	}
	if err := enrollment.AddRepositories(t.Context(), task.InstallationID, []string{task.Repository}); err != nil {
		t.Fatal(err)
	}
	current := nativeScope(t, enrollment, task.Repository)
	if current.Epoch <= first.Epoch {
		t.Fatal("reenrollment reused the withdrawn scope epoch")
	}
	request := collectnative.ScopedAttempt{Identity: collectnative.Identity{
		Repository: task.Repository, InstallationID: task.InstallationID, RunID: task.RunID, Attempt: task.Attempt,
	}, Epoch: first.Epoch}
	if err := collector.CollectScopedAttempt(t.Context(), request); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("old queued epoch was retargeted to renewed consent: %v", err)
	}
	task.ScopeEpoch = current.Epoch
	if admitted, err := queue.EnqueueRun(t.Context(), task); err != nil || !admitted {
		t.Fatalf("old completed identity suppressed new enrollment: %t %v", admitted, err)
	}
	if more, err := worker.Step(t.Context()); !more || err != nil {
		t.Fatalf("new epoch could not collect: %t %v", more, err)
	}
}

func TestNativeEnrollmentMetadataIndexCannotGrantScopeAfterRollback(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	injected := errors.New("interrupted authoritative publication")
	if err := enrollment.withMutationLock(t.Context(), func(ctx context.Context) error {
		if err := enrollment.addRepositories(ctx, 7, []string{"octo/api"}); err != nil {
			return err
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("scope mutation failure hidden: %v", err)
	}
	if indexed, err := enrollment.Metadata.HasMember(t.Context(), repositoriesKey, "octo/api"); err != nil || !indexed {
		t.Fatalf("fixture did not leave the displaced index: %t %v", indexed, err)
	}
	if enrolled, err := enrollment.Enrolled(t.Context(), "octo/api"); err != nil || enrolled {
		t.Fatalf("non-authoritative index widened scope: %t %v", enrolled, err)
	}
	if coverage, err := enrollment.Coverage(t.Context()); err != nil || coverage.Repositories != 0 {
		t.Fatalf("coverage trusted displaced index: %v %v", coverage, err)
	}
}

func TestNativeEnrollmentRecoversInstallationWithoutOperationalIndex(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.Authority.WithMutation(t.Context(), func(ctx context.Context) error {
		return enrollment.Authority.Enroll(ctx, 7, []string{"octo/api"})
	}); err != nil {
		t.Fatal(err)
	}
	if indexed, err := enrollment.Metadata.HasMember(t.Context(), installationsKey, "7"); err != nil || indexed {
		t.Fatalf("fixture unexpectedly has an operational installation index: %t %v", indexed, err)
	}
	if known, err := enrollment.KnownInstallation(t.Context(), 7); err != nil || !known {
		t.Fatalf("native ownership lost without operational index: %t %v", known, err)
	}
	installations, err := enrollment.Installations(t.Context())
	if err != nil || len(installations) != 1 || installations[0] != 7 {
		t.Fatalf("native installation not recoverable: %v %v", installations, err)
	}
	if coverage, err := enrollment.Coverage(t.Context()); err != nil || coverage.Installations != 1 || coverage.Repositories != 1 {
		t.Fatalf("coverage lost native source authority: %v %v", coverage, err)
	}
	if names, next, err := enrollment.ScanRepositories(t.Context(), "0", 1); err != nil || len(names) != 1 || names[0] != "octo/api" || next != "0" {
		t.Fatalf("native scan did not preserve the enrollment cursor contract: %v %q %v", names, next, err)
	}
}

func TestNativeEnrollmentTransactionFencesCanceledInstance(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	a, b := first.NativeEnrollment(), second.NativeEnrollment()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	staged, resume, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- a.WithMutation(ctx, func(ctx context.Context) error {
			if err := a.Enroll(ctx, 7, []string{"octo/api"}); err != nil {
				return err
			}
			close(staged)
			<-resume
			return a.Enroll(ctx, 7, []string{"octo/late"})
		})
	}()
	select {
	case <-staged:
	case <-t.Context().Done():
		t.Fatal("initial transaction did not stage")
	}
	if err := b.WithMutation(t.Context(), func(context.Context) error { return nil }); !errors.Is(err, collectnative.ErrBusy) {
		t.Fatalf("separate instance bypassed durable mutation ownership: %v", err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = b.WithMutation(t.Context(), func(ctx context.Context) error { return b.Enroll(ctx, 9, []string{"octo/api"}) })
		if !errors.Is(err, collectnative.ErrBusy) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(resume)
	if stale := <-finished; !errors.Is(stale, context.Canceled) {
		t.Fatalf("canceled owner resumed mutations: %v", stale)
	}
	if err != nil {
		t.Fatalf("successor failed after old transaction disconnected: %v", err)
	}
	scope, err := b.CurrentScope(t.Context(), "octo/api")
	if err != nil || scope.InstallationID != 9 {
		t.Fatalf("stale transaction overwrote successor: %v %v", scope, err)
	}
	if _, err := b.CurrentScope(t.Context(), "octo/late"); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("canceled instance admitted late scope: %v", err)
	}
}

func TestNativeBackfillBindsEpochAndSeparatesReenrollmentCursors(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	enumerator := &fakeWorkflowRunEnumerator{pages: map[workflowRunPageKey]workflowRunPage{
		{installationID: 7, repository: "octo/api", page: 1}: {runs: []githubapp.WorkflowRun{{ID: 303, Attempt: 3}}},
	}}
	backfill := Backfill{Metadata: enrollment.Metadata, Enrollment: enrollment, Queue: queue,
		RunEnumerator: enumerator, Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123",
		AcquisitionScopes: enrollment.Authority}
	repositories := []enrolledRepository{{name: "octo/api", installationID: 7}}
	if count, failures, err := backfill.enqueueHistoricalRuns(t.Context(), repositories); err != nil || count != 1 || failures != 0 {
		t.Fatalf("epoch-bound backfill: %d %d %v", count, failures, err)
	}
	leases, err := queue.LeaseRuns(t.Context(), "inspect", 1)
	if err != nil || len(leases) != 1 || leases[0].Task.ScopeEpoch != nativeScope(t, enrollment, "octo/api").Epoch {
		t.Fatalf("backfill did not carry its scope snapshot: %v %v", leases, err)
	}
	if _, err := enrollment.RemoveRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if count, failures, err := backfill.enqueueHistoricalRuns(t.Context(), repositories); err != nil || count != 1 || failures != 0 {
		t.Fatalf("old completion cursor or deduplication suppressed reenrollment: %d %d %v", count, failures, err)
	}
}
