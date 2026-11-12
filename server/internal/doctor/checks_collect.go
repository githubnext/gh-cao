package doctor

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/server"
)

const areaCollect = "collect"

// checkCollectionProfile reports which acquisition profile the environment
// selects, and fails when it selects both.
//
// The two profiles are alternatives, never layers. A process configured for
// both would collect from GitHub and ingest published artifacts into the same
// current Postgres sources.
func (d Doctor) checkCollectionProfile(context.Context) Check {
	const id, title = "collect.profile", "Acquisition profile"
	profile := d.profile()
	details := []Detail{
		detail("profile", profile.label),
		detail("collectAppId", presence(d.getenv("CAO_COLLECT_APP_ID"))),
		detail("sourceDirectory", presence(d.getenv("CAO_SOURCE_DIRECTORY"))),
		detail("admitOnly", fmt.Sprint(profile.admitOnly)),
	}
	var native server.CollectorConfig
	if err := native.ApplyNativeEnvironment(d.getenv); err != nil {
		return failed(id, areaCollect, title, err)
	}
	if !profile.collecting && (native.NativeAcquisition || native.NativeValues || native.NativeInventoryDigest != "") {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "native collection features require CAO_COLLECT_APP_ID",
			Details: details,
			Remedy:  "configure the collection App identifier or unset native collection features",
		}
	}
	if profile.conflict {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "both collection and published-snapshot ingestion are configured",
			Details: details,
			Remedy:  "the profiles are alternatives; unset CAO_SOURCE_DIRECTORY to collect, or unset CAO_COLLECT_APP_ID to ingest snapshots",
		}
	}
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: StatusPass,
		Summary: "this process runs the " + profile.label + " profile",
		Details: details,
	}
}

// checkCollectionSettings validates the collection environment against the
// real CollectorConfig rules.
//
// It never reads key material: a configured private key is represented by a
// placeholder, because Validate only asks whether one is present. That keeps
// the diagnostic from pulling a secret into a process that has no use for it.
func (d Doctor) checkCollectionSettings(context.Context) Check {
	const id, title = "collect.settings", "Collection settings"
	profile := d.profile()
	if !profile.collecting {
		return skipped(id, areaCollect, title, "collection is not configured; this server runs the Actions profile")
	}
	appID, appIDErr := strconv.ParseInt(d.getenv("CAO_COLLECT_APP_ID"), 10, 64)
	if appIDErr != nil || appID <= 0 {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "CAO_COLLECT_APP_ID is not a positive GitHub App identifier",
			Remedy:  "set CAO_COLLECT_APP_ID to the numeric App identifier",
		}
	}
	config := server.CollectorConfig{
		AppID:             appID,
		CatalogRoot:       d.getenv("CAO_COLLECT_CATALOG_ROOT"),
		ControlRepository: d.getenv("CAO_COLLECT_CONTROL_REPOSITORY"),
		PolicyPath:        d.getenv("CAO_POLICY_PATH"),
		Workers:           intEnv(d.getenv("CAO_COLLECT_WORKERS")),
	}
	if err := config.ApplyNativeEnvironment(d.getenv); err != nil {
		return failed(id, areaCollect, title, err)
	}
	if !config.NativeAcquisition {
		return failed(id, areaCollect, title, config.Validate())
	}
	keySource, keyPresent := "absent", false
	var keyErr error
	if !config.AdmitOnly {
		keySource, keyPresent, keyErr = d.privateKeySource()
	}
	details := []Detail{
		detail("appId", fmt.Sprint(appID)),
		detail("admitOnly", fmt.Sprint(profile.admitOnly)),
		detail("privateKey", keySource),
		detail("webhookSecret", presence(d.getenv("CAO_GITHUB_WEBHOOK_SECRET"))),
		detail("catalogRoot", presence(d.getenv("CAO_COLLECT_CATALOG_ROOT"))),
		detail("controlRepository", presence(d.getenv("CAO_COLLECT_CONTROL_REPOSITORY"))),
		detail("workers", orDefault(d.getenv("CAO_COLLECT_WORKERS"), "0")),
		detail("queueMaxLength", orDefault(d.getenv("CAO_COLLECT_QUEUE_MAX_LENGTH"), "default")),
		detail("inventoryLimit", orDefault(d.getenv("CAO_COLLECT_INVENTORY_LIMIT"), "unbounded")),
		detail("projectionInterval", orDefault(d.getenv("CAO_COLLECT_PROJECTION_INTERVAL"), "default")),
	}
	if keyErr != nil {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "the configured private key file is unusable: " + keyErr.Error(),
			Details: details,
			Remedy:  "confirm CAO_COLLECT_PRIVATE_KEY_FILE points at a readable mounted secret",
		}
	}
	// A placeholder stands in for the key: Validate only tests for presence,
	// so the real bytes are never read here.
	if keyPresent {
		config.PrivateKeyPEM = []byte("configured")
	}
	if err := config.Validate(); err != nil {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "the collection configuration is not usable: " + err.Error(),
			Details: details,
			Remedy:  "correct the CAO_COLLECT_* environment before starting the collector",
		}
	}
	classification := classifyCollectionSettings(d.getenv("CAO_GITHUB_WEBHOOK_SECRET") != "", profile.admitOnly, appID)
	doctorLog.Printf("collection settings classified status=%s reason=%s", classification.status, classification.reason)
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: classification.status,
		Summary: classification.summary, Details: details, Remedy: classification.remedy,
	}
}

// checkCollectionStorage verifies the durable canonical store needed by
// native admission, acquisition, and publication.
func (d Doctor) checkCollectionStorage(context.Context) Check {
	const id, title = "collect.storage", "Collection storage"
	if !d.profile().collecting {
		return skipped(id, areaCollect, title, "collection is not configured; this server runs the Actions profile")
	}
	if d.Backend != "" && d.Backend != "postgres" {
		return failed(id, areaCollect, title,
			fmt.Errorf("native collection requires PostgreSQL operational storage, not %s", d.Backend))
	}
	if err := operational.CheckStore(d.Store); err != nil {
		return failed(id, areaCollect, title,
			fmt.Errorf("native collection requires durable PostgreSQL operational storage: %w", err))
	}
	capability := d.Store.Capabilities().Collection
	if capability.Scope != operational.ScopeDeployment || capability.Persistence != operational.PersistenceRestart {
		return failed(id, areaCollect, title,
			fmt.Errorf("%w: native collection requires durable deployment-scoped PostgreSQL operational storage", operational.ErrUnsupported))
	}
	if d.Postgres == nil || !d.Postgres.Open() {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "native collection requires an open, durable deployment-scoped PostgreSQL store",
			Remedy:  "configure and open the deployment's PostgreSQL dashboard store before starting collection",
		}
	}
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: StatusPass,
		Summary: "native collection uses the deployment's PostgreSQL store",
	}
}

// collectionSettingsReason names why classifyCollectionSettings reached its
// result, stable across summary wording changes so it is useful to log
// without exposing the configured App identifier.
type collectionSettingsReason string

const (
	collectionSettingsReasonNoSecretAdmitOnly collectionSettingsReason = "no-webhook-secret-admit-only" // #nosec G101 -- diagnostic reason, not a credential
	collectionSettingsReasonNoSecretWorker    collectionSettingsReason = "no-webhook-secret-worker"     // #nosec G101 -- diagnostic reason, not a credential
	collectionSettingsReasonAdmitOnly         collectionSettingsReason = "admit-only"
	collectionSettingsReasonConfigured        collectionSettingsReason = "configured"
)

// collectionSettingsClassification is the status, summary, remedy, and
// stable reason classifyCollectionSettings derives from the already
// validated collection settings.
type collectionSettingsClassification struct {
	status  Status
	summary string
	remedy  string
	reason  collectionSettingsReason
}

// classifyCollectionSettings decides the collect.settings check's final
// outcome once CollectorConfig.Validate has already succeeded: a missing
// webhook secret is a failure in an admission-capable process (deliveries
// cannot be verified), a warning in a worker-only process, and otherwise the
// process reports whether it holds an App private key or runs
// admission-only. It is a pure function so each outcome is testable without
// a real CollectorConfig or environment.
func classifyCollectionSettings(webhookSecretPresent, admitOnly bool, appID int64) collectionSettingsClassification {
	if !webhookSecretPresent && admitOnly {
		// Without a secret the server cannot verify a delivery, so admission
		// has no way to distinguish GitHub from anyone else.
		return collectionSettingsClassification{
			status:  StatusFail,
			summary: "no webhook secret is configured; deliveries cannot be verified",
			remedy:  "set CAO_GITHUB_WEBHOOK_SECRET to the App's webhook secret",
			reason:  collectionSettingsReasonNoSecretAdmitOnly,
		}
	}
	if !webhookSecretPresent {
		return collectionSettingsClassification{
			status:  StatusWarn,
			summary: "collection workers are configured, but this environment cannot admit webhooks",
			remedy:  "this is expected in a worker-only process; a process that serves the webhook endpoint must configure CAO_GITHUB_WEBHOOK_SECRET",
			reason:  collectionSettingsReasonNoSecretWorker,
		}
	}
	if admitOnly {
		return collectionSettingsClassification{
			status:  StatusPass,
			summary: "admission-only collection is configured and holds no App private key",
			reason:  collectionSettingsReasonAdmitOnly,
		}
	}
	return collectionSettingsClassification{
		status:  StatusPass,
		summary: fmt.Sprintf("collection is configured for App %d", appID),
		reason:  collectionSettingsReasonConfigured,
	}
}

// privateKeySource reports where the App private key comes from without
// reading it. The file is stat-ed, never opened.
func (d Doctor) privateKeySource() (label string, present bool, err error) {
	result := resolvePrivateKeySource(
		os.Stat,
		d.getenv("CAO_COLLECT_PRIVATE_KEY_FILE"),
		d.getenv("CAO_COLLECT_PRIVATE_KEY_BASE64"),
		d.getenv("CAO_COLLECT_PRIVATE_KEY"),
	)
	doctorLog.Printf("private key source resolved reason=%s present=%t", result.reason, result.present)
	return result.label, result.present, result.err
}

// privateKeySourceReason names why resolvePrivateKeySource reached its
// result, stable across label wording changes so it is useful to log
// without exposing the configured file path.
type privateKeySourceReason string

const (
	privateKeySourceReasonFileConfigured   privateKeySourceReason = "file-configured"
	privateKeySourceReasonFileUnreadable   privateKeySourceReason = "file-unreadable"
	privateKeySourceReasonFileNotAFile     privateKeySourceReason = "file-not-a-file"
	privateKeySourceReasonBase64Configured privateKeySourceReason = "base64-configured"
	privateKeySourceReasonInlineConfigured privateKeySourceReason = "inline-configured"
	privateKeySourceReasonAbsent           privateKeySourceReason = "absent"
)

// privateKeySourceResult is the label, presence, error, and stable reason
// resolvePrivateKeySource derives from the configured private key inputs.
type privateKeySourceResult struct {
	label   string
	present bool
	err     error
	reason  privateKeySourceReason
}

// resolvePrivateKeySource applies the standard priority for the collection
// App's private key: a configured file path, stat-ed but never opened, then a
// base64 environment variable, then an inline PEM, then absent.
func resolvePrivateKeySource(
	stat func(string) (os.FileInfo, error),
	filePath, base64Value, inlineValue string,
) privateKeySourceResult {
	if filePath != "" {
		info, statErr := stat(filePath)
		if statErr != nil {
			return privateKeySourceResult{label: "file (unreadable)", err: statErr, reason: privateKeySourceReasonFileUnreadable}
		}
		if info.IsDir() {
			return privateKeySourceResult{
				label:  "file (not a file)",
				err:    fmt.Errorf("%s is a directory", filePath),
				reason: privateKeySourceReasonFileNotAFile,
			}
		}
		return privateKeySourceResult{label: "file (configured)", present: true, reason: privateKeySourceReasonFileConfigured}
	}
	if base64Value != "" {
		return privateKeySourceResult{
			label: "base64 environment variable (configured)", present: true, reason: privateKeySourceReasonBase64Configured,
		}
	}
	if inlineValue != "" {
		return privateKeySourceResult{
			label: "inline environment variable (configured)", present: true, reason: privateKeySourceReasonInlineConfigured,
		}
	}
	return privateKeySourceResult{label: "absent", reason: privateKeySourceReasonAbsent}
}

func (d Doctor) collectEnrollment() collect.Enrollment {
	services := d.Store.OperationalServices()
	return collect.Enrollment{Metadata: services.Collection, Leases: services.Leases}
}

func (d Doctor) collectQueue() collect.Queue {
	services := d.Store.OperationalServices()
	return collect.Queue{
		Tasks: services.Queue, Admission: services.Admission, Metadata: services.Collection,
		Leases: services.Leases, Deliveries: services.Deliveries, Metrics: services.IngestionMetrics,
		MaxLength: int64(d.queueMaxLength()),
	}
}

func (d Doctor) queueMaxLength() int {
	if value := intEnv(d.getenv("CAO_COLLECT_QUEUE_MAX_LENGTH")); value > 0 {
		return value
	}
	return 200_000
}

func (d Doctor) checkEnrollment(ctx context.Context) Check {
	const id, title = "collect.enrollment", "Ingestion scope"
	if !d.profile().collecting {
		return skipped(id, areaCollect, title, "collection is not configured")
	}
	if skip, ok := d.storeUnavailable(id, areaCollect, title); ok {
		return skip
	}
	coverage, err := d.collectEnrollment().Coverage(ctx)
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	details := []Detail{
		detail("installations", fmt.Sprint(coverage.Installations)),
		detail("repositories", fmt.Sprint(coverage.Repositories)),
	}
	if coverage.Repositories == 0 {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusWarn,
			Summary: "no repositories are enrolled, so nothing is in ingestion scope",
			Details: details,
			Remedy:  "run `cao-dashboard backfill` to enumerate installations, or install the App on a repository",
		}
	}
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: StatusPass,
		Summary: fmt.Sprintf("%d repositories across %d installations", coverage.Repositories, coverage.Installations),
		Details: details,
	}
}

func (d Doctor) checkQueue(ctx context.Context) Check {
	const id, title = "collect.queue", "Collection queue"
	if !d.profile().collecting {
		return skipped(id, areaCollect, title, "collection is not configured")
	}
	if skip, ok := d.storeUnavailable(id, areaCollect, title); ok {
		return skip
	}
	queue := d.collectQueue()
	depth, err := queue.Depth(ctx)
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	pending, err := queue.Pending(ctx)
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	oldestPending, err := queue.OldestPendingAge(ctx)
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	deadLetters, err := queue.DeadLetters(ctx)
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	maximum := d.queueMaxLength()
	details := []Detail{
		detail("backlog", fmt.Sprint(depth)),
		detail("pending", fmt.Sprint(pending)),
		detail("oldestPendingAge", oldestPending.String()),
		detail("deadLetters", fmt.Sprint(deadLetters)),
		detail("maxLength", fmt.Sprint(maximum)),
	}
	classification := classifyQueueBacklog(depth, pending, deadLetters, maximum)
	doctorLog.Printf("collection queue classified status=%s reason=%s", classification.status, classification.reason)
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: classification.status,
		Summary: classification.summary, Details: details, Remedy: classification.remedy,
	}
}

// queueBacklogReason names why checkQueue reached its status, stable across
// summary wording changes so it is useful to log without exposing the
// summary's interpolated depth and count values.
type queueBacklogReason string

const (
	queueBacklogReasonDeadLetters   queueBacklogReason = "dead-letters"
	queueBacklogReasonNearMaxLength queueBacklogReason = "near-max-length"
	queueBacklogReasonHealthy       queueBacklogReason = "healthy"
)

// queueBacklogClassification is the status, summary, and remedy
// classifyQueueBacklog derives from the collection queue's reported depth,
// pending, dead-letter, and configured maximum-length counts.
type queueBacklogClassification struct {
	status  Status
	summary string
	remedy  string
	reason  queueBacklogReason
}

// classifyQueueBacklog decides the collect.queue check's outcome from the
// queue's reported counts alone. It is a pure function so every threshold —
// a non-zero dead-letter count and a backlog approaching the configured
// maximum length — is testable without a fake Redis-backed queue.
func classifyQueueBacklog(depth, pending, deadLetters int64, maximum int) queueBacklogClassification {
	if deadLetters > 0 {
		return queueBacklogClassification{
			status:  StatusWarn,
			summary: fmt.Sprintf("%d tasks exhausted their retries and were dead-lettered", deadLetters),
			remedy:  "these repositories are not being collected; inspect the dead-letter stream for the failing reason",
			reason:  queueBacklogReasonDeadLetters,
		}
	}
	// Admission fails closed at MaxLength, so a backlog approaching it means
	// GitHub will need to retry deliveries until workers catch up.
	if maximum > 0 && depth >= int64(maximum*8/10) {
		return queueBacklogClassification{
			status:  StatusWarn,
			summary: fmt.Sprintf("the backlog of %d is within twenty percent of the %d bound", depth, maximum),
			remedy:  "workers are not keeping up and webhook admission will apply backpressure; scale collection workers",
			reason:  queueBacklogReasonNearMaxLength,
		}
	}
	return queueBacklogClassification{
		status:  StatusPass,
		summary: fmt.Sprintf("backlog %d, pending %d, no dead letters", depth, pending),
		reason:  queueBacklogReasonHealthy,
	}
}

// backfillClassificationReason names why checkBackfill reached its status,
// stable across summary wording changes so it is useful to log without
// exposing the reported phase or error text.
type backfillClassificationReason string

const (
	backfillReasonErrored    backfillClassificationReason = "errored"
	backfillReasonNeverRun   backfillClassificationReason = "never-run"
	backfillReasonIncomplete backfillClassificationReason = "incomplete"
	backfillReasonComplete   backfillClassificationReason = "complete"
)

// backfillClassification is the status, summary, and remedy
// classifyBackfillState derives from a collect.BackfillState alone.
type backfillClassification struct {
	status  Status
	summary string
	remedy  string
	reason  backfillClassificationReason
}

// classifyBackfillState decides the collect.backfill check's outcome from a
// collect.BackfillState alone. It is a pure function so each outcome -- an
// errored run, a namespace that has never seeded, an incomplete phase, and a
// completed run -- is testable without a fake Redis-backed store. A reported
// error is checked first, matching the prior inline behavior.
func classifyBackfillState(state collect.BackfillState) backfillClassification {
	if state.Error != "" {
		return backfillClassification{
			status:  StatusFail,
			summary: "the last cold start reported an error",
			remedy:  "re-run `cao-dashboard backfill`; cold start is idempotent and resumes from its checkpoint",
			reason:  backfillReasonErrored,
		}
	}
	if state.Phase == "idle" {
		return backfillClassification{
			status:  StatusWarn,
			summary: "cold start has never run in this namespace",
			remedy:  "run `cao-dashboard backfill` to enumerate installations and enqueue native acquisition",
			reason:  backfillReasonNeverRun,
		}
	}
	if state.Phase != "complete" {
		return backfillClassification{
			status:  StatusWarn,
			summary: fmt.Sprintf("cold start is in the %q phase", state.Phase),
			remedy:  "cold start did not reach the complete phase; re-run `cao-dashboard backfill` to resume",
			reason:  backfillReasonIncomplete,
		}
	}
	return backfillClassification{
		status:  StatusPass,
		summary: fmt.Sprintf("cold start is in the %q phase", state.Phase),
		reason:  backfillReasonComplete,
	}
}

func (d Doctor) checkBackfill(ctx context.Context) Check {
	const id, title = "collect.backfill", "Cold start"
	if !d.profile().collecting {
		return skipped(id, areaCollect, title, "collection is not configured")
	}
	if skip, ok := d.storeUnavailable(id, areaCollect, title); ok {
		return skip
	}
	state, err := collect.Backfill{StateStore: d.Store.OperationalServices().State}.State(ctx)
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	details := []Detail{
		detail("phase", state.Phase),
		detail("installations", fmt.Sprint(state.Installations)),
		detail("repositories", fmt.Sprint(state.Repositories)),
		detail("queuedRepositories", fmt.Sprint(state.QueuedRepositories)),
		detail("startedAt", orDefault(state.StartedAt, "(never)")),
		detail("completedAt", orDefault(state.CompletedAt, "(not completed)")),
	}
	if state.Error != "" {
		details = append(details, detail("error", state.Error))
	}
	classification := classifyBackfillState(state)
	doctorLog.Printf("cold start classified status=%s reason=%s", classification.status, classification.reason)
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: classification.status,
		Summary: classification.summary, Details: details, Remedy: classification.remedy,
	}
}

// checkBudget reads the recorded GitHub rate-limit headroom. It reads Redis
// only: the doctor never spends a GitHub request to report on rate limits.
func (d Doctor) checkBudget(ctx context.Context) Check {
	const id, title = "collect.budget", "GitHub rate-limit headroom"
	profile := d.profile()
	if !profile.collecting {
		return skipped(id, areaCollect, title, "collection is not configured")
	}
	if profile.admitOnly {
		return skipped(id, areaCollect, title, "admission-only collection never calls GitHub")
	}
	if skip, ok := d.storeUnavailable(id, areaCollect, title); ok {
		return skip
	}
	if skip, ok := d.postgresUnavailable(id, areaCollect, title); ok {
		return skip
	}
	floor := intEnv(d.getenv("CAO_COLLECT_RATE_LIMIT_FLOOR"))
	services := d.Store.OperationalServices()
	reporter := collect.Reporter{
		Enrollment: d.collectEnrollment(),
		Queue:      d.collectQueue(),
		Backfill:   collect.Backfill{StateStore: services.State},
		Budget:     &githubapp.Budget{Metadata: services.Collection, Store: services.RateLimits, Floor: floor},
		Metrics:    services.IngestionMetrics,
		Data:       d.Postgres,
	}
	status, err := reporter.Snapshot(ctx)
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	if len(status.RateLimits) == 0 {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusSkip,
			Summary: "no rate-limit headroom has been recorded yet",
		}
	}
	details := make([]Detail, 0, len(status.RateLimits)+1)
	details = append(details, detail("floor", orDefault(fmt.Sprint(floor), "default")))
	for _, headroom := range status.RateLimits {
		label := fmt.Sprintf("%d remaining", headroom.Remaining)
		if headroom.ParkedUntil != "" {
			label += ", parked until " + headroom.ParkedUntil
		}
		details = append(details, detail("installation "+fmt.Sprint(headroom.InstallationID), label))
	}
	classification := classifyBudgetHeadroom(status.RateLimits, floor)
	doctorLog.Printf("rate-limit headroom classified status=%s reason=%s", classification.status, classification.reason)
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: classification.status,
		Summary: classification.summary, Details: details, Remedy: classification.remedy,
	}
}

// budgetHeadroomReason names why classifyBudgetHeadroom reached its status,
// stable across summary wording changes so it is useful to log without
// exposing the reported installation identifiers.
type budgetHeadroomReason string

const (
	budgetHeadroomReasonParked  budgetHeadroomReason = "parked"
	budgetHeadroomReasonLow     budgetHeadroomReason = "low"
	budgetHeadroomReasonHealthy budgetHeadroomReason = "healthy"
)

// budgetHeadroomClassification is the status, summary, and remedy
// classifyBudgetHeadroom derives from the recorded rate-limit headroom
// alone.
type budgetHeadroomClassification struct {
	status  Status
	summary string
	remedy  string
	reason  budgetHeadroomReason
}

// classifyBudgetHeadroom decides the collect.budget check's outcome from the
// recorded per-installation rate-limit headroom and the configured floor
// alone. It is a pure function extracted from checkBudget's inline loop so
// the parked, low, and healthy outcomes are each testable without a real
// Redis-backed budget store. A parked installation is reported first,
// matching the prior inline priority.
func classifyBudgetHeadroom(rateLimits []collect.Headroom, floor int) budgetHeadroomClassification {
	var parked, low []string
	for _, headroom := range rateLimits {
		if headroom.ParkedUntil != "" {
			parked = append(parked, fmt.Sprint(headroom.InstallationID))
		} else if floor > 0 && headroom.Remaining <= floor {
			low = append(low, fmt.Sprint(headroom.InstallationID))
		}
	}
	if len(parked) > 0 {
		return budgetHeadroomClassification{
			status:  StatusWarn,
			summary: fmt.Sprintf("%d installations are parked on a rate limit", len(parked)),
			remedy:  "collection for these installations is paused until the limit resets; this is the budget working, not a fault",
			reason:  budgetHeadroomReasonParked,
		}
	}
	if len(low) > 0 {
		return budgetHeadroomClassification{
			status:  StatusWarn,
			summary: fmt.Sprintf("%d installations are at or below the configured rate-limit floor", len(low)),
			remedy:  "collection will stop short rather than exhaust the installation; widen the window or lower the run limit",
			reason:  budgetHeadroomReasonLow,
		}
	}
	return budgetHeadroomClassification{
		status:  StatusPass,
		summary: fmt.Sprintf("%d installations have recorded rate-limit headroom", len(rateLimits)),
		reason:  budgetHeadroomReasonHealthy,
	}
}

// checkTooling checks only the optional Value lane's selected dependencies.
// Native acquisition itself does not need the selected Value dependencies.
func (d Doctor) checkTooling(context.Context) Check {
	const id, title = "runtime.tooling", "Value runtime dependencies"
	profile := d.profile()
	if !profile.collecting || profile.admitOnly {
		return skipped(id, areaRuntime, title, "this process does not execute native Value")
	}
	var config server.CollectorConfig
	if err := config.ApplyNativeEnvironment(d.getenv); err != nil {
		return failed(id, areaRuntime, title, err)
	}
	if !config.NativeAcquisition {
		return failed(id, areaRuntime, title, config.Validate())
	}
	if !config.NativeValues {
		return Check{ID: id, Area: areaRuntime, Title: title, Status: StatusPass,
			Summary: "native Go collection requires no Node or Activity CLI"}
	}
	values := collect.NativeValueService{
		CatalogRoot: d.getenv("CAO_COLLECT_CATALOG_ROOT"), PolicyPath: d.getenv("CAO_POLICY_PATH"),
	}
	if err := values.ValidateRuntime(); err != nil {
		return failed(id, areaRuntime, title, err)
	}
	return Check{ID: id, Area: areaRuntime, Title: title, Status: StatusPass,
		Summary: "selected Go Value execution has reviewed policy/catalog assets"}
}

func presence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "absent"
	}
	return "configured"
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func intEnv(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}
