package githubquota

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// UsageInterval is the width of one retained usage slot.
const UsageInterval = operational.GitHubQuotaUsageInterval

// UsageRetention is how far back usage history is retained.
const UsageRetention = operational.GitHubQuotaUsageRetention

// UsagePoint is the peak observed usage of one bucket during one slot.
type UsagePoint struct {
	// Time is the start of the usage slot.
	Time   time.Time `json:"time"`
	Bucket BucketID  `json:"bucket"`
	// Limit and Used describe the authoritative GitHub observation with the
	// highest usage ratio in the slot.
	Limit int `json:"limit"`
	Used  int `json:"used"`
	// Reserved is the highest reserved capacity recorded in the slot.
	Reserved int `json:"reserved"`
	// UsagePercent is Used / Limit as a percentage.
	UsagePercent float64 `json:"usagePercent"`
}

// AggregateUsagePoint sums the peak usage of every observed bucket in one
// slot, so UsagePercent is weighted by each bucket's limit.
type AggregateUsagePoint struct {
	Time         time.Time `json:"time"`
	Buckets      int       `json:"buckets"`
	Limit        int       `json:"limit"`
	Used         int       `json:"used"`
	Reserved     int       `json:"reserved"`
	UsagePercent float64   `json:"usagePercent"`
}

// UsageReport is the retained usage history of every observed bucket.
type UsageReport struct {
	// GeneratedAt is the shared Redis clock the window was measured from.
	GeneratedAt     time.Time             `json:"generatedAt"`
	From            time.Time             `json:"from"`
	IntervalSeconds int                   `json:"intervalSeconds"`
	Buckets         []UsagePoint          `json:"buckets"`
	Aggregate       []AggregateUsagePoint `json:"aggregate"`
}

// recordUsage merges an accepted observation into the bounded usage history.
// History is observability only, so a failure is logged rather than failing
// the observation that was already recorded atomically.
func (s *Service) recordUsage(ctx context.Context, bucket BucketID, state operational.GitHubQuotaState) {
	if !state.Known || state.ObservedAt.IsZero() || state.Remaining > state.Limit {
		return
	}
	if _, err := s.store.RecordGitHubQuotaUsage(ctx, bucket.storageKey(), state.ObservedAt,
		state.Limit, state.Limit-state.Remaining, state.Reserved); err != nil {
		quotaLog.Printf("usage history update failed bucket=%s", bucket)
	}
}

// Usage reports the retained per-bucket and aggregate usage history, ordered
// by slot and then bucket.
func (s *Service) Usage(ctx context.Context) (_ UsageReport, err error) {
	ctx, op := startOperation(ctx, "usage", BucketID{})
	defer func() { op.finish(ctx, err) }()
	samples, now, err := s.store.GitHubQuotaUsage(ctx)
	if err != nil {
		quotaLog.Printf("usage history read failed")
		return UsageReport{}, err
	}
	points, skipped := buildUsagePoints(samples)
	if skipped > 0 {
		quotaLog.Printf("usage history samples skipped count=%d", skipped)
	}
	report := UsageReport{
		GeneratedAt:     now,
		From:            now.Add(-UsageRetention),
		IntervalSeconds: int(UsageInterval / time.Second),
		Buckets:         points,
		Aggregate:       aggregateUsagePoints(points),
	}
	op.span.SetAttributes(
		attribute.Int("cao_githubquota.usage_points", len(report.Buckets)),
		attribute.Int("cao_githubquota.usage_slots", len(report.Aggregate)))
	quotaLog.Printf("usage history read points=%d slots=%d", len(report.Buckets), len(report.Aggregate))
	return report, nil
}

// buildUsagePoints decodes each sample's storage key into a BucketID and
// bounds its counters into UsagePoint, discarding samples whose bucket key
// does not decode (for example a corrupted field). It
// returns the decoded points sorted by slot and then bucket, and how many
// samples were skipped, so Usage can log that count without recomputing it.
// It is a pure function extracted from Usage so decoding, bounding, and
// ordering are independently testable without a Redis-backed Store.
func buildUsagePoints(samples []operational.GitHubQuotaUsageSample) ([]UsagePoint, int) {
	points := make([]UsagePoint, 0, len(samples))
	skipped := 0
	for _, sample := range samples {
		bucket, ok := parseStorageKey(sample.Bucket)
		if !ok {
			skipped++
			continue
		}
		point := UsagePoint{
			Time:     sample.Slot,
			Bucket:   bucket,
			Limit:    boundedInt(sample.Limit),
			Used:     boundedInt(sample.Used),
			Reserved: boundedInt(sample.Reserved),
		}
		point.UsagePercent = usagePercent(point.Used, point.Limit)
		points = append(points, point)
	}
	sort.SliceStable(points, func(i, j int) bool {
		left, right := points[i], points[j]
		if !left.Time.Equal(right.Time) {
			return left.Time.Before(right.Time)
		}
		return left.Bucket.String() < right.Bucket.String()
	})
	return points, skipped
}

// aggregateUsagePoints sums every bucket's peak usage within each slot,
// assuming points is already ordered by slot (buildUsagePoints' contract).
// It is a pure function extracted from Usage so slot aggregation is
// independently testable without decoding samples or calling Redis.
func aggregateUsagePoints(points []UsagePoint) []AggregateUsagePoint {
	aggregate := make([]AggregateUsagePoint, 0, len(points))
	for _, point := range points {
		last := len(aggregate) - 1
		if last < 0 || !aggregate[last].Time.Equal(point.Time) {
			aggregate = append(aggregate, AggregateUsagePoint{Time: point.Time})
			last++
		}
		slot := &aggregate[last]
		slot.Buckets++
		slot.Limit = saturatingAdd(slot.Limit, point.Limit)
		slot.Used = saturatingAdd(slot.Used, point.Used)
		slot.Reserved = saturatingAdd(slot.Reserved, point.Reserved)
	}
	for index := range aggregate {
		slot := &aggregate[index]
		slot.UsagePercent = usagePercent(slot.Used, slot.Limit)
	}
	return aggregate
}

// parseStorageKey reverses BucketID.storageKey for persisted usage fields.
func parseStorageKey(key string) (BucketID, bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return BucketID{}, false
	}
	installation, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return BucketID{}, false
	}
	bucket := BucketID{App: parts[0], Installation: installation, Resource: parts[2]}
	if bucket.Validate() != nil {
		return BucketID{}, false
	}
	return bucket, true
}

func usagePercent(used, limit int) float64 {
	if limit <= 0 {
		return 0
	}
	return math.Round(float64(used)/float64(limit)*10000) / 100
}

func saturatingAdd(left, right int) int {
	if right > math.MaxInt32-left {
		return math.MaxInt32
	}
	return left + right
}
