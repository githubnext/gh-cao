package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

func mcpLogsRequest(t *testing.T, method string, params map[string]any) *http.Request {
	t.Helper()
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo":         map[string]string{"name": "test", "version": "1"},
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		request.Header.Set("Mcp-Name", name)
	}
	return request
}

func TestMCPLogsOptInAndLocalActionsAccess(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		actions bool
	}{
		{"disabled", false, false},
		{"local administrator", true, false},
		{"actions", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CAO_SERVER_LOGS_ENABLED", "false")
			if test.enabled {
				t.Setenv("CAO_SERVER_LOGS_ENABLED", "true")
			}
			app := newMCPTestAppWithActions(t, testActionsToken, testActionsActor)
			authenticate := func(request *http.Request) {
				authorize(request)
				if test.actions {
					request.Header.Set("Authorization", "Bearer "+testActionsToken)
					request.Header.Set("X-GitHub-Actor", testActionsActor)
				}
			}
			request := mcpLogsRequest(t, "tools/list", map[string]any{})
			authenticate(request)
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			var listing struct {
				Result struct {
					Tools []mcpContractTool `json:"tools"`
				} `json:"result"`
			}
			if response.Code != http.StatusOK {
				t.Fatalf("tools/list returned %d: %s", response.Code, response.Body.String())
			}
			if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(listing.Result.Tools))
			for _, tool := range listing.Result.Tools {
				names = append(names, tool.Name)
				if tool.Annotations["readOnlyHint"] != true || tool.Annotations["untrustedContentHint"] != true {
					t.Fatalf("unsafe annotations for %s: %v", tool.Name, tool.Annotations)
				}
			}
			expected := []string{"cao_catalog", "cao_query"}
			if test.enabled {
				expected = []string{"cao_catalog", "cao_logs", "cao_query"}
			}
			if !slices.Equal(names, expected) {
				t.Fatalf("tools = %v, want %v", names, expected)
			}
			request = mcpLogsRequest(t, "tools/call", map[string]any{"name": "cao_logs", "arguments": map[string]any{}})
			authenticate(request)
			response = httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if !test.enabled {
				if !strings.Contains(response.Body.String(), "Unknown tool") && !strings.Contains(response.Body.String(), "unknown tool") {
					t.Fatalf("disabled log tool was not rejected: %s", response.Body.String())
				}
				return
			}
			assertMCPLogsSnapshot(t, response)

			request = mcpLogsRequest(t, "tools/call", map[string]any{
				"name": "cao_logs", "arguments": map[string]any{"unexpected": true},
			})
			authenticate(request)
			response = httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if !strings.Contains(response.Body.String(), `"isError":true`) {
				t.Fatalf("unexpected arguments accepted: %s", response.Body.String())
			}
			if test.actions {
				for _, identity := range []struct{ token, actor string }{
					{testActionsToken + "x", testActionsActor},
					{testActionsToken, "attacker"},
					{testActionsToken, ""},
				} {
					request = mcpLogsRequest(t, "tools/call", map[string]any{"name": "cao_logs", "arguments": map[string]any{}})
					request.Header.Set("Authorization", "Bearer "+identity.token)
					request.Header.Set("X-GitHub-Actor", identity.actor)
					response = httptest.NewRecorder()
					app.Handler().ServeHTTP(response, request)
					if response.Code != http.StatusUnauthorized {
						t.Fatalf("invalid Actions identity returned %d: %s", response.Code, response.Body.String())
					}
				}
			}
		})
	}
}

func TestMCPLogsDisabledRuntime(t *testing.T) {
	runtime := &mcpRuntime{app: &App{}}
	result := runtime.call(t.Context(), "cao_logs", json.RawMessage(`{}`))
	if !result.IsError || result.StructuredContent != nil {
		t.Fatalf("disabled logs returned a snapshot: %#v", result)
	}
}

func assertMCPLogsSnapshot(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("logs returned %d, headers %v: %s", response.Code, response.Header(), response.Body.String())
	}
	var message struct {
		Result struct {
			IsError           bool              `json:"isError"`
			StructuredContent serverLogSnapshot `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	snapshot := message.Result.StructuredContent
	if message.Result.IsError || snapshot.Logs == nil || snapshot.Operational.Status == "" || len(message.Result.Content) != 1 {
		t.Fatalf("invalid log snapshot: %s", response.Body.String())
	}
	var textSnapshot serverLogSnapshot
	if err := json.Unmarshal([]byte(message.Result.Content[0].Text), &textSnapshot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, textSnapshot) {
		t.Fatal("text and structured snapshots differ")
	}
}

func TestMCPLogsHostedAdministratorSession(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	definitions, err := ParseDashboardQueries("../../../dashboard/site/src/agent/queries.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	app.config.DashboardQueries = definitions
	app.config.AgentCatalogPath, app.config.MCPContractPath = testAgentCatalog, testMCPContract
	app.logs = logger.EnableBuffer()
	app.mcp, err = app.newMCPHandler()
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := callbackSession(t, app)
	session, err := app.oauth.loadSession(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	app.config.AdminUsers = []string{strings.ToUpper(session.Login)}
	for _, test := range []struct {
		name   string
		admin  bool
		csrf   string
		cookie string
		bearer string
		status int
	}{
		{"administrator", true, csrf.Value, cookie.Value, "", http.StatusOK},
		{"missing CSRF", true, "", cookie.Value, "", http.StatusForbidden},
		{"invalid CSRF", true, "invalid", cookie.Value, "", http.StatusForbidden},
		{"non administrator", false, csrf.Value, cookie.Value, "", http.StatusForbidden},
		{"expired session", true, csrf.Value, "invalid-session", "", http.StatusUnauthorized},
		{"bearer cannot fall back to admin session", true, csrf.Value, cookie.Value, "invalid", http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			app.config.AdminUsers = nil
			if test.admin {
				app.config.AdminUsers = []string{strings.ToUpper(session.Login)}
			}
			for _, method := range []string{"tools/list", "tools/call"} {
				params := map[string]any{}
				if method == "tools/call" {
					params = map[string]any{"name": "cao_logs", "arguments": map[string]any{}}
				}
				request := mcpLogsRequest(t, method, params)
				hosted := azureRequest(t, http.MethodPost, "/mcp")
				request.URL, request.Host, request.TLS = hosted.URL, hosted.Host, hosted.TLS
				request.Header.Set("X-Forwarded-Host", hosted.Header.Get("X-Forwarded-Host"))
				request.Header.Set("X-Forwarded-Proto", hosted.Header.Get("X-Forwarded-Proto"))
				request.AddCookie(&http.Cookie{
					Name: sessionCookieName, Value: test.cookie,
					Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
				})
				request.Header.Set("X-CSRF-Token", test.csrf)
				if test.bearer != "" {
					request.Header.Set("Authorization", "Bearer "+test.bearer)
				}
				response := httptest.NewRecorder()
				app.Handler().ServeHTTP(response, request)
				if response.Code != test.status || response.Header().Get("Location") != "" {
					t.Fatalf("%s returned %d: %s", method, response.Code, response.Body.String())
				}
				if method == "tools/call" && test.status == http.StatusOK {
					assertMCPLogsSnapshot(t, response)
				}
			}
		})
	}
}

func TestMCPLogsSharesHTTPAuthorizationAndSafeSnapshot(t *testing.T) {
	for _, test := range []struct {
		name   string
		ctx    context.Context
		status int
	}{
		{"no session", t.Context(), http.StatusForbidden},
		{"viewer", context.WithValue(t.Context(), oauthSessionContextKey{}, oauthSession{Login: "viewer"}), http.StatusForbidden},
		{"admin", context.WithValue(t.Context(), oauthSessionContextKey{}, assuredSession("operator")), http.StatusOK},
		{"actions", context.WithValue(t.Context(), githubActionsActorContextKey{}, "workflow"), http.StatusOK},
		{"empty actor", context.WithValue(t.Context(), githubActionsActorContextKey{}, ""), http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, client := range []logCounterClient{
				{fields: []any{"taskQueued", "3", "unexpected", "private-value"}},
				{err: errors.New("private Operational failure")},
			} {
				app := &App{
					logs: logger.EnableBuffer(), config: Config{AdminUsers: []string{"operator"}},
					oauth: &githubOAuth{}, services: newTestOperationalStore(t, client).OperationalServices(),
				}
				runtime := &mcpRuntime{app: app}
				payload, err := runtime.callLogs(test.ctx, map[string]any{})
				response := httptest.NewRecorder()
				app.serverLogs(response, httptest.NewRequestWithContext(test.ctx, http.MethodGet, "/api/admin/logs", nil))
				if response.Code != test.status || (err == nil) != (test.status == http.StatusOK) {
					t.Fatalf("authorization differs: HTTP=%d MCP=%v", response.Code, err)
				}
				if err != nil {
					continue
				}
				var httpSnapshot serverLogSnapshot
				if err := json.Unmarshal(response.Body.Bytes(), &httpSnapshot); err != nil {
					t.Fatal(err)
				}
				snapshot := payload.(serverLogSnapshot)
				if !reflect.DeepEqual(snapshot.Operational, httpSnapshot.Operational) || snapshot.Logs == nil {
					t.Fatalf("MCP and HTTP snapshots differ: %+v / %+v", snapshot.Operational, httpSnapshot.Operational)
				}
				data, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "private") || strings.Contains(string(data), "unexpected") {
					t.Fatalf("private Operational data leaked: %s", data)
				}
			}
		})
	}
}
