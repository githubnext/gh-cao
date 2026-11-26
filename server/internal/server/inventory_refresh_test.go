package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
)

type inventoryEnrollmentFixture struct {
	collectnative.EnrollmentAuthority
	scopes []collectnative.EnrollmentScope
	err    error
}

func (f inventoryEnrollmentFixture) Installations(context.Context) ([]int64, error) {
	return []int64{7}, f.err
}

func (f inventoryEnrollmentFixture) ScanRepositories(context.Context, int64, string, int) ([]string, string, error) {
	names := make([]string, 0, len(f.scopes))
	for _, scope := range f.scopes {
		names = append(names, scope.Repository)
	}
	return names, "", f.err
}

func (f inventoryEnrollmentFixture) CurrentScope(_ context.Context, name string) (collectnative.EnrollmentScope, error) {
	for _, scope := range f.scopes {
		if scope.Repository == name {
			return scope, nil
		}
	}
	return collectnative.EnrollmentScope{}, collectnative.ErrIdentity
}

func TestInventoryRefreshAdmissionOnlyUsesCommittedEnrollment(t *testing.T) {
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("real PostgreSQL is required for durable inventory admission")
	}
	store, err := operationalpostgres.New(t.Context(), dsn, fmt.Sprintf("manual-inventory-%d", time.Now().UnixNano()), operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := store.DeleteNamespace(ctx); err != nil {
			t.Error(err)
		}
		_ = store.Close()
	})
	services := store.OperationalServices()
	collector := &Collector{
		config: CollectorConfig{AdmitOnly: true},
		enrollment: collect.Enrollment{Authority: inventoryEnrollmentFixture{scopes: []collectnative.EnrollmentScope{
			{Repository: "octo/api", InstallationID: 7, Epoch: 3},
			{Repository: "octo/web", InstallationID: 7, Epoch: 4},
		}}},
		queue: collect.Queue{Tasks: services.Queue, Admission: services.Admission, MaxLength: 1},
	}
	// Nil App client and native provider are deliberate: admission cannot call GitHub.
	app := &App{reconciler: collector}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/ingestion/refresh", nil)
	response := httptest.NewRecorder()
	app.refreshInventory(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "1 already queued") {
		t.Fatalf("partial admission = %d %s", response.Code, response.Body)
	}
	collector.queue.MaxLength = 10
	response = httptest.NewRecorder()
	app.refreshInventory(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"queued":1`) {
		t.Fatalf("retry admission = %d %s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	app.refreshInventory(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"queued":0`) {
		t.Fatalf("duplicate admission = %d %s", response.Code, response.Body)
	}
	collector.config.InventoryLimit = 1
	if _, err := collector.requestInventoryRefresh(t.Context()); !errors.Is(err, collectnative.ErrCapacity) {
		t.Fatalf("inventory limit ignored: %v", err)
	}
	collector.enrollment.Authority = inventoryEnrollmentFixture{err: errors.New("scope unavailable")}
	if _, err := collector.requestInventoryRefresh(t.Context()); err == nil {
		t.Fatal("unavailable scope silently accepted")
	}
}

func TestInventoryRefreshRequiresLocalBearerAndFailsClosed(t *testing.T) {
	app := &App{accessToken: "local-inventory-test"}
	handler := app.requireAccess(http.HandlerFunc(app.refreshInventory))
	for _, token := range []string{"", "wrong", "local-inventory-test"} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/api/v1/ingestion/refresh", nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		expected := http.StatusUnauthorized
		if token == "local-inventory-test" {
			expected = http.StatusNotFound
		}
		if response.Code != expected {
			t.Fatalf("capability %q = %d, want %d", token, response.Code, expected)
		}
	}
}

func TestInventoryRefreshRequiresHostedCSRFAdminAndStepUp(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active"})
	app := newAzureTestApp(t, github.URL)
	app.config.AdminUsers = []string{"octocat"}
	session, csrf := callbackSession(t, app)
	app.reconciler = &Collector{}
	send := func(cookie *http.Cookie, csrfToken string) *httptest.ResponseRecorder {
		request := azureRequest(t, http.MethodPost, "/api/v1/ingestion/refresh")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		request.Header.Set("X-CSRF-Token", csrfToken)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		return response
	}
	if response := send(nil, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", response.Code)
	}
	if response := send(session, ""); response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d", response.Code)
	}
	if response := send(session, csrf.Value); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("authorized but missing scope = %d %s", response.Code, response.Body)
	}
	app.config.AdminUsers = nil
	if response := send(session, csrf.Value); response.Code != http.StatusForbidden {
		t.Fatalf("non-admin = %d %s", response.Code, response.Body)
	}
	app.config.AdminUsers = []string{"octocat"}
	app.oauth.config.AdminReauthenticationMaxAge = time.Nanosecond
	if response := send(session, csrf.Value); response.Code != http.StatusUnauthorized ||
		!strings.Contains(response.Body.String(), "admin_step_up_required") {
		t.Fatalf("expired assurance = %d %s", response.Code, response.Body)
	}
}
