import { createHash } from 'node:crypto';
import { rename, rm, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { CANONICAL_SCHEMA_VERSION } from '../site/src/data/model/schema.js';
import { NORMALIZED_JSONL_INGESTION_VERSION } from '../site/src/data/ingest/coordinator.js';

export const DEFAULT_NORMALIZED_JSONL_SHARD_BYTES = 1024 * 1024;

function shardHeader(phase, records) {
  return `${JSON.stringify({
    kind: 'metadata',
    schemaVersion: CANONICAL_SCHEMA_VERSION,
    ingestionVersion: NORMALIZED_JSONL_INGESTION_VERSION,
    sourceRecords: records,
    phase,
    records
  })}\n`;
}

export async function writeNormalizedShardBucket(
  phase, bucket, lines, outputDirectory, maxBytes = DEFAULT_NORMALIZED_JSONL_SHARD_BYTES
) {
  if (!Number.isSafeInteger(maxBytes) || maxBytes < Buffer.byteLength(shardHeader(phase, 0))) {
    throw new RangeError('Normalized shard byte limit must fit its metadata header');
  }
  const names = [];
  let current = [];
  let currentBytes = 0;
  const flush = async () => {
    const content = shardHeader(phase, current.length) + current.join('');
    const digest = createHash('sha256').update(content).digest('hex');
    const name = `${bucket}-${String(names.length).padStart(4, '0')}-${digest.slice(0, 16)}.jsonl`;
    const outputPath = path.join(outputDirectory, name);
    try {
      await stat(outputPath);
    } catch (error) {
      if (error?.code !== 'ENOENT') throw error;
      const temporaryPath = `${outputPath}.${process.pid}.tmp`;
      try {
        await writeFile(temporaryPath, content, { flag: 'wx' });
        await rename(temporaryPath, outputPath);
      } finally {
        await rm(temporaryPath, { force: true });
      }
    }
    names.push(name);
    current = [];
    currentBytes = 0;
  };
  for (const line of lines) {
    const size = Buffer.byteLength(line);
    const headerBytes = Buffer.byteLength(shardHeader(phase, current.length + 1));
    if (current.length > 0 && headerBytes + currentBytes + size > maxBytes) await flush();
    current.push(line);
    currentBytes += size;
  }
  if (current.length > 0 || names.length === 0) await flush();
  return names;
}
