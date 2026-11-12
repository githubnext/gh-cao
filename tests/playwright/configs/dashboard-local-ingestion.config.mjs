import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "../../e2e",
  testMatch: ["dashboard-local-ingestion.spec.mjs"],
  outputDir: "../../../test-results/dashboard-local-ingestion",
  workers: 1,
  timeout: 90_000,
  preserveOutput: "always",
  use: {
    browserName: "chromium",
    headless: true,
    trace: "retain-on-failure",
  },
});
