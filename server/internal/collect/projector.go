package collect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var projectorLog = logger.New("cao:collect:projector")

const (
	projectionDirtyKey     = "collect:projection-dirty"
	projectionCompletedKey = "collect:projection-completed"
	projectionLockName     = "projection"
)

// Projector replays an already-compacted evidence lake into canonical dashboard
// data through snapshot ingestion. It never acquires or compacts evidence.
// Independently authorized admission callbacks publish provisional typed facts
// through postgresx; this replacement enriches or supersedes them atomically.
//
// Projector also hosts the separately selected native Value services, which do
// not use the lake or snapshot replacement lifecycle.
type Projector struct {
	State               operational.StateStore
	Leases              operational.LeaseStore
	Data                *postgresx.Store
	Lake                Lake
	Enrollment          Enrollment
	CatalogRoot         string
	GitHubAPIURL        string
	Tokens              TokenProvider
	OutcomeClient       OutcomeClient
	Quota               RunQuotaService
	QuotaApp            string
	RemoteQuota         *githubquota.Service
	RemoteSearchQuota   *githubquota.Service
	RemoteHTTP          *http.Client
	RemoteValuePlan     *RemoteValuePlanCache
	DatabaseQueriesPath string
	// PolicyPath is the reviewed deployment policy for Value evaluation.
	PolicyPath string
	// MinInterval is the shortest gap between two projections.
	MinInterval time.Duration
	// LockTTL bounds how long a projection may hold the global lease.
	LockTTL time.Duration
	// Timeout bounds one projection.
	Timeout time.Duration
	// InventoryRepositoryLimit bounds enrolled repository enumeration for
	// replay erasure and Value evaluation. Zero means unbounded; exceeding
	// a configured limit fails rather than silently truncating the scope.
	InventoryRepositoryLimit int
	nativeValueScope         *collectnative.EnrollmentScope
}

// defaultProjectionInterval is the shortest gap between two projections.
//
// Snapshot validation scales with retained evidence rather than with the
// collection that triggered it.
const defaultProjectionInterval = 5 * time.Minute

func (p Projector) minInterval() time.Duration {
	if p.MinInterval <= 0 {
		return defaultProjectionInterval
	}
	return p.MinInterval
}

func (p Projector) lockTTL() time.Duration {
	if p.LockTTL <= 0 {
		return 30 * time.Minute
	}
	return p.LockTTL
}

func (p Projector) timeout() time.Duration {
	if p.Timeout <= 0 {
		return 25 * time.Minute
	}
	return p.Timeout
}

// RequestProjection records a unique generation. A burst still coalesces into
// one projection, without confusing a concurrent request with completed work.
func (p Projector) RequestProjection(ctx context.Context) error {
	token, err := operationToken()
	if err != nil {
		return err
	}
	return p.State.SetOperationalState(ctx, projectionDirtyKey, []byte(token))
}

// PendingProjection reports whether the lake changed since the last
// projection.
func (p Projector) PendingProjection(ctx context.Context) (bool, error) {
	value, err := p.State.OperationalState(ctx, projectionDirtyKey)
	if err != nil || len(value) == 0 {
		return false, err
	}
	completed, err := p.State.OperationalState(ctx, projectionCompletedKey)
	return !bytes.Equal(value, completed), err
}

// ErrProjectionBusy reports that another process holds the projection lease.
var ErrProjectionBusy = errors.New("a projection update is already running")

// Project ingests the existing manifest and replaces canonical data. A failed
// projection leaves the previously active data serving.
func (p Projector) Project(ctx context.Context) (ingest.Result, error) {
	return p.project(ctx, false)
}

// Rebuild reprojects unconditionally. An operator rebuilding is repairing the
// canonical database, so an unchanged lake must still be reprojected rather
// than short-circuited.
func (p Projector) Rebuild(ctx context.Context) (ingest.Result, error) {
	return p.project(ctx, true)
}

func (p Projector) project(ctx context.Context, force bool) (ingest.Result, error) {
	if p.DatabaseQueriesPath == "" {
		return ingest.Result{}, errors.New("database query path is required")
	}
	if p.Data == nil {
		return ingest.Result{}, errors.New("projection requires Postgres")
	}
	token, err := operationToken()
	if err != nil {
		return ingest.Result{}, err
	}
	acquired, err := p.Leases.TryLock(ctx, projectionLockName, token, p.lockTTL())
	if err != nil {
		return ingest.Result{}, err
	}
	if !acquired {
		return ingest.Result{}, ErrProjectionBusy
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = p.Leases.Unlock(release, projectionLockName, token)
	}()
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	// Even an unconditional rebuild must survive interruption. Never consume
	// intent before canonical commit or overwrite a concurrently requested
	// generation when recording completion.
	if err := p.RequestProjection(ctx); err != nil {
		return ingest.Result{}, err
	}
	generation, err := p.State.OperationalState(ctx, projectionDirtyKey)
	if err != nil {
		return ingest.Result{}, err
	}
	// Release the lake after canonical commit, before recording completion:
	// concurrent erasure may publish a newer intent at that boundary.
	result, err := func() (ingest.Result, error) {
		unlock, err := p.Lake.lockEvidence(ctx)
		if err != nil {
			return ingest.Result{}, err
		}
		defer unlock()
		if err := p.prepareReplay(ctx); err != nil {
			return ingest.Result{}, err
		}
		revision, supplement, err := p.miningSupplement(ctx)
		if err != nil {
			return ingest.Result{}, err
		}
		// An unchanged collection need not rewrite an identical canonical dataset.
		return ingest.Run(ctx, p.Data, p.Lake.Directory, ingest.Options{
			DatabaseQueriesPath: p.DatabaseQueriesPath,
			Force:               force,
			SupplementRevision:  revision,
			Supplement:          supplement,
		})
	}()
	if err != nil {
		return ingest.Result{}, err
	}
	if err := p.State.SetOperationalState(ctx, projectionCompletedKey, generation); err != nil {
		return ingest.Result{}, fmt.Errorf("record projection completion: %w", err)
	}
	projectorLog.Printf("activated dashboard data revision=%d sources=%d", result.Revision, len(result.Counts))
	return result, nil
}

// prepareReplay preserves enrollment erasure before replaying a previously
// published snapshot. Missing or invalid compaction must be repaired by the
// snapshot producer, never by falling back to retired collection.
func (p Projector) prepareReplay(ctx context.Context) error {
	if p.Enrollment.Metadata != nil {
		repositories, err := p.enrolledRepositories(ctx)
		if err != nil {
			return fmt.Errorf("resolve retained value scope: %w", err)
		}
		enrolled := make(map[string]bool, len(repositories))
		for _, repository := range repositories {
			enrolled[strings.ToLower(repository)] = true
		}
		if err := p.Lake.filterOperationalValues(func(repository string) bool {
			return enrolled[repository]
		}); err != nil {
			return fmt.Errorf("filter retained value scope: %w", err)
		}
	}
	populated, err := p.Lake.Populated()
	if err != nil {
		return err
	}
	if !populated {
		return errors.New("replay requires an already-compacted evidence lake with a manifest")
	}
	return nil
}

// scanRepositoriesPage matches Enrollment.ScanRepositories's signature so
// collectEnrolledRepositories's pagination loop is testable against a plain
// function standing in for the real Redis-backed scan, without a fake
// Enrollment or a running Redis.
type scanRepositoriesPage func(ctx context.Context, cursor string, count int) ([]string, string, error)

// enrolledRepositoriesPageSize is how many repositories enrolledRepositories
// requests per scan cursor. It is a named constant, rather than an inline
// literal, so collectEnrolledRepositories's tests can assert the page size
// callers actually receive.
const enrolledRepositoriesPageSize = 500

// enrolledRepositories names every enrolled repository.
//
// The enumeration is deliberately unbounded by default. Truncating it would
// silently omit enrolled repositories from the inventory, and therefore from
// the dashboard, while still reporting a successful projection. An operator who
// needs a bound sets InventoryRepositoryLimit, and exceeding it fails the
// projection rather than publishing a partial inventory.
func (p Projector) enrolledRepositories(ctx context.Context) ([]string, error) {
	return collectEnrolledRepositories(ctx, p.InventoryRepositoryLimit, p.Enrollment.ScanRepositories)
}

// collectEnrolledRepositories drains scanPage cursor-by-cursor into a single
// sorted, de-duplication-free repository list, failing once the running
// total exceeds limit (a non-positive limit means unbounded). It is a pure
// loop over an injected page function so the pagination and limit-enforcement
// behavior is unit-testable without a live Redis-backed Enrollment.
func collectEnrolledRepositories(ctx context.Context, limit int, scanPage scanRepositoriesPage) ([]string, error) {
	var repositories []string
	cursor := ""
	pages := 0
	for {
		page, next, err := scanPage(ctx, cursor, enrolledRepositoriesPageSize)
		if err != nil {
			return nil, err
		}
		pages++
		repositories = append(repositories, page...)
		if limit > 0 && len(repositories) > limit {
			return nil, fmt.Errorf(
				"enrolled repositories exceed the configured inventory limit of %d",
				limit,
			)
		}
		if next == "0" || next == "" {
			break
		}
		cursor = next
	}
	sort.Strings(repositories)
	projectorLog.Printf("scanned enrolled repositories pages=%d count=%d", pages, len(repositories))
	return repositories, nil
}
