package sessionarchiveblob

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

// Reuses the actual projected events and full DuckDB query results from the
// pinned upstream runner. Protocol fixtures cannot satisfy this engine gate.
func TestKustoUsageGraderReplay(t *testing.T) {
	engine := kustoArchiveEngine(t)
	verifyUsageGraderReplay(t, func(ctx context.Context, kql string) queryTable {
		content, err := engine.Execute(ctx, "/v1/rest/query", kql)
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Tables []queryTable }
		if json.Unmarshal(content, &response) != nil || len(response.Tables) == 0 {
			t.Fatal("real Kusto returned no query table")
		}
		return response.Tables[0]
	})
}

func verifyUsageGraderReplay(t *testing.T, execute func(context.Context, string) queryTable) {
	t.Helper()
	directory := os.Getenv("CAO_SESSION_ARCHIVE_GRADER_REPLAY_DIR")
	if directory == "" {
		t.Fatal("analytical 0.2.0 acceptance requires evidence.json from the real DuckDB pinned upstream replay")
	}
	// #nosec G304 G703 -- Explicit private synthetic replay artifact, not archive storage input.
	content, err := os.ReadFile(filepath.Join(directory, "evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []struct {
		Name    string                 `json:"name"`
		Events  []sessionarchive.Event `json:"events"`
		Queries []struct {
			ID         string                           `json:"id"`
			FullQuery  json.RawMessage                  `json:"fullQuery"`
			FullResult *sessionarchive.AnalyticalResult `json:"fullResult"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(content, &scenarios); err != nil || len(scenarios) < 20 {
		t.Fatal("missing full upstream replay scenarios")
	}
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			manifest := fixture(t, 1, "usage-replay")
			manifest.Members = nil
			for _, event := range scenario.Events {
				if err := sessionarchive.ValidateEvent(event); err != nil {
					t.Fatal(err)
				}
				member := sessionarchive.Member{Namespace: event.Namespace, Repository: event.Repository, RunID: event.RunID, Attempt: event.RunAttempt, ID: event.TraceID, Version: event.ProjectionID}
				if !slices.ContainsFunc(manifest.Members, func(m sessionarchive.Member) bool { return m.ID == member.ID && m.Version == member.Version }) {
					manifest.Members = append(manifest.Members, member)
				}
			}
			manifest.Files[0].Members = manifest.Members
			manifest.Files[0].Day = scenario.Events[0].CompletionDay
			prefix, source, _ := strings.Cut(membershipKQL("Events", manifest.Files, manifest.Members), ";\n")
			source = strings.Replace(source, `external_table("Events")`, kustoEventSource(scenario.Events, manifest.Files[0]), 1)
			count := 0
			for _, replay := range scenario.Queries {
				if replay.FullResult == nil {
					continue
				}
				reviewed, err := os.ReadFile("../sessionarchive/testdata/graders/" + replay.ID + ".json")
				if err != nil {
					t.Fatal(err)
				}
				var current, recorded sessionarchive.Definition
				if json.Unmarshal(reviewed, &current) != nil || json.Unmarshal(replay.FullQuery, &recorded) != nil || !reflect.DeepEqual(current, recorded) {
					t.Fatal("replay does not bind the current reviewed definition")
				}
				plan, err := current.Plan()
				if err != nil {
					t.Fatal("reviewed scalar plan unavailable")
				}
				if err := plan.Require(sessionarchive.AnalyticalCapabilities(4096)); err != nil {
					t.Fatal("reviewed scalar plan unavailable")
				}
				count++
				kql, err := analyticalKQL(plan, source)
				if err != nil {
					t.Fatalf("%s has no KQL lowering: %v", replay.ID, err)
				}
				table := execute(t.Context(), prefix+";\n"+kql)
				if len(table.Columns) != 2 || len(table.Rows) != 1 || len(table.Rows[0]) != 2 {
					t.Fatal("real Kusto scalar query broke bounded result envelope")
				}
				var gaps int
				if json.Unmarshal(table.Rows[0][1], &gaps) != nil || gaps != replay.FullResult.Metadata.TracesWithGaps {
					t.Fatal("real Kusto lost unfiltered omission coverage")
				}
				var rows []map[string]any
				if json.Unmarshal(table.Rows[0][0], &rows) != nil {
					t.Fatal("invalid real Kusto result")
				}
				// JSON number spellings (1 vs 1.0) differ; compare native numeric values after decoding both.
				expectedContent, _ := json.Marshal(replay.FullResult.Rows)
				var expected []map[string]any
				if json.Unmarshal(expectedContent, &expected) != nil || !reflect.DeepEqual(rows, expected) {
					t.Fatalf("%s real Kusto differs from actual DuckDB/upstream replay: got %#v want %#v", replay.ID, rows, expected)
				}
			}
			if count != 14 {
				t.Fatalf("all fourteen full definitions are required for analytical acceptance, got %d", count)
			}
		})
	}
}
