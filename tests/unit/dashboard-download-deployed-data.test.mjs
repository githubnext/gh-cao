import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { copyFile, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import test from "node:test";
import { downloadDeployedDashboardData } from "../../activity/cao.mjs";

const executeFile = promisify(execFile);
function executeFileWithInput(file, arguments_, input, options = {}) {
  return new Promise((resolve, reject) => {
    const child = execFile(file, arguments_, options, (error, stdout, stderr) => {
      if (error) reject(error);
      else resolve({ stdout, stderr });
    });
    child.stdin.end(input);
  });
}

const campaignJson = JSON.parse(
  await readFile(new URL("../../package.json", import.meta.url), "utf8"),
);
const cao = path.resolve(campaignJson.bin.cao);

test("exposes the dashboard data CLI as cao", async () => {
  assert.equal(campaignJson.bin.cao, "activity/cao.mjs");
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-cli-"));
  const installedCommand = path.join(root, "cao");
  try {
    await symlink(cao, installedCommand);
    const { stdout } = await executeFile(installedCommand, ["help"]);
    assert.match(stdout, /^Usage:\n  cao discover-workflows\b/);
    assert.match(stdout, /\n  cao ingest /);
    assert.doesNotMatch(stdout, /\n  cao (?:init|add)\b/);
    assert.match(stdout, /gh cao --help; workflow and campaign installation is gh cao add SOURCE/);
    assert.match(stdout, /\n  cao discover-workflows /);
    assert.match(stdout, /\n  cao download /);
    assert.match(stdout, /cao query .*--stdin/);
    assert.match(stdout, /cao gh runs /);
    assert.match(stdout, /cao gh issues /);
    assert.match(stdout, /cao gh prs /);
    assert.match(stdout, /Import a selected CAO Activity snapshot before querying/);
    assert.match(stdout, /gh run download RUN_ID --repo OWNER\/REPO --name cao-activity-index --dir .cao/);
    assert.match(stdout, /Explicit --url or DASHBOARD_DATA_URL identifying payload-hashes.json \(no default\)/);
    assert.doesNotMatch(stdout, /githubnext\.github\.io\/gh-cao\/cao\/payload-hashes\.json/);
    assert.match(stdout, /execution repo for runs, target repo for issues and prs/);
    assert.match(stdout, /-s, --status\s+Runs only/);
    assert.match(stdout, /cao ingest-jsonl --input-dir SHARD_DIRECTORY/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("download requires an explicit manifest before network or cache changes", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-explicit-download-"));
  const previousURL = process.env.DASHBOARD_DATA_URL;
  delete process.env.DASHBOARD_DATA_URL;
  t.after(async () => {
    if (previousURL === undefined) delete process.env.DASHBOARD_DATA_URL;
    else process.env.DASHBOARD_DATA_URL = previousURL;
    await rm(root, { recursive: true, force: true });
  });
  t.mock.method(globalThis, "fetch", () => assert.fail("unconfigured sources must never use the network"));
  const output = path.join(root, "cache");
  await assert.rejects(downloadDeployedDashboardData({ output }), /explicit Activity snapshot manifest URL is required/);
  assert.deepEqual(await readdir(root), []);
  await mkdir(output);
  const database = path.join(output, "gh-aw-logs.sqlite");
  await writeFile(database, "existing cache");
  await assert.rejects(downloadDeployedDashboardData({ output }), /gh run download RUN_ID .*--name cao-activity-index --dir .cao/);
  for (const url of ["https://snapshot.example/", "https://snapshot.example/cao/", "https://snapshot.example/index.html"]) {
    await assert.rejects(downloadDeployedDashboardData({ output, url }), /must identify payload-hashes.json/);
  }
  assert.equal(await readFile(database, "utf8"), "existing cache");
  assert.deepEqual(await readdir(output), ["gh-aw-logs.sqlite"]);
  await assert.rejects(
    executeFile(process.execPath, [cao, "download", "--output", output], {
      env: { ...process.env, DASHBOARD_DATA_URL: "" },
    }),
    /explicit Activity snapshot manifest URL is required/,
  );
});

test("download rejects synthetic and SQLite-free manifests without fetching payloads", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-demo-rejected-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const database = path.join(root, "gh-aw-logs.sqlite");
  await writeFile(database, "existing cache");
  for (const [manifest, error] of [
    [{ synthetic: true, "gh-aw-logs.sqlite": "a".repeat(64) }, /Synthetic Pages demo/],
    [{ "gh-aw-logs-runs/demo.jsonl": "a".repeat(64) }, /no valid SQLite checksum/],
  ]) {
    const fetch = t.mock.method(globalThis, "fetch", async (url) => {
      assert.equal(String(url), "https://snapshot.example/payload-hashes.json");
      return new Response(JSON.stringify(manifest));
    });
    await assert.rejects(downloadDeployedDashboardData({
      output: root, url: "https://snapshot.example/payload-hashes.json",
    }), error);
    assert.equal(fetch.mock.callCount(), 1);
    fetch.mock.restore();
    assert.equal(await readFile(database, "utf8"), "existing cache");
    assert.deepEqual(await readdir(root), ["gh-aw-logs.sqlite"]);
  }
});

test("queries canonical data with the gh-like surface", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-gh-cli-"));
  const inputDirectory = path.join(root, "gh-aw-logs-shards");
  const input = path.join(inputDirectory, "gh-aw-logs.jsonl");
  const database = path.join(root, "gh-aw-logs.sqlite");
  const records = [
    {
      schema_version: 2,
      kind: "workflow_runs",
      request: { host: "github.com", repository: "githubnext/gh-cao", args: ["run", "list"] },
      payload: [{
        databaseId: 404,
        attempt: 1,
        workflowName: "CAO Activity",
        displayTitle: "Collect activity",
        event: "workflow_dispatch",
        status: "completed",
        conclusion: "success",
        createdAt: "2026-09-10T03:59:00Z",
        startedAt: "2026-09-10T04:00:00Z",
        updatedAt: "2026-09-10T04:01:00Z",
      }],
    },
    {
      schema_version: 2,
      kind: "run",
      run: {
        run_id: 404,
        run_attempt: "1",
        organization: "githubnext",
        repository: "githubnext/gh-cao",
        workflow_name: "CAO Activity",
        workflow_path: ".github/workflows/cao-activity.md",
        status: "completed",
        conclusion: "success",
        created_at: "2026-09-10T03:59:00Z",
        started_at: "2026-09-10T04:00:00Z",
        updated_at: "2026-09-10T04:01:00Z",
      },
    },
    {
      schema_version: 2,
      kind: "safe_output_item",
      safe_output: {
        run_id: 404,
        type: "create_issue",
        url: "https://github.com/octo/example/issues/12",
        timestamp: "2026-09-10T04:00:30Z",
      },
    },
    {
      schema_version: 2,
      kind: "safe_output_item",
      safe_output: {
        run_id: 404,
        type: "create_pull_request",
        url: "https://github.com/octo/example/pull/13",
        timestamp: "2026-09-10T04:00:45Z",
      },
    },
    {
      schema_version: 2,
      kind: "safe_output_item",
      safe_output: {
        run_id: 404,
        type: "add_issue_comment",
        url: "https://github.com/octo/example/issues/12",
        timestamp: "2026-09-10T04:00:55Z",
      },
    },
  ];

  try {
    await mkdir(inputDirectory, { recursive: true });
    await writeFile(input, `${records.map((record) => JSON.stringify(record)).join("\n")}\n`);
    await executeFile(cao, ["ingest-jsonl", "--input-dir", inputDirectory, "--database", database, "--retention-days", "all"]);

    const { stdout: runsOutput } = await executeFile(cao, [
      "gh", "runs",
      "--database", database,
      "-R", "githubnext/gh-cao",
      "-w", "cao-activity",
      "-s", "success",
      "--since", "2026-09-10",
      "--until", "2026-09-10",
      "-L", "1",
    ]);
    const runs = JSON.parse(runsOutput);
    assert.equal(runs.length, 1);
    assert.equal(runs[0].githubRunId, "404");

    const { stdout: failedRunsOutput } = await executeFile(cao, [
      "gh", "runs",
      "--database", database,
      "--status", "failure",
    ]);
    assert.deepEqual(JSON.parse(failedRunsOutput), []);

    const { stdout: issuesOutput } = await executeFile(cao, [
      "gh", "issues",
      "--database", database,
      "--repo", "octo/example",
      "--workflow", "CAO Activity",
      "--since", "2026-09-10T04:00:00Z",
      "--until", "2026-09-10T04:01:00Z",
    ]);
    assert.deepEqual(JSON.parse(issuesOutput).map(({ number, repository }) => ({ number, repository })), [
      { number: 12, repository: "octo/example" },
    ]);

    const { stdout: prsOutput } = await executeFile(cao, [
      "gh", "prs",
      "--database", database,
      "--repo", "octo/example",
      "--workflow", ".github/workflows/cao-activity.md",
    ]);
    assert.deepEqual(JSON.parse(prsOutput).map(({ number, repository }) => ({ number, repository })), [
      { number: 13, repository: "octo/example" },
    ]);

    const { stdout: outsideRangeOutput } = await executeFile(cao, [
      "gh", "runs",
      "--database", database,
      "--since", "2026-09-11",
    ]);
    assert.deepEqual(JSON.parse(outsideRangeOutput), []);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("rejects combining ingest-jsonl input modes", async () => {
  await assert.rejects(
    executeFile(cao, ["ingest-jsonl", "--input", "input.jsonl", "--input-dir", "shards"]),
    /--input and --input-dir cannot be combined/,
  );
});

test("downloads the deployed compacted activity shards and SQLite file without rebuilding", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "deployed-dashboard-data-"));
  const output = path.join(root, "activity");
  const runsContent = '{"kind":"metadata","phase":"runs","records":0}\n';
  const recordsContent = '{"kind":"metadata","phase":"records","records":0}\n';
  const databaseContent = Buffer.from("published sqlite bytes");
  const manifest = JSON.stringify({
    "gh-aw-logs.sqlite": createHash("sha256").update(databaseContent).digest("hex"),
    "gh-aw-logs-runs/fixture.jsonl": createHash("sha256").update(runsContent).digest("hex"),
    "gh-aw-logs-records/fixture.jsonl": createHash("sha256").update(recordsContent).digest("hex"),
  });
  const inventoryContent = JSON.stringify({ campaigns: { rows: [] } });
  const requests = [];
  const server = createServer((request, response) => {
    requests.push(request.url);
    response.writeHead(200, { "content-type": "application/x-ndjson" });
    response.end(request.url?.endsWith(".sqlite")
      ? databaseContent
      : request.url?.endsWith("inventory-sources.json") ? inventoryContent
      : request.url?.endsWith("payload-hashes.json") ? manifest
      : request.url?.includes("/gh-aw-logs-runs/") ? runsContent : recordsContent);
  });

  test("stages default .cao evidence through Node without operator tooling on PATH", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "deployed-dashboard-default-output-"));
    const logsContent = '{"schema_version":2,"kind":"run","run":{"run_id":404}}\n';
    const databaseContent = Buffer.from("default sqlite bytes");
    const manifest = JSON.stringify({
      "gh-aw-logs.sqlite": createHash("sha256").update(databaseContent).digest("hex"),
      "gh-aw-logs-shards/fixture.jsonl": createHash("sha256").update(logsContent).digest("hex"),
    });
    const inventoryContent = JSON.stringify({ campaigns: { rows: [] } });
    const server = createServer((request, response) => {
      response.writeHead(200, { "content-type": "application/x-ndjson" });
      response.end(request.url?.endsWith(".sqlite")
        ? databaseContent
        : request.url?.endsWith("inventory-sources.json") ? inventoryContent
        : request.url?.endsWith("payload-hashes.json") ? manifest : logsContent);
    });
    await new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });

    try {
      const address = server.address();
      assert.ok(address && typeof address === "object");
      await executeFile(process.execPath, [cao, "download"], {
        cwd: root,
        env: {
          ...process.env,
          PATH: "",
          Path: "",
          DASHBOARD_DATA_URL: `http://127.0.0.1:${address.port}/cao/payload-hashes.json`,
        },
      });
      assert.equal(await readFile(path.join(root, ".cao", "gh-aw-logs-shards", "fixture.jsonl"), "utf8"), logsContent);
      assert.deepEqual(await readFile(path.join(root, ".cao", "gh-aw-logs.sqlite")), databaseContent);
      assert.equal(await readFile(path.join(root, ".cao", "inventory-sources.json"), "utf8"), inventoryContent);
    } finally {
      await new Promise((resolve) => server.close(resolve));
      await rm(root, { recursive: true, force: true });
    }
  });

  test("rejects deployed SQLite files that do not match the manifest checksum", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "deployed-dashboard-sqlite-checksum-"));
    const output = path.join(root, "activity");
    const runsContent = '{"kind":"metadata","phase":"runs","records":0}\n';
    const databaseContent = Buffer.from("published sqlite bytes");
    const inventoryContent = JSON.stringify({ campaigns: { rows: [] } });
    const manifest = JSON.stringify({
      "gh-aw-logs.sqlite": createHash("sha256").update("different sqlite bytes").digest("hex"),
      "gh-aw-logs-runs/fixture.jsonl": createHash("sha256").update(runsContent).digest("hex"),
    });
    const server = createServer((request, response) => {
      response.writeHead(200, { "content-type": "application/x-ndjson" });
      response.end(request.url?.endsWith(".sqlite")
        ? databaseContent
        : request.url?.endsWith("inventory-sources.json") ? inventoryContent
        : request.url?.endsWith("payload-hashes.json") ? manifest : runsContent);
    });
    await new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });

    try {
      const address = server.address();
      assert.ok(address && typeof address === "object");
      await assert.rejects(
        executeFile(cao, [
          "download",
          "--url",
          `http://127.0.0.1:${address.port}/cao/payload-hashes.json`,
          "--output",
          output,
        ]),
        /Activity SQLite checksum mismatch: gh-aw-logs\.sqlite/,
      );
    } finally {
      await new Promise((resolve) => server.close(resolve));
      await rm(root, { recursive: true, force: true });
    }
  });

  test("retries transient deployed SQLite checksum mismatches", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "deployed-dashboard-sqlite-retry-"));
    const output = path.join(root, "activity");
    const runsContent = '{"kind":"metadata","phase":"runs","records":0}\n';
    const databaseContent = Buffer.from("published sqlite bytes");
    const inventoryContent = JSON.stringify({ campaigns: { rows: [] } });
    const goodManifest = JSON.stringify({
      "gh-aw-logs.sqlite": createHash("sha256").update(databaseContent).digest("hex"),
      "gh-aw-logs-runs/fixture.jsonl": createHash("sha256").update(runsContent).digest("hex"),
    });
    const staleManifest = JSON.stringify({
      "gh-aw-logs.sqlite": createHash("sha256").update("previous sqlite bytes").digest("hex"),
      "gh-aw-logs-runs/fixture.jsonl": createHash("sha256").update(runsContent).digest("hex"),
    });
    let manifestRequests = 0;
    const server = createServer((request, response) => {
      response.writeHead(200, { "content-type": "application/x-ndjson" });
      if (request.url?.endsWith(".sqlite")) response.end(databaseContent);
      else if (request.url?.endsWith("inventory-sources.json")) response.end(inventoryContent);
      else if (request.url?.endsWith("payload-hashes.json")) {
        manifestRequests += 1;
        response.end(manifestRequests === 1 ? staleManifest : goodManifest);
      } else {
        response.end(runsContent);
      }
    });
    await new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });

    try {
      const address = server.address();
      assert.ok(address && typeof address === "object");
      await executeFile(cao, [
        "download",
        "--url",
        `http://127.0.0.1:${address.port}/cao/payload-hashes.json`,
        "--output",
        output,
      ]);
      assert.equal(manifestRequests, 2);
      assert.deepEqual(await readFile(path.join(output, "gh-aw-logs.sqlite")), databaseContent);
      assert.equal(await readFile(path.join(output, "gh-aw-logs-runs", "fixture.jsonl"), "utf8"), runsContent);
    } finally {
      await new Promise((resolve) => server.close(resolve));
      await rm(root, { recursive: true, force: true });
    }
  });

  test("retries transient deployed shard HTTP and transport failures", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "deployed-dashboard-http-retry-"));
    const output = path.join(root, "activity");
    const runsContent = '{"kind":"metadata","phase":"runs","records":0}\n';
    const databaseContent = Buffer.from("published sqlite bytes");
    const inventoryContent = JSON.stringify({ campaigns: { rows: [] } });
    const manifest = JSON.stringify({
      "gh-aw-logs.sqlite": createHash("sha256").update(databaseContent).digest("hex"),
      "gh-aw-logs-runs/fixture.jsonl": createHash("sha256").update(runsContent).digest("hex"),
    });
    let shardRequests = 0;
    const server = createServer((request, response) => {
      if (request.url?.includes("/gh-aw-logs-runs/")) {
        shardRequests += 1;
        if (shardRequests === 1) {
          response.writeHead(503);
          response.end();
          return;
        }
        if (shardRequests === 2) {
          response.destroy();
          return;
        }
      }
      response.writeHead(200, { "content-type": "application/x-ndjson" });
      response.end(request.url?.endsWith(".sqlite")
        ? databaseContent
        : request.url?.endsWith("inventory-sources.json") ? inventoryContent
        : request.url?.endsWith("payload-hashes.json") ? manifest : runsContent);
    });
    await new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });

    try {
      const address = server.address();
      assert.ok(address && typeof address === "object");
      await executeFile(cao, [
        "download",
        "--url",
        `http://127.0.0.1:${address.port}/cao/payload-hashes.json`,
        "--output",
        output,
      ]);
      assert.equal(shardRequests, 3);
      assert.equal(await readFile(path.join(output, "gh-aw-logs-runs", "fixture.jsonl"), "utf8"), runsContent);
    } finally {
      await new Promise((resolve) => server.close(resolve));
      await rm(root, { recursive: true, force: true });
    }
  });

  test("uses default .cao input and database when omitted", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "cao-cli-default-input-database-"));
    const fixture = path.resolve("dashboard/site/test/fixtures/gh-aw-logs/cached-v2.jsonl");
    const caoRoot = path.join(root, ".cao");
    try {
      await mkdir(caoRoot, { recursive: true });
      await mkdir(path.join(caoRoot, "gh-aw-logs-shards"));
      await copyFile(fixture, path.join(caoRoot, "gh-aw-logs-shards", "fixture.jsonl"));
      const { stdout: auditStdout } = await executeFile(cao, ["audit-jsonl"], { cwd: root });
      const audit = JSON.parse(auditStdout);
      assert.equal(audit.command, "audit-jsonl");
      assert.equal(audit.source.records, 3);

      await executeFile(cao, ["ingest-jsonl", "--retention-days", "all", "--run-retention-days", "all"], { cwd: root });
      const { stdout: runsStdout } = await executeFile(cao, [
        "query",
        "--collection",
        "runs",
        "--where",
        "conclusion=success",
      ], { cwd: root });
      const runs = JSON.parse(runsStdout);
      assert.equal(runs.length, 1);
      assert.equal(runs[0].id, "github:run:githubnext/gh-cao:303");

      const { stdout: stdinStdout } = await executeFileWithInput(cao, [
        "query",
        "--stdin",
      ], JSON.stringify({
        name: "successful-runs",
        from: "runs",
        filter: {
          predicates: [{ field: "conclusion", equals: "success" }],
        },
        limit: 1,
      }), { cwd: root });
      const stdinRuns = JSON.parse(stdinStdout);
      assert.deepEqual(stdinRuns, runs);

      await assert.rejects(
        executeFileWithInput(cao, [
          "query",
          "--stdin",
          "--collection",
          "runs",
        ], '{"collection":"runs"}', { cwd: root }),
        /Option --collection cannot be combined with --stdin/,
      );
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  try {
    const address = server.address();
    assert.ok(address && typeof address === "object");
    const { stdout } = await executeFile(cao, [
      "download",
      "--url",
      `http://127.0.0.1:${address.port}/cao/payload-hashes.json`,
      "--manifest-sha256",
      createHash("sha256").update(manifest).digest("hex"),
      "--output",
      output,
    ]);
    const result = JSON.parse(stdout);
    assert.equal(await readFile(path.join(output, "gh-aw-logs-runs", "fixture.jsonl"), "utf8"), runsContent);
    assert.equal(await readFile(path.join(output, "gh-aw-logs-records", "fixture.jsonl"), "utf8"), recordsContent);
    assert.deepEqual(await readFile(path.join(output, "gh-aw-logs.sqlite")), databaseContent);
    assert.equal(await readFile(path.join(output, "inventory-sources.json"), "utf8"), inventoryContent);
    assert.deepEqual(requests.toSorted(), [
      "/cao/gh-aw-logs-records/fixture.jsonl",
      "/cao/gh-aw-logs-runs/fixture.jsonl",
      "/cao/gh-aw-logs.sqlite",
      "/cao/inventory-sources.json",
      "/cao/payload-hashes.json",
    ]);
    assert.match(result.databaseUrl, /\/cao\/gh-aw-logs\.sqlite$/);
    assert.deepEqual(result.payloadDirectories.map((directory) => path.basename(directory)).toSorted(), [
      "gh-aw-logs-records",
      "gh-aw-logs-runs",
    ]);
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(root, { recursive: true, force: true });
  }
});

test("binds a deployed snapshot to an independent manifest trust anchor", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "deployed-dashboard-manifest-checksum-"));
  const output = path.join(root, "activity");
  const logsContent = '{"kind":"metadata","phase":"runs","records":0}\n';
  const databaseContent = Buffer.from("published sqlite bytes");
  const inventoryContent = JSON.stringify({ campaigns: { rows: [] } });
  const manifest = JSON.stringify({
    "gh-aw-logs.sqlite": createHash("sha256").update(databaseContent).digest("hex"),
    "gh-aw-logs-runs/fixture.jsonl": createHash("sha256").update(logsContent).digest("hex"),
  });
  const server = createServer((request, response) => {
    response.writeHead(200, { "content-type": "application/x-ndjson" });
    response.end(request.url?.endsWith(".sqlite")
      ? databaseContent
      : request.url?.endsWith("inventory-sources.json") ? inventoryContent
      : request.url?.endsWith("payload-hashes.json") ? manifest : logsContent);
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  try {
    const address = server.address();
    assert.ok(address && typeof address === "object");
    await downloadDeployedDashboardData({
      url: `http://127.0.0.1:${address.port}/cao/payload-hashes.json`,
      manifestSha256: createHash("sha256").update(manifest).digest("hex"),
      output,
    });
    assert.deepEqual(await readFile(path.join(output, "gh-aw-logs.sqlite")), databaseContent);

    await assert.rejects(
      downloadDeployedDashboardData({
        url: `http://127.0.0.1:${address.port}/cao/payload-hashes.json`,
        manifestSha256: "0".repeat(64),
        output,
      }),
      /Activity snapshot manifest checksum mismatch: payload-hashes\.json/,
    );
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(root, { recursive: true, force: true });
  }
});
