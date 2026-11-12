import { createConnection } from 'node:net';

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

export async function executeCanonicalQuery(query, { signal } = {}) {
  if (!query || typeof query.name !== 'string' || !query.name.trim()) {
    fail('Canonical query requires a named Dashboard Language definition');
  }
  signal?.throwIfAborted();
  const socket = process.env.CAO_DASHBOARD_QUERY_SOCKET;
  if (!socket) fail('Canonical queries require the PostgreSQL query service: CAO_DASHBOARD_QUERY_SOCKET is not configured');
  const source = await queryThroughSocket(socket, query, signal);
  signal?.throwIfAborted();
  return source;
}
