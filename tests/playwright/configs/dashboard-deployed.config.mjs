import { existsSync } from "node:fs";
import { defineConfig } from "@playwright/test";
import {
  DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS,
  DEPLOYED_REFRESH_TIMEOUT_MS,
} from "../../e2e/dashboard-deployed-refresh-helpers.mjs";

const chromiumExecutable = existsSync("/usr/bin/chromium") ? "/usr/bin/chromium" : undefined;

export default defineConfig({
  testDir: "../../e2e",
  testMatch: ["**/dashboard-deployed-refresh.spec.mjs"],
  outputDir: "../../../test-results/dashboard-deployed/playwright",
  timeout: DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS + 2 * DEPLOYED_REFRESH_TIMEOUT_MS,
  workers: 1,
  preserveOutput: "always",
  use: {
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