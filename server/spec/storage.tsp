import "./postgres-emitter.mjs";
import "./daily-counts.tsp";
using TypeSpec.Reflection;
// Editable canonical Postgres storage contract. Generate SQL and Go bindings with npm run generate:storage.
namespace Cao.Postgres;

extern dec parent(target: ModelProperty, entity: valueof string, required?: valueof boolean);
extern dec runPartition(target: Model);
/** Keep typed, non-partitioned copies in an independently owned statistical snapshot. */
extern dec sampledBundle(target: Model);

@sampledBundle
model campaigns {
  aiCreditAllowance?: decimal;
  campaignLink?: Values.Link;
  currentVersion?: string;
  description?: string;
  enabled?: boolean;
  experimental?: boolean;
  icon?: string;
  id: string;
  inventoryWarnings?: int64;
  maxRepositories?: int64;
  minVersion?: string;
  mode?: string;
  monthlyAiCreditBudget?: decimal;
  name?: string;
  observedAt?: utcDateTime;
  readme?: string;
  readmePath?: string;
  rolloutPercent?: decimal;
  slug?: string;
  sourceCoordinate?: string;
  updateState?: string;
  version?: string;
  workerCount?: int64;
}

@sampledBundle
model repositories {
  id: string;
  name?: string;
  observedAt?: utcDateTime;
  organizationLink?: Values.Link;
  owner?: string;
  repositoryLink?: Values.Link;
  rolloutMode?: string;
  /** Collector-maintained lifecycle; absent means active for snapshot ingestion. */
  lifecycle?: string;
  visibility?: string;
}

/** Query-minimal workflow inventory; raw ghAwMetadata and ghAwManifest remain upstream. */
@sampledBundle
model workflows {
  admissionReason?: string;
  admissionStatus?: string;
  @parent("campaigns", false)
  campaignId?: string;
  createdAt?: utcDateTime;
  externalLink?: Values.Link;
  ghAwCurrentVersion?: string;
  ghAwUpdateState?: string;
  ghAwVersion?: string;
  githubId?: string;
  id: string;
  inventoryReady?: boolean;
  maxAiCredits?: decimal;
  name?: string;
  observedAt?: utcDateTime;
  organizationLink?: Values.Link;
  path?: string;
  registryState?: string;
  @parent("repositories")
  repositoryId: string;
  repositoryLink?: Values.Link;
  role?: string;
  rolloutMode?: string;
  sourceCoordinate?: string;
  sourceCurrentVersion?: string;
  sourceKind?: string;
  sourceUpdateState?: string;
  sourceVersion?: string;
  state?: string;
  updatedAt?: utcDateTime;
  workflowLink?: Values.Link;
}

/** Run facts and scalar metrics; nested context, usage, and assessment payloads remain upstream. */
@runPartition
@sampledBundle
model runs {
  routingAggregateVersion?: int64;
  routingState?: string;
  routingSelectedModel?: string;
  routingSelectedEffort?: string;
  routingGoal?: string;
  routingConfiguredMode?: string;
  routingMode?: string;
  routingStatus?: string;
  routingAwfVersion?: string;
  routingSessionClock?: decimal;
  routingSelectionCount?: int64;
  routingFailureCount?: int64;
  routingDegraded?: boolean;
  routingConfigurationConflict?: boolean;
  routingWarnings?: int64;
  routingAgentResult?: string;
  routingFlaggedRequests?: int64;
  routingDeviatedRequests?: int64;
  routingCompleteAic?: decimal;
  routingMainAic?: decimal;
  routingBaselineModel?: string;
  routingBaselineAic?: decimal;
  routingBaselineComplete?: boolean;
  routingFailureCodes?: string;
  routingDeviationCodes?: string;
  routingResolutionCodes?: string;
  routingUpstreamCodes?: string;
  routingCandidateCoverage?: string;
  routingTaskType?: string;
  routingScope?: string;
  routingComplexity?: string;
  actionMinutes?: decimal;
  admissionReason?: string;
  admissionStatus?: string;
  agentId?: string;
  agentRuntime?: string;
  agentVersion?: string;
  aicTotal?: decimal;
  attempt?: int64;
  branch?: string;
  cacheReadTokens?: int64;
  cacheWriteTokens?: int64;
  classification?: string;
  completedAt?: utcDateTime;
  conclusion?: string;
  createdAt?: utcDateTime;
  duration?: string;
  engine?: string;
  engineId?: string;
  engineVersion?: string;
  errorCount?: int64;
  event?: string;
  failureDetail?: string;
  failureJob?: string;
  failureKind?: string;
  failureLog?: string;
  failureMessage?: string;
  failureStep?: string;
  firewallVersion?: string;
  gatewayVersion?: string;
  ghAwVersion?: string;
  githubApiCalls?: int64;
  githubRunId?: string;
  headSha?: string;
  id: string;
  inputTokens?: int64;
  modelId?: string;
  organizationLink?: Values.Link;
  outputTokens?: int64;
  owner?: string;
  reasoningTokens?: int64;
  repository?: string;
  @parent("repositories")
  repositoryId: string;
  repositoryLink?: Values.Link;
  requestedModel?: string;
  resolvedModel?: string;
  resource?: string;
  resourceResetAt?: utcDateTime;
  resourceWaitHours?: decimal;
  rolloutMode?: string;
  runLink?: Values.Link;
  safeItemsCount?: int64;
  startedAt?: utcDateTime;
  status?: string;
  targetRepository?: string;
  terminalOutcome?: string;
  terminalOutcomeDetail?: string;
  title?: string;
  updatedAt?: utcDateTime;
  @parent("workflows")
  workflowId: string;
  workflowLink?: Values.Link;
  workflowPath?: string;
}

@runPartition
@sampledBundle
model domains {
  correlationId?: string;
  decision?: string;
  domain?: string;
  id: string;
  latencyMs?: decimal;
  method?: string;
  observedAt?: utcDateTime;
  payloadRef?: string;
  requestCount?: int64;
  responseBytes?: int64;
  @parent("runs")
  runId: string;
  source?: string;
  sourceSequence?: int64;
  status?: string;
  summary?: string;
  timestamp?: utcDateTime;
  type?: string;
}

/** Run-linked tool events, including separate call/outcome records; not configured tool definitions. */
@runPartition
@sampledBundle
model tools {
  correlationId?: string;
  failedCount?: int64;
  id: string;
  invocationCount?: int64;
  latencyMs?: decimal;
  mcpProtocolVersion?: string;
  mcpServer?: string;
  mcpServerVersion?: string;
  mcpTool?: string;
  name?: string;
  observedAt?: utcDateTime;
  payloadRef?: string;
  requestBytes?: int64;
  responseBytes?: int64;
  @parent("runs")
  runId: string;
  source?: string;
  sourceSequence?: int64;
  status?: string;
  summary?: string;
  timestamp?: utcDateTime;
  toolType?: string;
  type?: string;
}

/** Separate skill activity; collection membership replaces the unused isSkill flag. */
@runPartition
@sampledBundle
model skills {
  activationSource?: string;
  correlationId?: string;
  failedCount?: int64;
  id: string;
  invocationCount?: int64;
  name?: string;
  observedAt?: utcDateTime;
  payloadRef?: string;
  @parent("runs")
  runId: string;
  source?: string;
  sourceSequence?: int64;
  status?: string;
  summary?: string;
  timestamp?: utcDateTime;
  toolType?: string;
  type?: string;
}

@runPartition
@sampledBundle
model friction {
  aic?: decimal;
  inputTokens?: int64;
  outputTokens?: int64;
  cacheReadTokens?: int64;
  cacheWriteTokens?: int64;
  reasoningTokens?: int64;
  totalTokens?: int64;
  turns?: int64;
  toolCalls?: int64;
  latencyMs?: decimal;
  eventsTruncated?: boolean;
  linkedInvocations?: int64;
  totalEvents?: int64;
  canonicalUnit?: string;
  correlationId?: string;
  costGrain?: string;
  countedOccurrences?: int64;
  decision?: string;
  derived?: boolean;
  evidenceConfidence?: decimal;
  evidenceState?: string;
  frictionRatio?: decimal;
  id: string;
  measurementState?: string;
  missingReason?: string;
  observedAt?: utcDateTime;
  payloadRef?: string;
  @parent("runs")
  runId: string;
  source?: string;
  sourceSequence?: int64;
  summary?: string;
  suppressedOccurrences?: int64;
  timestamp?: utcDateTime;
  totalOccurrences?: int64;
  totalRunAic?: decimal;
  type?: string;
  unattributedOccurrences?: int64;
}

/**
 * Curated Run-owned evidence, governed by dashboard-data.md section 11.1.
 * JavaScript and Go ingestion and existing-database maintenance discard only
 * the exact low-information comparison, working-set, empty-session, and
 * low-priority generic advice tuples, or started/completed/failed/usage/
 * safe-output-count/failure-finding copies whose nonnull owning Run facts agree.
 * Additional nonnull evidence, conflicting attempts, missing Run facts, and
 * invalid comparison values retain the Audit. Grader/eval identities,
 * safe-output actions, behavior/assessment evidence, and specific diagnostics
 * are not eligible. Cleanup precedes size eviction; retained row counts and
 * changed revisions are published without adding duplicate Run storage.
 * Numeric summaries must match in full, be at most 1,024 ASCII characters,
 * and use at most three exponent digits; larger or malformed summaries remain.
 */
@runPartition
@sampledBundle
model audits {
  routingStage?: string;
  routingSchema?: string;
  routingStatus?: string;
  routingGoal?: string;
  routingConfiguredMode?: string;
  routingMode?: string;
  routingSelectedModel?: string;
  routingSelectedEffort?: string;
  routingRequestedModel?: string;
  routingRequestedEffort?: string;
  routingAppliedEffort?: string;
  routingTaskType?: string;
  routingScope?: string;
  routingComplexity?: string;
  routingPurpose?: string;
  routingAgentName?: string;
  routingAgentId?: string;
  routingRouted?: string;
  routingOutcome?: string;
  routingRouterVersion?: string;
  routingAwfVersion?: string;
  routingClassifierModel?: string;
  routingClassifierEffort?: string;
  routingClassifierAttempts?: decimal;
  routingLatencyMs?: decimal;
  routingDegraded?: boolean;
  routingDegradedReason?: string;
  routingEnabled?: boolean;
  routingSubstitution?: string;
  routingEffectiveModel?: string;
  routingSteeringType?: string;
  routingSteeringThreshold?: decimal;
  routingMaxCredits?: decimal;
  routingSessionClock?: decimal;
  routingDeviations?: string;
  routingInventoryComplete?: boolean;
  resolutionRole?: string;
  resolutionConfiguredModel?: string;
  resolutionResolvedModel?: string;
  component?: string;
  phase?: string;
  `model`?: string;
  provider?: string;
  effort?: string;
  durationMs?: decimal;
  sourceEngine?: string;
  exitCode?: int64;
  httpStatus?: int64;
  method?: string;
  direction?: string;
  requestBytes?: int64;
  responseBytes?: int64;
  premiumRequests?: decimal;
  totalCostUsd?: decimal;
  totalAic?: decimal;
  errorsCount?: int64;
  permissionDenialsCount?: int64;
  resource?: string;
  resetAt?: utcDateTime;
  rateLimit?: int64;
  remaining?: int64;
  used?: int64;
  requestAttempt?: int64;
  delayMs?: decimal;
  threshold?: decimal;
  value?: decimal;
  transport?: string;
  required?: boolean;
  scope?: string;
  digest?: string;
  acceptedAt?: utcDateTime;
  activationSource?: string;
  actor?: string;
  costGrain?: string;
  evidenceConfidence?: decimal;
  evidenceState?: string;
  aic?: decimal;
  assignmentRunId?: string;
  cacheReadTokens?: int64;
  cacheWriteTokens?: int64;
  claimRunAttempt?: int64;
  claimRunId?: string;
  code?: string;
  controlVariant?: string;
  correlationId?: string;
  decision?: string;
  eventsTruncated?: boolean;
  evidenceWindowEnd?: utcDateTime;
  evidenceWindowStart?: utcDateTime;
  experimentId?: string;
  id: string;
  implementationChangeId?: string;
  implementationCompletedAt?: utcDateTime;
  implementationPullRequestUrl?: string;
  implementationStartedAt?: utcDateTime;
  inputTokens?: int64;
  interventionId?: string;
  interventionState?: string;
  lifecycleObservationId?: string;
  linkedInvocations?: int64;
  measurementState?: string;
  missingReason?: string;
  name?: string;
  observedAt?: utcDateTime;
  opportunityId?: string;
  opportunityKind?: string;
  optimizedVariant?: string;
  optimizerRunAttempt?: int64;
  optimizerWorkflowName?: string;
  optimizerWorkflowPath?: string;
  outputTokens?: int64;
  payloadRef?: string;
  previousInterventionState?: string;
  previousRecommendationDisposition?: string;
  proposedSavingsAic?: decimal;
  reasoningTokens?: int64;
  recommendationChurnCount?: int64;
  recommendationChurnRate?: decimal;
  recommendationDisposition?: string;
  rejectedAt?: utcDateTime;
  @parent("runs")
  runId: string;
  source?: string;
  sourceSequence?: int64;
  status?: string;
  summary?: string;
  supersededAt?: utcDateTime;
  supersededByInterventionId?: string;
  supersedesInterventionId?: string;
  targetOrganization?: string;
  targetRepository?: string;
  targetWorkflowPath?: string;
  timestamp?: utcDateTime;
  toolCalls?: int64;
  totalEvents?: int64;
  totalTokens?: int64;
  turns?: int64;
  type?: string;
}

@runPartition
@sampledBundle
model issues {
  closed?: boolean;
  closedAt?: utcDateTime;
  correlationId?: string;
  githubEntityType?: string;
  id: string;
  isPullRequest?: boolean;
  name?: string;
  number?: int64;
  observedAt?: utcDateTime;
  payloadRef?: string;
  @parent("runs")
  runId: string;
  safeOutputId?: string;
  safeOutputType?: string;
  safeOutputUrl?: string;
  source?: string;
  sourceSequence?: int64;
  state?: string;
  stateReason?: string;
  status?: string;
  statusObservedAt?: utcDateTime;
  summary?: string;
  timestamp?: utcDateTime;
  type?: string;
}

/** Current bounded acquired evidence packet; replaced as one coherent revision. */
/** UTC evidence clocks must be exactly representable at PostgreSQL microsecond precision; sub-microsecond instants are rejected, never rounded. */
model operationalValueEvidence {
  id: string;
  schemaVersion: int64;
  repository: string;
  workflow: string;
  sourcePath: string;
  observedAt: utcDateTime;
  windowStart: utcDateTime;
  windowEnd: utcDateTime;
  contractDigest: string;
  @parent("repositories")
  repositoryId: string;
  @parent("workflows")
  workflowId: string;
  facts: Values.OperationalValueFact[];
}

/** Latest reviewed registration and collection decision, independent of outcome evidence. */
model workflowValueCollections {
  id: string;
  schemaVersion: int64;
  repository: string;
  workflow: string;
  sourcePath: string;
  observedAt: utcDateTime;
  registration: string;
  collector?: string;
  state: string;
  reasonCode: string;
  reason: string;
  policyDigest: string;
  lastAttemptAt?: utcDateTime;
  lastAttemptOutcome?: string;
  @parent("repositories")
  repositoryId: string;
  @parent("workflows")
  workflowId: string;
}

model operationalValues {
  @minValueExclusive(0)
  `cadence-days`?: decimal;
  `window-start`?: utcDateTime;
  `window-end`?: utcDateTime;
  `measurement-kind`?: string;
  `coverage-status`?: string;
  `missing-reason`?: string;
  `eligible-count`?: int64;
  `population-digest`?: string;
  `evidence-digest`?: string;
  `adoption-at`?: utcDateTime;
  `baseline-value`?: decimal;
  campaign?: string;
  `delta-from-baseline`?: decimal;
  `evaluation-mode`?: string;
  `evaluator-digest`?: string;
  `evidence-cutoff`?: utcDateTime;
  `evidence-link`?: string;
  experiment?: string;
  id: string;
  `maturity-at`?: utcDateTime;
  `maturity-status`?: string;
  `observation-id`?: string;
  `operational-case`?: string;
  `operational-value-direction`?: string;
  `operational-value-name`?: string;
  `operational-value-role`?: string;
  `operational-value-unit`?: string;
  @parent("repositories")
  repositoryId: string;
  `requested-evidence-at`?: utcDateTime;
  `rollout-mode`?: string;
  `rollup-denominator`?: decimal;
  `rollup-numerator`?: decimal;
  run?: string;
  `run-attempt`?: decimal;
  `run-link`?: string;
  timestamp?: utcDateTime;
  value?: decimal;
  valueId?: string;
  workflow?: string;
  `workflow-link`?: string;
  `workflow-name`?: string;
  `workflow-slug`?: string;
}

@sampledBundle
model experiments {
  campaign?: string;
  candidateVariant?: string;
  controlVariant?: string;
  decision?: string;
  evidenceStrength?: decimal;
  firstObservedAt?: utcDateTime;
  id: string;
  lastObservedAt?: utcDateTime;
  name?: string;
  normalizedEffect?: decimal;
  primaryMetric?: string;
  primarySource?: string;
  provenance?: Values.Provenance;
  readiness?: string;
  state?: string;
  timestamp?: utcDateTime;
  @parent("workflows")
  workflowId: string;
}

@runPartition
@sampledBundle
model experimentAssignments {
  artifactLink?: string;
  campaign?: string;
  exclusionReason?: string;
  @parent("experiments")
  experimentId: string;
  id: string;
  included?: boolean;
  provenance?: Values.Provenance;
  @parent("runs")
  runId: string;
  timestamp?: utcDateTime;
  traceLink?: string;
  variant?: string;
}

@sampledBundle
model graders {
  direction?: string;
  displayName?: string;
  firstObservedAt?: utcDateTime;
  id: string;
  lastObservedAt?: utcDateTime;
  name?: string;
  provenance?: Values.Provenance;
  role?: string;
  sourceGraderId?: string;
  threshold?: decimal;
  timestamp?: utcDateTime;
  unit?: string;
  @parent("workflows")
  workflowId: string;
}

@runPartition
@sampledBundle
model graderObservations {
  auditId?: string;
  baselineValue?: decimal;
  deltaFromBaseline?: decimal;
  evaluatorDigest?: string;
  evidenceLink?: string;
  exclusionReason?: string;
  @parent("experiments", false)
  experimentId?: string;
  @parent("graders")
  graderId: string;
  graderLink?: string;
  id: string;
  included?: boolean;
  maturityStatus?: string;
  provenance?: Values.Provenance;
  role?: string;
  @parent("runs")
  runId: string;
  status?: string;
  timestamp?: utcDateTime;
  value?: decimal;
  variant?: string;
}

@sampledBundle
model evals {
  direction?: string;
  displayName?: string;
  firstObservedAt?: utcDateTime;
  id: string;
  lastObservedAt?: utcDateTime;
  name?: string;
  provenance?: Values.Provenance;
  question?: string;
  requestedModel?: string;
  role?: string;
  sourceEvalId?: string;
  timestamp?: utcDateTime;
  @parent("workflows")
  workflowId: string;
}

@runPartition
@sampledBundle
model evalObservations {
  auditId?: string;
  direction?: string;
  @parent("evals")
  evalId: string;
  evalLink?: string;
  evalResult?: string;
  exclusionReason?: string;
  @parent("experiments", false)
  experimentId?: string;
  id: string;
  included?: boolean;
  provenance?: Values.Provenance;
  role?: string;
  @parent("runs")
  runId: string;
  status?: string;
  timestamp?: utcDateTime;
  variant?: string;
}

model marketplacePackages {
  addCommand?: string;
  artwork?: string;
  description?: string;
  forks?: int64;
  icon?: string;
  id: string;
  name?: string;
  observedAt?: utcDateTime;
  path?: string;
  publisher?: string;
  readme?: string;
  readmePath?: string;
  ref?: string;
  registryId?: string;
  registryName?: string;
  registryPrecedence?: int64;
  repository?: string;
  repositoryLink?: Values.Link;
  resolvedCommit?: string;
  sourceCoordinate?: string;
  stars?: int64;
  version?: string;
}

namespace Values {
  model OperationalValueFact {
    `fact-kind`: string;
    `fact-id`: string;
    `parent-id`?: string;
    `related-id`?: string;
    label?: string;
    title?: string;
    url?: string;
    at?: utcDateTime;
    `accepted-at`?: utcDateTime;
    `end-at`?: utcDateTime;
    value?: decimal;
    ref?: string;
    state?: string;
    origin?: string;
    scope?: string;
    `coverage-status`?: string;
    status?: string;
    detail?: string;
    goal?: string;
    acceptance?: string;
    `metric-id`?: string;
    `metric-name`?: string;
    `metric-unit`?: string;
    `metric-direction`?: string;
    `metric-version`?: string;
    `metric-formula`?: string;
    `result-label`?: string;
    `measurement-basis`?: string;
    `snapshot-name`?: string;
    `snapshot-unit`?: string;
    `snapshot-direction`?: string;
    `snapshot-id`?: string;
    `snapshot-version`?: string;
    `snapshot-formula`?: string;
    `outputs-available`?: boolean;
    `repository-state-available`?: boolean;
    `checks-available`?: boolean;
    `items-available`?: boolean;
    `output-unit`?: string;
    `implementation-unit`?: string;
    `output-scope`?: string;
    `output-coverage`?: string;
    `workflow-name`?: string;
    `cadence-days`?: decimal;
    `maturation-days`?: decimal;
  }
  /** Intrinsic relation-specific link from Dashboard Language section 9.1. */
  model Link {
    relation?: string;
    href?: string;
    label?: string;
  }

  model Provenance {
    source?: string;
    sourceId?: string;
    observedAt?: utcDateTime;
  }
}

namespace Inputs {
    model transactions {
      id: string;
      kind?: string;
      createdAt?: utcDateTime;
      payloadScope?: string;
      payloadHash?: string;
      payloadEtag?: string;
      records?: int64;
      committedRecords?: int64;
      rawPayloadRecords?: int64;
      rawRuns?: int64;
      agenticRunRecords?: int64;
      agenticRuns?: int64;
      duplicateRawRunObservations?: int64;
      duplicateAgenticRunObservations?: int64;
      unenrichedRuns?: int64;
      error?: string;
    }

    model securityFindings {
      id: string;
      organization?: string;
      repository?: string;
      workflow?: string;
      run?: string;
      `observed-at`?: utcDateTime;
      `run-link`?: Values.Link;
      `rollout-mode`?: string;
      `usable-verdict-percent`?: decimal;
      `prompt-injection-detected`?: boolean;
      `secret-leak-detected`?: boolean;
      `malicious-patch-detected`?: boolean;
      `inspection-warning-count`?: int64;
      `inspection-warning`?: string;
      `smell-summary`?: string;
      `job-status`?: string;
      `job-conclusion`?: string;
      `job-duration-seconds`?: decimal;
      runner?: string;
      engine?: string;
      `requested-model`?: string;
      `resolved-model`?: string;
    }

    model outcomePerformance {
      id: string;
      organization?: string;
      repository?: string;
      workflow?: string;
      run?: string;
      `run-conclusion`?: string;
      `rollout-mode`?: string;
      `safe-output-kind`?: string;
      `outcome-title`?: string;
      `outcome-status`?: string;
      `observed-at`?: utcDateTime;
      `run-link`?: Values.Link;
    }

    model workItems {
      id: string;
      `work-item-id`?: string;
      name?: string;
      objective?: string;
      organization?: string;
      repository?: string;
      workflow?: string;
      `workflow-name`?: string;
      `workflow-icon`?: string;
      `workflow-role`?: string;
      scope?: string;
      domain?: string;
      campaign?: string;
      `campaign-name`?: string;
      `campaign-icon`?: string;
      `work-type`?: string;
      `lifecycle-state`?: string;
      phase?: string;
      reason?: string;
      `reason-evidence-class`?: string;
      `next-action`?: string;
      `next-actor`?: string;
      `safe-output-kind`?: string;
      `waiting-on`?: string;
      `waiting-since`?: utcDateTime;
      owner?: string;
      `consequence-tier`?: string;
      `verification-state`?: string;
      `outcome-state`?: string;
      `started-at`?: utcDateTime;
      `ended-at`?: utcDateTime;
      `observed-at`?: utcDateTime;
      `evidence-link`?: Values.Link;
      `repository-link`?: Values.Link;
      `run-link`?: Values.Link;
    }
  }

  namespace Runtime {
    /** Administrator-only process-local webhook counters; never persisted as evidence. */
    model webhookStatus {
      configured: boolean;
      scope: string;
      received: int64;
      `in-flight`: int64;
      verified: int64;
      accepted: int64;
      rejected: int64;
      failed: int64;
      duplicates: int64;
      pings: int64;
      `last-received-at`?: utcDateTime;
      `last-accepted-at`?: utcDateTime;
      `last-failure-at`?: utcDateTime;
      `last-ping-at`?: utcDateTime;
    }
    /** Local authorized queue projections, unavailable without an execution host. */
    model cliCommands {
      id: int64;
      actionId: string;
      command: string;
      status: string;
      createdAt: int64;
      startedAt?: int64;
      completedAt?: int64;
      deadline?: int64;
      completionWeek?: string;
      retryOf?: int64;
      exitCode?: int64;
      error?: string;
      reason?: string;
      outputBytes?: int64;
      outputChunks?: int64;
      outputTruncated?: boolean;
    }
    model cliCommandSummary {
      total: int64;
      running: int64;
      revision: int64;
      `paused-reason`?: string;
    }
    model cliCommandStatuses {
      status: string;
    }
    model simulationDays {
      day: int64;
      date: utcDateTime;
    }
    model githubQuotaUsage {
      `observed-at`: utcDateTime;
      scope: string;
      bucket: string;
      app?: string;
      installation?: int64;
      resource?: string;
      buckets: int64;
      limit: int64;
      used: int64;
      reserved: int64;
      `usage-percent`: decimal;
    }
    model collectionHealth {
      configured: boolean;
      health: string;
      `queue-depth`: int64;
      `pending-tasks`: int64;
      `oldest-pending-age`: string;
      `dead-letters`: int64;
      backfill: string;
      `backfill-failures`: int64;
      `backfill-queued-run-tasks`: int64;
      `webhook-load`: int64;
      `collection-load`: int64;
      `failure-load`: int64;
      `last-projected`?: string;
      `health-revision`: int64;
      `last-webhook-at`?: string;
      `last-failure-at`?: string;
      `last-failure-code`?: string;
      `last-success-at`?: string;
      `webhook-received`: int64;
      `webhook-duplicate`: int64;
      `webhook-admission-failed`: int64;
      `task-queued`: int64;
      `task-coalesced`: int64;
      `collection-succeeded`: int64;
      `collection-failed`: int64;
      `collection-retried`: int64;
      `collection-dead-lettered`: int64;
    }
  }

namespace Storage {
  /** Credential-partitioned normalized GitHub resources, separate from canonical evidence. */
  model caoGithubResources {
    `namespace`: string;
    id: string;
    etag: string;
    upstreamEtag: string;
    validatedAt: utcDateTime;
    release: boolean;
    fullName?: string;
    description?: string;
    htmlUrl: string;
    defaultBranch?: string;
    stargazersCount?: int64;
    forksCount?: int64;
    openIssuesCount?: int64;
    archived?: boolean;
    visibility?: string;
    tagName?: string;
    name?: string;
    publishedAt?: string;
    draft?: boolean;
    prerelease?: boolean;
  }
  model caoContract {
    digest: string;
  }
  model caoState {
    `namespace`: string;
    revision: int64;
    dataRevision: string;
    evaluatedAt: utcDateTime;
    schemaVersion: int64;
  }

  /** Ownership and retry identity only; measured evidence remains in the typed canonical tables. */
  model caoValuePublications {
    `namespace`: string;
    packetId: string;
    digest: string;
    publishedAt: utcDateTime;
  }

  @sampledBundle
  model caoQuality {
    `namespace`: string;
    collection: string;
    availability: string;
    completeness: string;
    freshness: string;
    asOf?: utcDateTime;
    retrievedAt?: utcDateTime;
  }

  /** Private, atomically replaced snapshot metadata; never canonical ingestion inputs. */
  namespace Statistical {
    model statisticalFrame {
      id: string;
      availability: string;
      completeness: string;
      freshness: string;
      error?: string;
      `source-data-revision`: string;
      `source-revision`: int64;
      `storage-mode`: string;
      version?: string;
      `frame-version`?: string;
      `frame-id`?: string;
      seed?: string;
      `watermark-policy`?: string;
      watermark?: string;
      `window-start`?: string;
      population?: string;
      capacity?: int64;
      `timestamp-field`?: string;
      `identity-field`?: string;
      `allocation-policy`?: string;
      `failure-reserve-fraction`?: decimal;
      `failure-reserve-allocation`?: int64;
      `selection-stage`?: string;
      `hot-exclusion-policy`?: string;
      `hot-exclusion-default`?: int64;
      `input-count`?: int64;
      `population-count`?: int64;
      `selected-count`?: int64;
      `invalid-count`?: int64;
      `incomplete-count`?: int64;
      `duplicate-conflicts`?: int64;
      `quarantined-count`?: int64;
      `hot-excluded-count`?: int64;
      `weight-valid`: boolean;
      `full-population-estimable`?: boolean;
      `input-row-limit`?: int64;
      `input-byte-limit`?: int64;
    }
    model statisticalMembership {
      id: string;
      organization?: string;
      repository?: string;
      run?: string;
      `statistical-frame-id`: string;
      `statistical-stratum`: string;
      `statistical-priority`: string;
      `statistical-population`: int64;
      `statistical-allocation`: int64;
      `statistical-weight-valid`: boolean;
      `statistical-probability`?: decimal;
      `statistical-weight`?: decimal;
    }
    model statisticalStrata {
      id: string;
      stratum: string;
      population: int64;
      allocation: int64;
      cutoff?: string;
    }
  }

  /** Collector-owned state survives replacement of the derived canonical projection. */
  model caoRepositoryLifecycle {
    `namespace`: string;
    id: string;
    owner: string;
    name: string;
    lifecycle: string;
    changedAt: utcDateTime;
  }
}
