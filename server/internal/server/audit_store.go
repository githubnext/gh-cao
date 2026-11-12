package server

import (
	"context"
	"errors"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// OpenAuditReader opens existing audit state for ListAudit and VerifyAudit.
// PostgreSQL uses the configured evidence role without initialization or maintenance.
func OpenAuditReader(ctx context.Context) (operational.AuditLog, func() error, error) {
	return OpenAuditStore(ctx)
}

// OpenAuditStore opens the tamper-evident audit table for offline
// verification and export from the configured operational store.
func OpenAuditStore(ctx context.Context) (operational.AuditLog, func() error, error) {
	_, store, err := NewOperationalDiagnosticsFromEnv(ctx)
	if err != nil {
		return nil, nil, err
	}
	if store == nil {
		return nil, nil, errors.New("the audit table requires the postgres operational store")
	}
	auditStore := store.OperationalServices().Audit
	if auditStore == nil {
		_ = store.Close()
		return nil, nil, errors.New("the audit table requires the postgres operational store")
	}
	return auditStore, store.Close, nil
}
