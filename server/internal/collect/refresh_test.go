package collect

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestPushWorkflowPathsAndDefaultBranch(t *testing.T) {
	for _, tc := range []struct {
		name, ref, branch, changes string
		deleted                    bool
		want                       IntentKind
	}{
		{"added", "refs/heads/trunk", "trunk", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentWorkflowRefresh},
		{"modified", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.lock.yml"]}]`, false, IntentWorkflowRefresh},
		{"removed", "refs/heads/trunk", "trunk", `"commits":[{"removed":[".github/workflows/build.lock.yml"]}]`, false, IntentWorkflowRefresh},
		{"other branch", "refs/heads/main", "trunk", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentIgnore},
		{"tag", "refs/tags/trunk", "trunk", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentIgnore},
		{"no default", "refs/heads/main", "", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentIgnore},
		{"deleted branch", "refs/heads/trunk", "trunk", `"commits":[{"removed":[".github/workflows/build.lock.yml"]}]`, true, IntentIgnore},
		{"other files", "refs/heads/trunk", "trunk", `"commits":[{"modified":["README.md",".github/workflows-other/build.md"]}]`, false, IntentIgnore},
		{"markdown source", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.md"]}]`, false, IntentIgnore},
		{"yaml extension", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.yaml"]}]`, false, IntentIgnore},
		{"ordinary workflow", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.yml"]}]`, false, IntentIgnore},
		{"nested workflow", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/shared/build.lock.yml"]}]`, false, IntentIgnore},
		{"truncated", "refs/heads/trunk", "trunk", `"size":3,"commits":[{"modified":["README.md"]}]`, false, IntentWorkflowRefresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"ref":%q,"deleted":%t,"installation":{"id":7},"repository":{"full_name":"Octo/API","default_branch":%q},%s}`, tc.ref, tc.deleted, tc.branch, tc.changes)
			intent, err := ParseEvent("push", []byte(payload))
			if err != nil || intent.Kind != tc.want {
				t.Fatalf("classification = %+v, %v; want %s", intent, err, tc.want)
			}
			if intent.Kind == IntentWorkflowRefresh && intent.Repository != "octo/api" {
				t.Fatalf("repository was not normalized: %+v", intent)
			}
		})
	}
}

func refreshWorker(t *testing.T) Worker {
	t.Helper()
	b := volatileBackfill(t)
	if err := b.Queue.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	return Worker{Queue: b.Queue, Enrollment: b.Enrollment, Runner: Runner{Lake: b.Lake},
		Projector: Projector{State: b.StateStore, Leases: b.Enrollment.Leases, Lake: b.Lake}}
}

func refreshLease(t *testing.T, queue Queue) Lease {
	t.Helper()
	leases, err := queue.Lease(t.Context(), "refresh-test", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, %v", leases, err)
	}
	return leases[0]
}

func TestPushRefreshIsIndependentOfRunCollectionAndRechecksScope(t *testing.T) {
	w := refreshWorker(t)
	ctx := t.Context()
	if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Queue: w.Queue, Enrollment: w.Enrollment}
	payload := []byte(`{"ref":"refs/heads/trunk","installation":{"id":7},"repository":{"full_name":"octo/api","default_branch":"trunk"},"commits":[{"modified":[".github/workflows/build.lock.yml"]}]}`)
	for index := range 3 {
		result, err := admitter.AdmitDelivery(ctx, "push", payload, fmt.Sprintf("push-%d", index), time.Hour)
		if err != nil || result.Enqueued != (index == 0) || result.Duplicate {
			t.Fatalf("push %d: %+v, %v", index, result, err)
		}
	}
	duplicate, err := admitter.AdmitDelivery(ctx, "push", payload, "push-0", time.Hour)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate delivery: %+v, %v", duplicate, err)
	}
	if queued, err := w.Queue.Enqueue(ctx, Task{Repository: "octo/api", InstallationID: 7}); err != nil || !queued {
		t.Fatalf("workflow refresh hid a run collection: %t, %v", queued, err)
	}
	lease := refreshLease(t, w.Queue)
	if !lease.Task.InventoryRefresh {
		t.Fatalf("not a workflow refresh: %+v", lease)
	}
	if dirty, err := w.Projector.PendingProjection(ctx); err != nil || dirty {
		t.Fatalf("webhook admission projected inline: %t, %v", dirty, err)
	}
	w.process(ctx, lease)
	if dirty, err := w.Projector.PendingProjection(ctx); err != nil || !dirty {
		t.Fatalf("worker failed to request projection: %t, %v", dirty, err)
	}
	// No Runner configuration or credentials exist: inventory-only work must
	// not try to download run evidence.
	if pending, err := w.Queue.Pending(ctx); err != nil || pending != 0 {
		t.Fatalf("refresh was not completed: %d, %v", pending, err)
	}
	_ = refreshLease(t, w.Queue) // Leave the unrelated run task leased.
	if _, err := w.Enrollment.RemoveRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Queue.Enqueue(ctx, lease.Task); err != nil {
		t.Fatal(err)
	}
	lease = refreshLease(t, w.Queue)
	if err := w.Projector.State.SetOperationalState(ctx, projectionDirtyKey, nil); err != nil {
		t.Fatal(err)
	}
	w.process(ctx, lease)
	if dirty, err := w.Projector.PendingProjection(ctx); err != nil || dirty {
		t.Fatalf("stale push refreshed withdrawn repository: %t, %v", dirty, err)
	}
}

type refreshEnumerator struct {
	pages         map[int][]githubapp.Repository
	installations []githubapp.Installation
	failPage      int
	calls         int
}

func (e *refreshEnumerator) ListInstallations(context.Context) ([]githubapp.Installation, error) {
	return e.installations, nil
}

func (e *refreshEnumerator) QuotaRateLimit(context.Context, int64) (githubquota.ResponseQuota, error) {
	return githubquota.ResponseQuota{HasResponse: true, HasObservation: true, Resource: githubquota.ResourceCore,
		Observation: githubquota.Observation{Remaining: 5000, Limit: 5000, ResetAt: time.Now().Add(time.Hour)}}, nil
}

func (e *refreshEnumerator) ListRepositoriesPage(ctx context.Context, installationID int64, page int) ([]githubapp.Repository, int, githubquota.ResponseQuota, error) {
	e.calls++
	response, _ := e.QuotaRateLimit(ctx, installationID)
	if page == e.failPage {
		return nil, 0, response, errors.New("enumeration unavailable")
	}
	next := 0
	if _, ok := e.pages[page+1]; ok {
		next = page + 1
	}
	return e.pages[page], next, response, nil
}

func TestScopeDiscoveryIsQueuedCoalescedPaginatedAndVerified(t *testing.T) {
	w := refreshWorker(t)
	ctx := t.Context()
	if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/existing"}); err != nil {
		t.Fatal(err)
	}
	client := &refreshEnumerator{pages: map[int][]githubapp.Repository{
		1: {{FullName: "octo/existing"}, {FullName: "octo/first"}},
		2: {{FullName: "octo/second"}, {FullName: "octo/archived", Archived: true}},
	}}
	quota := &fakeRunQuota{}
	w.ScopeRefresher = ScopeRefresher{Client: client, Quota: quota, QuotaApp: "test-app"}
	admitter := Admitter{Queue: w.Queue, Enrollment: w.Enrollment}
	for i, repo := range []string{"octo/first", "octo/second"} {
		payload := []byte(fmt.Sprintf(`{"action":"added","installation":{"id":7},"repositories_added":[{"full_name":%q}]}`, repo))
		result, err := admitter.AdmitDelivery(ctx, "installation_repositories", payload, fmt.Sprintf("create-%d", i), time.Hour)
		if err != nil || result.Enqueued != (i == 0) {
			t.Fatalf("repository discovery admission: %+v, %v", result, err)
		}
	}
	if client.calls != 0 {
		t.Fatal("admission called GitHub")
	}
	if enrolled, _ := w.Enrollment.Enrolled(ctx, "octo/first"); enrolled {
		t.Fatal("payload inferred enrollment")
	}
	lease := refreshLease(t, w.Queue)
	lease.Task.NotBefore = time.Time{}
	w.process(ctx, lease)
	if client.calls != 2 || len(quota.reservations) != 2 || len(quota.commits) != 2 {
		t.Fatalf("pagination was not governed per request: calls=%d, quota=%+v", client.calls, quota)
	}
	for _, repo := range []string{"octo/first", "octo/second"} {
		if enrolled, err := w.Enrollment.Enrolled(ctx, repo); err != nil || !enrolled {
			t.Fatalf("verified repository not discovered: %s, %t, %v", repo, enrolled, err)
		}
	}
	if enrolled, _ := w.Enrollment.Enrolled(ctx, "octo/archived"); enrolled {
		t.Fatal("archived repository was discovered")
	}
	if pending, err := w.Projector.PendingProjection(ctx); err != nil || !pending {
		t.Fatalf("new repository inventory was not scheduled: %t, %v", pending, err)
	}
}

func TestScopeDiscoveryDoesNotPublishPartialEnumeration(t *testing.T) {
	for _, limit := range []int{1, 100} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			w := refreshWorker(t)
			client := &refreshEnumerator{pages: map[int][]githubapp.Repository{
				1: {{FullName: "octo/first"}, {FullName: "octo/second"}}, 2: {},
			}, failPage: 2}
			r := ScopeRefresher{Client: client, Quota: &fakeRunQuota{known: true}, QuotaApp: "test", RepositoryLimit: limit}
			if err := r.discover(t.Context(), w.Enrollment, w.Queue, nil, 7); err == nil {
				t.Fatal("failed or oversized discovery reported success")
			}
			if enrolled, _ := w.Enrollment.Enrolled(t.Context(), "octo/first"); enrolled {
				t.Fatal("partial discovery modified enrollment")
			}
		})
	}
}

func TestScopeWithdrawalQueuesBeforeChangingEnrollment(t *testing.T) {
	w := refreshWorker(t)
	ctx := t.Context()
	if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Queue: w.Queue, Enrollment: w.Enrollment}
	payload := []byte(`{"action":"removed","installation":{"id":7},"repositories_removed":[{"full_name":"octo/api"}]}`)
	result, err := admitter.AdmitDelivery(ctx, "installation_repositories", payload, "remove-1", time.Hour)
	if err != nil || !result.Enqueued {
		t.Fatalf("withdrawal was not queued: %+v, %v", result, err)
	}
	if enrolled, _ := w.Enrollment.Enrolled(ctx, "octo/api"); !enrolled {
		t.Fatal("admission changed enrollment inline")
	}
	lease := refreshLease(t, w.Queue)
	w.process(ctx, lease)
	if enrolled, _ := w.Enrollment.Enrolled(ctx, "octo/api"); enrolled {
		t.Fatal("background withdrawal did not remove enrollment")
	}
	erasure := refreshLease(t, w.Queue)
	if !erasure.Task.Erase || erasure.Task.Repository != "octo/api" {
		t.Fatalf("erasure not persisted before scope ACK: %+v", erasure)
	}
}

func TestRefreshBackpressureDoesNotConsumeDelivery(t *testing.T) {
	w := refreshWorker(t)
	w.Queue.MaxLength = 1
	if err := w.Enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Queue.Enqueue(t.Context(), Task{Repository: "octo/other"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Queue: w.Queue, Enrollment: w.Enrollment}
	payload := []byte(`{"action":"deleted","installation":{"id":7}}`)
	if _, err := admitter.AdmitDelivery(t.Context(), "installation", payload, "backpressure", time.Hour); err == nil {
		t.Fatal("full queue accepted a delivery")
	}
	if err := w.Queue.Complete(t.Context(), refreshLease(t, w.Queue)); err != nil {
		t.Fatal(err)
	}
	result, err := admitter.AdmitDelivery(t.Context(), "installation", payload, "backpressure", time.Hour)
	if err != nil || !result.Enqueued || result.Duplicate {
		t.Fatalf("failed admission consumed delivery identity: %+v, %v", result, err)
	}
}

func TestDiscoveryQuotaExhaustionDefersWithoutSpendingAttempts(t *testing.T) {
	w := refreshWorker(t)
	ctx := t.Context()
	quota, err := githubquota.New(w.Queue.Metadata.(operational.GitHubQuotaStore), githubquota.Options{SafetyReserve: 1000})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().UTC().Add(time.Hour)
	bucket := githubquota.BucketID{App: "test-app", Installation: 7, Resource: githubquota.ResourceCore}
	if err := quota.Observe(ctx, bucket, githubquota.Observation{Limit: 5000, Remaining: 1000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	client := &refreshEnumerator{}
	w.ScopeRefresher = ScopeRefresher{Client: client, Quota: quota, QuotaApp: "test-app"}
	task := Task{Repository: "installation/7", InstallationID: 7, Scope: &ScopeChange{Kind: IntentEnroll}, Attempt: 2}
	if _, err := w.Queue.Enqueue(ctx, task); err != nil {
		t.Fatal(err)
	}
	lease := refreshLease(t, w.Queue)
	w.process(ctx, lease)
	if client.calls != 0 {
		t.Fatal("GitHub was called without quota headroom")
	}
	if pending, err := w.Queue.Pending(ctx); err != nil || pending != 0 {
		t.Fatalf("quota-denied task was not durably deferred: %d, %v", pending, err)
	}
	if _, err := w.Queue.promoteDue(ctx, reset.Add(time.Minute), 1); err != nil {
		t.Fatal(err)
	}
	deferred := refreshLease(t, w.Queue)
	if deferred.Task.Attempt != task.Attempt || deferred.Task.NotBefore.Before(reset) {
		t.Fatalf("quota deferral consumed attempts or ignored reset: %+v", deferred.Task)
	}
}

func TestWithdrawalCannotRemoveEnrollmentBeforeErasureAdmission(t *testing.T) {
	w := refreshWorker(t)
	ctx := t.Context()
	w.Queue.MaxLength = 1
	if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Queue: w.Queue, Enrollment: w.Enrollment}
	payload := []byte(`{"action":"removed","installation":{"id":7},"repositories_removed":[{"full_name":"octo/api"}]}`)
	if _, err := admitter.AdmitDelivery(ctx, "installation_repositories", payload, "withdrawal-capacity", time.Hour); err != nil {
		t.Fatal(err)
	}
	lease := refreshLease(t, w.Queue)
	if err := w.processRefresh(ctx, lease); err == nil {
		t.Fatal("scope change succeeded without capacity for durable erasure")
	}
	if enrolled, err := w.Enrollment.Enrolled(ctx, "octo/api"); err != nil || !enrolled {
		t.Fatalf("enrollment removed before erasure admission: %t, %v", enrolled, err)
	}
	if pending, err := w.Queue.Pending(ctx); err != nil || pending != 1 {
		t.Fatalf("failed scope change lost the original task: %d, %v", pending, err)
	}
}
