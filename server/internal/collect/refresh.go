package collect

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

// RepositoryEnumerator keeps installation discovery paginated and quota-governed.
type RepositoryEnumerator interface {
	WorkflowRunQuotaRefresher
	ListRepositoriesPage(context.Context, int64, int) ([]githubapp.Repository, int, githubquota.ResponseQuota, error)
}

type ScopeRefresher struct {
	Client          RepositoryEnumerator
	Quota           RunQuotaService
	QuotaApp        string
	RepositoryLimit int
}

func (r ScopeRefresher) discover(ctx context.Context, enrollment Enrollment, queue Queue, data *postgresx.Store, installationID int64) error {
	if r.Client == nil {
		return errors.New("scope refresh requires a GitHub repository enumerator")
	}
	limit := r.RepositoryLimit
	if limit <= 0 {
		limit = 100_000
	}
	return enrollment.withMutationLock(ctx, func(ctx context.Context) error {
		var repositories []string
		var identifiers []int64
		count := 0
		for page := 1; page != 0; {
			reservation, err := reserveCollectionQuota(ctx, r.Quota, r.QuotaApp, installationID, r.Client)
			if err != nil {
				return err
			}
			items, next, response, listErr := r.Client.ListRepositoriesPage(ctx, installationID, page)
			if err := reconcileCollectionQuota(ctx, r.Quota, reservation.Bucket, reservation, response); err != nil {
				return err
			}
			if listErr != nil {
				return listErr
			}
			count += len(items)
			if count > limit {
				return errors.New("installation repository discovery exceeds the configured bound")
			}
			if next != 0 && next <= page {
				return errors.New("repository discovery returned a non-advancing page")
			}
			for _, item := range items {
				if item.Archived {
					continue
				}
				name, err := NormalizeRepository(item.FullName)
				if err != nil {
					return fmt.Errorf("normalize discovered repository: %w", err)
				}
				repositories = append(repositories, name)
				identifiers = append(identifiers, item.ID)
			}
			page = next
		}
		// Publish only a complete successful enumeration. A partial page set
		// must never enroll repositories or be interpreted as deletion.
		previous, err := enrollment.RepositoriesForInstallation(ctx, installationID)
		if err != nil {
			return err
		}
		current := make(map[string]bool, len(repositories))
		for _, name := range repositories {
			current[name] = true
		}
		var removed []string
		previousSet := make(map[string]bool, len(previous))
		for _, name := range previous {
			previousSet[name] = true
			if !current[name] {
				removed = append(removed, name)
			}
		}
		admitter := Admitter{Queue: queue}
		if err := admitter.queueErasures(ctx, installationID, removed); err != nil {
			return err
		}
		// Admission precedes publication. Discovery collection tasks check
		// membership under the same mutation lease before running.
		for _, name := range repositories {
			if previousSet[name] {
				continue
			}
			if _, err := queue.Enqueue(ctx, Task{Repository: name, InstallationID: installationID,
				Reason: "repository-discovery"}); err != nil {
				return err
			}
		}
		if _, err := enrollment.removeRepositories(ctx, installationID, removed); err != nil {
			return err
		}
		if data != nil {
			if err := data.ClearRepositoryLifecycles(ctx, identifiers); err != nil {
				return err
			}
		}
		if err := enrollment.addRepositories(ctx, installationID, repositories); err != nil {
			return err
		}
		return nil
	})
}

func (w Worker) processRefresh(ctx context.Context, lease Lease) (err error) {
	ctx, cancel := context.WithTimeout(ctx, min(w.Runner.timeout(), w.repositoryLockTTL()/2))
	defer cancel()
	task := lease.Task
	kind := "workflows"
	switch {
	case task.Lifecycle != "":
		kind = "repository"
	case task.Scope != nil:
		kind = string(task.Scope.Kind)
	}
	ctx, finish := startRefreshTelemetry(ctx, "worker", kind)
	recordRefreshQueueAge(ctx, task, kind)
	defer func() {
		outcome := "completed"
		if err != nil {
			outcome = "failed"
		}
		finish(outcome, err)
	}()
	switch {
	case task.Lifecycle != "":
		known, err := w.Enrollment.KnownInstallation(ctx, task.InstallationID)
		if err != nil {
			return err
		}
		owner, err := w.Enrollment.InstallationFor(ctx, task.Repository)
		if err != nil {
			return err
		}
		if !known || (owner != 0 && owner != task.InstallationID) {
			return w.Queue.Complete(ctx, lease)
		}
		if err := w.ScopeRefresher.discover(ctx, w.Enrollment, w.Queue, w.Projector.Data, task.InstallationID); err != nil {
			return err
		}
	case task.Scope != nil:
		if !isScopeIntent(task.Scope.Kind) {
			return errors.New("unsupported scope refresh kind")
		}
		if task.Scope.Kind == IntentEnroll {
			if err := w.ScopeRefresher.discover(ctx, w.Enrollment, w.Queue, w.Projector.Data, task.InstallationID); err != nil {
				return err
			}
		} else {
			if task.Reason == "repository-list-refresh" {
				client, ok := w.ScopeRefresher.Client.(InstallationEnumerator)
				if !ok {
					return errors.New("installation withdrawal requires current App enumeration")
				}
				current, err := client.ListInstallations(ctx)
				if err != nil {
					return err
				}
				for _, installation := range current {
					if installation.ID == task.InstallationID {
						return w.Queue.Complete(ctx, lease)
					}
				}
			}
			admitter := Admitter{Enrollment: w.Enrollment, Queue: w.Queue}
			if _, err := admitter.admitIntent(ctx, Intent{
				Kind: task.Scope.Kind, InstallationID: task.InstallationID,
				Repositories: task.Scope.Repositories,
			}); err != nil {
				return err
			}
		}
	default:
		owner, err := w.Enrollment.InstallationFor(ctx, task.Repository)
		if err != nil {
			return err
		}
		if owner == 0 || owner != task.InstallationID {
			return w.Queue.Complete(ctx, lease)
		}
	}
	// Persist the follow-up before acknowledging: a crash cannot lose the
	// inventory refresh after committing scope changes.
	if err := w.Projector.RequestProjection(ctx); err != nil {
		return err
	}
	return w.Queue.Complete(ctx, lease)
}

func (w Worker) retryRefresh(ctx context.Context, lease Lease, cause error) {
	if errors.Is(cause, ErrEnrollmentMutationBusy) {
		lease.Task.NotBefore = time.Now().UTC().Add(backoff(max(lease.Task.Attempt, 1)))
		if err := w.Queue.RequeueBlocked(ctx, lease); err != nil {
			workerLog.Printf("blocked refresh requeue failed")
		}
		return
	}
	var unavailable *githubquota.UnavailableError
	if errors.As(cause, &unavailable) {
		lease.Task.NotBefore = unavailable.RetryAt
		if !lease.Task.NotBefore.After(time.Now().UTC()) {
			lease.Task.NotBefore = time.Now().UTC().Add(time.Minute)
		}
		lease.Task.NotBefore = lease.Task.NotBefore.Add(backoff(1))
		if err := w.Queue.Defer(ctx, lease); err != nil {
			workerLog.Printf("quota-delayed refresh scheduling failed")
		}
		return
	}
	if err := w.Queue.Retry(ctx, lease, cause); err != nil {
		workerLog.Printf("refresh retry scheduling failed")
	}
}
