package dashboardquery

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// LocalDashboardMetadata reads the acquisition profile's observation contract
// in the caller's snapshot transaction. Ordinary Activity snapshots have no
// such marker and retain their existing unknown-quality semantics.
func LocalDashboardMetadata(ctx context.Context, db SnapshotReader) (metadata map[string]any, err error) {
	rows, err := db.QueryContext(ctx, `SELECT value FROM __idb_records
		WHERE database_name=? AND store_name='transactions' AND record_key='"local-dashboard"'`, DatabaseName)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var content string
	if err := rows.Scan(&content); err != nil {
		return nil, err
	}
	var record struct {
		Kind         string   `json:"kind"`
		CreatedAt    string   `json:"createdAt"`
		WindowStart  string   `json:"windowStart"`
		WindowEnd    string   `json:"windowEnd"`
		Repositories []string `json:"repositories"`
		Coverage     string   `json:"coverage"`
		Completeness string   `json:"completeness"`
	}
	if err := json.Unmarshal([]byte(content), &record); err != nil {
		return nil, errors.New("local dashboard observation contract is malformed")
	}
	start, startErr := time.Parse(time.RFC3339Nano, record.WindowStart)
	end, endErr := time.Parse(time.RFC3339Nano, record.WindowEnd)
	observed, observedErr := time.Parse(time.RFC3339Nano, record.CreatedAt)
	if record.Kind != "local-dashboard" || record.Completeness != "partial" ||
		startErr != nil || endErr != nil || observedErr != nil || !end.After(start) ||
		end.Sub(start) > 48*time.Hour || len(record.Repositories) == 0 || len(record.Repositories) > 20 {
		return nil, errors.New("local dashboard observation contract is invalid")
	}
	freshness := "unknown"
	if time.Since(observed) > 15*time.Minute {
		freshness = "stale"
	}
	return map[string]any{
		"as-of": record.WindowEnd, "retrieved-at": record.CreatedAt,
		"completeness": "partial", "freshness": freshness,
		"repository-scope": record.Repositories, "window-start": record.WindowStart,
		"coverage": record.Coverage,
	}, rows.Err()
}
