import "@typespec/http";
import "@typespec/json-schema";

using TypeSpec.Http;
using TypeSpec.JsonSchema;
namespace Cao;

model Predicate {
  field: string;
  equals?: unknown;
  in?: unknown[];
  includes?: string;
  gte?: unknown;
  lt?: unknown;
  optional?: boolean;
}
model Filter {
  predicates?: Predicate[];
  search?: { fields: string[]; query: string };
}
model SelectedField { field: string; as?: string; }
model Join {
  source: string;
  /** inner and left require unique right keys; inner-many explicitly expands all matching rows within query budgets. Missing or blank keys never match. */
  type?: string;
  on: { left: string; right: string }[];
  fields: SelectedField[];
}
model ComputedField {
  as: string;
  function: string;
  args: ({ field: string } | { value: unknown } | { context: string })[];
}
model AggregateValue {
  field: string;
  as: string;
  /** Includes median, p75, p90 and p95: continuous quantiles over known finite numeric values. */
  reducer: string;
  filter?: Filter;
}
model TemporalSeries {
  time: string;
  series: string;
  shape?: string;
  carry?: string[];
  measures?: { field: string; key?: string; kind: string }[];
  maps?: { field: string; definitions?: string; group?: string; kind: string }[];
  trend?: TemporalTrend;
}
model TemporalTrend { direction: string; }
model OrderField {
  field: string;
  direction?: string;
}
model QueryPageBoundary {
  @minItems(1)
  @maxItems(4)
  keys: string[];
  revision: "workflow-subjects";
  comparator: "utf8-bytewise-v1";
}
/** Terminal projection of processed rows into at most 64 chart panels. Declare field alone, or row and/or column; as names each panel's ordered row sequence. */
model Facet {
  field?: string;
  row?: string;
  column?: string;
  as: string;
}
/** An ordered rolling, change, previous-value or row-number operation, evaluated after aggregation and before temporal-series projection. previous preserves scalar type; row-number is one-based. */
model WindowField {
  operation: string;
  field: string;
  as: string;
  `order-by`: OrderField[];
  groupby?: string[];
  frame?: int32;
  reducer?: string;
  alignment?: string;
  mode?: string;
  `time-field`?: string;
  unit?: string;
}

/** A Dashboard Language query definition. See dashboard/site/dashboard.json for the language vocabulary. */
@jsonSchema
model QueryDefinition {
  name: string;
  from: string;
  `union`?: string[];
  joins?: Join[];
  filter?: Filter;
  compute?: ComputedField[];
  aggregate?: { by?: string[]; values: AggregateValue[] };
  @encodedName("application/json", "temporal-series") temporalSeries?: TemporalSeries;
  predict?: unknown[];
  @minItems(1)
  @maxItems(8)
  window?: WindowField[];
  select?: SelectedField[];
  @encodedName("application/json", "order-by") orderBy?: OrderField[];
  page?: QueryPageBoundary;
  limit?: int32;
  facet?: Facet;
  stores?: string[];
}

model PaginationRequest {
  limit: int32;
  continuationToken?: string;
}
/** Requests may supply query definitions or use server-configured definitions. */
@jsonSchema
@example(#{sourceNames: #["runs"], aliases: #[], pagination: #{runs: #{limit: 50}}})
model QueryRequest {
  sourceNames?: string[];
  queries?: QueryDefinition[];
  compiledQueries?: QueryDefinition[];
  aliases?: string[];
  replacedSources?: string[];
  pagination?: Record<PaginationRequest>;
  pageId?: string;
  viewId?: string;
  routeParameters?: JsonObject;
  queryContext?: JsonObject;
  context?: JsonObject;
  evaluatedAt?: string;
}
model QuerySource {
  source: string;
  rows: JsonObject[];
  metadata: JsonObject;
  continuationToken?: string;
}
model QueryMetrics {
  durationMs: int64;
  operations: int32;
  outputRows: int32;
  peakWorkingRows: int32;
  retainedRows: int32;
  peakWorkingBytes: int64;
  retainedBytes: int64;
  dependencyDepth: int32;
  rateLimitCost: int32;
  queryCount: int32;
  unionCount: int32;
  joinCount: int32;
  filterCount: int32;
  computeCount: int32;
  aggregateCount: int32;
  aggregateValueCount: int32;
  temporalSeriesCount: int32;
  selectCount: int32;
  orderByCount: int32;
  limitCount: int32;
  pushedDown: string[] | null;
  fallbackOperations: string[] | null;
}
@jsonSchema
model QueryResponse {
  revision: int64;
  healthRevision?: int64;
  evaluatedAt: string;
  sources: Record<QuerySource>;
  metrics: QueryMetrics;
}
model QueryPlanTooLarge {
  @statusCode statusCode: 422;
  error: string;
  code: "query_plan_too_large";
  queryId: string;
  boundary: string;
}

@tag("Queries")
@useAuth(DashboardAuth)
namespace Queries {
  /** Execute Dashboard Language against current Postgres dashboard entities. Up to 256 source names and 8 MiB of request body. */
  @route("/api/v1/query")
  @post op execute(
    /** Required in hosted mode for every POST; omitted in local bearer mode. */
    @header("X-CSRF-Token") csrfToken?: string,
    @body request: QueryRequest
  ):
    QueryResponse | BadRequest | Unauthorized | Forbidden | QueryPlanTooLarge | TooManyRequests | Unavailable;

  /** Server-sent stream: each `data:` line is a RevisionEvent JSON object; `: keepalive` comments arrive every 20 seconds. HTTP hosts must support flushing and must not impose a finite whole-request timeout on this route. */
  @route("/api/v1/events")
  @get op events(): {
    @header contentType: "text/event-stream";
    @body body: string;
  } | Unauthorized | Unavailable;

  /** Read-only projection diagnostics. */
  @route("/api/v1/diagnostics")
  @get op diagnostics(): {
    schemaVersion: int32;
    counts: Record<int32>;
    relationshipErrors: string[];
    duplicateRecordIds: Record<string[]>;
  } | Unauthorized | Unavailable;

  /** Check the current revision; does not itself rebuild or mutate the projection. */
  @route("/api/v1/refresh")
  @post op refresh(@header("X-CSRF-Token") csrfToken?: string):
    { revision: int64; evaluatedAt: string; changed: false } | Unauthorized | Forbidden | Unavailable;
}
