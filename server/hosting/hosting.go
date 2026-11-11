// Package hosting exposes CAO's hosted HTTP service to an externally owned
// listener without exposing its Redis, authentication, or projection internals.
package hosting

import (
	"context"
	"log"
	"net/http"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/server"
)

var hostingLog = logger.New("cao:hosting")

// Config optionally locates a disk-backed dashboard and query definitions.
// Empty paths select the resources embedded in the executable.
// Host policy and credentials are loaded from CAO's reviewed policy and
// deployment environment, never supplied by the embedding HTTP host.
type Config struct {
	SiteDirectory        string
	DashboardQueriesPath string
	DatabaseQueriesPath  string
	Logger               *log.Logger
}

type Service struct {
	app     *server.App
	handler http.Handler
}

// New requires a hosted policy with target.module=generic and
// target.listener=external. It never opens a socket or weakens CAO's request
// authentication, proxy, CSRF, or rate-limit boundaries.
func New(ctx context.Context, config Config) (*Service, error) {
	app, err := server.NewExternallyHostedAppFromEnv(
		ctx, config.SiteDirectory, config.DashboardQueriesPath, config.DatabaseQueriesPath, config.Logger,
	)
	if err != nil {
		hostingLog.Printf("hosting app construction failed")
		return nil, err
	}
	return &Service{app: app, handler: app.Handler()}, nil
}

// Start loads the authoritative projection and starts CAO background tasks.
// Keep ctx alive until after the HTTP host has drained active requests.
func (s *Service) Start(ctx context.Context) error { return s.app.Start(ctx) }

// Handler returns the complete CAO handler, including its security boundary.
// Before Start and after Drain it returns 503 for every request.
func (s *Service) Handler() http.Handler { return s.handler }

// Drain stops admitting requests and ends SSE streams. Call it before the
// embedding host's http.Server.Shutdown, while the startup context is alive.
func (s *Service) Drain() { s.app.Drain() }

// Stop cancels CAO background tasks and waits until they exit before closing
// the hosted PostgreSQL pool. If ctx expires, the pool stays open; retry Stop
// after tasks finish. Drain the HTTP host before calling Stop.
func (s *Service) Stop(ctx context.Context) error { return s.app.Stop(ctx) }
