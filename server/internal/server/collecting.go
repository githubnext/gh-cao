package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

// defaultQueueMaxLength bounds admitted outstanding work. Completed entries
// are deleted after ACK; reaching the bound rejects admission for retry rather
// than trimming work that a consumer may still need to recover.
const defaultQueueMaxLength = 200_000

// CollectorConfig configures the optional server collection profile.
//
// Leaving it nil selects the default Actions profile, in which the server
// ingests snapshots published by the Activity workflow and performs no GitHub
// collection of its own.
type CollectorConfig struct {
	// AppID and PrivateKeyPEM authenticate the GitHub App whose installations
	// define ingestion scope.
	AppID         int64
	PrivateKeyPEM []byte
	BaseURL       string
	UploadURL     string
	Transport     http.RoundTripper

	// LakeDirectory holds collected evidence in the published snapshot layout.
	LakeDirectory string
	// CatalogRoot contains activity/cao.mjs.
	CatalogRoot string
	// ControlRepository names the control repository for inventory discovery.
	ControlRepository string
	// PolicyPath is the reviewed deployment policy resolved for inventory.
	PolicyPath string
	// StaticInventoryPath is the source-bound inventory packaged with the
	// collector image.
	StaticInventoryPath string

	NodeBinary   string
	GitHubBinary string

	WindowDays            int
	RunLimit              int
	MaxStorageMB          int
	RequestTimeoutMinutes int
	RateLimitFloor        int
	MinProjectionInterval time.Duration
	// InventoryLimit optionally caps enrolled repositories named during
	// inventory discovery. Zero means unbounded; exceeding a configured limit
	// fails the projection rather than publishing a partial inventory.
	InventoryLimit    int
	CollectionTimeout time.Duration

	// Workers enables in-process collection workers. Deployments that scale
	// collection separately leave this zero and run the collect role instead.
	Workers int
	// Consumer identifies this process within the consumer group.
	Consumer string
	// RecoverDeliveries enables webhook delivery replay for gap recovery.
	RecoverDeliveries bool
	// RepositoryRefreshInterval repairs repository membership even when lifecycle
	// webhooks are missed. Zero selects one hour.
	RepositoryRefreshInterval time.Duration
	// QueueMaxLength applies backpressure to the outstanding task stream.
	QueueMaxLength int
	// AdmitOnly runs the admission half of the profile alone: the process
	// verifies deliveries and enqueues work, and never collects or projects.
	// It therefore needs no App private key and no evidence lake, which keeps
	// the internet-facing front end from holding credentials it cannot use.
	AdmitOnly bool
}

// Validate reports whether the collector can be constructed.
func (config *CollectorConfig) Validate() error {
	if config == nil {
		return nil
	}
	if config.AppID <= 0 {
		return errors.New("collection requires a GitHub App identifier")
	}
	if config.RepositoryRefreshInterval < 0 {
		return errors.New("repository refresh interval must not be negative")
	}
	if config.AdmitOnly {
		// Admission verifies deliveries against the webhook secret and writes
		// to Redis. Requiring collection credentials here would place the App
		// private key in a process that never calls GitHub.
		if len(config.PrivateKeyPEM) > 0 {
			return errors.New(
				"admission-only collection must not be given the App private key; " +
					"it never calls GitHub")
		}
		if config.Workers > 0 {
			return errors.New("admission-only collection cannot run workers")
		}
		if config.RecoverDeliveries {
			return errors.New("admission-only collection cannot replay deliveries")
		}
	} else {
		if len(config.PrivateKeyPEM) == 0 {
			return errors.New("collection requires a GitHub App private key")
		}
		if strings.TrimSpace(config.LakeDirectory) == "" {
			return errors.New("collection requires an evidence lake directory")
		}
		if strings.TrimSpace(config.CatalogRoot) == "" {
			return errors.New("collection requires the catalog root containing activity/cao.mjs")
		}
		if strings.TrimSpace(config.ControlRepository) == "" {
			return errors.New("collection requires a control repository for inventory discovery")
		}
		if strings.TrimSpace(config.PolicyPath) == "" {
			return errors.New("collection requires the reviewed control policy path")
		}
		if strings.TrimSpace(config.StaticInventoryPath) == "" {
			return errors.New("collection requires the packaged static control-plane inventory")
		}
	}
	if config.QueueMaxLength <= 0 {
		config.QueueMaxLength = defaultQueueMaxLength
	}
	return nil
}

// Collector is the assembled collection profile. It satisfies Reconciler, so
// the existing rebuild and webhook plumbing works unchanged, and additionally
// satisfies EventAdmitter, so deliveries are queued instead of projected
// inline.
type Collector struct {
	volatile      bool
	bootstrapDone chan struct{}
	bootstrapOnce sync.Once
	backfillMu    sync.Mutex
	config        CollectorConfig
	client        *githubapp.Client
	quota         *githubquota.Service
	enrollment    collect.Enrollment
	queue         collect.Queue
	lake          collect.Lake
	runner        collect.Runner
	projector     collect.Projector
	admitter      collect.Admitter
	backfill      collect.Backfill
	reporter      collect.Reporter
	replayer      collect.DeliveryReplayer
}

var _ Reconciler = (*Collector)(nil)
var _ EventAdmitter = (*Collector)(nil)

const collectionHealthSourceName = "collection-health"

// NewCollector assembles the collection profile from configuration.
func NewCollector(
	ctx context.Context, store operational.Store, data *postgresx.Store, config CollectorConfig, databaseQueriesPath string,
) (*Collector, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := operational.CheckStore(store); err != nil {
		return nil, err
	}
	capabilities := store.Capabilities()
	if capabilities.Collection.Scope == operational.ScopeUnsupported {
		return nil, errors.New("collection requires operational collection capability")
	}
	services := store.OperationalServices()
	volatile := capabilities.Collection.Persistence == operational.PersistenceVolatile
	if err := operational.ValidateOperationalServices(capabilities, services, operational.Requirements{
		SingleProcess: volatile, AllowVolatile: volatile, Collection: true,
	}); err != nil {
		return nil, fmt.Errorf("collection services: %w", err)
	}
	if volatile && config.AdmitOnly {
		return nil, errors.New("volatile collection requires co-resident workers and backfill")
	}
	if volatile && config.Workers <= 0 {
		return nil, errors.New("volatile collection requires at least one co-resident worker")
	}
	if data == nil {
		return nil, errors.New("collection requires Postgres")
	}
	quotaFloor := config.RateLimitFloor
	if quotaFloor <= 0 {
		quotaFloor = 1000
	}
	quota, err := githubquota.New(services.GitHubQuota, githubquota.Options{SafetyReserve: quotaFloor})
	if err != nil {
		return nil, fmt.Errorf("configure github quota: %w", err)
	}
	quotaApp := "github-app-" + strconv.FormatInt(config.AppID, 10)
	enrollment := collect.Enrollment{Metadata: services.Collection, Leases: services.Leases}
	queue := collect.Queue{
		Tasks: services.Queue, Admission: services.Admission, Metadata: services.Collection,
		Leases: services.Leases, Deliveries: services.Deliveries, Metrics: services.IngestionMetrics,
		MaxLength: int64(config.QueueMaxLength),
	}
	if config.AdmitOnly {
		// Erasure is enqueued rather than performed, because this process has
		// no evidence lake to erase from.
		backfill := collect.Backfill{StateStore: services.State, Metadata: services.Collection, Quota: quota, QuotaApp: quotaApp}
		collector := &Collector{
			volatile:   volatile,
			config:     config,
			quota:      quota,
			enrollment: enrollment,
			queue:      queue,
			backfill:   backfill,
			admitter:   collect.Admitter{Enrollment: enrollment, Queue: queue, Mining: true},
			reporter: collect.Reporter{
				Enrollment: enrollment, Queue: queue, Backfill: backfill, Metrics: services.IngestionMetrics, Data: data,
			},
		}
		return collector, nil
	}
	client, err := githubapp.New(githubapp.Config{
		AppID:         config.AppID,
		PrivateKeyPEM: config.PrivateKeyPEM,
		BaseURL:       config.BaseURL,
		UploadURL:     config.UploadURL,
		Transport:     config.Transport,
	})
	if err != nil {
		return nil, err
	}
	if err := client.ValidateRepositoryAccess(ctx, config.ControlRepository); err != nil {
		return nil, fmt.Errorf("validate repository visibility: %w", err)
	}
	lake := collect.Lake{Directory: config.LakeDirectory}
	if err := lake.Prepare(); err != nil {
		return nil, err
	}
	budget := &githubapp.Budget{Metadata: services.Collection, Store: services.RateLimits, Floor: config.RateLimitFloor}
	runner := collect.Runner{
		Lake:                  lake,
		CatalogRoot:           config.CatalogRoot,
		NodeBinary:            config.NodeBinary,
		GitHubBinary:          config.GitHubBinary,
		GitHubAPIURL:          config.BaseURL,
		WindowDays:            config.WindowDays,
		RunLimit:              config.RunLimit,
		MaxStorageMB:          config.MaxStorageMB,
		RequestTimeoutMinutes: config.RequestTimeoutMinutes,
		Timeout:               config.CollectionTimeout,
		Tokens:                client,
		Budget:                budget,
	}
	if err := runner.Validate(); err != nil {
		return nil, err
	}
	projector := collect.Projector{State: services.State, Leases: services.Leases, Data: data,
		Lake:                     lake,
		Enrollment:               enrollment,
		CatalogRoot:              config.CatalogRoot,
		NodeBinary:               config.NodeBinary,
		GitHubBinary:             config.GitHubBinary,
		GitHubAPIURL:             config.BaseURL,
		Tokens:                   client,
		Budget:                   budget,
		OutcomeClient:            client,
		Quota:                    quota,
		QuotaApp:                 quotaApp,
		WindowDays:               config.WindowDays,
		DatabaseQueriesPath:      databaseQueriesPath,
		ControlRepository:        config.ControlRepository,
		PolicyPath:               config.PolicyPath,
		StaticInventoryPath:      config.StaticInventoryPath,
		MinInterval:              config.MinProjectionInterval,
		InventoryRepositoryLimit: config.InventoryLimit,
	}
	backfill := collect.Backfill{StateStore: services.State, Metadata: services.Collection, Enrollment: enrollment, Queue: queue,
		Projector: projector, Lake: lake, Enumerator: client, RunEnumerator: client,
		Quota: quota, QuotaApp: quotaApp,
		WindowDays: config.WindowDays,
	}
	collector := &Collector{
		volatile:   volatile,
		config:     config,
		client:     client,
		quota:      quota,
		enrollment: enrollment,
		queue:      queue,
		lake:       lake,
		runner:     runner,
		projector:  projector,
		admitter: collect.Admitter{
			Enrollment: enrollment, Queue: queue, Mining: true,
			Projection: projector,
		},
		backfill: backfill,
		reporter: collect.Reporter{
			Enrollment: enrollment, Queue: queue, Backfill: backfill,
			Budget: budget, Metrics: services.IngestionMetrics, Data: data,
		},
		replayer: collect.DeliveryReplayer{State: services.State, Metadata: services.Collection, Client: client, Enabled: config.RecoverDeliveries},
	}
	if volatile {
		collector.bootstrapDone = make(chan struct{})
		collector.backfill.ReconstructScope = true
		collector.backfill.ScopeLimit = config.InventoryLimit
		if collector.backfill.ScopeLimit <= 0 {
			collector.backfill.ScopeLimit = 100_000
		}
		collector.backfill.ScopeReady = func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			collector.bootstrapOnce.Do(func() { close(collector.bootstrapDone) })
			return nil
		}
	}
	return collector, nil
}

// Rebuild reprojects the evidence lake. Cold start and recovery both reuse the
// existing administrative rebuild endpoint through this method.
func (c *Collector) Rebuild(ctx context.Context) (ingest.Result, error) {
	if c.config.AdmitOnly {
		return ingest.Result{}, ErrAdmitOnly
	}
	if c.volatile {
		state, err := c.runBackfill(ctx)
		return ingest.Result{Revision: state.Revision}, err
	}
	populated, err := c.lake.Populated()
	if err != nil {
		return ingest.Result{}, err
	}
	if populated {
		return c.projector.Rebuild(ctx)
	}
	state, err := c.backfill.Run(ctx)
	if err != nil {
		return ingest.Result{}, err
	}
	return ingest.Result{Revision: state.Revision}, nil
}

// Reconcile is the lease-taking path the default profile uses. Collection
// never needs it because Admit is preferred, but implementing it keeps the
// Reconciler contract total.
func (c *Collector) Reconcile(ctx context.Context, event GitHubWebhook) (ingest.Result, error) {
	if !c.RecoveryReady() {
		return ingest.Result{}, ErrCollectionBootstrapping
	}
	if _, err := c.admitter.Admit(ctx, event.Event, event.Payload); err != nil {
		if errors.Is(err, collect.ErrNotEnrolled) {
			return ingest.Result{}, nil
		}
		return ingest.Result{}, err
	}
	if c.config.AdmitOnly {
		// Admission is complete; a collection worker projects.
		return ingest.Result{}, nil
	}
	return c.projector.Project(ctx)
}

// Admit queues collection for one verified delivery without taking the global
// projection lease, so concurrent deliveries never contend.
func (c *Collector) Admit(ctx context.Context, event GitHubWebhook) (map[string]any, error) {
	if !c.RecoveryReady() {
		return nil, ErrCollectionBootstrapping
	}
	admission, err := c.admitter.AdmitDelivery(ctx, event.Event, event.Payload, event.Delivery, deliveryTTL)
	if err != nil {
		if errors.Is(err, collect.ErrNotEnrolled) {
			// Out-of-scope repositories are acknowledged, not retried.
			return map[string]any{"ignored": true, "reason": "not-enrolled"}, nil
		}
		return nil, err
	}
	result := map[string]any{
		"kind":      string(admission.Kind),
		"queued":    admission.Enqueued,
		"duplicate": admission.Duplicate,
	}
	if admission.Reason != "" {
		result["reason"] = admission.Reason
	}
	return result, nil
}

// Start launches cold start, in-process workers, and delivery recovery.
func (c *Collector) Start(ctx context.Context, onProjection func(revision int64)) error {
	return c.start(ctx, ctx, onProjection, func(work func()) { go work() })
}

func (c *Collector) start(startupCtx, ctx context.Context, onProjection func(revision int64), launch func(func())) error {
	if err := c.queue.Ensure(startupCtx); err != nil {
		return err
	}
	if c.config.AdmitOnly {
		// Nothing to start: this process only admits deliveries.
		return nil
	}
	launch(func() {
		for {
			_, err := c.runBackfill(ctx)
			if err == nil || ctx.Err() != nil {
				return
			}
			serverLog.Printf("cold start failed")
			if !c.volatile || c.RecoveryReady() {
				return
			}
			timer := time.NewTimer(30 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
	for index := 0; index < c.config.Workers; index++ {
		worker := collect.Worker{
			Queue:          c.queue,
			Runner:         c.runner,
			Projector:      c.projector,
			Enrollment:     c.enrollment,
			ScopeRefresher: c.scopeRefresher(),
			Discovery:      c.RepositoryDiscovery(),
			Consumer:       fmt.Sprintf("%s-%d", c.consumer(), index),
			Project:        true,
			OnProjection:   onProjection,
		}
		launch(func() {
			if c.volatile {
				select {
				case <-ctx.Done():
					return
				case <-c.bootstrapDone:
				}
			}
			if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
				serverLog.Printf("collection worker stopped")
			}
		})
	}
	if c.config.RecoverDeliveries {
		launch(func() {
			if c.volatile {
				select {
				case <-ctx.Done():
					return
				case <-c.bootstrapDone:
				}
			}
			c.recoverDeliveries(ctx)
		})
	}

	return nil
}

var ErrCollectionBootstrapping = errors.New("collection scope is being reconstructed; retry delivery")

func (c *Collector) RecoveryReady() bool {
	if !c.volatile {
		return true
	}
	select {
	case <-c.bootstrapDone:
		return true
	default:
		return false
	}
}

func (c *Collector) runBackfill(ctx context.Context) (collect.BackfillState, error) {
	if !c.backfillMu.TryLock() {
		return collect.BackfillState{}, errors.New("collection backfill is already running")
	}
	defer c.backfillMu.Unlock()
	return c.backfill.Run(ctx)
}

func (c *Collector) consumer() string {
	if name := strings.TrimSpace(c.config.Consumer); name != "" {
		return name
	}
	return "server"
}

func (c *Collector) recoverDeliveries(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.replayer.Recover(ctx); err != nil {
				serverLog.Printf("delivery recovery failed")
			}
		}
	}
}

// ErrAdmitOnly reports an operation that requires collection credentials and
// an evidence lake on a process configured to admit deliveries only.
var ErrAdmitOnly = errors.New(
	"this process admits deliveries only; run the collect or backfill role")

// Worker builds a standalone collection worker for the collect role.
func (c *Collector) Worker(consumer string) collect.Worker {
	return collect.Worker{
		Queue: c.queue, Runner: c.runner, Projector: c.projector,
		Enrollment: c.enrollment, Consumer: consumer, Project: true,
		ScopeRefresher: c.scopeRefresher(),
		Discovery:      c.RepositoryDiscovery(),
	}
}

func (c *Collector) scopeRefresher() collect.ScopeRefresher {
	return collect.ScopeRefresher{Client: c.client, Quota: c.quota,
		QuotaApp:        "github-app-" + strconv.FormatInt(c.config.AppID, 10),
		RepositoryLimit: c.config.InventoryLimit}
}

func (c *Collector) RepositoryDiscovery() collect.RepositoryDiscovery {
	return collect.RepositoryDiscovery{Client: c.client, Enrollment: c.enrollment,
		Queue: c.queue, State: c.projector.State, Leases: c.enrollment.Leases,
		Interval: c.config.RepositoryRefreshInterval}
}

// Backfill exposes cold start for the backfill role.
func (c *Collector) Backfill() collect.Backfill { return c.backfill }

// validateProfileExclusivity enforces that exactly one ingestion profile is
// configured. The Actions profile ingests snapshots published by the Activity
// workflow; the collection profile acquires evidence directly. Running both
// against one deployment would give two writers for one canonical database, so
// a dual configuration is rejected at startup rather than resolved silently.
func validateProfileExclusivity(config Config) error {
	if config.Collector == nil {
		return nil
	}
	if strings.TrimSpace(config.SourceDirectory) != "" {
		return errors.New(
			"collection and published-snapshot ingestion are alternatives: " +
				"configure either a source directory or collection, not both")
	}
	if config.Reconciler != nil {
		return errors.New("collection replaces the configured reconciler; configure only one")
	}
	if strings.TrimSpace(config.WebhookSecret) == "" {
		return errors.New("collection requires a webhook secret")
	}
	return config.Collector.Validate()
}

// Collector reports the assembled collection profile, or nil in the Actions
// profile.
func (a *App) Collector() *Collector {
	collector, _ := a.reconciler.(*Collector)
	return collector
}

// collectionStatus reports collection health. In the Actions profile it
// reports that collection is not configured rather than failing.
func (a *App) collectionStatus(response http.ResponseWriter, request *http.Request) {
	if !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "administrative authorization is required")
		return
	}
	collector, ok := a.reconciler.(*Collector)
	if !ok {
		writeJSON(response, http.StatusOK, collect.Status{
			Configured: false, Health: "not-configured", Counters: map[string]int64{},
		})
		return
	}
	status, err := collector.reporter.Snapshot(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "collection status is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (a *App) collectionHealthSource(ctx context.Context, allowed bool) (model.Source, error) {
	if !allowed {
		return unavailableSource(collectionHealthSourceName), nil
	}
	collector, ok := a.reconciler.(*Collector)
	if !ok {
		return collectionHealthSource(collect.Status{
			Health: "not-configured", Counters: map[string]int64{},
		}), nil
	}
	status, err := collector.reporter.Snapshot(ctx)
	if err != nil {
		return model.Source{}, err
	}
	return collectionHealthSource(status), nil
}

func collectionHealthSource(status collect.Status) model.Source {
	counters := status.Counters
	if counters == nil {
		counters = map[string]int64{}
	}
	return model.Source{
		Source: collectionHealthSourceName,
		Rows: []model.Row{{
			"configured":                status.Configured,
			"health":                    status.Health,
			"queue-depth":               status.QueueDepth,
			"pending-tasks":             status.PendingTasks,
			"oldest-pending-age":        status.OldestPending,
			"dead-letters":              status.DeadLetters,
			"backfill":                  status.Backfill,
			"backfill-failures":         status.BackfillFailures,
			"backfill-queued-run-tasks": status.BackfillRunTasks,
			"webhook-load":              status.Load["webhook"],
			"collection-load":           status.Load["collection"],
			"failure-load":              status.Load["failure"],
			"last-projected":            status.LastProjected,
			"health-revision":           status.HealthRevision,
			"last-webhook-at":           status.LastWebhookAt,
			"last-failure-at":           status.LastFailureAt,
			"last-failure-code":         status.LastFailureCode,
			"last-success-at":           status.LastSuccessAt,
			"webhook-received":          counters["webhookReceived"],
			"webhook-duplicate":         counters["webhookDuplicate"],
			"webhook-admission-failed":  counters["webhookAdmissionFailed"],
			"task-queued":               counters["taskQueued"],
			"task-coalesced":            counters["taskCoalesced"],
			"collection-succeeded":      counters["collectionSucceeded"],
			"collection-failed":         counters["collectionFailed"],
			"collection-retried":        counters["collectionRetried"],
			"collection-dead-lettered":  counters["collectionDeadLettered"],
		}},
		Metadata: model.Metadata{
			"source-id":    collectionHealthSourceName,
			"availability": "available",
			"completeness": "complete",
			"freshness":    "current",
		},
	}
}
