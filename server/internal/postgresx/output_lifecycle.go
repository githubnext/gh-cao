package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
)

var outputLifecycleFields = []string{
	"id", "repositoryId", "workflowId", "runId", "safeOutputType",
	"targetRepository", "targetUrl", "title",
	"deliveryState", "deliverySignal", "deliveredAt", "deliveryObservedAt",
	"deliverySource", "deliveryEvidenceId",
	"adoptionState", "adoptionSignal", "adoptionAt", "adoptionObservedAt", "adoptionSource",
	"ruleVersion", "reversalSignal", "reversedAt",
	"survivalState", "survivalSignal", "survivalObservedAt", "observedAt",
}

func (s *Store) readOutputLifecycle(ctx context.Context, tx *sql.Tx, id string) (model.Row, error) {
	rows, err := readExportRows(ctx, tx, s.namespace, "$outputLifecycles", outputLifecycleFields,
		"id=$2 LIMIT 1", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return rows[0], nil
}

func (s *Store) replaceOutputLifecycle(
	ctx context.Context,
	tx *sql.Tx,
	previous model.Row,
	incoming model.Row,
	at time.Time,
) (bool, error) {
	merged, err := mining.MergeOutputLifecycle(previous, incoming)
	if err != nil {
		return false, err
	}
	if previous != nil && reflect.DeepEqual(previous, merged) {
		return false, nil
	}
	if previous != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM output_lifecycles WHERE namespace=$1 AND id=$2`,
			s.namespace, previous["id"]); err != nil {
			return false, err
		}
	}
	if err := insertInvocationEntity(ctx, tx, s.namespace, "$outputLifecycles", merged, at); err != nil {
		return false, err
	}
	if err := partialInvocationQuality(ctx, tx, s.namespace, "$outputLifecycles"); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) mergeAcquisitionOutputLifecycle(
	ctx context.Context,
	tx *sql.Tx,
	row model.Row,
	at time.Time,
) (bool, error) {
	previous, err := s.readOutputLifecycle(ctx, tx, row["id"].(string))
	if errors.Is(err, sql.ErrNoRows) {
		previous = nil
	} else if err != nil {
		return false, err
	}
	return s.replaceOutputLifecycle(ctx, tx, previous, row, at)
}

func lifecycleString(value any) string {
	if value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}

func firstLifecycleValue(values ...any) any {
	for _, value := range values {
		if lifecycleString(value) != "" {
			return value
		}
	}
	return nil
}

func legacyLifecycleURL(value any) string {
	raw := strings.TrimSpace(lifecycleString(value))
	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" ||
		address.User != nil || address.RawQuery != "" || address.Fragment != "" ||
		address.RawPath != "" || address.Port() != "" {
		return ""
	}
	return address.String()
}

func lifecycleClock(row model.Row, fields ...string) (time.Time, error) {
	for _, field := range fields {
		if row[field] == nil {
			continue
		}
		value, err := (entityColumn{kind: "timestamp"}).bind(row[field])
		if err != nil {
			return time.Time{}, err
		}
		if instant, ok := value.(time.Time); ok && !instant.IsZero() {
			return instant, nil
		}
	}
	return time.Time{}, errors.New("safe-output lifecycle requires delivery and observation clocks")
}

// AppendLegacyOutputLifecycle upgrades pre-schema-37 normalized safe-output
// rows while their producing Run is already present from the runs phase.
func (w *Writer) AppendLegacyOutputLifecycle(ctx context.Context, source string, input model.Row) error {
	row, err := normalizeProjectedFacts(source, input)
	if err != nil {
		return err
	}
	if row["type"] != "safe_output.result" || strings.TrimSpace(lifecycleString(row["safeOutputType"])) == "" {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(lifecycleString(row["status"])))
	if status == "error" || status == "failure" || status == "failed" || status == "skipped" {
		return nil
	}
	runID := strings.TrimSpace(lifecycleString(row["runId"]))
	var repositoryID, workflowID string
	if err := w.tx.QueryRow(ctx, `SELECT repository_id,workflow_id FROM runs
		WHERE namespace=$1 AND id=$2`, w.store.namespace, runID).Scan(&repositoryID, &workflowID); err != nil {
		return err
	}
	deliveredAt, err := lifecycleClock(row, "timestamp", "observedAt")
	if err != nil {
		return err
	}
	observedAt, err := lifecycleClock(row, "observedAt", "timestamp")
	if err != nil {
		return err
	}
	evidenceID := strings.TrimSpace(lifecycleString(row["safeOutputId"]))
	if evidenceID == "" {
		evidenceID = strings.TrimSpace(lifecycleString(row["id"]))
	}
	targetURL := legacyLifecycleURL(firstLifecycleValue(row["safeOutputUrl"], row["correlationId"]))
	deliverySource := strings.TrimSpace(lifecycleString(row["source"]))
	if deliverySource == "" {
		deliverySource = "normalized-jsonl"
	}
	lifecycle, err := mining.OutputLifecycleRow(
		evidenceID, repositoryID, workflowID, runID,
		lifecycleString(row["safeOutputType"]), lifecycleString(row["summary"]),
		lifecycleString(row["targetRepository"]), targetURL,
		deliveredAt, observedAt, deliverySource,
	)
	if err != nil {
		return err
	}
	return w.append(ctx, "$outputLifecycles", lifecycle, false)
}
