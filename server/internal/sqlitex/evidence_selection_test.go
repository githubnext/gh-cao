package sqlitex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestValueEvidenceIndexedScopeAndBudgets(t *testing.T) {
	file := "../../../dashboard/site/test/fixtures/operational-value-evidence.jsonl"
	populations := []int{1, 100, 2000}
	if retained := os.Getenv("CAO_VALUE_EVIDENCE_FILE"); retained != "" {
		file = retained
		populations = []int{1, 100, 200}
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Packet model.Row `json:"operational_value_evidence"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		t.Fatal(err)
	}
	target := envelope.Packet
	for _, population := range populations {
		t.Run(fmt.Sprint(population), func(t *testing.T) {
			packets := []model.Row{target}
			for i := 1; i < population; i++ {
				other := model.Row{}
				for key, value := range target {
					other[key] = value
				}
				other["repository"] = fmt.Sprintf("load/repo-%d", i)
				other["id"] = strings.Replace(target["id"].(string),
					model.EncodeCoordinate(strings.ToLower(target["repository"].(string))),
					model.EncodeCoordinate(other["repository"].(string)), 1)
				packets = append(packets, other)
			}
			snapshot := testutil.SQLiteSnapshot(t, map[string][]model.Row{"operationalValueEvidence": packets})
			store, err := Open(t.Context(), snapshot)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			content, err := os.ReadFile("../../../dashboard/site/src/data/queries/database.json")
			if err != nil {
				t.Fatal(err)
			}
			var definitions []query.Definition
			if err := json.Unmarshal(content, &definitions); err != nil {
				t.Fatal(err)
			}
			for _, predicate := range []query.Predicate{
				{Field: "report-id", Equals: target["id"]},
				{Field: "workflow-route", Equals: fmt.Sprint(target["repository"]) + ":" + fmt.Sprint(target["sourcePath"])},
			} {
				plan := append(append([]query.Definition{}, definitions...), query.Definition{
					Name: "selected", From: "operational-value-evidence", Filter: &query.Filter{Predicates: []query.Predicate{predicate}},
				})
				start := time.Now()
				err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
					sources, metrics, err := reader.ExecuteSQLPlan(ctx, plan, []string{"selected"})
					if err != nil {
						return err
					}
					source := sources["selected"]
					if len(source.Rows) != len(target["facts"].([]any)) {
						t.Fatal("unrelated facts materialized")
					}
					t.Logf("packets=%d selected=%d peakRows=%d peakBytes=%d operations=%d elapsed=%s",
						population, len(source.Rows), metrics.PeakWorkingRows, metrics.PeakWorkingBytes, metrics.Operations, time.Since(start))
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			var snapshotID string
			for _, fact := range target["facts"].([]any) {
				row := fact.(map[string]any)
				if row["fact-kind"] == "snapshot" {
					snapshotID = row["fact-id"].(string)
					break
				}
			}
			route := target["id"].(string) + "|" + snapshotID
			want := 1
			for _, fact := range target["facts"].([]any) {
				row := fact.(map[string]any)
				if row["fact-kind"] == "snapshot-item" && row["parent-id"] == snapshotID {
					want++
				}
			}
			plan := append(append([]query.Definition{}, definitions...), query.Definition{
				Name: "snapshot-only", From: "operational-value-evidence",
				Filter: &query.Filter{Predicates: []query.Predicate{{Field: "snapshot-route", Equals: route}}},
			})
			err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
				sources, metrics, err := reader.ExecuteSQLPlan(ctx, plan, []string{"snapshot-only"})
				if err != nil {
					return err
				}
				if len(sources["snapshot-only"].Rows) != want || metrics.PeakWorkingRows > len(target["facts"].([]any)) {
					t.Fatal("snapshot-only selection lost items or expanded unrelated packets")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if population > 32 {
				err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
					_, _, err := reader.ExecuteSQLPlan(ctx, definitions, []string{"operational-value-evidence"})
					return err
				})
				if err == nil {
					t.Fatal("unscoped population bypassed packet admission budget")
				}
				if _, err := store.State(t.Context()); err != nil {
					t.Fatal("rejected Value work prevented an ordinary core state read", err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := store.WithReadTransaction(ctx, func(context.Context, dashboardstore.Reader) error {
				t.Fatal("cancelled read reached execution")
				return nil
			}); err == nil {
				t.Fatal("cancelled query succeeded")
			}
		})
	}
}

func TestEvidenceScopeUnionAndUnscopedConsumer(t *testing.T) {
	definitions := []query.Definition{
		{Name: "one", From: "operational-value-evidence", Filter: &query.Filter{Predicates: []query.Predicate{{Field: "report-id", Equals: "one"}}}},
		{Name: "two", From: "operational-value-evidence", Filter: &query.Filter{Predicates: []query.Predicate{{Field: "report-id", Equals: "two"}}}},
	}
	if len(query.SourceEqualities(definitions, []string{"one", "two"}, "operational-value-evidence", "$operationalValueEvidence", "report-id")) != 2 {
		t.Fatal("shared scope must union")
	}
	definitions[1].Filter = nil
	if query.SourceEqualities(definitions, []string{"one", "two"}, "operational-value-evidence", "$operationalValueEvidence", "report-id") != nil {
		t.Fatal("unscoped consumer lost evidence")
	}
}
