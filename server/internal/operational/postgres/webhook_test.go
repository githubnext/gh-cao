package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

func webhookOptions() wg.QueueOptions {
	rate := wg.RateBudget{Capacity: 100, Period: time.Hour}
	return wg.QueueOptions{
		MaxRows: 128, MaxBytes: 2 << 20, MaxTenantRows: 128, MaxTenantBytes: 2 << 20,
		MaxSourceRows: 128, MaxSourceBytes: 2 << 20, MaxDestinationRows: 128, MaxDestinationBytes: 2 << 20,
		MaxPayloadBytes: 65536, MaxBatch: 16, MaxConcurrency: 8,
		MaxAge: time.Hour, DedupWindow: time.Hour, TerminalRetention: 2 * time.Hour, LeaseDuration: time.Minute,
		Rates: wg.RateBudgets{Global: rate, App: rate, Tenant: rate, Destination: rate, Workflow: rate},
	}
}

func webhookPolicy() wg.PolicyRevision {
	return wg.PolicyRevision{Revision: 1, Digest: strings.Repeat("a", 64), Enabled: true}
}

func webhookJob(subscription, tenant string) wg.Job {
	now := time.Now().UTC().Add(-time.Second)
	return wg.Job{
		ID: "input-" + subscription, DeliveryID: "placeholder", State: wg.Ready, Version: 1, QueueRevision: webhookPolicy(),
		Subscription: wg.Subscription{
			SchemaVersion: "gh-aw.webhook-subscription/v1", Provider: "github", ID: subscription, TenantID: tenant, AppID: 1,
			Source:      wg.Source{ID: "source", Event: "issues", InstallationID: 10, RepositoryID: 20},
			Destination: wg.Destination{Kind: "workflow_dispatch", InstallationID: 30, RepositoryID: 40, Workflow: "build.yml", Ref: "main", GatewayInput: true},
			Delivery:    wg.DeliveryPolicy{TTLSeconds: 3600, LeaseSeconds: 60, MaxAttempts: 3, RetryDelaySeconds: 1},
		},
		SubscriptionDigest: strings.Repeat("b", 64), Payload: []byte(`{"ref":"main","inputs":{}}`),
		Event:     wg.EventIdentity{SourceID: "source", DeliveryID: "placeholder", Name: "issues", InstallationID: 10, RepositoryID: 20, OrganizationID: 50},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), NotBefore: now,
	}
}

func webhookAdmission(delivery string, jobs ...wg.Job) wg.Admission {
	raw := []byte(`{"installation":{"id":10},"repository":{"id":20},"organization":{"id":50}}`)
	for i := range jobs {
		jobs[i].DeliveryID = delivery
		jobs[i].Event.DeliveryID = delivery
		event := wg.Event{
			SourceID: jobs[i].Event.SourceID, DeliveryID: delivery, Name: jobs[i].Event.Name,
			InstallationID: jobs[i].Event.InstallationID, RepositoryID: jobs[i].Event.RepositoryID,
			OrganizationID: jobs[i].Event.OrganizationID, Payload: raw,
		}
		payload, err := wg.BuildDispatchPayload(jobs[i].Subscription, event, wg.DefaultLimits())
		if err != nil {
			panic(err)
		}
		jobs[i].Payload = payload
	}
	return wg.Admission{Revision: webhookPolicy(), DeliveryID: delivery, DeliveryDigest: wg.MetadataDigest(raw), Jobs: jobs}
}

func openWebhook(t *testing.T, s *Store, options wg.QueueOptions) *webhookQueue {
	t.Helper()
	queue, err := s.OpenWebhookQueue(t.Context(), "webhooks", options)
	if err != nil {
		t.Fatal(err)
	}
	q := queue.(*webhookQueue)
	if err := q.ApplyRevision(t.Context(), webhookPolicy()); err != nil {
		t.Fatal(err)
	}
	return q
}

func admitWebhook(t *testing.T, q wg.Queue, delivery string, jobs ...wg.Job) []wg.Job {
	t.Helper()
	result, err := q.AdmitBatch(t.Context(), webhookAdmission(delivery, jobs...))
	if err != nil {
		t.Fatal(err)
	}
	return result.Jobs
}

func claimWebhook(t *testing.T, q wg.Queue) wg.Job {
	t.Helper()
	job, err := q.Claim(t.Context(), wg.ClaimRequest{Owner: "worker", LeaseDuration: time.Minute})
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	return *job
}

func webhookJobFence(job wg.Job) wg.Fence {
	return wg.Fence{JobID: job.ID, Owner: job.ClaimID, Generation: job.Fence, Version: job.Version}
}

func webhookAuthorization(job wg.Job) wg.Authorization {
	now := time.Now().UTC()
	s := job.Subscription
	return wg.Authorization{Revision: job.QueueRevision, ApprovedSubscriptionDigest: job.SubscriptionDigest, Snapshot: wg.AuthorizationSnapshot{
		CheckedAt: now.Add(-time.Second), ExpiresAt: now.Add(29 * time.Second), Complete: true,
		JobID: job.ID, Fence: job.Fence, Attempt: job.Attempt + 1,
		SourceInstallationID: s.Source.InstallationID, SourceRepositoryID: s.Source.RepositoryID,
		SourceOrganizationID:      s.Source.OrganizationID,
		ReceivingInstallationID:   s.Source.InstallationID,
		DestinationInstallationID: s.Destination.InstallationID, DestinationRepositoryID: s.Destination.RepositoryID,
		SourceAccessible: true, DestinationAccessible: true, SourcePermissionsOK: true, DispatchPermissionsOK: true,
		SourceVisibility: "private", DestinationVisibility: "private",
		CurrentRevision: wg.Revision{RepositoryID: s.Destination.RepositoryID, CommitSHA: strings.Repeat("d", 40), MetadataPath: ".github/webhooks.json", Digest: job.SubscriptionDigest, CompilerID: "gh-aw"},
		Grant: wg.AdminGrant{
			SubscriptionID: s.ID, Digest: job.SubscriptionDigest,
			SourceInstallationID: s.Source.InstallationID, SourceRepositoryID: s.Source.RepositoryID,
			SourceOrganizationID:      s.Source.OrganizationID,
			ReceivingInstallationID:   s.Source.InstallationID,
			DestinationInstallationID: s.Destination.InstallationID, DestinationRepositoryID: s.Destination.RepositoryID,
			DispatchKind: s.Destination.Kind, ExpiresAt: now.Add(time.Hour),
		},
	}}
}

func dispatchWebhook(t *testing.T, q wg.Queue, job wg.Job) wg.Job {
	t.Helper()
	result, err := q.BeginDispatch(t.Context(), webhookJobFence(job), webhookAuthorization(job))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func expireWebhookLease(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(), `UPDATE cao_operational_webhook_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE namespace=$1 AND id=$2`, s.namespace, id); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookAtomicAdmissionDedupAndNamespace(t *testing.T) {
	s, dsn := testStore(t, Config{})
	q := openWebhook(t, s, webhookOptions())
	ctx := t.Context()
	admission := webhookAdmission("delivery", webhookJob("one", "tenant"), webhookJob("two", "tenant"))
	first, err := q.AdmitBatch(ctx, admission)
	if err != nil || len(first.Jobs) != 2 || first.Duplicate {
		t.Fatalf("admission: %+v %v", first, err)
	}
	peer, err := Open(ctx, dsn, s.namespace, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	other := openWebhook(t, peer, webhookOptions())
	var duplicate atomic.Int64
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			result, err := other.AdmitBatch(ctx, admission)
			if err != nil {
				t.Error(err)
			} else if result.Duplicate && len(result.Jobs) == 2 {
				duplicate.Add(1)
			}
		})
	}
	workers.Wait()
	if duplicate.Load() != 12 {
		t.Fatalf("duplicates: %d", duplicate.Load())
	}
	admission.DeliveryDigest = strings.Repeat("e", 64)
	if _, err := q.AdmitBatch(ctx, admission); !errors.Is(err, wg.ErrConflict) {
		t.Fatalf("delivery content changed: %v", err)
	}
	isolated, _ := testStore(t, Config{})
	if jobs := admitWebhook(t, openWebhook(t, isolated, webhookOptions()), "delivery", webhookJob("one", "tenant")); len(jobs) != 1 {
		t.Fatalf("namespace isolation: %d", len(jobs))
	}
}

func TestWebhookBatchCapacityRollbackAndProtectedAccounting(t *testing.T) {
	s, _ := testStore(t, Config{})
	options := webhookOptions()
	options.MaxRows, options.MaxTenantRows, options.MaxSourceRows, options.MaxDestinationRows = 2, 2, 2, 2
	q := openWebhook(t, s, options)
	var beforeBytes, beforeRows int64
	if err := s.db.QueryRowContext(t.Context(), `SELECT protected_bytes,protected_entries FROM cao_operational_namespaces WHERE namespace=$1`, s.namespace).Scan(&beforeBytes, &beforeRows); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AdmitBatch(t.Context(), webhookAdmission("full", webhookJob("one", "tenant"), webhookJob("two", "tenant"))); !errors.Is(err, wg.ErrCapacity) {
		t.Fatalf("batch capacity: %v", err)
	}
	var afterBytes, afterRows int64
	if err := s.db.QueryRowContext(t.Context(), `SELECT protected_bytes,protected_entries FROM cao_operational_namespaces WHERE namespace=$1`, s.namespace).Scan(&afterBytes, &afterRows); err != nil {
		t.Fatal(err)
	}
	if beforeBytes != afterBytes || beforeRows != afterRows {
		t.Fatalf("partial accounting: %d/%d -> %d/%d", beforeBytes, beforeRows, afterBytes, afterRows)
	}
	result, err := q.AdmitBatch(t.Context(), webhookAdmission("full", webhookJob("one", "tenant")))
	if err != nil || result.Duplicate || len(result.Jobs) != 1 {
		t.Fatalf("rollback consumed dedup: %+v %v", result, err)
	}
	if _, err := q.AdmitBatch(t.Context(), webhookAdmission("overflow", webhookJob("two", "tenant"))); !errors.Is(err, wg.ErrCapacity) {
		t.Fatalf("full queue: %v", err)
	}
	claimed := claimWebhook(t, q)
	if _, err := q.Finish(t.Context(), webhookJobFence(claimed), wg.Outcome{State: wg.Cancelled}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Maintain(t.Context(), 512); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AdmitBatch(t.Context(), webhookAdmission("overflow", webhookJob("two", "tenant"))); !errors.Is(err, wg.ErrCapacity) {
		t.Fatalf("dedup retention evicted under pressure: %v", err)
	}
}

func TestWebhookFencingRecoveryAndAmbiguousTerminal(t *testing.T) {
	s, _ := testStore(t, Config{})
	q := openWebhook(t, s, webhookOptions())
	admitWebhook(t, q, "delivery", webhookJob("one", "tenant"))
	first := claimWebhook(t, q)
	expireWebhookLease(t, s, first.ID)
	maintenance, err := q.Maintain(t.Context(), 512)
	if err != nil || maintenance.Recovered != 1 {
		t.Fatalf("recover: %+v %v", maintenance, err)
	}
	second := claimWebhook(t, q)
	if second.Fence <= first.Fence {
		t.Fatalf("fence did not advance: %d -> %d", first.Fence, second.Fence)
	}
	if _, err := q.Renew(t.Context(), webhookJobFence(first), time.Minute); !errors.Is(err, wg.ErrConflict) {
		t.Fatalf("stale renewal: %v", err)
	}
	dispatched := dispatchWebhook(t, q, second)
	if _, err := q.Finish(t.Context(), webhookJobFence(dispatched), wg.Outcome{State: wg.Ready}); !errors.Is(err, wg.ErrConflict) {
		t.Fatalf("unsafe retry: %v", err)
	}
	expireWebhookLease(t, s, dispatched.ID)
	maintenance, err = q.Maintain(t.Context(), 512)
	if err != nil || maintenance.Ambiguous != 1 {
		t.Fatalf("ambiguous: %+v %v", maintenance, err)
	}
	if _, err := q.Finish(t.Context(), webhookJobFence(dispatched), wg.Outcome{State: wg.Succeeded, AcceptedStatus: 204}); !errors.Is(err, wg.ErrConflict) {
		t.Fatalf("late completion: %v", err)
	}
	if job, err := q.Claim(t.Context(), wg.ClaimRequest{Owner: "replacement", LeaseDuration: time.Minute}); err != nil || job != nil {
		t.Fatalf("ambiguous operationalpatched: %v %v", job, err)
	}
}

func TestWebhookMonotonicPolicyAndStableDedup(t *testing.T) {
	s, _ := testStore(t, Config{})
	q := openWebhook(t, s, webhookOptions())
	admitWebhook(t, q, "delivery", webhookJob("one", "tenant"))
	claimed := claimWebhook(t, q)
	next := webhookPolicy()
	next.Revision++
	next.Digest = strings.Repeat("f", 64)
	if err := q.ApplyRevision(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyRevision(t.Context(), webhookPolicy()); !errors.Is(err, wg.ErrConflict) {
		t.Fatalf("policy rollback: %v", err)
	}
	changed := next
	changed.Enabled = false
	if err := q.ApplyRevision(t.Context(), changed); !errors.Is(err, wg.ErrConflict) {
		t.Fatalf("same revision changed: %v", err)
	}
	if _, err := q.BeginDispatch(t.Context(), webhookJobFence(claimed), webhookAuthorization(claimed)); !errors.Is(err, wg.ErrConflict) {
		t.Fatalf("cancelled claim dispatched: %v", err)
	}
	admission := webhookAdmission("delivery", webhookJob("one", "tenant"))
	admission.Revision = next
	admission.Jobs[0].QueueRevision = next
	admission.Jobs[0].SubscriptionDigest = strings.Repeat("e", 64)
	result, err := q.AdmitBatch(t.Context(), admission)
	if err != nil || !result.Duplicate || len(result.Jobs) != 1 || result.Jobs[0].State != wg.Cancelled {
		t.Fatalf("metadata change escaped stable dedup: %+v %v", result, err)
	}
}

func TestWebhookFairnessConcurrencyAndRestart(t *testing.T) {
	s, dsn := testStore(t, Config{})
	options := webhookOptions()
	options.MaxConcurrency = 2
	q := openWebhook(t, s, options)
	admitWebhook(t, q, "a1", webhookJob("a1", "a"))
	admitWebhook(t, q, "a2", webhookJob("a2", "a"))
	admitWebhook(t, q, "b1", webhookJob("b1", "b"))
	first := claimWebhook(t, q)
	second := claimWebhook(t, q)
	if first.Subscription.TenantID != "a" || second.Subscription.TenantID != "b" {
		t.Fatalf("unfair tenants: %s %s", first.Subscription.TenantID, second.Subscription.TenantID)
	}
	peer, err := Open(t.Context(), dsn, s.namespace, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	reopened := openWebhook(t, peer, options)
	if job, err := reopened.Claim(t.Context(), wg.ClaimRequest{Owner: "peer", LeaseDuration: time.Minute}); err != nil || job != nil {
		t.Fatalf("distributed concurrency: %v %v", job, err)
	}
	expireWebhookLease(t, s, first.ID)
	if _, err := reopened.Maintain(t.Context(), 512); err != nil {
		t.Fatal(err)
	}
	if job, err := reopened.Claim(t.Context(), wg.ClaimRequest{Owner: "peer", LeaseDuration: time.Minute}); err != nil || job == nil {
		t.Fatalf("restart recovery: %v %v", job, err)
	}
}

func TestWebhookNoTransactionRetainedByConsumer(t *testing.T) {
	s, _ := testStore(t, Config{})
	q := openWebhook(t, s, webhookOptions())
	admitWebhook(t, q, "delivery", webhookJob("one", "tenant"))
	job := dispatchWebhook(t, q, claimWebhook(t, q))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := s.WriteAttribute(ctx, "independent", "test", "available"); err != nil {
		t.Fatalf("dispatch retained namespace transaction: %v", err)
	}
	if _, err := q.Finish(t.Context(), webhookJobFence(job), wg.Outcome{State: wg.Succeeded, AcceptedStatus: 204}); err != nil {
		t.Fatal(err)
	}
}
