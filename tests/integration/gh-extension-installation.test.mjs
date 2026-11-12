import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { delimiter, join, resolve } from "node:path";
import test from "node:test";
import { setTimeout } from "node:timers/promises";

const platforms = [
  "darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64",
  "windows-amd64.exe", "windows-arm64.exe",
];

async function run(args, env) {
  const child = spawn("gh", args, { env, stdio: ["ignore", "pipe", "pipe"], timeout: 30_000 });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8").on("data", (chunk) => { stdout += chunk; });
  child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr += chunk; });
  const [status] = await once(child, "close");
  return { status, stdout, stderr };
}

async function standaloneDashboard(t, directory, env, requests) {
  const cwd = join(directory, "empty working directory");
  const bin = join(directory, "dashboard-only-path");
  mkdirSync(cwd);
  mkdirSync(bin);
  const ghName = process.platform === "win32" ? "gh.exe" : "gh";
  const gh = process.env.PATH.split(delimiter).map((path) => join(path, ghName)).find(existsSync);
  assert.ok(gh, "the release check requires the real GitHub CLI");
  copyFileSync(gh, join(bin, ghName));
  const cache = join(directory, "dashboard-cache");
  requests.length = 0;
  const child = spawn(join(bin, ghName), ["cao", "dashboard", "--repo", "octo/api",
    "--cache-dir", cache, "--duration", "20s"], {
    cwd, env: { ...env, PATH: bin }, stdio: ["ignore", "pipe", "pipe"], timeout: 45_000,
    detached: process.platform !== "win32",
  });
  let output = "";
  child.stdout.setEncoding("utf8").on("data", (chunk) => { output += chunk; });
  child.stderr.setEncoding("utf8").on("data", (chunk) => { output += chunk; });
  let exited = false;
  const done = once(child, "close").then(([status]) => { exited = true; return status; });
  t.after(async () => {
    if (!exited) {
      t.diagnostic(`${output}\nGitHub requests:\n${requests.join("\n")}`);
      if (process.platform === "win32") child.kill("SIGTERM");
      else process.kill(-child.pid, "SIGTERM");
    }
    await done;
  });
  let match;
  const deadline = Date.now() + 30_000;
  while (!(match = output.match(/Dashboard: (http:\/\/\S+)/))) {
    assert.ok(!exited && Date.now() < deadline, `standalone dashboard did not start:\n${output}\n${requests.join("\n")}`);
    await setTimeout(25);
  }
  assert.match(output, /Starting local CAO dashboard for octo\/api/);
  assert.match(output, /browser URL will appear when ready/);
  assert.match(output, /Open this URL in your browser/);
  assert.match(output, /Refresh: every 5m0s/);
  assert.match(output, /press Ctrl\+C to stop\. Cached data is kept/);
  assert.ok(output.includes(`SQLite cache: ${join(cache, "gh-aw-logs.sqlite")}`));
  assert.equal((output.match(/Dashboard: http:\/\//g) ?? []).length, 1);
  const url = new URL(match[1]);
  const accessToken = url.searchParams.get("access_token");
  assert.ok(accessToken, "the SQLite ingestion server must use ordinary bearer bootstrap");
  assert.equal(url.searchParams.has("local-preview"), false);
  assert.equal(url.pathname, "/");
  url.search = "";
  url.hash = "";
  for (const path of ["", "dashboard.json", "src/agent/catalog.generated.json",
    "src/agent/queries.generated.json", "src/agent/mcp-contract.json",
    "src/data/queries/database.json", "service-worker.js"]) {
    const response = await fetch(new URL(path, url), { signal: AbortSignal.timeout(5_000) });
    assert.equal(response.status, 200, `${path}: ${output}`);
    const content = await response.text();
    assert.ok(content.length > 0, path);
    if (path === "") {
      assert.match(content, /content="server-http"/);
      assert.match(content, /name="dashboard-storage-backend" content="sqlite"/);
      assert.doesNotMatch(content, /name="dashboard-api-base"/);
      for (const asset of [...content.matchAll(/(?:src|href)="([^"]+\.(?:js|css)(?:\?[^"]*)?)"/g)]) {
        const resource = await fetch(new URL(asset[1], url), { signal: AbortSignal.timeout(5_000) });
        assert.equal(resource.status, 200, asset[1]);
        assert.ok((await resource.text()).length > 0, asset[1]);
      }
    }
    if (path === "dashboard.json") {
      const standard = JSON.parse(content);
      const overview = standard.dashboard.pages.find((page) => page.id === "overview");
      const chunk = await (await fetch(new URL(overview.chunk, url))).json();
      assert.deepEqual(chunk.page.views.map((view) => view.id), ["overview-header", "overview-floor"]);
    }
  }
  const response = await fetch(new URL("api/v1/query", url), {
    method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${accessToken}` },
    body: JSON.stringify({ sourceNames: ["repositories", "runs"] }), signal: AbortSignal.timeout(5_000),
  });
  assert.equal(response.status, 200, await response.clone().text());
  const result = await response.json();
  assert.equal(result.sources.repositories.rows.length, 1);
  assert.equal(result.sources.runs.rows.length, 1);
  const snapshot = join(cache, "gh-aw-logs.sqlite");
  assert.equal(readFileSync(snapshot).subarray(0, 16).toString(), "SQLite format 3\0");
  assert.ok(requests.some((path) => path.startsWith("/repos/octo/api/actions/workflows/42/runs")),
    `embedded collector was not exercised:\n${requests.join("\n")}`);
  assert.equal(await done, 0, output);
  assert.ok(existsSync(snapshot), "the local SQLite cache must survive server shutdown");
  await assert.rejects(fetch(url, { signal: AbortSignal.timeout(1_000) }), /fetch failed/);
}

test("real GitHub CLI discovers, installs, and dispatches native release assets", { timeout: 120_000 }, async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "cao-gh-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const binary = readFileSync(resolve(process.env.CAO_EXTENSION_BINARY || "gh-cao"));
  const version = process.env.CAO_EXTENSION_VERSION || "dev";
  const socket = join(directory, "api.sock");
  const config = join(directory, "config");
  mkdirSync(config);
  // GitHub CLI's socket transport keeps the real github.com install command offline.
  writeFileSync(join(config, "config.yml"), `http_unix_socket: ${JSON.stringify(socket)}\n`);
  const origin = "https://api.github.com";
  let runtimeOnly = true;
  const requests = [];
  const server = createServer((request, response) => {
    requests.push(request.url);
    const assets = platforms.map((platform, index) => ({
      name: `gh-cao-${platform}`, url: `${origin}/assets/${index}`, size: binary.length,
    }));
    const latest = { tag_name: "v1.2.3", draft: false, prerelease: false, assets: runtimeOnly
      ? [{ name: "cao-runtime-0123456789abcdef.tgz", url: `${origin}/runtime` }]
      : [...assets, { name: "checksums.txt", url: `${origin}/checksums` }] };
    const path = new URL(request.url, origin).pathname;
    const workflow = { id: 42, name: "Agent", path: ".github/workflows/agent.lock.yml", state: "active" };
    const created = new Date(Date.now() - 60_000).toISOString();
    const run = {
      id: 123, workflow_id: 42, name: "Agent", path: workflow.path, run_attempt: 1,
      display_title: "Bundled dashboard evidence", status: "completed", conclusion: "success",
      event: "workflow_dispatch", head_branch: "main", head_sha: "a".repeat(40),
      created_at: created, run_started_at: created, updated_at: created,
      html_url: "https://github.com/octo/api/actions/runs/123",
    };
    let body;
    if (path === "/graphql") {
      body = { data: { repository: { id: "R_fixture", name: "api", owner: { login: "octo" },
        nameWithOwner: "octo/api", url: "https://github.com/octo/api" } } };
    } else if (path === "/rate_limit") {
      body = { resources: { core: { limit: 5000, remaining: 4999, used: 1, reset: Math.floor(Date.now() / 1000) + 3600 } } };
    } else if (path === "/repos/octo/api/actions/workflows") {
      body = { total_count: 1, workflows: [workflow] };
    } else if (path === "/repos/octo/api/actions/workflows/42") {
      body = workflow;
    } else if (path === "/repos/octo/api/actions/runs" || path === "/repos/octo/api/actions/workflows/42/runs") {
      body = { total_count: 1, workflow_runs: [run] };
    } else if (path === "/repos/octo/api/actions/runs/123") {
      body = run;
    } else if (path === "/repos/octo/api/actions/runs/123/artifacts") {
      body = { total_count: 0, artifacts: [] };
    } else if (path === "/repos/octo/api/actions/runs/123/jobs") {
      body = { total_count: 0, jobs: [] };
    } else if (request.url === "/repos/githubnext/gh-cao/releases/latest") {
      body = latest;
    } else if (request.url === "/repos/githubnext/gh-cao/releases/tags/v1.2.4") {
      body = { tag_name: "v1.2.4", draft: false, prerelease: true, assets };
    } else if (/^\/assets\/[0-5]$/.test(request.url)) {
      const platform = `${process.platform === "win32" ? "windows" : process.platform}-${process.arch === "x64" ? "amd64" : process.arch}${process.platform === "win32" ? ".exe" : ""}`;
      if (request.url !== `/assets/${platforms.indexOf(platform)}`) {
        response.writeHead(400).end("Wrong platform asset selected");
        return;
      }
      response.writeHead(200, { "Content-Type": "application/octet-stream" }).end(binary);
      return;
    } else {
      response.writeHead(404, { "Content-Type": "application/json" }).end(JSON.stringify({ message: "Not Found" }));
      return;
    }
    response.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify(body));
  });
  t.after(() => { server.closeAllConnections(); return new Promise((resolve) => server.close(resolve)); });
  server.listen(socket);
  await once(server, "listening");
  const baseEnv = { ...process.env };
  for (const name of [
    "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
    "GH_DEBUG", "GH_CONFIG_DIR", "GH_HOST", "GH_PATH", "GH_EXTENSION",
  ]) delete baseEnv[name];
  Object.assign(baseEnv, {
    GH_CONFIG_DIR: config, GH_HOST: "github.com",
    GH_TOKEN: "local-fixture-token", GH_PROMPT_DISABLED: "1",
    GH_NO_UPDATE_NOTIFIER: "1", GH_NO_EXTENSION_UPDATE_NOTIFIER: "1",
  });
  const repository = "githubnext/gh-cao";
  for (const scenario of [
    { name: "runtime-only latest", runtimeOnly: true, pinned: false },
    { name: "prerelease pin cannot bypass runtime-only latest", runtimeOnly: true, pinned: true },
    { name: "complete latest", runtimeOnly: false, pinned: false },
    { name: "prerelease pin after binary discovery", runtimeOnly: false, pinned: true },
  ]) {
    await t.test(scenario.name, async (t) => {
      runtimeOnly = scenario.runtimeOnly;
      requests.length = 0;
      const data = join(directory, scenario.name);
      const env = {
        ...baseEnv, XDG_DATA_HOME: data, XDG_STATE_HOME: join(data, "state"),
        XDG_CACHE_HOME: join(data, "cache"), LOCALAPPDATA: data,
      };
      const args = ["extension", "install", repository];
      if (scenario.pinned) args.push("--pin", "v1.2.4");
      const result = await run(args, env);
      assert.ok(requests.includes("/repos/githubnext/gh-cao/releases/latest"), result.stderr);
      if (scenario.runtimeOnly) {
        assert.notEqual(result.status, 0);
        assert.match(result.stderr, /extension is not installable: no usable release artifact or script found/);
        assert.ok(requests.includes("/repos/githubnext/gh-cao/contents/gh-cao"));
        assert.ok(!requests.some((path) => path.includes("/releases/tags/") || path.startsWith("/assets/")));
        return;
      }
      assert.equal(result.status, 0, result.stderr);
      assert.equal(requests.filter((path) => path.startsWith("/assets/")).length, 1);
      assert.equal(requests.includes("/repos/githubnext/gh-cao/releases/tags/v1.2.4"), scenario.pinned);
      const installed = await run(["cao", "--version"], env);
      assert.equal(installed.status, 0, installed.stderr);
      assert.equal(installed.stdout.trim(), `gh cao version ${version}`);
      const help = await run(["cao", "--help"], env);
      assert.equal(help.status, 0, help.stderr);
      assert.match(help.stdout, /Usage:/);
      const manifest = join(data, "gh", "extensions", "gh-cao", "manifest.yml");
      assert.match(readFileSync(manifest, "utf8"), new RegExp(`tag: ${scenario.pinned ? "v1.2.4" : "v1.2.3"}`));
      if (!scenario.pinned) {
        await t.test("dashboard runs with only gh and its installed extension", async (t) => {
          await standaloneDashboard(t, directory, env, requests);
        });
      }
    });
  }
});
