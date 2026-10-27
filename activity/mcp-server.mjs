/**
 * Stateless, read-only MCP server over the shared CAO agent catalog.
 *
 * The server is a transport: it exposes the same dashboard pages, the same
 * named Dashboard Language queries, and the same query execution path as the
 * `cao` CLI and the browser WebMCP adapter. It accepts no SQL, no unreviewed
 * query definition, and no write operation, and it keeps no protocol session
 * state, so any request is self-describing and independently routable.
 *
 * It is implemented on Node platform primitives only, without an MCP library,
 * and serves plain HTTP: the endpoint is reached over loopback or an isolated
 * job-local container network, so it carries no TLS material and no
 * self-signed certificate.
 */
import { createServer } from 'node:http';
import mcpContract from '../dashboard/site/src/agent/mcp-contract.json' with { type: 'json' };
import {
  describeAgentPages,
  describeAgentQueries,
  loadAgentDashboardDocument,
  NamedQueryError,
  runNamedDashboardQuery
} from './agent-catalog.mjs';
import { createDebug } from './debug.mjs';

const debug = createDebug('mcp');

/** Final stateless MCP protocol revision this server implements. */
export const MCP_PROTOCOL_VERSION = mcpContract.protocolVersion;

const GH_AW_MCP_PROTOCOL_VERSION = '2024-11-05';

/** Maximum accepted request body, in bytes. */
export const MAX_MCP_REQUEST_BYTES = mcpContract.limits.maxRequestBytes;

/** Default interface for the MCP endpoint. */
export const DEFAULT_MCP_HOST = '127.0.0.1';

/** Default port for the MCP endpoint. */
export const DEFAULT_MCP_PORT = 8765;

/** Conservative, deterministic cache hint for discovery results. */
export const MCP_LIST_CACHE = { ttlMs: 300_000, cacheScope: 'private' };

const SERVER_INFO = mcpContract.serverInfo;
const INSTRUCTIONS = mcpContract.instructions;

/**
 * The complete MCP tool surface, in deterministic order.
 * @type {Array<Record<string, unknown>>}
 */
export const MCP_TOOLS = mcpContract.tools;

/**
 * @param {unknown} value
 * @returns {value is Record<string, unknown>}
 */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * @param {number | string | null} id
 * @param {number} code
 * @param {string} message
 */
function errorResponse(id, code, message) {
  return { jsonrpc: '2.0', id: id ?? null, error: { code, message } };
}

/**
 * @param {number | string | null} id
 * @param {Record<string, unknown>} result
 */
function successResponse(id, result) {
  return { jsonrpc: '2.0', id: id ?? null, result };
}

/**
 * @param {unknown} payload
 * @param {boolean} [isError]
 */
function toolResult(payload, isError = false) {
  return {
    content: [{ type: 'text', text: JSON.stringify(payload) }],
    ...(isError ? { isError: true } : { structuredContent: payload })
  };
}

/**
 * Executes one `cao_catalog` invocation.
 * @param {Record<string, unknown>} args
 * @param {{ dashboardPath?: string }} context
 */
async function callCatalog(args, { dashboardPath }) {
  const kind = typeof args.kind === 'string' ? args.kind : '';
  const id = typeof args.id === 'string' && args.id.trim() ? args.id.trim() : undefined;
  if (kind === 'pages') {
    return describeAgentPages({ dashboardPath, pageId: id, json: true });
  }
  if (kind === 'queries') {
    return describeAgentQueries({ dashboardPath, queryId: id, json: true });
  }
  throw new NamedQueryError('cao_catalog requires kind to be "pages" or "queries"');
}

/**
 * Executes one `cao_query` invocation.
 * @param {Record<string, unknown>} args
 * @param {{ dashboardPath?: string, indexedDB: IDBFactory, signal?: AbortSignal }} context
 */
async function callQuery(args, { dashboardPath, indexedDB, signal }) {
  if (args.parameters !== undefined && !isPlainObject(args.parameters)) {
    throw new NamedQueryError('cao_query parameters must be an object');
  }
  // The declared schema types `limit` as an integer, so a string is a client
  // defect rather than a value to coerce.
  if (args.limit !== undefined && typeof args.limit !== 'number') {
    throw new NamedQueryError('cao_query limit must be a positive integer');
  }
  return runNamedDashboardQuery({
    indexedDB,
    dashboardPath,
    queryId: typeof args.id === 'string' ? args.id : '',
    parameters: /** @type {Record<string, unknown> | undefined} */ (args.parameters),
    limit: args.limit,
    signal
  });
}

/**
 * Handles one MCP JSON-RPC request.
 *
 * @param {{
 *   headers: Record<string, string | string[] | undefined>,
 *   body: string,
 *   indexedDB: IDBFactory,
 *   dashboardPath?: string,
 *   signal?: AbortSignal
 * }} request
 * @returns {Promise<{ status: number, body: Record<string, unknown> }>}
 */
export async function handleMcpRequest({ headers, body, indexedDB, dashboardPath, signal }) {
  /** @param {string} name */
  const header = (name) => {
    const value = headers[name] ?? headers[name.toLowerCase()];
    return typeof value === 'string' ? value.trim() : '';
  };
  if (Buffer.byteLength(body) > MAX_MCP_REQUEST_BYTES) {
    return { status: 413, body: errorResponse(null, -32600, `Request body exceeds ${MAX_MCP_REQUEST_BYTES} bytes`) };
  }
  let message;
  try {
    message = JSON.parse(body);
  } catch {
    return { status: 400, body: errorResponse(null, -32700, 'Request body is not valid JSON') };
  }
  if (!isPlainObject(message) || message.jsonrpc !== '2.0' || typeof message.method !== 'string') {
    return { status: 400, body: errorResponse(null, -32600, 'Request must be a JSON-RPC 2.0 object with a method') };
  }
  const id = typeof message.id === 'string' || typeof message.id === 'number' ? message.id : null;
  const method = message.method;
  const params = isPlainObject(message.params) ? message.params : {};
  if (method === 'initialize') {
    const requestedVersion = typeof params.protocolVersion === 'string' ? params.protocolVersion : '';
    if (requestedVersion !== GH_AW_MCP_PROTOCOL_VERSION && requestedVersion !== MCP_PROTOCOL_VERSION) {
      return {
        status: 400,
        body: errorResponse(id, -32600, `Unsupported MCP protocol version: ${requestedVersion || '(missing)'}`)
      };
    }
    return {
      status: 200,
      body: successResponse(id, {
        protocolVersion: requestedVersion,
        capabilities: { tools: {} },
        serverInfo: SERVER_INFO,
        instructions: INSTRUCTIONS
      })
    };
  }
  const protocolVersion = header('mcp-protocol-version');
  if (!protocolVersion) {
    return { status: 400, body: errorResponse(null, -32600, 'MCP-Protocol-Version header is required') };
  }
  if (protocolVersion !== MCP_PROTOCOL_VERSION && protocolVersion !== GH_AW_MCP_PROTOCOL_VERSION) {
    return {
      status: 400,
      body: errorResponse(null, -32600, `Unsupported MCP-Protocol-Version: ${protocolVersion}; this server implements ${MCP_PROTOCOL_VERSION}`)
    };
  }
  const declaredMethod = header('mcp-method');
  if (!declaredMethod && protocolVersion === MCP_PROTOCOL_VERSION) {
    return { status: 400, body: errorResponse(id, -32600, 'Mcp-Method header is required') };
  }
  if (declaredMethod && declaredMethod !== method) {
    return {
      status: 400,
      body: errorResponse(id, -32600, `Mcp-Method header ${declaredMethod} does not match request method ${method}`)
    };
  }
  const startedAt = Date.now();
  if (protocolVersion === GH_AW_MCP_PROTOCOL_VERSION && (method === 'notifications/initialized' || method === 'ping')) {
    return { status: 200, body: successResponse(id, {}) };
  }
  if (method === 'server/discover') {
    return {
      status: 200,
      body: successResponse(id, {
        protocolVersion: MCP_PROTOCOL_VERSION,
        serverInfo: SERVER_INFO,
        capabilities: { tools: {} },
        instructions: INSTRUCTIONS
      })
    };
  }
  if (method === 'tools/list') {
    return { status: 200, body: successResponse(id, { tools: MCP_TOOLS, cache: MCP_LIST_CACHE }) };
  }
  if (method !== 'tools/call') {
    return { status: 400, body: errorResponse(id, -32601, `Unknown method: ${method}`) };
  }
  const name = typeof params.name === 'string' ? params.name : '';
  const declaredName = header('mcp-name');
  if (declaredName && declaredName !== name) {
    return {
      status: 400,
      body: errorResponse(id, -32600, `Mcp-Name header ${declaredName} does not match tool ${name}`)
    };
  }
  const tool = MCP_TOOLS.find((candidate) => candidate.name === name);
  if (!tool) return { status: 400, body: errorResponse(id, -32602, `Unknown tool: ${name}`) };
  const args = params.arguments === undefined ? {} : params.arguments;
  if (!isPlainObject(args)) {
    return { status: 400, body: errorResponse(id, -32602, 'Tool arguments must be an object') };
  }
  if (signal?.aborted) {
    return { status: 499, body: errorResponse(id, -32800, 'Request cancelled') };
  }
  try {
    const payload = name === 'cao_catalog'
      ? await callCatalog(args, { dashboardPath })
      : await callQuery(args, { dashboardPath, indexedDB, signal });
    debug('%s %s %dms', method, name, Date.now() - startedAt);
    return { status: 200, body: successResponse(id, toolResult(payload)) };
  } catch (error) {
    if (error instanceof NamedQueryError) {
      debug('%s %s rejected %dms', method, name, Date.now() - startedAt);
      return { status: 200, body: successResponse(id, toolResult({ error: error.message }, true)) };
    }
    if (error instanceof Error && error.name === 'AbortError') {
      debug('%s %s cancelled %dms', method, name, Date.now() - startedAt);
      return { status: 499, body: errorResponse(id, -32800, 'Request cancelled') };
    }
    debug('%s %s failed %dms', method, name, Date.now() - startedAt);
    return { status: 500, body: errorResponse(id, -32603, 'Internal error executing the request') };
  }
}

/**
 * Reads the request body with a hard size bound.
 * @param {import('node:http').IncomingMessage} request
 */
async function readBody(request) {
  let size = 0;
  /** @type {Buffer[]} */
  const chunks = [];
  for await (const chunk of request) {
    size += chunk.length;
    if (size > MAX_MCP_REQUEST_BYTES) {
      throw Object.assign(new Error('Request body too large'), { oversized: true });
    }
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}

/**
 * Starts the read-only MCP endpoint over plain HTTP.
 *
 * The endpoint binds loopback by default. Binding another interface is an
 * explicit operator choice and is intended only for an isolated job-local
 * container network shared with the agent runtime.
 *
 * @param {{
 *   indexedDB: IDBFactory,
 *   dashboardPath?: string,
 *   host?: string,
 *   port?: number,
 *   signal?: AbortSignal
 * }} options
 */
export async function startMcpServer({
  indexedDB,
  dashboardPath,
  host = DEFAULT_MCP_HOST,
  port = DEFAULT_MCP_PORT,
  signal
}) {
  // Fail before binding when the dashboard definition is unusable.
  await loadAgentDashboardDocument(dashboardPath);
  /** @type {import('node:http').RequestListener} */
  const listener = async (request, response) => {
    /** @param {number} status @param {unknown} body */
    const send = (status, body) => {
      const payload = JSON.stringify(body);
      response.writeHead(status, {
        'content-type': 'application/json',
        'content-length': Buffer.byteLength(payload),
        'cache-control': 'no-store'
      });
      response.end(payload);
    };
    const url = new URL(request.url ?? '/', `http://${request.headers.host ?? host}`);
    if (request.method === 'GET' && url.pathname === '/healthz') {
      send(200, { status: 'ok', protocolVersion: MCP_PROTOCOL_VERSION });
      return;
    }
    if (url.pathname !== '/mcp') {
      send(404, errorResponse(null, -32601, 'Unknown endpoint; this server exposes POST /mcp'));
      return;
    }
    if (request.method !== 'POST') {
      send(405, errorResponse(null, -32600, 'The MCP endpoint accepts POST requests'));
      return;
    }
    try {
      const body = await readBody(request);
      const result = await handleMcpRequest({
        headers: request.headers,
        body,
        indexedDB,
        dashboardPath,
        signal
      });
      send(result.status, result.body);
    } catch (error) {
      if (/** @type {{ oversized?: boolean }} */ (error)?.oversized) {
        send(413, errorResponse(null, -32600, `Request body exceeds ${MAX_MCP_REQUEST_BYTES} bytes`));
        return;
      }
      send(500, errorResponse(null, -32603, 'Internal error reading the request'));
    }
  };
  const server = createServer(listener);
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, host, () => {
      server.removeListener('error', reject);
      resolve(undefined);
    });
  });
  const address = server.address();
  const boundPort = typeof address === 'object' && address ? address.port : port;
  // A wildcard bind is not connectable, so advertise a reachable loopback URL.
  const advertisedHost = host === '0.0.0.0' || host === '::' ? '127.0.0.1' : host;
  const url = `http://${advertisedHost}:${boundPort}/mcp`;
  const closed = new Promise((resolve) => server.once('close', resolve));
  const stop = async () => {
    server.close();
    server.closeAllConnections?.();
    await closed;
  };
  if (signal) {
    if (signal.aborted) await stop();
    else signal.addEventListener('abort', () => { void stop(); }, { once: true });
  }
  return { url, port: boundPort, server, stop, closed };
}
