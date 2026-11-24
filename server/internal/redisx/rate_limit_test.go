package redisx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type rateLimitCommandClient struct {
	value   any
	command []string
}

func (client *rateLimitCommandClient) Do(_ context.Context, command ...string) (any, error) {
	client.command = append([]string{}, command...)
	return client.value, nil
}

func (*rateLimitCommandClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, nil
}

func TestTakeRateLimitTokenUsesAtomicRedisTokenBucket(t *testing.T) {
	client := &rateLimitCommandClient{value: []any{int64(1), int64(29), int64(0), int64(2000)}}
	store := NewStore(client, "rate-test")

	result, err := store.TakeRateLimitToken(t.Context(), "query:subject", 30, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allowed || result.Remaining != 29 || result.RetryAfter != 0 || result.ResetAfter != 2*time.Second {
		t.Fatalf("unexpected rate limit result: %#v", result)
	}
	if len(client.command) != 7 || client.command[0] != "EVAL" || client.command[2] != "1" ||
		client.command[3] != "cao:rate-test:rate-limit:query:subject" ||
		client.command[4] != "30" || client.command[5] != "60000" || client.command[6] != "1" {
		t.Fatalf("unexpected Redis command: %#v", client.command)
	}
	script := client.command[1]
	for _, operation := range []string{`redis.call("TIME")`, `redis.call("HMGET"`, `redis.call("HSET"`, `redis.call("PEXPIRE"`} {
		if !strings.Contains(script, operation) {
			t.Fatalf("rate limit script does not contain %s", operation)
		}
	}
}

func TestTakeRateLimitTokensAssignsWeightedCost(t *testing.T) {
	client := &rateLimitCommandClient{value: []any{int64(1), int64(24), int64(0), int64(12000)}}
	store := NewStore(client, "rate-test")

	result, err := store.TakeRateLimitTokens(t.Context(), "query:subject", 30, time.Minute, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allowed || result.Remaining != 24 {
		t.Fatalf("unexpected weighted rate limit result: %#v", result)
	}
	if len(client.command) != 7 || client.command[6] != "6" {
		t.Fatalf("weighted cost was not passed to Redis: %#v", client.command)
	}
	if !strings.Contains(client.command[1], "tokens >= cost") ||
		!strings.Contains(client.command[1], "tokens = tokens - cost") ||
		!strings.Contains(client.command[1], "elseif cost > 1") ||
		!strings.Contains(client.command[1], "(cost - tokens)") {
		t.Fatal("rate limit script does not apply the weighted cost atomically")
	}
}

func TestTakeRateLimitTokenRejectsInvalidBoundsAndResponses(t *testing.T) {
	client := &rateLimitCommandClient{value: int64(1)}
	store := NewStore(client, "rate-test")
	if _, err := store.TakeRateLimitToken(t.Context(), "general:subject", 0, time.Minute); err == nil {
		t.Fatal("zero capacity was accepted")
	}
	if _, err := store.TakeRateLimitToken(t.Context(), "general:subject", 1, 0); err == nil {
		t.Fatal("zero refill period was accepted")
	}
	if _, err := store.TakeRateLimitToken(t.Context(), "general:subject", 1, time.Nanosecond); err == nil {
		t.Fatal("sub-millisecond refill period was accepted")
	}
	if _, err := store.TakeRateLimitTokens(t.Context(), "general:subject", 1, time.Minute, 0); err == nil {
		t.Fatal("zero cost was accepted")
	}
	if _, err := store.TakeRateLimitTokens(t.Context(), "general:subject", 1, time.Minute, 2); err == nil {
		t.Fatal("cost above capacity was accepted")
	}
	if _, err := store.TakeRateLimitToken(t.Context(), "general:subject", 1, time.Minute); err == nil {
		t.Fatal("invalid Redis response was accepted")
	}
	client.value = []any{int64(1), int64(2), int64(0), int64(1)}
	if _, err := store.TakeRateLimitToken(t.Context(), "general:subject", 1, time.Minute); err == nil {
		t.Fatal("out-of-range Redis response was accepted")
	}
}

func TestParseRateLimitReplyDecodesAllowedAndDeniedOutcomes(t *testing.T) {
	allowed, err := parseRateLimitReply([]any{int64(1), int64(29), int64(0), int64(2000)}, 30, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed || allowed.Remaining != 29 || allowed.RetryAfter != 0 || allowed.ResetAfter != 2*time.Second {
		t.Fatalf("unexpected allowed result: %#v", allowed)
	}

	denied, err := parseRateLimitReply([]any{int64(0), int64(0), int64(1500), int64(3000)}, 30, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.Remaining != 0 || denied.RetryAfter != 1500*time.Millisecond || denied.ResetAfter != 3*time.Second {
		t.Fatalf("unexpected denied result: %#v", denied)
	}

	// The EVAL reply carries every integer as a string, mirroring a real Redis
	// client's decoding.
	fromStrings, err := parseRateLimitReply([]any{"1", "5", "0", "100"}, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !fromStrings.Allowed || fromStrings.Remaining != 5 {
		t.Fatalf("unexpected string-decoded result: %#v", fromStrings)
	}
}

func TestParseRateLimitReplyRejectsMalformedAndOutOfBoundsReplies(t *testing.T) {
	cases := []struct {
		name         string
		value        any
		capacity     int
		refillPeriod time.Duration
	}{
		{"not a slice", int64(1), 30, time.Minute},
		{"wrong element count", []any{int64(1), int64(2), int64(3)}, 30, time.Minute},
		{"unparsable element", []any{"nonsense", int64(0), int64(0), int64(0)}, 30, time.Minute},
		{"allowed flag out of range", []any{int64(2), int64(0), int64(0), int64(0)}, 30, time.Minute},
		{"remaining exceeds capacity", []any{int64(1), int64(31), int64(0), int64(0)}, 30, time.Minute},
		{"retry-after exceeds refill period", []any{int64(0), int64(0), int64(60001), int64(0)}, 30, time.Minute},
		{"reset-after exceeds refill period", []any{int64(1), int64(0), int64(0), int64(60001)}, 30, time.Minute},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := parseRateLimitReply(testCase.value, testCase.capacity, testCase.refillPeriod); err == nil {
				t.Fatalf("expected %q to be rejected", testCase.name)
			}
		})
	}
}

func TestLegacyRateLimitRejectsInvalidArgumentsBeforeRedis(t *testing.T) {
	client := &rateLimitCommandClient{value: int64(0)}
	store := NewStore(client, "legacy-invalid")
	ctx := t.Context()
	cases := map[string]error{
		"reserve zero cost":  second(store.ReserveRateLimit(ctx, "budget", "1", 0, 0, 1)),
		"reserve zero now":   second(store.ReserveRateLimit(ctx, "budget", "1", 0, 1, 0)),
		"reserve empty name": second(store.ReserveRateLimit(ctx, "", "1", 0, 1, 1)),
		"observe negative":   store.ObserveRateLimit(ctx, "budget", "1", -1, 1),
		"observe empty":      store.ObserveRateLimit(ctx, "budget", " ", 1, 1),
		"park negative":      store.ParkRateLimit(ctx, "budget", "1", -1),
		"park empty":         store.ParkRateLimit(ctx, "", "1", 1),
	}
	for name, err := range cases {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s error = %v, want ErrInvalid", name, err)
		}
	}
	if client.command != nil {
		t.Fatalf("invalid arguments reached Redis: %#v", client.command)
	}
}

func second(_ int, err error) error { return err }
