package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

func TestNativeMCPReadOnlyContract(t *testing.T) {
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	dsn := nativeTestSnapshot(t, nil)
	handler, err := nativeMCPHandler(catalog, func(context.Context) (dashboardquery.CanonicalReader, func() error, error) {
		db, err := openNativeSnapshot(dsn)
		if err != nil {
			return nil, nil, err
		}
		return db, db.Close, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, method, path, protocol, declared, body string
		status                                       int
		contains                                     string
	}{
		{name: "health", method: "GET", path: "/healthz", status: 200, contains: `"status":"ok"`},
		{name: "endpoint", method: "GET", path: "/nope", status: 404, contains: "Unknown endpoint"},
		{name: "post only", method: "GET", path: "/mcp", status: 405, contains: "accepts POST"},
		{name: "protocol required", method: "POST", path: "/mcp", status: 400, contains: "header is required"},
		{name: "unsupported protocol", method: "POST", path: "/mcp", protocol: "2020-01-01", status: 400, contains: "Unsupported MCP"},
		{name: "json invalid", method: "POST", path: "/mcp", protocol: "2026-07-28", body: "{", status: 400, contains: "not valid JSON"},
		{name: "batch unsupported", method: "POST", path: "/mcp", protocol: "2026-07-28", body: "[]", status: 400, contains: "Request must be a JSON-RPC"},
		{name: "method required", method: "POST", path: "/mcp", protocol: "2026-07-28", body: `{"jsonrpc":"2.0","method":"tools/list"}`, status: 400, contains: "Mcp-Method header is required"},
		{name: "method mismatch", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/call", body: `{"jsonrpc":"2.0","method":"tools/list"}`, status: 400, contains: "does not match request"},
		{name: "discover", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "server/discover", body: `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`, status: 200, contains: "untrusted data"},
		{name: "list", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/list", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, status: 200, contains: `"readOnlyHint":true`},
		{name: "no write tool", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/call", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{}}}`, status: 400, contains: "Unknown tool"},
		{name: "catalog", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/call", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cao_catalog","arguments":{"kind":"queries","id":"campaign-runs"}}}`, status: 200, contains: `"structuredContent"`},
		{name: "query", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/call", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cao_query","arguments":{"id":"campaign-runs"}}}`, status: 200, contains: `"query":"campaign-runs"`},
		{name: "query refuses SQL", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/call", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cao_query","arguments":{"id":"SELECT * FROM runs"}}}`, status: 200, contains: `"isError":true`},
		{name: "typed limit", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/call", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cao_query","arguments":{"id":"campaign-runs","limit":"5"}}}`, status: 200, contains: "positive integer"},
		{name: "oversized", method: "POST", path: "/mcp", protocol: "2026-07-28", declared: "tools/list", body: strings.Repeat(" ", 65537), status: 413, contains: "exceeds 65536 bytes"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("MCP-Protocol-Version", tt.protocol)
			request.Header.Set("Mcp-Method", tt.declared)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.status || !strings.Contains(response.Body.String(), tt.contains) {
				t.Fatalf("status=%d body=%s, want %d containing %q", response.Code, response.Body, tt.status, tt.contains)
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" || !json.Valid(response.Body.Bytes()) {
				t.Fatal("response is not a non-cacheable JSON contract")
			}
		})
	}
}
