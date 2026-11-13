import { afterEach, describe, expect, it, vi } from "vitest";
import {
  DashboardServerError,
  disableRemoteDashboardPwa,
  queryRemoteDashboard,
  queryRemoteRepositoryMemory,
  refreshRemoteDashboard,
  subscribeRemoteRevision,
  usesRemoteDataBackend,
} from "../../src/remote-data-backend.js";
import { authoritativeDashboard } from "../authoritative-dashboard.js";
import { localIngestionProgress } from "../../src/dashboard-data-status.js";

afterEach(() => {
  vi.unstubAllGlobals();
  document.head.replaceChildren();
  localStorage.clear();
  document.cookie = "cao_csrf=; Max-Age=0; Path=/";
  localIngestionProgress.set(null);
});

describe("remote dashboard data backend", () => {
  it("shares local WebSocket progress without issuing queries and cleans up on unsubscribe", () => {
    const meta = document.createElement("meta");
    meta.name = "dashboard-live-transport";
    meta.content = "websocket";
    document.head.append(meta);
    localStorage.setItem("cao-dashboard-access-token", "private-capability");
    const socket = Object.assign(new EventTarget(), { close: vi.fn() });
    const Socket = vi.fn(function (/** @type {URL} */ url, /** @type {string[]} */ protocols) {
      expect(url.pathname).toBe("/api/v1/events/ws");
      expect(url.href).not.toContain("private-capability");
      expect(protocols).toEqual(["cao-dashboard", `cao-bearer.${btoa("private-capability").replace(/=+$/, "")}`]);
      return socket;
    });
    const fetchMock = vi.fn();
    vi.stubGlobal("WebSocket", Socket);
    vi.stubGlobal("fetch", fetchMock);
    const revision = vi.fn();
    const stop = subscribeRemoteRevision(revision);
    const stopSecond = subscribeRemoteRevision(vi.fn());
    try {
      expect(Socket).toHaveBeenCalledOnce();
      socket.dispatchEvent(new Event("open"));
      const progress = {
        passId: "pass-1", repository: "octo/api", phase: "receiving", receivedRuns: 7,
        startedAt: "2026-11-13T18:00:00Z", updatedAt: "2026-11-13T18:00:01Z"
      };
      socket.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ type: "ingestion-progress", progress }) }));
      expect(localIngestionProgress.get()).toEqual(progress);
      expect(fetchMock).not.toHaveBeenCalled();
      expect(revision).not.toHaveBeenCalled();
      stop();
      expect(socket.close).not.toHaveBeenCalled();
      stopSecond();
      expect(socket.close).toHaveBeenCalled();
      expect(localIngestionProgress.get()).toBeNull();
      socket.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ type: "ingestion-progress", progress }) }));
      expect(localIngestionProgress.get()).toBeNull();
    } finally { stop(); stopSecond(); }
  });

  it("never attempts WebSockets for hosted OAuth even with a local transport marker", async () => {
    for (const [name, content] of [["cao-auth-mode", "github"], ["dashboard-live-transport", "websocket"]]) {
      const meta = document.createElement("meta");
      meta.name = name;
      meta.content = content;
      document.head.append(meta);
    }
    const Socket = vi.fn();
    vi.stubGlobal("WebSocket", Socket);
    const fetchMock = vi.fn().mockResolvedValue(new Response(new ReadableStream(), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const stop = subscribeRemoteRevision(vi.fn());
    try {
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
      expect(Socket).not.toHaveBeenCalled();
      expect(fetchMock.mock.calls[0][0]).toContain("/api/v1/events");
      expect(fetchMock.mock.calls[0][1].credentials).toBe("same-origin");
    } finally { stop(); }
  });

  it("clears lost local progress, reconnects once and stops retrying after unsubscribe", async () => {
    vi.useFakeTimers();
    const meta = document.createElement("meta");
    meta.name = "dashboard-live-transport";
    meta.content = "websocket";
    document.head.append(meta);
    /** @type {Array<EventTarget & { close: ReturnType<typeof vi.fn> }>} */
    const sockets = [];
    vi.stubGlobal("WebSocket", vi.fn(function () {
      const socket = Object.assign(new EventTarget(), { close: vi.fn() });
      sockets.push(socket);
      return socket;
    }));
    vi.stubGlobal("fetch", vi.fn());
    const stop = subscribeRemoteRevision(vi.fn());
    try {
      const progress = {
        passId: "pass-1", repository: "octo/api", phase: "receiving", receivedRuns: 7,
        startedAt: "2026-11-13T18:00:00Z", updatedAt: "2026-11-13T18:00:01Z"
      };
      sockets[0].dispatchEvent(new Event("open"));
      sockets[0].dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ type: "ingestion-progress", progress }) }));
      expect(localIngestionProgress.get()).toEqual(progress);
      sockets[0].dispatchEvent(new Event("close"));
      await vi.advanceTimersByTimeAsync(0);
      expect(localIngestionProgress.get()).toBeNull();
      await vi.advanceTimersByTimeAsync(1000);
      expect(sockets).toHaveLength(2);
      sockets[0].dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ type: "ingestion-progress", progress }) }));
      expect(localIngestionProgress.get()).toBeNull();
      sockets[1].dispatchEvent(new Event("open"));
      stop();
      expect(sockets[1].close).toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(30_000);
      expect(sockets).toHaveLength(2);
    } finally { stop(); vi.useRealTimers(); }
  });

  it("preserves a coded query limit and failing query ID", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: 'Query "campaign-repository-coverage" needs more retained data.',
      code: "query_plan_too_large",
      queryId: "campaign-repository-coverage",
      boundary: "retained_bytes",
      traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
      spanId: "00f067aa0ba902b7",
    }), { status: 422, headers: { "Content-Type": "application/json" } })));
    await expect(queryRemoteDashboard([], { pages: [] })).rejects.toMatchObject({
      name: "DashboardServerError",
      code: "query_plan_too_large",
      queryId: "campaign-repository-coverage",
      boundary: "retained_bytes",
      traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
      spanId: "00f067aa0ba902b7",
    });
    expect(new DashboardServerError("failure", "query_plan_too_large", "campaign-inventory")).toBeInstanceOf(Error);
  });

  it("preserves response trace headers for uncoded server failures", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: "dashboard data is unavailable",
    }), {
      status: 503,
      headers: {
        "Content-Type": "application/json",
        "X-Trace-Id": "4bf92f3577b34da6a3ce929d0e0e4736",
        "X-Span-Id": "00f067aa0ba902b7",
      },
    })));

    await expect(queryRemoteDashboard([], { pages: [] })).rejects.toMatchObject({
      name: "DashboardServerError",
      code: "",
      traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
      spanId: "00f067aa0ba902b7",
    });
  });

  it("activates only for the server-injected backend marker", () => {
    expect(usesRemoteDataBackend(document)).toBe(false);
    const meta = document.createElement("meta");
    meta.name = "dashboard-data-backend";
    meta.content = "server-http";
    document.head.append(meta);
    expect(usesRemoteDataBackend(document)).toBe(true);
  });

  it("keeps API requests inside an explicitly configured server base path", async () => {
    const meta = document.createElement("meta");
    meta.name = "dashboard-api-base";
    meta.content = "/preview-capability/";
    document.head.append(meta);
    const fetchMock = vi.fn().mockImplementation(async () => new Response(JSON.stringify({
      sources: { repositories: { source: "repositories", rows: [], metadata: {} } },
    }), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await queryRemoteDashboard(["repositories"], { pages: [] });
    expect(fetchMock).toHaveBeenCalled();
    for (const [url, init] of fetchMock.mock.calls) {
      expect(new URL(url).pathname).toMatch(/^\/preview-capability\/api\/v1\//);
      expect(init.headers["Content-Type"]).toBe("application/json");
    }
  });

  it("removes static dashboard workers and caches in remote mode", async () => {
    const unregister = vi.fn().mockResolvedValue(true);
    const deleteCache = vi.fn().mockResolvedValue(true);

    await disableRemoteDashboardPwa({
      serviceWorkers: /** @type {never} */ ({
        getRegistrations: vi.fn().mockResolvedValue([{ unregister }]),
      }),
      cacheStorage: /** @type {never} */ ({
        keys: vi.fn().mockResolvedValue([
          "central-agentic-ops-dashboard-app-v1",
          "central-agentic-ops-dashboard-data-v1",
          "unrelated-cache",
        ]),
        delete: deleteCache,
      }),
    });

    expect(unregister).toHaveBeenCalledOnce();
    expect(deleteCache).toHaveBeenCalledTimes(2);
    expect(deleteCache).not.toHaveBeenCalledWith("unrelated-cache");
  });

  it("posts compiled query context without Redis connection details", async () => {
    localStorage.setItem("cao-dashboard-access-token", "test-access-token");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        revision: 4,
        evaluatedAt: "2026-09-23T00:00:00.000Z",
      }), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        revision: 4,
        evaluatedAt: "2026-09-23T00:00:00.000Z",
        sources: { runs: { source: "runs", rows: [], metadata: {} } },
      }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await queryRemoteDashboard(["simulated-usage"], {
      pages: [{
        id: "runs",
        form: {
          fields: [{ id: "multiplier", default: 2 }],
        },
        views: [{ id: "simulation", data: { source: "simulated-usage" } }],
      }],
      queries: [{
        name: "simulated-usage",
        parameters: [{ name: "multiplier", type: "number" }],
        from: "usage",
        compute: [{
          as: "simulated-aic",
          function: "product",
          args: [{ field: "aic" }, { parameter: "multiplier" }],
        }],
      }],
      views: [],
    }, undefined, { pageId: "runs", queryContext: { formValues: { multiplier: 3 } } });

    expect(result.revision).toBe(4);
    const [, init] = fetchMock.mock.calls.at(-1) ?? [];
    expect(init).toBeDefined();
    const request = JSON.parse(String(init?.body));
    expect(request.sourceNames).toEqual(["simulated-usage"]);
    expect(request.queries[0].compute[0].args[1]).toEqual({ value: 3 });
    expect(request.compiledQueries.at(-1).compute[0].args[1]).toEqual({ value: 3 });
    expect(JSON.stringify(request)).not.toMatch(/redis|credential|password/i);
    expect(init?.headers).toMatchObject({ Authorization: "Bearer test-access-token" });
  });

  it("does not resolve unrelated simulator parameters while querying another page", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      revision: 4,
      sources: { runs: { source: "runs", rows: [], metadata: {} } },
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await queryRemoteDashboard(["runs"], {
      pages: [{ id: "runs", views: [{ id: "inventory", data: { source: "runs" } }] }],
      queries: [
        { name: "run-counts", from: "runs" },
        {
          name: "simulator",
          from: "simulation-days",
          parameters: [{ name: "repositories", type: "number" }],
          compute: [{ as: "size", function: "product", args: [{ field: "day" }, { parameter: "repositories" }] }],
        },
      ],
      views: [],
    }, undefined, { pageId: "runs" });
    const request = JSON.parse(String(fetchMock.mock.calls.at(-1)?.[1]?.body));
    expect(request.queries).toEqual([]);
    expect(request.sourceNames).toEqual(["runs"]);
  });

  it("sends the authored simulator graph with only scalar form values", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      revision: 4,
      sources: { "simulator-database-summary": { source: "simulator-database-summary", rows: [], metadata: {} } },
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await queryRemoteDashboard(["simulator-database-summary"], authoritativeDashboard.dashboard, undefined, {
      pageId: "simulators",
      queryContext: { formValues: { repositories: 2000 } },
    });
    const request = JSON.parse(String(fetchMock.mock.calls.at(-1)?.[1]?.body));
    expect(request.queries.map((/** @type {{ name: string }} */ query) => query.name)).toEqual([
      "simulator-database-inputs", "simulator-run-size", "simulator-tool-size",
      "simulator-issue-size", "simulator-database-size", "simulator-database-total",
      "simulator-database-summary",
    ]);
    expect(request.queries[0].compute[0].args).toEqual([{ value: 2000 }, { value: 10 }]);
    expect(request.compiledQueries.length).toBeGreaterThan(0);
    expect(request.queries.every((/** @type {unknown} */ query) => !JSON.stringify(query).includes('"parameter"'))).toBe(true);
  });

  it("notifies once for rate-limited requests and clears the notice after recovery", async () => {
    const limited = () => new Response(JSON.stringify({ error: "rate limit exceeded" }), { status: 429 });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 1 }), { status: 200 }))
      .mockResolvedValueOnce(limited())
      .mockResolvedValueOnce(limited())
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 1, sources: {} }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "unavailable" }), { status: 503 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(refreshRemoteDashboard([], { pages: [] })).rejects.toThrow("rate limit exceeded");
    expect(document.querySelectorAll(".dashboard-notification")).toHaveLength(1);
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("Dashboard is rate limited");

    await expect(queryRemoteDashboard([], { pages: [] })).rejects.toThrow("rate limit exceeded");
    expect(document.querySelectorAll(".dashboard-notification")).toHaveLength(1);
    expect(document.querySelector(".dashboard-notification-exit")).toBeNull();

    await expect(queryRemoteDashboard([], { pages: [] })).resolves.toMatchObject({ sources: {} });
    expect(document.querySelector(".dashboard-notification-exit")).not.toBeNull();
    await expect(queryRemoteDashboard([], { pages: [] })).rejects.toThrow("unavailable");
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(0);
  });

  it("clears a repository-memory rate-limit notice when another memory path succeeds", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(new Response("rate limited", { status: 429 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ campaigns: [] }), { status: 200 })));

    await expect(queryRemoteRepositoryMemory("first", undefined)).rejects.toThrow();
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(1);
    await expect(queryRemoteRepositoryMemory("second", undefined)).resolves.toEqual({ campaigns: [] });
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(0);
  });

  it("refreshes through the server without asking the browser to ingest data", async () => {
    document.head.innerHTML = '<meta name="cao-auth-mode" content="github">';
    document.cookie = "cao_csrf=oauth-csrf-token; Path=/";
    localStorage.setItem("cao-dashboard-access-token", "stale-local-token");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 5, changed: false }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 5, sources: {} }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    await refreshRemoteDashboard([], { pages: [], queries: [], views: [] });

    expect(fetchMock.mock.calls.map(([url]) => new URL(url).pathname)).toEqual([
      "/api/v1/refresh",
      "/api/v1/query",
    ]);
    for (const [, init] of fetchMock.mock.calls) {
      expect(init).toMatchObject({
        credentials: "same-origin",
        headers: expect.objectContaining({ "X-CSRF-Token": "oauth-csrf-token" }),
      });
      expect(init?.headers).not.toHaveProperty("Authorization");
    }
  });

  it("restores a missing CSRF cookie before sending an OAuth mutation", async () => {
    document.head.innerHTML = '<meta name="cao-auth-mode" content="github">';
    const fetchMock = vi.fn().mockImplementation((url) => {
      if (new URL(url, location.href).pathname === "/api/auth/session") {
        document.cookie = "cao_csrf=renewed-token; Path=/";
        return Promise.resolve(new Response(JSON.stringify({ login: "octocat" }), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ revision: 5, sources: {} }), { status: 200 }));
    });

    vi.stubGlobal("fetch", fetchMock);

    await refreshRemoteDashboard([], { pages: [] });

    expect(fetchMock.mock.calls.map(([url]) => new URL(url, location.href).pathname)).toEqual([
      "/api/auth/session", "/api/v1/refresh", "/api/v1/query",
    ]);
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ credentials: "same-origin" });
    expect(fetchMock.mock.calls[0][1].headers).not.toHaveProperty("X-CSRF-Token");
    for (const [, init] of fetchMock.mock.calls.slice(1)) {
      expect(init.headers).toMatchObject({ "X-CSRF-Token": "renewed-token" });
    }
  });

  it("notifies when CSRF renewal is rate limited before a mutation", async () => {
    document.head.innerHTML = '<meta name="cao-auth-mode" content="github">';
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(null, { status: 429 }))
      .mockImplementationOnce(() => {
        document.cookie = "cao_csrf=renewed-token; Path=/";
        return Promise.resolve(new Response(JSON.stringify({ login: "octocat" }), { status: 200 }));
      })
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 2 }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 2, sources: {} }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(refreshRemoteDashboard([], { pages: [] }))
      .rejects.toThrow("GitHub authentication cookie could not be renewed");
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(1);
    await expect(refreshRemoteDashboard([], { pages: [] })).resolves.toMatchObject({ sources: {} });
    expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(0);
  });

  it("does not send a mutation when the missing CSRF cookie cannot be restored", async () => {
    document.head.innerHTML = '<meta name="cao-auth-mode" content="github">';
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ login: "octocat" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(refreshRemoteDashboard([], { pages: [] }))
      .rejects.toThrow("GitHub authentication cookie could not be renewed");
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(new URL(fetchMock.mock.calls[0][0], location.href).pathname).toBe("/api/auth/session");
  });

  it("resolves repository memory through the authenticated server API", async () => {
    const manifest = { version: 1, campaigns: [{ campaign: "security-review", files: [] }] };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(manifest), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ content: "# Context" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(queryRemoteRepositoryMemory("security-review", undefined)).resolves.toEqual(manifest);
    await expect(queryRemoteRepositoryMemory("security-review", "notes/context.md"))
      .resolves.toEqual({ content: "# Context" });

    expect(fetchMock.mock.calls.map(([url]) => new URL(url).pathname + new URL(url).search)).toEqual([
      "/api/v1/memory/security-review",
      "/api/v1/memory/security-review/content?path=notes%2Fcontext.md",
    ]);
  });

  it("authenticates the revision stream without an ambient cookie", async () => {
    localStorage.setItem("cao-dashboard-access-token", "stream-access-token");
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('data: {"revision":9}\n\n'));
        controller.close();
      },
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const revision = new Promise((resolve) => {
      const stop = subscribeRemoteRevision((value) => {
        stop();
        resolve(value);
      });
    });

    await expect(revision).resolves.toBe(9);
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/api/v1/events"), expect.objectContaining({
      credentials: "omit",
      headers: expect.objectContaining({ Authorization: "Bearer stream-access-token" }),
    }));
  });

  it("authenticates the revision stream with the OAuth session cookie when no bearer token exists", async () => {
    document.head.innerHTML = '<meta name="cao-auth-mode" content="github">';
    const stream = new ReadableStream({
      start(controller) {
        controller.close();
      },
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const stop = subscribeRemoteRevision(() => {});
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());
    stop();

    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/api/v1/events"), expect.objectContaining({
      credentials: "same-origin",
      headers: { Accept: "text/event-stream" },
    }));
  });

  it("shares one revision stream across subscribers until the last one unsubscribes", async () => {
    /** @type {(data: string) => void} */
    let emitEvent = () => {};
    const stream = new ReadableStream({
      start(controller) {
        emitEvent = (data) => controller.enqueue(new TextEncoder().encode(data));
      },
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    /** @type {number[]} */
    const firstRevisions = [];
    /** @type {number[]} */
    const secondRevisions = [];
    const stopFirst = subscribeRemoteRevision((revision) => firstRevisions.push(revision));
    const stopSecond = subscribeRemoteRevision((revision) => secondRevisions.push(revision));
    try {
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
      emitEvent('data: {"revision":5021}\n\n');
      await vi.waitFor(() => {
        expect(firstRevisions).toEqual([5021]);
        expect(secondRevisions).toEqual([5021]);
      });

      const streamSignal = fetchMock.mock.calls[0]?.[1]?.signal;
      stopFirst();
      expect(streamSignal?.aborted).toBe(false);
      stopSecond();
      expect(streamSignal?.aborted).toBe(true);
    } finally {
      stopFirst();
      stopSecond();
    }
  });

  it("does not let a separate query swallow a revision needed by active subscriptions", async () => {
    /** @type {ReadableStreamDefaultController<Uint8Array> | undefined} */
    let events;
    const stream = new ReadableStream({ start(controller) { events = controller; } });
    const fetchMock = vi.fn(async (url) => String(url).endsWith("/events")
      ? new Response(stream, { status: 200 })
      : new Response(JSON.stringify({
        revision: 6002, evaluatedAt: "2026-10-10T11:00:00Z", sources: {}
      }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const changed = vi.fn();
    const stop = subscribeRemoteRevision(changed);
    try {
      events?.enqueue(new TextEncoder().encode('data: {"revision":6001}\n\n'));
      await vi.waitFor(() => expect(changed.mock.calls[0]?.[0]).toBe(6001));
      changed.mockClear();
      await queryRemoteDashboard([], { pages: [] });
      events?.enqueue(new TextEncoder().encode('data: {"revision":6002}\n\n'));
      await vi.waitFor(() => expect(changed).toHaveBeenCalledOnce());
      expect(changed.mock.calls[0]?.[0]).toBe(6002);
    } finally { stop(); }
  });

  it("isolates failing subscribers and reconnects after the revision stream closes", async () => {
    /** @type {ReadableStreamDefaultController<Uint8Array> | undefined} */
    let streamController;
    const firstStream = new ReadableStream({
      start(controller) {
        streamController = controller;
      },
    });
    const secondStream = new ReadableStream();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(firstStream, {
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
      }))
      .mockResolvedValueOnce(new Response(secondStream, {
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    /** @type {number[]} */
    const revisions = [];
    /** @type {string[]} */
    const errors = [];
    const stopFailingSubscriber = subscribeRemoteRevision(
      () => { throw new Error("subscriber failure"); },
      () => { throw new Error("subscriber error handler failure"); },
    );
    const stopHealthySubscriber = subscribeRemoteRevision(
      (revision) => revisions.push(revision),
      (error) => errors.push(error.message),
    );
    try {
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
      streamController?.enqueue(new TextEncoder().encode('data: {"revision":5022}\n\n'));
      await vi.waitFor(() => expect(revisions).toEqual([5022]));

      streamController?.close();
      await vi.waitFor(() => expect(document.querySelector('[role="status"]')?.textContent)
        .toContain("Reconnecting automatically"));
      expect(errors).toEqual([]);
      expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(1);
      await new Promise((resolve) => setTimeout(resolve, 1100));
      expect(fetchMock).toHaveBeenCalledTimes(2);
      await vi.waitFor(() => expect(document.querySelector(".dashboard-notification-exit")).not.toBeNull());
    } finally {
      stopFailingSubscriber();
      stopHealthySubscriber();
    }
  });

  it("shows one reconnect notice for repeated network failures and clears it on recovery or unsubscribe", async () => {
    localStorage.setItem("cao-dashboard-access-token", "private-bearer-token");
    const stream = new ReadableStream();
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new TypeError("Load failed: private-bearer-token"))
      .mockRejectedValueOnce(new TypeError("Load failed: private-bearer-token"))
      .mockResolvedValueOnce(new Response(stream, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const onError = vi.fn();
    const stop = subscribeRemoteRevision(() => {}, onError);
    try {
      await vi.waitFor(() => expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(1));
      expect(document.body.textContent).not.toContain("private-bearer-token");
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2), { timeout: 1500 });
      expect(document.querySelectorAll(".dashboard-notification:not(.dashboard-notification-exit)")).toHaveLength(1);
      expect(onError).not.toHaveBeenCalled();
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3), { timeout: 2500 });
      await vi.waitFor(() => expect(document.querySelector(".dashboard-notification-exit")).not.toBeNull());
    } finally {
      stop();
    }
  });

  it("stays silent by default and logs only scalar metadata under its predictable category", async () => {
    const output = { debug: vi.fn() };
    vi.doMock("../../src/debug.js", async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual("../../src/debug.js")
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => "?debug=remote-data-backend", output }),
      };
    });
    vi.resetModules();
    const { refreshRemoteDashboard: refreshRemoteDashboardWithDebug } = await import("../../src/remote-data-backend.js");

    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 1, evaluatedAt: "2026-09-23T00:00:00.000Z" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "not-found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }))
      .mockRejectedValueOnce(new TypeError("Load failed: private-bearer-token"));
    vi.stubGlobal("fetch", fetchMock);

    await expect(refreshRemoteDashboardWithDebug([], { pages: [], queries: [], views: [] }))
      .rejects.toThrow();
    await expect(refreshRemoteDashboardWithDebug([], { pages: [], queries: [], views: [] }))
      .rejects.toThrow();

    expect(output.debug).toHaveBeenCalledWith("[cao:remote-data-backend]", { event: "refresh-checked", changed: false });
    expect(output.debug).toHaveBeenCalledWith("[cao:remote-data-backend]", expect.objectContaining({
      event: "request-failed", operation: "query", status: 404
    }));
    expect(output.debug).toHaveBeenCalledWith("[cao:remote-data-backend]", expect.objectContaining({
      event: "request-network-failed", operation: "refresh", errorName: "TypeError"
    }));

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== "object")).toBe(true);
    }
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/authorization|token|redis|Load failed/i);

    vi.doUnmock("../../src/debug.js");
    vi.resetModules();
  });

  it("is disabled by default (no debug output) when the debug query is absent", async () => {
    const output = { debug: vi.fn() };
    vi.doMock("../../src/debug.js", async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual("../../src/debug.js")
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => "", output }),
      };
    });
    vi.resetModules();
    const { refreshRemoteDashboard: refreshRemoteDashboardWithoutDebug } = await import("../../src/remote-data-backend.js");

    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 1, evaluatedAt: "2026-09-23T00:00:00.000Z" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ revision: 1, sources: {} }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    await refreshRemoteDashboardWithoutDebug([], { pages: [], queries: [], views: [] });

    expect(output.debug).not.toHaveBeenCalled();

    vi.doUnmock("../../src/debug.js");
    vi.resetModules();
  });
});
