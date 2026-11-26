import { h } from '../dom.js';
import { usesGitHubAuthentication } from '../auth.js';
import { usesRemoteDataBackend, requestInventoryRefresh } from '../remote-data-backend.js';
import { effect, state } from '../reactive.js';
import { publishNotification } from '../notification-service.js';
import { createFactoryScope } from './factory-elements.js';

function available() {
  return usesRemoteDataBackend()
    && Reflect.get(globalThis, '__INVENTORY_REFRESH__') === true
    && (!usesGitHubAuthentication() || Reflect.get(globalThis, '__DASHBOARD_ADMIN__') === true);
}

/** Interaction-only control: inventory is acquired by the server, not this element.
 * @param {import('./ui-elements.js').ElementRenderContext} _context
 */
export function renderInventorySync(_context) {
  const scope = createFactoryScope();
  const status = state('idle');
  const message = state(available()
    ? 'Sync repository and workflow inventory and local campaign information in the configured scope. Unsupported version evidence stays unavailable. No workflows are updated.'
    : 'Sync unavailable. Requires an authorized server with inventory collection configured; synthetic and static dashboards cannot sync.');
  const feedback = h('p', { 'aria-live': 'polite', role: 'status' });
  const button = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'configuration-transactions-button', onClick: sync
  }, 'Sync'));
  const root = h('section', { className: 'configuration-setting-row', 'aria-label': 'Inventory sync' },
    h('div', { className: 'configuration-setting-copy' }, feedback), button);

  async function sync() {
    if (scope.signal.aborted || status.get() === 'busy' || !available()) return;
    status.set('busy');
    message.set('Requesting inventory sync…');
    try {
      await requestInventoryRefresh(scope.signal);
      if (scope.signal.aborted) return;
      status.set('queued');
      message.set('Inventory sync queued. Evidence will update when server collection completes.');
      publishNotification({ message: 'Inventory sync queued', description: 'Evidence will update when server collection completes.', tone: 'info' });
    } catch {
      if (scope.signal.aborted) return;
      status.set('error');
      message.set('Unable to request inventory sync. Check server configuration and access, then retry.');
      publishNotification({ message: 'Unable to request inventory sync', tone: 'error' });
    }
  }

  effect(() => {
    const current = status.get();
    button.disabled = !available() || current === 'busy';
    button.textContent = current === 'busy' ? 'Syncing…' : 'Sync';
    button.setAttribute('aria-label', 'Sync');
    button.setAttribute('aria-busy', String(current === 'busy'));
    feedback.textContent = message.get();
    feedback.setAttribute('role', current === 'error' ? 'alert' : 'status');
  }, { signal: scope.signal });
  scope.bind(root);
  return root;
}
