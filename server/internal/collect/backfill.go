package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var backfillLog = logger.New("cao:collect:backfill")

const backfillMeterName = "github.com/githubnext/gh-cao/server"

const (
	backfillStateKey     = "collect:backfill"
	runBackfillCursorKey = "collect:run-backfill-cursors"
	runBackfillPageSize  = 100
	runBackfillComplete  = "done"
)

// Enumerator is the App-level enumeration the cold start needs. It is an
// interface so tests substitute a fake GitHub API.
type Enumerator interface {
	ListInstallations(ctx context.Context) ([]githubapp.Installation, error)
	ListRepositories(ctx context.Context, installationID int64) ([]githubapp.Repository, error)
}

// WorkflowRunEnumerator lists one page of historical workflow runs.
type WorkflowRunEnumerator interface {
	ListWorkflowRuns(
		ctx context.Context, installationID int64, repository string, page, perPage int,
	) ([]githubapp.WorkflowRun, int, githubquota.ResponseQuota, error)
}

type WorkflowRunQuotaRefresher interface {
	QuotaRateLimit(ctx context.Context, installationID int64) (githubquota.ResponseQuota, error)
}

type WorkflowRunWindowEnumerator interface {
	ListWorkflowRunsWindow(
		context.Context, int64, string, int, int, time.Time, time.Time,
	) ([]githubapp.WorkflowRun, int, githubquota.ResponseQuota, error)
}

type RunQuotaService interface {
	Reserve(
		ctx context.Context, bucket githubquota.BucketID, request githubquota.ReservationRequest,
	) (githubquota.Reservation, error)
	Observe(ctx context.Context, bucket githubquota.BucketID, observation githubquota.Observation) error
	Commit(
		ctx context.Context, reservation githubquota.Reservation, observation githubquota.Observation,
	) error
	Release(ctx context.Context, reservation githubquota.Reservation) error
	Park(ctx context.Context, bucket githubquota.BucketID, until time.Time, reason string) error
}

// Backfill performs resumable cold start.
//
// It replays the evidence lake first, because a populated lake repopulates an
// empty canonical database with zero GitHub requests. GitHub enumeration then
// repairs enrollment and admits durable repository and historical-run tasks.
type Backfill struct {
	StateStore    operational.StateStore
	Metadata      operational.CollectionMetadata
	Enrollment    Enrollment
	Queue         Queue
	Projector     Projector
	Lake          Lake
	Enumerator    Enumerator
	RunEnumerator WorkflowRunEnumerator
	Quota         RunQuotaService
	QuotaApp      string
	// WindowDays is opt-in; zero preserves unbounded historical enumeration.
	WindowDays       int
	ReconstructScope bool
	ScopeLimit       int
	ScopeReady       func(context.Context) error
	runAfter         time.Time
	runBefore        time.Time
}

// BackfillState is the resumable checkpoint, published for status reporting.
type BackfillState struct {
	Phase               string `json:"phase"`
	StartedAt           string `json:"startedAt,omitempty"`
	CompletedAt         string `json:"completedAt,omitempty"`
	Installations       int    `json:"installations"`
	Repositories        int    `json:"repositories"`
	QueuedRepositories  int    `json:"queuedRepositories"`
	QueuedRunTasks      int    `json:"queuedRunTasks"`
	EnumerationFailures int    `json:"enumerationFailures,omitempty"`
	LakeReplayed        bool   `json:"lakeReplayed"`
	Revision            int64  `json:"revision,omitempty"`
	Error               string `json:"error,omitempty"`
}

// Run performs cold start: replay, enumerate, seed repository tasks, and admit
// paginated historical workflow-run tasks. It does not process run artifacts.
func (b Backfill) Run(ctx context.Context) (result BackfillState, err error) {
	ctx, finish := startBackfillTelemetry(ctx, b.WindowDays)
	defer func() { finish(result, err) }()
	state := BackfillState{Phase: "replaying", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	backfillLog.Printf("cold start backfill started")
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	if b.WindowDays < 0 || b.WindowDays > 365 {
		return b.fail(ctx, state, errors.New("backfill window must be between 0 and 365 days"))
	}
	if b.WindowDays > 0 {
		b.runBefore, _ = time.Parse(time.RFC3339Nano, state.StartedAt)
		b.runAfter = b.runBefore.Add(-time.Duration(b.WindowDays) * 24 * time.Hour)
	}
	populated, err := b.Lake.Populated()
	if err != nil {
		return b.fail(ctx, state, err)
	}
	if populated && !b.ReconstructScope {
		result, err := b.replay(ctx)
		if err != nil {
			return b.fail(ctx, state, err)
		}
		state.LakeReplayed = true
		state.Revision = result.Revision
		backfillLog.Printf("replayed evidence lake revision=%d", result.Revision)
	}
	state.Phase = "enumerating"
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	var repositories []enrolledRepository
	var installations, enumerationFailures int
	if b.ReconstructScope {
		if b.Enumerator == nil {
			return b.fail(ctx, state, errors.New("scope reconstruction requires GitHub App enumeration"))
		}
		repositories, installations, enumerationFailures, err = b.reconstructScope(ctx)
	} else {
		repositories, installations, enumerationFailures, err = b.enumerate(ctx)
	}
	if err != nil {
		return b.fail(ctx, state, err)
	}
	if populated && b.ReconstructScope {
		result, replayErr := b.replay(ctx)
		if replayErr != nil {
			return b.fail(ctx, state, replayErr)
		}
		state.LakeReplayed = true
		state.Revision = result.Revision
	}
	if b.ScopeReady != nil {
		if err := b.ScopeReady(ctx); err != nil {
			return b.fail(ctx, state, err)
		}
	}
	state.Installations = installations
	state.Repositories = len(repositories)
	state.EnumerationFailures = enumerationFailures
	backfillLog.Printf(
		"backfill repository enumeration completed installations=%d repositories=%d failures=%d",
		installations, len(repositories), enumerationFailures,
	)
	state.Phase = "seeding"
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	queued, err := b.seed(ctx, repositories)
	if err != nil {
		return b.fail(ctx, state, err)
	}
	state.QueuedRepositories = queued
	runTasks, runFailures, err := b.enqueueHistoricalRuns(ctx, repositories)
	if err != nil {
		return b.fail(ctx, state, err)
	}
	state.QueuedRunTasks = runTasks
	state.EnumerationFailures += runFailures
	state.Phase = "collecting"
	if state.EnumerationFailures > 0 {
		state.Phase = "partial"
	}
	state.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	backfillLog.Printf(
		"cold start seeded repositories=%d queued=%d run_tasks=%d enumeration_failures=%d",
		len(repositories), queued, runTasks, state.EnumerationFailures,
	)
	return state, nil
}

// Replay repopulates from the evidence lake alone. It issues no GitHub
// requests and is the normal recovery path.
func (b Backfill) Replay(ctx context.Context) (ingest.Result, error) {
	populated, err := b.Lake.Populated()
	if err != nil {
		return ingest.Result{}, err
	}
	if !populated {
		return ingest.Result{}, errors.New("evidence lake holds no collected shards")
	}
	return b.replay(ctx)
}

func (b Backfill) replay(ctx context.Context) (ingest.Result, error) {
	return b.Projector.Project(ctx)
}

type enrolledRepository struct {
	name           string
	pushedAt       time.Time
	installationID int64
}

func (b Backfill) enumerate(ctx context.Context) ([]enrolledRepository, int, int, error) {
	if b.Enumerator == nil {
		return nil, 0, 0, errors.New("cold start requires GitHub App enumeration")
	}
	installations, err := b.Enumerator.ListInstallations(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	var repositories []enrolledRepository
	failures := 0
	for _, installation := range installations {
		if ctx.Err() != nil {
			return nil, 0, failures, ctx.Err()
		}
		covered, err := b.Enumerator.ListRepositories(ctx, installation.ID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, failures, ctx.Err()
			}
			// One installation that cannot be read must not abort cold start
			// for the rest; the gap is visible in enrollment coverage.
			backfillLog.Printf("installation enumeration failed; continuing installation=%d", installation.ID)
			failures++
			recordBackfillCounter(ctx, "cao_dashboard.collection.backfill.enumeration_failure.count",
				"Installation or repository enumeration failures during collection backfill")
			continue
		}
		names, enrolled, skipped := normalizeEnumeratedRepositories(covered)
		for index := range enrolled {
			enrolled[index].installationID = installation.ID
		}
		if skipped > 0 {
			backfillLog.Printf("dropped invalid repository names installation=%d skipped=%d", installation.ID, skipped)
		}
		repositories = append(repositories, enrolled...)
		if err := b.enrollRepositories(ctx, installation.ID, names); err != nil {
			return nil, 0, failures, err
		}
	}
	return repositories, len(installations), failures, nil
}

func (b Backfill) enrollRepositories(ctx context.Context, installation int64, names []string) error {
	for {
		err := b.Enrollment.AddRepositories(ctx, installation, names)
		if !errors.Is(err, ErrEnrollmentMutationBusy) {
			return err
		}
		recordBackfillCounter(ctx, "cao_dashboard.collection.backfill.enrollment_wait.count",
			"Retries while a concurrent webhook holds the enrollment mutation lease")
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// normalizeEnumeratedRepositories canonicalizes one installation's enumerated
// repositories, dropping any reference NormalizeRepository rejects. It is a
// pure function so cold start's name-canonicalization behavior is testable
// without a fake GitHub API.
func normalizeEnumeratedRepositories(covered []githubapp.Repository) (names []string, repositories []enrolledRepository, skipped int) {
	names = make([]string, 0, len(covered))
	for _, repository := range covered {
		normalized, err := NormalizeRepository(repository.FullName)
		if err != nil {
			skipped++
			continue
		}
		names = append(names, normalized)
		if repository.Archived {
			continue
		}
		repositories = append(repositories, enrolledRepository{
			name: normalized, pushedAt: repository.PushedAt,
		})
	}
	return names, repositories, skipped
}

type runPage struct {
	repository enrolledRepository
	cursor     runBackfillCursor
	nextPage   int
	runs       []githubapp.WorkflowRun
}

type runBackfillCursor struct {
	Page       int  `json:"page"`
	Discovered int  `json:"discovered"`
	Pass       int  `json:"pass"`
	Complete   bool `json:"complete,omitempty"`
}

type pendingRunTask struct {
	repository enrolledRepository
	run        githubapp.WorkflowRun
}

// enqueueHistoricalRuns walks each installation/repository cursor in pages.
// Each round's runs are enqueued newest-first across repositories, and a
// repository cursor advances only after every run from that page is durable.
func (b Backfill) enqueueHistoricalRuns(
	ctx context.Context, repositories []enrolledRepository,
) (queued, failures int, err error) {
	if b.RunEnumerator == nil {
		return 0, 0, errors.New("historical backfill requires workflow-run enumeration")
	}
	pages := make([]runPage, 0, len(repositories))
	active := make([]enrolledRepository, 0, len(repositories))
	seen := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		identity := strconv.FormatInt(repository.installationID, 10) + ":" + repository.name
		if repository.installationID <= 0 {
			continue
		}
		if b.Enrollment.Metadata != nil {
			owner, err := b.Enrollment.InstallationFor(ctx, repository.name)
			if err != nil {
				return queued, failures, err
			}
			if owner != repository.installationID {
				continue
			}
		}
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		active = append(active, repository)
	}
	sort.Slice(active, func(first, second int) bool {
		if active[first].pushedAt.Equal(active[second].pushedAt) {
			return active[first].name < active[second].name
		}
		return active[first].pushedAt.After(active[second].pushedAt)
	})
	if err := b.Queue.Ensure(ctx); err != nil {
		return 0, 0, err
	}
	backfillLog.Printf("historical run enumeration started repositories=%d", len(active))

	for len(active) > 0 {
		pages = pages[:0]
		remaining := make([]enrolledRepository, 0, len(active))
		for _, repository := range active {
			if err := ctx.Err(); err != nil {
				return queued, failures, err
			}
			cursorValue, err := b.Metadata.ReadAttribute(ctx, runBackfillCursorKey, runBackfillCursorField(repository))
			if err != nil {
				return queued, failures, err
			}
			cursor, err := parseRunBackfillCursor(cursorValue)
			if err != nil {
				return queued, failures, err
			}
			if cursor.Complete {
				continue
			}
			if cursor.Page < 1 {
				return queued, failures, errors.New("invalid historical run backfill cursor")
			}
			page := cursor.Page
			runs, nextPage, err := b.listWorkflowRuns(
				ctx, repository.installationID, repository.name, page,
			)
			if err != nil {
				if ctx.Err() != nil {
					return queued, failures, ctx.Err()
				}
				backfillLog.Printf(
					"workflow run enumeration failed; continuing repository=%s page=%d error_class=%s",
					repository.name, page, classifyBackfillError(err),
				)
				failures++
				recordBackfillCounter(ctx, "cao_dashboard.collection.backfill.enumeration_failure.count",
					"Installation or repository enumeration failures during collection backfill")
				// Keep this cursor unchanged for the next backfill invocation.
				continue
			}
			backfillLog.Printf(
				"workflow run page enumerated page=%d runs=%d has_next_page=%t",
				page, len(runs), nextPage != 0,
			)
			recordBackfillCounter(ctx, "cao_dashboard.collection.backfill.page.count",
				"Workflow run pages read during collection backfill")
			if len(runs) > 0 {
				recordBackfillCounter(ctx, "cao_dashboard.collection.backfill.run_discovered.count",
					"Workflow runs discovered during collection backfill", int64(len(runs)))
			}
			if nextPage != 0 && nextPage <= page {
				return queued, failures, errors.New("workflow run enumeration returned a non-advancing page")
			}
			pages = append(pages, runPage{
				repository: repository, cursor: cursor, nextPage: nextPage, runs: runs,
			})
		}
		var tasks []pendingRunTask
		for _, page := range pages {
			for _, run := range page.runs {
				tasks = append(tasks, pendingRunTask{repository: page.repository, run: run})
			}
		}
		sort.Slice(tasks, func(first, second int) bool {
			left, right := tasks[first], tasks[second]
			if left.run.CreatedAt.Equal(right.run.CreatedAt) {
				if left.run.ID == right.run.ID {
					return left.repository.name < right.repository.name
				}
				return left.run.ID > right.run.ID
			}
			return left.run.CreatedAt.After(right.run.CreatedAt)
		})
		pageQueued := 0
		pageDeduplicated := 0
		queuedByRepository := make(map[string]int)
		for _, pending := range tasks {
			attempt := pending.run.Attempt
			if attempt <= 0 {
				attempt = 1
			}
			enqueued, enqueueErr := b.Queue.EnqueueRun(ctx, RunTask{
				Repository: pending.repository.name, InstallationID: pending.repository.installationID,
				RunID: pending.run.ID, Attempt: attempt, CreatedAt: pending.run.CreatedAt,
			})
			if enqueueErr != nil {
				return queued, failures, enqueueErr
			}
			if enqueued {
				queued++
				pageQueued++
				queuedByRepository[runBackfillCursorField(pending.repository)]++
				recordBackfillCounter(ctx, "cao_dashboard.collection.backfill.run_task_queued.count",
					"Workflow run tasks durably queued during collection backfill")
			} else {
				pageDeduplicated++
				recordBackfillCounter(ctx, "cao_dashboard.collection.backfill.run_task_deduplicated.count",
					"Duplicate workflow run tasks suppressed during collection backfill")
			}
		}
		for _, page := range pages {
			cursor := page.cursor
			switch {
			case page.nextPage != 0:
				cursor.Page = page.nextPage
				cursor.Discovered += queuedByRepository[runBackfillCursorField(page.repository)]
				remaining = append(remaining, page.repository)
			case cursor.Discovered+queuedByRepository[runBackfillCursorField(page.repository)] > 0:
				// A new run inserted ahead of the current page shifts numeric
				// offsets and can move an older run past the final page. Repeat
				// from page one until a complete pass discovers no new identities.
				cursor.Page = 1
				cursor.Discovered = 0
				cursor.Pass++
				remaining = append(remaining, page.repository)
				backfillLog.Printf(
					"historical run backfill restarting verification pass=%d",
					cursor.Pass,
				)
			default:
				cursor.Complete = true
			}
			payload, err := json.Marshal(cursor)
			if err != nil {
				return queued, failures, err
			}
			if err := b.Metadata.WriteAttribute(
				ctx, runBackfillCursorKey, runBackfillCursorField(page.repository), string(payload),
			); err != nil {
				return queued, failures, err
			}
		}
		active = remaining
		backfillLog.Printf(
			"historical run page round admitted queued=%d deduplicated=%d repositories=%d",
			pageQueued, pageDeduplicated, len(pages),
		)
	}
	backfillLog.Printf("historical run enumeration completed queued=%d failures=%d", queued, failures)
	return queued, failures, nil
}

func (b Backfill) listWorkflowRuns(
	ctx context.Context, installationID int64, repository string, page int,
) ([]githubapp.WorkflowRun, int, error) {
	if b.Quota == nil {
		runs, nextPage, _, err := b.workflowRuns(ctx, installationID, repository, page)
		return runs, nextPage, err
	}
	refresher, _ := b.RunEnumerator.(WorkflowRunQuotaRefresher)
	reservation, err := reserveCollectionQuota(ctx, b.Quota, b.QuotaApp, installationID, refresher)
	if err != nil {
		return nil, 0, err
	}
	runs, nextPage, responseQuota, listErr := b.workflowRuns(ctx, installationID, repository, page)
	if quotaErr := reconcileCollectionQuota(ctx, b.Quota, reservation.Bucket, reservation, responseQuota); quotaErr != nil {
		return nil, 0, fmt.Errorf("record workflow run quota response: %w", quotaErr)
	}
	return runs, nextPage, listErr
}

func reserveCollectionQuota(ctx context.Context, quota RunQuotaService, app string, installationID int64, refresher WorkflowRunQuotaRefresher) (githubquota.Reservation, error) {
	if quota == nil || strings.TrimSpace(app) == "" {
		return githubquota.Reservation{}, errors.New("collection quota requires a service and GitHub App identity")
	}
	bucket := githubquota.BucketID{
		App: app, Installation: installationID, Resource: githubquota.ResourceCore,
	}
	reservation, err := quota.Reserve(
		ctx, bucket, githubquota.ReservationRequest{EstimatedCost: 1})
	if errors.Is(err, githubquota.ErrUnknown) {
		if refresher == nil {
			return githubquota.Reservation{}, err
		}
		response, refreshErr := refresher.QuotaRateLimit(ctx, installationID)
		responseBucket := bucket
		responseBucket.Resource = response.Resource
		if response.HasObservation && responseBucket.Normalize() == bucket.Normalize() {
			if err := quota.Observe(ctx, bucket, response.Observation); err != nil {
				return githubquota.Reservation{}, err
			}
		}
		if !response.ParkUntil.IsZero() {
			if err := quota.Park(ctx, responseBucket, response.ParkUntil, response.ParkReason); err != nil {
				return githubquota.Reservation{}, err
			}
		}
		if refreshErr != nil {
			return githubquota.Reservation{}, refreshErr
		}
		if !response.HasObservation || responseBucket.Normalize() != bucket.Normalize() {
			return githubquota.Reservation{}, errors.New("rate limit response did not report core quota")
		}
		reservation, err = quota.Reserve(
			ctx, bucket, githubquota.ReservationRequest{EstimatedCost: 1})
	}
	return reservation, err
}

func (b Backfill) workflowRuns(
	ctx context.Context, installationID int64, repository string, page int,
) ([]githubapp.WorkflowRun, int, githubquota.ResponseQuota, error) {
	if !b.runAfter.IsZero() {
		windowed, ok := b.RunEnumerator.(WorkflowRunWindowEnumerator)
		if !ok {
			return nil, 0, githubquota.ResponseQuota{}, errors.New("configured backfill window requires windowed enumeration")
		}
		return windowed.ListWorkflowRunsWindow(ctx, installationID, repository, page, runBackfillPageSize, b.runAfter, b.runBefore)
	}
	return b.RunEnumerator.ListWorkflowRuns(ctx, installationID, repository, page, runBackfillPageSize)
}

func reconcileCollectionQuota(ctx context.Context, quota RunQuotaService, bucket githubquota.BucketID, reservation githubquota.Reservation, response githubquota.ResponseQuota) error {
	if !response.HasResponse {
		// The request may have reached GitHub even if its response was lost.
		// Keep the reservation until its TTL expires rather than refunding quota.
		return nil
	}
	responseBucket := bucket
	responseBucket.Resource = response.Resource
	if !response.HasObservation {
		// Missing quota headers leave the request cost unknown; retain the
		// reservation until its TTL expires and apply any requested backoff.
		if !response.ParkUntil.IsZero() {
			return quota.Park(ctx, responseBucket, response.ParkUntil, response.ParkReason)
		}
		return nil
	}
	var err error
	if responseBucket.Normalize() == reservation.Bucket.Normalize() {
		err = quota.Commit(ctx, reservation, response.Observation)
	} else {
		err = quota.Release(ctx, reservation)
		if err == nil {
			err = quota.Observe(ctx, responseBucket, response.Observation)
		}
	}
	if err != nil {
		return err
	}
	if !response.ParkUntil.IsZero() {
		return quota.Park(ctx, responseBucket, response.ParkUntil, response.ParkReason)
	}
	return nil
}

func runBackfillCursorField(repository enrolledRepository) string {
	return strconv.FormatInt(repository.installationID, 10) + ":" + repository.name
}

func parseRunBackfillCursor(value string) (runBackfillCursor, error) {
	if value == "" {
		return runBackfillCursor{Page: 1}, nil
	}
	if value == runBackfillComplete {
		return runBackfillCursor{Complete: true}, nil
	}
	var cursor runBackfillCursor
	if err := json.Unmarshal([]byte(value), &cursor); err == nil && cursor.Page > 0 {
		return cursor, nil
	}
	// Accept the numeric page format used by the initial backfill release.
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 {
		return runBackfillCursor{}, errors.New("invalid historical run backfill cursor")
	}
	return runBackfillCursor{Page: page}, nil
}

func classifyBackfillError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return "network"
	}
	return "api"
}

func recordBackfillCounter(ctx context.Context, name, description string, value ...int64) {
	counter, err := otel.Meter(backfillMeterName).Int64Counter(
		name,
		metric.WithUnit("{item}"),
		metric.WithDescription(description),
	)
	if err != nil {
		return
	}
	increment := int64(1)
	if len(value) > 0 {
		increment = value[0]
	}
	counter.Add(ctx, increment)
}

// sortByRecency orders enrolled repositories most-recently-pushed first, in
// place. It is a pure function so seed's ordering behavior is testable
// without a queue or enrollment fake.
func sortByRecency(repositories []enrolledRepository) {
	sort.Slice(repositories, func(first, second int) bool {
		return repositories[first].pushedAt.After(repositories[second].pushedAt)
	})
}

// seed queues backfill tasks ordered by recency so active repositories become
// queryable first.
func (b Backfill) seed(ctx context.Context, repositories []enrolledRepository) (int, error) {
	sortByRecency(repositories)
	if err := b.Queue.Ensure(ctx); err != nil {
		return 0, err
	}
	queued := 0
	unmapped := 0
	for _, repository := range repositories {
		if ctx.Err() != nil {
			return queued, ctx.Err()
		}
		if b.Lake.Directory != "" {
			if err := b.Lake.mineCachedInfo(ctx, repository.name); err != nil {
				return queued, fmt.Errorf("backfill recorded episodes: %w", err)
			}
			if b.Projector.State != nil {
				if err := b.Projector.RequestProjection(ctx); err != nil {
					return queued, err
				}
			}
		}
		installationID, err := b.Enrollment.InstallationFor(ctx, repository.name)
		if err != nil || installationID == 0 {
			// Enrollment covers the repository but no installation maps to
			// it; the repository stays queryable once enrollment is
			// repaired, so the gap is worth surfacing rather than treating
			// as a failure.
			unmapped++
			continue
		}
		enqueued, err := b.Queue.Enqueue(ctx, Task{
			Repository:     repository.name,
			InstallationID: installationID,
			Reason:         "backfill",
		})
		if err != nil {
			return queued, err
		}
		if enqueued {
			queued++
		}
	}
	if unmapped > 0 {
		backfillLog.Printf("seed skipped repositories without installation mapping count=%d", unmapped)
	}
	return queued, nil
}

// failedBackfillState transitions state into its terminal failed phase:
// phase becomes "failed", cause's message is recorded as the error, and
// completedAt marks when the failure was observed. It is a pure function
// extracted from Backfill.fail so the terminal-state transition is testable
// without a store or a real clock.
func failedBackfillState(state BackfillState, cause error, completedAt string) BackfillState {
	state.Phase = "failed"
	state.Error = cause.Error()
	state.CompletedAt = completedAt
	return state
}

func (b Backfill) fail(ctx context.Context, state BackfillState, cause error) (BackfillState, error) {
	previousPhase := state.Phase
	failed := failedBackfillState(state, cause, time.Now().UTC().Format(time.RFC3339Nano))
	backfillLog.Printf("backfill aborted phase=%s", previousPhase)
	checkpoint, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	return failed, errors.Join(cause, b.publish(checkpoint, failed))
}

func (b Backfill) publish(ctx context.Context, state BackfillState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := b.StateStore.SetOperationalState(ctx, backfillStateKey, payload); err != nil {
		backfillLog.Printf("backfill checkpoint write failed")
		return errors.New("persist backfill checkpoint")
	}
	return nil
}

// State reads the last published cold-start checkpoint.
func (b Backfill) State(ctx context.Context) (BackfillState, error) {
	payload, err := b.StateStore.OperationalState(ctx, backfillStateKey)
	if err != nil || len(payload) == 0 {
		return BackfillState{Phase: "idle"}, err
	}
	var state BackfillState
	if err := json.Unmarshal(payload, &state); err != nil {
		return BackfillState{Phase: "idle"}, fmt.Errorf("decode backfill state: %w", err)
	}
	return state, nil
}
