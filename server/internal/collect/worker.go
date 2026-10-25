package collect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var workerLog = logger.New("cao:collect:worker")

// Worker leases collection tasks, collects one repository at a time, and
// requests a coalesced projection.
type Worker struct {
	Queue          Queue
	Runner         Runner
	Projector      Projector
	Enrollment     Enrollment
	ScopeRefresher ScopeRefresher
	// Consumer identifies this worker within the consumer group.
	Consumer string
	// BatchSize bounds how many tasks are leased at once.
	BatchSize int
	// BlockInterval bounds how long a read waits for new work.
	BlockInterval time.Duration
	// ReclaimAfter is how long a task may be unacknowledged before another
	// worker takes it over.
	ReclaimAfter time.Duration
	// RepositoryLockTTL bounds the per-repository lease.
	RepositoryLockTTL time.Duration
	// Project enables the projection loop on this worker. Every worker may
	// request projection; whichever wins the lease performs it.
	Project bool
	// OnProjection is notified after a successful projection so the server can
	// broadcast the new revision.
	OnProjection func(revision int64)
}

// consumerNameSource identifies which input determined a worker's resolved
// consumer name. It is useful for diagnosing a misconfigured deployment
// without logging the name itself.
type consumerNameSource string

const (
	consumerNameSourceExplicit  consumerNameSource = "explicit"
	consumerNameSourceGenerated consumerNameSource = "generated"
)

// resolveConsumerName applies the standard priority for a worker's consumer
// name: an explicit configured value, then a name generated from hostname
// and a random suffix obtained from hostnameFunc and tokenFunc. It is a pure
// function so the resolution logic can be exercised without depending on the
// real hostname or a random token. It returns the resolved name and which
// input supplied it, so callers can log the source without exposing the
// name.
func resolveConsumerName(explicit string, hostnameFunc func() (string, error), tokenFunc func() (string, error)) (string, consumerNameSource) {
	if explicit != "" {
		return explicit, consumerNameSourceExplicit
	}
	host, err := hostnameFunc()
	if err != nil || host == "" {
		host = "worker"
	}
	suffix, err := tokenFunc()
	if err != nil {
		return host, consumerNameSourceGenerated
	}
	return host + "-" + suffix[:8], consumerNameSourceGenerated
}

func (w Worker) consumer() string {
	name, source := resolveConsumerName(w.Consumer, os.Hostname, operationToken)
	workerLog.Printf("worker resolved consumer name source=%s", source)
	return name
}

func (w Worker) batchSize() int {
	if w.BatchSize <= 0 {
		return 4
	}
	return w.BatchSize
}

func (w Worker) blockInterval() time.Duration {
	if w.BlockInterval <= 0 {
		return 5 * time.Second
	}
	return w.BlockInterval
}

func (w Worker) reclaimAfter() time.Duration {
	if w.ReclaimAfter <= 0 {
		return 30 * time.Minute
	}
	return w.ReclaimAfter
}

func (w Worker) repositoryLockTTL() time.Duration {
	if w.RepositoryLockTTL <= 0 {
		return 30 * time.Minute
	}
	return w.RepositoryLockTTL
}

// Run processes tasks until the context is cancelled.
func (w Worker) Run(ctx context.Context) error {
	if err := w.Runner.Validate(); err != nil {
		return err
	}
	if err := w.Queue.Ensure(ctx); err != nil {
		return err
	}
	consumer := w.consumer()
	workerLog.Printf("worker started projection_role=%t", w.Project)
	if w.Project {
		projectionCtx, cancelProjection := context.WithCancel(ctx)
		projectionDone := make(chan struct{})
		valueDone := make(chan struct{})
		go func() {
			defer close(projectionDone)
			w.projectionLoop(projectionCtx)
		}()
		go func() {
			defer close(valueDone)
			w.localValueLoop(projectionCtx)
		}()
		defer func() {
			cancelProjection()
			<-projectionDone
			<-valueDone
		}()
	}
	for ctx.Err() == nil {
		reclaimed, err := w.Queue.Reclaim(ctx, consumer, w.reclaimAfter(), w.batchSize())
		if err != nil {
			workerLog.Printf("task reclaim failed")
		}
		leases := reclaimed
		if len(leases) < w.batchSize() {
			fresh, err := w.Queue.Lease(ctx, consumer, w.batchSize()-len(leases), w.blockInterval())
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				workerLog.Printf("task lease failed")
				sleep(ctx, w.blockInterval())
				continue
			}
			leases = append(leases, fresh...)
		}
		if len(leases) == 0 {
			continue
		}
		collected := false
		for _, lease := range leases {
			if ctx.Err() != nil {
				break
			}
			if w.process(ctx, lease) {
				collected = true
			}
		}
		if collected {
			if err := w.Projector.RequestProjection(ctx); err != nil {
				workerLog.Printf("projection request failed")
			}
		}
	}
	workerLog.Printf("worker stopped")
	return ctx.Err()
}

// process handles one lease and reports whether evidence changed.
func (w Worker) process(ctx context.Context, lease Lease) bool {
	task := lease.Task
	ctx = telemetry.ExtractTraceparent(ctx, task.Traceparent)
	if !task.NotBefore.IsZero() && task.NotBefore.After(time.Now().UTC()) {
		deferCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := w.Queue.Defer(deferCtx, lease); err != nil {
			workerLog.Printf("delayed task schedule failed")
		}
		return false
	}
	token, err := operationToken()
	if err != nil {
		return false
	}
	if err := w.Queue.LockRepository(ctx, task.lockKey(), token, w.repositoryLockTTL()); err != nil {
		if errors.Is(err, ErrRepositoryBusy) {
			lease.Task.NotBefore = time.Now().UTC().Add(backoff(max(task.Attempt, 1)))
			if err := w.Queue.RequeueBlocked(ctx, lease); err != nil {
				workerLog.Printf("blocked task requeue failed")
			}
			return false
		}
		workerLog.Printf("repository lease failed")
		return false
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = w.Queue.UnlockRepository(release, task.lockKey(), token)
	}()
	// Clearing the debounce marker before collecting means events that arrive
	// during a collection enqueue a follow-up rather than being collapsed into
	// a collection that already started.
	if err := w.Queue.Admit(ctx, task); err != nil {
		workerLog.Printf("debounce clear failed")
		return false
	}
	if task.Scope != nil || task.InventoryRefresh {
		if err := w.processRefresh(ctx, lease); err != nil {
			workerLog.Printf("background inventory refresh failed")
			w.retryRefresh(ctx, lease, err)
		}
		return false
	}
	if w.Enrollment.Metadata != nil {
		installationID, err := w.Enrollment.InstallationFor(ctx, task.Repository)
		if err != nil {
			workerLog.Printf("repository enrollment check failed")
			if retryErr := w.Queue.Retry(ctx, lease, err); retryErr != nil {
				workerLog.Printf("task retry failed")
			}
			return false
		}
		switch {
		case task.Erase:
			if installationID > 0 {
				if task.InstallationID > 0 && installationID != task.InstallationID {
					if err := w.Queue.Complete(ctx, lease); err != nil {
						workerLog.Printf("stale erasure acknowledgement failed")
					}
					return false
				}
				lease.Task.NotBefore = time.Now().UTC().Add(backoff(max(task.Attempt, 1)))
				if err := w.Queue.Defer(ctx, lease); err != nil {
					workerLog.Printf("pending erasure defer failed")
				}
				return false
			}
		case installationID == 0:
			return w.processErasure(ctx, lease)
		default:
			lease.Task.InstallationID = installationID
			task = lease.Task
		}
	}
	if task.Erase {
		return w.processErasure(ctx, lease)
	}
	if task.Lifecycle != "" {
		if err := w.processRepositoryLifecycle(ctx, lease); err != nil {
			workerLog.Printf("repository lifecycle update failed")
			w.retryRefresh(ctx, lease, err)
		}
		return false
	}
	if w.Projector.Data != nil {
		active, err := w.Projector.Data.RepositoryActive(ctx, task.Repository)
		if err != nil {
			if retryErr := w.Queue.Retry(ctx, lease, err); retryErr != nil {
				workerLog.Printf("task retry failed")
			}
			return false
		}
		if !active {
			if err := w.Queue.Complete(ctx, lease); err != nil {
				workerLog.Printf("task acknowledgement failed")
			}
			return false
		}
	}
	if err := w.Runner.Collect(ctx, task); err != nil {
		workerLog.Printf("collection failed attempt=%d", task.Attempt)
		if retryErr := w.Queue.Retry(ctx, lease, err); retryErr != nil {
			workerLog.Printf("task retry failed")
		}
		return false
	}
	if err := w.Queue.Complete(ctx, lease); err != nil {
		workerLog.Printf("task acknowledgement failed")
		return true
	}
	workerLog.Printf("collection completed repository=%s completed_at=%s", task.Repository, time.Now().UTC().Format(time.RFC3339Nano))
	return true
}

func (w Worker) processErasure(ctx context.Context, lease Lease) bool {
	if w.Runner.Traces != nil {
		if err := w.Runner.Traces.WithdrawRepository(ctx, lease.Task.Repository); err != nil {
			workerLog.Printf("trace withdrawal failed")
			if retryErr := w.Queue.Retry(ctx, lease, err); retryErr != nil {
				workerLog.Printf("trace withdrawal retry failed")
			}
			return false
		}
	}
	if err := w.Runner.Lake.ForgetContext(ctx, lease.Task.Repository); err != nil {
		workerLog.Printf("evidence erasure failed repository=%s", lease.Task.Repository)
		if retryErr := w.Queue.Retry(ctx, lease, err); retryErr != nil {
			workerLog.Printf("task retry failed repository=%s", lease.Task.Repository)
		}
		return false
	}
	// The task remains reclaimable if the follow-up cannot be persisted.
	// Repeating Forget is safe; acknowledging without projection intent is not.
	if err := w.Projector.RequestProjection(ctx); err != nil {
		workerLog.Printf("erasure projection request failed repository=%s", lease.Task.Repository)
		return false
	}
	if err := w.Queue.Complete(ctx, lease); err != nil {
		workerLog.Printf("task acknowledgement failed repository=%s", lease.Task.Repository)
	}
	workerLog.Printf("erased retained evidence repository=%s", lease.Task.Repository)
	return true
}

// projectionLoop performs coalesced projection. Any worker may run it; the
// global projection lease decides which one actually does.
func (w Worker) projectionLoop(ctx context.Context) {
	interval := w.Projector.minInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pending, err := w.Projector.PendingProjection(ctx)
		if err != nil {
			continue
		}
		if !pending {
			continue
		}
		result, err := w.Projector.Project(ctx)
		if err != nil {
			if !errors.Is(err, ErrProjectionBusy) {
				workerLog.Printf("projection failed")
			}
			continue
		}
		if w.OnProjection != nil {
			w.OnProjection(result.Revision)
		}
	}
}

func (w Worker) localValueLoop(ctx context.Context) {
	ticker := time.NewTicker(w.Projector.minInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Projector.CollectLocalValues(ctx); err != nil && !errors.Is(err, ErrLocalValueBusy) && ctx.Err() == nil {
				workerLog.Printf("local Value computation deferred or failed; core Activity publication is independent")
			}
		}
	}
}

func sleep(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func operationToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
