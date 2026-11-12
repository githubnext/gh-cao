import { createHash, randomBytes } from "node:crypto";
import { execFile } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { CANONICAL_SCHEMA_VERSION } from "../../dashboard/site/src/data/model/schema.js";
import { NORMALIZED_JSONL_INGESTION_VERSION } from "../../dashboard/site/src/data/ingest/coordinator.js";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";
import { startCanvasServer as startPreparedCanvasServer } from "../../dashboard/canvas-server.mjs";

const execute = promisify(execFile);

export async function prepareCanvasPostgres() {
  if (!process.env.POSTGRES_URL) throw new Error("Canvas tests require an explicitly configured disposable POSTGRES_URL.");
  const endpoint = new URL(process.env.POSTGRES_URL);
  if (!["127.0.0.1", "[::1]"].includes(endpoint.hostname)) throw new Error("Canvas tests require numeric-loopback PostgreSQL.");
  const schema = "canvas_test_" + randomBytes(10).toString("hex");
  const namespace = "canvas-test";
  const binary = process.env.CAO_DASHBOARD_EXECUTABLE ?? resolve(".tmp/cao-dashboard");
  const psql = async (sql) => execute("psql", ["-X", "--dbname", process.env.POSTGRES_URL,
    "-v", "ON_ERROR_STOP=1", "-At", "-c", sql], { maxBuffer: 1 << 20 });
  await psql("CREATE SCHEMA " + schema);
  let closed = false;
  const close = async () => {
    if (closed) return;
    closed = true;
    const parents = (await psql("SELECT quote_ident(c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='"
      + schema + "' AND c.relkind='p'")).stdout.trim().split("\n").filter(Boolean);
    for (const name of parents) await psql("DROP TABLE " + schema + "." + name + " CASCADE");
    await psql("DROP SCHEMA " + schema + " CASCADE");
  };
  endpoint.searchParams.set("search_path", schema);
  try {
    for (const store of ["canonical", "operational"]) {
      await execute(binary, ["database", "bootstrap", "--store", store, "--namespace", namespace], {
        env: { ...process.env, CAO_DATABASE_ADMIN_URL: endpoint.href, OTEL_SDK_DISABLED: "true" },
        timeout: 120_000, maxBuffer: 1 << 20,
      });
    }
    return { postgresURL: endpoint.href, namespace, close };
  } catch (error) {
    await close();
    throw error;
  }
}

/**
 * @param {NonNullable<Parameters<typeof startPreparedCanvasServer>[0]> & {
 *   prepareSnapshot?: (destination: string, repository?: string, ghExecutable?: string, workingDirectory?: string) => Promise<unknown>
 * }} options
 */
export async function startCanvasServer({ prepareSnapshot, ...options } = {}) {
  if (Object.hasOwn(options, "cliQueuePath")) return startPreparedCanvasServer(options);
  // An unconfigured fixture must not consult the developer's GitHub credentials.
  options.ghExecutable ??= process.execPath;
  const directory = prepareSnapshot
    ? await mkdtemp(join(options.workingDirectory ?? process.cwd(), ".cao-fixture-"))
    : null;
  let postgres;
  try {
    if (prepareSnapshot && directory) {
      const prepared = await prepareSnapshot(directory, options.repository, options.ghExecutable, options.workingDirectory);
      if (prepared && typeof prepared === "object" && "repository" in prepared && typeof prepared.repository === "string") {
        options.repository ??= prepared.repository;
      }
    }
    if (!options.postgresURL) postgres = await prepareCanvasPostgres();
    const preview = await startPreparedCanvasServer({ ...options,
      ...(postgres ? { postgresURL: postgres.postgresURL, namespace: postgres.namespace } : {}),
      ...(directory ? { snapshotDirectory: directory } : {}) });
    return {
      ...preview,
      async close() {
        try { await preview.close(); }
        finally {
          await postgres?.close();
          if (directory) await rm(directory, { recursive: true, force: true });
        }
      },
    };
  } catch (error) {
    await postgres?.close();
    if (directory) await rm(directory, { recursive: true, force: true });
    throw error;
  }
}

// Artifact fixtures use canonical records, not the removed logical-source HTTP transport.
export async function writeCanvasSnapshot(destination, records = {}) {
  await mkdir(destination, { recursive: true });
  const hashes = {};
  for (const phase of ["runs", "records"]) {
    const rows = Object.entries({ ...normalize([]), ...records })
      .filter(([collection]) => ["campaigns", "repositories", "workflows", "runs", "experiments", "experimentAssignments"].includes(collection) === (phase === "runs"))
      .flatMap(([collection, values]) => values.map((record) => ({ kind: "record", collection, record })));
    const name = "gh-aw-logs-" + phase + "/canonical.jsonl";
    await mkdir(join(destination, "gh-aw-logs-" + phase), { recursive: true });
    const content = [
      { kind: "metadata", schemaVersion: CANONICAL_SCHEMA_VERSION, ingestionVersion: NORMALIZED_JSONL_INGESTION_VERSION, phase, records: rows.length },
      ...rows,
    ].map((row) => JSON.stringify(row)).join("\n") + "\n";
    await writeFile(join(destination, name), content);
    hashes[name] = createHash("sha256").update(content).digest("hex");
  }
  await mkdir(join(destination, "gh-aw-logs-shards"), { recursive: true });
  await writeFile(join(destination, "inventory-sources.json"), "{}");
  hashes["inventory-sources.json"] = createHash("sha256").update("{}").digest("hex");
  await writeFile(join(destination, "payload-hashes.json"), JSON.stringify(hashes));
  return join(destination, "gh-aw-logs-runs", "canonical.jsonl");
}
