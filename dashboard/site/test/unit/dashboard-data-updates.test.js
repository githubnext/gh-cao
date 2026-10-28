// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  automaticDashboardBackgroundUpdatesActive,
  automaticDashboardDataUpdatesEnabled,
  dashboardBackgroundUpdatesUnavailableReason,
  dashboardServiceWorkerUrl,
  dashboardInstalled,
  dashboardDataUpdateBlockedReason,
  ensureHealthyDashboardServiceWorker,
  periodicBackgroundSyncSupported,
  requestDashboardRefresh,
  setAutomaticDashboardDataUpdatesEnabled,
  startDashboardAppUpdates,
  startAutomaticDashboardDataUpdates
} from '../../src/dashboard-data-updates.js';
import { dashboardAppUpdateDownloading } from '../../src/dashboard-app-update-state.js';

class FakeWorker extends EventTarget {
  /** @param {boolean} healthy */
  constructor(healthy = true) {
    super();
    this.healthy = healthy;
    this.state = 'activated';
    /** @type {{ type?: string }[]} */
    this.messages = [];
  }

  /** @param {{ type?: string }} message @param {MessagePort[]} [ports] */
  postMessage(message, ports = []) {
    this.messages.push(message);
    if (message.type === 'CANARY' && this.healthy) {
      ports[0]?.postMessage({ type: 'CANARY_OK', version: 'test' });
    }
    if (message.type === 'ACTIVATE') {
      this.state = 'activated';
      this.dispatchEvent(new Event('statechange'));
    }
    if (message.type === 'DOWNLOAD_DATA') {
      ports[0]?.postMessage({ type: 'DOWNLOAD_COMPLETE', version: 'test' });
    }
    if (message.type === 'CONFIGURE_BACKGROUND_DATA') {
      ports[0]?.postMessage({ type: 'BACKGROUND_DATA_CONFIGURED', version: 'test' });
    }
    if (message.type === 'CLEAR_BACKGROUND_DATA') {
      ports[0]?.postMessage({ type: 'BACKGROUND_DATA_CLEARED', version: 'test' });
    }
  }
}

/** @param {FakeWorker} worker */
function registration(worker) {
  return Object.assign(new EventTarget(), {
    active: worker,
    waiting: null,
    installing: /** @type {FakeWorker | null} */ (null),
    update: vi.fn().mockResolvedValue(undefined),
    unregister: vi.fn().mockResolvedValue(true),
    periodicSync: {
      register: vi.fn().mockResolvedValue(undefined),
      getTags: vi.fn().mockResolvedValue(['central-agentic-ops-dashboard-data']),
      unregister: vi.fn().mockResolvedValue(undefined)
    }
  });
}

const grantedPermissions = /** @type {Permissions} */ (/** @type {unknown} */ ({
  query: vi.fn().mockResolvedValue({ state: 'granted' })
}));

describe('automatic dashboard data updates', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    dashboardAppUpdateDownloading.set(false);
  });

  it.each(['installed', 'redundant'])('tracks update downloads until the worker is %s and removes listeners on stop', async (state) => {
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = Object.assign(new EventTarget(), {
      controller: worker,
      register: vi.fn().mockResolvedValue(currentRegistration)
    });
    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (vi.fn()))
    });
    await vi.waitFor(() => expect(worker.messages).toContainEqual(expect.objectContaining({ type: 'CACHE_APP_ASSETS' })));
    expect(dashboardAppUpdateDownloading.get()).toBe(false);

    const candidate = new FakeWorker();
    candidate.state = 'installing';
    Object.assign(currentRegistration, { installing: candidate });
    currentRegistration.dispatchEvent(new Event('updatefound'));
    expect(dashboardAppUpdateDownloading.get()).toBe(true);

    candidate.state = state;
    candidate.dispatchEvent(new Event('statechange'));
    expect(dashboardAppUpdateDownloading.get()).toBe(false);

    candidate.state = 'installing';
    candidate.dispatchEvent(new Event('statechange'));
    expect(dashboardAppUpdateDownloading.get()).toBe(true);
    stop();
    expect(dashboardAppUpdateDownloading.get()).toBe(false);
    candidate.dispatchEvent(new Event('statechange'));
    currentRegistration.dispatchEvent(new Event('updatefound'));
    expect(dashboardAppUpdateDownloading.get()).toBe(false);
  });

  it.each([true, false])('observes an existing installation, with controlled=%s', async (controlled) => {
    const worker = new FakeWorker();
    const candidate = new FakeWorker();
    candidate.state = 'installing';
    const currentRegistration = Object.assign(registration(worker), { installing: candidate });
    const serviceWorkers = Object.assign(new EventTarget(), {
      controller: controlled ? worker : null,
      register: vi.fn().mockResolvedValue(currentRegistration)
    });
    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (vi.fn()))
    });
    await vi.waitFor(() => expect(currentRegistration.update).toHaveBeenCalledOnce());
    expect(dashboardAppUpdateDownloading.get()).toBe(controlled);
    candidate.state = 'redundant';
    candidate.dispatchEvent(new Event('statechange'));
    await vi.waitFor(() => expect(worker.messages).toContainEqual(expect.objectContaining({ type: 'CACHE_APP_ASSETS' })));
    expect(dashboardAppUpdateDownloading.get()).toBe(false);
    stop();
  });

  it('does not start observing a registration that resolves after shutdown', async () => {
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    /** @type {(value: typeof currentRegistration) => void} */
    let resolveRegistration = () => {};
    const registered = new Promise((resolve) => { resolveRegistration = resolve; });
    const serviceWorkers = Object.assign(new EventTarget(), {
      controller: worker,
      register: vi.fn().mockReturnValue(registered)
    });
    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers))
    });
    stop();
    resolveRegistration(currentRegistration);
    await vi.waitFor(() => expect(worker.messages).toContainEqual({ type: 'CANARY' }));
    const candidate = new FakeWorker();
    candidate.state = 'installing';
    Object.assign(currentRegistration, { installing: candidate });
    currentRegistration.dispatchEvent(new Event('updatefound'));
    expect(dashboardAppUpdateDownloading.get()).toBe(false);
    expect(worker.messages).not.toContainEqual(expect.objectContaining({ type: 'CACHE_APP_ASSETS' }));
  });

  it('is off by default and persists explicit opt-in', () => {
    expect(automaticDashboardDataUpdatesEnabled()).toBe(false);
    setAutomaticDashboardDataUpdatesEnabled(true);
    expect(automaticDashboardDataUpdatesEnabled()).toBe(true);
    setAutomaticDashboardDataUpdatesEnabled(false);
    expect(automaticDashboardDataUpdatesEnabled()).toBe(false);
  });

  it('offers an existing app a dismissible update when the new worker takes control', async () => {
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = new EventTarget();
    Object.assign(serviceWorkers, {
      controller: worker,
      register: vi.fn().mockResolvedValue(currentRegistration)
    });

    const reload = vi.fn();
    const notify = vi.fn().mockReturnValue({ dismiss: vi.fn(), update: vi.fn() });
    const setTimer = vi.fn();

    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      scriptUrl: new URL('https://example.test/service-worker.js'),
      reload,
      notify,
      setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (setTimer))
    });
    await vi.waitFor(() => expect(currentRegistration.update).toHaveBeenCalledOnce());

    serviceWorkers.dispatchEvent(new Event('controllerchange'));
    serviceWorkers.dispatchEvent(new Event('controllerchange'));

    expect(notify).toHaveBeenCalledOnce();
    expect(notify.mock.calls[0][0]).toMatchObject({
      message: 'An update to the dashboard has been downloaded. Update now?',
      duration: 0
    });
    expect(reload).not.toHaveBeenCalled();
    notify.mock.calls[0][0].actions[1].run();
    expect(notify.mock.results[0].value.dismiss).toHaveBeenCalledOnce();
    serviceWorkers.dispatchEvent(new Event('controllerchange'));
    expect(notify).toHaveBeenCalledOnce();
    expect(reload).not.toHaveBeenCalled();
    notify.mock.calls[0][0].actions[0].run();
    expect(reload).toHaveBeenCalledOnce();
    expect(worker.messages).toContainEqual(expect.objectContaining({ type: 'CACHE_APP_ASSETS' }));
    stop();
  });

  it('accepts only update messages from the controlling worker and deduplicates the prompt', async () => {
    const worker = new FakeWorker();
    const serviceWorkers = Object.assign(new EventTarget(), {
      controller: worker,
      register: vi.fn().mockResolvedValue(registration(worker))
    });
    const reload = vi.fn();
    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      scriptUrl: new URL('https://example.test/service-worker.js'),
      reload,
      setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (vi.fn()))
    });
    await vi.waitFor(() => expect(serviceWorkers.register).toHaveBeenCalledOnce());
    const untrustedMessage = new MessageEvent('message', {
      data: { type: 'APP_UPDATE_DOWNLOADED' },
      source: window
    });
    serviceWorkers.dispatchEvent(untrustedMessage);
    expect(document.querySelector('.dashboard-notification')).toBeNull();
    serviceWorkers.dispatchEvent(new MessageEvent('message', {
      data: { type: 'APP_UPDATE_DOWNLOADED' },
      source: /** @type {MessageEventSource} */ (/** @type {unknown} */ (worker))
    }));
    serviceWorkers.dispatchEvent(new Event('controllerchange'));
    expect(document.querySelectorAll('.dashboard-notification')).toHaveLength(1);
    expect(reload).not.toHaveBeenCalled();
    /** @type {HTMLButtonElement} */ (document.querySelector('.dashboard-notification-action')).click();
    expect(reload).toHaveBeenCalledOnce();
    stop();
    document.querySelector('.dashboard-notifications')?.remove();
  });

  it('registers a stable service worker script URL that ignores page debug parameters', async () => {
    const originalUrl = window.location.href;
    window.history.replaceState(null, '', '/?debug=*&debug-shard-limit=2&debug-eager-ingest=1');
    try {
      const scriptUrl = dashboardServiceWorkerUrl();
      expect(scriptUrl.search).toBe('');
      expect(scriptUrl.pathname.endsWith('/service-worker.js')).toBe(true);

      const worker = new FakeWorker();
      const currentRegistration = registration(worker);
      const register = vi.fn().mockResolvedValue(currentRegistration);
      const serviceWorkers = Object.assign(new EventTarget(), { controller: worker, register });
      const reload = vi.fn();
      const stop = startDashboardAppUpdates({
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        reload,
        setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (vi.fn()))
      });
      await vi.waitFor(() => expect(register).toHaveBeenCalledOnce());
      const [registeredUrl] = register.mock.calls[0];
      expect(String(registeredUrl)).toBe(scriptUrl.href);
      expect(reload).not.toHaveBeenCalled();
      stop();
    } finally {
      window.history.replaceState(null, '', originalUrl);
    }
  });

  it('checks deployed application assets when a refresh is requested', async () => {
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = new EventTarget();
    Object.assign(serviceWorkers, {
      controller: worker,
      register: vi.fn().mockResolvedValue(currentRegistration)
    });
    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      scriptUrl: new URL('https://example.test/service-worker.js')
    });
    await vi.waitFor(() => expect(worker.messages).toContainEqual(expect.objectContaining({ type: 'CACHE_APP_ASSETS' })));

    requestDashboardRefresh(window);

    await vi.waitFor(() => expect(currentRegistration.update).toHaveBeenCalledTimes(2));
    stop();
  });

  it('does not reload when the first installed worker takes control', async () => {
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = new EventTarget();
    Object.assign(serviceWorkers, {
      controller: null,
      register: vi.fn().mockResolvedValue(currentRegistration)
    });
    const reload = vi.fn();
    const notify = vi.fn();

    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      scriptUrl: new URL('https://example.test/service-worker.js'),
      reload,
      notify
    });
    await vi.waitFor(() => expect(currentRegistration.update).toHaveBeenCalledOnce());

    serviceWorkers.dispatchEvent(new Event('controllerchange'));
    Object.assign(serviceWorkers, { controller: worker });
    serviceWorkers.dispatchEvent(new MessageEvent('message', {
      data: { type: 'APP_UPDATE_DOWNLOADED' },
      source: /** @type {MessageEventSource} */ (/** @type {unknown} */ (worker))
    }));

    expect(reload).not.toHaveBeenCalled();
    expect(notify).not.toHaveBeenCalled();
    stop();
  });

  it('retries an interrupted application update check', async () => {
    const serviceWorkers = new EventTarget();
    const register = vi.fn().mockRejectedValue(new TypeError('connection lost'));
    Object.assign(serviceWorkers, {
      controller: null,
      register
    });
    const setTimer = vi.fn();
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});

    const stop = startDashboardAppUpdates({
      serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      scriptUrl: new URL('https://example.test/service-worker.js'),
      setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (setTimer))
    });
    await vi.waitFor(() => expect(setTimer).toHaveBeenCalledWith(expect.any(Function), 5 * 60 * 1000));

    setTimer.mock.calls[0][0]();
    await vi.waitFor(() => expect(register).toHaveBeenCalledTimes(2));

    stop();
    consoleError.mockRestore();
  });

  it('detects Periodic Background Sync browser support', () => {
    expect(periodicBackgroundSyncSupported(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ ({})),
      /** @type {Permissions} */ (/** @type {unknown} */ ({ query() {} })),
      { periodicSync: {} }
    )).toBe(true);
    expect(periodicBackgroundSyncSupported(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ ({})),
      /** @type {Permissions} */ (/** @type {unknown} */ ({ query() {} })),
      {}
    )).toBe(false);
  });

  it('identifies installed dashboard apps and unavailable background updates', () => {
    expect(dashboardInstalled({ matches: true }, false)).toBe(true);
    expect(dashboardInstalled({ matches: false }, true)).toBe(true);
    expect(dashboardInstalled({ matches: false }, false)).toBe(false);

    expect(dashboardBackgroundUpdatesUnavailableReason()).toBe(
      'Periodic Background Sync is not supported by this browser.'
    );
    expect(dashboardBackgroundUpdatesUnavailableReason(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ ({})),
      /** @type {Permissions} */ (/** @type {unknown} */ ({ query() {} })),
      { periodicSync: {} },
      { matches: false },
      false
    )).toBe('Install this dashboard as an app to enable hourly background downloads.');
    expect(dashboardBackgroundUpdatesUnavailableReason(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ ({})),
      /** @type {Permissions} */ (/** @type {unknown} */ ({ query() {} })),
      { periodicSync: {} },
      { matches: true },
      false
    )).toBeNull();
  });

  it('recognizes minimal-ui app windows alongside standalone windows', () => {
    const matchMedia = vi.fn().mockReturnValue(
      /** @type {MediaQueryList} */ ({ matches: true })
    );
    vi.stubGlobal('matchMedia', matchMedia);
    try {
      expect(dashboardInstalled()).toBe(true);
      expect(matchMedia).toHaveBeenCalledWith('(display-mode: standalone), (display-mode: minimal-ui)');
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('blocks downloads on metered connections and low battery', () => {
    expect(dashboardDataUpdateBlockedReason({ saveData: true }, undefined)).toBe('metered connection');
    expect(dashboardDataUpdateBlockedReason({ metered: true }, undefined)).toBe('metered connection');
    expect(dashboardDataUpdateBlockedReason({ type: 'cellular' }, undefined)).toBe('metered connection');
    expect(dashboardDataUpdateBlockedReason(undefined, { charging: false, level: 0.2 })).toBe('low battery');
    expect(dashboardDataUpdateBlockedReason(undefined, { charging: true, level: 0.1 })).toBeNull();
    expect(dashboardDataUpdateBlockedReason(undefined, { charging: false, level: 0.21 })).toBeNull();
  });

  it('preserves the setting when service workers are unsupported', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    localStorage.setItem('central-agentic-ops.dashboard.background-data-updates-active', 'true');
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (false))
      }
    );

    expect(automaticDashboardDataUpdatesEnabled()).toBe(true);
    expect(automaticDashboardBackgroundUpdatesActive()).toBe(false);
    stop();
  });

  it('preserves the setting when service worker registration fails', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    localStorage.setItem('central-agentic-ops.dashboard.background-data-updates-active', 'true');
    const serviceWorkers = {
      register: vi.fn().mockRejectedValue(new DOMException('Not allowed', 'SecurityError')),
      getRegistrations: vi.fn().mockResolvedValue([])
    };
    const setTimer = vi.fn();
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js'),
        setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (setTimer))
      }
    );

    await vi.waitFor(() => expect(automaticDashboardBackgroundUpdatesActive()).toBe(false));
    expect(automaticDashboardDataUpdatesEnabled()).toBe(true);
    expect(serviceWorkers.register).toHaveBeenCalled();
    expect(setTimer).not.toHaveBeenCalled();
    stop();
  });

  it('registers background sync but does not download while battery constraints block updates', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = { register: vi.fn().mockResolvedValue(currentRegistration) };
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        getBattery: async () => ({ charging: false, level: 0.1 }),
        permissions: grantedPermissions,
        online: () => true
      }
    );

    await vi.waitFor(() => expect(currentRegistration.periodicSync.register).toHaveBeenCalledOnce());
    expect(worker.messages.filter((message) => message.type === 'DOWNLOAD_DATA')).toHaveLength(0);
    stop();
  });

  it('canaries a worker and schedules the next download for one hour later', async () => {
    vi.useFakeTimers();
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = {
      register: vi.fn().mockResolvedValue(currentRegistration)
    };
    let now = 1000;
    const stop = startAutomaticDashboardDataUpdates(
      [
        'https://example.test/payload-hashes.json',
        'https://example.test/payload-hashes.json',
        'https://example.test/inventory-sources.json'
      ],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        getBattery: async () => ({ charging: true, level: 1 }),
        permissions: grantedPermissions,
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js'),
        now: () => now
      }
    );

    await vi.waitFor(() => expect(
      worker.messages.filter((message) => message.type === 'DOWNLOAD_DATA')
    ).toHaveLength(1));
    expect(currentRegistration.update).toHaveBeenCalled();
    expect(currentRegistration.periodicSync.register).toHaveBeenCalledWith(
      'central-agentic-ops-dashboard-data',
      { minInterval: 60 * 60 * 1000 }
    );
    expect(automaticDashboardBackgroundUpdatesActive()).toBe(true);
    expect(worker.messages).toContainEqual(expect.objectContaining({
      type: 'CONFIGURE_BACKGROUND_DATA',
      urls: [
        'https://example.test/payload-hashes.json',
        'https://example.test/payload-hashes.json',
        'https://example.test/inventory-sources.json'
      ]
    }));

    now += 60 * 60 * 1000;
    await vi.advanceTimersByTimeAsync(60 * 60 * 1000);
    await vi.waitFor(() => expect(
      worker.messages.filter((message) => message.type === 'DOWNLOAD_DATA')
    ).toHaveLength(2));
    stop();
  });

  it('refreshes opted-in dashboard data immediately with online=1 despite a recent download', async () => {
    const originalUrl = window.location.href;
    window.history.replaceState(null, '', '/?online=1');
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-update-last-success', '1000');
    const worker = new FakeWorker();
    const serviceWorkers = { register: vi.fn().mockResolvedValue(registration(worker)) };
    const setTimer = vi.fn();
    try {
      const stop = startAutomaticDashboardDataUpdates(
        ['https://example.test/payload-hashes.json'],
        {
          serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
          getBattery: async () => ({ charging: true, level: 1 }),
          permissions: grantedPermissions,
          online: () => true,
          now: () => 2000,
          setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (setTimer))
        }
      );
      await vi.waitFor(() => expect(worker.messages.filter((message) => message.type === 'DOWNLOAD_DATA')).toHaveLength(1));
      expect(setTimer).toHaveBeenCalledWith(expect.any(Function), 60 * 60 * 1000);
      stop();
    } finally {
      window.history.replaceState(null, '', originalUrl);
    }
  });

  it('unregisters periodic background updates when disabled', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = {
      register: vi.fn().mockResolvedValue(currentRegistration)
    };
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        getBattery: async () => ({ charging: true, level: 1 }),
        permissions: grantedPermissions,
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js')
      }
    );
    await vi.waitFor(() => expect(currentRegistration.periodicSync.register).toHaveBeenCalledOnce());

    setAutomaticDashboardDataUpdatesEnabled(false);

    await vi.waitFor(() => expect(currentRegistration.periodicSync.unregister)
      .toHaveBeenCalledWith('central-agentic-ops-dashboard-data'));
    expect(worker.messages).toContainEqual({ type: 'CLEAR_BACKGROUND_DATA' });
    expect(currentRegistration.unregister).not.toHaveBeenCalled();
    stop();
  });

  it('unregisters background sync when disabled during configuration', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = {
      register: vi.fn().mockResolvedValue(currentRegistration)
    };
    /** @type {(value: { state: string }) => void} */
    let resolvePermission = () => {};
    const permissions = /** @type {Permissions} */ (/** @type {unknown} */ ({
      query: vi.fn().mockReturnValue(new Promise((resolve) => {
        resolvePermission = resolve;
      }))
    }));
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        permissions,
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js')
      }
    );
    await vi.waitFor(() => expect(permissions.query).toHaveBeenCalledOnce());

    setAutomaticDashboardDataUpdatesEnabled(false);
    resolvePermission({ state: 'granted' });

    await vi.waitFor(() => expect(worker.messages).toContainEqual({ type: 'CLEAR_BACKGROUND_DATA' }));
    expect(automaticDashboardBackgroundUpdatesActive()).toBe(false);
    expect(worker.messages.filter((message) => message.type === 'DOWNLOAD_DATA')).toHaveLength(0);
    stop();
  });

  it('discovers and removes an untracked dashboard worker while disabled', async () => {
    const worker = new FakeWorker();
    const orphanedRegistration = {
      ...registration(worker),
      scope: 'https://example.test/'
    };
    Object.assign(worker, { scriptURL: 'https://example.test/service-worker.js?force-update=1' });
    const serviceWorkers = {
      getRegistrations: vi.fn().mockResolvedValue([orphanedRegistration])
    };
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        scriptUrl: new URL('https://example.test/service-worker.js')
      }
    );

    await vi.waitFor(() => expect(worker.messages).toContainEqual({ type: 'CLEAR_BACKGROUND_DATA' }));
    expect(worker.messages).toContainEqual({ type: 'CLEAR_BACKGROUND_DATA' });
    stop();
  });

  it('preserves the setting when periodic background sync is denied', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    localStorage.setItem('central-agentic-ops.dashboard.background-data-updates-active', 'true');
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = {
      register: vi.fn().mockResolvedValue(currentRegistration)
    };
    const setTimer = vi.fn();
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        getBattery: async () => ({ charging: true, level: 1 }),
        permissions: /** @type {Permissions} */ (/** @type {unknown} */ ({
          query: vi.fn().mockResolvedValue({ state: 'denied' })
        })),
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js'),
        setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (setTimer))
      }
    );

    await vi.waitFor(() => expect(worker.messages).toContainEqual({ type: 'CLEAR_BACKGROUND_DATA' }));
    expect(automaticDashboardDataUpdatesEnabled()).toBe(true);
    expect(automaticDashboardBackgroundUpdatesActive()).toBe(false);
    expect(worker.messages.filter((message) => message.type === 'DOWNLOAD_DATA')).toHaveLength(0);
    expect(setTimer).not.toHaveBeenCalled();
    window.dispatchEvent(new Event('online'));
    await Promise.resolve();
    expect(serviceWorkers.register).toHaveBeenCalledOnce();
    stop();
  });

  it('requests periodic background sync permission when prompted', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    const serviceWorkers = {
      register: vi.fn().mockResolvedValue(currentRegistration)
    };
    const permissions = /** @type {Permissions} */ (/** @type {unknown} */ ({
      query: vi.fn().mockResolvedValue({ state: 'prompt' })
    }));
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        getBattery: async () => ({ charging: true, level: 1 }),
        permissions,
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js')
      }
    );

    await vi.waitFor(() => expect(currentRegistration.periodicSync.register).toHaveBeenCalledOnce());
    expect(permissions.query).toHaveBeenCalledWith({ name: 'periodic-background-sync' });
    expect(automaticDashboardBackgroundUpdatesActive()).toBe(true);
    stop();
  });

  it('preserves the setting when periodic background sync is unsupported', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    localStorage.setItem('central-agentic-ops.dashboard.background-data-updates-active', 'true');
    const worker = new FakeWorker();
    const currentRegistration = { ...registration(worker), periodicSync: undefined };
    const serviceWorkers = {
      register: vi.fn().mockResolvedValue(currentRegistration)
    };
    const setTimer = vi.fn();
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        getBattery: async () => ({ charging: true, level: 1 }),
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js'),
        setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (setTimer))
      }
    );

    await vi.waitFor(() => expect(worker.messages).toContainEqual({ type: 'CLEAR_BACKGROUND_DATA' }));
    expect(automaticDashboardDataUpdatesEnabled()).toBe(true);
    expect(automaticDashboardBackgroundUpdatesActive()).toBe(false);
    expect(worker.messages.filter((message) => message.type === 'DOWNLOAD_DATA')).toHaveLength(0);
    expect(setTimer).not.toHaveBeenCalled();
    stop();
  });

  it('does not retry when periodic background sync registration is not allowed', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    const worker = new FakeWorker();
    const currentRegistration = registration(worker);
    currentRegistration.periodicSync.register.mockRejectedValue(
      new DOMException('Not allowed', 'NotAllowedError')
    );
    const serviceWorkers = {
      register: vi.fn().mockResolvedValue(currentRegistration)
    };
    const setTimer = vi.fn();
    const stop = startAutomaticDashboardDataUpdates(
      ['https://example.test/payload-hashes.json'],
      {
        serviceWorkers: /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
        permissions: grantedPermissions,
        online: () => true,
        scriptUrl: new URL('https://example.test/service-worker.js'),
        setTimer: /** @type {typeof window.setTimeout} */ (/** @type {unknown} */ (setTimer))
      }
    );

    await vi.waitFor(() => expect(worker.messages).toContainEqual({ type: 'CLEAR_BACKGROUND_DATA' }));
    expect(automaticDashboardDataUpdatesEnabled()).toBe(true);
    expect(automaticDashboardBackgroundUpdatesActive()).toBe(false);
    expect(setTimer).not.toHaveBeenCalled();
    stop();
  });

  it('waits for asset installation beyond the canary timeout without unregistering', async () => {
    vi.useFakeTimers();
    const worker = new FakeWorker();
    worker.state = 'installing';
    const currentRegistration = {
      ...registration(worker),
      active: null,
      installing: worker
    };
    const serviceWorkers = { register: vi.fn().mockResolvedValue(currentRegistration) };
    const healthy = ensureHealthyDashboardServiceWorker(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      new URL('https://example.test/service-worker.js')
    );
    const result = expect(healthy).resolves.toMatchObject({ worker });

    await vi.advanceTimersByTimeAsync(3500);
    expect(currentRegistration.unregister).not.toHaveBeenCalled();
    expect(serviceWorkers.register).toHaveBeenCalledOnce();
    worker.state = 'installed';
    worker.dispatchEvent(new Event('statechange'));
    await result;
    expect(worker.state).toBe('activated');
  });

  it('does not report an activating worker as healthy before activation completes', async () => {
    vi.useFakeTimers();
    const worker = new FakeWorker();
    worker.state = 'activating';
    const currentRegistration = registration(worker);
    const serviceWorkers = { register: vi.fn().mockResolvedValue(currentRegistration) };
    const healthy = ensureHealthyDashboardServiceWorker(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      new URL('https://example.test/service-worker.js')
    );
    const settled = vi.fn();
    const result = healthy.then(settled);

    await vi.advanceTimersByTimeAsync(3500);
    expect(settled).not.toHaveBeenCalled();
    expect(currentRegistration.unregister).not.toHaveBeenCalled();
    worker.state = 'activated';
    worker.dispatchEvent(new Event('statechange'));
    await result;
    expect(settled).toHaveBeenCalledWith({ registration: currentRegistration, worker });
  });

  it('reports an installation timeout without tearing down the pending worker', async () => {
    vi.useFakeTimers();
    const worker = new FakeWorker();
    worker.state = 'installing';
    const currentRegistration = {
      ...registration(worker),
      active: null,
      installing: worker
    };
    const serviceWorkers = { register: vi.fn().mockResolvedValue(currentRegistration) };
    const result = expect(ensureHealthyDashboardServiceWorker(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      new URL('https://example.test/service-worker.js')
    )).rejects.toThrow('Service worker installation timed out.');

    await vi.advanceTimersByTimeAsync(120000);
    await result;
    expect(currentRegistration.unregister).not.toHaveBeenCalled();
    expect(serviceWorkers.register).toHaveBeenCalledOnce();
  });

  it('reports an activation timeout without unregistering the activating worker', async () => {
    vi.useFakeTimers();
    const worker = new FakeWorker();
    worker.state = 'activating';
    const currentRegistration = registration(worker);
    const serviceWorkers = { register: vi.fn().mockResolvedValue(currentRegistration) };
    const result = expect(ensureHealthyDashboardServiceWorker(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      new URL('https://example.test/service-worker.js')
    )).rejects.toThrow('Service worker activation timed out.');

    await vi.advanceTimersByTimeAsync(120000);
    await result;
    expect(currentRegistration.unregister).not.toHaveBeenCalled();
    expect(serviceWorkers.register).toHaveBeenCalledOnce();
  });

  it.each([false, true])('waits for a waiting worker to activate, with recovery=%s', async (recovery) => {
    vi.useFakeTimers();
    const worker = new FakeWorker();
    worker.state = 'installed';
    const postMessage = worker.postMessage.bind(worker);
    vi.spyOn(worker, 'postMessage').mockImplementation((message, ports) => {
      if (message.type !== 'ACTIVATE') return postMessage(message, ports);
      worker.state = 'activating';
      worker.dispatchEvent(new Event('statechange'));
    });
    const currentRegistration = {
      ...registration(new FakeWorker()),
      waiting: worker
    };
    const brokenRegistration = registration(new FakeWorker(false));
    const serviceWorkers = {
      register: recovery
        ? vi.fn().mockResolvedValueOnce(brokenRegistration).mockResolvedValue(currentRegistration)
        : vi.fn().mockResolvedValue(currentRegistration)
    };
    const settled = vi.fn();
    const result = ensureHealthyDashboardServiceWorker(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      new URL('https://example.test/service-worker.js')
    ).then(settled);

    await vi.advanceTimersByTimeAsync(3500);
    expect(worker.messages).toContainEqual({ type: 'CANARY' });
    expect(worker.state).toBe('activating');
    expect(settled).not.toHaveBeenCalled();
    worker.state = 'activated';
    worker.dispatchEvent(new Event('statechange'));
    await result;
    expect(settled).toHaveBeenCalledWith({ registration: currentRegistration, worker });
    expect(currentRegistration.unregister).not.toHaveBeenCalled();
    expect(brokenRegistration.unregister).not.toHaveBeenCalled();
  });

  it('force-registers a cache-busted worker without unregistering when the active canary fails', async () => {
    vi.useFakeTimers();
    const brokenRegistration = registration(new FakeWorker(false));
    const recoveredWorker = new FakeWorker();
    const recoveredRegistration = registration(recoveredWorker);
    const serviceWorkers = {
      register: vi.fn()
        .mockResolvedValueOnce(brokenRegistration)
        .mockResolvedValueOnce(recoveredRegistration)
    };
    const recovery = ensureHealthyDashboardServiceWorker(
      /** @type {ServiceWorkerContainer} */ (/** @type {unknown} */ (serviceWorkers)),
      new URL('https://example.test/service-worker.js')
    );

    await vi.advanceTimersByTimeAsync(3000);
    await recovery;

    expect(brokenRegistration.unregister).not.toHaveBeenCalled();
    expect(serviceWorkers.register).toHaveBeenCalledTimes(2);
    expect(String(serviceWorkers.register.mock.calls[1][0])).toContain('force-update=');
  });
});
