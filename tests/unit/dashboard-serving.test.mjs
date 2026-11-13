import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { promisify } from "node:util";

test("ordinary dashboard development uses Go, not the canvas server", async () => {
  const root = JSON.parse(await readFile(new URL("../../package.json", import.meta.url)));
  const site = JSON.parse(await readFile(new URL("../../dashboard/site/package.json", import.meta.url)));
  assert.equal(root.scripts["dashboard:local"],
    'CAO_MARKETPLACE_POLICY_PATH="${CAO_MARKETPLACE_POLICY_PATH:-$PWD/.github/workflows/cao.json}" go -C server run ./cmd/cao-dashboard serve --site ../dashboard/site/dist');
  assert.equal(root.scripts.dev, "npm run dashboard:local --");
  assert.equal(site.scripts.dev, "npm --prefix ../.. run dev --");
  assert.match(root.scripts["predashboard:local"], /^CAO_DASHBOARD_PROFILE=server /);
  assert.equal(root.scripts["dashboard:server:serve"], root.scripts["dashboard:local"]);
  for (const command of Object.values(root.scripts)) {
    assert.doesNotMatch(command, /node dashboard\/(?:local|canvas)-server\.mjs/);
  }
});

test("local setup and browser debug launchers explicitly select disk-backed dashboard assets", async () => {
  const setup = await readFile(new URL("../../scripts/setup-dashboard-server-macos.sh", import.meta.url), "utf8");
  assert.match(setup, /serve \\\n\s+--site \.\.\/dashboard\/site\/dist/);
  const config = new URL("../playwright/configs/dashboard-server.config.mjs", import.meta.url).href;
  const { stdout } = await promisify(execFile)(process.execPath, ["--input-type=module", "-e",
    `import config from ${JSON.stringify(config)}; console.log(config.webServer.command);`,
  ], { env: { ...process.env, DASHBOARD_SERVER_COMMAND: "" } });
  assert.match(stdout, /serve --site "[^"]+\/dashboard\/site\/dist"/);
});

test("retired canvas servers and operational helpers are not shipped", async () => {
  for (const path of ["dashboard/canvas-server.mjs", "dashboard/canvas-go-host.mjs",
    "dashboard/cli-action-api.mjs", "dashboard/cli-action-queue.mjs", "dashboard/cli-action-quota.mjs",
    "com.github.copilot/extensions/cao-dashboard/local-preview.mjs",
    "server/cmd/cao-cli-quota/main.go", "server/internal/server/canvas.go"]) {
    await assert.rejects(readFile(new URL(`../../${path}`, import.meta.url)), { code: "ENOENT" });
  }
});
