import "./adx-emitter.mjs";
using TypeSpec.Reflection;

/** Closed, scalar-only export of retained canonical session evidence. */
namespace Cao.ADX;

/** Canonical collection; own property names map to lower-initial source fields. */
extern dec source(target: Model, collection: valueof string);

model Identity {
  Namespace: string;
  RunId: string;
  RunAttempt?: int64 | null;
  ProjectionId: string;
  RowDigest: string;
  ContractVersion: 1;
}

model Observation extends Identity {
  ObservationId: string;
  /** Distinguishes absent fields from explicitly present nulls. */
  PresentFields: string[];
  /** Kusto strings cannot retain null separately from an empty string. */
  NullFields: string[];
  Availability: string;
  Completeness: string;
  Freshness: string;
}

@source("$runs")
model SessionFacts extends Observation {
  GithubRunId?: string | null;
  WorkflowId?: string | null;
  RepositoryId?: string | null;
  Engine?: string | null;
  ModelId?: string | null;
  Status?: string | null;
  Conclusion?: string | null;
  StartedAt?: utcDateTime | null;
  CompletedAt?: utcDateTime | null;
  AicTotal?: float64 | null;
  InputTokens?: int64 | null;
  OutputTokens?: int64 | null;
  CacheReadTokens?: int64 | null;
  CacheWriteTokens?: int64 | null;
  ReasoningTokens?: int64 | null;
  ErrorCount?: int64 | null;
  GithubApiCalls?: int64 | null;
}

@source("$tools")
model ToolEvents extends Observation {
  Type?: string | null;
  Name?: string | null;
  ToolType?: string | null;
  Status?: string | null;
  Source?: string | null;
  SourceSequence?: int64 | null;
  CorrelationId?: string | null;
  Timestamp?: utcDateTime | null;
  ObservedAt?: utcDateTime | null;
  LatencyMs?: float64 | null;
  InvocationCount?: int64 | null;
  FailedCount?: int64 | null;
  RequestBytes?: int64 | null;
  ResponseBytes?: int64 | null;
}

@source("$domains")
model FirewallEvents extends Observation {
  Type?: string | null;
  Domain?: string | null;
  Method?: string | null;
  Decision?: string | null;
  Status?: string | null;
  Source?: string | null;
  SourceSequence?: int64 | null;
  Timestamp?: utcDateTime | null;
  ObservedAt?: utcDateTime | null;
  LatencyMs?: float64 | null;
  RequestCount?: int64 | null;
  ResponseBytes?: int64 | null;
}

@source("$graderObservations")
model GraderObservations extends Observation {
  GraderId?: string | null;
  ExperimentId?: string | null;
  EvaluatorDigest?: string | null;
  Status?: string | null;
  Role?: string | null;
  Included?: boolean | null;
  MaturityStatus?: string | null;
  Timestamp?: utcDateTime | null;
  Value?: float64 | null;
  BaselineValue?: float64 | null;
  DeltaFromBaseline?: float64 | null;
}

@source("$evalObservations")
model EvalObservations extends Observation {
  EvalId?: string | null;
  ExperimentId?: string | null;
  EvalResult?: string | null;
  Direction?: string | null;
  Status?: string | null;
  Included?: boolean | null;
  Timestamp?: utcDateTime | null;
}

/** Published only after every exact fact digest is visible. */
model SessionVersions extends Identity {
  SourceRevision: int64;
  Coverage: "retained-canonical-snapshot";
  UsageObservations: "unavailable";
  SessionFactsCount: int64;
  ToolEventsCount: int64;
  FirewallEventsCount: int64;
  GraderObservationsCount: int64;
  EvalObservationsCount: int64;
}
