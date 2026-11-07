package collect

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func (a Admitter) admitNativeEvent(ctx context.Context, intent Intent, delivery string, ttl time.Duration) (Admission, error) {
	if _, err := a.Queue.fencedRunQueue(); err != nil {
		return Admission{}, err
	}
	if delivery != "" {
		if a.Queue.Deliveries == nil {
			return Admission{}, collectnative.ErrUnsupported
		}
		reserved, err := a.Queue.Deliveries.ReserveDelivery(ctx, delivery, 30*time.Second)
		if err != nil {
			return Admission{}, err
		}
		switch reserved {
		case operational.DeliveryAlreadyCommitted:
			return Admission{Kind: intent.Kind, Duplicate: true}, nil
		case operational.DeliveryInProgress:
			return Admission{}, ErrDeliveryInProgress
		case operational.DeliveryReserved:
		default:
			return Admission{}, errors.New("unknown native delivery reservation")
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = a.Queue.Deliveries.ReleaseDeliveryReservation(cleanup, delivery)
		}()
	}
	admission, err := a.enqueueNativeIntent(ctx, intent, delivery, ttl)
	if err != nil || delivery == "" || admission.Enqueued || admission.Duplicate {
		return admission, err
	}
	// Ignored events have no task on which to atomically commit the delivery.
	fresh, err := a.Queue.Deliveries.RememberDelivery(ctx, delivery, ttl)
	admission.Duplicate = !fresh
	return admission, err
}

func (a Admitter) enqueueNativeIntent(ctx context.Context, intent Intent, delivery string, ttl time.Duration) (Admission, error) {
	if intent.Kind == IntentIgnore {
		return Admission{Kind: intent.Kind}, nil
	}
	task := Task{Repository: intent.Repository, InstallationID: intent.InstallationID, Reason: intent.Reason,
		RepositoryID: intent.RepositoryID, Lifecycle: intent.Lifecycle, ValueRefresh: intent.ValueRefresh,
		InventoryRefresh: intent.Kind == IntentWorkflowRefresh}
	if a.Mining {
		task.Outcome = intent.Outcome
	}
	if isScopeIntent(intent.Kind) {
		if intent.InstallationID <= 0 {
			return Admission{}, collectnative.ErrIdentity
		}
		// Current App coverage, not an old payload's repository list, decides
		// both additions and withdrawals when a replica consumes this task.
		task.Repository = "installation/" + strconv.FormatInt(intent.InstallationID, 10)
		task.Scope = &ScopeChange{Kind: intent.Kind}
	} else {
		name, err := NormalizeRepository(intent.Repository)
		if err != nil {
			return Admission{}, err
		}
		task.Repository = name
		scope, err := a.Enrollment.Authority.CurrentScope(ctx, name)
		switch {
		case errors.Is(err, collectnative.ErrIdentity) && intent.Kind == IntentRepository:
			known, lookupErr := a.Enrollment.KnownInstallation(ctx, intent.InstallationID)
			if lookupErr != nil {
				return Admission{}, lookupErr
			}
			if !known {
				return Admission{}, ErrNotEnrolled
			}
		case err != nil:
			return Admission{}, err
		case scope.InstallationID != intent.InstallationID:
			return Admission{}, ErrNotEnrolled
		default:
			task.ScopeEpoch = scope.Epoch
		}
		if intent.Kind == IntentCollect {
			if intent.WorkflowPath == "" {
				return Admission{}, collectnative.ErrIdentity
			}
			if !strings.HasSuffix(intent.WorkflowPath, ".lock.yml") {
				return Admission{Kind: IntentIgnore}, nil
			}
			if !collectnative.IsAgenticWorkflowPath(intent.WorkflowPath) {
				return Admission{}, collectnative.ErrIdentity
			}
			if _, err := a.Queue.EnqueueRun(ctx, RunTask{Repository: name, InstallationID: intent.InstallationID,
				ScopeEpoch: task.ScopeEpoch, RunID: intent.RunID, Attempt: intent.RunAttempt, Observation: "completed"}); err != nil {
				return Admission{}, err
			}
		}
	}
	return a.Queue.enqueueNativeControl(ctx, task, intent.Kind, delivery, ttl)
}
