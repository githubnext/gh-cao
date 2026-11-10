package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func validateRedisProfile(profile HostProfile) error {
	if profile.RedisSession != HostRedisPooled && profile.RedisSession != HostRedisSerialized {
		return fmt.Errorf("host profile %q has unsupported Redis session %q", profile.Name, profile.RedisSession)
	}
	if profile.IsolateProcessNamespace && (!profile.SingleReplica || profile.RedisSession != HostRedisSerialized) {
		return fmt.Errorf("host profile %q requires namespace isolation with a serialized Redis session and one replica", profile.Name)
	}
	if profile.SingleReplica && profile.RedisSession != HostRedisSerialized {
		return fmt.Errorf("host profile %q requires one replica with a serialized Redis session", profile.Name)
	}
	return nil
}

func classifyRedisSessionMismatch(required HostRedisSession, reportsSession, singleSession bool) bool {
	switch required {
	case HostRedisSerialized:
		return !reportsSession || !singleSession
	case HostRedisPooled:
		return reportsSession && singleSession
	default:
		return false
	}
}

func validateRedisStoreComposition(store *redisx.Store, profile HostProfile) error {
	if err := validateRedisProfile(profile); err != nil {
		return err
	}
	client, reports := store.Client.(interface{ SingleSession() bool })
	single := reports && client.SingleSession()
	if classifyRedisSessionMismatch(profile.RedisSession, reports, single) {
		return fmt.Errorf("host profile %q requires a %s Redis client", profile.Name, profile.RedisSession)
	}
	if profile.IsolateProcessNamespace != store.ProcessIsolated() {
		return fmt.Errorf("host profile %q process namespace isolation does not match the Redis store", profile.Name)
	}
	return nil
}

// configureOperationalStore is the composition-only hook for legacy callers
// that supply an already constructed Redis adapter to New.
func configureOperationalStore(_ context.Context, store operational.Store, config *Config) error {
	redisStore, isRedis := store.(*redisx.Store)
	if !isRedis {
		config.RedisMaxBytes = 0
		return nil
	}
	if err := validateRedisStoreComposition(redisStore, config.HostProfile); err != nil {
		return err
	}
	maxBytes, err := RedisMaxBytesFromEnv(config.RedisMaxBytes)
	if err != nil {
		return err
	}
	config.RedisMaxBytes = maxBytes
	queryConfig, err := config.QueryCache.resolve()
	if err != nil {
		return err
	}
	if err := redisStore.SetQueryMaintenanceBytes(queryConfig.MaxBytes); err != nil {
		return err
	}
	return redisStore.SetMaxMemoryBytes(maxBytes)
}

func newOperationalStore(ctx context.Context, host *resolvedHostPolicy) (operational.Store, string, error) {
	backend := firstNonempty(host.OperationalBackend, "postgres")
	if backend == "memory" {
		if !host.Profile.SingleProcess || !host.Profile.SingleReplica ||
			!host.SingleReplicaConfirmed || !host.AllowVolatile ||
			host.Profile.Authentication != HostAuthenticationOAuth ||
			!host.Profile.RequiresHTTPS || host.Profile.Listener != HostListenerProcess {
			return nil, "", errors.New("memory operational-store requires OAuth HTTPS, one process listener, one replica, and allow-volatile")
		}
		config, err := memoryConfigFromEnv()
		if err != nil {
			return nil, "", err
		}
		store, err := memory.New(config)
		if err != nil {
			return nil, "", err
		}
		hostedLog.PrintfContext(ctx, "operational backend=memory scope=process persistence=volatile sessions=restart-invalidated revocations=best-effort accepted-work=lost-on-restart")
		return store, "", nil
	}
	if backend == "postgres" {
		config, err := PostgresOperationalConfigFromEnv()
		if err != nil {
			return nil, "", err
		}
		store, err := postgres.Open(ctx, host.OperationalPostgresURL, host.OperationalNamespace, config)
		if err != nil {
			return nil, "", fmt.Errorf("open provisioned operational storage (run database bootstrap as owner before rollout): %w", err)
		}
		hostedLog.PrintfContext(ctx, "operational backend=postgres scope=deployment persistence=restart")
		return store, "", nil
	}
	if backend != "redis" {
		return nil, "", fmt.Errorf("unsupported operational-store backend %q", backend)
	}
	return newRedisOperationalStore(ctx, host)
}

func newRedisOperationalStore(ctx context.Context, host *resolvedHostPolicy) (*redisx.Store, string, error) {
	if err := validateRedisProfile(host.Profile); err != nil {
		return nil, "", err
	}
	if err := validateHostedRedisURL(host.RedisURL, host.RedisOptions.AllowPrivatePlaintext, host.RedisOptions.ForceTLS); err != nil {
		return nil, "", err
	}
	maxBytes, err := RedisMaxBytesFromEnv(0)
	if err != nil {
		return nil, "", err
	}
	namespace := firstNonempty(host.OperationalNamespace, host.RedisNamespace, "hosted-dashboard")
	revocations, err := durableRevocationKeyPrefix(host.Profile.IsolateProcessNamespace, namespace)
	if err != nil {
		return nil, "", err
	}
	client, err := redisx.NewWithOptions(host.RedisURL, host.RedisOptions)
	if err != nil {
		return nil, "", err
	}
	store, err := storeFromClient(ctx, client, namespace, host.Profile.IsolateProcessNamespace)
	if err != nil {
		return nil, "", err
	}
	if err := validateRedisStoreComposition(store, host.Profile); err != nil {
		_ = store.Close()
		return nil, "", err
	}
	if err := store.SetMaxMemoryBytes(maxBytes); err != nil {
		_ = store.Close()
		return nil, "", err
	}
	hostedLog.PrintfContext(ctx, "operational backend=redis")
	return store, revocations, nil
}

// NewOperationalDiagnosticsFromEnv never creates a fresh memory instance:
// process-local live state can only be inspected inside its owning process.
func NewOperationalDiagnosticsFromEnv(ctx context.Context) (OperationalEnvironment, operational.Store, error) {
	host, err := loadHostPolicyFromEnv()
	if err != nil {
		return OperationalEnvironment{}, nil, err
	}
	settings := operationalEnvironment(host)
	if settings.Backend == "memory" {
		return settings, nil, nil
	}
	if settings.Backend == "postgres" {
		config, err := PostgresOperationalConfigFromEnv()
		if err != nil {
			return settings, nil, err
		}
		store, err := postgres.Open(ctx, host.OperationalPostgresURL, host.OperationalNamespace, config)
		if err != nil {
			return settings, nil, err
		}
		return settings, store, nil
	}
	store, _, err := newRedisOperationalStore(ctx, host)
	if err != nil {
		return settings, nil, err
	}
	return settings, store, nil
}

func validateStandaloneOperational(host *resolvedHostPolicy) error {
	if host.OperationalBackend == "memory" || !host.Profile.SupportsCollection ||
		host.Profile.RedisSession == HostRedisSerialized {
		return fmt.Errorf("host profile %q is not supported by standalone collection roles; process-local admission, collection, and backfill must share one instance", host.Profile.Name)
	}
	return nil
}

func storeFromClient(ctx context.Context, client *redisx.Client, namespaceValue string, isolateSession bool) (*redisx.Store, error) {
	namespace, err := redisx.NormalizeNamespace(firstNonempty(namespaceValue, "hosted-dashboard"))
	if err != nil {
		return nil, err
	}
	var store *redisx.Store
	if isolateSession {
		store, err = redisx.NewProcessIsolatedStore(client, namespace)
		if err != nil {
			return nil, err
		}
	} else {
		store = redisx.NewStore(client, namespace)
	}
	if err := store.Ping(ctx); err != nil {
		_ = store.Close()
		return nil, errors.New("redis is unavailable")
	}
	return store, nil
}

func durableRevocationKeyPrefix(isolateProcess bool, namespace string) (string, error) {
	if !isolateProcess {
		return "", nil
	}
	normalized, err := redisx.NormalizeNamespace(firstNonempty(strings.TrimSpace(namespace), "hosted-dashboard"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(normalized))
	// Preserve queued revocations across the original Upstash profile migration.
	return "upstash-" + hex.EncodeToString(sum[:12]) + "-durable:", nil
}

// RedisProviderDiagnostics contains provider-only probes. Operational consumers
// receive capabilities and Health instead, never the Redis client or raw keys.
type RedisProviderDiagnostics struct{ store *redisx.Store }

func NewRedisProviderDiagnostics(store *redisx.Store) *RedisProviderDiagnostics {
	return &RedisProviderDiagnostics{store: store}
}

func (d *RedisProviderDiagnostics) Ping(ctx context.Context) error { return d.store.Ping(ctx) }
func (d *RedisProviderDiagnostics) MaxMemoryBytes() int64          { return d.store.MaxMemoryBytes() }
func (d *RedisProviderDiagnostics) EffectiveMaxMemoryBytes(maximum int64) (int64, error) {
	return d.store.EffectiveMaxMemoryBytes(maximum)
}

func (d *RedisProviderDiagnostics) Info(ctx context.Context, section string) (string, error) {
	switch section {
	case "server", "memory", "stats", "persistence", "clients":
	default:
		return "", errors.New("unsupported Redis diagnostic section")
	}
	value, err := d.store.Client.Do(ctx, "INFO", section)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", errors.New("INFO returned no data")
	}
	return fmt.Sprint(value), nil
}

func (d *RedisProviderDiagnostics) NamespaceStats(ctx context.Context, namespace string, limit int) (int, bool, []string, error) {
	keys, complete, err := d.scan(ctx, namespace+":*", limit)
	if err != nil {
		return 0, false, nil, err
	}
	foreign, _, foreignErr := d.scan(ctx, "*", 2000)
	others := map[string]bool{}
	if foreignErr == nil {
		for _, key := range foreign {
			if strings.HasPrefix(key, namespace+":") {
				continue
			}
			if prefix, _, found := strings.Cut(key, ":"); found {
				others[prefix] = true
			}
		}
	}
	names := make([]string, 0, len(others))
	for name := range others {
		names = append(names, name)
	}
	sort.Strings(names)
	return len(keys), complete, names, nil
}

func (d *RedisProviderDiagnostics) scan(ctx context.Context, pattern string, limit int) ([]string, bool, error) {
	if limit <= 0 || limit > 20000 {
		return nil, false, errors.New("invalid Redis diagnostic sample limit")
	}
	cursor := "0"
	var keys []string
	for {
		value, err := d.store.Client.Do(ctx, "SCAN", cursor, "MATCH", pattern, "COUNT", "500")
		if err != nil {
			return nil, false, err
		}
		items, ok := value.([]any)
		if !ok || len(items) != 2 {
			return nil, false, errors.New("unexpected SCAN reply")
		}
		cursor = fmt.Sprint(items[0])
		batch, ok := items[1].([]any)
		if !ok {
			return nil, false, errors.New("unexpected SCAN key list")
		}
		for _, entry := range batch {
			if entry != nil {
				keys = append(keys, fmt.Sprint(entry))
				if len(keys) >= limit {
					return keys, false, nil
				}
			}
		}
		if cursor == "0" || cursor == "" {
			return keys, true, nil
		}
	}
}
