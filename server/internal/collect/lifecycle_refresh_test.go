package collect

import (
	"context"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/mining"
)

type lifecycleStoreFixture struct {
	targets  []string
	more     bool
	before   time.Time
	recorded []mining.Outcome
}

func (f *lifecycleStoreFixture) LifecycleRefreshDue(
	context.Context, collectnative.EnrollmentScope, time.Time,
) (bool, error) {
	return len(f.targets) > 0, nil
}

func (f *lifecycleStoreFixture) LifecycleRefreshTargets(
	_ context.Context,
	_ collectnative.EnrollmentScope,
	before time.Time,
	_ int,
) ([]string, bool, error) {
	f.before = before
	return f.targets, f.more, nil
}

func (f *lifecycleStoreFixture) RecordOutcome(
	_ context.Context,
	_ collectnative.EnrollmentScope,
	outcome mining.Outcome,
) error {
	f.recorded = append(f.recorded, outcome)
	return nil
}

type lifecycleReaderFixture struct {
	outcome      mining.Outcome
	target       mining.Target
	installation int64
	calls        int
}

func (f *lifecycleReaderFixture) ReadOutcome(
	_ context.Context, installation int64, target mining.Target,
) (mining.Outcome, githubquota.ResponseQuota, error) {
	f.calls++
	f.installation = installation
	f.target = target
	return f.outcome, githubquota.ResponseQuota{
		HasResponse: true, Resource: githubquota.ResourceCore, HasObservation: true,
		Observation: githubquota.Observation{
			Limit: 5000, Remaining: 4999, ResetAt: time.Now().UTC().Add(time.Hour),
		},
	}, nil
}

func (f *lifecycleReaderFixture) QuotaRateLimit(context.Context, int64) (githubquota.ResponseQuota, error) {
	return githubquota.ResponseQuota{}, nil
}

func TestNativeLifecycleRefreshRereadsAuthoritativeState(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := &lifecycleStoreFixture{
		targets: []string{"https://github.com/octo/api/issues/7"},
		more:    true,
	}
	reader := &lifecycleReaderFixture{outcome: mining.Outcome{
		Repository: "octo/api", Number: 7, URL: "https://github.com/octo/api/issues/7",
		State: "open", UpdatedAt: now, ObservedAt: now, Source: "github-api",
	}}
	quota := &fakeRunQuota{known: true}
	service := NativeLifecycleService{
		Store: store, Client: reader, Quota: quota, QuotaApp: "github-app-1",
	}
	scope := collectnative.EnrollmentScope{Repository: "octo/api", InstallationID: 1, Epoch: 1}
	more, err := service.Refresh(t.Context(), scope, true)
	if err != nil {
		t.Fatal(err)
	}
	if !more || !store.before.IsZero() || reader.calls != 1 || len(store.recorded) != 1 ||
		reader.installation != scope.InstallationID || reader.target.Repository != scope.Repository ||
		store.recorded[0].Source != "github-api" || len(quota.commits) != 1 {
		t.Fatalf("authoritative refresh = more:%t before:%s calls:%d recorded:%+v commits:%d",
			more, store.before, reader.calls, store.recorded, len(quota.commits))
	}
}

func TestNativeLifecycleRepairUsesDailyStalenessCutoff(t *testing.T) {
	store := &lifecycleStoreFixture{}
	service := NativeLifecycleService{
		Store: store, Client: &lifecycleReaderFixture{}, Quota: &fakeRunQuota{known: true},
		QuotaApp: "github-app-1",
	}
	scope := collectnative.EnrollmentScope{Repository: "octo/api", InstallationID: 1, Epoch: 1}
	if _, err := service.Refresh(t.Context(), scope, false); err != nil {
		t.Fatal(err)
	}
	if store.before.IsZero() || time.Since(store.before) < 23*time.Hour {
		t.Fatalf("repair cutoff = %s", store.before)
	}
}
