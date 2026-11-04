package collect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/ingest"
)

type NativePublicationStore interface {
	collectnative.PublicationConsumer
	State(context.Context) (dashboardstore.State, error)
}

type NativeScopeEnumerator interface {
	RepositoryEnumerator
	ListInstallationsPage(context.Context, int) ([]githubapp.Installation, int, error)
}

func (b Backfill) replayNative(ctx context.Context) (ingest.Result, int, error) {
	if b.NativePublications == nil || b.AcquisitionScopes == nil {
		return ingest.Result{}, 0, fmt.Errorf("%w: native replay requires durable publication and scope adapters", collectnative.ErrUnsupported)
	}
	activated := 0
	for {
		count, err := b.NativePublications.Replay(ctx, 128)
		if err != nil {
			return ingest.Result{}, activated, err
		}
		activated += count
		if count < 128 {
			break
		}
	}
	state, err := b.NativePublications.State(ctx)
	if err != nil {
		return ingest.Result{}, activated, err
	}
	evaluated := ""
	if !state.EvaluatedAt.IsZero() {
		evaluated = state.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	}
	return ingest.Result{Revision: state.Revision, DataRevision: state.DataRevision,
		EvaluatedAt: evaluated, Counts: state.Counts}, activated, nil
}

func (b Backfill) nativeInstallations(ctx context.Context, client NativeScopeEnumerator, limit int) (map[int64]bool, error) {
	listed := map[int64]bool{}
	for page := 1; page != 0; {
		if page > 1000 {
			return nil, collectnative.ErrCapacity
		}
		items, next, err := client.ListInstallationsPage(ctx, page)
		if err != nil {
			return nil, err
		}
		if len(items) > 100 || next != 0 && next <= page {
			return nil, collectnative.ErrIdentity
		}
		for _, installation := range items {
			if installation.ID <= 0 || listed[installation.ID] {
				return nil, collectnative.ErrIdentity
			}
			listed[installation.ID] = true
			if len(listed) > limit {
				return nil, collectnative.ErrCapacity
			}
		}
		page = next
	}
	return listed, nil
}

func (b Backfill) nativeRepositories(ctx context.Context, limit int) ([]enrolledRepository, error) {
	var repositories []enrolledRepository
	for cursor := ""; ; {
		names, next, err := b.Enrollment.Authority.ScanRepositories(ctx, 0, cursor, 500)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			scope, err := b.AcquisitionScopes.CurrentScope(ctx, name)
			if errors.Is(err, collectnative.ErrIdentity) {
				continue
			}
			if err != nil {
				return nil, err
			}
			repositories = append(repositories, enrolledRepository{name: name, installationID: scope.InstallationID})
			if len(repositories) > limit {
				return nil, collectnative.ErrCapacity
			}
		}
		if next == "" {
			return repositories, nil
		}
		if next <= cursor {
			return nil, errors.New("native enrollment returned a non-advancing repository cursor")
		}
		cursor = next
	}
}

func (b Backfill) refreshNativeScope(ctx context.Context, refresher ScopeRefresher, installation int64) error {
	for {
		err := refresher.discover(ctx, b.Enrollment, b.Queue, nil, installation)
		if !errors.Is(err, ErrEnrollmentMutationBusy) {
			return err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (b Backfill) runNative(ctx context.Context, state BackfillState) (BackfillState, error) {
	client, ok := b.Enumerator.(NativeScopeEnumerator)
	if !ok || b.Enrollment.Authority == nil || b.NativePublications == nil || b.AcquisitionScopes == nil ||
		b.Quota == nil || b.ReconstructScope {
		return b.fail(ctx, state, fmt.Errorf("%w: native cold start requires authoritative paged scope, quota and publication adapters", collectnative.ErrUnsupported))
	}
	if err := b.Queue.EnsureRuns(ctx); err != nil {
		return b.fail(ctx, state, err)
	}
	limit := b.ScopeLimit
	if limit <= 0 {
		limit = 100000
	}
	state.Phase = "enumerating"
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	listed, err := b.nativeInstallations(ctx, client, limit)
	if err != nil {
		return b.fail(ctx, state, err)
	}
	refresher := ScopeRefresher{Client: client, Quota: b.Quota, QuotaApp: b.QuotaApp, RepositoryLimit: limit}
	identifiers := make([]int64, 0, len(listed))
	for installation := range listed {
		identifiers = append(identifiers, installation)
	}
	slices.Sort(identifiers)
	for _, installation := range identifiers {
		if err := b.refreshNativeScope(ctx, refresher, installation); err != nil {
			if ctx.Err() != nil {
				return b.fail(ctx, state, ctx.Err())
			}
			state.EnumerationFailures++
			backfillLog.Printf("native scope refresh deferred installation=%d", installation)
		}
	}
	known, err := b.Enrollment.Installations(ctx)
	if err != nil {
		return b.fail(ctx, state, err)
	}
	for _, installation := range known {
		if !listed[installation] {
			if _, err := b.Enrollment.RemoveInstallation(ctx, installation); err != nil {
				return b.fail(ctx, state, err)
			}
		}
	}
	repositories, err := b.nativeRepositories(ctx, limit)
	if err != nil {
		return b.fail(ctx, state, err)
	}
	state.Installations, state.Repositories = len(listed), len(repositories)
	state.Phase = "replaying"
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	published, activated, err := b.replayNative(ctx)
	if err != nil {
		return b.fail(ctx, state, err)
	}
	state.Revision, state.NativePublications = published.Revision, activated
	if b.ScopeReady != nil {
		if err := b.ScopeReady(ctx); err != nil {
			return b.fail(ctx, state, err)
		}
	}
	state.Phase = "seeding"
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	queued, _, err := b.enqueueNativeHistory(ctx, repositories)
	if err != nil {
		return b.fail(ctx, state, err)
	}
	state.QueuedEnumerations = queued
	state.Phase = "collecting"
	if state.EnumerationFailures != 0 {
		state.Phase = "partial"
	}
	state.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := b.publish(ctx, state); err != nil {
		return b.fail(ctx, state, err)
	}
	return state, nil
}
