package collect

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/mining"
)

const (
	defaultLifecycleRefreshInterval = 24 * time.Hour
	defaultLifecycleRefreshBatch    = 25
)

type NativeLifecycleStore interface {
	LifecycleRefreshDue(context.Context, collectnative.EnrollmentScope, time.Time) (bool, error)
	LifecycleRefreshTargets(context.Context, collectnative.EnrollmentScope, time.Time, int) ([]string, bool, error)
	RecordOutcome(context.Context, collectnative.EnrollmentScope, mining.Outcome) error
}

type NativeLifecycleReader interface {
	ReadOutcome(context.Context, int64, mining.Target) (mining.Outcome, githubquota.ResponseQuota, error)
	WorkflowRunQuotaRefresher
}

type NativeLifecycleService struct {
	Store           NativeLifecycleStore
	Client          NativeLifecycleReader
	Quota           RunQuotaService
	QuotaApp        string
	RefreshInterval time.Duration
	BatchSize       int
}

func (s NativeLifecycleService) interval() time.Duration {
	if s.RefreshInterval <= 0 {
		return defaultLifecycleRefreshInterval
	}
	return s.RefreshInterval
}

func (s NativeLifecycleService) batchSize() int {
	if s.BatchSize <= 0 {
		return defaultLifecycleRefreshBatch
	}
	return s.BatchSize
}

func (s NativeLifecycleService) validate() error {
	if s.Store == nil || s.Client == nil || s.Quota == nil || strings.TrimSpace(s.QuotaApp) == "" ||
		s.interval() < time.Hour || s.batchSize() < 1 || s.batchSize() > 100 {
		return collectnative.ErrUnsupported
	}
	return nil
}

func (s NativeLifecycleService) Due(
	ctx context.Context,
	scope collectnative.EnrollmentScope,
	now time.Time,
) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.Store.LifecycleRefreshDue(ctx, scope, now.UTC().Add(-s.interval()))
}

func (s NativeLifecycleService) Refresh(
	ctx context.Context,
	scope collectnative.EnrollmentScope,
	force bool,
) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	before := time.Now().UTC().Add(-s.interval())
	if force {
		before = time.Time{}
	}
	targets, more, err := s.Store.LifecycleRefreshTargets(ctx, scope, before, s.batchSize())
	if err != nil {
		return false, err
	}
	for _, raw := range targets {
		target, err := mining.ParseTarget(raw)
		if err != nil || target.Repository != scope.Repository {
			return false, collectnative.ErrIdentity
		}
		reservation, err := reserveCollectionQuota(ctx, s.Quota, s.QuotaApp, scope.InstallationID, s.Client)
		if err != nil {
			return false, err
		}
		outcome, response, readErr := s.Client.ReadOutcome(ctx, scope.InstallationID, target)
		if quotaErr := reconcileCollectionQuota(ctx, s.Quota, reservation.Bucket, reservation, response); quotaErr != nil {
			return false, quotaErr
		}
		if readErr != nil {
			return false, readErr
		}
		if outcome.Source != "github-api" {
			return false, errors.New("lifecycle refresh requires authoritative GitHub API evidence")
		}
		if err := s.Store.RecordOutcome(ctx, scope, outcome); err != nil {
			return false, err
		}
	}
	return more, nil
}
