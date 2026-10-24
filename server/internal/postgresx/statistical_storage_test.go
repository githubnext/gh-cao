package postgresx

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func statisticalResults(t *testing.T, store *Store) map[string]model.Source {
	t.Helper()
	content, err := os.ReadFile("../../../dashboard/site/src/data/queries/database.json")
	if err != nil {
		t.Fatal(err)
	}
	var definitions []query.Definition
	if err := json.Unmarshal(content, &definitions); err != nil {
		t.Fatal(err)
	}
	result, _, err := store.ExecuteSQLPlan(t.Context(), definitions, []string{
		"runs", "tools", "statistical-runs", "statistical-tools", "statistical-audits",
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPersistedStatisticalBundles(t *testing.T) {
	store, config := nativeTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	publishStatisticalFixture(t, store, "persisted-fixture", "")
	baseline := statisticalResults(t, store)
	frame := baseline["statistical-runs"].Metadata["statistical-frame"].(map[string]any)
	if frame["storage-mode"] != "persisted-bundles" || frame["source-data-revision"] != "persisted-fixture" ||
		baseline["statistical-runs"].Metadata["freshness"] != "current" {
		t.Fatalf("snapshot publication was not identified: %v", baseline["statistical-runs"].Metadata)
	}
	assertSnapshot := func(t *testing.T) {
		t.Helper()
		current := statisticalResults(t, store)
		for _, source := range []string{"statistical-runs", "statistical-tools", "statistical-audits"} {
			if !reflect.DeepEqual(current[source].Rows, baseline[source].Rows) {
				t.Fatalf("%s did not retain its frozen evidence", source)
			}
		}
		currentFrame := current["statistical-runs"].Metadata["statistical-frame"].(map[string]any)
		if frame["frame-id"] != currentFrame["frame-id"] {
			t.Fatal("frozen population frame changed without a complete replacement")
		}
	}

	t.Run("complete-owned-observations-and-parent-closure", func(t *testing.T) {
		for source, table := range statisticalTables {
			var expected int
			switch source {
			case "$runs":
				expected = 6
			case "$workflows", "$experiments":
				expected = 2
			case "$campaigns", "$repositories", "$graders", "$evals", "$statisticalFrame":
				expected = 1
			case "$statisticalMembership":
				expected = 6
			case "$statisticalStrata":
				continue
			default:
				expected = 2
			}
			var count int
			if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+query.SQLIdentifier(table.name)+
				" WHERE namespace=$1", store.namespace).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != expected {
				t.Fatalf("%s: retained %d records, expected %d", source, count, expected)
			}
		}
	})

	t.Run("replay-preserves-membership-and-weights", func(t *testing.T) {
		publishStatisticalFixture(t, store, "persisted-fixture", "")
		assertSnapshot(t)
	})

	t.Run("aborted-and-failed-publication-preserve-previous-snapshot", func(t *testing.T) {
		writer, err := store.BeginIngestion(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Append(t.Context(), "$runs", model.Row{"id": "broken", "repositoryId": "missing", "workflowId": "missing"}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Publish(t.Context(), "must-not-publish"); err == nil {
			t.Fatal("invalid parent relationships were published")
		}
		writer.Abort(t.Context())
		assertSnapshot(t)
		writer, err = store.BeginIngestion(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		// Readers continue to see the last complete publication while replacement is staged.
		assertSnapshot(t)
		writer.Abort(t.Context())
		assertSnapshot(t)
	})

	t.Run("bundle-budget-rejects-whole-snapshot-not-children", func(t *testing.T) {
		connection, err := pgx.ConnectConfig(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = connection.Close(t.Context()) }()
		tx, err := connection.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if _, err := tx.Exec(t.Context(), "SELECT revision FROM cao_state WHERE namespace=$1 FOR UPDATE", store.namespace); err != nil {
			t.Fatal(err)
		}
		// Highly compressible evidence must still be charged at expanded size.
		if _, err := tx.Exec(t.Context(), `UPDATE tools SET summary=repeat('x',3*1024*1024)
			WHERE namespace=$1 AND id='a'`, store.namespace); err != nil {
			t.Fatal(err)
		}
		writer := &Writer{store: store, connection: connection, tx: tx}
		// Exercise the production admission path without allocating 256 MiB in a test.
		if err := writer.persistStatisticalSnapshot(t.Context(), "rejected-bundles", 2<<20); err != nil {
			t.Fatal(err)
		}
		assertSnapshot(t)
		if _, err := tx.Exec(t.Context(), "UPDATE cao_state SET revision=revision+1 WHERE namespace=$1", store.namespace); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		result := statisticalResults(t, store)
		if result["statistical-runs"].Metadata["availability"] != "unavailable" ||
			len(result["statistical-tools"].Rows) != 0 || len(result["runs"].Rows) != 9 {
			t.Fatal("rejected bundle admission leaked partial evidence or changed exact queries")
		}
		for source, table := range statisticalTables {
			if source == "$statisticalFrame" {
				continue
			}
			var count int
			if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+query.SQLIdentifier(table.name)+
				" WHERE namespace=$1", store.namespace).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial bundle retained for %s: count=%d error=%v", source, count, err)
			}
		}
		publishStatisticalFixture(t, store, "persisted-fixture", "")
		assertSnapshot(t)
	})

	t.Run("restart-and-real-partition-retention-preserve-frozen-evidence", func(t *testing.T) {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		store, err = NewConfig(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		assertSnapshot(t)
		result := statisticalResults(t, store)
		if len(result["tools"].Rows) != 0 || len(result["statistical-tools"].Rows) != 2 {
			t.Fatal("statistical evidence still depends on retained canonical detail")
		}
		if result["statistical-runs"].Metadata["freshness"] != "stale" {
			t.Fatal("independent canonical revision did not mark the frozen snapshot stale")
		}
	})

	t.Run("namespace-isolation", func(t *testing.T) {
		other, err := NewConfig(t.Context(), config, "another-namespace")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = other.Close() }()
		if result := statisticalResults(t, other); len(result["statistical-runs"].Rows) != 0 ||
			result["statistical-runs"].Metadata["availability"] != "unavailable" {
			t.Fatal("unpublished namespace inherited another namespace's sample")
		}
		publishStatisticalFixture(t, other, "different-snapshot", "2026-10-16T12:00:00.000Z")
		assertSnapshot(t)
	})

	t.Run("new-watermark-expires-whole-bundles-with-a-new-frame", func(t *testing.T) {
		publishStatisticalFixture(t, store, "advanced-clock", "2026-10-16T12:00:00.000Z")
		result := statisticalResults(t, store)
		currentFrame := result["statistical-runs"].Metadata["statistical-frame"].(map[string]any)
		if len(result["statistical-runs"].Rows) != 0 || len(result["statistical-tools"].Rows) != 0 ||
			len(result["runs"].Rows) != 9 || currentFrame["frame-id"] == frame["frame-id"] ||
			currentFrame["population-count"] != 0 || currentFrame["selected-count"] != 0 {
			t.Fatal("advancing the window did not atomically replace frame and bundle evidence")
		}
	})
}

func TestFrozenStatisticalDefinitionsIsolateExactSources(t *testing.T) {
	definitions := []query.Definition{
		{Name: "runs", From: "$runs", Joins: []query.Join{{Source: "workflows"}}},
		{Name: "workflows", From: "$workflows"},
		{Name: "statistical-runs", From: "runs", Joins: []query.Join{{Source: "$statisticalMembership"}}},
		{Name: "exact", From: "runs"},
	}
	before, err := json.Marshal(definitions)
	if err != nil {
		t.Fatal(err)
	}
	frozen := frozenStatisticalDefinitions(definitions)
	after, err := json.Marshal(definitions)
	if err != nil || string(before) != string(after) {
		t.Fatal("rewriting a statistical branch mutated shared exact definitions")
	}
	if frozen[0].From != "$runs" || frozen[3].From != "runs" ||
		!strings.HasPrefix(frozen[2].From, "frozen:") {
		t.Fatal("statistical and exact dependencies were not isolated")
	}
}

func TestCancelledStatisticalPublicationRollsBack(t *testing.T) {
	store, _ := nativeTestStore(t)
	publishStatisticalFixture(t, store, "before-cancellation", "")
	before := statisticalResults(t, store)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := writer.persistStatisticalSnapshot(ctx, "cancelled", 256<<20); err == nil {
		t.Fatal("cancelled publication succeeded")
	}
	writer.Abort(t.Context())
	after := statisticalResults(t, store)
	if !reflect.DeepEqual(before["statistical-runs"], after["statistical-runs"]) {
		t.Fatal("cancelled publication changed the committed sample")
	}
}
