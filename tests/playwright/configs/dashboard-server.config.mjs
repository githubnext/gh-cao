import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig } from "@playwright/test";

const chromiumExecutable = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
  || (existsSync("/usr/bin/chromium") ? "/usr/bin/chromium" : undefined);
const origin = "http://127.0.0.1:8443";
const repositoryRoot = fileURLToPath(new URL("../../..", import.meta.url));
const accessToken = process.env.DASHBOARD_SERVER_ACCESS_TOKEN
  || "0123456789abcdef0123456789abcdef";
const source = `${repositoryRoot}/test-results/dashboard-server/fixture`;
const serverCommand = process.env.DASHBOARD_SERVER_COMMAND
  || `go -C "${repositoryRoot}/server" run ./cmd/cao-dashboard serve --site "${repositoryRoot}/dashboard/site/dist" --source "${source}" --access-token ${accessToken}`;

export default defineConfig({
  testDir: "../../e2e",
  testMatch: ["**/dashboard-server.spec.mjs"],
  outputDir: "../../../test-results/dashboard-server/playwright",
  timeout: 120_000,
  workers: 1,
  preserveOutput: "always",
  webServer: {
    command: `node "${repositoryRoot}/tests/e2e/dashboard-server-fixture.mjs" "${source}" && ${serverCommand}`,
    env: { DASHBOARD_SERVER_SOURCE: source },
    url: `${origin}/api/v1/health`,
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: "pipe",
    stderr: "pipe",
  },
  use: {
    baseURL: origin,
    browserName: "chromium",
    headless: true,
    launchOptions: {
      args: ["--no-sandbox", "--disable-dev-shm-usage"],
      ...(chromiumExecutable ? { executablePath: chromiumExecutable } : {}),
    },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
});
