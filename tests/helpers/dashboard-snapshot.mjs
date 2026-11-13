import { createHash, randomBytes } from "node:crypto";
import { execFile } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { CANONICAL_SCHEMA_VERSION } from "../../dashboard/site/src/data/model/schema.js";
import { NORMALIZED_JSONL_INGESTION_VERSION } from "../../dashboard/site/src/data/ingest/coordinator.js";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";

const execute = promisify(execFile);

function dashboardPostgresEndpoint(value) {
  if (!value) throw new Error("Dashboard tests require an explicitly configured disposable POSTGRES_URL.");
  const endpoint = new URL(value);
  if (!["127.0.0.1", "[::1]"].includes(endpoint.hostname)) throw new Error("Dashboard tests require numeric-loopback PostgreSQL.");
  return endpoint;
}

const postgresCommand = (endpoint, sql) => execute("psql", ["-X", "--dbname", endpoint,
  "-v", "ON_ERROR_STOP=1", "-At", "-c", sql], { timeout: 120_000, maxBuffer: 1 << 20 });

export async function prepareDashboardPostgresTemplate(endpoint = process.env.POSTGRES_URL) {
  const target = dashboardPostgresEndpoint(endpoint);
  const database = "dashboard_template_" + randomBytes(10).toString("hex");
  const close = () => postgresCommand(endpoint, "DROP DATABASE IF EXISTS " + database + " WITH (FORCE)");
  await postgresCommand(endpoint, "CREATE DATABASE " + database);
  target.pathname = "/" + database;
  try {
    const fixture = await prepareDashboardPostgres({ endpoint: target.href, template: null });
    await postgresCommand(endpoint, "ALTER DATABASE " + database + " ALLOW_CONNECTIONS false");
    return { postgresURL: fixture.postgresURL, close };
  } catch (error) {
    await close();
    throw error;
  }
}

export async function prepareDashboardPostgres({
  endpoint: configuredEndpoint = process.env.POSTGRES_URL,
  template = process.env.CAO_TEST_POSTGRES_TEMPLATE,
} = {}) {
  const endpoint = dashboardPostgresEndpoint(configuredEndpoint);
  if (template) {
    const source = dashboardPostgresEndpoint(template);
    if (source.host !== endpoint.host || source.username !== endpoint.username
        || source.password !== endpoint.password || !/^\/dashboard_template_[a-f0-9]{20}$/.test(source.pathname)
        || !/^dashboard_test_[a-f0-9]{20}$/.test(source.searchParams.get("search_path") ?? "")) {
      throw new Error("Dashboard template must belong to the configured PostgreSQL test server.");
    }
    const database = "dashboard_clone_" + randomBytes(10).toString("hex");
    await postgresCommand(configuredEndpoint, "CREATE DATABASE " + database + " TEMPLATE " + source.pathname.slice(1));
    source.pathname = "/" + database;
    return {
      postgresURL: source.href, namespace: "dashboard-test",
      close: () => postgresCommand(configuredEndpoint, "DROP DATABASE IF EXISTS " + database + " WITH (FORCE)"),
    };
  }
  const schema = "dashboard_test_" + randomBytes(10).toString("hex");
  const namespace = "dashboard-test";
  const binary = process.env.CAO_DASHBOARD_EXECUTABLE ?? resolve(".tmp/cao-dashboard");
  const psql = (sql) => postgresCommand(configuredEndpoint, sql);
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

// Artifact fixtures use canonical records, not the removed logical-source HTTP transport.
export async function writeDashboardSnapshot(destination, records = {}) {
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
