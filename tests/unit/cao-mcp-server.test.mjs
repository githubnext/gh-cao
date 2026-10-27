import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import {
  MAX_MCP_REQUEST_BYTES,
  MCP_LIST_CACHE,
  MCP_PROTOCOL_VERSION,
  handleMcpRequest,
  startMcpServer,
} from "../../activity/mcp-server.mjs";
import { installSqliteIndexedDB } from "../../dashboard/site/src/data/storage/sqlite-indexeddb.js";

const temporaryDirectory = await mkdtemp(path.join(tmpdir(), "cao-mcp-test-"));
const indexedDB = await installSqliteIndexedDB(
  path.join(temporaryDirectory, "gh-aw-logs.sqlite"),
);

test.after(async () => {
  await rm(temporaryDirectory, { recursive: true, force: true });
});

/**
 * @param {string} method
 * @param {unknown} params
 * @param {Record<string, string>} [headers]
 */
function request(method, params, headers = {}) {
  return handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": method,
      ...headers,
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
    indexedDB,
  });
}

/**
 * Issues one MCP request over the wire so the transport is tested at the real
 * protocol boundary rather than through a client library.
 *
 * @param {string} url
 * @param {Record<string, unknown>} message
 */
function postOverHttp(url, message) {
  const body = JSON.stringify(message);
  return new Promise((resolve, reject) => {
    const request = http.request(url, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "content-length": Buffer.byteLength(body),
        "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
        "Mcp-Method": String(message.method),
      },
    }, (response) => {
      let text = "";
      response.setEncoding("utf8");
      response.on("data", (chunk) => { text += chunk; });
      response.on("end", () => {
        try {
          resolve({ status: response.statusCode, payload: JSON.parse(text) });
        } catch (error) {
          reject(error);
        }
      });
    });
    request.on("error", reject);
    request.end(body);
  });
}

test("server/discover advertises the stateless protocol revision", async () => {
  const { status, body } = await request("server/discover", {});
  assert.equal(status, 200);
  assert.equal(body.result.protocolVersion, MCP_PROTOCOL_VERSION);
  assert.equal(body.result.serverInfo.name, "cao");
  assert.ok(body.result.capabilities.tools);
});

test("gh-aw gateway can initialize and list tools over HTTP", async () => {
  const initialize = await handleMcpRequest({
    headers: {},
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "initialize",
      params: {
        capabilities: {},
        clientInfo: { name: "check-mcp-servers", version: "1.0.0" },
        protocolVersion: "2024-11-05",
      },
    }),
    indexedDB,
  });
  assert.equal(initialize.status, 200);
  assert.equal(initialize.body.result.protocolVersion, "2024-11-05");
  assert.deepEqual(initialize.body.result.capabilities, { tools: {} });

  const listed = await handleMcpRequest({
    headers: { "mcp-protocol-version": "2024-11-05" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list", params: {} }),
    indexedDB,
  });
  assert.equal(listed.status, 200);
  assert.deepEqual(
    listed.body.result.tools.map((tool) => tool.name),
    ["cao_catalog", "cao_query"],
  );
});

test("tools/list returns a deterministic, cacheable catalog", async () => {
  const { status, body } = await request("tools/list", {});
  assert.equal(status, 200);
  assert.deepEqual(
    body.result.tools.map((tool) => tool.name),
    ["cao_catalog", "cao_query"],
  );
  assert.deepEqual(body.result.cache, MCP_LIST_CACHE);
  const repeated = await request("tools/list", {});
  assert.deepEqual(repeated.body, body);
});

test("cao_catalog describes pages and queries", async () => {
  const pages = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "pages" } },
    { "mcp-name": "cao_catalog" },
  );
  assert.equal(pages.status, 200);
  assert.ok(pages.body.result.structuredContent.pages.length > 0);

  const query = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "queries", id: "campaign-runs" } },
    { "mcp-name": "cao_catalog" },
  );
  assert.equal(query.body.result.structuredContent.query.id, "campaign-runs");
  assert.equal(query.body.result.structuredContent.query.execution.local, true);
});

test("cao_catalog rejects an unknown kind", async () => {
  const { body } = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "sql" } },
    { "mcp-name": "cao_catalog" },
  );
  assert.equal(body.result.isError, true);
});

test("cao_query returns rows with availability metadata", async () => {
  const { status, body } = await request(
    "tools/call",
    { name: "cao_query", arguments: { id: "campaign-runs", limit: 5 } },
    { "mcp-name": "cao_query" },
  );
  assert.equal(status, 200);
  const payload = body.result.structuredContent;
  assert.equal(payload.query, "campaign-runs");
  assert.ok(Array.isArray(payload.rows));
  assert.ok(payload.metadata.availability);
  assert.ok(payload.metadata.completeness);
  assert.ok(payload.metadata.freshness);
  assert.ok(payload.metadata["as-of"]);
});

test("cao_query runs a typed simulator scenario and rejects invalid inputs", async () => {
  const parameters = {
    repositories: 2,
    "runs-per-day": 3,
    "tools-per-run": 4,
    "issues-per-run": 1,
    "skip-rate": 50,
  };
  const invoke = (values) => request(
    "tools/call",
    { name: "cao_query", arguments: { id: "simulator-database-summary", parameters: values } },
    { "mcp-name": "cao_query" },
  );
  const { status, body } = await invoke(parameters);
  assert.equal(status, 200);
  assert.equal(body.result.isError, undefined);
  assert.deepEqual(body.result.structuredContent.rows, [
    { table: "Run summaries", bytes: 92160 },
    { table: "Tools (30-day TTL)", bytes: 92160 },
    { table: "Issues (30-day TTL)", bytes: 46080 },
    { table: "Total", bytes: 230400 },
  ]);
  for (const values of [
    { ...parameters, repositories: 100001 },
    { ...parameters, "skip-rate": 0 },
    { ...parameters, "tools-per-run": "NaN" },
    { repositories: 2 },
  ]) {
    const rejected = await invoke(values);
    assert.equal(rejected.body.result.isError, true);
  }
});

test("cao_query refuses unknown queries and unknown parameters", async () => {
  const unknownQuery = await request(
    "tools/call",
    { name: "cao_query", arguments: { id: "not-a-query" } },
    { "mcp-name": "cao_query" },
  );
  assert.equal(unknownQuery.body.result.isError, true);
  assert.match(unknownQuery.body.result.content[0].text, /not-a-query/);

  const unknownParameter = await request(
    "tools/call",
    {
      name: "cao_query",
      arguments: { id: "campaign-runs", parameters: { nope: "value" } },
    },
    { "mcp-name": "cao_query" },
  );
  assert.equal(unknownParameter.body.result.isError, true);
});

test("MCP does not expose arbitrary SQL or declarative queries", async () => {
  const { body } = await request("tools/list", {});
  const names = body.result.tools.map((tool) => tool.name);
  for (const forbidden of ["cao_sql", "execute_sql", "query_sqlite"]) {
    assert.ok(!names.includes(forbidden));
  }
  const unknownTool = await request(
    "tools/call",
    { name: "cao_sql", arguments: {} },
    { "mcp-name": "cao_sql" },
  );
  assert.equal(unknownTool.status, 400);
  assert.equal(unknownTool.body.error.code, -32602);
});

test("requests must carry an agreeing protocol and method header", async () => {
  const missingVersion = await handleMcpRequest({
    headers: { "mcp-method": "tools/list" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    indexedDB,
  });
  assert.equal(missingVersion.status, 400);
  assert.match(missingVersion.body.error.message, /MCP-Protocol-Version/);

  const wrongVersion = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": "2025-06-18",
      "mcp-method": "tools/list",
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    indexedDB,
  });
  assert.equal(wrongVersion.status, 400);

  const wrongMethod = await request("tools/list", {}, { "mcp-method": "tools/call" });
  assert.equal(wrongMethod.status, 400);
  assert.equal(wrongMethod.body.error.code, -32600);

  const wrongName = await request(
    "tools/call",
    { name: "cao_query", arguments: { id: "campaign-runs" } },
    { "mcp-name": "cao_catalog" },
  );
  assert.equal(wrongName.status, 400);
  assert.equal(wrongName.body.error.code, -32600);
});

test("malformed, oversized, and unknown requests are rejected", async () => {
  const malformed = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": "tools/list",
    },
    body: "{not json",
    indexedDB,
  });
  assert.equal(malformed.status, 400);
  assert.equal(malformed.body.error.code, -32700);

  const oversized = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": "tools/call",
    },
    body: "x".repeat(MAX_MCP_REQUEST_BYTES + 1),
    indexedDB,
  });
  assert.equal(oversized.status, 413);

  const unknownMethod = await request("resources/list", {});
  assert.equal(unknownMethod.status, 400);
  assert.equal(unknownMethod.body.error.code, -32601);
});

test("cancelled requests stop before returning rows", async () => {
  const controller = new AbortController();
  controller.abort();
  const { status, body } = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": "tools/call",
      "mcp-name": "cao_query",
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: { name: "cao_query", arguments: { id: "campaign-runs" } },
    }),
    indexedDB,
    signal: controller.signal,
  });
  assert.equal(status, 499);
  assert.equal(body.error.code, -32800);
});

test("the server answers MCP requests over plain HTTP", async () => {
  const server = await startMcpServer({ indexedDB, host: "127.0.0.1", port: 0 });
  try {
    assert.match(server.url, /^http:\/\/127\.0\.0\.1:\d+\/mcp$/);
    const { status, payload } = await postOverHttp(
      server.url,
      { jsonrpc: "2.0", id: 7, method: "tools/list" },
    );
    assert.equal(status, 200);
    assert.equal(payload.id, 7);
    assert.deepEqual(
      payload.result.tools.map((tool) => tool.name),
      ["cao_catalog", "cao_query"],
    );
  } finally {
    await server.stop();
  }
});

test("the server reports health without a protocol handshake", async () => {
  const server = await startMcpServer({ indexedDB, host: "127.0.0.1", port: 0 });
  try {
    const health = await new Promise((resolve, reject) => {
      http.get(server.url.replace("/mcp", "/healthz"), (response) => {
        let text = "";
        response.setEncoding("utf8");
        response.on("data", (chunk) => { text += chunk; });
        response.on("end", () => resolve({ status: response.statusCode, payload: JSON.parse(text) }));
      }).on("error", reject);
    });
    assert.equal(health.status, 200);
    assert.equal(health.payload.status, "ok");
    assert.equal(health.payload.protocolVersion, MCP_PROTOCOL_VERSION);
  } finally {
    await server.stop();
  }
});

/**
 * Issues one raw HTTP request so transport concerns are tested at the wire.
 * @param {string} url
 * @param {{ method?: string, headers?: Record<string, string>, body?: string }} [options]
 */
function fetchOverHttp(url, options = {}) {
  return new Promise((resolve, reject) => {
    const outgoing = http.request(url, {
      method: options.method ?? "GET",
      headers: options.headers ?? {},
    }, (response) => {
      let text = "";
      response.setEncoding("utf8");
      response.on("data", (chunk) => { text += chunk; });
      response.on("end", () => resolve({ status: response.statusCode, text }));
    });
    outgoing.on("error", reject);
    outgoing.end(options.body);
  });
}

test("every advertised tool is read-only and closed to undeclared arguments", async () => {
  const { body } = await request("tools/list", {});
  assert.equal(body.result.tools.length, 2);
  for (const tool of body.result.tools) {
    assert.equal(tool.annotations.readOnlyHint, true);
    assert.equal(tool.annotations.untrustedContentHint, true);
    assert.equal(tool.inputSchema.additionalProperties, false);
    assert.ok(Array.isArray(tool.inputSchema.required));
    assert.ok(tool.description.length > 0);
  }
});

test("server/discover instructs agents to treat rows as untrusted data", async () => {
  const { body } = await request("server/discover", {});
  assert.match(body.result.instructions, /untrusted/i);
  assert.match(body.result.instructions, /cao_catalog/);
});

test("responses echo the request identifier, including a string identifier", async () => {
  const { body } = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": "tools/list",
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: "abc", method: "tools/list" }),
    indexedDB,
  });
  assert.equal(body.id, "abc");
  assert.equal(body.jsonrpc, "2.0");
});

test("a request without an identifier answers with a null identifier", async () => {
  const { status, body } = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": "tools/list",
    },
    body: JSON.stringify({ jsonrpc: "2.0", method: "tools/list" }),
    indexedDB,
  });
  assert.equal(status, 200);
  assert.equal(body.id, null);
});

test("requests that are not JSON-RPC 2.0 are refused", async () => {
  for (const body of [
    JSON.stringify({ id: 1, method: "tools/list" }),
    JSON.stringify({ jsonrpc: "1.0", id: 1, method: "tools/list" }),
    JSON.stringify({ jsonrpc: "2.0", id: 1 }),
    JSON.stringify([{ jsonrpc: "2.0", id: 1, method: "tools/list" }]),
    JSON.stringify("tools/list"),
  ]) {
    const response = await handleMcpRequest({
      headers: {
        "mcp-protocol-version": MCP_PROTOCOL_VERSION,
        "mcp-method": "tools/list",
      },
      body,
      indexedDB,
    });
    assert.equal(response.status, 400, `${body} is refused`);
    assert.equal(response.body.error.code, -32600);
  }
});

test("a missing Mcp-Method header is refused even when the body is valid", async () => {
  const { status, body } = await handleMcpRequest({
    headers: { "mcp-protocol-version": MCP_PROTOCOL_VERSION },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    indexedDB,
  });
  assert.equal(status, 400);
  assert.match(body.error.message, /Mcp-Method/);
});

test("tool arguments must be an object", async () => {
  for (const args of ["id", 7, ["campaign-runs"], null]) {
    const { status, body } = await request(
      "tools/call",
      { name: "cao_query", arguments: args },
      { "mcp-name": "cao_query" },
    );
    assert.equal(status, 400, `${JSON.stringify(args)} is refused`);
    assert.equal(body.error.code, -32602);
  }
});

test("cao_catalog reads a page by identifier and reports unknown identifiers", async () => {
  const pages = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "pages" } },
    { "mcp-name": "cao_catalog" },
  );
  const [first] = pages.body.result.structuredContent.pages;
  const page = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "pages", id: first.id } },
    { "mcp-name": "cao_catalog" },
  );
  assert.deepEqual(page.body.result.structuredContent.page, first);

  const unknown = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "pages", id: "not-a-page" } },
    { "mcp-name": "cao_catalog" },
  );
  assert.equal(unknown.body.result.isError, true);
});

test("cao_catalog reports the same queries the catalog declares", async () => {
  const { body } = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "queries" } },
    { "mcp-name": "cao_catalog" },
  );
  const { queries } = body.result.structuredContent;
  assert.ok(queries.length > 0);
  for (const query of queries) {
    assert.ok(query.id);
    assert.equal(typeof query.execution.local, "boolean");
    if (!query.execution.local) assert.ok(query.execution.reason);
  }
});

test("cao_catalog requires a kind", async () => {
  const { body } = await request(
    "tools/call",
    { name: "cao_catalog", arguments: {} },
    { "mcp-name": "cao_catalog" },
  );
  assert.equal(body.result.isError, true);
});

test("tool results carry both text content and structured content", async () => {
  const { body } = await request(
    "tools/call",
    { name: "cao_query", arguments: { id: "campaign-runs", limit: 1 } },
    { "mcp-name": "cao_query" },
  );
  assert.equal(body.result.content[0].type, "text");
  assert.deepEqual(
    JSON.parse(body.result.content[0].text),
    body.result.structuredContent,
  );
  assert.notEqual(body.result.isError, true);
});

test("cao_query refuses a row bound that is not a positive integer", async () => {
  for (const limit of [0, -1, 2.5, "5"]) {
    const { body } = await request(
      "tools/call",
      { name: "cao_query", arguments: { id: "campaign-runs", limit } },
      { "mcp-name": "cao_query" },
    );
    assert.equal(body.result.isError, true, `limit ${limit} is refused`);
  }
});

test("cao_query clamps an oversized row bound instead of trusting it", async () => {
  const { body } = await request(
    "tools/call",
    { name: "cao_query", arguments: { id: "campaign-runs", limit: 10_000_000 } },
    { "mcp-name": "cao_query" },
  );
  assert.notEqual(body.result.isError, true);
  assert.ok(body.result.structuredContent.metadata.limit <= 5000);
});

test("cao_query never accepts SQL or an unreviewed query definition", async () => {
  for (const id of [
    "select * from runs",
    JSON.stringify({ name: "adhoc", from: "runs" }),
    "",
  ]) {
    const { body } = await request(
      "tools/call",
      { name: "cao_query", arguments: { id } },
      { "mcp-name": "cao_query" },
    );
    assert.equal(body.result.isError, true, `${id} is refused`);
  }
});

test("cao_query reports why a non-local query cannot run", async () => {
  const { body } = await request(
    "tools/call",
    { name: "cao_catalog", arguments: { kind: "queries" } },
    { "mcp-name": "cao_catalog" },
  );
  const blocked = body.result.structuredContent.queries.find((query) => !query.execution.local);
  assert.ok(blocked, "the dashboard declares at least one non-local query");
  const result = await request(
    "tools/call",
    { name: "cao_query", arguments: { id: blocked.id } },
    { "mcp-name": "cao_query" },
  );
  const payload = result.body.result.structuredContent;
  assert.equal(payload.metadata.availability, "unavailable");
  assert.deepEqual(payload.rows, []);
});

test("the request body bound is measured in bytes, not characters", async () => {
  const body = JSON.stringify({
    jsonrpc: "2.0",
    id: 1,
    method: "tools/call",
    params: {
      name: "cao_query",
      arguments: { id: "campaign-runs", parameters: { campaign: "\u00e9".repeat(MAX_MCP_REQUEST_BYTES / 2) } },
    },
  });
  assert.ok(body.length < MAX_MCP_REQUEST_BYTES);
  assert.ok(Buffer.byteLength(body) > MAX_MCP_REQUEST_BYTES);
  const response = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": "tools/call",
    },
    body,
    indexedDB,
  });
  assert.equal(response.status, 413);
});

test("the transport refuses unknown endpoints and non-POST requests", async () => {
  const server = await startMcpServer({ indexedDB, host: "127.0.0.1", port: 0 });
  try {
    const unknown = await fetchOverHttp(server.url.replace("/mcp", "/secrets"));
    assert.equal(unknown.status, 404);

    const wrongVerb = await fetchOverHttp(server.url);
    assert.equal(wrongVerb.status, 405);
  } finally {
    await server.stop();
  }
});

test("the transport enforces the body bound before parsing", async () => {
  const server = await startMcpServer({ indexedDB, host: "127.0.0.1", port: 0 });
  try {
    const oversized = await fetchOverHttp(server.url, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
        "Mcp-Method": "tools/list",
      },
      body: "x".repeat(MAX_MCP_REQUEST_BYTES + 1024),
    });
    assert.equal(oversized.status, 413);
  } finally {
    await server.stop();
  }
});

test("the transport binds loopback and reports its endpoint", async () => {
  const server = await startMcpServer({ indexedDB, host: "127.0.0.1", port: 0 });
  try {
    assert.equal(server.url, `http://127.0.0.1:${server.port}/mcp`);
    assert.ok(server.port > 0);
  } finally {
    await server.stop();
  }
});

test("stopping the server closes the endpoint", async () => {
  const server = await startMcpServer({ indexedDB, host: "127.0.0.1", port: 0 });
  const { url } = server;
  await server.stop();
  await assert.rejects(() => fetchOverHttp(url.replace("/mcp", "/healthz")));
});

test("the server executes queries over the wire, not only in process", async () => {
  const server = await startMcpServer({ indexedDB, host: "127.0.0.1", port: 0 });
  try {
    const response = await fetchOverHttp(server.url, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
        "Mcp-Method": "tools/call",
        "Mcp-Name": "cao_query",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 11,
        method: "tools/call",
        params: { name: "cao_query", arguments: { id: "campaign-runs", limit: 2 } },
      }),
    });
    assert.equal(response.status, 200);
    const payload = JSON.parse(response.text);
    assert.equal(payload.id, 11);
    assert.equal(payload.result.structuredContent.query, "campaign-runs");
  } finally {
    await server.stop();
  }
});
