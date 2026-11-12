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
	result, err := db.ReadCanonical(ctx, CanonicalReadRequest{Kind: "local-metadata", MaxRows: 1, MaxBytes: 1 << 20})
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}
	content, err := json.Marshal(result.Rows[0])
	if err != nil {
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
	if err := json.Unmarshal(content, &record); err != nil {
		return nil, errors.New("local dashboard observation contract is malformed")
	}
	start, startErr := time.Parse(time.RFC3339Nano, record.WindowStart)
	end, endErr := time.Parse(time.RFC3339Nano, record.WindowEnd)
	observed, observedErr := time.Parse(time.RFC3339Nano, record.CreatedAt)
	if record.Kind != "local-dashboard" || record.Completeness != "partial" ||
		startErr != nil || endErr != nil || observedErr != nil || !end.After(start) ||
		end.Sub(start) > 7*24*time.Hour || len(record.Repositories) == 0 || len(record.Repositories) > 20 {
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
	}, nil
}
