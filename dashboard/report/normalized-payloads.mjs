import { createHash } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { readFile, readdir, mkdir, rename, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { createDebug } from './debug.mjs';
import { adaptCachedGhAwJsonlStream } from '../site/src/data/adapters/gh-aw-logs.js';
import { GH_AW_JSONL_INGESTION_VERSION, NORMALIZED_JSONL_INGESTION_VERSION } from '../site/src/data/ingest/coordinator.js';
import { normalize } from '../site/src/data/normalize/index.js';
import { mergeOperationalValueEvidence } from '../site/src/data/model/operational-value-evidence.js';
import { mergeWorkflowValueCollection } from '../site/src/data/model/workflow-value-collection.js';
import { CANONICAL_SCHEMA_VERSION, mergeEvidenceDefinition } from '../site/src/data/model/schema.js';
import { mergeActivityStructuralRecord } from '../site/src/data/storage/retention.js';
import { NORMALIZED_COLLECTIONS } from './collections.mjs';
import { consolidationBucket, normalizedPhaseBatch, relationshipSafeEvidenceBatch, STRUCTURAL_CONSOLIDATION_BUCKET } from './normalized-phase.mjs';
import { DEFAULT_NORMALIZED_JSONL_SHARD_BYTES, writeNormalizedShardBucket } from './normalized-shards.mjs';
import { AUDIT_CURATION_VERSION, auditCurationRunFacts, discardAudit } from '../site/src/data/model/audit-curation.js';
import { jsonlLines, jsonlRecords, hashFileContents } from './evidence-files.mjs';
import { DEFAULT_NORMALIZATION_CACHE_BYTES, pruneNormalizationCaches } from './normalization-cache.mjs';

const debugHash = createDebug('hash-payloads');

// Per-shard normalization output is retained only as an incremental cache. It lives
// in a subdirectory so it is never published, hashed into the manifest, or ingested.
const PAYLOAD_CACHE_DIRECTORY = '.payloads';

function* normalizedJsonlLines(payload) {
  const records = Object.values(payload.batch)
    .reduce((total, collection) => total + collection.length, 0);
  yield `${JSON.stringify({
    kind: 'metadata',
    schemaVersion: payload.schemaVersion,
    ingestionVersion: payload.ingestionVersion,
    sourceRecords: payload.sourceRecords,
    phase: payload.phase ?? 'all',
    records
  })}\n`;
  for (const collection of NORMALIZED_COLLECTIONS) {
    for (const record of payload.batch[collection] ?? []) {
      yield `${JSON.stringify({ kind: 'record', collection, record })}\n`;
    }
  }
}

/**
 * Computes SHA-256 checksums for the activity snapshot payloads: the
 * SQLite projection and every retained `--cached-jsonl` wildcard shard file.
 * Missing files are tolerated (an
 * absent shard directory yields no shard entries) so this can run
 * immediately after ingestion in the same workflow step.
 */
function workflowHintsFromInventory(input) {
  const rows = input?.workflows?.rows;
  if (!Array.isArray(rows)) return [];
  return rows.flatMap((candidate) => (
    candidate
      && typeof candidate === 'object'
      && typeof candidate.organization === 'string'
      && typeof candidate.repository === 'string'
      && typeof candidate['workflow-name'] === 'string'
      && typeof candidate.workflow === 'string'
      ? [{
          owner: candidate.organization,
          repository: candidate.repository,
          name: candidate['workflow-name'],
          path: candidate.workflow
        }]
      : []
  ));
}

/**
 * Collapses every per-shard payload for one phase into deduplicated, day-bucketed
 * shards. Source payloads may repeat canonical records; keying on (collection, id)
 * keeps exactly one winning copy before ordering and byte-bounded publication.
 *
 * @param {string} phase
 * @param {string[]} cachePaths per-shard payloads in ingestion order
 * @param {string} outputDirectory
 * @param {number} maxBytes
 */
async function consolidatePhasePayloads(
  phase, cachePaths, outputDirectory, maxBytes, {
    runWorkflowIds = new Map(), runFacts = new Map(), referencedAuditIds = new Set()
  } = {}
) {
  /** @type {Map<string, Map<string, Record<string, unknown>>>} */
  const deduped = new Map(NORMALIZED_COLLECTIONS.map((collection) => [collection, new Map()]));
  let sourceRecords = 0;
  for (const cachePath of cachePaths) {
    for (const { value: entry } of jsonlRecords([cachePath])) {
      if (entry?.kind !== 'record') continue;
      const records = deduped.get(entry.collection);
      if (!records) continue;
      const record = entry.record;
      const id = String(record.id);
      sourceRecords += 1;
      const existing = records.get(id);
      if (entry.collection === 'operationalValueEvidence') {
        records.set(id, mergeOperationalValueEvidence(existing, record));
        continue;
      }
      if (entry.collection === 'workflowValueCollections') {
        records.set(id, mergeWorkflowValueCollection(existing, record));
        continue;
      }
      // Mirrors canonical ingestion precedence: discovery owns repository and
      // workflow inventory fields, every other collection is last-observation-wins.
      if (existing && (entry.collection === 'repositories' || entry.collection === 'workflows')) {
        records.set(id, mergeActivityStructuralRecord(entry.collection, existing, record));
        continue;
      }
      if (existing && ['experiments', 'graders', 'evals'].includes(entry.collection)) {
        records.set(id, mergeEvidenceDefinition(existing, record));
        continue;
      }
      records.set(id, record);
    }
  }
  if (phase === 'records' && runWorkflowIds.size > 0) {
    const batch = Object.fromEntries(
      NORMALIZED_COLLECTIONS.map((collection) =>
        [collection, [...deduped.get(collection).values()]])
    );
    const safe = relationshipSafeEvidenceBatch(batch, runWorkflowIds);
    for (const collection of ['experimentAssignments', 'graderObservations', 'evalObservations']) {
      deduped.set(collection, new Map(safe[collection].map((record) => [String(record.id), record])));
    }
  }
  const consolidatedRunWorkflowIds = phase === 'runs'
    ? new Map([...deduped.get('runs').values()].map((run) =>
      [String(run.id), String(run.workflowId)]))
    : runWorkflowIds;
  if (phase === 'runs') {
    runFacts = new Map([...deduped.get('runs').values()].map((run) =>
      [String(run.id), auditCurationRunFacts(run)]));
  }
  for (const collection of ['experimentAssignments', 'graderObservations', 'evalObservations']) {
    for (const record of deduped.get(collection).values()) {
      if (typeof record.auditId === 'string') referencedAuditIds.add(record.auditId);
    }
  }
  /** @type {Map<string, string[]>} */
  const buckets = new Map();
  let uniqueRecords = 0;
  for (const collection of NORMALIZED_COLLECTIONS) {
    const records = deduped.get(collection);
    for (const record of [...records.values()].sort((left, right) =>
      String(left.id) < String(right.id) ? -1 : String(left.id) > String(right.id) ? 1 : 0)) {
      if (collection === 'audits' && !referencedAuditIds.has(String(record.id))
          && discardAudit(record, runFacts.get(String(record.runId)))) continue;
      const bucket = consolidationBucket(collection, record);
      const lines = buckets.get(bucket) ?? buckets.set(bucket, []).get(bucket);
      lines.push(`${JSON.stringify({ kind: 'record', collection, record })}\n`);
      uniqueRecords += 1;
    }
    records.clear();
  }
  debugHash(
    'consolidating phase=%s sourceRecords=%d uniqueRecords=%d buckets=%d',
    phase,
    sourceRecords,
    uniqueRecords,
    buckets.size
  );
  /** @type {string[]} */
  const written = [];
  for (const bucket of [...buckets.keys()].sort()) {
    written.push(...await writeNormalizedShardBucket(
      phase, bucket, buckets.get(bucket), outputDirectory, maxBytes
    ));
    buckets.delete(bucket);
  }
  // A phase with no records still publishes one header-only shard, so an empty
  // collection is explicit rather than indistinguishable from missing output.
  if (written.length === 0) {
    written.push(...await writeNormalizedShardBucket(
      phase, STRUCTURAL_CONSOLIDATION_BUCKET, [], outputDirectory, maxBytes
    ));
  }
  return { names: written, runWorkflowIds: consolidatedRunWorkflowIds, runFacts, referencedAuditIds };
}

export async function normalizeSnapshotPayloads({
  databasePath,
  shardDirectory,
  normalizedDirectory,
  runsDirectory,
  recordsDirectory,
  inventoryPath,
  maxBytes = DEFAULT_NORMALIZED_JSONL_SHARD_BYTES,
  maxCacheBytes = DEFAULT_NORMALIZATION_CACHE_BYTES
}) {
  if (!Number.isSafeInteger(maxCacheBytes) || maxCacheBytes < 0) {
    throw new RangeError('Normalization cache byte limit must be a non-negative integer');
  }
  const hashFile = async (filePath) => {
    const digest = await hashFileContents(filePath);
    debugHash('hashed %s -> %s', filePath, digest);
    return digest;
  };
  const payloadHasRecords = async (filePath) => {
    let lines = 0;
    for await (const line of jsonlLines([filePath])) {
      lines += 1;
      if (lines > 1) return true;
    }
    return false;
  };
  const hashes = {};
  if (databasePath) hashes[path.basename(databasePath)] = await hashFile(databasePath);
  if (shardDirectory) {
    let shardNames = [];
    try {
      shardNames = (await readdir(shardDirectory)).filter((name) => name.endsWith('.jsonl')).sort();
    } catch (error) {
      if (!(error && error.code === 'ENOENT')) throw error;
    }
    debugHash('hashing %d shard(s) in %s', shardNames.length, shardDirectory);
    const inventorySource = inventoryPath ? await readFile(inventoryPath, 'utf8') : '{}';
    const workflowHints = workflowHintsFromInventory(JSON.parse(inventorySource));
    const normalizationContext = createHash('sha256')
      .update(`${CANONICAL_SCHEMA_VERSION}\0${GH_AW_JSONL_INGESTION_VERSION}\0${NORMALIZED_JSONL_INGESTION_VERSION}\0${AUDIT_CURATION_VERSION}\0${JSON.stringify(workflowHints)}`)
      .digest('hex')
      .slice(0, 16);
    const retainedPayloads = {
      normalized: new Set(),
      runs: new Set(),
      records: new Set()
    };
    const retainedCachePayloads = {
      runs: new Set(),
      records: new Set()
    };
    const cachePaths = { runs: [], records: [] };
    const cacheDirectories = {
      runs: runsDirectory ? path.join(runsDirectory, PAYLOAD_CACHE_DIRECTORY) : null,
      records: recordsDirectory ? path.join(recordsDirectory, PAYLOAD_CACHE_DIRECTORY) : null
    };
    if (normalizedDirectory) await mkdir(normalizedDirectory, { recursive: true });
    if (runsDirectory) await mkdir(cacheDirectories.runs, { recursive: true });
    if (recordsDirectory) await mkdir(cacheDirectories.records, { recursive: true });
    const rawHashes = new Map();
    const expectedCacheNames = new Set();
    for (const name of shardNames) {
      const rawHash = await hashFile(path.join(shardDirectory, name));
      rawHashes.set(name, rawHash);
      expectedCacheNames.add(`${path.parse(name).name}-${rawHash}-${normalizationContext}.jsonl`);
    }
    const disposableDirectories = Object.values(cacheDirectories).filter(Boolean);
    await pruneNormalizationCaches(disposableDirectories, { retainedNames: expectedCacheNames, maxBytes: maxCacheBytes });
    for (const name of shardNames) {
      const shardPath = path.join(shardDirectory, name);
      if ((await stat(shardPath)).size === 0) {
        await rm(shardPath);
        debugHash('dropped empty source shard %s', shardPath);
        continue;
      }
      const rawHash = rawHashes.get(name);
      if (!runsDirectory && !recordsDirectory) {
        hashes[`${path.basename(shardDirectory)}/${name}`] = rawHash;
      }
      if (!normalizedDirectory && !runsDirectory && !recordsDirectory) continue;
      const payloadName = `${rawHash}-${normalizationContext}.jsonl`;
      const phasedPayloadName = `${path.parse(name).name}-${payloadName}`;
      const outputPaths = [
        normalizedDirectory ? ['normalized', path.join(normalizedDirectory, payloadName)] : null,
        runsDirectory ? ['runs', path.join(cacheDirectories.runs, phasedPayloadName)] : null,
        recordsDirectory ? ['records', path.join(cacheDirectories.records, phasedPayloadName)] : null
      ].filter(Boolean);
      const missing = [];
      for (const output of outputPaths) {
        try {
          await stat(output[1]);
        } catch (error) {
          if (!(error && error.code === 'ENOENT')) throw error;
          missing.push(output);
        }
      }
      if (missing.length > 0) {
        const adapted = await adaptCachedGhAwJsonlStream(createReadStream(shardPath), {
          workflowHints,
          payloadIdentity: rawHash
        });
        const batch = normalize(adapted.observations);
        const metadata = {
          schemaVersion: CANONICAL_SCHEMA_VERSION,
          ingestionVersion: NORMALIZED_JSONL_INGESTION_VERSION,
          sourceRecords: adapted.records
        };
        const payloads = {
          normalized: { ...metadata, batch },
          runs: {
            ...metadata,
            phase: 'runs',
            batch: normalizedPhaseBatch(batch, 'runs')
          },
          records: {
            ...metadata,
            phase: 'records',
            batch: normalizedPhaseBatch(batch, 'records')
          }
        };
        await Promise.all(missing.map(async ([phase, outputPath]) => {
          const temporaryPath = `${outputPath}.${process.pid}.tmp`;
          await pipeline(
            Readable.from(normalizedJsonlLines(payloads[phase])),
            createWriteStream(temporaryPath, { flags: 'wx' })
          );
          await rename(temporaryPath, outputPath);
        }));
      }
      for (const [phase, outputPath] of outputPaths) {
        if (!await payloadHasRecords(outputPath)) {
          await rm(outputPath, { force: true });
          debugHash('dropped empty %s shard %s', phase, outputPath);
          continue;
        }
        if (phase === 'normalized') {
          retainedPayloads.normalized.add(path.basename(outputPath));
          hashes[`${path.basename(path.dirname(outputPath))}/${path.basename(outputPath)}`] = await hashFile(outputPath);
          continue;
        }
        retainedCachePayloads[phase].add(path.basename(outputPath));
        cachePaths[phase].push(outputPath);
      }
    }
    let runWorkflowIds = new Map();
    let runFacts = new Map();
    let referencedAuditIds = new Set();
    for (const [phase, directory] of [
      ['runs', runsDirectory],
      ['records', recordsDirectory]
    ].filter(([, directory]) => Boolean(directory))) {
      const consolidated = await consolidatePhasePayloads(
        phase,
        cachePaths[phase],
        directory,
        maxBytes,
        { runWorkflowIds, runFacts, referencedAuditIds }
      );
      if (phase === 'runs') {
        runWorkflowIds = consolidated.runWorkflowIds;
        runFacts = consolidated.runFacts;
        referencedAuditIds = consolidated.referencedAuditIds;
      }
      for (const name of consolidated.names) {
        retainedPayloads[phase].add(name);
        hashes[`${path.basename(directory)}/${name}`] = await hashFile(path.join(directory, name));
      }
      for (const name of await readdir(cacheDirectories[phase])) {
        if (name.endsWith('.jsonl') && !retainedCachePayloads[phase].has(name)) {
          await rm(path.join(cacheDirectories[phase], name), { force: true });
        }
      }
    }
    for (const [phase, directory] of [
      ['normalized', normalizedDirectory],
      ['runs', runsDirectory],
      ['records', recordsDirectory]
    ].filter(([, directory]) => Boolean(directory))) {
      for (const name of await readdir(directory)) {
        if ((name.endsWith('.json') || name.endsWith('.jsonl')) && !retainedPayloads[phase].has(name)) {
          await rm(path.join(directory, name), { force: true });
        }
      }
    }
    const cache = await pruneNormalizationCaches(disposableDirectories, { maxBytes: maxCacheBytes });
    debugHash('normalization cache retainedBytes=%d removedBytes=%d maxBytes=%d',
      cache.retainedBytes, cache.removedBytes, cache.maxBytes);
  }
  return hashes;
}
