package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/redisx"
	"github.com/githubnext/gh-cao/server/internal/server"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

func TestServeWithExistingPostgresOperationalTables(t *testing.T) {
	endpoint := os.Getenv("POSTGRES_URL")
	if endpoint == "" {
		t.Skip("POSTGRES_URL is not set")
	}
	if _, err := benchmarkPostgresConfig(endpoint); err != nil {
		t.Skip("POSTGRES_URL is not a local test instance")
	}
	namespace, err := newBenchmarkNamespace()
	if err != nil {
		t.Fatal(err)
	}
	schema := "cao_serve_" + namespace
	admin, err := pgx.Connect(t.Context(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("remove isolated Postgres schema: %v", err)
		}
	}()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	t.Setenv("CAO_POSTGRES_URL", parsed.String())
	t.Setenv("OTEL_SDK_DISABLED", "true")
	operational, err := postgres.New(t.Context(), parsed.String(), namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := operational.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_OPERATIONAL_NAMESPACE", namespace)
	canonical, err := postgresx.NewWithNamespace(t.Context(), parsed.String(), namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := newServeCommand()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--postgres-url=", "--dashboard-queries", filepath.Join(t.TempDir(), "missing-dashboard-queries.json")})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing-dashboard-queries.json") {
		t.Fatalf("serve must reach dashboard query loading after both stores initialize: %v", err)
	}
}

func TestServeHostedLoadsMaterializedDashboardQueriesByDefault(t *testing.T) {
	cmd := newServeHostedCommand()
	path, err := cmd.Flags().GetString("dashboard-queries")
	if err != nil {
		t.Fatal(err)
	}
	if path != "../dashboard/site/src/agent/queries.generated.json" {
		t.Fatalf("unexpected dashboard queries default: %q", path)
	}
	definitions, err := server.ParseDashboardQueries("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	catalogPath, err := cmd.Flags().GetString("agent-catalog")
	if err != nil {
		t.Fatal(err)
	}
	if catalogPath != "../dashboard/site/src/agent/catalog.generated.json" {
		t.Fatalf("agent catalog default = %q", catalogPath)
	}
	content, err := os.ReadFile("../../../dashboard/site/src/agent/catalog.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Queries []struct {
			ID string `json:"id"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(content, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Queries) == 0 {
		t.Fatal("default agent catalog has no queries")
	}
	loaded := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		loaded[definition.Name] = true
	}
	for _, entry := range catalog.Queries {
		if !loaded[entry.ID] {
			t.Errorf("default dashboard query definitions omit catalog query %q", entry.ID)
		}
	}
}

func TestServeHostedRejectsUnsupportedOperationalBackend(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	cmd := newServeHostedCommand()
	cmd.SetArgs([]string{"--operational-store", "sqlite"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "must be redis, memory, or postgres") {
		t.Fatalf("error = %v, want unsupported operational backend", err)
	}
}

func TestServeHostedCLIRequiresExperimentalFlag(t *testing.T) {
	cmd := newServeHostedCommand()
	cmd.SetArgs([]string{"--hosted-cli-actions"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--hosted-cli-actions requires --experimental") {
		t.Fatalf("error = %v, want experimental feature gate", err)
	}
	if cmd.Flags().Lookup("experimental") == nil {
		t.Fatal("serve-hosted does not expose --experimental")
	}
}

func TestOperationalBackendSelectionUsesComposedReviewedPolicy(t *testing.T) {
	directory := t.TempDir()
	base := `{"version":1,"control-plane":{"web":{"host":{"target":{"module":"container","replicas":1},"operational-store":{"backend":"memory","single-process":true,"allow-volatile":true}}}}}`
	if err := os.WriteFile(filepath.Join(directory, "cao.json"), []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := `{"extends":"cao.json","control-plane":{"web":{"host":{}}}}`
	path := filepath.Join(directory, "cao.memory.json")
	if err := os.WriteFile(path, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	t.Setenv("OTEL_SDK_DISABLED", "true")
	settings, err := server.OperationalEnvironmentFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Backend != "memory" {
		t.Fatalf("selected = %q, want memory", settings.Backend)
	}
	cmd := newServeHostedCommand()
	cmd.SetArgs([]string{"--operational-store", "redis"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "matching reviewed") {
		t.Fatalf("error = %v, want reviewed-backend mismatch before opening services", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "cao.json"), []byte(strings.Replace(base, `"allow-volatile":true`, `"allow-volatile":false`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = newServeHostedCommand()
	cmd.SetArgs([]string{"--operational-store", "memory"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "acknowledgements") {
		t.Fatalf("error = %v, want rejected volatile acknowledgement", err)
	}
}

func TestResolveRedisEndpoint(t *testing.T) {
	const defaultValue = "redis://127.0.0.1:6379/0"

	tests := []struct {
		name       string
		flagValue  string
		envValue   string
		wantURL    string
		wantSource redisEndpointSource
	}{
		{
			name:       "flag takes priority over env and default",
			flagValue:  "redis://flag:6379/1",
			envValue:   "redis://env:6379/2",
			wantURL:    "redis://flag:6379/1",
			wantSource: redisEndpointSourceFlag,
		},
		{
			name:       "env used when flag is empty",
			flagValue:  "",
			envValue:   "redis://env:6379/2",
			wantURL:    "redis://env:6379/2",
			wantSource: redisEndpointSourceEnv,
		},
		{
			name:       "env used when flag is only whitespace",
			flagValue:  "   ",
			envValue:   "redis://env:6379/2",
			wantURL:    "redis://env:6379/2",
			wantSource: redisEndpointSourceEnv,
		},
		{
			name:       "default used when flag and env are empty",
			flagValue:  "",
			envValue:   "",
			wantURL:    defaultValue,
			wantSource: redisEndpointSourceDefault,
		},
		{
			name:       "default used when env is only whitespace",
			flagValue:  "",
			envValue:   "  ",
			wantURL:    defaultValue,
			wantSource: redisEndpointSourceDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotSource := resolveRedisEndpoint(tt.flagValue, tt.envValue, defaultValue)
			if gotURL != tt.wantURL {
				t.Errorf("resolveRedisEndpoint() url = %q, want %q", gotURL, tt.wantURL)
			}
			if gotSource != tt.wantSource {
				t.Errorf("resolveRedisEndpoint() source = %q, want %q", gotSource, tt.wantSource)
			}
		})
	}
}

func TestResolvePostgresEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		flagValue  string
		envValue   string
		wantURL    string
		wantSource postgresEndpointSource
		wantErr    bool
	}{
		{
			name:       "flag takes priority over env",
			flagValue:  "postgres://flag/db",
			envValue:   "postgres://env/db",
			wantURL:    "postgres://flag/db",
			wantSource: postgresEndpointSourceFlag,
		},
		{
			name:       "env used when flag is empty",
			flagValue:  "",
			envValue:   "postgres://env/db",
			wantURL:    "postgres://env/db",
			wantSource: postgresEndpointSourceEnv,
		},
		{
			name:       "env used when flag is only whitespace",
			flagValue:  "   ",
			envValue:   "postgres://env/db",
			wantURL:    "postgres://env/db",
			wantSource: postgresEndpointSourceEnv,
		},
		{
			name:      "error when flag and env are empty",
			flagValue: "",
			envValue:  "",
			wantErr:   true,
		},
		{
			name:      "error when flag and env are only whitespace",
			flagValue: "  ",
			envValue:  "  ",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotSource, err := resolvePostgresEndpoint(tt.flagValue, tt.envValue)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error for a missing Postgres endpoint")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotURL != tt.wantURL {
				t.Errorf("resolvePostgresEndpoint() url = %q, want %q", gotURL, tt.wantURL)
			}
			if gotSource != tt.wantSource {
				t.Errorf("resolvePostgresEndpoint() source = %q, want %q", gotSource, tt.wantSource)
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	const buildVersion = "1.2.3"

	tests := []struct {
		name        string
		envOverride string
		wantVersion string
		wantSource  versionSource
	}{
		{
			name:        "env override takes priority over the build version",
			envOverride: "override-9.9.9",
			wantVersion: "override-9.9.9",
			wantSource:  versionSourceEnvOverride,
		},
		{
			name:        "build version used when the env override is empty",
			envOverride: "",
			wantVersion: buildVersion,
			wantSource:  versionSourceBuildLdflag,
		},
		{
			name:        "build version used when the env override is only whitespace",
			envOverride: "   ",
			wantVersion: buildVersion,
			wantSource:  versionSourceBuildLdflag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotSource := resolveVersion(buildVersion, tt.envOverride)
			if gotVersion != tt.wantVersion {
				t.Errorf("resolveVersion() version = %q, want %q", gotVersion, tt.wantVersion)
			}
			if gotSource != tt.wantSource {
				t.Errorf("resolveVersion() source = %q, want %q", gotSource, tt.wantSource)
			}
		})
	}
}

func TestResolveHostedTLSMode(t *testing.T) {
	tests := []struct {
		name     string
		certFile string
		want     hostedTLSMode
	}{
		{
			name:     "non-empty cert flag is configured",
			certFile: "/etc/cao/tls.pem",
			want:     hostedTLSModeConfigured,
		},
		{
			name:     "empty cert flag is disabled",
			certFile: "",
			want:     hostedTLSModeDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveHostedTLSMode(tt.certFile); got != tt.want {
				t.Errorf("resolveHostedTLSMode(%q) = %q, want %q", tt.certFile, got, tt.want)
			}
		})
	}
}

func TestResolveNamespaceDefault(t *testing.T) {
	const checkoutDefault = "checkout-abc123"

	tests := []struct {
		name          string
		envValue      string
		wantNamespace string
		wantSource    namespaceDefaultSource
	}{
		{
			name:          "env override takes priority over checkout default",
			envValue:      "custom-namespace",
			wantNamespace: "custom-namespace",
			wantSource:    namespaceDefaultSourceEnv,
		},
		{
			name:          "checkout default used when env is empty",
			envValue:      "",
			wantNamespace: checkoutDefault,
			wantSource:    namespaceDefaultSourceCheckout,
		},
		{
			name:          "checkout default used when env is only whitespace",
			envValue:      "   ",
			wantNamespace: checkoutDefault,
			wantSource:    namespaceDefaultSourceCheckout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNamespace, gotSource := resolveNamespaceDefault(tt.envValue, checkoutDefault)
			if gotNamespace != tt.wantNamespace {
				t.Errorf("resolveNamespaceDefault() namespace = %q, want %q", gotNamespace, tt.wantNamespace)
			}
			if gotSource != tt.wantSource {
				t.Errorf("resolveNamespaceDefault() source = %q, want %q", gotSource, tt.wantSource)
			}
		})
	}
}

func TestCheckoutNamespaceDefaultMatchesRedisxDefaultNamespace(t *testing.T) {
	want, err := redisx.DefaultNamespace(".")
	if err != nil {
		t.Fatalf("redisx.DefaultNamespace() error = %v, want nil", err)
	}
	got, err := checkoutNamespaceDefault()
	if err != nil {
		t.Fatalf("checkoutNamespaceDefault() error = %v, want nil", err)
	}
	if got != want {
		t.Errorf("checkoutNamespaceDefault() = %q, want %q", got, want)
	}
	if _, normalizeErr := redisx.NormalizeNamespace(got); normalizeErr != nil {
		t.Errorf("checkoutNamespaceDefault() = %q is not a valid namespace: %v", got, normalizeErr)
	}
}

func TestRegisterRedisNamespaceFlagUsesCheckoutDefault(t *testing.T) {
	want, err := checkoutNamespaceDefault()
	if err != nil {
		t.Fatalf("checkoutNamespaceDefault() error = %v, want nil", err)
	}

	cmd := &cobra.Command{Use: "test"}
	namespace, namespaceErr := registerRedisNamespaceFlag(cmd, "usage")
	if namespaceErr != nil {
		t.Fatalf("registerRedisNamespaceFlag() error = %v, want nil", namespaceErr)
	}
	if *namespace != want {
		t.Errorf("registerRedisNamespaceFlag() default = %q, want %q", *namespace, want)
	}
	if got, err := cmd.Flags().GetString("redis-namespace"); err != nil || got != want {
		t.Errorf("cmd.Flags().GetString(redis-namespace) = (%q, %v), want (%q, nil)", got, err, want)
	}
}

func TestRegisterRedisNamespaceFlagWithEnvOverridePrefersEnv(t *testing.T) {
	t.Setenv("CAO_REDIS_NAMESPACE", "custom-namespace")

	cmd := &cobra.Command{Use: "test"}
	namespace, source, namespaceErr := registerRedisNamespaceFlagWithEnvOverride(cmd, "usage")
	if namespaceErr != nil {
		t.Fatalf("registerRedisNamespaceFlagWithEnvOverride() error = %v, want nil", namespaceErr)
	}
	if *namespace != "custom-namespace" {
		t.Errorf("registerRedisNamespaceFlagWithEnvOverride() default = %q, want %q", *namespace, "custom-namespace")
	}
	if source != namespaceDefaultSourceEnv {
		t.Errorf("registerRedisNamespaceFlagWithEnvOverride() source = %q, want %q", source, namespaceDefaultSourceEnv)
	}
}

func TestRegisterRedisNamespaceFlagWithEnvOverrideFallsBackToCheckout(t *testing.T) {
	t.Setenv("CAO_REDIS_NAMESPACE", "")
	want, err := checkoutNamespaceDefault()
	if err != nil {
		t.Fatalf("checkoutNamespaceDefault() error = %v, want nil", err)
	}

	cmd := &cobra.Command{Use: "test"}
	namespace, source, namespaceErr := registerRedisNamespaceFlagWithEnvOverride(cmd, "usage")
	if namespaceErr != nil {
		t.Fatalf("registerRedisNamespaceFlagWithEnvOverride() error = %v, want nil", namespaceErr)
	}
	if *namespace != want {
		t.Errorf("registerRedisNamespaceFlagWithEnvOverride() default = %q, want %q", *namespace, want)
	}
	if source != namespaceDefaultSourceCheckout {
		t.Errorf("registerRedisNamespaceFlagWithEnvOverride() source = %q, want %q", source, namespaceDefaultSourceCheckout)
	}
}

func TestResolveConsumerName(t *testing.T) {
	stubHostname := func() (string, error) { return "stub-host", nil }

	t.Run("flag takes priority over hostname", func(t *testing.T) {
		gotName, gotSource, err := resolveConsumerName("worker-1", stubHostname)
		if err != nil {
			t.Fatalf("resolveConsumerName() error = %v, want nil", err)
		}
		if gotName != "worker-1" {
			t.Errorf("resolveConsumerName() name = %q, want %q", gotName, "worker-1")
		}
		if gotSource != consumerNameSourceFlag {
			t.Errorf("resolveConsumerName() source = %q, want %q", gotSource, consumerNameSourceFlag)
		}
	})

	t.Run("hostname used when flag is empty", func(t *testing.T) {
		gotName, gotSource, err := resolveConsumerName("", stubHostname)
		if err != nil {
			t.Fatalf("resolveConsumerName() error = %v, want nil", err)
		}
		if gotName != "stub-host" {
			t.Errorf("resolveConsumerName() name = %q, want %q", gotName, "stub-host")
		}
		if gotSource != consumerNameSourceHostname {
			t.Errorf("resolveConsumerName() source = %q, want %q", gotSource, consumerNameSourceHostname)
		}
	})

	t.Run("hostname used when flag is only whitespace", func(t *testing.T) {
		gotName, gotSource, err := resolveConsumerName("   ", stubHostname)
		if err != nil {
			t.Fatalf("resolveConsumerName() error = %v, want nil", err)
		}
		if gotName != "stub-host" {
			t.Errorf("resolveConsumerName() name = %q, want %q", gotName, "stub-host")
		}
		if gotSource != consumerNameSourceHostname {
			t.Errorf("resolveConsumerName() source = %q, want %q", gotSource, consumerNameSourceHostname)
		}
	})

	t.Run("error propagated when flag empty and hostname fails", func(t *testing.T) {
		failingHostname := func() (string, error) { return "", errors.New("no hostname") }
		_, _, err := resolveConsumerName("", failingHostname)
		if err == nil {
			t.Fatal("resolveConsumerName() error = nil, want non-nil")
		}
	})
}

func TestResolveIngestSource(t *testing.T) {
	tests := []struct {
		name           string
		flagValue      string
		positionalArgs []string
		wantSource     string
		wantOrigin     ingestSourceOrigin
		wantErr        bool
	}{
		{
			name:           "flag takes priority over positional arg",
			flagValue:      "/flag/dir",
			positionalArgs: []string{"/positional/dir"},
			wantSource:     "/flag/dir",
			wantOrigin:     ingestSourceOriginFlag,
		},
		{
			name:           "positional arg used when flag is empty",
			flagValue:      "",
			positionalArgs: []string{"/positional/dir"},
			wantSource:     "/positional/dir",
			wantOrigin:     ingestSourceOriginPositionalArg,
		},
		{
			name:           "positional arg used when flag is only whitespace",
			flagValue:      "   ",
			positionalArgs: []string{"/positional/dir"},
			wantSource:     "/positional/dir",
			wantOrigin:     ingestSourceOriginPositionalArg,
		},
		{
			name:           "error when flag and positional args are empty",
			flagValue:      "",
			positionalArgs: nil,
			wantErr:        true,
		},
		{
			name:           "error when flag empty and multiple positional args",
			flagValue:      "",
			positionalArgs: []string{"/one", "/two"},
			wantErr:        true,
		},
		{
			name:           "error when flag empty and single positional arg is blank",
			flagValue:      "",
			positionalArgs: []string{"   "},
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSource, gotOrigin, err := resolveIngestSource(tt.flagValue, tt.positionalArgs)
			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveIngestSource() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveIngestSource() error = %v, want nil", err)
			}
			if gotSource != tt.wantSource {
				t.Errorf("resolveIngestSource() source = %q, want %q", gotSource, tt.wantSource)
			}
			if gotOrigin != tt.wantOrigin {
				t.Errorf("resolveIngestSource() origin = %q, want %q", gotOrigin, tt.wantOrigin)
			}
		})
	}
}

func TestNewRedisStoreSucceedsWithValidURLAndNamespace(t *testing.T) {
	store, err := newRedisStore("redis://127.0.0.1:6379/0", "checkout-abc123")
	if err != nil {
		t.Fatalf("newRedisStore() error = %v, want nil", err)
	}
	if store == nil {
		t.Fatal("newRedisStore() store = nil, want non-nil")
	}
}

func TestNewRedisStoreFailsOnInvalidURL(t *testing.T) {
	_, err := newRedisStore("not-a-url", "checkout-abc123")
	if err == nil {
		t.Fatal("newRedisStore() error = nil, want non-nil for an invalid URL")
	}
}

func TestNewRedisStoreFailsOnInvalidNamespace(t *testing.T) {
	_, err := newRedisStore("redis://127.0.0.1:6379/0", "")
	if err == nil {
		t.Fatal("newRedisStore() error = nil, want non-nil for an invalid namespace")
	}
}

func TestDoctorStoreSucceedsWithValidEndpoint(t *testing.T) {
	store := doctorStore("redis://127.0.0.1:6379/0", "checkout-abc123")
	if store == nil {
		t.Fatal("doctorStore() = nil, want non-nil for a valid endpoint")
	}
}

func TestDoctorStoreToleratesUnconstructibleClient(t *testing.T) {
	// An unreachable or malformed endpoint must not prevent the doctor
	// report from being produced; doctorStore reports the failure via
	// commandLog and returns nil rather than an error so the report still
	// runs and explains why the Redis checks could not run.
	store := doctorStore("not-a-url", "checkout-abc123")
	if store != nil {
		t.Fatal("doctorStore() != nil, want nil for an unconstructible client")
	}
}

func TestResolveBackfillMode(t *testing.T) {
	tests := []struct {
		name       string
		replayOnly bool
		want       backfillMode
	}{
		{
			name:       "replay-only flag selects replay-only mode",
			replayOnly: true,
			want:       backfillModeReplayOnly,
		},
		{
			name:       "unset flag selects full mode",
			replayOnly: false,
			want:       backfillModeFull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveBackfillMode(tt.replayOnly); got != tt.want {
				t.Errorf("resolveBackfillMode(%v) = %q, want %q", tt.replayOnly, got, tt.want)
			}
		})
	}
}

func TestResolveDoctorBackendSource(t *testing.T) {
	tests := []struct {
		name        string
		selected    bool
		policyPath  string
		marketplace string
		redisFlag   bool
		redisEnv    string
		want        doctorBackendSource
	}{
		{name: "nothing configured", want: doctorBackendSourceDefault},
		{name: "selected policy", selected: true, want: doctorBackendSourcePolicy},
		{name: "policy path", policyPath: "policy.json", want: doctorBackendSourcePolicy},
		{name: "marketplace policy path", marketplace: "market.json", want: doctorBackendSourcePolicy},
		{name: "blank policy paths are ignored", policyPath: "  ", marketplace: "\t", want: doctorBackendSourceDefault},
		{name: "redis flag", redisFlag: true, want: doctorBackendSourceRedis},
		{name: "redis env", redisEnv: "redis://127.0.0.1:6379/0", want: doctorBackendSourceRedis},
		{name: "blank redis env is ignored", redisEnv: "  ", want: doctorBackendSourceDefault},
		{name: "policy outranks redis", policyPath: "policy.json", redisFlag: true, redisEnv: "redis://x", want: doctorBackendSourcePolicy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveDoctorBackendSource(tt.selected, tt.policyPath, tt.marketplace, tt.redisFlag, tt.redisEnv)
			if got != tt.want {
				t.Errorf("resolveDoctorBackendSource() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyWorkerStop(t *testing.T) {
	someErr := errors.New("lease failed")

	tests := []struct {
		name          string
		runErr        error
		ctxErr        error
		wantReason    workerStopReason
		wantPropagate bool
	}{
		{
			name:          "nil run error is a clean stop regardless of context",
			runErr:        nil,
			ctxErr:        context.Canceled,
			wantReason:    workerStopReasonClean,
			wantPropagate: false,
		},
		{
			name:          "run error with a cancelled context is an expected shutdown",
			runErr:        someErr,
			ctxErr:        context.Canceled,
			wantReason:    workerStopReasonShutdown,
			wantPropagate: false,
		},
		{
			name:          "run error with a live context is a real failure",
			runErr:        someErr,
			ctxErr:        nil,
			wantReason:    workerStopReasonError,
			wantPropagate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotPropagate := classifyWorkerStop(tt.runErr, tt.ctxErr)
			if gotReason != tt.wantReason {
				t.Errorf("classifyWorkerStop() reason = %q, want %q", gotReason, tt.wantReason)
			}
			if gotPropagate != tt.wantPropagate {
				t.Errorf("classifyWorkerStop() propagate = %v, want %v", gotPropagate, tt.wantPropagate)
			}
		})
	}
}

func TestResolveActionsEnvironment(t *testing.T) {
	values := map[string]string{
		"GITHUB_TOKEN":      "a-token",
		"GITHUB_ACTOR":      "an-actor",
		"GITHUB_REPOSITORY": "owner/repo",
		"GITHUB_API_URL":    "https://api.example.com",
	}
	getenv := func(name string) string { return values[name] }

	got := resolveActionsEnvironment(getenv)

	want := actionsEnvironment{
		Token:      "a-token",
		Actor:      "an-actor",
		Repository: "owner/repo",
		APIURL:     "https://api.example.com",
	}
	if got != want {
		t.Fatalf("resolveActionsEnvironment() = %+v, want %+v", got, want)
	}
}

func TestResolveActionsEnvironmentLeavesMissingVariablesEmpty(t *testing.T) {
	getenv := func(string) string { return "" }

	got := resolveActionsEnvironment(getenv)

	if got != (actionsEnvironment{}) {
		t.Fatalf("resolveActionsEnvironment() = %+v, want zero value", got)
	}
}

func TestActionsEnvironmentPresentReportsPresenceNotValues(t *testing.T) {
	tests := []struct {
		name string
		env  actionsEnvironment
		want string
	}{
		{
			name: "all set",
			env: actionsEnvironment{
				Token: "secret-token", Actor: "octocat",
				Repository: "owner/repo", APIURL: "https://api.example.com",
			},
			want: "token=true actor=true repository=true api_url=true",
		},
		{
			name: "none set",
			env:  actionsEnvironment{},
			want: "token=false actor=false repository=false api_url=false",
		},
		{
			name: "partially set",
			env:  actionsEnvironment{Token: "secret-token"},
			want: "token=true actor=false repository=false api_url=false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.env.present()
			if got != tt.want {
				t.Errorf("present() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "secret-token") {
				t.Errorf("present() = %q, must not include the token value", got)
			}
		})
	}
}

func TestRootCommandRegistersEverySubcommand(t *testing.T) {
	want := []string{"admission", "adx", "app-key-check", "audit", "azure-functions", "backfill", "benchmark-queries", "collect", "compile-queries", "database", "doctor", "execute-cli", "hosted-cli", "ingest", "otel-smoke", "serve", "serve-hosted", "serve-pages", "simulate-api", "simulate-webhooks"}
	root := newRootCommand()

	got := make([]string, 0, len(want))
	for _, cmd := range root.Commands() {
		got = append(got, cmd.Name())
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("newRootCommand() registered %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("newRootCommand() registered %v, want %v", got, want)
			break
		}
	}
}

func TestTelemetrySmokeCommandUsesEnvironmentWithoutReportingSecrets(t *testing.T) {
	const (
		traceID             = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID              = "00f067aa0ba902b7"
		authorizationHeader = "Basic command-test-credential"
	)
	cao := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set(telemetry.TraceIDHeader, traceID)
		response.Header().Set(telemetry.SpanIDHeader, spanID)
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cao.Close)
	openObserve := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz":
			response.WriteHeader(http.StatusOK)
		case "/api/default/default/traces/latest":
			if got := request.Header.Get("Authorization"); got != authorizationHeader {
				t.Errorf("authorization header = %q, want %q", got, authorizationHeader)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"hits":[{"trace_id":"` + traceID + `"}]}`))
		case "/api/default/_search":
			if got := request.Header.Get("Authorization"); got != authorizationHeader {
				t.Errorf("metrics authorization header = %q, want %q", got, authorizationHeader)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"hits":[{"metric_rows":1}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(openObserve.Close)

	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", openObserve.URL+"/api/default/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "Authorization="+authorizationHeader)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", openObserve.URL+"/api/default/v1/metrics")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "Authorization="+authorizationHeader)

	var output bytes.Buffer
	command := newTelemetrySmokeCommand()
	command.SetOut(&output)
	command.SetArgs([]string{
		"--cao-readiness-url", cao.URL + "/api/readiness",
		"--require-metrics",
		"--timeout", "1s",
		"--poll-interval", "1ms",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var report telemetry.SmokeReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode smoke report: %v", err)
	}
	if !report.Passed || !report.MetricsVerified {
		t.Fatalf("smoke report did not verify traces and metrics: %+v", report)
	}
	for _, forbidden := range []string{traceID, authorizationHeader, cao.URL, openObserve.URL} {
		if strings.Contains(output.String(), forbidden) {
			t.Errorf("command output contains sensitive runtime value %q", forbidden)
		}
	}
}

func TestRootCommandRejectsUnknownSubcommand(t *testing.T) {
	root := newRootCommand()
	root.SetArgs([]string{"bogus"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Fatalf("Execute() error = %q, want to contain %q", err.Error(), `unknown command "bogus"`)
	}
}

func TestRootCommandReportsUsageErrorWithNoSubcommand(t *testing.T) {
	root := newRootCommand()
	root.SetArgs(nil)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "usage: cao-dashboard") {
		t.Fatalf("Execute() error = %q, want to contain %q", err.Error(), "usage: cao-dashboard")
	}
}

func TestSetupTelemetryPropagatesSetupError(t *testing.T) {
	setupErr := errors.New("exporter unavailable")
	fakeSetup := func(context.Context, string) (telemetry.Shutdown, error) {
		return nil, setupErr
	}

	closeFn, err := setupTelemetry(context.Background(), "dev", fakeSetup)
	if err == nil {
		t.Fatal("setupTelemetry() error = nil, want non-nil")
	}
	if !errors.Is(err, setupErr) {
		t.Errorf("setupTelemetry() error = %v, want to wrap %v", err, setupErr)
	}
	if closeFn != nil {
		t.Error("setupTelemetry() close = non-nil, want nil on setup error")
	}
}

func TestSetupTelemetryCallsShutdownOnClose(t *testing.T) {
	var shutdownCalled bool
	var shutdownCtxErr error
	fakeSetup := func(context.Context, string) (telemetry.Shutdown, error) {
		return func(ctx context.Context) error {
			shutdownCalled = true
			// Capture Err() while shutdownCtx is still live: its own
			// bounding timeout is cancelled by setupTelemetry's deferred
			// cancel() as soon as this callback returns.
			shutdownCtxErr = ctx.Err()
			return nil
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	closeFn, err := setupTelemetry(ctx, "dev", fakeSetup)
	if err != nil {
		t.Fatalf("setupTelemetry() error = %v, want nil", err)
	}
	// The close function must still flush after the parent context is
	// cancelled, because it normally runs during signal-triggered shutdown.
	cancel()
	closeFn()

	if !shutdownCalled {
		t.Fatal("setupTelemetry() close did not invoke the underlying shutdown")
	}
	if shutdownCtxErr != nil {
		t.Errorf("setupTelemetry() close ran shutdown with a cancelled context, want a live one; err = %v", shutdownCtxErr)
	}
}

func TestSetupTelemetryCloseSurvivesShutdownError(t *testing.T) {
	fakeSetup := func(context.Context, string) (telemetry.Shutdown, error) {
		return func(context.Context) error {
			return errors.New("flush failed")
		}, nil
	}

	closeFn, err := setupTelemetry(context.Background(), "dev", fakeSetup)
	if err != nil {
		t.Fatalf("setupTelemetry() error = %v, want nil", err)
	}
	// A shutdown failure must not panic or otherwise disrupt the
	// subcommand's own return path.
	closeFn()
}

func TestRootCommandParsesKnownSubcommandFlags(t *testing.T) {
	root := newRootCommand()

	found, _, err := root.Find([]string{"serve-hosted", "--listen", "127.0.0.1:9000"})
	if err != nil {
		t.Fatalf("Find() error = %v, want nil", err)
	}
	if found.Name() != "serve-hosted" {
		t.Fatalf("Find() name = %q, want %q", found.Name(), "serve-hosted")
	}
	if err := found.ParseFlags([]string{"--listen", "127.0.0.1:9000"}); err != nil {
		t.Fatalf("ParseFlags() error = %v, want nil", err)
	}
	gotListen, err := found.Flags().GetString("listen")
	if err != nil {
		t.Fatalf("Flags().GetString(listen) error = %v, want nil", err)
	}
	if gotListen != "127.0.0.1:9000" {
		t.Errorf("listen flag = %q, want %q", gotListen, "127.0.0.1:9000")
	}
}
