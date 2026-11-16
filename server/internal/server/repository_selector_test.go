package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
)

func TestRepositorySelectorCookieFollowsLocalTransport(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<head></head>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{accessToken: testAccessToken, config: Config{
		SiteDirectory: site,
		OperatorActions: &LocalOperatorConfig{
			OperatorActions: &LocalOperatorActionsConfig{Repositories: []string{"octo/control"}},
		},
	}}
	for scheme, secure := range map[string]bool{"http": false, "https": true} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, scheme+"://127.0.0.1:8080/", nil)
		response := httptest.NewRecorder()
		app.serveIndex(response, request, testAccessToken)
		var session *http.Cookie
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == localCLISessionCookie {
				session = cookie
			}
		}
		if session == nil || session.Secure != secure || !session.HttpOnly || session.Path != "/__cli_commands" || session.SameSite != http.SameSiteStrictMode {
			t.Fatalf("%s session cookie does not match local transport: %+v", scheme, session)
		}
	}
}

func TestRepositorySelectorUsesDeclaredGitHubLookupAndLocalCapability(t *testing.T) {
	catalog, err := hostedcli.ParseCatalog([]byte(`{"dashboard":{"cli-actions":[{
		"id":"add-repository","command":"gh cao repos add {{repository-pattern}}",
		"selector":{"query":{"command":"gh search repos","fields":["fullName","description"],"limit":2},
		"mapping":{"value":"fullName","label":"fullName","description":"description","target":"repository-pattern"}}
	}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repositories/search" || r.Method != http.MethodPost {
			t.Errorf("unexpected adapter request: %s %s", r.Method, r.URL.Path)
		}
		var input struct {
			Text  string `json:"text"`
			Limit int    `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Limit != 2 {
			t.Errorf("unexpected lookup request: %+v %v", input, err)
		}
		switch input.Text {
		case "failed":
			http.Error(w, "private upstream diagnostics", http.StatusBadGateway)
		case "malformed":
			_, _ = io.WriteString(w, "{")
		case "empty":
			_, _ = io.WriteString(w, "[]")
		case "null":
			_, _ = io.WriteString(w, "null")
		case "oversized":
			_, _ = io.WriteString(w, `[{"fullName":"octo/a"},{"fullName":"octo/b"},{"fullName":"octo/c"}]`)
		case "invalid":
			_, _ = io.WriteString(w, `[{"fullName":"--repo"}]`)
		default:
			if input.Text != "octo/ser" {
				t.Errorf("search text was not preserved: %q", input.Text)
			}
			_, _ = io.WriteString(w, `[{"fullName":"Octo/Service","description":"GitHub suggestion","unselected":"not returned"}]`)
		}
	}))
	defer adapter.Close()
	app := &App{
		accessToken: testAccessToken,
		config:      Config{OperatorActions: &LocalOperatorConfig{AdapterURL: adapter.URL}},
		hostedCLI:   &hostedCLI{catalog: catalog, local: &LocalOperatorActionsConfig{Repositories: []string{"octo/control"}}},
	}
	for _, test := range []struct {
		name, body, origin, credential string
		status                         int
		calls                          bool
	}{
		{"cookie", `{"id":"add-repository","text":"octo/ser"}`, "http://127.0.0.1:8080", "cookie", 200, true},
		{"bearer", `{"id":"add-repository","text":"octo/ser"}`, "http://127.0.0.1:8080", "bearer", 200, true},
		{"no auth", `{"id":"add-repository","text":"octo/ser"}`, "http://127.0.0.1:8080", "", 403, false},
		{"forged cookie", `{"id":"add-repository","text":"octo/ser"}`, "http://127.0.0.1:8080", "forged", 403, false},
		{"cross origin", `{"id":"add-repository","text":"octo/ser"}`, "https://evil.example", "cookie", 403, false},
		{"missing origin", `{"id":"add-repository","text":"octo/ser"}`, "", "cookie", 403, false},
		{"unknown action", `{"id":"other","text":"octo/ser"}`, "http://127.0.0.1:8080", "cookie", 404, false},
		{"wildcard", `{"id":"add-repository","text":"octo/*"}`, "http://127.0.0.1:8080", "cookie", 400, false},
		{"short text", `{"id":"add-repository","text":"o"}`, "http://127.0.0.1:8080", "cookie", 400, false},
		{"control text", `{"id":"add-repository","text":"octo\nrepo"}`, "http://127.0.0.1:8080", "cookie", 400, false},
		{"browser command", `{"id":"add-repository","text":"octo/ser","command":"gh repo delete"}`, "http://127.0.0.1:8080", "cookie", 400, false},
		{"browser limit", `{"id":"add-repository","text":"octo/ser","limit":1000}`, "http://127.0.0.1:8080", "cookie", 400, false},
		{"upstream failure", `{"id":"add-repository","text":"failed"}`, "http://127.0.0.1:8080", "cookie", 502, true},
		{"bad response", `{"id":"add-repository","text":"malformed"}`, "http://127.0.0.1:8080", "cookie", 502, true},
		{"null response", `{"id":"add-repository","text":"null"}`, "http://127.0.0.1:8080", "cookie", 502, true},
		{"over limit", `{"id":"add-repository","text":"oversized"}`, "http://127.0.0.1:8080", "cookie", 502, true},
		{"invalid option", `{"id":"add-repository","text":"invalid"}`, "http://127.0.0.1:8080", "cookie", 502, true},
		{"no matches", `{"id":"add-repository","text":"empty"}`, "http://127.0.0.1:8080", "cookie", 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8080/__cli_commands/selector", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			switch test.credential {
			case "cookie":
				request.AddCookie(&http.Cookie{Name: localCLISessionCookie, Value: app.localCLISession()})
			case "forged":
				request.AddCookie(&http.Cookie{Name: localCLISessionCookie, Value: "wrong"})
			case "bearer":
				request.Header.Set("Authorization", "Bearer "+testAccessToken)
			}
			before := calls
			response := httptest.NewRecorder()
			app.repositorySelector(response, request)
			if response.Code != test.status {
				t.Fatalf("got %d expected %d: %s", response.Code, test.status, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" || (calls > before) != test.calls {
				t.Fatal("lookup bypassed authorization, declared limits, or private response caching")
			}
			if strings.Contains(response.Body.String(), "private upstream") || strings.Contains(response.Body.String(), "unselected") {
				t.Fatal("lookup exposed unmapped upstream data")
			}
			if test.name == "cookie" && response.Body.String() != "{\"options\":[{\"value\":\"Octo/Service\",\"label\":\"Octo/Service\",\"description\":\"GitHub suggestion\"}]}\n" {
				t.Fatalf("unexpected options: %s", response.Body.String())
			}
			if test.name == "no matches" && response.Body.String() != "{\"options\":[]}\n" {
				t.Fatal("empty search results were replaced with unverified input")
			}
		})
	}
}

func TestRepositorySelectorCancelsNativeLookup(t *testing.T) {
	canceled := make(chan struct{})
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		close(canceled)
	}))
	defer adapter.Close()
	catalog, err := hostedcli.LoadCatalog(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		accessToken: testAccessToken,
		config:      Config{OperatorActions: &LocalOperatorConfig{AdapterURL: adapter.URL}},
		hostedCLI:   &hostedCLI{catalog: catalog, local: &LocalOperatorActionsConfig{}},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:8080/__cli_commands/selector", strings.NewReader(`{"id":"add-repository","text":"octo/repo"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	response := httptest.NewRecorder()
	app.repositorySelector(response, request)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline did not report unavailable lookup: %d %s", response.Code, response.Body.String())
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("abandoned lookup was not canceled in the native adapter")
	}
}
