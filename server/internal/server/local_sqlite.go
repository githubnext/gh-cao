package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/sqlitex"
)

// NewLocalSQLiteApp owns a read-only snapshot and bounded process-local request
// services, without local refresh or hosted collection.
func NewLocalSQLiteApp(ctx context.Context, path string, config Config) (*App, error) {
	return newLocalSQLiteApp(ctx, path, "", 0, config)
}

// NewLocalSQLiteOverlayApp keeps the deployed snapshot immutable and optionally
// refreshes a separate local delta with the operator's GitHub credentials.
func NewLocalSQLiteOverlayApp(ctx context.Context, path, overlay string, interval time.Duration, config Config) (*App, error) {
	if config.Canvas != nil {
		return nil, errors.New("canvas integration requires snapshot-only SQLite serving")
	}
	if strings.TrimSpace(overlay) == "" {
		return nil, errors.New("local SQLite overlay path is required")
	}
	if interval < 0 {
		return nil, errors.New("local backfill interval cannot be negative")
	}
	return newLocalSQLiteApp(ctx, path, overlay, interval, config)
}

func newLocalSQLiteApp(ctx context.Context, path, overlay string, interval time.Duration, config Config) (*App, error) {
	if config.Database != nil {
		return nil, errors.New("local SQLite cannot replace a configured database")
	}
	if err := validateLocalSQLiteConfig(config); err != nil {
		return nil, err
	}
	if err := validateLocalCanvas(config, true); err != nil {
		return nil, err
	}
	var metadata map[string]model.Metadata
	if config.Canvas != nil && config.Canvas.MemoryDirectory != "" {
		var err error
		metadata, err = ingest.ReadInventoryMetadata(ctx, filepath.Join(config.Canvas.MemoryDirectory, "inventory-sources.json"))
		if err != nil {
			return nil, errors.New("read published canvas inventory quality")
		}
	}
	var database *sqlitex.Store
	var err error
	if overlay == "" {
		database, err = sqlitex.OpenWithMetadata(ctx, path, metadata)
	} else {
		database, err = sqlitex.OpenOverlay(ctx, path, overlay, metadata)
	}
	if err != nil {
		return nil, err
	}
	services, err := memory.New(memory.Config{})
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	store := localSQLiteOperationalStore{Store: services}
	config.Database = database
	if config.RateLimits.Query.Capacity == 0 {
		config.RateLimits.Query.Capacity = 300
	}
	app, err := New(ctx, store, config)
	if err != nil {
		_ = store.Close()
		_ = database.Close()
		return nil, err
	}
	app.ownedOperational = store
	app.ownedDatabase = database
	if interval > 0 {
		path, err = filepath.Abs(path)
		if err != nil {
			_ = database.Close()
			_ = store.Close()
			return nil, err
		}
		app.localBackfill = &localSQLiteBackfill{
			database: database, snapshot: path, overlay: overlay, interval: interval,
			catalog: filepath.Clean(filepath.Join(filepath.Dir(config.DatabaseQueriesPath), "../../../../..")),
		}
		if app.sessionArchive != nil {
			app.localBackfill.traces = app.sessionArchive
		}
	}
	return app, nil
}

func validateLocalSQLiteConfig(config Config) error {
	if config.HostProfile != (HostProfile{}) && config.HostProfile != localHostProfile() ||
		config.GitHubOAuth != nil || config.Collector != nil || config.Reconciler != nil ||
		config.SourceDirectory != "" || config.WebhookSecret != "" {
		return errors.New("SQLite snapshots support only local bearer serving without ingestion, collection or webhooks")
	}
	return ValidateListen(config.Listen, config.CertFile, config.KeyFile)
}

type localSQLiteOperationalStore struct{ *memory.Store }

func (s localSQLiteOperationalStore) Capabilities() operational.Capabilities {
	capabilities := s.Store.Capabilities()
	capabilities.Sessions = operational.Capability{}
	capabilities.Revocations = operational.Capability{}
	capabilities.Collection = operational.Capability{}
	return capabilities
}

func (s localSQLiteOperationalStore) OperationalServices() operational.OperationalServices {
	services := s.Store.OperationalServices()
	services.Backend = s
	services.Health = s
	services.Sessions, services.SessionInvalidator, services.Revocations = nil, nil, nil
	services.Queue, services.Admission, services.Collection = nil, nil, nil
	return services
}

func (s localSQLiteOperationalStore) Health(ctx context.Context) (operational.Health, error) {
	health, err := s.Store.Health(ctx)
	health.Capabilities = s.Capabilities()
	return health, err
}
