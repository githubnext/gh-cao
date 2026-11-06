package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/redisx"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type testReconciler struct {
	calls  int
	event  GitHubWebhook
	called chan struct{}
}

type emptyRedisClient struct{}

func (emptyRedisClient) Do(_ context.Context, command ...string) (any, error) {
	switch command[0] {
	case "PING":
		return "PONG", nil
	case "HGETALL":
		return []any{}, nil
	default:
		return nil, nil
	}
}

func (emptyRedisClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, nil
}

func (reconciler *testReconciler) Rebuild(context.Context) (ingest.Result, error) {
	reconciler.calls++
	return ingest.Result{Revision: 2}, nil
}

func (reconciler *testReconciler) Reconcile(_ context.Context, event GitHubWebhook) (ingest.Result, error) {
	reconciler.calls++
	reconciler.event = event
	if reconciler.called != nil {
		close(reconciler.called)
	}
	return ingest.Result{Revision: 2}, nil
}

func TestWebhookSignatureVerification(t *testing.T) {
	payload := []byte(`{"action":"completed"}`)
	secret := []byte("webhook-secret")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !validWebhookSignature(payload, signature, secret) {
		t.Fatal("valid GitHub webhook signature was rejected")
	}
	if validWebhookSignature([]byte(`{"action":"tampered"}`), signature, secret) {
		t.Fatal("tampered webhook payload was accepted")
	}
}

func TestReconcileInterruptedRebuildLeavesHeldLockUntouched(t *testing.T) {
	status := rebuildStatus{State: "running", StartedAt: "2026-01-01T00:00:00Z"}
	completedAt := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)

	got := reconcileInterruptedRebuild(status, true, false, completedAt)

	if !reflect.DeepEqual(got, status) {
		t.Fatalf("held lock must leave status untouched: got %+v, want %+v", got, status)
	}
}

func TestReconcileInterruptedRebuildIgnoresNonRunningState(t *testing.T) {
	status := rebuildStatus{State: "succeeded", Revision: 7}
	completedAt := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)

	got := reconcileInterruptedRebuild(status, false, true, completedAt)

	if !reflect.DeepEqual(got, status) {
		t.Fatalf("non-running status must be left untouched: got %+v, want %+v", got, status)
	}
}

func TestReconcileInterruptedRebuildMarksRequiredWhenDatabaseNotReady(t *testing.T) {
	status := rebuildStatus{State: "running", StartedAt: "2026-01-01T00:00:00Z"}
	completedAt := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)

	got := reconcileInterruptedRebuild(status, false, false, completedAt)

	if got.State != "interrupted" || !got.Required {
		t.Fatalf("unready database must report interrupted and required: %+v", got)
	}
	if got.CompletedAt != completedAt.Format(time.RFC3339Nano) {
		t.Fatalf("CompletedAt = %q, want %q", got.CompletedAt, completedAt.Format(time.RFC3339Nano))
	}
}

func TestReconcileInterruptedRebuildClearsRequiredWhenDatabaseReady(t *testing.T) {
	status := rebuildStatus{State: "running", StartedAt: "2026-01-01T00:00:00Z"}
	completedAt := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)

	got := reconcileInterruptedRebuild(status, false, true, completedAt)

	if got.State != "interrupted" || got.Required {
		t.Fatalf("ready database must report interrupted without requiring a rebuild: %+v", got)
	}
}

func TestWebhookReconcilesThroughInjectedCanonicalUpdater(t *testing.T) {
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}

	reconciler := &testReconciler{called: make(chan struct{})}
	var authBranches []string
	app := &App{services: redisx.NewStore(client, "webhook-test").OperationalServices(), reconciler: reconciler,
		webhookSecret: []byte("webhook-secret"),
		hub:           newEventHub(),
		oauth: &githubOAuth{log: func(branch string) {
			authBranches = append(authBranches, branch)
		}},
	}
	payload := `{"action":"completed"}`
	rejected := httptest.NewRecorder()
	rejectedRequest := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/github/webhook", strings.NewReader(payload),
	)
	rejectedRequest.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	app.githubWebhook(rejected, rejectedRequest)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("invalid webhook signature returned %d", rejected.Code)
	}
	mac := hmac.New(sha256.New, app.webhookSecret)
	_, _ = mac.Write([]byte(payload))
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/github/webhook", strings.NewReader(payload),
	)
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("X-GitHub-Delivery", "delivery-1")
	request.Header.Set("X-GitHub-Event", "workflow_run")
	response := httptest.NewRecorder()

	app.githubWebhook(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("webhook returned %d: %s", response.Code, response.Body.String())
	}
	<-reconciler.called
	deadline := time.Now().Add(time.Second)
	for {
		held, err := app.services.Leases.LockHeld(t.Context(), "projection")
		if err != nil {
			t.Fatal(err)
		}
		if !held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("projection lock was not released after reconciliation")
		}
		time.Sleep(time.Millisecond)
	}
	if reconciler.calls != 1 || reconciler.event.Event != "workflow_run" {
		t.Fatalf("webhook was not reconciled: %#v", reconciler)
	}
	if strings.Join(authBranches, ",") != "webhook.signature_rejected,webhook.signature_accepted" {
		t.Fatalf("unexpected webhook authentication branch logs: %v", authBranches)
	}
}

func TestEmptyRedisIsHealthyButNotReady(t *testing.T) {
	app := &App{services: redisx.NewStore(emptyRedisClient{}, "empty-test").OperationalServices(), database: integrationDatabase(t)}

	healthResponse := httptest.NewRecorder()
	app.health(
		healthResponse,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil),
	)
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("empty Redis health returned %d: %s", healthResponse.Code, healthResponse.Body.String())
	}
	var health map[string]any
	if err := json.Unmarshal(healthResponse.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	data := health["data"].(map[string]any)
	if data["available"] != false || data["rebuildRequired"] != true {
		t.Fatalf("empty Redis health did not require rebuild: %#v", health)
	}

	readinessResponse := httptest.NewRecorder()
	app.readiness(
		readinessResponse,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/readiness", nil),
	)
	if readinessResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty Redis readiness returned %d: %s", readinessResponse.Code, readinessResponse.Body.String())
	}
}

func TestHealthReportsPostgresFailureAsUnhealthy(t *testing.T) {
	database := integrationDatabase(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	app := &App{services: redisx.NewStore(emptyRedisClient{}, "empty-test").OperationalServices(), database: database}
	response := httptest.NewRecorder()
	app.health(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil))
	var health map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusServiceUnavailable || health["status"] != "unhealthy" {
		t.Fatalf("Postgres failure must report unhealthy: status=%d payload=%v", response.Code, health)
	}
}

func TestHostedRebuildRequiresExplicitAdministrator(t *testing.T) {
	var branches []string
	app := &App{
		oauth: &githubOAuth{log: func(branch string) {
			branches = append(branches, branch)
		}},
		config: Config{AdminUsers: []string{"cao-admin"}},
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/admin/rebuild", nil)
	request = request.WithContext(context.WithValue(
		request.Context(),
		oauthSessionContextKey{},
		oauthSession{Login: "dashboard-reader"},
	))
	if app.adminAuthorized(request) {
		t.Fatal("non-administrator was authorized to rebuild")
	}
	request = request.WithContext(context.WithValue(
		request.Context(),
		oauthSessionContextKey{},
		assuredSession("CAO-ADMIN"),
	))
	if !app.adminAuthorized(request) {
		t.Fatal("explicit administrator was denied")
	}
	if strings.Join(branches, ",") != "admin.denied,admin.allowed" {
		t.Fatalf("unexpected administrator branch logs: %v", branches)
	}
}

func TestSimulatorUsesProductionNativeWebhookAdmissionAndCollectionQueue(t *testing.T) {
	database, pgconfig := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	endpoint, err := url.Parse(pgconfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	parameters := endpoint.Query()
	parameters.Set("search_path", pgconfig.RuntimeParams["search_path"])
	endpoint.RawQuery = parameters.Encode()
	store, err := operationalpostgres.New(t.Context(), endpoint.String(), "simulator-native-admission", operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	site := t.TempDir()
	if err := os.WriteFile(site+"/index.html", []byte("<html><body></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	const secret = "simulator-webhook-secret"
	app, err := New(t.Context(), store, Config{
		Database:      database,
		Listen:        "127.0.0.1:0",
		SiteDirectory: site,
		AccessToken:   strings.Repeat("x", 32),
		WebhookSecret: secret,
		Collector:     &CollectorConfig{NativeAcquisition: true, AppID: 1, AdmitOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	repositories := []string{"simulator/repo-00001", "simulator/repo-00002",
		"simulator/repo-00003", "simulator/repo-00004"}
	if err := app.Collector().enrollment.AddRepositories(t.Context(), 1, repositories); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	deliveries, err := (simulator.Scenario{
		Name:                "production-webhook-path",
		Repositories:        4,
		EventsPerRepository: 2,
		Seed:                3,
		Distribution:        "uniform",
		DuplicateEvery:      2,
	}).Generate()
	if err != nil {
		t.Fatal(err)
	}
	var completed *simulator.Delivery
	for _, delivery := range deliveries {
		status, result := postNativeSimulatorDelivery(t, server, secret, delivery)
		if status != http.StatusAccepted {
			t.Fatalf("native simulator delivery %s: %d %#v", delivery.Event, status, result)
		}
		if delivery.Event == "workflow_run" {
			if result["queued"] != true || result["duplicate"] != false {
				t.Fatalf("native workflow was not durably admitted: %#v", result)
			}
			if completed == nil {
				copy := delivery
				completed = &copy
			}
		}
	}
	if completed == nil {
		t.Fatal("simulator did not generate a completed workflow")
	}
	status, duplicate := postNativeSimulatorDelivery(t, server, secret, *completed)
	if status != http.StatusAccepted || duplicate["duplicate"] != true || duplicate["queued"] != false {
		t.Fatalf("native simulator delivery was not deduplicated: %d %#v", status, duplicate)
	}
	stats, err := app.Collector().queue.Tasks.QueueStats(t.Context(), "collect:run-tasks", "")
	if err != nil || stats.Length == 0 {
		t.Fatalf("native admission lost queued run attempts: %+v %v", stats, err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/ingestion/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	response, err := server.Client().Do(request) // #nosec G704 -- the request targets the local httptest server.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("native ingestion health returned %d", response.StatusCode)
	}
	var health collect.Status
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.QueueDepth == 0 || health.Counters["webhookReceived"] != int64(len(deliveries)+1) ||
		health.Counters["webhookDuplicate"] != 1 {
		t.Fatalf("native ingestion health lost admitted simulator work: %+v", health)
	}
}

func postNativeSimulatorDelivery(t *testing.T, server *httptest.Server, secret string, delivery simulator.Delivery) (int, map[string]any) {
	t.Helper()
	payload := delivery.Payload
	if delivery.Event == "workflow_run" {
		var err error
		payload, err = nativeSimulatorRunPayload(payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/github/webhook", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("X-GitHub-Delivery", delivery.ID)
	request.Header.Set("X-GitHub-Event", delivery.Event)
	response, err := server.Client().Do(request) // #nosec G704 -- the request targets the local httptest server.
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close simulator response: %v", err)
		}
	}()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, result
}

func nativeSimulatorRunPayload(payload []byte) ([]byte, error) {
	// The simulator predates native admission and omits the workflow identity required for run tasks.
	var event map[string]json.RawMessage
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, err
	}
	var run map[string]any
	if err := json.Unmarshal(event["workflow_run"], &run); err != nil {
		return nil, err
	}
	run["path"] = ".github/workflows/test.lock.yml"
	run["run_attempt"] = 1
	content, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	event["workflow_run"] = content
	return json.Marshal(event)
}

func TestNativeCollectorRejectsRedisWithoutFencedQueue(t *testing.T) {
	address, closeServer := fakeRedis(t)
	t.Cleanup(closeServer)
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewCollector(t.Context(), redisx.NewStore(client, "native-redis-rejected"),
		integrationDatabase(t), CollectorConfig{NativeAcquisition: true, AppID: 1, AdmitOnly: true}, "")
	if err == nil || !strings.Contains(err.Error(), "fenced task receipts") {
		t.Fatalf("native admission accepted a queue without fenced receipts: %v", err)
	}
}
