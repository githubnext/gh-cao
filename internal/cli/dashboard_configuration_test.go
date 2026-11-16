package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func dashboardConfigurationTestPolicy(t *testing.T, path string, repositories ...string) {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"version": 1, "gh-aw-version": "v1.2.3",
		"control-plane": map[string]any{
			"scope":    map[string]any{"allowed-owners": []string{"octo"}, "allowed-repositories": repositories},
			"defaults": map[string]any{"mode": "review"}, "campaigns": map[string]any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardConfigurationReloadPreservesEvidenceAndExplicitScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	dashboardConfigurationTestPolicy(t, path, "octo/first")
	first, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &dashboardConfiguration{path: path}
	configuration.current.Store(first)
	ctx := context.WithValue(t.Context(), dashboardConfigurationKey{}, configuration)
	cache := t.TempDir()
	if err := initializeDashboardSnapshot(ctx, cache, []string{"octo/observed"}, dashboardOptions{window: time.Hour}); err != nil {
		t.Fatal(err)
	}
	before, err := readDashboardSnapshotManifest(ctx, cache)
	if err != nil {
		t.Fatal(err)
	}
	dashboardConfigurationTestPolicy(t, path, "octo/first", "octo/second")
	second, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration.current.Store(second)
	if err := refreshDashboardConfigurationSnapshot(ctx, cache); err != nil {
		t.Fatal(err)
	}
	after, err := readDashboardSnapshotManifest(ctx, cache)
	if err != nil {
		t.Fatal(err)
	}
	for name, digest := range before {
		if name != "inventory-sources.json" && after[name] != digest {
			t.Fatalf("configuration reload changed retained evidence: %s", name)
		}
	}
	content, err := readDashboardCheckpointPayload(ctx, cache, "inventory-sources.json", after["inventory-sources.json"])
	if err != nil {
		t.Fatal(err)
	}
	var inventory map[string]struct {
		Rows     []map[string]any `json:"rows"`
		Metadata map[string]any   `json:"metadata"`
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory["repositories"].Rows) != 2 || inventory["repositories"].Rows[1]["repository"] != "second" {
		t.Fatalf("configured repositories did not reload: %s", content)
	}
	if _, err := readDashboardStatus(ctx, cache, []string{"octo/observed"}); err != nil {
		t.Fatalf("configuration widened or replaced explicit collection scope: %v", err)
	}
	dashboardConfigurationTestPolicy(t, path, "octo/second")
	removed, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration.current.Store(removed)
	if err := refreshDashboardConfigurationSnapshot(ctx, cache); err != nil {
		t.Fatal(err)
	}
	manifest, err := readDashboardSnapshotManifest(ctx, cache)
	if err != nil {
		t.Fatal(err)
	}
	content, err = readDashboardCheckpointPayload(ctx, cache, "inventory-sources.json", manifest["inventory-sources.json"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &inventory); err != nil || len(inventory["repositories"].Rows) != 1 {
		t.Fatalf("removed repository stayed configured: %s %v", content, err)
	}
}

func TestDashboardConfigurationKeepsCurrentRepositoryBuiltIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	dashboardConfigurationTestPolicy(t, path, "octo/current", "octo/configured")
	snapshot, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &dashboardConfiguration{path: path, builtins: []string{"octo/current", "OCTO/CURRENT"}}
	configuration.current.Store(snapshot)
	if len(configuration.repositoryRows()) != 2 {
		t.Fatal("configured current repository was duplicated")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	missing, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration.current.Store(missing)
	if !reflect.DeepEqual(configuration.repositoryRows(), []map[string]any{{"organization": "octo", "repository": "current"}}) {
		t.Fatal("current repository disappeared with the policy")
	}
}

func TestDashboardConfigurationRejectsInvalidPolicyAndRetriesPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	dashboardConfigurationTestPolicy(t, path, "octo/first")
	initial, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{"version":`, `{"version":1,"control-plane":{"unexpected":true}}`, `{"version":1,"version":1}`} {
		if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readDashboardConfiguration(path); err == nil {
			t.Fatal("invalid configuration was accepted")
		}
	}
	configuration := &dashboardConfiguration{path: path}
	configuration.current.Store(initial)
	dashboardConfigurationTestPolicy(t, path, "octo/second")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	attempts := make(chan bool, 2)
	go func() {
		defer close(done)
		failed := false
		configuration.watch(ctx, func() error {
			attempts <- failed
			if !failed {
				failed = true
				return errors.New("publication temporarily unavailable")
			}
			return nil
		}, io.Discard)
	}()
	defer func() { cancel(); <-done }()
	for _, retry := range []bool{false, true} {
		select {
		case actual := <-attempts:
			if actual != retry {
				t.Fatal("configuration publication retry was lost")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("configuration watcher did not publish and retry")
		}
	}
	if !reflect.DeepEqual(configuration.current.Load().repositories, []map[string]any{{"organization": "octo", "repository": "second"}}) {
		t.Fatal("watcher did not keep the latest validated configuration")
	}
}
