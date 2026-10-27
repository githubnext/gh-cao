package dashboardquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxEvidencePackets = 32
const MaxEvidenceBytes = 16 << 20
const MaxEvidenceFacts = 20000

type EvidenceSelection struct{ Field, Value string }

var evidenceWorkflowRoute = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9-]*/[a-zA-Z0-9._-]+):\.github/workflows/([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)

// ReadEvidencePackets restricts primary-key ranges before reading JSON bodies.
// Workflow ranges follow the declared packet identity, not an arbitrary inverse
// of a dashboard computed expression.
func ReadEvidencePackets(ctx context.Context, db SnapshotReader, selections []EvidenceSelection) ([]map[string]any, error) {
	var clauses []string
	var args []any
	for _, selection := range selections {
		switch selection.Field {
		case "report-id":
			key, _ := json.Marshal(selection.Value)
			clauses = append(clauses, "record_key=?")
			args = append(args, string(key))
		case "workflow-route":
			parts := evidenceWorkflowRoute.FindStringSubmatch(selection.Value)
			if parts == nil {
				clauses = append(clauses, "0")
				continue
			}
			encoded := strings.ReplaceAll(url.QueryEscape(strings.ToLower(parts[1])+":"+parts[2]+":"), "+", "%20")
			prefix := `"operational-value-evidence:` + encoded
			clauses = append(clauses, "(record_key>=? AND record_key<?)")
			args = append(args, prefix, prefix+"\x7f")
		default:
			return nil, errors.New("unsupported evidence selection")
		}
	}
	where := ""
	if len(clauses) > 0 {
		where = " AND (" + strings.Join(clauses, " OR ") + ")"
	}
	rows, err := readCollectionFields(ctx, db, "operationalValueEvidence", nil, MaxEvidencePackets, MaxEvidenceBytes, where, args)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, row := range rows {
		facts, _ := row["facts"].([]any)
		count += len(facts)
		if count > MaxEvidenceFacts {
			return nil, errors.New("value evidence exceeds bounded fact budget; select an exact workflow or report")
		}
	}
	return rows, nil
}

// OperationalValueEvidenceMetadata preserves evidence clocks without promoting
// incomplete acquisition or old captures to complete/fresh observations.
func OperationalValueEvidenceMetadata(packets []map[string]any) (map[string]any, error) {
	result := map[string]any{"completeness": "unknown", "freshness": "unknown"}
	for logical, field := range map[string]string{"as-of": "windowEnd", "retrieved-at": "observedAt"} {
		var earliest time.Time
		for _, packet := range packets {
			text, _ := packet[field].(string)
			instant, err := time.Parse(time.RFC3339Nano, text)
			if err != nil {
				return nil, errors.New("invalid operational value evidence clock")
			}
			if earliest.IsZero() || instant.Before(earliest) {
				earliest, result[logical] = instant, text
			}
		}
	}
	return result, nil
}

// OperationalValueEvidenceRows only projects acquired facts. Report computation
// belongs to the declarative query plan, never the storage adapter.
func OperationalValueEvidenceRows(packets []map[string]any) ([]map[string]any, error) {
	result := []map[string]any{}
	snapshotRouteBytes := 0
	for _, packet := range packets {
		repository, ok := packet["repository"].(string)
		parts := strings.Split(repository, "/")
		facts, valid := packet["facts"].([]any)
		if !ok || len(parts) != 2 || !valid || len(facts) == 0 || len(facts) > 20000 {
			return nil, errors.New("invalid operational value evidence packet")
		}
		snapshotRoutes := map[string]string{}
		for _, item := range facts {
			fact, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("invalid operational value evidence fact")
			}
			row := map[string]any{
				"report-id": packet["id"], "organization": parts[0], "repository": parts[1],
				"workflow": packet["workflow"], "observed-at": packet["observedAt"],
				"workflow-path":  packet["sourcePath"],
				"workflow-route": repository + ":" + fmt.Sprint(packet["sourcePath"]),
				"window-start":   packet["windowStart"], "window-end": packet["windowEnd"],
				"contract-digest": packet["contractDigest"],
			}
			for key, value := range fact {
				row[key] = value
			}
			if fact["fact-kind"] == "snapshot" || fact["fact-kind"] == "snapshot-item" {
				key := fact["fact-id"]
				if fact["fact-kind"] == "snapshot-item" {
					key = fact["parent-id"]
				}
				snapshotID, validSnapshot := key.(string)
				reportID, validReport := packet["id"].(string)
				if !validSnapshot || !validReport || snapshotID == "" || reportID == "" {
					return nil, errors.New("invalid operational value snapshot identity")
				}
				route, exists := snapshotRoutes[snapshotID]
				if !exists {
					snapshotRouteBytes += len(reportID) + 1 + len(snapshotID)
					if snapshotRouteBytes > MaxEvidenceBytes {
						return nil, errors.New("value snapshot identities exceed bounded read budget")
					}
					route = reportID + "|" + snapshotID
					snapshotRoutes[snapshotID] = route
				}
				row["snapshot-route"] = route
			}
			result = append(result, row)
		}
	}
	return result, nil
}
