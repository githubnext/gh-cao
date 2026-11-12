package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
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
	// NativeAcquisition selects distributed Go acquisition and publication.
	NativeAcquisition bool
	// NativeInventory is the immutable reviewed source contract, not a path or
	// an ingestion-scope grant. Replicas pin its digest in PostgreSQL.
	NativeInventory *collectnative.InventorySnapshot
	// NativeInventoryDigest selects a previously provisioned shared document
	// instead of requiring a local inventory file on every replica.
	NativeInventoryDigest string
	// NativeValues separately selects the existing reviewed Value execution
	// lane. Only this feature requires its reviewed catalog and policy.
	NativeValues bool
	// AppID and PrivateKeyPEM authenticate the GitHub App whose installations
	// define ingestion scope.
	AppID         int64
	PrivateKeyPEM []byte
	BaseURL       string
	UploadURL     string
	Transport     http.RoundTripper

	// CatalogRoot identifies the reviewed Value policy imports when selected.
	CatalogRoot string
	// ControlRepository names the reviewed inventory's control repository.
	ControlRepository string
	// PolicyPath is the reviewed Value policy when selected.
	PolicyPath string

	WindowDays            int
	RunLimit              int
	MaxStorageMB          int
	RequestTimeoutMinutes int
	RateLimitFloor        int
	// MinValueInterval controls only the separately selected native Value sweeps.
	MinValueInterval time.Duration
	// InventoryLimit optionally caps enrolled repositories named during
	// inventory discovery. Zero means unbounded; exceeding a configured limit
	// fails rather than publishing a partial inventory.
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
	// It therefore needs no App private key, which keeps the internet-facing
	// front end from holding credentials it cannot use.
	AdmitOnly bool
}

// Validate reports whether the collector can be constructed.
func (config *CollectorConfig) Validate() error {
	if config == nil {
		return nil
	}
	if config.NativeAcquisition {
		return config.validateNative()
	}
	return fmt.Errorf("%w: hosted collection requires CAO_COLLECT_NATIVE=true; the filesystem collector is retired",
		collectnative.ErrUnsupported)
}

// Collector is the assembled native collection profile. It satisfies
// Reconciler and EventAdmitter so deliveries are queued, not projected inline.
type Collector struct {
	backfillMu sync.Mutex
	config     CollectorConfig
	client     *githubapp.Client
	quota      *githubquota.Service
	enrollment collect.Enrollment
	queue      collect.Queue
	admitter   collect.Admitter
	backfill   collect.Backfill
	reporter   collect.Reporter
	replayer   collect.DeliveryReplayer
	native     *nativeCollection
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
	queue := collect.Queue{
		Tasks: services.Queue, Admission: services.Admission, Metadata: services.Collection,
		Leases: services.Leases, Deliveries: services.Deliveries, Metrics: services.IngestionMetrics,
		MaxLength: int64(config.QueueMaxLength),
	}
	return newNativeCollector(ctx, services, capabilities, data, config, quota, queue)
}

// Rebuild starts a native backfill through the existing administrative endpoint.
func (c *Collector) Rebuild(ctx context.Context) (ingest.Result, error) {
	if c.config.AdmitOnly {
		return ingest.Result{}, ErrAdmitOnly
	}
	state, err := c.runBackfill(ctx)
	return ingest.Result{Revision: state.Revision}, err
}

// Reconcile implements the Reconciler contract for callers that do not use
// delivery admission directly.
func (c *Collector) Reconcile(ctx context.Context, event GitHubWebhook) (ingest.Result, error) {
	if _, err := c.admitter.Admit(ctx, event.Event, event.Payload); err != nil {
		if errors.Is(err, collect.ErrNotEnrolled) {
			return ingest.Result{}, nil
		}
		return ingest.Result{}, err
	}
	// Admission is complete; a native collection worker projects.
	return ingest.Result{}, nil
}

// Admit queues collection for one verified delivery without taking the global
// projection lease, so concurrent deliveries never contend.
func (c *Collector) Admit(ctx context.Context, event GitHubWebhook) (map[string]any, error) {
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
func (c *Collector) Start(ctx context.Context, _ func(revision int64)) error {
	return c.start(ctx, ctx, func(work func()) { go work() })
}

func (c *Collector) start(startupCtx, ctx context.Context, launch func(func())) error {
	if err := c.queue.EnsureRuns(startupCtx); err != nil {
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
			serverLog.PrintfContext(startupCtx, "cold start failed")
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
		worker := c.Worker(fmt.Sprintf("%s-%d", c.consumer(), index))
		launch(func() {
			if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
				serverLog.PrintfContext(startupCtx, "collection worker stopped")
			}
		})
	}
	if c.config.RecoverDeliveries {
		launch(func() {
			c.recoverDeliveries(ctx)
		})
	}

	return nil
}

func (c *Collector) RecoveryReady() bool {
	return true
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
				serverLog.PrintfContext(ctx, "delivery recovery failed")
			}
		}
	}
}

// ErrAdmitOnly reports an operation that requires collection credentials on a
// process configured to admit deliveries only.
var ErrAdmitOnly = errors.New(
	"this process admits deliveries only; run the collect or backfill role")

// Worker builds a standalone collection worker for the collect role.
func (c *Collector) Worker(consumer string) collect.Worker {
	worker := c.native.worker
	worker.Consumer = consumer
	return collect.Worker{Native: &worker}
}

func (c *Collector) RepositoryDiscovery() collect.RepositoryDiscovery {
	return collect.RepositoryDiscovery{Native: &c.native.worker}
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
		writeError(request.Context(), response, http.StatusServiceUnavailable, "collection status is unavailable")
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
