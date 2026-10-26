import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import {
  captureDashboardQueryResponse,
  dashboardAssessmentPageHash,
  declaredDashboardViewIds,
  visibleBusyViewSelector,
} from "./dashboard-view-assessment.mjs";
import {
  dashboardPageChunkPath,
  mergeDashboardPage,
  normalizeDashboardPageChunk,
} from "../../dashboard/site/src/dashboard-chunks.js";
import { scrollRenderedViewsIntoView } from "./dashboard-deployed-refresh-helpers.mjs";
import { serverDashboardPageExclusionReason } from "./dashboard-server-page-coverage.mjs";
import { createDashboardServerQueryBudget } from "./dashboard-server-query-budget.mjs";

const SERVER_LOCAL_ONLY_PAGES = new Map([
  ["configuration", "configuration-policy is provided by the local preview fixture, not the PostgreSQL server"],
]);

const accessToken = process.env.DASHBOARD_SERVER_ACCESS_TOKEN
  || "0123456789abcdef0123456789abcdef";

const queryBudget = createDashboardServerQueryBudget();
let captureQueryResponse;

test.beforeEach(async ({ context }) => {
  captureQueryResponse = undefined;
  await context.route("**/api/v1/query", async (route) => {
    const capture = captureQueryResponse;
    await queryBudget.run(async () => {
      const response = await route.fetch();
      capture?.({ ...captureDashboardQueryResponse(response), request: route.request() });
      await route.fulfill({ response });
      return response;
    });
  });
  await queryBudget.waitForBudget();
});

test.afterEach(async ({ context }) => {
  await context.unrouteAll({ behavior: "ignoreErrors" });
});

const populatedViews = [
  {
    page: "runs",
    heading: "Runs",
    view: "runs-runs-source",
    field: "run",
    value: "424242",
  },
  {
    page: "workflows",
    heading: "Workflows",
    view: "workflows-inventory",
    field: "workflow-name",
    value: "Dashboard",
  },
  {
    page: "repositories",
    heading: "Repositories",
    view: "repositories-activity",
    field: "repository",
    value: "gh-cao",
  },
];

for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
  test(`Ingestion shows live process webhook counters at ${viewport.width}px`, async ({ context, page }, testInfo) => {
    await page.setViewportSize(viewport);
    await page.clock.install();
    await page.goto(`/?access_token=${accessToken}#page-indexing`);
    await page.getByRole('button', { name: viewport.width < 768 ? 'Switch to Cards view' : 'Cards', exact: true }).click();
    const view = page.locator('[data-view-id="ingestion-webhook-status"]');
    await expect(view.getByRole('heading', { name: 'Webhook counters (process-local, since restart)' })).toBeVisible();
    await expect(page.locator('[data-page-id="indexing"]')).not.toHaveAttribute('aria-busy', 'true');
    const received = view.locator('dl > div').filter({ has: page.locator('dt', { hasText: /^Received$/ }) }).locator('dd');
    const headers = { Authorization: `Bearer ${accessToken}` };
    const beforeResponse = await context.request.get('/api/v1/ingestion/webhook/status', { headers });
    expect(beforeResponse.ok()).toBe(true);
    const before = await beforeResponse.json();
    expect(before.scope).toBe('process');
    await expect(view).toContainText(before.configured ? 'Configured' : 'Not configured', { timeout: 60_000 });
    await expect(received).toHaveText(String(before.received));
    const delivery = await context.request.post('/api/github/webhook', {
      headers: { 'X-GitHub-Event': 'ping', 'X-GitHub-Delivery': `browser-ping-${viewport.width}` },
      data: { zen: 'synthetic browser status check' }
    });
    // This fixture intentionally has no webhook secret, so no ping can validate.
    expect(delivery.status()).toBe(503);
    await page.clock.fastForward(30_000);
    await expect.poll(async () => {
      await page.clock.runFor(1000);
      return received.textContent();
    }, { timeout: 20_000 }).toBe(String(before.received + 1));
    await expect(view.locator('dl > div').filter({ has: page.locator('dt', { hasText: /^Failed$/ }) }).locator('dd'))
      .toHaveText(String(before.failed + 1));
    await expect(view.locator('dl > div').filter({ has: page.locator('dt', { hasText: /^Signed pings$/ }) }).locator('dd'))
      .toHaveText('0');
    await expect(view.locator('.entity-card-list-card')).toBeVisible();
    await view.scrollIntoViewIfNeeded();
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const path = testInfo.outputPath(`ingestion-webhook-${viewport.width}.png`);
    await page.screenshot({ path, fullPage: true });
    await testInfo.attach(`Populated Go-server Ingestion (${viewport.width}px)`, { path, contentType: 'image/png' });
  });
}

test("deployed shards populate server-backed dashboard views", async ({ context, page }) => {
  test.setTimeout(240_000);
  await page.goto(`/?access_token=${accessToken}`);
  await expect(page).toHaveURL("http://127.0.0.1:8443/");
  await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveAttribute("content", "server-http");
  const headers = { Authorization: `Bearer ${accessToken}` };

  const health = await context.request.get("/api/v1/health", { headers });
  expect(health.ok()).toBe(true);
  const healthPayload = await health.json();
  expect(healthPayload.redis).toEqual({ connected: true });
  expect(healthPayload.revision).toBeGreaterThan(0);
  expect(healthPayload.counts).toMatchObject({
    $repositories: 1,
    $workflows: 1,
    $runs: 1,
    $domains: 1,
    $tools: 1,
    $audits: 1,
    $issues: 1,
  });
  expect(JSON.stringify(healthPayload)).not.toMatch(/redis:\/\/|password|credential/i);

  const query = await queryBudget.run(() => context.request.post("/api/v1/query", {
    headers,
    data: { sourceNames: ["runs"], queries: [] },
  }));
  expect(query.ok()).toBe(true);
  const queryPayload = await query.json();
  expect(queryPayload.sources.runs.rows).toEqual([
    expect.objectContaining({
      run: "424242",
      repository: "gh-cao",
      workflow: ".github/workflows/dashboard.md",
    }),
  ]);

  for (const expected of populatedViews) {
    await queryBudget.waitForBudget();
    await page.goto(`/#page-${expected.page}`);
    await expect(page.getByRole("heading", { name: expected.heading, exact: true, level: 1 })).toBeVisible();
    const tableMode = page.getByRole("button", { name: "Table", exact: true });
    await expect(tableMode).toBeVisible();
    await tableMode.click();
    await expect(tableMode).toHaveAttribute("aria-pressed", "true");
    const activePage = page.locator(`[data-page-id="${expected.page}"]`);
    await expect(activePage).not.toHaveAttribute("aria-busy", "true", { timeout: 30_000 });
    await scrollRenderedViewsIntoView(activePage);
    const view = activePage.locator(`[data-view-id="${expected.view}"]`);
    await expect(view).toBeVisible();
    await expect(view.locator(`td[data-field="${expected.field}"]`, { hasText: expected.value })).toBeVisible();
    await expect(view).not.toContainText("Unavailable");
  }
});

  test("every server-backed dashboard page resolves its queries against the Go Postgres server", async ({ context, page }) => {
    test.setTimeout(1_600_000);
    const outputDirectory = resolve("test-results/dashboard-server");
    await mkdir(outputDirectory, { recursive: true });
    const results = [];
    let blocker;

    try {
      const initialQueryResponses = [];
      captureQueryResponse = (response) => initialQueryResponses.push(response);
      await page.goto(`/?access_token=${accessToken}`);
      await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveAttribute("content", "server-http");
      await expect(page.locator('[data-page-id="overview"]')).not.toHaveAttribute("aria-busy", "true", { timeout: 30_000 });
      captureQueryResponse = undefined;
      const dashboardResponse = await context.request.get("/dashboard.json");
      expect(dashboardResponse.ok()).toBe(true);
      const dashboard = await dashboardResponse.json();
      const pages = [];
      for (const definition of dashboard.dashboard.pages) {
        const chunkPath = dashboardPageChunkPath(definition);
        if (!chunkPath) {
          pages.push(definition);
          continue;
        }
        const chunkResponse = await context.request.get(`/${chunkPath.replace(/^\/+/, "")}`);
        expect(chunkResponse.ok(), `Load page definition for ${definition.id}`).toBe(true);
        pages.push(mergeDashboardPage(definition, normalizeDashboardPageChunk(await chunkResponse.json()).page));
      }
      expect(pages.length).toBeGreaterThan(0);
      expect(await page.evaluate(() => Reflect.get(globalThis, "__CLI_QUEUE__"))).not.toBe(true);

      for (const definition of pages) {
        const localOnlyReason = SERVER_LOCAL_ONLY_PAGES.get(definition.id);
        if (localOnlyReason) {
          results.push({ pageId: definition.id, status: "skipped", queries: 0, errors: [localOnlyReason] });
          continue;
        }
        const result = { pageId: definition.id, status: "failed", queries: 0, errors: [] };
        results.push(result);
        const queryResponses = definition.id === "overview" ? [...initialQueryResponses] : [];
        const browserErrors = [];
        const onPageError = (error) => browserErrors.push(error.message);
        page.on("pageerror", onPageError);
        try {
          await queryBudget.waitForBudget();
          captureQueryResponse = (response) => queryResponses.push(response);
          const exclusionReason = serverDashboardPageExclusionReason(definition);
          await page.evaluate((hash) => { window.location.hash = hash; },
            dashboardAssessmentPageHash(definition, dashboard));
          const activePage = page.locator(`[data-page-id="${definition.id}"]`);
          if (exclusionReason) {
            await expect(page.locator('[data-page-id="overview"]')).toBeVisible();
            await expect(activePage).toHaveCount(0);
            await expect(page.locator(".cli-commands-navigation:visible")).toHaveCount(0);
            result.status = "unsupported";
            result.reason = exclusionReason;
            continue;
          }
          await expect(activePage).toBeVisible({ timeout: 30_000 });
          await expect(activePage).not.toHaveAttribute("data-page-pending", "", { timeout: 30_000 });
          await expect(activePage).not.toHaveAttribute("aria-busy", "true", { timeout: 30_000 });
          await activePage.locator("details.view-disclosure").evaluateAll((items) => {
            for (const item of items) item.open = true;
          });
          const views = activePage.locator("[data-view-id]");
          const declared = declaredDashboardViewIds(definition, dashboard.dashboard.views);
          const pageViews = (definition.views ?? []).map((view) => (
            typeof view === "string"
              ? dashboard.dashboard.views.find((candidate) => candidate.id === view)
              : view
          ));
          const expectsQueries = pageViews.some((view) => (
            view?.data && (typeof view.data.source === "string" || Array.isArray(view.data.sources))
          ));
          const rendered = await views.evaluateAll((items) =>
            items.map((item) => item.getAttribute("data-view-id")));
          const unavailable = activePage.locator('[data-view-state="unavailable"][role="alert"]');
          if (definition.route?.["availability-view"] && await unavailable.isVisible()) {
            expect(rendered, "Unavailable detail pages replace their views with recovery actions").toEqual([]);
            await expect(unavailable).toContainText(definition.route["availability-message"]);
            await expect(unavailable.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
            await expect(unavailable.getByRole("link")).toHaveAttribute(
              "href", `#page-${definition.route["navigation-page"]}`,
            );
            result.availability = "unavailable";
          } else {
            for (const viewId of declared) {
              if (!rendered.includes(viewId)) result.errors.push(`Missing view: ${viewId}`);
            }
          }
          await scrollRenderedViewsIntoView(activePage);
          await expect(activePage.locator(visibleBusyViewSelector)).toHaveCount(0, { timeout: 30_000 });
          const failures = await activePage.locator('[aria-label^="Unable to load "]')
            .evaluateAll((items) => items.map((item) => item.getAttribute("aria-label")));
          result.errors.push(...failures, ...browserErrors);
          if (expectsQueries) {
            await expect.poll(() => queryResponses.length, {
              message: `Wait for Go server queries for ${definition.id}`,
              timeout: 30_000,
            }).toBeGreaterThan(0);
          }
          for (const { response, body, request } of queryResponses) {
            result.queries += 1;
            if (!response.ok()) {
              result.errors.push(`Query HTTP ${response.status()}`);
              continue;
            }
            const { payload, error } = await body;
            if (error) throw error;
            if (!payload.sources || typeof payload.sources !== "object") {
              result.errors.push("Query returned no sources object");
              continue;
            }
            const input = request.postDataJSON();
            const replaced = new Set(input.replacedSources ?? []);
            for (const name of [...(input.sourceNames ?? []), ...(input.aliases ?? [])]) {
              if (!replaced.has(name) && !Object.hasOwn(payload.sources, name)) {
                result.errors.push(`Query did not resolve source: ${name}`);
              }
            }
          }
          result.status = result.errors.length === 0 ? "passed" : "failed";
        } catch (error) {
          result.errors.push(error instanceof Error ? error.message : String(error));
        } finally {
          await Promise.all(queryResponses.map(({ body }) => body));
          captureQueryResponse = undefined;
          page.off("pageerror", onPageError);
        }
      }
    } catch (error) {
      blocker = error instanceof Error ? error.message : String(error);
    } finally {
      const passed = results.filter((result) => result.status === "passed").length;
      const skipped = results.filter((result) => result.status === "skipped").length;
      const unsupported = results.filter((result) => result.status === "unsupported").length;
      const failed = results.some((result) => result.status === "failed");
      const summary = [
        "### Go Postgres dashboard page checks",
        "",
        `**${blocker || failed ? "FAILED" : "PASSED"}** — ${passed}/${results.length - skipped - unsupported} server-backed pages passed; ${skipped} local-only page(s) skipped; ${unsupported} capability-gated route(s) verified unavailable.`,
        ...(blocker ? ["", `Setup failed: ${blocker.replaceAll("\n", " ")}`] : []),
        "",
        "| Page | Status | Queries | Errors / scope reason |",
        "|---|---|---:|---|",
        ...results.map((result) =>
          `| ${result.pageId} | ${result.status.toUpperCase()} | ${result.queries} | ${[...result.errors, ...(result.reason ? [result.reason] : [])].map((error) => error.replaceAll("|", "\\|").replaceAll("\n", " ")).join("; ") || "—"} |`),
        "",
      ].join("\n");
      await writeFile(resolve(outputDirectory, "summary.md"), summary);
      await writeFile(resolve(outputDirectory, "summary.json"), `${JSON.stringify({ blocker, results }, null, 2)}\n`);
    }
    expect(blocker, "Dashboard setup must succeed").toBeUndefined();
    expect(results.length, "Every declared page must be assessed").toBeGreaterThan(0);
    expect(results.filter((result) => result.status === "failed"), "Every server-backed dashboard page must load").toEqual([]);
});
