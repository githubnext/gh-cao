import { createHash } from "node:crypto";
import { execFileSync, spawn } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium } from "@playwright/test";
import { IDBFactory } from "fake-indexeddb";
import {
  loadDatabaseQuerySources,
} from "../../dashboard/site/src/data/queries/database.js";
import {
  DATABASE_STORES,
  readCollections,
} from "../../dashboard/site/src/data/storage/indexeddb.js";
import {
  installSqliteIndexedDB,
} from "../../dashboard/site/src/data/storage/sqlite-indexeddb.js";
import { CLI_QUEUE_SOURCES } from "../../dashboard/site/src/data/cli-queue.js";
import { loadDashboardSourceSync } from "../../dashboard/report/bundle-dashboards.mjs";
import {
  listQueries,
  queryExecutionRequirements,
} from "../../dashboard/site/src/agent/catalog.js";
import {
  MAX_NAMED_QUERY_LIMIT,
  executeNamedQuery,
} from "../../dashboard/site/src/agent/query-executor.js";
import {
  MCP_PROTOCOL_VERSION,
  handleMcpRequest,
} from "../../activity/mcp-server.mjs";
import { waitForServer } from "./dashboard-query-parity-readiness.mjs";

const repositoryRoot = resolve(import.meta.dirname, "../..");
const siteRoot = join(repositoryRoot, "dashboard/site");
const dashboardPath = join(siteRoot, "dashboard.json");
const databaseQueriesPath = join(siteRoot, "src/data/queries/database.json");
const agentCatalogPath = join(siteRoot, "src/agent/catalog.generated.json");
const mcpContractPath = join(siteRoot, "src/agent/mcp-contract.json");
const reportDirectory = resolve(
  process.env.DASHBOARD_QUERY_PARITY_OUTPUT ?? "test-results/dashboard-query-parity",
);
const reportPath = join(reportDirectory, "report.json");
const accessToken = "dashboard-query-parity-access-token";
const serverURL = "http://127.0.0.1:18443";
const evidenceDay = new Date(Date.now() - 86_400_000).toISOString().slice(0, 10);
const metadata = {
  "as-of": `${evidenceDay}T18:05:00Z`,
  completeness: "complete",
  freshness: "fresh",
  "artifact-generation": "synthetic-query-parity",
};

function logicalSource(rows) {
  return { rows, metadata };
}

function firstDifference(left, right, path = "$") {
  if (Object.is(left, right)) return null;
  if (typeof left !== typeof right || left === null || right === null) {
    return { path, left, right };
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return { path, leftLength: left?.length, rightLength: right?.length };
    }
    for (let index = 0; index < left.length; index += 1) {
      const difference = firstDifference(left[index], right[index], `${path}[${index}]`);
      if (difference) return difference;
    }
    return null;
  }
  if (typeof left === "object") {
    const keys = [...new Set([...Object.keys(left), ...Object.keys(right)])].toSorted();
    for (const key of keys) {
      if (!Object.hasOwn(left, key) || !Object.hasOwn(right, key)) {
        return { path: `${path}.${key}`, left: left[key], right: right[key] };
      }
      const difference = firstDifference(left[key], right[key], `${path}.${key}`);
      if (difference) return difference;
    }
    return null;
  }
  return { path, left, right };
}

function representativeSources() {
  const workflows = [
    ["alpha", "review", "orchestrator"],
    ["beta", "live", "worker"],
    ["idle", "review", "worker"],
  ];
  const runs = [
    ["1001", "alpha", "success", "workflow_dispatch", `${evidenceDay}T17:00:00Z`, 4],
    ["1002", "alpha", "failure", "schedule", `${evidenceDay}T17:15:00Z`, 9],
    ["1003", "beta", "success", "schedule", `${evidenceDay}T17:30:00Z`, 6],
    ["1004", "beta", "timed-out", "workflow_dispatch", `${evidenceDay}T17:45:00Z`, 12],
  ];
  const workflowPath = (name) => `.github/workflows/${name}.md`;
  return {
    campaigns: logicalSource([
      {
        campaign: "synthetic-operations",
        "campaign-name": "Synthetic operations",
        "campaign-description": "Representative query parity data",
        "campaign-mode": "review",
        "campaign-enabled": true,
        "campaign-worker-count": 2,
        "observed-at": metadata["as-of"],
      },
    ]),
    repositories: logicalSource([
      {
        organization: "synthetic-org",
        repository: "control-plane",
        visibility: "private",
        "rollout-mode": "review",
        "observed-at": metadata["as-of"],
      },
      {
        organization: "synthetic-org",
        repository: "target",
        visibility: "private",
        "rollout-mode": "live",
        "observed-at": metadata["as-of"],
      },
    ]),
    workflows: logicalSource(workflows.map(([name, mode, role], index) => ({
      organization: "synthetic-org",
      repository: index === 1 ? "target" : "control-plane",
      workflow: workflowPath(name),
      "workflow-id": String(500 + index),
      "workflow-name": `Synthetic ${name}`,
      "workflow-role": role,
      "workflow-active": "true",
      "workflow-registry-state": "active",
      "admission-status": "admitted",
      "inventory-ready": true,
      campaign: "synthetic-operations",
      "campaign-name": "Synthetic operations",
      "rollout-mode": mode,
      "created-at": "2026-09-01T00:00:00Z",
      "updated-at": metadata["as-of"],
      "observed-at": metadata["as-of"],
    }))),
    runs: logicalSource(runs.map(([id, workflow, conclusion, event, startedAt, aic], index) => ({
      organization: "synthetic-org",
      repository: workflow === "beta" ? "target" : "control-plane",
      workflow: workflowPath(workflow),
      run: id,
      "run-attempt": 1,
      "run-status": "completed",
      "run-conclusion": conclusion,
      event,
      "started-at": startedAt,
      "ended-at": new Date(Date.parse(startedAt) + (index + 1) * 60_000).toISOString(),
      duration: String((index + 1) * 60),
      "rollout-mode": workflow === "beta" ? "live" : "review",
      engine: index % 2 === 0 ? "copilot" : "claude",
      "requested-model": index % 2 === 0 ? "model-a" : "model-b",
      "resolved-model": index % 2 === 0 ? "model-a" : "model-c",
      "aic-total": aic,
      "observed-at": metadata["as-of"],
    }))),
    usage: logicalSource(runs.map(([id, workflow, , , startedAt, aic], index) => ({
      organization: "synthetic-org",
      repository: workflow === "beta" ? "target" : "control-plane",
      workflow: workflowPath(workflow),
      run: id,
      engine: index % 2 === 0 ? "copilot" : "claude",
      "resolved-model": index % 2 === 0 ? "model-a" : "model-c",
      "rollout-mode": workflow === "beta" ? "live" : "review",
      aic,
      "input-tokens": 1000 + index * 100,
      "output-tokens": 100 + index * 10,
      "observed-at": startedAt,
    }))),
    "work-items": logicalSource(runs.map(([id, workflow, conclusion, , startedAt]) => ({
      "work-item-id": `synthetic:${workflow}:${id}`,
      name: `Synthetic ${workflow}`,
      objective: "Exercise dashboard query semantics",
      organization: "synthetic-org",
      repository: workflow === "beta" ? "target" : "control-plane",
      workflow: workflowPath(workflow),
      run: id,
      campaign: "synthetic-operations",
      "lifecycle-state": conclusion === "success" ? "completed" : "blocked",
      phase: "completed",
      "verification-state": conclusion === "success" ? "verified" : "pending",
      "outcome-state": conclusion === "success" ? "accepted" : "pending",
      "started-at": startedAt,
      "observed-at": metadata["as-of"],
    }))),
    "operational-values": logicalSource([
      {
        "operation-id": "synthetic-operations",
        operation: "Synthetic operations",
        repository: "control-plane",
        "verification-state": "verified",
        "outcome-state": "accepted",
        value: 42,
        "observed-at": metadata["as-of"],
      },
    ]),
    outcomes: logicalSource([
      {
        "safe-output": "issue-17",
        "safe-output-kind": "create-issue",
        "outcome-title": "Synthetic finding",
        "outcome-status": "open",
        "outcome-state": "accepted",
        organization: "synthetic-org",
        repository: "control-plane",
        workflow: workflowPath("alpha"),
        run: "1002",
        "rollout-mode": "review",
        "published-at": `${evidenceDay}T17:16:00Z`,
        "observed-at": metadata["as-of"],
        "external-link": { href: "https://example.invalid/issues/17" },
      },
    ]),
    "grader-observations": logicalSource([
      {
        organization: "synthetic-org",
        repository: "control-plane",
        workflow: workflowPath("alpha"),
        run: "1001",
        grader: "synthetic-quality",
        score: 0.75,
        "observed-at": metadata["as-of"],
      },
    ]),
    "firewall-observations": logicalSource([
      {
        organization: "synthetic-org",
        repository: "control-plane",
        workflow: workflowPath("alpha"),
        run: "1001",
        domain: "api.github.com",
        decision: "allowed",
        "request-count": 3,
        "observed-at": metadata["as-of"],
      },
    ]),
  };
}

function dashboardQueries() {
  const { document } = loadDashboardSourceSync(dashboardPath);
  // This corpus has no scenario inputs; parameterized queries are exercised
  // separately with explicit values by the named-query tests.
  const unparameterized = new Set(listQueries(document)
    .filter((query) => !query.parameters.some((parameter) => parameter.type))
    .map((query) => query.id));
  const resolveContext = (value) => {
    if (Array.isArray(value)) return value.map(resolveContext);
    if (!value || typeof value !== "object") return value;
    if (value.context === "time-end") return { value: metadata["as-of"] };
    return Object.fromEntries(Object.entries(value).map(
      ([key, item]) => [key, resolveContext(item)],
    ));
  };
  // Ingestion receipts and local command queues are backend-local state,
  // not shared canonical evidence for cross-backend comparisons.
  const candidates = resolveContext(document.dashboard.queries).filter(
    (query) => unparameterized.has(query.name),
  );
  const excluded = new Set(candidates
    .filter((query) => query.from === "transactions" || CLI_QUEUE_SOURCES.has(query.from))
    .map((query) => query.name));
  const inputs = (query) => [
    query.from,
    ...(query.union ?? []),
    ...(query.joins ?? []).map((join) => join.source),
  ];
  // Queries derived from receipt-backed queries inherit their backend-local values.
  for (let changed = true; changed;) {
    changed = false;
    for (const query of candidates) {
      if (!excluded.has(query.name) && inputs(query).some((input) => excluded.has(input))) {
        excluded.add(query.name);
        changed = true;
      }
    }
  }
  return candidates.filter((query) => !excluded.has(query.name));
}

function rowsByQuery(result, names) {
  return Object.fromEntries(names.map((name) => [name, result[name]?.rows ?? []]));
}

async function executeNodeBackend(factory, sources, queries, names) {
  const result = await loadDatabaseQuerySources(factory, sources, {
    ingest: true,
    queries,
    sourceNames: names,
  });
  return rowsByQuery(result, names);
}

/**
 * Executes the shared named-query executor that the `cao` CLI uses so agent
 * transports stay in parity with the dashboard itself.
 */
async function executeNamedQueryBackend(factory, document, names) {
  const rows = {};
  for (const name of names) {
    const result = await executeNamedQuery({
      indexedDB: factory,
      document,
      queryId: name,
      limit: MAX_NAMED_QUERY_LIMIT,
    });
    rows[name] = result.rows;
  }
  return rows;
}

/**
 * Executes the same named queries over the MCP wire boundary.
 */
async function executeMcpBackend(factory, dashboardPath, names) {
  const rows = {};
  for (const name of names) {
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
        params: {
          name: "cao_query",
          arguments: { id: name, limit: MAX_NAMED_QUERY_LIMIT },
        },
      }),
      indexedDB: factory,
      dashboardPath,
    });
    if (status !== 200 || body.result?.isError) {
      throw new Error(`MCP cao_query failed for ${name}: ${JSON.stringify(body)}`);
    }

    rows[name] = body.result.structuredContent.rows;
  }
  return rows;
}

async function callGoMcp(method, params = {}) {
  const response = await fetch(`${serverURL}/mcp`, {
    method: "POST",
    headers: {
      authorization: ["Bearer", accessToken].join(" "),
      accept: "application/json, text/event-stream",
      "content-type": "application/json",
      "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
      "Mcp-Method": method,
      ...(params.name ? { "Mcp-Name": params.name } : {}),
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method,
      params: {
        ...params,
        _meta: {
          "io.modelcontextprotocol/protocolVersion": MCP_PROTOCOL_VERSION,
          "io.modelcontextprotocol/clientCapabilities": {},
          "io.modelcontextprotocol/clientInfo": { name: "parity", version: "1" },
        },
      },
    }),
  });
  const body = await response.json();
  if (!response.ok) throw new Error(`Go MCP ${method} failed (${response.status}): ${JSON.stringify(body)}`);
  return body;
}

async function callNodeMcp(factory, dashboardDocumentPath, method, params = {}) {
  const { status, body } = await handleMcpRequest({
    headers: {
      "mcp-protocol-version": MCP_PROTOCOL_VERSION,
      "mcp-method": method,
      ...(params.name ? { "mcp-name": params.name } : {}),
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
    indexedDB: factory,
    dashboardPath: dashboardDocumentPath,
  });
  if (status !== 200) throw new Error(`Node MCP ${method} failed (${status}): ${JSON.stringify(body)}`);
  return body;
}

function canonicalToolResult(result) {
  const canonical = structuredClone(result);
  for (const key of ["_meta", "content", "resultType", "ttlMs", "cacheScope"]) delete canonical[key];
  if (canonical.isError === false) delete canonical.isError;
  const metadata = canonical.structuredContent?.metadata;
  if (metadata) {
    // Source quality is recorded by each backend's own ingestion, so the local
    // projection and hosted database legitimately differ on these fields.
    delete metadata["as-of"];
    delete metadata.completeness;
    delete metadata.freshness;
  }
  return canonical;
}

async function executeBrowserBackend(sources, queries, names) {
  const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
    ?? (existsSync("/usr/bin/chromium") ? "/usr/bin/chromium" : undefined);
  const browser = await chromium.launch({
    headless: true,
    args: ["--no-sandbox"],
    ...(executablePath ? { executablePath } : {}),
  });
  try {
    const context = await browser.newContext();
    await context.route("http://localhost/**", async (route) => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname === "/" || pathname === "/index.html") {
        await route.fulfill({ contentType: "text/html", body: "<main>Query parity</main>" });
        return;
      }
      if (pathname === "/sources/manifest.json") {
        await route.fulfill({ status: 404, body: "Not found" });
        return;
      }
      if (pathname === "/sources.json") {
        await route.fulfill({
          contentType: "application/json",
          body: JSON.stringify(sources),
        });
        return;
      }
      const filePath = join(siteRoot, pathname);
      if (!filePath.startsWith(`${siteRoot}/`) || !existsSync(filePath)) {
        await route.fulfill({ status: 404, body: "Not found" });
        return;
      }
      await route.fulfill({
        contentType: pathname.endsWith(".json") ? "application/json" : "application/javascript",
        body: readFileSync(filePath),
      });
    });
    const page = await context.newPage();
    await page.goto("http://localhost/");
    return await page.evaluate(async ({ queries: definitions, names: requested }) => {
      const { loadCanonicalDashboardSources } = await import(
        `${location.origin}/src/data-processor.js`
      );
      const result = await loadCanonicalDashboardSources(
        `${location.origin}/sources.json`,
        requested,
        { githubUrlBase: "https://github.com", pages: [], queries: definitions },
      );
      return Object.fromEntries(requested.map((name) => [name, result[name]?.rows ?? []]));
    }, { queries, names });
  } finally {
    await browser.close();
  }
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function writeDeployedArtifact(directory, factory, sources) {
  const canonical = await readCollections(factory, DATABASE_STORES.filter(
    (name) => name !== "transactions",
  ));
  const runCollections = new Set(["campaigns", "repositories", "workflows", "runs"]);
  const encode = (collections, phase) => {
    const lines = collections.flatMap((collection) => canonical[collection].map((record) =>
      JSON.stringify({ kind: "record", collection, record })
    ));
    return [
      JSON.stringify({
        kind: "metadata",
        schemaVersion: 13,
        ingestionVersion: 3,
        phase,
        records: lines.length,
      }),
      ...lines,
      "",
    ].join("\n");
  };
  const runs = encode([...runCollections], "runs");
  const recordCollections = ["domains", "tools", "skills", "friction", "audits", "issues", "operationalValues"];
  const records = encode(recordCollections, "records");
  const runsName = "gh-aw-logs-runs/synthetic.jsonl";
  const recordsName = "gh-aw-logs-records/synthetic.jsonl";
  await mkdir(join(directory, dirname(runsName)), { recursive: true });
  await mkdir(join(directory, dirname(recordsName)), { recursive: true });
  await Promise.all([
    writeFile(join(directory, runsName), runs),
    writeFile(join(directory, recordsName), records),
    writeFile(
      join(directory, "inventory-sources.json"),
      `${JSON.stringify(Object.fromEntries(Object.entries(sources).filter(
        ([name]) => !runCollections.has(name) && !recordCollections.includes(name),
      )), null, 2)}\n`,
    ),
    writeFile(
      join(directory, "payload-hashes.json"),
      `${JSON.stringify({
        [runsName]: sha256(runs),
        [recordsName]: sha256(records),
      }, null, 2)}\n`,
    ),
  ]);
}

async function executePostgresBackend(
  artifactDirectory,
  dashboardDocumentPath,
  agentFactory,
  queries,
  names,
  localNames,
) {
  const binary = process.env.DASHBOARD_SERVER_BINARY;
  if (!binary) throw new Error("DASHBOARD_SERVER_BINARY is required");
  const namespace = `query-parity-${process.pid}`;
  execFileSync(binary, [
    "database", "bootstrap", "--store", "canonical", "--namespace", namespace,
  ], {
    stdio: "inherit",
    env: { ...process.env, CAO_DATABASE_ADMIN_URL: process.env.CAO_POSTGRES_URL },
  });
  const marketplacePolicyPath = join(artifactDirectory, "cao.json");
  await writeFile(marketplacePolicyPath, JSON.stringify({
    version: 1,
    "control-plane": {
      marketplace: { registries: [] },
    },
  }));
  const child = spawn(binary, [
    "serve",
    "--source", artifactDirectory,
    "--access-token", accessToken,
    "--listen", "127.0.0.1:18443",
    "--site", siteRoot,
    "--dashboard-queries", dashboardDocumentPath,
    "--agent-catalog", agentCatalogPath,
    "--mcp-contract", mcpContractPath,
    "--mcp-enabled",
    "--database-queries", databaseQueriesPath,
    "--redis-url", process.env.REDIS_URL ?? "redis://127.0.0.1:6379/0",
    "--redis-namespace", namespace,
  ], {
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, CAO_MARKETPLACE_POLICY_PATH: marketplacePolicyPath },
  });
  let logs = "";
  child.stdout.on("data", (chunk) => { logs += chunk; });
  child.stderr.on("data", (chunk) => { logs += chunk; });
  try {
    await waitForServer(serverURL, { child });
    const response = await fetch(`${serverURL}/api/v1/query`, {
      method: "POST",
      headers: {
        authorization: ["Bearer", accessToken].join(" "),
        "content-type": "application/json",
      },
      body: JSON.stringify({ sourceNames: names, queries }),
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(`Postgres query failed (${response.status}): ${JSON.stringify(payload)}`);
    const goTools = await callGoMcp("tools/list");
    const nodeTools = await handleMcpRequest({
      headers: {
        "mcp-protocol-version": MCP_PROTOCOL_VERSION,
        "mcp-method": "tools/list",
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list", params: {} }),
      indexedDB: new IDBFactory(),
    });
    if (stableJSON(goTools.result.tools) !== stableJSON(nodeTools.body.result.tools)) {
      throw new Error(`MCP contract drift: ${JSON.stringify({
        node: nodeTools.body.result.tools,
        go: goTools.result.tools,
      })}`);
    }
    for (const testCase of [
      { name: "cao_catalog", arguments: { kind: "queries" } },
      { name: "cao_query", arguments: { id: "campaign-runs", limit: 1 } },
      {
        name: "cao_query",
        arguments: { id: "mcp-tool-calls", parameters: { tool: "github/search" }, limit: 2 },
      },
      { name: "cao_query", arguments: { id: "not-a-query" } },
      {
        name: "cao_query",
        arguments: { id: "campaign-runs", parameters: { unknown: "value" } },
      },
    ]) {
      const params = { name: testCase.name, arguments: testCase.arguments };
      const [nodeResult, goResult] = await Promise.all([
        callNodeMcp(
          agentFactory,
          testCase.name === "cao_catalog" ? dashboardPath : dashboardDocumentPath,
          "tools/call",
          params,
        ),
        callGoMcp("tools/call", params),
      ]);
      const expected = canonicalToolResult(nodeResult.result);
      const actual = canonicalToolResult(goResult.result);
      if (stableJSON(actual) !== stableJSON(expected)) {
        throw new Error(
          `MCP behavioral drift for ${JSON.stringify(testCase)}: `
          + JSON.stringify(firstDifference(expected, actual)),
        );
      }
    }
    const goMcpRows = {};
    const representativeNames = [
      "campaign-runs",
      "mcp-tool-calls",
      "runs-table",
      "workflow-inventory",
    ].filter((name) => localNames.includes(name));
    for (const name of representativeNames) {
      const result = await callGoMcp("tools/call", {
        name: "cao_query",
        arguments: { id: name, limit: MAX_NAMED_QUERY_LIMIT },
      });
      if (result.result?.isError) {
        throw new Error(`Go MCP cao_query failed for ${name}: ${JSON.stringify(result)}`);
      }
      goMcpRows[name] = result.result.structuredContent.rows;
    }
    return { postgres: rowsByQuery(payload.sources, names), goMcp: goMcpRows };
  } finally {
    child.kill("SIGTERM");
    await Promise.race([
      new Promise((resolvePromise) => child.once("exit", resolvePromise)),
      new Promise((resolvePromise) => setTimeout(resolvePromise, 5000)),
    ]);
    if (!child.killed) child.kill("SIGKILL");
    if (child.exitCode && child.exitCode !== 0) {
      process.stderr.write(logs);
    }
  }
}

function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.entries(value).sort(([left], [right]) =>
      left.localeCompare(right)
    ).map(([key, item]) => `${JSON.stringify(key)}:${stableJSON(item)}`).join(",")}}`;
  }
  return JSON.stringify(value) ?? "null";
}

function normalizedRows(rows, ordered) {
  // Timestamps denote the same instant with or without zero milliseconds.
  const normalized = JSON.parse(JSON.stringify(rows), (_key, value) =>
    typeof value === "string" ? value.replace(/(T\d{2}:\d{2}:\d{2})\.0+Z$/, "$1Z") : value
  );
  return ordered ? normalized : normalized.toSorted((left, right) =>
    stableJSON(left).localeCompare(stableJSON(right))
  );
}

export function normalizedParityRows(query, rows, ordered) {
  // Frame hashes bind backend-specific capacities and source quality. Compare
  // composition without requiring different sampling designs to share an ID.
  const normalized = normalizedRows(rows, ordered).map((row) => {
    if (!Object.hasOwn(row, "statistical-frame-id")) return row;
    if (!/^sha256:[a-f0-9]{64}$/.test(row["statistical-frame-id"])) {
      throw new Error("Statistical parity rows require a valid frame identity");
    }
    return { ...row, "statistical-frame-id": "backend-specific-frame" };
  });
  if (query?.from !== "transactions") return normalized;
  return normalized.map((row) => {
    const copy = { ...row };
    delete copy.id;
    delete copy["created-at"];
    delete copy["payload-hash"];
    return copy;
  });
}

function compareBackends(results, queries) {
  const baselineName = "node-indexeddb";
  const queryIndex = new Map(queries.map((query) => [query.name, query]));
  const mismatches = [];
  for (const [backend, rowsByName] of Object.entries(results)) {
    if (backend === baselineName) continue;
    for (const query of queries) {
      const { name } = query;
      if (!Object.hasOwn(results[baselineName], name)) continue;
      const baselineRows = results[baselineName][name];
      if (backend === "postgres" && query?.from === "transactions") continue;
      const ordered = (query?.["order-by"]?.length ?? 0) > 0;
      const expected = normalizedParityRows(query, baselineRows, ordered);
      const actual = normalizedParityRows(query, rowsByName[name] ?? [], ordered);
      if (stableJSON(actual) !== stableJSON(expected)) {
        mismatches.push({
          query: name,
          backend,
          semantics: ordered ? "ordered" : "unordered",
          expectedRows: expected.length,
          actualRows: actual.length,
          expected,
          actual,
        });
      }
    }
  }
  return mismatches;
}

async function main() {
  mkdirSync(reportDirectory, { recursive: true });
  const temporaryDirectory = mkdtempSync(join(tmpdir(), "dashboard-query-parity-"));
  const artifactDirectory = join(temporaryDirectory, "artifact");
  const sqlitePath = join(temporaryDirectory, "dashboard.sqlite");
  const sources = representativeSources();
  const queries = dashboardQueries();
  if (!queries.some((query) => query.window?.length)) {
    throw new Error("Dashboard query parity requires an observation-window query.");
  }
  const names = queries.map(({ name }) => name);
  const report = {
    generatedAt: new Date().toISOString(),
    queryCount: queries.length,
    backends: [],
    orderedQueries: queries.filter((query) => query["order-by"]?.length).length,
    unorderedQueries: queries.filter((query) => !query["order-by"]?.length).length,
    mismatches: [],
    status: "failed",
  };
  try {
    const nodeFactory = new IDBFactory();
    const nodeRows = await executeNodeBackend(nodeFactory, sources, queries, names);
    await mkdir(artifactDirectory, { recursive: true });
    await writeDeployedArtifact(artifactDirectory, nodeFactory, sources);
    const resolvedDocumentPath = join(temporaryDirectory, "resolved-dashboard.json");
    const { document: dashboardDocument } = loadDashboardSourceSync(dashboardPath);
    const parityQueries = new Map(queries.map((query) => [query.name, query]));
    const resolvedDocument = {
      ...dashboardDocument,
      dashboard: {
        ...dashboardDocument.dashboard,
        queries: dashboardDocument.dashboard.queries.map(
          (query) => parityQueries.get(query.name) ?? query,
        ),
      },
    };
    writeFileSync(resolvedDocumentPath, JSON.stringify(resolvedDocument));
    const localNames = names.filter(
      (name) => queryExecutionRequirements(resolvedDocument, name).local,
    );
    const localQueries = queries.filter(({ name }) => localNames.includes(name));
    const agentSqlitePath = join(temporaryDirectory, "agent.sqlite");
    const sqliteFactory = installSqliteIndexedDB(agentSqlitePath);
    await executeNodeBackend(sqliteFactory, sources, queries, names);
    const agentRows = await executeNamedQueryBackend(
      sqliteFactory,
      resolvedDocument,
      localNames,
    );
    const mcpRows = await executeMcpBackend(
      sqliteFactory,
      resolvedDocumentPath,
      localNames,
    );
    // Agent transports read only the local projection, so their baseline is the
    // same dashboard engine reading the database without deployed logical
    // sources.
    const databaseBaseline = rowsByQuery(
      await loadDatabaseQuerySources(nodeFactory, {}, {
        queries,
        sourceNames: localNames,
      }),
      localNames,
    );
    report.agentQueryCount = localNames.length;
    report.mismatches.push(...compareBackends(
      {
        "node-indexeddb": databaseBaseline,
        "cao-named-query": agentRows,
        "cao-mcp": mcpRows,
      },
      queries,
    ));
    const [browserRows, sqliteRows, postgresResult] = await Promise.all([
      executeBrowserBackend(sources, queries, names),
      executeNodeBackend(installSqliteIndexedDB(sqlitePath), sources, queries, names),
      executePostgresBackend(
        artifactDirectory,
        resolvedDocumentPath,
        sqliteFactory,
        queries,
        names,
        localNames,
      ),
    ]);
    const results = {
      "node-indexeddb": nodeRows,
      "playwright-indexeddb": browserRows,
      "sqlite-indexeddb": sqliteRows,
    };
    report.backends = [...Object.keys(results), "postgres", "cao-named-query", "cao-mcp"];
    // Server-only sources such as collection health have no local IndexedDB
    // equivalent and are therefore outside the cross-backend parity contract.
    report.mismatches.push(...compareBackends(results, localQueries));
    // The server executes canonical queries rather than the injected logical
    // inventory fixtures used by the browser comparison.
    report.mismatches.push(...compareBackends({
      "node-indexeddb": rowsByQuery(
        await loadDatabaseQuerySources(nodeFactory, {}, {
          queries,
          sourceNames: names,
        }),
        names,
      ),
      postgres: postgresResult.postgres,
    }, localQueries));
    report.mismatches.push(...compareBackends({
      "node-indexeddb": Object.fromEntries(
        Object.keys(postgresResult.goMcp).map((name) => [name, databaseBaseline[name]]),
      ),
      "go-mcp": postgresResult.goMcp,
    }, localQueries));
    report.backends.push("go-mcp");
    report.status = report.mismatches.length === 0 ? "passed" : "failed";
    if (report.mismatches.length > 0) {
      throw new Error(`${report.mismatches.length} dashboard query parity comparison(s) failed`);
    }
    process.stdout.write(
      `Dashboard query parity passed for ${queries.length} queries across ${report.backends.length} backends.\n`,
    );
  } catch (error) {
    report.error = error instanceof Error ? error.message : String(error);
    throw error;
  } finally {
    writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main();
}
