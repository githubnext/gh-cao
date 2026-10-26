import "@typespec/http";
import "@typespec/json-schema";
import "./trace.tsp";

using TypeSpec.Http;
using TypeSpec.JsonSchema;
namespace Cao;

/** A trusted archive query template. No SQL/KQL, object names, filesystem paths, or cross-store joins are accepted. */
@jsonSchema
model SessionArchiveQuery {
  kind: "timeline" | "summary";
  /** Exact lowercase owner/repository in the reviewed archive and collection scope. */
  repository: string;
  /** Required positive GitHub run ID for timeline; forbidden for summary. Not a canonical Run ID or trace digest. */
  runId?: string;
  /** Required positive exact attempt for timeline; forbidden for summary. */
  attempt?: int64;
  /** Maximum complete output rows, 1-10000 (default 10000). Exceeding a limit fails, never truncates a full approved trace. */
  @minValue(1) @maxValue(10000) limit?: int32;
}

model SessionArchiveSummary {
  repository: string;
  runId: string;
  attempt: int64;
  projectionId: string;
  events: int64;
}

/** One authorized pinned generation. Approved payload JSON, explicit nulls, sequence, identity, omissions and provenance follow the shared trace contract, not another HTTP-only evidence model. */
@jsonSchema
model SessionArchiveResult {
  generation: uint64;
  kind: "timeline" | "summary";
  events?: Trace.Archive.Event[];
  summary?: SessionArchiveSummary[];
}

model SessionArchiveCounters {
  enabled: boolean;
  generation: uint64;
  activeTraces: int32;
  stagedTraces: int32;
  /** Durable source/admission gaps remain visible and reacquirable; zero does not imply complete upstream logs. */
  gaps: int32;
  archiveBytes: int64;
  stagingBytes: int64;
}

@jsonSchema
model SessionArchiveStatus {
  enabled: boolean;
  /** Active is a running lifecycle, not proof that pending evidence is published or that Azure acceptance gates passed. */
  stage: "disabled" | "active" | "retrying";
  archive?: SessionArchiveCounters;
}

@tag("Private session archive")
@useAuth(DashboardAuth)
namespace SessionArchive {
  /** Default-off additional analytics, independent of operational dashboard readiness. Disabled returns only enabled=false and stage=disabled. Enabled status requires separately approved full-trace-read access: the local bearer with explicit content-read approval, or a fresh hosted OAuth session for an explicitly listed archive reader. Dashboard membership, administrator status, Actions identities, and storage credentials alone never grant trace content access. No filenames or destination credentials are returned. */
  @route("/api/v1/session-archive/status")
  @get op status(): SessionArchiveStatus | Unauthorized | Forbidden | TooManyRequests | Unavailable;

  /** Execute only timeline or summary for one exact authorized repository, with an additional full-trace-read grant and (hosted) CSRF token. Maximum request 4096 bytes, result 8 MiB/10000 rows, and execution 30 seconds; backend budgets may be tighter. Authorization is rechecked before delivering any result. Corrections use active projection membership; withdrawal overrides historical/pinned readers. A budget failure is an error, not a complete-looking truncated trace. Existing SQLite/PostgreSQL dashboard queries remain on their existing backends. Hosted Blob/ADX query activation additionally requires generation-bound live Azure conformance; upload success or a local simulator does not establish that evidence. */
  @route("/api/v1/session-archive/query")
  @post op query(
    @header("X-CSRF-Token") csrfToken?: string,
    @body query: SessionArchiveQuery
  ): SessionArchiveResult | BadRequest | Unauthorized | Forbidden | NotFound | TooManyRequests | Unprocessable | Unavailable;
}
