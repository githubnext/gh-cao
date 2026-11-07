package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

type nativeCollection struct {
	acquisition postgresx.Acquisition
	inventory   *collect.NativeInventoryService
	collector   collectnative.Collector
	worker      collect.NativeWorker
	archive     *sessionArchiveService
	values      *collect.NativeValueService
}

// Keep native assembly separate from the legacy lake/Runner/Projector: none of
// their constructors or filesystem validation may run for this boundary.
func newNativeCollector(ctx context.Context, services operational.OperationalServices, capabilities operational.Capabilities,
	data *postgresx.Store, config CollectorConfig, quota *githubquota.Service, queue collect.Queue,
) (*Collector, error) {
	if err := config.validateNative(); err != nil {
		return nil, err
	}
	if data == nil || !data.Open() || capabilities.Collection.Scope != operational.ScopeDeployment ||
		capabilities.Collection.Persistence != operational.PersistenceRestart {
		return nil, fmt.Errorf("%w: native collection requires durable deployment-scoped PostgreSQL ownership and queue receipts",
			collectnative.ErrUnsupported)
	}
	if err := queue.EnsureRuns(ctx); err != nil {
		return nil, err
	}
	enrollment := collect.Enrollment{Metadata: services.Collection, Leases: services.Leases, Authority: data.NativeEnrollment()}
	acquisition := data.Acquisition()
	quotaApp := "github-app-" + strconv.FormatInt(config.AppID, 10)
	backfill := collect.Backfill{StateStore: services.State, Metadata: services.Collection, Enrollment: enrollment,
		Queue: queue, Quota: quota, QuotaApp: quotaApp, AcquisitionScopes: enrollment.Authority,
		NativePublications: acquisition, ScopeLimit: config.InventoryLimit, WindowDays: config.WindowDays}
	native := &nativeCollection{acquisition: acquisition}
	collector := &Collector{config: config, quota: quota, enrollment: enrollment, queue: queue, backfill: backfill, native: native}
	collector.admitter = collect.Admitter{Enrollment: enrollment, Queue: queue, Mining: true}
	collector.reporter = collect.Reporter{Enrollment: enrollment, Queue: queue, Backfill: backfill,
		Metrics: services.IngestionMetrics, Data: data}
	if config.AdmitOnly {
		if len(config.PrivateKeyPEM) != 0 || config.Workers != 0 || config.RecoverDeliveries || config.NativeValues {
			return nil, errors.New("native admission must not hold App credentials, run workers or replay App deliveries")
		}
		return collector, nil
	}
	if config.NativeInventory == nil {
		if config.NativeInventoryDigest == "" {
			return nil, fmt.Errorf("%w: native collection requires immutable reviewed inventory bytes or its provisioned digest",
				collectnative.ErrUnsupported)
		}
		snapshot, err := acquisition.LoadInventoryConfiguration(ctx, config.NativeInventoryDigest)
		if err != nil {
			return nil, fmt.Errorf("load selected native inventory: %w", err)
		}
		config.NativeInventory = &snapshot
		collector.config = config
	}
	if config.NativeInventoryDigest != "" && config.NativeInventory.Digest != config.NativeInventoryDigest {
		return nil, errors.New("native inventory bytes differ from the selected reviewed digest")
	}
	if err := config.NativeInventory.Validate(); err != nil {
		return nil, err
	}
	if config.ControlRepository != config.NativeInventory.ControlRepository() {
		return nil, errors.New("native collection control repository differs from the reviewed inventory")
	}
	strict, err := acquisition.RequireInventory(*config.NativeInventory)
	if err != nil {
		return nil, err
	}
	client, err := githubapp.New(githubapp.Config{AppID: config.AppID, PrivateKeyPEM: config.PrivateKeyPEM,
		BaseURL: config.BaseURL, UploadURL: config.UploadURL, Transport: config.Transport})
	if err != nil {
		return nil, err
	}
	if err := client.ValidateRepositoryAccess(ctx, config.ControlRepository); err != nil {
		return nil, fmt.Errorf("validate native control repository visibility: %w", err)
	}
	provider := collectnative.GitHub{BaseURL: config.BaseURL, Tokens: client, Quota: quota, App: quotaApp,
		HTTP: &http.Client{Transport: config.Transport, Timeout: time.Minute}}
	inventory, err := collect.NewNativeInventoryService(ctx, *config.NativeInventory, provider, acquisition)
	if err != nil {
		return nil, err
	}
	native.acquisition, native.inventory = strict, inventory
	native.collector = collectnative.Collector{Provider: provider, Store: acquisition, Timeout: config.CollectionTimeout}
	if err := native.collector.Validate(); err != nil {
		return nil, err
	}
	native.worker = collect.NativeWorker{Queue: queue, Enrollment: enrollment, Client: client,
		Inventory: inventory, Values: acquisition, Outcomes: acquisition,
		WindowDays: config.WindowDays, RefreshInterval: config.RepositoryRefreshInterval,
		ScopeRefresher: collect.ScopeRefresher{Client: client, Quota: quota, QuotaApp: quotaApp, RepositoryLimit: config.InventoryLimit},
		Runs: collect.RunWorker{Collector: native, Discovery: provider, Scopes: enrollment.Authority,
			Publications: strict, AcquisitionTimeout: config.CollectionTimeout}}
	if config.NativeValues {
		search, err := githubquota.New(services.GitHubQuota, githubquota.Options{SafetyReserve: 1})
		if err != nil {
			return nil, err
		}
		values := &collect.NativeValueService{Projector: collect.Projector{
			Data: data, State: services.State, Leases: services.Leases, Enrollment: enrollment,
			CatalogRoot: config.CatalogRoot, PolicyPath: config.PolicyPath,
			GitHubAPIURL: config.BaseURL, Tokens: client, QuotaApp: quotaApp,
			RemoteQuota: quota, RemoteSearchQuota: search, RemoteValuePlan: &collect.RemoteValuePlanCache{},
			RemoteHTTP: &http.Client{Transport: config.Transport, Timeout: 65 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
			MinInterval: config.MinProjectionInterval, InventoryRepositoryLimit: config.InventoryLimit,
		}}
		if err := values.Validate(); err != nil {
			return nil, fmt.Errorf("configure selected native Value lane: %w", err)
		}
		native.values, native.worker.Values = values, values
	}
	collector.client = client
	collector.backfill.Enumerator = client
	collector.backfill.NativeInventory = inventory
	collector.backfill.NativePublications = strict
	collector.reporter.Backfill = collector.backfill
	collector.replayer = collect.DeliveryReplayer{State: services.State, Metadata: services.Collection,
		Client: client, Enabled: config.RecoverDeliveries}
	return collector, nil
}

func (n *nativeCollection) CollectScopedAttempt(ctx context.Context, request collectnative.ScopedAttempt) error {
	collector := n.collector
	collector.Destinations = slices.Clone(collector.Destinations)
	if n.archive != nil {
		destination, err := n.archive.NativeDestination(collectnative.EnrollmentScope{
			Repository: request.Repository, InstallationID: request.InstallationID, Epoch: request.Epoch,
		})
		if err != nil {
			return err
		}
		collector.Destinations = append(collector.Destinations, destination)
	}
	return collector.CollectScopedAttempt(ctx, request)
}

// Configure before any worker starts; all role/admission copies must use the
// same source authority so approved trace erasure shares its ownership commit.
func (c *Collector) nativeAuthority(authority collectnative.EnrollmentAuthority) {
	c.enrollment.Authority = authority
	c.admitter.Enrollment = c.enrollment
	c.backfill.Enrollment = c.enrollment
	c.backfill.AcquisitionScopes = authority
	c.reporter.Enrollment = c.enrollment
	c.reporter.Backfill = c.backfill
	c.native.worker.Enrollment = c.enrollment
	c.native.worker.Runs.Scopes = authority
	if c.native.values != nil {
		c.native.values.Projector.Enrollment = c.enrollment
	}
}
