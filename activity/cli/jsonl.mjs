import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { readdir, rename, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { adaptCachedGhAwJsonlStream } from '../../dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from '../../dashboard/site/src/data/normalize/index.js';
import { ENTITY_COLLECTIONS } from '../cli-usage.mjs';
import { UsageError } from './options.mjs';
import { jsonlRecords, totalFileBytes } from './files.mjs';
import { inspectShardRepositories, recordRepository } from './jsonl-identity.mjs';

export const DEFAULT_COMPACTED_JSONL_SHARD_BYTES = 4 * 1024 * 1024;

async function auditJsonl(inputPath) {
  const input = path.resolve(inputPath);
  const adapted = await adaptCachedGhAwJsonlStream(createReadStream(input));
  const canonical = normalize(adapted.observations);
  return {
    command: 'audit-jsonl',
    input,
    source: {
      records: adapted.records,
      rawRunObservations: adapted.rawPayloadRecords,
      uniqueRawRuns: adapted.rawRuns,
      duplicateRawRunObservations: adapted.duplicateRawRunObservations,
      enrichedRunObservations: adapted.agenticRunRecords,
      uniqueEnrichedRuns: adapted.agenticRuns,
      duplicateEnrichedRunObservations: adapted.duplicateAgenticRunObservations,
      unenrichedRuns: adapted.unenrichedRuns,
      enrichmentCoveragePercent: canonical.runs.length === 0
        ? 0
        : Number((adapted.agenticRuns / canonical.runs.length * 100).toFixed(1))
    },
    canonical: Object.fromEntries(ENTITY_COLLECTIONS.map((collection) => [
      collection,
      canonical[collection].length
    ]))
  };
}

export async function auditJsonlDirectory(inputDirectory) {
  const directory = path.resolve(inputDirectory);
  const names = (await readdir(directory)).filter((name) => name.endsWith('.jsonl')).sort();
  const audits = [];
  for (const name of names) audits.push(await auditJsonl(path.join(directory, name)));
  const sourceKeys = Object.keys(audits[0]?.source ?? {});
  const source = Object.fromEntries(sourceKeys.map((key) => [
    key,
    audits.reduce((sum, audit) => sum + (Number(audit.source[key]) || 0), 0)
  ]));
  const canonical = Object.fromEntries(ENTITY_COLLECTIONS.map((collection) => [
    collection,
    audits.reduce((sum, audit) => sum + audit.canonical[collection], 0)
  ]));
  source.enrichmentCoveragePercent = canonical.runs === 0
    ? 0
    : Number((source.uniqueEnrichedRuns / canonical.runs * 100).toFixed(1));
  return {
    command: 'audit-jsonl',
    inputDirectory: directory,
    shards: names.length,
    source,
    canonical
  };
}

function workflowRunId(value) { return value === undefined || value === null ? null : String(value); }

function isAgenticWorkflowRun(record) {
  return typeof record?.run?.workflow_path === 'string' && record.run.workflow_path.endsWith('.lock.yml');
}

function createJsonlCompactionState() { return { seenRunRecordDigests: new Set(), deduplicatedRunRecords: 0 }; }

function compactedJsonlLine(record, line, agenticRunIds, state) {
  if (record?.kind === 'run' || record?.kind === 'token_efficiency_run_context') {
    if (!isAgenticWorkflowRun(record)) return null;
    if (record.kind === 'run') {
      const digest = createHash('sha256').update(line).digest('hex');
      if (state.seenRunRecordDigests.has(digest)) {
        state.deduplicatedRunRecords += 1;
        return null;
      }
      state.seenRunRecordDigests.add(digest);
    }
    return line;
  }
  if (record?.kind === 'safe_output_item') {
    return agenticRunIds.has(workflowRunId(record.safe_output?.run_id)) ? line : null;
  }
  if (record?.kind === 'token_efficiency_observation' || record?.kind === 'token_efficiency_lifecycle_observation') {
    return agenticRunIds.has(workflowRunId(record.observation?.optimizerRunId)) ? line : null;
  }
  if (record?.kind !== 'workflow_runs' || !Array.isArray(record.payload)) return line;
  const payload = record.payload.filter((run) => agenticRunIds.has(workflowRunId(run?.databaseId)));
  if (payload.length === 0) return null;
  return payload.length === record.payload.length ? line : JSON.stringify({ ...record, payload });
}

async function inspectJsonlCompaction(records) {
  const agenticRunIds = new Set();
  for (const { value: record } of records()) {
    if (isAgenticWorkflowRun(record)) {
      const runId = workflowRunId(record.run.run_id);
      if (runId !== null) agenticRunIds.add(runId);
    }
  }
  let sourceRecords = 0;
  let retainedRecords = 0;
  let filtered = false;
  const state = createJsonlCompactionState();
  for (const { value, rawLine } of records()) {
    const line = rawLine.replace(/\r?\n$/, '');
    sourceRecords += 1;
    const retained = compactedJsonlLine(value, line, agenticRunIds, state);
    if (retained === null) {
      filtered = true;
      continue;
    }
    retainedRecords += 1;
    if (retained !== line) filtered = true;
  }
  return { agenticRunIds, sourceRecords, retainedRecords, deduplicatedRunRecords: state.deduplicatedRunRecords, filtered };
}

async function compactJsonlShardGroup(directory, prefix, names, maxBytes, repository, ownership) {
  const sourcePaths = names.map((name) => path.join(directory, name));
  const mixed = sourcePaths.some((filePath) => ownership.get(filePath).mixed);
  const records = function* () {
    for (const filePath of sourcePaths) {
      const identity = ownership.get(filePath);
      for (const record of jsonlRecords([filePath])) {
        if (!identity.mixed) {
          yield record;
        } else if (identity.repositoryFor(record.value) === repository) {
          if (recordRepository(record.value)) yield record;
          else {
            const value = { ...record.value, request: { ...record.value.request, repository } };
            yield { ...record, value, rawLine: JSON.stringify(value) };
          }
        }
      }
    }
  };
  const sourceBytes = await totalFileBytes(sourcePaths);
  const inspection = await inspectJsonlCompaction(records);
  if (!mixed && sourcePaths.length <= 1 && sourceBytes <= maxBytes && !inspection.filtered) {
    return {
      prefix,
      sourceFiles: sourcePaths.length,
      sourceRecords: inspection.sourceRecords,
      retainedRecords: inspection.retainedRecords,
      deduplicatedRunRecords: inspection.deduplicatedRunRecords,
      sourceBytes,
      compactedBytes: sourceBytes,
      output: sourcePaths[0] ?? null,
      outputs: sourcePaths
    };
  }

  const latestSequence = names.reduce((latest, name) => {
    const match = name.slice(prefix.length).match(/^(\d+)-/);
    return match ? Math.max(latest, Number(match[1])) : latest;
  }, 0);
  const sequence = Math.max(Math.floor(Date.now() / 1000), latestSequence + 1);
  const outputPaths = [];
  const temporaryPaths = [];
  let bufferedLines = [];
  let bufferedBytes = 0;
  let retainedRecords = 0;
  const state = createJsonlCompactionState();
  const flush = async () => {
    if (bufferedLines.length === 0) return;
    const content = bufferedLines.join('');
    const hash = createHash('sha256').update(content).digest('hex').slice(0, 16);
    const part = String(outputPaths.length).padStart(4, '0');
    const outputPath = path.join(directory, `${prefix}${sequence}-${part}-${hash}.jsonl`);
    const temporaryPath = path.join(directory, `.${path.basename(outputPath)}.${process.pid}.tmp`);
    temporaryPaths.push(temporaryPath);
    await writeFile(temporaryPath, content, { flag: 'wx' });
    await rename(temporaryPath, outputPath);
    temporaryPaths.pop();
    outputPaths.push(outputPath);
    bufferedLines = [];
    bufferedBytes = 0;
  };
  try {
    for (const { value, rawLine } of records()) {
      const line = rawLine.replace(/\r?\n$/, '');
      const retained = compactedJsonlLine(value, line, inspection.agenticRunIds, state);
      if (retained === null) continue;
      const outputLine = `${retained}\n`;
      const lineBytes = Buffer.byteLength(outputLine);
      if (bufferedLines.length > 0 && bufferedBytes + lineBytes > maxBytes) await flush();
      bufferedLines.push(outputLine);
      bufferedBytes += lineBytes;
      retainedRecords += 1;
    }
    await flush();
  } catch (error) {
    await Promise.all([...temporaryPaths, ...outputPaths].map((filePath) => rm(filePath, { force: true })));
    throw error;
  }
  const compactedBytes = await totalFileBytes(outputPaths);
  return {
    prefix,
    sourceFiles: sourcePaths.length,
    sourceRecords: inspection.sourceRecords,
    retainedRecords,
    deduplicatedRunRecords: state.deduplicatedRunRecords,
    sourceBytes,
    compactedBytes,
    output: outputPaths[0] ?? null,
    outputs: outputPaths
  };
}

export async function compactJsonlShards(
  inputDirectory,
  groupDefinitions,
  maxBytes = DEFAULT_COMPACTED_JSONL_SHARD_BYTES,
  { excludeOutOfScope = false } = {}
) {
  if (groupDefinitions.length === 0) throw new UsageError('At least one --group is required');
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 1) {
    throw new UsageError('--max-bytes must be a positive integer');
  }
  const groupsByRepository = new Map();
  for (const definition of groupDefinitions) {
    const separator = definition.indexOf('=');
    const repository = definition.slice(0, separator).toLowerCase();
    const prefix = definition.slice(separator + 1);
    if (separator < 1 || !/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/.test(repository)) {
      throw new UsageError('--group must start with an OWNER/REPOSITORY coordinate');
    }
    if (!/^[A-Za-z0-9._~-]+$/.test(prefix)) {
      throw new UsageError('--group shard prefix must contain only letters, numbers, dots, underscores, tildes, and hyphens');
    }
    if (groupsByRepository.has(repository)) throw new UsageError(`Duplicate compact-jsonl repository: ${repository}`);
    groupsByRepository.set(repository, { prefix, names: [] });
  }
  const directory = path.resolve(inputDirectory);
  const ownership = new Map();
  const excludedPaths = new Set();
  for (const name of (await readdir(directory)).filter((name) => name.endsWith('.jsonl')).sort()) {
    const filePath = path.join(directory, name);
    const identity = inspectShardRepositories(filePath, { excludeUnscopedRateLimits: excludeOutOfScope });
    ownership.set(filePath, identity);
    if (!excludeOutOfScope && identity.mixed
        && [...identity.repositories].some((repository) => !groupsByRepository.has(repository))) {
      throw new UsageError('Mixed shard contains repositories outside the declared compaction groups; compaction requires operator repair');
    }
    if (excludeOutOfScope && identity.repositories.size > 0) {
      const expectedPrefix = [...groupsByRepository.values()].some(({ prefix }) => name.startsWith(prefix));
      const inScope = [...identity.repositories].some((repository) => groupsByRepository.has(repository));
      if (!expectedPrefix && !inScope && [...identity.repositories].some((repository) => !groupsByRepository.has(repository))) {
        throw new UsageError('Foreign shard lacks a declared download prefix; compaction requires operator repair');
      }
      if (expectedPrefix) excludedPaths.add(filePath);
    }
    for (const repository of identity.repositories) {
      if (groupsByRepository.has(repository)) groupsByRepository.get(repository).names.push(name);
    }
  }
  const groups = [];
  const sourcePaths = new Set([...excludedPaths, ...[...groupsByRepository.values()].flatMap(({ names }) =>
    names.map((name) => path.join(directory, name)))]);
  let excludedRecords = 0;
  if (excludeOutOfScope) {
    for (const filePath of sourcePaths) {
      const identity = ownership.get(filePath);
      const foreignOnly = [...identity.repositories].every((repository) => !groupsByRepository.has(repository));
      for (const { value } of jsonlRecords([filePath])) {
        if (foreignOnly && !recordRepository(value) && value?.kind !== 'github_api_rate_limit') {
          throw new UsageError('Foreign shard contains unattributable records; compaction requires operator repair');
        }
        if (!groupsByRepository.has(identity.repositoryFor(value))) excludedRecords += 1;
      }
    }
  }
  try {
    for (const [repository, { prefix, names }] of [...groupsByRepository].sort(([left], [right]) => left.localeCompare(right))) {
      groups.push({
        repository,
        ...await compactJsonlShardGroup(directory, prefix, names, maxBytes, repository, ownership)
      });
    }
  } catch (error) {
    await Promise.all(groups.flatMap((group) => group.outputs)
      .filter((filePath) => !sourcePaths.has(filePath)).map((filePath) => rm(filePath, { force: true })));
    throw error;
  }
  const outputs = new Set(groups.flatMap((group) => group.outputs));
  await Promise.all([...sourcePaths].filter((filePath) => !outputs.has(filePath)).map((filePath) => rm(filePath)));
  return {
    command: 'compact-jsonl',
    inputDirectory: directory,
    repairedShards: [...ownership.values()].filter((identity) => identity.mixed).length,
    excludedRecords,
    groups
  };
}
