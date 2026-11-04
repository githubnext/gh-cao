package collect

import (
	"context"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
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
	for ctx.Err() == nil {
		more, err := w.Step(ctx)
		if err != nil && ctx.Err() == nil {
			workerLog.Printf("historical run processing deferred")
		}
		if err != nil || !more {
			sleep(ctx, time.Second)
		}
	}
	return ctx.Err()
}

// Step handles at most one exact attempt, allowing bounded shutdown and fair
// reclaim. Errors leave a durable lease or replacement; they never imply ACK.
func (w RunWorker) Step(ctx context.Context) (bool, error) {
	if err := w.validate(); err != nil {
		return false, err
	}
	if err := w.Queue.EnsureRuns(ctx); err != nil {
		return false, err
	}
	leases, err := w.Queue.ReclaimRuns(ctx, w.Consumer, w.reclaimAfter(), 1)
	if err != nil {
		return false, err
	}
	if len(leases) == 0 {
		leases, err = w.Queue.LeaseRuns(ctx, w.Consumer, 1)
	}
	if err != nil || len(leases) == 0 {
		return false, err
	}
	return true, w.process(ctx, leases[0])
}

func (w RunWorker) process(ctx context.Context, lease RunLease) error {
	if lease.Task.NotBefore.After(time.Now().UTC()) {
		return w.Queue.DeferRun(ctx, lease)
	}
	acquire, cancel := context.WithTimeout(ctx, w.timeout())
	err := w.Collector.CollectScopedAttempt(acquire, collectnative.ScopedAttempt{
		Identity: collectnative.Identity{
			Repository: lease.Task.Repository, InstallationID: lease.Task.InstallationID,
			RunID: lease.Task.RunID, Attempt: lease.Task.Attempt,
		},
		Epoch: lease.Task.ScopeEpoch,
	})
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return w.Queue.CompleteRun(ctx, lease)
	}
	if errors.Is(err, collectnative.ErrBusy) {
		return errors.Join(err, w.Queue.DeferRun(ctx, lease))
	}
	return errors.Join(err, w.Queue.RetryRun(ctx, lease))
}
