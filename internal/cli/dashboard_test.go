package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func dashboardTestGH(t *testing.T, now time.Time) ghExecutor {
	t.Helper()
	return func(_ context.Context, args ...string) (string, string, error) {
		encode := func(value any) (string, string, error) {
			content, err := json.Marshal(value)
			return string(content), "", err
		}
		repository := "octo/api"
		for index, arg := range args {
			if arg == "--repo" && index+1 < len(args) {
				repository = strings.TrimPrefix(args[index+1], "github.com/")
			}
		}
		if args[0] == "repo" {
			if len(args) > 2 && args[2] != "--json" {
				repository = strings.TrimPrefix(args[2], "github.com/")
			}
			return encode(map[string]any{"nameWithOwner": repository, "url": "https://github.com/" + repository, "id": "R_" + repository})
		}
		switch args[0] + " " + args[1] {
		case "workflow list":
			return encode([]dashboardWorkflow{{42, "Agent", ".github/workflows/agent.lock.yml", "active"},
				{43, "Tests", ".github/workflows/tests.yml", "active"}})
		case "run list":
			return encode([]map[string]any{
				{"databaseId": 123, "workflowDatabaseId": 42, "workflowName": "Agent", "attempt": 1,
					"createdAt": now.Add(-time.Hour).Format(time.RFC3339Nano), "startedAt": now.Add(-time.Hour).Format(time.RFC3339Nano),
					"updatedAt": now.Format(time.RFC3339Nano),
					"status":    "in_progress", "conclusion": "", "displayTitle": "Agent work", "event": "schedule",
					"url": "https://github.com/" + repository + "/actions/runs/123"},
				{"databaseId": 124, "workflowDatabaseId": 43, "attempt": 1, "createdAt": now.Format(time.RFC3339Nano)},
			})
		default:
			t.Fatalf("unexpected GitHub command: %v", args)
			return "", "", nil
		}
	}
}

func dashboardTestLogs(t *testing.T) dashboardLogsCollector {
	t.Helper()
	return func(_ context.Context, request dashboardLogRequest) error {
		if request.Count < 1 || request.Directory == "" || request.StartDate == "" {
			t.Fatalf("unbounded log request: %+v", request)
		}
		content, err := json.Marshal(map[string]any{
			"schema_version": 5, "kind": "run",
			"run": map[string]any{"run_id": 123, "run_attempt": "1", "repository": request.Repository,
				"updated_at": time.Now().UTC().Format(time.RFC3339Nano), "status": "completed",
				"agent_id": "copilot", "engine_id": "copilot", "model_id": "old", "resolved_model": "observed",
				"token_usage_summary": map[string]any{"total_aic": 2.5, "total_input_tokens": 100}},
		})
		if err != nil {
			return err
		}
		return os.WriteFile(request.Cache, append(content, '\n'), 0o600)
	}
}

func TestDashboardValidationBeforeCollection(t *testing.T) {
	for _, args := range [][]string{
		{"--repo", "octo/*"}, {"--repo", "octo/../api"}, {"--repo", "evil.example/octo/api"},
		{"--repo", "octo/api", "--window", "49h"}, {"--repo", "octo/api", "--duration", "0s"},
		{"--repo", "octo/api", "--count", "0"}, {"--repo", "octo/api", "--count", "1001"},
		{"--repo", "octo/api", "--listen", "0.0.0.0:8080"}, {"--repo", "octo/api", "--refresh", "1s"},
		{"--repo", "octo/api", "--repo", "octo/web", "--logs-dir", "."},
		{"--server-executable", "server"}, {"run", "rerun", "--repo", "octo/api", "../123"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			gh := func(context.Context, ...string) (string, string, error) {
				t.Fatal("invalid input must not contact GitHub")
				return "", "", nil
			}
			logs := func(context.Context, dashboardLogRequest) error {
				t.Fatal("invalid input must not collect")
				return nil
			}
			cmd := dashboardCommand("test", gh, logs)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetArgs(args)
			if err := cmd.ExecuteContext(t.Context()); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}

func TestDashboardRepositoryResolution(t *testing.T) {
	cmd := &cobra.Command{}
	options := &dashboardOptions{window: dashboardMaximumDuration, count: 200}
	repositories, first, err := resolveDashboardOptions(cmd, options, dashboardTestGH(t, time.Now()))
	if err != nil || !reflect.DeepEqual(repositories, []string{"octo/api"}) {
		t.Fatalf("current repository resolution: %v %v", repositories, err)
	}
	options.repositories = []string{"OCTO/API", "octo/api"}
	repositories, second, err := resolveDashboardOptions(cmd, options, nil)
	if err != nil || first != second || !reflect.DeepEqual(repositories, []string{"octo/api"}) {
		t.Fatalf("explicit repository scope was not canonical/deduplicated: %v %v", repositories, err)
	}
}

func TestDashboardCollectionCanonicalQueriesAndRefresh(t *testing.T) {
	now := time.Now().UTC().Add(-time.Second)
	cache := filepath.Join(t.TempDir(), "private cache #1")
	options := dashboardOptions{window: dashboardMaximumDuration, count: 200}
	repositories := []string{"octo/api", "octo/web"}
	metadata, err := collectDashboard(t.Context(), repositories, cache, options, dashboardTestGH(t, now), dashboardTestLogs(t))
	if err != nil {
		t.Fatal(err)
	}
	if metadata["completeness"] != "partial" {
		t.Fatal("bounded evidence must remain partial")
	}
	db, err := openNativeSnapshot(filepath.Join(cache, "gh-aw-logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	runs, err := readNativeCollection(t.Context(), db, "runs")
	if err != nil || len(runs) != 2 {
		t.Fatalf("repository-scoped IDs collided or ordinary Actions were counted: %v %v", runs, err)
	}
	for _, run := range runs {
		if run["status"] != "in_progress" || run["aicTotal"] != 2.5 || run["resolvedModel"] != "observed" {
			t.Fatalf("current GitHub state must win over cached enrichment: %v", run)
		}
		if run["outputTokens"] != nil {
			t.Fatal("missing usage was fabricated")
		}
	}
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	result, err := catalog.runQuery(t.Context(), db, "runs-table", nil, 100)
	if err != nil || len(result["rows"].([]map[string]any)) != 2 || nativeObject(result["metadata"])["availability"] == "unavailable" {
		t.Fatalf("production canonical Dashboard Language query cannot serve local runs: %v %v", result, err)
	}
	if quality := nativeObject(result["metadata"]); quality["completeness"] != "partial" || quality["as-of"] != metadata["windowEnd"] {
		t.Fatalf("local acquisition coverage/clock was lost in the query boundary: %v", quality)
	}
	if _, err := readDashboardStatus(t.Context(), cache, []string{"octo/api"}); err == nil {
		t.Fatal("shared cache scope widened or narrowed without rejection")
	}
	before, err := os.ReadFile(filepath.Join(cache, "gh-aw-logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	fail := func(context.Context, dashboardLogRequest) error { return errors.New("unavailable") }
	if _, err := collectDashboard(t.Context(), repositories, cache, options, dashboardTestGH(t, now), fail); err == nil {
		t.Fatal("failed collection did not propagate")
	}
	after, _ := os.ReadFile(filepath.Join(cache, "gh-aw-logs.sqlite"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed collection modified the previous snapshot")
	}
	updated, err := collectDashboard(t.Context(), repositories, cache, options, dashboardTestGH(t, now), dashboardTestLogs(t))
	if err != nil || updated["createdAt"] == metadata["createdAt"] {
		t.Fatalf("refresh did not commit a fresh observation: %v", err)
	}
	status, err := readDashboardStatus(t.Context(), cache, repositories)
	if err != nil || status["createdAt"] != updated["createdAt"] {
		t.Fatalf("refresh metadata is not atomically visible: %v %v", status, err)
	}
}

func TestDashboardCacheFailsClosed(t *testing.T) {
	cache := t.TempDir()
	options := dashboardOptions{window: dashboardMaximumDuration, count: 2}
	for _, name := range []string{"foreign database", "locked collector"} {
		t.Run(name, func(t *testing.T) {
			directory := filepath.Join(cache, name)
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if name == "foreign database" {
				writeTestFile(t, filepath.Join(directory, "gh-aw-logs.sqlite"), "operator data")
			} else {
				if err := os.Mkdir(filepath.Join(directory, "collection.lock"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			gh := func(context.Context, ...string) (string, string, error) {
				t.Fatal("must fail before remote reads")
				return "", "", nil
			}
			if _, err := collectDashboard(t.Context(), []string{"octo/api"}, directory, options, gh, nil); err == nil {
				t.Fatal("unsafe cache accepted")
			}
		})
	}
}

func TestDashboardRunOperationIsExplicitAndScoped(t *testing.T) {
	cache := t.TempDir()
	options := dashboardOptions{window: dashboardMaximumDuration, count: 2}
	if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache, options, dashboardTestGH(t, time.Now().Add(-time.Second)), dashboardTestLogs(t)); err != nil {
		t.Fatal(err)
	}
	calls := [][]string{}
	gh := func(_ context.Context, args ...string) (string, string, error) {
		calls = append(calls, args)
		return "", "", nil
	}
	for _, id := range []string{"999", "123"} {
		cmd := dashboardCommand("test", gh, nil)
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs([]string{"run", "cancel", id, "--repo", "octo/api", "--cache-dir", cache})
		err := cmd.ExecuteContext(t.Context())
		if id == "999" && err == nil || id == "123" && err != nil {
			t.Fatalf("run operation id=%s err=%v", id, err)
		}
	}
	if !reflect.DeepEqual(calls, [][]string{{"run", "cancel", "123", "--repo", "github.com/octo/api"}}) {
		t.Fatalf("unscoped or implicit mutation: %v", calls)
	}
}

func TestDashboardLogCacheRejectsCrossScopeAndUnknownVersions(t *testing.T) {
	for _, content := range []string{
		`{"schema_version":5,"kind":"run","run":{"repository":"other/api","run_id":123}}`,
		`{"schema_version":99,"kind":"run","run":{"repository":"octo/api","run_id":123}}`,
		`{not JSON}`,
	} {
		filename := filepath.Join(t.TempDir(), "logs.jsonl")
		writeTestFile(t, filename, content)
		if _, err := readDashboardLogCache(filename, "octo/api"); err == nil {
			t.Fatalf("accepted invalid cache: %s", content)
		}
	}
}

func TestDashboardServerEnvironmentHasNoAuthorityOrTelemetry(t *testing.T) {
	for _, name := range []string{"CAO_POLICY_PATH", "CAO_POSTGRES_URL", "GH_TOKEN", "GITHUB_TOKEN", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		t.Setenv(name, "must-not-reach-server")
	}
	for _, value := range dashboardServerEnvironment() {
		if strings.Contains(value, "must-not-reach-server") {
			t.Fatal("read-only server inherited credentials, authority, or telemetry")
		}
	}
}

func TestDashboardCachedUsageUnionIsBoundedAndAttemptSafe(t *testing.T) {
	now := time.Now().UTC()
	raw := []map[string]any{{"databaseId": 1, "workflowDatabaseId": 42, "attempt": 2,
		"status": "in_progress", "createdAt": now.Add(-time.Hour).Format(time.RFC3339Nano)}}
	cached := dashboardCachedLogs{Runs: map[string]map[string]any{}, Enriched: map[string]map[string]any{
		"1": {"run_id": 1, "run_attempt": 1, "created_at": now.Format(time.RFC3339Nano), "aic": 999},
		"2": {"run_id": 2, "run_attempt": "1", "workflow_path": ".github/workflows/agent.lock.yml",
			"created_at": now.Add(-time.Hour).Format(time.RFC3339Nano), "status": "completed", "aic": 5.0},
		"3": {"run_id": 3, "run_attempt": "1", "workflow_path": ".github/workflows/agent.lock.yml",
			"created_at": now.Add(-2 * time.Hour).Format(time.RFC3339Nano), "aic": 10.0},
	}}
	workflows := []dashboardWorkflow{{42, "Agent", ".github/workflows/agent.lock.yml", "active"}}
	merged := mergeDashboardCachedRuns(raw, workflows, cached, 2)
	if len(merged) != 2 {
		t.Fatalf("usage sample was unbounded or not combined with active work: %v", merged)
	}
	evidence := []dashboardRepositoryEvidence{{Repository: "octo/api", Workflows: workflows, Runs: merged, Enriched: cached.Enriched}}
	batch, err := projectDashboardEvidence(evidence, now.Add(-48*time.Hour), now)
	if err == nil {
		batch, err = hydrateDashboardEvidence(t.Context(), evidence, batch, now)
	}
	if err != nil || len(batch["runs"]) != 2 {
		t.Fatalf("usage union did not project: %v %v", batch, err)
	}
	byID := map[string]map[string]any{}
	for _, run := range batch["runs"] {
		byID[nativeScalar(run["githubRunId"])] = run
	}
	if byID["1"]["aicTotal"] != nil {
		t.Fatal("usage from a previous run attempt was attached")
	}
	if byID["2"]["aicTotal"] != 5.0 {
		t.Fatal("cached completed usage was discarded")
	}
	raw[0]["attempt"] = "invalid"
	if _, err := projectDashboardEvidence([]dashboardRepositoryEvidence{{Repository: "octo/api", Workflows: workflows, Runs: raw}}, now.Add(-48*time.Hour), now); err == nil {
		t.Fatal("invalid attempt accepted")
	}
}

func TestDashboardEmptyActivityDoesNotRequireArtifacts(t *testing.T) {
	for _, absent := range []string{"runs", "agentic workflows"} {
		t.Run(absent, func(t *testing.T) {
			base := dashboardTestGH(t, time.Now())
			gh := func(ctx context.Context, args ...string) (string, string, error) {
				if absent == "runs" && args[0] == "run" ||
					absent == "agentic workflows" && args[0] == "workflow" {
					return "[]", "", nil
				}
				return base(ctx, args...)
			}
			logs := func(context.Context, dashboardLogRequest) error {
				t.Fatal("empty activity must not require a gh-aw artifact download")
				return nil
			}
			metadata, err := collectDashboard(t.Context(), []string{"octo/api"}, t.TempDir(),
				dashboardOptions{window: dashboardMaximumDuration, count: 2}, gh, logs)
			if err != nil || metadata["counts"].(map[string]int)["runs"] != 0 {
				t.Fatalf("empty activity failed to publish: %v %v", metadata, err)
			}
		})
	}
}

func TestDashboardArtifactCachesKeepExactScopeAndOwnership(t *testing.T) {
	for _, external := range []bool{false, true} {
		cache := t.TempDir()
		options := dashboardOptions{window: dashboardMaximumDuration, count: 2}
		repositories := []string{"a--b/c", "a/b--c"}
		if external {
			options.logs = t.TempDir()
			repositories = repositories[:1]
		}
		requests := []dashboardLogRequest{}
		writeLogs := dashboardTestLogs(t)
		logs := func(ctx context.Context, request dashboardLogRequest) error {
			requests = append(requests, request)
			return writeLogs(ctx, request)
		}
		if _, err := collectDashboard(t.Context(), repositories, cache, options, dashboardTestGH(t, time.Now()), logs); err != nil {
			t.Fatal(err)
		}
		for _, request := range requests {
			if request.ManagedCache == external {
				t.Fatalf("external artifact ownership was changed: %+v", request)
			}
		}
		if !external && (requests[0].Directory == requests[1].Directory || requests[0].Cache == requests[1].Cache) {
			t.Fatal("distinct repositories shared an artifact cache")
		}
	}
}
