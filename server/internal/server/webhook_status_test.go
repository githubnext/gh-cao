package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

func TestWebhookStatusRuntimeSource(t *testing.T) {
	app := &App{}
	loader := &databaseLoader{app: app}
	source, err := loader.runtimeSource(t.Context(), webhookStatusSourceName, nil)
	if err != nil || source.Metadata["availability"] != "unavailable" || len(source.Rows) != 0 {
		t.Fatalf("unauthorized runtime source exposed counters: %+v, %v", source, err)
	}
	loader.allowCollectionHealth = true
	source, err = loader.runtimeSource(t.Context(), webhookStatusSourceName, nil)
	if err != nil || len(source.Rows) != 1 || source.Rows[0]["received"] != int64(0) ||
		source.Rows[0]["configured"] != false || source.Rows[0]["scope"] != "process" {
		t.Fatalf("runtime source requires storage or lost process status: %+v, %v", source, err)
	}
	if _, exists := source.Rows[0]["last-ping-at"]; exists {
		t.Fatal("runtime source fabricated a ping timestamp")
	}
	loader.app = nil
	if _, err := loader.runtimeSource(t.Context(), webhookStatusSourceName, nil); err == nil {
		t.Fatal("missing webhook provider did not fail closed")
	}
}

type webhookQueryProbe struct {
	dashboardstore.Store
	reads int
}

func (s *webhookQueryProbe) WithReadTransaction(context.Context, func(context.Context, postgresx.NativeReader) error) error {
	s.reads++
	return context.Canceled
}

func TestWebhookStatusQueriesBypassResultCacheTransitively(t *testing.T) {
	for _, name := range []string{webhookStatusSourceName, "derived", "joined", "union"} {
		t.Run(name, func(t *testing.T) {
			cache := &queryCacheClient{}
			database := &webhookQueryProbe{}
			app := &App{database: database, services: newTestOperationalStore(t).OperationalServices()}
			app.config.DashboardQueries = []query.Definition{
				{Name: "derived", From: webhookStatusSourceName},
				{Name: "joined", From: "$runs", Joins: []query.Join{{
					Source: "derived", On: []query.JoinKey{{Left: "id", Right: "id"}},
				}}},
				{Name: "union", From: "$runs", Union: []string{"derived"}},
			}
			_, _, err := app.executeQuery(t.Context(), queryRequest{SourceNames: []string{name}}, true)
			if !errors.Is(err, context.Canceled) || database.reads != 1 || cache.reads != 0 || cache.writes != 0 {
				t.Fatalf("live query accessed cached state: database=%d cache reads=%d writes=%d error=%v",
					database.reads, cache.reads, cache.writes, err)
			}
		})
	}
}

func TestWebhookStatusProductionSQLQuery(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	app, _ := newWebhookStatusApp(t)
	app.database = database
	for i := 0; i < 2; i++ {
		request, err := simulator.SignedRequest(t.Context(), "http://127.0.0.1/api/github/webhook",
			string(app.webhookSecret), simulator.Delivery{ID: "sql-ping", Event: "ping", Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("simulator ping returned HTTP %d", response.Code)
		}
		for _, allowed := range []bool{false, true} {
			result, status, err := app.executeQuery(t.Context(), queryRequest{
				Queries:     []query.Definition{{Name: "ingestion-webhook-status", From: webhookStatusSourceName}},
				SourceNames: []string{"ingestion-webhook-status"},
			}, allowed)
			if err != nil || status != http.StatusOK {
				t.Fatalf("production runtime SQL query returned HTTP %d: %v", status, err)
			}
			source := result.Sources["ingestion-webhook-status"]
			if !allowed {
				if len(source.Rows) != 0 || source.Metadata["availability"] != "unavailable" {
					t.Fatal("SQL query exposed unauthorized webhook counters")
				}
				continue
			}
			if len(source.Rows) != 1 || source.Rows[0]["received"] != json.Number(strconv.Itoa(i+1)) ||
				source.Rows[0]["pings"] != json.Number(strconv.Itoa(i+1)) || source.Rows[0]["duplicates"] != json.Number(strconv.Itoa(i)) {
				t.Fatalf("SQL query returned stale or invalid webhook counters: %+v", source)
			}
		}
	}
}

func newWebhookStatusApp(t *testing.T) (*App, *memory.Store) {
	t.Helper()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	services := store.OperationalServices()
	queue := collect.Queue{
		Tasks: services.Queue, Admission: services.Admission, Metadata: services.Collection,
		Leases: services.Leases, Deliveries: services.Deliveries, Metrics: services.IngestionMetrics,
	}
	return &App{
		services: services, accessToken: testAccessToken, webhookSecret: []byte("simulator-webhook-secret"),
		reconciler: &Collector{admitter: collect.Admitter{
			Enrollment: collect.Enrollment{Metadata: services.Collection, Leases: services.Leases}, Queue: queue,
		}},
	}, store
}

func readWebhookStatus(t *testing.T, handler http.Handler) webhookStatus {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1/api/v1/ingestion/webhook/status", nil)
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("webhook status returned HTTP %d, cache-control=%q", response.Code, response.Header().Get("Cache-Control"))
	}
	var status webhookStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Received != status.InFlight+status.Accepted+status.Rejected+status.Failed {
		t.Fatalf("webhook outcomes do not account for received requests: %+v", status)
	}
	return status
}

func TestWebhookRegisteredRouteValidatesSimulatorPing(t *testing.T) {
	app, _ := newWebhookStatusApp(t)
	handler := app.Handler()
	initial := readWebhookStatus(t, handler)
	if !initial.Configured || initial.Scope != "process" || initial.Received != 0 || initial.LastPingAt != "" {
		t.Fatalf("unexpected initial webhook status: %+v", initial)
	}
	delivery := simulator.Delivery{ID: "registration-ping", Event: "ping", Payload: json.RawMessage(`{"zen":"synthetic registration","hook_id":1}`)}
	for _, test := range []struct {
		name, secret string
		mutate       func(*http.Request)
		want         int
	}{
		{"invalid_signature", "incorrect-secret", nil, http.StatusUnauthorized},
		{"tampered_body", string(app.webhookSecret), func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"hook_id":2}`))
		}, http.StatusUnauthorized},
		{"missing_delivery", string(app.webhookSecret), func(r *http.Request) {
			r.Header.Del("X-GitHub-Delivery")
		}, http.StatusBadRequest},
		{"missing_event", string(app.webhookSecret), func(r *http.Request) {
			r.Header.Del("X-GitHub-Event")
		}, http.StatusBadRequest},
		{"registration_ping", string(app.webhookSecret), nil, http.StatusAccepted},
		{"duplicate_ping", string(app.webhookSecret), nil, http.StatusAccepted},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := simulator.SignedRequest(t.Context(), "http://127.0.0.1/api/github/webhook", test.secret, delivery)
			if err != nil {
				t.Fatal(err)
			}
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("registered webhook returned HTTP %d, want %d", response.Code, test.want)
			}
			if test.name == "duplicate_ping" {
				var ack struct {
					Accepted, Duplicate bool
				}
				if err := json.Unmarshal(response.Body.Bytes(), &ack); err != nil || !ack.Accepted || !ack.Duplicate {
					t.Fatal("replayed registration ping was not acknowledged as duplicate")
				}
			}
		})
	}
	status := readWebhookStatus(t, handler)
	if status.Received != 6 || status.Verified != 4 || status.Accepted != 2 ||
		status.Rejected != 4 || status.Failed != 0 || status.Duplicates != 1 || status.Pings != 2 {
		t.Fatalf("incorrect simulator ping counters: %+v", status)
	}
	for _, timestamp := range []string{status.LastReceivedAt, status.LastAcceptedAt, status.LastPingAt} {
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
			t.Fatal("missing or invalid webhook timestamp", err)
		}
	}
	if status.LastFailureAt != "" {
		t.Fatal("client rejections were reported as server failures")
	}
}

func TestWebhookStatusTracksConcurrentSimulatorDeliveries(t *testing.T) {
	app, _ := newWebhookStatusApp(t)
	// Scope webhooks may queue enrollment for a worker; this test runs admission only.
	enrollment := collect.Enrollment{Metadata: app.services.Collection, Leases: app.services.Leases}
	if err := enrollment.AddRepositories(t.Context(), 1, []string{
		"simulator/repo-00001", "simulator/repo-00002", "simulator/repo-00003", "simulator/repo-00004",
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	result, err := (simulator.Scenario{
		Name: "webhook-live-counters", Repositories: 4, EventsPerRepository: 6,
		Seed: 17, DuplicateEvery: 3,
	}).Deliver(t.Context(), server.Client(), server.URL+"/api/github/webhook", string(app.webhookSecret), 8)
	if err != nil {
		t.Fatal(err)
	}
	status := readWebhookStatus(t, app.Handler())
	if result.Attempts == 0 || result.Failed != 0 || result.Duplicates == 0 ||
		status.Received != int64(result.Attempts) || status.Verified != status.Received ||
		status.Accepted != int64(result.Accepted) || status.Duplicates != int64(result.Duplicates) ||
		status.Pings != 0 || status.Failed != 0 || status.Rejected != 0 {
		t.Fatalf("live counters do not match simulator results: status=%+v result=%+v", status, result)
	}
}

func TestWebhookStatusCountsFailuresAndSurvivesStoreOutage(t *testing.T) {
	for _, test := range []string{"missing_secret", "missing_reconciler", "admission_unavailable", "oversized_body"} {
		t.Run(test, func(t *testing.T) {
			app, store := newWebhookStatusApp(t)
			delivery := simulator.Delivery{ID: "failed-ping", Event: "ping", Payload: json.RawMessage(`{}`)}
			want, configured, verified := http.StatusServiceUnavailable, true, int64(0)
			switch test {
			case "missing_secret":
				app.webhookSecret = nil
				configured = false
			case "missing_reconciler":
				app.reconciler = nil
				configured = false
			case "admission_unavailable":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				verified = 1
			case "oversized_body":
				delivery.Payload = bytes.Repeat([]byte(" "), (10<<20)+1)
				want = http.StatusBadRequest
			}
			request, err := simulator.SignedRequest(t.Context(), "http://127.0.0.1/api/github/webhook", "simulator-webhook-secret", delivery)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != want {
				t.Fatalf("webhook returned HTTP %d, want %d", response.Code, want)
			}
			// No operational services are needed to read the live snapshot.
			status := readWebhookStatus(t, http.HandlerFunc(app.webhookStatus))
			if status.Configured != configured || status.Received != 1 || status.Accepted != 0 ||
				status.Verified != verified || status.Pings != 0 {
				t.Fatalf("incorrect failed delivery counters: %+v", status)
			}
			if want >= 500 && (status.Failed != 1 || status.LastFailureAt == "") {
				t.Fatalf("server failure was not recorded: %+v", status)
			}
			if want < 500 && (status.Rejected != 1 || status.Failed != 0) {
				t.Fatalf("invalid body was not counted as rejection: %+v", status)
			}
		})
	}
}

func TestWebhookStatusShowsInFlightDelivery(t *testing.T) {
	app, _ := newWebhookStatusApp(t)
	handler := app.Handler()
	delivery := simulator.Delivery{ID: "in-flight-ping", Event: "ping", Payload: json.RawMessage(`{}`)}
	request, err := simulator.SignedRequest(t.Context(), "http://127.0.0.1/api/github/webhook", string(app.webhookSecret), delivery)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	request.Body = reader
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(response, request)
	}()
	if _, err := writer.Write(delivery.Payload); err != nil {
		t.Fatal(err)
	}
	status := readWebhookStatus(t, handler)
	if status.Received != 1 || status.InFlight != 1 || status.Verified != 0 || status.Accepted != 0 {
		t.Errorf("blocked body was not visible in live status: %+v", status)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook did not finish after body completed")
	}
	status = readWebhookStatus(t, handler)
	if response.Code != http.StatusAccepted || status.InFlight != 0 || status.Verified != 1 || status.Accepted != 1 {
		t.Fatalf("completed delivery left incorrect status: %+v, HTTP %d", status, response.Code)
	}
}

func TestWebhookStatusAuthorizationAndProcessIsolation(t *testing.T) {
	app, _ := newWebhookStatusApp(t)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1/api/v1/ingestion/webhook/status", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status returned HTTP %d", response.Code)
	}
	app.oauth = &githubOAuth{}
	request = request.WithContext(context.WithValue(t.Context(), oauthSessionContextKey{}, oauthSession{Login: "reader"}))
	response = httptest.NewRecorder()
	app.webhookStatus(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-administrator status returned HTTP %d", response.Code)
	}
	fresh := &App{}
	status := readWebhookStatus(t, http.HandlerFunc(fresh.webhookStatus))
	if status.Configured || status.Received != 0 || status.LastReceivedAt != "" {
		t.Fatalf("new process inherited webhook state: %+v", status)
	}
}
