import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

describe("live Dashboard Language sources", () => {
  it("always streams normalized JSONL response bodies", () => {
    const worker = readFileSync(resolve("src/data-worker.js"), "utf8");

    expect(worker).toContain("ingestNormalizedJsonl(");
    expect(worker).toContain("responseChunks(/** @type {ReadableStream<Uint8Array>} */ (response.body))");
    expect(worker).not.toContain("ingestNormalizedJson(indexedDB, await response.json()");
  });

  it("loads canonical data progressively with abort-scoped page subscriptions", () => {
    const shell = readFileSync(resolve("index.html"), "utf8");
    const main = readFileSync(resolve("src/main.js"), "utf8");
    const preview = readFileSync(resolve("src/dashboard-app.js"), "utf8");
    const startup = readFileSync(resolve("src/data/startup.js"), "utf8");
    const definition = readFileSync(resolve("src/dashboard-definition.js"), "utf8");

    expect(shell).toContain("script.src = './src/main.js';");
    expect(main).toContain("const remoteBackend = usesRemoteDataBackend(document)");
    expect(main).toContain("if (!window.indexedDB && !remoteBackend)");
    expect(main).toContain("if (remoteBackend) await disableRemoteDashboardPwa()");
    expect(main).toContain('renderIndexedDBUnsupported()');
    expect(main).toContain('await import("./dashboard-app.js")');
    expect(preview).toContain('await loadDashboardSchema()');
    expect(definition).toContain("fetch('./dashboard.json', { cache: 'no-store' })");
    expect(preview).toContain('await ensureDashboardPageLoaded(initialDashboardPageId());');
    expect(preview).toContain('renderSources({}, "loading")');
    expect(preview.indexOf('await ensureDashboardPageLoaded(initialDashboardPageId());'))
      .toBeLessThan(preview.indexOf('renderSources({}, "loading")'));
    expect(preview).toContain("subscribeWorkerLoadingProgress");
    expect(preview).toContain("setLoadingProgressState(document, state)");
    expect(preview).toContain('loading: state === "loading"');
    expect(preview).not.toContain("renderAgenticLoader");
    expect(preview).not.toContain("AGENTIC_LOADER");
    expect(preview).not.toContain("Loading dashboard data…");
    expect(preview).not.toContain("startLoadingProgress(document)");
    expect(preview).not.toContain("runWithLoadingProgress");
    expect(preview).not.toContain("loadingProgress.complete()");
    expect(startup).toContain("refreshCanonicalDashboardSources");
    expect(startup).not.toContain("runWithLoadingProgress");
    expect(startup).toContain("subscribeCanonicalDashboardView(");
    expect(startup).toContain("signal: pageOptions.signal");
    expect(startup).toContain("pageOptions.onUpdate(transformedSources)");
    expect(preview).toContain("dashboardPagePaginatedSourceBindings, dashboardPageSourceNames");
    expect(startup).not.toContain("drainSourceContinuation");
    expect(startup).not.toContain("drainChartSources");
    expect(startup).toContain("pageSourceNames(pageId, pageOptions.queryContext?.viewMode)");
    expect(startup).toContain("pagePaginatedSourceBindings(pageId)");
    expect(startup).toContain("refreshCanonicalDashboardSources(\n      sourceUrl,\n      []");
    expect(startup).not.toContain("loadInitialSources");
    expect(startup).toContain('render(shellSources, hasCompleteSnapshot ? "cached" : "loading", loadPageSources, undefined, snapshot)');
    expect(startup).toContain('render(shellSources, hasCompleteSnapshot ? "stale" : "loading", loadPageSources, refreshSources, snapshot)');
    expect(startup).not.toContain("initialPageLoaded");
    expect(startup.indexOf('render(shellSources, hasCompleteSnapshot ? "cached"')).toBeLessThan(startup.indexOf("await settleUi()"));
    expect(startup.indexOf("await settleUi()")).toBeLessThan(startup.indexOf("startAutomaticUpdates();", startup.indexOf("await settleUi()")));
    expect(preview).toContain("notifyRefreshError(retryRefresh, {");
    expect(startup).toContain("refreshSources");
    expect(startup).not.toContain("if (changed) return;");
    expect(preview).not.toContain("loadDashboardSources(fetch, sourceUrl)");
    expect(preview).not.toContain("ingestDashboardSources(window.indexedDB, sources");
    expect(preview).not.toContain('./source-cache.js');
    expect(preview).not.toContain("Showing cached data…");
    expect(preview).not.toContain("Showing cached data while loading the latest dashboard data…");
    expect(preview).toContain('loadDashboardQuerySources(sources, {');
    expect(preview).not.toContain('window.indexedDB');
    expect(preview).toContain('has("fixtures")');
    expect(preview).toContain("Unable to load live dashboard data:");
    expect(preview).not.toContain('dashboard-preview-update');
    expect(preview).not.toContain('get("local-preview")');
    expect(preview).not.toContain('viewer.json');
    expect(preview).not.toContain('localViewer');
    expect(preview).not.toContain('__dashboard_socket');
    expect(preview).not.toContain("copilot-prompt");
    expect(preview).not.toContain("dashboard-copilot");
    expect(preview).not.toContain("Retain the illustrative fixture data");
    expect(preview).not.toContain("loadCanonicalDashboardSources");
    expect(preview).not.toContain("refreshCanonicalDashboardSources");
    expect(startup).not.toContain("renderDashboard");
  });
});
