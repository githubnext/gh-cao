package simulator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKustoBridge(t *testing.T) {
	var command string
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.URL.Path != "/v1/rest/mgmt" {
			t.Error("bridge forwarded authentication or used wrong endpoint")
		}
		var body struct{ DB, CSL string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DB != "Synthetic" {
			t.Error("invalid management envelope")
		}
		command = body.CSL
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"synthetic engine error"}}`))
	}))
	defer engine.Close()
	bridge, err := NewKusto(engine.URL, "Synthetic", []string{"TraceEvents"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/rest/ingest/Synthetic/TraceEvents?streamFormat=MultiJSON&mappingName=cao_v1",
		strings.NewReader("{\"Sequence\":0}\n{\"Sequence\":1}\n"))
	request.Header.Set("Authorization", "Bearer synthetic-not-a-credential")
	response := httptest.NewRecorder()
	bridge.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "synthetic engine error") ||
		command != ".ingest inline into table TraceEvents with (format='multijson', ingestionMappingReference='cao_v1') <|\n{\"Sequence\":0}\n{\"Sequence\":1}\n" {
		t.Fatalf("bridge changed query/error semantics: %s %s", command, response.Body.String())
	}
}

func TestKustoBridgeRejectsUnsafeInput(t *testing.T) {
	for _, endpoint := range []string{"https://example.invalid:443", "http://localhost:8080", "http://127.0.0.1:8080/path"} {
		if bridge, err := NewKusto(endpoint, "Synthetic", nil); err == nil {
			bridge.Close()
			t.Fatalf("accepted endpoint %s", endpoint)
		}
	}
	for _, name := range []string{"bad'; .drop database x", "bad/path", ""} {
		if _, err := NewKusto("http://127.0.0.1:8080", name, nil); err == nil {
			t.Fatal("accepted unsafe database")
		}
		if _, err := NewKusto("http://127.0.0.1:8080", "Synthetic", []string{name}); err == nil {
			t.Fatal("accepted unsafe table")
		}
	}
	engine := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("rejected request reached engine")
	}))
	defer engine.Close()
	bridge, err := NewKusto(engine.URL, "Synthetic", []string{"TraceEvents"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	for _, test := range []struct{ path, body string }{
		{"/v1/rest/ingest/Synthetic/Other?streamFormat=MultiJSON&mappingName=cao_v1", "{}"},
		{"/v1/rest/ingest/Synthetic/TraceEvents?streamFormat=MultiJSON&mappingName=other", "{}"},
		{"/v1/rest/ingest/Synthetic/TraceEvents?streamFormat=MultiJSON&mappingName=cao_v1", "[]"},
		{"/v1/rest/ingest/Synthetic/TraceEvents?streamFormat=MultiJSON&mappingName=cao_v1", ""},
		{"/v1/rest/query", `{"db":"Other","csl":"print 1"}`},
		{"/v1/rest/query", strings.Repeat("x", (1<<20)+1)},
		{"/unrelated", "{}"},
	} {
		response := httptest.NewRecorder()
		bridge.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, test.path, strings.NewReader(test.body)))
		if response.Code < 400 {
			t.Fatalf("accepted invalid bridge input: %s", test.path)
		}
	}
}

func TestKustoBridgeForwardsExactQuery(t *testing.T) {
	body := `{"db":"Synthetic","csl":"print value=1","properties":{"Options":{"truncationmaxrecords":7}}}`
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != body || r.URL.Path != "/v1/rest/query" {
			t.Error("bridge rewrote a production query")
		}
		_, _ = w.Write([]byte(`{"Tables":[]}`))
	}))
	defer engine.Close()
	bridge, err := NewKusto(engine.URL, "Synthetic", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	response := httptest.NewRecorder()
	bridge.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/rest/query", strings.NewReader(body)))
	if response.Code != http.StatusOK || response.Body.String() != `{"Tables":[]}` {
		t.Fatal("bridge rewrote the engine response")
	}
}
