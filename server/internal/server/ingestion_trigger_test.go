package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartIngestionForwardsToNativeAdapter(t *testing.T) {
	adapterPrefix := "/" + strings.Repeat("b", 48)
	calls := 0
	status := http.StatusAccepted
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != adapterPrefix+"/ingest" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.WriteHeader(status)
	}))
	defer adapter.Close()
	start := func(app *App) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		app.startIngestion(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/ingestion/start", nil))
		return response
	}
	if response := start(&App{}); response.Code != http.StatusNotFound {
		t.Fatalf("missing adapter status = %d", response.Code)
	}
	unsafe := &App{config: Config{OperatorActions: &LocalOperatorConfig{AdapterURL: "http://example.com" + adapterPrefix}}}
	if response := start(unsafe); response.Code != http.StatusNotFound || calls != 0 {
		t.Fatalf("non-loopback adapter status = %d calls = %d", response.Code, calls)
	}
	app := &App{config: Config{OperatorActions: &LocalOperatorConfig{AdapterURL: adapter.URL + adapterPrefix}}}
	if response := start(app); response.Code != http.StatusAccepted || calls != 1 ||
		!strings.Contains(response.Body.String(), `"requested":true`) {
		t.Fatalf("forwarded status = %d calls = %d body = %s", response.Code, calls, response.Body)
	}
	status = http.StatusNotFound
	if response := start(app); response.Code != http.StatusBadGateway {
		t.Fatalf("rejected adapter status = %d", response.Code)
	}
}
