package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func testStore(t *testing.T, config Config) (*Store, string) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL is required for real PostgreSQL operational tests")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	namespace := "operational-test-" + hex.EncodeToString(nonce[:])
	s, err := New(t.Context(), dsn, namespace, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanup, err := Open(ctx, dsn, namespace, config)
		if err != nil {
			t.Error(err)
			return
		}
		if err := cleanup.DeleteNamespace(ctx); err != nil {
			t.Error(err)
		}
		_ = cleanup.Close()
		_ = s.Close()
	})
	return s, dsn
}

func TestConfigurationAndTransport(t *testing.T) {
	for _, config := range []Config{{MaxProtectedBytes: -1}, {MaxCacheBytes: 1, MaxCacheValueBytes: 2}} {
		if _, err := New(t.Context(), "postgres://127.0.0.1:1/test?sslmode=disable", "test", config); !errors.Is(err, ErrInvalid) {
			t.Fatalf("config: %v", err)
		}
	}
	if _, err := New(t.Context(), "", "test", Config{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty connection: %v", err)
	}
	for _, dsn := range []string{
		"postgres://user:secret@remote.example/db?sslmode=disable",
		"postgres://user:secret@remote.example/db?sslmode=prefer",
	} {
		if _, err := New(t.Context(), dsn, "test", Config{}); err == nil || !strings.Contains(err.Error(), "TLS") {
			t.Fatalf("transport: %v", err)
		}
	}
}

func TestRestartAndDeploymentIsolation(t *testing.T) {
	s, dsn := testStore(t, Config{})
	ctx := t.Context()
	if err := operational.ValidateOperationalServices(s.Capabilities(), s.OperationalServices(), operational.Requirements{OAuth: true, Collection: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSession(ctx, "session", "encrypted", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureQueue(ctx, "ready", "workers"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueTask(ctx, operational.EnqueueRequest{Queue: "ready", Fields: operational.TaskFields{Task: "unfinished"}}); err != nil {
		t.Fatal(err)
	}
	read := operational.QueueRead{Queue: "ready", Group: "workers", Consumer: "original", Count: 1}
	messages, err := s.ReadTasks(ctx, read)
	if err != nil || len(messages) != 1 {
		t.Fatalf("read: %v %v", messages, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dsn, s.namespace, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if value, err := reopened.SessionRecord(ctx, "session"); err != nil || value != "encrypted" {
		t.Fatalf("session: %q %v", value, err)
	}
	read.Consumer = "replacement"
	claimed, err := reopened.ClaimTasks(ctx, read, 0)
	if err != nil || len(claimed) != 1 || claimed[0].ID != messages[0].ID {
		t.Fatalf("restart lease: %v %v", claimed, err)
	}
	if err := reopened.CompleteTask(ctx, "ready", "workers", claimed[0].ID); err != nil {
		t.Fatal(err)
	}
	other, _ := testStore(t, Config{})
	if value, err := other.SessionRecord(ctx, "session"); err != nil || value != "" {
		t.Fatalf("namespace leak: %q %v", value, err)
	}
	if _, err := Open(ctx, dsn, s.namespace, Config{MaxProtectedEntries: 3}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched bounds: %v", err)
	}
	if _, err := Open(ctx, dsn, s.namespace+"-absent", Config{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("doctor initialized missing namespace: %v", err)
	}
}

func TestCrossConnectionAtomicity(t *testing.T) {
	s, dsn := testStore(t, Config{})
	peer, err := Open(t.Context(), dsn, s.namespace, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	ctx := t.Context()
	var allowed, acquired atomic.Int64
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			adapter := s
			if i%2 != 0 {
				adapter = peer
			}
			result, err := adapter.TakeRateLimitToken(ctx, "shared", 5, time.Hour)
			if err != nil {
				t.Error(err)
				return
			}
			if result.Allowed {
				allowed.Add(1)
			}
			ok, err := adapter.TryLock(ctx, "owned", fmt.Sprint(i), time.Hour)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				acquired.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 5 || acquired.Load() != 1 {
		t.Fatalf("limits=%d locks=%d", allowed.Load(), acquired.Load())
	}
	now := time.Now().UTC()
	if _, _, _, err := s.ObserveGitHubQuota(ctx, "bucket", operational.GitHubQuotaObservation{Limit: 100, Remaining: 30, ResetAt: now.Add(time.Hour), ObservedAt: now}, ""); err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int64
	for i := range 40 {
		wg.Go(func() {
			adapter := s
			if i%2 != 0 {
				adapter = peer
			}
			result, _, _, err := adapter.ReserveGitHubQuota(ctx, "bucket", fmt.Sprint(i), 1, 10, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if result == operational.GitHubQuotaAdmitted {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 20 {
		t.Fatalf("quota admitted=%d", admitted.Load())
	}
}

func TestProtectedCapacityAndRollback(t *testing.T) {
	s, _ := testStore(t, Config{MaxProtectedEntries: 4, MaxCacheEntries: 1})
	ctx := t.Context()
	if err := s.PutSession(ctx, "session", "encrypted", time.Hour, strings.Repeat("n", 1024)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureQueue(ctx, "ready", "workers"); err != nil {
		t.Fatal(err)
	}
	request := operational.DeliveryRequest{Delivery: "retryable", DeliveryTTL: time.Hour, EnqueueRequest: operational.EnqueueRequest{Queue: "ready", Fields: operational.TaskFields{Task: "work"}}}
	if _, err := s.AdmitDelivery(ctx, request); !errors.Is(err, operational.ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	if stats, err := s.QueueStats(ctx, "ready", "workers"); err != nil || stats.Length != 0 {
		t.Fatalf("partial admission: %+v %v", stats, err)
	}
	value, err := s.InvalidateSession(ctx, "session", "encrypted", strings.Repeat("n", 1024))
	if err != nil || value != "encrypted" {
		t.Fatalf("reserved logout: %q %v", value, err)
	}
	if err := s.CompleteRevocation(ctx, "session", "encrypted", strings.Repeat("n", 1024)); err != nil {
		t.Fatal(err)
	}
	if result, err := s.AdmitDelivery(ctx, request); err != nil || result != operational.DeliveryEnqueued {
		t.Fatalf("consumed delivery on capacity failure: %d %v", result, err)
	}
	read := operational.QueueRead{Queue: "ready", Group: "workers", Consumer: "one", Count: 1}
	messages, err := s.ReadTasks(ctx, read)
	if err != nil || len(messages) != 1 {
		t.Fatalf("read: %v %v", messages, err)
	}
	if _, err := s.EnqueueTask(ctx, operational.EnqueueRequest{Queue: "full", Fields: operational.TaskFields{Task: "block"}}); !errors.Is(err, operational.ErrCapacity) {
		t.Fatalf("protected pressure: %v", err)
	}
	if err := s.ReplaceTask(ctx, operational.Replacement{Source: "ready", Group: "workers", ID: messages[0].ID, Destination: "ready", Delayed: "delayed", Due: time.Now().Add(time.Hour), Fields: operational.TaskFields{Task: strings.Repeat("x", 2<<20)}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid replacement: %v", err)
	}
	if claimed, err := s.ClaimTasks(ctx, read, 0); err != nil || len(claimed) != 1 {
		t.Fatalf("lost original: %v %v", claimed, err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := s.CacheMarketplaceRegistry(ctx, id, "gen", []byte(id), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if stats, err := s.QueueStats(ctx, "ready", "workers"); err != nil || stats.Pending != 1 {
		t.Fatalf("cache evicted protected work: %+v %v", stats, err)
	}
}

func TestExpiryCacheMetadataAndDiagnostics(t *testing.T) {
	s, _ := testStore(t, Config{})
	ctx := t.Context()
	digest := strings.Repeat("a", 64)
	if ok, _, err := s.CacheQueryResult(ctx, digest, []byte{}, 1024, 4096); err != nil || !ok {
		t.Fatalf("empty result: %t %v", ok, err)
	}

	if value, stats, err := s.CachedQueryResult(ctx, digest, 1024, 4096); err != nil || value == nil || len(value) != 0 || stats.Entries != 1 {
		t.Fatalf("empty cache: %v %+v %v", value, stats, err)
	}
	if ok, _, err := s.CacheQueryResult(ctx, strings.Repeat("b", 64), make([]byte, 2048), 1024, 4096); err != nil || ok {
		t.Fatalf("oversize query: %t %v", ok, err)
	}
	if err := s.PutSession(ctx, "expired", "old", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE cao_operational_records SET expires_at=clock_timestamp()-interval '1 second' WHERE namespace=$1 AND kind IN ('session','session-reserve')`, s.namespace); err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionRecord(ctx, "expired"); err != nil || value != "" {
		t.Fatalf("expired authority: %q %v", value, err)
	}
	if err := s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingRevocation(ctx, "")
	if err != nil || pending.ID != "expired" || pending.Value != "old" {
		t.Fatalf("expired session cleanup: %+v %v", pending, err)
	}
	if err := s.CompleteRevocation(ctx, pending.ID, pending.Value, ""); err != nil {
		t.Fatal(err)
	}
	var entries int64
	if err := s.db.QueryRowContext(ctx, `SELECT protected_entries FROM cao_operational_namespaces WHERE namespace=$1`, s.namespace).Scan(&entries); err != nil || entries != 0 {
		t.Fatalf("expiry accounting: %d %v", entries, err)
	}
	if _, err := s.AddMembers(ctx, "old:1", "a/repo", "b/repo"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAttribute(ctx, "owners", "a/repo", "1"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.TransferOwners(ctx, "owners", "old:", "", 2, []string{"a/repo", "a/repo"}); err != nil || n != 1 {
		t.Fatalf("transfer: %d %v", n, err)
	}
	if has, err := s.HasMember(ctx, "old:1", "a/repo"); err != nil || has {
		t.Fatalf("stale owner: %t %v", has, err)
	}
	if _, err := s.AddMembers(ctx, "scan", "c", "a", "b"); err != nil {
		t.Fatal(err)
	}
	first, next, err := s.ScanMembers(ctx, "scan", "0", 2)
	if err != nil || strings.Join(first, ",") != "a,b" || next == "0" {
		t.Fatalf("scan: %v %q %v", first, next, err)
	}
	last, next, err := s.ScanMembers(ctx, "scan", next, 2)
	if err != nil || strings.Join(last, ",") != "c" || next != "0" {
		t.Fatalf("scan tail: %v %q %v", last, next, err)
	}
	if err := s.IncrementIngestionCounter(ctx, "webhookReceived"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordIngestionHealthEvent(ctx, "failure", "collection", time.Now()); err != nil {
		t.Fatal(err)
	}
	counters, events, err := s.IngestionHealth(ctx)
	if err != nil || counters["webhookReceived"] != 1 || events["healthRevision"] != "2" {
		t.Fatalf("diagnostics: %v %v %v", counters, events, err)
	}
	loads, err := s.Loads(ctx, []string{"webhook"}, time.Minute)
	if err != nil || loads["webhook"] <= 0 {
		t.Fatalf("load: %v %v", loads, err)
	}
}

func TestWaitingTransactionCancellation(t *testing.T) {
	s, dsn := testStore(t, Config{})
	peer, err := Open(t.Context(), dsn, s.namespace, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), `SELECT namespace FROM cao_operational_namespaces WHERE namespace=$1 FOR UPDATE`, s.namespace); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := peer.TryLock(ctx, "blocked", "owner", time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting transaction: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := peer.Ping(t.Context()); err != nil {
		t.Fatalf("cancellation poisoned pool: %v", err)
	}
}

func TestProtectedByteCapacityAndRevocation(t *testing.T) {
	s, _ := testStore(t, Config{MaxProtectedBytes: 4096})
	ctx := t.Context()
	namespace := strings.Repeat("n", 1024)
	if err := s.PutSession(ctx, "session", "encrypted", time.Hour, namespace); err != nil {
		t.Fatal(err)
	}
	var used int64
	if err := s.db.QueryRowContext(ctx, `SELECT protected_bytes FROM cao_operational_namespaces WHERE namespace=$1`, s.namespace).Scan(&used); err != nil {
		t.Fatal(err)
	}
	filler := strings.Repeat("x", int(s.config.MaxProtectedBytes-used-charge(key{"attribute", "fill", "value"}, nil)))
	if err := s.WriteAttribute(ctx, "fill", "value", filler); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT protected_bytes FROM cao_operational_namespaces WHERE namespace=$1`, s.namespace).Scan(&used); err != nil || used != 4096 {
		t.Fatalf("byte budget: %d %v", used, err)
	}
	if err := s.WriteAttribute(ctx, "extra", "value", "x"); !errors.Is(err, operational.ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	if value, err := s.InvalidateSession(ctx, "session", "encrypted", namespace); err != nil || value != "encrypted" {
		t.Fatalf("reserved logout at byte capacity: %q %v", value, err)
	}
	if revocation, err := s.PendingRevocation(ctx, namespace); err != nil || revocation.ID != "session" {
		t.Fatalf("durable revocation: %+v %v", revocation, err)
	}
}

func TestNoopQuotaMutationsDoNotNeedProtectedCapacity(t *testing.T) {
	s, _ := testStore(t, Config{MaxProtectedEntries: 2})
	ctx := t.Context()
	if err := s.PutSession(ctx, "session", "encrypted", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	const bucket = "github-app-1:installation-1:core"
	if released, _, err := s.ReleaseGitHubQuota(ctx, bucket, "missing"); err != nil || released {
		t.Fatalf("release at capacity: %t %v", released, err)
	}
	if state, err := s.UnparkGitHubQuota(ctx, bucket); err != nil || state.Known || !state.ParkedUntil.IsZero() {
		t.Fatalf("unpark at capacity: %+v %v", state, err)
	}
	if _, _, err := s.ParkGitHubQuota(ctx, bucket, time.Now().Add(time.Hour), "why"); !errors.Is(err, operational.ErrCapacity) {
		t.Fatalf("park must still need capacity: %v", err)
	}
}

func TestNoopQuotaMutationsPreserveExistingState(t *testing.T) {
	s, _ := testStore(t, Config{})
	ctx := t.Context()
	const bucket = "github-app-1:installation-1:core"
	now := time.Now().UTC()
	if _, _, _, err := s.ObserveGitHubQuota(ctx, bucket, operational.GitHubQuotaObservation{
		Limit: 5000, Remaining: 4000, ObservedAt: now, ResetAt: now.Add(time.Hour),
	}, ""); err != nil {
		t.Fatal(err)
	}
	until := now.Add(30 * time.Minute)
	if extended, _, err := s.ParkGitHubQuota(ctx, bucket, until, "why"); err != nil || !extended {
		t.Fatalf("park: %t %v", extended, err)
	}
	if extended, state, err := s.ParkGitHubQuota(ctx, bucket, until.Add(-time.Minute), "shorter"); err != nil || extended || state.ParkReason != "why" {
		t.Fatalf("non-extending park: %t %+v %v", extended, state, err)
	}
	if state, err := s.UnparkGitHubQuota(ctx, bucket); err != nil || !state.ParkedUntil.IsZero() || state.ParkReason != "" || !state.Known || state.Remaining != 4000 {
		t.Fatalf("unpark: %+v %v", state, err)
	}
	if state, err := s.UnparkGitHubQuota(ctx, bucket); err != nil || !state.Known || state.Remaining != 4000 {
		t.Fatalf("repeated unpark: %+v %v", state, err)
	}
	admission, _, _, err := s.ReserveGitHubQuota(ctx, bucket, "reservation", 10, 0, time.Hour)
	if err != nil || admission != operational.GitHubQuotaAdmitted {
		t.Fatalf("reserve: %d %v", admission, err)
	}
	if released, state, err := s.ReleaseGitHubQuota(ctx, bucket, "reservation"); err != nil || !released || state.Reserved != 0 {
		t.Fatalf("release: %t %+v %v", released, state, err)
	}
	if released, state, err := s.ReleaseGitHubQuota(ctx, bucket, "reservation"); err != nil || released || state.Remaining != 4000 {
		t.Fatalf("repeated release: %t %+v %v", released, state, err)
	}
}

func TestCorruptQuotaFailsClosed(t *testing.T) {
	s, _ := testStore(t, Config{})
	if err := s.transact(t.Context(), func(tx *transaction) error {
		return tx.putJSON(key{"quota", "bucket", ""}, quota{}, time.Time{})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ReserveGitHubQuota(t.Context(), "bucket", "reservation", 1, 0, time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("corrupt quota: %v", err)
	}
}

func TestRateLimitBackwardClockDoesNotRefill(t *testing.T) {
	s, _ := testStore(t, Config{})
	if err := s.transact(t.Context(), func(tx *transaction) error {
		return tx.putJSON(key{"limiter", "future", ""}, tokenBucket{At: tx.now.Add(time.Hour)}, time.Time{})
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := s.TakeRateLimitToken(t.Context(), "future", 100000, time.Minute)
		if err != nil || result.Allowed || result.Remaining != 0 {
			t.Fatalf("backward clock created tokens: %+v %v", result, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
