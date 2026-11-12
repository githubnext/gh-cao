package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-cao/server/internal/redisx"
)

type blackboxFixtures struct {
	OAuth struct {
		LoginStatus    int    `json:"loginStatus"`
		RedirectPath   string `json:"redirectPath"`
		StateCookie    string `json:"stateCookie"`
		CallbackStatus int    `json:"callbackStatus"`
		SessionCookie  string `json:"sessionCookie"`
		CSRFCookie     string `json:"csrfCookie"`
	} `json:"oauth"`
	ProxyCORS []struct {
		Name          string `json:"name"`
		Method        string `json:"method"`
		Path          string `json:"path"`
		ForwardedHost string `json:"forwardedHost"`
		Origin        string `json:"origin"`
		Status        int    `json:"status"`
		AllowOrigin   string `json:"allowOrigin"`
	} `json:"proxyCors"`
	Static []struct {
		Name         string `json:"name"`
		Path         string `json:"path"`
		Authorized   bool   `json:"bearer"`
		Status       int    `json:"status"`
		ContentType  string `json:"contentType"`
		BodyContains string `json:"bodyContains"`
	} `json:"static"`
	SSE struct {
		Path          string `json:"path"`
		Status        int    `json:"status"`
		ContentType   string `json:"contentType"`
		DataPrefix    string `json:"dataPrefix"`
		Separator     string `json:"separator"`
		RevisionField string `json:"revisionField"`
	} `json:"sse"`
	Hosting struct {
		BeforeStartStatus int `json:"beforeStartStatus"`
		ActiveStatus      int `json:"activeStatus"`
		AfterDrainStatus  int `json:"afterDrainStatus"`
	} `json:"hosting"`
	MCP struct {
		Path               string   `json:"path"`
		DisabledStatus     int      `json:"disabledStatus"`
		UnauthorizedStatus int      `json:"unauthorizedStatus"`
		Tools              []string `json:"tools"`
	} `json:"mcp"`
}

func loadBlackboxFixtures(t *testing.T) blackboxFixtures {
	t.Helper()
	content, err := os.ReadFile("../../spec/blackbox-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures blackboxFixtures
	if err := json.Unmarshal(content, &fixtures); err != nil {
		t.Fatal(err)
	}
	return fixtures
}

func TestBlackboxOAuthAndProxyCORS(t *testing.T) {
	fixtures := loadBlackboxFixtures(t)
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	cors, err := (CORSPolicy{AllowedOrigins: []string{"https://tools.example.com"}}).normalize()
	if err != nil {
		t.Fatal(err)
	}
	app.config.CORS = cors

	login := httptest.NewRecorder()
	app.Handler().ServeHTTP(login, azureRequest(t, http.MethodGet, "/auth/login"))
	if login.Code != fixtures.OAuth.LoginStatus {
		t.Fatalf("login status = %d", login.Code)
	}
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil || location.Path != fixtures.OAuth.RedirectPath {
		t.Fatalf("login location = %q: %v", login.Header().Get("Location"), err)
	}
	state := firstCookie(t, login.Result(), fixtures.OAuth.StateCookie)
	if !state.HttpOnly || !state.Secure || location.Query().Get("state") == "" {
		t.Fatal("OAuth state redirect or cookie missing required protection")
	}
	authorizeFakeGitHub(t, app, location.String())
	callback := azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(location.Query().Get("state")))
	callback.AddCookie(state)
	result := httptest.NewRecorder()
	app.Handler().ServeHTTP(result, callback)
	if result.Code != fixtures.OAuth.CallbackStatus {
		t.Fatalf("callback status = %d: %s", result.Code, result.Body.String())
	}
	session := firstCookie(t, result.Result(), fixtures.OAuth.SessionCookie)
	csrf := firstCookie(t, result.Result(), fixtures.OAuth.CSRFCookie)
	if !session.HttpOnly || !session.Secure || csrf.HttpOnly || !csrf.Secure {
		t.Fatal("OAuth session/CSRF cookie attributes do not match the wire contract")
	}

	for _, fixture := range fixtures.ProxyCORS {
		t.Run(fixture.Name, func(t *testing.T) {
			request := azureRequest(t, fixture.Method, fixture.Path)
			if fixture.ForwardedHost != "" {
				request.Header.Set("X-Forwarded-Host", fixture.ForwardedHost)
			}
			if fixture.Origin != "" {
				request.Header.Set("Origin", fixture.Origin)
				request.Header.Set("Access-Control-Request-Method", http.MethodGet)
			}
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != fixture.Status ||
				response.Header().Get("Access-Control-Allow-Origin") != fixture.AllowOrigin ||
				response.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("status = %d, CORS headers = %v", response.Code, response.Header())
			}
		})
	}
}

func TestBlackboxStaticAssets(t *testing.T) {
	fixtures := loadBlackboxFixtures(t)
	app := newMCPTestApp(t, false)
	if err := os.WriteFile(filepath.Join(app.config.SiteDirectory, "fixture.js"), []byte("export default 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures.Static {
		t.Run(fixture.Name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost"+fixture.Path, nil)
			if fixture.Authorized {
				authorize(request)
			}
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != fixture.Status {
				t.Fatalf("status = %d, want %d", response.Code, fixture.Status)
			}
			if fixture.ContentType != "" && !strings.HasPrefix(response.Header().Get("Content-Type"), fixture.ContentType) {
				t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
			}
			if !strings.Contains(response.Body.String(), fixture.BodyContains) {
				t.Fatalf("body does not contain %q", fixture.BodyContains)
			}
		})
	}
}

func TestServerAgentGuide(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	app := newMCPTestApp(t, true)
	app.config.BuildRevision = sha
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), method, "http://localhost/llms.txt", nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
			response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s /llms.txt: status %d, headers %v", method, response.Code, response.Header())
		}
		if method == http.MethodHead {
			if response.Body.Len() != 0 {
				t.Fatal("HEAD /llms.txt returned a body")
			}
			continue
		}
		for _, expected := range []string{
			"Server commit SHA: " + sha,
			"https://githubnext.github.io/gh-cao/agent/llms.txt",
			"https://githubnext.github.io/gh-cao/agent-analysis/",
			"POST /mcp", "X-GitHub-OIDC-Token", "cao_catalog", "cao_query",
		} {
			if !strings.Contains(response.Body.String(), expected) {
				t.Errorf("agent guide missing %q", expected)
			}
		}
	}
	app.config.BuildRevision = "not-a-sha\nAuthorization: secret"
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/llms.txt", nil))
	if !strings.Contains(response.Body.String(), "Server commit SHA: unknown") ||
		strings.Contains(response.Body.String(), "not-a-sha") {
		t.Fatal("invalid build revision was exposed")
	}

	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	hosted := newAzureTestApp(t, github.URL)
	response = httptest.NewRecorder()
	hosted.Handler().ServeHTTP(response, azureRequest(t, http.MethodGet, "/llms.txt"))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "# Central Agentic Ops dashboard server") {
		t.Fatalf("hosted unauthenticated /llms.txt: status %d", response.Code)
	}
}

func TestBlackboxSSEFramingAndDisconnect(t *testing.T) {
	fixture := loadBlackboxFixtures(t).SSE
	address, closeRedis := fakeRedis(t)
	defer closeRedis()
	redisClient, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		database: integrationDatabase(t), hub: newEventHub(), services: redisx.NewStore(redisClient, "blackbox-stream").OperationalServices(), config: Config{HostProfile: localHostProfile()}, accessToken: testAccessToken,
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+fixture.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	authorize(request)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != fixture.Status || response.Header.Get("Content-Type") != fixture.ContentType {
		t.Fatalf("SSE status = %d, content type = %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	frame, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(frame, fixture.DataPrefix) {
		t.Fatalf("SSE data line = %q: %v", frame, err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(frame, fixture.DataPrefix))), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload[fixture.RevisionField]; !ok {
		t.Fatalf("SSE frame missing %q", fixture.RevisionField)
	}
	empty, err := reader.ReadString('\n')
	if err != nil || frame[len(frame)-1:]+empty != fixture.Separator {
		t.Fatalf("SSE frame separator = %q: %v", empty, err)
	}
	cancel()
	if _, err := io.Copy(io.Discard, reader); err == nil {
		t.Fatal("canceled client unexpectedly kept an open SSE stream")
	}
}

func TestBlackboxMCP(t *testing.T) {
	fixture := loadBlackboxFixtures(t).MCP
	disabled := newMCPTestApp(t, false)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost"+fixture.Path, nil)
	authorize(request)
	response := httptest.NewRecorder()
	disabled.Handler().ServeHTTP(response, request)
	if response.Code != fixture.DisabledStatus {
		t.Fatalf("disabled MCP status = %d", response.Code)
	}
	enabled := newMCPTestApp(t, true)
	response = httptest.NewRecorder()
	enabled.Handler().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost"+fixture.Path, nil))
	if response.Code != fixture.UnauthorizedStatus {
		t.Fatalf("unauthorized MCP status = %d", response.Code)
	}
	server := httptest.NewServer(enabled.Handler())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   server.URL + fixture.Path,
		HTTPClient: &http.Client{Transport: bearerTransport{token: testAccessToken, base: http.DefaultTransport}},
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != len(fixture.Tools) {
		t.Fatalf("MCP tools = %d, want %d", len(tools.Tools), len(fixture.Tools))
	}
	for index, name := range fixture.Tools {
		if tools.Tools[index].Name != name || tools.Tools[index].Annotations == nil || !tools.Tools[index].Annotations.ReadOnlyHint {
			t.Fatalf("MCP tool %d differs from read-only fixture: %#v", index, tools.Tools[index])
		}
	}
}
