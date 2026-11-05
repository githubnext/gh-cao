package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
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
		AdmitOnly:         profile.admitOnly,
		LakeDirectory:     d.getenv("CAO_COLLECT_LAKE_DIRECTORY"),
		CatalogRoot:       d.getenv("CAO_COLLECT_CATALOG_ROOT"),
		ControlRepository: d.getenv("CAO_COLLECT_CONTROL_REPOSITORY"),
		PolicyPath:        d.getenv("CAO_POLICY_PATH"),
		StaticInventoryPath: d.getenv(
			"CAO_COLLECT_STATIC_INVENTORY",
		),
		Workers:           intEnv(d.getenv("CAO_COLLECT_WORKERS")),
		RecoverDeliveries: envTruthy(d.getenv("CAO_COLLECT_RECOVER_DELIVERIES")),
	}
	if err := config.ApplyNativeEnvironment(d.getenv); err != nil {
		return failed(id, areaCollect, title, err)
	}
	keySource, keyPresent := "absent", false
	var keyErr error
	if !config.NativeAcquisition || !config.AdmitOnly {
		keySource, keyPresent, keyErr = d.privateKeySource()
	}
	details := []Detail{
		detail("appId", fmt.Sprint(appID)),
		detail("admitOnly", fmt.Sprint(profile.admitOnly)),
		detail("privateKey", keySource),
		detail("webhookSecret", presence(d.getenv("CAO_GITHUB_WEBHOOK_SECRET"))),
		detail("lakeDirectory", presence(d.getenv("CAO_COLLECT_LAKE_DIRECTORY"))),
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
			remedy:  "run `cao-dashboard backfill` so an empty database is repopulated from installations and the evidence lake",
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
		detail("lakeReplayed", fmt.Sprint(state.LakeReplayed)),
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

// lakePopulationReason names why checkLake reached its status, stable across
// summary wording changes so it is useful to log without exposing the
// reported byte and run-shard counts.
type lakePopulationReason string

const (
	lakePopulationReasonEmpty     lakePopulationReason = "empty"
	lakePopulationReasonPopulated lakePopulationReason = "populated"
)

// lakePopulationClassification is the status, summary, and remedy
// classifyLakePopulation derives from the evidence lake's populated flag and
// reported byte and run-shard counts.
type lakePopulationClassification struct {
	status  Status
	summary string
	remedy  string
	reason  lakePopulationReason
}

// classifyLakePopulation decides the collect.lake check's outcome from the
// lake's populated flag and reported size alone. It is a pure function so
// the empty-versus-populated distinction is testable without a real
// directory tree on disk.
func classifyLakePopulation(populated bool, totalBytes int64, runShards int) lakePopulationClassification {
	if !populated {
		return lakePopulationClassification{
			status:  StatusWarn,
			summary: "the evidence lake holds no replayable evidence",
			remedy:  "until it is populated, a cold start must re-collect from GitHub rather than replay locally",
			reason:  lakePopulationReasonEmpty,
		}
	}
	return lakePopulationClassification{
		status: StatusPass,
		summary: fmt.Sprintf("%s of replayable evidence across %d run shards",
			humanBytes(totalBytes), runShards),
		reason: lakePopulationReasonPopulated,
	}
}

// checkLake inspects the evidence lake, which is what makes a cold start a
// local replay instead of a full re-collection from GitHub.
func (d Doctor) checkLake(context.Context) Check {
	const id, title = "collect.lake", "Evidence lake"
	profile := d.profile()
	if !profile.collecting {
		return skipped(id, areaCollect, title, "collection is not configured")
	}
	if profile.admitOnly {
		return skipped(id, areaCollect, title, "admission-only collection holds no evidence lake")
	}
	if envTruthy(d.getenv("CAO_COLLECT_NATIVE")) {
		return skipped(id, areaCollect, title, "native collection stores durable evidence in PostgreSQL, not an Activity lake")
	}
	directory := d.getenv("CAO_COLLECT_LAKE_DIRECTORY")
	if directory == "" {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "no evidence lake directory is configured",
			Remedy:  "set CAO_COLLECT_LAKE_DIRECTORY to a durable absolute path",
		}
	}
	lake := collect.Lake{Directory: directory}
	if err := lake.Validate(); err != nil {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "the evidence lake directory is not usable: " + err.Error(),
			Details: []Detail{detail("directory", directory)},
			Remedy:  "the lake must be an absolute path on durable storage",
		}
	}
	info, err := os.Stat(directory)
	if err != nil {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusFail,
			Summary: "the evidence lake directory could not be read: " + err.Error(),
			Details: []Detail{detail("directory", directory)},
			Remedy:  "confirm the volume is mounted and writable by this process",
		}
	}
	shards, shardBytes := directorySize(lake.ShardDirectory())
	runs, runBytes := directorySize(lake.RunsDirectory())
	records, recordBytes := directorySize(lake.RecordsDirectory())
	details := []Detail{
		detail("directory", directory),
		detail("mode", info.Mode().Perm().String()),
		detail("shardFiles", fmt.Sprint(shards)),
		detail("runFiles", fmt.Sprint(runs)),
		detail("recordFiles", fmt.Sprint(records)),
		detail("size", humanBytes(shardBytes+runBytes+recordBytes)),
		detail("manifest", filePresence(lake.ManifestPath())),
		detail("inventory", filePresence(lake.InventoryPath())),
	}
	populated, err := lake.Populated()
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	classification := classifyLakePopulation(populated, shardBytes+runBytes+recordBytes, runs)
	doctorLog.Printf("evidence lake classified status=%s reason=%s", classification.status, classification.reason)
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: classification.status,
		Summary: classification.summary, Details: details, Remedy: classification.remedy,
	}
}

// checkProjectionLock reports whether an ingestion is in flight. The lock has
// a time to live, so a held lock is information rather than a fault.
func (d Doctor) checkProjectionLock(ctx context.Context) Check {
	const id, title = "collect.projection", "Ingestion lock"
	if d.profile().collecting && envTruthy(d.getenv("CAO_COLLECT_NATIVE")) {
		return skipped(id, areaCollect, title, "native publication uses PostgreSQL attempt/source fences, not the legacy projection lock")
	}
	if skip, ok := d.storeUnavailable(id, areaCollect, title); ok {
		return skip
	}
	held, err := d.Store.OperationalServices().Leases.LockHeld(ctx, "projection")
	if err != nil {
		return failed(id, areaCollect, title, err)
	}
	if held {
		return Check{
			ID: id, Area: areaCollect, Title: title, Status: StatusPass,
			Summary: "an ingestion currently holds the lock",
			Details: []Detail{detail("held", "true")},
		}
	}
	return Check{
		ID: id, Area: areaCollect, Title: title, Status: StatusPass,
		Summary: "no ingestion is in flight",
		Details: []Detail{detail("held", "false")},
	}
}

// toolingLookup are the PATH and filesystem probes missingTooling needs,
// injected so the missing-tool detection is testable without depending on
// which binaries or files happen to exist on the test runner.
type toolingLookup struct {
	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
}

// missingTooling reports which collection-tooling dependencies (Node, gh, and
// the activity CLI script) are unavailable, using the injected lookup. It is
// a pure function so every combination of missing tool is testable without
// touching the real PATH or filesystem.
func missingTooling(lookup toolingLookup, node, gh, script string) []string {
	var missing []string
	if _, err := lookup.lookPath(node); err != nil {
		missing = append(missing, "node")
	}
	if _, err := lookup.lookPath(gh); err != nil {
		missing = append(missing, "gh")
	}
	if script == "" {
		missing = append(missing, "catalog root")
	} else if _, err := lookup.stat(script); err != nil {
		missing = append(missing, "activity/cao.mjs")
	}
	return missing
}

// checkTooling confirms the external programs collection shells out to are
// actually present, which is the difference between a working collector and
// one that fails on its first task.
func (d Doctor) checkTooling(context.Context) Check {
	const id, title = "runtime.tooling", "Collection tooling"
	profile := d.profile()
	if !profile.collecting || profile.admitOnly {
		return skipped(id, areaRuntime, title, "this process does not run the collection CLI")
	}
	var config server.CollectorConfig
	if err := config.ApplyNativeEnvironment(d.getenv); err != nil {
		return failed(id, areaRuntime, title, err)
	}
	if config.NativeAcquisition {
		if !config.NativeValues {
			return Check{ID: id, Area: areaRuntime, Title: title, Status: StatusPass,
				Summary: "native Go collection requires no Node, gh or Activity CLI"}
		}
		values := collect.NativeValueService{Projector: collect.Projector{
			CatalogRoot: d.getenv("CAO_COLLECT_CATALOG_ROOT"), PolicyPath: d.getenv("CAO_POLICY_PATH"),
			NodeBinary: d.getenv("CAO_COLLECT_NODE_BINARY"),
		}}
		if err := values.ValidateRuntime(); err != nil {
			return failed(id, areaRuntime, title, err)
		}
		return Check{ID: id, Area: areaRuntime, Title: title, Status: StatusPass,
			Summary: "selected Value execution has Node and reviewed policy/catalog assets; native collection needs no Activity CLI"}
	}
	node := orDefault(d.getenv("CAO_COLLECT_NODE_BINARY"), "node")
	gh := orDefault(d.getenv("CAO_COLLECT_GH_BINARY"), "gh")
	catalog := d.getenv("CAO_COLLECT_CATALOG_ROOT")
	script := ""
	if catalog != "" {
		script = filepath.Join(catalog, "activity", "cao.mjs")
	}
	details := []Detail{
		detail("node", binaryPresence(node)),
		detail("gh", binaryPresence(gh)),
		detail("activityCli", orDefault(filePresence(script), "(no catalog root configured)")),
	}
	missing := missingTooling(toolingLookup{lookPath: exec.LookPath, stat: os.Stat}, node, gh, script)
	doctorLog.Printf("collection tooling checked missing_count=%d", len(missing))
	if len(missing) > 0 {
		return Check{
			ID: id, Area: areaRuntime, Title: title, Status: StatusFail,
			Summary: "collection tooling is missing: " + strings.Join(missing, ", "),
			Details: details,
			Remedy:  "collection shells out to these; without them every task fails at the first step",
		}
	}
	return Check{
		ID: id, Area: areaRuntime, Title: title, Status: StatusPass,
		Summary: "node, gh, and the activity CLI are all available",
		Details: details,
	}
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

func filePresence(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("present (%s, modified %s)", humanBytes(info.Size()),
		info.ModTime().UTC().Format(time.RFC3339))
}

func binaryPresence(name string) string {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return name + " (not found)"
	}
	return resolved
}

// directorySizeReadFailure classifies why directorySize's os.ReadDir call did
// not return entries, so an unreadable lake directory is diagnosable without
// logging the directory path itself.
type directorySizeReadFailure string

const (
	directorySizeReadFailureNone    directorySizeReadFailure = "none"
	directorySizeReadFailureMissing directorySizeReadFailure = "missing"
	directorySizeReadFailureOther   directorySizeReadFailure = "other"
)

// classifyDirectorySizeReadFailure distinguishes a directory that does not
// exist, which is the unpopulated-lake state checkLake already reports, from
// any other read failure, such as a permissions error, which is worth
// surfacing as a diagnostic. It is a pure function extracted from
// directorySize so this distinction is testable against constructed errors
// without touching the filesystem.
func classifyDirectorySizeReadFailure(err error) directorySizeReadFailure {
	switch {
	case err == nil:
		return directorySizeReadFailureNone
	case os.IsNotExist(err):
		return directorySizeReadFailureMissing
	default:
		return directorySizeReadFailureOther
	}
}

// directorySize counts the files and bytes in one lake directory. A missing
// directory reports zero rather than an error, because an unpopulated lake is
// a state the caller reports, not a failure to observe it. Any other read
// failure, such as a permissions error, is logged so it is diagnosable
// without being promoted to a check failure.
func directorySize(directory string) (files int, bytes int64) {
	entries, err := os.ReadDir(directory)
	if failure := classifyDirectorySizeReadFailure(err); failure != directorySizeReadFailureNone {
		if failure == directorySizeReadFailureOther {
			doctorLog.Printf("lake directory read failed")
		}
		return 0, 0
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files++
		bytes += info.Size()
	}
	return files, bytes
}
