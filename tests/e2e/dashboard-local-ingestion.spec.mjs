import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { DatabaseSync } from "node:sqlite";
import { DATABASE_VERSION } from "../../dashboard/site/src/data/storage/indexeddb.js";

const databaseName = "gh-aw-cao-dashboard-data";
const accessToken = "0123456789abcdef0123456789abcdef";
const site = resolve("dashboard/site/dist");
let directory, database, child, url;

function put(store, row) {
  database.prepare(`INSERT INTO __idb_records VALUES(?,?,?,?)
    ON CONFLICT(database_name,store_name,record_key) DO UPDATE SET value=excluded.value`)
    .run(databaseName, store, JSON.stringify(row.id), JSON.stringify(row));
}

function status(ingestionStatus = "paused", scannedHours = 48) {
  put("transactions", {
    id: "local-dashboard-ingestion", kind: "local-dashboard-ingestion",
    createdAt: new Date().toISOString(), ingestionStatus, scannedHours, targetHours: 168,
    ingestionRepository: "octo/api", apiRemaining: 999, apiReserve: 1000,
    apiResetAt: new Date(Date.now() + 3_600_000).toISOString(),
    nextAttemptAt: new Date(Date.now() + 3_600_000).toISOString(),
    ingestionMessage: "Synthetic local ingestion fixture. History and artifact coverage remain partial.",
  });
}

test.beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "cao-local-ingestion-"));
  const path = join(directory, "gh-aw-logs.sqlite");
  database = new DatabaseSync(path);
  database.exec(`
    PRAGMA busy_timeout=5000;
    CREATE TABLE __idb_databases(name TEXT PRIMARY KEY, version INTEGER NOT NULL);
    CREATE TABLE __idb_stores(database_name TEXT,name TEXT,key_path TEXT,PRIMARY KEY(database_name,name));
    CREATE TABLE __idb_indexes(database_name TEXT,store_name TEXT,name TEXT,key_path TEXT,PRIMARY KEY(database_name,store_name,name));
    CREATE TABLE __idb_records(database_name TEXT,store_name TEXT,record_key TEXT,value TEXT,PRIMARY KEY(database_name,store_name,record_key));
  `);
  database.prepare("INSERT INTO __idb_databases VALUES(?,?)").run(databaseName, DATABASE_VERSION);
  for (const store of ["repositories", "workflows", "runs", "transactions"]) {
    database.prepare("INSERT INTO __idb_stores VALUES(?,?,'\"id\"')").run(databaseName, store);
  }
  const now = new Date().toISOString();
  put("repositories", { id: "repository:octo%2Fapi", owner: "octo", name: "api", observedAt: now });
  put("workflows", {
    id: "workflow:agent", repositoryId: "repository:octo%2Fapi",
    path: ".github/workflows/agent.md", name: "Agent", state: "active", observedAt: now,
  });
  put("runs", {
    id: "github:run:octo/api:123", githubRunId: "123", attempt: 1,
    repositoryId: "repository:octo%2Fapi", workflowId: "workflow:agent",
    owner: "octo", repository: "api", repositoryFullName: "octo/api",
    workflowPath: ".github/workflows/agent.md", status: "completed", conclusion: "success",
    createdAt: now, startedAt: now, updatedAt: now, observedAt: now,
  });
  put("transactions", {
    id: "local-dashboard", kind: "local-dashboard", createdAt: now, windowEnd: now,
    windowStart: new Date(Date.now() - 7 * 86_400_000).toISOString(),
    repositories: ["octo/api"], completeness: "partial", coverage: "Explicit synthetic local-server test fixture.",
  });
  status();
  const environment = Object.fromEntries(Object.entries(process.env)
    .filter(([name]) => !/^(?:CAO_|DASHBOARD_|GH_|GITHUB_|OTEL_)/.test(name)));
  child = spawn(resolve(".tmp/cao-dashboard"), [
    "serve", "--sqlite-ingestion", "--sqlite", path, "--site", site,
    "--listen", "127.0.0.1:0", "--access-token", accessToken, "--exit-on-stdin-close",
    "--database-queries", join(site, "src/data/queries/database.json"),
    "--dashboard-queries", join(site, "src/agent/queries.generated.json"),
    "--agent-catalog", join(site, "src/agent/catalog.generated.json"),
    "--mcp-contract", join(site, "src/agent/mcp-contract.json"),
  ], { env: { ...environment, OTEL_SDK_DISABLED: "true" }, stdio: ["pipe", "pipe", "pipe"] });
  let output = "";
  child.stdout.on("data", (chunk) => { output += chunk; });
  child.stderr.on("data", (chunk) => { output += chunk; });
  await expect.poll(() => {
    const address = output.match(/http:\/\/127\.0\.0\.1:\d+/)?.[0];
    if (address) url = address;
    if (child.exitCode !== null) throw new Error(`Local ingestion server exited: ${output}`);
    return url;
  }).toBeTruthy();
  const health = await fetch(`${url}/api/v1/health`, { headers: { Authorization: `Bearer ${accessToken}` } });
  expect(health.ok).toBe(true);
});

test.afterAll(async () => {
  if (child?.exitCode === null) {
    const exited = once(child, "exit");
    child.stdin.end();
    await exited;
  }
  database?.close();
  if (directory) await rm(directory, { recursive: true, force: true });
});

for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
  test(`local ingestion status refreshes without navigation at ${viewport.width}px`, async ({ page }, testInfo) => {
    status();
    await page.setViewportSize(viewport);
    await page.goto(`${url}/?access_token=${accessToken}#page-overview`);
    await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveAttribute("content", "server-http");
    await expect(page.locator('meta[name="dashboard-storage-backend"]')).toHaveAttribute("content", "sqlite");
    const view = page.locator('[data-view-id="local-ingestion-progress"]');
    await expect(async () => {
      await view.scrollIntoViewIfNeeded();
      await expect(view).toContainText("paused");
    }).toPass({ timeout: 15_000 });
    await expect(view).toContainText("168");
    await expect(view).toContainText("999");
    const initialUrl = page.url();
    status("collecting", 72);
    await expect(view).toContainText("collecting", { timeout: 45_000 });
    await expect(view).toContainText("72");
    expect(page.url()).toBe(initialUrl);
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const screenshot = testInfo.outputPath(`local-ingestion-${viewport.width}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach("Populated local SQLite dashboard (synthetic fixture)", {
      body: await readFile(screenshot), contentType: "image/png",
    });
  });
}
