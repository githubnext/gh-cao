package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type runtimeInventoryProvider struct {
	raw        []byte
	provenance collectnative.InventoryProvenance
	sources    map[string]model.Source
	calls      []string
	failure    error
}

func (p *runtimeInventoryProvider) BuildInventory(_ context.Context, scope collectnative.EnrollmentScope, snapshot collectnative.InventorySnapshot) (collectnative.InventoryEvidence, error) {
	p.calls = append(p.calls, scope.Repository)
	if p.failure != nil {
		return collectnative.InventoryEvidence{}, p.failure
	}
	sources := map[string]model.Source{}
	for name, source := range p.sources {
		sources[name] = source
	}
	owner, name, _ := strings.Cut(scope.Repository, "/")
	sources["repositories"] = model.Source{Source: "repositories",
		Rows: []model.Row{{"organization": owner, "repository": name}}}
	if scope.Repository != snapshot.ControlRepository() {
		sources["workflows"] = model.Source{Source: "workflows", Rows: []model.Row{}}
	}
	return collectnative.InventoryEvidence{Provenance: p.provenance, Digest: snapshot.Digest,
		SourceSnapshot: p.raw, Scope: scope, ObservedAt: time.Now().UTC(), Sources: sources}, nil
}

func runtimeInventorySnapshot(t *testing.T) (collectnative.InventorySnapshot, *runtimeInventoryProvider) {
	t.Helper()
	provenance := collectnative.InventoryProvenance{SourceRepository: "octo/z-control",
		SourceSHA: strings.Repeat("a", 40), PolicySHA: strings.Repeat("b", 40)}
	raw := []byte(`{"version":1,"controlRepository":"octo/z-control","sourceRepository":"octo/z-control",
		"sourceSHA":"` + provenance.SourceSHA + `","policySHA":"` + provenance.PolicySHA + `",
		"policyResolution":"available","sources":{
		"campaigns":{"source":"campaigns","rows":[{"campaign":"security","campaign-name":"Security"}]},
		"repositories":{"source":"repositories","rows":[{"organization":"octo","repository":"z-control"}]},
		"workflows":{"source":"workflows","rows":[{"organization":"octo","repository":"z-control",
			"workflow":".github/workflows/test.md","workflow-role":"worker",
			"workflow-source-kind":"source-managed","campaign":"security"}]},
		"configuration-policy":{"source":"configuration-policy","rows":[{"path":".github/workflows/cao.json",
			"raw":"reviewed","diagnostics":[{"severity":"valid"}]}]}
	}}`)
	sum := sha256.Sum256(raw)
	snapshot, err := collectnative.NewInventorySnapshot(raw, hex.EncodeToString(sum[:]), provenance)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Sources map[string]model.Source `json:"sources"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return snapshot, &runtimeInventoryProvider{raw: raw, provenance: provenance, sources: document.Sources}
}

func TestNativeInventoryColdStartPreparesControlBeforeReplay(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api", "octo/z-control"}); err != nil {
		t.Fatal(err)
	}
	task := historicalNativeTask()
	collector := collectnative.Collector{Provider: historicalNativeProvider{
		recorded: time.Now().UTC().Format(time.RFC3339)}, Store: data.Acquisition()}
	if err := collector.CollectScopedAttempt(t.Context(), collectnative.ScopedAttempt{
		Identity: collectnative.Identity{Repository: task.Repository, InstallationID: 7, RunID: task.RunID, Attempt: task.Attempt},
		Epoch:    nativeScope(t, enrollment, task.Repository).Epoch,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, provider := runtimeInventorySnapshot(t)
	inventory, err := NewNativeInventoryService(t.Context(), snapshot, provider, data.Acquisition())
	if err != nil {
		t.Fatal(err)
	}
	strict, err := data.Acquisition().RequireInventory(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	services := queue.Tasks.(operational.Store).OperationalServices()
	backfill := Backfill{StateStore: services.State, Metadata: services.Collection, Queue: queue,
		Enrollment: enrollment, AcquisitionScopes: enrollment.Authority, NativePublications: strict,
		NativeInventory: inventory, Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123",
		Enumerator: nativeScopeFixture{installations: []githubapp.Installation{{ID: 7}},
			repositories: map[int64][]githubapp.Repository{7: {{FullName: "octo/api"}, {FullName: "octo/z-control"}}}}}
	t.Setenv("PATH", "/nonexistent")
	if _, err := backfill.Replay(t.Context()); err == nil || len(provider.calls) != 0 {
		t.Fatal("direct replay fetched inventory or bypassed missing strict readiness")
	}
	provider.failure = errors.New("GitHub unavailable")
	if _, err := backfill.Run(t.Context()); err == nil {
		t.Fatal("first cold start activated without valid control inventory")
	}
	if len(provider.calls) != 1 || provider.calls[0] != "octo/z-control" {
		t.Fatalf("targets refreshed before the control source: %v", provider.calls)
	}
	nativeCanonicalCount(t, data, "$runs", 0)
	provider.calls, provider.failure = nil, nil
	state, err := backfill.Run(t.Context())
	if err != nil || state.Phase != "collecting" || state.NativePublications != 1 {
		t.Fatalf("cold start failed to prepare inventory before strict replay: %+v %v", state, err)
	}
	if len(provider.calls) != 2 || provider.calls[0] != "octo/z-control" || provider.calls[1] != "octo/api" {
		t.Fatalf("source-first scoped preparation changed: %v", provider.calls)
	}
	nativeCanonicalCount(t, data, "$runs", 1)
	provider.failure = errors.New("GitHub unavailable again")
	if _, err := backfill.Replay(t.Context()); err != nil || len(provider.calls) != 2 {
		t.Fatalf("direct replay was not credential-free: %v %v", provider.calls, err)
	}
	state, err = backfill.Run(t.Context())
	if err != nil || state.Phase != "partial" || state.EnumerationFailures != 2 {
		t.Fatalf("valid retained inventory failed refresh recovery: %+v %v", state, err)
	}
	pending, err := data.Acquisition().PendingInventoryScopes(t.Context(), 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("failed refresh retry intent was dropped: %v %v", pending, err)
	}
}

type inventoryRetryFixture struct {
	called  chan time.Time
	blocked bool
}

func (f inventoryRetryFixture) RetryPending(ctx context.Context, limit int) (int, error) {
	if limit != 64 {
		return 0, errors.New("unbounded inventory retry")
	}
	select {
	case f.called <- time.Now():
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if f.blocked {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return 0, errors.New("transient inventory provider failure")
}

func TestNativeInventoryRetryCadenceAndCancellation(t *testing.T) {
	retryer := inventoryRetryFixture{called: make(chan time.Time, 3)}
	worker := NativeWorker{InventoryRetryInterval: time.Nanosecond}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- worker.retryInventory(ctx, retryer) }()
	var first, second time.Time
	select {
	case first = <-retryer.called:
	case <-ctx.Done():
		t.Fatal("initial durable inventory retry was not scheduled")
	}
	select {
	case second = <-retryer.called:
	case <-ctx.Done():
		t.Fatal("transient inventory error stopped subsequent retries")
	}
	if second.Sub(first) < time.Second {
		t.Fatal("inventory retry busy-spun despite minimum cadence")
	}
	cancel()
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Fatalf("inventory retry did not join on cancellation: %v", err)
	}
}

type blockingInventoryHooks struct {
	nativeRuntimeHooks
	inventoryRetryFixture
}

func TestNativeInventoryRetryPreservesRunAndControlRoleLiveness(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (Admitter{Enrollment: enrollment, Queue: queue}).AdmitDelivery(
		t.Context(), "workflow_run", []byte(nativeCompletedPayload), "inventory-runtime-completion", time.Hour); err != nil {
		t.Fatal(err)
	}
	client := nativeScopeFixture{installations: []githubapp.Installation{{ID: 7}},
		repositories: map[int64][]githubapp.Repository{7: {{FullName: "octo/api"}}}}
	hooks := blockingInventoryHooks{nativeRuntimeHooks{values: make(chan collectnative.EnrollmentScope, 1)},
		inventoryRetryFixture{called: make(chan time.Time, 1), blocked: true}}
	published := make(chan int, 1)
	worker := NativeWorker{Queue: queue, Enrollment: enrollment, Client: client,
		Inventory: hooks, Values: hooks, Outcomes: hooks, Consumer: "inventory-runtime",
		ScopeRefresher: ScopeRefresher{Client: client, Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123"},
		Runs: RunWorker{Collector: collectnative.Collector{
			Provider: historicalNativeProvider{recorded: time.Now().UTC().Format(time.RFC3339)}, Store: data.Acquisition()},
			Discovery: emptyNativeHistory{}, Publications: notifyingNativePublications{PublicationConsumer: data.Acquisition(), done: published}}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	select {
	case <-hooks.called:
	case <-ctx.Done():
		t.Error("native runtime did not start durable inventory retry")
	}
	select {
	case <-published:
	case <-ctx.Done():
		t.Error("blocked inventory provider stalled the run role")
	}
	select {
	case <-hooks.values:
	case <-ctx.Done():
		t.Error("blocked inventory provider stalled the control role")
	}
	cancel()
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Fatalf("native roles failed to join on cancellation: %v", err)
	}
}

func TestNativeReconcileResultCountsAtomicAdmissions(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	for installation, repository := range map[int64]string{7: "octo/api", 9: "octo/withdrawn"} {
		if err := enrollment.AddRepositories(t.Context(), installation, []string{repository}); err != nil {
			t.Fatal(err)
		}
	}
	worker := nativeEventWorker(queue, enrollment, &nativeRefreshFixture{})
	worker.RefreshInterval = 24 * time.Hour
	result, err := worker.ReconcileResult(t.Context())
	if err != nil || result.Installations != 1 || result.RefreshesQueued != 1 || result.WithdrawalsQueued != 1 {
		t.Fatalf("discovery counts did not reflect atomic native admissions: %+v %v", result, err)
	}
	result, err = worker.ReconcileResult(t.Context())
	if err != nil || result.Installations != 1 || result.RefreshesQueued != 0 || result.WithdrawalsQueued != 0 {
		t.Fatalf("duplicate reconciliation reported newly enqueued work: %+v %v", result, err)
	}
	if err := worker.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), nativeControlStream, ""); err != nil || stats.Length != 2 {
		t.Fatalf("native result/wrapper reconciliation duplicated control work: %+v %v", stats, err)
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), taskStream, ""); err != nil || stats.Length != 0 {
		t.Fatalf("native discovery result enqueued legacy work: %+v %v", stats, err)
	}
}
