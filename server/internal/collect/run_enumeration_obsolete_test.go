package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type changingEnumerationDiscovery struct {
	*nativeHistoryFixture
	afterCall func() error
}

type enumerationScopeFailure struct {
	err error
}

func (s enumerationScopeFailure) CurrentScope(context.Context, string) (collectnative.EnrollmentScope, error) {
	return collectnative.EnrollmentScope{}, s.err
}

func (p changingEnumerationDiscovery) DiscoveryWorkflows(ctx context.Context, scope collectnative.EnrollmentScope, page int) ([]int64, int, error) {
	workflows, next, err := p.nativeHistoryFixture.DiscoveryWorkflows(ctx, scope, page)
	if err == nil && p.afterCall != nil {
		err = p.afterCall()
	}
	return workflows, next, err
}

func (p changingEnumerationDiscovery) DiscoveryRuns(ctx context.Context, scope collectnative.EnrollmentScope, workflow int64, after, before time.Time, page int) ([]collectnative.DiscoveredRun, int, error) {
	runs, next, err := p.nativeHistoryFixture.DiscoveryRuns(ctx, scope, workflow, after, before, page)
	if err == nil && p.afterCall != nil {
		err = p.afterCall()
	}
	return runs, next, err
}

func TestNativeEnumerationAcknowledgesObsoleteEpochs(t *testing.T) {
	for _, scenario := range []string{"withdrawn", "transferred", "reenrolled", "workflow-discovery", "run-discovery", "provider-claim-lost", "provider-error"} {
		t.Run(scenario, func(t *testing.T) {
			data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
			queue, _, _ := nativeRunQueue(t)
			queue.MaxLength = 1
			acquisition := data.Acquisition()
			if err := acquisition.SetEnrollment(t.Context(), "octo/api", 7); err != nil {
				t.Fatal(err)
			}
			before := time.Now().UTC().Truncate(time.Second)
			backfill := Backfill{Queue: queue, WindowDays: 7, AcquisitionScopes: acquisition, runBefore: before}
			repositories := []enrolledRepository{{name: "octo/api", installationID: 7}}
			if _, _, err := backfill.enqueueNativeHistory(t.Context(), repositories); err != nil {
				t.Fatal(err)
			}
			leases, err := queue.LeaseRuns(t.Context(), "obsolete", 1)
			if err != nil || len(leases) != 1 {
				t.Fatalf("enumeration lease: %v %v", leases, err)
			}
			lease := leases[0]
			if scenario == "run-discovery" {
				lease.Task.Enumeration.WorkflowID = 41
			}
			change := func() error {
				installation := int64(0)
				if scenario == "transferred" {
					installation = 9
				}
				if err := acquisition.SetEnrollment(t.Context(), "octo/api", installation); err != nil {
					return err
				}
				if scenario == "reenrolled" {
					return acquisition.SetEnrollment(t.Context(), "octo/api", 7)
				}
				return nil
			}
			fixture := &nativeHistoryFixture{after: before.Add(-7 * 24 * time.Hour), before: before}
			discovery := changingEnumerationDiscovery{nativeHistoryFixture: fixture}
			if scenario == "workflow-discovery" || scenario == "run-discovery" || scenario == "provider-claim-lost" || scenario == "provider-error" {
				discovery.afterCall = func() error {
					if err := change(); err != nil {
						return err
					}
					if scenario == "provider-claim-lost" {
						return collectnative.ErrClaimLost
					}
					if scenario == "provider-error" {
						return errors.New("provider access removed during acquisition")
					}
					return nil
				}
			} else if err := change(); err != nil {
				t.Fatal(err)
			}
			worker := RunWorker{Queue: queue, Scopes: acquisition, Discovery: discovery}
			if err := worker.process(t.Context(), lease); err != nil {
				t.Fatalf("obsolete enumeration was deferred: %v", err)
			}
			if discovery.afterCall == nil && fixture.calls != 0 {
				t.Fatal("obsolete enumeration called discovery")
			}
			for _, stream := range []string{runEnumerationStream, runTaskStream, runDeadLetterStream} {
				stats, err := queue.Tasks.QueueStats(t.Context(), stream, queue.group())
				if err != nil || stats.Length != 0 || stats.Pending != 0 {
					t.Fatalf("obsolete discovery left a cursor or child task in %s: %+v %v", stream, stats, err)
				}
			}
			if err := worker.process(t.Context(), lease); !errors.Is(err, operational.ErrTaskLeaseLost) {
				t.Fatalf("reused obsolete receipt was accepted: %v", err)
			}
			if err := acquisition.SetEnrollment(t.Context(), "octo/api", 7); err != nil {
				t.Fatal(err)
			}
			if count, _, err := backfill.enqueueNativeHistory(t.Context(), repositories); err != nil || count != 1 {
				t.Fatalf("obsolete enumeration consumed bounded capacity: %d %v", count, err)
			}
			current, err := queue.LeaseRuns(t.Context(), "current", 1)
			if err != nil || len(current) != 1 || current[0].Task.ScopeEpoch == lease.Task.ScopeEpoch {
				t.Fatalf("current epoch not admitted: %v %v", current, err)
			}
			worker.Discovery = fixture
			if err := worker.process(t.Context(), current[0]); err != nil {
				t.Fatalf("current enumeration could not advance: %v", err)
			}
			cursor, err := queue.LeaseRuns(t.Context(), "inspect", 1)
			if err != nil || len(cursor) != 1 || cursor[0].Task.Enumeration.WorkflowID != 41 {
				t.Fatalf("current enumeration lost its cursor: %v %v", cursor, err)
			}
		})
	}
}

func TestNativeEnumerationDefersClaimContentionWithinCurrentEpoch(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	acquisition := data.Acquisition()
	if err := acquisition.SetEnrollment(t.Context(), "octo/api", 7); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Truncate(time.Second)
	backfill := Backfill{Queue: queue, WindowDays: 7, AcquisitionScopes: acquisition, runBefore: before}
	if _, _, err := backfill.enqueueNativeHistory(t.Context(), []enrolledRepository{{name: "octo/api", installationID: 7}}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.LeaseRuns(t.Context(), "contended", 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("initial lease: %v %v", leases, err)
	}
	discovery := changingEnumerationDiscovery{
		nativeHistoryFixture: &nativeHistoryFixture{after: before.Add(-7 * 24 * time.Hour), before: before},
		afterCall:            func() error { return collectnative.ErrClaimLost },
	}
	worker := RunWorker{Queue: queue, Scopes: acquisition, Discovery: discovery}
	if err := worker.process(t.Context(), leases[0]); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("current-epoch contention was not preserved: %v", err)
	}
	if _, err := queue.Tasks.PromoteTasks(t.Context(), delayedRunEnumerationSet, runEnumerationStream, before.Add(time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	deferred, err := queue.LeaseRuns(t.Context(), "retry", 1)
	if err != nil || len(deferred) != 1 || deferred[0].Task.Failures != 0 ||
		deferred[0].Task.ScopeEpoch != leases[0].Task.ScopeEpoch || deferred[0].Task.Enumeration.WorkflowID != 0 {
		t.Fatalf("contention lost the original cursor/epoch: %v %v", deferred, err)
	}
}

func TestNativeEnumerationRetriesScopeReadFailure(t *testing.T) {
	queue, _, _ := nativeRunQueue(t)
	before := time.Now().UTC().Truncate(time.Second)
	task := RunTask{Repository: "octo/api", InstallationID: 7, ScopeEpoch: 1,
		Enumeration: &RunEnumeration{After: before.Add(-7 * 24 * time.Hour), Before: before, WindowDays: 7, Page: 1}}
	if _, err := queue.EnqueueRun(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.LeaseRuns(t.Context(), "scope-unavailable", 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("initial lease: %v %v", leases, err)
	}
	unavailable := errors.New("scope database unavailable")
	fixture := &nativeHistoryFixture{after: task.Enumeration.After, before: before}
	worker := RunWorker{Queue: queue, Scopes: enumerationScopeFailure{err: unavailable}, Discovery: fixture}
	if err := worker.process(t.Context(), leases[0]); !errors.Is(err, unavailable) {
		t.Fatalf("scope read failure was hidden or acknowledged: %v", err)
	}
	if fixture.calls != 0 {
		t.Fatal("discovery proceeded without current scope")
	}
	if _, err := queue.Tasks.PromoteTasks(t.Context(), delayedRunEnumerationSet, runEnumerationStream, before.Add(time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	retried, err := queue.LeaseRuns(t.Context(), "scope-recovery", 1)
	if err != nil || len(retried) != 1 || retried[0].Task.Failures != 1 ||
		retried[0].Task.ScopeEpoch != task.ScopeEpoch || retried[0].Task.Enumeration.WorkflowID != 0 {
		t.Fatalf("scope read failure lost its recoverable task: %v %v", retried, err)
	}
}
