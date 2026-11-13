package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

const syntheticRunPath = "/repos/simulator/repo-00001/actions/runs"

func TestPostgresBackfillGitHubFaults(t *testing.T) {
	backfillServices(t)
	for _, mode := range []string{
		"internal-error", "service-unavailable", "unauthorized", "malformed-response",
		"connection-failure", "timeout", "primary-rate-limit", "secondary-rate-limit",
	} {
		t.Run(mode, func(t *testing.T) {
			backfillDebugTelemetry(t, t.Context())
			scenario := simulator.Scenario{
				Name: mode, Repositories: 1,
				History:   &simulator.History{Days: 14, RunsPerDay: 15, AsOf: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)},
				RateLimit: &simulator.APIRateLimit{Limit: 5000, Window: "1h"},
			}
			if mode == "primary-rate-limit" {
				scenario.RateLimit.Window = "2s"
			}
			h := newSyntheticBackfill(t, t.Context(), scenario)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			reset := h.api.Started().Add(time.Hour).Truncate(time.Second).Add(time.Second).UTC().Format(time.RFC3339)
			retryAfter := 1
			if mode == "primary-rate-limit" {
				// Start the short reset window when the fault is served, not before cold replay.
				reset = ""
				retryAfter = 2
			}
			count := 1
			if mode == "connection-failure" {
				count = 2 // net/http can retry an idempotent request after an idle connection is lost.
			}
			if err := h.proxy.Inject(simulator.APIFault{
				Path: syntheticRunPath, Page: 2, Count: count, Mode: mode, RetryAfter: retryAfter,
				ResetAt: reset,
			}); err != nil {
				t.Fatal(err)
			}
			first, err := h.backfill.Run(ctx)
			if err != nil || first.Phase != "partial" || first.EnumerationFailures != 1 || first.QueuedRunTasks != 100 {
				t.Fatalf("fault must preserve a partial checkpoint: %+v, %v", first, err)
			}
			committed := backfillState(t, ctx, h.data)
			if !committed.Ready || committed.Counts["$runs"] != 105 {
				t.Fatalf("API fault lost the replayed seven-day projection: %+v", committed)
			}
			cursor := backfillCursor(t, ctx, h.backfill)
			if cursor.Page != 2 || cursor.Complete {
				t.Fatalf("failed page advanced the cursor: %+v", cursor)
			}
			bucket := githubquota.BucketID{App: "simulator", Installation: 1, Resource: "core"}
			state, err := h.quota.State(ctx, bucket)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "primary-rate-limit" || mode == "secondary-rate-limit" {
				want := githubquota.StatusParked
				if mode == "primary-rate-limit" {
					want = githubquota.StatusExhausted
				}
				if state.Status != want {
					t.Fatalf("quota status = %s, want %s", state.Status, want)
				}
				before := h.proxy.RequestCount(syntheticRunPath, 2)
				deferred, err := h.backfill.Run(ctx)
				if err != nil || deferred.Phase != "partial" || deferred.QueuedRunTasks != 0 {
					t.Fatalf("limited rerun should defer: %+v, %v", deferred, err)
				}
				if h.proxy.RequestCount(syntheticRunPath, 2) != before ||
					backfillCursor(t, ctx, h.backfill) != cursor {
					t.Fatal("limited rerun reached GitHub or moved the cursor")
				}
				retry := state.ResetAt
				if mode == "secondary-rate-limit" {
					retry = state.ParkedUntil
				}
				timer := time.NewTimer(time.Until(retry) + 50*time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-timer.C:
				}
			}
			// A new authenticated client simulates a process restart; only
			// Operational cursors, reservations, and durable admissions survive.
			resumed := h.backfill
			client := h.restart()
			resumed.Enumerator = client
			resumed.RunEnumerator = client
			second, err := resumed.Run(ctx)
			if err != nil || second.Phase != "collecting" || second.QueuedRunTasks != 5 {
				t.Fatalf("recovery did not resume the failed page: %+v, %v", second, err)
			}
			if !backfillCursor(t, ctx, resumed).Complete {
				t.Fatal("recovered enumeration checkpoint is not complete")
			}
			depth, err := taskQueueLength(ctx, resumed.Queue.Tasks, "collect:run-tasks")
			if err != nil || depth != 105 {
				t.Fatalf("recovery lost or duplicated historical runs: %d, %v", depth, err)
			}
			if !reflect.DeepEqual(backfillState(t, ctx, h.data), committed) {
				t.Fatal("API recovery changed an unchanged Postgres projection")
			}
		})
	}
}

type backfillCheckpoint struct {
	Page     int  `json:"page"`
	Complete bool `json:"complete"`
}

func backfillCursor(t *testing.T, ctx context.Context, backfill collect.Backfill) backfillCheckpoint {
	t.Helper()
	raw, err := backfill.Metadata.ReadAttribute(ctx, "collect:run-backfill-cursors", "1:simulator/repo-00001")
	if err != nil {
		t.Fatal(err)
	}
	var cursor backfillCheckpoint
	if err := json.Unmarshal([]byte(raw), &cursor); err != nil {
		t.Fatal(err)
	}
	return cursor
}

func TestPostgresBackfillEnumerationAndQuotaBootstrapFailures(t *testing.T) {
	backfillServices(t)
	for _, path := range []string{"/app/installations", "/installation/repositories", "/app/installations/1/access_tokens", "/rate_limit"} {
		t.Run(path, func(t *testing.T) {
			backfillDebugTelemetry(t, t.Context())
			h := newSyntheticBackfill(t, t.Context(), simulator.Scenario{
				Name: "bootstrap", Repositories: 1,
				History: &simulator.History{Days: 14, RunsPerDay: 1, AsOf: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)},
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := h.proxy.Inject(simulator.APIFault{Path: path, Count: 1, Mode: "unauthorized"}); err != nil {
				t.Fatal(err)
			}
			failed, err := h.backfill.Run(ctx)
			if path == "/app/installations" {
				if err == nil || failed.Phase != "failed" || failed.Error == "" || failed.CompletedAt == "" {
					t.Fatalf("installation discovery should fail visibly: %+v, %v", failed, err)
				}
			} else if err != nil || failed.Phase != "partial" || failed.EnumerationFailures != 1 {
				t.Fatalf("bootstrap fault should be reported as partial: %+v, %v", failed, err)
			}
			if failed.QueuedRunTasks != 0 || h.proxy.RequestCount(syntheticRunPath, 1) != 0 {
				t.Fatal("bootstrap failure admitted historical work")
			}
			recovered, err := h.backfill.Run(ctx)
			if err != nil || recovered.Phase != "collecting" || recovered.QueuedRunTasks != 7 {
				t.Fatalf("bootstrap recovery = %+v, %v", recovered, err)
			}
		})
	}
}

func TestPostgresBackfillCancellationDoesNotCompleteCursor(t *testing.T) {
	backfillServices(t)
	ctx := t.Context()
	backfillDebugTelemetry(t, ctx)
	h := newSyntheticBackfill(t, ctx, simulator.Scenario{
		Name: "cancel", Repositories: 1,
		History: &simulator.History{Days: 14, RunsPerDay: 15, AsOf: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)},
	})
	if err := h.proxy.Inject(simulator.APIFault{Path: syntheticRunPath, Page: 2, Count: 1, Mode: "timeout"}); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := h.backfill.Run(cancelled)
		done <- err
	}()
	wait, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for h.proxy.RequestCount(syntheticRunPath, 2) == 0 {
		select {
		case <-wait.Done():
			t.Fatal("backfill did not reach its cancellable second page")
		case <-tick.C:
		}
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backfill error = %v", err)
	}
	if cursor := backfillCursor(t, ctx, h.backfill); cursor.Page != 2 || cursor.Complete {
		t.Fatalf("cancellation advanced incomplete page: %+v", cursor)
	}
	checkpoint, err := h.backfill.State(ctx)
	if err != nil || checkpoint.Phase != "failed" || checkpoint.CompletedAt == "" || checkpoint.Error == "" {
		t.Fatalf("cancelled backfill did not persist its failed state: %+v, %v", checkpoint, err)
	}
	state, err := h.backfill.Run(ctx)
	if err != nil || state.QueuedRunTasks != 5 || state.Phase != "collecting" {
		t.Fatalf("cancelled backfill did not resume: %+v, %v", state, err)
	}
}

func TestPostgresBackfillMissingQuotaHeadersKeepUnknownCostReserved(t *testing.T) {
	backfillServices(t)
	ctx := t.Context()
	backfillDebugTelemetry(t, ctx)
	h := newSyntheticBackfill(t, ctx, simulator.Scenario{
		Name: "missing-quota", Repositories: 1,
		History: &simulator.History{Days: 14, RunsPerDay: 15, AsOf: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)},
	})
	if err := h.proxy.Inject(simulator.APIFault{Path: syntheticRunPath, Page: 2, Count: 1, Mode: "missing-quota"}); err != nil {
		t.Fatal(err)
	}
	state, err := h.backfill.Run(ctx)
	if err != nil || state.Phase != "collecting" || state.QueuedRunTasks != 105 {
		t.Fatalf("successful response without quota headers lost valid run identities: %+v, %v", state, err)
	}
	bucket, err := h.quota.State(ctx, githubquota.BucketID{App: "simulator", Installation: 1, Resource: "core"})
	if err != nil || bucket.Reserved != 1 {
		t.Fatalf("unknown response cost was incorrectly refunded: %+v, %v", bucket, err)
	}
}
