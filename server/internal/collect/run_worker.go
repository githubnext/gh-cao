package collect

import (
	"context"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

// RunAttemptCollector returns success only after durable safe-evidence
// publication. It must not enroll a task's repository or discover other runs.
type RunAttemptCollector interface {
	CollectScopedAttempt(context.Context, collectnative.ScopedAttempt) error
}

// RunWorker consumes historical admission through the native exact-attempt
// boundary. It does not activate the hosted profile or replace its lifecycle
// consumer; canonical publication intents survive task acknowledgement.
type RunWorker struct {
	Queue              Queue
	Collector          RunAttemptCollector
	Discovery          collectnative.RunDiscovery
	Scopes             collectnative.ScopeReader
	Publications       collectnative.PublicationConsumer
	Consumer           string
	AcquisitionTimeout time.Duration
	ReclaimAfter       time.Duration
}

func (w RunWorker) timeout() time.Duration {
	if w.AcquisitionTimeout <= 0 {
		return 10 * time.Minute
	}
	return w.AcquisitionTimeout
}

func (w RunWorker) reclaimAfter() time.Duration {
	if w.ReclaimAfter <= 0 {
		return 15 * time.Minute
	}
	return w.ReclaimAfter
}

func (w RunWorker) validate() error {
	if w.Collector == nil || w.Consumer == "" || w.timeout() >= w.reclaimAfter() {
		return errors.New("historical worker requires a collector, consumer and a timeout shorter than reclaim")
	}
	_, err := w.Queue.fencedRunQueue()
	return err
}

func (w RunWorker) Run(ctx context.Context) error {
	if w.Consumer == "" {
		w.Consumer = (Worker{}).consumer()
	}
	if err := w.validate(); err != nil {
		return err
	}
	preferEnumeration := false
	for ctx.Err() == nil {
		more, err := w.step(ctx, preferEnumeration)
		preferEnumeration = !preferEnumeration
		if ctx.Err() == nil && w.Publications != nil {
			count, publishErr := w.Publications.Replay(ctx, 128)
			more = more || count > 0
			err = errors.Join(err, publishErr)
		}
		if err != nil && ctx.Err() == nil {
			workerLog.Printf("historical run processing deferred")
		}
		if err != nil || !more {
			sleep(ctx, time.Second)
		}
	}
	return ctx.Err()
}

// Step handles one exact attempt or bounded enumeration page. Errors leave a
// durable lease or replacement; they never imply ACK.
func (w RunWorker) Step(ctx context.Context) (bool, error) {
	return w.step(ctx, false)
}

func (w RunWorker) step(ctx context.Context, preferEnumeration bool) (bool, error) {
	if err := w.validate(); err != nil {
		return false, err
	}
	if err := w.Queue.EnsureRuns(ctx); err != nil {
		return false, err
	}
	streams := []string{runTaskStream, runEnumerationStream}
	if preferEnumeration {
		streams[0], streams[1] = streams[1], streams[0]
	}
	for _, stream := range streams {
		leases, err := w.Queue.reclaimRunStream(ctx, w.Consumer, stream, w.reclaimAfter(), 1)
		if err != nil {
			return false, err
		}
		if len(leases) == 0 {
			leases, err = w.Queue.leaseRunStream(ctx, w.Consumer, stream, 1)
		}
		if err != nil {
			return false, err
		}
		if len(leases) != 0 {
			return true, w.process(ctx, leases[0])
		}
	}
	return false, nil
}

func (w RunWorker) process(ctx context.Context, lease RunLease) error {
	if lease.Task.NotBefore.After(time.Now().UTC()) {
		return w.Queue.DeferRun(ctx, lease)
	}
	acquire, cancel := context.WithTimeout(ctx, w.timeout())
	var next *RunEnumeration
	var err error
	if lease.Task.Enumeration != nil {
		next, err = w.enumerate(acquire, lease.Task)
	} else {
		err = w.collectAttempt(acquire, lease.Task)
	}
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		if next != nil {
			lease.Task.Enumeration, lease.Task.Failures = next, 0
			lease.Task.NotBefore = time.Time{}
			return w.Queue.replaceRun(ctx, lease, false)
		}
		return w.Queue.CompleteRun(ctx, lease)
	}
	if errors.Is(err, collectnative.ErrBusy) || errors.Is(err, collectnative.ErrClaimLost) || errors.Is(err, operational.ErrCapacity) {
		return errors.Join(err, w.Queue.DeferRun(ctx, lease))
	}
	var unavailable *githubquota.UnavailableError
	if errors.As(err, &unavailable) {
		lease.Task.NotBefore = unavailable.RetryAt
		if !lease.Task.NotBefore.After(time.Now().UTC()) {
			lease.Task.NotBefore = time.Now().UTC().Add(time.Minute)
		}
		return errors.Join(err, w.Queue.DeferRun(ctx, lease))
	}
	return errors.Join(err, w.Queue.RetryRun(ctx, lease))
}

func (w RunWorker) collectAttempt(ctx context.Context, task RunTask) error {
	if w.Scopes != nil {
		scope, err := w.Scopes.CurrentScope(ctx, task.Repository)
		if errors.Is(err, collectnative.ErrIdentity) || err == nil &&
			(scope.InstallationID != task.InstallationID || scope.Epoch != task.ScopeEpoch) {
			workerLog.Printf("obsolete native attempt acknowledged")
			return nil
		}
		if err != nil {
			return err
		}
	}
	return w.Collector.CollectScopedAttempt(ctx, collectnative.ScopedAttempt{
		Identity: collectnative.Identity{
			Repository: task.Repository, InstallationID: task.InstallationID,
			RunID: task.RunID, Attempt: task.Attempt,
		},
		Epoch: task.ScopeEpoch,
	})
}
