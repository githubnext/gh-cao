package collect

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

// Each adapter must fence its mutation against the supplied committed scope.
// Ingestion scope does not grant workflow execution or Value acquisition rights.
type NativeInventoryRefresher interface {
	RefreshInventory(context.Context, collectnative.EnrollmentScope) error
}

type NativeInventoryRetryer interface {
	RetryPending(context.Context, int) (int, error)
}

type NativeValueInvalidator interface {
	InvalidateValue(context.Context, collectnative.EnrollmentScope) error
}

type NativeValueRuntime interface {
	RunValues(context.Context) error
}

type NativeOutcomeRecorder interface {
	RecordOutcome(context.Context, collectnative.EnrollmentScope, mining.Outcome) error
}

// NativeWorker is the existing collection worker's file-free runtime. Control
// delivery receipts fence ACK/retry; authenticated source transactions fence
// ownership changes, independent of the process that enumerated App coverage.
type NativeWorker struct {
	Queue           Queue
	Enrollment      Enrollment
	ScopeRefresher  ScopeRefresher
	Client          NativeScopeEnumerator
	Runs            RunWorker
	Inventory       NativeInventoryRefresher
	Values          NativeValueInvalidator
	Outcomes        NativeOutcomeRecorder
	Consumer        string
	WindowDays      int
	RefreshInterval time.Duration
	// InventoryRetryInterval is independent of scope repair. Zero uses one minute.
	InventoryRetryInterval time.Duration
}

func (w NativeWorker) validate() error {
	if w.Enrollment.Authority == nil || w.Client == nil || w.ScopeRefresher.Client == nil ||
		w.Inventory == nil || w.Values == nil || w.Outcomes == nil ||
		w.WindowDays < 0 || w.WindowDays > 365 || w.RefreshInterval < 0 || w.InventoryRetryInterval < 0 {
		return collectnative.ErrUnsupported
	}
	_, err := w.Queue.fencedRunQueue()
	return err
}

func (w NativeWorker) interval() time.Duration {
	if w.RefreshInterval <= 0 {
		return time.Hour
	}
	return w.RefreshInterval
}

func (w NativeWorker) Run(ctx context.Context) error {
	if err := w.validate(); err != nil {
		return err
	}
	if w.Consumer == "" {
		w.Consumer = (Worker{}).consumer()
	}
	w.Runs.Queue, w.Runs.Scopes = w.Queue, w.Enrollment.Authority
	w.Runs.Consumer = w.Consumer + "-runs"
	if w.Runs.Publications == nil {
		return collectnative.ErrUnsupported
	}
	if err := w.Runs.validate(); err != nil {
		return err
	}
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Runs.Run(lifetime) }()
	defer func() { cancel(); <-done }()
	var inventoryDone chan error
	if retryer, ok := w.Inventory.(NativeInventoryRetryer); ok {
		inventoryDone = make(chan error, 1)
		go func() { inventoryDone <- w.retryInventory(lifetime, retryer) }()
		defer func() { cancel(); <-inventoryDone }()
	}
	var valueDone chan error
	if runtime, ok := w.Values.(NativeValueRuntime); ok {
		valueDone = make(chan error, 1)
		go func() { valueDone <- runtime.RunValues(lifetime) }()
		defer func() { cancel(); <-valueDone }()
	}
	var nextRefresh time.Time
	for ctx.Err() == nil {
		select {
		case err := <-done:
			// Preserve the result for the joined shutdown path.
			done <- err
			if err == nil {
				return errors.New("native attempt role stopped unexpectedly")
			}
			return err
		case err := <-inventoryDone:
			inventoryDone <- err
			if err == nil {
				return errors.New("native inventory retry role stopped unexpectedly")
			}
			return err
		case err := <-valueDone:
			valueDone <- err
			if err == nil {
				return errors.New("native Value role stopped unexpectedly")
			}
			return err
		default:
		}
		if !time.Now().Before(nextRefresh) {
			if err := w.Reconcile(ctx); err != nil {
				if ctx.Err() == nil {
					workerLog.Printf("native scope repair admission deferred")
				}
				nextRefresh = time.Now().Add(min(w.interval(), time.Minute))
			} else {
				nextRefresh = time.Now().Add(w.interval())
			}
		}
		more, err := w.Step(ctx)
		if err != nil && ctx.Err() == nil {
			workerLog.Printf("native control processing deferred")
		}
		if err != nil || !more {
			sleep(ctx, time.Second)
		}
	}
	return ctx.Err()
}

func (w NativeWorker) retryInventory(ctx context.Context, retryer NativeInventoryRetryer) error {
	interval := w.InventoryRetryInterval
	if interval == 0 {
		interval = time.Minute
	}
	interval = max(interval, time.Second)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			attempt, cancel := context.WithTimeout(ctx, time.Minute)
			completed, err := retryer.RetryPending(attempt, 64)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				workerLog.Printf("native inventory retry deferred completed=%d error_type=%T", completed, err)
			}
			timer.Reset(interval)
		}
	}
}

// Reconcile admits bounded, generation-specific repairs. A complete App list
// is required before absent installations can produce withdrawal work.
func (w NativeWorker) Reconcile(ctx context.Context) error {
	_, err := w.ReconcileResult(ctx)
	return err
}

func (w NativeWorker) ReconcileResult(ctx context.Context) (DiscoveryResult, error) {
	result := DiscoveryResult{}
	if err := w.validate(); err != nil {
		return result, err
	}
	listed, err := (Backfill{}).nativeInstallations(ctx, w.Client, 100000)
	if err != nil {
		return result, err
	}
	result.Installations = len(listed)
	known, err := w.Enrollment.Installations(ctx)
	if err != nil {
		return result, err
	}
	identifiers := make(map[int64]bool, len(listed)+len(known))
	for installation := range listed {
		identifiers[installation] = true
	}
	for _, installation := range known {
		identifiers[installation] = true
	}
	if len(identifiers) > 100000 {
		return result, collectnative.ErrCapacity
	}
	ordered := make([]int64, 0, len(identifiers))
	for installation := range identifiers {
		ordered = append(ordered, installation)
	}
	slices.Sort(ordered)
	snapshot := time.Now().UTC().Truncate(w.interval()).Truncate(time.Second)
	for _, installation := range ordered {
		kind := IntentEnroll
		if !listed[installation] {
			kind = IntentRemoveInstallation
		}
		admission, err := w.Queue.enqueueNativeControl(ctx, Task{
			Repository: "installation/" + strconv.FormatInt(installation, 10), InstallationID: installation,
			Reason: "native-scope-repair", Scope: &ScopeChange{Kind: kind}, EnqueuedAt: snapshot,
		}, kind, "native-scope-repair:"+strconv.FormatInt(installation, 10)+":"+snapshot.Format(time.RFC3339),
			max(24*time.Hour, 2*w.interval()))
		if err != nil {
			return result, err
		}
		if admission.Enqueued {
			if kind == IntentEnroll {
				result.RefreshesQueued++
			} else {
				result.WithdrawalsQueued++
			}
		}
	}
	return result, nil
}

func (w NativeWorker) Step(ctx context.Context) (bool, error) {
	if err := w.validate(); err != nil {
		return false, err
	}
	leases, err := w.Queue.nativeControlLease(ctx, w.Consumer+"-control", 15*time.Minute)
	if err != nil || len(leases) == 0 {
		return false, err
	}
	lease := leases[0]
	if lease.Task.NotBefore.After(time.Now().UTC()) {
		return true, w.Queue.retryNativeControl(ctx, lease, false)
	}
	process, cancel := context.WithTimeout(ctx, 10*time.Minute)
	err = w.process(process, lease.Task)
	cancel()
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err == nil {
		return true, w.Queue.completeNativeControl(ctx, lease)
	}
	deferral := errors.Is(err, ErrEnrollmentMutationBusy) || errors.Is(err, collectnative.ErrBusy) || errors.Is(err, operational.ErrCapacity)
	var unavailable *githubquota.UnavailableError
	if errors.As(err, &unavailable) {
		lease.Task.NotBefore, deferral = unavailable.RetryAt, true
	}
	return true, errors.Join(err, w.Queue.retryNativeControl(ctx, lease, !deferral))
}

func (w NativeWorker) process(ctx context.Context, task Task) error {
	if !validNativeControl(task) {
		return collectnative.ErrIdentity
	}
	if task.Scope != nil || task.Lifecycle != "" {
		if task.Scope == nil {
			known, err := w.Enrollment.KnownInstallation(ctx, task.InstallationID)
			if err != nil || !known {
				return err
			}
			if task.ScopeEpoch > 0 {
				current, err := w.Enrollment.Authority.CurrentScope(ctx, task.Repository)
				if errors.Is(err, collectnative.ErrIdentity) || err == nil &&
					(current.Epoch != task.ScopeEpoch || current.InstallationID != task.InstallationID) {
					return nil
				}
				if err != nil {
					return err
				}
			}
		}
		return w.reconcileInstallation(ctx, task.InstallationID, task.EnqueuedAt)
	}
	scope, err := w.Enrollment.Authority.CurrentScope(ctx, task.Repository)
	if errors.Is(err, collectnative.ErrIdentity) || err == nil &&
		(scope.Epoch != task.ScopeEpoch || scope.InstallationID != task.InstallationID) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.InventoryRefresh {
		if err := w.Inventory.RefreshInventory(ctx, scope); err != nil {
			return err
		}
	}
	if task.Outcome != nil {
		if err := w.Outcomes.RecordOutcome(ctx, scope, *task.Outcome); err != nil {
			return err
		}
	}
	if task.ValueRefresh {
		return w.Values.InvalidateValue(ctx, scope)
	}
	return nil
}

func (w NativeWorker) reconcileInstallation(ctx context.Context, installation int64, snapshot time.Time) error {
	listed, err := (Backfill{}).nativeInstallations(ctx, w.Client, 100000)
	if err != nil {
		return err
	}
	if !listed[installation] {
		_, err := w.Enrollment.RemoveInstallation(ctx, installation)
		return err
	}
	if err := w.ScopeRefresher.discover(ctx, w.Enrollment, w.Queue, nil, installation); err != nil {
		return err
	}
	if snapshot.IsZero() {
		return collectnative.ErrIdentity
	}
	before := snapshot.UTC().Truncate(time.Second)
	after := time.Unix(0, 0).UTC()
	if w.WindowDays > 0 {
		after = before.Add(-time.Duration(w.WindowDays) * 24 * time.Hour)
	}
	for cursor, count := "", 0; ; {
		names, next, err := w.Enrollment.Authority.ScanRepositories(ctx, installation, cursor, 100)
		if err != nil {
			return err
		}
		count += len(names)
		if count > 100000 {
			return collectnative.ErrCapacity
		}
		for _, name := range names {
			scope, err := w.Enrollment.Authority.CurrentScope(ctx, name)
			if errors.Is(err, collectnative.ErrIdentity) {
				continue
			}
			if err != nil {
				return err
			}
			if scope.InstallationID != installation {
				continue
			}
			if err := w.Inventory.RefreshInventory(ctx, scope); err != nil {
				return err
			}
			if _, err := w.Queue.EnqueueRun(ctx, RunTask{Repository: name, InstallationID: installation,
				ScopeEpoch: scope.Epoch, Observation: "repair:" + before.Format(time.RFC3339),
				Enumeration: &RunEnumeration{Page: 1, WindowDays: w.WindowDays, After: after, Before: before}}); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		if next <= cursor {
			return collectnative.ErrIdentity
		}
		cursor = next
	}
}
