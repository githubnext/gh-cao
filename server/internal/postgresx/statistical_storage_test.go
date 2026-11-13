package postgresx

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestLazyStatisticalSummaries(t *testing.T) {
	store, config := nativeTestStore(t)
	now := time.Now()
	store.statistical.now = func() time.Time { return now }
	publishStatisticalFixture(t, store, "lazy-fixture", "")
	if store.statistical.snapshot != nil || !store.statistical.lastAttempt.IsZero() {
		t.Fatal("ingestion performed statistical work")
	}
	baseline := statisticalResults(t, store)
	frame := baseline["statistical-runs"].Metadata["statistical-frame"].(map[string]any)
	if frame["storage-mode"] != "lazy-summaries" || frame["source-data-revision"] != "lazy-fixture" ||
		baseline["statistical-runs"].Metadata["freshness"] != "current" {
		t.Fatalf("lazy sample has no provenance: %v", baseline["statistical-runs"].Metadata)
	}
	if len(store.statistical.snapshot.sources) != 4 {
		t.Fatal("summary cache contains linked evidence")
	}
	links, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "links", From: statisticalSnapshotPrefix + "$campaigns"},
	}, []string{"links"})
	if err != nil || len(links["links"].Rows) != 1 ||
		!reflect.DeepEqual(links["links"].Rows[0]["campaignLink"], map[string]any{
			"href": "https://example.test/campaign", "relation": "campaign",
		}) {
		t.Fatalf("cached typed context lost structured values: %+v %v", links, err)
	}
	for _, table := range statisticalTables {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+query.SQLIdentifier(table.name)+
			" WHERE namespace=$1", store.namespace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("lazy queries wrote persistent samples: %s count=%d err=%v", table.name, count, err)
		}
	}
	first := store.statistical.snapshot
	publishStatisticalFixture(t, store, "advanced-fixture", "2026-10-16T12:00:00.000Z")
	if store.statistical.snapshot != first {
		t.Fatal("ingestion rebuilt or discarded the cache")
	}
	current := statisticalResults(t, store)
	if !reflect.DeepEqual(current["statistical-runs"].Rows, baseline["statistical-runs"].Rows) ||
		current["statistical-runs"].Metadata["freshness"] != "stale" {
		t.Fatal("throttled sample mixed revisions or lost its frozen summaries")
	}
	for _, name := range []string{"statistical-tools", "statistical-audits"} {
		if current[name].Metadata["availability"] != "unavailable" || len(current[name].Rows) != 0 {
			t.Fatalf("%s claimed unavailable historical detail", name)
		}
	}
	now = now.Add(defaultStatisticalRefreshInterval - time.Nanosecond)
	statisticalResults(t, store)
	if store.statistical.snapshot != first {
		t.Fatal("revision bypassed the refresh throttle")
	}
	now = now.Add(time.Nanosecond)
	refreshed := statisticalResults(t, store)
	if store.statistical.snapshot == first || len(refreshed["statistical-runs"].Rows) != 0 ||
		len(refreshed["runs"].Rows) != 9 {
		t.Fatal("eligible demand did not replace the sample independently of exact Runs")
	}
	second := store.statistical.snapshot
	now = now.Add(defaultStatisticalRefreshInterval)
	statisticalResults(t, store)
	if store.statistical.snapshot != second {
		t.Fatal("unchanged revision was recomputed")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if reopened.statistical.snapshot != nil || len(statisticalResults(t, reopened)["statistical-runs"].Rows) != 0 {
		t.Fatal("restart reused an old independent bundle archive")
	}
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

func TestCancelledLazyStatisticalQueryPreservesSample(t *testing.T) {
	store, _ := nativeTestStore(t)
	publishStatisticalFixture(t, store, "before-cancellation", "")
	before := statisticalResults(t, store)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := store.ExecuteSQLPlan(ctx, []query.Definition{
		{Name: "sample", From: "$statisticalMembership"},
	}, []string{"sample"}); err == nil {
		t.Fatal("cancelled query succeeded")
	}
	after := statisticalResults(t, store)
	if !reflect.DeepEqual(before["statistical-runs"], after["statistical-runs"]) {
		t.Fatal("cancelled query changed the cached sample")
	}
}
