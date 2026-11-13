package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
)

func newOperationalStore(ctx context.Context, host *resolvedHostPolicy) (operational.Store, error) {
	backend := firstNonempty(host.OperationalBackend, "postgres")
	if backend == "memory" {
		if !host.Profile.SingleProcess || !host.Profile.SingleReplica ||
			!host.SingleReplicaConfirmed || !host.AllowVolatile ||
			host.Profile.Authentication != HostAuthenticationOAuth ||
			!host.Profile.RequiresHTTPS || host.Profile.Listener != HostListenerProcess {
			return nil, errors.New("memory operational-store requires OAuth HTTPS, one process listener, one replica, and allow-volatile")
		}
		config, err := memoryConfigFromEnv()
		if err != nil {
			return nil, err
		}
		store, err := memory.New(config)
		if err != nil {
			return nil, err
		}
		hostedLog.PrintfContext(ctx, "operational backend=memory scope=process persistence=volatile sessions=restart-invalidated revocations=best-effort accepted-work=lost-on-restart")
		return store, nil
	}
	if backend == "postgres" {
		config, err := PostgresOperationalConfigFromEnv()
		if err != nil {
			return nil, err
		}
		store, err := postgres.Open(ctx, host.OperationalPostgresURL, host.OperationalNamespace, config)
		if err != nil {
			return nil, fmt.Errorf("open provisioned operational storage (run database bootstrap as owner before rollout): %w", err)
		}
		hostedLog.PrintfContext(ctx, "operational backend=postgres scope=deployment persistence=restart")
		return store, nil
	}
	return nil, fmt.Errorf("unsupported operational-store backend %q", backend)
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
	store, err := newOperationalStore(ctx, host)
	return settings, store, err
}

func validateStandaloneOperational(host *resolvedHostPolicy) error {
	if host.OperationalBackend == "memory" || !host.Profile.SupportsCollection {
		return fmt.Errorf("host profile %q is not supported by standalone collection roles; process-local admission, collection, and backfill must share one instance", host.Profile.Name)
	}
	return nil
}
