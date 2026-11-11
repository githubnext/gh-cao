import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { ENTITY_STORES } from "../../dashboard/site/src/data/storage/indexeddb.js";
import config from "../playwright/configs/dashboard-deployed.config.mjs";
import {
  activateTableViewMode,
  configureDeployedStorageQuota,
  DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS,
  DEPLOYED_REFRESH_TIMEOUT_MS,
  DEPLOYED_STORAGE_QUOTA_BYTES,
  deployedStorageContext,
  populatedDashboardPages,
  populatedOverviewStations,
  scrollRenderedViewsIntoView,
  shouldIgnoreRequestFailure as ignoreRequestFailure,
} from "../e2e/dashboard-deployed-refresh-helpers.mjs";
import { authoritativeDashboard } from "../helpers/authoritative-dashboard.mjs";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const deployedDashboardUrl = "http://127.0.0.1:8123/gh-cao/";
const shouldIgnoreRequestFailure = (input) => ignoreRequestFailure({ dashboardUrl: deployedDashboardUrl, ...input });

test("deployed dashboard test has enough time for sequential refresh checks", () => {
  assert.equal(DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS, 900_000);
  assert.equal(DEPLOYED_REFRESH_TIMEOUT_MS, 600_000);
  assert.equal(config.timeout, DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS + 2 * DEPLOYED_REFRESH_TIMEOUT_MS);
  assert.ok(config.timeout < 50 * 60_000, "test deadline must fit within the integration job");
  const deployedTest = readFileSync(resolve(repositoryRoot, "tests/e2e/dashboard-deployed-refresh.spec.mjs"), "utf8");
  assert.equal((deployedTest.match(/timeout: DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS/g) ?? []).length, 1);
  assert.equal((deployedTest.match(/timeout: DEPLOYED_REFRESH_TIMEOUT_MS/g) ?? []).length, 1);
});

test("deployed dashboard quota is bounded and scoped to the tested origin", async () => {
  const calls = [];
  const session = {
    async send(method, parameters) { calls.push({ method, parameters }); },
    async detach() { assert.fail("quota session must survive ingestion"); },
  };
  const page = { context: () => ({
    async newCDPSession(target) {
      assert.equal(target, page);
      return session;
    },
  }) };
  assert.equal(await configureDeployedStorageQuota(page, "http://127.0.0.1:1234/cao/"), session);
  assert.equal(DEPLOYED_STORAGE_QUOTA_BYTES, 4 * 1024 ** 3);
  assert.deepEqual(calls, [{
    method: "Storage.overrideQuotaForOrigin",
    parameters: { origin: "http://127.0.0.1:1234", quotaSize: DEPLOYED_STORAGE_QUOTA_BYTES },
  }]);
});

test("deployed storage uses a fresh disk-backed profile and preserves browser options", async () => {
  const calls = [];
  const context = { async close() { calls.push("close"); } };
  await deployedStorageContext({
    playwright: {
      chromium: {
        async launchPersistentContext(directory, options) {
          assert.equal(directory, "");
          assert.deepEqual(options, {
            headless: true,
            channel: undefined,
            executablePath: "/usr/bin/chromium",
            args: ["--disable-dev-shm-usage"],
            viewport: { width: 1280, height: 720 },
          });
          calls.push("launch");
          return context;
        },
      },
    },
    browserName: "chromium",
    headless: true,
    launchOptions: {
      executablePath: "/usr/bin/chromium",
      args: ["--disable-dev-shm-usage"],
    },
    contextOptions: { viewport: { width: 1280, height: 720 } },
  }, async (actual) => {
    assert.equal(actual, context);
    calls.push("use");
  });
  assert.deepEqual(calls, ["launch", "use", "close"]);
});

test("deployed storage closes its profile when the test fails", async () => {
  let closed = false;
  const context = { async close() { closed = true; } };
  await assert.rejects(deployedStorageContext({
    playwright: { chromium: { async launchPersistentContext() { return context; } } },
    browserName: "chromium",
  }, async () => { throw new Error("benchmark failed"); }), /benchmark failed/);
  assert.equal(closed, true);
});

test("deployed data suites opt out of the memory-sized incognito storage pool", () => {
  for (const filename of ["dashboard-deployed-refresh.spec.mjs", "dashboard-query-performance.spec.mjs"]) {
    const source = readFileSync(resolve(repositoryRoot, "tests/e2e", filename), "utf8");
    assert.match(source, /baseTest\.extend\(\{ context: deployedStorageContext \}\)/);
  }
});

test("deployed dashboard quota configuration fails visibly and releases the session", async () => {
  let detached = false;
  const page = { context: () => ({
    async newCDPSession() {
      return {
        async send() { throw new Error("quota override failed"); },
        async detach() { detached = true; },
      };
    },
  }) };
  await assert.rejects(configureDeployedStorageQuota(page, deployedDashboardUrl), /quota override failed/);
  assert.equal(detached, true);
});

test("deployed dashboard check targets declared navigation pages", () => {
  const navigationPageIds = new Set(authoritativeDashboard.dashboard.navigation.flatMap(({ pages }) => pages));

  for (const { pageId, storeName } of populatedDashboardPages) {
    assert.ok(navigationPageIds.has(pageId), `expected '${pageId}' to be a declared navigation page`);
    assert.ok(ENTITY_STORES.includes(storeName), `expected '${storeName}' to be a canonical entity store`);
  }
});

test("overview inventory checks allow zero health or reach but reject missing denominators", () => {
  const navigationPageIds = new Set(authoritativeDashboard.dashboard.navigation.flatMap(({ pages }) => pages));
  for (const { href, detail } of populatedOverviewStations) {
    assert.ok(navigationPageIds.has(href.replace("#page-", "")));
    const suffix = href === "#page-workflows" ? "healthy workflows" : "repositories reached";
    for (const counts of ["0/7", "0/748", "707/748", "1,000/1,234"]) {
      assert.match(`${counts} ${suffix}`, detail);
    }
    for (const counts of ["0/0", "7/0", "0/", "/7", "NaN/7"]) {
      assert.doesNotMatch(`${counts} ${suffix}`, detail);
    }
    assert.doesNotMatch("Unavailable", detail);
    assert.doesNotMatch("", detail);
  }
  const deployedTest = readFileSync(resolve(repositoryRoot, "tests/e2e/dashboard-deployed-refresh.spec.mjs"), "utf8");
  assert.match(deployedTest, /of populatedOverviewStations/);
  assert.match(deployedTest, /toHaveText\(detail\)/);
  assert.doesNotMatch(deployedTest, /\.factory-station-empty/);
});

test("deployed dashboard scrolling uses a stable view snapshot", async () => {
  const scrollCalls = [];
  const focusCalls = [];
  const disposed = [];
  const elements = [
    {
      scrollIntoView: (options) => scrollCalls.push(options),
      matches: (selector) => selector === "[data-lazy-view]",
      focus: (options) => focusCalls.push(options),
    },
    {
      scrollIntoView: (options) => scrollCalls.push(options),
      matches: () => false,
      focus: (options) => focusCalls.push(options),
    },
  ];
  let locatorSelector;
  const activePage = {
    locator(selector) {
      locatorSelector = selector;
      return {
        async elementHandles() {
          return elements.map((element, index) => ({
            async evaluate(callback) {
              callback(element);
            },
            async dispose() {
              disposed.push(index);
            },
          }));
        },
      };
    },
  };

  await scrollRenderedViewsIntoView(activePage);

  assert.equal(locatorSelector, "[data-view-id]");
  assert.deepEqual(scrollCalls, [
    { block: "center", inline: "nearest" },
    { block: "center", inline: "nearest" },
  ]);
  assert.deepEqual(focusCalls, [
    { preventScroll: true },
  ]);
  assert.deepEqual(disposed, [0, 1]);
});

test("deployed dashboard activates table mode before hydrating table views", async () => {
  const clicks = [];
  const activePage = {
    locator(selector) {
      assert.equal(selector, '[data-view-mode-value="table"]');
      return {
        async count() {
          return 1;
        },
        async click() {
          clicks.push(selector);
        },
      };
    },
  };

  assert.equal(await activateTableViewMode(activePage), true);
  assert.deepEqual(clicks, ['[data-view-mode-value="table"]']);
});

test("deployed dashboard tolerates pages without table mode", async () => {
  const activePage = {
    locator() {
      return {
        async count() {
          return 0;
        },
        async click() {
          assert.fail("table mode should not be clicked");
        },
      };
    },
  };

  assert.equal(await activateTableViewMode(activePage), false);
});

test("deployed dashboard scrolling tolerates lazy view replacement only", async () => {
  const disposed = [];
  const activePage = {
    locator() {
      return {
        async elementHandles() {
          return [
            {
              async evaluate() {
                const error = new Error("Element is not attached to the DOM");
                error.name = "DetachedElementError";
                throw error;
              },
              async dispose() {
                disposed.push(0);
              },
            },
            {
              async evaluate() {},
              async dispose() {
                disposed.push(1);
              },
            },
          ];
        },
      };
    },
  };

  await scrollRenderedViewsIntoView(activePage);

  assert.deepEqual(disposed, [0, 1]);
});

test("deployed dashboard scrolling tolerates empty view snapshots", async () => {
  const activePage = {
    locator() {
      return {
        async elementHandles() {
          return [];
        },
      };
    },
  };

  await scrollRenderedViewsIntoView(activePage);
});

test("deployed dashboard scrolling reports unexpected errors", async () => {
  const disposed = [];
  const evaluated = [];
  const activePage = {
    locator() {
      return {
        async elementHandles() {
          return [{
            async evaluate() {
              evaluated.push(0);
              throw new Error("unexpected scroll failure");
            },
            async dispose() {
              disposed.push(0);
            },
          }, {
            async evaluate() {
              evaluated.push(1);
            },
            async dispose() {
              disposed.push(1);
            },
          }];
        },
      };
    },
  };

  await assert.rejects(scrollRenderedViewsIntoView(activePage), /unexpected scroll failure/);

  assert.deepEqual(evaluated, [0]);
  assert.deepEqual(disposed, [0, 1]);
});

test("deployed dashboard failure tracking ignores only benign aborted probes", () => {
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: `${deployedDashboardUrl}demo-data/gh-aw-logs-runs/shard.jsonl`,
    errorText: "net::ERR_ABORTED",
  }), true);
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: `${deployedDashboardUrl}gh-aw-logs-runs/shard.jsonl`,
    errorText: "net::ERR_ABORTED",
  }), true);
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: `${deployedDashboardUrl}gh-aw-logs-records/shard.jsonl`,
    errorText: "net::ERR_ABORTED",
  }), true);
  assert.equal(shouldIgnoreRequestFailure({
    method: "GET",
    url: `${deployedDashboardUrl}gh-aw-logs-runs/shard.jsonl`,
    errorText: "net::ERR_ABORTED",
    reloading: true,
  }), true);
  assert.equal(shouldIgnoreRequestFailure({
    method: "GET",
    url: `${deployedDashboardUrl}gh-aw-logs-runs/shard.jsonl`,
    errorText: "net::ERR_ABORTED",
  }), false);
  assert.equal(shouldIgnoreRequestFailure({
    method: "GET",
    url: `${deployedDashboardUrl}payload-hashes.json`,
    errorText: "net::ERR_ABORTED",
    hadSuccessResponse: true,
  }), true);
  assert.equal(shouldIgnoreRequestFailure({
    method: "GET",
    url: `${deployedDashboardUrl}payload-hashes.json`,
    errorText: "net::ERR_ABORTED",
  }), false);
  assert.equal(shouldIgnoreRequestFailure({
    method: "GET",
    url: `${deployedDashboardUrl}payload-hashes.json`,
    errorText: "net::ERR_FAILED",
    hadSuccessResponse: true,
  }), false);
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: `${deployedDashboardUrl}gh-aw-logs-runs/shard.jsonl`,
    errorText: "net::ERR_NAME_NOT_RESOLVED",
  }), false);
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: "https://example.com/shard.json",
    errorText: "net::ERR_ABORTED",
  }), false);
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: "https://githubnext.github.io/other-dashboard/cao/gh-aw-logs-runs/shard.jsonl",
    errorText: "net::ERR_ABORTED",
  }), false);
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: `${deployedDashboardUrl}assets/app.js`,
    errorText: "net::ERR_ABORTED",
  }), false);
  assert.equal(shouldIgnoreRequestFailure({
    method: "HEAD",
    url: `${deployedDashboardUrl}payload-hashes.json`,
    errorText: "net::ERR_ABORTED",
  }), false);
});
