package server

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testAgentCatalog = "../../../dashboard/site/src/agent/catalog.generated.json"
	testMCPContract  = "../../../dashboard/site/src/agent/mcp-contract.json"
	testActionsToken = "test-actions-token-0123456789abcdef0123456789"
	testActionsActor = "octocat"
)

func TestMCPDisabledEndpointIsAbsent(t *testing.T) {
	app := newMCPTestApp(t, false)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/mcp", nil)
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled MCP returned %d, want 404", response.Code)
	}
}

func TestMCPRejectsCatalogQueriesWithoutLoadedDefinitions(t *testing.T) {
	app := newMCPTestApp(t, true)
	definitions := app.config.DashboardQueries
	catalog, err := readJSONFile[agentCatalog](app.config.AgentCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Queries) == 0 {
		t.Fatal("agent catalog has no queries to verify")
	}
	for _, name := range []string{"all", catalog.Queries[0].ID} {
		t.Run(name, func(t *testing.T) {
			app.config.DashboardQueries = nil
			if name != "all" {
				for _, definition := range definitions {
					if definition.Name != name {
						app.config.DashboardQueries = append(app.config.DashboardQueries, definition)
					}
				}
			}
			_, err := app.newMCPHandler()
			if err == nil || !strings.Contains(err.Error(), "has no loaded dashboard definition") {
				t.Fatalf("missing definitions returned %v", err)
			}
			if name != "all" && !strings.Contains(err.Error(), name) {
				t.Fatalf("error does not identify missing query: %v", err)
			}
		})
	}
}

func TestMCPRequiresLocalBearerAndDiscoversReadOnlyTools(t *testing.T) {
	app := newMCPTestApp(t, true)
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()

	unauthorized := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/mcp", nil)
	rejected := httptest.NewRecorder()
	app.Handler().ServeHTTP(rejected, unauthorized)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP returned %d, want 401", rejected.Code)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{
			token: testAccessToken, base: http.DefaultTransport,
		}},
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 2 || listed.Tools[0].Name != "cao_catalog" || listed.Tools[1].Name != "cao_query" {
		t.Fatalf("unexpected tools: %#v", listed.Tools)
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %q is not read-only", tool.Name)
		}
	}
}

func TestMCPAcceptsConfiguredGitHubActionsIdentityOnly(t *testing.T) {
	app := newMCPTestAppWithActions(t, testActionsToken, testActionsActor)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"}}}}`)
	request := func(path, token, actor string) *http.Request {
		value := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost"+path, bytes.NewReader(body))
		value.Header.Set("Content-Type", "application/json")
		value.Header.Set("Accept", "application/json, text/event-stream")
		value.Header.Set("MCP-Protocol-Version", "2026-07-28")
		value.Header.Set("Mcp-Method", "tools/list")
		value.Header.Set("Authorization", "Bearer "+token)
		value.Header.Set("X-GitHub-Actor", actor)
		return value
	}
	for _, test := range []struct {
		name   string
		path   string
		token  string
		actor  string
		status int
	}{
		{"valid", "/mcp", testActionsToken, "OctoCat", http.StatusOK},
		{"wrong token", "/mcp", testActionsToken + "x", testActionsActor, http.StatusUnauthorized},
		{"wrong actor", "/mcp", testActionsToken, "attacker", http.StatusUnauthorized},
		{"missing actor", "/mcp", testActionsToken, "", http.StatusUnauthorized},
		{"API scope", "/api/v1/query", testActionsToken, testActionsActor, http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request(test.path, test.token, test.actor))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestGitHubActionsMCPIdentityValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		config Config
		wantOK bool
	}{
		{"disabled", Config{GitHubActionsToken: "short", GitHubActionsActor: testActionsActor}, true},
		{"valid", Config{MCPEnabled: true, GitHubActionsToken: testActionsToken, GitHubActionsActor: testActionsActor}, true},
		{"dashboard token reused", Config{
			MCPEnabled: true, GitHubActionsToken: testAccessToken, GitHubActionsActor: testActionsActor,
		}, false},
		{"missing actor", Config{MCPEnabled: true, GitHubActionsToken: testActionsToken}, false},
		{"short token", Config{MCPEnabled: true, GitHubActionsToken: "short", GitHubActionsActor: testActionsActor}, false},
		{"invalid actor", Config{MCPEnabled: true, GitHubActionsToken: testActionsToken, GitHubActionsActor: "octo cat"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := githubActionsMCPIdentity(test.config, testAccessToken)
			if (err == nil) != test.wantOK {
				t.Fatalf("githubActionsMCPIdentity() error = %v, want success %t", err, test.wantOK)
			}
		})
	}
}

func TestGitHubActionsMCPRequiresAllReadPermissions(t *testing.T) {
	for _, denied := range []string{"", "actions", "contents", "issues", "pull-requests"} {
		t.Run("denied="+denied, func(t *testing.T) {
			seen := map[string]bool{}
			api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer "+testActionsToken {
					t.Error("permission probe did not use the configured token")
				}
				permission := permissionForProbePath(request.URL.Path)
				seen[permission] = true
				if permission == denied {
					response.WriteHeader(http.StatusForbidden)
					return
				}
				response.WriteHeader(http.StatusOK)
			}))
			defer api.Close()
			err := verifyGitHubActionsPermissions(t.Context(), Config{
				ActionsRepository: "githubnext/gh-cao",
				GitHubAPIURL:      api.URL,
				ActionsHTTPClient: api.Client(),
			}, testActionsToken)
			if denied == "" {
				if err != nil {
					t.Fatalf("complete permissions rejected: %v", err)
				}
				for _, permission := range []string{"actions", "contents", "issues", "pull-requests"} {
					if !seen[permission] {
						t.Errorf("%s permission was not checked", permission)
					}
				}
			} else if err == nil || !strings.Contains(err.Error(), denied+": read") {
				t.Fatalf("denied %s permission returned %v", denied, err)
			}
		})
	}
}

func TestGitHubActionsMCPPermissionCheckRequiresRepository(t *testing.T) {
	err := verifyGitHubActionsPermissions(t.Context(), Config{}, testActionsToken)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("missing repository returned %v", err)
	}
}

func TestResolveQueryLimit(t *testing.T) {
	const defaultRows, maxRows = 50, 500

	tests := []struct {
		name        string
		value       any
		wantLimit   int
		wantOutcome queryLimitOutcome
		wantErr     bool
	}{
		{name: "nil selects default", value: nil, wantLimit: defaultRows, wantOutcome: queryLimitOutcomeDefault},
		{name: "exact value under max", value: float64(10), wantLimit: 10, wantOutcome: queryLimitOutcomeExact},
		{name: "value equal to max clamps", value: float64(maxRows), wantLimit: maxRows, wantOutcome: queryLimitOutcomeClamped},
		{name: "value above max clamps", value: float64(maxRows * 2), wantLimit: maxRows, wantOutcome: queryLimitOutcomeClamped},
		{name: "zero is rejected", value: float64(0), wantErr: true, wantOutcome: queryLimitOutcomeRejected},
		{name: "negative is rejected", value: float64(-1), wantErr: true, wantOutcome: queryLimitOutcomeRejected},
		{name: "fractional is rejected", value: float64(1.5), wantErr: true, wantOutcome: queryLimitOutcomeRejected},
		{name: "NaN is rejected", value: math.NaN(), wantErr: true, wantOutcome: queryLimitOutcomeRejected},
		{name: "infinity is rejected", value: math.Inf(1), wantErr: true, wantOutcome: queryLimitOutcomeRejected},
		{name: "non-numeric is rejected", value: "10", wantErr: true, wantOutcome: queryLimitOutcomeRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limit, outcome, err := resolveQueryLimit(test.value, defaultRows, maxRows)
			if test.wantErr {
				if err == nil {
					t.Fatalf("resolveQueryLimit(%v) = nil error, want error", test.value)
				}
			} else if err != nil {
				t.Fatalf("resolveQueryLimit(%v) returned unexpected error: %v", test.value, err)
			}
			if outcome != test.wantOutcome {
				t.Errorf("resolveQueryLimit(%v) outcome = %s, want %s", test.value, outcome, test.wantOutcome)
			}
			if !test.wantErr && limit != test.wantLimit {
				t.Errorf("resolveQueryLimit(%v) = %d, want %d", test.value, limit, test.wantLimit)
			}
		})
	}
}

func TestMCPCatalogAndNamedQueryUseSharedData(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	app := newMCPTestApp(t, true)
	app.database = database
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{
			token: testAccessToken, base: http.DefaultTransport,
		}},
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	catalog, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_catalog", Arguments: map[string]any{"kind": "queries", "id": "campaign-runs"},
	})
	if err != nil || catalog.IsError {
		t.Fatalf("catalog call failed: result=%#v err=%v", catalog, err)
	}
	entry := catalog.StructuredContent.(map[string]any)["query"].(map[string]any)
	if entry["id"] != "campaign-runs" {
		t.Fatalf("unexpected catalog entry: %#v", entry)
	}

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query", Arguments: map[string]any{"id": "campaign-runs", "limit": 1},
	})
	if err != nil || result.IsError {
		text := ""
		if len(result.Content) > 0 {
			if content, ok := result.Content[0].(*mcp.TextContent); ok {
				text = content.Text
			}
		}
		t.Fatalf("query call failed: %s err=%v", text, err)
	}
	payload := result.StructuredContent.(map[string]any)
	if payload["query"] != "campaign-runs" {
		t.Fatalf("unexpected query result: %#v", payload)
	}
	rows := payload["rows"].([]any)
	if len(rows) != 0 {
		t.Fatalf("empty fixture returned rows: %#v", rows)
	}
	metadata := payload["metadata"].(map[string]any)
	if metadata["availability"] != "empty" {
		t.Fatalf("unexpected availability: %#v", metadata)
	}

	count, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query", Arguments: map[string]any{"id": "indexing-campaigns-table-count", "limit": 1},
	})
	if err != nil || count.IsError {
		t.Fatalf("campaign count query failed: result=%#v err=%v", count, err)
	}
	countPayload := count.StructuredContent.(map[string]any)
	if countPayload["query"] != "indexing-campaigns-table-count" {
		t.Fatalf("unexpected campaign count query: %#v", countPayload)
	}
	if countPayload["metadata"].(map[string]any)["availability"] == "unavailable" {
		t.Fatalf("campaign count query was unavailable: %#v", countPayload)
	}

	invalid, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query", Arguments: map[string]any{"id": "not-a-query"},
	})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid query was not a tool error: result=%#v err=%v", invalid, err)
	}

	parameterized, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query",
		Arguments: map[string]any{
			"id": "mcp-tool-calls", "parameters": map[string]any{"tool": "github/search"},
			"limit": 1000000,
		},
	})
	if err != nil || parameterized.IsError {
		t.Fatalf("parameterized query failed: result=%#v err=%v", parameterized, err)
	}
	parameterMetadata := parameterized.StructuredContent.(map[string]any)["metadata"].(map[string]any)
	if parameterMetadata["limit"] != float64(5000) {
		t.Fatalf("oversized limit was not bounded: %#v", parameterMetadata)
	}
	if parameterMetadata["parameters"].(map[string]any)["tool"] != "github/search" {
		t.Fatalf("query parameters were not reported: %#v", parameterMetadata)
	}

	invalidParameters, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query",
		Arguments: map[string]any{
			"id": "campaign-runs", "parameters": map[string]any{"unknown": "value"},
		},
	})
	if err != nil || !invalidParameters.IsError {
		t.Fatalf("invalid parameters were not a tool error: result=%#v err=%v", invalidParameters, err)
	}

	invalidUnavailableParameters, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query",
		Arguments: map[string]any{
			"id": "skill-invocations", "parameters": map[string]any{"unknown": "value"},
		},
	})
	if err != nil || !invalidUnavailableParameters.IsError {
		t.Fatalf("invalid parameters for unavailable query were not a tool error: result=%#v err=%v",
			invalidUnavailableParameters, err)
	}
}

func TestHostedMCPConfigurationRequiresRepository(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database:    constructorDatabase(),
		HostProfile: hostedHostProfile(), Listen: "127.0.0.1:8080",
		SiteDirectory: site, MCPEnabled: true,
		Proxy: ProxyPolicy{
			AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true,
		},
		GitHubOAuth: validOAuthConfig("https://github.test"),
	})
	if err == nil || !strings.Contains(err.Error(), "hosted MCP requires an Actions repository") {
		t.Fatalf("hosted mode returned %v, want repository rejection", err)
	}
}

func TestMCPContractAnnotationsArePreservedOnWire(t *testing.T) {
	app := newMCPTestApp(t, true)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"}}}}`)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", "tools/list")
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("tools/list returned %d: %s", response.Code, response.Body.String())
	}
	var message struct {
		Result struct {
			Tools []struct {
				Annotations map[string]any `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	for _, tool := range message.Result.Tools {
		if tool.Annotations["readOnlyHint"] != true || tool.Annotations["untrustedContentHint"] != true {
			t.Fatalf("contract annotations drifted: %#v", tool.Annotations)
		}
	}
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

func newMCPTestApp(t *testing.T, enabled bool) *App {
	t.Helper()
	return newMCPTestAppConfig(t, enabled, "", "", "", nil)
}

func newMCPTestAppWithActions(t *testing.T, token, actor string) *App {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)
	return newMCPTestAppConfig(t, true, token, actor, api.URL, api.Client())
}

func newMCPTestAppConfig(
	t *testing.T,
	enabled bool,
	actionsToken, actionsActor, apiURL string,
	httpClient *http.Client,
) *App {
	t.Helper()
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	definitions, err := ParseDashboardQueries("../../../dashboard/site/src/agent/queries.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database: constructorDatabase(), DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json",
		Listen: "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
		DashboardQueries: definitions, AgentCatalogPath: testAgentCatalog,
		MCPContractPath: testMCPContract, MCPEnabled: enabled,
		GitHubActionsToken: actionsToken, GitHubActionsActor: actionsActor,
		ActionsRepository: "githubnext/gh-cao",
		GitHubAPIURL:      apiURL, ActionsHTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func permissionForProbePath(path string) string {
	switch {
	case strings.HasSuffix(path, "/actions/runs"):
		return "actions"
	case strings.HasSuffix(path, "/contents"):
		return "contents"
	case strings.HasSuffix(path, "/issues"):
		return "issues"
	case strings.HasSuffix(path, "/pulls"):
		return "pull-requests"
	default:
		return ""
	}
}
