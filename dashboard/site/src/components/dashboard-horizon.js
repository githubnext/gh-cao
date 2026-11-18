import { h } from '../dom.js';
import { effect, state } from '../reactive.js';
import { renderLabeledSpan } from './ui-primitives.js';
import { createDebug } from '../debug.js';
import { renderFilterBar } from './filter-bar.js';

const debugDashboardHorizon = createDebug('dashboard-horizon');

/**
 * @typedef {{
 *   available: boolean,
 *   evaluatedAt: string,
 *   duration: string,
 *   start: string,
 *   end: string
 * }} HorizonViewModel
 */

/**
 * @param {{
 *   dashboard: import('../presenter.js').PresentableDashboard,
 *   initialValue: HorizonViewModel,
 *   defaultRange?: string,
 *   referenceEnd?: string,
 *   onChange?: (filters: Map<string, string[]>, timeWindow?: import('./filter-bar.js').TimeWindow) => void,
 *   formatDate: (value: string) => string
 * }} options
 * @returns {{
 *   element: HTMLElement,
 *   update: (next: HorizonViewModel) => void,
 *   collapse: () => void,
 *   dispose: () => void
 * }}
 */
export function renderDashboardHorizon(options) {
  const lifetime = new AbortController();
  const value = state(options.initialValue);
  const horizon = options.dashboard.horizon;
  const label = horizon?.label || 'Horizon';
  const skeleton = h('span', { 'aria-hidden': 'true' });
  const startTime = /** @type {HTMLTimeElement} */ (h('time'));
  const endTime = /** @type {HTMLTimeElement} */ (h('time'));
  const durationValue = h('span');
  const details = h(
    'details',
    { className: 'horizon-details', role: 'group', 'aria-label': 'Horizon details' },
    h('summary', null, 'Coverage details'),
    h(
      'span',
      { className: 'horizon-details-description' },
      horizon?.tooltip.description ?? 'Evidence coverage and data quality for this dashboard.'
    ),
    h(
      'span',
      { className: 'horizon-details-values' },
      renderLabeledSpan('Start', startTime),
      renderLabeledSpan('End', endTime),
      renderLabeledSpan('Duration', durationValue)
    )
  );
  const root = renderFilterBar((filters, timeWindow) => {
    if (!lifetime.signal.aborted) options.onChange?.(filters, timeWindow);
  }, {
    defaultRange: options.defaultRange,
    referenceEnd: options.referenceEnd,
    signal: lifetime.signal,
    showFilters: false,
    maxDays: 30
  });
  root.classList.add('dashboard-horizon');
  root.prepend(skeleton);
  root.querySelector('.filter-tuning-controls')?.append(details);
  let wasAvailable = false;
  root.addEventListener('dashboard-horizon-expanded', (event) => {
    debugDashboardHorizon({ event: 'toggled', expanded: event instanceof CustomEvent && event.detail === true });
  }, { signal: lifetime.signal });

  effect(() => {
    const current = value.get();
    root.classList.toggle('dashboard-horizon-skeleton', !current.available);
    skeleton.hidden = current.available;
    if (!current.available) {
      root.setAttribute('aria-label', 'Horizon unavailable');
      root.removeAttribute('data-dashboard-evaluated-at');
      return;
    }

    root.setAttribute('aria-label', `${label} ${current.duration}`);
    root.dataset.dashboardEvaluatedAt = current.evaluatedAt;
    if (!wasAvailable) {
      wasAvailable = true;
      debugDashboardHorizon({ event: 'resolved', duration: current.duration });
    }
    durationValue.textContent = current.duration;
    startTime.dateTime = current.start;
    startTime.textContent = `${options.formatDate(current.start)} UTC`;
    endTime.dateTime = current.end;
    endTime.textContent = `${options.formatDate(current.end)} UTC`;
  }, { signal: lifetime.signal });

  return {
    element: root,
    /**
     * Updates the bound horizon. Once resolved, an empty refresh retains the
     * global horizon instead of flashing the loading skeleton.
     * @param {HorizonViewModel} next
     */
    update(next) {
      value.set((current) => {
        if (current.available && !next.available) return current;
        return current.available === next.available
          && current.evaluatedAt === next.evaluatedAt
          && current.duration === next.duration
          && current.start === next.start
          && current.end === next.end
          ? current
          : next;
      });
    },
    collapse() {
      root.dispatchEvent(new Event('dashboard-horizon-collapse'));
    },
    dispose() {
      lifetime.abort();
      debugDashboardHorizon({ event: 'disposed' });
    }
  };
}

/**
 * Lets the data-status trigger open the horizon's time and mode panel; the
 * header has no separate horizon control. Mobile popups keep their own status
 * dialog because the mobile menu shows the time controls inline.
 * @param {HTMLElement} status
 * @param {HTMLElement} horizon
 */
export function bindStatusToHorizon(status, horizon) {
  const trigger = status.querySelector(':scope > .tooltip-trigger');
  if (!(trigger instanceof HTMLElement)) return;
  trigger.dataset.horizonTrigger = '';
  trigger.setAttribute('aria-expanded', 'false');
  trigger.addEventListener('click', () => {
    if (status.classList.contains('tooltip-mobile-popup')) return;
    horizon.dispatchEvent(new Event('dashboard-horizon-toggle'));
  });
  horizon.addEventListener('dashboard-horizon-expanded', (event) => {
    const expanded = event instanceof CustomEvent && event.detail === true;
    trigger.setAttribute('aria-expanded', String(expanded));
    if (!expanded && horizon.contains(document.activeElement)) trigger.focus();
  });
}
