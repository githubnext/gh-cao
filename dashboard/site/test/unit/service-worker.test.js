import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import vm from 'node:vm';
import { MessageChannel } from 'node:worker_threads';
import { describe, expect, it, vi } from 'vitest';
import { isDebugEnabled as pageIsDebugEnabled } from '../../src/debug.js';

const source = readFileSync(resolve('service-worker.js'), 'utf8');

/** @param {string[]} [cacheKeys] @param {{ search?: string, clientUrl?: string, appAssets?: string[], appMode?: boolean }} [options] */
function serviceWorkerHarness(cacheKeys = [], options = {}) {
  /** @type {Record<string, (event: any) => void>} */
  const listeners = {};
  /** @type {Map<string, Response>} */
  const entries = new Map();
  const cache = {
    /** @param {string | Request} key @param {Response} response */
    put: async (key, response) => { entries.set(String(key), response.clone()); },
    /** @param {string | Request} key */
    match: async (key) => entries.get(String(key))?.clone(),
    /** @param {string | Request} key */
    delete: async (key) => entries.delete(String(key)),
    keys: async () => [...entries.keys()].map((url) => new Request(url))
  };
  const fetch = vi.fn(async (
    /** @type {string | URL | Request} */ _url,
    /** @type {RequestInit | undefined} */ _init
  ) => new Response('updated data'));
  const deleteCache = vi.fn(async (key) => {
    cacheKeys = cacheKeys.filter((candidate) => candidate !== key);
    return true;
  });
  const debugConsole = { debug: vi.fn() };
  const client = {
    type: 'window',
    url: options.clientUrl ?? 'https://example.test/dashboard/',
    postMessage: vi.fn((message, ports) => {
      if (message.type === 'REQUEST_DASHBOARD_APP_MODE') {
        ports[0].postMessage({ type: 'DASHBOARD_APP_MODE', appMode: options.appMode === true });
      }
    })
  };
  const worker = {
    location: {
      href: `https://example.test/dashboard/service-worker.js${options.search ?? ''}`,
      origin: 'https://example.test',
      search: options.search ?? ''
    },
    registration: { scope: 'https://example.test/dashboard/' },
    navigator: {
      connection: { type: 'wifi', saveData: false, effectiveType: '4g' }
    },
    clients: {
      claim: vi.fn().mockResolvedValue(undefined),
      matchAll: vi.fn().mockResolvedValue([client]),
      get: vi.fn().mockResolvedValue(client)
    },
    skipWaiting: vi.fn().mockResolvedValue(undefined),
    /** @param {string} type @param {(event: any) => void} listener */
    addEventListener: (type, listener) => {
      listeners[type] = listener;
    }
  };
  /** @type {Record<string, unknown>} */
  const sandbox = {
    self: worker,
    caches: {
      open: async () => cache,
      keys: async () => cacheKeys,
      delete: deleteCache,
      match: cache.match
    },
    fetch,
    URL,
    Response,
    AbortSignal,
    Request,
    MessageChannel,
    setTimeout,
    clearTimeout,
    Set,
    Error,
    Promise,
    JSON,
    console: debugConsole
  };
  vm.runInNewContext(source.replace('const APP_ASSETS = [];',
    `const APP_ASSETS = ${JSON.stringify(options.appAssets ?? [])};`), sandbox);
  const fetchListener = listeners.fetch;
  listeners.fetch = (event) => fetchListener({ clientId: 'dashboard-client', ...event });
  const messageListener = listeners.message;
  listeners.message = (event) => messageListener({ source: client, ...event });
  return {
    listeners,
    worker,
    client,
    fetch,
    cache,
    entries,
    deleteCache,
    debugConsole,
    isDebugEnabled: /** @type {(category: string) => boolean} */ (sandbox.isDebugEnabled)
  };
}

/** @param {(event: any) => void} listener @param {Record<string, unknown>} event */
async function dispatchExtendedEvent(listener, event) {
  /** @type {Promise<unknown> | undefined} */
  let task;
  listener({ ...event, waitUntil: (/** @type {Promise<unknown>} */ value) => { task = value; } });
  await task;
}

describe('dashboard service worker', () => {
  it.each([false, true])('precaches JavaScript only in app mode, with appMode=%s', async (appMode) => {
    const { listeners, entries, fetch } = serviceWorkerHarness([], {
      appMode,
      appAssets: ['./', 'src/main.js', 'src/data-worker.js', 'dashboard-pages/overview.json']
    });
    await dispatchExtendedEvent(listeners.install, {});
    expect(entries.has('https://example.test/dashboard/')).toBe(true);
    expect(entries.has('https://example.test/dashboard/dashboard-pages/overview.json')).toBe(true);
    expect(entries.has('https://example.test/dashboard/src/main.js')).toBe(appMode);
    expect(entries.has('https://example.test/dashboard/src/data-worker.js')).toBe(appMode);
    expect(fetch).toHaveBeenCalledTimes(appMode ? 4 : 2);
  });

  it('does not precache scripts when installation has no window client', async () => {
    const { listeners, worker, entries } = serviceWorkerHarness([], {
      appAssets: ['./', 'src/main.js']
    });
    worker.clients.matchAll.mockResolvedValue([]);
    await dispatchExtendedEvent(listeners.install, {});
    expect(entries.has('https://example.test/dashboard/')).toBe(true);
    expect(entries.has('https://example.test/dashboard/src/main.js')).toBe(false);
  });

  it('ignores installed windows outside this dashboard scope', async () => {
    const { listeners, entries, client } = serviceWorkerHarness([], {
      appMode: true,
      clientUrl: 'https://example.test/another-app/',
      appAssets: ['./', 'src/main.js']
    });
    await dispatchExtendedEvent(listeners.install, {});
    expect(entries.has('https://example.test/dashboard/src/main.js')).toBe(false);
    expect(client.postMessage).not.toHaveBeenCalled();
  });

  it.each(['src/main.js?sha=abc', 'src/data-worker.mjs', 'scripts/bootstrap.cjs', 'module'])(
    'does not cache or serve cached browser-tab scripts at %s',
    async (path) => {
      const { listeners, worker, fetch, entries } = serviceWorkerHarness();
      const request = new Request(`https://example.test/dashboard/${path}`);
      const script = { method: request.method, url: request.url, destination: 'script' };
      entries.set(request.url, new Response('cached app script'));
      worker.navigator.connection.effectiveType = '2g';
      /** @type {Promise<Response> | undefined} */
      let response;
      listeners.fetch({
        request: script,
        respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
      });
      await expect((await response)?.text()).resolves.toBe('updated data');
      await expect(entries.get(request.url)?.clone().text()).resolves.toBe('cached app script');
      fetch.mockRejectedValueOnce(new TypeError('offline'));
      listeners.fetch({
        request: script,
        respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
      });
      await expect(response).rejects.toThrow('offline');
    }
  );

  it('does not cache scripts from unidentified clients', async () => {
    const { listeners, entries, client } = serviceWorkerHarness([], { appMode: true });
    /** @type {Promise<Response> | undefined} */
    let response;
    listeners.fetch({
      clientId: '',
      request: new Request('https://example.test/dashboard/src/main.js'),
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await response;
    expect(entries.size).toBe(0);
    expect(client.postMessage).not.toHaveBeenCalled();
  });

  it.each(['CACHE_APP_ASSETS', 'CONFIGURE_BACKGROUND_DATA'])(
    'excludes browser-tab JavaScript from %s without changing data configuration',
    async (type) => {
      const { listeners, entries, fetch } = serviceWorkerHarness();
      const scripts = ['https://example.test/dashboard/src/main.js', 'https://example.test/dashboard/src/data-worker.js'];
      await dispatchExtendedEvent(listeners.message, {
        data: {
          type,
          urls: type === 'CACHE_APP_ASSETS' ? scripts : ['https://example.test/dashboard/payload-hashes.json'],
          assets: scripts
        },
        ports: [{ postMessage: vi.fn() }]
      });
      expect(fetch).not.toHaveBeenCalled();
      for (const script of scripts) expect(entries.has(script)).toBe(false);
      expect(entries.has('https://example.test/dashboard/.dashboard-data-update-config'))
        .toBe(type === 'CONFIGURE_BACKGROUND_DATA');
    }
  );

  it('fills the complete script cache when a previously unpinned dashboard opens in app mode', async () => {
    const { listeners, entries } = serviceWorkerHarness([], {
      appMode: true,
      appAssets: ['./', 'src/main.js', 'src/data-worker.js']
    });
    await dispatchExtendedEvent(listeners.message, {
      data: { type: 'CACHE_APP_ASSETS', urls: ['https://example.test/dashboard/'] }
    });
    expect(entries.has('https://example.test/dashboard/src/main.js')).toBe(true);
    expect(entries.has('https://example.test/dashboard/src/data-worker.js')).toBe(true);
  });

  it('fails closed when a window cannot report app mode', async () => {
    vi.useFakeTimers();
    try {
      const { listeners, client, fetch, entries, debugConsole } = serviceWorkerHarness([], {
        search: '?debug=data:ingestion:sw'
      });
      client.postMessage.mockImplementation(() => {});
      const task = dispatchExtendedEvent(listeners.message, {
        data: { type: 'CACHE_APP_ASSETS', urls: ['https://example.test/dashboard/src/main.js'] }
      });
      await vi.advanceTimersByTimeAsync(1000);
      await task;
      expect(fetch).not.toHaveBeenCalled();
      expect(entries.size).toBe(0);
      expect(debugConsole.debug).toHaveBeenCalledWith(
        '[cao:data:ingestion:sw]', 'app mode response timed out'
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('checks app mode per requesting window rather than reusing another window permission', async () => {
    const { listeners, client, worker, entries, fetch } = serviceWorkerHarness([], { appMode: true });
    const request = new Request('https://example.test/dashboard/src/main.js');
    /** @type {Promise<Response> | undefined} */
    let response;
    const fetchScript = () => listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    fetchScript();
    await response;
    expect(entries.size).toBe(1);
    const browserClient = {
      ...client,
      postMessage: vi.fn((_message, ports) => {
        ports[0].postMessage({ type: 'DASHBOARD_APP_MODE', appMode: false });
      })
    };
    worker.clients.get.mockResolvedValue(browserClient);
    fetch.mockResolvedValueOnce(new Response('browser script'));
    fetchScript();
    await expect((await response)?.text()).resolves.toBe('browser script');
    await expect(entries.get(String(request))?.text()).resolves.toBe('updated data');
    expect(browserClient.postMessage).toHaveBeenCalledOnce();
  });

  it('prepares the shell and lazy page chunks before installation completes', async () => {
    const { listeners, entries, fetch } = serviceWorkerHarness([], {
      appAssets: ['./', 'dashboard.json', 'dashboard-pages/overview.json']
    });
    await dispatchExtendedEvent(listeners.install, {});
    expect(entries.has('https://example.test/dashboard/')).toBe(true);
    expect(entries.has('https://example.test/dashboard/dashboard.json')).toBe(true);
    expect(entries.has('https://example.test/dashboard/dashboard-pages/overview.json')).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('notifies open dashboard pages only after activation finishes', async () => {
    const { listeners, worker } = serviceWorkerHarness(['central-agentic-ops-dashboard-app-old']);
    const client = { postMessage: vi.fn() };
    worker.clients.matchAll.mockResolvedValue([client]);
    await dispatchExtendedEvent(listeners.activate, {});
    expect(worker.clients.claim).toHaveBeenCalledOnce();
    expect(client.postMessage).toHaveBeenCalledWith({
      type: 'APP_UPDATE_DOWNLOADED',
      version: 'development'
    });
  });

  it('uses the precached script when its versioned URL cannot be fetched offline', async () => {
    const { listeners, entries, fetch } = serviceWorkerHarness([], { appMode: true });
    const script = new Request('https://example.test/dashboard/src/main.js?sha=abc');
    entries.set('https://example.test/dashboard/src/main.js', new Response('cached script'));
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    /** @type {Promise<Response> | undefined} */
    let response;
    listeners.fetch({
      request: script,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect((await response)?.text()).resolves.toBe('cached script');
  });

  it('loads the precached data worker when offline debugging parameters are set', async () => {
    const { listeners, entries, fetch } = serviceWorkerHarness([], { appMode: true });
    const script = new Request('https://example.test/dashboard/src/data-worker.js?debug=1&debug-shard-limit=2&debug-eager-ingest=1');
    entries.set('https://example.test/dashboard/src/data-worker.js', new Response('cached worker'));
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    /** @type {Promise<Response> | undefined} */
    let response;
    listeners.fetch({
      request: script,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect((await response)?.text()).resolves.toBe('cached worker');
  });
  it('prefers cached assets and data on slow connections without network requests', async () => {
    const { listeners, worker, fetch, entries } = serviceWorkerHarness();
    worker.navigator.connection.effectiveType = '2g';
    const asset = new Request('https://example.test/dashboard/dashboard.json', { cache: 'no-store' });
    const data = new Request('https://example.test/dashboard/payload-hashes.json');
    entries.set(asset.url, new Response('cached dashboard'));
    entries.set(String(data), new Response('cached data'));
    for (const request of [asset, data]) {
      /** @type {Promise<Response> | undefined} */
      let response;
      listeners.fetch({
        request,
        respondWith: (/** @type {Promise<Response>} */ value) => { response = value; },
        waitUntil: () => {}
      });
      await expect((await response)?.text()).resolves.toBe(request === asset ? 'cached dashboard' : 'cached data');
    }
    expect(fetch).not.toHaveBeenCalled();
  });

  it('prefers the cached manifest on slow connections even for no-store data requests', async () => {
    const { listeners, worker, fetch, entries } = serviceWorkerHarness();
    worker.navigator.connection.effectiveType = 'slow-2g';
    const request = new Request('https://example.test/dashboard/payload-hashes.json', { cache: 'no-store' });
    entries.set(String(request), new Response('cached manifest'));
    /** @type {Promise<Response> | undefined} */
    let response;
    listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect((await response)?.text()).resolves.toBe('cached manifest');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('caches refreshed activity shards and reuses them on slow connections', async () => {
    const { listeners, worker, fetch, entries } = serviceWorkerHarness();
    const request = new Request('https://example.test/dashboard/gh-aw-logs-runs/logs-1.jsonl', { cache: 'reload' });
    /** @type {Promise<Response> | undefined} */
    let response;
    await dispatchExtendedEvent(listeners.fetch, {
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect((await response)?.text()).resolves.toBe('updated data');
    expect(fetch.mock.lastCall?.[0]).toHaveProperty('cache', 'reload');
    await expect(entries.get(String(request))?.clone().text()).resolves.toBe('updated data');

    worker.navigator.connection.effectiveType = '2g';
    fetch.mockClear();
    listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; },
      waitUntil: () => {}
    });
    await expect((await response)?.text()).resolves.toBe('updated data');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('leaves the server-side data API outside application caches', () => {
    expect(source).toContain("!url.pathname.startsWith('/api/')");
  });

  it('rejects manifests without compacted run-information shards', async () => {
    const { listeners, fetch } = serviceWorkerHarness();
    const payloadHashes = JSON.stringify({
      'gh-aw-logs-shards/logs-1.jsonl': 'c'.repeat(64)
    });
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? payloadHashes : '[]'
    ));
    const completed = vi.fn();

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'DOWNLOAD_DATA',
        urls: ['https://example.test/dashboard/payload-hashes.json']
      },
      ports: [{ postMessage: completed }]
    });

    expect(completed).toHaveBeenCalledWith(expect.objectContaining({ type: 'DOWNLOAD_FAILED' }));
    expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/logs-1.jsonl'))).toBe(false);
  });

  it('downloads independently published run-information and record shards', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness();
    const firstStem = `gh-aw-logs-1000-a-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    const secondStem = `gh-aw-logs-2000-b-${'c'.repeat(64)}-${'d'.repeat(16)}.jsonl`;
    const payloadHashes = JSON.stringify({
      'gh-aw-logs-shards/logs-1.jsonl': 'c'.repeat(64),
      [`gh-aw-logs-runs/${firstStem}`]: 'd'.repeat(64),
      [`gh-aw-logs-runs/${secondStem}`]: 'e'.repeat(64),
      [`gh-aw-logs-records/${secondStem}`]: 'f'.repeat(64)
    });
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? payloadHashes : '[]'
    ));

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'DOWNLOAD_DATA',
        urls: ['https://example.test/dashboard/payload-hashes.json']
      },
      ports: [{ postMessage: vi.fn() }]
    });

    expect(entries.has('https://example.test/dashboard/gh-aw-logs-shards/logs-1.jsonl')).toBe(false);
    expect(entries.has(`https://example.test/dashboard/gh-aw-logs-runs/${firstStem}`)).toBe(true);
    expect(entries.has(`https://example.test/dashboard/gh-aw-logs-runs/${secondStem}`)).toBe(true);
    expect(entries.has(`https://example.test/dashboard/gh-aw-logs-records/${secondStem}`)).toBe(true);
  });

  it('downloads currently compacted phase shard names', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness();
    const runName = 'gh-aw-logs-runs/2026-08-19-0000-d87bcbb6fd3ba859.jsonl';
    const recordName = 'gh-aw-logs-records/2026-09-11-0002-494f59593ace108c.jsonl';
    const payloadHashes = JSON.stringify({
      [runName]: 'd'.repeat(64),
      [recordName]: 'e'.repeat(64)
    });
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? payloadHashes : '[]'
    ));

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'DOWNLOAD_DATA',
        urls: ['https://example.test/dashboard/payload-hashes.json']
      },
      ports: [{ postMessage: vi.fn() }]
    });

    expect(entries.has(`https://example.test/dashboard/${runName}`)).toBe(true);
    expect(entries.has(`https://example.test/dashboard/${recordName}`)).toBe(true);
  });

  it('downloads configured dashboard data during periodic background sync with no page open', async () => {
    const { listeners, worker, fetch, entries } = serviceWorkerHarness();
    const runName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    const payloadHashes = JSON.stringify({
      'gh-aw-logs.sqlite': 'b'.repeat(64),
      [runName]: 'c'.repeat(64)
    });
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? payloadHashes : 'updated data'
    ));
    const configured = vi.fn();
    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'CONFIGURE_BACKGROUND_DATA',
        urls: [
          'https://example.test/dashboard/payload-hashes.json',
          'https://example.test/dashboard/inventory-sources.json'
        ]
      },
      ports: [{ postMessage: configured }]
    });
    expect(configured).toHaveBeenCalledWith(expect.objectContaining({
      type: 'BACKGROUND_DATA_CONFIGURED'
    }));

    await dispatchExtendedEvent(listeners.periodicsync, {
      tag: 'central-agentic-ops-dashboard-data'
    });

    expect(fetch).toHaveBeenCalledTimes(3);
    expect(entries.has(`https://example.test/dashboard/${runName}`)).toBe(true);
    expect(entries.has('https://example.test/dashboard/payload-hashes.json')).toBe(true);
    entries.set(
      'https://example.test/dashboard/.dashboard-data-update-config',
      new Response(JSON.stringify({
        urls: [
          'https://example.test/dashboard/payload-hashes.json',
          'https://example.test/dashboard/inventory-sources.json'
        ],
        lastSuccess: 0
      }))
    );
    await dispatchExtendedEvent(listeners.periodicsync, {
      tag: 'central-agentic-ops-dashboard-data'
    });
    expect(fetch).toHaveBeenCalledTimes(5);
    expect(fetch.mock.calls.filter(([url]) => String(url).endsWith(runName))).toHaveLength(1);
    worker.navigator.connection.type = 'cellular';
    await dispatchExtendedEvent(listeners.periodicsync, {
      tag: 'central-agentic-ops-dashboard-data'
    });
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it('does not publish a new payload hash when the matching JSONL download fails', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness();
    const runName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    const payloadHashes = JSON.stringify({ [runName]: 'a'.repeat(64) });
    fetch.mockImplementation(async (url) => {
      if (String(url).endsWith('/payload-hashes.json')) return new Response(payloadHashes);
      throw new TypeError('network failure');
    });
    const completed = vi.fn();

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'DOWNLOAD_DATA',
        urls: [
          'https://example.test/dashboard/payload-hashes.json'
        ]
      },
      ports: [{ postMessage: completed }]
    });

    expect(completed).toHaveBeenCalledWith(expect.objectContaining({ type: 'DOWNLOAD_FAILED' }));
    expect(entries.has('https://example.test/dashboard/payload-hashes.json')).toBe(false);
  });

  it('removes an obsolete hash when background downloads fall back to ETags', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness();
    const hashesUrl = 'https://example.test/dashboard/payload-hashes.json';
    const runName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    entries.set(hashesUrl, Response.json({ [runName]: 'a'.repeat(64) }));
    fetch.mockImplementation(async (url) => (
      String(url).endsWith('/payload-hashes.json')
        ? new Response(null, { status: 404 })
        : new Response('updated data', { headers: { etag: '"updated"' } })
    ));

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'DOWNLOAD_DATA',
        urls: [
          hashesUrl
        ]
      },
      ports: [{ postMessage: vi.fn() }]
    });

    expect(entries.has(hashesUrl)).toBe(false);
  });

  it('serves cached dashboard assets and data while offline', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness([], { appMode: true });
    const request = new Request('https://example.test/dashboard/src/main.js');
    fetch.mockResolvedValueOnce(new Response('online'));
    /** @type {Promise<Response> | undefined} */
    let response;
    listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect(response).resolves.toHaveProperty('status', 200);
    expect(entries.size).toBe(1);

    fetch.mockRejectedValueOnce(new TypeError('offline'));
    listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect((await response)?.text()).resolves.toBe('online');

    const dataRequest = new Request(
      `https://example.test/dashboard/gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`
    );
    entries.set(String(dataRequest), new Response('cached data'));
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    listeners.fetch({
      request: dataRequest,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; },
      waitUntil: () => {}
    });
    await expect((await response)?.text()).resolves.toBe('cached data');
  });

  it('fetches the online dashboard shell and its assets without HTTP or offline cache reuse', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness([], {
      clientUrl: 'https://example.test/dashboard/?online=1',
      appMode: true
    });
    const shell = new Request('https://example.test/dashboard/?online=1');
    const asset = new Request('https://example.test/dashboard/src/main.js');
    entries.set(String(shell), new Response('stale shell'));
    entries.set(String(asset), new Response('stale asset'));
    /** @type {Promise<Response> | undefined} */
    let response;
    for (const request of [shell, asset]) {
      fetch.mockResolvedValueOnce(new Response('fresh'));
      listeners.fetch({
        request,
        clientId: request === asset ? 'online-tab' : '',
        respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
      });
      await expect((await response)?.text()).resolves.toBe('fresh');
      expect(fetch.mock.lastCall?.[0]).toHaveProperty('cache', 'no-store');
      await expect(entries.get(String(request))?.text()).resolves.toBe('fresh');
    }
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    listeners.fetch({
      request: asset,
      clientId: 'online-tab',
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect(response).rejects.toThrow('offline');
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    listeners.fetch({
      request: shell,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });
    await expect(response).rejects.toThrow('offline');
  });

  it('never falls back to cached data for an online dashboard tab', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness([], {
      clientUrl: 'https://example.test/dashboard/?online=1'
    });
    const request = new Request('https://example.test/dashboard/payload-hashes.json');
    entries.set(String(request), new Response('stale'));
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    /** @type {Promise<Response> | undefined} */
    let response;
    listeners.fetch({
      request,
      clientId: 'online-tab',
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; },
      waitUntil: () => {}
    });
    await expect(response).rejects.toThrow('offline');
    expect(fetch.mock.lastCall?.[0]).toHaveProperty('cache', 'no-store');
  });

  it('redownloads unchanged shards for an online dashboard tab', async () => {
    const { listeners, fetch } = serviceWorkerHarness();
    const runName = 'gh-aw-logs-runs/2026-08-19-0000-d87bcbb6fd3ba859.jsonl';
    const payloadHashes = JSON.stringify({ [runName]: 'a'.repeat(64) });
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? payloadHashes : 'fresh shard'
    ));
    const urls = ['https://example.test/dashboard/payload-hashes.json'];
    const completed = vi.fn();
    for (const sourceUrl of [
      'https://example.test/dashboard/',
      'https://example.test/dashboard/',
      'https://example.test/dashboard/?online=1'
    ]) {
      await dispatchExtendedEvent(listeners.message, {
        data: { type: 'DOWNLOAD_DATA', urls },
        source: { url: sourceUrl },
        ports: [{ postMessage: completed }]
      });
    }
    expect(completed).toHaveBeenCalledWith(expect.objectContaining({ type: 'DOWNLOAD_COMPLETE' }));
    expect(fetch.mock.calls.filter(([url]) => String(url).endsWith(runName))).toHaveLength(2);
  });

  it('streams foreground dashboard data before its cache write completes', async () => {
    const { listeners, fetch, cache, entries } = serviceWorkerHarness();
    let finishCacheWrite = () => {};
    const cacheWriteBlocked = new Promise((resolve) => { finishCacheWrite = () => resolve(undefined); });
    vi.spyOn(cache, 'put').mockImplementation(async (key, response) => {
      await cacheWriteBlocked;
      entries.set(String(key), response.clone());
    });
    fetch.mockResolvedValueOnce(new Response('streamed data'));
    const request = new Request(
      `https://example.test/dashboard/gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`
    );
    /** @type {Promise<Response> | undefined} */
    let response;
    /** @type {Promise<unknown> | undefined} */
    let cacheWrite;

    listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; },
      waitUntil: (/** @type {Promise<unknown>} */ value) => { cacheWrite = value; }
    });

    await expect((await response)?.text()).resolves.toBe('streamed data');
    expect(entries.has(String(request))).toBe(false);
    finishCacheWrite();
    await cacheWrite;
    expect(entries.has(String(request))).toBe(true);
  });

  it('falls back to the cached application shell for offline navigation', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness();
    entries.set('https://example.test/dashboard/', new Response('cached shell'));
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    /** @type {Promise<Response> | undefined} */
    let response;

    listeners.fetch({
      request: {
        method: 'GET',
        url: 'https://example.test/dashboard/repositories',
        cache: 'default',
        mode: 'navigate'
      },
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });

    await expect((await response)?.text()).resolves.toBe('cached shell');
  });

  it('caches application assets independently of background data updates', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness([], { appMode: true });

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'CACHE_APP_ASSETS',
        urls: [
          'https://example.test/dashboard/',
          'https://example.test/dashboard/src/main.js',
          'https://outside.example/main.js'
        ]
      }
    });

    expect(fetch).toHaveBeenCalledTimes(2);
    expect(entries.has('https://example.test/dashboard/')).toBe(true);
    expect(entries.has('https://example.test/dashboard/src/main.js')).toBe(true);
  });

  it('keeps successful assets when the connection drops part way through caching', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness([], { appMode: true });
    fetch.mockImplementation(async (url) => {
      if (String(url).endsWith('/src/main.js')) throw new TypeError('connection lost');
      return new Response(`cached ${url}`);
    });

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'CACHE_APP_ASSETS',
        urls: [
          'https://example.test/dashboard/',
          'https://example.test/dashboard/src/main.js',
          'https://example.test/dashboard/manifest.webmanifest'
        ]
      }
    });

    expect(fetch).toHaveBeenCalledTimes(3);
    expect(entries.has('https://example.test/dashboard/')).toBe(true);
    expect(entries.has('https://example.test/dashboard/src/main.js')).toBe(false);
    expect(entries.has('https://example.test/dashboard/manifest.webmanifest')).toBe(true);
  });

  it('preserves a cached asset when its network refresh is interrupted', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness([], { appMode: true });
    const request = new Request('https://example.test/dashboard/src/main.js');
    entries.set(String(request), new Response('previous version'));
    fetch.mockRejectedValueOnce(new TypeError('connection lost'));
    /** @type {Promise<Response> | undefined} */
    let response;

    listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });

    await expect((await response)?.text()).resolves.toBe('previous version');
    await expect(entries.get(String(request))?.text()).resolves.toBe('previous version');
  });

  it('removes obsolete versioned caches after activation', async () => {
    const { listeners, worker, deleteCache } = serviceWorkerHarness([
      'central-agentic-ops-dashboard-data-development',
      'central-agentic-ops-dashboard-data-old',
      'central-agentic-ops-dashboard-app-development',
      'central-agentic-ops-dashboard-app-old',
      'central-agentic-ops-dashboard-config',
      'unrelated-cache'
    ]);

    await dispatchExtendedEvent(listeners.activate, {});

    expect(deleteCache).toHaveBeenCalledTimes(2);
    expect(deleteCache).toHaveBeenCalledWith('central-agentic-ops-dashboard-data-old');
    expect(deleteCache).toHaveBeenCalledWith('central-agentic-ops-dashboard-app-old');
    expect(worker.clients.claim).toHaveBeenCalledOnce();
  });

  it('does not use stale cached data for hash-identified foreground downloads', async () => {
    const { listeners, fetch, entries } = serviceWorkerHarness();
    const request = new Request(`https://example.test/dashboard/gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`, {
      cache: 'no-store'
    });
    entries.set(String(request), new Response('stale data'));
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    /** @type {Promise<Response> | undefined} */
    let response;

    listeners.fetch({
      request,
      respondWith: (/** @type {Promise<Response>} */ value) => { response = value; }
    });

    await expect(response).rejects.toThrow('offline');
  });

  it('clears persisted scheduling before worker unregistration', async () => {
    const { listeners, entries } = serviceWorkerHarness();
    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'CONFIGURE_BACKGROUND_DATA',
        urls: ['https://example.test/dashboard/payload-hashes.json']
      },
      ports: [{ postMessage: vi.fn() }]
    });
    expect(entries.size).toBeGreaterThan(0);
    const cleared = vi.fn();

    await dispatchExtendedEvent(listeners.message, {
      data: { type: 'CLEAR_BACKGROUND_DATA' },
      ports: [{ postMessage: cleared }]
    });

    expect(cleared).toHaveBeenCalledWith(expect.objectContaining({ type: 'BACKGROUND_DATA_CLEARED' }));
  });

  it('logs data ingestion steps when the registered script URL carries a debug parameter', async () => {
    const { listeners, fetch, debugConsole } = serviceWorkerHarness([], { search: '?debug=data:ingestion:sw' });
    const runName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    const payloadHashes = JSON.stringify({
      [runName]: 'c'.repeat(64)
    });
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? payloadHashes : 'shard data'
    ));
    const configured = vi.fn();
    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'CONFIGURE_BACKGROUND_DATA',
        urls: ['https://example.test/dashboard/payload-hashes.json']
      },
      ports: [{ postMessage: configured }]
    });

    await dispatchExtendedEvent(listeners.periodicsync, {
      tag: 'central-agentic-ops-dashboard-data'
    });

    expect(debugConsole.debug).toHaveBeenCalledWith(
      '[cao:data:ingestion:sw]',
      'downloading dashboard data',
      expect.anything()
    );
    expect(debugConsole.debug).toHaveBeenCalledWith(
      '[cao:data:ingestion:sw]',
      'dashboard data download complete'
    );
  });

  it('logs data ingestion steps when the page forwards its debug value on DOWNLOAD_DATA', async () => {
    const { listeners, fetch, debugConsole } = serviceWorkerHarness();
    const runName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? JSON.stringify({ [runName]: 'c'.repeat(64) }) : 'shard data'
    ));
    const completed = vi.fn();

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'DOWNLOAD_DATA',
        urls: ['https://example.test/dashboard/payload-hashes.json'],
        debug: '*'
      },
      ports: [{ postMessage: completed }]
    });

    expect(completed).toHaveBeenCalledWith(expect.objectContaining({ type: 'DOWNLOAD_COMPLETE' }));
    expect(debugConsole.debug).toHaveBeenCalledWith(
      '[cao:data:ingestion:sw]',
      'dashboard data download complete'
    );
  });

  it('does not log data ingestion steps without a debug value', async () => {
    const { listeners, fetch, debugConsole } = serviceWorkerHarness();
    const runName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    fetch.mockImplementation(async (url) => new Response(
      String(url).endsWith('/payload-hashes.json') ? JSON.stringify({ [runName]: 'c'.repeat(64) }) : 'shard data'
    ));

    await dispatchExtendedEvent(listeners.message, {
      data: {
        type: 'DOWNLOAD_DATA',
        urls: ['https://example.test/dashboard/payload-hashes.json'],
        debug: ''
      },
      ports: [{ postMessage: vi.fn() }]
    });

    expect(debugConsole.debug).not.toHaveBeenCalled();
  });

  it('matches the page debug logger category semantics for representative patterns', () => {
    const { isDebugEnabled: workerIsDebugEnabled } = serviceWorkerHarness([], { search: '?debug=data:ingestion:*,-data:ingestion:sw:noisy' });
    const cases = [
      'data:ingestion:sw',
      'data:ingestion:sw:noisy',
      'data:ingestion',
      'render',
      'other'
    ];
    for (const category of cases) {
      expect(workerIsDebugEnabled(category))
        .toBe(pageIsDebugEnabled(category, '?debug=data:ingestion:*,-data:ingestion:sw:noisy'));
    }
  });

});
