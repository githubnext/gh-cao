package redisx

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func quotaReply(code, extra int64, reason any) []any {
	return []any{code, extra, int64(5000), int64(4200), int64(1_900_000_000_000), int64(1_899_999_000_000),
		int64(0), int64(300), int64(1_899_999_500_000), int64(1), reason}
}

func TestReserveGitHubQuotaUsesOneAtomicScriptPerBucket(t *testing.T) {
	client := &rateLimitCommandClient{value: quotaReply(0, 1_900_000_100_000, "")}
	store := NewStore(client, "quota-test")

	admission, expires, state, err := store.ReserveGitHubQuota(
		t.Context(), "collector:123:core", "abc123", 500, 1000, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if admission != GitHubQuotaAdmitted || expires.UnixMilli() != 1_900_000_100_000 {
		t.Fatalf("unexpected admission %v expires %v", admission, expires)
	}
	if !state.Known || state.Remaining != 4200 || state.Reserved != 300 || state.Limit != 5000 {
		t.Fatalf("unexpected state %#v", state)
	}
	command := client.command
	if len(command) != 10 || command[0] != "EVAL" || command[2] != "3" ||
		command[3] != "cao:quota-test:github:quota:{collector:123:core}:state" ||
		command[4] != "cao:quota-test:github:quota:{collector:123:core}:reservations" ||
		command[5] != "cao:quota-test:github:quota:{collector:123:core}:expiries" ||
		command[6] != "abc123" || command[7] != "500" || command[8] != "1000" || command[9] != "60000" {
		t.Fatalf("unexpected Redis command: %#v", command[2:])
	}
	for _, fragment := range []string{
		`redis.call("TIME")`,
		`redis.call("ZRANGEBYSCORE", KEYS[3], "-inf", now)`,
		`tonumber(s[1]) - reserved_total() - tonumber(ARGV[2]) < tonumber(ARGV[3])`,
		`redis.call("ZADD", KEYS[3], expires, ARGV[1])`,
	} {
		if !strings.Contains(command[1], fragment) {
			t.Fatalf("reservation script does not contain %s", fragment)
		}
	}
}

func TestGitHubQuotaRejectsInvalidArguments(t *testing.T) {
	store := NewStore(&rateLimitCommandClient{value: quotaReply(0, 0, "")}, "quota-test")
	ctx := t.Context()
	if _, err := store.GitHubQuotaSnapshot(ctx, "bad key {x}"); err == nil {
		t.Fatal("invalid bucket key was accepted")
	}
	if _, _, _, err := store.ReserveGitHubQuota(ctx, "a:1:core", "id", 0, 0, time.Minute); err == nil {
		t.Fatal("zero cost was accepted")
	}
	if _, _, _, err := store.ReserveGitHubQuota(ctx, "a:1:core", "id", 1, -1, time.Minute); err == nil {
		t.Fatal("negative minimum was accepted")
	}
	if _, _, _, err := store.ReserveGitHubQuota(ctx, "a:1:core", "id", 1, 0, 0); err == nil {
		t.Fatal("zero TTL was accepted")
	}
	if _, _, _, err := store.ReserveGitHubQuota(ctx, "a:1:core", "", 1, 0, time.Minute); err == nil {
		t.Fatal("empty reservation ID was accepted")
	}
	if _, _, _, err := store.ObserveGitHubQuota(ctx, "a:1:core", GitHubQuotaObservation{Limit: 1, Remaining: -1, ResetAt: time.Now(), ObservedAt: time.Now()}, ""); err == nil {
		t.Fatal("negative remaining was accepted")
	}
	if _, _, _, err := store.ObserveGitHubQuota(ctx, "a:1:core", GitHubQuotaObservation{Limit: 1, Remaining: 1}, ""); err == nil {
		t.Fatal("observation without a reset time was accepted")
	}
	if _, _, err := store.ParkGitHubQuota(ctx, "a:1:core", time.Time{}, "x"); err == nil {
		t.Fatal("parking without an end time was accepted")
	}
	if _, _, err := store.ParkGitHubQuota(ctx, "a:1:core", time.Now(), strings.Repeat("x", 257)); err == nil {
		t.Fatal("oversized parking reason was accepted")
	}
}

func TestGitHubQuotaRejectsPreEpochInstants(t *testing.T) {
	client := &rateLimitCommandClient{value: quotaReply(0, 0, "")}
	store := NewStore(client, "quota-test")
	ctx := t.Context()
	future := time.Now().Add(time.Hour)
	for _, at := range []time.Time{time.Unix(0, 0), time.Unix(-5, 0)} {
		if _, _, _, err := store.ObserveGitHubQuota(ctx, "a:1:core", GitHubQuotaObservation{Limit: 1, Remaining: 1, ResetAt: at}, ""); err == nil {
			t.Fatalf("reset at %v was accepted", at)
		}
		if _, _, _, err := store.ObserveGitHubQuota(ctx, "a:1:core", GitHubQuotaObservation{Limit: 1, Remaining: 1, ResetAt: future, ObservedAt: at}, ""); err == nil {
			t.Fatalf("observation at %v was accepted", at)
		}
		if _, _, err := store.ParkGitHubQuota(ctx, "a:1:core", at, "x"); err == nil {
			t.Fatalf("parking until %v was accepted", at)
		}
	}
	if client.command != nil {
		t.Fatalf("rejected input reached Redis: %v", client.command)
	}
}

func TestParseGitHubQuotaReply(t *testing.T) {
	code, extra, state, err := parseGitHubQuotaReply(quotaReply(2, 1, "retry-after"))
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 || extra != 1 || state.ParkReason != "retry-after" || !state.ParkedUntil.IsZero() ||
		state.ResetAt.UnixMilli() != 1_900_000_000_000 || state.Now.UnixMilli() != 1_899_999_500_000 {
		t.Fatalf("unexpected decoded reply: %d %d %#v", code, extra, state)
	}
	strings := []any{"0", "0", "0", "0", "0", "0", "0", "0", "1000", "0", nil}
	if _, _, state, err := parseGitHubQuotaReply(strings); err != nil || state.Known {
		t.Fatalf("unknown state reply was not decoded: %#v %v", state, err)
	}
	for name, reply := range map[string]any{
		"not a slice":       int64(1),
		"short":             []any{int64(0)},
		"negative field":    []any{int64(0), int64(0), int64(-1), int64(0), int64(0), int64(0), int64(0), int64(0), int64(1), int64(1), ""},
		"invalid known":     []any{int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(1), int64(2), ""},
		"missing clock":     []any{int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(1), ""},
		"unparsable number": []any{"x", int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(1), int64(1), ""},
		"non-string reason": []any{int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(1), int64(1), int64(3)},
	} {
		if _, _, _, err := parseGitHubQuotaReply(reply); err == nil {
			t.Fatalf("%s reply was accepted", name)
		}
	}
}

func TestGitHubQuotaScriptsAgainstRedis(t *testing.T) {
	rawURL := os.Getenv("REDIS_URL")
	if rawURL == "" {
		t.Skip("REDIS_URL is not set")
	}
	client, err := New(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(client, "quota-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bucket := "collector:123:core"
	reset := time.Now().Add(time.Hour).Truncate(time.Second)

	state, err := store.GitHubQuotaSnapshot(ctx, bucket)
	if err != nil || state.Known || state.Now.IsZero() {
		t.Fatalf("unexpected empty snapshot %#v %v", state, err)
	}
	if admission, _, _, err := store.ReserveGitHubQuota(ctx, bucket, "r0", 1, 0, time.Minute); err != nil || admission != GitHubQuotaUnknown {
		t.Fatalf("unknown bucket admission = %v, %v", admission, err)
	}
	outcome, _, state, err := store.ObserveGitHubQuota(ctx, bucket, GitHubQuotaObservation{Limit: 5000, Remaining: 1500, ResetAt: reset}, "")
	if err != nil || outcome != GitHubQuotaObservationReplaced || state.Remaining != 1500 || !state.ResetAt.Equal(reset) ||
		state.ObservedAt.IsZero() || state.ObservedAt.After(state.Now) {
		t.Fatalf("first observation = %v %#v %v", outcome, state, err)
	}
	// 1500 - 400 >= 1000 admits; 1500 - 400 - 200 < 1000 denies.
	if admission, expires, _, err := store.ReserveGitHubQuota(ctx, bucket, "r1", 400, 1000, time.Minute); err != nil || admission != GitHubQuotaAdmitted || expires.IsZero() {
		t.Fatalf("reservation within floor = %v %v %v", admission, expires, err)
	}
	if admission, _, _, err := store.ReserveGitHubQuota(ctx, bucket, "r1", 1, 0, time.Minute); err != nil || admission != GitHubQuotaDuplicateReservation {
		t.Fatalf("duplicate reservation = %v %v", admission, err)
	}
	if admission, _, state, err := store.ReserveGitHubQuota(ctx, bucket, "r2", 200, 1000, time.Minute); err != nil || admission != GitHubQuotaExhausted || state.Reserved != 400 {
		t.Fatalf("reservation crossing floor = %v %#v %v", admission, state, err)
	}
	// A same-window observation never raises remaining quota.
	if outcome, _, state, err := store.ObserveGitHubQuota(ctx, bucket, GitHubQuotaObservation{Limit: 5000, Remaining: 2000, ResetAt: reset, ObservedAt: time.Now()}, ""); err != nil || outcome != GitHubQuotaObservationReconciled || state.Remaining != 1500 {
		t.Fatalf("same-window higher observation = %v %#v %v", outcome, state, err)
	}
	// An older-window observation is ignored.
	if outcome, _, state, err := store.ObserveGitHubQuota(ctx, bucket, GitHubQuotaObservation{Limit: 5000, Remaining: 10, ResetAt: reset.Add(-time.Hour), ObservedAt: time.Now()}, ""); err != nil || outcome != GitHubQuotaObservationStale || state.Remaining != 1500 {
		t.Fatalf("stale observation = %v %#v %v", outcome, state, err)
	}
	// Commit records the observation and removes the reservation atomically.
	outcome, released, state, err := store.ObserveGitHubQuota(ctx, bucket, GitHubQuotaObservation{Limit: 5000, Remaining: 1200, ResetAt: reset, ObservedAt: time.Now()}, "r1")
	if err != nil || outcome != GitHubQuotaObservationReconciled || !released || state.Remaining != 1200 || state.Reserved != 0 {
		t.Fatalf("commit = %v %v %#v %v", outcome, released, state, err)
	}
	// A new window replaces the recorded state.
	if outcome, _, state, err = store.ObserveGitHubQuota(ctx, bucket, GitHubQuotaObservation{Limit: 5000, Remaining: 4999, ResetAt: reset.Add(time.Hour), ObservedAt: time.Now()}, ""); err != nil || outcome != GitHubQuotaObservationReplaced || state.Remaining != 4999 {
		t.Fatalf("new window = %v %#v %v", outcome, state, err)
	}
	// Reservations expire on the Redis clock.
	if admission, _, _, err := store.ReserveGitHubQuota(ctx, bucket, "r3", 100, 0, 50*time.Millisecond); err != nil || admission != GitHubQuotaAdmitted {
		t.Fatalf("short reservation = %v %v", admission, err)
	}
	time.Sleep(120 * time.Millisecond)
	if state, err = store.GitHubQuotaSnapshot(ctx, bucket); err != nil || state.Reserved != 0 {
		t.Fatalf("expired reservation still held %#v %v", state, err)
	}
	if admission, _, _, err := store.ReserveGitHubQuota(ctx, bucket, "r4", 100, 0, time.Minute); err != nil || admission != GitHubQuotaAdmitted {
		t.Fatalf("reservation = %v %v", admission, err)
	}
	if released, state, err := store.ReleaseGitHubQuota(ctx, bucket, "r4"); err != nil || !released || state.Reserved != 0 {
		t.Fatalf("release = %v %#v %v", released, state, err)
	}
	// Parking only extends and preserves the primary quota state.
	parkedUntil := time.Now().Add(time.Minute)
	if extended, state, err := store.ParkGitHubQuota(ctx, bucket, parkedUntil, "retry-after"); err != nil || !extended || state.ParkReason != "retry-after" || state.Remaining != 4999 {
		t.Fatalf("park = %v %#v %v", extended, state, err)
	}
	if extended, state, err := store.ParkGitHubQuota(ctx, bucket, parkedUntil.Add(-time.Second), "shorter"); err != nil || extended || state.ParkReason != "retry-after" {
		t.Fatalf("shorter park = %v %#v %v", extended, state, err)
	}
	if admission, _, _, err := store.ReserveGitHubQuota(ctx, bucket, "r5", 1, 0, time.Minute); err != nil || admission != GitHubQuotaParked {
		t.Fatalf("parked admission = %v %v", admission, err)
	}
	if state, err = store.UnparkGitHubQuota(ctx, bucket); err != nil || !state.ParkedUntil.IsZero() || state.ParkReason != "" || state.Remaining != 4999 {
		t.Fatalf("unpark = %#v %v", state, err)
	}
	if admission, _, _, err := store.ReserveGitHubQuota(ctx, bucket, "r6", 1, 0, time.Minute); err != nil || admission != GitHubQuotaAdmitted {
		t.Fatalf("unparked admission = %v %v", admission, err)
	}
}
