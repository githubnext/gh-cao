import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig } from "@playwright/test";

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
  || (existsSync("/usr/bin/chromium") ? "/usr/bin/chromium" : undefined);
const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));

export default defineConfig({
  testDir: "../../e2e",
  testMatch: ["**/docs-hero-reveal.spec.mjs", "**/docs-repo-assist.spec.mjs", "**/docs-hierarchy-sizing.spec.mjs", "**/docs-narrow-viewport.spec.mjs", "**/docs-slideshow.spec.mjs", "**/docs-typography.spec.mjs", "**/docs-visuals.spec.mjs"],
  outputDir: "../../../test-results",
  timeout: 30_000,
  workers: 1,
  use: {
    baseURL: "http://127.0.0.1:4321/gh-cao/",
    headless: true,
    launchOptions: {
      args: ["--no-sandbox"],
      ...(executablePath ? { executablePath } : {}),
    },
  },
  webServer: {
    command: "npx astro preview --host 127.0.0.1 --port 4321",
    cwd: repositoryRoot,
    url: "http://127.0.0.1:4321/gh-cao/",
    reuseExistingServer: !process.env.CI,
    timeout: 15_000,
  },
});