package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

var sessionMCPArguments = json.RawMessage(`{"repository":"octo/one","language-version":"0.2.0","query":{
"name":"session-events","subject":"Inspect retained synthetic events.","from":"archive-events",
"select":[{"field":"Sequence"}]}}`)

func TestMCPSessionDataLakeScalarDefinitions(t *testing.T) {
	for _, name := range []string{"execution-step-count", "execution-duration", "working-set-rebuild-factor", "context-growth", "dependency-order-violation-rate", "evidence-saturation-stopping-lag", "recurrence-laminarity", "recurrence-trapping-time"} {
		t.Run(name, func(t *testing.T) {
			// #nosec G304 -- Fixed reviewed names, not caller-provided file paths.
			content, err := os.ReadFile("../sessionarchive/testdata/graders/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var definition sessionarchive.Definition
			if err := json.Unmarshal(content, &definition); err != nil {
				t.Fatal(err)
			}
			app := newMCPTestApp(t, true)
			reader := &sessionArchiveReadProbe{}
			probe := &sessionArchiveProbe{query: func(_ context.Context, input sessionArchiveQuery) (sessionArchiveResult, error) {
				if input.LanguageVersion != "0.2.0" || input.Query.Name != name || len(input.Query.PostAggregate) == 0 {
					t.Fatal("new typed query did not reach archive boundary unchanged")
				}
				rows := []map[string]any{{"value": nil, "availability": "unavailable", "reason": "synthetic-test"}}
				metadata := definition.Metadata(false, 0, 0)
				return sessionArchiveResult{Kind: "session-query", Generation: 1, Rows: &rows,
					Columns:  []sessionarchive.Column{{Name: "value", Type: "number"}, {Name: "availability", Type: "string"}, {Name: "reason", Type: "string"}},
					Metadata: &metadata, reader: reader}, nil
			}}
			app.sessionArchive = &sessionArchiveService{backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true}}
			for _, version := range []string{"0.1.0", "0.2.0"} {
				arguments := map[string]any{"repository": "octo/one", "language-version": version, "query": definition}
				request := mcpLogsRequest(t, "tools/call", map[string]any{"name": "cao_session_query", "arguments": arguments})
				authorize(request)
				response := httptest.NewRecorder()
				app.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"isError":true`) != (version == "0.1.0") {
					t.Fatalf("scalar profile or typed null rejected by SDK: %s", response.Body.String())
				}
			}
			if probe.called != 1 || reader.closed != 1 {
				t.Fatal("scalar query bypassed profile admission or lost HTTP-owned reader lifetime")
			}
		})
	}
}

func TestMCPSessionDataLakeAccessAndReaderLifetime(t *testing.T) {
	for _, outcome := range []string{"success", "no-grant", "actions", "disabled", "unsupported-backend", "query-error",
		"revoked", "withdrawn", "response-budget"} {
		t.Run(outcome, func(t *testing.T) {
			app := newMCPTestAppWithActions(t, testActionsToken, testActionsActor)
			reader := &sessionArchiveReadProbe{}
			probe := &sessionArchiveProbe{query: func(ctx context.Context, input sessionArchiveQuery) (sessionArchiveResult, error) {
				if input.Kind != "session-query" || input.Repository != "octo/one" || input.Query.From != "archive-events" {
					t.Fatal("session data lake call reached the wrong query boundary")
				}
				rows := []map[string]any{{"Sequence": 7}}
				if outcome == "response-budget" {
					rows = []map[string]any{{"Sequence": strings.Repeat("synthetic-private-canary", 200000)}}
				}
				result := sessionArchiveResult{
					Kind: "session-query", Generation: 1, Rows: &rows,
					Columns:  []sessionarchive.Column{{Name: "Sequence", Type: "integer"}},
					Metadata: &sessionarchive.QueryMetadata{UpstreamCoverage: "unknown"}, reader: reader,
				}
				switch outcome {
				case "query-error":
					return result, errors.New("synthetic backend failure")
				case "revoked":
					app.sessionArchive.read.Approved = false
				case "withdrawn":
					reader.deliver = func(func() error) error { return sessionarchive.ErrWithdrawn }
				}
				return result, nil
			}}
			app.sessionArchive = &sessionArchiveService{
				backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: outcome != "no-grant"},
			}
			if outcome == "disabled" {
				app.sessionArchive = nil
			}
			if outcome == "unsupported-backend" {
				probe.kinds = []string{"timeline", "summary"}
			}
			request := mcpLogsRequest(t, "tools/call", map[string]any{"name": "cao_session_query", "arguments": sessionMCPArguments})
			authorize(request)
			if outcome == "actions" {
				request.Header.Set("Authorization", "Bearer "+testActionsToken)
				request.Header.Set("X-GitHub-Actor", testActionsActor)
			}
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			var result struct {
				Result struct {
					IsError           bool                 `json:"isError"`
					StructuredContent sessionArchiveResult `json:"structuredContent"`
				}
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || result.Result.IsError != (outcome != "success") {
				t.Fatalf("session data lake call returned %d %s", response.Code, response.Body.String())
			}
			queried := outcome == "success" || outcome == "query-error" || outcome == "revoked" || outcome == "withdrawn" || outcome == "response-budget"
			if (probe.called == 1) != queried || (reader.closed == 1) != queried {
				t.Fatalf("authorization or reader ownership changed: calls=%d closes=%d", probe.called, reader.closed)
			}
			if outcome != "success" && (result.Result.StructuredContent.Rows != nil || strings.Contains(response.Body.String(), "synthetic-private-canary")) {
				t.Fatal("refused session data lake call released analytical rows")
			}
		})
	}
}

func TestMCPSessionDataLakeDiscoveryAndValidation(t *testing.T) {
	app := newMCPTestApp(t, true)
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{backend: probe, repositories: []string{"octo/one"}}
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
	if strings.Count(session.InitializeResult().Instructions, "cao_session_query") != 1 {
		t.Fatal("hosted MCP instructions omitted or duplicated the session data lake boundary")
	}
	for _, approved := range []bool{false, true} {
		app.sessionArchive.read.Approved = approved
		request := mcpLogsRequest(t, "tools/list", map[string]any{})
		authorize(request)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		var listing struct {
			Result struct{ Tools []mcpContractTool }
		}

		if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		found := slices.ContainsFunc(listing.Result.Tools, func(tool mcpContractTool) bool {
			if tool.Name != "cao_session_query" {
				return false
			}
			if tool.Annotations["readOnlyHint"] != true || tool.Annotations["untrustedContentHint"] != true {
				t.Fatal("session data lake tool lost read-only/untrusted annotations")
			}
			return true
		})
		if found != approved {
			t.Fatal("tool discovery ignored separate trace-read authorization")
		}
	}
	for _, arguments := range []json.RawMessage{
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"0.2.0"`, `"0.1.0"`, 1)),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"0.2.0"`, `"2.0.0"`, 1)),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"Sequence"`, `"PayloadJSON"`, 1)),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"octo/one"`, `"octo/two"`, 1)),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"octo/one"`, `"Octo/One"`, 1)),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"query":`, `"kind":"timeline","query":`, 1)),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"query":`, `"sql":"SELECT 1","query":`, 1)),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"query":`, `"path":"/private/archive.parquet","query":`, 1)),
		json.RawMessage(`{"repository":"octo/one","language-version":"0.2.0","query":null}`),
		json.RawMessage(strings.Replace(string(sessionMCPArguments), `"Inspect retained synthetic events."`, `"`+strings.Repeat("x", sessionArchiveRequestBytes)+`"`, 1)),
	} {
		request := mcpLogsRequest(t, "tools/call", map[string]any{"name": "cao_session_query", "arguments": arguments})
		authorize(request)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if !strings.Contains(response.Body.String(), `"isError":true`) || probe.called != 0 {
			t.Fatalf("invalid session query reached execution: %s", response.Body.String())
		}
	}
}

func TestMCPSessionDataLakeHostedGrantAndCSRF(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.config.AgentCatalogPath, app.config.MCPContractPath = testAgentCatalog, testMCPContract
	var err error
	app.config.DashboardQueries, err = ParseDashboardQueries("../../../dashboard/site/src/agent/queries.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := callbackSession(t, app)
	session, err := app.oauth.loadSession(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	app.config.AdminUsers = []string{session.Login}
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true}}
	app.mcp, err = app.newMCPHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"granted", "admin-without-trace-grant", "missing-csrf", "reader-revoked"} {
		t.Run(outcome, func(t *testing.T) {
			probe.called = 0
			reader := &sessionArchiveReadProbe{}
			app.sessionArchive.read.Users = []string{strings.ToLower(session.Login)}
			if outcome == "admin-without-trace-grant" {
				app.sessionArchive.read.Users = nil
			}
			probe.query = func(context.Context, sessionArchiveQuery) (sessionArchiveResult, error) {
				rows := []map[string]any{{"Sequence": 7}}
				if outcome == "reader-revoked" {
					app.sessionArchive.read.Users = nil
				}
				return sessionArchiveResult{Kind: "session-query", Generation: 1, Rows: &rows,
					Columns:  []sessionarchive.Column{{Name: "Sequence", Type: "integer"}},
					Metadata: &sessionarchive.QueryMetadata{UpstreamCoverage: "unknown"}, reader: reader}, nil
			}
			request := mcpLogsRequest(t, "tools/call", map[string]any{"name": "cao_session_query", "arguments": sessionMCPArguments})
			hosted := azureRequest(t, http.MethodPost, "/mcp")
			request.URL, request.Host, request.TLS = hosted.URL, hosted.Host, hosted.TLS
			request.Header.Set("X-Forwarded-Host", hosted.Header.Get("X-Forwarded-Host"))
			request.Header.Set("X-Forwarded-Proto", hosted.Header.Get("X-Forwarded-Proto"))
			request.AddCookie(cookie)
			if outcome != "missing-csrf" {
				request.Header.Set("X-CSRF-Token", csrf.Value)
			}
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if outcome == "missing-csrf" {
				if response.Code != http.StatusForbidden || probe.called != 0 {
					t.Fatal("session data lake tool bypassed hosted CSRF protection")
				}
				return
			}
			var message struct {
				Result struct {
					IsError bool `json:"isError"`
				}
			}
			if json.Unmarshal(response.Body.Bytes(), &message) != nil || response.Code != http.StatusOK ||
				message.Result.IsError != (outcome != "granted") {
				t.Fatalf("hosted session data lake grant changed: %d %s", response.Code, response.Body.String())
			}
			queried := outcome != "admin-without-trace-grant"
			if (probe.called == 1) != queried || (reader.closed == 1) != queried {
				t.Fatal("hosted grant refusal reached data or leaked a reader")
			}
		})
	}
}

func TestMCPSessionDataLakeCanceledMethodDoesNotQuery(t *testing.T) {
	app := newMCPTestApp(t, true)
	probe := &sessionArchiveProbe{}
	app.sessionArchive = &sessionArchiveService{backend: probe, repositories: []string{"octo/one"}, read: SessionArchiveReadAccess{Approved: true}}
	request := mcpLogsRequest(t, "tools/call", map[string]any{})
	authorize(request)
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), mcpArchiveDeliveryKey{}, &mcpArchiveDelivery{request: request}))
	cancel()
	result := (&mcpRuntime{app: app}).call(ctx, "cao_session_query", sessionMCPArguments)
	if !result.IsError || probe.called != 0 {
		t.Fatal("canceled MCP method executed a session data lake query")
	}
}
