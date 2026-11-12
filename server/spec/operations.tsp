import "@typespec/http";
import "@typespec/json-schema";

using TypeSpec.Http;
using TypeSpec.JsonSchema;
namespace Cao;

model RebuildStatus {
  state: string;
  required: boolean;
  /** Server-generated lifecycle correlation, independent of the private lease owner token. */
  rebuildId?: string;
  /** pending, recorded (durable), local (non-durable capability-level evidence), or unavailable. Publication success does not imply terminal evidence was persisted. */
  auditState?: string;
  startedAt?: string;
  completedAt?: string;
  revision?: int64;
  dataRevision?: string;
  /** Retained collection counts after canonical Audit curation and relationship validation; not transport input counts. */
  counts?: Record<int32>;
}
@jsonSchema
@example(#{accepted: true, duplicate: true})
model WebhookAcknowledgement {
  accepted: true;
  duplicate?: boolean;
  ignored?: boolean;
  reason?: string;
  kind?: string;
  queued?: boolean;
}
/** Bounded in-memory counters for requests that reached this process's webhook handler. Reset on restart, not aggregated across replicas. Accepted means HTTP acknowledgement, not completed reconciliation or verified GitHub-side registration. */
@example(#{configured: true, scope: "process", received: 3, inFlight: 0, verified: 2, accepted: 2, rejected: 1, failed: 0, duplicates: 0, pings: 1})
model WebhookStatus {
  configured: boolean;
  scope: "process";
  received: int64;
  inFlight: int64;
  verified: int64;
  accepted: int64;
  rejected: int64;
  failed: int64;
  duplicates: int64;
  pings: int64;
  lastReceivedAt?: utcDateTime;
  lastAcceptedAt?: utcDateTime;
  lastFailureAt?: utcDateTime;
  lastPingAt?: utcDateTime;
}
model CollectionStatus {
  configured: boolean;
  health: string;
  coverage: { installations: int64; repositories: int64 };
  queueDepth: int64;
  pendingTasks: int64;
  oldestPendingAge?: string;
  deadLetters: int64;
  backfill: string;
  backfillFailures: int32;
  backfillQueuedRunTasks: int32;
  lastProjected?: string;
  counters: Record<int64>;
  /** Half-life 60 seconds: each event contributes one unit, decaying by half every minute. Keys are webhook, collection, and failure. */
  load?: Record<float64>;
  healthRevision: int64;
  lastWebhookAt?: string;
  lastFailureAt?: string;
  lastFailureCode?: string;
  lastSuccessAt?: string;
  rateLimits?: { installationId: int64; remaining: int32; parkedUntil?: string }[];
}
model QuotaBucket { app: string; installation: int64; resource: string; }
model QuotaPoint {
  time: utcDateTime;
  bucket: QuotaBucket;
  limit: int32;
  used: int32;
  reserved: int32;
  usagePercent: float64;
}
model QuotaAggregate {
  time: utcDateTime;
  buckets: int32;
  limit: int32;
  used: int32;
  reserved: int32;
  usagePercent: float64;
}
model QuotaUsage {
  generatedAt: utcDateTime;
  from: utcDateTime;
  intervalSeconds: int32;
  buckets: QuotaPoint[];
  aggregate: QuotaAggregate[];
}
@example(#{timestamp: utcDateTime.fromISO("2026-10-02T19:00:00Z"), `namespace`: "cao:server", message: "listener ready"})
model ServerLogRecord {
  timestamp: utcDateTime;
  `namespace`: string;
  message: string;
}
/** Only fixed, numeric ingestion counters are returned; Redis keys and values are never exposed. */
model ServerLogRedis {
  status: "ok" | "not-configured" | "unavailable";
  counters?: Record<int64>;
  queueDepth?: int64;
  pendingTasks?: int64;
  deadLetters?: int64;
}
@example(#{logs: #[], redis: #{status: "ok", counters: #{webhookReceived: 3}, queueDepth: 0, pendingTasks: 0, deadLetters: 0}})
model ServerLogSnapshot {
  logs: ServerLogRecord[];
  redis: ServerLogRedis;
}

@tag("Ingestion and administration")
namespace Operations {
  /** Public signed GitHub delivery (maximum 10 MiB); validated with HMAC-SHA256 over the raw body. Repository created, deleted, archived and unarchived deliveries durably queue repository lifecycle updates; a successful acknowledgement does not mean the database update has completed. Never use dashboard bearer/session credentials for webhook authentication. The body is a GitHub event-specific JSON object. */
  @route("/api/github/webhook")
  @post op webhook(
    @header("X-Hub-Signature-256") signature: string,
    @header("X-GitHub-Delivery") delivery: string,
    @header("X-GitHub-Event") event: string,
    @body payload: JsonObject
  ): { @statusCode code: 202; @body body: WebhookAcknowledgement } |
    BadRequest | Unauthorized | Conflict | Unavailable;

  /** Start an asynchronous rebuild coordinated by the selected operational store; local bearer or hosted admin session and CSRF on hosted mutations. Hosted rebuilds require durable audit admission and start evidence; an unavailable audit service fails closed before reconciliation. Lifecycle events correlate actor, rebuildId, prior revision and terminal outcome without payloads. Canonical publication, audit appends and replaceable status are separate commits: terminal audit failures report auditState=unavailable where status storage remains available, never roll back a published revision, and require operator reconciliation of incomplete histories. Local bearer mode retains capability-level attribution, not personal identity or a durability guarantee. A volatile collector also reconstructs GitHub scope and reruns backfill, even with a populated lake, when durable audit is supplied. Canonical ingestion and existing-database maintenance curate Audits according to dashboard-data.md section 11.1, including unchanged artifact inputs. Changed retained data publishes a new revision; missing Run evidence, grader/eval identities, and actionable diagnostics remain retained. */
  @useAuth(DashboardAuth)
  @route("/api/admin/rebuild")
  @post op rebuild(@header("X-CSRF-Token") csrfToken?: string):
    { @statusCode code: 202; @body body: RebuildStatus } |
    Unauthorized | Forbidden | Conflict | Unavailable;

  @useAuth(DashboardAuth)
  @route("/api/admin/rebuild/status")
  @get op rebuildStatus(): RebuildStatus | Unauthorized | Unavailable;

  /** Local `gh cao dashboard` only: asks the owning CLI process to start a bounded collection pass now. Requires the local bearer capability; not registered for hosted OAuth deployments. Requests coalesce into at most one pending pass and never widen repository scope, rollout mode or the GitHub API quota reserve. Acceptance does not mean collection has finished; progress is reported through the local ingestion status. Returns 404 when no native ingestion adapter is attached and 502 when the owning process is unavailable or rejects the request. */
  @useAuth(BearerAuth)
  @route("/api/v1/ingestion/start")
  @post op startIngestion():
    { @statusCode code: 202; @body body: { requested: true } } |
    Unauthorized | NotFound | Failure<502>;

  /** Administrator-only; returns not-configured when no collector is active. */
  @useAuth(DashboardAuth)
  @route("/api/admin/collection/status")
  @get op collectionStatus(): CollectionStatus | Unauthorized | Forbidden | Unavailable;

  /** Alias for the administrative collection status endpoint. */
  @useAuth(DashboardAuth)
  @route("/api/v1/ingestion/health")
  @get op ingestionHealth(): CollectionStatus | Unauthorized | Forbidden | Unavailable;

  /** Administrator-only live webhook status. Reads process-local counters without querying GitHub, the evidence database, collection queues or durable ingestion metrics; normal authentication and rate limiting still apply. Verified counts valid HMAC signatures, including requests later rejected for missing headers. Pings counts acknowledged, signed GitHub ping deliveries, not proof of current remote webhook configuration. Middleware rejections are outside these counters. */
  @useAuth(DashboardAuth)
  @route("/api/v1/ingestion/webhook/status")
  @get op webhookStatus(): { @header cacheControl: "no-store"; @body body: WebhookStatus } |
    Unauthorized | Forbidden | Unavailable;

  /** Administrator-only retained GitHub API usage; does not expose credentials. */
  @useAuth(DashboardAuth)
  @route("/api/v1/github-quota/usage")
  @get op quotaUsage(): QuotaUsage | Unauthorized | Forbidden | Unavailable;

  /** Available only when CAO_SERVER_LOGS_ENABLED=true. Returns up to 10,000 recent server debug log records and fixed Redis ingestion counters (or availability status); requires a local bearer capability, a hosted administrator session, or the hosted MCP GitHub Actions identity. */
  @useAuth(DashboardAuth)
  @route("/api/admin/logs")
  @get op logs(@header("X-GitHub-Actor") githubActor?: string, @header("X-GitHub-OIDC-Token") githubOIDCToken?: string):
    { @header contentType: "application/json"; @header contentDisposition: "attachment; filename=\"cao-server-logs.json\""; @body body: ServerLogSnapshot } |
    Unauthorized | Forbidden | Unavailable;
}
