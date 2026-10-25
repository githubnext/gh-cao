import { createTypeSpecLibrary, emitFile, getNamespaceFullName, navigateProgram } from '@typespec/compiler';

export const $lib = createTypeSpecLibrary({ name: 'cao-trace', diagnostics: {} });

const namespace = 'Cao.Trace';
const tableNames = [
  'TraceEvents', 'TraceContentParts', 'TraceManifest',
  'TraceSampleFrames', 'TraceSampleMembership', 'TraceDatasets',
  'TraceEpisodes', 'TraceOutcomes', 'TraceMiningManifest'
];
const kinds = new Map([
  ['string', 'string'], ['int64', 'long'], ['float64', 'real'], ['utcDateTime', 'datetime']
]);

function kind(property) {
  const type = property.type;
  if (property.optional) throw new Error(`Trace fields must be required: ${property.name}`);
  if (property.name === 'ContractVersion' && type.kind === 'Number' && type.value === 1) return 'long';
  if (type.kind === 'Scalar' && getNamespaceFullName(type.namespace) === 'TypeSpec' && kinds.has(type.name)) {
    return kinds.get(type.name);
  }
  if (type.kind === 'Model' && type.name === 'Array' && type.indexer?.value.kind === 'Scalar'
    && type.indexer.value.name === 'string' && getNamespaceFullName(type.indexer.value.namespace) === 'TypeSpec') {
    return 'dynamic';
  }
  throw new Error(`Unsupported trace type: ${property.name}: ${type.kind} ${type.name ?? ''}`);
}

function properties(model) {
  return [...(model.baseModel ? properties(model.baseModel) : []), ...model.properties.values()];
}

export async function $onEmit(context) {
  const models = new Map();
  navigateProgram(context.program, {
    model(model) {
      if (getNamespaceFullName(model.namespace) === namespace) models.set(model.name, model);
    }
  });
  const identity = models.get('Identity');
  if (models.size !== tableNames.length + 1 || !identity || identity.baseModel || identity.indexer
    || identity.properties.get('ContractVersion')?.type.kind !== 'Number'
    || identity.properties.get('ContractVersion')?.type.value !== 1) {
    throw new Error('Trace contract requires nine tables, Identity, and literal ContractVersion 1');
  }
  const tables = tableNames.map((name) => {
    const model = models.get(name);
    if (!model || model.baseModel !== identity || model.indexer) throw new Error(`Invalid trace table: ${name}`);
    const columns = properties(model).map((property) => {
      if (!/^[A-Z][a-zA-Z0-9]*$/.test(property.name)) throw new Error(`Invalid trace column: ${property.name}`);
      return { name: property.name, kind: kind(property) };
    });
    if (new Set(columns.map(({ name }) => name)).size !== columns.length) {
      throw new Error(`Duplicate trace columns: ${name}`);
    }
    return { name, columns };
  });
  const go = [
    '// Code generated from server/spec/trace.tsp. DO NOT EDIT.', '', 'package adxtrace', '',
    'import _ "embed"', '', 'const ContractVersion = 1', '', 'var Tables = map[string][]string{',
    ...tables.map(({ name, columns }) =>
      `\t${JSON.stringify(name)}: {${columns.map(({ name }) => JSON.stringify(name)).join(', ')}},`),
    '}', '', '//go:embed schema.gen.kql', 'var schema string', '',
    '// Schema returns the operator-applied trace KQL; runtime never provisions ADX.',
    'func Schema() string { return schema }', ''
  ];
  const kql = ['// Generated from server/spec/trace.tsp. Do not edit.'];
  for (const { name, columns } of tables) {
    const mapping = columns.map(({ name }) => ({ column: name, Properties: { Path: `$.${name}` } }));
    kql.push(
      `.create-merge table ${name} (${columns.map(({ name, kind }) => `${name}:${kind}`).join(', ')});`,
      `.create-or-alter table ${name} ingestion json mapping 'cao_v1' '${JSON.stringify(mapping)}';`,
      `.alter table ${name} policy streamingingestion enable;`,
      `.alter-merge table ${name} policy retention softdelete = 7d recoverability = enabled;`
    );
  }
  kql.push(`.create-or-alter function cao_ReadyTraces() {
    let events = TraceEvents
        | summarize by Namespace, RunId, ProjectionId, RowDigest
        | summarize ActualEventCount = count() by Namespace, RunId, ProjectionId;
    let parts = TraceContentParts
        | summarize by Namespace, RunId, ProjectionId, RowDigest
        | summarize ActualPartCount = count() by Namespace, RunId, ProjectionId;
    TraceManifest
    | summarize arg_max(SourceRevision, *) by Namespace, RunId
    | where State == "ready"
    | join kind=leftouter (events) on Namespace, RunId, ProjectionId
    | join kind=leftouter (parts) on Namespace, RunId, ProjectionId
    | where EventCount == coalesce(ActualEventCount, 0) and PartCount == coalesce(ActualPartCount, 0)
    | project Namespace, RunId, RunAttempt, ProjectionId, RowDigest, ContractVersion,
        SourceRevision, Repository, ArtifactDigest, PolicyDigest, EventCount, PartCount,
        ContentDigest, State, Gaps, CapturedAt
};`);
  for (const name of ['TraceEvents', 'TraceContentParts']) {
    kql.push(`.create-or-alter function cao_${name}() {
    ${name}
    | summarize take_any(*) by Namespace, RunId, RunAttempt, ProjectionId, RowDigest
    | join kind=inner (cao_ReadyTraces() | project Namespace, RunId, ProjectionId) on Namespace, RunId, ProjectionId
};`);
  }
  kql.push(`.create-or-alter function cao_TraceSampleFrames() {
    let sources = cao_ReadyTraces() | project Namespace, TraceId = RunId, TraceVersion = ProjectionId, SourceReady = true;
    let membership = TraceSampleMembership
        | summarize arg_max(SourceRevision, *) by Namespace, FrameId, TraceId
        | join kind=leftouter (sources) on Namespace, TraceId, TraceVersion
        | summarize ActualMembers = count(), ReadyMembers = countif(State == "ready" and isnotnull(SourceReady)),
            Versions = make_set(ProjectionId, 2) by Namespace, FrameId;
    TraceSampleFrames
    | summarize arg_max(SourceRevision, *) by Namespace, RunId
    | where State == "ready" and SourceGaps == 0 and Population >= Selected and Selected >= 0
    | join kind=leftouter (membership) on Namespace, $left.RunId == $right.FrameId
    | where Selected == coalesce(ActualMembers, 0) and Selected == coalesce(ReadyMembers, 0)
        and (Selected == 0 or (array_length(Versions) == 1 and tostring(Versions[0]) == ProjectionId))
    | project Namespace, RunId, RunAttempt, ProjectionId, RowDigest, ContractVersion, SourceRevision,
        Repository, Day, Cutoff, Mode, SeedDigest, Algorithm, Population, Selected, SourceGaps, Excluded, MembershipDigest, State
};`);
  kql.push(`.create-or-alter function cao_TraceSampleMembership() {
    TraceSampleMembership
    | summarize arg_max(SourceRevision, *) by Namespace, FrameId, TraceId
    | where State == "ready"
    | join kind=inner (cao_TraceSampleFrames() | project Namespace, FrameId = RunId) on Namespace, FrameId
    | join kind=inner (cao_ReadyTraces() | project Namespace, TraceId = RunId, TraceVersion = ProjectionId) on Namespace, TraceId, TraceVersion
};`);
  kql.push(`// Historical reads cannot resurrect currently invalidated evidence or withdrawn traces.
.create-or-alter function cao_TraceMiningAt(namespace:string, asOf:datetime) {
    let current = TraceMiningManifest
        | summarize arg_max(SourceRevision, *) by Namespace, RunId
        | where State == "ready"
        | project Namespace, RunId;
    let traces = cao_ReadyTraces() | project Namespace, RunId, TraceVersion = ProjectionId;
    let episodes = TraceEpisodes
        | summarize by Namespace, RunId, ProjectionId, RowDigest
        | summarize ActualEpisodes = count() by Namespace, RunId, ProjectionId;
    let outcomes = TraceOutcomes
        | summarize by Namespace, RunId, ProjectionId, RowDigest
        | summarize ActualOutcomes = count() by Namespace, RunId, ProjectionId;
    TraceMiningManifest
    | where (isempty(namespace) or Namespace == namespace) and CapturedAt <= asOf
    | summarize arg_max(SourceRevision, *) by Namespace, RunId
    | where State == "ready" and FreshUntil > asOf
    | join kind=inner (current) on Namespace, RunId
    | join kind=inner (traces) on Namespace, RunId, TraceVersion
    | join kind=leftouter (episodes) on Namespace, RunId, ProjectionId
    | join kind=leftouter (outcomes) on Namespace, RunId, ProjectionId
    | where EpisodeCount == coalesce(ActualEpisodes, 0) and OutcomeCount == coalesce(ActualOutcomes, 0)
    | project Namespace, RunId, RunAttempt, ProjectionId, CanonicalNamespace, CanonicalRunId,
        CanonicalRevision, Repository, TraceVersion, EpisodeCount, OutcomeCount, State, Gaps, CapturedAt, FreshUntil
};`);
  kql.push(`.create-or-alter function cao_TraceMining() {
    cao_TraceMiningAt("", now())
};`);
  for (const name of ['TraceEpisodes', 'TraceOutcomes']) {
    kql.push(`.create-or-alter function cao_${name}() {
    ${name}
    | summarize take_any(*) by Namespace, RunId, RunAttempt, ProjectionId, RowDigest
    | join kind=inner (cao_TraceMining() | project Namespace, RunId, ProjectionId, CanonicalRevision, TraceVersion) on Namespace, RunId, ProjectionId
};`);
  }
  kql.push(`.create-or-alter function cao_TraceOutcomesAt(namespace:string, asOf:datetime) {
    TraceOutcomes
    | where Namespace == namespace
    | summarize take_any(*) by Namespace, RunId, RunAttempt, ProjectionId, RowDigest
    | join kind=inner (cao_TraceMiningAt(namespace, asOf)
        | project Namespace, RunId, ProjectionId, CanonicalRevision, TraceVersion) on Namespace, RunId, ProjectionId
    | extend AvailableAsOf = isnotempty(AuditId) and todatetime(ObservedAt) <= asOf and todatetime(ObjectUpdatedAt) <= asOf
};`);
  kql.push(`// This is a selected-output denominator, never a probability-weighted population or correctness rate.
.create-or-alter function cao_TraceOutcomeSummary(namespace:string, asOf:datetime) {
    cao_TraceOutcomesAt(namespace, asOf)
    | summarize arg_max(CanonicalRevision, *) by Namespace, CanonicalNamespace, OutputId
    | extend Status = iff(AvailableAsOf, Status, "unknown")
    | summarize EligibleOutputs = count(), Accepted = countif(Status == "accepted"),
        Rejected = countif(Status == "rejected"), Pending = countif(Status == "pending"),
        Lifecycle = countif(Status == "lifecycle"), Unknown = countif(Status == "unknown"),
        UnknownAttempts = countif(ProducingAttempt == 0), ObservedAt = max(todatetime(ObservedAt))
        by Namespace, CanonicalNamespace
    | extend AsOf = asOf, Scope = "selected_outputs", EvidenceState = "partial"
};`);
  kql.push(`// Exactly one row per producing run attempt; do not multiply run usage by ObservedHops.
.create-or-alter function cao_TraceEpisodeRuns(namespace:string, episodeId:string) {
    let nodes = cao_TraceEpisodes()
        | where Namespace == namespace and EpisodeId == episodeId and Kind == "episode.membership";
    let parents = nodes | where isnotempty(HopId)
        | summarize by Namespace, EpisodeId, ParentHopId = HopId
        | extend ParentObserved = true;
    nodes
    | join kind=leftouter (parents) on Namespace, EpisodeId, ParentHopId
    | summarize ObservedHops = count(), MissingParents = countif(isnotempty(ParentHopId) and isnull(ParentObserved)),
        TraceIds = make_set(RunId, 100000)
        by Namespace, CanonicalNamespace, EpisodeId, Repository, CanonicalRunId, RunAttempt
    | extend Scope = "selected_runs", EvidenceState = "partial"
};`);
  kql.push(`.create-or-alter function cao_TraceDatasets() {
    let datasetLatest = TraceDatasets | summarize arg_max(SourceRevision, *) by Namespace, DatasetId, RunId;
    let sources = cao_ReadyTraces() | project Namespace, RunId, ProjectionId, SourceReady = true;
    let mining = cao_TraceMining() | project Namespace, RunId, MiningVersion = ProjectionId, MiningReady = true;
    let members = datasetLatest | join kind=leftouter (sources) on Namespace, RunId, ProjectionId
        | join kind=leftouter (mining) on Namespace, RunId, MiningVersion
        | extend SourceReady = iff(isnotempty(MiningVersion) and isnull(MiningReady), bool(null), SourceReady);
    let complete = members
        | summarize ActualMemberCount = count(), ExpectedMemberCount = min(MemberCount),
            MaximumMemberCount = max(MemberCount), DigestValues = make_set(DatasetDigest, 2),
            EmptyDigests = countif(isempty(DatasetDigest)),
            InvalidMembers = countif(State != "ready" or isnull(SourceReady) or isnull(MemberCount) or MemberCount <= 0)
            by Namespace, DatasetId
        | where ExpectedMemberCount > 0 and ExpectedMemberCount == MaximumMemberCount
            and ActualMemberCount == ExpectedMemberCount and array_length(DigestValues) == 1
            and EmptyDigests == 0 and InvalidMembers == 0
        | project Namespace, DatasetId;
    members
    | join kind=inner (complete) on Namespace, DatasetId
    | project Namespace, RunId, RunAttempt, ProjectionId, RowDigest, ContractVersion,
        SourceRevision, DatasetId, MemberCount, DatasetDigest, Transform, Split, TaskGroup, Evaluator, QualityLabel, State,
        MiningVersion, OutcomeId, Disposition, LabelRuleVersion, LabelCutoff
};`);
  kql.push(`// maxEvents bounds a zero-based sequence prefix, not a whole-trace completeness claim.
.create-or-alter function cao_TraceDocuments(namespace:string, traceId:string, maxEvents:long = 1024) {
    let eventLimit = min_of(max_of(maxEvents, 1), 32768);
    let parts = cao_TraceContentParts()
        | where Namespace == namespace and RunId == traceId and Sequence >= 0 and Sequence < eventLimit
        | summarize Variants = count(), Content = take_any(Content) by Namespace, RunId, RunAttempt, ProjectionId, Sequence, Part
        | summarize ActualPartCount = count(), AmbiguousParts = countif(Variants != 1),
            FirstPart = min(Part), LastPart = max(Part),
            PartNumbers = make_list(Part, 65536), Chunks = make_list(Content, 65536)
            by Namespace, RunId, RunAttempt, ProjectionId, Sequence
        | where AmbiguousParts == 0 and FirstPart == 0 and LastPart == ActualPartCount - 1
            and array_length(PartNumbers) == ActualPartCount and array_length(Chunks) == ActualPartCount
        | extend (OrderedParts, OrderedChunks) = array_sort_asc(PartNumbers, Chunks)
        | extend Content = strcat_array(OrderedChunks, "")
        | project Namespace, RunId, RunAttempt, ProjectionId, Sequence, ActualPartCount, Content;
    cao_TraceEvents()
    | where Namespace == namespace and RunId == traceId and Sequence >= 0 and Sequence < eventLimit
    | summarize EventVariants = count(), take_any(*) by Namespace, RunId, RunAttempt, ProjectionId, Sequence
    | where EventVariants == 1
    | join kind=inner (parts) on Namespace, RunId, RunAttempt, ProjectionId, Sequence
    | where PartCount > 0 and PartCount == ActualPartCount and hash_sha256(Content) == ContentDigest
    | extend Event = parse_json(Content)
    | where gettype(Event) == "dictionary"
    | project Namespace, RunId, RunAttempt, ProjectionId, RowDigest, ContractVersion,
        Sequence, Type, EventId, ParentId, EpisodeId, HopId, ParentHopId, ToolCallId,
        SessionId, Timestamp, PartCount, ContentDigest, Content, Event
    | order by Sequence asc
};`);
  kql.push(`.create-or-alter function cao_TraceTimeline(namespace:string, traceId:string, maxEvents:long = 1024) {
    cao_TraceDocuments(namespace, traceId, maxEvents)
    | project Namespace, RunId, RunAttempt, ProjectionId, Sequence, EventId, ParentId,
        EpisodeId, HopId, ParentHopId, ToolCallId, SessionId, Timestamp, Type, Event
    | order by Sequence asc
};`);
  kql.push(`// Context contains only supplied workflow.info fields; engine ID never identifies a harness.
.create-or-alter function cao_TraceContext(namespace:string, traceId:string, maxEvents:long = 1024) {
    cao_TraceDocuments(namespace, traceId, maxEvents)
    | where Type == "workflow.info"
    | project Namespace, RunId, RunAttempt, ProjectionId, Sequence, EventId,
        EpisodeId, HopId, ParentHopId, EngineId = tostring(Event.data.engineId),
        EngineHarness = Event.data.engineHarness,
        AgentVersion = tostring(Event.data.agentVersion), CliVersion = tostring(Event.data.cliVersion),
        AwfVersion = tostring(Event.data.awfVersion), Context = Event.data
    | order by Sequence asc
};`);
  kql.push(`// Observed candidates are investigation inputs, not diagnoses or causal findings.
.create-or-alter function cao_TraceConfigurationCandidates(namespace:string, traceId:string, maxEvents:long = 1024) {
    cao_TraceDocuments(namespace, traceId, maxEvents)
    | where Type contains "mismatch" or Type contains "configuration" or Type endswith ".error"
        or tostring(Event.data.status) in ("error", "failed", "failure") or Event.data.success == false
    | extend Candidate = iff(Type contains "mismatch" or Type contains "configuration",
        "configuration-candidate", "observed-error-candidate")
    | project Namespace, RunId, RunAttempt, ProjectionId, Sequence, EventId,
        EpisodeId, HopId, ParentHopId, ToolCallId, SessionId, Timestamp, Type, Candidate, Event
    | order by Sequence asc
};`);
  kql.push(`// Sizes distinguish approved event bytes, approved output text and producer-reported bytes.
.create-or-alter function cao_TraceToolResponseCandidates(namespace:string, traceId:string, minimumBytes:long = 65536, maxEvents:long = 1024) {
    cao_TraceDocuments(namespace, traceId, maxEvents)
    | where Type in ("tool.execution_complete", "mcp.rpc.response")
    | extend ApprovedEventBytes = string_size(Content),
        ApprovedOutputTextBytes = iff(isnull(Event.data.output), long(null), string_size(tostring(Event.data.output))),
        ReportedOutputBytes = tolong(Event.data.outputSize)
    | where max_of(ApprovedEventBytes, ApprovedOutputTextBytes, ReportedOutputBytes) >= max_of(minimumBytes, 0)
    | project Namespace, RunId, RunAttempt, ProjectionId, Sequence, EventId,
        ToolCallId, SessionId, Type, ToolName = tostring(Event.data.toolName),
        Candidate = "large-tool-response-candidate", ApprovedEventBytes, ApprovedOutputTextBytes,
        ReportedOutputBytes, Event
    | order by Sequence asc
};`);
  kql.push(`// Repeated starts do not prove duplicate work, avoidable retries, or achievable savings.
.create-or-alter function cao_TraceRepeatedToolCandidates(namespace:string, traceId:string, minimumStarts:long = 2, maxEvents:long = 1024) {
    cao_TraceDocuments(namespace, traceId, maxEvents)
    | where Type == "tool.execution_start"
    | extend ToolName = tostring(Event.data.toolName), ServerName = tostring(Event.data.mcpServerName)
    | where isnotempty(ToolName)
    | summarize ObservedStarts = count(), Sequences = make_list(Sequence, 32768),
        ToolCallIds = make_list(ToolCallId, 32768)
        by Namespace, RunId, RunAttempt, ProjectionId, SessionId, ServerName, ToolName
    | where ObservedStarts >= max_of(minimumStarts, 2)
    | extend (Sequences, ToolCallIds) = array_sort_asc(Sequences, ToolCallIds)
    | extend Candidate = "repeated-tool-candidate"
    | order by ObservedStarts desc, ToolName asc
};`);
  await emitFile(context.program, { path: `${context.emitterOutputDir}/schema.gen.go`, content: go.join('\n') });
  await emitFile(context.program, { path: `${context.emitterOutputDir}/schema.gen.kql`, content: kql.join('\n') + '\n' });
}
