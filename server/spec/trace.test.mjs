import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { compile, getNamespaceFullName, navigateProgram, NodeHost } from '@typespec/compiler';
import { $onEmit } from './trace-emitter.mjs';

const entry = fileURLToPath(new URL('trace.tsp', import.meta.url));
const program = await compile(NodeHost, entry, { noEmit: true });
assert.deepEqual(program.diagnostics, []);
const models = new Map();
navigateProgram(program, {
  model(model) {
    if (getNamespaceFullName(model.namespace) === 'Cao.Trace') models.set(model.name, model);
  }
});

const identity = {
  Namespace: 'string', RunId: 'string', RunAttempt: 'long',
  ProjectionId: 'string', RowDigest: 'string', ContractVersion: 'long'
};
const tables = {
  TraceEvents: {
    Sequence: 'long', Type: 'string', EventId: 'string', ParentId: 'string',
    EpisodeId: 'string', HopId: 'string', ParentHopId: 'string', ToolCallId: 'string',
    SessionId: 'string', Timestamp: 'string', PartCount: 'long', ContentDigest: 'string'
  },
  TraceContentParts: { Sequence: 'long', Part: 'long', Content: 'string' },
  TraceManifest: {
    SourceRevision: 'long', Repository: 'string', ArtifactDigest: 'string', PolicyDigest: 'string',
    EventCount: 'long', PartCount: 'long', ContentDigest: 'string', State: 'string',
    Gaps: 'dynamic', CapturedAt: 'datetime'
  },
  TraceSampleFrames: {
    SourceRevision: 'long', Repository: 'string', Day: 'string', Cutoff: 'datetime',
    Mode: 'string', SeedDigest: 'string', Algorithm: 'string', Population: 'long',
    Selected: 'long', SourceGaps: 'long', Excluded: 'long', MembershipDigest: 'string', State: 'string'
  },
  TraceSampleMembership: {
    SourceRevision: 'long', FrameId: 'string', TraceId: 'string', TraceVersion: 'string',
    Probability: 'real', Weight: 'real', State: 'string'
  },
  TraceDatasets: {
    SourceRevision: 'long', DatasetId: 'string', MemberCount: 'long', DatasetDigest: 'string', Transform: 'string', Split: 'string',
    TaskGroup: 'string', Evaluator: 'string', QualityLabel: 'string', State: 'string',
    MiningVersion: 'string', OutcomeId: 'string', Disposition: 'string', LabelRuleVersion: 'string', LabelCutoff: 'string'
  },
  TraceEpisodes: {
    SourceRevision: 'long', CanonicalNamespace: 'string', CanonicalRunId: 'string', Repository: 'string',
    AuditId: 'string', Kind: 'string', EpisodeId: 'string', HopId: 'string', ParentHopId: 'string',
    ReferencedRunId: 'string', ReferencedRepository: 'string', ReferencedWorkflow: 'string',
    ObservedAt: 'string', Scope: 'string', EvidenceState: 'string', BindingVersion: 'string'
  },
  TraceOutcomes: {
    SourceRevision: 'long', CanonicalNamespace: 'string', CanonicalRunId: 'string', Repository: 'string',
    OutputId: 'string', OutputUrl: 'string', OutputType: 'string', AuditId: 'string', ProducingAttempt: 'long',
    Status: 'string', Signal: 'string', ObjectState: 'string', ObjectUpdatedAt: 'string', ObservedAt: 'string',
    AcquisitionSource: 'string', Scope: 'string', EvidenceState: 'string', LabelRuleVersion: 'string'
  },
  TraceMiningManifest: {
    SourceRevision: 'long', CanonicalNamespace: 'string', CanonicalRunId: 'string', CanonicalRevision: 'long',
    Repository: 'string', TraceVersion: 'string', EpisodeCount: 'long', OutcomeCount: 'long',
    State: 'string', Gaps: 'dynamic', CapturedAt: 'datetime', FreshUntil: 'datetime'
  }
};
const typeNames = new Map([
  ['string', 'string'], ['int64', 'long'], ['float64', 'real'], ['utcDateTime', 'datetime']
]);
const kql = readFileSync(new URL('../internal/adxtrace/schema.gen.kql', import.meta.url), 'utf8');
const go = readFileSync(new URL('../internal/adxtrace/schema.gen.go', import.meta.url), 'utf8');

test('trace TypeSpec declares exactly nine independent tables with required, capacity-invariant fields', () => {
  assert.deepEqual([...models.keys()].sort(), ['Identity', ...Object.keys(tables)].sort());
  assert.equal(models.get('Identity').properties.get('ContractVersion').type.value, 1);
  for (const [name, fields] of [['Identity', identity], ...Object.entries(tables)]) {
    const model = models.get(name);
    if (name !== 'Identity') assert.equal(model.baseModel, models.get('Identity'));
    assert.deepEqual([...model.properties.keys()], Object.keys(fields));
    for (const property of model.properties.values()) {
      assert.equal(property.optional, false, `${name}.${property.name} must be required`);
      const type = property.type;
      const actual = type.kind === 'Number' ? 'long'
        : type.kind === 'Model' && type.name === 'Array' && type.indexer.value.name === 'string' ? 'dynamic'
          : typeNames.get(type.name);
      assert.equal(actual, fields[property.name], `${name}.${property.name}`);
    }
  }
});

test('trace generated Go, native KQL columns, mappings, streaming and retention match TypeSpec', async () => {
  const directory = fileURLToPath(new URL(`.trace-spec-test-${randomUUID()}`, import.meta.url));
  mkdirSync(`${directory}/adxtrace`, { recursive: true });
  try {
    await $onEmit({ program, emitterOutputDir: `${directory}/adxtrace`, options: {} });
    assert.equal(execFileSync('gofmt', [`${directory}/adxtrace/schema.gen.go`], { encoding: 'utf8' }), go);
    assert.equal(readFileSync(`${directory}/adxtrace/schema.gen.kql`, 'utf8'), kql);
    assert.match(go, /package adxtrace/);
    assert.match(go, /const ContractVersion = 1/);
    assert.match(go, /var Tables = map\[string\]\[\]string\{/);
    assert.match(go, /\/\/go:embed schema\.gen\.kql/);
    assert.match(go, /func Schema\(\) string\s+\{ return schema \}/);
    for (const [name, fields] of Object.entries(tables)) {
      const columns = { ...identity, ...fields };
      const definition = kql.split(`.create-merge table ${name} (`)[1]?.split(');')[0];
      assert.equal(definition, Object.entries(columns).map(([name, kind]) => `${name}:${kind}`).join(', '));
      const mapping = JSON.parse(kql.split(`table ${name} ingestion json mapping 'cao_v1' '`)[1].split("';")[0]);
      assert.deepEqual(mapping, Object.keys(columns).map((name) => ({ column: name, Properties: { Path: `$.${name}` } })));
      const goColumns = go.match(new RegExp(`"${name}":\\s*\\{([^}]+)\\}`))?.[1];
      assert.deepEqual([...goColumns.matchAll(/"([^"]+)"/g)].map((match) => match[1]), Object.keys(columns));
      assert.ok(kql.includes(`.alter table ${name} policy streamingingestion enable;`));
      assert.ok(kql.includes(`.alter-merge table ${name} policy retention softdelete = 7d recoverability = enabled;`));
    }
    assert.doesNotMatch(kql, /skipvalidation|external table|materialized-view|purge/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('archive projection derives explicit Parquet/Go columns from the shared TypeSpec event contract', async () => {
  const directory = fileURLToPath(new URL(`.archive-spec-test-${randomUUID()}`, import.meta.url));
  mkdirSync(`${directory}/adxtrace`, { recursive: true });
  try {
    await $onEmit({ program, emitterOutputDir: `${directory}/adxtrace`, options: {} });
    const generated = readFileSync(`${directory}/sessionarchive/schema.gen.json`, 'utf8');
    assert.equal(generated, readFileSync(new URL('../internal/sessionarchive/schema.gen.json', import.meta.url), 'utf8'));
    const go = execFileSync('gofmt', [`${directory}/sessionarchive/schema.gen.go`], { encoding: 'utf8' });
    assert.equal(go, readFileSync(new URL('../internal/sessionarchive/schema.gen.go', import.meta.url), 'utf8'));
    const schema = JSON.parse(generated);
    assert.equal(schema.version, 1);
    const columns = Object.fromEntries(schema.columns.map(({ name, type }) => [name, type]));
    assert.equal(columns.PayloadJSON, 'VARCHAR');
    assert.equal(columns.Gaps, 'VARCHAR[]');
    assert.equal(columns.Sequence, 'BIGINT');
    assert.equal(columns.RunAttempt, 'BIGINT');
    assert.equal(columns.RunId, 'VARCHAR');
    assert.equal(columns.TraceId, 'VARCHAR');
    assert.equal(columns.PartCount, undefined);
    assert.equal(columns.RowDigest, undefined);
    for (const key of ['ArtifactDigest', 'PolicyDigest', 'CompletionDay', 'CapturedAt', 'ContentDigest']) {
      assert.equal(columns[key], 'VARCHAR');
    }
    assert.match(go, /PayloadJSON\s+string/);
    assert.match(go, /RunID\s+string\s+`json:"RunId"`/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

function curated(name) {
  return kql.split(`.create-or-alter function ${name}(`)[1]?.split('};')[0];
}

test('mining readiness binds independent versions, exact counts, source scope and bounded freshness', () => {
  const ready = curated('cao_TraceMiningAt');
  assert.match(ready, /arg_max\(SourceRevision, \*\) by Namespace, RunId\s*\| where State == "ready"/);
  assert.match(ready, /CapturedAt <= asOf/);
  assert.match(ready, /FreshUntil > asOf/);
  assert.match(ready, /join kind=inner \(current\) on Namespace, RunId/);
  assert.match(ready, /join kind=inner \(traces\) on Namespace, RunId, TraceVersion/);
  for (const table of ['Episodes', 'Outcomes']) {
    assert.match(ready, new RegExp(`Trace${table}\\s*\\| summarize by Namespace, RunId, ProjectionId, RowDigest`));
    assert.match(ready, new RegExp(`${table === 'Episodes' ? 'Episode' : 'Outcome'}Count == coalesce\\(Actual${table}, 0\\)`));
  }
  assert.doesNotMatch(ready, /\bdcount\b|TraceDatasets|TraceSampleMembership/);
  assert.match(curated('cao_TraceDatasets'), /join kind=leftouter \(mining\) on Namespace, RunId, MiningVersion/);
  assert.match(curated('cao_TraceDatasets'), /isnotempty\(MiningVersion\) and isnull\(MiningReady\), bool\(null\)/);
  assert.match(curated('cao_TraceOutcomeSummary'), /EligibleOutputs = count\(\)/);
  for (const status of ['accepted', 'rejected', 'pending', 'lifecycle', 'unknown']) {
    assert.ok(curated('cao_TraceOutcomeSummary').includes(`Status == "${status}"`));
  }
  assert.doesNotMatch(curated('cao_TraceOutcomeSummary'), /Weight|Probability|\bdcount\b/);
  assert.match(curated('cao_TraceEpisodeRuns'), /by Namespace, CanonicalNamespace, EpisodeId, Repository, CanonicalRunId, RunAttempt/);
  assert.match(curated('cao_TraceEpisodeRuns'), /MissingParents = countif/);
});

test('curated trace functions apply withdrawal after latest-version resolution and invalidate entire datasets', () => {
  assert.match(curated('cao_ReadyTraces'),
    /TraceManifest\s*\| summarize arg_max\(SourceRevision, \*\) by Namespace, RunId\s*\| where State == "ready"/);
  for (const name of ['TraceEvents', 'TraceContentParts']) {
    assert.match(curated(`cao_${name}`),
      /summarize take_any\(\*\) by Namespace, RunId, RunAttempt, ProjectionId, RowDigest/);
    assert.match(curated(`cao_${name}`),
      /join kind=inner \(cao_ReadyTraces\(\) \| project Namespace, RunId, ProjectionId\) on Namespace, RunId, ProjectionId/);
  }
  assert.match(curated('cao_TraceSampleFrames'),
    /summarize arg_max\(SourceRevision, \*\) by Namespace, RunId\s*\| where State == "ready"/);
  assert.match(curated('cao_TraceSampleMembership'),
    /summarize arg_max\(SourceRevision, \*\) by Namespace, FrameId, TraceId\s*\| where State == "ready"/);
  assert.match(curated('cao_TraceSampleMembership'),
    /cao_TraceSampleFrames\(\) \| project Namespace, FrameId = RunId\) on Namespace, FrameId/);
  assert.match(curated('cao_TraceSampleMembership'),
    /cao_ReadyTraces\(\) \| project Namespace, TraceId = RunId, TraceVersion = ProjectionId\) on Namespace, TraceId, TraceVersion/);
  assert.match(curated('cao_TraceDatasets'),
    /let datasetLatest = TraceDatasets \| summarize arg_max\(SourceRevision, \*\) by Namespace, DatasetId, RunId;/);
  assert.match(curated('cao_TraceDatasets'),
    /let sources = cao_ReadyTraces\(\) \| project Namespace, RunId, ProjectionId, SourceReady = true/);
  assert.match(curated('cao_TraceDatasets'),
    /datasetLatest \| join kind=leftouter \(sources\) on Namespace, RunId, ProjectionId/);
  assert.match(curated('cao_TraceDatasets'),
    /InvalidMembers = countif\(State != "ready" or isnull\(SourceReady\) or isnull\(MemberCount\) or MemberCount <= 0\)/);
  assert.match(curated('cao_TraceDatasets'), /join kind=inner \(complete\) on Namespace, DatasetId/);
  assert.match(curated('cao_TraceDatasets'), /SourceRevision, DatasetId, MemberCount, DatasetDigest, Transform, Split, TaskGroup, Evaluator, QualityLabel, State/);
  assert.doesNotMatch(curated('cao_TraceDatasets'), /by Namespace, DatasetId, RunId, ProjectionId/);
});

test('trace readiness requires exact distinct raw row counts without circular dependencies or approximate cardinality', () => {
  const ready = curated('cao_ReadyTraces');
  for (const [table, counts, column] of [
    ['TraceEvents', 'events', 'Event'], ['TraceContentParts', 'parts', 'Part']
  ]) {
    assert.ok(ready.includes(`let ${counts} = ${table}
        | summarize by Namespace, RunId, ProjectionId, RowDigest
        | summarize Actual${column}Count = count() by Namespace, RunId, ProjectionId;`));
    assert.ok(ready.includes(`join kind=leftouter (${counts}) on Namespace, RunId, ProjectionId`));
    assert.ok(ready.includes(`${column}Count == coalesce(Actual${column}Count, 0)`));
  }
  assert.ok(ready.indexOf('arg_max(SourceRevision, *)') < ready.indexOf('where State == "ready"'));
  assert.ok(ready.indexOf('where State == "ready"') < ready.indexOf('join kind=leftouter'));
  assert.match(ready, /project Namespace, RunId, RunAttempt, ProjectionId, RowDigest, ContractVersion/);
  assert.doesNotMatch(ready, /cao_\w+\(|\bdcount\b|count_distinct|make_set/);
});

test('dataset readiness requires a complete consistent positive membership and nonempty shared digest', () => {
  const datasets = curated('cao_TraceDatasets');
  assert.match(datasets, /let complete = members\s*\| summarize ActualMemberCount = count\(\), ExpectedMemberCount = min\(MemberCount\)/);
  assert.match(datasets, /MaximumMemberCount = max\(MemberCount\), DigestValues = make_set\(DatasetDigest, 2\)/);
  assert.match(datasets, /EmptyDigests = countif\(isempty\(DatasetDigest\)\)/);
  assert.match(datasets, /by Namespace, DatasetId\s*\| where ExpectedMemberCount > 0 and ExpectedMemberCount == MaximumMemberCount/);
  assert.match(datasets, /ActualMemberCount == ExpectedMemberCount and array_length\(DigestValues\) == 1/);
  assert.match(datasets, /EmptyDigests == 0 and InvalidMembers == 0/);
  assert.ok(datasets.indexOf('arg_max(SourceRevision, *)') < datasets.indexOf('State != "ready"'));
  assert.ok(datasets.indexOf('InvalidMembers == 0') < datasets.indexOf('join kind=inner (complete)'));
  assert.doesNotMatch(datasets, /where State == "ready"|by Namespace, DatasetId, (MemberCount|DatasetDigest)|\bdcount\b/);
});

test('sample frame readiness requires all current selected sources and a shared membership version', () => {
  const frames = curated('cao_TraceSampleFrames');
  assert.match(frames, /let sources = cao_ReadyTraces\(\)/);
  assert.match(frames, /arg_max\(SourceRevision, \*\) by Namespace, FrameId, TraceId/);
  assert.match(frames, /join kind=leftouter \(sources\) on Namespace, TraceId, TraceVersion/);
  assert.match(frames, /ReadyMembers = countif\(State == "ready" and isnotnull\(SourceReady\)\)/);
  assert.match(frames, /State == "ready" and SourceGaps == 0 and Population >= Selected and Selected >= 0/);
  assert.match(frames, /Selected == coalesce\(ActualMembers, 0\) and Selected == coalesce\(ReadyMembers, 0\)/);
  assert.match(frames, /Selected == 0 or \(array_length\(Versions\) == 1 and tostring\(Versions\[0\]\) == ProjectionId\)/);
  assert.doesNotMatch(frames, /cao_TraceSampleMembership\(\)|\bdcount\b/);
});

test('trace documents reconstruct ordered UTF-8 JSON only after contiguous parts, exact counts and digest verification', () => {
  const documents = curated('cao_TraceDocuments');
  assert.match(documents, /namespace:string, traceId:string, maxEvents:long = 1024/);
  assert.match(documents, /min_of\(max_of\(maxEvents, 1\), 32768\)/);
  assert.match(documents, /let parts = cao_TraceContentParts\(\)/);
  assert.match(documents, /cao_TraceEvents\(\)/);
  assert.equal(documents.match(/Namespace == namespace and RunId == traceId and Sequence >= 0 and Sequence < eventLimit/g).length, 2);
  assert.match(documents, /Variants = count\(\), Content = take_any\(Content\) by Namespace, RunId, RunAttempt, ProjectionId, Sequence, Part/);
  assert.match(documents, /ActualPartCount = count\(\), AmbiguousParts = countif\(Variants != 1\)/);
  assert.match(documents, /AmbiguousParts == 0 and FirstPart == 0 and LastPart == ActualPartCount - 1/);
  assert.match(documents, /array_length\(PartNumbers\) == ActualPartCount and array_length\(Chunks\) == ActualPartCount/);
  assert.match(documents, /array_sort_asc\(PartNumbers, Chunks\)/);
  assert.match(documents, /Content = strcat_array\(OrderedChunks, ""\)/);
  assert.match(documents, /EventVariants = count\(\), take_any\(\*\) by Namespace, RunId, RunAttempt, ProjectionId, Sequence/);
  assert.match(documents, /where EventVariants == 1/);
  assert.match(documents, /join kind=inner \(parts\) on Namespace, RunId, RunAttempt, ProjectionId, Sequence/);
  assert.match(documents, /PartCount > 0 and PartCount == ActualPartCount and hash_sha256\(Content\) == ContentDigest/);
  assert.match(documents, /Event = parse_json\(Content\)\s*\| where gettype\(Event\) == "dictionary"/);
  assert.match(documents, /project Namespace, RunId, RunAttempt, ProjectionId, RowDigest, ContractVersion/);
  assert.match(documents, /Sequence, Type, EventId, ParentId, EpisodeId, HopId, ParentHopId, ToolCallId/);
  assert.match(documents, /order by Sequence asc/);
  for (const [earlier, later] of [
    ['array_sort_asc', 'strcat_array'],
    ['PartCount == ActualPartCount', 'parse_json'],
    ['hash_sha256', 'parse_json'],
    ['parse_json', 'order by Sequence asc']
  ]) assert.ok(documents.indexOf(earlier) < documents.indexOf(later), `${earlier} before ${later}`);
  assert.doesNotMatch(documents, /base64|tostring\(parse_json|join kind=innerunique/);
});

test('trace analytical helpers are bounded, ready-document-backed and preserve observed provenance', () => {
  const helpers = [
    'cao_TraceTimeline', 'cao_TraceContext', 'cao_TraceConfigurationCandidates',
    'cao_TraceToolResponseCandidates', 'cao_TraceRepeatedToolCandidates'
  ];
  for (const helper of helpers) {
    const body = curated(helper);
    assert.ok(body, helper);
    assert.match(body, /namespace:string, traceId:string/);
    assert.match(body, /maxEvents:long = 1024/);
    assert.match(body, /cao_TraceDocuments\(namespace, traceId, maxEvents\)/);
    assert.match(body, /Namespace, RunId, RunAttempt, ProjectionId/);
    assert.doesNotMatch(body, /\bjoin\b|TraceEvents\b|TraceContentParts\b|TraceManifest\b/);
    assert.ok(kql.indexOf('function cao_TraceDocuments(') < kql.indexOf(`function ${helper}(`));
  }
  assert.ok(kql.indexOf('function cao_ReadyTraces(') < kql.indexOf('function cao_TraceDocuments('));
  assert.ok(kql.indexOf('function cao_TraceContentParts(') < kql.indexOf('function cao_TraceDocuments('));
  assert.match(curated('cao_TraceTimeline'), /order by Sequence asc/);
  const context = curated('cao_TraceContext');
  assert.match(context, /where Type == "workflow.info"/);
  assert.match(context, /EngineHarness = Event.data.engineHarness/);
  assert.match(context, /Context = Event.data/);
  assert.doesNotMatch(context, /coalesce|iff|case\(/);
  assert.match(curated('cao_TraceConfigurationCandidates'), /"configuration-candidate", "observed-error-candidate"/);
  assert.match(curated('cao_TraceConfigurationCandidates'), /Type endswith "\.error"/);
  assert.match(curated('cao_TraceToolResponseCandidates'), /Type in \("tool.execution_complete", "mcp.rpc.response"\)/);
  assert.match(curated('cao_TraceToolResponseCandidates'), /ApprovedEventBytes = string_size\(Content\)/);
  assert.match(curated('cao_TraceToolResponseCandidates'), /ReportedOutputBytes = tolong\(Event.data.outputSize\)/);
  assert.match(curated('cao_TraceToolResponseCandidates'), /Candidate = "large-tool-response-candidate"/);
  assert.match(curated('cao_TraceRepeatedToolCandidates'), /where Type == "tool.execution_start"/);
  assert.match(curated('cao_TraceRepeatedToolCandidates'), /ObservedStarts = count\(\)/);
  assert.match(curated('cao_TraceRepeatedToolCandidates'), /array_sort_asc\(Sequences, ToolCallIds\)/);
  assert.match(curated('cao_TraceRepeatedToolCandidates'), /Candidate = "repeated-tool-candidate"/);
  assert.doesNotMatch(curated('cao_TraceRepeatedToolCandidates'), /sum\(|Savings|Avoidable|Causal/);
});

test('trace emitter fails closed on malformed, nullable, optional, opaque and open table fields', async () => {
  const original = readFileSync(entry, 'utf8');
  for (const [before, after, expected] of [
    ['Content: string;', 'Content: unknown;', /Unsupported trace type/],
    ['Content: string;', 'Content: string | null;', /Unsupported trace type/],
    ['Content: string;', 'Content?: string;', /must be required/],
    ['Content: string;', 'Content: Record<string>;', /Unsupported trace type/],
    ['Gaps: string[];', 'Gaps: int64[];', /Unsupported trace type/],
    ['Weight: float64;', 'Weight: decimal;', /Unsupported trace type/],
    ['MemberCount: int64;', 'MemberCount?: int64;', /must be required/],
    ['DatasetDigest: string;', 'DatasetDigest: string | null;', /Unsupported trace type/],
    ['ContractVersion: 1;', 'ContractVersion: int64;', /literal ContractVersion 1/],
    ['ContractVersion: 1;', 'ContractVersion: 2;', /literal ContractVersion 1/],
    ['model TraceEvents extends Identity', 'model TraceEvents', /Invalid trace table/],
    ['model TraceEvents extends Identity {', 'model TraceEvents extends Identity {\n ...Record<unknown>;', /Invalid trace table/]
  ]) {
    assert.ok(original.includes(before));
    const host = {
      ...NodeHost,
      async readFile(path) {
        const file = await NodeHost.readFile(path);
        return path === entry ? { ...file, text: original.replace(before, after) } : file;
      }
    };
    const invalid = await compile(host, entry, { noEmit: true });
    assert.deepEqual(invalid.diagnostics.map(({ code, message }) => ({ code, message })), [], after);
    await assert.rejects($onEmit({ program: invalid, emitterOutputDir: '.', options: {} }), expected);
  }
});
