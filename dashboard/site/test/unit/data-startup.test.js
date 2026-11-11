import { beforeEach, describe, expect, it, vi } from "vitest";

/** @type {string[]} */
const calls = [];
const dataProcessor = vi.hoisted(() => ({
  loadDashboardSnapshotMetadata: vi.fn(),
  loadCanonicalDashboardPage: vi.fn(),
  loadCanonicalDashboardSources: vi.fn(),
  refreshCanonicalDashboardSources: vi.fn(),
  subscribeDatabaseUpgrade: vi.fn(),
  subscribeCanonicalDashboardView: vi.fn(),
  subscribeWorkerLoadingProgress: vi.fn(),
}));
const updates = vi.hoisted(() => ({
  DASHBOARD_REFRESH_REQUEST_EVENT: "dashboard-refresh-request",
  startAutomaticDashboardDataUpdates: vi.fn(),
}));
const deployment = vi.hoisted(() => ({ DEMO_MODE: false }));

vi.mock("../../src/data-processor.js", () => dataProcessor);
vi.mock("../../src/dashboard-data-updates.js", () => updates);
vi.mock("../../src/deployment-profile.js", () => deployment);

import { createBatchedSourceLoader, startDashboardData } from "../../src/data/startup.js";
import { dashboardViewAliasName } from "../../src/data/queries/view-payload-compiler.js";
import { browserFirstLoad, preparationStorage } from "../../src/browser-first-load.js";
import { dashboardDataStatus } from "../../src/dashboard-data-status.js";

const cachedSources = {
  runs: { source: "runs", rows: [{ run: "cached" }] },
};

/** @param {Record<string, unknown>} [overrides] */
function options(overrides = {}) {
  let renderedPage = false;
  const browserWindow = new EventTarget();
  Object.defineProperty(browserWindow, "navigator", {
    value: { storage: { persisted: async () => true } },
    configurable: true
  });
  return {
    browserWindow: /** @type {Window} */ (/** @type {unknown} */ (browserWindow)),
    document,
    sourceUrl: "https://example.test/dashboard/payload-hashes.json",
    dashboardContext: { pages: [], queries: [] },
    pageSourceNames: () => ["runs"],
    pagePaginatedSourceBindings: () => ({}),
    render: (
      /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ _sources,
      /** @type {'ready' | 'loading' | 'cached' | 'stale'} */ state,
      /** @type {(pageId: string, options: { signal: AbortSignal, onUpdate: () => void }) => Promise<unknown>} */ loadPageSources,
    ) => {
      calls.push(`render:${state}`);
      if (!renderedPage && state !== "loading") {
        renderedPage = true;
        void loadPageSources("overview", {
          signal: new AbortController().signal,
          onUpdate: () => {},
        }).catch(() => {});
      }
    },
    settleUi: async () => {
      calls.push("settle");
    },
    ...overrides,
  };
}

describe("dashboard data startup", () => {
  beforeEach(() => {
    calls.length = 0;
    vi.clearAllMocks();
    deployment.DEMO_MODE = false;
    browserFirstLoad.set({ status: "inactive", dismissed: false });
    dataProcessor.subscribeDatabaseUpgrade.mockReturnValue(() => {});
    dataProcessor.subscribeWorkerLoadingProgress.mockReturnValue(() => {});
    document.head.replaceChildren();
    document.body.replaceChildren();
    dataProcessor.loadCanonicalDashboardPage.mockImplementation(async (sourceNames = ["runs"]) => {
      if (sourceNames.length === 0) return {};
      calls.push("cache");
      return cachedSources;
    });
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue({
      createdAt: "2026-09-28T12:00:00.000Z",
    });

    dataProcessor.subscribeCanonicalDashboardView.mockImplementation(
      (_id, _sources, _context, listener, _pagination, options) => {
        void dataProcessor.loadCanonicalDashboardPage().then(listener, options.onError);
        return () => {};
      },
    );
    dataProcessor.refreshCanonicalDashboardSources.mockImplementation(() => {
      calls.push("refresh");
      return new Promise(() => {});
    });
    updates.startAutomaticDashboardDataUpdates.mockImplementation(() => {
      calls.push("automatic");
      return () => {};
    });
  });

  it("loads source requests from the same view in one worker query", async () => {
    const runs = { source: "runs", rows: [{ run: "1" }] };
    const outcomes = { source: "outcomes", rows: [{ outcome: "1" }] };
    const runsAlias = dashboardViewAliasName("overview", { id: "overview-floor" }, 0, "runs", 0);
    const outcomesAlias = dashboardViewAliasName("overview", { id: "overview-floor" }, 0, "outcomes", 1);
    dataProcessor.loadCanonicalDashboardPage.mockResolvedValue({
      [runsAlias]: runs,
      [outcomesAlias]: outcomes,
    });
    const loader = createBatchedSourceLoader({ pages: [], queries: [] });

    const [loadedRuns, loadedOutcomes] = await Promise.all([
      loader("runs", { pageId: "overview", viewId: "overview-floor", sourceIndex: 0 }),
      loader("outcomes", { pageId: "overview", viewId: "overview-floor", sourceIndex: 1 }),
    ]);

    expect(dataProcessor.loadCanonicalDashboardPage).toHaveBeenCalledOnce();
    expect(dataProcessor.loadCanonicalDashboardPage).toHaveBeenCalledWith(
      ["runs", "outcomes"],
      { pages: [], queries: [] },
      undefined,
      {
        pageId: "overview",
        viewId: "overview-floor",
        queryContext: undefined,
      },
    );
    expect(loadedRuns).toBe(runs);
    expect(loadedOutcomes).toBe(outcomes);
  });

  it("loads source requests from different views on the same page in one worker query", async () => {
    const status = { source: "status", rows: [{ status: "healthy" }] };
    const campaigns = { source: "campaigns", rows: [{ campaign: "doctor" }] };
    const statusAlias = dashboardViewAliasName("overview", { id: "overview-header" }, 0, "status", 0);
    const campaignsAlias = dashboardViewAliasName("overview", { id: "overview-campaigns" }, 0, "campaigns", 0);
    dataProcessor.loadCanonicalDashboardPage.mockResolvedValue({
      [statusAlias]: status,
      [campaignsAlias]: campaigns,
    });
    const loader = createBatchedSourceLoader({ pages: [], queries: [] });

    const [loadedStatus, loadedCampaigns] = await Promise.all([
      loader("status", { pageId: "overview", viewId: "overview-header", sourceIndex: 0 }),
      loader("campaigns", { pageId: "overview", viewId: "overview-campaigns", sourceIndex: 0 }),
    ]);

    expect(dataProcessor.loadCanonicalDashboardPage).toHaveBeenCalledOnce();
    expect(dataProcessor.loadCanonicalDashboardPage).toHaveBeenCalledWith(
      ["status", "campaigns"],
      { pages: [], queries: [] },
      undefined,
      {
        pageId: "overview",
        viewId: undefined,
        queryContext: undefined,
      },
    );
    expect(loadedStatus).toBe(status);
    expect(loadedCampaigns).toBe(campaigns);
  });

  it("rejects every request when a batch cannot be grouped", async () => {
    const loader = createBatchedSourceLoader({ pages: [], queries: [] });
    const circularContext = {};
    circularContext.filters = circularContext;

    await expect(Promise.all([
      loader("runs", { queryContext: /** @type {never} */ (circularContext) }),
      loader("outcomes", { queryContext: /** @type {never} */ (circularContext) }),
    ])).rejects.toThrow("circular");
  });

  it("warns about data loss in private or best-effort storage and cleans up on disposal", async () => {
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue(null);
    const config = options();
    Object.defineProperty(config.browserWindow, "navigator", {
      value: { storage: { persisted: async () => false } }
    });
    const stop = await startDashboardData(config);
    await vi.waitFor(() => expect(document.querySelector(".dashboard-notification-warning")?.textContent)
      .toContain("data will be lost when the private session ends"));
    stop();
    await vi.waitFor(() => expect(document.querySelector(".dashboard-notification-warning")).toBeNull());
  });

  it("does not warn when browser storage is persistent", async () => {
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue(null);
    const config = options();
    Object.defineProperty(config.browserWindow, "navigator", {
      value: { storage: { persisted: async () => true } }
    });
    const stop = await startDashboardData(config);
    await Promise.resolve();
    expect(document.querySelector(".dashboard-notification-warning")).toBeNull();
    stop();
  });

  it.each([false, null])("does not warn for synthetic enterprise data when persistence is %s", async (persisted) => {
    deployment.DEMO_MODE = true;
    const config = options();
    Object.defineProperty(config.browserWindow, "navigator", {
      value: { storage: { persisted: async () => persisted } }
    });
    const stop = await startDashboardData(config);
    await vi.waitFor(() => expect(preparationStorage.get()).toMatchObject({ persisted }));
    expect(document.querySelector('[data-notification-id="dashboard:browser-storage"]')).toBeNull();
    stop();
  });

  it("renders cached data and lets the UI settle before any download starts", async () => {
    await startDashboardData(options());

    expect(document.querySelector("dialog")).toBeNull();
    expect(dashboardDataStatus.get()).toEqual({
      phase: "cached", snapshot: { createdAt: "2026-09-28T12:00:00.000Z" }
    });
    expect(dataProcessor.subscribeWorkerLoadingProgress).not.toHaveBeenCalled();
    expect(calls).toEqual([
      "render:cached",
      "settle",
      "cache",
      "automatic",
      "refresh",
    ]);
  });

  it("resolves shell notice sources before the first render, independently of page preparation", async () => {
    const notices = { freshness: { source: "freshness", rows: [{ "latest-data-at": 1 }] } };
    dataProcessor.loadCanonicalDashboardPage.mockImplementation(async (names) => names.length ? notices : {});
    const render = vi.fn();
    const context = {
      pages: [], queries: [{ name: "freshness", from: "runs" }],
      callouts: [{
        id: "old-data", title: "Old data", description: "Inspect ingestion.",
        "visible-when": { source: "freshness", field: "latest-data-at", "older-than-hours": 24 }
      }]
    };
    const stop = await startDashboardData(options({ dashboardContext: context, render }));
    expect(dataProcessor.loadCanonicalDashboardPage).toHaveBeenCalledWith(["freshness"], context);
    expect(render.mock.calls[0][0]).toBe(notices);
    expect(render.mock.calls[0][1]).toBe("cached");
    stop();
  });

  it("reports failed background updates once and clears the notice when the subscription recovers", async () => {
    /** @type {((sources: Record<string, import('../../src/presenter.js').LogicalSourceInput>) => void) | undefined} */
    let deliver;
    /** @type {((error: Error) => void) | undefined} */
    let fail;
    dataProcessor.subscribeCanonicalDashboardView.mockImplementation(
      (_id, _names, _context, listener, _pagination, subscriptionOptions) => {
        deliver = listener;
        fail = subscriptionOptions.onError;
        listener(cachedSources);
        return () => {};
      },
    );
    const owner = new AbortController();
    const stop = await startDashboardData(options({
      render: /** @type {Parameters<typeof startDashboardData>[0]['render']} */ ((
        _sources, _state, loadPageSources,
      ) => {
        void loadPageSources.subscribeBackgroundSources?.(["runs"], {
          signal: owner.signal,
          onUpdate: () => {},
        });
      }),
    }));
    await vi.waitFor(() => expect(deliver).toBeDefined());
    fail?.(new Error("private error with token"));
    fail?.(new Error("private error with token"));
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(1);
    expect(document.querySelector(".dashboard-notification-action")?.textContent).toBe("Reload dashboard");
    expect(document.body.textContent).not.toContain("private error");
    deliver?.({});
    expect(document.querySelector(".dashboard-notification-exit")).not.toBeNull();
    fail?.(new Error("private error with token"));
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(1);
    owner.abort();
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(0);
    stop();
  });

  it("keeps a cold start in an accessible loading state until the first complete snapshot", async () => {
    dataProcessor.loadDashboardSnapshotMetadata
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce({ createdAt: "2026-09-28T12:00:00.000Z" });
    dataProcessor.refreshCanonicalDashboardSources.mockResolvedValue({ changed: false });

    await startDashboardData(options());
    await vi.waitFor(() => expect(calls).toContain("render:ready"));

    expect(calls).toContain("render:loading");
    expect(dataProcessor.loadDashboardSnapshotMetadata).toHaveBeenCalledTimes(2);
    expect(browserFirstLoad.get().status).toBe("inactive");
    expect(dashboardDataStatus.get().phase).toBe("ready");
    expect(document.querySelector("dialog")).toBeNull();
  });

  it("shows the first-import screen only without a complete browser snapshot and cleans up on stop", async () => {
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue(null);
    const stopProgress = vi.fn();
    dataProcessor.subscribeWorkerLoadingProgress.mockReturnValue(stopProgress);
    const stop = await startDashboardData(options());
    expect(document.querySelector("dialog")?.open).toBe(true);
    expect(browserFirstLoad.get().status).toBe("loading");
    expect(dashboardDataStatus.get()).toEqual({ phase: "loading", snapshot: null });
    const progress = dataProcessor.subscribeWorkerLoadingProgress.mock.calls[0]?.[0];
    progress({ id: "ingestion", phase: "update", completed: 1, total: 4 });
    expect(browserFirstLoad.get()).toMatchObject({ completed: 1, total: 4 });
    progress({ id: "ingestion", phase: "complete" });
    expect(browserFirstLoad.get().status).toBe("loading");
    stop();
    expect(stopProgress).toHaveBeenCalledOnce();
    expect(document.querySelector("dialog")).toBeNull();
    expect(browserFirstLoad.get().status).toBe("inactive");
  });

  it("opens the import dialog immediately on a database upgrade, before the upgrade or UI settles", async () => {
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue(null);
    let finishUpgrade = () => {};
    dataProcessor.loadCanonicalDashboardPage.mockImplementationOnce(() => new Promise((resolve) => {
      finishUpgrade = () => resolve({});
    }));
    /** @type {(versions: { oldVersion: number, newVersion: number }) => void} */
    let notifyUpgrade = () => {};
    const stopUpgrade = vi.fn();
    dataProcessor.subscribeDatabaseUpgrade.mockImplementation((notify) => {
      notifyUpgrade = notify;
      return stopUpgrade;
    });
    const startup = startDashboardData(options());
    expect(document.querySelector('dialog')).toBeNull();
    notifyUpgrade({ oldVersion: 36, newVersion: 37 });
    notifyUpgrade({ oldVersion: 36, newVersion: 37 });
    const dialog = document.querySelector('dialog');
    expect(dialog?.open).toBe(true);
    expect(dialog?.textContent).toContain('We are refreshing your browser copy for this version');
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Updating the local database');
    expect(calls).not.toContain("settle");
    /** @type {HTMLButtonElement | null} */ (dialog?.querySelector('button.first-load-browse'))?.click();
    expect(dialog?.open).toBe(false);
    finishUpgrade();
    const stop = await startup;
    expect(stopUpgrade).toHaveBeenCalledOnce();
    expect(browserFirstLoad.get()).toMatchObject({ status: "loading", dismissed: true, reason: "upgrade", oldVersion: 36, newVersion: 37 });
    expect(document.querySelectorAll('dialog')).toHaveLength(1);
    stop();
    expect(document.querySelector('dialog')).toBeNull();
  });

  it("closes the early dialog if an upgrade retains a complete snapshot", async () => {
    dataProcessor.loadCanonicalDashboardPage.mockImplementationOnce(async () => {
      dataProcessor.subscribeDatabaseUpgrade.mock.calls[0][0]();
      return {};
    });
    await startDashboardData(options());
    expect(document.querySelector('dialog')).toBeNull();
    expect(browserFirstLoad.get().status).toBe("inactive");
    expect(dataProcessor.subscribeWorkerLoadingProgress).not.toHaveBeenCalled();
  });

  it("cleans up the early dialog if opening the upgraded database fails", async () => {
    dataProcessor.loadCanonicalDashboardPage.mockImplementationOnce(async () => {
      dataProcessor.subscribeDatabaseUpgrade.mock.calls[0][0]();
      throw new Error("Open failed");
    });
    await expect(startDashboardData(options())).rejects.toThrow("Open failed");
    expect(document.querySelector('dialog')).toBeNull();
    expect(browserFirstLoad.get().status).toBe("inactive");
  });

  it("automatically removes an open import screen and releases progress when the first snapshot loads", async () => {
    dataProcessor.loadDashboardSnapshotMetadata
      .mockResolvedValueOnce(null)
      .mockResolvedValue({ createdAt: "2026-09-28T12:00:00.000Z" });
    let finishImport = () => {};
    dataProcessor.refreshCanonicalDashboardSources.mockImplementation(() => new Promise((resolve) => {
      finishImport = () => resolve({ changed: true });
    }));
    const stopProgress = vi.fn();
    dataProcessor.subscribeWorkerLoadingProgress.mockReturnValue(stopProgress);
    const startupOptions = options();
    const stop = await startDashboardData(startupOptions);
    expect(document.querySelector("dialog")?.open).toBe(true);
    finishImport();
    await vi.waitFor(() => expect(document.querySelector("dialog")).toBeNull());
    expect(browserFirstLoad.get().status).toBe("inactive");
    expect(stopProgress).toHaveBeenCalledOnce();
    dataProcessor.refreshCanonicalDashboardSources.mockResolvedValue({ changed: false });
    startupOptions.browserWindow.dispatchEvent(new Event("dashboard-refresh-request"));
    await vi.waitFor(() => expect(dataProcessor.loadDashboardSnapshotMetadata).toHaveBeenCalledTimes(3));
    expect(document.querySelector("dialog")).toBeNull();
    expect(stopProgress).toHaveBeenCalledOnce();
    stop();
    expect(stopProgress).toHaveBeenCalledOnce();
  });

  it("keeps failed and cancelled first imports incomplete and supports retry without reopening a dismissed screen", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue(null);
    const cancellation = new Error("Data ingestion cancelled.");
    cancellation.name = "DataProcessingCancelledError";
    dataProcessor.refreshCanonicalDashboardSources
      .mockRejectedValueOnce(cancellation)
      .mockResolvedValueOnce({ changed: true });
    const startupOptions = options();
    const stop = await startDashboardData(startupOptions);
    await vi.waitFor(() => expect(browserFirstLoad.get().status).toBe("failed"));
    expect(dashboardDataStatus.get()).toEqual({ phase: "stale", snapshot: null });
    expect(document.querySelector("dialog")?.textContent).toContain("could not finish");
    browserFirstLoad.set((current) => ({ ...current, dismissed: true }));
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue({ createdAt: "2026-09-28T12:00:00.000Z" });
    startupOptions.browserWindow.dispatchEvent(new Event("dashboard-refresh-request"));
    expect(browserFirstLoad.get().status).toBe("loading");
    expect(dashboardDataStatus.get().phase).toBe("loading");
    expect(document.querySelector("dialog")?.open).toBe(false);
    await vi.waitFor(() => expect(browserFirstLoad.get().status).toBe("inactive"));
    expect(dashboardDataStatus.get().phase).toBe("ready");
    stop();
    vi.restoreAllMocks();
  });

  it("does not claim readiness if snapshot metadata cannot confirm a complete first import", async () => {
    const errorLog = vi.spyOn(console, "error").mockImplementation(() => {});
    dataProcessor.loadDashboardSnapshotMetadata
      .mockResolvedValueOnce(null)
      .mockRejectedValueOnce(new Error("Snapshot metadata unavailable"));
    dataProcessor.refreshCanonicalDashboardSources.mockResolvedValue({ changed: false });
    const stop = await startDashboardData(options());
    await vi.waitFor(() => expect(browserFirstLoad.get().status).toBe("failed"));
    expect(calls).not.toContain("render:ready");
    expect(errorLog).toHaveBeenCalled();
    stop();
    vi.restoreAllMocks();
  });

  it("does not show a browser import screen for a backend without a local snapshot", async () => {
    const marker = document.createElement("meta");
    marker.name = "dashboard-data-backend";
    marker.content = "server-http";
    document.head.append(marker);
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue(null);
    const stop = await startDashboardData(options());
    expect(calls).toContain("render:cached");
    expect(document.querySelector("dialog")).toBeNull();
    expect(browserFirstLoad.get().status).toBe("inactive");
    expect(dataProcessor.subscribeWorkerLoadingProgress).not.toHaveBeenCalled();
    stop();
  });

  it("publishes hosted refresh completion without requiring a local snapshot or a page rerender", async () => {
    const marker = document.createElement("meta");
    marker.name = "dashboard-data-backend";
    marker.content = "server-http";
    document.head.append(marker);
    dataProcessor.loadDashboardSnapshotMetadata.mockResolvedValue(null);
    dataProcessor.refreshCanonicalDashboardSources.mockResolvedValue({ changed: true });
    const stop = await startDashboardData(options());
    await vi.waitFor(() => expect(dashboardDataStatus.get()).toEqual({ phase: "ready", snapshot: null }));
    expect(calls).not.toContain("render:ready");
    stop();
  });

  it("treats a successfully completed empty snapshot as ready", async () => {
    dataProcessor.loadDashboardSnapshotMetadata
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce({ createdAt: "2026-09-28T12:00:00.000Z" });
    dataProcessor.refreshCanonicalDashboardSources.mockResolvedValue({ changed: false });
    /** @type {Array<{ state: string, snapshot?: { createdAt: string } | null }>} */
    const renders = [];

    await startDashboardData(options({
      render: (
        /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ _sources,
        /** @type {'ready' | 'loading' | 'cached' | 'stale'} */ state,
        /** @type {(pageId: string, options: { signal: AbortSignal, onUpdate: () => void }) => Promise<unknown>} */ _loadPageSources,
        /** @type {(() => void) | undefined} */ _retryRefresh,
        /** @type {{ createdAt: string } | null | undefined} */ snapshot,
      ) => renders.push({ state, snapshot }),
    }));
    await vi.waitFor(() => expect(renders.some(({ state }) => state === "ready")).toBe(true));

    expect(renders[0]?.state).toBe("loading");
    expect(renders.at(-1)).toEqual({
      state: "ready",
      snapshot: { createdAt: "2026-09-28T12:00:00.000Z" },
    });
  });

  it("keeps the last complete snapshot visible and dated during refresh", async () => {
    /** @type {Array<{ state: string, snapshot?: { createdAt: string } | null }>} */
    const renders = [];
    await startDashboardData(options({
      render: (
        /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ _sources,
        /** @type {'ready' | 'loading' | 'cached' | 'stale'} */ state,
        /** @type {(pageId: string, options: { signal: AbortSignal, onUpdate: () => void }) => Promise<unknown>} */ _loadPageSources,
        /** @type {(() => void) | undefined} */ _retryRefresh,
        /** @type {{ createdAt: string } | null | undefined} */ snapshot,
      ) => renders.push({ state, snapshot }),
    }));

    expect(renders).toEqual([{
      state: "cached",
      snapshot: { createdAt: "2026-09-28T12:00:00.000Z" },
    }]);
    expect(calls).toContain("refresh");
  });

  it("releases background queries only after UI-bound startup work is registered", async () => {
    /** @type {() => void} */
    let settle = () => {};
    const settled = new Promise((resolve) => {
      settle = () => resolve(undefined);
    });
    dataProcessor.subscribeCanonicalDashboardView.mockImplementation(
      (id, _sources, _context, listener) => {
        calls.push(`subscribe:${id}`);
        listener({});
        return () => {};
      },
    );
    const startup = startDashboardData(options({
      settleUi: () => settled,
      render: /** @type {Parameters<typeof startDashboardData>[0]['render']} */ ((
        _sources,
        state,
        loadPageSources,
      ) => {
        calls.push(`render:${state}`);
        if (state !== "cached") return;
        void loadPageSources("overview", {
          signal: new AbortController().signal,
          onUpdate: () => {},
        });
        void loadPageSources.subscribeBackgroundSources?.(["maintenance-campaign-updates"], {
          signal: new AbortController().signal,
          onUpdate: () => {},
        });
        void loadPageSources.subscribeBackgroundSources?.(["maintenance-campaign-updates"], {
          signal: new AbortController().signal,
          onUpdate: () => {},
        });
      }),
    }));

    await vi.waitFor(() => expect(calls).toContain("subscribe:page:overview"));
    expect(calls.some((call) => call.startsWith("subscribe:sources:maintenance-campaign-updates:"))).toBe(false);
    expect(calls).not.toContain("refresh");

    settle();
    await startup;

    expect(calls.indexOf("subscribe:page:overview"))
      .toBeLessThan(calls.findIndex((call) => call.startsWith("subscribe:sources:maintenance-campaign-updates:")));
    const backgroundSubscriptions = calls.filter((call) => call.startsWith("subscribe:sources:maintenance-campaign-updates:"));
    expect(backgroundSubscriptions).toHaveLength(2);
    expect(new Set(backgroundSubscriptions).size).toBe(2);
    expect(calls.indexOf("subscribe:page:overview")).toBeLessThan(calls.indexOf("refresh"));
  });

  it("starts ingestion after settling when an empty cache emits no page snapshot", async () => {
    dataProcessor.subscribeCanonicalDashboardView.mockImplementation(() => () => {});

    await startDashboardData(options());

    expect(calls).toEqual([
      "render:cached",
      "settle",
      "automatic",
      "refresh",
    ]);
  });

  it("downloads current deployed data when a refresh is requested", async () => {
    dataProcessor.refreshCanonicalDashboardSources.mockResolvedValue({ changed: false });
    const startupOptions = options();
    await startDashboardData(startupOptions);
    await vi.waitFor(() => expect(dataProcessor.loadDashboardSnapshotMetadata).toHaveBeenCalledTimes(2));
    await Promise.resolve();
    dataProcessor.refreshCanonicalDashboardSources.mockClear();

    startupOptions.browserWindow.dispatchEvent(new Event("dashboard-refresh-request"));

    expect(dataProcessor.refreshCanonicalDashboardSources).toHaveBeenCalledOnce();
  });

  it("keeps remote navigation mounted when the revision is unchanged", async () => {
    const marker = document.createElement("meta");
    marker.name = "dashboard-data-backend";
    marker.content = "server-http";
    document.head.append(marker);
    const root = document.createElement("div");
    root.id = "root";
    const dashboard = document.createElement("div");
    dashboard.className = "dashboard-root dashboard-refreshing";
    dashboard.setAttribute("aria-busy", "true");
    root.append(dashboard);
    document.body.append(root);
    dataProcessor.refreshCanonicalDashboardSources.mockResolvedValue({ changed: false });

    await startDashboardData(options({
      render: (
        /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ _sources,
        /** @type {'ready' | 'loading' | 'cached' | 'stale'} */ state,
      ) => calls.push(`render:${state}`),
    }));
    await vi.waitFor(() => expect(dataProcessor.refreshCanonicalDashboardSources).toHaveBeenCalled());
    await vi.waitFor(() => expect(dashboard.classList.contains("dashboard-refreshing")).toBe(false));

    expect(calls).toEqual(["render:cached", "settle"]);
    expect(root.firstElementChild).toBe(dashboard);
    expect(dashboard.hasAttribute("aria-busy")).toBe(false);
    expect(updates.startAutomaticDashboardDataUpdates).not.toHaveBeenCalled();
  });

  it.each([false, true])("preserves the initial compilation scope for continuation loads (view binding: %s)", async (viewBinding) => {
    const alias = "view:runs:timeline:runs-table";
    const page = {
      source: alias,
      rows: [{ run: "2" }],
      continuationToken: "next",
      metadata: { "total-row-count": 2 },
    };
    /** @type {import('../../src/presenter.js').PageSourceLoader | undefined} */
    let loadPageSources;
    dataProcessor.subscribeCanonicalDashboardView.mockImplementation(
      (_id, _sources, _context, listener) => {
        listener({ [alias]: page });
        return () => {};
      },
    );
    dataProcessor.loadCanonicalDashboardPage.mockResolvedValue({
      [alias]: {
        ...page,
        rows: [{ run: "1" }],
        continuationToken: undefined,
      },
    });

    await startDashboardData(options({
      pageSourceNames: () => ["runs-table", "navigation-summary"],
      pagePaginatedSourceBindings: () => ({
        [alias]: { sourceName: "runs-table", viewId: "timeline" },
      }),
      render: (
        /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ _sources,
        /** @type {'ready' | 'loading' | 'cached' | 'stale'} */ state,
        /** @type {import('../../src/presenter.js').PageSourceLoader} */ loader,
      ) => {
        calls.push(`render:${state}`);
        loadPageSources = loader;
      },
    }));

    const loader = loadPageSources;
    if (!loader) throw new Error("Page source loader was not registered.");
    const pageOptions = {
      signal: new AbortController().signal,
      onUpdate: () => {},
    };
    const sources = await (viewBinding && loader.subscribeViewSources
      ? loader.subscribeViewSources("runs", "timeline", ["runs-table"], pageOptions)
      : loader("runs", pageOptions));
    const loadContinuation = sources[alias]?.loadContinuation;
    if (!loadContinuation) throw new Error("Continuation loader was not bound.");
    await loadContinuation("next");

    expect(dataProcessor.subscribeCanonicalDashboardView).toHaveBeenCalledWith(
      viewBinding ? "page:runs:view:timeline:1" : "page:runs",
      viewBinding ? ["runs-table"] : ["runs-table", "navigation-summary"],
      expect.anything(),
      expect.any(Function),
      { [alias]: { limit: 25 } },
      expect.objectContaining({ pageId: "runs" }),
    );
    expect(dataProcessor.loadCanonicalDashboardPage).toHaveBeenLastCalledWith(
      viewBinding ? ["runs-table"] : ["runs-table", "navigation-summary"],
      expect.anything(),
      { [alias]: { limit: 25, continuationToken: "next" } },
      expect.objectContaining({ pageId: "runs", viewId: viewBinding ? "timeline" : undefined }),
    );
  });
});
