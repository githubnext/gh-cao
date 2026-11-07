package postgresx_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeAcquisitionObservedRoutingActivityOracle(t *testing.T) {
	content, err := os.ReadFile("../../../dashboard/site/test/fixtures/model-routing-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Sessions []struct {
			Run     string            `json:"run"`
			Records []json.RawMessage `json:"records"`
		} `json:"observed-sessions"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil || len(fixture.Sessions) == 0 {
		t.Fatal("missing authoritative routing fixtures")
	}
	for _, sample := range fixture.Sessions {
		t.Run(sample.Run, func(t *testing.T) {
			store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
			acquisition := store.Acquisition()
			runID, err := strconv.ParseInt(sample.Run, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			identity := collectnative.Identity{Repository: "github/gh-aw", InstallationID: 7, RunID: runID, Attempt: 1}
			if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
				t.Fatal(err)
			}
			records := make([][]byte, len(sample.Records))
			for index, record := range sample.Records {
				records[index] = record
			}
			session, _, err := collectnative.ProjectSession(t.Context(), bytes.Join(records, []byte("\n")),
				"usage/aw_session.jsonl", identity, collectnative.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			evidence := acquisitionEvidence(identity)
			evidence.Run["organization"] = "github"
			evidence.Run["cao_unified_session"] = session
			evidence.Run["updated_at"] = time.Now().UTC().Format(time.RFC3339)
			expected := nativeOracleRun(t, evidence)
			publication := publishNative(t, acquisition, evidence)
			if _, err := acquisition.Activate(t.Context(), publication); err != nil {
				t.Fatal(err)
			}
			contract, err := collectnative.CanonicalContract()
			if err != nil {
				t.Fatal(err)
			}
			selected := make([]query.SelectedField, 0, len(contract.RoutingFields))
			for field := range contract.RoutingFields {
				selected = append(selected, query.SelectedField{Field: field})
			}
			results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
				{Name: "routing", From: "$runs", Select: selected},
			}, []string{"routing"})
			if err != nil || len(results["routing"].Rows) != 1 {
				t.Fatalf("canonical routing read failed: %v", err)
			}
			for field := range contract.RoutingFields {
				actual, _ := json.Marshal(results["routing"].Rows[0][field])
				want, _ := json.Marshal(expected[field])
				if !routingValuesEqual(actual, want) {
					t.Errorf("observed routing %s: got=%s want=%s", field, actual, want)
				}
			}
		})
	}
}
