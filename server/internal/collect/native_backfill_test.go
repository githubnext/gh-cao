package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type nativeScopeFixture struct {
	installations []githubapp.Installation
	repositories  map[int64][]githubapp.Repository
	failure       error
}

func (nativeScopeFixture) ListInstallations(context.Context) ([]githubapp.Installation, error) {
	return nil, errors.New("native cold start fell back to unbounded installation enumeration")
}

func (nativeScopeFixture) ListRepositories(context.Context, int64) ([]githubapp.Repository, error) {
	return nil, errors.New("native cold start fell back to unbounded repository enumeration")
}

func (p nativeScopeFixture) ListInstallationsPage(_ context.Context, page int) ([]githubapp.Installation, int, error) {
	if page != 1 {
		return nil, 0, errors.New("unexpected installation page")
	}
	return p.installations, 0, nil
}

func (p nativeScopeFixture) ListRepositoriesPage(_ context.Context, installation int64, page int) ([]githubapp.Repository, int, githubquota.ResponseQuota, error) {
	if page != 1 {
		return nil, 0, githubquota.ResponseQuota{}, errors.New("unexpected repository page")
	}
	return p.repositories[installation], 0, githubquota.ResponseQuota{}, p.failure
}

func (nativeScopeFixture) QuotaRateLimit(context.Context, int64) (githubquota.ResponseQuota, error) {
	return githubquota.ResponseQuota{}, errors.New("unexpected quota bootstrap")
}

func TestNativeColdStartUsesSharedScopeAndPublicationWithoutLake(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api", "octo/withdrawn"}); err != nil {
		t.Fatal(err)
	}
	task := historicalNativeTask()
	collector := collectnative.Collector{Provider: historicalNativeProvider{time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)}, Store: data.Acquisition()}
	if err := collector.CollectScopedAttempt(t.Context(), collectnative.ScopedAttempt{Identity: collectnative.Identity{
		Repository: task.Repository, InstallationID: 7, RunID: task.RunID, Attempt: task.Attempt,
	}, Epoch: nativeScope(t, enrollment, task.Repository).Epoch}); err != nil {
		t.Fatal(err)
	}
	services := queue.Tasks.(operational.Store).OperationalServices()
	ready := false
	backfill := Backfill{StateStore: services.State, Metadata: services.Collection, Queue: queue, Enrollment: enrollment,
		AcquisitionScopes: enrollment.Authority, NativePublications: data.Acquisition(), WindowDays: 7,
		Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123",
		Enumerator: nativeScopeFixture{installations: []githubapp.Installation{{ID: 7}},
			repositories: map[int64][]githubapp.Repository{7: {{FullName: "octo/api"}, {FullName: "octo/withdrawn", Archived: true}}}},
		ScopeReady: func(context.Context) error { ready = true; return nil }}
	t.Setenv("PATH", "/nonexistent")
	state, err := backfill.Run(t.Context())
	if err != nil || state.Phase != "collecting" || state.LakeReplayed || state.NativePublications != 1 ||
		state.QueuedEnumerations != 1 || state.QueuedRunTasks != 0 || state.Repositories != 1 || !ready {
		t.Fatalf("native cold start retained a legacy prerequisite or dishonest status: %+v %v", state, err)
	}
	nativeCanonicalCount(t, data, "$runs", 1)
	if _, err := enrollment.Authority.CurrentScope(t.Context(), "octo/withdrawn"); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("archived repository retained native scope: %v", err)
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), taskStream, ""); err != nil || stats.Length != 0 {
		t.Fatalf("native cold start admitted file-dependent legacy work: %v %v", stats, err)
	}
	if result, err := backfill.Replay(t.Context()); err != nil || result.Revision != state.Revision {
		t.Fatalf("native rebuild required the filesystem lake: %v %v", result, err)
	}
}

func TestNativeColdStartPreservesScopeOnPartialRepositoryDiscovery(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	services := queue.Tasks.(operational.Store).OperationalServices()
	backfill := Backfill{StateStore: services.State, Metadata: services.Collection, Queue: queue, Enrollment: enrollment,
		AcquisitionScopes: enrollment.Authority, NativePublications: data.Acquisition(),
		Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123",
		Enumerator: nativeScopeFixture{installations: []githubapp.Installation{{ID: 7}}, failure: errors.New("incomplete repository API response")}}
	state, err := backfill.Run(t.Context())
	if err != nil || state.Phase != "partial" || state.EnumerationFailures != 1 || state.Repositories != 1 {
		t.Fatalf("partial discovery became complete or erased scope: %+v %v", state, err)
	}
	if scope := nativeScope(t, enrollment, "octo/api"); scope.InstallationID != 7 {
		t.Fatal("partial discovery changed authoritative scope")
	}
}

type notifyingNativePublications struct {
	collectnative.PublicationConsumer
	done chan int
}

func (p notifyingNativePublications) Replay(ctx context.Context, limit int) (int, error) {
	count, err := p.PublicationConsumer.Replay(ctx, limit)
	if count > 0 && err == nil {
		select {
		case p.done <- count:
		default:
		}
	}
	return count, err
}

func TestNativeRunRoleRecoversPendingPublicationWithoutQueuedTask(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	collector := collectnative.Collector{Provider: historicalNativeProvider{time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)}, Store: data.Acquisition()}
	task := historicalNativeTask()
	if err := collector.CollectScopedAttempt(t.Context(), collectnative.ScopedAttempt{Identity: collectnative.Identity{
		Repository: task.Repository, InstallationID: task.InstallationID, RunID: task.RunID, Attempt: task.Attempt,
	}, Epoch: nativeScope(t, enrollment, task.Repository).Epoch}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	published, stopped := make(chan int, 1), make(chan error, 1)
	worker := RunWorker{Queue: queue, Consumer: "recover-outbox", Collector: collector,
		Publications: notifyingNativePublications{PublicationConsumer: data.Acquisition(), done: published}}
	t.Setenv("PATH", "/nonexistent")
	go func() { stopped <- worker.Run(ctx) }()
	select {
	case count := <-published:
		if count != 1 {
			t.Errorf("recovered %d publication intents, expected one", count)
		}
	case <-ctx.Done():
		t.Error("running native role did not recover its post-ACK publication outbox")
	}
	cancel()
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Fatalf("native role did not honor cancellation: %v", err)
	}
	nativeCanonicalCount(t, data, "$runs", 1)
}
