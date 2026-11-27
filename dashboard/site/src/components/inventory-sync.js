import { createDebug, diagnosticErrorName } from '../debug.js';
import { h } from '../dom.js';
import { effect, state } from '../reactive.js';
import { publishNotification } from '../notification-service.js';
import { syncRemoteInventory, usesRemoteDataBackend } from '../remote-data-backend.js';
import { createFactoryScope } from './factory-elements.js';

const debugInventorySync = createDebug('inventory-sync');

/**
 * Interaction-only: Dashboard Language declares placement and no data sources.
 * HTTP admission, authentication and abortable pending state cannot be represented
 * by evidence queries or CLI safe-output actions.
 * @param {import('./ui-elements.js').ElementRenderContext} context
 */
export function renderInventorySync(context) {
  const scope = createFactoryScope();
  const available = usesRemoteDataBackend();
  const pending = state(false);
  const message = state(available
    ? 'Refresh repository and campaign inventory only; workflows are not changed.'
    : 'Sync unavailable: inventory refresh requires a configured dashboard server, not the static demo.');
  const statusId = `inventory-sync-${crypto.randomUUID()}`;
  const status = h('span', { id: statusId, role: 'status', 'aria-live': 'polite' });
  const button = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'btn', 'aria-describedby': statusId,
    onclick: async () => {
      if (!available || pending.get() || scope.signal.aborted) return;
      pending.set(true);
      debugInventorySync({ event: 'requested' });
      message.set('Requesting inventory sync…');
      try {
        await syncRemoteInventory(scope.signal);
        if (scope.signal.aborted) return;
        debugInventorySync({ event: 'queued' });
        const queued = 'Inventory sync queued. Updates will appear as collection progresses; workflows are not changed.';
        message.set(queued);
        publishNotification({ tone: 'info', message: queued });
      } catch (error) {
        if (scope.signal.aborted) return;
        debugInventorySync({ event: 'failed', errorName: diagnosticErrorName(error) });
        const failed = 'Inventory sync was not confirmed. Check your administrator access and server collection configuration, then retry.';
        message.set(failed);
        publishNotification({ tone: 'error', message: failed });
      } finally {
        if (!scope.signal.aborted) pending.set(false);
      }
    }
  }, 'Sync'));
  const root = h('div', {
    className: 'inventory-sync', 'aria-label': context.title
  }, button, status);
  effect(() => {
    // Native disabling during a request drops keyboard focus in Chromium.
    button.disabled = !available;
    button.setAttribute('aria-disabled', String(!available || pending.get()));
    button.setAttribute('aria-busy', String(pending.get()));
    status.textContent = message.get();
  }, { signal: scope.signal });
  scope.bind(root);
  return root;
}
