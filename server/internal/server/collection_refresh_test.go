package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestCollectionRefreshFailsClosedForUnconfiguredAndNonAdmin(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		app := &App{}
		want := http.StatusServiceUnavailable
		if hosted {
			app.oauth = &githubOAuth{}
			want = http.StatusForbidden
		}
		response := httptest.NewRecorder()
		app.collectionRefresh(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/admin/collection/refresh", nil))
		if response.Code != want || app.inventoryRefreshConfigured() {
			t.Fatalf("unconfigured/non-admin refresh status=%d, want=%d", response.Code, want)
		}
	}
}

func TestLocalInventoryRefreshDurableAdmissionAndCompletion(t *testing.T) {
	_, config := testutil.PostgresWithConfig(t, t.Context(), "POSTGRES_URL")
	store, err := operationalpostgres.New(t.Context(), testutil.ConnectionString(t, config), "inventory-refresh-test", operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	prefix := "/" + strings.Repeat("a", 48)
	success := false
	calls := 0
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != prefix+"/inventory/refresh" {
			http.NotFound(w, r)
			return
		}
		if !success {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer adapter.Close()
	app := &App{services: store.OperationalServices(), config: Config{OperatorActions: &LocalOperatorConfig{
		AdapterURL: adapter.URL + prefix, InventoryRefresh: true,
	}}}
	if err := store.EnsureQueue(t.Context(), localInventoryQueue, localInventoryGroup); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.collectionRefresh(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/admin/collection/refresh", nil))
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"queued":true`) || calls != 0 {
		t.Fatalf("refresh was not asynchronous durable admission: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
	response = httptest.NewRecorder()
	app.collectionRefresh(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/admin/collection/refresh", nil))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("repeated manual refresh was not bounded: %d", response.Code)
	}
	if err := app.stepLocalInventoryRefresh(t.Context()); err == nil {
		t.Fatal("failed native publication was acknowledged")
	}
	stats, err := store.QueueStats(t.Context(), localInventoryQueue, localInventoryGroup)
	if err != nil || stats.Length != 1 || stats.Pending != 1 {
		t.Fatalf("failed refresh lost durable receipt: %+v %v", stats, err)
	}
	// Fresh admission is independent of a failed, still recoverable receipt.
	success = true
	if err := app.enqueueLocalInventoryRefresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.stepLocalInventoryRefresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	stats, err = store.QueueStats(t.Context(), localInventoryQueue, localInventoryGroup)
	if err != nil || stats.Length != 1 {
		t.Fatalf("successful publication did not ACK only its owned receipt: %+v %v", stats, err)
	}
	app.config.OperatorActions = nil
	if err := app.stepLocalInventoryRefresh(context.Background()); err == nil {
		t.Fatal("removed native adapter did not fail closed")
	}
}
