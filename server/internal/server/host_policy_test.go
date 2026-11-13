package server

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestHostPolicyResolvesGenericRedisURLAndTLS(t *testing.T) {
	required := true
	collection := true
	policy := hostPolicy{
		Target: targetPolicy{
			Module:         "generic",
			Name:           "managed-redis",
			Authentication: HostAuthenticationOAuth,
			Listener:       HostListenerProcess,
			RequireHTTPS:   &required,
		},
		Operational: &operationalPolicy{
			Backend: "redis", NamespaceEnv: "CACHE_NAMESPACE",
			Redis: &redisPolicy{
				Module:             "generic",
				SupportsCollection: &collection,
				TLS: redisTLSPolicy{
					Mode:             redisTLSRequired,
					ServerNameEnv:    "CACHE_SERVER_NAME",
					CACertificateEnv: "CACHE_CA",
				},
			},
		},
	}
	values := map[string]string{
		"REDIS_URL":              "redis://cache.internal:6379",
		"CACHE_NAMESPACE":        "production",
		"CACHE_SERVER_NAME":      "cache.example.com",
		"CACHE_CA":               "certificate",
		"CAO_DATABASE_NAMESPACE": "canonical",
	}
	resolved, err := policy.resolve(mapLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RedisURL != values["REDIS_URL"] || resolved.DatabaseNamespace != "canonical" ||
		resolved.OperationalNamespace != "production" {
		t.Fatalf("unexpected Redis resolution: %+v", resolved)
	}
	if !resolved.RedisOptions.ForceTLS ||
		resolved.RedisOptions.TLSServerName != "cache.example.com" ||
		resolved.RedisOptions.TLSCACertificatePEM != "certificate" {
		t.Fatalf("TLS settings were not preserved: %+v", resolved.RedisOptions)
	}
}

func TestRedisProviderModulesMapEnvironment(t *testing.T) {
	for _, module := range []string{
		"generic", "redis-cloud", "railway", "render", "digitalocean",
	} {
		t.Run(module, func(t *testing.T) {
			policy := hostPolicy{
				Target:      targetPolicy{Module: "container"},
				Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: module}},
			}
			resolved, err := policy.resolve(mapLookup(map[string]string{
				"REDIS_URL": "rediss://cache.example.com:6379",
			}))
			if err != nil {
				t.Fatal(err)
			}
			if resolved.RedisURL == "" {
				t.Fatal("provider module did not map REDIS_URL")
			}
		})
	}

	t.Run("aws-elasticache", func(t *testing.T) {
		policy := hostPolicy{
			Target:      targetPolicy{Module: "container"},
			Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "aws-elasticache"}},
		}
		resolved, err := policy.resolve(mapLookup(map[string]string{
			"REDIS_URL": "rediss://cache.example.amazonaws.com:6379",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(resolved.RedisURL, "amazonaws.com") {
			t.Fatalf("unexpected ElastiCache URL: %q", resolved.RedisURL)
		}
	})

	t.Run("gcp-memorystore", func(t *testing.T) {
		policy := hostPolicy{
			Target: targetPolicy{Module: "container"},
			Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{
				Module: "gcp-memorystore",
				TLS:    redisTLSPolicy{Mode: redisTLSRequired},
			}},
		}
		resolved, err := policy.resolve(mapLookup(map[string]string{
			"REDISHOST": "10.0.0.5",
			"REDISPORT": "6378",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if resolved.RedisURL != "rediss://10.0.0.5:6378" {
			t.Fatalf("unexpected Memorystore URL: %q", resolved.RedisURL)
		}
	})
}

func TestHostPolicyFileIsRequired(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv(marketplacePolicyPathEnv, "")
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if _, err := loadHostPolicyFromEnv(); err == nil {
		t.Fatal("missing default cao.json was accepted")
	}
	t.Chdir(workingDirectory)

	t.Setenv("CAO_POLICY_PATH", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := loadHostPolicyFromEnv(); err == nil {
		t.Fatal("explicitly configured missing policy was accepted")
	}
}

func TestConfiguredHostPolicyReadsCaoJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	document := `{
		"version": 1,
		"control-plane": {
			"scope": {"allowed-owners": ["example"]},
			"web": {
				"host": {
					"target": {"module": "container", "name": "render"},
					"operational-store": {"backend": "redis", "redis": {"module": "render", "tls": {"mode": "required"}}}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	t.Setenv("REDIS_URL", "rediss://cache.example.com:6379")
	resolved, err := loadHostPolicyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile.Name != "render" || !resolved.RedisOptions.ForceTLS {
		t.Fatalf("unexpected resolved policy: %+v", resolved)
	}
}

func TestConfiguredHostPolicyComposesDeploymentProfile(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "cao.json")
	profilePath := filepath.Join(directory, "cao.test.json")
	base := `{
		"version": 1,
		"control-plane": {
			"scope": {"allowed-owners": ["example"]},
			"web": {"experimental": true}
		}
	}`
	profile := `{
		"extends": "cao.json",
		"control-plane": {
			"web": {
				"host": {
					"target": {"module": "container", "name": "test", "replicas": 1},
					"operational-store": {"backend": "redis", "redis": {"module": "generic", "tls": {"mode": "required"}}}
				}
			}
		}
	}`
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", profilePath)
	t.Setenv("REDIS_URL", "rediss://cache.example.com:6379")
	resolved, err := loadHostPolicyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile.Name != "test" || !resolved.RedisOptions.ForceTLS {
		t.Fatalf("unexpected resolved deployment profile: %+v", resolved)
	}
}

func TestDeploymentProfilesRejectAuthorityOverridesAndCycles(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "cao.json")
	if err := os.WriteFile(basePath, []byte(`{"control-plane":{"web":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	wideningPath := filepath.Join(directory, "cao.widening.json")
	if err := os.WriteFile(wideningPath, []byte(`{
		"extends": "cao.json",
		"control-plane": {
			"campaigns": {"example": {"mode": "live"}},
			"web": {"host": {}}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComposedHostPolicy(wideningPath); err == nil ||
		!strings.Contains(err.Error(), "only extend control-plane.web.host") {
		t.Fatalf("authority override was not rejected: %v", err)
	}

	aPath := filepath.Join(directory, "cao.a.json")
	bPath := filepath.Join(directory, "cao.b.json")
	if err := os.WriteFile(aPath, []byte(`{
		"extends": "cao.b.json",
		"control-plane": {"web": {"host": {}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`{
		"extends": "cao.a.json",
		"control-plane": {"web": {"host": {}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComposedHostPolicy(aPath); err == nil ||
		!strings.Contains(err.Error(), "cycle") {
		t.Fatalf("deployment profile cycle was not rejected: %v", err)
	}

	outsidePath := filepath.Join(filepath.Dir(directory), "cao.outside.json")
	if err := os.WriteFile(outsidePath, []byte(`{"control-plane":{"web":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	escapePath := filepath.Join(directory, "cao.escape.json")
	if err := os.WriteFile(escapePath, []byte(`{
		"extends": "../cao.outside.json",
		"control-plane": {"web": {"host": {}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComposedHostPolicy(escapePath); err == nil ||
		!strings.Contains(err.Error(), "within the policy directory") {
		t.Fatalf("deployment profile traversal was not rejected: %v", err)
	}
}

func TestHostPolicyRejectsFlatHostFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	document := `{
		"control-plane": {
			"web": {
				"host": {
					"name": "legacy",
					"authentication": "github-oauth",
					"listener": "process",
					"redis": {"preset": "generic"}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	if _, err := loadHostPolicyFromEnv(); err == nil {
		t.Fatal("flat host policy was accepted")
	}
}

func TestHostPolicyRejectsLegacyRedisSelection(t *testing.T) {
	for _, selection := range []string{
		`"redis":{"module":"generic"}`,
		`"redis":{},"operational-store":{"backend":"postgres"}`,
		`"redis":null`,
		`"operational-store":{"backend":"redis","redis":{"module":"generic","namespace-env":"CACHE_NAMESPACE"}}`,
	} {
		t.Run(selection, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cao.json")
			document := `{"control-plane":{"web":{"host":{"target":{"module":"container"},` + selection + `}}}}`
			if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CAO_POLICY_PATH", path)
			t.Setenv("REDIS_URL", "rediss://cache.example")
			t.Setenv("CAO_POSTGRES_URL", "postgres://127.0.0.1/db?sslmode=disable")
			if _, err := loadHostPolicyFromEnv(); err == nil {
				t.Fatal("legacy Redis selection was accepted")
			}
			if strings.HasPrefix(selection, `"redis":`) {
				if _, err := OperationalPolicySelectedFromEnv(); err == nil {
					t.Fatal("local CLI selection ignored a legacy Redis policy")
				}
			}
		})
	}
}

func TestHostAndRedisModulesComposeIndependently(t *testing.T) {
	resolved, err := (hostPolicy{
		Target:      targetPolicy{Module: "container", Replicas: 1},
		Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "upstash"}},
	}).resolve(mapLookup(map[string]string{
		"REDIS_URL": "rediss://example.upstash.io:6379",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile.Listener != HostListenerProcess ||
		resolved.Profile.RedisSession != HostRedisSerialized ||
		!resolved.Profile.SingleReplica ||
		resolved.Profile.SupportsCollection {
		t.Fatalf("modules did not compose expected capabilities: %+v", resolved.Profile)
	}
	if !resolved.SingleReplicaConfirmed {
		t.Fatal("policy resolution ignored the single-replica target configuration")
	}
}

func TestExternalHostProfileComposesWithRedis(t *testing.T) {
	profile, err := (hostPolicy{
		Target: targetPolicy{
			Module: "generic", Authentication: HostAuthenticationOAuth,
			Listener: HostListenerExternal,
		},
		Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "redis-cloud"}},
	}).resolve(mapLookup(map[string]string{
		"REDIS_URL": "rediss://cache.example.com:6379",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if profile.Profile.Listener != HostListenerExternal ||
		!profile.Profile.RequiresHTTPS || !profile.Profile.SupportsCollection {
		t.Fatalf("external host lost security or Redis capabilities: %+v", profile.Profile)
	}
}

func TestDeploymentModuleRegistry(t *testing.T) {
	targets := make([]string, 0, len(hostTargetModules))
	for name := range hostTargetModules {
		targets = append(targets, name)
	}
	sort.Strings(targets)
	if !slices.Equal(targets, []string{"container", "generic"}) {
		t.Fatalf("unexpected host target modules: %v", targets)
	}
	providers := make([]string, 0, len(redisProviderModules))
	for name := range redisProviderModules {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	if !slices.Equal(providers, []string{
		"aws-elasticache", "digitalocean", "gcp-memorystore", "generic", "local",
		"railway", "redis-cloud", "render", "upstash",
	}) {
		t.Fatalf("unexpected Redis provider modules: %v", providers)
	}
}

func TestModulesRejectUnknownOrOverriddenCapabilities(t *testing.T) {
	tests := []hostPolicy{
		{Target: targetPolicy{Module: "unknown"}, Operational: &operationalPolicy{Backend: "redis"}},
		{Target: targetPolicy{Module: "container", Listener: HostListenerPlatform}, Operational: &operationalPolicy{Backend: "redis"}},
		{Target: targetPolicy{Module: "container"}, Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "unknown"}}},
		{Target: targetPolicy{Module: "container", Replicas: 1}, Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "upstash", Session: HostRedisPooled}}},
		{Target: targetPolicy{Module: "container", Replicas: 1}, Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "upstash", TLS: redisTLSPolicy{Mode: redisTLSDisabled}}}},
		{Target: targetPolicy{Module: "azure-functions"}, Operational: &operationalPolicy{Backend: "redis"}},
		{Target: targetPolicy{Module: "container"}, Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "upstash"}}},
	}
	for _, policy := range tests {
		if _, err := policy.resolve(mapLookup(map[string]string{
			"REDIS_URL": "rediss://cache.example.com:6379",
		})); err == nil {
			t.Fatalf("accepted invalid module policy: %+v", policy)
		}
		if _, err := (hostPolicy{
			Target:      targetPolicy{Module: "container", Replicas: 1},
			Operational: &operationalPolicy{Backend: "redis", Redis: &redisPolicy{Module: "upstash"}},
		}).resolve(mapLookup(map[string]string{
			"REDIS_URL": "rediss://cache.example.com:6379",
		})); err == nil {
			t.Fatal("Upstash module accepted a non-Upstash endpoint")
		}
	}
}

func TestGenericTLSOptionsRejectInvalidCA(t *testing.T) {
	_, err := redisx.NewWithOptions(
		"redis://cache.example.com:6379",
		redisx.Options{ForceTLS: true, TLSCACertificatePEM: "not a certificate"},
	)
	if err == nil {
		t.Fatal("invalid Redis CA certificate was accepted")
	}
}

func TestResolveRedisTLSMode(t *testing.T) {
	cases := []struct {
		name         string
		policyMode   redisTLSMode
		providerMode redisTLSMode
		wantMode     redisTLSMode
		wantSource   redisTLSModeSource
		wantErr      bool
	}{
		{
			name:       "explicit policy mode takes precedence over the provider",
			policyMode: redisTLSDisabled, providerMode: redisTLSAuto,
			wantMode: redisTLSDisabled, wantSource: redisTLSModeSourcePolicy,
		},
		{
			name:       "provider mode is used when the policy leaves it unset",
			policyMode: "", providerMode: redisTLSRequired,
			wantMode: redisTLSRequired, wantSource: redisTLSModeSourceProvider,
		},
		{
			name:       "auto is the default when neither the policy nor the provider set a mode",
			policyMode: "", providerMode: "",
			wantMode: redisTLSAuto, wantSource: redisTLSModeSourceDefault,
		},
		{
			name:       "a provider requiring TLS accepts an explicit required policy mode",
			policyMode: redisTLSRequired, providerMode: redisTLSRequired,
			wantMode: redisTLSRequired, wantSource: redisTLSModeSourcePolicy,
		},
		{
			name:       "a provider requiring TLS rejects a disabled policy mode",
			policyMode: redisTLSDisabled, providerMode: redisTLSRequired,
			wantErr: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mode, source, err := resolveRedisTLSMode(testCase.policyMode, testCase.providerMode, "generic")
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode != testCase.wantMode || source != testCase.wantSource {
				t.Fatalf("resolveRedisTLSMode() = (%q, %q), want (%q, %q)", mode, source, testCase.wantMode, testCase.wantSource)
			}
		})
	}
}

func TestRedisEnvironmentRejectsInvalidPort(t *testing.T) {
	for _, port := range []string{"0", "65536", "invalid"} {
		_, err := redisURLFromEnvironment(
			mapLookup(map[string]string{"REDISHOST": "cache.internal", "REDISPORT": port}),
			"", "REDISHOST", "REDISPORT", "", "", redisTLSAuto,
		)
		if err == nil {
			t.Errorf("accepted invalid Redis port %q", port)
		}
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
