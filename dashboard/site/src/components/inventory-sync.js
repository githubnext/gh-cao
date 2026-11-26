import { button, h } from '../dom.js';
import { usesRemoteDataBackend } from '../backend-mode.js';
import { requestInventoryRefresh } from '../remote-data-backend.js';
import { publishNotification } from '../notification-service.js';
import { effect, state } from '../reactive.js';
import { createFactoryScope } from './factory-elements.js';

/** Task interaction, not data selection or query execution.
 * @param {import('./ui-elements.js').ElementRenderContext} context
 */
export function renderInventorySync(context) {
  const scope = createFactoryScope();
  const pending = state(false);
  const available = usesRemoteDataBackend();
  const feedback = h('p', { className: 'muted', role: 'status', 'aria-live': 'polite' },
    available
      ? 'Sync requests repository, workflow and campaign information from the server. Scheduled refreshes also run automatically.'
      : 'Sync is unavailable in the synthetic demo. Use a local or hosted server to refresh information.');
  const sync = button({
    className: 'btn',
    type: 'button',
    disabled: !available,
    onclick: async () => {
      if (pending.get() || scope.signal.aborted || !available) return;
      pending.set(true);
      feedback.textContent = 'Requesting refresh…';
      try {
        const result = await requestInventoryRefresh(scope.signal);
        if (scope.signal.aborted) return;
        const message = result.mode === 'inventory'
          ? 'Sync queued. Information will update as refresh tasks complete.'
          : 'Sync requested. Information will update as the local collection pass completes.';
        feedback.textContent = message;
        publishNotification({ message, tone: 'info' });
      } catch {
        if (scope.signal.aborted) return;
        const message = 'Sync could not be confirmed. Check server access and collection availability, then retry.';
        feedback.textContent = message;
        publishNotification({ message, tone: 'error' });
      } finally {
        if (!scope.signal.aborted) pending.set(false);
      }
    }
  }, 'Sync');
  const root = h('div', { 'aria-label': context.title }, sync, feedback);
  effect(() => {
    sync.disabled = !available || pending.get();
    sync.textContent = pending.get() ? 'Syncing…' : 'Sync';
    sync.setAttribute('aria-busy', String(pending.get()));
  }, { signal: scope.signal });
  scope.bind(root);
  return root;
}
