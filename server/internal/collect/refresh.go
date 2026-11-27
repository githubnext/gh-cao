package collect

import (
	"context"
	"errors"
	"fmt"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var refreshLog = logger.New("cao:collect:refresh")

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
				refreshLog.Printf("repository discovery exceeded bound: limit=%d", limit)
				return errors.New("installation repository discovery exceeds the configured bound")
			}
			if next != 0 && next <= page {
				refreshLog.Printf("repository discovery page did not advance: page=%d", page)
				return errors.New("repository discovery returned a non-advancing page")
			}
			names, ids, err := activeRepositories(items)
			if err != nil {
				return err
			}
			repositories = append(repositories, names...)
			identifiers = append(identifiers, ids...)
			page = next
		}
		// Publish only a complete successful enumeration. A partial page set
		// must never enroll repositories or be interpreted as deletion.
		previous, err := enrollment.RepositoriesForInstallation(ctx, installationID)
		if err != nil {
			return err
		}
		removed, previousSet := diffRepositories(previous, repositories)
		refreshLog.Printf("repository discovery complete: discovered=%d previous=%d removed=%d",
			len(repositories), len(previous), len(removed))
		if enrollment.Authority == nil {
			admitter := Admitter{Queue: queue}
			if err := admitter.queueErasures(ctx, installationID, removed); err != nil {
				return err
			}
			// Legacy tasks are admitted before membership publication. Native
			// ownership commits its erasure atomically, without file consumers.
			for _, name := range repositories {
				if previousSet[name] {
					continue
				}
				if _, err := queue.Enqueue(ctx, Task{Repository: name, InstallationID: installationID,
					Reason: "repository-discovery"}); err != nil {
					return err
				}
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

// activeRepositories drops archived repositories and returns the normalized
// names with their identifiers, in discovery order.
func activeRepositories(items []githubapp.Repository) ([]string, []int64, error) {
	var names []string
	var identifiers []int64
	for _, item := range items {
		if item.Archived {
			continue
		}
		name, err := NormalizeRepository(item.FullName)
		if err != nil {
			return nil, nil, fmt.Errorf("normalize discovered repository: %w", err)
		}
		names = append(names, name)
		identifiers = append(identifiers, item.ID)
	}
	return names, identifiers, nil
}

// diffRepositories returns the previously enrolled repositories absent from
// current, in previous order, and the set of all previous repositories.
func diffRepositories(previous, current []string) ([]string, map[string]bool) {
	present := make(map[string]bool, len(current))
	for _, name := range current {
		present[name] = true
	}
	var removed []string
	previousSet := make(map[string]bool, len(previous))
	for _, name := range previous {
		previousSet[name] = true
		if !present[name] {
			removed = append(removed, name)
		}
	}
	return removed, previousSet
}
