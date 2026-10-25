package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestCollectionHealthSourceIsAvailableOnlyToAuthorizedReaders(t *testing.T) {
	app := &App{}
	unavailable, err := app.collectionHealthSource(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Metadata["availability"] != "unavailable" || len(unavailable.Rows) != 0 {
		t.Fatalf("unauthorized health source exposed data: %+v", unavailable)
	}
	available, err := app.collectionHealthSource(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if available.Metadata["availability"] != "available" || len(available.Rows) != 1 {
		t.Fatalf("health source was not produced by the server query boundary: %+v", available)
	}
	if available.Rows[0]["configured"] != false || available.Rows[0]["health"] != "not-configured" {
		t.Fatalf("unconfigured profile was not explicit: %+v", available.Rows[0])
	}
}

func TestCollectionHealthSourceReportsBackfillProgress(t *testing.T) {
	source := collectionHealthSource(collect.Status{
		Configured: true, Health: "recovering", Backfill: "partial",
		BackfillFailures: 2, BackfillRunTasks: 17, OldestPending: "23s",
		Load:     map[string]float64{"webhook": 2.5, "collection": 1.5, "failure": 0.5},
		Counters: map[string]int64{"webhookReceived": 23, "collectionSucceeded": 11},
	})
	if got := source.Rows[0]["backfill"]; got != "partial" {
		t.Fatalf("backfill phase = %v, want partial", got)
	}
	if got := source.Rows[0]["backfill-failures"]; got != 2 {
		t.Fatalf("backfill failures = %v, want 2", got)
	}
	if got := source.Rows[0]["backfill-queued-run-tasks"]; got != 17 {
		t.Fatalf("backfill queued run tasks = %v, want 17", got)
	}
	if got := source.Rows[0]["oldest-pending-age"]; got != "23s" {
		t.Fatalf("oldest pending age = %v, want 23s", got)
	}
	if got := source.Rows[0]["webhook-load"]; got != 2.5 {
		t.Fatalf("webhook load = %v, want 2.5", got)
	}
	if source.Rows[0]["webhook-received"] != int64(23) || source.Rows[0]["collection-succeeded"] != int64(11) {
		t.Fatalf("Redis counters were not exposed to the admin dashboard: %+v", source.Rows[0])
	}
}

func TestCollectionHealthQueryRunsThroughServerQueryEngine(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{services: redisx.NewStore(client, "health-query-test").OperationalServices(), database: database}
	input := queryRequest{
		Queries:     []query.Definition{{Name: "ingestion-health", From: collectionHealthSourceName}},
		SourceNames: []string{"ingestion-health"},
	}
	for _, test := range []struct {
		name       string
		authorized bool
		want       string
	}{
		{name: "authorized", authorized: true, want: "available"},
		{name: "unauthorized", authorized: false, want: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, status, err := app.executeQuery(t.Context(), input, test.authorized)
			if err != nil || status != http.StatusOK {
				t.Fatalf("executeQuery() status=%d err=%v", status, err)
			}
			source, ok := result.Sources["ingestion-health"]
			if !ok || source.Metadata["availability"] != test.want {
				t.Fatalf("query returned unexpected health source: %+v", source)
			}
			if test.authorized && (len(source.Rows) != 1 || source.Rows[0]["health"] != "not-configured") {
				t.Fatalf("authorized health query returned wrong row: %+v", source.Rows)
			}
		})
	}
}

func TestCollectionStatusRequiresHostedAdministrator(t *testing.T) {
	app := &App{
		oauth:  &githubOAuth{},
		config: Config{AdminUsers: []string{"operator"}},
	}
	request := httptest.NewRequestWithContext(
		context.WithValue(t.Context(), oauthSessionContextKey{}, oauthSession{Login: "reader"}),
		http.MethodGet, "/api/v1/ingestion/health", nil,
	)
	response := httptest.NewRecorder()
	app.collectionStatus(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-admin status request returned %d", response.Code)
	}

	request = httptest.NewRequestWithContext(
		context.WithValue(t.Context(), oauthSessionContextKey{}, assuredSession("operator")),
		http.MethodGet, "/api/v1/ingestion/health", nil,
	)
	response = httptest.NewRecorder()
	app.collectionStatus(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("administrator status request returned %d: %s", response.Code, response.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["configured"] != false || status["health"] != "not-configured" {
		t.Fatalf("unexpected unconfigured collection response: %#v", status)
	}
}

func TestProfilesAreMutuallyExclusive(t *testing.T) {
	collector := &CollectorConfig{
		AppID:         1,
		PrivateKeyPEM: []byte("key"),
		LakeDirectory: t.TempDir(),
		CatalogRoot:   t.TempDir(),
	}
	t.Run("published snapshots and collection cannot both be configured", func(t *testing.T) {
		err := validateProfileExclusivity(Config{
			Collector:       collector,
			SourceDirectory: t.TempDir(),
			WebhookSecret:   strings.Repeat("s", 32),
		})
		if err == nil || !strings.Contains(err.Error(), "alternatives") {
			t.Fatalf("expected a dual-profile rejection, got %v", err)
		}
	})
	t.Run("collection replaces a configured reconciler", func(t *testing.T) {
		err := validateProfileExclusivity(Config{
			Collector:     collector,
			Reconciler:    DirectoryReconciler{},
			WebhookSecret: strings.Repeat("s", 32),
		})
		if err == nil {
			t.Fatal("expected a conflicting reconciler to be rejected")
		}
	})
	t.Run("collection requires a webhook secret", func(t *testing.T) {
		err := validateProfileExclusivity(Config{Collector: collector})
		if err == nil || !strings.Contains(err.Error(), "webhook secret") {
			t.Fatalf("expected a missing webhook secret to be rejected, got %v", err)
		}
	})
	t.Run("the default profile is unaffected", func(t *testing.T) {
		if err := validateProfileExclusivity(Config{SourceDirectory: t.TempDir()}); err != nil {
			t.Fatalf("the published-snapshot profile must validate unchanged: %v", err)
		}
	})
}

func TestClassifyCollectorProfile(t *testing.T) {
	cases := []struct {
		name    string
		rawApp  string
		profile collectorProfile
	}{
		{"blank selects the Actions snapshot profile", "", collectorProfileActionsSnapshot},
		{"whitespace-only selects the Actions snapshot profile", "   ", collectorProfileActionsSnapshot},
		{"any App identifier selects the collection profile", "12345", collectorProfileCollection},
		{"a non-numeric value still selects the collection profile", "not-a-number", collectorProfileCollection},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyCollectorProfile(tc.rawApp); got != tc.profile {
				t.Fatalf("classifyCollectorProfile(%q) = %q, want %q", tc.rawApp, got, tc.profile)
			}
		})
	}
}

func TestCollectorConfigFromEnvSelectsTheDefaultProfileWhenUnset(t *testing.T) {
	t.Setenv("CAO_COLLECT_APP_ID", "")
	config, err := CollectorConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config != nil {
		t.Fatal("an unset App identifier must select the published-snapshot profile")
	}
}

func TestCollectorConfigFromEnvFailsClosed(t *testing.T) {
	t.Run("an incomplete configuration is rejected", func(t *testing.T) {
		t.Setenv("CAO_COLLECT_APP_ID", "12345")
		t.Setenv("CAO_COLLECT_PRIVATE_KEY", "")
		t.Setenv("CAO_COLLECT_PRIVATE_KEY_BASE64", "")
		t.Setenv("CAO_COLLECT_PRIVATE_KEY_FILE", "")
		if _, err := CollectorConfigFromEnv(); err == nil {
			t.Fatal("expected a missing private key to be rejected")
		}
	})
	t.Run("a non-numeric App identifier is rejected", func(t *testing.T) {
		t.Setenv("CAO_COLLECT_APP_ID", "not-a-number")
		if _, err := CollectorConfigFromEnv(); err == nil {
			t.Fatal("expected an invalid App identifier to be rejected")
		}
	})
}

func TestCollectorConfigFromEnvReadsTheCollectionProfile(t *testing.T) {
	lake := t.TempDir()
	catalog := t.TempDir()
	t.Setenv("CAO_COLLECT_APP_ID", "12345")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----")
	t.Setenv("CAO_COLLECT_LAKE_DIRECTORY", lake)
	t.Setenv("CAO_COLLECT_CATALOG_ROOT", catalog)
	t.Setenv("CAO_COLLECT_CONTROL_REPOSITORY", "octo/control")
	t.Setenv("CAO_POLICY_PATH", "/app/.github/workflows/cao.hosted.json")
	t.Setenv("CAO_COLLECT_STATIC_INVENTORY", "/app/catalog/control-plane-inventory.json")
	t.Setenv("CAO_COLLECT_WORKERS", "4")
	t.Setenv("CAO_COLLECT_PROJECTION_INTERVAL", "90s")
	t.Setenv("CAO_COLLECT_RECOVER_DELIVERIES", "true")
	t.Setenv("CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL", "30m")
	config, err := CollectorConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config == nil {
		t.Fatal("expected the collection profile to be selected")
	}
	if config.AppID != 12345 || config.Workers != 4 {
		t.Fatalf("unexpected configuration: %+v", config)
	}
	if config.MinProjectionInterval.Seconds() != 90 {
		t.Fatalf("projection interval = %s, want 90s", config.MinProjectionInterval)
	}
	if !config.RecoverDeliveries {
		t.Fatal("expected delivery recovery to be enabled")
	}
	if config.RepositoryRefreshInterval.Minutes() != 30 {
		t.Fatalf("repository refresh interval = %s, want 30m", config.RepositoryRefreshInterval)
	}
	if config.PolicyPath != "/app/.github/workflows/cao.hosted.json" ||
		config.StaticInventoryPath != "/app/catalog/control-plane-inventory.json" {
		t.Fatalf("unexpected inventory configuration: %+v", config)
	}
}

func TestCollectorRepositoryRefreshIntervalRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []string{"-1m", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CAO_COLLECT_APP_ID", "1")
			t.Setenv("CAO_COLLECT_ADMIT_ONLY", "true")
			t.Setenv("CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL", value)
			if _, err := CollectorConfigFromEnv(); err == nil {
				t.Fatal("invalid repository refresh interval was accepted")
			}
		})
	}
}
func TestCollectorPrivateKeyPrefersAFileReference(t *testing.T) {
	directory := t.TempDir()
	path := directory + "/key.pem"
	if err := os.WriteFile(path, []byte("-----BEGIN PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_FILE", path)
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_BASE64", "aWdub3JlZA==")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY", "inline-should-not-be-used")
	key, err := collectorPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if string(key) != "-----BEGIN PRIVATE KEY-----" {
		t.Fatalf("unexpected key material: %q", key)
	}
}

func TestCollectorPrivateKeyDecodesBase64BeforeInline(t *testing.T) {
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_FILE", "")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_BASE64", "LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tCg==")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY", "inline-should-not-be-used")
	key, err := collectorPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if string(key) != "-----BEGIN PRIVATE KEY-----\n" {
		t.Fatalf("unexpected key material: %q", key)
	}
}

func TestCollectorPrivateKeyRejectsInvalidBase64(t *testing.T) {
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_FILE", "")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_BASE64", "not-base64")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY", "inline-must-not-hide-invalid-base64")
	if _, err := collectorPrivateKey(); err == nil {
		t.Fatal("invalid base64 private key was accepted")
	} else if strings.Contains(err.Error(), "not-base64") {
		t.Fatal("invalid base64 private key leaked into the error")
	}
}

func TestCollectorPrivateKeyDecodedContentStillRequiresAValidPEM(t *testing.T) {
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_FILE", "")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY_BASE64", "bm90LWEtcGVt")
	t.Setenv("CAO_COLLECT_PRIVATE_KEY", "")
	key, err := collectorPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := githubapp.New(githubapp.Config{AppID: 1, PrivateKeyPEM: key}); err == nil {
		t.Fatal("decoded non-PEM private key was accepted by the GitHub App client")
	}
}

// An admission-only front end verifies deliveries and enqueues work. Giving it
// the App private key would place a credential it cannot use in the
// internet-facing process.
func TestAdmitOnlyRefusesCollectionCredentials(t *testing.T) {
	config := CollectorConfig{AppID: 42, AdmitOnly: true}
	if err := config.Validate(); err != nil {
		t.Fatalf("admission-only configuration was rejected: %v", err)
	}
	withKey := CollectorConfig{AppID: 42, AdmitOnly: true, PrivateKeyPEM: []byte("key")}
	if err := withKey.Validate(); err == nil {
		t.Fatal("admission-only accepted the App private key")
	}
	withWorkers := CollectorConfig{AppID: 42, AdmitOnly: true, Workers: 1}
	if err := withWorkers.Validate(); err == nil {
		t.Fatal("admission-only accepted collection workers")
	}
	collecting := CollectorConfig{AppID: 42}
	if err := collecting.Validate(); err == nil {
		t.Fatal("the collection profile was accepted without a private key")
	}
}
