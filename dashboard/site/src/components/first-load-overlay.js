import { h } from '../dom.js';
import { agenticWorkflowMark, octicon } from '../octicons.js';
import { derived, effect, onCleanup, render, state } from '../reactive.js';
import { browserFirstLoad, preparationDiagnostics, preparationStorage } from '../browser-first-load.js';
import { createDebug } from '../debug.js';
import { createCopyControl, createModalDialog } from './ui-primitives.js';
import { restoreDashboardTheme } from './theme-settings.js';
import { createFirstLoadMessagePicker, FIRST_LOAD_MESSAGE_INTERVAL_MS } from './first-load-messages.js';
import { renderAgenticLoader } from './agentic-loader.js';
import { updateNotificationActions, updateNotificationDetails } from '../notification-service.js';

const debugFirstLoadOverlay = createDebug('first-load-overlay');

/** @param {string} wide @param {string} compact */
function responsiveCopy(wide, compact) {
  return [
    h('span', { className: 'first-load-wide-copy' }, wide),
    h('span', { className: 'first-load-compact-copy' }, compact)
  ];
}

/**
 * Browser-local import presentation cannot be expressed as a canonical data query.
 * @param {{ document: Document, signal: AbortSignal, retry: () => void }} options
 */
export function mountFirstLoadOverlay({ document, signal: ownerSignal, retry }) {
  if (ownerSignal.aborted || browserFirstLoad.get().status === 'inactive') return;
  debugFirstLoadOverlay({ event: 'mounted', status: browserFirstLoad.get().status });
  const lifetime = new AbortController();
  const signal = lifetime.signal;
  ownerSignal.addEventListener('abort', () => lifetime.abort(), { once: true, signal });
  const { dialog, open, close } = createModalDialog({
    className: 'first-load-overlay',
    ariaLabel: 'Preparing your dashboard'
  });
  signal.addEventListener('abort', () => {
    close();
    dialog.remove();
  }, { once: true });
  const dismiss = () => {
    debugFirstLoadOverlay({ event: 'dismissed' });
    browserFirstLoad.set((current) => ({ ...current, dismissed: true }));
  };
  const dismissButton = h('button', {
    type: 'button',
    className: 'first-load-browse',
    onClick: dismiss
  }, 'Explore data', octicon('arrow-right'));
  const retryButton = h('button', {
    type: 'button',
    className: 'first-load-browse',
    onClick: () => {
      debugFirstLoadOverlay({ event: 'retry-requested' });
      retry();
    }
  }, 'Retry import');
  const title = h('h2');
  const eyebrow = h('p', { className: 'first-load-eyebrow' });
  const description = h('p', { className: 'first-load-description' });
  const durationNote = h('p', { className: 'first-load-note first-load-duration' });
  const reasonCopy = h('span');
  const copyControl = createCopyControl({
    getContent: () => {
      const { reason, status, stage, completed, total, oldVersion, newVersion } = browserFirstLoad.get();
      return JSON.stringify({
        event: 'dashboard-browser-database-repopulation',
        reason: reason ?? 'missing-snapshot',
        status,
        stage: stage ?? null,
        completed: completed ?? null,
        total: total ?? null,
        schemaVersionBefore: oldVersion ?? null,
        schemaVersionAfter: newVersion ?? null,
        ...preparationDiagnostics()
      }, null, 2);
    },
    label: 'Copy preparation details',
    buttonClassName: 'first-load-details-link',
    statusClassName: 'first-load-copy-status',
    successText: 'Preparation details copied.',
    failureText: 'Could not copy preparation details.',
    trackState: true
  });
  const message = h('p', { className: 'first-load-message', 'aria-live': 'off' });
  const nextMessage = createFirstLoadMessagePicker();
  const currentMessage = state(nextMessage());
  const rotating = derived(() => {
    const current = browserFirstLoad.get();
    return current.status === 'loading' && !current.dismissed && !current.ingestion;
  }, { signal });
  const status = h('p', { className: 'first-load-status', role: 'status' });
  const progress = h('div', { className: 'first-load-progress' });
  const loader = renderAgenticLoader();
  const ingestionSubtitle = h('p', { className: 'dashboard-notification-details-subtitle' });
  const ingestionHistory = h('ul', { className: 'dashboard-notification-details', 'aria-label': 'Ingestion progress history' });
  const ingestionActions = h('div', { className: 'dashboard-notification-actions' });
  const compactIngestionSummary = h('span', { className: 'first-load-compact-copy' });
  const ingestionDetails = h('details', { className: 'first-load-ingestion-details' },
    h('summary', null, h('span', { className: 'first-load-wide-copy' }, 'Ingestion progress history'), compactIngestionSummary),
    ingestionSubtitle, ingestionHistory, ingestionActions);
  const continuationNote = h('p', { className: 'first-load-note' }, responsiveCopy(
    'No need to wait here. The import continues as you explore.',
    'Explore now. The import keeps going.'
  ));
  dialog.append(
    h('div', { className: 'first-load-background', 'aria-hidden': 'true' }),
    h('header', { className: 'first-load-header' },
      h('div', { className: 'first-load-brand' }, agenticWorkflowMark(), h('span', null, 'Central Agentic Ops'))
    ),
    h('section', { className: 'first-load-card' },
    eyebrow,
    title,
    description,
    loader,
    progress,
    status,
    message,
    ingestionDetails,
    durationNote,
    dismissButton,
    retryButton,
    continuationNote,
    h('details', { className: 'first-load-about' },
      h('summary', null, 'About this preparation'),
      h('p', { className: 'first-load-note' }, 'We download the latest published activity snapshot and build a local database in this browser. Views update as evidence becomes available.'),
      h('p', { className: 'first-load-note first-load-reason' }, reasonCopy, ' ', copyControl.button),
      copyControl.status,
      h('ol', { className: 'first-load-steps' },
        h('li', null, h('strong', null, 'Download'), h('span', null, 'Collect the latest published snapshot')),
        h('li', null, h('strong', null, 'Prepare'), h('span', null, 'Cache data for this visit and the next')),
        h('li', null, h('strong', null, 'Explore'), h('span', null, 'Follow the evidence as it arrives'))
      ),
      h('p', { className: 'first-load-note first-load-server-option' },
        'For larger datasets, deploy a CAO backend server to run queries server-side and avoid this browser import. See ',
        h('a', { href: 'https://githubnext.github.io/gh-cao/deployment/', target: '_blank', rel: 'noopener noreferrer', 'aria-label': 'deployment options (opens in a new tab)' }, 'deployment options'),
        '.'
      )
    )
  ));
  dialog.addEventListener('cancel', (event) => {
    event.preventDefault();
    dismiss();
  }, { signal });
  document.body.append(dialog);
  render(reasonCopy, () => browserFirstLoad.get().reason === 'upgrade'
    ? 'Why is the database being rebuilt? This dashboard version needs a newer browser database format. We rebuild this local copy from published activity so it stays compatible; your campaign data is not changed.'
    : 'Why is the database being populated? This browser has no completed local copy yet. This can happen on your first visit, after clearing browser data, after a private browsing session ends, or if an earlier import did not finish.', { signal });
  render(eyebrow, () => browserFirstLoad.get().reason === 'upgrade' ? 'Dashboard update' : 'Welcome to CAO', { signal });
  render(description, () => {
    const current = browserFirstLoad.get();
    if (current.status === 'failed') return 'Your browser copy is not ready yet. You can retry without changing any campaign data.';
    return current.reason === 'upgrade'
      ? responsiveCopy(
        'We are refreshing your browser copy for this version. Your campaign activity will be ready to explore again soon.',
        'Refreshing your browser copy for this version.'
      )
      : responsiveCopy(
        'We are bringing your campaign activity together, so you can see the big picture and follow the details.',
        'Bringing your campaign activity together for a first look.'
      );
  }, { signal });
  render(durationNote, () => preparationStorage.get() && preparationStorage.get()?.persisted !== true
    ? 'Preparation can take several minutes. This browser may need to download the data again on a future visit.'
    : browserFirstLoad.get().reason === 'upgrade'
    ? responsiveCopy(
      'This update can take several minutes. Future visits reuse cached data.',
      'Updates can take a few minutes. Future visits use cached data.'
    )
    : responsiveCopy(
      'The first import can take several minutes. Future visits reuse cached data.',
      'First visits take a few minutes. Next time is quicker.'
    ), { signal });
  render(title, () => {
    const current = browserFirstLoad.get();
    if (current.status === 'failed') return responsiveCopy(
      current.reason === 'upgrade' ? 'The dashboard update could not finish.' : 'The first import could not finish.',
      'Import incomplete.'
    );
    return responsiveCopy(
      current.reason === 'upgrade' ? 'Updating your dashboard.' : 'Your dashboard is taking shape.',
      'Preparing your dashboard.'
    );
  }, { signal });
  render(message, () => browserFirstLoad.get().ingestion?.message ?? currentMessage.get(), { signal });
  effect(() => {
    const current = browserFirstLoad.get();
    message.hidden = current.status === 'failed';
    message.classList.toggle('first-load-message-ingesting', Boolean(current.ingestion));
  }, { signal });
  effect(() => {
    if (!rotating.get()) return;
    const timer = setInterval(() => currentMessage.set(nextMessage()), FIRST_LOAD_MESSAGE_INTERVAL_MS);
    onCleanup(() => clearInterval(timer));
  }, { signal });
  effect(() => {
    const { status: phase, ingestion } = browserFirstLoad.get();
    ingestionDetails.hidden = phase !== 'loading' || !ingestion;
    if (!ingestion || phase !== 'loading') return;
    compactIngestionSummary.textContent = ingestion.message;
    ingestionSubtitle.textContent = ingestion.detailsSubtitle ?? '';
    ingestionSubtitle.hidden = !ingestion.detailsSubtitle;
    updateNotificationDetails(ingestionHistory, ingestion.details ?? []);
    updateNotificationActions(ingestionActions, ingestion.actions ?? []);
  }, { signal });
  render(status, () => {
    const current = browserFirstLoad.get();
    if (current.status === 'failed') return responsiveCopy(
      'Campaign status is not available yet. Retry the import to finish preparing your dashboard.',
      'Try again to finish preparing your dashboard.'
    );
    if (current.stage === 'maintenance') return 'Applying retention limits...';
    if (current.stage === 'inventory') return 'Preparing inventory...';
    if (current.stage === 'queries') return 'Refreshing dashboard queries...';
    if (current.reason === 'upgrade' && !current.stage) return 'Updating the local database...';
    return current.total
      ? 'Importing activity files...'
      : 'Preparing the latest activity snapshot...';
  }, { signal });
  render(progress, () => {
    const current = browserFirstLoad.get();
    if (current.status === 'failed') return [];
    return h('progress', {
      'aria-label': 'Dashboard import progress',
      ...(current.total ? { max: current.total, value: current.completed ?? 0 } : {})
    });
  }, { signal });
  effect(() => {
    const current = browserFirstLoad.get();
    if (current.status === 'inactive') {
      lifetime.abort();
      return;
    }
    const visible = !current.dismissed;
    dismissButton.hidden = current.status === 'failed';
    retryButton.hidden = current.status !== 'failed';
    continuationNote.hidden = current.status === 'failed';
    durationNote.hidden = current.status === 'failed';
    loader.hidden = current.status !== 'loading';
    if (visible && !dialog.open) {
      restoreDashboardTheme(dialog);
      open();
    }
    else if (!visible && dialog.open) close();
  }, { signal });
}
