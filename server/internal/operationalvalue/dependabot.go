package operationalvalue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/githubnext/gh-cao/server/internal/query"
)

const (
	dependabotRepository = "github/gh-aw"
	dependabotWorkflow   = "dependabot-update-planner"
	dependabotCollector  = "dependabot/operational-value/dependabot-update-planner.mjs"
	dependabotAdoption   = "2026-09-17T08:48:00Z"
	dependabotEvaluator  = "be54f77f49e5d28b30a4d72d41708f3b65d505f02cb166c24a9ae9887099e961"
)

var dependabotMetrics = []struct {
	id, name, unit, role, digest string
}{
	{"open-security-alert-count", "Open security alerts", "alerts", "primary",
		"d73a2676ebb04ebf34093873dc5eb72b8e54a461c8b5f068b7b3bd100fc2507a"},
	{"open-high-critical-alert-count", "Open critical/high security alerts", "alerts", "diagnostic",
		"c0bcc97030ca99e4ed5bbe1b89b64ebde02bc7b4659682058e407cf155603ed2"},
	{"open-dependabot-pr-count", "Open Dependabot pull requests", "pull-requests", "diagnostic",
		"774112c80afca79b272669e30a75dc5b1216191f9345e6e14970863bb617652a"},
}

func dependabotCutoff(observed time.Time) (time.Time, bool) {
	adoption, _ := time.Parse(time.RFC3339, dependabotAdoption)
	period := 7 * 24 * time.Hour
	if observed.Before(adoption.Add(period)) {
		return time.Time{}, false
	}
	return adoption.Add(observed.Sub(adoption) / period * period), true
}

func reviewedDependabotEvidence(reviewed valuePolicy) bool {
	for _, entry := range reviewed.Control.Campaigns["activity"].Values {
		if entry.Repository == dependabotRepository && entry.Workflow == dependabotWorkflow &&
			entry.Collector == dependabotCollector && entry.APIRequests == 111 {
			return true
		}
	}
	return false
}

type dependabotSnapshot struct {
	values  [3]int
	capture time.Time
	digest  string
}

// The query reads only the three reviewed packet identities through the
// canonical SQL query boundary. A missing or partial triplet is not zero.
func readDependabotSnapshot(
	ctx context.Context, cutoff time.Time, execute QueryExecutor,
) (*dependabotSnapshot, error) {
	identity := "operational-value-evidence:"
	route := dependabotRepository + ":.github/workflows/" + dependabotWorkflow + ".md"
	factID := "cutoff:" + cutoff.UTC().Format(time.RFC3339)
	definitions := []query.Definition{
		{Name: "operational-value-evidence", From: "$operationalValueEvidence"},
		{Name: "dependabot-cutoff", From: "operational-value-evidence",
			Filter: &query.Filter{Predicates: []query.Predicate{
				{Field: "workflow-route", Equals: route},
				{Field: "fact-id", In: []any{"contract", factID}},
			}}},
	}
	sources, err := execute(ctx, definitions, []string{"dependabot-cutoff"})
	if err != nil {
		return nil, fmt.Errorf("hosted Dependabot canonical evidence query: %w", err)
	}
	source, ok := sources["dependabot-cutoff"]
	if !ok {
		return nil, errors.New("hosted Dependabot canonical query did not return its source")
	}
	if source.Metadata["availability"] != "available" || len(source.Rows) == 0 {
		return nil, nil
	}
	var result dependabotSnapshot
	var captured bool
	seen := [3][3]bool{}
	for _, row := range source.Rows {
		id := textField(row, "report-id")
		index := -1
		for i, metric := range dependabotMetrics {
			if id == identity+url.QueryEscape(dependabotRepository+":"+dependabotWorkflow+":"+metric.id+":dependabot-lifecycle-v1") {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, errors.New("unreviewed Dependabot metric packet in canonical query")
		}
		capture, err := time.Parse(time.RFC3339Nano, textField(row, "observed-at"))
		if err != nil || capture.Before(cutoff) || capture.After(time.Now()) ||
			textField(row, "window-end") == "" {
			return nil, errors.New("invalid Dependabot evidence capture or cutoff")
		}
		end, err := time.Parse(time.RFC3339Nano, textField(row, "window-end"))
		if err != nil || !end.Equal(cutoff) || captured && !result.capture.Equal(capture) {
			return nil, errors.New("Dependabot evidence packets disagree on their capture or cutoff")
		}
		captured, result.capture = true, capture
		if textField(row, "contract-digest") != dependabotMetrics[index].digest {
			return nil, errors.New("Dependabot evidence changed its frozen contract digest")
		}
		switch textField(row, "fact-kind") {
		case "contract":
			if seen[index][0] || textField(row, "fact-id") != "contract" ||
				textField(row, "metric-id") != dependabotMetrics[index].id ||
				textField(row, "metric-name") != dependabotMetrics[index].name ||
				textField(row, "metric-unit") != dependabotMetrics[index].unit ||
				textField(row, "snapshot-id") != dependabotMetrics[index].id ||
				textField(row, "metric-direction") != "decrease" ||
				textField(row, "metric-version") != "dependabot-lifecycle-v1" {
				return nil, errors.New("Dependabot evidence changed its frozen metric contract")
			}
			seen[index][0] = true
		case "snapshot":
			at, err := time.Parse(time.RFC3339Nano, textField(row, "at"))
			value, valid := safeValueCount(row["value"])
			if seen[index][1] || textField(row, "fact-id") != factID ||
				err != nil || !at.Equal(cutoff) || !valid ||
				row["items-available"] != false {
				return nil, errors.New("Dependabot snapshot is not a complete native count")
			}
			result.values[index], seen[index][1] = value, true
		case "coverage":
			start, startErr := time.Parse(time.RFC3339Nano, textField(row, "at"))
			end, endErr := time.Parse(time.RFC3339Nano, textField(row, "end-at"))
			if seen[index][2] || textField(row, "fact-id") != factID ||
				startErr != nil || endErr != nil || !start.Equal(cutoff.Add(-7*24*time.Hour)) ||
				!end.Equal(cutoff) || row["coverage-status"] != "complete" {
				return nil, errors.New("Dependabot evidence is missing complete cutoff coverage")
			}
			seen[index][2] = true
		default:
			return nil, errors.New("unexpected Dependabot fact in scoped cutoff query")
		}
	}
	for _, kinds := range seen {
		if !kinds[0] || !kinds[1] || !kinds[2] {
			return nil, nil
		}
	}
	if result.values[1] > result.values[0] {
		return nil, errors.New("critical/high Dependabot alerts exceed all open alerts")
	}
	encoded, err := json.Marshal(struct {
		Values  [3]int
		Cutoff  string
		Capture string
	}{result.values, optimizerTime(cutoff), optimizerTime(result.capture)})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	result.digest = hex.EncodeToString(hash[:])
	return &result, nil
}

func writeDependabotValue(
	ctx context.Context, output *bytes.Buffer, observed time.Time,
	remote bool, execute QueryExecutor,
) error {
	cutoff, due := dependabotCutoff(observed)
	if !due {
		return nil
	}
	var snapshot *dependabotSnapshot
	if remote {
		var err error
		snapshot, err = readDependabotSnapshot(ctx, cutoff, execute)
		if err != nil {
			return err
		}
	}
	if snapshot == nil {
		return writeUnavailableDependabotValue(output, observed)
	}
	for index, metric := range dependabotMetrics {
		record := map[string]any{
			"timestamp": optimizerTime(snapshot.capture), "repository": dependabotRepository,
			"campaign": "dependabot", "campaign_id": "campaign:dependabot",
			"value_id": dependabotWorkflow + "." + metric.id, "value": snapshot.values[index],
			"metric_role": metric.role, "metric_name": metric.name, "metric_unit": metric.unit,
			"metric_direction": "decrease", "maturity_status": "matured",
			"adoption_at": dependabotAdoption, "evaluation_mode": "baseline-comparable",
			"workflow_slug": dependabotWorkflow, "workflow_name": "Dependabot / Update Planner",
			"evaluator_digest": dependabotEvaluator, "cadence_days": 7,
			"window_start": optimizerTime(cutoff.Add(-7 * 24 * time.Hour)),
			"window_end":   optimizerTime(cutoff), "measurement_kind": "snapshot",
			"coverage_status": "complete", "evidence_digest": snapshot.digest,
		}
		if err := json.NewEncoder(output).Encode(map[string]any{
			"schema_version": 2, "kind": "operational_value", "operational_value": record,
		}); err != nil {
			return err
		}
	}
	return nil
}
