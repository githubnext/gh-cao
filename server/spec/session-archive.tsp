import "@typespec/http";
import "@typespec/json-schema";
import "./trace.tsp";

using TypeSpec.Http;
using TypeSpec.JsonSchema;
namespace Cao;

model SessionArchivePredicate {
  field: string;
  equals?: string | int64 | float64 | boolean;
  @minItems(1) @maxItems(32) in?: (string | int64 | float64 | boolean)[];
  gte?: string | int64 | float64;
  lt?: string | int64 | float64;
  /** State of a declared promoted field, inspected through generated evidence metadata only. Exclusive with comparison operands. On archive-events, field may also name an approved structured evidence family (for example Error, Usage or Provenance) advertised in analytical.stateFamilies; a family is never a comparison, selection, grouping or computation field. */
  state?: "present" | "absent" | "null" | "invalid" | "excluded" | "redacted" | "valid";
}

alias SessionArchiveFunction = "if" | "equals" | "gt" | "lte" | "sum" | "difference" | "product" | "quotient" | "coalesce" | "is-null" | "and";
model SessionArchiveFieldArgument {
  field: string;
}
model SessionArchiveValueArgument {
  value: string | int64 | float64 | boolean;
  type?: "string" | "integer" | "number" | "boolean";
}
/** Null is allowed only as an explicitly typed numeric computation literal. */
model SessionArchiveNullArgument {
  value: null;
  type: "integer" | "number";
}
model SessionArchiveFunctionArgument {
  function: SessionArchiveFunction;
  @maxItems(3) args: SessionArchiveExpression[];
}
union SessionArchiveExpression {
  field: SessionArchiveFieldArgument,
  value: SessionArchiveValueArgument,
  missing: SessionArchiveNullArgument,
  function: SessionArchiveFunctionArgument
}

/** Shared DuckDB/ADX analytical profile of Dashboard Language, not a full dashboard document. Unknown keys and fields fail before execution. Predicates are conjunctive, with exactly one of equals, in, or a gte/lt range; boolean ranges are forbidden. String comparisons are exact and case-sensitive; ordering is typed, with UTF-8 lexical string order and empty strings last. Counts exclude empty strings and null scalars, but retain false and zero; distinct-count is exact, never approximate. */
@jsonSchema
model SessionArchiveDefinition {
  @pattern("^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$") @maxLength(128) name: string;
  @minLength(1) @maxLength(512) subject: string;
  objective?: string;
  acceptance?: string;
  /** Query chaining (Dashboard Language DLS-QUERY-017): earlier definitions in the same request, evaluated in order before this one. Each reads an archive source or any earlier definition through from or joins, and every earlier definition is read by a later one or by this definition, so no stage is unused. An earlier definition either aggregates with every trace and projection identity field (Namespace, Repository, RunId, RunAttempt, TraceId, ProjectionId) in aggregate.by, outputting its groups, aggregates and computations, or is row-level and selects its input key unrenamed (the trace and projection identity plus Sequence for archive-events, Ordinal for item sources), outputting the selected fields. Earlier definitions must not declare queries, order-by or limit, and aggregating ones must not declare select. Names are unique and never shadow a source. Rejected unless status advertises analytical.chaining at least this length. */
  @maxItems(8) queries?: SessionArchiveDefinition[];
  /** One of archive-events, archive-objectives, archive-objective-dependencies, archive-actions, archive-states or archive-ir-events, or the name of an earlier definition in queries. archive-events is the shared event source. The item sources flatten the last agent-phase trajectory.ir collection of each complete active trace at query time, with one anchor row per trace; archive-objective-dependencies further flattens each objective's dependsOn array into rows keyed by the parent Ordinal and Position. Local DuckDB and hosted KQL both lower them, and they are rejected unless status advertises them in analytical.sources. Any other value names an earlier definition in queries; an aggregate over it must keep complete trace binding in aggregate.by, and field-state predicates and first are unavailable there. */
  @pattern("^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$") @maxLength(128) from: string;
  /** Trace-scoped equality joins (Dashboard Language DLS-QUERY-005..008), applied in order before filter. Both sides always match on the complete trace binding (Namespace, Repository, RunId, RunAttempt, TraceId, ProjectionId, PolicyDigest); on adds integer or string keys, never a binding field. on must cover the right side's row key (an earlier aggregate's by fields, or the source key), so each left row meets at most one right row. Right rows with a null or empty-string key never match. inner drops unmatched left rows; left keeps them with null joined fields (empty string for strings). Imported names must not collide with input fields, case-insensitively, and cannot be targets of field-state predicates. Rejected unless status advertises analytical.joins at least this length. */
  @maxItems(4) joins?: SessionArchiveJoin[];
  filter?: { @minItems(1) @maxItems(64) predicates: SessionArchivePredicate[] };
  /** Row-level computations after filter and before window and aggregate, in Dashboard Language order. Each may reference input fields and preceding computations; filter cannot read them. is-null and coalesce over strings are rejected because archive strings are never null. Rejected unless status advertises analytical.compute. */
  @maxItems(32) compute?: { as: string; function: SessionArchiveFunction; @maxItems(3) args: SessionArchiveExpression[] }[];
  /** Dashboard Language previous and row-number windows (DLS §5.5.4) over row-level queries only; forbidden with aggregate. groupby must include every trace and projection identity field, and groupby with order-by must cover the input key so window order is total. previous returns the field from the preceding row in its partition, or null (empty string for strings) on the first row. Rejected unless status advertises each operation in analytical.windows. */
  @maxItems(8) window?: SessionArchiveWindow[];
  aggregate?: {
    @maxItems(16) by?: string[];
    @minItems(1) @maxItems(16) values: {
      field: string;
      as: string;
      reducer: "count" | "distinct-count" | "sum" | "max" | "first";
      filter?: { @minItems(1) @maxItems(64) predicates: SessionArchivePredicate[] };
      /** first requires Sequence ascending and complete trace binding in aggregate.by. Sum of real observations also requires this order, preserving sequential IEEE arithmetic. Over an earlier definition, a real sum instead orders ascending by exactly the earlier aggregate.by fields absent from this aggregate.by, in their declared order. */
      @encodedName("application/json", "order-by") orderBy?: OrderField[];
    }[];
  };
  /** Explicit bounded phase after aggregation, before selection/order/limit. Computations can reference groups, aggregates and preceding computations, never source rows. */
  @encodedName("application/json", "post-aggregate") @maxItems(32)
  postAggregate?: { as: string; function: SessionArchiveFunction; @maxItems(3) args: SessionArchiveExpression[] }[];
  /** Required for non-aggregate queries. Only generated scalar event columns are allowed; PayloadJSON and Gaps are not query fields. */
  @minItems(1) @maxItems(32) select?: SelectedField[];
  @encodedName("application/json", "order-by") @maxItems(16) orderBy?: OrderField[];
  /** Intentional top-N selection after aggregation, projection and deterministic ordering. Omitted means the complete result must fit the independent response budget. */
  @minValue(1) @maxValue(10000) limit?: int32;
}

/** Trusted archive queries. No SQL/KQL, object names, filesystem paths, or cross-store joins are accepted. Query-only fields are forbidden on timeline/summary templates and vice versa. Optional fields must be omitted rather than null. */
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
  /** Required only for kind=session-query on both DuckDB and ADX. Only 0.2.0 is accepted; older versions are rejected. */
  `language-version`?: "0.2.0";
  /** Required only for kind=session-query; runId, attempt and envelope limit are forbidden. */
  query?: SessionArchiveDefinition;
}

model SessionArchiveColumn {
  name: string;
  type: "string" | "integer" | "number" | "boolean";
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
  queryDigest: string;
  implementation: string;
  `language-version`: "0.2.0";
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

model SessionArchiveSourceCapability {
  name: "archive-objectives" | "archive-objective-dependencies" | "archive-actions" | "archive-states" | "archive-ir-events";
  fields: SessionArchiveColumn[];
}

model SessionArchiveJoin {
  /** An archive source or an earlier definition in queries. */
  @pattern("^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$") @maxLength(128) source: string;
  /** Defaults to inner. Cross, right and full joins are not part of the profile. */
  type?: "inner" | "left";
  @maxItems(4) on?: { left: string; right: string }[];
  @minItems(1) @maxItems(16) fields: { field: string; as?: string }[];
}

model SessionArchiveWindow {
  /** Required for previous; optional for row-number. */
  field?: string;
  as: string;
  operation: "previous" | "row-number";
  @encodedName("application/json", "order-by") @minItems(1) @maxItems(8) orderBy: OrderField[];
  @minItems(1) @maxItems(8) groupby: string[];
}

model SessionArchiveCapabilities {
  `language-version`: "0.2.0";
  source: "archive-events";
  fields: SessionArchiveColumn[];
  /** Additional item sources supported by this backend. Omitted means archive-events only. */
  sources?: SessionArchiveSourceCapability[];
  predicates: string[];
  reducers: string[];
  functions: string[];
  fieldStates: string[];
  /** Independent complete-result budget; an explicit limit must not exceed it. */
  maxRows: int32;
  /** Maximum earlier definitions a session-query may chain through queries. Omitted means chained queries are rejected. */
  chaining?: int32;
  /** Maximum trace-scoped joins one definition may declare. Omitted means joins are rejected. */
  joins?: int32;
  /** Row-level compute before aggregate. Omitted means compute is rejected. */
  compute?: boolean;
  /** Supported window operations. Omitted means window is rejected. */
  windows?: ("previous" | "row-number")[];
  /** Structured evidence families accepted as state-predicate fields. Omitted means none. */
  stateFamilies?: string[];
}

@jsonSchema
model SessionArchiveStatus {
  enabled: boolean;
  /** Active is a running lifecycle, not proof that pending evidence is published or that Azure acceptance gates passed. */
  stage: "disabled" | "active" | "retrying";
  archive?: SessionArchiveCounters;
  /** Local archives advertise timeline, summary, and session-query. Hosted archives advertise session-query only after separate analytical-plan destination acceptance. No backend fallback is implied. */
  queryKinds?: string[];
  /** Present only when session-query is supported. Generated scalar vocabulary and supported operators. ADX has a tighter 4096-row budget than DuckDB's 10000. */
  analytical?: SessionArchiveCapabilities;
}

@tag("Private session archive")
@useAuth(DashboardAuth)
namespace SessionArchive {
  /** Default-off additional analytics, independent of operational dashboard readiness. Disabled returns only enabled=false and stage=disabled. Enabled status requires separately approved full-trace-read access: the local bearer with explicit content-read approval, or a fresh hosted OAuth session for an explicitly listed archive reader. Dashboard membership, administrator status, Actions identities, and storage credentials alone never grant trace content access. No filenames or destination credentials are returned. */
  @route("/api/v1/session-archive/status")
  @get op status(): SessionArchiveStatus | Unauthorized | Forbidden | TooManyRequests | Unavailable;

  /** Execute timeline, summary, or the shared session-query Dashboard Language profile for one exact authorized repository, with an additional full-trace-read grant and (hosted) CSRF token. Maximum request 4096 bytes, result 8 MiB/10000 rows, and execution 30 seconds; ADX is limited to 4096 output rows. Hosted session-query additionally requires separate analytical-plan destination acceptance; existing capture acceptance and native privacy identity remain unchanged. Unsupported operators and backend budget failures are explicit errors, never fallbacks. Authorization is rechecked inside withdrawal-aware result delivery through the final response write. Corrections use active projection membership before any analytical operation; withdrawal overrides historical/pinned readers. A budget failure is an error, not a complete-looking truncated trace. An explicit query.limit is intentional selection, reported separately from evidence coverage. Ordinary dashboard queries remain on PostgreSQL. Hosted Blob/ADX query activation additionally requires generation-bound live Azure conformance; upload success or a local simulator does not establish that evidence. */
  @route("/api/v1/session-archive/query")
  @post op query(
    @header("X-CSRF-Token") csrfToken?: string,
    @body query: SessionArchiveQuery
  ): SessionArchiveResult | BadRequest | Unauthorized | Forbidden | NotFound | TooManyRequests | Unprocessable | Unavailable;
}
