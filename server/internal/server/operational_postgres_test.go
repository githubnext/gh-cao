package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
)

func TestPostgresHostedOAuthSurvivesRestartWithoutOperational(t *testing.T) {
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL is required for PostgreSQL hosted integration")
	}

	namespace := "hosted-postgres-" + time.Now().Format("20060102150405.000000000")
	store, err := postgres.New(t.Context(), dsn, namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanup, err := postgres.Open(ctx, dsn, namespace, postgres.Config{})
		if err != nil {
			t.Error(err)
			return
		}
		if err := cleanup.DeleteNamespace(ctx); err != nil {
			t.Error(err)
		}
		_ = cleanup.Close()
		_ = store.Close()
	})
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	config := Config{
		Database: constructorDatabase(), SiteDirectory: hostedTestSite(t),
		HostProfile: hostedHostProfile(), Listen: "0.0.0.0:8443",
		CertFile: "certificate.pem", KeyFile: "key.pem",
		Proxy:       ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true},
		GitHubOAuth: validOAuthConfig(github.URL),
	}
	first, err := New(t.Context(), focusedTestStore(store), config)
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := callbackSession(t, first)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := postgres.Open(t.Context(), dsn, namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	second, err := New(t.Context(), focusedTestStore(reopened), config)
	if err != nil {
		t.Fatal(err)
	}
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(session)
	response := httptest.NewRecorder()
	second.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("restart lost durable session: %d: %s", response.Code, response.Body.String())
	}
	request = azureRequest(t, http.MethodPost, "/auth/logout")
	request.AddCookie(session)
	request.AddCookie(csrf)
	request.Header.Set("X-CSRF-Token", csrf.Value)
	response = httptest.NewRecorder()
	second.Handler().ServeHTTP(response, request)
	if response.Code < 200 || response.Code >= 400 {
		t.Fatalf("logout failed: %d: %s", response.Code, response.Body.String())
	}
	request = azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(session)
	response = httptest.NewRecorder()
	second.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("logout resurrected session: %d", response.Code)
	}
}
