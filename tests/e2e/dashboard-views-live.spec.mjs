import { expect, test } from "@playwright/test";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { startDashboardPagesPreview } from "../helpers/dashboard-pages-preview.mjs";
import { pageCapabilityAvailable } from "../../dashboard/site/src/view-availability.js";
import {
  dashboardAssessmentCleanupBudgetMs,
  dashboardAssessmentPageHash,
  dashboardAssessmentPageBudgetMs,
  dashboardAssessmentStartupBudgetMs,
  dashboardAssessmentTimeout,
  declaredDashboardViewIds,
  ignoredDashboardPageIds,
  isExpectedPageCloseAbort,
  isIgnoredDashboardPageId,
  isSpuriousAbortAfterSuccessResponse,
  renderAssessedDashboardQueryUsageGraph,
  visibleBusyViewSelector,
  visibleLoadingViewSelector,
  visibleViewSelector,
} from "./dashboard-view-assessment.mjs";
import {
  dashboardPageChunkPath,
  mergeDashboardPage,
  normalizeDashboardPageChunk,
} from "../../dashboard/site/src/dashboard-chunks.js";

const outputDirectory = resolve(
  process.env.DASHBOARD_VIEWS_OUTPUT_DIR || "test-results/dashboard-views",
);
const maximumDomNodes = 6_000;
const maximumFailedViews = 5;

function selectedPages(dashboard) {
  const selected = process.env.DASHBOARD_PAGE_IDS;
  const pages = dashboard.dashboard.pages.filter((page) => !isIgnoredDashboardPageId(page.id) && pageCapabilityAvailable(page));
  if (selected === undefined) return pages;
  const pageIds = new Set(selected.split(",").filter(Boolean));
  return pages.filter((page) => pageIds.has(page.id));
}

function messageText(value) {
  return value instanceof Error ? value.message : String(value);
}

async function loadPageDefinition(previewUrl, pageDefinition) {
  const chunkPath = dashboardPageChunkPath(pageDefinition);
  if (!chunkPath) return { page: pageDefinition, queries: [] };
  const response = await fetch(new URL(chunkPath, `${previewUrl.replace(/\/$/, "")}/`));
  if (!response.ok) {
    throw new Error(
      `Unable to load dashboard page definition "${pageDefinition.id}": HTTP ${response.status}.`,
    );
  }
  const chunk = normalizeDashboardPageChunk(await response.json());
  return {
    page: mergeDashboardPage(pageDefinition, chunk.page),
    queries: chunk.queries,
  };
}

test("each selected dashboard view renders with synthetic enterprise demo data", async ({ context }, testInfo) => {
  const assessmentStartedAt = Date.now();
  await rm(outputDirectory, { force: true, recursive: true });
  await mkdir(outputDirectory, { recursive: true });

  const summary = {
    generatedAt: new Date().toISOString(),
    evidenceKind: "synthetic-enterprise-demo",
    evidenceLimits: "Browser UX only; not operational freshness or completeness evidence.",
    maximumDomNodes,
    selectionMode: process.env.DASHBOARD_PAGE_IDS === undefined ? "all" : "affected",
    ignoredPageIds: [...ignoredDashboardPageIds],
    selectedPageIds: [],
    results: [],
  };
  let preview;

  try {
    if (process.env.DASHBOARD_PAGE_IDS === "") return;
    preview = await startDashboardPagesPreview();
    const dashboardResponse = await fetch(`${preview.url}/dashboard.json`);
    if (!dashboardResponse.ok) {
      throw new Error(`Unable to load composed dashboard.json: HTTP ${dashboardResponse.status}.`);
    }
    const dashboard = await dashboardResponse.json();
    summary.unavailableCapabilities = dashboard.dashboard.pages.filter(page => !pageCapabilityAvailable(page))
      .map(page => ({ pageId: page.id, capability: page.requires.capability }));
    const pageChunks = await Promise.all(
      selectedPages(dashboard).map((page) => loadPageDefinition(preview.url, page)),
    );
    const pages = pageChunks.map((chunk) => chunk.page);
    summary.selectedPageIds = pages.map((page) => page.id);
    if (pages.length === 0) {
      if (summary.selectionMode === "affected") return;
      throw new Error("No selected page IDs exist in the composed dashboard.");
    }
    summary.queryUsageGraph = renderAssessedDashboardQueryUsageGraph(dashboard, pageChunks);
    const assessmentTimeoutMs = dashboardAssessmentTimeout(pages.length);
    const assessmentDeadline = assessmentStartedAt
      + assessmentTimeoutMs
      - dashboardAssessmentCleanupBudgetMs;
    test.setTimeout(assessmentTimeoutMs);

    for (const [pageIndex, pageDefinition] of pages.entries()) {
      const result = {
        pageId: pageDefinition.id,
        title: pageDefinition.title || pageDefinition.page || pageDefinition.id,
        status: "incomplete",
        domNodes: null,
        declaredViews: declaredDashboardViewIds(pageDefinition, dashboard.dashboard.views),
        renderedViews: [],
        missingViews: [],
        missingData: [],
        emptyViews: [],
        loadingViews: [],
        crashed: false,
        errors: [],
        assessmentErrors: [],
        failedRequests: [],
      };
      const availableRunBudgetMs = assessmentDeadline - Date.now();
      if (availableRunBudgetMs <= 0) {
        result.assessmentErrors.push(
          "Dashboard view assessment exhausted its run budget before this page could start.",
        );
        result.status = "failed";
        summary.results.push(result);
        break;
      }
      const assessedPage = await context.newPage();
      let closing = false;
      let crashed = false;
      const succeededRequests = new WeakSet();
      await assessedPage.setViewportSize({ width: 1440, height: 900 });
      await assessedPage.addInitScript(() => {
        window.__dashboardRefreshStatus = null;
        window.__dashboardPageRenderCounts = {};
        document.addEventListener("dashboard-data", (event) => {
          if (event.detail?.kind === "refresh") {
            window.__dashboardRefreshStatus = event.detail.status;
            if (event.detail.status === "completed") {
              window.__dashboardPageRenderCounts = {};
            }
          }
        });
        document.addEventListener("dashboard-render", (event) => {
          if (event.detail?.kind !== "page" || event.detail?.status !== "completed") return;
          const pageId = event.detail.pageId;
          window.__dashboardPageRenderCounts[pageId] =
            (window.__dashboardPageRenderCounts[pageId] || 0) + 1;
        });
      });
      assessedPage.on("crash", () => {
        crashed = true;
      });
      assessedPage.on("console", (message) => {
        if (message.type() === "error") result.errors.push(message.text());
      });
      assessedPage.on("pageerror", (error) => result.errors.push(error.message));
      assessedPage.on("requestfailed", (request) => {
        const errorText = request.failure()?.errorText || "failed";
        if (
          isExpectedPageCloseAbort(errorText, closing)
          || isSpuriousAbortAfterSuccessResponse(errorText, succeededRequests.has(request))
        ) return;
        result.failedRequests.push(
          `${request.method()} ${request.url()}: ${errorText}`,
        );
      });
      assessedPage.on("response", (response) => {
        if (response.status() >= 400) {
          result.failedRequests.push(`${response.status()} ${response.url()}`);
          return;
        }
        succeededRequests.add(response.request());
      });

      const assessPage = async () => {
        try {
          await assessedPage.goto(`${preview.url}/${dashboardAssessmentPageHash(pageDefinition, dashboard)}`, {
            waitUntil: "domcontentloaded",
          });
          const dashboardRoot = assessedPage.locator(".dashboard-root");
          const activePage = assessedPage.locator(`[data-page-id="${pageDefinition.id}"]`);
          await expect(dashboardRoot).toBeVisible();
          // Each new document refreshes independently, even when IndexedDB is warm.
          await assessedPage.waitForFunction(() =>
            ["completed", "failed"].includes(window.__dashboardRefreshStatus),
          null, { timeout: dashboardAssessmentStartupBudgetMs });
          expect(
            await assessedPage.evaluate(() => window.__dashboardRefreshStatus),
            "The dashboard must complete its canonical data refresh",
          ).toBe("completed");
          await assessedPage.waitForFunction((pageId) =>
            (window.__dashboardPageRenderCounts[pageId] || 0) >= 1,
          pageDefinition.id, {
            timeout: dashboardAssessmentPageBudgetMs,
          });
          await expect(dashboardRoot).not.toHaveAttribute("aria-busy", "true", { timeout: 120_000 });
          await expect(activePage).toBeVisible();
          await expect(activePage).not.toHaveAttribute("data-page-pending", "", { timeout: 120_000 });
          await expect(activePage).not.toHaveAttribute("aria-busy", "true", { timeout: 120_000 });
          await expect(activePage).not.toHaveAttribute("data-page-prerequisite-unavailable", "true");
          await activePage.locator("details.view-disclosure").evaluateAll((disclosures) => {
            for (const disclosure of disclosures) disclosure.open = true;
          });

          // Rendered-view accounting covers every declared view, including views the
          // page's view-mode selection currently hides; only visible views are scrolled
          // into view and awaited.
          const renderedViewElements = activePage.locator("[data-view-id]");
          const visibleViews = activePage.locator(visibleViewSelector);
          for (let index = 0; index < await visibleViews.count(); index += 1) {
            await visibleViews.nth(index).scrollIntoViewIfNeeded().catch(() => {});
          }
          const busyViews = activePage.locator(visibleBusyViewSelector);
          const hydrationDeadline = Date.now() + 30_000;
          while (await busyViews.count() > 0 && Date.now() < hydrationDeadline) {
            await busyViews.first().scrollIntoViewIfNeeded().catch(() => {});
            await assessedPage.waitForTimeout(100);
          }
          await expect(busyViews).toHaveCount(0);
          await expect(activePage.locator(visibleLoadingViewSelector)).toHaveCount(0);

          result.renderedViews = (await renderedViewElements.evaluateAll((elements) =>
            elements.map((element) => element.getAttribute("data-view-id")).filter(Boolean)
          ));
          result.missingViews = result.declaredViews.filter(
            (viewId) => !result.renderedViews.includes(viewId),
          );
          result.missingData = await activePage.locator('[aria-label^="Unable to load "]')
            .evaluateAll((elements) => elements.map((element) => element.getAttribute("aria-label")));
          result.loadingViews = await activePage.locator(visibleLoadingViewSelector)
            .evaluateAll((elements) => elements.map((element) =>
              element.closest("[data-view-id]")?.getAttribute("data-view-id") || "page"
            ));
          result.emptyViews = await activePage.locator(".empty:visible, .empty-message:visible")
            .evaluateAll(elements => elements.map(element => ({
              view: element.closest("[data-view-id]")?.getAttribute("data-view-id"),
              message: element.textContent,
            })));
          result.domNodes = await assessedPage.locator("*").count();
          result.crashed = crashed;
          result.status = (
            !crashed
            && result.errors.length === 0
            && result.failedRequests.length === 0
            && result.missingViews.length === 0
            && result.missingData.length === 0
            && result.loadingViews.length === 0
            && result.domNodes <= maximumDomNodes
          ) ? "passed" : "failed";
        } catch (error) {
          if (!closing) result.assessmentErrors.push(messageText(error));
          result.crashed = crashed;
          result.status = "failed";
        }
      };
      const remainingRunBudgetMs = assessmentDeadline - Date.now();
      if (remainingRunBudgetMs <= 0) {
        closing = true;
        await assessedPage.close();
        result.assessmentErrors.push(
          "Dashboard view assessment exhausted its run budget during page setup.",
        );
        result.status = "failed";
        summary.results.push(result);
        break;
      }
      const pageTimeoutMs = Math.min(
        dashboardAssessmentPageBudgetMs
          + (pageIndex === 0 ? dashboardAssessmentStartupBudgetMs : 0),
        remainingRunBudgetMs,
      );
      const assessment = assessPage();
      let timeout;
      const outcome = await Promise.race([
        assessment.then(() => "completed"),
        new Promise((resolve) => {
          timeout = setTimeout(() => resolve("timed-out"), pageTimeoutMs);
        }),
      ]);
      clearTimeout(timeout);
      if (outcome === "timed-out") {
        result.assessmentErrors.push(
          `Dashboard page assessment exceeded its ${pageTimeoutMs}ms budget.`,
        );
        result.status = "failed";
      }
      closing = true;
      await assessedPage.close();
      await assessment;
      summary.results.push(result);
      if (summary.results.filter((entry) => entry.status !== "passed").length >= maximumFailedViews) {
        break;
      }
    }
    for (const result of summary.results) {
      if (result.status === "passed"
          && (
            result.errors.length > 0
            || result.assessmentErrors.length > 0
            || result.failedRequests.length > 0
          )) {
        result.status = "failed";
      }
    }
  } catch (error) {
    summary.blocker = messageText(error);
  } finally {
    await writeFile(
      join(outputDirectory, "summary.json"),
      `${JSON.stringify(summary, null, 2)}\n`,
    );
    await preview?.close();
  }

  await testInfo.attach("dashboard-views-summary", {
    path: join(outputDirectory, "summary.json"),
    contentType: "application/json",
  });
  expect(summary.blocker, "The dashboard view assessment was blocked").toBeUndefined();
  expect(
    summary.results.filter((result) => result.status !== "passed"),
    "Every selected dashboard view must render within its DOM budget without crashes or missing data",
  ).toEqual([]);
});
