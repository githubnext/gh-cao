package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/redisx"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

const backfillDatabaseQueries = "../../../dashboard/site/src/data/queries/database.json"

func TestPostgresBackfillIntegration(t *testing.T) {
	export := os.Getenv("CAO_BACKFILL_INTEGRATION") == "1"
	if !export && (os.Getenv("REDIS_URL") == "" ||
		(os.Getenv("CAO_POSTGRES_URL") == "" && os.Getenv("POSTGRES_URL") == "")) {
		t.Skip("set Postgres/Redis endpoints or run npm run test:integration:dashboard-backfill")
	}
	required := []string{"CAO_POSTGRES_URL", "REDIS_URL"}
	if os.Getenv("CAO_BACKFILL_SKIP_OPENOBSERVE") != "1" {
		required = append(required, "CAO_BACKFILL_OPENOBSERVE_URL", "CAO_LOCAL_OTEL_EMAIL", "CAO_LOCAL_OTEL_PASSWORD",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS")
	}
	for _, name := range required {
		if export && os.Getenv(name) == "" {
			t.Fatalf("%s is required when backfill integration is enabled", name)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	started := time.Now()
	service := fmt.Sprintf("cao-backfill-test-%d", started.UnixNano())
	var flush func(*testing.T)
	if export {
		flush = backfillTelemetry(t, ctx, service)
	}
	data := integrationDatabase(t)
	client, err := redisx.New(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatal("configure local Redis")
	}
	store := redisx.NewStore(client, service)
	if err := store.Ping(ctx); err != nil {
		t.Fatal("local Redis is unavailable")
	}
	directory, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	lake := collect.Lake{Directory: directory}
	enrollment := collect.Enrollment{Metadata: store, Leases: store}
	github, api, proxy, _ := syntheticGitHub(t, simulator.Scenario{
		Name: "backfill-replay", Repositories: 1,
		History: &simulator.History{Days: 1, RunsPerDay: 1, AsOf: started.Add(-time.Minute).UTC().Format(time.RFC3339)},
	})
	quota, err := githubquota.New(store, githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	backfill := collect.Backfill{StateStore: store, Metadata: store, Lake: lake, Enrollment: enrollment,
		Queue:      collect.Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxLength: 100},
		Enumerator: github, RunEnumerator: github, Quota: quota, QuotaApp: "simulator", WindowDays: 7,
	}
	app, err := New(ctx, store, Config{
		Database: data, DatabaseQueriesPath: backfillDatabaseQueries,
		Listen: "127.0.0.1:0", SiteDirectory: t.TempDir(), AccessToken: testAccessToken,
		QueryCache: QueryCacheConfig{Disabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(app.Handler())
	defer local.Close()
	var committed postgresx.State
	var expected map[string]model.Source
	var queryTrace string

	if !t.Run("snapshot ingestion and backfill admission remain independent", func(t *testing.T) {
		backfillReadiness(t, ctx, local, http.StatusServiceUnavailable)
		_, err := ingest.Run(ctx, data, lake.Directory, ingest.Options{DatabaseQueriesPath: backfillDatabaseQueries})
		if err != nil {
			t.Fatal(err)
		}
		state, err := backfill.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase != "collecting" || state.LakeReplayed || state.Revision != 0 ||
			state.Repositories != 1 || state.Installations != 1 || state.QueuedRepositories != 1 ||
			state.QueuedRunTasks != 1 || state.EnumerationFailures != 0 || state.CompletedAt == "" {
			t.Fatalf("unexpected cold-start result: %+v", state)
		}
		persisted, err := backfill.State(ctx)
		if err != nil || persisted != state {
			t.Fatalf("backfill checkpoint was not persisted: %+v, %v", persisted, err)
		}
		if installation, err := enrollment.InstallationFor(ctx, "simulator/repo-00001"); err != nil || installation != 1 {
			t.Fatalf("enrollment installation = %d, err=%v", installation, err)
		}
		assertBackfillQueueDepth(t, ctx, backfill.Queue)
		committed = backfillState(t, ctx, data)
		if !committed.Ready || committed.DataRevision == "" ||
			committed.Counts["$runs"] != 1 || committed.Counts["$repositories"] != 1 {
			t.Fatalf("native Postgres state = %+v", committed)
		}
		result, traceID := backfillHTTPQuery(t, ctx, local)
		queryTrace = traceID
		expected = result.Sources
		assertBackfillRows(t, result)
		backfillReadiness(t, ctx, local, http.StatusOK)
	}) {
		return
	}

	if !t.Run("rerun reuses revision and suppresses duplicate tasks", func(t *testing.T) {
		state, err := backfill.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state.Revision != 0 || state.QueuedRepositories != 0 || state.QueuedRunTasks != 0 ||
			state.Phase != "collecting" || proxy.RequestCount("/repos/simulator/repo-00001/actions/runs", 1) != 2 {
			t.Fatalf("rerun advanced revision, duplicated work, or ignored cursor: %+v", state)
		}
		assertBackfillQueueDepth(t, ctx, backfill.Queue)
		if got := backfillState(t, ctx, data); !reflect.DeepEqual(got, committed) {
			t.Fatalf("unchanged replay changed Postgres state: %+v", got)
		}
	}) {
		return
	}

	// Replay must not depend on GitHub enumeration or quota availability.
	backfill.Enumerator = nil
	backfill.RunEnumerator = nil
	requestsBeforeReplay := api.Stats().Requests
	if !t.Run("lost Postgres projection recovers from retained lake without GitHub", func(t *testing.T) {
		if err := data.DeleteNamespace(ctx); err != nil {
			t.Fatal(err)
		}
		backfillReadiness(t, ctx, local, http.StatusServiceUnavailable)
		replayed, err := ingest.Run(ctx, data, lake.Directory, ingest.Options{DatabaseQueriesPath: backfillDatabaseQueries})
		if err != nil {
			t.Fatal(err)
		}
		restored := backfillState(t, ctx, data)
		if replayed.Revision != 1 || !restored.Ready || restored.DataRevision != committed.DataRevision ||
			!reflect.DeepEqual(restored.Counts, committed.Counts) {
			t.Fatalf("lake recovery did not restore native data: %+v", restored)
		}
		committed = restored
		result, _ := backfillHTTPQuery(t, ctx, local)
		if !reflect.DeepEqual(result.Sources, expected) {
			t.Fatal("recovery changed canonical rows or metadata")
		}
		backfillReadiness(t, ctx, local, http.StatusOK)
		if api.Stats().Requests != requestsBeforeReplay {
			t.Fatal("projection recovery contacted the GitHub simulator")
		}
	}) {
		return
	}

	if !t.Run("malformed replacement fails and preserves serving data", func(t *testing.T) {
		shard := filepath.Join(lake.RecordsDirectory(), "subset.jsonl")
		original, err := os.ReadFile(shard) // #nosec G304 -- shard is a fixed file in this test's temporary lake.
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := os.ReadFile(lake.ManifestPath())
		if err != nil {
			t.Fatal(err)
		}
		var hashes map[string]string
		if err := json.Unmarshal(manifest, &hashes); err != nil {
			t.Fatal(err)
		}
		malformed := append(bytes.Clone(original), []byte("not-json\n")...)
		hash := sha256.Sum256(malformed)
		hashes["gh-aw-logs-records/subset.jsonl"] = hex.EncodeToString(hash[:])
		changedManifest, err := json.Marshal(hashes)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(shard, malformed, 0o600); err != nil { // #nosec G703 -- fixed file in the test's temporary lake.
			t.Fatal(err)
		}
		if err := os.WriteFile(lake.ManifestPath(), changedManifest, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ingest.Run(ctx, data, lake.Directory, ingest.Options{DatabaseQueriesPath: backfillDatabaseQueries}); err == nil {
			t.Fatal("backfill accepted a hash-valid but malformed record shard")
		}
		if got := backfillState(t, ctx, data); !reflect.DeepEqual(got, committed) {
			t.Fatalf("failed replay replaced committed state: %+v", got)
		}
		result, _ := backfillHTTPQuery(t, ctx, local)
		if !reflect.DeepEqual(result.Sources, expected) {
			t.Fatal("failed replay changed rows served by the live HTTP server")
		}
		backfillReadiness(t, ctx, local, http.StatusOK)
		if err := os.WriteFile(shard, original, 0o600); err != nil { // #nosec G703 -- restores the same fixed temporary fixture.
			t.Fatal(err)
		}
		if err := os.WriteFile(lake.ManifestPath(), manifest, 0o600); err != nil { // #nosec G703 -- fixed manifest in the test's temporary lake.
			t.Fatal(err)
		}
		repaired, err := ingest.Run(ctx, data, lake.Directory, ingest.Options{DatabaseQueriesPath: backfillDatabaseQueries})
		if err != nil || repaired.Revision != committed.Revision {
			t.Fatalf("repaired replay failed or advanced unchanged data: %+v, %v", repaired, err)
		}
	}) {
		return
	}

	t.Run("empty lake fails closed", func(t *testing.T) {
		empty := backfill
		empty.Lake = collect.Lake{Directory: t.TempDir()}
		if err := empty.Lake.Prepare(); err != nil {
			t.Fatal(err)
		}
		if _, err := ingest.Run(ctx, data, empty.Lake.Directory, ingest.Options{DatabaseQueriesPath: backfillDatabaseQueries}); err == nil {
			t.Fatal("empty evidence lake was accepted")
		}
		if got := backfillState(t, ctx, data); !reflect.DeepEqual(got, committed) {
			t.Fatal("empty lake changed existing data")
		}
	})
	if export {
		t.Run("OpenObserve receives correlated traces and backfill metrics", func(t *testing.T) {
			if flush == nil {
				t.Skip("OpenObserve image pull was rate-limited; core backfill assertions still ran")
			}
			flush(t)
			assertBackfillTelemetry(t, ctx, service, queryTrace, started)
		})
	}
}

func backfillState(t *testing.T, ctx context.Context, data *postgresx.Store) postgresx.State {
	t.Helper()
	state, err := data.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertBackfillQueueDepth(t *testing.T, ctx context.Context, queue collect.Queue) {
	t.Helper()
	depth, err := queue.Depth(ctx)
	if err != nil || depth != 1 {
		t.Fatalf("durable repository task depth = %d, err=%v", depth, err)
	}
	runDepth, err := taskQueueLength(ctx, queue.Tasks, "collect:run-tasks")
	if err != nil || runDepth != 1 {
		t.Fatalf("durable historical run task depth = %d, err=%v", runDepth, err)
	}
}

func backfillReadiness(t *testing.T, ctx context.Context, local *httptest.Server, want int) {
	t.Helper()
	backfillRequest(t, ctx, local, http.MethodGet, "/api/readiness", nil, want, nil)
}

func backfillHTTPQuery(t *testing.T, ctx context.Context, local *httptest.Server) (queryResponse, string) {
	t.Helper()
	input := queryRequest{}
	for _, source := range []string{"runs", "repositories", "workflows", "domains", "tools", "audits"} {
		input.Queries = append(input.Queries, query.Definition{Name: "backfill-" + source, From: "$" + source})
		input.SourceNames = append(input.SourceNames, "backfill-"+source)
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var result queryResponse
	traceID := backfillRequest(t, ctx, local, http.MethodPost, "/api/v1/query", body, http.StatusOK, &result)
	return result, traceID
}

func backfillRequest(t *testing.T, ctx context.Context, local *httptest.Server, method, path string, body []byte, status int, result any) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, local.URL+path, bytes.NewReader(body)) // #nosec G704 -- httptest binds this endpoint to loopback.
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := local.Client().Do(request) // #nosec G704 -- the request targets the local httptest server.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != status {
		t.Fatalf("%s %s returned %d, want %d", method, path, response.StatusCode, status)
	}
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			t.Fatal(err)
		}
	} else if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	traceID := response.Header.Get(telemetry.TraceIDHeader)
	if os.Getenv("CAO_BACKFILL_INTEGRATION") == "1" &&
		(len(traceID) != 32 || len(response.Header.Get(telemetry.SpanIDHeader)) != 16) {
		t.Fatal("live server response lacks W3C trace/span correlation")
	}
	return traceID
}

func assertBackfillRows(t *testing.T, result queryResponse) {
	t.Helper()
	for _, name := range []string{"runs", "repositories", "workflows", "domains", "tools", "audits"} {
		if len(result.Sources["backfill-"+name].Rows) != 1 {
			t.Fatalf("%s rows = %d, want one deployed-fixture row", name, len(result.Sources["backfill-"+name].Rows))
		}
	}
	run := result.Sources["backfill-runs"].Rows[0]
	if run["id"] != "run:424242" || run["repositoryId"] != "repository:githubnext/gh-cao" ||
		run["workflowId"] != "workflow:githubnext/gh-cao:dashboard" {
		t.Fatalf("backfill lost canonical run relationships: %+v", run)
	}
}
