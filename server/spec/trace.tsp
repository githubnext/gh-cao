import "./trace-emitter.mjs";

/** Approved, immutable trace evidence; independent of destination capacity. */
namespace Cao.Trace;

model Identity {
  Namespace: string;
  /** Trace identity, not necessarily a GitHub workflow run ID. */
  RunId: string;
  RunAttempt: int64;
  /** Immutable approved trace digest. */
  ProjectionId: string;
  RowDigest: string;
  ContractVersion: 1;
}

model TraceEvents extends Identity {
  /** Zero-based contiguous event ordering within the approved trace. */
  Sequence: int64;
  Type: string;
  EventId: string;
  ParentId: string;
  EpisodeId: string;
  HopId: string;
  ParentHopId: string;
  ToolCallId: string;
  SessionId: string;
  Timestamp: string;
  PartCount: int64;
  /** SHA-256 of the concatenated UTF-8 JSON content, in Part order. */
  ContentDigest: string;
}

/** UTF-8 JSON text chunks split on rune boundaries, never base64 or source bytes. */
model TraceContentParts extends Identity {
  Sequence: int64;
  /** Zero-based contiguous chunk ordering within Sequence. */
  Part: int64;
  Content: string;
}

model TraceManifest extends Identity {
  SourceRevision: int64;
  Repository: string;
  ArtifactDigest: string;
  PolicyDigest: string;
  EventCount: int64;
  PartCount: int64;
  ContentDigest: string;
  State: string;
  Gaps: string[];
  CapturedAt: utcDateTime;
}

/** RunId identifies a frozen sample frame. */
model TraceSampleFrames extends Identity {
  SourceRevision: int64;
  Repository: string;
  Day: string;
  Cutoff: utcDateTime;
  Mode: string;
  SeedDigest: string;
  Algorithm: string;
  Population: int64;
  Selected: int64;
  SourceGaps: int64;
  Excluded: int64;
  MembershipDigest: string;
  State: string;
}

model TraceSampleMembership extends Identity {
  SourceRevision: int64;
  FrameId: string;
  TraceId: string;
  TraceVersion: string;
  Probability: float64;
  Weight: float64;
  State: string;
}

/** DatasetId identifies the immutable dataset; ProjectionId binds its source trace version. */
model TraceDatasets extends Identity {
  SourceRevision: int64;
  DatasetId: string;
  MemberCount: int64;
  DatasetDigest: string;
  Transform: string;
  Split: string;
  TaskGroup: string;
  Evaluator: string;
  QualityLabel: string;
  State: string;
  MiningVersion: string;
  OutcomeId: string;
  Disposition: string;
  LabelRuleVersion: string;
  LabelCutoff: string;
}

/** ProjectionId is the independent mining version, not the trace version. */
model TraceEpisodes extends Identity {
  SourceRevision: int64;
  CanonicalNamespace: string;
  CanonicalRunId: string;
  Repository: string;
  AuditId: string;
  Kind: string;
  EpisodeId: string;
  HopId: string;
  ParentHopId: string;
  ReferencedRunId: string;
  ReferencedRepository: string;
  ReferencedWorkflow: string;
  ObservedAt: string;
  Scope: string;
  EvidenceState: string;
  BindingVersion: string;
}

model TraceOutcomes extends Identity {
  SourceRevision: int64;
  CanonicalNamespace: string;
  CanonicalRunId: string;
  Repository: string;
  OutputId: string;
  OutputUrl: string;
  OutputType: string;
  AuditId: string;
  /** Zero means unknown; the enclosing RunAttempt is not proof of output production. */
  ProducingAttempt: int64;
  Status: string;
  Signal: string;
  ObjectState: string;
  ObjectUpdatedAt: string;
  ObservedAt: string;
  AcquisitionSource: string;
  Scope: string;
  EvidenceState: string;
  LabelRuleVersion: string;
}

model TraceMiningManifest extends Identity {
  SourceRevision: int64;
  CanonicalNamespace: string;
  CanonicalRunId: string;
  CanonicalRevision: int64;
  Repository: string;
  TraceVersion: string;
  EpisodeCount: int64;
  OutcomeCount: int64;
  State: string;
  Gaps: string[];
  CapturedAt: utcDateTime;
  FreshUntil: utcDateTime;
}
