package collect

import (
	"context"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

type RunEnumeration struct {
	WorkflowID         int64     `json:"workflowId,omitempty"`
	RemainingWorkflows []int64   `json:"remainingWorkflows,omitempty"`
	NextWorkflowPage   int       `json:"nextWorkflowPage,omitempty"`
	Page               int       `json:"page"`
	WindowDays         int       `json:"windowDays"`
	After              time.Time `json:"after"`
	Before             time.Time `json:"before"`
}

func validRunEnumeration(task RunTask) bool {
	e := task.Enumeration
	if e == nil || task.RunID != 0 || task.Attempt != 0 || task.ScopeEpoch <= 0 ||
		e.WorkflowID < 0 || e.Page <= 0 || e.Page > 10000 || e.WindowDays < 0 || e.WindowDays > 365 ||
		e.After.IsZero() || !e.After.Before(e.Before) ||
		e.Before.Nanosecond() != 0 || e.After.Nanosecond() != 0 {
		return false
	}
	if len(e.RemainingWorkflows) > 99 || e.NextWorkflowPage < 0 || e.NextWorkflowPage > 10000 ||
		e.WorkflowID == 0 && (len(e.RemainingWorkflows) != 0 || e.NextWorkflowPage != 0) {
		return false
	}
	for _, workflow := range e.RemainingWorkflows {
		if workflow <= 0 {
			return false
		}
	}
	if e.WindowDays == 0 {
		return e.After.Equal(time.Unix(0, 0))
	}
	return e.After.Equal(e.Before.Add(-time.Duration(e.WindowDays) * 24 * time.Hour))
}

// enqueueNativeHistory persists the complete query window before any run API
// request. Queue identity retention keeps that snapshot immutable on restart.
func (b Backfill) enqueueNativeHistory(ctx context.Context, repositories []enrolledRepository) (int, int, error) {
	if err := b.Queue.EnsureRuns(ctx); err != nil {
		return 0, 0, err
	}
	if b.WindowDays < 0 || b.WindowDays > 365 {
		return 0, 0, errors.New("native historical window must be between 0 and 365 days")
	}
	before := b.runBefore
	if before.IsZero() {
		before = time.Now().UTC()
	}
	before = before.UTC().Truncate(time.Second)
	after := time.Unix(0, 0).UTC()
	if b.WindowDays > 0 {
		after = before.Add(-time.Duration(b.WindowDays) * 24 * time.Hour)
	}
	queued := 0
	for _, repository := range repositories {
		scope, err := b.AcquisitionScopes.CurrentScope(ctx, repository.name)
		if errors.Is(err, collectnative.ErrIdentity) {
			continue
		}
		if err != nil {
			return queued, 0, err
		}
		if scope.Repository != repository.name || scope.InstallationID != repository.installationID || scope.Epoch <= 0 {
			return queued, 0, collectnative.ErrIdentity
		}
		admitted, err := b.Queue.EnqueueRun(ctx, RunTask{
			Repository: repository.name, InstallationID: scope.InstallationID, ScopeEpoch: scope.Epoch,
			Enumeration: &RunEnumeration{Page: 1, WindowDays: b.WindowDays, After: after, Before: before},
		})
		if err != nil {
			return queued, 0, err
		}
		if admitted {
			queued++
		}
	}
	return queued, 0, nil
}

func (w RunWorker) enumerationScope(ctx context.Context, task RunTask) (collectnative.EnrollmentScope, error) {
	if w.Scopes == nil || w.Discovery == nil {
		return collectnative.EnrollmentScope{}, collectnative.ErrUnsupported
	}
	scope, err := w.Scopes.CurrentScope(ctx, task.Repository)
	if err != nil {
		return collectnative.EnrollmentScope{}, err
	}
	if scope.Repository != task.Repository || scope.InstallationID != task.InstallationID || scope.Epoch != task.ScopeEpoch {
		return collectnative.EnrollmentScope{}, collectnative.ErrClaimLost
	}
	return scope, nil
}

func (w RunWorker) enumerate(ctx context.Context, task RunTask) (*RunEnumeration, error) {
	scope, err := w.enumerationScope(ctx, task)
	if err != nil {
		return nil, err
	}
	e := *task.Enumeration
	var next int
	var tasks []RunTask
	if e.WorkflowID == 0 {
		var workflows []int64
		workflows, next, err = w.Discovery.DiscoveryWorkflows(ctx, scope, e.Page)
		if err == nil && len(workflows) > runBackfillPageSize {
			err = collectnative.ErrCapacity
		}
		for _, workflow := range workflows {
			if workflow <= 0 {
				return nil, collectnative.ErrIdentity
			}
		}
		if err != nil {
			return nil, err
		}
		if next != 0 && (next <= e.Page || next > 10000) {
			return nil, collectnative.ErrIdentity
		}
		if len(workflows) != 0 {
			e.WorkflowID, e.RemainingWorkflows = workflows[0], append([]int64(nil), workflows[1:]...)
			e.Page, e.NextWorkflowPage = 1, next
			return &e, nil
		}
	} else {
		var runs []collectnative.DiscoveredRun
		runs, next, err = w.Discovery.DiscoveryRuns(ctx, scope, e.WorkflowID, e.After, e.Before, e.Page)
		if err == nil && len(runs) > runBackfillPageSize {
			err = collectnative.ErrCapacity
		}
		for _, run := range runs {
			if run.Validate() != nil || run.Repository != scope.Repository || run.InstallationID != scope.InstallationID ||
				run.CreatedAt.IsZero() || run.CreatedAt.Before(e.After) || run.CreatedAt.After(e.Before) {
				return nil, collectnative.ErrIdentity
			}
			tasks = append(tasks, RunTask{Repository: scope.Repository, InstallationID: scope.InstallationID,
				ScopeEpoch: scope.Epoch, RunID: run.RunID, Attempt: run.Attempt, CreatedAt: run.CreatedAt})
		}
	}
	if err != nil {
		return nil, err
	}
	if next != 0 && (next <= e.Page || next > 10000) {
		return nil, collectnative.ErrIdentity
	}
	if _, err := w.enumerationScope(ctx, task); err != nil {
		return nil, err
	}
	for _, child := range tasks {
		if _, err := w.Queue.EnqueueRun(ctx, child); err != nil {
			return nil, err
		}
	}
	if next == 0 {
		switch {
		case len(e.RemainingWorkflows) > 0:
			e.WorkflowID, e.RemainingWorkflows = e.RemainingWorkflows[0], e.RemainingWorkflows[1:]
			e.Page = 1
		case e.NextWorkflowPage > 0:
			e.WorkflowID, e.Page, e.NextWorkflowPage = 0, e.NextWorkflowPage, 0
		default:
			//nolint:nilnil // A nil cursor marks successful exhaustion of the immutable query.
			return nil, nil
		}
		return &e, nil
	}
	e.Page = next
	return &e, nil
}
