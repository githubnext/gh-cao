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
	reset := time.Now().Add(time.Hour).Unix()
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
		case "api --hostname":
			if args[len(args)-1] == "rate_limit" {
				return encode(map[string]any{"resources": map[string]any{"core": dashboardQuota{
					Limit: 5000, Remaining: 5000, Reset: reset,
				}}})
			}
			return `{"total_count":0,"workflow_runs":[]}`, "", nil
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
		{"--repo", "octo/api", "--window", "169h"}, {"--repo", "octo/api", "--duration", "0s"},
		{"--repo", "octo/api", "--count", "0"}, {"--repo", "octo/api", "--count", "1001"},
		{"--repo", "octo/api", "--listen", "0.0.0.0:8080"}, {"--repo", "octo/api", "--refresh", "1s"},
		{"--repo", "octo/api", "--repo", "octo/web", "--logs-dir", "."},
		{"--server-executable", "server"},
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
	if _, err := collectDashboard(t.Context(), repositories, cache, options, dashboardTestGH(t, now), dashboardTestLogs(t)); err != nil {
		t.Fatal(err)
	}
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
	before := runs
	fail := func(context.Context, dashboardLogRequest) error { return errors.New("unavailable") }
	if _, err := collectDashboard(t.Context(), repositories, cache, options, dashboardTestGH(t, now), fail); err == nil {
		t.Fatal("failed collection did not propagate")
	}
	after, err := readNativeCollection(t.Context(), db, "runs")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed collection modified the previous evidence")
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

func TestDashboardRepositoryAccessFailureExplainsRecovery(t *testing.T) {
	base := dashboardTestGH(t, time.Now())
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		if args[0] == "api" {
			return base(ctx, args...)
		}
		return "", "", errors.New("private upstream diagnostic")
	}
	logs := func(context.Context, dashboardLogRequest) error {
		t.Fatal("repository access failure must not collect artifacts")
		return nil
	}
	_, err := collectDashboard(t.Context(), []string{"octo/api"}, t.TempDir(),
		dashboardOptions{window: dashboardMaximumDuration, count: 2}, gh, logs)
	if err == nil || !strings.Contains(err.Error(), "gh auth status --hostname github.com") ||
		!strings.Contains(err.Error(), "octo/api") || strings.Contains(err.Error(), "private upstream diagnostic") {
		t.Fatalf("repository failure lost safe recovery instructions: %v", err)
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

func TestDashboardRejectsSubcommandsBeforeCollection(t *testing.T) {
	for _, args := range [][]string{
		{"status"}, {"refresh"}, {"prompt", "runs-table"}, {"run"},
		{"run", "cancel", "123"}, {"run", "rerun", "123"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			gh := func(context.Context, ...string) (string, string, error) {
				t.Fatal("removed subcommands must not contact GitHub")
				return "", "", nil
			}
			logs := func(context.Context, dashboardLogRequest) error {
				t.Fatal("removed subcommands must not collect")
				return nil
			}
			cache := t.TempDir()
			cmd := dashboardCommand("test", gh, logs)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(append(args, "--repo", "octo/api", "--cache-dir", cache))
			if err := cmd.ExecuteContext(t.Context()); err == nil {
				t.Fatal("removed subcommand accepted")
			}
			entries, err := os.ReadDir(cache)
			if err != nil || len(entries) != 0 {
				t.Fatalf("removed subcommand changed the cache: %v %v", entries, err)
			}
		})
	}
}

func TestDashboardHelpIsServeOnly(t *testing.T) {
	cmd := dashboardCommand("test", nil, nil)
	if len(cmd.Commands()) != 0 {
		t.Fatal("dashboard must not register subcommands")
	}
	output := &bytes.Buffer{}
	cmd.SetOut(output)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Available Commands:") || strings.Contains(output.String(), "[command]") {
		t.Fatalf("dashboard help advertises subcommands: %s", output)
	}
	for _, flag := range []string{"--repo", "--refresh", "--listen"} {
		if !strings.Contains(output.String(), flag) {
			t.Fatalf("dashboard help lost serving option %s", flag)
		}
	}
}

func TestDashboardServerAssetsRequireCompleteBundle(t *testing.T) {
	files := []string{"cao-dashboard", "site/index.html", "site/dashboard.json",
		"site/src/agent/queries.generated.json", "site/src/data/queries/database.json",
		"site/src/agent/catalog.generated.json", "site/src/agent/mcp-contract.json"}
	for _, missing := range append([]string{""}, files...) {
		t.Run(missing, func(t *testing.T) {
			directory := t.TempDir()
			for _, name := range files {
				if name != missing {
					writeTestFile(t, filepath.Join(directory, filepath.FromSlash(name)), "{}")
				}
			}
			_, _, cleanup, err := dashboardServerAssets(dashboardOptions{
				server: filepath.Join(directory, "cao-dashboard"), site: filepath.Join(directory, "site"),
			})
			if cleanup != nil {
				defer cleanup()
			}
			if missing == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), filepath.Base(missing)) {
				t.Fatalf("missing bundled resource %s was not rejected: %v", missing, err)
			}
		})
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

func TestDashboardRetainsCachedEvidenceWithoutObservedAgenticRuns(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonagentic sample", true: "empty sample"}[empty], func(t *testing.T) {
			now := time.Now().UTC().Add(-time.Second)
			cache := t.TempDir()
			content, err := json.Marshal(map[string]any{
				"schema_version": 5, "kind": "run", "run": map[string]any{
					"repository": "octo/api", "run_id": 123, "run_attempt": "1",
					"workflow_path": ".github/workflows/agent.lock.yml",
					"created_at":    now.Add(-time.Hour).Format(time.RFC3339Nano),
					"started_at":    now.Add(-time.Hour).Format(time.RFC3339Nano),
					"updated_at":    now.Format(time.RFC3339Nano),
					"status":        "completed", "conclusion": "success",
					"token_usage_summary": map[string]any{"total_aic": 2.5},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cache, dashboardCoordinate("octo/api")+".jsonl"), append(content, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			base := dashboardTestGH(t, now)
			gh := func(ctx context.Context, args ...string) (string, string, error) {
				if args[0] == "run" {
					if empty {
						return "[]", "", nil
					}
					content, err := json.Marshal([]map[string]any{{
						"databaseId": 124, "workflowDatabaseId": 43, "attempt": 1,
						"createdAt": now.Format(time.RFC3339Nano),
					}})
					return string(content), "", err
				}
				return base(ctx, args...)
			}
			logs := func(context.Context, dashboardLogRequest) error {
				t.Fatal("no observed agentic workflows must not trigger artifact acquisition")
				return nil
			}
			_, err = collectDashboard(t.Context(), []string{"octo/api"}, cache,
				dashboardOptions{window: dashboardMaximumDuration, count: 1}, gh, logs)
			if err != nil {
				t.Fatal(err)
			}
			db, err := openNativeSnapshot(filepath.Join(cache, "gh-aw-logs.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			runs, err := readNativeCollection(t.Context(), db, "runs")
			if err != nil || len(runs) != 1 || nativeScalar(runs[0]["githubRunId"]) != "123" || runs[0]["aicTotal"] != 2.5 {
				t.Fatalf("valid cached run and enrichment were discarded: %v %v", runs, err)
			}
		})
	}
}

func TestDashboardListenValidation(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "127.0.0.1:65535", "[::1]:8080"} {
		if err := validateDashboardListen(address); err != nil {
			t.Fatalf("valid loopback listener %q rejected: %v", address, err)
		}
	}
	for _, address := range []string{
		"127.0.0.1:http", "127.0.0.1:65536", "127.0.0.1:-1", "127.0.0.1:", "127.0.0.1:+80",
		"localhost:8080", "0.0.0.0:8080",
	} {
		if err := validateDashboardListen(address); err == nil {
			t.Errorf("invalid loopback listener %q accepted", address)
		}
	}
}

func TestDashboardRejectsInvalidExistingCacheWithoutObservedRuns(t *testing.T) {
	for _, content := range []string{
		"{",
		`{"schema_version":99,"kind":"run","run":{"repository":"octo/api","run_id":123}}`,
		`{"schema_version":5,"kind":"run","run":{"repository":"other/api","run_id":123}}`,
	} {
		t.Run(content, func(t *testing.T) {
			cache := t.TempDir()
			base := dashboardTestGH(t, time.Now().UTC().Add(-time.Second))
			options := dashboardOptions{window: dashboardMaximumDuration, count: 2}
			if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache, options, base, dashboardTestLogs(t)); err != nil {
				t.Fatal(err)
			}
			db, err := openNativeSnapshot(filepath.Join(cache, "gh-aw-logs.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			before, err := readNativeCollection(t.Context(), db, "runs")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cache, dashboardCoordinate("octo/api")+".jsonl"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			gh := func(ctx context.Context, args ...string) (string, string, error) {
				if args[0] == "run" {
					return "[]", "", nil
				}
				return base(ctx, args...)
			}
			logs := func(context.Context, dashboardLogRequest) error {
				t.Fatal("empty activity must not trigger artifact acquisition")
				return nil
			}
			if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache, options, gh, logs); err == nil {
				t.Fatal("invalid cached evidence was silently discarded")
			}
			after, err := readNativeCollection(t.Context(), db, "runs")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("invalid cached evidence changed the previous snapshot: %v", err)
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
		if !external {
			if _, err := collectDashboard(t.Context(), repositories, cache, options, dashboardTestGH(t, time.Now()), logs); err != nil {
				t.Fatal(err)
			}
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

func TestDashboardOptionalLocalArtifactCache(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "without local files", true: "with local cache"}[present], func(t *testing.T) {
			t.Chdir(t.TempDir())
			local := filepath.Join(".github", "aw", "logs")
			if present {
				if err := os.MkdirAll(local, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cache := t.TempDir()
			writeLogs := dashboardTestLogs(t)
			logs := func(ctx context.Context, request dashboardLogRequest) error {
				if request.ManagedCache == present {
					t.Fatalf("local artifact cache selection changed: %+v", request)
				}
				if present {
					expected, err := filepath.Abs(local)
					if err != nil || request.Directory != expected {
						t.Fatalf("existing local cache was not reused: %+v, %v", request, err)
					}
				}
				return writeLogs(ctx, request)
			}
			if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache,
				dashboardOptions{window: dashboardMaximumDuration, count: 2},
				dashboardTestGH(t, time.Now().UTC().Add(-time.Second)), logs); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(policyPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("dashboard must not require or create local CAO policy: %v", err)
			}
		})
	}
}
