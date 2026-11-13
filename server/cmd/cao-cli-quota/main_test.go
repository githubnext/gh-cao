package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func testBridge(t *testing.T, remaining int) (*bridge, *snapshot) {
	t.Helper()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	store, err := memory.New(memory.Config{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := githubquota.New(store, githubquota.Options{})
	if err != nil {
		t.Fatal(err)
	}
	current := &snapshot{identity: "gh-test", observations: map[string]githubquota.Observation{}}
	for _, resource := range []string{"core", "graphql"} {
		current.observations[resource] = githubquota.Observation{
			Limit: 5000, Remaining: remaining, ResetAt: now.Add(time.Hour),
		}
	}
	b := newBridge(service, func(context.Context, []string) (snapshot, error) { return *current, nil })
	b.now = func() time.Time { return now }
	return b, current
}

func TestAdmissionUsesSharedReservationsAndFreshObservations(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	first := b.handle(ctx, request{ID: 1, Operation: "admit"})
	if first.Capacity != 4 || first.ReservationID == "" {
		t.Fatalf("first admission: %+v", first)
	}
	for _, resource := range []string{"core", "graphql"} {
		state, err := b.service.State(ctx, bucket(current.identity, resource))
		if err != nil || state.Reserved != 50 {
			t.Fatalf("reservation not in shared service: %+v, %v", state, err)
		}
	}
	// A subprocess consumes a different amount than its estimate.
	m := current.observations["core"]
	m.Remaining = 175
	current.observations["core"] = m
	finished := b.handle(ctx, request{Operation: "complete", ReservationID: first.ReservationID})
	if finished.Capacity != 1 {
		t.Fatalf("low quota did not lower concurrency: %+v", finished)
	}
	state, err := b.service.State(ctx, bucket(current.identity, "core"))
	if err != nil || state.Reserved != 0 || state.Remaining != 175 {
		t.Fatalf("completion invented consumption: %+v, %v", state, err)
	}
	second := b.handle(ctx, request{Operation: "admit"})
	if second.ReservationID == "" || second.Capacity != 1 {
		t.Fatalf("low quota admission: %+v", second)
	}
	third := b.handle(ctx, request{Operation: "admit"})
	if third.Capacity != 0 || third.Reason != "exhausted" || third.ReservationID != "" {
		t.Fatalf("exhausted quota admitted work: %+v", third)
	}
}

func TestUnknownGraphQLAndFailedRefreshPause(t *testing.T) {
	b, current := testBridge(t, 5000)
	first := b.handle(context.Background(), request{Operation: "admit"})
	if first.ReservationID == "" {
		t.Fatal("expected reservation")
	}
	b.read = func(context.Context, []string) (snapshot, error) { return snapshot{}, errors.New("unavailable") }
	unknown := b.handle(context.Background(), request{Operation: "complete", ReservationID: first.ReservationID})
	if unknown.Capacity != 0 || unknown.Reason != "unknown" {
		t.Fatalf("failed refresh should pause: %+v", unknown)
	}
	state, err := b.service.State(context.Background(), bucket(current.identity, "core"))
	if err != nil || state.Reserved != 50 {
		t.Fatalf("failed refresh released reservation: %+v, %v", state, err)
	}
	b.read = func(context.Context, []string) (snapshot, error) { return *current, nil }
	delete(current.observations, "graphql")
	unknown = b.handle(context.Background(), request{Operation: "admit"})
	if unknown.Capacity != 0 || unknown.Reason != "unknown" {
		t.Fatalf("missing graphql should pause: %+v", unknown)
	}
}

func TestConcurrencyBoundAndChangedIdentity(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	first := b.handle(ctx, request{Operation: "admit", MaxConcurrency: 1})
	if first.ReservationID == "" {
		t.Fatal("expected initial reservation")
	}
	blocked := b.handle(ctx, request{Operation: "admit", MaxConcurrency: 1})
	if blocked.ReservationID != "" || blocked.Reason != "concurrency" {
		t.Fatalf("concurrency exceeded: %+v", blocked)
	}
	current.identity = "gh-other-user"
	completed := b.handle(ctx, request{Operation: "complete", ReservationID: first.ReservationID})
	if completed.Capacity != 4 {
		t.Fatalf("new credential context unavailable: %+v", completed)
	}
	previous, err := b.service.State(ctx, bucket("gh-test", "core"))
	if err != nil || previous.Reserved != 50 {
		t.Fatalf("other user's observation reconciled old identity: %+v, %v", previous, err)
	}
}

func TestExternalRunningCapacityDoesNotAcquireReservations(t *testing.T) {
	b, current := testBridge(t, 175)
	ctx := context.Background()
	result := b.handle(ctx, request{Operation: "admit", Running: 1})
	if result.ReservationID != "" || result.Capacity != 1 || result.Reason != "concurrency" {
		t.Fatalf("reserved while local workers occupy capacity: %+v", result)
	}
	state, err := b.service.State(ctx, bucket(current.identity, "core"))
	if err != nil || state.Reserved != 0 {
		t.Fatalf("external running count leaked reservation: %+v, %v", state, err)
	}
}

func TestCoreOnlyDispatchIgnoresExhaustedGraphQL(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	graphql := current.observations["graphql"]
	graphql.Remaining = 0
	current.observations["graphql"] = graphql
	mixed := b.handle(ctx, request{Operation: "admit"})
	if mixed.Capacity != 0 || mixed.Reason != "exhausted" {
		t.Fatalf("mixed-resource command ignored exhausted GraphQL: %+v", mixed)
	}
	dispatch := b.handle(ctx, request{Operation: "admit", Resources: []string{"core"}})
	if dispatch.Capacity != 4 || dispatch.ReservationID == "" {
		t.Fatalf("GraphQL exhaustion blocked core-only dispatch: %+v", dispatch)
	}
	if len(b.reservations[dispatch.ReservationID].buckets) != 1 {
		t.Fatal("core-only dispatch reserved unrelated resources")
	}
	_ = b.handle(ctx, request{Operation: "complete", ReservationID: dispatch.ReservationID})
	core := current.observations["core"]
	core.Remaining = 0
	current.observations["core"] = core
	blocked := b.handle(ctx, request{Operation: "admit", Resources: []string{"core"}})
	if blocked.Capacity != 0 || blocked.Reason != "exhausted" {
		t.Fatalf("core exhaustion admitted dispatch: %+v", blocked)
	}
}

func TestResourcesValidateAndOnlyRequiredObservationsAreUsed(t *testing.T) {
	b, current := testBridge(t, 5000)
	delete(current.observations, "graphql")
	ctx := context.Background()
	dispatch := b.handle(ctx, request{Operation: "admit", Resources: []string{"core"}})
	if dispatch.ReservationID == "" {
		t.Fatalf("missing unrelated resource blocked core: %+v", dispatch)
	}
	completed := b.handle(ctx, request{Operation: "complete", ReservationID: dispatch.ReservationID})
	if completed.Reason != "available" || len(b.reservations) != 0 {
		t.Fatalf("core-only completion required GraphQL: %+v", completed)
	}
	if mixed := b.handle(ctx, request{Operation: "admit"}); mixed.Reason != "unknown" {
		t.Fatalf("default mixed command accepted missing GraphQL: %+v", mixed)
	}
	for _, resources := range [][]string{{"core", "core"}, {"search"}, {"graphql", "invalid"}} {
		if got := b.handle(ctx, request{Operation: "admit", Resources: resources}); got.Reason != "unknown" {
			t.Fatalf("invalid resource set admitted: %v %+v", resources, got)
		}
	}
}

func TestUnclaimedReservationReleaseDoesNotRefreshOrInventConsumption(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	result := b.handle(ctx, request{Operation: "admit"})
	b.read = func(context.Context, []string) (snapshot, error) {
		t.Fatal("release refreshed GitHub for work never started")
		return snapshot{}, nil
	}
	_ = b.handle(ctx, request{Operation: "release", ReservationID: result.ReservationID})
	state, err := b.service.State(ctx, bucket(current.identity, "core"))
	if err != nil || state.Reserved != 0 || state.Remaining != 5000 || len(b.reservations) != 0 {
		t.Fatalf("unclaimed reservation release incorrect: %+v, %v", state, err)
	}
}

func TestSecondaryLimitParksBothResources(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	first := b.handle(ctx, request{Operation: "admit"})
	parked := b.handle(ctx, request{
		Operation: "complete", ReservationID: first.ReservationID, RateLimited: true, RetryAfterSeconds: 120,
	})
	if parked.Capacity != 0 || parked.Reason != "parked" || parked.RetryAt == "" {
		t.Fatalf("secondary limit not parked: %+v", parked)
	}
	for _, resource := range []string{"core", "graphql"} {
		state, err := b.service.State(ctx, bucket(current.identity, resource))
		if err != nil || state.Status != githubquota.StatusParked {
			t.Fatalf("shared meter not parked: %+v, %v", state, err)
		}
	}
	if got := b.handle(ctx, request{Operation: "admit"}); got.ReservationID != "" {
		t.Fatalf("parked bridge admitted: %+v", got)
	}
}

func TestRefreshOnEveryStartAndResetRecovery(t *testing.T) {
	b, current := testBridge(t, 100)
	calls := 0
	b.read = func(context.Context, []string) (snapshot, error) {
		calls++
		return *current, nil
	}
	ctx := context.Background()
	if got := b.handle(ctx, request{Operation: "admit"}); got.Reason != "exhausted" {
		t.Fatalf("expected exhaustion: %+v", got)
	}
	for resource, observation := range current.observations {
		observation.ResetAt = observation.ResetAt.Add(time.Hour)
		observation.Remaining = 5000
		current.observations[resource] = observation
	}
	if got := b.handle(ctx, request{Operation: "admit"}); got.Capacity != 4 || got.ReservationID == "" {
		t.Fatalf("new reset window not admitted: %+v", got)
	}
	if calls != 2 {
		t.Fatalf("starts did not refresh: %d", calls)
	}
}

func TestGhSnapshotUsesActualUserHostAndBothMeters(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "test-gh" || strings.Join(args[:3], " ") != "api --hostname example.test" {
			t.Fatalf("wrong credential context: %s %v", name, args)
		}
		if args[3] == "user" {
			return []byte("123\n"), nil
		}
		return []byte(`{"resources":{"core":{"limit":5000,"remaining":4900,"reset":1792070000},"graphql":{"limit":5000,"remaining":4800,"reset":1792070000}}}`), nil
	}
	first, err := ghSnapshot(context.Background(), run, "test-gh", "example.test")
	if err != nil || len(first.observations) != 2 || !strings.HasPrefix(first.identity, "gh-") {
		t.Fatalf("snapshot: %+v, %v", first, err)
	}
	second, err := ghSnapshot(context.Background(), run, "test-gh", "example.test")
	if err != nil || first.identity != second.identity || strings.Contains(first.identity, "123") {
		t.Fatalf("identity is unstable or exposes user: %s %s", first.identity, second.identity)
	}
}

func TestGhSnapshotRejectsMissingAndInvalidMeters(t *testing.T) {
	for _, data := range []string{
		`{}`,
		`{"resources":{"core":{"limit":5000,"remaining":10,"reset":1}}}`,
		`{"resources":{"core":{"limit":5000,"remaining":10,"reset":1},"graphql":{"limit":5000,"reset":1}}}`,
		`{"resources":{"core":{"limit":5000,"remaining":10,"reset":1},"graphql":{"limit":0,"remaining":0,"reset":1}}}`,
	} {
		run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[3] == "user" {
				return []byte("123"), nil
			}
			return []byte(data), nil
		}
		if _, err := ghSnapshot(context.Background(), run, "gh", "github.com"); err == nil {
			t.Fatalf("accepted incomplete quota: %s", data)
		}
	}
}

func TestGhSnapshotOnlyValidatesRequiredResources(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[3] == "user" {
			return []byte("123"), nil
		}
		return []byte(`{"resources":{"core":{"limit":5000,"remaining":4900,"reset":1792070000}}}`), nil
	}
	current, err := ghSnapshot(context.Background(), run, "gh", "github.com", "core")
	if err != nil || len(current.observations) != 1 {
		t.Fatalf("core-only snapshot required unrelated resource: %+v, %v", current, err)
	}
	if _, err := ghSnapshot(context.Background(), run, "gh", "github.com"); err == nil {
		t.Fatal("default mixed snapshot accepted missing GraphQL")
	}
}

func TestJSONLinesPersistentAndRedacted(t *testing.T) {
	b, _ := testBridge(t, 5000)
	var out bytes.Buffer
	input := strings.NewReader("{\"id\":1,\"operation\":\"admit\"}\n{\"id\":2,\"operation\":\"status\"}\n")
	if err := serve(input, &out, b); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || strings.Contains(out.String(), "gh-test") {
		t.Fatalf("invalid or unredacted protocol: %s", out.String())
	}
	for i, line := range lines {
		var result decision
		if err := json.Unmarshal([]byte(line), &result); err != nil || result.ID != i+1 || result.Capacity != 4 {
			t.Fatalf("invalid response: %s, %v", line, err)
		}
	}
}

func TestRequestLimitsDefaultsAndOverrides(t *testing.T) {
	c, cost, floor := request{}.limits()
	if c != 4 || cost != 50 || floor != 100 {
		t.Fatalf("defaults: %d %d %d", c, cost, floor)
	}
	c, cost, floor = request{MaxConcurrency: 2, EstimatedCost: 7, MinimumRemaining: 9}.limits()
	if c != 2 || cost != 7 || floor != 9 {
		t.Fatalf("overrides: %d %d %d", c, cost, floor)
	}
	c, cost, floor = request{MaxConcurrency: -1, EstimatedCost: -1, MinimumRemaining: -1}.limits()
	if c != 4 || cost != 50 || floor != 100 {
		t.Fatalf("negative values: %d %d %d", c, cost, floor)
	}
}

func TestParkDurationClamps(t *testing.T) {
	for seconds, want := range map[int]time.Duration{
		-5: time.Minute, 0: time.Minute, 60: time.Minute, 300: 5 * time.Minute,
		86400: 24 * time.Hour, 1000000: 24 * time.Hour,
	} {
		if got := parkDuration(seconds); got != want {
			t.Errorf("parkDuration(%d) = %v, want %v", seconds, got, want)
		}
	}
}

func TestReleaseFreesReservationAndIgnoresUnknownID(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	admitted := b.handle(ctx, request{ID: 1, Operation: "admit"})
	if admitted.ReservationID == "" {
		t.Fatalf("admission: %+v", admitted)
	}
	if err := b.release(ctx, "missing"); err != nil || len(b.reservations) != 1 {
		t.Fatalf("unknown release changed state: %v, %d", err, len(b.reservations))
	}
	if err := b.release(ctx, admitted.ReservationID); err != nil {
		t.Fatal(err)
	}
	if len(b.reservations) != 0 {
		t.Fatalf("reservation retained: %d", len(b.reservations))
	}
	state, err := b.service.State(ctx, bucket(current.identity, "core"))
	if err != nil || state.Reserved != 0 {
		t.Fatalf("quota still reserved: %+v, %v", state, err)
	}
}

func TestReconcileSettlesOnlyFinishedReservations(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	first := b.handle(ctx, request{ID: 1, Operation: "admit"})
	second := b.handle(ctx, request{ID: 2, Operation: "admit"})
	if first.ReservationID == "" || second.ReservationID == "" {
		t.Fatalf("admissions: %+v %+v", first, second)
	}
	b.reservations[first.ReservationID].finished = true

	if err := b.reconcile(ctx, *current); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.reservations[first.ReservationID]; ok {
		t.Fatal("finished reservation was not settled")
	}
	if _, ok := b.reservations[second.ReservationID]; !ok {
		t.Fatal("active reservation was settled")
	}
}

func TestReconcileKeepsFinishedReservationWithoutObservation(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	admitted := b.handle(ctx, request{ID: 1, Operation: "admit"})
	b.reservations[admitted.ReservationID].finished = true
	delete(current.observations, "graphql")

	if err := b.reconcile(ctx, *current); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.reservations[admitted.ReservationID]; !ok {
		t.Fatal("reservation settled without a fresh observation")
	}
}

func TestReconcileWithNoReservationsIsNoop(t *testing.T) {
	b, current := testBridge(t, 5000)
	if err := b.reconcile(context.Background(), *current); err != nil || len(b.reservations) != 0 {
		t.Fatalf("unexpected result: %v, %d", err, len(b.reservations))
	}
}

func TestActiveCountIgnoresFinishedReservations(t *testing.T) {
	b, _ := testBridge(t, 5000)
	if got := b.activeCount(); got != 0 {
		t.Fatalf("empty bridge active = %d", got)
	}
	ctx := context.Background()
	first := b.handle(ctx, request{ID: 1, Operation: "admit"})
	b.handle(ctx, request{ID: 2, Operation: "admit"})
	if got := b.activeCount(); got != 2 {
		t.Fatalf("active = %d, want 2", got)
	}
	b.reservations[first.ReservationID].finished = true
	if got := b.activeCount(); got != 1 {
		t.Fatalf("active after finish = %d, want 1", got)
	}
}

func TestReserveAllReservesEveryResourceAndRollsBackOnFailure(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	for _, resource := range []string{"core", "graphql"} {
		if err := b.service.Observe(ctx, bucket(current.identity, resource), current.observations[resource]); err != nil {
			t.Fatal(err)
		}
	}
	held, err := b.reserveAll(ctx, current.identity, []string{"core", "graphql"}, 50, 100)
	if err != nil || len(held) != 2 {
		t.Fatalf("reserveAll = %d buckets, %v", len(held), err)
	}
	for _, r := range held {
		if err := b.service.Release(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// graphql has no observation under this identity, so its reservation fails
	// after core succeeded and core must be rolled back.
	if err := b.service.Observe(ctx, bucket("gh-other", "core"), current.observations["core"]); err != nil {
		t.Fatal(err)
	}
	if _, err := b.reserveAll(ctx, "gh-other", []string{"core", "graphql"}, 50, 100); err == nil {
		t.Fatal("expected failure for unobserved graphql bucket")
	}
	state, err := b.service.State(ctx, bucket("gh-other", "core"))
	if err != nil || state.Reserved != 0 {
		t.Fatalf("core not rolled back: %+v, %v", state, err)
	}
}

func TestParkAfterRateLimitExtendsParkAndParksHeldBuckets(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	held := b.handle(ctx, request{Operation: "admit"})
	if held.ReservationID == "" {
		t.Fatalf("setup admission failed: %+v", held)
	}
	if err := b.parkAfterRateLimit(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if want := b.now().Add(time.Minute); !b.parkedUntil.Equal(want) {
		t.Fatalf("parkedUntil = %v, want clamped minimum %v", b.parkedUntil, want)
	}
	if err := b.parkAfterRateLimit(ctx, 3600); err != nil {
		t.Fatal(err)
	}
	if err := b.parkAfterRateLimit(ctx, 60); err != nil {
		t.Fatal(err)
	}
	if want := b.now().Add(time.Hour); !b.parkedUntil.Equal(want) {
		t.Fatalf("parkedUntil shrank: %v, want %v", b.parkedUntil, want)
	}
	for _, resource := range []string{"core", "graphql"} {
		state, err := b.service.State(ctx, bucket(current.identity, resource))
		if err != nil || state.Status != githubquota.StatusParked {
			t.Fatalf("%s not parked: %+v, %v", resource, state, err)
		}
	}
}

func TestParkAfterRateLimitWithoutReservationsOnlyParksBridge(t *testing.T) {
	b, _ := testBridge(t, 5000)
	if err := b.parkAfterRateLimit(context.Background(), 120); err != nil {
		t.Fatal(err)
	}
	if want := b.now().Add(2 * time.Minute); !b.parkedUntil.Equal(want) {
		t.Fatalf("parkedUntil = %v, want %v", b.parkedUntil, want)
	}
}

func TestRefreshObservesAndRejectsUnusableSnapshots(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	got, err := b.refresh(ctx, []string{"core"})
	if err != nil || got.identity != "gh-test" {
		t.Fatalf("refresh = %+v, %v", got, err)
	}
	state, err := b.service.State(ctx, bucket("gh-test", "core"))
	if err != nil || state.Status == githubquota.StatusUnknown {
		t.Fatalf("core not observed: %+v, %v", state, err)
	}

	delete(current.observations, "graphql")
	if _, err := b.refresh(ctx, []string{"core", "graphql"}); err == nil {
		t.Fatal("missing observation accepted")
	}
	current.observations["graphql"] = githubquota.Observation{Limit: 5000, Remaining: 1, ResetAt: b.now().Add(-time.Second)}
	if _, err := b.refresh(ctx, []string{"graphql"}); err == nil {
		t.Fatal("expired observation accepted")
	}
	current.observations["graphql"] = githubquota.Observation{Limit: 0, ResetAt: b.now().Add(time.Hour)}
	if _, err := b.refresh(ctx, []string{"graphql"}); err == nil {
		t.Fatal("zero-limit observation accepted")
	}
}

func TestRefreshPropagatesReadFailure(t *testing.T) {
	b, _ := testBridge(t, 5000)
	b.read = func(context.Context, []string) (snapshot, error) { return snapshot{}, errors.New("boom") }
	if _, err := b.refresh(context.Background(), []string{"core"}); err == nil {
		t.Fatal("read failure not propagated")
	}
}

func TestSnapshotFromRateLimitIdentityAndObservations(t *testing.T) {
	data := []byte(`{"resources":{"core":{"limit":5000,"remaining":4000,"reset":4102444800},"graphql":{"limit":100,"remaining":7,"reset":4102444801}}}`)
	got, err := snapshotFromRateLimit("GitHub.com", 42, data, []string{"core", "graphql"})
	if err != nil {
		t.Fatal(err)
	}
	same, err := snapshotFromRateLimit("github.com", 42, data, []string{"core"})
	if err != nil {
		t.Fatal(err)
	}
	if got.identity != same.identity || !strings.HasPrefix(got.identity, "gh-") {
		t.Fatalf("identity must be host-case-insensitive and prefixed: %q vs %q", got.identity, same.identity)
	}
	other, err := snapshotFromRateLimit("github.com", 43, data, []string{"core"})
	if err != nil {
		t.Fatal(err)
	}
	if other.identity == got.identity {
		t.Fatal("different users must have different identities")
	}
	if len(same.observations) != 1 || got.observations["graphql"].Remaining != 7 || got.observations["core"].ResetAt.Unix() != 4102444800 {
		t.Fatalf("unexpected observations: %+v", got.observations)
	}
}

func TestSnapshotFromRateLimitRejectsBadPayloads(t *testing.T) {
	cases := map[string]string{
		"invalid json":   `{`,
		"missing meter":  `{"resources":{}}`,
		"zero limit":     `{"resources":{"core":{"limit":0,"remaining":0,"reset":1}}}`,
		"over limit":     `{"resources":{"core":{"limit":10,"remaining":11,"reset":1}}}`,
		"negative":       `{"resources":{"core":{"limit":10,"remaining":-1,"reset":1}}}`,
		"missing reset":  `{"resources":{"core":{"limit":10,"remaining":1}}}`,
		"null remaining": `{"resources":{"core":{"limit":10,"remaining":null,"reset":1}}}`,
	}
	for name, payload := range cases {
		if _, err := snapshotFromRateLimit("github.com", 1, []byte(payload), []string{"core"}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseUserID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   int64
		fails  bool
	}{
		{name: "numeric with newline", output: "12345\n", want: 12345},
		{name: "zero", output: "0", fails: true},
		{name: "negative", output: "-7", fails: true},
		{name: "string", output: `"abc"`, fails: true},
		{name: "empty", output: "", fails: true},
		{name: "fractional", output: "1.5", fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseUserID([]byte(tc.output))
			if tc.fails {
				if err == nil || err.Error() != "GitHub credential identity is invalid" {
					t.Fatalf("expected invalid identity error, got %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestAdmissionOutcome(t *testing.T) {
	cases := []struct {
		name                                string
		operation                           string
		concurrency, active, running, avail int
		wantCapacity                        int
		wantReason                          string
	}{
		{"exhausted", "admit", 4, 0, 0, 0, 0, "exhausted"},
		{"negative headroom is exhausted", "status", 4, 1, 0, -1, 0, "exhausted"},
		{"available", "admit", 4, 1, 0, 2, 3, "available"},
		{"capacity capped by concurrency", "admit", 2, 0, 0, 10, 2, "available"},
		{"active at capacity", "admit", 2, 2, 0, 5, 2, "concurrency"},
		{"external running at capacity", "admit", 3, 0, 3, 5, 3, "concurrency"},
		{"status ignores concurrency bound", "status", 2, 2, 5, 5, 2, "available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capacity, reason := admissionOutcome(tc.operation, tc.concurrency, tc.active, tc.running, tc.avail)
			if capacity != tc.wantCapacity || reason != tc.wantReason {
				t.Fatalf("got (%d, %q), want (%d, %q)", capacity, reason, tc.wantCapacity, tc.wantReason)
			}
		})
	}
}

func TestHandleLineDecodesRequestsAndRejectsMalformedInput(t *testing.T) {
	b, _ := testBridge(t, 5000)
	got := b.handleLine([]byte(`{"id":7,"operation":"status"}`))
	if got.ID != 7 || got.Reason != "available" || got.Capacity != 4 {
		t.Fatalf("valid line: %+v", got)
	}
	for _, line := range []string{``, `not json`, `{"id":"x"}`, `[1]`} {
		bad := b.handleLine([]byte(line))
		if bad.ID != 0 || bad.Reason != "unknown" || bad.RetryAt == "" || bad.Capacity != 0 {
			t.Fatalf("line %q not rejected: %+v", line, bad)
		}
	}
}

func TestServeContinuesAfterMalformedLine(t *testing.T) {
	b, _ := testBridge(t, 5000)
	var out bytes.Buffer
	input := strings.NewReader("garbage\n{\"id\":2,\"operation\":\"status\"}\n")
	if err := serve(input, &out, b); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 responses: %s", out.String())
	}
	var first, second decision
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Reason != "unknown" {
		t.Fatalf("first: %s, %v", lines[0], err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil || second.ID != 2 || second.Reason != "available" {
		t.Fatalf("second: %s, %v", lines[1], err)
	}
}

func TestHeadroomFoldsResourceBucketsIntoAdmissionRoom(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	if _, err := b.refresh(ctx, []string{"core", "graphql"}); err != nil {
		t.Fatal(err)
	}
	room := b.headroom(ctx, current.identity, []string{"core", "graphql"}, 4, 50, 100)
	if room.status != githubquota.StatusAvailable || room.available != 4 {
		t.Fatalf("plentiful quota should be capped by concurrency: %+v", room)
	}
	if want := b.now().Add(time.Hour); !room.resetAt.Equal(want) {
		t.Fatalf("resetAt = %v, want %v", room.resetAt, want)
	}

	scarce := current.observations["graphql"]
	scarce.Remaining = 260
	current.observations["graphql"] = scarce
	if _, err := b.refresh(ctx, []string{"core", "graphql"}); err != nil {
		t.Fatal(err)
	}
	if room = b.headroom(ctx, current.identity, []string{"core", "graphql"}, 4, 50, 100); room.available != 3 {
		t.Fatalf("scarcest bucket should bound room: %+v", room)
	}
	if room = b.headroom(ctx, current.identity, []string{"core"}, 4, 50, 100); room.available != 4 {
		t.Fatalf("unrequested resource must not bound room: %+v", room)
	}
}

func TestHeadroomClampsBelowFloorToZero(t *testing.T) {
	b, current := testBridge(t, 40)
	ctx := context.Background()
	if _, err := b.refresh(ctx, []string{"core"}); err != nil {
		t.Fatal(err)
	}
	room := b.headroom(ctx, current.identity, []string{"core"}, 4, 50, 100)
	if room.status != githubquota.StatusAvailable || room.available != 0 {
		t.Fatalf("quota below the floor should leave no room: %+v", room)
	}
}

func TestHeadroomReportsParkedAndUnknownBuckets(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	unobserved := b.headroom(ctx, current.identity, []string{"core"}, 4, 50, 100)
	if unobserved.status != githubquota.StatusUnknown {
		t.Fatalf("unobserved bucket should be unknown: %+v", unobserved)
	}
	if _, err := b.refresh(ctx, []string{"core", "graphql"}); err != nil {
		t.Fatal(err)
	}
	until := b.now().Add(5 * time.Minute)
	if err := b.service.Park(ctx, bucket(current.identity, "graphql"), until, githubquota.ParkReasonSecondaryRateLimit); err != nil {
		t.Fatal(err)
	}
	parked := b.headroom(ctx, current.identity, []string{"core", "graphql"}, 4, 50, 100)
	if parked.status != githubquota.StatusParked || !parked.parkedUntil.Equal(until) {
		t.Fatalf("parked bucket should report resume time: %+v", parked)
	}
}

func TestHandleReportsParkedBucketWithResumeTime(t *testing.T) {
	b, current := testBridge(t, 5000)
	ctx := context.Background()
	if _, err := b.refresh(ctx, []string{"core", "graphql"}); err != nil {
		t.Fatal(err)
	}
	until := b.now().Add(10 * time.Minute)
	if err := b.service.Park(ctx, bucket(current.identity, "core"), until, githubquota.ParkReasonSecondaryRateLimit); err != nil {
		t.Fatal(err)
	}
	got := b.handle(ctx, request{ID: 9, Operation: "status", Resources: []string{"core"}})
	if got.ID != 9 || got.Reason != "parked" || got.RetryAt != until.Format(time.RFC3339) || got.ReservationID != "" {
		t.Fatalf("parked bucket decision = %+v", got)
	}
}
