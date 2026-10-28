import { scopedStorageKey } from './storage-scope.js';
import { createDebug, debugParameter } from './debug.js';
import { publishNotification } from './notification-service.js';
import { dashboardAppUpdateDownloading } from './dashboard-app-update-state.js';

const debugServiceWorker = createDebug('data:ingestion:sw-client');

const ENABLED_STORAGE_KEY = scopedStorageKey('central-agentic-ops.dashboard.automatic-data-updates');
const LAST_SUCCESS_STORAGE_KEY = scopedStorageKey('central-agentic-ops.dashboard.automatic-data-update-last-success');
const BACKGROUND_ACTIVE_STORAGE_KEY = scopedStorageKey('central-agentic-ops.dashboard.background-data-updates-active');
const SETTING_EVENT = 'dashboard-automatic-data-updates-setting-change';
const BACKGROUND_STATUS_EVENT = 'dashboard-background-data-updates-status-change';
const UPDATE_INTERVAL_MS = 60 * 60 * 1000;
const RETRY_INTERVAL_MS = 5 * 60 * 1000;
const LOW_BATTERY_LEVEL = 0.2;
const CANARY_TIMEOUT_MS = 3000;
const DOWNLOAD_TIMEOUT_MS = 2 * 60 * 1000;
const PERIODIC_SYNC_TAG = 'central-agentic-ops-dashboard-data';
const APP_UPDATE_INTERVAL_MS = 60 * 60 * 1000;
export const DASHBOARD_REFRESH_REQUEST_EVENT = 'dashboard-refresh-request';

/** Requests both a fresh data download and a deployed application version check. */
export function requestDashboardRefresh(eventTarget = window) {
  eventTarget.dispatchEvent(new Event(DASHBOARD_REFRESH_REQUEST_EVENT));
}

/** @typedef {{ saveData?: boolean, metered?: boolean, type?: string, effectiveType?: string, addEventListener?: EventTarget['addEventListener'], removeEventListener?: EventTarget['removeEventListener'] }} ConnectionState */
/** @typedef {{ charging: boolean, level: number, addEventListener?: EventTarget['addEventListener'], removeEventListener?: EventTarget['removeEventListener'] }} BatteryState */

class NonRetryableBackgroundSyncError extends Error {}

/** @param {unknown} error */
function isPermissionDeniedError(error) {
  const errorName = error && typeof error === 'object'
    ? /** @type {{ name?: unknown }} */ (error).name
    : undefined;
  return errorName === 'NotAllowedError' || errorName === 'SecurityError';
}

/** @param {Storage} [storage] */
export function automaticDashboardDataUpdatesEnabled(storage = localStorage) {
  return storage.getItem(ENABLED_STORAGE_KEY) === 'true';
}

/** @param {Storage} [storage] */
export function automaticDashboardBackgroundUpdatesActive(storage = localStorage) {
  return storage.getItem(BACKGROUND_ACTIVE_STORAGE_KEY) === 'true';
}

/**
 * @param {ServiceWorkerContainer | undefined} [serviceWorkers]
 * @param {Permissions | undefined} [permissions]
 * @param {object | undefined} [registrationPrototype]
 */
export function periodicBackgroundSyncSupported(
  serviceWorkers = navigator.serviceWorker,
  permissions = navigator.permissions,
  registrationPrototype = globalThis.ServiceWorkerRegistration?.prototype
) {
  return Boolean(
    serviceWorkers
    && typeof permissions?.query === 'function'
    && registrationPrototype
    && 'periodicSync' in registrationPrototype
  );
}

/**
 * @param {{ matches: boolean } | undefined} [standaloneDisplayMode]
 * @param {boolean | undefined} [navigatorStandalone]
 */
export function dashboardInstalled(
  standaloneDisplayMode = window.matchMedia?.('(display-mode: standalone), (display-mode: minimal-ui)'),
  navigatorStandalone = /** @type {Navigator & { standalone?: boolean }} */ (navigator).standalone
) {
  return standaloneDisplayMode?.matches === true || navigatorStandalone === true;
}

/**
 * @param {ServiceWorkerContainer | undefined} [serviceWorkers]
 * @param {Permissions | undefined} [permissions]
 * @param {object | undefined} [registrationPrototype]
 * @param {{ matches: boolean } | undefined} [standaloneDisplayMode]
 * @param {boolean | undefined} [navigatorStandalone]
 * @returns {string | null}
 */
export function dashboardBackgroundUpdatesUnavailableReason(
  serviceWorkers,
  permissions,
  registrationPrototype,
  standaloneDisplayMode,
  navigatorStandalone
) {
  if (!periodicBackgroundSyncSupported(serviceWorkers, permissions, registrationPrototype)) {
    return 'Periodic Background Sync is not supported by this browser.';
  }
  if (!dashboardInstalled(standaloneDisplayMode, navigatorStandalone)) {
    return 'Install this dashboard as an app to enable hourly background downloads.';
  }
  return null;
}

/** @param {EventListener} listener */
export function onAutomaticDashboardBackgroundUpdateStatus(listener) {
  const eventTarget = window;
  eventTarget.addEventListener(BACKGROUND_STATUS_EVENT, listener);
  return () => {
    if (typeof eventTarget.removeEventListener === 'function') {
      eventTarget.removeEventListener(BACKGROUND_STATUS_EVENT, listener);
    }
  };
}

/** @param {boolean} enabled @param {Storage} [storage] */
export function setAutomaticDashboardDataUpdatesEnabled(enabled, storage = localStorage) {
  if (enabled) {
    storage.setItem(ENABLED_STORAGE_KEY, 'true');
    storage.removeItem(BACKGROUND_ACTIVE_STORAGE_KEY);
  }
  else {
    storage.removeItem(ENABLED_STORAGE_KEY);
    storage.removeItem(LAST_SUCCESS_STORAGE_KEY);
    storage.removeItem(BACKGROUND_ACTIVE_STORAGE_KEY);
    window.dispatchEvent(new Event(BACKGROUND_STATUS_EVENT));
  }
  window.dispatchEvent(new Event(SETTING_EVENT));
}

/**
 * @param {ConnectionState | undefined} connection
 * @param {BatteryState | undefined} battery
 */
export function dashboardDataUpdateBlockedReason(connection, battery) {
  if (connection?.saveData || connection?.metered || connection?.type === 'cellular'
    || connection?.effectiveType === 'slow-2g' || connection?.effectiveType === '2g') return 'metered connection';
  if (battery && !battery.charging && battery.level <= LOW_BATTERY_LEVEL) return 'low battery';
  return null;
}

/**
 * Returns the stable service worker script URL. It deliberately omits page
 * query parameters such as `?debug=`: registering a different script URL for
 * the same scope is a service worker update, and activating that update prompts
 * every already-controlled dashboard page to reload.
 */
export function dashboardServiceWorkerUrl() {
  return new URL('../service-worker.js', import.meta.url);
}

/**
 * @param {ServiceWorker} worker
 * @param {unknown} message
 * @param {number} timeoutMs
 * @returns {Promise<unknown>}
 */
function requestWorker(worker, message, timeoutMs) {
  return new Promise((resolve, reject) => {
    const channel = new MessageChannel();
    const timeout = window.setTimeout(() => {
      channel.port1.close();
      reject(new Error('Service worker response timed out.'));
    }, timeoutMs);
    channel.port1.onmessage = (event) => {
      window.clearTimeout(timeout);
      channel.port1.close();
      resolve(event.data);
    };
    worker.postMessage(message, [channel.port2]);
  });
}

/**
 * @param {ServiceWorkerRegistration} registration
 * @param {ServiceWorker} worker
 * @param {string[]} dataUrls
 * @param {Permissions | undefined} permissions
 */
async function configureBackgroundDashboardDataUpdates(registration, worker, dataUrls, permissions) {
  const response = await requestWorker(
    worker,
    {
      type: 'CONFIGURE_BACKGROUND_DATA',
      urls: dataUrls,
      assets: [
        new URL('./', window.location.href).href,
        ...(globalThis.performance?.getEntriesByType?.('resource') ?? []).map((entry) => entry.name)
      ]
    },
    CANARY_TIMEOUT_MS
  );
  if (!response || typeof response !== 'object'
      || /** @type {{ type?: unknown }} */ (response).type !== 'BACKGROUND_DATA_CONFIGURED') {
    throw new Error('Service worker background data configuration failed.');
  }
  const lastSuccess = Number(/** @type {{ lastSuccess?: unknown }} */ (response).lastSuccess ?? 0);
  const periodicSync = /** @type {ServiceWorkerRegistration & { periodicSync?: { register: (tag: string, options: { minInterval: number }) => Promise<void>, getTags: () => Promise<string[]> } }} */ (registration).periodicSync;
  if (!periodicSync || !permissions?.query) {
    throw new NonRetryableBackgroundSyncError('Periodic Background Sync is not supported by this browser.');
  }
  const permission = await permissions.query(
    /** @type {PermissionDescriptor} */ (/** @type {unknown} */ ({ name: 'periodic-background-sync' }))
  );
  if (permission.state === 'denied') {
    throw new NonRetryableBackgroundSyncError('Periodic Background Sync permission was denied.');
  }
  try {
    await periodicSync.register(PERIODIC_SYNC_TAG, { minInterval: UPDATE_INTERVAL_MS });
  } catch (error) {
    if (isPermissionDeniedError(error)) {
      throw new NonRetryableBackgroundSyncError('Periodic Background Sync registration was not allowed.');
    }
    throw error;
  }
  if (!(await periodicSync.getTags()).includes(PERIODIC_SYNC_TAG)) {
    throw new Error('Periodic Background Sync registration could not be verified.');
  }
  return Number.isFinite(lastSuccess) && lastSuccess > 0 ? lastSuccess : 0;
}

/**
 * Clears background-data state without removing the app worker used for asset
 * caching and application updates.
 * @param {ServiceWorkerContainer | undefined} serviceWorkers
 * @param {URL} scriptUrl
 * @param {ServiceWorkerRegistration | undefined} current
 */
async function disableDashboardBackgroundUpdates(serviceWorkers, scriptUrl, current) {
  if (!serviceWorkers) return;
  const scope = new URL('./', scriptUrl).href;
  const discovered = await serviceWorkers.getRegistrations?.().catch(() => []) ?? [];
  /** @type {ServiceWorkerRegistration[]} */
  const registrations = [];
  for (const candidate of [current, ...discovered]) {
    const candidateWorkers = candidate
      ? [candidate.installing, candidate.waiting, candidate.active].filter(Boolean)
      : [];
    const ownsScope = candidate === current || (
      candidate?.scope === scope && candidateWorkers.some((worker) => {
        const workerUrl = new URL(/** @type {ServiceWorker} */ (worker).scriptURL);
        return workerUrl.origin === scriptUrl.origin && workerUrl.pathname === scriptUrl.pathname;
      })
    );
    if (candidate && ownsScope && !registrations.includes(candidate)) {
      registrations.push(candidate);
    }
  }
  if (registrations.length === 0) {
    const registration = await serviceWorkers.getRegistration?.(scope);
    if (registration?.scope === scope) registrations.push(registration);
  }
  await Promise.all(registrations.map(async (registration) => {
    const workers = [registration.installing, registration.waiting, registration.active].filter(Boolean);
    await Promise.all(workers.map((worker) =>
      requestWorker(/** @type {ServiceWorker} */ (worker), { type: 'CLEAR_BACKGROUND_DATA' }, CANARY_TIMEOUT_MS)
        .catch(() => undefined)
    ));
    const periodicSync = /** @type {ServiceWorkerRegistration & { periodicSync?: { unregister: (tag: string) => Promise<void> } }} */ (registration).periodicSync;
    await periodicSync?.unregister(PERIODIC_SYNC_TAG).catch(() => undefined);
  }));
}

/** @param {ServiceWorker} worker */
async function canaryWorker(worker) {
  const response = await requestWorker(worker, { type: 'CANARY' }, CANARY_TIMEOUT_MS);
  return Boolean(response && typeof response === 'object'
    && /** @type {{ type?: unknown }} */ (response).type === 'CANARY_OK');
}

/** @param {ServiceWorker | null} worker */
function waitForWorker(worker) {
  if (!worker || worker.state === 'redundant') return Promise.resolve(null);
  if (worker.state === 'installed' || worker.state === 'activated') return Promise.resolve(worker);
  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(() => {
      worker.removeEventListener('statechange', onStateChange);
      reject(new Error('Service worker installation timed out.'));
    }, DOWNLOAD_TIMEOUT_MS);
    const onStateChange = () => {
      if (!['installed', 'activated', 'redundant'].includes(worker.state)) return;
      window.clearTimeout(timeout);
      worker.removeEventListener('statechange', onStateChange);
      resolve(worker.state === 'redundant' ? null : worker);
    };
    worker.addEventListener('statechange', onStateChange);
  });
}

/** @param {ServiceWorker} worker */
function waitForActivation(worker) {
  if (worker.state === 'activated') return Promise.resolve(worker);
  if (worker.state === 'redundant') return Promise.resolve(null);
  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(() => {
      worker.removeEventListener('statechange', onStateChange);
      reject(new Error('Service worker activation timed out.'));
    }, DOWNLOAD_TIMEOUT_MS);
    const onStateChange = () => {
      if (!['activated', 'redundant'].includes(worker.state)) return;
      window.clearTimeout(timeout);
      worker.removeEventListener('statechange', onStateChange);
      resolve(worker.state === 'activated' ? worker : null);
    };
    worker.addEventListener('statechange', onStateChange);
  });
}

/**
 * Updates the registration without HTTP cache reuse, canaries a waiting worker
 * before activation, and force-registers a cache-busted script if the active
 * worker cannot answer the canary. Recovery updates the existing registration:
 * unregistering during installation or activation can invalidate clients.claim().
 * @param {ServiceWorkerContainer} serviceWorkers
 * @param {URL} scriptUrl
 * @param {(registration: ServiceWorkerRegistration) => void} [onRegistration]
 */
export async function ensureHealthyDashboardServiceWorker(serviceWorkers, scriptUrl, onRegistration) {
  const options = { scope: new URL('./', scriptUrl).pathname, updateViaCache: /** @type {ServiceWorkerUpdateViaCache} */ ('none') };
  debugServiceWorker('registering service worker', { scriptUrl: scriptUrl.href, scope: options.scope });
  let registration = await serviceWorkers.register(scriptUrl, options);
  onRegistration?.(registration);
  if (registration.active) await registration.update();

  const candidate = await waitForWorker(registration.waiting ?? registration.installing);
  if (candidate) {
    let healthy = false;
    try {
      healthy = await canaryWorker(candidate);
    } catch (error) {
      debugServiceWorker('candidate worker canary failed', { message: error instanceof Error ? error.message : String(error) });
      // Keep the healthy active worker; a later update can replace this candidate.
    }
    if (healthy) {
      if (candidate.state === 'installed') candidate.postMessage({ type: 'ACTIVATE' });
      const activated = await waitForActivation(candidate);
      if (activated) {
        debugServiceWorker('activated candidate worker');
        return { registration, worker: activated };
      }
    }
  }

  const active = registration.active ? await waitForActivation(registration.active) : null;
  if (active) {
    try {
      if (await canaryWorker(active)) {
        debugServiceWorker('active worker canary succeeded');
        return { registration, worker: active };
      }
    } catch (error) {
      debugServiceWorker('active worker canary failed', { message: error instanceof Error ? error.message : String(error) });
      // Force recovery below when the controlling worker cannot answer.
    }
  }

  debugServiceWorker('forcing service worker recovery');
  const recoveryUrl = new URL(scriptUrl);
  recoveryUrl.searchParams.set('force-update', String(Date.now()));
  registration = await serviceWorkers.register(recoveryUrl, options);
  onRegistration?.(registration);
  const recovered = await waitForWorker(registration.waiting ?? registration.installing ?? registration.active);
  if (!recovered || !await canaryWorker(recovered)) {
    throw new Error('Unable to install a healthy dashboard service worker.');
  }
  if (recovered.state === 'installed') recovered.postMessage({ type: 'ACTIVATE' });
  const activated = await waitForActivation(recovered);
  if (!activated) throw new Error('Unable to activate a healthy dashboard service worker.');
  return { registration, worker: activated };
}

/**
 * Keeps the application worker current, reports update installation to the
 * reactive footer, and offers a reload after a verified replacement takes control.
 * @param {{
 *   serviceWorkers?: ServiceWorkerContainer,
 *   scriptUrl?: URL,
 *   reload?: () => void,
 *   notify?: typeof publishNotification,
 *   setTimer?: typeof window.setTimeout,
 *   clearTimer?: typeof window.clearTimeout
 * }} [dependencies]
 */
export function startDashboardAppUpdates(dependencies = {}) {
  const serviceWorkers = dependencies.serviceWorkers ?? navigator.serviceWorker;
  if (!serviceWorkers) return () => {};
  const scriptUrl = dependencies.scriptUrl ?? dashboardServiceWorkerUrl();
  const reload = dependencies.reload ?? window.location.reload.bind(window.location);
  const notify = dependencies.notify ?? publishNotification;
  const setTimer = dependencies.setTimer ?? window.setTimeout.bind(window);
  const clearTimer = dependencies.clearTimer ?? window.clearTimeout.bind(window);
  let controlled = Boolean(serviceWorkers.controller);
  let firstInstall = !controlled;
  let notified = false;
  let stopped = false;
  let checking = false;
  /** @type {number | undefined} */
  let timer;
  /** @type {ServiceWorkerRegistration | undefined} */
  let observedRegistration;
  /** @type {ServiceWorker | null} */
  let installingWorker = null;

  const syncDownloading = () => {
    dashboardAppUpdateDownloading.set(!stopped && controlled && installingWorker?.state === 'installing');
  };
  const onUpdateFound = () => {
    installingWorker?.removeEventListener('statechange', syncDownloading);
    installingWorker = observedRegistration?.installing ?? null;
    installingWorker?.addEventListener('statechange', syncDownloading);
    syncDownloading();
  };
  /** @param {ServiceWorkerRegistration} registration */
  const observeRegistration = (registration) => {
    if (stopped || observedRegistration === registration) return;
    observedRegistration?.removeEventListener('updatefound', onUpdateFound);
    observedRegistration = registration;
    registration.addEventListener('updatefound', onUpdateFound);
    onUpdateFound();
  };

  const offerUpdate = () => {
    if (stopped || notified || !controlled) return;
    notified = true;
    /** @type {import('./notification-service.js').NotificationHandle} */
    let notification;
    notification = notify({
      message: 'An update to the dashboard has been downloaded. Update now?',
      duration: 0,
      actions: [
        { label: 'Update now', run: () => { notification.dismiss(); reload(); } },
        { label: 'Dismiss', run: () => notification.dismiss() }
      ]
    });
  };
  const onControllerChange = () => {
    if (controlled) {
      firstInstall = false;
      offerUpdate();
    }
    controlled = true;
    syncDownloading();
  };
  /** @param {MessageEvent} event */
  const onWorkerMessage = (event) => {
    if (!firstInstall && event.data?.type === 'APP_UPDATE_DOWNLOADED'
        && event.source === serviceWorkers.controller) {
      offerUpdate();
    }
  };
  serviceWorkers.addEventListener?.('controllerchange', onControllerChange);
  serviceWorkers.addEventListener?.('message', onWorkerMessage);

  /** @param {number} delay */
  const schedule = (delay) => {
    if (stopped) return;
    if (timer !== undefined) clearTimer(timer);
    timer = setTimer(() => void checkForUpdate(), delay);
  };
  const checkForUpdate = async () => {
    if (checking || stopped) return;
    checking = true;
    try {
      const { worker } = await ensureHealthyDashboardServiceWorker(serviceWorkers, scriptUrl, observeRegistration);
      if (stopped) return;
      worker.postMessage({
        type: 'CACHE_APP_ASSETS',
        urls: [
          new URL('./', window.location.href).href,
          new URL('../manifest.webmanifest', import.meta.url).href,
          ...(globalThis.performance?.getEntriesByType?.('resource') ?? []).map((entry) => entry.name)
        ]
      });
      schedule(APP_UPDATE_INTERVAL_MS);
    } catch (error) {
      console.error(`Unable to update dashboard application assets: ${error instanceof Error ? error.message : String(error)}`);
      schedule(RETRY_INTERVAL_MS);
    } finally {
      checking = false;
    }
  };
  const onRefreshRequest = () => void checkForUpdate();
  window.addEventListener(DASHBOARD_REFRESH_REQUEST_EVENT, onRefreshRequest);
  void checkForUpdate();

  return () => {
    stopped = true;
    observedRegistration?.removeEventListener('updatefound', onUpdateFound);
    installingWorker?.removeEventListener('statechange', syncDownloading);
    syncDownloading();
    if (timer !== undefined) clearTimer(timer);
    serviceWorkers.removeEventListener?.('controllerchange', onControllerChange);
    serviceWorkers.removeEventListener?.('message', onWorkerMessage);
    window.removeEventListener(DASHBOARD_REFRESH_REQUEST_EVENT, onRefreshRequest);
  };
}

/**
 * @param {string[]} dataUrls
 * @param {{
 *   serviceWorkers?: ServiceWorkerContainer,
 *   storage?: Storage,
 *   connection?: ConnectionState,
 *   getBattery?: () => Promise<BatteryState>,
 *   permissions?: Permissions,
 *   online?: () => boolean,
 *   scriptUrl?: URL,
 *   now?: () => number,
 *   setTimer?: typeof window.setTimeout,
 *   clearTimer?: typeof window.clearTimeout
 * }} [dependencies]
 */
export function startAutomaticDashboardDataUpdates(dataUrls, dependencies = {}) {
  const serviceWorkers = dependencies.serviceWorkers ?? navigator.serviceWorker;
  const storage = dependencies.storage ?? localStorage;
  const connection = dependencies.connection
    ?? /** @type {Navigator & { connection?: ConnectionState }} */ (navigator).connection;
  const getBattery = dependencies.getBattery
    ?? /** @type {Navigator & { getBattery?: () => Promise<BatteryState> }} */ (navigator).getBattery?.bind(navigator);
  const permissions = dependencies.permissions ?? navigator.permissions;
  const online = dependencies.online ?? (() => navigator.onLine);
  const scriptUrl = dependencies.scriptUrl ?? dashboardServiceWorkerUrl();
  const now = dependencies.now ?? Date.now;
  const setTimer = dependencies.setTimer ?? window.setTimeout.bind(window);
  const clearTimer = dependencies.clearTimer ?? window.clearTimeout.bind(window);
  /** @type {number | undefined} */
  let timer;
  let stopped = false;
  let running = false;
  let rerun = false;
  let backgroundConfigured = false;
  let backgroundSyncUnavailable = false;
  let onlineRefreshPending = new URL(window.location.href).searchParams.get('online') === '1';
  /** @type {BatteryState | undefined} */
  let battery;
  /** @type {ServiceWorkerRegistration | undefined} */
  let registration;
  /** @type {ServiceWorker | undefined} */
  let healthyWorker;
  const markBackgroundUpdatesInactive = () => {
    storage.removeItem(BACKGROUND_ACTIVE_STORAGE_KEY);
    window.dispatchEvent(new Event(BACKGROUND_STATUS_EVENT));
  };

  /** @param {number} delay @param {boolean} [checkForWorkerUpdate] */
  const schedule = (delay, checkForWorkerUpdate = false) => {
    if (stopped) return;
    if (timer !== undefined) clearTimer(timer);
    timer = setTimer(() => {
      if (checkForWorkerUpdate) {
        healthyWorker = undefined;
        backgroundConfigured = false;
      }
      void reconcile();
    }, delay);
  };
  const performReconcile = async () => {
    if (stopped) return;
    if (!automaticDashboardDataUpdatesEnabled(storage)) {
      backgroundSyncUnavailable = false;
      await disableDashboardBackgroundUpdates(serviceWorkers, scriptUrl, registration);
      registration = undefined;
      healthyWorker = undefined;
      backgroundConfigured = false;
      return;
    }
    if (backgroundSyncUnavailable) return;
    if (!serviceWorkers) {
      markBackgroundUpdatesInactive();
      return;
    }
    if (!online()) return schedule(RETRY_INTERVAL_MS);
    if (!healthyWorker || !registration) {
      try {
        const healthy = await ensureHealthyDashboardServiceWorker(serviceWorkers, scriptUrl);
        registration = healthy.registration;
        healthyWorker = healthy.worker;
      } catch (error) {
        console.error(`Unable to configure automatic dashboard data updates: ${error instanceof Error ? error.message : String(error)}`);
        markBackgroundUpdatesInactive();
        await disableDashboardBackgroundUpdates(serviceWorkers, scriptUrl, registration);
        registration = undefined;
        healthyWorker = undefined;
        backgroundSyncUnavailable = isPermissionDeniedError(error);
        if (automaticDashboardDataUpdatesEnabled(storage)
            && !backgroundSyncUnavailable) {
          schedule(RETRY_INTERVAL_MS);
        }
        return;
      }
    }
    const worker = healthyWorker;
    const currentRegistration = registration;
    if (!worker || !currentRegistration) return schedule(RETRY_INTERVAL_MS);
    if (!backgroundConfigured) {
      try {
        const backgroundLastSuccess = await configureBackgroundDashboardDataUpdates(
          currentRegistration,
          worker,
          dataUrls,
          permissions
        );
        const foregroundLastSuccess = Number(storage.getItem(LAST_SUCCESS_STORAGE_KEY) ?? 0);
        if (backgroundLastSuccess > foregroundLastSuccess) {
          storage.setItem(LAST_SUCCESS_STORAGE_KEY, String(backgroundLastSuccess));
        }
        if (!automaticDashboardDataUpdatesEnabled(storage)) {
          await disableDashboardBackgroundUpdates(serviceWorkers, scriptUrl, currentRegistration);
          registration = undefined;
          healthyWorker = undefined;
          return;
        }
        backgroundConfigured = true;
        storage.setItem(BACKGROUND_ACTIVE_STORAGE_KEY, 'true');
        window.dispatchEvent(new Event(BACKGROUND_STATUS_EVENT));
      } catch (error) {
        console.error(`Unable to configure background dashboard data updates: ${error instanceof Error ? error.message : String(error)}`);
        markBackgroundUpdatesInactive();
        await disableDashboardBackgroundUpdates(serviceWorkers, scriptUrl, registration);
        registration = undefined;
        healthyWorker = undefined;
        backgroundConfigured = false;
        backgroundSyncUnavailable = error instanceof NonRetryableBackgroundSyncError;
        if (automaticDashboardDataUpdatesEnabled(storage)
            && !backgroundSyncUnavailable) {
          schedule(RETRY_INTERVAL_MS);
        }
        return;
      }
    }
    if (!battery && getBattery) {
      battery = await getBattery().catch(() => undefined);
      battery?.addEventListener?.('chargingchange', onConstraintChange);
      battery?.addEventListener?.('levelchange', onConstraintChange);
    }
    if (dashboardDataUpdateBlockedReason(connection, battery)) {
      schedule(RETRY_INTERVAL_MS);
      return;
    }
    const lastSuccess = Number(storage.getItem(LAST_SUCCESS_STORAGE_KEY) ?? 0);
    const remaining = UPDATE_INTERVAL_MS - (now() - lastSuccess);
    if (!onlineRefreshPending && lastSuccess > 0 && remaining > 0) {
      schedule(remaining, true);
      return;
    }
    try {
      debugServiceWorker('requesting data download', { urls: dataUrls });
      const response = await requestWorker(
        worker,
        { type: 'DOWNLOAD_DATA', urls: dataUrls, debug: debugParameter() },
        DOWNLOAD_TIMEOUT_MS
      );
      if (!response || typeof response !== 'object'
          || /** @type {{ type?: unknown }} */ (response).type !== 'DOWNLOAD_COMPLETE') {
        throw new Error('Service worker data download failed.');
      }
      debugServiceWorker('data download complete', response);
      if (!automaticDashboardDataUpdatesEnabled(storage)) {
        await disableDashboardBackgroundUpdates(serviceWorkers, scriptUrl, registration);
        return;
      }
      onlineRefreshPending = false;
      storage.setItem(LAST_SUCCESS_STORAGE_KEY, String(now()));
      schedule(backgroundConfigured ? UPDATE_INTERVAL_MS : RETRY_INTERVAL_MS, backgroundConfigured);
    } catch (error) {
      debugServiceWorker('data download failed', { message: error instanceof Error ? error.message : String(error) });
      console.error(`Unable to update dashboard data automatically: ${error instanceof Error ? error.message : String(error)}`);
      schedule(RETRY_INTERVAL_MS);
    }
  };
  const reconcile = async () => {
    if (running) {
      rerun = true;
      return;
    }
    running = true;
    try {
      await performReconcile();
    } finally {
      running = false;
      if (rerun && !stopped) {
        rerun = false;
        void reconcile();
      }
    }
  };
  const onSettingChange = () => {
    backgroundSyncUnavailable = false;
    void reconcile();
  };
  /** @param {StorageEvent} event */
  const onStorageChange = (event) => {
    if (event.key === ENABLED_STORAGE_KEY || event.key === null) void reconcile();
  };
  const onConstraintChange = () => void reconcile();
  window.addEventListener(SETTING_EVENT, onSettingChange);
  window.addEventListener('storage', onStorageChange);
  window.addEventListener('online', onConstraintChange);
  connection?.addEventListener?.('change', onConstraintChange);
  void reconcile();

  return () => {
    stopped = true;
    if (timer !== undefined) clearTimer(timer);
    window.removeEventListener(SETTING_EVENT, onSettingChange);
    window.removeEventListener('storage', onStorageChange);
    window.removeEventListener('online', onConstraintChange);
    connection?.removeEventListener?.('change', onConstraintChange);
    battery?.removeEventListener?.('chargingchange', onConstraintChange);
    battery?.removeEventListener?.('levelchange', onConstraintChange);
  };
}
