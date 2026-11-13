package githubquota

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestObservationsRecordPeakUsageHistory(t *testing.T) {
	service, store := newTestService(t, Options{ReservationTTL: time.Hour})
	ctx := t.Context()
	primary := BucketID{App: "collector", Installation: 1}
	secondary := BucketID{App: "backfill", Installation: 1}
	reset := testNow.Add(time.Hour)

	observe(t, service, primary, 4000, reset)
	observe(t, service, primary, 3000, reset)
	observe(t, service, secondary, 1000, reset)
	if err := service.Observe(ctx, primary, Observation{
		Limit: 5000, Remaining: 4500, ResetAt: testNow.Add(-time.Minute), ObservedAt: testNow,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(ctx, primary, ReservationRequest{EstimatedCost: 100}); err != nil {
		t.Fatal(err)
	}
	store.advance(UsageInterval)
	if err := service.Observe(ctx, primary, Observation{Limit: 5000, Remaining: 3000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}

	report, err := service.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !report.GeneratedAt.Equal(testNow.Add(UsageInterval)) || report.IntervalSeconds != 900 ||
		!report.From.Equal(report.GeneratedAt.Add(-UsageRetention)) {
		t.Fatalf("unexpected report window %+v", report)
	}
	slot := testNow.Truncate(UsageInterval)
	wantBuckets := []UsagePoint{
		{Time: slot, Bucket: BucketID{App: "backfill", Installation: 1, Resource: ResourceCore}, Limit: 5000, Used: 4000, UsagePercent: 80},
		{Time: slot, Bucket: BucketID{App: "collector", Installation: 1, Resource: ResourceCore}, Limit: 5000, Used: 2000, UsagePercent: 40},
		{Time: slot.Add(UsageInterval), Bucket: BucketID{App: "collector", Installation: 1, Resource: ResourceCore}, Limit: 5000, Used: 2000, Reserved: 100, UsagePercent: 40},
	}
	if len(report.Buckets) != len(wantBuckets) {
		t.Fatalf("buckets = %+v", report.Buckets)
	}
	for index, want := range wantBuckets {
		if report.Buckets[index] != want {
			t.Fatalf("bucket point %d = %+v, want %+v", index, report.Buckets[index], want)
		}
	}
	wantAggregate := []AggregateUsagePoint{
		{Time: slot, Buckets: 2, Limit: 10000, Used: 6000, UsagePercent: 60},
		{Time: slot.Add(UsageInterval), Buckets: 1, Limit: 5000, Used: 2000, Reserved: 100, UsagePercent: 40},
	}
	if len(report.Aggregate) != len(wantAggregate) {
		t.Fatalf("aggregate = %+v", report.Aggregate)
	}
	for index, want := range wantAggregate {
		if report.Aggregate[index] != want {
			t.Fatalf("aggregate point %d = %+v, want %+v", index, report.Aggregate[index], want)
		}
	}
}

func TestUsageHistoryFailureDoesNotFailObservation(t *testing.T) {
	service, store := newTestService(t, Options{})
	store.usageErr = errors.New("history unavailable")
	bucket := BucketID{App: "collector", Installation: 1}
	observe(t, service, bucket, 4000, testNow.Add(time.Hour))
	state, err := service.State(t.Context(), bucket)
	if err != nil || state.Remaining != 4000 {
		t.Fatalf("observation was not recorded: %+v %v", state, err)
	}
	if _, err := service.Usage(t.Context()); err == nil {
		t.Fatal("usage read failure was not reported")
	}
}

func TestUsageReportExcludesExpiredSlotsAndDegenerateLimits(t *testing.T) {
	service, store := newTestService(t, Options{})
	observe(t, service, BucketID{App: "collector", Installation: 1}, 4000, testNow.Add(time.Hour))
	if err := service.Observe(t.Context(), BucketID{App: "search", Installation: 1, Resource: "search"}, Observation{
		Limit: 0, Remaining: 0, ResetAt: testNow.Add(time.Hour), ObservedAt: testNow,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := service.Usage(t.Context())
	if err != nil || len(report.Buckets) != 2 || report.Buckets[1].UsagePercent != 0 {
		t.Fatalf("unexpected report %+v %v", report, err)
	}
	store.advance(UsageRetention + UsageInterval)
	report, err = service.Usage(t.Context())
	if err != nil || len(report.Buckets) != 0 || len(report.Aggregate) != 0 {
		t.Fatalf("expired history was reported: %+v %v", report, err)
	}
}

func TestParseStorageKey(t *testing.T) {
	bucket := BucketID{App: "collector", Installation: 12, Resource: "graphql"}
	parsed, ok := parseStorageKey(bucket.storageKey())
	if !ok || parsed != bucket {
		t.Fatalf("parseStorageKey() = %+v %t", parsed, ok)
	}
	for _, key := range []string{"collector:12", "collector:x:core", "collector:0:core", "Collector:1:core", "a:1:core:extra"} {
		if _, ok := parseStorageKey(key); ok {
			t.Errorf("parseStorageKey(%q) accepted an invalid key", key)
		}
	}
}

func TestBuildUsagePointsSortsBySlotThenBucketAndCountsSkipped(t *testing.T) {
	first := BucketID{App: "collector", Installation: 1, Resource: ResourceCore}
	second := BucketID{App: "backfill", Installation: 1, Resource: ResourceCore}
	early := time.Unix(1000, 0)
	late := time.Unix(2000, 0)
	samples := []operational.GitHubQuotaUsageSample{
		{Bucket: first.storageKey(), Slot: late, Limit: 5000, Used: 1000},
		{Bucket: "not-a-valid-key", Slot: early, Limit: 1, Used: 1},
		{Bucket: second.storageKey(), Slot: early, Limit: 4000, Used: 2000},
		{Bucket: first.storageKey(), Slot: early, Limit: 5000, Used: 500},
	}

	points, skipped := buildUsagePoints(samples)

	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(points) != 3 {
		t.Fatalf("points = %+v, want 3 entries", points)
	}
	// Within the early slot, buckets sort by their string form: "backfill" < "collector".
	if !points[0].Time.Equal(early) || points[0].Bucket != second {
		t.Fatalf("points[0] = %+v, want bucket %+v at %v", points[0], second, early)
	}
	if !points[1].Time.Equal(early) || points[1].Bucket != first {
		t.Fatalf("points[1] = %+v, want bucket %+v at %v", points[1], first, early)
	}
	if !points[2].Time.Equal(late) || points[2].Bucket != first {
		t.Fatalf("points[2] = %+v, want bucket %+v at %v", points[2], first, late)
	}
	if points[1].UsagePercent != 10 {
		t.Fatalf("points[1].UsagePercent = %v, want 10", points[1].UsagePercent)
	}
}

func TestBuildUsagePointsBoundsOversizedCounters(t *testing.T) {
	bucket := BucketID{App: "collector", Installation: 1, Resource: ResourceCore}
	samples := []operational.GitHubQuotaUsageSample{
		{Bucket: bucket.storageKey(), Slot: time.Unix(1, 0), Limit: math.MaxInt64, Used: -5, Reserved: math.MaxInt64},
	}

	points, skipped := buildUsagePoints(samples)

	if skipped != 0 || len(points) != 1 {
		t.Fatalf("buildUsagePoints() = %+v, skipped=%d", points, skipped)
	}
	if points[0].Limit != math.MaxInt32 || points[0].Used != 0 || points[0].Reserved != math.MaxInt32 {
		t.Fatalf("points[0] = %+v, want counters bounded to [0, MaxInt32]", points[0])
	}
}

func TestAggregateUsagePointsSumsEachSlotAndWeightsUsagePercentByLimit(t *testing.T) {
	early := time.Unix(1000, 0)
	late := time.Unix(2000, 0)
	points := []UsagePoint{
		{Time: early, Limit: 4000, Used: 2000, Reserved: 100},
		{Time: early, Limit: 5000, Used: 500, Reserved: 0},
		{Time: late, Limit: 5000, Used: 2500},
	}

	aggregate := aggregateUsagePoints(points)

	if len(aggregate) != 2 {
		t.Fatalf("aggregate = %+v, want 2 slots", aggregate)
	}
	want := AggregateUsagePoint{Time: early, Buckets: 2, Limit: 9000, Used: 2500, Reserved: 100, UsagePercent: usagePercent(2500, 9000)}
	if aggregate[0] != want {
		t.Fatalf("aggregate[0] = %+v, want %+v", aggregate[0], want)
	}
	want = AggregateUsagePoint{Time: late, Buckets: 1, Limit: 5000, Used: 2500, UsagePercent: 50}
	if aggregate[1] != want {
		t.Fatalf("aggregate[1] = %+v, want %+v", aggregate[1], want)
	}
}

func TestAggregateUsagePointsEmptyInput(t *testing.T) {
	if aggregate := aggregateUsagePoints(nil); len(aggregate) != 0 {
		t.Fatalf("aggregateUsagePoints(nil) = %+v, want empty", aggregate)
	}
}
