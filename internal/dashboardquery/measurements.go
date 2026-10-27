package dashboardquery

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
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
	var clauses []string
	var args []any
	for _, selection := range selections {
		switch selection.Field {
		case "id":
			key, _ := json.Marshal(selection.Value)
			clauses = append(clauses, "record_key=?")
			args = append(args, string(key))
		case "workflow-route":
			parts := evidenceWorkflowRoute.FindStringSubmatch(selection.Value)
			if parts == nil {
				clauses = append(clauses, "0")
				continue
			}
			marker := strings.ReplaceAll(url.QueryEscape(":"+strings.ToLower(parts[1])+":"+parts[2]+"."), "+", "%20")
			clauses = append(clauses, "record_key GLOB ?")
			args = append(args, `"operational-value:*`+marker+`*`)
		default:
			return nil, errors.New("unsupported native measurement selection")
		}
	}
	where := ""
	if len(clauses) > 0 {
		where = " AND (" + strings.Join(clauses, " OR ") + ")"
	}
	return readCollectionFields(ctx, db, "operationalValues", nil, 20000, MaxEvidenceBytes, where, args)
}
