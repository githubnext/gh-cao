package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"
)

func prepareDashboardCache(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("dashboard cache must be a real directory")
	}
	return os.Chmod(directory, 0o700)
}

func readDashboardStatus(ctx context.Context, cache string, repositories []string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Join(cache, "inventory-sources.json"))
	if err != nil {
		return nil, fmt.Errorf("no local dashboard evidence; run gh cao dashboard: %w", err)
	}
	var inventory struct {
		Repositories struct {
			Metadata struct {
				LocalDashboard map[string]any `json:"local-dashboard"`
			} `json:"metadata"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		return nil, errors.New("local dashboard metadata is malformed")
	}
	metadata := inventory.Repositories.Metadata.LocalDashboard
	if metadata == nil {
		return nil, errors.New("cache is not on-demand dashboard evidence; select a separate --cache-dir")
	}
	scope := []string{}
	for _, value := range nativeArray(metadata["repositories"]) {
		repository, ok := value.(string)
		if !ok {
			return nil, errors.New("local dashboard metadata contains an invalid repository scope")
		}
		scope = append(scope, repository)
	}
	if !slices.Equal(scope, repositories) {
		return nil, errors.New("dashboard cache repository scope differs from the requested scope; select a separate --cache-dir")
	}
	return metadata, nil
}

func initializeDashboardSnapshot(ctx context.Context, cache string, repositories []string, options dashboardOptions) error {
	if err := prepareDashboardCache(cache); err != nil {
		return err
	}
	lock := filepath.Join(cache, "collection.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return errors.New("dashboard collection is already locked")
	}
	defer func() { _ = os.Remove(lock) }()
	if _, err := os.Stat(filepath.Join(cache, "inventory-sources.json")); err == nil {
		_, err = readDashboardStatus(ctx, cache, repositories)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	metadata := dashboardObservationMetadata(repositories, options, time.Now().UTC(), nil)
	metadata["coverage"] = "Initial collection pending; upstream evidence is not yet available."
	return publishDashboardSnapshot(ctx, cache, map[string][]map[string]any{"transactions": {metadata}})
}

func publishDashboardSnapshot(ctx context.Context, cache string, batch map[string][]map[string]any) error {
	return writeDashboardSnapshot(ctx, cache, batch, false)
}

// The cache retains verified canonical input, not a second query backend.
// PostgreSQL owns query execution and daily-count reconciliation.
func writeDashboardSnapshot(ctx context.Context, cache string, batch map[string][]map[string]any, keepHistory bool) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prepareDashboardCache(cache); err != nil {
		return err
	}
	if keepHistory {
		batch, err = retainDashboardHistory(ctx, cache, batch)
		if err != nil {
			return err
		}
	}
	staging, err := os.MkdirTemp(cache, ".deployed-dashboard-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	manifest := map[string]string{}
	published := map[string][]map[string]any{}
	for store, rows := range batch {
		if store == "transactions" {
			rows = slices.DeleteFunc(slices.Clone(rows), func(row map[string]any) bool {
				return row["id"] != "local-dashboard-ingestion"
			})
		}
		published[store] = rows
	}
	stores := make([]string, 0, len(published))
	for store := range published {
		stores = append(stores, store)
	}
	slices.Sort(stores)
	for _, phase := range []string{"runs", "records"} {
		var content bytes.Buffer
		count := 0
		for _, store := range stores {
			runPhase := slices.Contains([]string{"campaigns", "repositories", "workflows", "runs", "experiments", "experimentAssignments"}, store)
			if runPhase == (phase == "runs") {
				count += len(published[store])
			}
		}
		encoder := json.NewEncoder(&content)
		if err := encoder.Encode(map[string]any{"kind": "metadata", "phase": phase, "records": count}); err != nil {
			return err
		}
		for _, store := range stores {
			runPhase := slices.Contains([]string{"campaigns", "repositories", "workflows", "runs", "experiments", "experimentAssignments"}, store)
			if runPhase != (phase == "runs") {
				continue
			}
			for _, row := range published[store] {
				if err := encoder.Encode(map[string]any{"kind": "record", "collection": store, "record": row}); err != nil {
					return err
				}
			}
		}
		directory := "gh-aw-logs-" + phase
		if err := os.MkdirAll(filepath.Join(staging, directory), 0o700); err != nil {
			return err
		}
		name := directory + "/local.jsonl"
		if err := os.WriteFile(filepath.Join(staging, name), content.Bytes(), 0o600); err != nil {
			return err
		}
		hash := sha256.Sum256(content.Bytes())
		manifest[name] = hex.EncodeToString(hash[:])
	}
	var local, ingestion map[string]any
	for _, row := range batch["transactions"] {
		switch row["id"] {
		case "local-dashboard":
			local = row
		case "local-dashboard-ingestion":
			ingestion = row
		}
	}
	quality := map[string]any{"availability": "available", "completeness": "partial", "freshness": "unknown"}
	if local != nil {
		quality["as-of"], quality["local-dashboard"] = local["windowEnd"], local
	}
	if ingestion != nil {
		quality["local-dashboard-ingestion"] = ingestion
	}
	inventory, err := json.Marshal(map[string]any{
		"repositories": map[string]any{"rows": []any{}, "metadata": quality},
		"workflows":    map[string]any{"rows": []any{}, "metadata": quality},
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "inventory-sources.json"), inventory, 0o600); err != nil {
		return err
	}
	hash := sha256.Sum256(inventory)
	manifest["inventory-sources.json"] = hex.EncodeToString(hash[:])
	content, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "payload-hashes.json"), content, 0o600); err != nil {
		return err
	}
	return nativePublishDownload(staging, cache, []string{"gh-aw-logs-runs", "gh-aw-logs-records", "inventory-sources.json", "payload-hashes.json"})
}

func readDashboardArtifactBatch(ctx context.Context, cache string) (map[string][]map[string]any, error) {
	batch := map[string][]map[string]any{}
	content, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
	if errors.Is(err, os.ErrNotExist) {
		return batch, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest map[string]string
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	for _, phase := range []string{"runs", "records"} {
		name := "gh-aw-logs-" + phase + "/local.jsonl"
		if err := readDashboardArtifactPhase(ctx, cache, name, manifest[name], batch); err != nil {
			return nil, err
		}
	}
	return batch, nil
}

func readDashboardArtifactPhase(ctx context.Context, cache, name, expected string, batch map[string][]map[string]any) (err error) {
	file, err := os.Open(filepath.Join(cache, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(file, hash))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var row struct {
			Kind       string         `json:"kind"`
			Collection string         `json:"collection"`
			Record     map[string]any `json:"record"`
		}
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		if row.Kind == "metadata" {
			continue
		}
		if row.Kind != "record" || row.Collection == "" || nativeText(row.Record["id"]) == "" {
			return errors.New("local dashboard canonical artifact contains an invalid record")
		}
		batch[row.Collection] = append(batch[row.Collection], row.Record)
	}
	if expected == "" || hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("local dashboard canonical artifact checksum mismatch")
	}
	return nil
}

func retainDashboardHistory(ctx context.Context, cache string, incoming map[string][]map[string]any) (map[string][]map[string]any, error) {
	previous, err := readDashboardArtifactBatch(ctx, cache)
	if err != nil {
		return nil, err
	}
	cutoff := ""
	for _, row := range incoming["transactions"] {
		if row["id"] == "local-dashboard" {
			cutoff = nativeText(row["windowStart"])
		}
	}
	retainAfter, err := time.Parse(time.RFC3339Nano, cutoff)
	if err != nil {
		return nil, errors.New("history publication requires a valid detail retention cutoff")
	}
	merged := map[string]map[string]map[string]any{}
	for collection, rows := range previous {
		merged[collection] = map[string]map[string]any{}
		for _, row := range rows {
			merged[collection][nativeText(row["id"])] = row
		}
	}
	rejected, replaced := map[string]bool{}, map[string]bool{}
	revision := func(row map[string]any) float64 {
		return math.Max(0, math.Max(nativeTimestamp(row["updatedAt"]), nativeTimestamp(row["completedAt"])))
	}
	for _, row := range incoming["runs"] {
		id := nativeText(row["id"])
		before := merged["runs"][id]
		if before == nil {
			continue
		}
		oldAttempt, newAttempt := nativeNumber(before["attempt"]), nativeNumber(row["attempt"])
		if newAttempt < oldAttempt || newAttempt == oldAttempt && revision(row) < revision(before) {
			rejected[id] = true
		} else if oldAttempt == newAttempt {
			for field, value := range before {
				if _, exists := row[field]; !exists {
					row[field] = value
				}
			}
		} else {
			replaced[id] = true
		}
	}
	for collection, rows := range merged {
		if collection != "runs" {
			for id, row := range rows {
				if replaced[nativeText(row["runId"])] {
					delete(rows, id)
				}
			}
		}
	}
	for collection, rows := range incoming {
		if merged[collection] == nil {
			merged[collection] = map[string]map[string]any{}
		}
		for _, row := range rows {
			if collection == "runs" && rejected[nativeText(row["id"])] || rejected[nativeText(row["runId"])] {
				continue
			}
			merged[collection][nativeText(row["id"])] = row
		}
	}
	for id, row := range merged["runs"] {
		if created, err := time.Parse(time.RFC3339Nano, nativeText(row["createdAt"])); err == nil && created.Before(retainAfter) {
			delete(merged["runs"], id)
		}
	}
	batch := map[string][]map[string]any{}
	for collection, rows := range merged {
		ids := make([]string, 0, len(rows))
		for id, row := range rows {
			if runID := nativeText(row["runId"]); runID == "" || merged["runs"][runID] != nil {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			batch[collection] = append(batch[collection], rows[id])
		}
	}
	return batch, nil
}

func readDashboardIngestion(ctx context.Context, cache string) (state dashboardIngestion, err error) {
	state.Cursors = map[string]dashboardHistoryCursor{}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	content, err := os.ReadFile(filepath.Join(cache, "inventory-sources.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	var inventory struct {
		Repositories struct {
			Metadata map[string]json.RawMessage `json:"metadata"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		return state, err
	}
	content = inventory.Repositories.Metadata["local-dashboard-ingestion"]
	if len(content) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(content, &state); err != nil || state.Cursors == nil {
		return state, errors.New("local dashboard ingestion checkpoint is malformed")
	}
	if state.ID != "local-dashboard-ingestion" || state.Kind != "local-dashboard-ingestion" || state.NextRepo < 0 {
		return state, errors.New("local dashboard ingestion checkpoint has invalid identity or repository cursor")
	}
	for _, cursor := range state.Cursors {
		if cursor.Page < 1 || cursor.End.IsZero() || !cursor.Start.IsZero() && cursor.Start.After(cursor.End) {
			return state, errors.New("local dashboard history cursor is invalid")
		}
	}
	return state, nil
}

func dashboardIngestionRecord(state dashboardIngestion) (map[string]any, error) {
	content, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var record map[string]any
	err = json.Unmarshal(content, &record)
	return record, err
}

func writeDashboardIngestion(ctx context.Context, cache string, state dashboardIngestion) error {
	content, err := os.ReadFile(filepath.Join(cache, "inventory-sources.json"))
	if err != nil {
		return err
	}
	var inventory map[string]struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		return err
	}
	metadata, ok := inventory["repositories"].Metadata["local-dashboard"].(map[string]any)
	if !ok {
		return errors.New("local dashboard metadata is missing")
	}
	record, err := dashboardIngestionRecord(state)
	if err != nil {
		return err
	}
	batch, err := readDashboardArtifactBatch(ctx, cache)
	if err != nil {
		return err
	}
	batch["transactions"] = []map[string]any{metadata, record}
	return publishDashboardSnapshot(ctx, cache, batch)
}
