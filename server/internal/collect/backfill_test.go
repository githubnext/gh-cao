package collect

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type workflowRunPageKey struct {
	installationID int64
	repository     string
	page           int
}

type workflowRunPage struct {
	runs            []githubapp.WorkflowRun
	nextPage        int
	responseQuota   githubquota.ResponseQuota
	err             error
	rateObservation githubquota.Observation
	rateLimitErr    error
}

type fakeWorkflowRunEnumerator struct {
	pages map[workflowRunPageKey]workflowRunPage
	calls []workflowRunPageKey
}

type fakeRepositoryEnumerator struct {
	installations []githubapp.Installation
	repositories  map[int64][]githubapp.Repository
	failures      map[int64]error
}

type fakeRunQuota struct {
	known        bool
	unavailable  error
	observations []githubquota.Observation
	reservations []githubquota.Reservation
	commits      []githubquota.Observation
	releases     int
	parks        int
}

func (f *fakeRunQuota) Reserve(
	_ context.Context, bucket githubquota.BucketID, request githubquota.ReservationRequest,
) (githubquota.Reservation, error) {
	if request.EstimatedCost != 1 {
		return githubquota.Reservation{}, errors.New("unexpected quota reservation cost")
	}
	if f.unavailable != nil {
		return githubquota.Reservation{}, f.unavailable
	}
	if !f.known {
		return githubquota.Reservation{}, githubquota.ErrUnknown
	}
	reservation := githubquota.Reservation{
		ID: "run-list", Bucket: bucket.Normalize(), Amount: request.EstimatedCost,
	}
	f.reservations = append(f.reservations, reservation)
	return reservation, nil
}

func (f *fakeRunQuota) Observe(
	_ context.Context, _ githubquota.BucketID, observation githubquota.Observation,
) error {
	f.known = true
	f.observations = append(f.observations, observation)
	return nil
}

func (f *fakeRunQuota) Commit(
	_ context.Context, _ githubquota.Reservation, observation githubquota.Observation,
) error {
	f.commits = append(f.commits, observation)
	return nil
}

func (f *fakeRunQuota) Release(context.Context, githubquota.Reservation) error {
	f.releases++
	return nil
}

func (f *fakeRunQuota) Park(context.Context, githubquota.BucketID, time.Time, string) error {
	f.parks++
	return nil
}

func (f fakeRepositoryEnumerator) ListInstallations(context.Context) ([]githubapp.Installation, error) {
	return f.installations, nil
}

func (f fakeRepositoryEnumerator) ListRepositories(
	_ context.Context, installationID int64,
) ([]githubapp.Repository, error) {
	if err := f.failures[installationID]; err != nil {
		return nil, err
	}
	return f.repositories[installationID], nil
}

func (f *fakeWorkflowRunEnumerator) ListWorkflowRuns(
	_ context.Context, installationID int64, repository string, page, perPage int,
) ([]githubapp.WorkflowRun, int, githubquota.ResponseQuota, error) {
	if perPage != runBackfillPageSize {
		return nil, 0, githubquota.ResponseQuota{}, errors.New("unexpected workflow run page size")
	}
	key := workflowRunPageKey{installationID: installationID, repository: repository, page: page}
	f.calls = append(f.calls, key)
	result, ok := f.pages[key]
	if !ok {
		return nil, 0, githubquota.ResponseQuota{}, errors.New("unexpected workflow run page")
	}
	return result.runs, result.nextPage, result.responseQuota, result.err
}

func (f *fakeWorkflowRunEnumerator) QuotaRateLimit(
	context.Context, int64,
) (githubquota.ResponseQuota, error) {
	for _, page := range f.pages {
		if page.rateLimitErr != nil {
			return githubquota.ResponseQuota{
				HasResponse: true, Resource: githubquota.ResourceCore,
			}, page.rateLimitErr
		}
		if !page.rateObservation.ResetAt.IsZero() {
			return githubquota.ResponseQuota{
				HasResponse: true, Resource: githubquota.ResourceCore,
				HasObservation: true, Observation: page.rateObservation,
			}, nil
		}
	}
	return githubquota.ResponseQuota{}, errors.New("unexpected quota bootstrap request")
}

func TestNormalizeEnumeratedRepositoriesCanonicalizesAndCounts(t *testing.T) {
	pushedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	covered := []githubapp.Repository{
		{FullName: "Octo/API", PushedAt: pushedAt},
		{FullName: "octo/tools", PushedAt: pushedAt.Add(time.Hour)},
	}
	names, repositories, skipped := normalizeEnumeratedRepositories(covered)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if want := []string{"octo/api", "octo/tools"}; !equalStrings(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if len(repositories) != 2 {
		t.Fatalf("len(repositories) = %d, want 2", len(repositories))
	}
	if repositories[0].name != "octo/api" || !repositories[0].pushedAt.Equal(pushedAt) {
		t.Fatalf("repositories[0] = %+v, want name=octo/api pushedAt=%v", repositories[0], pushedAt)
	}
	if repositories[1].name != "octo/tools" || !repositories[1].pushedAt.Equal(pushedAt.Add(time.Hour)) {
		t.Fatalf("repositories[1] = %+v", repositories[1])
	}
}

func TestNormalizeEnumeratedRepositoriesDropsInvalidReferences(t *testing.T) {
	covered := []githubapp.Repository{
		{FullName: "octo/api"},
		{FullName: "not-a-repository"},
		{FullName: ""},
		{FullName: "octo/../escape"},
	}
	names, repositories, skipped := normalizeEnumeratedRepositories(covered)
	if skipped != 3 {
		t.Fatalf("skipped = %d, want 3", skipped)
	}
	if want := []string{"octo/api"}; !equalStrings(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if len(repositories) != 1 || repositories[0].name != "octo/api" {
		t.Fatalf("repositories = %+v", repositories)
	}
}

func TestNormalizeEnumeratedRepositoriesHandlesNoRepositories(t *testing.T) {
	names, repositories, skipped := normalizeEnumeratedRepositories(nil)
	if len(names) != 0 || len(repositories) != 0 || skipped != 0 {
		t.Fatalf("names=%v repositories=%v skipped=%d, want all empty", names, repositories, skipped)
	}
}

func equalStrings(got, want []string) bool {
	return slices.Equal(got, want)
}

func TestSortByRecencyOrdersMostRecentFirst(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repositories := []enrolledRepository{
		{name: "octo/old", pushedAt: base},
		{name: "octo/newest", pushedAt: base.Add(2 * time.Hour)},
		{name: "octo/mid", pushedAt: base.Add(time.Hour)},
	}
	sortByRecency(repositories)
	want := []string{"octo/newest", "octo/mid", "octo/old"}
	for index, repository := range repositories {
		if repository.name != want[index] {
			t.Fatalf("repositories[%d] = %q, want %q", index, repository.name, want[index])
		}
	}
}

func TestSortByRecencyHandlesEmptyAndSingleton(t *testing.T) {
	empty := []enrolledRepository(nil)
	sortByRecency(empty)
	if len(empty) != 0 {
		t.Fatalf("empty = %v, want empty", empty)
	}
	single := []enrolledRepository{{name: "octo/api"}}
	sortByRecency(single)
	if len(single) != 1 || single[0].name != "octo/api" {
		t.Fatalf("single = %+v", single)
	}
}

func TestEnumerateContinuesAfterInstallationRepositoryFailure(t *testing.T) {
	store, ctx := integrationStore(t)
	backfill := Backfill{
		Enrollment: Enrollment{Metadata: store, Leases: store},
		Enumerator: fakeRepositoryEnumerator{
			installations: []githubapp.Installation{{ID: 1}, {ID: 2}},
			repositories: map[int64][]githubapp.Repository{
				2: {{FullName: "octo/api"}},
			},
			failures: map[int64]error{1: errors.New("installation unavailable")},
		},
	}
	repositories, installations, failures, err := backfill.enumerate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if installations != 2 || failures != 1 {
		t.Fatalf("installations=%d failures=%d, want 2 and 1", installations, failures)
	}
	if len(repositories) != 1 || repositories[0].name != "octo/api" ||
		repositories[0].installationID != 2 {
		t.Fatalf("repositories=%+v, want octo/api from installation 2", repositories)
	}
}

func TestEnqueueHistoricalRunsPaginatesResumesAndPrioritizesNewRuns(t *testing.T) {
	store, ctx := integrationStore(t)
	previousMeter := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(ctx)
		otel.SetMeterProvider(previousMeter)
	})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	run := func(id int64, age int) githubapp.WorkflowRun {
		return githubapp.WorkflowRun{ID: id, Attempt: 1, CreatedAt: base.Add(time.Duration(-age) * time.Hour)}
	}
	failure := errors.New("installation repository API unavailable")
	enumerator := &fakeWorkflowRunEnumerator{pages: map[workflowRunPageKey]workflowRunPage{
		{installationID: 1, repository: "octo/one", page: 1}: {
			runs: []githubapp.WorkflowRun{run(101, 0), run(102, 2)}, nextPage: 2,
		},
		{installationID: 1, repository: "octo/one", page: 2}: {
			runs: []githubapp.WorkflowRun{run(103, 4)},
		},
		{installationID: 2, repository: "octo/two", page: 1}: {
			runs: []githubapp.WorkflowRun{run(201, 1), run(202, 3)},
		},
		{installationID: 3, repository: "octo/three", page: 1}: {err: failure},
	}}
	backfill := Backfill{StateStore: store, Metadata: store, Queue: Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxLength: 100}, RunEnumerator: enumerator}
	repositories := []enrolledRepository{
		{name: "octo/one", installationID: 1},
		{name: "octo/two", installationID: 2},
		{name: "octo/three", installationID: 3},
	}

	queued, failures, err := backfill.enqueueHistoricalRuns(ctx, repositories)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 5 || failures != 1 {
		t.Fatalf("queued=%d failures=%d, want queued=5 failures=1", queued, failures)
	}
	if got, err := store.ReadAttribute(ctx, runBackfillCursorKey, "1:octo/one"); err != nil {
		t.Fatalf("read one cursor: %v", err)
	} else if cursor, err := parseRunBackfillCursor(got); err != nil || !cursor.Complete {
		t.Fatalf("one cursor=%q decoded=%+v err=%v, want complete", got, cursor, err)
	}
	if got, err := store.ReadAttribute(ctx, runBackfillCursorKey, "3:octo/three"); err != nil || got != "" {
		t.Fatalf("failed repository cursor=%q err=%v, want unchanged", got, err)
	}

	enumerator.pages[workflowRunPageKey{installationID: 3, repository: "octo/three", page: 1}] =
		workflowRunPage{runs: []githubapp.WorkflowRun{run(301, 5)}}
	queued, failures, err = backfill.enqueueHistoricalRuns(ctx, repositories)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 1 || failures != 0 {
		t.Fatalf("resume queued=%d failures=%d, want queued=1 failures=0", queued, failures)
	}
	if len(enumerator.calls) != 9 {
		t.Fatalf("enumerator calls=%v, want 9 calls with completed repositories skipped", enumerator.calls)
	}

	if err := store.EnsureQueue(ctx, runTaskStream, "run-backfill-test"); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadTasks(ctx, operational.QueueRead{Queue: runTaskStream, Group: "run-backfill-test", Consumer: "test", Count: 10, Block: 0})
	if err != nil {
		t.Fatal(err)
	}
	gotRunIDs := make([]int64, 0, len(messages))
	for _, message := range messages {
		var task RunTask
		if err := json.Unmarshal([]byte(message.Fields.Task), &task); err != nil {
			t.Fatal(err)
		}
		gotRunIDs = append(gotRunIDs, task.RunID)
	}
	wantRunIDs := []int64{101, 201, 102, 202, 103, 301}
	if !slices.Equal(gotRunIDs, wantRunIDs) {
		t.Fatalf("run task order=%v, want newest-first %v", gotRunIDs, wantRunIDs)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &metrics); err != nil {
		t.Fatal(err)
	}
	assertBackfillMetric(t, metrics, "cao_dashboard.collection.backfill.page.count", 8)
	assertBackfillMetric(t, metrics, "cao_dashboard.collection.backfill.run_discovered.count", 12)
	assertBackfillMetric(t, metrics, "cao_dashboard.collection.backfill.run_task_queued.count", 6)
	assertBackfillMetric(t, metrics, "cao_dashboard.collection.backfill.enumeration_failure.count", 1)
}

func TestRunEnumerationBootstrapsAndReconcilesQuota(t *testing.T) {
	reset := time.Now().Add(time.Hour).UTC()
	initial := githubquota.Observation{Limit: 5000, Remaining: 4800, ResetAt: reset}
	response := githubquota.ResponseQuota{
		HasResponse: true, Resource: githubquota.ResourceCore, HasObservation: true,
		Observation: githubquota.Observation{Limit: 5000, Remaining: 4799, ResetAt: reset},
	}
	enumerator := &fakeWorkflowRunEnumerator{pages: map[workflowRunPageKey]workflowRunPage{
		{installationID: 7, repository: "octo/api", page: 1}: {
			runs: []githubapp.WorkflowRun{{ID: 42, Attempt: 1}}, responseQuota: response,
			rateObservation: initial,
		},
	}}
	quota := &fakeRunQuota{}
	backfill := Backfill{RunEnumerator: enumerator, Quota: quota, QuotaApp: "github-app-123"}

	runs, nextPage, err := backfill.listWorkflowRuns(t.Context(), 7, "octo/api", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != 42 || nextPage != 0 {
		t.Fatalf("runs=%+v nextPage=%d, want run 42 and no next page", runs, nextPage)
	}
	if len(quota.observations) != 1 || quota.observations[0] != initial {
		t.Fatalf("bootstrap observations=%+v, want %+v", quota.observations, initial)
	}
	if len(quota.reservations) != 1 || quota.reservations[0].Bucket !=
		(githubquota.BucketID{App: "github-app-123", Installation: 7, Resource: "core"}) {
		t.Fatalf("reservations=%+v, want an installation-scoped core reservation", quota.reservations)
	}
	if len(quota.commits) != 1 || quota.commits[0] != response.Observation || quota.releases != 0 {
		t.Fatalf("commits=%+v releases=%d, want the response observation committed once",
			quota.commits, quota.releases)
	}
}

func TestRunEnumerationQuotaExhaustionLeavesCursorUnchanged(t *testing.T) {
	store, ctx := integrationStore(t)
	enumerator := &fakeWorkflowRunEnumerator{pages: map[workflowRunPageKey]workflowRunPage{
		{installationID: 7, repository: "octo/api", page: 1}: {
			runs: []githubapp.WorkflowRun{{ID: 42, Attempt: 1}},
		},
	}}
	backfill := Backfill{StateStore: store, Metadata: store, Queue: Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxLength: 100},
		RunEnumerator: enumerator, Quota: &fakeRunQuota{unavailable: githubquota.ErrExhausted},
		QuotaApp: "github-app-123",
	}
	queued, failures, err := backfill.enqueueHistoricalRuns(ctx, []enrolledRepository{
		{name: "octo/api", installationID: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	if queued != 0 || failures != 1 || len(enumerator.calls) != 0 {
		t.Fatalf("queued=%d failures=%d API calls=%d, want no API calls and one deferred failure",
			queued, failures, len(enumerator.calls))
	}
	if cursor, err := store.ReadAttribute(ctx, runBackfillCursorKey, "7:octo/api"); err != nil || cursor != "" {
		t.Fatalf("cursor=%q err=%v, want unchanged", cursor, err)
	}
}

func assertBackfillMetric(
	t *testing.T, metrics metricdata.ResourceMetrics, name string, want int64,
) {
	t.Helper()
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q has data type %T, want int64 sum", name, metric.Data)
			}
			if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != want {
				t.Fatalf("metric %q data points=%+v, want one point with value %d", name, sum.DataPoints, want)
			}
			if sum.DataPoints[0].Attributes.Len() != 0 {
				t.Fatalf("metric %q must not include high-cardinality attributes: %v",
					name, sum.DataPoints[0].Attributes)
			}
			return
		}
	}
	t.Fatalf("metric %q was not recorded", name)
}

func TestFailedBackfillStateSetsPhaseErrorAndCompletedAt(t *testing.T) {
	state := BackfillState{
		Phase:         "enumerating",
		StartedAt:     "2026-01-01T00:00:00Z",
		Installations: 3,
	}
	cause := errors.New("enumeration failed")
	completedAt := "2026-01-01T00:05:00Z"
	failed := failedBackfillState(state, cause, completedAt)
	if failed.Phase != "failed" {
		t.Fatalf("Phase = %q, want %q", failed.Phase, "failed")
	}
	if failed.Error != cause.Error() {
		t.Fatalf("Error = %q, want %q", failed.Error, cause.Error())
	}
	if failed.CompletedAt != completedAt {
		t.Fatalf("CompletedAt = %q, want %q", failed.CompletedAt, completedAt)
	}
	// Fields unrelated to the failure transition must survive untouched.
	if failed.StartedAt != state.StartedAt || failed.Installations != state.Installations {
		t.Fatalf("failed = %+v, want StartedAt/Installations preserved from %+v", failed, state)
	}
}

func TestFailedBackfillStateOverwritesPriorTerminalFields(t *testing.T) {
	state := BackfillState{Phase: "seeding", Error: "", CompletedAt: ""}
	failed := failedBackfillState(state, errors.New("queue unavailable"), "2026-02-02T00:00:00Z")
	if failed.Phase != "failed" || failed.Error != "queue unavailable" || failed.CompletedAt != "2026-02-02T00:00:00Z" {
		t.Fatalf("failed = %+v", failed)
	}
}
