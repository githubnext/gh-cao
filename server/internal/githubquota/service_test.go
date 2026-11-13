package githubquota

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
)

var testNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func newTestService(t *testing.T, options Options) (*Service, *memoryStore) {
	t.Helper()
	store := newMemoryStore(testNow)
	service, err := New(store, options)
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func observe(t *testing.T, service *Service, bucket BucketID, remaining int, reset time.Time) {
	t.Helper()
	if err := service.Observe(t.Context(), bucket, Observation{Limit: 5000, Remaining: remaining, ResetAt: reset, ObservedAt: testNow}); err != nil {
		t.Fatal(err)
	}
}

func TestBucketIdentityIncludesAppInstallationAndResource(t *testing.T) {
	bucket := BucketID{App: " Collector ", Installation: 123}.Normalize()
	if bucket.App != "collector" || bucket.Resource != ResourceCore || bucket.Validate() != nil {
		t.Fatalf("unexpected normalized bucket %#v", bucket)
	}
	if bucket.String() != "collector/123/core" || bucket.storageKey() != "collector:123:core" {
		t.Fatalf("unexpected bucket rendering %q %q", bucket.String(), bucket.storageKey())
	}
	for _, invalid := range []BucketID{
		{App: "", Installation: 1, Resource: "core"},
		{App: "a:b", Installation: 1, Resource: "core"},
		{App: "app", Installation: 0, Resource: "core"},
		{App: "app", Installation: 1, Resource: "co re"},
	} {
		if invalid.Validate() == nil {
			t.Fatalf("invalid bucket %#v was accepted", invalid)
		}
	}
}

func TestReserveRequiresCostWithinTheMinimumRemain(t *testing.T) {
	service, _ := newTestService(t, Options{})
	ctx := t.Context()
	bucket := BucketID{App: "collector", Installation: 1}
	observe(t, service, bucket, 1500, testNow.Add(time.Hour))

	reservation, err := service.Reserve(ctx, bucket, ReservationRequest{EstimatedCost: 500, MinimumRemain: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if reservation.ID == "" || reservation.Amount != 500 || reservation.Bucket.Resource != ResourceCore ||
		!reservation.ExpiresAt.Equal(testNow.Add(DefaultReservationTTL)) {
		t.Fatalf("unexpected reservation %#v", reservation)
	}
	// remaining 1500 - reserved 500 - cost 1 < 1000: the floor is never crossed.
	_, err = service.Reserve(ctx, bucket, ReservationRequest{EstimatedCost: 1, MinimumRemain: 1000})
	var unavailable *UnavailableError
	if !errors.Is(err, ErrExhausted) || !errors.As(err, &unavailable) ||
		unavailable.Status != StatusExhausted || !unavailable.RetryAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("reservation crossing the floor = %v", err)
	}
	state, err := service.State(ctx, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if state.Remaining != 1500 || state.Reserved != 500 || state.Available != 1000 || state.Status != StatusAvailable {
		t.Fatalf("reservation modified the observed remaining value: %#v", state)
	}
	if err := service.Release(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if state, _ = service.State(ctx, bucket); state.Reserved != 0 {
		t.Fatalf("release left reserved capacity: %#v", state)
	}
}

func TestSafetyReserveIsAFloorForEveryRequest(t *testing.T) {
	service, _ := newTestService(t, Options{SafetyReserve: 900})
	bucket := BucketID{App: "collector", Installation: 1}
	observe(t, service, bucket, 1000, testNow.Add(time.Hour))
	if _, err := service.Reserve(t.Context(), bucket, ReservationRequest{EstimatedCost: 200}); !errors.Is(err, ErrExhausted) {
		t.Fatalf("safety reserve was crossed: %v", err)
	}
	if _, err := service.Reserve(t.Context(), bucket, ReservationRequest{EstimatedCost: 100}); err != nil {
		t.Fatal(err)
	}
	state, _ := service.State(t.Context(), bucket)
	if state.Status != StatusExhausted {
		t.Fatalf("bucket at the safety reserve reported %s", state.Status)
	}
	if selected, err := service.Select(t.Context(), []BucketID{bucket}, Requirement{}); err != nil || selected != bucket.Normalize() {
		t.Fatalf("zero-cost selection at the safety reserve = %s, %v", selected, err)
	}
	if _, err := service.Select(t.Context(), []BucketID{bucket}, Requirement{EstimatedCost: 1}); !errors.Is(err, ErrExhausted) {
		t.Fatalf("selection crossing the safety reserve = %v", err)
	}
}

func TestReservationsExpire(t *testing.T) {
	service, store := newTestService(t, Options{ReservationTTL: time.Minute})
	bucket := BucketID{App: "collector", Installation: 1}
	observe(t, service, bucket, 1000, testNow.Add(time.Hour))
	if _, err := service.Reserve(t.Context(), bucket, ReservationRequest{EstimatedCost: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(t.Context(), bucket, ReservationRequest{EstimatedCost: 1}); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second reservation = %v", err)
	}
	store.advance(time.Minute)
	if _, err := service.Reserve(t.Context(), bucket, ReservationRequest{EstimatedCost: 1}); err != nil {
		t.Fatalf("expired reservation still held capacity: %v", err)
	}
}

func TestObservationReconciliation(t *testing.T) {
	service, _ := newTestService(t, Options{})
	ctx := t.Context()
	bucket := BucketID{App: "collector", Installation: 1}
	reset := testNow.Add(time.Hour)
	observe(t, service, bucket, 3000, reset)
	observe(t, service, bucket, 3500, reset)
	if state, _ := service.State(ctx, bucket); state.Remaining != 3000 {
		t.Fatalf("same-window observation increased remaining: %#v", state)
	}
	observe(t, service, bucket, 10, reset.Add(-time.Hour))
	if state, _ := service.State(ctx, bucket); state.Remaining != 3000 {
		t.Fatalf("older-window observation was applied: %#v", state)
	}
	reservation, err := service.Reserve(ctx, bucket, ReservationRequest{EstimatedCost: 400})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(ctx, reservation, Observation{Limit: 5000, Remaining: 2700, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	if state, _ := service.State(ctx, bucket); state.Remaining != 2700 || state.Reserved != 0 {
		t.Fatalf("commit did not reconcile: %#v", state)
	}
	observe(t, service, bucket, 5000, reset.Add(time.Hour))
	if state, _ := service.State(ctx, bucket); state.Remaining != 5000 || !state.ResetAt.Equal(reset.Add(time.Hour)) {
		t.Fatalf("new reset window did not replace state: %#v", state)
	}
	for _, invalid := range []Observation{
		{Limit: 10, Remaining: 11, ResetAt: reset},
		{Limit: 10, Remaining: -1, ResetAt: reset},
		{Limit: 10, Remaining: 1},
	} {
		if err := service.Observe(ctx, bucket, invalid); err == nil {
			t.Fatalf("invalid observation %#v was accepted", invalid)
		}
	}
}

func TestStatusDistinguishesUnknownParkedExhaustedAndAvailable(t *testing.T) {
	service, store := newTestService(t, Options{})
	ctx := t.Context()
	bucket := BucketID{App: "collector", Installation: 1}
	if state, _ := service.State(ctx, bucket); state.Status != StatusUnknown {
		t.Fatalf("unobserved bucket reported %s", state.Status)
	}
	if _, err := service.Reserve(ctx, bucket, ReservationRequest{EstimatedCost: 1}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown reservation = %v", err)
	}
	observe(t, service, bucket, 0, testNow.Add(time.Hour))
	if state, _ := service.State(ctx, bucket); state.Status != StatusExhausted {
		t.Fatalf("empty bucket reported %s", state.Status)
	}
	observe(t, service, bucket, 0, testNow.Add(2*time.Hour))
	observe(t, service, bucket, 0, testNow.Add(2*time.Hour))
	observe(t, service, bucket, 100, testNow.Add(3*time.Hour))
	if state, _ := service.State(ctx, bucket); state.Status != StatusAvailable {
		t.Fatalf("bucket with quota reported %s", state.Status)
	}
	until := testNow.Add(time.Minute)
	if err := service.Park(ctx, bucket, until, "operator suspension\n"); err != nil {
		t.Fatal(err)
	}
	if err := service.Park(ctx, bucket, testNow.Add(time.Second), "shorter"); err != nil {
		t.Fatal(err)
	}
	state, _ := service.State(ctx, bucket)
	if state.Status != StatusParked || state.ParkReason != "operator suspension" || state.Remaining != 100 ||
		!state.ParkedUntil.Equal(until) {
		t.Fatalf("parking changed quota or did not extend only: %#v", state)
	}
	var unavailable *UnavailableError
	if _, err := service.Reserve(ctx, bucket, ReservationRequest{EstimatedCost: 1}); !errors.Is(err, ErrParked) ||
		!errors.As(err, &unavailable) || !unavailable.RetryAt.Equal(until) {
		t.Fatalf("parked reservation = %v", err)
	}
	if err := service.Unpark(ctx, bucket); err != nil {
		t.Fatal(err)
	}
	if state, _ := service.State(ctx, bucket); state.Status != StatusAvailable {
		t.Fatalf("unparked bucket reported %s", state.Status)
	}
	store.advance(3 * time.Hour)
	if state, _ := service.State(ctx, bucket); state.Status != StatusUnknown {
		t.Fatalf("bucket past its reset reported %s", state.Status)
	}
}

func TestSelectPrefersUsableBucketWithGreatestHeadroom(t *testing.T) {
	service, _ := newTestService(t, Options{})
	ctx := t.Context()
	reset := testNow.Add(time.Hour)
	parked := BucketID{App: "collector", Installation: 1}
	unknown := BucketID{App: "collector", Installation: 2}
	small := BucketID{App: "collector", Installation: 3}
	large := BucketID{App: "backfill", Installation: 3}
	reservedLarge := BucketID{App: "repository-memory", Installation: 3}
	observe(t, service, parked, 5000, reset)
	if err := service.Park(ctx, parked, testNow.Add(time.Minute), "retry-after"); err != nil {
		t.Fatal(err)
	}
	observe(t, service, small, 1200, reset)
	observe(t, service, large, 3000, reset)
	observe(t, service, reservedLarge, 4000, reset)
	if _, err := service.Reserve(ctx, reservedLarge, ReservationRequest{EstimatedCost: 2000}); err != nil {
		t.Fatal(err)
	}

	selected, err := service.Select(ctx, []BucketID{parked, unknown, small, large, reservedLarge, large}, Requirement{EstimatedCost: 100, MinimumRemain: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if selected != large.Normalize() {
		t.Fatalf("selected %s, want %s", selected, large.Normalize())
	}

	_, err = service.Select(ctx, []BucketID{parked, unknown, small}, Requirement{EstimatedCost: 300, MinimumRemain: 1000})
	var unavailable *UnavailableError
	if !errors.Is(err, ErrExhausted) || !errors.As(err, &unavailable) || unavailable.Bucket != small.Normalize() {
		t.Fatalf("selection without capacity = %v", err)
	}
	if _, err := service.Select(ctx, []BucketID{unknown, parked}, Requirement{}); !errors.Is(err, ErrParked) {
		t.Fatalf("selection of parked and unknown = %v", err)
	}
	if _, err := service.Select(ctx, []BucketID{unknown}, Requirement{}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("selection of unknown = %v", err)
	}
	if _, err := service.Select(ctx, nil, Requirement{}); !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("selection without candidates = %v", err)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("nil store was accepted")
	}
	if _, err := New(newMemoryStore(testNow), Options{SafetyReserve: -1}); err == nil {
		t.Fatal("negative safety reserve was accepted")
	}
	service, _ := newTestService(t, Options{})
	if _, err := service.Reserve(t.Context(), BucketID{App: "a", Installation: 1}, ReservationRequest{}); err == nil {
		t.Fatal("zero-cost reservation was accepted")
	}
	if err := service.Release(t.Context(), Reservation{Bucket: BucketID{App: "a", Installation: 1}}); err == nil {
		t.Fatal("release without an ID was accepted")
	}
}

func TestRegistryDiscoversBucketsWithoutCredentials(t *testing.T) {
	registry, err := ParseRegistry([]byte(`{"providers":[
		{"name":"collector-primary","app":"collector","installations":[123,456]},
		{"name":"collector-secondary","app":"collector-2","installations":[123],"resources":["core","graphql"]},
		{"name":"repository-memory","app":"repository-memory","installations":[123]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	all, err := registry.Buckets()
	if err != nil || len(all) != 5 {
		t.Fatalf("all buckets = %#v %v", all, err)
	}
	collectors, err := registry.BucketsFor(123, "", "collector-primary", "collector-secondary")
	if err != nil {
		t.Fatal(err)
	}
	want := []BucketID{{App: "collector", Installation: 123, Resource: "core"}, {App: "collector-2", Installation: 123, Resource: "core"}}
	if len(collectors) != len(want) || collectors[0] != want[0] || collectors[1] != want[1] {
		t.Fatalf("collector buckets = %#v", collectors)
	}
	if _, err := registry.Buckets("missing"); err == nil {
		t.Fatal("unknown provider was accepted")
	}
	provider, ok := registry.Provider("Collector-Primary")
	if !ok || provider.App != "collector" {
		t.Fatalf("provider lookup = %#v %v", provider, ok)
	}
	provider.Installations[0] = 999
	if again, _ := registry.Provider("collector-primary"); again.Installations[0] != 123 {
		t.Fatal("provider lookup exposed internal state")
	}
	for name, document := range map[string]string{
		"unknown field":    `{"providers":[{"name":"a","app":"a","installations":[1],"privateKey":"x"}]}`,
		"duplicate":        `{"providers":[{"name":"a","app":"a","installations":[1]},{"name":"a","app":"b","installations":[1]}]}`,
		"no installations": `{"providers":[{"name":"a","app":"a","installations":[]}]}`,
		"invalid app":      `{"providers":[{"name":"a","app":"a:b","installations":[1]}]}`,
		"trailing":         `{"providers":[]} {}`,
	} {
		if _, err := ParseRegistry([]byte(document)); err == nil {
			t.Fatalf("%s registry was accepted", name)
		}
	}
}

func TestParseResponse(t *testing.T) {
	header := http.Header{}
	header.Set("X-RateLimit-Limit", "5000")
	header.Set("X-RateLimit-Remaining", "4321")
	header.Set("X-RateLimit-Reset", strconv.FormatInt(testNow.Add(time.Hour).Unix(), 10))
	header.Set("X-RateLimit-Resource", "GraphQL")
	quota := ParseResponse(header, http.StatusOK, testNow)
	if !quota.HasObservation || quota.Resource != "graphql" || quota.Observation.Remaining != 4321 ||
		!quota.Observation.ResetAt.Equal(testNow.Add(time.Hour)) || !quota.ParkUntil.IsZero() {
		t.Fatalf("unexpected success quota %#v", quota)
	}

	header.Set("Retry-After", "30")
	quota = ParseResponse(header, http.StatusForbidden, testNow)
	if quota.ParkReason != ParkReasonSecondaryRateLimit || !quota.ParkUntil.Equal(testNow.Add(30*time.Second)) {
		t.Fatalf("unexpected secondary rate limit %#v", quota)
	}
	header.Set("Retry-After", testNow.Add(2*time.Minute).Format(http.TimeFormat))
	quota = ParseResponse(header, http.StatusServiceUnavailable, testNow)
	if quota.ParkReason != ParkReasonRetryAfter || !quota.ParkUntil.Equal(testNow.Add(2*time.Minute)) {
		t.Fatalf("unexpected Retry-After date %#v", quota)
	}

	header.Del("Retry-After")
	if quota = ParseResponse(header, http.StatusForbidden, testNow); !quota.ParkUntil.IsZero() {
		t.Fatalf("ambiguous 403 parked the bucket: %#v", quota)
	}
	if quota = ParseResponse(header, http.StatusTooManyRequests, testNow); !quota.ParkUntil.Equal(testNow.Add(SecondaryRateLimitBackoff)) {
		t.Fatalf("429 without Retry-After did not park: %#v", quota)
	}
	if quota = ParseResponse(http.Header{"X-Ratelimit-Remaining": {"7"}}, http.StatusOK, testNow); quota.HasObservation || quota.Resource != ResourceCore {
		t.Fatalf("partial headers produced an observation: %#v", quota)
	}
}

func TestRecordResponseObservesAndParks(t *testing.T) {
	service, _ := newTestService(t, Options{})
	header := http.Header{}
	header.Set("X-RateLimit-Limit", "5000")
	header.Set("X-RateLimit-Remaining", "4000")
	header.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	header.Set("Retry-After", "60")
	if _, err := service.RecordResponse(t.Context(), "collector", 9, header, http.StatusTooManyRequests); err != nil {
		t.Fatal(err)
	}
	state, err := service.State(t.Context(), BucketID{App: "collector", Installation: 9})
	if err != nil {
		t.Fatal(err)
	}
	if state.Remaining != 4000 || state.ParkReason != ParkReasonSecondaryRateLimit || state.ParkedUntil.IsZero() {
		t.Fatalf("response was not recorded: %#v", state)
	}
}

func TestSanitizeReasonKeepsShortPrintableText(t *testing.T) {
	reason := sanitizeReason("  abuse\tdetected\x00 " + strings.Repeat("é", 150))
	if strings.ContainsAny(reason, "\t\x00") || len(reason) > 200 || !strings.HasPrefix(reason, "abusedetected ") {
		t.Fatalf("unexpected sanitized reason %q", reason)
	}
	if !strings.HasSuffix(reason, "é") {
		t.Fatalf("truncation split a rune: %q", reason)
	}
}

func TestServiceCoordinatesReplicasThroughOperational(t *testing.T) {
	rawURL := os.Getenv("POSTGRES_URL")
	if rawURL == "" {
		t.Skip("POSTGRES_URL is not set")
	}
	namespace := "quota-service-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	replicas := make([]*Service, 2)
	for index := range replicas {
		store, err := operationalpostgres.New(t.Context(), rawURL, namespace, operationalpostgres.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.DeleteNamespace(context.Background()); _ = store.Close() })
		service, err := New(store, Options{SafetyReserve: 100})
		if err != nil {
			t.Fatal(err)
		}
		replicas[index] = service
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	primary := BucketID{App: "collector", Installation: 7, Resource: ResourceCore}
	secondary := BucketID{App: "collector-secondary", Installation: 7, Resource: ResourceCore}
	reset := time.Now().Add(time.Hour)
	if err := replicas[0].Observe(ctx, primary, Observation{Limit: 5000, Remaining: 1000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	if err := replicas[1].Observe(ctx, secondary, Observation{Limit: 5000, Remaining: 800, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	selected, err := replicas[1].Select(ctx, []BucketID{secondary, primary}, Requirement{EstimatedCost: 300, MinimumRemain: 200})
	if err != nil || selected != primary {
		t.Fatalf("selected %s, %v", selected, err)
	}
	first, err := replicas[0].Reserve(ctx, primary, ReservationRequest{EstimatedCost: 600, MinimumRemain: 200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicas[1].Reserve(ctx, primary, ReservationRequest{EstimatedCost: 300, MinimumRemain: 200}); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second replica crossed the floor: %v", err)
	}
	selected, err = replicas[1].Select(ctx, []BucketID{primary, secondary}, Requirement{EstimatedCost: 300, MinimumRemain: 200})
	if err != nil || selected != secondary {
		t.Fatalf("selected %s after reservation, %v", selected, err)
	}
	if err := replicas[1].Commit(ctx, first, Observation{Limit: 5000, Remaining: 450, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	states, err := replicas[0].States(ctx, []BucketID{primary, secondary})
	if err != nil {
		t.Fatal(err)
	}
	if states[0].Remaining != 450 || states[0].Reserved != 0 || states[0].Status != StatusAvailable ||
		states[1].Remaining != 800 || states[1].CheckedAt.IsZero() {
		t.Fatalf("unexpected replica states %#v", states)
	}
	usage, err := replicas[1].Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	peaks := map[BucketID]int{}
	for _, point := range usage.Buckets {
		peaks[point.Bucket] = max(peaks[point.Bucket], point.Used)
	}
	if len(usage.Aggregate) == 0 || peaks[primary] != 4550 || peaks[secondary] != 4200 {
		t.Fatalf("unexpected shared usage history %+v", usage)
	}
}
