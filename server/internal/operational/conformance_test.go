package operational_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestAdapters(t *testing.T) {
	factories := map[string]func(*testing.T) operational.Store{
		"memory": func(t *testing.T) operational.Store {
			t.Helper()
			store, err := memory.New(memory.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		"postgres": func(t *testing.T) operational.Store {
			t.Helper()
			url := os.Getenv("POSTGRES_URL")
			if url == "" {
				t.Skip("POSTGRES_URL is required for real PostgreSQL conformance")
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				t.Fatal(err)
			}
			store, err := postgres.New(t.Context(), url, "operational-conformance-"+hex.EncodeToString(nonce[:]), postgres.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := store.DeleteNamespace(ctx); err != nil {
					t.Error(err)
				}
				_ = store.Close()
			})
			return store
		},
		"redis": func(t *testing.T) operational.Store {
			t.Helper()
			url := os.Getenv("REDIS_URL")
			if url == "" {
				t.Skip("REDIS_URL is required for real Redis conformance")
			}
			client, err := redisx.New(url)
			if err != nil {
				t.Fatal(err)
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				t.Fatal(err)
			}
			store := redisx.NewStore(client, "operational-conformance-"+hex.EncodeToString(nonce[:]))
			if err := store.InitializeDisposableCaches(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) { conformance(t, factory(t)) })
	}
}

func conformance(t *testing.T, store operational.Store) {
	t.Helper()
	ctx := t.Context()
	services := store.OperationalServices()
	if err := operational.ValidateOperationalServices(store.Capabilities(), services, operational.Requirements{
		SingleProcess: true, AllowVolatile: true, OAuth: true, Collection: true,
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("session-cas-and-logout", func(t *testing.T) {
		sessions := services.Sessions
		if err := sessions.PutSession(ctx, "session", "encrypted-original", time.Hour, ""); err != nil {
			t.Fatal(err)
		}
		saved, err := sessions.CompareSession(ctx, "session", "encrypted-original", "encrypted-refresh", time.Hour, "")
		if err != nil || !saved {
			t.Fatalf("refresh: %t, %v", saved, err)
		}
		if _, err := services.SessionInvalidator.InvalidateSession(ctx, "session", "encrypted-original", ""); !errors.Is(err, operational.ErrConflict) {
			t.Fatalf("stale logout: %v", err)
		}
		value, err := services.SessionInvalidator.InvalidateSession(ctx, "session", "encrypted-refresh", "")
		if err != nil || value != "encrypted-refresh" {
			t.Fatalf("stage: %q, %v", value, err)
		}
		saved, err = sessions.CompareSession(ctx, "session", "encrypted-refresh", "resurrected", time.Hour, "")
		if err != nil || saved {
			t.Fatalf("resurrected: %t, %v", saved, err)
		}
		if err := services.Revocations.QueueRevocation(ctx, "session", "newer-encrypted", ""); err != nil {
			t.Fatal(err)
		}
		if err := services.Revocations.CompleteRevocation(ctx, "session", value, ""); err != nil {
			t.Fatal(err)
		}
		record, err := services.Revocations.PendingRevocation(ctx, "")
		if err != nil || record.Value != "newer-encrypted" {
			t.Fatalf("stale completion: %#v, %v", record, err)
		}
		if err := services.Revocations.CompleteRevocation(ctx, "session", record.Value, ""); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("atomic-delivery-capacity", func(t *testing.T) {
		queue := services.Queue
		if err := queue.EnsureQueue(ctx, "admission", "workers"); err != nil {
			t.Fatal(err)
		}
		request := operational.DeliveryRequest{
			Delivery: "same-delivery", DeliveryTTL: time.Hour,
			EnqueueRequest: operational.EnqueueRequest{
				Queue: "admission", Delayed: "delayed-admission", Capacity: 1,
				Fields: operational.TaskFields{Repository: "octo/repo", Task: "{}"},
			},
		}
		var admitted atomic.Int64
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				result, err := services.Admission.AdmitDelivery(ctx, request)
				if err != nil {
					t.Error(err)
					return
				}
				if result == operational.DeliveryEnqueued {
					admitted.Add(1)
				}
			})
		}
		wg.Wait()
		if admitted.Load() != 1 {
			t.Fatalf("admissions = %d", admitted.Load())
		}
		request.Delivery = "retryable-delivery"
		if _, err := services.Admission.AdmitDelivery(ctx, request); !errors.Is(err, operational.ErrCapacity) {
			t.Fatalf("capacity: %v", err)
		}
		messages, err := queue.ReadTasks(ctx, operational.QueueRead{Queue: "admission", Group: "workers", Consumer: "one", Count: 1})
		if err != nil || len(messages) != 1 {
			t.Fatalf("read: %#v, %v", messages, err)
		}
		if err := queue.CompleteTask(ctx, "admission", "workers", messages[0].ID); err != nil {
			t.Fatal(err)
		}
		result, err := services.Admission.AdmitDelivery(ctx, request)
		if err != nil || result != operational.DeliveryEnqueued {
			t.Fatalf("capacity consumed delivery: %d, %v", result, err)
		}
	})
	t.Run("delay-and-ownership", func(t *testing.T) {
		queue := services.Queue
		if err := queue.EnsureQueue(ctx, "retry", "workers"); err != nil {
			t.Fatal(err)
		}
		enqueued, err := queue.EnqueueTask(ctx, operational.EnqueueRequest{Queue: "retry", Capacity: 5, Fields: operational.TaskFields{Repository: "octo/retry", Task: "{}"}})
		if err != nil || !enqueued {
			t.Fatalf("enqueue: %t, %v", enqueued, err)
		}
		read := operational.QueueRead{Queue: "retry", Group: "workers", Consumer: "one", Count: 1}
		messages, err := queue.ReadTasks(ctx, read)
		if err != nil || len(messages) != 1 {
			t.Fatalf("read: %#v, %v", messages, err)
		}
		now := time.Now()
		if err := queue.ReplaceTask(ctx, operational.Replacement{
			Source: "retry", Group: "workers", ID: messages[0].ID, Destination: "retry",
			Delayed: "delayed-retry", Due: now.Add(time.Hour), Fields: messages[0].Fields,
		}); err != nil {
			t.Fatal(err)
		}
		depth, err := queue.DelayedDepth(ctx, "delayed-retry")
		if err != nil || depth != 1 {
			t.Fatalf("delayed: %d, %v", depth, err)
		}
		promoted, err := queue.PromoteTasks(ctx, "delayed-retry", "retry", now, 1)
		if err != nil || promoted != 0 {
			t.Fatalf("early promotion: %d, %v", promoted, err)
		}
		promoted, err = queue.PromoteTasks(ctx, "delayed-retry", "retry", now.Add(2*time.Hour), 1)
		if err != nil || promoted != 1 {
			t.Fatalf("promotion: %d, %v", promoted, err)
		}
		lock := services.Leases
		acquired, err := lock.TryLock(ctx, "owned", "one", time.Hour)
		if err != nil || !acquired {
			t.Fatalf("lock: %t, %v", acquired, err)
		}
		if renewed, err := lock.RenewLock(ctx, "owned", "wrong", time.Hour); err != nil || renewed {
			t.Fatalf("stale renew: %t, %v", renewed, err)
		}
		if err := lock.Unlock(ctx, "owned", "wrong"); err != nil {
			t.Fatal(err)
		}
		if held, err := lock.LockHeld(ctx, "owned"); err != nil || !held {
			t.Fatalf("stale release: %t, %v", held, err)
		}
	})
	t.Run("quota-and-cancellation", func(t *testing.T) {
		quota := services.GitHubQuota
		now := time.Now().UTC()
		bucket := "github-app-1:installation-1:core"
		admission, _, _, err := quota.ReserveGitHubQuota(ctx, bucket, "unknown", 1, 10, time.Minute)
		if err != nil || admission != operational.GitHubQuotaUnknown {
			t.Fatalf("unknown quota: %d, %v", admission, err)
		}
		_, _, _, err = quota.ObserveGitHubQuota(ctx, bucket, operational.GitHubQuotaObservation{
			Limit: 5000, Remaining: 4000, ObservedAt: now, ResetAt: now.Add(time.Hour),
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		admission, _, _, err = quota.ReserveGitHubQuota(ctx, bucket, "reserved", 1, 10, time.Minute)
		if err != nil || admission != operational.GitHubQuotaAdmitted {
			t.Fatalf("reservation: %d, %v", admission, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := services.Health.Ping(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled operation: %v", err)
		}
	})
	t.Run("in-flight-blocking-read-cancellation", func(t *testing.T) {
		queue := services.Queue
		if err := queue.EnsureQueue(ctx, "cancel-blocking-read", "workers"); err != nil {
			t.Fatal(err)
		}
		readCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := queue.ReadTasks(readCtx, operational.QueueRead{
				Queue: "cancel-blocking-read", Group: "workers", Consumer: "one",
				Count: 1, Block: 10 * time.Second,
			})
			done <- err
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("in-flight cancellation: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocking read ignored cancellation")
		}
		if err := services.Health.Ping(ctx); err != nil {
			t.Fatalf("cancellation poisoned subsequent operations: %v", err)
		}
	})
	t.Run("focused-cache-limits-state-and-metadata", func(t *testing.T) {
		cache := services.Cache
		if err := cache.CacheMarketplaceRegistry(ctx, "registry", "generation", []byte("cached"), time.Hour); err != nil {
			t.Fatal(err)
		}
		if value, err := cache.CachedMarketplaceRegistry(ctx, "registry", "generation"); err != nil || string(value) != "cached" {
			t.Fatalf("cache: %q, %v", value, err)
		}
		limit, err := services.RequestLimiter.TakeRateLimitTokens(ctx, "requests", 3, time.Hour, 2)
		if err != nil || !limit.Allowed || limit.Remaining != 1 {
			t.Fatalf("request limit: %+v, %v", limit, err)
		}
		limit, err = services.RequestLimiter.TakeRateLimitTokens(ctx, "requests", 3, time.Hour, 2)
		if err != nil || limit.Allowed {
			t.Fatalf("exhausted request limit: %+v, %v", limit, err)
		}
		if err := services.State.SetOperationalState(ctx, "checkpoint", []byte("saved")); err != nil {
			t.Fatal(err)
		}
		if value, err := services.State.OperationalState(ctx, "checkpoint"); err != nil || string(value) != "saved" {
			t.Fatalf("state: %q, %v", value, err)
		}
		if _, err := services.Collection.AddMembers(ctx, "enrollment", "octo/repo"); err != nil {
			t.Fatal(err)
		}
		if present, err := services.Collection.HasMember(ctx, "enrollment", "octo/repo"); err != nil || !present {
			t.Fatalf("metadata: %t, %v", present, err)
		}
		reset := time.Now().Add(time.Hour).Unix()
		if err := services.RateLimits.ObserveRateLimit(ctx, "legacy-budget", "installation", 30, reset); err != nil {
			t.Fatal(err)
		}
		result, err := services.RateLimits.ReserveRateLimit(ctx, "legacy-budget", "installation", 10, 5, time.Now().Unix())
		if err != nil || result != 0 {
			t.Fatalf("rate-limit state: %d, %v", result, err)
		}
		if value, err := services.Collection.ReadAttribute(ctx, "legacy-budget", "installation"); err != nil || value == "" {
			t.Fatalf("rate-limit metadata: %q, %v", value, err)
		}
		if err := services.IngestionMetrics.IncrementIngestionCounter(ctx, "webhookReceived"); err != nil {
			t.Fatal(err)
		}
		if counters, _, err := services.IngestionMetrics.IngestionHealth(ctx); err != nil || counters["webhookReceived"] != 1 {
			t.Fatalf("metrics: %v, %v", counters, err)
		}
		for _, name := range []string{"adxExported", "adxFailure", "adxGap"} {
			if err := services.IngestionMetrics.IncrementIngestionCounter(ctx, name); err != nil {
				t.Fatal(err)
			}
			if counters, _, err := services.IngestionMetrics.IngestionHealth(ctx); err != nil || counters[name] != 1 {
				t.Fatalf("ADX metrics: %v, %v", counters, err)
			}
		}
		if err := services.Backend.Maintain(ctx); err != nil {
			t.Fatal(err)
		}
		if health, err := services.Health.Health(ctx); err != nil || !health.Ready {
			t.Fatalf("health: %+v, %v", health, err)
		}
	})
	t.Run("focused-delivery-reservations", func(t *testing.T) {
		deliveries := services.Deliveries
		reserved, err := deliveries.ReserveDelivery(ctx, "reserved-delivery", time.Hour)
		if err != nil || reserved != operational.DeliveryReserved {
			t.Fatalf("reserve: %d, %v", reserved, err)
		}
		reserved, err = deliveries.ReserveDelivery(ctx, "reserved-delivery", time.Hour)
		if err != nil || reserved != operational.DeliveryInProgress {
			t.Fatalf("concurrent reserve: %d, %v", reserved, err)
		}
		if err := deliveries.ReleaseDeliveryReservation(ctx, "reserved-delivery"); err != nil {
			t.Fatal(err)
		}
		if fresh, err := deliveries.RememberDelivery(ctx, "reserved-delivery", time.Hour); err != nil || !fresh {
			t.Fatalf("commit: %t, %v", fresh, err)
		}
		reserved, err = deliveries.ReserveDelivery(ctx, "reserved-delivery", time.Hour)
		if err != nil || reserved != operational.DeliveryAlreadyCommitted {
			t.Fatalf("committed reserve: %d, %v", reserved, err)
		}
		if err := deliveries.ForgetDelivery(ctx, "reserved-delivery"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCapabilitiesRejectInvalidServices(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	requirements := operational.Requirements{OAuth: true, Collection: true, SingleProcess: true, AllowVolatile: true}
	if err := operational.ValidateOperationalServices(store.Capabilities(), store.OperationalServices(), requirements); err != nil {
		t.Fatal(err)
	}
	requirements.AllowVolatile = false
	if err := operational.ValidateOperationalServices(store.Capabilities(), store.OperationalServices(), requirements); err == nil {
		t.Fatal("volatile state accepted without acknowledgement")
	}
	requirements.AllowVolatile = true
	requirements.SingleProcess = false
	if err := operational.ValidateOperationalServices(store.Capabilities(), store.OperationalServices(), requirements); err == nil {
		t.Fatal("process-only state accepted for multiple processes")
	}
	var absent *memory.Store
	if err := operational.CheckStore(absent); !errors.Is(err, operational.ErrUnavailable) {
		t.Fatalf("typed-nil store: %v", err)
	}
	services := store.OperationalServices()
	services.Sessions = absent
	requirements.SingleProcess = true
	if err := operational.ValidateOperationalServices(store.Capabilities(), services, requirements); err == nil {
		t.Fatal("typed-nil service accepted")
	}
	focused := store.OperationalServices()
	if err := operational.ValidateOperationalServices(store.Capabilities(), focused, requirements); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*operational.OperationalServices){
		"backend":             func(s *operational.OperationalServices) { s.Backend = absent },
		"cache":               func(s *operational.OperationalServices) { s.Cache = absent },
		"request limiter":     func(s *operational.OperationalServices) { s.RequestLimiter = absent },
		"sessions":            func(s *operational.OperationalServices) { s.Sessions = absent },
		"session invalidator": func(s *operational.OperationalServices) { s.SessionInvalidator = absent },
		"revocations":         func(s *operational.OperationalServices) { s.Revocations = absent },
		"leases":              func(s *operational.OperationalServices) { s.Leases = absent },
		"state":               func(s *operational.OperationalServices) { s.State = absent },
		"deliveries":          func(s *operational.OperationalServices) { s.Deliveries = absent },
		"queue":               func(s *operational.OperationalServices) { s.Queue = absent },
		"admission":           func(s *operational.OperationalServices) { s.Admission = absent },
		"collection":          func(s *operational.OperationalServices) { s.Collection = absent },
		"github quota":        func(s *operational.OperationalServices) { s.GitHubQuota = absent },
		"rate limits":         func(s *operational.OperationalServices) { s.RateLimits = absent },
		"health":              func(s *operational.OperationalServices) { s.Health = absent },
		"ingestion metrics":   func(s *operational.OperationalServices) { s.IngestionMetrics = absent },
	}
	for name, omit := range tests {
		t.Run(name, func(t *testing.T) {
			incomplete := focused
			omit(&incomplete)
			if err := operational.ValidateOperationalServices(store.Capabilities(), incomplete, requirements); err == nil {
				t.Fatal("missing advertised service accepted")
			}
		})
	}
}
