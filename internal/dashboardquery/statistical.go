package dashboardquery

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const StatisticalVersion = "canonical-snapshot-bottom-k-v1"
const StatisticalFrameVersion = "canonical-snapshot-frame-v2"
const StatisticalSeed = "cao-statistical-v1"
const StatisticalPostgresCapacity = 64000
const StatisticalSQLiteCapacity = 8000
const StatisticalIndexedDBCapacity = 16000
const StatisticalMaxInputRows = 200000
const StatisticalMaxInputBytes = 256 << 20

type StatisticalOptions struct {
	Context   context.Context
	Watermark string
	Capacity  int
	Complete  bool
	Seed      string
	HotIDs    map[string]bool
	Logf      func(string, ...any)
}

var statisticalTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)
var statisticalIdentity = regexp.MustCompile(`^github:run:[^/:]+/[^:]+:[^:]+$`)

func statisticalInstant(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok || strings.HasPrefix(text, "0000-") || !statisticalTimestamp.MatchString(text) {
		return time.Time{}, false
	}
	instant, err := time.Parse(time.RFC3339Nano, text)
	return instant.Truncate(time.Millisecond), err == nil
}

// CanonicalStatisticalWatermark supplies a stable snapshot clock when published
// quality does not carry one. An empty/undated snapshot has no admissible clock.
func CanonicalStatisticalWatermark(rows []map[string]any) string {
	var latest time.Time
	for _, row := range rows {
		for _, field := range []string{"startedAt", "createdAt", "completedAt", "updatedAt"} {
			if at, valid := statisticalInstant(row[field]); valid && at.After(latest) {
				latest = at
			}
		}
	}
	if latest.IsZero() {
		return ""
	}
	return latest.UTC().Format("2006-01-02T15:04:05.000Z")
}

// StatisticalPriority uses length-prefixed UTF-8, independent of ingestion order.
func StatisticalPriority(identity, seed string) string {
	if seed == "" {
		seed = StatisticalSeed
	}

	return statisticalDigest([]string{StatisticalVersion, seed, identity})
}

func statisticalDigest(parts []string) string {
	hash := sha256.New()
	for _, part := range parts {
		_ = binary.Write(hash, binary.BigEndian, uint32(len(part)))
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// StatisticalMembership omits evidence detail from the independent join root.
func StatisticalMembership(rows []map[string]any) []map[string]any {
	result := make([]map[string]any, len(rows))
	for index, row := range rows {
		member := map[string]any{"id": row["id"], "organization": row["owner"], "repository": row["repository"], "run": String(row["githubRunId"])}
		for field, value := range row {
			if strings.HasPrefix(field, "statistical-") {
				member[field] = value
			}
		}
		result[index] = member
	}
	return result
}

func statisticalFailure(outcome string) bool {
	return slices.Contains([]string{"failure", "timed_out", "action_required", "startup_failure"}, outcome)
}

func statisticalAllocation(counts map[string]int, capacity int) map[string]int {
	total := 0
	for _, count := range counts {
		total += count
	}
	budget := min(total, capacity)
	type quota struct {
		key       string
		remainder int
	}
	quotas := []quota{}
	result := map[string]int{}
	left := budget
	for key, count := range counts {
		allocation, remainder := 0, 0
		if total > 0 {
			allocation, remainder = budget*count/total, budget*count%total
		}
		result[key] = allocation
		left -= allocation
		quotas = append(quotas, quota{key, remainder})
	}
	slices.SortFunc(quotas, func(a, b quota) int {
		if a.remainder != b.remainder {
			return b.remainder - a.remainder
		}
		return strings.Compare(a.key, b.key)
	})
	for _, quota := range quotas {
		if left <= 0 {
			break
		}
		result[quota.key]++
		left--
	}
	return result
}

type statisticalCandidate struct {
	row      map[string]any
	priority string
	variants map[[sha256.Size]byte]bool
}

func compareStatisticalCandidates(a, b statisticalCandidate) int {
	if order := strings.Compare(a.priority, b.priority); order != 0 {
		return order
	}
	return strings.Compare(a.row["id"].(string), b.row["id"].(string))
}

// The largest retained priority is the first candidate displaced by a smaller one.
type statisticalHeap []statisticalCandidate

func (h statisticalHeap) Len() int           { return len(h) }
func (h statisticalHeap) Less(i, j int) bool { return compareStatisticalCandidates(h[i], h[j]) > 0 }
func (h statisticalHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *statisticalHeap) Push(value any)    { *h = append(*h, value.(statisticalCandidate)) }
func (h *statisticalHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	(*h)[last] = statisticalCandidate{}
	*h = (*h)[:last]
	return value
}

// SampleStatisticalRuns freezes a candidate frame over the current snapshot.
// It is not a persisted reservoir and makes no claim about discarded history.
func SampleStatisticalRuns(rows []map[string]any, options StatisticalOptions) ([]map[string]any, map[string]any, error) {
	startedAt := time.Now()
	diagnostics := statisticalDiagnostics{stage: "validation"}
	selected, frame, err := sampleStatisticalRuns(rows, options, &diagnostics)
	if options.Logf != nil {
		status := "completed"
		if err != nil {
			status = "failed"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status = "cancelled"
			}
		}
		// Only fixed labels, counts, and timings belong in diagnostics.
		options.Logf("event=statistical-sample status=%s stage=%s duration_ms=%d input_count=%d input_bytes=%d population_count=%v selected_count=%d invalid_count=%v duplicate_conflicts=%v weight_valid=%v",
			status, diagnostics.stage, time.Since(startedAt).Milliseconds(), len(rows), diagnostics.inputBytes,
			frame["population-count"], len(selected), frame["invalid-count"], frame["duplicate-conflicts"], frame["weight-valid"])
	}
	return selected, frame, err
}

type statisticalDiagnostics struct {
	stage      string
	inputBytes int
}

var ErrStatisticalBudget = errors.New("statistical candidate frame exceeds input budget")

func sampleStatisticalRuns(rows []map[string]any, options StatisticalOptions, diagnostics *statisticalDiagnostics) ([]map[string]any, map[string]any, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if len(rows) > StatisticalMaxInputRows {
		return nil, nil, fmt.Errorf("statistical candidate frame exceeds input row budget: %w", ErrStatisticalBudget)
	}
	watermark, ok := statisticalInstant(options.Watermark)
	if !ok {
		return nil, nil, errors.New("statistical sampling requires an explicit canonical UTC watermark")
	}
	if options.Capacity < 0 || options.Capacity > 100000 {
		return nil, nil, errors.New("invalid statistical capacity")
	}
	if options.Seed == "" {
		options.Seed = StatisticalSeed
	}
	start := watermark.Add(-7 * 24 * time.Hour)
	identities := map[string]statisticalCandidate{}
	invalid, conflicts, incomplete, hotExcluded := 0, 0, 0, 0
	inputBytes := 0
	diagnostics.stage = "candidates"
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		content, err := json.Marshal(row)
		if err != nil {
			return nil, nil, err
		}
		inputBytes += len(content)
		diagnostics.inputBytes = inputBytes
		if inputBytes > StatisticalMaxInputBytes {
			return nil, nil, fmt.Errorf("statistical candidate frame exceeds working byte budget: %w", ErrStatisticalBudget)
		}
		id, ok := row["id"].(string)
		if !ok || !statisticalIdentity.MatchString(id) {
			invalid++
			continue
		}
		prior, exists := identities[id]
		if exists {
			if prior.variants == nil {
				previous, err := json.Marshal(prior.row)
				if err != nil {
					return nil, nil, err
				}
				prior.variants = map[[sha256.Size]byte]bool{sha256.Sum256(previous): true}
			}
			prior.variants[sha256.Sum256(content)] = true
			identities[id] = prior
		} else {
			identities[id] = statisticalCandidate{row: row}
		}
	}
	for _, candidate := range identities {
		conflicts += max(0, len(candidate.variants)-1)
	}
	groups := map[string][]statisticalCandidate{}
	quarantined := 0
	for id, candidate := range identities {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(candidate.variants) > 1 {
			quarantined++
			continue
		}
		if candidate.row["status"] != "completed" {
			incomplete++
			continue
		}
		at, ok := statisticalInstant(candidate.row["startedAt"])
		if !ok {
			at, ok = statisticalInstant(candidate.row["createdAt"])
		}
		outcome, hasOutcome := candidate.row["conclusion"].(string)
		if !ok || !hasOutcome || outcome == "" {
			invalid++
			continue
		}
		if at.Before(start) || !at.Before(watermark) {
			continue
		}
		if options.HotIDs[id] {
			hotExcluded++
			continue
		}
		if !statisticalFailure(outcome) && !slices.Contains([]string{"success", "cancelled", "skipped", "neutral", "stale"}, outcome) {
			outcome = "other"
		}
		key := at.UTC().Format("2006-01-02") + "/" + outcome
		groups[key] = append(groups[key], candidate)
	}
	counts, failures := map[string]int{}, map[string]int{}
	population := 0
	keys := []string{}
	for key, candidates := range groups {
		counts[key] = len(candidates)
		population += len(candidates)
		keys = append(keys, key)
		if statisticalFailure(strings.Split(key, "/")[1]) {
			failures[key] = len(candidates)
		}
	}
	reserve := statisticalAllocation(failures, (options.Capacity+9)/10)
	reserved := 0
	residual := map[string]int{}
	for key, count := range counts {
		reserved += reserve[key]
		residual[key] = count - reserve[key]
	}
	remainder := statisticalAllocation(residual, options.Capacity-reserved)
	valid := options.Complete && invalid == 0 && conflicts == 0
	estimable := valid
	selected := []map[string]any{}
	strata := []map[string]any{}
	populationParts := []string{}
	diagnostics.stage = "selection"
	slices.Sort(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		candidates := groups[key]
		n, k := len(candidates), reserve[key]+remainder[key]
		slices.SortFunc(candidates, func(a, b statisticalCandidate) int {
			return compareRecordStrings(a.row["id"].(string), b.row["id"].(string))
		})
		populationParts = append(populationParts, key, strconv.Itoa(n))
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			populationParts = append(populationParts, candidate.row["id"].(string))
		}
		estimable = estimable && k > 0
		retained := make(statisticalHeap, 0, k)
		if k > 0 {
			for _, candidate := range candidates {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
				candidate.priority = StatisticalPriority(candidate.row["id"].(string), options.Seed)
				candidate.variants = nil
				if len(retained) < k {
					heap.Push(&retained, candidate)
				} else if compareStatisticalCandidates(candidate, retained[0]) < 0 {
					retained[0] = candidate
					heap.Fix(&retained, 0)
				}
			}
			slices.SortFunc(retained, compareStatisticalCandidates)
		}
		for _, candidate := range retained {
			row := map[string]any{}
			for field, value := range candidate.row {
				if !strings.HasPrefix(field, "statistical-") {
					row[field] = value
				}
			}
			row["statistical-stratum"], row["statistical-priority"] = key, candidate.priority
			row["statistical-population"], row["statistical-allocation"], row["statistical-weight-valid"] = n, k, valid
			if valid {
				row["statistical-probability"], row["statistical-weight"] = float64(k)/float64(n), float64(n)/float64(k)
			}
			selected = append(selected, row)
		}
		var cutoff any
		if k > 0 {
			cutoff = retained[k-1].priority
		}
		strata = append(strata, map[string]any{"stratum": key, "population": n, "allocation": k, "cutoff": cutoff})
	}
	frame := map[string]any{
		"version": StatisticalVersion, "frame-version": StatisticalFrameVersion, "seed": options.Seed,
		"watermark-policy": "explicit",
		"watermark":        watermark.UTC().Format("2006-01-02T15:04:05.000Z"),
		"window-start":     start.UTC().Format("2006-01-02T15:04:05.000Z"),
		"population":       "canonical-snapshot-completed-runs", "capacity": options.Capacity,
		"timestamp-field": "startedAt-or-createdAt", "identity-field": "id",
		"allocation-policy":        "largest-remainder-failure-reserve-v1",
		"failure-reserve-fraction": 0.1, "failure-reserve-allocation": reserved,
		"selection-stage":      "before-consumer-filter",
		"hot-exclusion-policy": "explicit-identities-only", "hot-exclusion-default": 0,
		"input-count": len(rows), "population-count": population, "selected-count": len(selected),
		"invalid-count": invalid, "incomplete-count": incomplete, "duplicate-conflicts": conflicts,
		"quarantined-count": quarantined, "hot-excluded-count": hotExcluded, "weight-valid": valid, "full-population-estimable": estimable,
		"input-row-limit": StatisticalMaxInputRows, "input-byte-limit": StatisticalMaxInputBytes,
		"strata": strata,
	}
	frameParts := []string{StatisticalFrameVersion, StatisticalVersion, options.Seed, "frame", frame["watermark"].(string), strconv.Itoa(options.Capacity), strconv.FormatBool(valid)}
	frameParts = append(frameParts, populationParts...)
	for _, stratum := range strata {
		cutoff, _ := stratum["cutoff"].(string)
		frameParts = append(frameParts, stratum["stratum"].(string), strconv.Itoa(stratum["population"].(int)), strconv.Itoa(stratum["allocation"].(int)), cutoff)
	}
	for _, row := range selected {
		frameParts = append(frameParts, row["id"].(string), row["statistical-priority"].(string), row["statistical-stratum"].(string))
	}
	diagnostics.stage = "frame"
	hash := sha256.New()
	for _, part := range frameParts {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		_ = binary.Write(hash, binary.BigEndian, uint32(len(part)))
		_, _ = hash.Write([]byte(part))
	}
	frame["frame-id"] = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	for _, row := range selected {
		row["statistical-frame-id"] = frame["frame-id"]
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	diagnostics.stage = "complete"
	return selected, frame, nil
}
