package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalCanvasUsesSharedHandlerAndPrivateAdapter(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	prefix := "/" + strings.Repeat("a", 48)
	adapterPrefix := "/" + strings.Repeat("b", 48)
	var calls int
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if (r.URL.Path != adapterPrefix+"/__query_designer" && r.URL.Path != adapterPrefix+"/__copilot_session") || r.Header.Get("Authorization") != "" ||
			r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected adapter request: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer adapter.Close()
	app.config.Canvas = &LocalCanvasConfig{Prefix: prefix, AdapterURL: adapter.URL + adapterPrefix}
	if err := validateLocalCanvas(app.config, true); err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	request := func(method, path, origin string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), method, "http://127.0.0.1:8080"+path, bytes.NewReader(body))
		req.URL.Scheme, req.URL.Host = "", ""
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set("Authorization", "Bearer untrusted")
		req.Header.Set("Cookie", "private=secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	for _, path := range []string{"/api/v1/diagnostics", "/", "/__query_designer", "/__copilot_session"} {
		if got := request("GET", path, "", nil).Code; got != http.StatusNotFound {
			t.Fatalf("uncapable route returned %d", got)
		}
	}
	for _, path := range []string{"/sources.json", "/sources/manifest.json", "/sources/runs.json",
		"/inventory-sources.json", "/payload-hashes.json", "/gh-aw-logs.sqlite",
		"/api/admin/logs", "/api/admin/rebuild", "/api/v1/value", "/api/admission",
		"/api/conclusion", "/api/v1/cli/commands", "/auth/login", "/mcp"} {
		if got := request("GET", prefix+path, "", nil).Code; got != http.StatusNotFound {
			t.Fatalf("legacy/raw route %s returned %d", path, got)
		}
		for _, path := range []string{"/api/admin/rebuild", "/api/v1/value", "/api/admission", "/api/conclusion", "/mcp"} {
			if got := request("POST", prefix+path, "http://127.0.0.1:8080", []byte(`{}`)).Code; got != http.StatusNotFound {
				t.Fatalf("canvas exposed non-canonical mutation %s: %d", path, got)
			}
		}
	}
	index := request("GET", prefix+"/?local-preview=canvas", "", nil)
	if index.Code != http.StatusOK || index.Header().Get("X-Frame-Options") != "" ||
		!strings.Contains(index.Body.String(), `content="server-http"`) ||
		!strings.Contains(index.Body.String(), `name="dashboard-api-base" content="`+prefix+`/"`) {
		t.Fatalf("invalid canvas bootstrap: %d %s", index.Code, index.Body.String())
	}
	query := request("POST", prefix+"/api/v1/query", "http://127.0.0.1:8080",
		[]byte(`{"sourceNames":["repositories"]}`))
	if query.Code != http.StatusOK {
		t.Fatalf("query: %d %s", query.Code, query.Body.String())
	}
	var result queryResponse
	if err := json.Unmarshal(query.Body.Bytes(), &result); err != nil || len(result.Sources["repositories"].Rows) != 2 {
		t.Fatalf("canonical query result: %v", err)
	}
	for _, path := range []string{"/api/v1/query", "/__query_designer", "/__copilot_session"} {
		if got := request("POST", prefix+path, "https://untrusted.example", nil).Code; got != http.StatusForbidden {
			t.Fatalf("cross-origin request returned %d", got)
		}
	}
	for index, path := range []string{"/__query_designer", "/__copilot_session"} {
		if got := request("POST", prefix+path, "http://127.0.0.1:8080", nil).Code; got != http.StatusAccepted || calls != index+1 {
			t.Fatalf("adapter routing %s: %d %d", path, got, calls)
		}
	}
	adapter.Close()
	for _, path := range []string{"/__query_designer", "/__copilot_session"} {
		if got := request("POST", prefix+path, "http://127.0.0.1:8080", nil).Code; got != http.StatusBadGateway {
			t.Fatalf("adapter outage %s returned %d", path, got)
		}
	}
	if got := request("GET", prefix+"/api/v1/diagnostics", "", nil).Code; got != http.StatusOK {
		t.Fatalf("adapter outage affected canonical data: %d", got)
	}
}

func TestLocalCanvasMarkdownUsesPlainTextRegardlessOfSystemMIME(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	app.config.Canvas = &LocalCanvasConfig{
		Prefix:     "/" + strings.Repeat("a", 48),
		AdapterURL: "http://127.0.0.1:1234/" + strings.Repeat("b", 48),
	}
	if err := os.WriteFile(filepath.Join(app.config.SiteDirectory, "guide.md"), []byte("# Guide\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.static(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/guide.md", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		response.Body.String() != "# Guide\n" {
		t.Fatalf("canvas markdown response: %d %s %q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func TestLocalCanvasRejectsWidenedHosts(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	valid := LocalCanvasConfig{
		Prefix:     "/" + strings.Repeat("a", 48),
		AdapterURL: "http://127.0.0.1:1234/" + strings.Repeat("b", 48),
	}
	for _, target := range []string{"http://example.com:1234/", "http://localhost:1234/",
		"https://127.0.0.1:1234/", "http://127.0.0.1:1234/short",
		"http://127.0.0.1:1234/" + strings.Repeat("b", 48) + "?secret=value"} {
		config := app.config
		canvas := valid
		canvas.AdapterURL = target
		config.Canvas = &canvas
		if validateLocalCanvas(config, true) == nil {
			t.Fatal("unsafe adapter accepted")
		}
	}
	config := app.config
	config.Canvas = &valid
	if validateLocalCanvas(config, false) == nil {
		t.Fatal("hosted canvas accepted")
	}
	config.Database = nil
	config.Listen = "0.0.0.0:1234"
	if err := validateLocalCanvas(config, true); err == nil {
		t.Fatal("public canvas accepted")
	}
}
