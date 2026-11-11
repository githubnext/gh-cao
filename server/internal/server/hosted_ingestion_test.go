package server

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestHostedIngestionSelection(t *testing.T) {
	for _, test := range []struct {
		name, source, external string
		collector              bool
		wantError              bool
	}{
		{name: "missing-source", wantError: true},
		{name: "snapshot", source: "/snapshot"},
		{name: "collector", collector: true},
		{name: "explicit-external", external: "true"},
		{name: "disabled-external", external: "false", wantError: true},
		{name: "disabled-with-snapshot", external: "false", source: "/snapshot"},
		{name: "external-with-snapshot", external: "true", source: "/snapshot", wantError: true},
		{name: "external-with-collector", external: "true", collector: true, wantError: true},
		{name: "invalid-selection", external: "yes", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateHostedIngestion(test.source, test.collector, test.external)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected ingestion selection: %v", err)
			}
		})
	}
}

func TestHostedExternalIngestionPreservesPublishedDatabase(t *testing.T) {
	database, connection := testutil.PostgresWithConfig(t, t.Context(), "POSTGRES_URL")
	seedDatabase(t, database, nil)
	before, err := database.State(t.Context())
	if err != nil || !before.Ready {
		t.Fatalf("published evidence unavailable: %+v: %v", before, err)
	}
	endpoint, err := url.Parse(connection.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	parameters := endpoint.Query()
	parameters.Set("search_path", connection.RuntimeParams["search_path"])
	endpoint.RawQuery = parameters.Encode()
	dsn := endpoint.String()
	operationalStore, err := postgres.New(t.Context(), dsn, "external-ingestion-operational", postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := operationalStore.Close(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	policy := filepath.Join(directory, "cao.json")
	if err := os.WriteFile(policy, []byte(`{"control-plane":{"web":{"host":{
		"target":{"module":"container","replicas":1},
		"operational-store":{"backend":"postgres"}
	}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	queries := filepath.Join(directory, "queries.json")
	if err := os.WriteFile(queries, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"CAO_POLICY_PATH":           policy,
		"CAO_POSTGRES_URL":          dsn,
		"REDIS_NAMESPACE":           "default",
		"CAO_OPERATIONAL_NAMESPACE": "external-ingestion-operational",
		"CAO_SOURCE_DIRECTORY":      "",
		"CAO_EXTERNAL_INGESTION":    "true",
		"CAO_COLLECT_APP_ID":        "",
		"CAO_ALLOWED_HOSTS":         "dashboard.example",
		"CAO_TRUSTED_PROXY_CIDRS":   "10.45.0.0/23",
		"CAO_GITHUB_CLIENT_ID":      "fixture",
		"CAO_GITHUB_CLIENT_SECRET":  "fixture",
		"CAO_GITHUB_REDIRECT_URL":   "https://dashboard.example/auth/callback",
		"CAO_GITHUB_ALLOWED_ORGS":   "fixture",
		"CAO_GITHUB_ADMIN_USERS":    "fixture-admin",
		"CAO_SESSION_SECRET":        strings.Repeat("s", 32),
		"CAO_GITHUB_WEBHOOK_SECRET": strings.Repeat("w", 32),
	} {
		t.Setenv(name, value)
	}
	app, err := NewHostedAppFromEnv(t.Context(), "0.0.0.0:8080", "", "", hostedTestSite(t), queries, queries,
		false, "", "", log.New(io.Discard, "", 0), "postgres")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := app.oauth.queueRevocation(t.Context(), oauthSession{
		ID: "external-ingestion-expired", AccessToken: "expired-fixture",
		ProviderAccessExpires: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	cleanupDeadline := time.Now().Add(5 * time.Second)
	for {
		pending, err := app.oauth.revocations.PendingRevocation(t.Context(), app.oauth.config.RevocationKeyPrefix)
		if err == nil && pending.ID == "" {
			break
		}
		if err != nil || time.Now().After(cleanupDeadline) {
			t.Fatalf("OAuth cleanup did not run without local acquisition: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	after, err := database.State(t.Context())
	if err != nil || after.Revision != before.Revision || !after.Ready {
		t.Fatalf("web startup replaced published evidence: before=%+v after=%+v: %v", before, after, err)
	}
	for _, test := range []struct {
		path, peer string
		status     int
	}{
		{"/api/readiness", "10.45.0.10:12345", http.StatusOK},
		{"/api/auth/session", "10.45.0.10:12345", http.StatusUnauthorized},
		{"/api/readiness", "203.0.113.1:12345", http.StatusMisdirectedRequest},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://dashboard.example"+test.path, nil)
		request.RemoteAddr = test.peer
		request.Header.Set("X-Forwarded-Proto", "https")
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s via %s: got %d, want %d", test.path, test.peer, response.Code, test.status)
		}
	}
	events := app.hub.Subscribe(t.Context(), app, false)
	if events == nil {
		t.Fatal("event subscription unavailable")
	}
	defer app.hub.Unsubscribe(events)
	seedDatabase(t, database, nil)
	published, err := database.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case observation := <-events:
			if observation.revision == published.Revision {
				return
			}
		case <-timeout.C:
			t.Fatal("active subscription missed externally published evidence")
		}
	}
}
