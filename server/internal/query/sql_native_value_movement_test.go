package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeValueMovementDashboardQueriesCompileToSQL(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard-fragments", "shared-value.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fragment struct {
		Queries []Definition `json:"queries"`
	}
	if err := json.Unmarshal(data, &fragment); err != nil {
		t.Fatal(err)
	}
	plan, err := CompileSQL(fragment.Queries,
		[]string{"value-current-measurements", "value-campaign-measurement-summary"},
		func(name string) (SQLRelation, error) {
			switch name {
			case "workflow-value-measurements":
				return nativeValueMeasurementRelation(), nil
			case "workflows":
				return nativeValueWorkflowRelation(), nil
			case "campaigns":
				return nativeValueCampaignRelation(), nil
			default:
				return SQLRelation{}, fmt.Errorf("unexpected source %s", name)
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	for output, fields := range map[string][]string{
		"value-current-measurements": {
			"movement-state", "native-change", "comparison-reason",
			"previous-window-start", "previous-window-end",
		},
		"value-campaign-measurement-summary": {
			"measured-repositories", "improving-repositories", "worsening-repositories",
			"unchanged-repositories", "mixed-repositories", "insufficient-repositories",
		},
	} {
		for _, field := range fields {
			if _, ok := plan.Outputs[output].Columns[field]; !ok {
				t.Fatalf("%s omitted SQL output %q", output, field)
			}
		}
	}
}

func nativeValueMeasurementRelation() SQLRelation {
	columns := map[string]SQLColumn{}
	for _, field := range []string{
		"id", "repository", "workflow-route", "campaign", "workflow-slug", "workflow-name",
		"value-id", "metric-role", "metric-name", "metric-unit", "metric-direction",
		"evaluator-digest", "measurement-kind", "maturity-status", "coverage-status",
		"missing-reason", "evidence-digest", "population-digest", "evaluation-mode", "adoption-at",
	} {
		columns[field] = SQLColumn{Expression: "NULL::text", Presence: "FALSE", Kind: SQLText}
	}
	for _, field := range []string{"observed-at", "window-start", "window-end"} {
		columns[field] = SQLColumn{Expression: "NULL::timestamptz", Presence: "FALSE", Kind: SQLTimestamp}
	}
	for _, field := range []string{"value", "eligible-count", "cadence-days"} {
		columns[field] = SQLColumn{Expression: "NULL::numeric", Presence: "FALSE", Kind: SQLNumber}
	}
	return SQLRelation{SQL: "(SELECT 1) AS native_values", Columns: columns}
}

func nativeValueWorkflowRelation() SQLRelation {
	columns := map[string]SQLColumn{}
	for _, field := range []string{
		"organization", "repository", "workflow", "workflow-role", "campaign", "campaign-name", "campaign-icon",
	} {
		columns[field] = SQLColumn{Expression: "NULL::text", Presence: "FALSE", Kind: SQLText}
	}
	return SQLRelation{SQL: "(SELECT 1) AS workflows", Columns: columns}
}

func nativeValueCampaignRelation() SQLRelation {
	columns := map[string]SQLColumn{}
	for _, field := range []string{"campaign", "campaign-name", "campaign-icon"} {
		columns[field] = SQLColumn{Expression: "NULL::text", Presence: "FALSE", Kind: SQLText}
	}
	return SQLRelation{SQL: "(SELECT 1) AS campaigns", Columns: columns}
}
