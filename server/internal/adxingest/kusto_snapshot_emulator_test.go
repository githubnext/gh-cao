package adxingest

import (
	"encoding/json"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestKustoEmulatorScalarMappings(t *testing.T) {
	k := newKustoAcceptance(t)
	session := testSession()
	session.Run["startedAt"], session.Run["aicTotal"] = "2026-01-01T00:00:00Z", 1.25
	session.Run["inputTokens"] = json.Number("9007199254740993")
	session.Facts["$domains"] = []model.Row{{"id": "domain", "runId": "run", "domain": "synthetic.invalid",
		"decision": "blocked", "requestCount": int64(2)}}
	session.Facts["$graderObservations"] = []model.Row{{"id": "grader", "runId": "run",
		"graderId": "synthetic-review", "included": false, "value": 0.25}}
	session.Facts["$evalObservations"] = []model.Row{{"id": "eval", "runId": "run",
		"evalId": "synthetic-eval", "included": true, "evalResult": "unknown"}}
	projection, err := project(session)
	if err != nil {
		t.Fatal(err)
	}
	for table, rows := range projection.Tables {
		for range 2 {
			if err := k.client.Write(t.Context(), table, rows); err != nil {
				t.Fatal(err)
			}
		}
	}
	k.assert(t, "cao_SessionFacts() | count", [][]any{{0}})
	if err := k.client.Verify(t.Context(), projection, false); err != nil {
		t.Fatal(err)
	}
	if err := k.client.Write(t.Context(), "SessionVersions", []model.Row{projection.Manifest}); err != nil {
		t.Fatal(err)
	}
	if err := k.client.Verify(t.Context(), projection, true); err != nil {
		t.Fatal(err)
	}
	k.assert(t, "cao_SessionFacts() | project tostring(InputTokens), AicTotal, StartedAt == datetime(2026-01-01)",
		[][]any{{"9007199254740993", 1.25, true}})
	k.assert(t, "cao_ToolEvents() | project Name", [][]any{{"read"}})
	k.assert(t, "cao_FirewallEvents() | project Domain, Decision, RequestCount", [][]any{{"synthetic.invalid", "blocked", 2}})
	k.assert(t, "cao_GraderObservations() | project GraderId, Included, Value", [][]any{{"synthetic-review", false, 0.25}})
	k.assert(t, "cao_EvalObservations() | project EvalId, Included, EvalResult", [][]any{{"synthetic-eval", true, "unknown"}})
}
