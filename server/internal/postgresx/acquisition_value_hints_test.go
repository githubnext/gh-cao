package postgresx_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeValueHintsFenceConcurrentInvalidationAndAcknowledgement(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	a, b := first.Acquisition(), second.Acquisition()
	identity := acquisitionIdentity()
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	scope, err := a.CurrentScope(t.Context(), identity.Repository)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	var workers sync.WaitGroup
	for _, acquisition := range []postgresx.Acquisition{a, b} {
		workers.Go(func() {
			for range 8 {
				if err := acquisition.InvalidateValue(t.Context(), scope); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	workers.Wait()
	hint, err := b.ValueHint(t.Context(), scope)
	if err != nil || hint.Dirty != 16 || hint.Processed != 0 {
		t.Fatalf("concurrent invalidation lost a committed generation: %+v %v", hint, err)
	}
	if err := a.InvalidateValue(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := b.CompleteValueHint(t.Context(), scope, hint.Dirty, now, time.Time{}); err != nil {
		t.Fatal(err)
	}
	hint, err = a.ValueHint(t.Context(), scope)
	if err != nil || hint.Dirty != 17 || hint.Processed != 16 || hint.AttemptDay != now.Format("2006-01-02") {
		t.Fatalf("acknowledgement swallowed concurrent invalidation: %+v %v", hint, err)
	}
	if err := b.CompleteValueHint(t.Context(), scope, hint.Dirty, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	hint, err = a.ValueHint(t.Context(), scope)
	if err != nil || hint.Processed != 16 || !hint.RetryAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("failed acquisition acknowledged dirty evidence: %+v %v", hint, err)
	}
	if err := a.CompleteValueHint(t.Context(), scope, hint.Dirty+1, now, time.Time{}); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("unobserved generation was acknowledged: %v", err)
	}
	if err := a.CompleteValueHint(t.Context(), scope, hint.Dirty, now, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteValueHint(t.Context(), scope, 16, now, now.Add(time.Hour)); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("obsolete generation changed the newer consumer state: %v", err)
	}
	if err := a.SetEnrollment(t.Context(), identity.Repository, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err := b.InvalidateValue(t.Context(), scope); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("old delivery invalidated a new consent epoch: %v", err)
	}
	if err := b.CompleteValueHint(t.Context(), scope, 17, now, time.Time{}); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("old completion crossed a consent epoch: %v", err)
	}
	current, err := a.CurrentScope(t.Context(), identity.Repository)
	if err != nil {
		t.Fatal(err)
	}
	hint, err = b.ValueHint(t.Context(), current)
	if err != nil || hint.Dirty != 0 || hint.Processed != 0 || hint.AttemptDay != "" || !hint.RetryAt.IsZero() {
		t.Fatalf("reenrollment recovered a previous consent's invalidation: %+v %v", hint, err)
	}
}
