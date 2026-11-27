package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	operationalmemory "github.com/githubnext/gh-cao/server/internal/operational/memory"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

const collectionSyncPath = "/api/admin/collection/sync"

func TestCollectionSyncLocalBearerAndUnconfigured(t *testing.T) {
	store, err := operationalmemory.New(operationalmemory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := &App{accessToken: testAccessToken, config: Config{Listen: "127.0.0.1:8443"}, services: store.OperationalServices()}
	for _, test := range []struct {
		name, token, body string
		want              int
	}{
		{name: "missing bearer", want: http.StatusUnauthorized},
		{name: "incorrect bearer", token: "incorrect", want: http.StatusUnauthorized},
		{name: "unconfigured", token: testAccessToken, want: http.StatusServiceUnavailable},
		{name: "body rejected", token: testAccessToken, body: `{"repository":"other/private"}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8443"+collectionSyncPath, strings.NewReader(test.body))
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestCollectionSyncHostedAdminAndCSRF(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active"})
	app := newAzureTestApp(t, github.URL)
	app.config.AdminUsers = []string{"octocat"}
	cookie, csrf := callbackSession(t, app)
	for _, test := range []struct {
		name, csrf string
		admin      bool
		want       int
	}{
		{name: "missing csrf", admin: true, want: http.StatusForbidden},
		{name: "wrong csrf", csrf: "incorrect", admin: true, want: http.StatusForbidden},
		{name: "non administrator", csrf: csrf.Value, want: http.StatusForbidden},
		{name: "administrator unconfigured", csrf: csrf.Value, admin: true, want: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			app.config.AdminUsers = []string{"other"}
			if test.admin {
				app.config.AdminUsers = []string{"octocat"}
			}
			request := azureRequest(t, http.MethodPost, collectionSyncPath)
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", test.csrf)
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
	app.config.AdminUsers = []string{"operator"}
	for _, session := range []oauthSession{{Login: "operator"}, func() oauthSession {
		session := assuredSession("operator")
		session.AuthenticatedAt = time.Now().Add(-24 * time.Hour)
		return session
	}()} {
		request := httptest.NewRequestWithContext(context.WithValue(t.Context(), oauthSessionContextKey{}, session),
			http.MethodPost, collectionSyncPath, nil)
		response := httptest.NewRecorder()
		app.collectionSync(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("unassured administrator = %d", response.Code)
		}
	}
}

func TestCollectionSyncAdmitOnlyQueuesWithoutGitHubOrInventory(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	dsn := os.Getenv("CAO_TEST_POSTGRES_URL")
	if dsn == "" {
		dsn = os.Getenv("POSTGRES_URL")
	}
	if dsn == "" {
		t.Fatal("CAO_TEST_POSTGRES_URL or POSTGRES_URL is required for durable queue coverage")
	}
	store, err := operationalpostgres.New(t.Context(), dsn, "sync-admission-"+strings.ReplaceAll(t.Name(), "/", "-"), operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.DeleteNamespace(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
		_ = store.Close()
	})
	services := store.OperationalServices()
	enrollment := collect.Enrollment{Authority: data.NativeEnrollment(), Metadata: services.Collection, Leases: services.Leases}
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	collector := &Collector{native: &nativeCollection{}, config: CollectorConfig{AdmitOnly: true},
		enrollment: enrollment, queue: collect.Queue{Tasks: services.Queue, Metadata: services.Collection, MaxLength: 1}}
	app := &App{reconciler: collector, accessToken: testAccessToken, config: Config{Listen: "127.0.0.1:8443"}, services: services}
	for range 2 {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8443"+collectionSyncPath, nil)
		request.Header.Set("Authorization", "Bearer "+testAccessToken)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		var result map[string]bool
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusAccepted || len(result) != 1 || !result["queued"] || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("admission-only response = %d %s", response.Code, response.Body.String())
		}
	}
	if stats, err := services.Queue.QueueStats(t.Context(), "collect:native-control", ""); err != nil || stats.Length != 1 {
		t.Fatalf("coalesced queue = %+v %v", stats, err)
	}
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/second"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.collectionSync(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, collectionSyncPath, nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("queue backpressure = %d: %s", response.Code, response.Body.String())
	}
	collector.queue = collect.Queue{}
	response = httptest.NewRecorder()
	app.collectionSync(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, collectionSyncPath, nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported queue = %d", response.Code)
	}
}

func TestNativeCollectionRefreshIntervalRejectsSlowerThanDaily(t *testing.T) {
	for _, duration := range []time.Duration{0, time.Hour, 24 * time.Hour} {
		config := CollectorConfig{NativeAcquisition: true, AdmitOnly: true, AppID: 42, RepositoryRefreshInterval: duration}
		if err := config.Validate(); err != nil {
			t.Fatalf("supported interval %v: %v", duration, err)
		}
	}
	config := CollectorConfig{NativeAcquisition: true, AdmitOnly: true, AppID: 42, RepositoryRefreshInterval: 25 * time.Hour}
	if err := config.Validate(); err == nil {
		t.Fatal("slower-than-daily interval accepted")
	}
}
