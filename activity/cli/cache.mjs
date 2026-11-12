import { createWriteStream } from 'node:fs';
import { cp, lstat, mkdir, readFile, readdir, rename, rm, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { DAY_MS } from './options.mjs';
import { jsonlRecords, totalFileBytes } from './files.mjs';

export const DEFAULT_RAW_CACHE_BYTES = 1024 * 1024 * 1024;
export const DEFAULT_NORMALIZATION_CACHE_BYTES = 256 * 1024 * 1024;

function cacheRecord(record) {
  if (!record || typeof record !== 'object' || Array.isArray(record)) {
    throw new TypeError('Activity cache records must be JSON objects');
  }
  return record;
}

function runIdentifier(run) {
  const id = run?.run_id ?? run?.databaseId ?? run?.database_id;
  return id === undefined || id === null ? null : String(id);
}

function parentRun(record) {
  if (record.kind === 'run' || record.kind === 'token_efficiency_run_context') return runIdentifier(record.run);
  if (record.kind === 'safe_output_item') return runIdentifier(record.safe_output);
  if (record.kind === 'token_efficiency_observation' || record.kind === 'token_efficiency_lifecycle_observation') {
    const id = record.observation?.optimizerRunId;
    return id === undefined || id === null ? null : String(id);
  }
  return null;
}

function runTimestamp(run) {
  let latest = null;
  for (const value of [
    run.completed_at, run.updated_at, run.updatedAt, run.started_at,
    run.startedAt, run.created_at, run.createdAt
  ]) {
    const timestamp = typeof value === 'string' ? Date.parse(value) : NaN;
    if (Number.isFinite(timestamp)) latest = Math.max(latest ?? timestamp, timestamp);
  }
  return latest;
}

async function shardPaths(directory, { allowMissing = false } = {}) {
  let info;
  try {
    info = await lstat(directory);
  } catch (error) {
    if (allowMissing && error.code === 'ENOENT') return [];
    throw error;
  }
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Shard directory must be a non-symlink directory');
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    if (entry.name.endsWith('.jsonl') && entry.isSymbolicLink()) {
      throw new Error(`Refusing to recycle symlink shard: ${entry.name}`);
    }
  }
  return entries.filter((entry) => entry.isFile() && entry.name.endsWith('.jsonl'))
    .map((entry) => path.join(directory, entry.name)).sort();
}

export async function recycleActivityCache(inputDirectory, {
  maxBytes = DEFAULT_RAW_CACHE_BYTES,
  retentionWindowMs = 30 * DAY_MS,
  now = Date.now()
} = {}) {
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 1) throw new RangeError('Cache byte limit must be a positive integer');
  if (!Number.isSafeInteger(retentionWindowMs) || retentionWindowMs < 1 || !Number.isFinite(now)) {
    throw new RangeError('Cache retention and evaluation time must be valid');
  }
  const directory = path.resolve(inputDirectory);
  const paths = await shardPaths(directory);
  const sourceBytes = await totalFileBytes(paths);
  const runs = new Map();
  for (const { value } of jsonlRecords(paths)) {
    const record = cacheRecord(value);
    const observations = record.kind === 'workflow_runs' && Array.isArray(record.payload)
      ? record.payload
      : ['run', 'token_efficiency_run_context'].includes(record.kind) && record.run ? [record.run] : [];
    for (const run of observations) {
      const id = runIdentifier(run);
      if (id === null) continue;
      const timestamp = runTimestamp(run);
      const previous = runs.get(id);
      if (!previous || timestamp !== null && (previous.timestamp === null || timestamp >= previous.timestamp)) {
        runs.set(id, { timestamp, status: run.status ?? previous?.status, bytes: previous?.bytes ?? 0 });
      }
    }
  }
  const expired = new Set([...runs].filter(([, run]) =>
    run.timestamp !== null && run.timestamp < now - retentionWindowMs
      && (!run.status || run.status === 'completed')).map(([id]) => id));
  const retained = new Set([...runs.keys()].filter((id) => !expired.has(id)));
  const keepRun = (id) => id === null || !runs.has(id) || retained.has(id);
  const filterRecord = (record, line) => {
    if (record.kind === 'operational_value') {
      const timestamp = Date.parse(record.operational_value?.timestamp);
      return Number.isFinite(timestamp) && timestamp < now - 365 * DAY_MS ? null : line;
    }
    if (record.kind === 'workflow_runs' && Array.isArray(record.payload)) {
      if (record.payload.length === 0) return JSON.stringify(record);
      const payload = record.payload.filter((run) => keepRun(runIdentifier(run)));
      return payload.length === 0 ? null : JSON.stringify({ ...record, payload });
    }
    return keepRun(parentRun(record)) ? line : null;
  };
  let protectedBytes = 0;
  for (const { value, rawLine } of jsonlRecords(paths)) {
    const line = rawLine.replace(/\r?\n$/, '');
    const record = cacheRecord(value);
    if (filterRecord(record, line) === null) continue;
    if (record.kind === 'workflow_runs' && Array.isArray(record.payload)) {
      // Charge the envelope once; per-run payload costs conservatively include a comma.
      protectedBytes += Buffer.byteLength(JSON.stringify({ ...record, payload: [] })) + 1;
      for (const run of record.payload) {
        const id = runIdentifier(run);
        const bytes = Buffer.byteLength(JSON.stringify(run)) + 1;
        if (retained.has(id)) runs.get(id).bytes += bytes;
        else if (keepRun(id)) protectedBytes += bytes;
      }
    } else {
      const id = parentRun(record);
      const bytes = Buffer.byteLength(line) + 1;
      if (retained.has(id)) runs.get(id).bytes += bytes;
      else protectedBytes += bytes;
    }
  }
  if (protectedBytes > maxBytes) {
    throw new Error(`Unattributed records and operational-value history require ${protectedBytes} bytes, exceeding cache limit ${maxBytes}; no shards changed`);
  }
  let plannedBytes = protectedBytes + [...retained].reduce((total, id) => total + runs.get(id).bytes, 0);
  const evicted = [];
  const oldest = [...retained].sort((left, right) =>
    (runs.get(left).timestamp ?? Infinity) - (runs.get(right).timestamp ?? Infinity) || left.localeCompare(right));
  for (const id of oldest) {
    if (plannedBytes <= maxBytes) break;
    retained.delete(id);
    plannedBytes -= runs.get(id).bytes;
    evicted.push(id);
  }
  let removedFiles = 0;
  let rewrittenFiles = 0;
  for (const file of paths) {
    const temporary = `${file}.${process.pid}.recycle.tmp`;
    let changed = false;
    let records = 0;
    async function* lines() {
      for (const { value, rawLine } of jsonlRecords([file])) {
        const line = rawLine.replace(/\r?\n$/, '');
        const filtered = filterRecord(cacheRecord(value), line);
        if (filtered !== line) changed = true;
        if (filtered !== null) {
          records += 1;
          yield `${filtered}\n`;
        }
      }
    }
    try {
      await pipeline(Readable.from(lines()), createWriteStream(temporary, { flags: 'wx' }));
      changed ||= (await stat(temporary)).size !== (await stat(file)).size;
      if (records === 0) {
        await rm(file);
        removedFiles += 1;
      } else if (changed) {
        await rename(temporary, file);
        rewrittenFiles += 1;
      }
    } finally {
      await rm(temporary, { force: true });
    }
  }
  const retainedBytes = await totalFileBytes(await shardPaths(directory));
  if (retainedBytes > maxBytes) throw new Error(`Recycled cache still exceeds byte limit: ${retainedBytes} > ${maxBytes}`);
  return {
    command: 'recycle-cache', inputDirectory: directory, sourceBytes, retainedBytes, maxBytes,
    expiredRuns: expired.size, evictedRuns: evicted.length, retainedRuns: retained.size,
    removedFiles, rewrittenFiles
  };
}

export async function pruneNormalizationCaches(directories, {
  retainedNames, maxBytes = DEFAULT_NORMALIZATION_CACHE_BYTES
} = {}) {
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 0) throw new RangeError('Normalization cache byte limit must be a non-negative integer');
  const files = [];
  let removedBytes = 0;
  for (const directory of directories) {
    let entries;
    try {
      if ((await lstat(directory)).isSymbolicLink()) throw new Error('Normalization cache must not be a symlink');
      entries = await readdir(directory, { withFileTypes: true });
    } catch (error) {
      if (error.code === 'ENOENT') continue;
      throw error;
    }
    for (const entry of entries) {
      if (!entry.isFile() || !/\.(jsonl|tmp)$/.test(entry.name)) continue;
      const file = path.join(directory, entry.name);
      const info = await stat(file);
      if (entry.name.endsWith('.tmp') || retainedNames && !retainedNames.has(entry.name)) {
        await rm(file);
        removedBytes += info.size;
      } else {
        files.push({ file, bytes: info.size, modified: info.mtimeMs });
      }
    }
  }
  let retainedBytes = files.reduce((total, file) => total + file.bytes, 0);
  for (const file of files.sort((a, b) => a.modified - b.modified || a.file.localeCompare(b.file))) {
    if (retainedBytes <= maxBytes) break;
    await rm(file.file);
    retainedBytes -= file.bytes;
    removedBytes += file.bytes;
  }
  return { retainedBytes, removedBytes, maxBytes };
}
