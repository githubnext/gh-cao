package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	dashboardShardRuns       = 16
	dashboardShardBytes      = 1 << 20
	dashboardIndexPath       = "local-dashboard-index/"
	dashboardCheckpointsPath = ".dashboard-checkpoints/"
)

type dashboardShardGroup struct {
	Runs            []string `json:"runs"`
	Issues          []string `json:"issues,omitempty"`
	OldestCreatedAt string   `json:"oldestCreatedAt,omitempty"`
	Shards          []string `json:"shards"`
}

// This private, verified index locates evidence; it is not a query source.
type dashboardShardIndex struct {
	Version int                   `json:"version"`
	Shared  []string              `json:"shared"`
	Groups  []dashboardShardGroup `json:"groups"`
}

func readDashboardSnapshotManifest(ctx context.Context, cache string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest map[string]string
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	if len(manifest) == 0 {
		return nil, errors.New("local dashboard payload manifest is empty")
	}
	hasRuns := false
	for name, digest := range manifest {
		if !filepath.IsLocal(name) || filepath.ToSlash(filepath.Clean(name)) != name || len(digest) != 64 {
			return nil, errors.New("local dashboard payload manifest is invalid")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, errors.New("local dashboard payload manifest hash is invalid")
		}
		hasRuns = hasRuns || strings.HasPrefix(name, "gh-aw-logs-runs/") && dashboardCanonicalShard(name)
	}
	if !hasRuns {
		return nil, errors.New("local dashboard payload manifest is missing Run shards")
	}
	return manifest, nil
}

func readDashboardVerifiedPayload(ctx context.Context, cache, name, expected string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(cache)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	content, err := root.ReadFile(name)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	if expected != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("local dashboard payload checksum mismatch for %s", name)
	}
	return content, ctx.Err()
}

func readDashboardShardIndex(ctx context.Context, cache string, manifest map[string]string) (dashboardShardIndex, string, error) {
	index := dashboardShardIndex{Version: 1}
	indexName := ""
	for name := range manifest {
		if strings.HasPrefix(name, dashboardIndexPath) {
			if indexName != "" {
				return index, "", errors.New("local dashboard has multiple shard indexes")
			}
			indexName = name
		}
	}
	if indexName == "" {
		return index, "", nil
	}
	content, err := readDashboardVerifiedPayload(ctx, cache, indexName, manifest[indexName])
	if err != nil {
		return index, "", err
	}
	if err := json.Unmarshal(content, &index); err != nil {
		return index, "", err
	}
	if index.Version != 1 || len(index.Shared) == 0 {
		return index, "", errors.New("local dashboard shard index is invalid")
	}
	shards, runs, issues := map[string]bool{}, map[string]bool{}, map[string]bool{}
	register := func(names []string) error {
		if len(names) == 0 {
			return errors.New("local dashboard shard group is empty")
		}
		for _, name := range names {
			if !dashboardCanonicalShard(name) || manifest[name] == "" || shards[name] {
				return errors.New("local dashboard shard index has invalid or duplicate payload references")
			}
			shards[name] = true
		}
		return nil
	}
	if err := register(index.Shared); err != nil {
		return index, "", err
	}
	for _, group := range index.Groups {
		if len(group.Runs) == 0 || len(group.Runs) > dashboardShardRuns {
			return index, "", errors.New("local dashboard shard group exceeds its Run bound")
		}
		for _, id := range group.Runs {
			if id == "" || runs[id] {
				return index, "", errors.New("local dashboard shard index has invalid or duplicate Runs")
			}
			runs[id] = true
		}
		for _, id := range group.Issues {
			if id == "" || issues[id] {
				return index, "", errors.New("local dashboard shard index has invalid or duplicate Issues")
			}
			issues[id] = true
		}
		if group.OldestCreatedAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, group.OldestCreatedAt); err != nil {
				return index, "", errors.New("local dashboard shard index has an invalid retention clock")
			}
		}
		if err := register(group.Shards); err != nil {
			return index, "", err
		}
	}
	for name := range manifest {
		if dashboardCanonicalShard(name) && !shards[name] {
			return index, "", errors.New("local dashboard shard index omits a canonical payload")
		}
	}
	return index, indexName, nil
}

func writeDashboardIncrementalSnapshot(ctx context.Context, cache string, incoming map[string][]map[string]any, bundles []byte) error {
	manifest, err := readDashboardSnapshotManifest(ctx, cache)
	if err != nil {
		return err
	}
	old := maps.Clone(manifest)
	index, indexName, err := readDashboardShardIndex(ctx, cache, manifest)
	if err != nil {
		return err
	}
	previous := map[string][]map[string]any{}
	if indexName == "" {
		// Upgrade a legacy/full-replacement snapshot once, not on every bundle.
		previous, err = readDashboardArtifactBatch(ctx, cache)
		if err != nil {
			return err
		}
		for name := range manifest {
			if dashboardCanonicalShard(name) {
				delete(manifest, name)
			}
		}
	} else {
		touched := map[string]bool{}
		issues := map[string]bool{}
		for collection, rows := range incoming {
			for _, row := range rows {
				if collection == "issues" {
					issues[nativeText(row["id"])] = true
				}
				if collection == "runs" {
					touched[nativeText(row["id"])] = true
				} else if id := nativeText(row["runId"]); id != "" {
					touched[id] = true
				}
			}
		}
		cutoff, err := dashboardHistoryCutoff(incoming)
		if err != nil {
			return err
		}
		read := func(names []string) error {
			for _, name := range names {
				if err := readDashboardArtifactPhase(ctx, cache, name, manifest[name], previous); err != nil {
					return err
				}
				delete(manifest, name)
			}
			return nil
		}
		if err := read(index.Shared); err != nil {
			return err
		}
		retained := make([]dashboardShardGroup, 0, len(index.Groups))
		for _, group := range index.Groups {
			created, parseErr := time.Parse(time.RFC3339Nano, group.OldestCreatedAt)
			affected := parseErr == nil && created.Before(cutoff)
			for _, id := range group.Runs {
				if touched[id] {
					affected = true
					break
				}
			}
			// Issue identity belongs to the GitHub object, not its producing Run.
			for _, id := range group.Issues {
				if issues[id] {
					affected = true
					break
				}
			}
			if affected {
				if err := read(group.Shards); err != nil {
					return err
				}
			} else {
				retained = append(retained, group)
			}
		}
		index.Groups = retained
		delete(manifest, indexName)
	}
	merged, err := mergeDashboardHistory(previous, incoming)
	if err != nil {
		return err
	}
	shared, owned := map[string][]map[string]any{}, map[string]map[string][]map[string]any{}
	for collection, rows := range merged {
		for _, row := range rows {
			if collection == "transactions" && row["id"] == "local-dashboard-ingestion" {
				continue
			}
			id := nativeText(row["runId"])
			if collection == "runs" {
				id = nativeText(row["id"])
			}
			target := shared
			if id != "" {
				if owned[id] == nil {
					owned[id] = map[string][]map[string]any{}
				}
				target = owned[id]
			}
			target[collection] = append(target[collection], row)
		}
	}
	index.Shared, err = writeDashboardShardGroup(ctx, cache, shared, manifest)
	if err != nil {
		return err
	}
	ids := slices.Sorted(maps.Keys(owned))
	for offset := 0; offset < len(ids); offset += dashboardShardRuns {
		group := dashboardShardGroup{}
		batch := map[string][]map[string]any{}
		for _, id := range ids[offset:min(offset+dashboardShardRuns, len(ids))] {
			rows := owned[id]
			group.Runs = append(group.Runs, id)
			created := nativeText(rows["runs"][0]["createdAt"])
			if value, err := time.Parse(time.RFC3339Nano, created); err == nil {
				oldest, err := time.Parse(time.RFC3339Nano, group.OldestCreatedAt)
				if err != nil || value.Before(oldest) {
					group.OldestCreatedAt = created
				}
			}
			for collection, values := range rows {
				batch[collection] = append(batch[collection], values...)
			}
			for _, issue := range rows["issues"] {
				group.Issues = append(group.Issues, nativeText(issue["id"]))
			}
		}
		group.Shards, err = writeDashboardShardGroup(ctx, cache, batch, manifest)
		if err != nil {
			return err
		}
		index.Groups = append(index.Groups, group)
	}
	content, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if _, err := writeDashboardImmutablePayload(ctx, cache, dashboardIndexPath, ".json", content, manifest); err != nil {
		return err
	}
	if err := publishDashboardSnapshotCheckpoint(ctx, cache, manifest, old, merged, bundles); err != nil {
		return err
	}
	// Only retire files from the previous committed manifest. A failed prepare
	// leaves the old manifest and its evidence intact.
	root, err := os.OpenRoot(cache)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for name := range old {
		if _, exists := manifest[name]; !exists && (dashboardCanonicalShard(name) || strings.HasPrefix(name, dashboardIndexPath)) {
			if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if bundles == nil {
		return cleanupDashboardSnapshotOrphans(ctx, cache, manifest)
	}
	return nil
}

func writeDashboardShardGroup(ctx context.Context, cache string, batch map[string][]map[string]any, manifest map[string]string) ([]string, error) {
	var names []string
	stores := slices.Sorted(maps.Keys(batch))
	for _, phase := range []string{"runs", "records"} {
		var lines [][]byte
		size := 0
		flush := func() error {
			var content bytes.Buffer
			if err := json.NewEncoder(&content).Encode(map[string]any{"kind": "metadata", "phase": phase, "records": len(lines)}); err != nil {
				return err
			}
			for _, line := range lines {
				content.Write(line)
				content.WriteByte('\n')
			}
			name, err := writeDashboardImmutablePayload(ctx, cache, "gh-aw-logs-"+phase+"/", ".jsonl", content.Bytes(), manifest)
			if err != nil {
				return err
			}
			names = append(names, name)
			lines, size = nil, 0
			return nil
		}
		for _, store := range stores {
			runPhase := slices.Contains([]string{"campaigns", "repositories", "workflows", "runs", "experiments", "experimentAssignments"}, store)
			if runPhase != (phase == "runs") {
				continue
			}
			for _, row := range batch[store] {
				line, err := json.Marshal(map[string]any{"kind": "record", "collection": store, "record": row})
				if err != nil {
					return nil, err
				}
				if size+len(line)+1+128 > dashboardShardBytes && len(lines) > 0 {
					if err := flush(); err != nil {
						return nil, err
					}
				}
				lines, size = append(lines, line), size+len(line)+1
			}
		}
		if len(lines) > 0 || phase == "runs" && len(names) == 0 {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	return names, nil
}

func writeDashboardImmutablePayload(ctx context.Context, cache, prefix, suffix string, content []byte, manifest map[string]string) (name string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	name = prefix + hash + suffix
	root, err := os.OpenRoot(cache)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return "", err
	}
	if existing, err := root.ReadFile(name); err == nil {
		if !bytes.Equal(existing, content) {
			return "", fmt.Errorf("local dashboard immutable payload checksum mismatch for %s", name)
		}
		manifest[name] = hash
		return name, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := os.CreateTemp(cache, ".dashboard-payload-")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, os.Remove(file.Name())) }()
	_, writeErr := file.Write(content)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return "", err
	}
	if err := root.Link(filepath.Base(file.Name()), name); errors.Is(err, os.ErrExist) {
		if _, err := readDashboardVerifiedPayload(ctx, cache, name, hash); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	manifest[name] = hash
	return name, nil
}

// The manifest is the commit pointer. If a process stops between replacing a
// fixed-name alias and committing the manifest, restore the selected old bytes.
func readDashboardCheckpointPayload(ctx context.Context, cache, name, expected string) ([]byte, error) {
	if name != "inventory-sources.json" && name != "run-bundles.json" || len(expected) != 64 {
		return nil, errors.New("local dashboard checkpoint payload is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(cache)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	content, err := root.ReadFile(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	hash := sha256.Sum256(content)
	if err == nil && hex.EncodeToString(hash[:]) == expected {
		return content, ctx.Err()
	}
	content, err = readDashboardVerifiedPayload(ctx, cache, dashboardCheckpointsPath+expected+".json", expected)
	if err != nil {
		return nil, fmt.Errorf("recover local dashboard checkpoint %s: %w", name, err)
	}
	if err := replaceDashboardSnapshotAlias(ctx, cache, name, content); err != nil {
		return nil, err
	}
	return content, nil
}

func replaceDashboardSnapshotAlias(ctx context.Context, cache, name string, content []byte) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(cache, ".dashboard-checkpoint-")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := file.Write(content)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	root, err := os.OpenRoot(cache)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return root.Rename(filepath.Base(file.Name()), name)
}

func publishDashboardSnapshotCheckpoint(ctx context.Context, cache string, manifest, previous map[string]string, batch map[string][]map[string]any, bundles []byte) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	inventory, err := dashboardSnapshotInventory(batch)
	if err != nil {
		return err
	}
	payloads := map[string][]byte{"inventory-sources.json": inventory}
	names := []string{"inventory-sources.json"}
	for _, name := range []string{"inventory-sources.json", "run-bundles.json"} {
		if expected := previous[name]; expected != "" {
			content, err := readDashboardCheckpointPayload(ctx, cache, name, expected)
			if err != nil {
				return err
			}
			if _, err := writeDashboardImmutablePayload(ctx, cache, dashboardCheckpointsPath, ".json", content, map[string]string{}); err != nil {
				return err
			}
		}
	}
	delete(manifest, "run-bundles.json")
	if bundles != nil {
		payloads["run-bundles.json"] = bundles
		names = append(names, "run-bundles.json")
	}
	for name, content := range payloads {
		hash := sha256.Sum256(content)
		manifest[name] = hex.EncodeToString(hash[:])
		if _, err := writeDashboardImmutablePayload(ctx, cache, dashboardCheckpointsPath, ".json", content, map[string]string{}); err != nil {
			return err
		}
	}
	payloads["payload-hashes.json"], err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	names = append(names, "payload-hashes.json")
	for _, name := range names {
		if err := replaceDashboardSnapshotAlias(ctx, cache, name, payloads[name]); err != nil {
			// Cancellation must not prevent restoring the last committed aliases.
			restoreCtx := context.WithoutCancel(ctx)
			for _, alias := range []string{"inventory-sources.json", "run-bundles.json"} {
				if expected := previous[alias]; expected != "" {
					_, restoreErr := readDashboardCheckpointPayload(restoreCtx, cache, alias, expected)
					err = errors.Join(err, restoreErr)
				}
			}
			return err
		}
	}
	for _, name := range []string{"inventory-sources.json", "run-bundles.json"} {
		if hash := previous[name]; hash != "" && hash != manifest[name] {
			if err := os.Remove(filepath.Join(cache, dashboardCheckpointsPath, hash+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// Full reconciliation, not the small-bundle path, collects preparations left
// unreferenced by an interrupted publisher.
func cleanupDashboardSnapshotOrphans(ctx context.Context, cache string, manifest map[string]string) error {
	root, err := os.OpenRoot(cache)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, directory := range []string{"gh-aw-logs-runs", "gh-aw-logs-records", strings.TrimSuffix(dashboardIndexPath, "/"), strings.TrimSuffix(dashboardCheckpointsPath, "/")} {
		file, err := root.Open(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		entries, err := file.ReadDir(-1)
		if err := errors.Join(err, file.Close()); err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := filepath.ToSlash(filepath.Join(directory, entry.Name()))
			suffix := filepath.Ext(entry.Name())
			if suffix != ".jsonl" && suffix != ".json" {
				continue
			}
			hash := strings.TrimSuffix(entry.Name(), suffix)
			if !entry.Type().IsRegular() || len(hash) != 64 || manifest[name] != "" {
				continue
			}
			if _, err := hex.DecodeString(hash); err != nil {
				continue
			}
			if directory+"/" == dashboardCheckpointsPath && (hash == manifest["inventory-sources.json"] || hash == manifest["run-bundles.json"]) {
				continue
			}
			if err := root.Remove(name); err != nil {
				return err
			}
		}
	}
	return nil
}
