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
}
