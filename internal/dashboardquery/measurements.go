package dashboardquery

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var measurementWorkflowSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// MeasurementRows only exposes a workflow route when the retained metric
// namespace agrees with its declared workflow. It does not infer activation.
func MeasurementRows(rows []map[string]any) {
	for _, row := range rows {
		repository, _ := row["repository"].(string)
		workflow, _ := row["workflow-slug"].(string)
		valueID, _ := row["valueId"].(string)
		if repository != "" && measurementWorkflowSlug.MatchString(workflow) && strings.HasPrefix(valueID, workflow+".") {
			row["workflow-route"] = repository + ":.github/workflows/" + workflow + ".md"
		}
	}
}

// Scope by canonical record keys before decoding observation bodies. Workflow
// key scans remain bounded by the enclosing read transaction's cancellation.
func ReadMeasurements(ctx context.Context, db SnapshotReader, selections []EvidenceSelection) ([]map[string]any, error) {
	for _, selection := range selections {
		switch selection.Field {
		case "id", "workflow-route":
		default:
			return nil, errors.New("unsupported native measurement selection")
		}
	}
	result, err := db.ReadCanonical(ctx, CanonicalReadRequest{
		Kind: "measurements", Collection: "operationalValues", Selections: selections,
		MaxRows: 20000, MaxBytes: MaxEvidenceBytes,
	})
	return result.Rows, err
}
