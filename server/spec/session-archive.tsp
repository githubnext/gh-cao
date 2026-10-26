import "@typespec/http";
import "@typespec/json-schema";
import "./trace.tsp";

using TypeSpec.Http;
using TypeSpec.JsonSchema;
namespace Cao;

model SessionArchivePredicate {
  field: string;
  equals?: string | int64;
  @minItems(1) @maxItems(32) in?: (string | int64)[];
  gte?: string | int64;
  lt?: string | int64;
}

/** Local DuckDB analytical profile of Dashboard Language, not a full dashboard document. Unknown keys and fields fail before execution. Predicates are conjunctive, with exactly one of equals, in, or a gte/lt range. String comparisons are exact and case-sensitive; ordering is typed, with UTF-8 lexical string order and empty strings last. */
@jsonSchema
model SessionArchiveDefinition {
  @pattern("^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$") @maxLength(128) name: string;
  @minLength(1) @maxLength(512) subject: string;
  objective?: string;
  acceptance?: string;
  from: "archive-events";
  filter?: { @minItems(1) @maxItems(64) predicates: SessionArchivePredicate[] };
  aggregate?: {
    @maxItems(16) by?: string[];
    @minItems(1) @maxItems(16) values: {
      field: string;
      as: string;
      reducer: "count" | "distinct-count";
    }[];
  };
  /** Required for non-aggregate queries. Only generated scalar event columns are allowed; PayloadJSON and Gaps are not query fields. */
  @minItems(1) @maxItems(32) select?: SelectedField[];
  @encodedName("application/json", "order-by") @maxItems(16) orderBy?: OrderField[];
  /** Intentional top-N selection after aggregation, projection and deterministic ordering. Omitted means the complete result must fit the independent response budget. */
  @minValue(1) @maxValue(10000) limit?: int32;
}

/** Trusted archive queries. No SQL/KQL, object names, filesystem paths, or cross-store joins are accepted. Query-only fields are forbidden on legacy templates and vice versa. Optional fields must be omitted rather than null. */
@jsonSchema
model SessionArchiveQuery {
  kind: "timeline" | "summary" | "session-query";
  /** Exact lowercase owner/repository in the reviewed archive and collection scope. */
  repository: string;
  /** Required positive GitHub run ID for timeline; forbidden for summary. Not a canonical Run ID or trace digest. */
  runId?: string;
  /** Required positive exact attempt for timeline; forbidden for summary. */
  attempt?: int64;
  /** Maximum complete output rows, 1-10000 (default 10000). Exceeding a limit fails, never truncates a full approved trace. */
  @minValue(1) @maxValue(10000) limit?: int32;
  /** Required only for kind=session-query. Hosted ADX does not support this analytical profile. */
  `language-version`?: "0.1.0";
  /** Required only for kind=session-query; runId, attempt and envelope limit are forbidden. */
  query?: SessionArchiveDefinition;
}

model SessionArchiveColumn {
  name: string;
  type: "string" | "integer";
}

model SessionArchiveQueryMetadata {
  /** Whether the explicit analytical limit omitted matching output rows. Never indicates a silently exceeded resource budget. */
  limited: boolean;
  /** Always unknown: archive coverage is not proof of complete upstream history. */
  upstreamCoverage: "unknown";
  /** Repository-scoped durable acquisition/admission gaps at the pinned generation's assessment. */
  admissionGaps: int32;
  /** Retained active traces with recorded omissions, before user filtering; distinct from admission gaps. */
  tracesWithGaps: int32;
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
  kind: "timeline" | "summary" | "session-query";
  events?: Trace.Archive.Event[];
  summary?: SessionArchiveSummary[];
  columns?: SessionArchiveColumn[];
  /** Present even for an empty analytical result. Integer values are serialized without float conversion. */
  rows?: JsonObject[];
  metadata?: SessionArchiveQueryMetadata;
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
  /** Enabled local archives advertise timeline, summary, session-query; hosted archives advertise timeline and summary only. */
  queryKinds?: string[];
}

@tag("Private session archive")
@useAuth(DashboardAuth)
namespace SessionArchive {
  /** Default-off additional analytics, independent of operational dashboard readiness. Disabled returns only enabled=false and stage=disabled. Enabled status requires separately approved full-trace-read access: the local bearer with explicit content-read approval, or a fresh hosted OAuth session for an explicitly listed archive reader. Dashboard membership, administrator status, Actions identities, and storage credentials alone never grant trace content access. No filenames or destination credentials are returned. */
  @route("/api/v1/session-archive/status")
  @get op status(): SessionArchiveStatus | Unauthorized | Forbidden | TooManyRequests | Unavailable;

  /** Execute timeline, summary, or the local-only session-query Dashboard Language profile for one exact authorized repository, with an additional full-trace-read grant and (hosted) CSRF token. Maximum request 4096 bytes, result 8 MiB/10000 rows, and execution 30 seconds; backend budgets may be tighter. Hosted session-query requests fail explicitly with 422, never fall back. Authorization is rechecked inside withdrawal-aware result delivery through the final response write. Corrections use active projection membership before any analytical operation; withdrawal overrides historical/pinned readers. A budget failure is an error, not a complete-looking truncated trace. An explicit query.limit is intentional selection, reported separately from evidence coverage. Existing SQLite/PostgreSQL dashboard queries remain on their existing backends. Hosted Blob/ADX query activation additionally requires generation-bound live Azure conformance; upload success or a local simulator does not establish that evidence. */
  @route("/api/v1/session-archive/query")
  @post op query(
    @header("X-CSRF-Token") csrfToken?: string,
    @body query: SessionArchiveQuery
  ): SessionArchiveResult | BadRequest | Unauthorized | Forbidden | NotFound | TooManyRequests | Unprocessable | Unavailable;
}
