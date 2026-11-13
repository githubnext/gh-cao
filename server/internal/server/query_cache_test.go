package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

type valueRevisionCacheStore struct {
	dashboardstore.Store
	revision int64
	err      error
	reads    int
}

func (store *valueRevisionCacheStore) State(context.Context) (dashboardstore.State, error) {
	store.reads++
	return dashboardstore.State{Revision: store.revision}, store.err
}

func TestValueQueryCacheTracksCommittedRevisionTransitively(t *testing.T) {
	store := &valueRevisionCacheStore{revision: 1}
	app := &App{database: store, databaseQueries: []query.Definition{
		{Name: "operational-value-evidence", From: "$operationalValueEvidence"},
		{Name: "operational-values", From: "$operationalValues"},
	}}
	app.config.DashboardQueries = []query.Definition{
		{Name: "card", From: "operational-value-evidence"},
		{Name: "joined", From: "$workflows", Joins: []query.Join{{Source: "card"}}},
		{Name: "union", Union: []string{"card", "$workflows"}},
		{Name: "retained-values", From: "operational-values"},
		{Name: "retained-joined", From: "$workflows", Joins: []query.Join{{Source: "$operationalValues"}}},
		{Name: "retained-union", Union: []string{"$operationalValues", "$workflows"}},
	}
	for _, name := range []string{"operational-value-evidence", "card", "joined", "union",
		"$operationalValues", "operational-values", "retained-values", "retained-joined", "retained-union"} {
		input := queryRequest{SourceNames: []string{name}}
		before, err := app.queryResultCacheIdentity(t.Context(), input, false)
		if err != nil {
			t.Fatal(err)
		}
		store.revision++
		after, err := app.queryResultCacheIdentity(t.Context(), input, false)
		if err != nil || before == after {
			t.Fatalf("%s reused a previous value revision: %v", name, err)
		}
	}
	store.reads = 0
	input := queryRequest{SourceNames: []string{"$workflows"}}
	before, err := app.queryResultCacheIdentity(t.Context(), input, false)
	if err != nil {
		t.Fatal(err)
	}
	store.revision++
	after, err := app.queryResultCacheIdentity(t.Context(), input, false)
	if err != nil || before != after || store.reads != 0 {
		t.Fatalf("ordinary cache behavior changed: reads=%d err=%v", store.reads, err)
	}
	store.err = errors.New("private connection string")
	_, err = app.queryResultCacheIdentity(t.Context(), queryRequest{SourceNames: []string{"card"}}, false)
	if !errors.Is(err, errValueRevisionUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatalf("revision failure was hidden or leaked: %v", err)
	}
}

type queryCacheClient struct {
	value    any
	put      []byte
	err      error
	reads    int
	writes   int
	stats    redisx.QueryCacheStats
	writeErr error
}

const cacheTestMemoryInfo = "# Memory\r\nused_memory:1024\r\nmaxmemory:0\r\n"

func cacheTestCapabilityReply() []any {
	return []any{[]any{int64(1), cacheTestMemoryInfo}, []any{int64(1), nil}}
}

func (client *queryCacheClient) Do(_ context.Context, args ...string) (any, error) {
	if client.err != nil {
		return nil, client.err
	}
	if args[0] == "INFO" {
		return cacheTestMemoryInfo, nil
	}
	if args[0] == "EVAL" && strings.Contains(args[1], `return {probe("INFO"`) {
		return cacheTestCapabilityReply(), nil
	}
	if args[0] == "EVAL" && args[1] != "" && len(args) == 13 {
		switch args[5] {
		case "get":
			client.reads++
			return []any{client.value, client.stats.MemoryBytes, client.stats.Entries, client.stats.Expired, client.stats.Evicted}, nil
		case "put":
			client.writes++
			if client.writeErr != nil {
				return nil, client.writeErr
			}
			client.put = []byte(args[10])
			return []any{int64(1), client.stats.MemoryBytes, client.stats.Entries, client.stats.Expired, client.stats.Evicted}, nil
		}
	}
	return nil, errors.New("unexpected query cache command")
}

func (*queryCacheClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, errors.New("unexpected query cache pipeline")
}

func TestQueryCacheConfiguration(t *testing.T) {
	config, err := (QueryCacheConfig{}).resolve()
	if err != nil || config.MaxResultBytes != 1<<20 || config.MaxBytes != 64<<20 ||
		config.MinDuration != 100*time.Millisecond {
		t.Fatalf("unexpected defaults: %+v err=%v", config, err)
	}
	for _, config := range []QueryCacheConfig{
		{MaxResultBytes: -1}, {MaxBytes: -1}, {MinDuration: -1},
		{MaxResultBytes: 2048, MaxBytes: 1024},
	} {
		if _, err := config.resolve(); err == nil {
			t.Fatalf("invalid cache configuration accepted: %+v", config)
		}
	}
	t.Setenv("CAO_QUERY_CACHE_MAX_RESULT_BYTES", "4096")
	t.Setenv("CAO_QUERY_CACHE_MAX_BYTES", "16384")
	config, err = queryCacheConfigFromEnv(QueryCacheConfig{})
	if err != nil || config.MaxResultBytes != 4096 || config.MaxBytes != 16384 {
		t.Fatalf("environment limits: %+v err=%v", config, err)
	}
	for _, invalid := range []string{"", "0", "-1", "1mb", "9223372036854775808"} {
		t.Setenv("CAO_QUERY_CACHE_MAX_BYTES", invalid)
		if _, err := queryCacheConfigFromEnv(QueryCacheConfig{}); err == nil {
			t.Fatalf("invalid environment byte count accepted: %q", invalid)
		}
	}
}

func TestQueryCacheEmergencyDisableAndExplicitConfiguration(t *testing.T) {
	t.Setenv("CAO_QUERY_CACHE_MAX_RESULT_BYTES", "1024")
	t.Setenv("CAO_QUERY_CACHE_MAX_BYTES", "8192")
	for _, value := range []string{"true", "1", "false", "0"} {
		t.Setenv("CAO_QUERY_CACHE_DISABLED", value)
		config, err := queryCacheConfigFromEnv(QueryCacheConfig{})
		disabled := value == "true" || value == "1"
		if err != nil || config.Disabled != disabled {
			t.Fatalf("cache disable switch %q: config=%+v err=%v", value, config, err)
		}
	}
	for _, value := range []string{"", "yes", "disable"} {
		t.Setenv("CAO_QUERY_CACHE_DISABLED", value)
		if _, err := queryCacheConfigFromEnv(QueryCacheConfig{}); err == nil {
			t.Fatalf("invalid disable switch was accepted: %q", value)
		}
	}
	config, err := queryCacheConfigFromEnv(QueryCacheConfig{
		Disabled: true, MaxResultBytes: 4096, MaxBytes: 16384,
	})
	if err != nil || !config.Disabled || config.MaxResultBytes != 4096 || config.MaxBytes != 16384 {
		t.Fatalf("environment overrode explicit configuration: config=%+v err=%v", config, err)
	}
}

func TestQueryCacheIdentity(t *testing.T) {
	app := &App{databaseQueries: []query.Definition{{Name: "runs", From: "$runs"}}}
	input := queryRequest{
		SourceNames:     []string{"runs"},
		RouteParameters: map[string]any{"b": "two", "a": "one"},
	}
	original, err := app.queryCacheIdentity(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, decodeErr := hex.DecodeString(original); decodeErr != nil || len(decoded) != sha256.Size {
		t.Fatalf("query cache identity is not a SHA-256 digest: %q, %v", original, decodeErr)
	}
	input.RouteParameters = map[string]any{"a": "one", "b": "two"}
	input.EvaluatedAt = "2026-10-01T00:00:00Z"
	same, err := app.queryCacheIdentity(input, false)
	if err != nil || original != same {
		t.Fatalf("map order or ignored evaluation timestamp changed ETag: %s %s err=%v", original, same, err)
	}
	for _, mutate := range []func(*queryRequest){
		func(input *queryRequest) { input.RouteParameters["a"] = "changed" },
		func(input *queryRequest) { input.Pagination = map[string]paginationRequest{"runs": {Limit: 1}} },
		func(input *queryRequest) { input.CompiledQueries = []query.Definition{{Name: "runs", From: "$tools"}} },
		func(input *queryRequest) { input.ReplacedSources = []string{"runs"} },
	} {
		cloned := input
		cloned.RouteParameters = map[string]any{"a": "one", "b": "two"}
		mutate(&cloned)
		changed, err := app.queryCacheIdentity(cloned, false)
		if err != nil || changed == original {
			t.Fatalf("changed request reused ETag: %s err=%v", changed, err)
		}
	}
	admin, err := app.queryCacheIdentity(input, true)
	if err != nil || admin == original {
		t.Fatalf("admin and non-admin queries shared ETag: %s err=%v", admin, err)
	}
	app.config.DashboardQueries = []query.Definition{{Name: "dashboard", From: "$runs"}}
	changed, err := app.queryCacheIdentity(input, false)
	if err != nil || changed == original {
		t.Fatal("configured dashboard definitions did not contribute to ETag")
	}
}

func compactQueryResult() queryResponse {
	return queryResponse{
		Revision: 1, EvaluatedAt: "2026-10-01T00:00:00Z",
		Sources: map[string]model.Source{"runs": {
			Source: "runs", Rows: []model.Row{{"id": json.Number("9007199254740993")}},
			Metadata: model.Metadata{"availability": "available"},
		}},
		Metrics: model.Metrics{OutputRows: 1, DurationMS: 2000, Operations: 500_000},
	}
}

func TestQueryCacheCompactEncodingAndNumericFidelity(t *testing.T) {
	client := &queryCacheClient{}
	app := &App{services: redisx.NewStore(client, "query-test").OperationalServices()}
	config, err := (QueryCacheConfig{}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	key, err := app.queryCacheIdentity(queryRequest{SourceNames: []string{"runs"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	result := compactQueryResult()
	if err := app.storeCachedQuery(t.Context(), key, result, config); err != nil {
		t.Fatal(err)
	}
	client.value = string(client.put)
	cached, hit, err := app.loadCachedQuery(t.Context(), key, config)
	if err != nil || !hit || cached.Revision != result.Revision ||
		cached.EvaluatedAt != result.EvaluatedAt || !reflect.DeepEqual(cached.Sources, result.Sources) {
		t.Fatalf("cache round trip: %+v hit=%t err=%v", cached, hit, err)
	}
	if cached.Metrics.Operations != 0 || cached.Metrics.DurationMS != 0 ||
		cached.Metrics.OutputRows != 1 || queryRateLimitCost(cached.Metrics) != 1 {
		t.Fatalf("cache hit reported or charged fresh plan work: %+v", cached.Metrics)
	}
	exact := int64(len(client.put))
	if _, compact, err := encodeCompactQuery(key, result, exact); err != nil || !compact {
		t.Fatalf("result at byte limit was rejected: compact=%t err=%v", compact, err)
	}
	if _, compact, err := encodeCompactQuery(key, result, exact-1); err != nil || compact {
		t.Fatalf("result beyond byte limit was admitted: compact=%t err=%v", compact, err)
	}
	result.Sources["bulky"] = model.Source{
		Rows: []model.Row{{"payload": strings.Repeat("x", int(config.MaxResultBytes)+1)}},
	}
	if err := app.storeCachedQuery(t.Context(), key, result, config); err != nil || client.writes != 1 {
		t.Fatalf("oversized result reached Redis: writes=%d err=%v", client.writes, err)
	}
	for _, data := range []string{"not json", `{"etag":"wrong","result":{}}`} {
		client.value = data
		if _, _, err := app.loadCachedQuery(t.Context(), key, config); err == nil {
			t.Fatalf("invalid cache entry was accepted: %s", data)
		}
	}
}

func TestQueryCacheErrorsAreExplicitAndRedacted(t *testing.T) {
	client := &queryCacheClient{err: errors.New("secret redis endpoint")}
	app := &App{database: constructorDatabase(), services: redisx.NewStore(client, "query-errors").OperationalServices()}
	_, status, err := app.executeQuery(t.Context(), queryRequest{SourceNames: []string{"runs"}}, false)
	if status != http.StatusServiceUnavailable || err == nil ||
		err.Error() != "query result cache is unavailable" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("cache failure was hidden or leaked: status=%d err=%v", status, err)
	}
}

func TestStatisticalQueriesBypassOrdinaryResultCache(t *testing.T) {
	app, fixture := newLocalPostgresTestApp(t, false)
	client := &queryCacheClient{}
	app.services.Cache = redisx.NewStore(client, "statistical-cache").OperationalServices().Cache
	app.config.QueryCache = QueryCacheConfig{MinDuration: time.Nanosecond}
	input := queryRequest{SourceNames: []string{"statistical-runs"}}
	first, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK {
		t.Fatalf("initial sample query: status=%d err=%v", status, err)
	}
	fixture.publish(t, "sample-updated", "updated")
	second, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || second.Revision <= first.Revision ||
		second.Sources["statistical-runs"].Metadata["freshness"] != "stale" ||
		client.reads != 0 || client.writes != 0 {
		t.Fatalf("ordinary cache masked sample revision/freshness: %+v reads=%d writes=%d err=%v",
			second, client.reads, client.writes, err)
	}
}

func TestQueryCacheRejectsTrailingAndIncompleteDocuments(t *testing.T) {
	client := &queryCacheClient{}
	app := &App{services: redisx.NewStore(client, "corruption-test").OperationalServices()}
	config, err := (QueryCacheConfig{}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	key, err := app.queryCacheIdentity(queryRequest{SourceNames: []string{"runs"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	valid, compact, err := encodeCompactQuery(key, compactQueryResult(), config.MaxResultBytes)
	if err != nil || !compact {
		t.Fatalf("encode valid entry: compact=%t err=%v", compact, err)
	}
	for _, data := range []string{
		string(valid) + `{}`,
		string(valid) + `trailing-garbage`,
		`{"etag":"` + key + `","result":null}`,
		`{"etag":"` + key + `","result":{"evaluatedAt":"time","sources":null}}`,
	} {
		client.value = data
		if _, hit, err := app.loadCachedQuery(t.Context(), key, config); err == nil || hit {
			t.Fatalf("corrupted entry was accepted: hit=%t err=%v", hit, err)
		}
	}
	client.value = string(valid) + " \n\t"
	if _, hit, err := app.loadCachedQuery(t.Context(), key, config); err != nil || !hit {
		t.Fatalf("valid trailing whitespace was rejected: hit=%t err=%v", hit, err)
	}
	client.writeErr = errors.New("private Redis transport failure")
	if err := app.storeCachedQuery(t.Context(), key, compactQueryResult(), config); err == nil {
		t.Fatal("cache-write failure was hidden")
	}
}

type blockedQueryCacheClient struct {
	deadline chan time.Duration
}

func (client blockedQueryCacheClient) Do(ctx context.Context, _ ...string) (any, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("cache operation had no deadline")
	}
	client.deadline <- time.Until(deadline)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockedQueryCacheClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, errors.New("unexpected cache pipeline")
}

func TestQueryCacheCancellationAndOperationDeadline(t *testing.T) {
	client := blockedQueryCacheClient{deadline: make(chan time.Duration, 1)}
	app := &App{database: constructorDatabase(), services: redisx.NewStore(client, "deadline-test").OperationalServices()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type execution struct {
		status int
		err    error
	}
	finished := make(chan execution, 1)
	go func() {
		_, status, err := app.executeQuery(ctx, queryRequest{SourceNames: []string{"runs"}}, false)
		finished <- execution{status, err}
	}()
	select {
	case remaining := <-client.deadline:
		if remaining <= 0 || remaining > queryCacheTimeout {
			t.Fatalf("cache operation deadline = %v", remaining)
		}
	case <-time.After(time.Second):
		t.Fatal("cache operation did not start")
	}
	cancel()
	select {
	case result := <-finished:
		if result.status != http.StatusServiceUnavailable || !errors.Is(result.err, context.Canceled) {
			t.Fatalf("cancellation: status=%d err=%v", result.status, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cache operation ignored cancellation")
	}
}
func TestResolveQueryContextDoesNotMutateSharedDefinitions(t *testing.T) {
	original := []query.Definition{{Name: "runs", Compute: []query.ComputedField{{
		Args: []query.Argument{{Context: "time-end"}, {Context: "time-start"}},
	}}}}
	for _, evaluatedAt := range []string{"first", "second"} {
		cloned := append([]query.Definition{}, original...)
		ResolveQueryContext(cloned, evaluatedAt)
		if original[0].Compute[0].Args[0].Context != "time-end" ||
			cloned[0].Compute[0].Args[0].Value != evaluatedAt ||
			original[0].Compute[0].Args[1].Context != "time-start" ||
			cloned[0].Compute[0].Args[1].Context != "" ||
			cloned[0].Compute[0].Args[1].Value != nil {
			t.Fatalf("shared definitions were mutated: original=%+v cloned=%+v", original, cloned)
		}
	}
}

func TestQueryCacheSurvivesIngestionAndSeparatesAuthorization(t *testing.T) {
	database := integrationDatabase(t)
	store, keys := serverQueryCacheIntegrationStore(t)
	client := store.Client
	seed := func(id string) {
		t.Helper()
		seedDatabase(t, database, map[string]model.Source{
			"$repositories": {Source: "$repositories", Rows: []model.Row{{"id": "repository"}}},
			"$workflows":    {Source: "$workflows", Rows: []model.Row{{"id": "workflow", "repositoryId": "repository"}}},
			"$runs": {
				Source: "$runs", Rows: []model.Row{{"id": id, "repositoryId": "repository", "workflowId": "workflow"}},
				Metadata: model.Metadata{"availability": "available"},
			},
		})
	}
	seed("old")
	app := &App{
		database: database, services: store.OperationalServices(), config: Config{QueryCache: QueryCacheConfig{MinDuration: time.Nanosecond}},
		databaseQueries: []query.Definition{{Name: "runs", From: "$runs"}},
	}
	input := queryRequest{SourceNames: []string{"runs"}}
	first, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK {
		t.Fatalf("initial query: status=%d err=%v", status, err)
	}
	seed("new")
	input.EvaluatedAt = "ignored-refresh-timestamp"
	second, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || second.Revision != first.Revision ||
		second.Sources["runs"].Rows[0]["id"] != "old" {
		t.Fatalf("ingestion invalidated cached snapshot: result=%+v status=%d err=%v", second, status, err)
	}
	admin, status, err := app.executeQuery(t.Context(), input, true)
	if err != nil || status != http.StatusOK || admin.Sources["runs"].Rows[0]["id"] != "new" {
		t.Fatalf("authorization classes shared a cached result: result=%+v status=%d err=%v", admin, status, err)
	}
	key, err := app.queryCacheIdentity(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(t.Context(), "ZADD", keys[1], "0", key); err != nil {
		t.Fatal(err)
	}
	fresh, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || fresh.Revision == first.Revision ||
		fresh.Sources["runs"].Rows[0]["id"] != "new" {
		t.Fatalf("expired query did not recompute: result=%+v status=%d err=%v", fresh, status, err)
	}
	app.config.QueryCache.Disabled = true
	seed("disabled")
	fresh, status, err = app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || fresh.Sources["runs"].Rows[0]["id"] != "disabled" {
		t.Fatalf("disabled cache was used: result=%+v status=%d err=%v", fresh, status, err)
	}
}

func serverQueryCacheIntegrationStore(t *testing.T) (*redisx.Store, []string) {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL is not set")
	}
	client, err := redisx.New(url)
	if err != nil {
		t.Fatal(err)
	}
	store := redisx.NewStore(client, "query-server-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	keys := []string{store.Key("{query-cache:v1}:entries"), store.Key("{query-cache:v1}:expiry")}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := client.Do(ctx, append([]string{"DEL"}, keys...)...); err != nil {
			t.Errorf("clean up query cache: %v", err)
		}
	})
	return store, keys
}

func TestQueryCacheSharedByAuthenticatedHTTPAndMCP(t *testing.T) {
	database := integrationDatabase(t)
	store, keys := serverQueryCacheIntegrationStore(t)
	seed := func(id string) {
		t.Helper()
		seedDatabase(t, database, map[string]model.Source{
			"$repositories": {Source: "$repositories", Rows: []model.Row{{
				"id": "repository", "owner": "githubnext", "name": "gh-cao",
			}}},
			"$campaigns": {Source: "$campaigns", Rows: []model.Row{{
				"id": "campaign", "slug": "cache-fixture",
			}}},
			"$workflows": {Source: "$workflows", Rows: []model.Row{{
				"id": "workflow", "repositoryId": "repository", "path": ".github/workflows/cache-fixture.yml",
				"name": "Cache fixture", "campaignId": "campaign",
			}}},
			"$runs": {
				Source: "$runs", Rows: []model.Row{{
					"id": id, "repositoryId": "repository", "workflowId": "workflow",
					"owner": "githubnext", "repository": "gh-cao", "githubRunId": "123",
					"attempt": 1, "title": id,
				}},
				Metadata: model.Metadata{"availability": "available"},
			},
		})
	}
	seed("http-snapshot")
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	definitions, err := ParseDashboardQueries("../../../dashboard/site/src/agent/queries.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := findDefinition(definitions, "campaign-runs")
	if !ok {
		t.Fatal("generated agent queries do not define campaign-runs")
	}
	app, err := New(t.Context(), store, Config{
		Database: database, DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json",
		Listen: "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
		QueryCache: QueryCacheConfig{MinDuration: time.Nanosecond}, DashboardQueries: definitions,
		MCPEnabled: true, AgentCatalogPath: testAgentCatalog, MCPContractPath: testMCPContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The local bearer profile bypasses Redis; exercise the hosted cache path.
	app.config.QueryCache.Disabled = false
	alias := "agent:campaign-runs"
	definition.Name = alias + ":source"
	limit := 2
	input := queryRequest{
		Aliases: []string{alias}, Queries: definitions,
		CompiledQueries: []query.Definition{
			definition,
			{Name: alias, From: alias + ":source", Limit: &limit},
		},
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/api/v1/query", bytes.NewReader(body))
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP query status=%d body=%s", response.Code, response.Body.String())
	}
	seed("new-ingestion")
	unauthorized := httptest.NewRecorder()
	app.Handler().ServeHTTP(unauthorized,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/api/v1/query", bytes.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized || strings.Contains(unauthorized.Body.String(), "http-snapshot") {
		t.Fatal("cache lookup bypassed current HTTP authentication")
	}
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "query-cache-test", Version: "1"}, nil)
	session, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{
			token: testAccessToken, base: http.DefaultTransport,
		}},
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query", Arguments: map[string]any{"id": "campaign-runs", "limit": 1},
	})
	if err != nil || result.IsError {
		t.Fatalf("MCP query: result=%+v err=%v", result, err)
	}
	payload := result.StructuredContent.(map[string]any)
	rows := payload["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["run-title"] != "http-snapshot" ||
		rows[0].(map[string]any)["campaign"] != "cache-fixture" ||
		rows[0].(map[string]any)["workflow-name"] != "Cache fixture" {
		t.Fatalf("MCP did not reuse HTTP query snapshot: %+v", payload)
	}
	count, err := store.Client.Do(t.Context(), "HLEN", keys[0])
	if err != nil || count != int64(1) {
		t.Fatalf("HTTP and MCP did not share one cache entry: count=%v err=%v", count, err)
	}
}
