package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var recoveryLog = logger.New("cao:collect:recovery")

const deliveryCursorKey = "collect:delivery-cursor"
const deliveryProgressKey = "collect:delivery-progress"

// DeliveryReplayer recovers missed events by asking GitHub to redeliver App
// webhook deliveries.
//
// This recovers missing activity events without polling every repository's
// runs. Periodic repository-list reconciliation independently repairs scope.
type DeliveryReplayer struct {
	State    operational.StateStore
	Metadata operational.CollectionMetadata
	Client   DeliveryClient
	Enabled  bool
	// Limit bounds how many deliveries one recovery pass inspects.
	Limit int
}

// DeliveryClient is the App-level delivery API used for recovery.
type DeliveryClient interface {
	ListDeliveries(ctx context.Context, cursor string, limit int) ([]githubapp.Delivery, string, error)
	Redeliver(ctx context.Context, deliveryID int64) error
}

// Result summarizes one recovery pass.
type ReplayResult struct {
	Inspected   int `json:"inspected"`
	Redelivered int `json:"redelivered"`
}

// redeliveryPlan is the pure outcome of scanning one page of deliveries
// against the previously recorded cursor: which deliveries need redelivery,
// how many were inspected before the cursor boundary was reached, and the
// newest delivery GUID the cursor should advance to next.
type redeliveryPlan struct {
	toRedeliver   []int64
	inspected     int
	newest        string
	foundBoundary bool
}

// planRedeliveries walks one page of deliveries, newest first, stopping once
// it reaches the delivery GUID recorded by a prior pass (boundary). It is a
// pure function, so the boundary stop condition and the non-2xx redelivery
// selection are testable without a fake GitHub API or Redis store.
func planRedeliveries(deliveries []githubapp.Delivery, boundary string) redeliveryPlan {
	plan := redeliveryPlan{}
	for _, delivery := range deliveries {
		if plan.newest == "" {
			plan.newest = delivery.GUID
		}
		if boundary != "" && delivery.GUID == boundary {
			plan.foundBoundary = true
			break
		}
		plan.inspected++
		if delivery.StatusCode >= 200 && delivery.StatusCode < 300 {
			continue
		}
		plan.toRedeliver = append(plan.toRedeliver, delivery.ID)
	}
	return plan
}

type deliveryProgress struct {
	Boundary string `json:"boundary"`
	Newest   string `json:"newest"`
	Cursor   string `json:"cursor"`
}

// Recover requests redelivery of failed deliveries newer than the recorded
// cursor. Redelivered events flow through ordinary admission, including
// signature verification and deduplication, so replay is safe to repeat.
func (r DeliveryReplayer) Recover(ctx context.Context) (ReplayResult, error) {
	if !r.Enabled || r.Client == nil {
		return ReplayResult{}, nil
	}
	limit := r.Limit
	if limit <= 0 {
		limit = 200
	}
	lastSeen, err := r.State.OperationalState(ctx, deliveryCursorKey)
	if err != nil {
		return ReplayResult{}, err
	}
	progress := deliveryProgress{Boundary: string(lastSeen)}
	rawProgress, err := r.State.OperationalState(ctx, deliveryProgressKey)
	if err != nil {
		return ReplayResult{}, err
	}
	if len(rawProgress) > 0 {
		if err := json.Unmarshal(rawProgress, &progress); err != nil {
			return ReplayResult{}, errors.New("stored delivery recovery progress is invalid")
		}
	}
	deliveries, next, err := r.Client.ListDeliveries(ctx, progress.Cursor, limit)
	if err != nil {
		return ReplayResult{}, err
	}
	plan := planRedeliveries(deliveries, progress.Boundary)
	if progress.Newest == "" {
		progress.Newest = plan.newest
	}
	result := ReplayResult{Inspected: plan.inspected}
	for _, deliveryID := range plan.toRedeliver {
		if err := r.Client.Redeliver(ctx, deliveryID); err != nil {
			return result, fmt.Errorf("redeliver App webhook delivery %d: %w", deliveryID, err)
		}
		result.Redelivered++
	}
	if !plan.foundBoundary && next != "" {
		if next == progress.Cursor {
			return result, errors.New("GitHub delivery recovery cursor did not advance")
		}
		progress.Cursor = next
		encoded, err := json.Marshal(progress)
		if err != nil {
			return result, err
		}
		if err := r.State.SetOperationalState(ctx, deliveryProgressKey, encoded); err != nil {
			return result, err
		}
		recoveryLog.Printf("delivery recovery inspected=%d redelivered=%d more=true", result.Inspected, result.Redelivered)
		return result, nil
	}
	if progress.Newest != "" {
		if err := r.State.SetOperationalState(ctx, deliveryCursorKey, []byte(progress.Newest)); err != nil {
			return result, err
		}
	}
	if err := r.Metadata.Clear(ctx, "state:"+deliveryProgressKey); err != nil {
		return result, err
	}
	recoveryLog.Printf("delivery recovery inspected=%d redelivered=%d", result.Inspected, result.Redelivered)
	return result, nil
}

// Status is the collection status surface.
//
// In the Actions profile the same surface reports that collection is not
// configured and never fails.
type Status struct {
	Configured       bool               `json:"configured"`
	Health           string             `json:"health"`
	Coverage         Coverage           `json:"coverage"`
	QueueDepth       int64              `json:"queueDepth"`
	PendingTasks     int64              `json:"pendingTasks"`
	OldestPending    string             `json:"oldestPendingAge,omitempty"`
	DeadLetters      int64              `json:"deadLetters"`
	Backfill         string             `json:"backfill"`
	BackfillFailures int                `json:"backfillFailures"`
	BackfillRunTasks int                `json:"backfillQueuedRunTasks"`
	LastProjected    string             `json:"lastProjected,omitempty"`
	Counters         map[string]int64   `json:"counters"`
	Load             map[string]float64 `json:"load"`
	HealthRevision   int64              `json:"healthRevision"`
	LastWebhookAt    string             `json:"lastWebhookAt,omitempty"`
	LastFailureAt    string             `json:"lastFailureAt,omitempty"`
	LastFailureCode  string             `json:"lastFailureCode,omitempty"`
	LastSuccessAt    string             `json:"lastSuccessAt,omitempty"`
	RateLimits       []Headroom         `json:"rateLimits,omitempty"`
}

// Headroom is one installation's remaining GitHub budget.
type Headroom struct {
	InstallationID int64  `json:"installationId"`
	Remaining      int    `json:"remaining"`
	ParkedUntil    string `json:"parkedUntil,omitempty"`
}

// Reporter builds the collection status snapshot. It never includes tokens,
// secrets, prompts, or payload contents.
type Reporter struct {
	Enrollment Enrollment
	Queue      Queue
	Backfill   Backfill
	Budget     *githubapp.Budget
	Metrics    operational.IngestionMetrics
	Data       *postgresx.Store
}

// Snapshot reads current collection status.
func (r Reporter) Snapshot(ctx context.Context) (Status, error) {
	status := Status{Configured: true}
	if r.Data == nil {
		return status, errors.New("collection status requires Postgres")
	}
	coverage, err := r.Enrollment.Coverage(ctx)
	if err != nil {
		return status, err
	}
	status.Coverage = coverage
	if status.QueueDepth, err = r.Queue.Depth(ctx); err != nil {
		return status, err
	}
	if status.PendingTasks, err = r.Queue.Pending(ctx); err != nil {
		return status, err
	}
	if oldest, pendingErr := r.Queue.OldestPendingAge(ctx); pendingErr != nil {
		return status, pendingErr
	} else if oldest > 0 {
		status.OldestPending = oldest.String()
	}
	if status.DeadLetters, err = r.Queue.DeadLetters(ctx); err != nil {
		return status, err
	}
	counters, events, err := r.Metrics.IngestionHealth(ctx)
	if err != nil {
		return status, err
	}
	status.Counters = counters
	status.Load, err = r.Metrics.Loads(ctx, []string{"webhook", "collection", "failure"}, time.Minute)
	if err != nil {
		return status, err
	}
	status.HealthRevision, _ = strconv.ParseInt(events["healthRevision"], 10, 64)
	status.LastWebhookAt = events["lastWebhookAt"]
	status.LastFailureAt = events["lastFailureAt"]
	status.LastFailureCode = events["lastFailureCode"]
	status.LastSuccessAt = events["lastSuccessAt"]
	status.Health = ingestionHealthState(status, events)
	state, err := r.Backfill.State(ctx)
	if err != nil {
		return status, err
	}
	status.Backfill = state.Phase
	status.BackfillFailures = state.EnumerationFailures
	status.BackfillRunTasks = state.QueuedRunTasks
	active, err := r.Data.State(ctx)
	if err != nil {
		return status, err
	}
	if active.Ready && !active.EvaluatedAt.IsZero() {
		status.LastProjected = active.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	}
	if r.Budget != nil {
		installations, err := r.Enrollment.Installations(ctx)
		if err != nil {
			return status, err
		}
		for _, installation := range installations {
			remaining, parked, err := r.Budget.Headroom(ctx, installation)
			if err != nil {
				continue
			}
			headroom := Headroom{InstallationID: installation, Remaining: remaining}
			if !parked.IsZero() {
				headroom.ParkedUntil = parked.Format(time.RFC3339Nano)
			}
			status.RateLimits = append(status.RateLimits, headroom)
		}
	}
	return status, nil
}

func ingestionHealthState(status Status, events map[string]string) string {
	failureAt, hasFailure := parseHealthTime(events["lastFailureAt"])
	successAt, hasSuccess := parseHealthTime(events["lastSuccessAt"])
	if status.DeadLetters > 0 || (hasFailure && (!hasSuccess || failureAt.After(successAt))) {
		return "degraded"
	}
	if (events["lastFailureAt"] != "" && !hasFailure) ||
		(events["lastSuccessAt"] != "" && !hasSuccess) {
		return "degraded"
	}
	if status.QueueDepth > 0 || status.PendingTasks > 0 {
		return "recovering"
	}
	return "healthy"
}

func parseHealthTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}
