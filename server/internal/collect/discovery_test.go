package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
)

type discoveryInstallations struct {
	items []githubapp.Installation
	err   error
	calls int
}

func (e *discoveryInstallations) ListInstallations(context.Context) ([]githubapp.Installation, error) {
	e.calls++
	return e.items, e.err
}

func repositoryDiscovery(t *testing.T) (RepositoryDiscovery, Worker, *discoveryInstallations) {
	t.Helper()
	w := refreshWorker(t)
	client := &discoveryInstallations{items: []githubapp.Installation{{ID: 7}}}
	d := RepositoryDiscovery{Client: client, Enrollment: w.Enrollment, Queue: w.Queue,
		State: w.Projector.State, Leases: w.Enrollment.Leases}
	return d, w, client
}

func TestRepositoryListRefreshInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, 30 * time.Minute, 2 * time.Hour} {
		d, _, _ := repositoryDiscovery(t)
		d.Interval = interval
		want := interval
		if want == 0 {
			want = time.Hour
		}
		if got := d.interval(); got != want {
			t.Fatalf("configured interval %s resolved to %s, want %s", interval, got, want)
		}
	}
}

func TestRepositoryListBackfillRepairsMissingWebhooks(t *testing.T) {
	d, w, client := repositoryDiscovery(t)
	ctx := t.Context()
	if err := w.Enrollment.AddRepositories(ctx, 8, []string{"octo/removed"}); err != nil {
		t.Fatal(err)
	}
	result, err := d.Refresh(ctx)
	if err != nil || result.Installations != 1 || result.RefreshesQueued != 1 || result.WithdrawalsQueued != 1 {
		t.Fatalf("repository backfill = %+v, %v", result, err)
	}
	if enrolled, _ := w.Enrollment.Enrolled(ctx, "octo/removed"); !enrolled {
		t.Fatal("repository-list admission removed membership inline")
	}
	w.ScopeRefresher = ScopeRefresher{Client: &refreshEnumerator{
		pages: map[int][]githubapp.Repository{1: {{ID: 42, FullName: "octo/new"}}},
	}, Quota: &fakeRunQuota{}, QuotaApp: "test"}
	refresh := refreshLease(t, w.Queue)
	refresh.Task.NotBefore = time.Time{}
	w.process(ctx, refresh)
	withdrawal := refreshLease(t, w.Queue)
	w.process(ctx, withdrawal)
	for repo, want := range map[string]bool{"octo/new": true, "octo/removed": false} {
		if enrolled, err := w.Enrollment.Enrolled(ctx, repo); err != nil || enrolled != want {
			t.Fatalf("repaired membership %s = %t, %v, want %t", repo, enrolled, err, want)
		}
	}
	if client.calls != 1 {
		t.Fatalf("unexpected App enumeration count: %d", client.calls)
	}
}

func TestRepositoryListRefreshIsRestartAndReplicaCoordinated(t *testing.T) {
	d, _, client := repositoryDiscovery(t)
	ctx := t.Context()
	if _, err := d.refresh(ctx, false); err != nil {
		t.Fatal(err)
	}
	replica := d
	if _, err := replica.refresh(ctx, false); err != nil || client.calls != 1 {
		t.Fatalf("another worker repeated a fresh discovery: %d calls, %v", client.calls, err)
	}
	if err := d.State.SetOperationalState(ctx, discoveryLastKey, []byte(time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano))); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.refresh(ctx, false); err != nil || client.calls != 2 {
		t.Fatalf("stale inventory was not refreshed: %d calls, %v", client.calls, err)
	}
	if _, err := replica.Refresh(ctx); err != nil || client.calls != 3 {
		t.Fatalf("operator repair was suppressed by cadence: %d calls, %v", client.calls, err)
	}
}

func TestRepositoryListRefreshFailsClosedAndRetriesAdmission(t *testing.T) {
	for _, failure := range []string{"list", "invalid", "duplicate", "capacity", "timestamp"} {
		t.Run(failure, func(t *testing.T) {
			d, w, client := repositoryDiscovery(t)
			ctx := t.Context()
			if err := w.Enrollment.AddRepositories(ctx, 8, []string{"octo/kept"}); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "list":
				client.err = errors.New("GitHub unavailable")
			case "invalid":
				client.items = []githubapp.Installation{{ID: 0}}
			case "duplicate":
				client.items = []githubapp.Installation{{ID: 7}, {ID: 7}}
			case "capacity":
				d.Queue.MaxLength = 1
				if _, err := d.Queue.Enqueue(ctx, Task{Repository: "octo/blocker"}); err != nil {
					t.Fatal(err)
				}
			case "timestamp":
				if err := d.State.SetOperationalState(ctx, discoveryLastKey, []byte("invalid")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.refresh(ctx, false); err == nil {
				t.Fatal("failed inventory refresh reported success")
			}
			if enrolled, err := w.Enrollment.Enrolled(ctx, "octo/kept"); err != nil || !enrolled {
				t.Fatalf("failed refresh evicted existing scope: %t, %v", enrolled, err)
			}
			if failure == "capacity" {
				if last, err := d.State.OperationalState(ctx, discoveryLastKey); err != nil || len(last) != 0 {
					t.Fatalf("failed admission advanced discovery checkpoint: %q, %v", last, err)
				}
				if err := d.Queue.Complete(ctx, refreshLease(t, d.Queue)); err != nil {
					t.Fatal(err)
				}
				d.Queue.MaxLength = 10
				if result, err := d.Refresh(ctx); err != nil || result.RefreshesQueued != 1 || result.WithdrawalsQueued != 1 {
					t.Fatalf("repair could not retry: %+v, %v", result, err)
				}
			}
		})
	}
}

func TestRepositoryListRefreshHonorsLeaseAndCancellation(t *testing.T) {
	d, _, client := repositoryDiscovery(t)
	ctx := t.Context()
	if acquired, err := d.Leases.TryLock(ctx, discoveryLease, "other-worker", time.Minute); err != nil || !acquired {
		t.Fatal("could not establish discovery contention")
	}

	if _, err := d.refresh(ctx, false); err != nil || client.calls != 0 {
		t.Fatalf("replica ignored the discovery lease: %v", err)
	}
	if _, err := d.Refresh(ctx); err == nil {
		t.Fatal("operator repair reported success while another pass held its lease")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(cancelled)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("repository discovery did not stop with its worker")
	}
}

func TestRepositoryListWithdrawalRechecksReinstalledApp(t *testing.T) {
	d, w, client := repositoryDiscovery(t)
	ctx := t.Context()
	if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	client.items = nil
	if result, err := d.Refresh(ctx); err != nil || result.WithdrawalsQueued != 1 {
		t.Fatalf("missed installation deletion was not queued: %+v, %v", result, err)
	}
	w.ScopeRefresher.Client = &refreshEnumerator{installations: []githubapp.Installation{{ID: 7}}}
	w.process(ctx, refreshLease(t, w.Queue))
	if enrolled, err := w.Enrollment.Enrolled(ctx, "octo/api"); err != nil || !enrolled {
		t.Fatalf("delayed withdrawal removed a current installation: %t, %v", enrolled, err)
	}
	if depth, err := w.Queue.Depth(ctx); err != nil || depth != 0 {
		t.Fatalf("stale withdrawal was not acknowledged: %d, %v", depth, err)
	}
}

func TestRepositoryListRefreshPersistsAcrossRestart(t *testing.T) {
	for _, backend := range []string{"redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			services, reopen := durableErasureServices(t, backend)
			d := RepositoryDiscovery{Client: &discoveryInstallations{items: []githubapp.Installation{{ID: 7}}},
				Enrollment: Enrollment{Metadata: services.Collection, Leases: services.Leases},
				Queue:      erasureQueue(services), State: services.State, Leases: services.Leases}
			if result, err := d.refresh(t.Context(), false); err != nil || result.RefreshesQueued != 1 {
				t.Fatalf("initial repository refresh: %+v, %v", result, err)
			}
			restarted := reopen()
			client := &discoveryInstallations{items: []githubapp.Installation{{ID: 7}}}
			d.Client, d.State, d.Leases = client, restarted.State, restarted.Leases
			d.Queue = erasureQueue(restarted)
			d.Enrollment = Enrollment{Metadata: restarted.Collection, Leases: restarted.Leases}
			if _, err := d.refresh(t.Context(), false); err != nil || client.calls != 0 {
				t.Fatalf("restart ignored persisted discovery cadence: calls=%d, %v", client.calls, err)
			}
		})
	}
}
