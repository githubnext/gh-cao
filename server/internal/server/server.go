package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/marketplace"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/repositorymemory"
	"github.com/githubnext/gh-cao/server/internal/sqlitex"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var serverLog = logger.New("cao:server")

var localAccessBootstrap = template.Must(template.New("local-access-bootstrap").Parse(
	`<script>localStorage.setItem("cao-dashboard-access-token",{{.}});const u=new URL(location.href);u.searchParams.delete("access_token");history.replaceState(null,"",u.pathname+u.search+u.hash);</script>`,
))

type Config struct {
	Listen                 string
	SiteDirectory          string
	CertFile               string
	KeyFile                string
	AccessToken            string
	Canvas                 *LocalCanvasConfig
	OnListen               func(string) error
	HostProfile            HostProfile
	SingleReplicaConfirmed bool
	AllowVolatile          bool
	Proxy                  ProxyPolicy
	// CORS is the reviewed cross-origin policy; the zero value is
	// same-origin only.
	CORS                 CORSPolicy
	GitHubOAuth          *GitHubOAuthConfig
	DatabaseQueriesPath  string
	Database             dashboardstore.Store
	DashboardQueries     []query.Definition
	DashboardQueriesPath string
	AgentCatalogPath     string
	MCPContractPath      string
	MCPEnabled           bool
	BuildRevision        string
	GitHubActionsToken   string
	GitHubActionsActor   string
	ActionsRepository    string
	GitHubAPIURL         string
	ActionsHTTPClient    *http.Client
	SourceDirectory      string
	Reconciler           Reconciler
	Collector            *CollectorConfig
	WebhookSecret        string
	AdminUsers           []string
	Logger               *log.Logger
	// Audit records security audit events for administrator and hosted CLI
	// operations; nil disables them (local mode and tests).
	Audit     *audit.Logger
	Admission *AdmissionConfig
	ADX       *adxingest.Config
	// RateLimits overrides inbound request rate limits; the zero value keeps
	// the production defaults.
	RateLimits RateLimitConfig
	QueryCache QueryCacheConfig
	// RedisMaxBytes bounds cache pressure against whole-node memory usage.
	// Zero selects CAO_REDIS_MAX_BYTES or the 200 MB default.
	RedisMaxBytes int64
}

type App struct {
	services         operational.OperationalServices
	ownedOperational operational.Store
	database         dashboardstore.Store
	ownedDatabase    dashboardstore.Store
	localBackfill    *localSQLiteBackfill
	closeDatabase    sync.Once
	closeError       error
	databaseQueries  []query.Definition
	config           Config
	accessToken      string
	oauth            *githubOAuth
	hub              *eventHub
	canonical        canonicalService
	reconciler       Reconciler
	memory           *repositorymemory.RemoteResolver
	webhookSecret    []byte
	webhookMetrics   webhookMetrics
	mcp              http.Handler
	actionsToken     string
	actionsActor     string
	quota            *githubquota.Service
	logs             *logger.Buffer
	startMu          sync.Mutex
	startContext     context.Context
	stop             context.CancelFunc
	draining         bool
	drain            chan struct{}
	taskMu           sync.Mutex
	taskCount        int
	tasksDone        chan struct{}
	cacheOnce        sync.Once
	cacheMetrics     *queryCacheTelemetry
	cacheError       error
	githubResources  githubResourceCache
	hostedCLI        *hostedCLI
	valuePublisher   operationalValuePublisher
	admission        *admission.Service
	adx              *adxingest.Worker
}

func New(ctx context.Context, store operational.Store, config Config) (*App, error) {
	if err := operational.CheckStore(store); err != nil {
		return nil, fmt.Errorf("operational storage is required: %w", err)
	}
	postgresDatabase, isPostgres := config.Database.(*postgresx.Store)
	sqliteDatabase, localSQLite := config.Database.(*sqlitex.Store)
	if err := validateLocalCanvas(config, localSQLite); err != nil {
		return nil, err
	}
	if config.Database == nil || isPostgres && postgresDatabase == nil || localSQLite && sqliteDatabase == nil {
		return nil, errors.New("dashboard database is required")
	}
	adxConfig, err := resolveADXConfig(config)
	if err != nil {
		return nil, err
	}
	config.ADX = &adxConfig
	if localSQLite {
		if err := validateLocalSQLiteConfig(config); err != nil {
			return nil, err
		}
		config.QueryCache.Disabled = true
	}
	if !isPostgres && (config.Collector != nil || config.SourceDirectory != "") {
		return nil, errors.New("server ingestion and collection require PostgreSQL; local SQLite serves an existing snapshot")
	}
	var databaseQueries []query.Definition
	if config.DatabaseQueriesPath != "" {
		content, err := os.ReadFile(config.DatabaseQueriesPath)
		if err != nil {
			return nil, fmt.Errorf("read database queries: %w", err)
		}
		parsed, err := query.ParseDefinitions(content)
		if err != nil {
			return nil, err
		}
		databaseQueries = append(databaseQueries, parsed...)
	}
	if err := validateHostProfile(store, &config); err != nil {
		return nil, err
	}
	services := store.OperationalServices()
	capabilities := store.Capabilities()
	requirements := operational.Requirements{
		SingleProcess: localSQLite || (config.HostProfile.SingleProcess && config.SingleReplicaConfirmed) || config.HostProfile.IsolateProcessNamespace,
		AllowVolatile: config.AllowVolatile || config.HostProfile.IsolateProcessNamespace,
		OAuth:         config.HostProfile.Authentication == HostAuthenticationOAuth,
		Collection:    config.Collector != nil,
	}
	if err := operational.ValidateOperationalServices(capabilities, services, requirements); err != nil {
		return nil, fmt.Errorf("operational services: %w", err)
	}
	if services.Audit != nil && !config.Audit.Durable() {
		config.Audit = config.Audit.WithStore(services.Audit)
	}
	if capabilities.Collection.Persistence == operational.PersistenceVolatile &&
		capabilities.Collection.Scope != operational.ScopeUnsupported {
		if !config.AllowVolatile || !config.SingleReplicaConfirmed ||
			config.HostProfile.Listener != HostListenerProcess ||
			config.HostProfile.Authentication != HostAuthenticationOAuth {
			return nil, errors.New("volatile collection requires OAuth, one process listener, a confirmed single replica, and explicit restart-loss acknowledgement")
		}
		if config.Collector != nil && config.Collector.AdmitOnly {
			return nil, errors.New("volatile collection cannot use an admission-only process")
		}
	}
	if err := config.RateLimits.validate(); err != nil {
		return nil, err
	}
	cache, err := queryCacheConfigFromEnv(config.QueryCache)
	if err != nil {
		return nil, err
	}
	config.QueryCache = cache
	if err := configureOperationalStore(ctx, store, &config); err != nil {
		return nil, err
	}
	serverLog.Printf("configured redis_max_bytes=%d maintenance_interval_ms=%d",
		config.RedisMaxBytes, redisMaintenanceInterval.Milliseconds())
	queryCacheLog.Printf("configured enabled=%t ttl_ms=%d min_duration_ms=%d max_result_bytes=%d max_bytes=%d max_entries=%d",
		!cache.Disabled, operational.QueryCacheTTL.Milliseconds(), cache.MinDuration.Milliseconds(),
		cache.MaxResultBytes, cache.MaxBytes, operational.QueryCacheMaxEntries)
	cors, err := config.CORS.normalize()
	if err != nil {
		return nil, err
	}
	config.CORS = cors
	profile := config.HostProfile
	serverLog.Printf("initializing host_profile=%s", profile.Name)
	if err := validateHostedMode(store, &config); err != nil {
		return nil, err
	}
	activationAdmission, err := newActivationAdmission(store, config)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(config.SiteDirectory)
	if err != nil || !info.IsDir() {
		return nil, errors.New("site directory must exist and contain the built dashboard")
	}
	if profile.Authentication == HostAuthenticationOAuth {
		if err := validateHostedSite(config.SiteDirectory); err != nil {
			return nil, err
		}
	}
	if config.Logger == nil {
		config.Logger = log.Default()
	}
	var oauth *githubOAuth
	var accessToken string
	if profile.Authentication == HostAuthenticationOAuth {
		oauthConfig := *config.GitHubOAuth
		if capabilities.Sessions.Persistence == operational.PersistenceVolatile {
			oauthConfig.loginStateSecret, err = randomToken(32)
			if err != nil {
				return nil, fmt.Errorf("initialize process login state: %w", err)
			}
		}
		oauth = newGitHubOAuth(oauthConfig, services.Sessions, services.SessionInvalidator, services.Revocations)
		if oauthConfig.LocalDevelopmentUser != "" {
			config.Logger.Printf("single-user local OAuth development mode enabled; organization membership authorization is disabled")
		}
	} else {
		accessToken = strings.TrimSpace(config.AccessToken)
		if accessToken == "" {
			generated, err := generateAccessToken()
			if err != nil {
				return nil, fmt.Errorf("generate dashboard access token: %w", err)
			}
			accessToken = generated
		} else if len(accessToken) < 32 {
			return nil, errors.New("dashboard access token must contain at least 32 characters")
		}
	}
	reconciler := config.Reconciler
	var quota *githubquota.Service
	var memoryResolver *repositorymemory.RemoteResolver
	if err := validateProfileExclusivity(config); err != nil {
		return nil, err
	}
	if config.Collector != nil {
		collector, err := NewCollector(ctx, store, postgresDatabase, *config.Collector, config.DatabaseQueriesPath)
		if err != nil {
			return nil, fmt.Errorf("configure collection: %w", err)
		}
		quota = collector.quota
		reconciler = collector
		if !config.Collector.AdmitOnly {
			memoryResolver = &repositorymemory.RemoteResolver{
				Cache:         repositoryMemoryServices{Cache: services.Cache, LeaseStore: services.Leases},
				Installations: collector.enrollment,
				Source:        collector.client,
				Governor: &githubapp.Budget{
					Store: services.RateLimits, Metadata: services.Collection, Floor: config.Collector.RateLimitFloor, Cost: 1,
				},
				ControlRepository: config.Collector.ControlRepository,
			}
		}
	}
	if reconciler == nil && config.SourceDirectory != "" {
		reconciler = DirectoryReconciler{
			Store: postgresDatabase, SourceDirectory: config.SourceDirectory,
			DatabaseQueriesPath: config.DatabaseQueriesPath,
		}
	}
	if (config.MCPEnabled || os.Getenv("CAO_SERVER_LOGS_ENABLED") == "true") &&
		profile.Authentication == HostAuthenticationOAuth {
		if _, _, err := parseActionsRepository(config.ActionsRepository); err != nil {
			return nil, fmt.Errorf("hosted MCP requires an Actions repository: %w", err)
		}
	}
	actionsToken, actionsActor, err := githubActionsMCPIdentity(config, accessToken)
	if err != nil {
		return nil, err
	}
	if actionsToken != "" {
		if err := verifyGitHubActionsPermissionsAtStartup(config, actionsToken); err != nil {
			return nil, err
		}
	}
	if store != nil && quota == nil {
		quota, err = githubquota.New(services.GitHubQuota, githubquota.Options{})
		if err != nil {
			return nil, fmt.Errorf("configure github quota: %w", err)
		}
	}
	serverLog.Printf("initialized host_profile=%s oauth=%t source_ingestion=%t", profile.Name, oauth != nil, config.SourceDirectory != "")
	app := &App{
		admission: activationAdmission,
		services:  services,
		database:  config.Database, databaseQueries: databaseQueries, config: config, accessToken: accessToken, oauth: oauth, hub: newEventHub(),
		canonical: canonicalService{store: config.Database, definitions: databaseQueries}, reconciler: reconciler, memory: memoryResolver,
		webhookSecret: []byte(config.WebhookSecret), actionsToken: actionsToken, actionsActor: actionsActor,
		quota: quota, drain: make(chan struct{}),
	}
	if isPostgres {
		app.valuePublisher = postgresDatabase
	}
	if resources, ok := config.Database.(githubResourceStore); ok {
		app.githubResources.store = resources
	}
	if os.Getenv("CAO_SERVER_LOGS_ENABLED") == "true" {
		app.logs = logger.EnableBuffer()
	}
	if config.MCPEnabled {
		handler, err := app.newMCPHandler()
		if err != nil {
			return nil, fmt.Errorf("configure MCP: %w", err)
		}
		app.mcp = handler
	}
	if config.ADX.Enabled {
		worker, err := newADXWorker(config, services, capabilities)
		if err != nil {
			return nil, err
		}
		app.adx = worker
	}
	return app, nil
}

//nolint:contextcheck // Startup validation has no request context.
func verifyGitHubActionsPermissionsAtStartup(config Config, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return verifyGitHubActionsPermissions(ctx, config, token)
}

// Start initializes the projection and background tasks without taking
// ownership of an HTTP listener. The caller must keep ctx alive while serving
// and call Drain before shutting down its HTTP server, then Stop after HTTP
// requests have drained.
func (a *App) Start(ctx context.Context) error {
	return a.start(ctx, ctx)
}

func (a *App) start(startupCtx, runtimeCtx context.Context) error {
	if a.config.HostProfile.Listener == HostListenerPlatform {
		return fmt.Errorf("host profile %q delegates startup to the platform", a.config.HostProfile.Name)
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	if a.startContext != nil {
		return errors.New("dashboard service has already started")
	}
	if a.draining {
		return errors.New("dashboard service has already stopped")
	}
	if err := startupCtx.Err(); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(runtimeCtx)
	if err := a.initializeRedisMaintenance(startupCtx); err != nil {
		cancel()
		return err
	}
	serverLog.Printf("starting service initial_ingestion=%t", a.config.SourceDirectory != "")
	if a.config.SourceDirectory != "" {
		database, ok := a.database.(*postgresx.Store)
		if !ok {
			cancel()
			return errors.New("initial ingestion requires PostgreSQL")
		}
		result, err := ingest.Run(startupCtx, database, a.config.SourceDirectory, ingest.Options{DatabaseQueriesPath: a.config.DatabaseQueriesPath})
		if err != nil {
			cancel()
			return fmt.Errorf("initial ingestion failed: %w", err)
		}
		a.hub.Broadcast(result.Revision)
		a.config.Logger.Printf("activated dashboard revision %d", result.Revision)
	}
	if collector := a.Collector(); collector != nil {
		if err := collector.start(startupCtx, runCtx, a.hub.Broadcast, a.startTask); err != nil {
			a.cancelAndWaitTasks(cancel, startupCtx)
			return fmt.Errorf("start collection: %w", err)
		}
		serverLog.Printf("collection profile started workers=%d", a.config.Collector.Workers)
	}
	if err := startupCtx.Err(); err != nil {
		a.cancelAndWaitTasks(cancel, startupCtx)
		return err
	}
	if err := runCtx.Err(); err != nil {
		a.cancelAndWaitTasks(cancel, startupCtx)
		return err
	}
	a.startContext = runCtx
	a.stop = cancel
	if a.localBackfill != nil {
		a.startTask(func() { a.localBackfill.run(runCtx, a.config.Logger, a.hub.Broadcast) })
	}
	if a.adx != nil {
		a.startTask(func() { a.adx.Run(runCtx) })
	}
	if a.services.Backend != nil {
		a.startTask(func() { a.runRedisMaintenance(runCtx, redisMaintenanceInterval) })
	}
	if a.oauth != nil && (a.config.SourceDirectory != "" || a.Collector() != nil) {
		a.startTask(func() { a.oauth.runRevocationWorker(runCtx) })
	}
	return nil
}

// Drain stops admitting requests and ends active SSE streams. The host then
// calls http.Server.Shutdown to wait for ordinary HTTP requests before Stop.
func (a *App) Drain() {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.drainLocked()
}

func (a *App) drainLocked() {
	if !a.draining {
		a.draining = true
		if a.drain == nil {
			a.drain = make(chan struct{})
		}
		close(a.drain)
	}
}

// startTask is called under startMu during startup.
func (a *App) startTask(work func()) {
	a.taskMu.Lock()
	if a.taskCount == 0 {
		a.tasksDone = make(chan struct{})
	}
	a.taskCount++
	a.taskMu.Unlock()
	go func() {
		defer a.finishTask()
		work()
	}()
}

func (a *App) finishTask() {
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	a.taskCount--
	if a.taskCount == 0 {
		close(a.tasksDone)
	}
}

// launchTask tracks work admitted by an HTTP request while the service is
// running. Direct handler users without a started lifecycle retain their
// existing detached-operation behavior.
func (a *App) launchTask(work func()) bool {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	if a.draining || (a.startContext != nil && a.startContext.Err() != nil) {
		return false
	}
	if a.startContext == nil {
		go work()
	} else {
		a.startTask(work)
	}
	return true
}

func (a *App) operationContext(requestCtx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(requestCtx), projectionTimeout)
	a.startMu.Lock()
	runCtx := a.startContext
	a.startMu.Unlock()
	if runCtx == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(runCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// Stop cancels CAO-owned background tasks and waits until they exit or ctx is
// canceled. The external host must call Drain and wait for HTTP shutdown first.
func (a *App) Stop(ctx context.Context) error {
	a.startMu.Lock()
	a.drainLocked()
	if a.stop != nil {
		a.stop()
	}
	a.startMu.Unlock()
	if a.hub != nil {
		a.hub.shutdown()
	}
	if err := a.waitTasks(ctx); err != nil {
		return err
	}
	if a.hub != nil {
		if err := a.hub.wait(ctx); err != nil {
			return err
		}
	}
	if a.ownedDatabase != nil || a.ownedOperational != nil {
		a.closeDatabase.Do(func() {
			if a.ownedDatabase != nil {
				a.closeError = a.ownedDatabase.Close()
			}
			if a.ownedOperational != nil {
				a.closeError = errors.Join(a.closeError, a.ownedOperational.Close())
			}
		})
		return a.closeError
	}
	return nil
}

func (a *App) waitTasks(ctx context.Context) error {
	a.taskMu.Lock()
	if a.taskCount == 0 {
		a.taskMu.Unlock()
		return nil
	}
	done := a.tasksDone
	a.taskMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) cancelAndWaitTasks(cancel context.CancelFunc, parent context.Context) {
	cancel()
	ctx, waitCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer waitCancel()
	if err := a.waitTasks(ctx); err != nil {
		serverLog.Printf("background startup cleanup failed")
	}
}

func (a *App) requireStarted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		a.startMu.Lock()
		ctx := a.startContext
		draining := a.draining
		a.startMu.Unlock()
		if draining || ctx == nil || ctx.Err() != nil {
			writeError(response, http.StatusServiceUnavailable, "dashboard service is not running")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (a *App) requireNotDraining(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		a.startMu.Lock()
		draining := a.draining
		a.startMu.Unlock()
		if draining {
			writeError(response, http.StatusServiceUnavailable, "dashboard service is not running")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (a *App) Serve(ctx context.Context) error {
	if a.config.HostProfile.Listener != HostListenerProcess {
		return fmt.Errorf("host profile %q delegates listener ownership", a.config.HostProfile.Name)
	}
	defer func(ctx context.Context) {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := a.Stop(stopCtx); err != nil {
			serverLog.Printf("background shutdown failed")
		}
	}(ctx)
	if err := a.start(ctx, context.WithoutCancel(ctx)); err != nil {
		return err
	}
	serverLog.Printf("starting server tls=%t", a.config.CertFile != "")
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", a.config.Listen)
	if err != nil {
		return err
	}
	serverLog.Printf("listener ready")
	if a.config.OnListen != nil {
		if err := a.config.OnListen(listener.Addr().String()); err != nil {
			_ = listener.Close()
			return err
		}
	}
	servingListener := listener
	if a.config.CertFile != "" {
		certificate, certificateErr := tls.LoadX509KeyPair(a.config.CertFile, a.config.KeyFile)
		if certificateErr != nil {
			_ = listener.Close()
			return fmt.Errorf("load TLS certificate: %w", certificateErr)
		}
		servingListener = tls.NewListener(listener, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		})
	}
	httpServer := &http.Server{
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       65 * time.Second,
		WriteTimeout:      65 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	if a.oauth != nil {
		a.config.Logger.Printf("serving hosted dashboard on %s", a.config.Listen)
	} else if a.config.Canvas == nil {
		a.config.Logger.Printf("serving local dashboard at %s", a.capabilityURL())
	}
	serveDone := make(chan struct{})
	shutdownResult := make(chan error, 1)
	defer close(serveDone)
	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			a.Drain()
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err := httpServer.Shutdown(shutdown)
			if err != nil {
				serverLog.Printf("HTTP shutdown failed")
			}
			shutdownResult <- err
		case <-serveDone:
		}
	}(ctx)
	err = httpServer.Serve(servingListener)
	if errors.Is(err, http.ErrServerClosed) {
		return <-shutdownResult
	}
	return err
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	routePatterns := map[string]struct{}{}
	register := func(pattern string, handler http.HandlerFunc) {
		_, route, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(pattern, func(response http.ResponseWriter, request *http.Request) {
			if pattern != "GET /auth/callback" {
				span := trace.SpanFromContext(request.Context())
				span.SetName(request.Method + " " + route)
				routeAttribute := semconv.HTTPRoute(route)
				span.SetAttributes(routeAttribute)
				labeler, _ := otelhttp.LabelerFromContext(request.Context())
				labeler.Add(routeAttribute)
			}
			handler(response, request)
		})
		routePatterns[pattern] = struct{}{}
	}
	if a.oauth != nil {
		register("GET /auth/login", a.oauth.login)
		register("GET /auth/logged-out", a.oauth.loggedOut)
		register("GET /auth/callback", a.oauth.callback)
		register("POST /auth/logout", a.oauth.logout)
		register("POST /auth/switch-account", a.oauth.switchAccount)
		register("GET /api/auth/session", a.oauth.currentAccount)
	}
	register("GET /api/v1/health", a.health)
	register("GET /api/health", a.health)
	register("GET /api/readiness", a.readiness)
	register("GET /llms.txt", a.llms)
	register("GET /api/v1/events", a.events)
	register("POST /api/v1/query", a.query)
	register("POST /api/v1/value", a.publishValue)
	register("POST /api/admission", a.activationAdmission)
	register("POST /api/conclusion", a.conclusionAdmission)
	if a.mcp != nil {
		register("POST /mcp", a.mcp.ServeHTTP)
	}
	register("GET /api/v1/diagnostics", a.diagnostics)
	register("GET /api/v1/memory/{campaign}", a.repositoryMemoryCampaign)
	register("GET /api/v1/memory/{campaign}/content", a.repositoryMemoryContent)
	register("POST /api/v1/refresh", a.refresh)
	register("GET /api/repositories", a.repositories)
	register("GET /api/repositories/{id}", a.repository)
	register("GET /api/repositories/{id}/runs", a.repositoryRuns)
	register("GET /api/workflows/{id}/runs", a.workflowRuns)
	register("POST /api/github/webhook", a.githubWebhook)
	register("POST /api/admin/rebuild", a.rebuild)
	register("GET /api/admin/rebuild/status", a.rebuildStatus)
	register("GET /api/admin/collection/status", a.collectionStatus)
	if a.logs != nil {
		register("GET /api/admin/logs", a.serverLogs)
	}
	register("GET /api/v1/ingestion/health", a.collectionStatus)
	register("GET /api/v1/ingestion/webhook/status", a.webhookStatus)
	register("GET /api/v1/github-quota/usage", a.gitHubQuotaUsage)
	register("GET /api/v1/github/repos/{owner}/{repo}", a.gitHubRepositoryResource)
	register("GET /api/v1/github/repos/{owner}/{repo}/releases/latest", a.gitHubReleaseResource)
	a.registerHostedCLIRoutes(register)
	mux.HandleFunc("/", a.static)
	handler := a.preAuthRateLimit(a.cors(a.requireAccess(a.rateLimit(mux))))
	switch a.config.HostProfile.Listener {
	case HostListenerExternal:
		handler = a.requireStarted(handler)
	case HostListenerProcess:
		handler = a.requireNotDraining(handler)
	case HostListenerPlatform:
	}
	dashboardHandler := handler
	handler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if admissionPath(request.URL.Path) {
			route := semconv.HTTPRoute(request.URL.Path)
			trace.SpanFromContext(request.Context()).SetAttributes(route)
			labeler, _ := otelhttp.LabelerFromContext(request.Context())
			labeler.Add(route)
			a.serveAdmission(response, request, request.URL.Path == "/api/conclusion")
			return
		}
		dashboardHandler.ServeHTTP(response, request)
	})
	tracedHandler := withResponseTraceHeaders(handler)
	instrumented := otelhttp.NewHandler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		original := request.Context().Value(originalHTTPRequestKey{}).(*http.Request)
		restored := original.WithContext(request.Context())
		restored.Body = request.Body
		tracedHandler.ServeHTTP(response, restored)
	}), telemetry.SpanHTTPServer,
		// Sanitized parents are non-recording contexts, not SDK providers.
		otelhttp.WithTracerProvider(otel.GetTracerProvider()),
		// Traceparent is extracted and sanitized below. Never let a global
		// propagator reintroduce baggage, tracestate or a different parent.
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
		otelhttp.WithFilter(func(request *http.Request) bool {
			// OAuth callbacks use a dedicated, allowlisted server span instead
			// of the generic HTTP instrumentation.
			return request.Method != http.MethodGet || request.URL.Path != "/auth/callback"
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
			// Exact routes are named up front; parameterized routes are
			// named when the mux dispatches to their registered handler.
			// Other paths share one bounded name without a second mux lookup.
			pattern := request.Method + " " + request.URL.Path
			if _, ok := routePatterns[pattern]; ok {
				return pattern
			}
			return request.Method + " /*"
		}),
	)
	safeTelemetry := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		traceparent := request.Header.Get("Traceparent")
		if admissionPath(request.URL.Path) && len(request.Header.Values("Traceparent")) != 1 {
			traceparent = ""
		}
		parentContext := request.Context()
		if admissionPath(request.URL.Path) {
			parentContext = trace.ContextWithSpanContext(parentContext, trace.SpanContext{})
		}
		ctx := telemetry.ExtractTraceparent(parentContext, traceparent)
		if admissionPath(request.URL.Path) {
			parent := trace.SpanContextFromContext(ctx)
			// Use one local sampling choice, not the remote caller's sampled flag.
			ctx = trace.ContextWithRemoteSpanContext(ctx, parent.WithTraceFlags(trace.FlagsSampled))
		}
		safe := request.Clone(context.WithValue(ctx, originalHTTPRequestKey{}, request))
		safe.RemoteAddr = ""
		safe.Host = ""
		safe.RequestURI = ""
		safe.Header = make(http.Header)
		if traceparent != "" && !admissionPath(request.URL.Path) {
			safe.Header.Set("Traceparent", traceparent)
		}
		path := ""
		if _, ok := routePatterns[request.Method+" "+request.URL.Path]; ok {
			path = request.URL.Path
		}
		safe.URL = &url.URL{Path: path}
		switch request.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions:
		default:
			safe.Method = ""
		}
		instrumented.ServeHTTP(response, safe)
	})
	return a.canvasHandler(securityHeaders(safeTelemetry))
}

type originalHTTPRequestKey struct{}

// withResponseTraceHeaders exposes the W3C trace/span ids that otelhttp
// assigned to the in-flight request as response headers, so operators can
// correlate a client-visible request with exported spans without requiring
// the client to send its own traceparent header.
func withResponseTraceHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !admissionPath(request.URL.Path) {
			telemetry.SetResponseTraceHeaders(response, trace.SpanContextFromContext(request.Context()))
		}
		next.ServeHTTP(response, request)
	})
}

func generateAccessToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func (a *App) capabilityURL() string {
	scheme := "http"
	if a.config.CertFile != "" {
		scheme = "https"
	}
	return scheme + "://" + a.config.Listen + "/?access_token=" + a.accessToken
}

func (a *App) requireAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if a.oauth != nil {
			a.logAuthBranch("access.hosted_mode")
			a.requireGitHubAccess(next).ServeHTTP(response, request)
			return
		}
		if !validLocalRequestHost(request.Host) {
			a.logAuthBranch("access.local_host_rejected")
			http.Error(response, "invalid request host", http.StatusMisdirectedRequest)
			return
		}
		if publicServiceEndpoint(request.URL.Path) || request.URL.Path == "/api/github/webhook" {
			a.logAuthBranch("access.local_public_allowed")
			next.ServeHTTP(response, request)
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/api/") && request.URL.Path != "/mcp" {
			if (request.Method == http.MethodGet || request.Method == http.MethodHead) &&
				constantTimeTokenEqual(request.URL.Query().Get("access_token"), a.accessToken) {
				a.logAuthBranch("access.local_capability_accepted")
				a.serveIndex(response, request, a.accessToken)
				return
			}
			a.logAuthBranch("access.local_static_allowed")
			next.ServeHTTP(response, request)
			return
		}
		if a.authorized(request) {
			a.logAuthBranch("access.local_bearer_accepted")
			next.ServeHTTP(response, request)
			return
		}
		if request.URL.Path == "/mcp" || (request.URL.Path == "/api/admin/logs" && a.logs != nil) {
			if actor, ok := a.authorizedGitHubActions(request); ok {
				a.logAuthBranch("access.local_actions_accepted")
				request = request.WithContext(context.WithValue(
					request.Context(), githubActionsActorContextKey{}, actor))
				next.ServeHTTP(response, request)
				return
			}
			a.logAuthBranch("access.local_actions_rejected")
		}
		a.logAuthBranch("access.local_bearer_rejected")
		writeError(response, http.StatusUnauthorized, "dashboard access token is required")
	})
}

func (a *App) logAuthBranch(branch string) {
	if a.oauth != nil {
		a.oauth.emitAuthBranch(branch)
		return
	}
	serverLog.Printf("oauth branch=%s", branch)
}

func validLocalRequestHost(value string) bool {
	host := value
	if parsedHost, _, err := net.SplitHostPort(value); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func (a *App) authorized(request *http.Request) bool {
	token, ok := bearerToken(request)
	if ok && constantTimeTokenEqual(token, a.accessToken) {
		return true
	}
	if request.Method != http.MethodGet || !strings.HasPrefix(request.URL.Path, "/api/v1/github/") {
		return false
	}
	cookie, err := request.Cookie("cao_github_local")
	return err == nil && a.accessToken != "" && constantTimeTokenEqual(cookie.Value, a.githubLocalResourceSession())
}

type githubActionsActorContextKey struct{}

func githubActionsMCPIdentity(config Config, accessToken string) (string, string, error) {
	token := strings.TrimSpace(config.GitHubActionsToken)
	if !config.MCPEnabled || token == "" {
		return "", "", nil
	}
	actor := strings.TrimSpace(config.GitHubActionsActor)
	if len(token) < 32 {
		return "", "", errors.New("GitHub Actions MCP token must contain at least 32 characters")
	}
	if constantTimeTokenEqual(token, accessToken) {
		return "", "", errors.New("GitHub Actions MCP token must differ from the dashboard access token")
	}
	if actor == "" {
		return "", "", errors.New("GitHub Actions MCP actor is required when its token is configured")
	}
	if len(actor) > 100 || strings.IndexFunc(actor, func(value rune) bool {
		return value <= ' ' || value == '\x7f'
	}) >= 0 {
		return "", "", errors.New("GitHub Actions MCP actor is invalid")
	}
	return token, strings.ToLower(actor), nil
}

func (a *App) authorizedGitHubActions(request *http.Request) (string, bool) {
	if a.actionsToken == "" || a.actionsActor == "" {
		return "", false
	}
	token, ok := bearerToken(request)
	actor := strings.ToLower(strings.TrimSpace(request.Header.Get("X-GitHub-Actor")))
	return a.actionsActor, ok &&
		constantTimeTokenEqual(token, a.actionsToken) &&
		constantTimeTokenEqual(actor, a.actionsActor)
}

func bearerToken(request *http.Request) (string, bool) {
	const prefix = "Bearer "
	authorization := request.Header.Get("Authorization")
	return strings.TrimPrefix(authorization, prefix), strings.HasPrefix(authorization, prefix)
}

func (a *App) requireGitHubAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !validProxyRequest(request, a.proxyPolicy()) {
			a.logAuthBranch("access.proxy_rejected")
			http.Error(response, "invalid forwarded request host", http.StatusMisdirectedRequest)
			return
		}
		if !a.oauth.validLocalDevelopmentRequest(request) {
			a.logAuthBranch("access.local_development_boundary_rejected")
			writeError(response, http.StatusForbidden, "Local development OAuth requires a direct loopback HTTPS request")
			return
		}
		if publicServiceEndpoint(request.URL.Path) ||
			request.URL.Path == "/api/github/webhook" ||
			strings.HasPrefix(request.URL.Path, "/auth/login") ||
			strings.HasPrefix(request.URL.Path, "/auth/logged-out") ||
			strings.HasPrefix(request.URL.Path, "/auth/callback") {
			a.logAuthBranch("access.public_allowed")
			next.ServeHTTP(response, request)
			return
		}
		_, sessionCookieErr := request.Cookie(sessionCookieName)
		if (request.URL.Path == "/mcp" && a.mcp != nil &&
			(request.Header.Get("Authorization") != "" || sessionCookieErr != nil)) ||
			(request.URL.Path == "/api/admin/logs" && a.logs != nil && request.Header.Get("Authorization") != "") {
			ctx, span := telemetry.Tracer().Start(request.Context(), "cao_dashboard.auth.mcp")
			defer span.End()
			actor, err := verifyHostedActionsMCP(ctx, a.config, request)
			if err != nil {
				a.logAuthBranch("access.hosted_actions_rejected")
				var refusal *hostedMCPRefusal
				code := "authentication_failed"
				if errors.As(err, &refusal) {
					code = refusal.code
				}
				result := map[string]string{
					"error": "GitHub Actions MCP authentication is required",
					"code":  code,
				}
				spanContext := span.SpanContext()
				if spanContext.IsValid() {
					telemetry.SetResponseTraceHeaders(response, spanContext)
					result["traceId"] = spanContext.TraceID().String()
				}
				serverLog.Printf("hosted MCP authentication rejected code=%s trace_id=%s", code, result["traceId"])
				writeJSON(response, http.StatusUnauthorized, result)
				return
			}
			a.logAuthBranch("access.hosted_actions_accepted")
			next.ServeHTTP(response, request.WithContext(context.WithValue(
				request.Context(), githubActionsActorContextKey{}, actor)))
			return
		}
		var session oauthSession
		var ok bool
		if request.URL.Path == "/auth/logout" || request.URL.Path == "/auth/switch-account" {
			a.logAuthBranch("access.mutation_session_checked")
			session, ok = a.oauth.loadRequestSession(request)
		} else {
			a.logAuthBranch("access.refreshable_session_checked")
			session, ok = a.oauth.session(response, request)
		}
		if !ok {
			if strings.HasPrefix(request.URL.Path, "/api/") || request.URL.Path == "/auth/logout" || request.URL.Path == "/mcp" {
				a.logAuthBranch("access.unauthorized")
				writeError(response, http.StatusUnauthorized, "GitHub authentication is required")
				return
			}
			if !navigationRequest(request) {
				// Subresource fetches (web app manifest, service worker,
				// scripts) must not follow a redirect to the cross-origin
				// GitHub authorize endpoint, which browsers block by CORS.
				a.logAuthBranch("access.subresource_unauthorized")
				writeError(response, http.StatusUnauthorized, "GitHub authentication is required")
				return
			}
			a.logAuthBranch("access.login_redirected")
			http.Redirect(response, request, "/auth/login", http.StatusFound)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			if !constantTimeTokenEqual(request.Header.Get("X-CSRF-Token"), session.CSRFToken) {
				a.logAuthBranch("access.csrf_rejected")
				writeError(response, http.StatusForbidden, "CSRF token is required")
				return
			}
			a.logAuthBranch("access.csrf_accepted")
		} else {
			a.logAuthBranch("access.safe_method")
		}
		a.logAuthBranch("access.authorized")
		request = request.WithContext(context.WithValue(request.Context(), oauthSessionContextKey{}, session))
		if request.URL.Path == "/mcp" && a.mcp != nil && !a.adminAuthorized(request) {
			a.writeAdminDenied(response, request, "administrator access is required")
			return
		}
		next.ServeHTTP(response, request)
	})
}

// navigationRequest reports whether a request may enter the GitHub login
// flow. Browsers identify subresources and embedded navigations with Fetch
// Metadata; clients that omit these headers keep the existing behavior.
func navigationRequest(request *http.Request) bool {
	mode := request.Header.Get("Sec-Fetch-Mode")
	destination := request.Header.Get("Sec-Fetch-Dest")
	return (mode == "" || mode == "navigate") &&
		(destination == "" || destination == "document")
}

func (a *App) proxyPolicy() ProxyPolicy {
	return a.config.Proxy
}

type oauthSessionContextKey struct{}

func (a *App) adminAuthorized(request *http.Request) bool {
	return a.adminAuthorizedContext(request.Context())
}

func (a *App) adminAuthorizedContext(ctx context.Context) bool {
	if a.oauth == nil {
		a.logAuthBranch("admin.local_mode_allowed")
		return true
	}
	session, ok := ctx.Value(oauthSessionContextKey{}).(oauthSession)
	if !ok {
		a.logAuthBranch("admin.session_missing")
		return false
	}
	if !a.adminListed(session) {
		a.logAuthBranch("admin.denied")
		return false
	}
	if !a.oauth.adminAssured(session, time.Now().UTC()) {
		a.logAuthBranch("admin.step_up_required")
		return false
	}
	a.logAuthBranch("admin.allowed")
	return true
}

func (a *App) adminListed(session oauthSession) bool {
	for _, login := range a.config.AdminUsers {
		if strings.EqualFold(strings.TrimSpace(login), session.Login) {
			return true
		}
	}
	return false
}

// writeAdminDenied distinguishes a configured administrator whose step-up
// lapsed (401, re-authenticate) from a non-administrator (403).
func (a *App) writeAdminDenied(response http.ResponseWriter, request *http.Request, message string) {
	if a.oauth != nil {
		session, ok := request.Context().Value(oauthSessionContextKey{}).(oauthSession)
		reason := "not_admin"
		if ok && a.adminListed(session) {
			reason = "step_up_required"
		}
		if err := a.config.Audit.Record(request.Context(), "admin.denied", audit.Denied,
			slog.String("actor", session.Login), slog.String("reason", reason),
			slog.String("method", request.Method), slog.String("path", request.URL.Path)); err != nil {
			serverLog.Printf("administrator denial audit unavailable")
			if errors.Is(err, audit.ErrUnavailable) || !a.config.Audit.Durable() {
				writeError(response, http.StatusServiceUnavailable, "administrator denial audit is unavailable")
				return
			}
		}
		if reason == "step_up_required" {
			maxAge := int(a.oauth.adminReauthenticationMaxAge() / time.Second)
			response.Header().Set("WWW-Authenticate", fmt.Sprintf(`CAO-Step-Up realm="admin", max_age=%d`, maxAge))
			writeErrorPayload(response, http.StatusUnauthorized, map[string]string{
				"error":          "Sign in again with two-factor authentication or single sign-on to perform administrator actions.",
				"code":           "admin_step_up_required",
				"reauthenticate": "/auth/login",
			})
			return
		}
	}
	writeError(response, http.StatusForbidden, message)
}

func constantTimeTokenEqual(candidate, expected string) bool {
	return len(candidate) == len(expected) &&
		subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		if request.Context().Value(localCanvasContextKey{}) != true {
			response.Header().Set("X-Frame-Options", "DENY")
		}
		response.Header().Set("Referrer-Policy", "same-origin")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		if request.Context().Value(localCanvasContextKey{}) == true {
			response.Header().Set("Cache-Control", "no-store")
		}
		if strings.HasPrefix(request.URL.Path, "/api/") {
			response.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(response, request)
	})
}

func (a *App) health(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	active, activeErr := a.database.State(ctx)
	redisHealthy := a.services.Health.Ping(ctx) == nil
	status := http.StatusOK
	if !redisHealthy || activeErr != nil {
		status = http.StatusServiceUnavailable
	}
	serverLog.Printf("health status=%d redis=%t data=%t", status, redisHealthy, active.Ready)
	data := map[string]any{"available": active.Ready, "rebuildRequired": !active.Ready}
	if active.Ready && !active.EvaluatedAt.IsZero() {
		data["evaluatedAt"] = active.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	}
	payload := map[string]any{
		"status":      "healthy",
		"redis":       map[string]any{"connected": redisHealthy},
		"operational": a.operationalHealth(redisHealthy),
		"data":        data,
	}
	if status != http.StatusOK {
		payload["status"] = "unhealthy"
	}
	detailsAuthorized := a.authorized(request) || (a.oauth != nil && a.oauth.requestHasSession(request))
	if detailsAuthorized {
		if a.oauth != nil {
			a.logAuthBranch("health.details_authorized")
		}
		rowCount := 0
		for _, count := range active.Counts {
			rowCount += count
		}
		payload["revision"] = active.Revision
		payload["counts"] = active.Counts
		payload["sourceCount"] = len(active.Counts)
		payload["rowCount"] = rowCount
	} else if a.oauth != nil {
		a.logAuthBranch("health.details_redacted")
	}
	writeJSON(response, status, payload)
}

func (a *App) readiness(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	active, activeErr := a.database.State(ctx)
	redisHealthy := a.services.Health.Ping(ctx) == nil
	ready := redisHealthy && activeErr == nil && active.Ready
	if collector := a.Collector(); collector != nil {
		ready = ready && collector.RecoveryReady()
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	data := map[string]any{
		"available":       active.Ready,
		"rebuildRequired": !active.Ready,
	}
	if active.Ready && !active.EvaluatedAt.IsZero() {
		data["evaluatedAt"] = active.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(response, status, map[string]any{
		"ready":       ready,
		"redis":       map[string]any{"connected": redisHealthy},
		"operational": a.operationalHealth(redisHealthy),
		"data":        data,
	})
}

func publicServiceEndpoint(path string) bool {
	return path == "/api/v1/health" || path == "/api/health" || path == "/api/readiness" || path == "/llms.txt"
}

func (a *App) llms(response http.ResponseWriter, request *http.Request) {
	revision := a.config.BuildRevision
	if len(revision) != 40 || strings.IndexFunc(revision, func(char rune) bool {
		return char < '0' || (char > '9' && char < 'a') || char > 'f'
	}) >= 0 {
		revision = "unknown"
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprintf(response, `# Central Agentic Ops dashboard server

Server commit SHA: %s

## Agent documentation

- CAO agent resources: https://githubnext.github.io/gh-cao/agent/llms.txt
- Published dashboard agent guide: https://githubnext.github.io/gh-cao/cao/llms.txt
- Agent analysis and MCP setup: https://githubnext.github.io/gh-cao/agent-analysis/

## Configure MCP

The read-only MCP endpoint is POST /mcp, only when the server is started with --mcp-enabled.
It exposes cao_catalog (discover pages and queries) and cao_query (run a reviewed named query).
When CAO_SERVER_LOGS_ENABLED=true, cao_logs reads the same diagnostic snapshot as GET /api/admin/logs.
Local clients use the dashboard bearer capability in the Authorization header.
Hosted administrators can use their GitHub OAuth session cookie and X-CSRF-Token.
Other hosted clients must run in GitHub Actions on the authorized repository's default branch,
send a repository-scoped GITHUB_TOKEN as a bearer token and an Actions OIDC token
in X-GitHub-OIDC-Token (audience https://cao.githubnext.com).
See the agent analysis guide and server setup documentation:
https://github.com/githubnext/gh-cao/blob/main/server/README.md#local-mcp-endpoint
Never put credentials in a URL or in source control.
`, revision)
}

func (a *App) events(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	allowHealth := a.adminAuthorized(request)
	a.startMu.Lock()
	drain := a.drain
	a.startMu.Unlock()
	var expires time.Time
	session, _ := request.Context().Value(oauthSessionContextKey{}).(oauthSession)
	if a.oauth != nil {
		expires = a.oauth.eventAuthorizationExpires(session, allowHealth)
		ctx, cancel := context.WithDeadline(request.Context(), expires)
		defer cancel()
		request = request.WithContext(ctx)
		go func() {
			select {
			case <-drain:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	valid := func() bool {
		return request.Context().Err() == nil && (expires.IsZero() || time.Now().Before(expires))
	}
	if !valid() {
		return
	}
	if err := http.NewResponseController(response).SetWriteDeadline(expires); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		serverLog.Printf("event stream requires a response writer with deadline control")
		writeError(response, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Connection", "keep-alive")
	channel := a.hub.Subscribe(request.Context(), a, allowHealth)
	if channel == nil {
		return
	}
	defer a.hub.Unsubscribe(channel)
	active, _ := a.database.State(request.Context())
	healthRevision, _ := a.ingestionHealthRevision(request.Context(), allowHealth)
	if !valid() {
		return
	}
	writeEvent(response, active.Revision, healthRevision)
	flusher.Flush()
	lastRevision := active.Revision
	lastHealthRevision := healthRevision
	serverLog.Printf("event stream subscribed")
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-drain:
			serverLog.Printf("event stream drained")
			return
		case observation, ok := <-channel:
			if !ok || !valid() {
				return
			}
			healthRevision := int64(0)
			if allowHealth {
				healthRevision = observation.healthRevision
			}
			if observation.revision != lastRevision || healthRevision != lastHealthRevision {
				writeEvent(response, observation.revision, healthRevision)
				flusher.Flush()
				lastRevision = observation.revision
				lastHealthRevision = healthRevision
			}
		case <-heartbeat.C:
			if a.oauth != nil {
				ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
				latest, err := a.oauth.loadSession(ctx, session.ID)
				cancel()
				if err != nil || !time.Now().Before(a.oauth.eventAuthorizationExpires(latest, allowHealth)) ||
					(allowHealth && !a.adminAuthorizedContext(context.WithValue(request.Context(), oauthSessionContextKey{}, latest))) {
					serverLog.Printf("event stream authorization unavailable")
					return
				}
			}
			if !valid() {
				return
			}
			_, _ = io.WriteString(response, ": keepalive\n\n")
			flusher.Flush()
		case <-request.Context().Done():
			serverLog.Printf("event stream closed")
			return
		}
	}
}

func (a *App) ingestionHealthRevision(ctx context.Context, allowed bool) (int64, error) {
	if !allowed {
		return 0, nil
	}
	_, events, err := a.services.IngestionMetrics.IngestionHealth(ctx)
	if err != nil {
		return 0, err
	}
	revision, err := strconv.ParseInt(events["healthRevision"], 10, 64)
	if err != nil && events["healthRevision"] == "" {
		return 0, nil
	}
	return revision, err
}

func writeEvent(writer io.Writer, revision, healthRevision int64) {
	payload, _ := json.Marshal(map[string]int64{
		"revision": revision, "healthRevision": healthRevision,
	})
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", payload)
}

type queryRequest struct {
	SourceNames     []string                     `json:"sourceNames"`
	Queries         []query.Definition           `json:"queries,omitempty"`
	CompiledQueries []query.Definition           `json:"compiledQueries,omitempty"`
	Aliases         []string                     `json:"aliases,omitempty"`
	ReplacedSources []string                     `json:"replacedSources,omitempty"`
	Pagination      map[string]paginationRequest `json:"pagination,omitempty"`
	PageID          string                       `json:"pageId,omitempty"`
	ViewID          string                       `json:"viewId,omitempty"`
	RouteParameters map[string]any               `json:"routeParameters,omitempty"`
	QueryContext    map[string]any               `json:"queryContext,omitempty"`
	Context         map[string]any               `json:"context,omitempty"`
	EvaluatedAt     string                       `json:"evaluatedAt,omitempty"`
}

type paginationRequest struct {
	Limit             int    `json:"limit"`
	ContinuationToken string `json:"continuationToken,omitempty"`
}

func (a *App) query(response http.ResponseWriter, request *http.Request) {
	ctx, span := telemetry.Tracer().Start(request.Context(), telemetry.SpanQueryExecute)
	defer span.End()
	request = request.WithContext(ctx)
	fail := func(status int, message string) {
		if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, "query execution failed")
		}
		writeError(response, status, message)
	}
	request.Body = http.MaxBytesReader(response, request.Body, 8<<20)
	decoder := json.NewDecoder(request.Body)
	var input queryRequest
	if err := decoder.Decode(&input); err != nil {
		fail(http.StatusBadRequest, "invalid query request")
		return
	}
	if len(input.SourceNames) > 256 || len(input.Aliases) > 256 || len(input.ReplacedSources) > 256 {
		fail(http.StatusBadRequest, "query request exceeds source limits")
		return
	}
	serverLog.Printf(
		"query received sources=%d aliases=%d definitions=%d compiled=%d paginated=%d",
		len(input.SourceNames), len(input.Aliases), len(input.Queries), len(input.CompiledQueries), len(input.Pagination),
	)
	for _, name := range append(append([]string{}, input.SourceNames...), input.Aliases...) {
		if strings.TrimSpace(name) == "" {
			fail(http.StatusBadRequest, "source names must be non-empty")
			return
		}
	}
	result, status, err := a.executeQuery(ctx, input, a.adminAuthorized(request))
	if err != nil {
		writeQueryError(response, status, err)
		return
	}

	result.Metrics.RateLimitCost = queryRateLimitCost(result.Metrics)
	if status, err := a.chargeQueryRateLimit(ctx, response, result.Metrics.RateLimitCost); err != nil {
		fail(status, err.Error())
		return
	}
	span.SetAttributes(queryTelemetryAttributes(input, result)...)
	span.SetStatus(codes.Ok, "")
	writeJSON(response, http.StatusOK, result)
}

func writeQueryError(response http.ResponseWriter, status int, err error) {
	var limitErr *query.PlanLimitError
	if errors.As(err, &limitErr) {
		writeErrorPayload(response, http.StatusUnprocessableEntity, map[string]string{
			"error": limitErr.Error(), "code": "query_plan_too_large", "queryId": limitErr.QueryID, "boundary": limitErr.Boundary,
		})
		return
	}
	writeError(response, status, err.Error())
}

type queryResponse struct {
	Revision       int64                   `json:"revision"`
	HealthRevision int64                   `json:"healthRevision,omitempty"`
	EvaluatedAt    string                  `json:"evaluatedAt"`
	Sources        map[string]model.Source `json:"sources"`
	Metrics        model.Metrics           `json:"metrics"`
}

func (a *App) executeQuery(ctx context.Context, input queryRequest, allowCollectionHealth bool) (queryResponse, int, error) {
	if a.database == nil {
		return queryResponse{}, http.StatusServiceUnavailable, errors.New("dashboard data is unavailable")
	}
	cache, err := a.config.QueryCache.resolve()
	if err != nil {
		return queryResponse{}, http.StatusInternalServerError, err
	}
	cacheEnabled := !cache.Disabled && a.services.Cache != nil && (len(input.SourceNames) > 0 || len(input.Aliases) > 0)
	liveWebhook := false
	if cacheEnabled {
		definitions, requested := a.queryPlan(input)
		if err := query.WalkPlan(definitions, requested, func(name string) error {
			liveWebhook = liveWebhook || name == webhookStatusSourceName
			return nil
		}, func(query.Definition) error { return nil }); err != nil {
			return queryResponse{}, http.StatusBadRequest, err
		}
		cacheEnabled = !liveWebhook
	}
	if !cacheEnabled {
		reason := "readiness"
		switch {
		case liveWebhook:
			reason = "live-webhook"
		case cache.Disabled:
			reason = "disabled"
		case a.services.Cache == nil:
			reason = "no-redis"
		}
		if err := a.recordQueryCacheBypass(ctx, reason); err != nil {
			return queryResponse{}, http.StatusInternalServerError, err
		}
	}
	var cacheKey string
	if cacheEnabled {
		cacheKey, err = a.queryResultCacheIdentity(ctx, input, allowCollectionHealth)
		if err != nil {
			if errors.Is(err, errValueRevisionUnavailable) {
				return queryResponse{}, http.StatusServiceUnavailable, err
			}
			return queryResponse{}, http.StatusBadRequest, err
		}
		cached, hit, err := a.loadCachedQuery(ctx, cacheKey, cache)
		if err != nil {
			return queryCacheError(err)
		}
		if hit {
			return cached, http.StatusOK, nil
		}
	}
	started := time.Now()
	var response queryResponse
	var status int
	err = a.database.WithReadTransaction(ctx, func(ctx context.Context, reader postgresx.NativeReader) error {
		var queryErr error
		response, status, queryErr = a.executeQueryWithReader(ctx, input, allowCollectionHealth, reader)
		return queryErr
	})
	if err != nil && status == 0 {
		status = http.StatusServiceUnavailable
	}
	if err == nil && cacheEnabled && time.Since(started) >= cache.MinDuration {
		if cacheErr := a.storeCachedQuery(ctx, cacheKey, response, cache); cacheErr != nil {
			return queryCacheError(cacheErr)
		}
	} else if err == nil && cacheEnabled {
		if telemetryErr := a.recordQueryCacheBypass(ctx, "cheap-query"); telemetryErr != nil {
			return queryResponse{}, http.StatusInternalServerError, telemetryErr
		}
	}
	return response, status, err
}

func (a *App) executeQueryWithReader(ctx context.Context, input queryRequest, allowCollectionHealth bool, reader postgresx.NativeReader) (queryResponse, int, error) {
	active, err := reader.State(ctx)
	if err != nil || !active.Ready {
		return queryResponse{}, http.StatusServiceUnavailable, errors.New("dashboard data is unavailable")
	}
	evaluatedAt := evaluationTime(active)
	healthRevision := int64(0)
	if allowCollectionHealth {
		_, events, healthErr := a.services.IngestionMetrics.IngestionHealth(ctx)
		if healthErr == nil {
			healthRevision, _ = strconv.ParseInt(events["healthRevision"], 10, 64)
		}
	}
	if len(input.SourceNames) == 0 && len(input.Aliases) == 0 {
		return queryResponse{
			Revision: active.Revision, HealthRevision: healthRevision, EvaluatedAt: evaluatedAt,
			Sources: map[string]model.Source{},
			Metrics: model.Metrics{PushedDown: []string{}, FallbackOperations: []string{}},
		}, http.StatusOK, nil
	}
	definitions, requested := a.queryPlan(input)
	ResolveQueryContext(definitions, evaluatedAt)
	started := time.Now()
	loader := &databaseLoader{
		ctx: ctx, database: reader, operational: a.services.Cache, dataRevision: active.DataRevision,
		app: a, allowCollectionHealth: allowCollectionHealth,
	}
	pages := map[string]postgresx.SQLPage{}
	for name, page := range input.Pagination {
		offset, err := paginationOffset(name, strconv.FormatInt(active.Revision, 10), page)
		if err != nil {
			return queryResponse{}, http.StatusBadRequest, err
		}
		pages[name] = postgresx.SQLPage{Offset: offset, Limit: page.Limit}
	}
	sources, metrics, err := loader.ExecuteSQLPlan(definitions, requested, pages)
	if err != nil {
		serverLog.Printf("query failed")
		return queryResponse{}, http.StatusBadRequest, err
	}
	for name := range input.Pagination {
		source, ok := sources[name]
		if !ok {
			continue
		}
		offset := pages[name].Offset
		total, _ := source.Metadata["total-row-count"].(int)
		end := offset + len(source.Rows)
		if end < total {
			cursor, _ := json.Marshal(map[string]any{"source": name, "revision": strconv.FormatInt(active.Revision, 10), "offset": end})
			source.ContinuationToken = base64.RawURLEncoding.EncodeToString(cursor)
		}
		sources[name] = source
	}
	metrics.DurationMS = time.Since(started).Milliseconds()
	serverLog.Printf("query completed sources=%d duration_ms=%d", len(sources), metrics.DurationMS)
	return queryResponse{
		Revision: active.Revision, HealthRevision: healthRevision,
		EvaluatedAt: evaluatedAt, Sources: sources, Metrics: metrics,
	}, http.StatusOK, nil
}

func (a *App) queryPlan(input queryRequest) ([]query.Definition, []string) {
	definitions := append([]query.Definition{}, a.databaseQueries...)
	definitions = append(definitions, a.config.DashboardQueries...)
	if len(input.Queries) > 0 {
		definitions = append(append([]query.Definition{}, a.databaseQueries...), input.Queries...)
	}
	definitions = append(definitions, input.CompiledQueries...)
	replaced := map[string]bool{}
	for _, name := range input.ReplacedSources {
		replaced[name] = true
	}
	requested := append([]string{}, input.Aliases...)
	for _, name := range input.SourceNames {
		if !replaced[name] {
			requested = append(requested, name)
		}
	}
	return definitions, requested
}

type databaseLoader struct {
	ctx                   context.Context
	database              postgresx.NativeReader
	operational           operational.Cache
	dataRevision          string
	app                   *App
	allowCollectionHealth bool
}

func (loader *databaseLoader) ExecuteSQLPlan(definitions []query.Definition, requested []string, pages map[string]postgresx.SQLPage) (map[string]model.Source, model.Metrics, error) {
	return loader.database.ExecuteSQLPlanWithOptions(loader.ctx, definitions, requested, postgresx.SQLExecutionOptions{
		Pages: pages, Runtime: func(ctx context.Context, name string) (model.Source, error) {
			return loader.runtimeSource(ctx, name, definitions)
		},
	})
}

func (loader *databaseLoader) runtimeSource(ctx context.Context, name string, definitions []query.Definition) (model.Source, error) {
	if name == webhookStatusSourceName {
		if loader.app == nil {
			return model.Source{}, errors.New("webhook-status provider is unavailable")
		}
		return loader.app.webhookStatusSource(loader.allowCollectionHealth), nil
	}
	if name == "cli-commands" || name == "cli-command-summary" || name == "cli-command-statuses" {
		return unavailableSource(name), nil
	}
	if name == simulationDaysSourceName {
		return simulationDaysSource(), nil
	}
	if name == collectionHealthSourceName {
		if loader.app == nil {
			return model.Source{}, errors.New("collection-health provider is unavailable")
		}
		return loader.app.collectionHealthSource(ctx, loader.allowCollectionHealth)
	}

	if name == gitHubQuotaUsageSourceName {
		if loader.app == nil {
			return model.Source{}, errors.New("GitHub quota provider is unavailable")
		}
		return loader.app.gitHubQuotaUsageSource(ctx, loader.allowCollectionHealth)
	}
	if name == "$marketplacePackages" {
		source := marketplaceSource(ctx, loader.operational, loader.dataRevision)
		for index, input := range source.Rows {
			row := model.Row{}
			for _, definition := range definitions {
				if definition.Name != marketplace.SourceName {
					continue
				}
				for _, field := range definition.Select {
					logical := field.As
					if logical == "" {
						logical = field.Field
					}
					if value, present := input[logical]; present {
						row[field.Field] = value
					}
				}
			}
			source.Rows[index] = row
		}
		return source, nil
	}
	return model.Source{}, errors.New("runtime SQL source is not registered")
}

// RuntimeQuerySourceNames lists sources resolved by the server rather than
// stored as Postgres dashboard entities.
func RuntimeQuerySourceNames() []string {
	return []string{collectionHealthSourceName, gitHubQuotaUsageSourceName, webhookStatusSourceName, marketplace.SourceName, simulationDaysSourceName,
		"cli-commands", "cli-command-summary", "cli-command-statuses"}
}

func unavailableSource(name string) model.Source {
	return model.Source{
		Source: name,
		Rows:   []model.Row{},
		Metadata: model.Metadata{
			"source-id":    name,
			"availability": "unavailable",
			"completeness": "unknown",
			"freshness":    "unknown",
		},
	}
}

func paginationOffset(source, revision string, page paginationRequest) (int, error) {
	if page.Limit <= 0 || page.Limit > query.MaxOutputRows {
		return 0, fmt.Errorf("pagination limit for %q is invalid", source)
	}
	offset := 0
	if page.ContinuationToken != "" {
		data, err := base64.RawURLEncoding.DecodeString(page.ContinuationToken)
		if err != nil {
			return 0, fmt.Errorf("invalid or stale continuation token for %q", source)
		}
		var cursor struct {
			Source   string `json:"source"`
			Revision string `json:"revision"`
			Offset   int    `json:"offset"`
		}
		if json.Unmarshal(data, &cursor) != nil || cursor.Source != source || cursor.Revision != revision || cursor.Offset <= 0 {
			return 0, fmt.Errorf("invalid or stale continuation token for %q", source)
		}
		offset = cursor.Offset
	}
	return offset, nil
}

func (a *App) diagnostics(response http.ResponseWriter, request *http.Request) {
	active, err := a.database.State(request.Context())
	if err != nil || !active.Ready {
		writeError(response, http.StatusServiceUnavailable, "dashboard data is unavailable")
		return
	}
	diagnostics, err := a.database.Diagnostics(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "dashboard diagnostics are unavailable")
		return
	}
	serverLog.Printf("diagnostics served")
	writeJSON(response, http.StatusOK, diagnostics)
}

func (a *App) refresh(response http.ResponseWriter, request *http.Request) {
	active, err := a.database.State(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "dashboard data is unavailable")
		return
	}
	serverLog.Printf("refresh checked revision=%d", active.Revision)
	writeJSON(response, http.StatusOK, map[string]any{
		"revision":    active.Revision,
		"evaluatedAt": evaluationTime(active),
		"changed":     false,
	})
}

func evaluationTime(active postgresx.State) string {
	if !active.EvaluatedAt.IsZero() {
		return active.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// ResolveQueryContext binds the ingestion evaluation time to dashboard queries.
func ResolveQueryContext(definitions []query.Definition, evaluatedAt string) {
	for definitionIndex := range definitions {
		definitions[definitionIndex].Compute = slices.Clone(definitions[definitionIndex].Compute)
		for computedIndex := range definitions[definitionIndex].Compute {
			definitions[definitionIndex].Compute[computedIndex].Args = slices.Clone(definitions[definitionIndex].Compute[computedIndex].Args)
			for argumentIndex := range definitions[definitionIndex].Compute[computedIndex].Args {
				argument := &definitions[definitionIndex].Compute[computedIndex].Args[argumentIndex]
				switch argument.Context {
				case "time-end":
					argument.Context = ""
					argument.Value = evaluatedAt
				case "time-start":
					argument.Context = ""
					argument.Value = nil
				}
			}
		}
	}
}

func (a *App) static(response http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, "/api/") || request.URL.Path == "/mcp" {
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := strings.TrimPrefix(request.URL.Path, "/")
	if clean == "" {
		clean = "index.html"
	}
	if !filepath.IsLocal(clean) {
		http.NotFound(response, request)
		return
	}
	root, err := os.OpenRoot(a.config.SiteDirectory)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(clean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && a.config.Canvas == nil {
			a.serveIndex(response, request, "")
		} else {
			http.NotFound(response, request)
		}
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if info.IsDir() || filepath.Base(clean) == "index.html" {
		a.serveIndex(response, request, "")
		return
	}
	if strings.EqualFold(filepath.Ext(clean), ".md") {
		response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		if a.config.Canvas != nil {
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	http.ServeContent(response, request, info.Name(), info.ModTime(), file)
}

func (a *App) serveIndex(response http.ResponseWriter, request *http.Request, accessToken string) {
	root, err := os.OpenRoot(a.config.SiteDirectory)
	if err != nil {
		http.Error(response, "not found", http.StatusNotFound)
		return
	}
	defer func() { _ = root.Close() }()
	content, err := root.ReadFile("index.html")
	if err != nil {
		http.Error(response, "not found", http.StatusNotFound)
		return
	}
	html := string(content)
	storageBackend := "postgres"
	if _, local := a.database.(*sqlitex.Store); local {
		storageBackend = "sqlite"
	}
	injections := `<meta name="dashboard-data-backend" content="server-http"><meta name="dashboard-storage-backend" content="` + storageBackend + `">`
	if a.config.Canvas != nil {
		injections += `<meta name="dashboard-api-base" content="` + a.config.Canvas.Prefix + `/">`
	}
	if a.oauth != nil {
		injections += `<meta name="cao-auth-mode" content="github">`
		if a.adminAuthorized(request) {
			injections += `<script>globalThis.__DASHBOARD_ADMIN__ = true;</script>`
		}
		if a.hostedCLI != nil {
			injections += `<script>globalThis.__CLI_QUEUE__ = true;</script>`
		}
		injections += `<script>const m=document.cookie.match(/(?:^|;\s*)cao_csrf=([^;]+)/);if(m){const c=decodeURIComponent(m[1]);const f=window.fetch.bind(window);window.fetch=(i,n={})=>{const u=typeof i==="string"?i:i.url;if(u&&new URL(u,location.href).origin===location.origin){const h=new Headers(n.headers||{});if(!h.has("X-CSRF-Token"))h.set("X-CSRF-Token",c);n={...n,headers:h};}return f(i,n);};}</script>`
	} else if accessToken != "" {
		http.SetCookie(response, &http.Cookie{
			Name: "cao_github_local", Value: a.githubLocalResourceSession(), Path: "/api/v1/github/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		})
		var bootstrap strings.Builder
		if err := localAccessBootstrap.Execute(&bootstrap, accessToken); err != nil {
			writeError(response, http.StatusInternalServerError, "dashboard bootstrap is unavailable")
			return
		}
		injections += bootstrap.String()
	}
	if index := strings.Index(strings.ToLower(html), "</head>"); index >= 0 {
		html = html[:index] + injections + html[index:]
	} else {
		html = injections + html
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	// #nosec G705 -- content comes from the configured local static site, with fixed bootstrap markup inserted.
	_, _ = io.WriteString(response, html)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeErrorPayload(response, status, map[string]string{"error": message})
}

func writeErrorPayload(response http.ResponseWriter, status int, payload map[string]string) {
	traceID := response.Header().Get(telemetry.TraceIDHeader)
	spanID := response.Header().Get(telemetry.SpanIDHeader)
	if traceID != "" {
		payload["traceId"] = traceID
	}
	if spanID != "" {
		payload["spanId"] = spanID
	}
	serverLog.Printf(
		"http error status=%d trace_id=%s span_id=%s",
		status, traceID, spanID,
	)
	writeJSON(response, status, payload)
}

type eventHub struct {
	mu      sync.Mutex
	clients map[chan eventObservation]bool
	current eventObservation
	cancel  context.CancelFunc
	wake    chan struct{}
	workers sync.WaitGroup
	stopped bool
	done    chan struct{}
}

type eventObservation struct {
	revision       int64
	healthRevision int64
}

func newEventHub() *eventHub {
	return &eventHub{clients: map[chan eventObservation]bool{}, wake: make(chan struct{}, 1)}
}

func (hub *eventHub) Subscribe(ctx context.Context, app *App, allowHealth bool) chan eventObservation {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.stopped {
		return nil
	}
	channel := make(chan eventObservation, 1)
	hub.clients[channel] = allowHealth
	if hub.cancel == nil {
		ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		hub.cancel = cancel
		hub.workers.Add(1)
		go hub.observe(ctx, app)
	}
	return channel
}

func (hub *eventHub) Unsubscribe(channel chan eventObservation) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	delete(hub.clients, channel)
	close(channel)
	if len(hub.clients) == 0 && hub.cancel != nil {
		hub.cancel()
		hub.cancel = nil
	}
}

func (hub *eventHub) Broadcast(revision int64) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.current.revision = revision
	hub.publishLocked()
	select {
	case hub.wake <- struct{}{}:
	default:
	}
}

func (hub *eventHub) publishLocked() {
	for channel := range hub.clients {
		select {
		case channel <- hub.current:
		default:
			// A slow stream needs the latest state, not every intermediate revision.
			select {
			case <-channel:
			default:
			}
			select {
			case channel <- hub.current:
			default:
			}
		}
	}
}

func (hub *eventHub) observe(ctx context.Context, app *App) {
	defer hub.workers.Done()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-hub.wake:
		}
		hub.mu.Lock()
		needsHealth := false
		for _, allowed := range hub.clients {
			needsHealth = needsHealth || allowed
		}
		hub.mu.Unlock()
		active, err := app.database.State(ctx)
		if err != nil {
			continue
		}
		healthRevision, healthErr := app.ingestionHealthRevision(ctx, needsHealth)
		hub.mu.Lock()
		if ctx.Err() == nil {
			changed := hub.current.revision != active.Revision ||
				(needsHealth && healthErr == nil && hub.current.healthRevision != healthRevision)
			if healthErr == nil && changed {
				hub.current = eventObservation{revision: active.Revision, healthRevision: healthRevision}
				hub.publishLocked()
			}
		}
		hub.mu.Unlock()
	}
}

func (hub *eventHub) shutdown() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.stopped {
		return
	}
	hub.stopped = true
	if hub.cancel != nil {
		hub.cancel()
		hub.cancel = nil
	}
	hub.done = make(chan struct{})
	go func() {
		hub.workers.Wait()
		close(hub.done)
	}()
}

func (hub *eventHub) wait(ctx context.Context) error {
	select {
	case <-hub.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dashboardQueriesShape identifies which JSON shape decodeDashboardQueries
// found the query definitions in. It is useful for diagnosing a
// misconfigured or newly wrapped queries file without logging the file's
// path or contents.
type dashboardQueriesShape string

const (
	dashboardQueriesShapeArray     dashboardQueriesShape = "array"
	dashboardQueriesShapeQueries   dashboardQueriesShape = "queries"
	dashboardQueriesShapeDashboard dashboardQueriesShape = "dashboard.queries"
)

// decodeDashboardQueries parses the raw bytes of a dashboard query file into
// its definitions and which JSON shape supplied them. It accepts a bare
// top-level array of query.Definition, or a wrapped document exposing them
// under "queries" or, failing that, "dashboard.queries". It is a pure
// function over already-read bytes, so every shape ParseDashboardQueries
// accepts is testable without a file on disk.
func decodeDashboardQueries(content []byte) ([]query.Definition, dashboardQueriesShape, error) {
	var definitions []query.Definition
	if json.Unmarshal(content, &definitions) == nil {
		return definitions, dashboardQueriesShapeArray, nil
	}
	var document struct {
		Queries   []query.Definition `json:"queries"`
		Dashboard struct {
			Queries []query.Definition `json:"queries"`
		} `json:"dashboard"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, "", fmt.Errorf("parse dashboard query file: %w", err)
	}
	if len(document.Queries) > 0 {
		return document.Queries, dashboardQueriesShapeQueries, nil
	}
	return document.Dashboard.Queries, dashboardQueriesShapeDashboard, nil
}

func ParseDashboardQueries(path string) ([]query.Definition, error) {
	if path == "" {
		return nil, nil
	}
	// #nosec G304 -- the operator explicitly configures the local dashboard query path.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	definitions, shape, err := decodeDashboardQueries(content)
	if err != nil {
		return nil, err
	}
	serverLog.Printf("dashboard queries parsed shape=%s count=%d", shape, len(definitions))
	return definitions, nil
}

func ParsePort(address string) int {
	_, port, _ := net.SplitHostPort(address)
	value, _ := strconv.Atoi(port)
	return value
}
