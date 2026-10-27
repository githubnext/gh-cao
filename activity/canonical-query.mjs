import { spawnSync } from 'node:child_process';
import { createConnection } from 'node:net';
import { fileURLToPath } from 'node:url';

const ACTIVITY_CLI = fileURLToPath(new URL('./cao.mjs', import.meta.url));
const MAX_QUERY_BYTES = 1 << 20;
const MAX_RESPONSE_BYTES = 16 << 20;
const QUERY_TIMEOUT_MS = 30_000;

function fail(message) {
  throw new Error(message);
}

function queryThroughSocket(socket, query, signal) {
  return new Promise((resolve, reject) => {
    const request = `${JSON.stringify({
      schemaVersion: 1,
      queries: [query],
      requested: [query.name],
    })}\n`;
    if (Buffer.byteLength(request) > MAX_QUERY_BYTES) {
      reject(new Error('Canonical query request exceeds its byte budget'));
      return;
    }
    let settled = false;
    let content = '';
    const client = createConnection(socket);
    const timeout = setTimeout(() => {
      client.destroy(new Error('Canonical query timed out'));
    }, QUERY_TIMEOUT_MS);
    const finish = (error, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      signal?.removeEventListener('abort', abort);
      if (error) reject(error);
      else resolve(value);
    };
    const abort = () => client.destroy(signal.reason instanceof Error
      ? signal.reason
      : new Error('Canonical query cancelled'));
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
    client.setEncoding('utf8');
    client.on('connect', () => client.end(request));
    client.on('data', (chunk) => {
      content += chunk;
      if (Buffer.byteLength(content) > MAX_RESPONSE_BYTES) {
        client.destroy(new Error('Canonical query response exceeds its byte budget'));
      }
    });
    client.on('error', (error) => finish(error));
    client.on('end', () => {
      let response;
      try {
        response = JSON.parse(content);
      } catch {
        finish(new Error('Canonical query returned invalid JSON'));
        return;
      }
      if (response?.schemaVersion !== 1) {
        finish(new Error('Canonical query returned an unsupported response'));
        return;
      }
      if (typeof response.error === 'string' && response.error) {
        finish(new Error(response.error));
        return;
      }
      const source = response.sources?.[query.name];
      if (!source || !Array.isArray(source.rows) || !source.metadata
          || typeof source.metadata !== 'object' || Array.isArray(source.metadata)) {
        finish(new Error('Canonical query returned an invalid source'));
        return;
      }
      finish(undefined, source);
    });
  });
}

function querySQLite(database, query) {
  if (typeof database !== 'string' || !database.trim()) {
    fail('Canonical SQLite query requires a database path');
  }
  const result = spawnSync(process.execPath, [
    process.env.CAO_ACTIVITY_CLI || ACTIVITY_CLI,
    'query',
    '--database',
    database,
    '--stdin',
  ], {
    input: `${JSON.stringify(query)}\n`,
    encoding: 'utf8',
    maxBuffer: MAX_RESPONSE_BYTES,
  });
  if (result.error || result.status !== 0) {
    fail(String(result.stderr || result.error?.message || 'Canonical SQLite query failed').trim());
  }
  let rows;
  try {
    rows = JSON.parse(result.stdout);
  } catch {
    fail('Canonical SQLite query returned invalid JSON');
  }
  if (!Array.isArray(rows)) fail('Canonical SQLite query returned invalid rows');
  return {
    source: query.name,
    rows,
    metadata: {
      availability: rows.length === 0 ? 'empty' : 'available',
      completeness: 'unknown',
      freshness: 'unknown',
    },
  };
}

export async function executeCanonicalQuery(query, { database, signal } = {}) {
  if (!query || typeof query.name !== 'string' || !query.name.trim()) {
    fail('Canonical query requires a named Dashboard Language definition');
  }
  signal?.throwIfAborted();
  const socket = process.env.CAO_DASHBOARD_QUERY_SOCKET;
  const source = socket
    ? await queryThroughSocket(socket, query, signal)
    : querySQLite(database, query);
  signal?.throwIfAborted();
  return source;
}
