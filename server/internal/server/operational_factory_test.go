package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestOperationalPostgresPolicyAndFactory(t *testing.T) {
	policy := hostPolicy{
		Target:      targetPolicy{Module: "container", Replicas: 2},
		Operational: &operationalPolicy{Backend: "postgres", Postgres: &postgresPolicy{URLEnv: "OPERATIONAL_POSTGRES_URL"}},
	}

	host, err := policy.resolve(mapLookup(map[string]string{
		"OPERATIONAL_POSTGRES_URL": "postgres://127.0.0.1/example?sslmode=disable",
		"CAO_DATABASE_NAMESPACE":   "canonical", "CAO_OPERATIONAL_NAMESPACE": "operational",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if host.Profile.RequiresRedis || host.Profile.SingleProcess || host.AllowVolatile ||
		!host.Profile.SupportsCollection || host.DatabaseNamespace != "canonical" ||
		host.OperationalNamespace != "operational" || host.OperationalBackend != "postgres" {
		t.Fatal("PostgreSQL resolution changed topology or namespace")
	}
	if err := validateStandaloneOperational(host); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*operationalPolicy){
		"redis":                 func(p *operationalPolicy) { p.Redis = &redisPolicy{Module: "generic"} },
		"process":               func(p *operationalPolicy) { p.SingleProcess = true },
		"volatile":              func(p *operationalPolicy) { p.AllowVolatile = true },
		"secret instead of env": func(p *operationalPolicy) { p.Postgres = &postgresPolicy{URLEnv: "postgres://secret"} },
	} {
		t.Run(name, func(t *testing.T) {
			p := policy
			selector := *policy.Operational
			p.Operational = &selector
			change(p.Operational)
			if _, err := p.resolve(mapLookup(map[string]string{"OPERATIONAL_POSTGRES_URL": "configured"})); err == nil {
				t.Fatal("unsafe PostgreSQL policy accepted")
			}
		})
	}
	if _, err := policy.resolve(mapLookup(nil)); err == nil {
		t.Fatal("missing PostgreSQL credentials accepted")
	}
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL is required for factory integration")
	}
	host.OperationalPostgresURL = dsn
	host.OperationalNamespace = "operational-factory-" + t.Name()
	if _, _, err := newOperationalStore(t.Context(), host); err == nil {
		t.Fatal("runtime factory bootstrapped a missing namespace")
	}
	provisioned, err := postgres.New(t.Context(), dsn, host.OperationalNamespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = provisioned.Close() }()
	store, _, err := newOperationalStore(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := operational.ValidateOperationalServices(store.Capabilities(), store.OperationalServices(), operational.Requirements{OAuth: true, Collection: true}); err != nil {
		t.Fatal(err)
	}
	health, err := store.OperationalServices().Health.Health(t.Context())
	if err != nil || !health.Ready {
		t.Fatalf("PostgreSQL health: %+v %v", health, err)
	}
	if cleaner, ok := store.(interface{ DeleteNamespace(context.Context) error }); ok {
		if err := cleaner.DeleteNamespace(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOperationalDefaultIsPostgresWithoutRedis(t *testing.T) {
	policy := hostPolicy{Target: targetPolicy{Module: "container"}}
	resolved, err := policy.resolve(mapLookup(map[string]string{"CAO_POSTGRES_URL": "postgres://127.0.0.1/db?sslmode=disable"}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.OperationalBackend != "postgres" || resolved.RedisURL != "" || resolved.Profile.RequiresRedis {
		t.Fatal("default hosting still requires Redis")
	}
	if _, err := policy.resolve(mapLookup(map[string]string{"REDIS_URL": "redis://127.0.0.1"})); err == nil {
		t.Fatal("Redis credentials implicitly selected a backend")
	}
}

func TestOperationalPostgresRejectsMalformedURLReferences(t *testing.T) {
	for name, value := range map[string]string{
		"null": "null", "empty": `""`, "oversized": `"` + strings.Repeat("A", 129) + `"`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cao.json")
			document := `{"control-plane":{"web":{"host":{"target":{"module":"container"},"operational-store":{"backend":"postgres","postgres":{"url-env":` + value + `}}}}}}`
			if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CAO_POLICY_PATH", path)
			t.Setenv("CAO_POSTGRES_URL", "postgres://127.0.0.1/db?sslmode=disable")
			if _, err := loadHostPolicyFromEnv(); err == nil || !strings.Contains(err.Error(), "url-env") {
				t.Fatalf("malformed reference was not rejected: %v", err)
			}
		})
	}
}

func memoryHostPolicy() hostPolicy {
	return hostPolicy{
		Target: targetPolicy{Module: "container", Replicas: 1},
		Operational: &operationalPolicy{
			Backend: "memory", SingleProcess: true, AllowVolatile: true,
		},
	}
}

func TestOperationalMemoryPolicyNeedsNoRedisAndPreservesPostgresNamespace(t *testing.T) {
	policy := memoryHostPolicy()
	resolved, err := policy.resolve(mapLookup(map[string]string{
		"CAO_DATABASE_NAMESPACE":    "existing-dataset",
		"CAO_OPERATIONAL_NAMESPACE": "volatile-state",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RedisURL != "" || resolved.Profile.RequiresRedis ||
		resolved.DatabaseNamespace != "existing-dataset" ||
		resolved.OperationalNamespace != "volatile-state" ||
		!resolved.Profile.SingleProcess || !resolved.AllowVolatile {
		t.Fatalf("unexpected memory resolution: %+v", resolved)
	}
}

func TestOperationalSelectionRejectsAmbiguityAndUnsafeTopology(t *testing.T) {
	for name, change := range map[string]func(*hostPolicy){
		"nested Redis conflict":            func(p *hostPolicy) { p.Operational.Redis = &redisPolicy{Module: "local"} },
		"missing process acknowledgement":  func(p *hostPolicy) { p.Operational.SingleProcess = false },
		"missing volatile acknowledgement": func(p *hostPolicy) { p.Operational.AllowVolatile = false },
		"missing replica confirmation":     func(p *hostPolicy) { p.Target.Replicas = 0 },
		"multiple replicas":                func(p *hostPolicy) { p.Target.Replicas = 2 },
		"platform scaling": func(p *hostPolicy) {
			p.Target = targetPolicy{Module: "generic", Authentication: HostAuthenticationOAuth, Listener: HostListenerPlatform}
		},
		"unknown backend": func(p *hostPolicy) { p.Operational.Backend = "sqlite" },
	} {
		t.Run(name, func(t *testing.T) {
			policy := memoryHostPolicy()
			change(&policy)
			if _, err := policy.resolve(mapLookup(nil)); err == nil {
				t.Fatal("accepted unsafe operational selection")
			}
		})
	}
}

func TestOperationalMemoryRejectsExternalAndPlatformListeners(t *testing.T) {
	policy := memoryHostPolicy()
	yes := true
	policy.Target = targetPolicy{
		Module: "generic", Authentication: HostAuthenticationOAuth,
		Listener: HostListenerExternal, SupportsSingleReplica: &yes, Replicas: 1,
	}
	if _, err := policy.resolve(mapLookup(nil)); err == nil {
		t.Fatal("memory accepted a detached external listener")
	}
	policy.Target.Listener = HostListenerProcess
	if _, err := policy.resolve(mapLookup(nil)); err != nil {
		t.Fatal(err)
	}
	policy.Target.Listener = HostListenerPlatform
	if _, err := policy.resolve(mapLookup(nil)); err == nil {
		t.Fatal("platform topology accepted process-local authority")
	}
}

func TestOperationalRedisSelectorRetainsDefaultAndProviderConfiguration(t *testing.T) {
	policy := hostPolicy{
		Target:      targetPolicy{Module: "container"},
		Operational: &operationalPolicy{Backend: "redis"},
	}
	resolved, err := policy.resolve(mapLookup(map[string]string{"REDIS_URL": "rediss://cache.example"}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.OperationalBackend != "redis" || resolved.Profile.RedisSession != HostRedisPooled {
		t.Fatalf("Redis defaults changed: %+v", resolved)
	}
	policy.Operational.Redis = &redisPolicy{Module: "upstash"}
	if _, err := policy.resolve(mapLookup(map[string]string{"REDIS_URL": "rediss://cache.upstash.io"})); err == nil {
		t.Fatal("Upstash replica requirement was bypassed")
	}
}

func TestOperationalMemoryFactoryAndStandaloneRoles(t *testing.T) {
	t.Setenv("CAO_REDIS_MAX_BYTES", "not-a-Redis-budget")
	host, err := memoryHostPolicy().resolve(mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStandaloneOperational(host); err == nil {
		t.Fatal("standalone memory role accepted")
	}
	store, prefix, err := newOperationalStore(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if prefix != "" || store.Capabilities().Sessions.Scope != operational.ScopeProcess ||
		store.Capabilities().Collection.Persistence != operational.PersistenceVolatile {
		t.Fatal("memory factory changed process-lifetime guarantees")
	}
	services := store.OperationalServices()
	if services.Collection != store.OperationalServices().Collection {
		t.Fatal("unstable service identity")
	}
	health, err := services.Health.Health(t.Context())
	if err != nil || !health.Ready {
		t.Fatalf("memory health: %+v %v", health, err)
	}
}

func TestOperationalMemoryHostRequiresOAuthHTTPSAndCoResidentCollection(t *testing.T) {
	host, err := memoryHostPolicy().resolve(mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := newOperationalStore(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	config := Config{
		HostProfile: host.Profile, SingleReplicaConfirmed: true,
		AllowVolatile: true,
		GitHubOAuth:   &GitHubOAuthConfig{}, Proxy: ProxyPolicy{RequireHTTPS: true},
	}
	if err := validateHostProfile(store, &config); err != nil {
		t.Fatal(err)
	}
	config.HostProfile.SingleProcess = false
	if err := validateHostProfile(store, &config); err == nil {
		t.Fatal("replica confirmation substituted for single-process acknowledgement")
	}
	config.HostProfile.SingleProcess = true
	config.AllowVolatile = false
	if err := validateHostProfile(store, &config); err == nil {
		t.Fatal("memory accepted missing volatile-state acknowledgement")
	}
	config.AllowVolatile = true
	config.Proxy.RequireHTTPS = false
	if err := validateHostProfile(store, &config); err == nil {
		t.Fatal("memory accepted disabled HTTPS enforcement")
	}
	config.Proxy.RequireHTTPS = true
	config.HostProfile.Listener = HostListenerExternal
	if err := validateHostProfile(store, &config); err == nil {
		t.Fatal("memory accepted external listener")
	}
	config.HostProfile.Listener = HostListenerProcess
	config.GitHubOAuth = nil
	if err := validateHostProfile(store, &config); err == nil {
		t.Fatal("memory accepted missing OAuth")
	}
	config.GitHubOAuth = &GitHubOAuthConfig{}
	config.Collector = &CollectorConfig{AdmitOnly: true}
	if err := validateHostProfile(store, &config); err == nil {
		t.Fatal("memory accepted disconnected admission")
	}
}

func TestOperationalMemoryBoundsFromEnvironment(t *testing.T) {
	t.Setenv("CAO_OPERATIONAL_CACHE_MAX_ENTRIES", "16")
	t.Setenv("CAO_OPERATIONAL_CACHE_MAX_BYTES", "4096")
	config, err := memoryConfigFromEnv()
	if err != nil || config.MaxCacheEntries != 16 || config.MaxCacheBytes != 4096 {
		t.Fatalf("memory bounds: %+v %v", config, err)
	}
	host, err := memoryHostPolicy().resolve(mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := newOperationalStore(t.Context(), host)
	if err != nil {
		t.Fatalf("partial cache bounds cannot initialize memory: %v", err)
	}
	_ = store.Close()
	t.Setenv("CAO_OPERATIONAL_CACHE_MAX_ENTRIES", "-1")
	if _, err := memoryConfigFromEnv(); err == nil {
		t.Fatal("invalid bound accepted")
	}
}

func TestOperationalFactoryConfiguresDirectRedisOptions(t *testing.T) {
	client, err := redisx.New("redis://127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}
	store := redisx.NewStore(client, "composition-test")
	t.Setenv("CAO_REDIS_MAX_BYTES", "8192")
	config := Config{HostProfile: localHostProfile()}
	if err := configureOperationalStore(t.Context(), store, &config); err != nil {
		t.Fatal(err)
	}
	if config.RedisMaxBytes != 8192 || store.MaxMemoryBytes() != 8192 {
		t.Fatalf("Redis adapter option not applied: config=%d adapter=%d", config.RedisMaxBytes, store.MaxMemoryBytes())
	}
	t.Setenv("CAO_REDIS_MAX_BYTES", "invalid")
	config.RedisMaxBytes = 4096
	if err := configureOperationalStore(t.Context(), store, &config); err != nil {
		t.Fatalf("explicit Redis option did not override environment: %v", err)
	}
	if config.RedisMaxBytes != 4096 || store.MaxMemoryBytes() != 4096 {
		t.Fatal("explicit Redis option changed")
	}
}

func TestOperationalFactoryIgnoresRedisOptionsForMemory(t *testing.T) {
	t.Setenv("CAO_REDIS_MAX_BYTES", "invalid")
	host, err := memoryHostPolicy().resolve(mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := newOperationalStore(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	config := Config{HostProfile: host.Profile, RedisMaxBytes: -1}
	if err := configureOperationalStore(t.Context(), store, &config); err != nil {
		t.Fatalf("memory read a Redis-only option: %v", err)
	}
	if config.RedisMaxBytes != 0 {
		t.Fatal("memory retained an irrelevant Redis byte budget")
	}
}
