import { h } from '../dom.js';
import { octicon } from '../octicons.js';
import { formatMediumUtcDateTime, formatUtcDateTime, renderActionLabel, renderTooltip } from './ui-primitives.js';
import { publishNotification } from '../notification-service.js';
import { createDebug } from '../debug.js';
import { derived, effect, render, state, untracked } from '../reactive.js';
import { dashboardDataStatus } from '../dashboard-data-status.js';
import { isDashboardRateLimited } from '../rate-limit-notification.js';
import { createFactoryScope } from './factory-elements.js';
import { requestLocalIngestion, usesRemoteDataBackend } from '../remote-data-backend.js';
import { timestampMilliseconds } from '../data/model/schema.js';
import { formatCompactNumber, formatHumanFriendlyTimestamp, formatNumber, stringOrFallback } from '../view-formatters.js';

const debugRefreshError = createDebug('refresh-error');

/**
 * @param {() => void} retry
 * @param {{ snapshot?: { createdAt: string } | null }} [options]
 */
export function notifyRefreshError(retry, { snapshot = null } = {}) {
  debugRefreshError({ event: 'shown' });
  return publishNotification({
    id: 'dashboard:refresh-error',
    message: 'Dashboard data could not be refreshed.',
    description: snapshot
      ? `Some views may be unavailable. Showing the most recent cached data: the last complete snapshot from ${formatSnapshotDate(snapshot)}.`
      : 'A complete dashboard snapshot is not available yet.',
    tone: 'error',
    duration: 0,
    action: { label: 'Retry', run: () => {
      debugRefreshError({ event: 'retry-requested' });
      retry();
    } }
  });
}

/**
 * @param {{ createdAt: string } | null} snapshot
 * @param {{ refreshing?: boolean, failed?: boolean, configuration?: import('../dashboard-data-status.js').DashboardStatusConfiguration, sources?: Record<string, import('../presenter.js').LogicalSourceInput>, loadPageSources?: import('../presenter.js').PageSourceLoader, mobileHeader?: ParentNode }} [options]
 */
export function renderDashboardCurrentStatus(snapshot, { refreshing = false, failed = false, configuration, sources = {}, loadPageSources, mobileHeader } = {}) {
  const scope = createFactoryScope();
  const phase = failed ? 'stale' : refreshing ? 'cached' : snapshot ? 'ready' : 'loading';
  const freshness = state(/** @type {import('../presenter.js').LogicalSourceInput | undefined} */ (sources[configuration?.['freshness-source'] ?? '']));
  const health = state(/** @type {import('../presenter.js').LogicalSourceInput | undefined} */ (sources[configuration?.['health-source'] ?? '']));
  const ingestion = state(/** @type {import('../presenter.js').LogicalSourceInput | undefined} */ (sources[configuration?.['ingestion-source'] ?? '']));
  const clock = state(Date.now());
  const detailsExpanded = state(false);
  const startRequest = state(/** @type {'none' | 'requesting' | 'requested'} */ ('none'));
  const remote = usesRemoteDataBackend(document);
  const ingestionRow = derived(() => {
    const source = ingestion.get();
    return remote && source?.metadata.availability !== 'unavailable' ? source?.rows[0] : undefined;
  }, { signal: scope.signal });
  if (configuration) {
    const timer = setInterval(() => clock.set(Date.now()), 60_000);
    scope.signal.addEventListener('abort', () => clearInterval(timer), { once: true });
    const bindings = [
      { name: configuration['freshness-source'], result: freshness },
      ...(remote && configuration['health-source'] ? [{ name: configuration['health-source'], result: health }] : []),
      ...(remote && configuration['ingestion-source'] ? [{ name: configuration['ingestion-source'], result: ingestion }] : [])
    ];
    for (const { name, result } of bindings) {
      if (!loadPageSources?.subscribeBackgroundSources) continue;
      /** @param {Record<string, import('../presenter.js').LogicalSourceInput>} next */
      const update = (next) => {
        if (!scope.signal.aborted) result.set(next[name]);
      };
      void loadPageSources.subscribeBackgroundSources([name], { signal: scope.signal, onUpdate: update })
        .then(update)
        .catch((error) => {
          if (scope.signal.aborted) return;
          debugRefreshError({ event: 'status-subscription-failed', source: name, errorName: error instanceof Error ? error.name : 'unknown' });
          result.set(undefined);
        });
    }
  }
  const trigger = h('button', { type: 'button', className: 'tooltip-trigger' });
  const description = h('span', { className: 'tooltip-description' });
  const status = renderTooltip({
    id: 'dashboard-current-status-tooltip',
    label: 'Dashboard data status',
    trigger,
    content: description,
    className: 'dashboard-current-status',
    viewportAnchored: true,
    mobilePopup: true,
    signal: scope.signal
  });
  const presentation = derived(() => {
    const limited = isDashboardRateLimited();
    if (failed || limited) return {
      ...dataStatusPresentation(phase, snapshot, limited),
      icon: 'stop', tone: 'danger'
    };
    if (!configuration) return dataStatusPresentation(phase, snapshot, limited);
    const latest = freshness.get();
    const collection = health.get();
    const row = collection?.metadata?.availability !== 'unavailable' ? collection?.rows?.[0] : undefined;
    if (row?.configured === true && row.health === 'degraded') return {
      icon: 'stop', tone: 'danger', label: 'Data ingestion is broken',
      detail: 'Collection reports degraded ingestion, with an unresolved failure or dead-lettered work. Check Ingestion for diagnostics.'
    };
    const progress = ingestionRow.get();
    if (progress && typeof progress['status-label'] === 'string'
        && typeof progress['status-name'] === 'string'
        && typeof progress['status-icon'] === 'string' && typeof progress['status-tone'] === 'string') return {
      icon: progress['status-icon'], tone: progress['status-tone'], label: progress['status-label'],
      value: progress['status-name'],
      detail: typeof progress.message === 'string' ? progress.message : 'Ingestion details are unavailable.'
    };
    const value = latest?.metadata?.availability !== 'unavailable' ? latest?.rows?.[0]?.['latest-data-at'] : undefined;
    const timestamp = typeof value === 'number' && Number.isFinite(value) ? value : timestampMilliseconds(value);
    if (timestamp !== null && clock.get() - timestamp >= configuration['stale-after-hours'] * 3_600_000) return {
      icon: 'alert', tone: 'attention', label: 'Dashboard data is stale',
      detail: `The latest workflow evidence is from ${formatMediumUtcDateTime(timestamp)}, at least ${configuration['stale-after-hours']} hours old. Inactivity alone does not prove ingestion is broken.`
    };
    if (row?.configured === true && row.health === 'recovering') return {
      icon: 'alert', tone: 'attention', label: 'Data ingestion is recovering',
      detail: 'Collection reports queued or in-flight ingestion work. Check Ingestion for progress.'
    };
    if (refreshing) return dataStatusPresentation(phase, snapshot, limited);
    if (timestamp === null || timestamp > clock.get() || (remote && configuration['health-source'] && (row?.configured !== true || row.health !== 'healthy'))) return {
      icon: 'question', tone: 'muted', label: 'Data ingestion status is unknown',
      detail: timestamp === null || timestamp > clock.get()
        ? 'Workflow evidence freshness is unavailable. A successful dashboard refresh does not verify upstream ingestion.'
        : 'Workflow evidence is fresh, but collection health is unavailable or not configured. Upstream ingestion health could not be verified.'
    };
    return {
      icon: 'check-circle-fill', tone: 'success', label: 'Dashboard data is current',
      detail: `Workflow evidence is current as of ${formatMediumUtcDateTime(timestamp)}.${remote ? ' Collection reports healthy ingestion.' : ' Upstream collection health is not available in this static dashboard.'}`
    };
  }, { signal: scope.signal });
  const running = derived(() => {
    const progress = ingestionRow.get();
    return progress?.activity === 'Running' || progress?.status === 'collecting';
  }, { signal: scope.signal });
  const startIngestion = () => {
    if (startRequest.get() === 'requesting' || running.get()) return;
    startRequest.set('requesting');
    debugRefreshError({ event: 'ingestion-start-requested' });
    requestLocalIngestion(scope.signal).then(() => {
      if (!scope.signal.aborted) startRequest.set('requested');
    }, (error) => {
      if (scope.signal.aborted) return;
      startRequest.set('none');
      debugRefreshError({ event: 'ingestion-start-failed', errorName: error instanceof Error ? error.name : 'unknown' });
      publishNotification({ id: 'dashboard:ingestion-start', tone: 'error', message: 'Ingestion could not be started.' });
    });
  };
  effect(() => {
    if (running.get() && untracked(() => startRequest.get()) === 'requested') startRequest.set('none');
  }, { signal: scope.signal });
  render(trigger, () => [octicon(presentation.get().icon), renderActionLabel('Data ingestion')], { signal: scope.signal });
  render(description, () => {
    const current = presentation.get();
    const progress = ingestionRow.get();
    if (!progress) return current.detail;
    const scanned = progress['scanned-hours'];
    const target = progress['target-hours'];
    const determinate = typeof scanned === 'number' && Number.isFinite(scanned) && scanned >= 0
      && typeof target === 'number' && Number.isFinite(target) && target > 0;
    const next = formatHumanFriendlyTimestamp(progress['next-attempt-at'], clock.get());
    const active = running.get();
    const request = startRequest.get();
    return h('span', { className: 'ingestion-status-card' },
      h('span', { className: 'ingestion-status-heading' }, octicon(current.icon),
        h('strong', null, 'value' in current ? current.value : current.label),
        h('span', {
          className: 'ingestion-status-state',
          'data-state': active ? 'running' : 'idle'
        }, octicon(active ? 'sync' : 'dot-fill'), active ? 'Running' : 'Idle')),
      h('span', { className: 'ingestion-status-repository' }, stringOrFallback(progress.repository, 'Unknown repository')),
      h('span', { className: 'ingestion-status-history' },
        h('span', null, 'History'),
        h('strong', null, `${ingestionNumber(scanned)} / ${ingestionNumber(target)} h`)),
      determinate
        ? h('progress', { max: target, value: scanned, 'aria-label': 'History scanned', 'aria-valuetext': `${ingestionNumber(scanned)} of ${ingestionNumber(target)} repository-hours` })
        : h('span', { className: 'ingestion-status-unavailable' }, 'History unavailable'),
      h('span', { className: 'ingestion-status-budget', 'aria-label': `API points remaining: ${ingestionNumber(progress['api-remaining'])}` },
        octicon('shield'), h('strong', null, ingestionNumber(progress['api-remaining'], true)),
        h('span', null, 'API left'),
        h('small', null, `${ingestionNumber(progress['api-reserve'], true)} reserved`)),
      h('span', { className: 'ingestion-status-footer' },
        next ? h('span', { title: formatUtcDateTime(progress['next-attempt-at']), 'aria-label': `Next attempt ${next}` }, octicon('clock'), next) : null,
        h('small', { title: 'History discovery and artifacts remain partial.' }, 'Partial')),
      h('button', {
        type: 'button',
        className: 'btn ingestion-status-start',
        disabled: active || request === 'requesting',
        onClick: startIngestion
      }, octicon(active ? 'sync' : 'play'),
      active ? 'Ingestion running' : request === 'requesting' ? 'Starting…' : request === 'requested' ? 'Ingestion requested' : 'Start ingestion now'),
      h('details', {
        open: detailsExpanded.get(),
        onToggle: (/** @type {Event} */ event) => {
          if (event.currentTarget instanceof HTMLDetailsElement) detailsExpanded.set(event.currentTarget.open);
        }
      }, h('summary', null, 'Details'),
      h('span', { className: 'ingestion-status-diagnostics' },
        h('span', null, current.detail),
        h('span', null, `Quota reset: ${formatUtcDateTime(progress['api-reset-at'])}`),
        h('span', null, `Observed: ${formatUtcDateTime(progress['observed-at'])}`)))
    );
  }, { signal: scope.signal });
  effect(() => {
    const { tone, label, icon } = presentation.get();
    trigger.setAttribute('aria-label', label);
    status.dataset.tone = tone;
    status.classList.toggle('dashboard-current-status-refreshing', icon === 'sync');
    status.hidden = !configuration && !snapshot && !isDashboardRateLimited() && !failed;
  }, { signal: scope.signal });
  const menu = mobileHeader?.querySelector('.mobile-nav-menu');
  if (menu) {
    const indicator = h('button', {
      type: 'button',
      className: 'mobile-ingestion-indicator',
      onClick: (/** @type {Event} */ event) => {
        // The menu's outside-click dismissal would otherwise close the menu that hosts the popup.
        event.stopPropagation();
        if (menu instanceof HTMLDetailsElement && !menu.open) {
          menu.open = true;
          // The popover target lives inside the menu, so it can only open once the menu has rendered.
          requestAnimationFrame(() => {
            if (!scope.signal.aborted) trigger.click();
          });
        } else trigger.click();
      }
    });
    render(indicator, () => octicon(presentation.get().icon), { signal: scope.signal });
    effect(() => {
      const { tone, label, icon } = presentation.get();
      const active = running.get();
      indicator.dataset.tone = tone;
      indicator.dataset.state = active ? 'running' : 'idle';
      indicator.classList.toggle('mobile-ingestion-indicator-refreshing', icon === 'sync' || active);
      indicator.setAttribute('aria-label', ingestionRow.get() ? `${label} (${active ? 'running' : 'idle'})` : label);
      indicator.title = indicator.getAttribute('aria-label') ?? label;
      indicator.hidden = status.hidden;
    }, { signal: scope.signal });
    menu.before(indicator);
    scope.signal.addEventListener('abort', () => indicator.remove(), { once: true });
  }
  scope.bind(status);
  return status;
}

/** @param {unknown} value @param {boolean} [compact] */
function ingestionNumber(value, compact = false) {
  return typeof value === 'number' && Number.isFinite(value)
    ? compact ? formatCompactNumber(value) : formatNumber(value)
    : 'Unknown';
}

/** @param {AbortSignal} signal */
export function renderDashboardIngestionStatus(signal) {
  const element = h('div', {
    className: 'dashboard-ingestion-status',
    role: 'status',
    'aria-live': 'polite',
    'aria-atomic': 'true'
  });
  render(element, () => {
    const { phase, snapshot } = dashboardDataStatus.get();
    const status = dataStatusPresentation(phase, snapshot, isDashboardRateLimited());
    element.dataset.tone = status.tone;
    return [
      h('div', { className: 'dashboard-ingestion-status-heading' },
        octicon(status.icon), h('span', null, 'Data ingestion'), h('strong', null, status.value)),
      h('p', null, status.detail)
    ];
  }, { signal });
  return element;
}

/**
 * @param {ReturnType<typeof dashboardDataStatus.get>['phase']} phase
 * @param {{ createdAt: string } | null} snapshot
 * @param {boolean} limited
 */
function dataStatusPresentation(phase, snapshot, limited) {
  if (limited) return {
    icon: 'alert', tone: 'attention', value: 'Rate limited', label: 'Dashboard is rate limited',
    detail: 'Dashboard requests are rate limited. Please try again shortly.'
  };
  if (phase === 'stale') return {
    icon: 'alert', tone: 'danger', value: 'Refresh failed', label: 'Dashboard refresh failed',
    detail: snapshot
      ? `Showing the last complete snapshot from ${formatSnapshotDate(snapshot)}.`
      : 'A complete dashboard snapshot is not available yet.'
  };
  if (phase === 'ready') return {
    icon: 'check-circle-fill', tone: 'success', value: 'Current', label: 'Dashboard data is current',
    detail: snapshot
      ? `Dashboard data is current as of ${formatSnapshotDate(snapshot)}.`
      : 'Dashboard data loaded successfully.'
  };
  if (phase === 'cached') return {
    icon: 'sync', tone: 'muted', value: 'Refreshing', label: 'Refreshing dashboard data',
    detail: snapshot
      ? `Showing the last complete snapshot from ${formatSnapshotDate(snapshot)} while dashboard data refreshes.`
      : 'Refreshing dashboard data.'
  };
  return {
    icon: phase === 'loading' ? 'sync' : 'question', tone: 'muted',
    value: phase === 'loading' ? 'Importing' : 'Unknown', label: 'Dashboard data is not ready',
    detail: phase === 'loading'
      ? 'Waiting for dashboard data to finish loading.'
      : 'Dashboard refresh status is not available yet.'
  };
}

/**
 * @param {{ createdAt: string }} snapshot
 */
function formatSnapshotDate(snapshot) {
  const timestamp = Date.parse(snapshot.createdAt);
  return Number.isFinite(timestamp)
    ? formatMediumUtcDateTime(timestamp)
    : 'an unknown time';
}
