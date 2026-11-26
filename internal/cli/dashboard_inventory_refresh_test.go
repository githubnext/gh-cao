package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func dashboardInventoryRefreshTestSnapshot(t *testing.T, ctx context.Context, repositories []string) string {
	t.Helper()
	cache := t.TempDir()
	options := dashboardOptions{window: 4 * time.Hour, count: 10}
	if err := initializeDashboardSnapshot(ctx, cache, repositories, options); err != nil {
		t.Fatal(err)
	}
	metadata, err := readDashboardStatus(ctx, cache, repositories)
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339Nano, nativeText(metadata["windowEnd"]))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := projectDashboardEvidence([]dashboardRepositoryEvidence{{
		Repository: repositories[0], GithubID: "R_" + repositories[0],
		Workflows:        []dashboardWorkflow{{42, "Old agent", ".github/workflows/agent.lock.yml", "disabled_manually"}},
		AgenticWorkflows: map[string]bool{"agent": true},
		Runs: []map[string]any{{
			"databaseId": 123, "workflowDatabaseId": 42, "attempt": 2,
			"createdAt": now.Add(-2 * time.Hour).Format(time.RFC3339Nano),
			"updatedAt": now.Add(-time.Hour).Format(time.RFC3339Nano),
			"status":    "completed", "conclusion": "success", "displayTitle": "Retained history",
		}},
	}}, now.Add(-options.window), now)
	if err != nil {
		t.Fatal(err)
	}
	remaining := 4321
	checkpoint, err := dashboardIngestionRecord(dashboardIngestion{
		ID: "local-dashboard-ingestion", Kind: "local-dashboard-ingestion",
		CreatedAt: now.Format(time.RFC3339Nano), Status: "paused", Message: "History remains pending",
		Repository: repositories[0], ScannedHours: 2, TargetHours: 4, Remaining: &remaining,
		Reserve: 1000, ResetAt: now.Add(time.Hour).Format(time.RFC3339),
		NextAt: now.Add(time.Minute).Format(time.RFC3339), NextRepo: 1,
		Cursors: map[string]dashboardHistoryCursor{repositories[0]: {
			Start: now.Add(-4 * time.Hour), End: now, Through: now.Add(-time.Hour), Page: 3,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	batch["transactions"] = []map[string]any{metadata, checkpoint}
	if err := writeDashboardSnapshot(ctx, cache, batch, true); err != nil {
		t.Fatal(err)
	}
	if rows := readDashboardPublishedCollection(t, cache, "runs"); len(rows) != 1 {
		t.Fatalf("production fixture did not retain its historical run: %v", rows)
	}
	return cache
}

func TestDashboardInventoryRefreshBeforeHistoryCollection(t *testing.T) {
	t.Setenv("GH_HOST", "github.com")
	ctx, cache := t.Context(), t.TempDir()
	repositories := []string{"octo/api"}
	options := dashboardOptions{window: time.Hour, count: 10}
	if err := initializeDashboardSnapshot(ctx, cache, repositories, options); err != nil {
		t.Fatal(err)
	}
	before, err := readDashboardIngestion(ctx, cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshDashboardInventory(ctx, repositories, cache, options, dashboardTestGH(t, time.Now())); err != nil {
		t.Fatal(err)
	}
	after, err := readDashboardIngestion(ctx, cache)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("inventory-only refresh fabricated or invalidated a history checkpoint: %+v %v", after, err)
	}
	if rows := readDashboardPublishedCollection(t, cache, "workflows"); len(rows) != 1 {
		t.Fatalf("initial inventory refresh did not publish workflows: %v", rows)
	}
	if rows := readDashboardPublishedCollection(t, cache, "runs"); len(rows) != 0 {
		t.Fatalf("initial inventory refresh collected run history: %v", rows)
	}
}

func TestDashboardInventoryRefreshReadsAllScopesWithoutRunsOrCursorChanges(t *testing.T) {
	t.Setenv("GH_HOST", "github.com")
	repositories := []string{"octo/api", "octo/web"}
	options := dashboardOptions{window: time.Hour, count: 10}
	path := filepath.Join(t.TempDir(), "cao.json")
	dashboardConfigurationTestPolicy(t, path, "octo/configured-only")
	snapshot, err := readDashboardConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &dashboardConfiguration{path: path, builtins: []string{"octo/api", "octo/current"}}
	configuration.current.Store(snapshot)
	ctx := context.WithValue(t.Context(), dashboardConfigurationKey{}, configuration)
	cache := dashboardInventoryRefreshTestSnapshot(t, ctx, repositories)
	before, err := readDashboardIngestion(ctx, cache)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := readDashboardStatus(ctx, cache, repositories)
	if err != nil {
		t.Fatal(err)
	}
	runs := readDashboardPublishedCollection(t, cache, "runs")
	base := dashboardTestGH(t, time.Now())
	seen := map[string]map[string]int{}
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		if args[0] == "api" {
			if !reflect.DeepEqual(args, []string{"api", "--hostname", "github.com", "rate_limit"}) {
				t.Fatalf("inventory refresh expanded beyond quota reads: %v", args)
			}
			return base(ctx, args...)
		}
		repository := ""
		if args[0] == "repo" {
			repository = strings.TrimPrefix(args[2], "github.com/")
		} else if args[0] == "workflow" || args[0] == "aw" && args[1] == "list" {
			for index, arg := range args {
				if arg == "--repo" {
					repository = strings.TrimPrefix(args[index+1], "github.com/")
				}
			}
		} else {
			t.Fatalf("inventory refresh expanded into history or writes: %v", args)
		}
		if repository != "octo/api" && repository != "octo/web" && repository != "octo/current" {
			t.Fatalf("configuration-only target triggered a GitHub read: %v", args)
		}
		if seen[repository] == nil {
			seen[repository] = map[string]int{}
		}
		seen[repository][args[0]]++
		return base(ctx, args...)
	}
	for pass := 1; pass <= 2; pass++ {
		if err := refreshDashboardInventory(ctx, repositories, cache, options, gh); err != nil {
			t.Fatal(err)
		}
		for _, repository := range []string{"octo/api", "octo/web", "octo/current"} {
			if !reflect.DeepEqual(seen[repository], map[string]int{"repo": pass, "workflow": pass, "aw": pass}) {
				t.Fatalf("refresh did not reread every inventory for %s: %v", repository, seen)
			}
		}
	}
	after, err := readDashboardIngestion(ctx, cache)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("inventory refresh changed history checkpoint: before=%+v after=%+v error=%v", before, after, err)
	}
	afterMetadata, err := readDashboardStatus(ctx, cache, repositories)
	if err != nil || !reflect.DeepEqual(metadata, afterMetadata) {
		t.Fatalf("inventory refresh changed local-dashboard cutoff/coverage metadata: %v", err)
	}
	if afterRuns := readDashboardPublishedCollection(t, cache, "runs"); !reflect.DeepEqual(runs, afterRuns) {
		t.Fatalf("inventory refresh replaced or evicted retained history: %v", afterRuns)
	}
	if rows := readDashboardPublishedCollection(t, cache, "workflows"); len(rows) != 3 {
		t.Fatalf("production inventory publication missed workflows: %v", rows)
	} else {
		for _, row := range rows {
			if row["name"] != "Agent" || row["state"] != "active" || row["role"] != "standalone" {
				t.Fatalf("fresh inventory did not replace prior workflow state: %v", row)
			}
		}
	}
}

func TestDashboardInventoryRefreshQuotaFailureRetainsPreviousSnapshot(t *testing.T) {
	for _, scenario := range []string{
		"quota preflight", "quota after first repository", "workflow read error",
		"wrong repository", "null workflow inventory", "truncated workflow inventory", "invalid agentic inventory",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("GH_HOST", "github.com")
			ctx := t.Context()
			repositories := []string{"octo/api", "octo/web"}
			cache := dashboardInventoryRefreshTestSnapshot(t, ctx, repositories)
			before, err := readDashboardArtifactBatch(ctx, cache)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := readDashboardStatus(ctx, cache, repositories)
			if err != nil {
				t.Fatal(err)
			}
			base := dashboardTestGH(t, time.Now())
			completedFirst := false
			gh := func(ctx context.Context, args ...string) (string, string, error) {
				if args[0] == "api" && (scenario == "quota preflight" || scenario == "quota after first repository" && completedFirst) {
					return fmt.Sprintf(`{"resources":{"core":{"limit":5000,"used":4000,"remaining":1000,"reset":%d}}}`,
						time.Now().Add(time.Hour).Unix()), "", nil
				}
				if args[0] == "aw" && args[1] == "list" && args[3] == "octo/api" {
					completedFirst = true
				}
				if args[0] == "repo" && args[2] == "github.com/octo/web" && scenario == "wrong repository" {
					return `{"nameWithOwner":"octo/other","url":"https://github.com/octo/other","id":"R_other"}`, "", nil
				}
				if args[0] == "workflow" && args[3] == "github.com/octo/web" {
					switch scenario {
					case "workflow read error":
						return "", "", errors.New("workflow inventory unavailable")
					case "null workflow inventory":
						return "null", "", nil
					case "truncated workflow inventory":
						content, err := json.Marshal(make([]dashboardWorkflow, 1000))
						return string(content), "", err
					}
				}
				if args[0] == "aw" && args[3] == "octo/web" && scenario == "invalid agentic inventory" {
					return `[{"workflow":"../outside"}]`, "", nil
				}
				return base(ctx, args...)
			}
			err = refreshDashboardInventory(ctx, repositories, cache, dashboardOptions{window: time.Hour, count: 10}, gh)
			if err == nil {
				t.Fatal("failed inventory refresh was acknowledged as success")
			}
			if strings.HasPrefix(scenario, "quota") && !errors.Is(err, errDashboardQuota) {
				t.Fatalf("quota pause was not reported: %v", err)
			}
			if scenario != "quota preflight" && !completedFirst {
				t.Fatal("failure fixture did not exercise a partially acquired inventory")
			}
			afterManifest, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
			if err != nil || string(manifest) != string(afterManifest) {
				t.Fatalf("failed refresh published a new success manifest: %v", err)
			}
			after, err := readDashboardArtifactBatch(ctx, cache)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("partial inventory overwrote previous snapshot: %v", err)
			}
			afterMetadata, err := readDashboardStatus(ctx, cache, repositories)
			if err != nil || !reflect.DeepEqual(metadata, afterMetadata) {
				t.Fatalf("failed refresh advertised fresh observation metadata: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cache, "collection.lock")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed refresh did not release the collection lock: %v", err)
			}
		})
	}
}

func TestDashboardInventoryRefreshRereadsLocalCampaignDeclarations(t *testing.T) {
	t.Setenv("GH_HOST", "github.com")
	t.Chdir(t.TempDir())
	if err := os.Mkdir("care", 0o700); err != nil {
		t.Fatal(err)
	}
	declaration := filepath.Join("care", "cao.json")
	if err := os.WriteFile(declaration, []byte(`{"campaign":"care","orchestrator":"agent","workers":{"lint":"care-lint"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("care", "aw.yml"), []byte("name: Local care\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repositories := []string{"octo/api", "octo/web"}
	cache := dashboardInventoryRefreshTestSnapshot(t, t.Context(), repositories)
	options := dashboardOptions{window: time.Hour, count: 10, localRepository: "octo/api"}
	gh := dashboardTestGH(t, time.Now())
	for _, orchestrator := range []string{"agent", "other-agent"} {
		content := fmt.Sprintf(`{"campaign":"care","orchestrator":%q,"workers":{"lint":"care-lint"}}`, orchestrator)
		if err := os.WriteFile(declaration, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := refreshDashboardInventory(t.Context(), repositories, cache, options, gh); err != nil {
			t.Fatal(err)
		}
		campaigns := readDashboardPublishedCollection(t, cache, "campaigns")
		if len(campaigns) != 1 || campaigns[0]["name"] != "Local care" {
			t.Fatalf("local campaign declaration did not reach production snapshot: %v", campaigns)
		}
		for _, workflow := range readDashboardPublishedCollection(t, cache, "workflows") {
			local := workflow["repositoryId"] == "repository:"+dashboardCoordinate("octo/api")
			member := local && orchestrator == "agent"
			if member && (workflow["role"] != "orchestrator" || workflow["campaignId"] != dashboardCampaignID("care")) {
				t.Fatalf("local declaration did not define campaign membership: %v", workflow)
			}
			if !member && (workflow["role"] != "standalone" || workflow["campaignId"] != nil) {
				t.Fatalf("campaign membership was stale or leaked into the remote scope: %v", workflow)
			}
		}
	}
}

func TestDashboardInventoryRefreshAdapterIsRegisteredBeforeIngestion(t *testing.T) {
	requests := make(chan dashboardInventoryRefreshRequest)
	ctx := context.WithValue(t.Context(), dashboardInventoryRefreshKey{}, requests)
	filename, cleanup, err := startDashboardActionAdapter(ctx, []string{"octo/api"}, t.TempDir(), make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		AdapterURL       string `json:"adapterUrl"`
		InventoryRefresh bool   `json:"inventoryRefresh"`
	}
	if err := json.Unmarshal(data, &config); err != nil || !config.InventoryRefresh {
		t.Fatalf("native adapter capability not advertised: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2 {
			request := <-requests
			request.done <- errDashboardQuota
		}
	}()
	for range 2 {
		response, err := http.Post(config.AdapterURL+"/inventory/refresh", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("quota pause was acknowledged: %d", response.StatusCode)
		}
		response, err = http.Post(config.AdapterURL+"/ingest", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("repeat ingestion failed: %d", response.StatusCode)
		}
	}
	<-done
}
