#!/usr/bin/env node
// Container end-to-end test for the read-only CAO MCP server.
//
// Builds the MCP image, mounts a fixture SQLite snapshot read-only, serves MCP
// over plain HTTP on a job-local published port, and verifies that the snapshot
// and the container filesystem stay unchanged.
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";

import { installSqliteIndexedDB } from "../../dashboard/site/src/data/storage/sqlite-indexeddb.js";
import { readCollections } from "../../dashboard/site/src/data/storage/indexeddb.js";
import { MCP_PROTOCOL_VERSION } from "../../activity/mcp-server.mjs";

const run = promisify(execFile);
const repositoryRoot = path.resolve(import.meta.dirname, "../..");
const engine = process.env.CAO_CONTAINER_ENGINE ?? "docker";
if (!["docker", "podman"].includes(engine)) {
  throw new Error("CAO_CONTAINER_ENGINE must be docker or podman");
}
const image = "cao-mcp-e2e";
const container = "cao-mcp-e2e";
const port = Number(process.env.CAO_MCP_E2E_PORT ?? 18765);

/**
 * @param {string} url
 * @param {Record<string, unknown>} message
 */
function post(url, message) {
  const body = JSON.stringify(message);
  return new Promise((resolve, reject) => {
    const request = http.request(url, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "content-length": Buffer.byteLength(body),
        "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
        "Mcp-Method": String(message.method),
        ...(message.params?.name ? { "Mcp-Name": String(message.params.name) } : {}),
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

async function waitForHealth() {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      const healthy = await new Promise((resolve, reject) => {
        http.get(`http://127.0.0.1:${port}/healthz`, (response) => {
          response.resume();
          resolve(response.statusCode === 200);
        }).on("error", reject);
      });
      if (healthy) return;
    } catch {
      // The container is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error("CAO MCP container did not become healthy");
}

async function main() {
  try {
    await run(engine, ["info"]);
  } catch {
    process.stdout.write(`Skipping CAO MCP container test: ${engine} is unavailable.\n`);
    return;
  }
  const directory = await mkdtemp(path.join(tmpdir(), "cao-mcp-container-"));
  const dataDirectory = path.join(directory, "data");
  const snapshot = path.join(dataDirectory, "gh-aw-logs.sqlite");
  await mkdir(dataDirectory, { recursive: true });
  const fixture = await installSqliteIndexedDB(snapshot);
  await readCollections(fixture, ["runs"]);
  await run("chmod", ["-R", "a+rX", directory]);
  const digestBefore = createHash("sha256")
    .update(await readFile(snapshot))
    .digest("hex");
  await run(engine, [
    "build", "-f", "Dockerfile.mcp", "-t", image, ".",
  ], { cwd: repositoryRoot, maxBuffer: 64 * 1024 * 1024 });
  await run(engine, ["rm", "-f", container]).catch(() => {});
  await run(engine, [
    "run", "--detach", "--name", container,
    "--read-only", "--tmpfs", "/tmp",
    "--volume", `${dataDirectory}:/data:ro`,
    "--publish", `127.0.0.1:${port}:8765`,
    image,
  ]);
  try {
    await waitForHealth();
    const url = `http://127.0.0.1:${port}/mcp`;
    const tools = await post(url, {
      jsonrpc: "2.0",
      id: 1,
      method: "tools/list",
    });
    const names = tools.payload.result.tools.map((tool) => tool.name);
    if (names.join(",") !== "cao_catalog,cao_query") {
      throw new Error(`Unexpected MCP tools: ${names.join(", ")}`);
    }
    const catalog = await post(url, {
      jsonrpc: "2.0",
      id: 2,
      method: "tools/call",
      params: { name: "cao_catalog", arguments: { kind: "queries" } },
    });
    const queries = catalog.payload.result.structuredContent.queries;
    const executable = queries.find((query) => query.execution.local);
    if (!executable) throw new Error("No locally executable query was discovered");
    const result = await post(url, {
      jsonrpc: "2.0",
      id: 3,
      method: "tools/call",
      params: { name: "cao_query", arguments: { id: executable.id, limit: 5 } },
    });
    const payload = result.payload.result.structuredContent;
    if (payload.query !== executable.id || !Array.isArray(payload.rows)) {
      throw new Error(`Unexpected cao_query result: ${JSON.stringify(payload)}`);
    }
    if (!payload.metadata?.availability) {
      throw new Error("cao_query result is missing availability metadata");
    }
    const { stdout: identity } = await run(engine, ["exec", container, "id", "-u"]);
    if (identity.trim() === "0") throw new Error("The container runs as root");
    const write = await run(engine, [
      "exec", container, "sh", "-c", "touch /app/write-probe",
    ]).then(() => "written").catch(() => "read-only");
    if (write !== "read-only") throw new Error("The container filesystem is writable");
    const digestAfter = createHash("sha256")
      .update(await readFile(snapshot))
      .digest("hex");
    if (digestBefore !== digestAfter) throw new Error("The mounted snapshot changed");
    process.stdout.write(
      `CAO MCP container served ${queries.length} catalogued queries over HTTP without modifying its snapshot.\n`,
    );
  } finally {
    await run(engine, ["rm", "-f", container]).catch(() => {});
    await rm(directory, { recursive: true, force: true });
  }
}

await main();
