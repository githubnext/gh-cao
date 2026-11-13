package postgresx

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

type bundleCapacityTrace struct{ statements []string }

func (trace *bundleCapacityTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	trace.statements = append(trace.statements, data.SQL)
	return ctx
}

func (*bundleCapacityTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (trace *bundleCapacityTrace) ranked() bool {
	for _, statement := range trace.statements {
		if strings.Contains(statement, "CREATE TEMP TABLE cao_capacity_weights") {
			return true
		}
	}
	return false
}

func assertBundleCapacityAccounting(t *testing.T, store *Store) {
	t.Helper()
	var mismatches int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM cao_ingestion_accounting a
		JOIN cao_state s ON s.namespace=a.namespace
		LEFT JOIN (SELECT collection,sum(records)::bigint AS records,sum(bytes)::bigint AS bytes
			FROM cao_run_accounting WHERE namespace=$1 GROUP BY collection) r ON r.collection=a.collection
		WHERE a.namespace=$1 AND (a.revision<>s.revision OR
			(r.collection IS NOT NULL AND (a.records<>r.records OR a.bytes<>r.bytes)))`, store.namespace).
		Scan(&mismatches); err != nil || mismatches != 0 {
		t.Fatalf("capacity accounting diverged from revision-fenced receipts: mismatches=%d err=%v", mismatches, err)
	}
	for _, source := range tableNames() {
		if !entityTables[source].partitioned {
			continue
		}
		var matches bool
		if err := store.db.QueryRowContext(t.Context(), `SELECT a.records=(SELECT count(*) FROM `+
			query.SQLIdentifier(entityTables[source].name)+` WHERE namespace=$1)
			AND a.bytes=coalesce((SELECT sum(bytes) FROM cao_run_accounting WHERE namespace=$1 AND collection=$2),0)
			AND a.bytes=coalesce((SELECT sum(`+entityBytesSQL(entityTables[source])+`) FROM `+
			query.SQLIdentifier(entityTables[source].name)+` t WHERE namespace=$1),0)
			FROM cao_ingestion_accounting a WHERE namespace=$1 AND collection=$2`, store.namespace, source).
			Scan(&matches); err != nil || !matches {
			t.Fatalf("%s capacity totals differ from retained data: matches=%v err=%v", source, matches, err)
		}
	}
}

func TestRunBundlesUnderBudgetAvoidNamespaceRanking(t *testing.T) {
	for _, count := range []int{2, 1024} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store, _ := nativeTestStore(t)
			ids := []string{"run-a"}
			for index := 1; index < count; index++ {
				ids = append(ids, fmt.Sprintf("run-%04d", index))
			}
			at := seedBundleRuns(t, store, ids...)
			trace := &bundleCapacityTrace{}
			store.config.Tracer = trace
			start := time.Now()
			state, err := store.PublishRunBundles(t.Context(), bundleUpdate(at))
			if err != nil || state.Counts["$runs"] != count {
				t.Fatalf("under-budget delta failed: %+v %v", state, err)
			}
			if trace.ranked() {
				t.Fatal("under-budget delta ranked the namespace's Run receipts")
			}
			for _, statement := range trace.statements {
				if strings.Contains(statement, "FROM cao_run_accounting WHERE namespace=$1 GROUP BY collection") {
					t.Fatal("under-budget delta aggregated every retained receipt")
				}
			}
			t.Logf("retained_runs=%d delta_elapsed=%s statements=%d namespace_rankings=0 namespace_receipt_aggregations=0",
				count, time.Since(start), len(trace.statements))
			assertBundleCapacityAccounting(t, store)
			writer, err := store.beginCanonicalWrite(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Abort(t.Context())
			if err := writer.loadAccounting(t.Context()); err != nil {
				t.Fatal(err)
			}
			writer.delta = true
			start = time.Now()
			if err := writer.capRuns(t.Context()); err != nil {
				t.Fatal(err)
			}
			fastElapsed := time.Since(start)
			db := writer.capacitySQL(false)
			db.measured = true
			start = time.Now()
			if err := capRunSubtrees(t.Context(), store.namespace, store.capacity(), db); err != nil {
				t.Fatal(err)
			}
			t.Logf("retained_runs=%d capacity_fast_elapsed=%s exact_ranking_elapsed=%s",
				count, fastElapsed, time.Since(start))
		})
	}
}

func TestRunBundlesCapacityTotalsTrackReplacementAndIndependentWriters(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	store.runCapacity = runCapacity{rows: 2, bytes: query.MaxWorkingBytes}
	trace := &bundleCapacityTrace{}
	store.config.Tracer = trace
	input := bundleUpdate(at)
	input.Runs[0].Collections = map[string]RunBundleCollection{"tools": {Coverage: "complete"}}
	if _, err := store.PublishRunBundles(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if trace.ranked() {
		t.Fatal("exact row boundary unnecessarily ranked receipts")
	}
	assertBundleCapacityAccounting(t, store)

	// A complete replacement changes both row and byte totals. A third tool
	// requires deterministic eviction of run-b, not permission to exceed limits.
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	input.Runs[0].Run["createdAt"] = at.Add(time.Minute)
	input.Runs[0].Run["startedAt"] = at.Add(time.Minute)
	input.Runs[0].Run["updatedAt"] = at.Add(2 * time.Minute)
	input.Runs[0].Collections["tools"] = RunBundleCollection{Coverage: "complete", Rows: []model.Row{
		{"id": "tool-a1", "runId": "run-a", "name": "one"},
		{"id": "tool-a2", "runId": "run-a", "name": "two"},
	}}
	state, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || state.Counts["$runs"] != 1 || state.Counts["$tools"] != 2 || !trace.ranked() {
		t.Fatalf("over-budget delta skipped exact subtree eviction: %+v %v", state, err)
	}
	assertCapacityIDs(t, store, "run-a")
	assertBundleCapacityAccounting(t, store)

	store.runCapacity.rows = 20
	if err := store.RecordAdmittedRun(t.Context(), admissionIdentity()); err != nil {
		t.Fatal(err)
	}
	trace.statements = nil
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	input.Runs[0].Run["updatedAt"] = at.Add(3 * time.Minute)
	input.Runs[0].Collections = nil
	state, err = store.PublishRunBundles(t.Context(), input)
	if err != nil || state.Counts["$runs"] != 2 || trace.ranked() {
		t.Fatalf("independent writer totals were not reconstructed before the fast path: %+v %v", state, err)
	}
	assertBundleCapacityAccounting(t, store)

	var bytes int64
	if err := store.db.QueryRowContext(t.Context(), "SELECT sum(bytes) FROM cao_run_accounting WHERE namespace=$1",
		store.namespace).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	store.runCapacity.bytes = bytes
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	trace.statements = nil
	if _, err := store.PublishRunBundles(t.Context(), input); err != nil || trace.ranked() {
		t.Fatalf("exact byte boundary unnecessarily ranked receipts: %v", err)
	}
	before, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	store.runCapacity.bytes = 1
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	if _, err := store.PublishRunBundles(t.Context(), input); err == nil || !strings.Contains(err.Error(), "cannot fit") {
		t.Fatalf("oversized newest subtree was accepted: %v", err)
	}
	after, err := store.State(t.Context())
	if err != nil || before.Revision != after.Revision {
		t.Fatalf("failed capacity write changed publication: before=%+v after=%+v err=%v", before, after, err)
	}
	assertBundleCapacityAccounting(t, store)
}
