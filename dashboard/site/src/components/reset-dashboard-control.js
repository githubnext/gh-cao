import { h } from '../dom.js';
import { deleteCanonicalDatabase } from '../data/storage/canonical-database.js';
import { octicon } from '../octicons.js';
import { clearScopedStorage } from '../storage-scope.js';
import { createDebug } from '../debug.js';
import { effect, state } from '../reactive.js';
import { createFactoryScope } from './factory-elements.js';
import { createModalDialog, renderCloseButton, renderLiveRegion } from './ui-primitives.js';
import { actionPresentation, normalizeAction } from '../action-model.js';

const debug = createDebug('data:reset');

/**
 * @param {IDBFactory} indexedDB
 * @param {{ onBlocked?: () => void }} [options]
 */
async function deleteAppDatabases(indexedDB, options = {}) {
  await deleteCanonicalDatabase(indexedDB, options);
}

/**
 * @param {Storage} storage
 * @param {IDBFactory} indexedDB
 * @param {{ onBlocked?: () => void }} [options]
 */
export async function resetLocalDashboardData(storage, indexedDB, options = {}) {
  debug('resetting local dashboard data');
  try {
    await deleteAppDatabases(indexedDB, options);
    debug('reset local dashboard data succeeded');
  } catch (error) {
    debug('reset local dashboard data failed', error);
    throw error;
  } finally {
    clearScopedStorage(storage);
  }
}

function browserStorage() {
  try {
    return globalThis.window?.localStorage;
  } catch {
    return undefined;
  }
}

/**
 * Resets local dashboard data using the current browser's storage, as the
 * reset control does. Used for recovery when startup data preparation stalls.
 * @param {{ onBlocked?: () => void }} [options]
 */
export async function resetBrowserDashboardData(options = {}) {
  const storage = browserStorage();
  const indexedDB = globalThis.window?.indexedDB;
  if (!storage || !indexedDB) throw new Error('Local browser storage is unavailable.');
  await resetLocalDashboardData(storage, indexedDB, options);
}

/** Removes only caches owned by this dashboard deployment. */
export async function clearDashboardAppCaches(cacheStorage = globalThis.caches) {
  if (!cacheStorage) throw new Error('Browser cache storage is unavailable.');
  const keys = await cacheStorage.keys();
  await Promise.all(keys.filter((key) => key === 'central-agentic-ops-dashboard-config'
    || key.startsWith('central-agentic-ops-dashboard-app-')
    || key.startsWith('central-agentic-ops-dashboard-data-'))
    .map((key) => cacheStorage.delete(key)));
}

/** @param {{ onBlocked?: () => void }} [options] */
export async function clearBrowserDashboardApp(options = {}) {
  await resetBrowserDashboardData(options);
  await clearDashboardAppCaches();
}

/**
 * @param {{ storage?: Storage, indexedDB?: IDBFactory, cacheStorage?: CacheStorage, reload?: () => void, clearApp?: boolean }} [options]
 * @returns {HTMLElement}
 */
export function renderResetDashboardControl(options = {}) {
  const storage = options.storage ?? browserStorage();
  const indexedDB = options.indexedDB ?? globalThis.window?.indexedDB;
  const reload = options.reload ?? (() => globalThis.window?.location.reload());
  const clearApp = options.clearApp === true;
  const action = normalizeAction({
    level: 'ui',
    verb: clearApp ? 'clear' : 'reset',
    label: clearApp ? 'Clear app' : 'Reset local data',
    confirmation: true
  }, { id: clearApp ? 'clear-app' : 'reset-local-data', type: 'ui' });
  const presentation = actionPresentation(action);
  const scope = createFactoryScope();
  /** @type {HTMLButtonElement} */
  let trigger;

  // `statusMessage` and `busy` are the dialog's entire visible state; the
  // effect below is the only place that writes them onto the owned DOM.
  const statusMessage = state(/** @type {string | null} */ (null));
  const busy = state(false);

  const { dialog, open, close } = createModalDialog({
    className: 'reset-dashboard-dialog',
    ariaLabel: 'Reset dashboard confirmation',
    onFallbackClose: () => trigger.focus()
  });
  const status = /** @type {HTMLOutputElement} */ (renderLiveRegion('output', 'reset-dashboard-status'));
  const cancel = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button',
    className: 'reset-dashboard-cancel',
    onClick: close
  }, 'Cancel'));
  const confirm = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button',
    className: 'reset-dashboard-confirm',
    'data-action-level': action.level,
    onClick: async () => {
      if (!storage || !indexedDB) {
        statusMessage.set('Local browser storage is unavailable.');
        return;
      }
      busy.set(true);
      statusMessage.set('Resetting…');
      try {
        await resetLocalDashboardData(storage, indexedDB, {
          onBlocked: () => {
            statusMessage.set('Waiting for other open dashboard tabs to close…');
          }
        });
        if (clearApp) await clearDashboardAppCaches(options.cacheStorage);
        reload();
      } catch (error) {
        statusMessage.set(error instanceof Error && error.name === 'IndexedDBDeleteBlockedError'
          ? 'Reset timed out because another open dashboard tab is still using local data. Close other tabs and try again.'
          : clearApp ? 'Could not clear the app.' : 'Could not reset local dashboard data.');
        busy.set(false);
      }
    }
  }, 'Reset'));
  effect(() => {
    status.textContent = statusMessage.get() ?? '';
    const isBusy = busy.get();
    confirm.disabled = isBusy;
    cancel.disabled = isBusy;
  }, { signal: scope.signal });
  trigger = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button',
    className: 'reset-dashboard-trigger',
    'data-action-level': action.level,
    onClick: open
  }, octicon(presentation.icon), h('span', null, presentation.label)));
  dialog.append(
    h('header', { className: 'reset-dashboard-dialog-header' },
      h('h2', null, `${presentation.label}?`),
      renderCloseButton({
        className: 'reset-dashboard-dialog-close',
        label: 'Close reset confirmation',
        onClick: close
      })
    ),
    h('div', { className: 'reset-dashboard-dialog-body' },
      h('p', null, clearApp
        ? 'This deletes the cached website, offline data, indexed dashboard data, and local settings in this browser. Reopening the app requires a network connection.'
        : 'This permanently deletes the indexed dashboard data and all local settings stored in this browser.'),
      h('strong', null, 'This action cannot be undone.')
    ),
    h('footer', { className: 'reset-dashboard-dialog-footer' }, status, cancel, confirm)
  );
  dialog.addEventListener('close', () => trigger.focus());
  const root = h('div', { className: 'reset-dashboard-control' }, trigger, dialog);
  scope.bind(root);
  return root;
}
