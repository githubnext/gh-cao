package operationalvalue

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func dependabotCutoffRows(at time.Time, values [3]int) []model.Row {
	rows := []model.Row{}
	for index, metric := range dependabotMetrics {
		id := "operational-value-evidence:" + url.QueryEscape(
			dependabotRepository+":"+dependabotWorkflow+":"+metric.id+":dependabot-lifecycle-v1")
		common := model.Row{
			"report-id": id, "window-end": optimizerTime(at),
			"observed-at":     optimizerTime(at.Add(time.Hour)),
			"contract-digest": metric.digest,
		}

		add := func(fact model.Row) {
			row := model.Row{}
			for key, value := range common {
				row[key] = value
			}
			for key, value := range fact {
				row[key] = value
			}
			rows = append(rows, row)
		}
		factID := "cutoff:" + at.Format(time.RFC3339)
		add(model.Row{"fact-kind": "contract", "fact-id": "contract",
			"metric-id": metric.id, "metric-name": metric.name, "metric-unit": metric.unit,
			"metric-direction": "decrease", "metric-version": "dependabot-lifecycle-v1",
			"snapshot-id": metric.id})
		add(model.Row{"fact-kind": "snapshot", "fact-id": factID, "at": optimizerTime(at),
			"value": json.Number(strconv.Itoa(values[index])), "items-available": false})
		add(model.Row{"fact-kind": "coverage", "fact-id": factID,
			"at":     optimizerTime(at.Add(-7 * 24 * time.Hour)),
			"end-at": optimizerTime(at), "coverage-status": "complete"})
	}
	return rows
}

func TestHostedDependabotRegistrationConsumesOnlyCanonicalCompleteCutoff(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	policy := filepath.Join(dir, "cao.json")
	document := `{"version":1,"gh-aw-version":"v1.0.1","control-plane":{
		"scope":{"allowed-repositories":["github/gh-aw"]},
		"activity":{"repositories":["github/gh-aw"]},
		"campaigns":{
			"dependabot":{"targets":{"github/gh-aw":{"mode":"review"}},
				"workers":{"update-planner":{"workflow":"dependabot-update-planner"}}},
			"activity":{"workflow-values":[{"repository":"github/gh-aw",
				"workflow":"dependabot-update-planner",
				"collector":"dependabot/operational-value/dependabot-update-planner.mjs",
				"api-requests":111,"acquisition":{"requirements":[
					{"source":"github","pool":"core","primitive":"github.dependabot-alert-lifecycle.v1","scope":"github/gh-aw","cache":"invocation"},
					{"source":"github","pool":"search","primitive":"github.dependabot-pull-request-lifecycle.v1","scope":"github/gh-aw","cache":"invocation"}]}}]}}}}`
	if err := os.WriteFile(policy, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "snapshot")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
		[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, dependabotAdoption)
	at = at.Add(7 * 24 * time.Hour)
	config := Config{Root: root, Repositories: []string{dependabotRepository},
		ObservedAt: at.Add(2 * time.Hour), NodeBinary: "/nonexistent-node"}
	if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, present := range []bool{false, true} {
		output := filepath.Join(dir, strconv.FormatBool(present)+".jsonl")
		calls := 0
		err := ComputeHostedSnapshot(t.Context(), config, policy, snapshot, output,
			func(_ context.Context, defs []query.Definition, requested []string) (map[string]model.Source, error) {
				calls++
				if len(requested) != 1 {
					t.Fatalf("unexpected hosted query requests: %+v", requested)
				}
				if requested[0] == "dependabot-cutoff" {
					if !present {
						return map[string]model.Source{"dependabot-cutoff": {
							Metadata: model.Metadata{"availability": "available"},
						}}, nil
					}
					return map[string]model.Source{"dependabot-cutoff": {
						Rows:     dependabotCutoffRows(at, [3]int{3, 1, 2}),
						Metadata: model.Metadata{"availability": "available"},
					}}, nil
				}
				return map[string]model.Source{requested[0]: {
					Metadata: model.Metadata{"availability": "available", "canonical-revision": int64(11)},
				}}, nil
			})
		if err != nil || calls != 2 {
			t.Fatalf("native local evaluation error=%v calls=%d", err, calls)
		}
		rows, err := ReadMeasurements(output)
		if err != nil || len(rows) != 6 {
			t.Fatalf("three Dependabot and three Optimization records expected: %d %v", len(rows), err)
		}
		var values []json.Number
		for _, row := range rows {
			if row["campaign"] != "dependabot" {
				continue
			}
			if !present {
				if row["value"] != nil || row["coverage-status"] != "missing" {
					t.Fatalf("no accepted packet must be unavailable: %+v", row)
				}
				continue
			}
			value, ok := row["value"].(json.Number)
			if !ok || row["coverage-status"] != "complete" || row["evaluator-digest"] != dependabotEvaluator {
				t.Fatalf("complete packet was not evaluated: %+v", row)
			}
			values = append(values, value)
		}
		if present && (len(values) != 3 || values[0] != "3" || values[1] != "1" || values[2] != "2") {
			t.Fatalf("frozen metric values differ from complete cutoff: %+v", values)
		}
	}
	disabled := strings.Replace(document, `"api-requests":111`, `"api-requests":111,"enabled":false`, 1)
	if err := os.WriteFile(policy, []byte(disabled), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "disabled.jsonl")
	if err := ComputeHostedSnapshot(t.Context(), config, policy, snapshot, output,
		func(_ context.Context, _ []query.Definition, requested []string) (map[string]model.Source, error) {
			if requested[0] == "dependabot-cutoff" {
				return map[string]model.Source{"dependabot-cutoff": {
					Rows:     dependabotCutoffRows(at, [3]int{3, 1, 2}),
					Metadata: model.Metadata{"availability": "available"},
				}}, nil
			}
			return map[string]model.Source{requested[0]: {
				Metadata: model.Metadata{"availability": "available", "canonical-revision": int64(11)},
			}}, nil
		}); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadMeasurements(output)
	if err != nil || len(rows) != 6 {
		t.Fatalf("disabled acquisition lost retained evaluation: %d %v", len(rows), err)
	}
	for _, row := range rows {
		if row["campaign"] == "dependabot" && (row["value"] == nil || row["coverage-status"] != "complete") {
			t.Fatalf("disabled acquisition erased accepted cutoff: %+v", row)
		}
	}
}

func TestDependabotNativeCutoffRequiresCompleteCanonicalTriplet(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, dependabotAdoption)
	at = at.Add(7 * 24 * time.Hour)
	for _, tt := range []struct {
		name        string
		mutate      func([]model.Row) []model.Row
		missing     bool
		shouldError bool
	}{
		{"complete", func(rows []model.Row) []model.Row { return rows }, false, false},
		{"empty complete", func(rows []model.Row) []model.Row {
			for _, row := range rows {
				if row["fact-kind"] == "snapshot" {
					row["value"] = json.Number("0")
				}
			}
			return rows
		}, false, false},
		{"missing metric", func(rows []model.Row) []model.Row { return rows[:6] }, true, false},
		{"missing coverage", func(rows []model.Row) []model.Row { return rows[:8] }, true, false},
		{"incomplete coverage", func(rows []model.Row) []model.Row {
			rows[2]["coverage-status"] = "partial"
			return rows
		}, false, true},
		{"malformed count", func(rows []model.Row) []model.Row {
			rows[1]["value"] = json.Number("-1")
			return rows
		}, false, true},
		{"unreviewed contract", func(rows []model.Row) []model.Row {
			rows[0]["contract-digest"] = dependabotMetrics[1].digest
			return rows
		}, false, true},
		{"high exceeds total", func(rows []model.Row) []model.Row {
			rows[4]["value"] = json.Number("9")
			return rows
		}, false, true},
		{"wrong capture", func(rows []model.Row) []model.Row {
			rows[4]["observed-at"] = optimizerTime(at.Add(2 * time.Hour))
			return rows
		}, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows := tt.mutate(dependabotCutoffRows(at, [3]int{2, 1, 1}))
			calls := 0
			source := func(_ context.Context, defs []query.Definition, requested []string) (map[string]model.Source, error) {
				calls++
				if len(defs) != 2 || defs[0].From != "$operationalValueEvidence" ||
					defs[1].From != "operational-value-evidence" ||
					defs[1].Filter.Predicates[0].Equals !=
						dependabotRepository+":.github/workflows/"+dependabotWorkflow+".md" ||
					len(requested) != 1 || requested[0] != "dependabot-cutoff" {
					t.Fatalf("Dependabot did not use an exact canonical query: %+v", defs)
				}
				return map[string]model.Source{"dependabot-cutoff": {
					Rows: rows, Metadata: model.Metadata{"availability": "available"},
				}}, nil
			}
			var output bytes.Buffer
			err := writeDependabotValue(t.Context(), &output, at.Add(2*time.Hour), true, source)
			if (err != nil) != tt.shouldError {
				t.Fatalf("cutoff error=%v wantError=%v", err, tt.shouldError)
			}
			if tt.shouldError {
				return
			}
			if calls != 1 {
				t.Fatalf("query calls=%d, want one", calls)
			}
			measurements := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
			if len(measurements) != 3 {
				t.Fatalf("metric count=%d, want three", len(measurements))
			}
			for i, line := range measurements {
				var envelope measurementEnvelope
				decoder := json.NewDecoder(bytes.NewReader(line))
				decoder.UseNumber()
				if err := decoder.Decode(&envelope); err != nil {
					t.Fatal(err)
				}
				row, err := canonicalMeasurement(envelope)
				if err != nil {
					t.Fatal(err)
				}
				if tt.missing {
					if row["value"] != nil || row["coverage-status"] != "missing" {
						t.Fatalf("incomplete evidence became numeric: %+v", row)
					}
				} else if row["value"] == nil || row["coverage-status"] != "complete" ||
					row["valueId"] != dependabotWorkflow+"."+dependabotMetrics[i].id {
					t.Fatalf("complete metric is not a native cutoff: %+v", row)
				}
			}
		})
	}
}
