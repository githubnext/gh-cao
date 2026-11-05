package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/operational"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type nativeHistoryFixture struct {
	after, before time.Time
	calls         int
}

func (p *nativeHistoryFixture) DiscoveryWorkflows(_ context.Context, scope collectnative.EnrollmentScope, page int) ([]int64, int, error) {
	p.calls++
	if scope.Repository != "octo/api" || scope.InstallationID != 7 {
		return nil, 0, collectnative.ErrIdentity
	}
	switch page {
	case 1:
		return []int64{41, 42}, 2, nil
	case 2:
		return []int64{43}, 0, nil
	default:
		return nil, 0, errors.New("unexpected workflow page")
	}
}

func (p *nativeHistoryFixture) DiscoveryRuns(_ context.Context, scope collectnative.EnrollmentScope, workflow int64, after, before time.Time, page int) ([]collectnative.DiscoveredRun, int, error) {
	p.calls++
	if !after.Equal(p.after) || !before.Equal(p.before) || scope.Repository != "octo/api" || scope.InstallationID != 7 {
		return nil, 0, errors.New("historical query escaped frozen scope/window")
	}
	if workflow == 41 && page == 1 {
		return []collectnative.DiscoveredRun{{Identity: collectnative.Identity{
			Repository: scope.Repository, InstallationID: scope.InstallationID, RunID: 303, Attempt: 3,
		}, CreatedAt: after}}, 2, nil
	}
	if workflow == 42 && page == 1 {
		return []collectnative.DiscoveredRun{{Identity: collectnative.Identity{
			Repository: scope.Repository, InstallationID: scope.InstallationID, RunID: 304, Attempt: 2,
		}, CreatedAt: before}}, 0, nil
	}
	if workflow == 41 && page == 2 || workflow == 43 && page == 1 {
		return nil, 0, nil
	}
	return nil, 0, errors.New("unexpected run page")
}

func (p *nativeHistoryFixture) Runs(context.Context, string, int64, time.Time, func(collectnative.Run) error) error {
	return errors.New("exact historical work widened to repository acquisition")
}

func (p *nativeHistoryFixture) Attempt(_ context.Context, identity collectnative.Identity) (collectnative.Run, error) {
	if identity.Repository != "octo/api" || identity.InstallationID != 7 ||
		(identity.RunID != 303 || identity.Attempt != 3) && (identity.RunID != 304 || identity.Attempt != 2) {
		return collectnative.Run{}, collectnative.ErrIdentity
	}
	at := p.after
	if identity.RunID == 304 {
		at = p.before
	}
	return collectnative.Run{Identity: identity, WorkflowPath: ".github/workflows/test.lock.yml",
		Fields: map[string]any{"created_at": at.Format(time.RFC3339), "updated_at": at.Format(time.RFC3339),
			"status": "completed", "conclusion": "success"}}, nil
}

func (*nativeHistoryFixture) Artifacts(context.Context, collectnative.Run, func(collectnative.Artifact) error) error {
	return nil
}

func TestNativeBackfillFreezesWindowAndResumesAcrossReplicasWithoutFiles(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	queue, dsn, namespace := nativeRunQueue(t)
	queue.MaxLength = 1
	other, err := operationalpostgres.Open(t.Context(), dsn, namespace, operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	peer := queue
	peer.Tasks = other
	enrollment := nativeEnrollmentFixture(t, first, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	fixture := &nativeHistoryFixture{after: before.Add(-7 * 24 * time.Hour), before: before}
	backfill := Backfill{Queue: queue, WindowDays: 7, AcquisitionScopes: enrollment.Authority, runBefore: before}
	repositories := []enrolledRepository{{name: "octo/api", installationID: 7}}
	t.Setenv("PATH", "/nonexistent")
	if count, failures, err := backfill.enqueueHistoricalRuns(t.Context(), repositories); err != nil || count != 1 || failures != 0 {
		t.Fatalf("durable native historical admission: %d %d %v", count, failures, err)
	}
	if fixture.calls != 0 {
		t.Fatal("backfill queried GitHub before persisting its immutable snapshot")
	}
	backfill.runBefore = before.Add(24 * time.Hour)
	if count, _, err := backfill.enqueueHistoricalRuns(t.Context(), repositories); err != nil || count != 0 {
		t.Fatalf("restart replaced the original query window: %d %v", count, err)
	}
	workers := []RunWorker{
		{Queue: queue, Consumer: "first", Collector: collectnative.Collector{Provider: fixture, Store: first.Acquisition()},
			Scopes: first.Acquisition(), Discovery: fixture},
		{Queue: peer, Consumer: "successor", Collector: collectnative.Collector{Provider: fixture, Store: second.Acquisition()},
			Scopes: second.Acquisition(), Discovery: fixture},
	}
	for step := range 12 {
		more, err := workers[step%2].Step(t.Context())
		if err != nil {
			t.Fatalf("separate-instance page/attempt handoff at step %d: %v", step, err)
		}
		if !more {
			break
		}
		if step == 11 {
			t.Fatal("native enumeration did not finish within its bounded page count")
		}
	}
	if count, err := second.Acquisition().Replay(t.Context(), 10); err != nil || count != 2 {
		t.Fatalf("frozen inclusive boundary runs were not published: %d %v", count, err)
	}
	nativeCanonicalCount(t, second, "$runs", 2)
	for _, stream := range []string{runTaskStream, runEnumerationStream, runDeadLetterStream} {
		stats, err := queue.Tasks.QueueStats(t.Context(), stream, queue.group())
		if err != nil || stats.Length != 0 || stats.Pending != 0 {
			t.Fatalf("native page lifecycle left unfinished work in %s: %v %v", stream, stats, err)
		}
	}
}

func TestNativeEnumerationReceiptCannotRegressReclaimedCursor(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	backfill := Backfill{Queue: queue, WindowDays: 7, AcquisitionScopes: enrollment.Authority, runBefore: before}
	if _, _, err := backfill.enqueueHistoricalRuns(t.Context(), []enrolledRepository{{name: "octo/api", installationID: 7}}); err != nil {
		t.Fatal(err)
	}
	old, err := queue.LeaseRuns(t.Context(), "same-name", 1)
	if err != nil || len(old) != 1 {
		t.Fatalf("initial page lease: %v %v", old, err)
	}
	fresh, err := queue.ReclaimRuns(t.Context(), "same-name", 0, 1)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("reclaimed page lease: %v %v", fresh, err)
	}
	fixture := &nativeHistoryFixture{after: before.Add(-7 * 24 * time.Hour), before: before}
	worker := RunWorker{Queue: queue, Consumer: "same-name", Discovery: fixture, Scopes: enrollment.Authority}
	if err := worker.process(t.Context(), fresh[0]); err != nil {
		t.Fatal(err)
	}
	if err := worker.process(t.Context(), old[0]); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("stale worker replaced its successor's persisted cursor: %v", err)
	}
	next, err := queue.LeaseRuns(t.Context(), "inspect", 1)
	if err != nil || len(next) != 1 || next[0].Task.Enumeration.WorkflowID != 41 ||
		len(next[0].Task.Enumeration.RemainingWorkflows) != 1 || next[0].Task.Enumeration.RemainingWorkflows[0] != 42 {
		t.Fatalf("page cursor did not preserve the successor's bounded state: %v %v", next, err)
	}
}
