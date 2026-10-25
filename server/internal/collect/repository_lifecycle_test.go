package collect

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
)

func TestRepositoryReconciliationScenarios(t *testing.T) {
	for _, tc := range []struct {
		name, action, coordinate string
		initial                  []string
		current                  []githubapp.Repository
		want                     []string
		erased                   bool
	}{
		{"created", "created", "octo/api", nil,
			[]githubapp.Repository{{ID: 42, FullName: "octo/api"}}, []string{"octo/api"}, false},
		{"archived", "archived", "octo/api", []string{"octo/api"},
			[]githubapp.Repository{{ID: 42, FullName: "octo/api", Archived: true}}, nil, true},
		{"deleted", "deleted", "octo/api", []string{"octo/api"}, nil, nil, true},
		{"unarchived", "unarchived", "octo/api", nil,
			[]githubapp.Repository{{ID: 42, FullName: "octo/api"}}, []string{"octo/api"}, false},
		{"renamed", "renamed", "octo/new", []string{"octo/api"},
			[]githubapp.Repository{{ID: 42, FullName: "octo/new"}}, []string{"octo/new"}, true},
		{"transferred out", "transferred", "other/api", []string{"octo/api"}, nil, nil, true},
		{"transferred in", "transferred", "octo/api", nil,
			[]githubapp.Repository{{ID: 42, FullName: "octo/api"}}, []string{"octo/api"}, false},
		{"late create after delete", "created", "octo/api", []string{"octo/api"}, nil, nil, true},
		{"late archive after unarchive", "archived", "octo/api", []string{"octo/api"},
			[]githubapp.Repository{{ID: 42, FullName: "octo/api"}}, []string{"octo/api"}, false},
		{"late rename", "renamed", "octo/old", []string{"octo/api"},
			[]githubapp.Repository{{ID: 42, FullName: "octo/latest"}}, []string{"octo/latest"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := refreshWorker(t)
			ctx := t.Context()
			initial := append([]string{"octo/kept"}, tc.initial...)
			if err := w.Enrollment.AddRepositories(ctx, 7, initial); err != nil {
				t.Fatal(err)
			}
			retained := retainedRepository(t, w.Runner.Lake, "octo/api")
			kept := retainedRepository(t, w.Runner.Lake, "octo/kept")
			current := append([]githubapp.Repository{{ID: 43, FullName: "octo/kept"}}, tc.current...)
			client := &refreshEnumerator{pages: map[int][]githubapp.Repository{1: current}}
			w.ScopeRefresher = ScopeRefresher{Client: client, Quota: &fakeRunQuota{}, QuotaApp: "test"}
			admitter := Admitter{Enrollment: w.Enrollment, Queue: w.Queue}
			payload := []byte(fmt.Sprintf(`{"action":%q,"repository":{"id":42,"full_name":%q},"installation":{"id":7}}`, tc.action, tc.coordinate))
			result, err := admitter.AdmitDelivery(ctx, "repository", payload, "lifecycle", time.Hour)
			if err != nil || !result.Enqueued || result.Kind != IntentRepository {
				t.Fatalf("admission: %+v, %v", result, err)
			}
			duplicate, err := admitter.AdmitDelivery(ctx, "repository", payload, "lifecycle", time.Hour)
			if err != nil || !duplicate.Duplicate || client.calls != 0 {
				t.Fatalf("deduplication or inline GitHub call: %+v, %v, calls=%d", duplicate, err, client.calls)
			}
			w.process(ctx, refreshLease(t, w.Queue))
			want := append([]string{"octo/kept"}, tc.want...)
			repositories, err := w.Enrollment.RepositoriesForInstallation(ctx, 7)
			if err != nil || len(repositories) != len(want) {
				t.Fatalf("enrollment = %v, %v, want %v", repositories, err, want)
			}
			for _, name := range want {
				if enrolled, err := w.Enrollment.Enrolled(ctx, name); err != nil || !enrolled {
					t.Fatalf("missing current repository %s: %t, %v", name, enrolled, err)
				}
			}
			collections := map[string]bool{}
			for {
				leases, err := w.Queue.Lease(ctx, "follow-up", 10, 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(leases) == 0 {
					break
				}
				for _, lease := range leases {
					if lease.Task.Erase {
						if !w.process(ctx, lease) {
							t.Fatalf("erasure did not complete: %+v", lease)
						}
					} else {
						collections[lease.Task.Repository] = true
						if err := w.Queue.Complete(ctx, lease); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			for _, name := range tc.want {
				alreadyEnrolled := false
				for _, initial := range tc.initial {
					alreadyEnrolled = alreadyEnrolled || name == initial
				}
				if collections[name] == alreadyEnrolled {
					t.Fatalf("initial collection for %s = %t, already enrolled = %t", name, collections[name], alreadyEnrolled)
				}
			}
			if collections["octo/kept"] {
				t.Fatal("repository-list refresh recollected an unchanged repository")
			}
			_, err = os.Stat(retained)
			if os.IsNotExist(err) != tc.erased {
				t.Fatalf("old evidence erasure = %v, want %t", err, tc.erased)
			}
			if _, err := os.Stat(kept); err != nil {
				t.Fatalf("unrelated evidence removed: %v", err)
			}
			if pending, err := w.Projector.PendingProjection(ctx); err != nil || !pending {
				t.Fatalf("canonical refresh not persisted: %t, %v", pending, err)
			}
			if depth, err := w.Queue.Depth(ctx); err != nil || depth != 0 {
				t.Fatalf("work leaked after reconciliation: %d, %v", depth, err)
			}
		})
	}
}

func TestRepositoryReconciliationRejectsUnknownAndStaleInstallations(t *testing.T) {
	for _, installation := range []int64{0, 8} {
		w := refreshWorker(t)
		if err := w.Enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
			t.Fatal(err)
		}
		admitter := Admitter{Enrollment: w.Enrollment, Queue: w.Queue}
		for _, action := range []string{"created", "archived", "deleted", "unarchived", "renamed", "transferred"} {
			body := []byte(fmt.Sprintf(`{"action":%q,"repository":{"id":42,"full_name":"octo/api"},"installation":{"id":%d}}`, action, installation))
			result, err := admitter.AdmitDelivery(t.Context(), "repository", body, fmt.Sprintf("%d-%s", installation, action), time.Hour)
			if err != nil || result.Enqueued {
				t.Fatalf("unknown installation admitted: %+v, %v", result, err)
			}
		}
	}
	w := refreshWorker(t)
	if err := w.Enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: w.Enrollment, Queue: w.Queue}
	body := []byte(`{"action":"deleted","repository":{"id":42,"full_name":"octo/api"},"installation":{"id":7}}`)
	if _, err := admitter.AdmitDelivery(t.Context(), "repository", body, "stale", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Enrollment.RemoveInstallation(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	w.process(t.Context(), refreshLease(t, w.Queue))
	if depth, err := w.Queue.Depth(t.Context()); err != nil || depth != 0 {
		t.Fatalf("removed installation was rediscovered: %d, %v", depth, err)
	}
}

func TestReenrollmentCancelsPendingErasure(t *testing.T) {
	w := refreshWorker(t)
	ctx := t.Context()
	path := retainedRepository(t, w.Runner.Lake, "octo/api")
	if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Queue.Enqueue(ctx, Task{Repository: "octo/api", InstallationID: 7, Erase: true}); err != nil {
		t.Fatal(err)
	}
	w.process(ctx, refreshLease(t, w.Queue))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reenrolled repository evidence erased: %v", err)
	}
	if depth, err := w.Queue.Depth(ctx); err != nil || depth != 0 {
		t.Fatalf("obsolete erasure was deferred indefinitely: %d, %v", depth, err)
	}
}

func TestDiscoveryEvictionRequiresCompleteEnumerationAndErasureCapacity(t *testing.T) {
	for _, failure := range []string{"page", "limit", "queue"} {
		t.Run(failure, func(t *testing.T) {
			w := refreshWorker(t)
			ctx := t.Context()
			if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
				t.Fatal(err)
			}
			w.ScopeRefresher = ScopeRefresher{Client: &refreshEnumerator{
				pages: map[int][]githubapp.Repository{1: {{FullName: "octo/new"}}, 2: {}},
			}, Quota: &fakeRunQuota{}, QuotaApp: "test"}
			switch failure {
			case "page":
				w.ScopeRefresher.Client.(*refreshEnumerator).failPage = 2
			case "limit":
				w.ScopeRefresher.RepositoryLimit = 1
				w.ScopeRefresher.Client.(*refreshEnumerator).pages[2] = []githubapp.Repository{{FullName: "octo/another"}}
			case "queue":
				w.Queue.MaxLength = 1
				if _, err := w.Queue.Enqueue(ctx, Task{Repository: "octo/blocker"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.ScopeRefresher.discover(ctx, w.Enrollment, w.Queue, nil, 7); err == nil {
				t.Fatal("incomplete reconciliation reported success")
			}
			if enrolled, err := w.Enrollment.Enrolled(ctx, "octo/api"); err != nil || !enrolled {
				t.Fatalf("failed discovery evicted repository: %t, %v", enrolled, err)
			}
			if enrolled, err := w.Enrollment.Enrolled(ctx, "octo/new"); err != nil || enrolled {
				t.Fatalf("failed discovery admitted partial inventory: %t, %v", enrolled, err)
			}
		})
	}
}
