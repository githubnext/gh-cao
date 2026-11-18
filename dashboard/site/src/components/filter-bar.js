import { h } from '../dom.js';
import { createDebug } from '../debug.js';
import { debounce } from '../debounce.js';
import { dashboardHorizonHours, formatDashboardHorizon } from '../horizon.js';
import { effect, state } from '../reactive.js';
import { scopedStorageKey } from '../storage-scope.js';
import { renderCountBadge, renderLabeledControl, renderIconSpan } from './ui-primitives.js';

const debug = createDebug('filter-bar');

/** @typedef {{ range: string, start: string, end: string }} TimeWindow */
const FILTER_DEBOUNCE_MS = 500;
const TIME_RANGE_OPTIONS = ['1h', '6h', '24h', '3d', '1w', '2w', '4w', '30d'];
const MODE_OPTIONS = ['review', 'live', 'unknown'];
const ALL_RECORDED = 'all';
const TIME_WINDOW_SELECT_LABEL = 'Time window';
export const HORIZON_FILTER_STORAGE_KEY = scopedStorageKey('central-agentic-ops.dashboard.horizon-filter-settings');

/**
 * @param {(filters: Map<string, string[]>, timeWindow?: TimeWindow) => void} onChange
 * @param {{ defaultRange?: string, referenceEnd?: string, signal?: AbortSignal, restoreSettings?: boolean, showFilters?: boolean, maxDays?: number }} [options]
 * @returns {HTMLElement}
 */
export function renderFilterBar(onChange, options = {}) {
  const persisted = readHorizonSettings();
  const filters = /** @type {HTMLInputElement} */ (h('input', {
    type: 'search',
    value: typeof persisted.filters === 'string' ? persisted.filters : '',
    'aria-label': 'Current filters',
    spellcheck: 'false'
  }));
  const count = renderCountBadge(0, '0 filters');
  // The filter count badge is the smallest DOM update needed from the
  // current filter count; a single effect is the only place that writes it
  // onto the owned `count` node, matching the `createCopyControl`
  // state/effect shape for owned interactive DOM.
  const filterCount = state(0);
  effect(() => {
    const currentCount = filterCount.get();
    count.textContent = String(currentCount);
    count.setAttribute('aria-label', `${currentCount} filters`);
  }, { signal: options.signal });
  const applyFilters = debounce(onChange, FILTER_DEBOUNCE_MS);
  options.signal?.addEventListener('abort', () => applyFilters.cancel(), { once: true });
  /** @type {ReturnType<typeof renderHorizonControl>} */
  let horizonControl;
  const emit = () => {
    applyFilters.cancel();
    if (!horizonControl.valid()) return;
    if (options.showFilters === false) {
      const savedFilters = readHorizonSettings().filters;
      filters.value = typeof savedFilters === 'string' ? savedFilters : '';
    }
    stripModeTokens();
    const parsed = parseFilters(filters.value);
    parsed.set('mode', horizonControl.modes());
    updateCount(parsed);
    onChange(parsed, horizonControl.value());
  };
  horizonControl = renderHorizonControl(options.defaultRange ?? ALL_RECORDED, options.referenceEnd, emit, options.signal, options.maxDays);
  const root = h(
    'div',
    { className: 'toolbar filter-bar', 'aria-label': 'Dashboard filters' },
    h(
      'div',
      { className: 'filter-tuning-controls' },
      options.showFilters === false ? null : h(
        'div',
        { className: 'filter-control' },
        filters,
        renderIconSpan('search-control', 'eye', { ariaHidden: true }),
        count
      ),
      horizonControl.element
    ),
    null
  );
  const expanded = state(false);
  // Reflect expanded state on the root; external triggers mirror the event.
  effect(() => {
    const isExpanded = expanded.get();
    root.classList.toggle('filter-bar-expanded', isExpanded);
    root.dispatchEvent(new CustomEvent('dashboard-horizon-expanded', { detail: isExpanded }));
  }, { signal: options.signal });
  root.addEventListener('dashboard-horizon-toggle', () => expanded.set((current) => !current), { signal: options.signal });
  root.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape' || !expanded.get()) return;
    expanded.set(false);
    event.stopPropagation();
  }, { signal: options.signal });
  root.addEventListener('dashboard-horizon-collapse', () => expanded.set(false), { signal: options.signal });
  filters.addEventListener('input', () => {
    if (!horizonControl.valid()) return;
    stripModeTokens();
    persistFilterText(filters.value);
    const parsed = parseFilters(filters.value);
    parsed.set('mode', horizonControl.modes());
    updateCount(parsed);
    applyFilters(parsed, horizonControl.value());
  }, { signal: options.signal });
  /** Strips `mode:` / `rollout-mode:` tokens from the freeform filter input, since applied
   * mode values always come from the horizon control's checkboxes, not this text field. */
  function stripModeTokens() {
    const cursor = filters.selectionStart;
    const nextValue = filters.value
      .split(/\s+/)
      .filter((token) => {
        const separator = token.indexOf(':');
        const field = separator > 0 ? token.slice(0, separator) : '';
        return field !== 'mode' && field !== 'rollout-mode';
      })
      .join(' ');
    if (nextValue !== filters.value) {
      filters.value = nextValue;
      if (cursor !== null) {
        const nextCursor = Math.min(cursor, nextValue.length);
        filters.setSelectionRange(nextCursor, nextCursor);
      }
    }
  }
  /** @param {Map<string, string[]>} parsed */
  function updateCount(parsed) {
    filterCount.set([...parsed.values()].reduce((total, values) => total + values.length, 0));
  }
  const initialFilters = parseFilters(filters.value);
  initialFilters.set('mode', horizonControl.modes());
  updateCount(initialFilters);
  const persistedModes = Array.isArray(persisted.modes)
    ? [...new Set(persisted.modes.filter((mode) => MODE_OPTIONS.includes(mode)))]
    : MODE_OPTIONS;
  const hasModeOverride = Array.isArray(persisted.modes)
    && (persistedModes.length > 0 || persisted.modes.length === 0)
    && (persistedModes.length !== MODE_OPTIONS.length
      || persistedModes.some((mode, index) => mode !== MODE_OPTIONS[index]));
  const hasCustomizedSettings = filters.value.length > 0
    || (typeof persisted.range === 'string' && persisted.range !== (options.defaultRange ?? ALL_RECORDED))
    || hasModeOverride;
  if (hasCustomizedSettings && options.restoreSettings !== false) queueMicrotask(() => {
    if (!options.signal?.aborted) emit();
  });
  return root;
}

/**
 * Closes an expanded horizon control when a click occurs outside its filter bar.
 * @param {HTMLElement} root
 * @param {() => void} [collapseDashboardHorizon]
 */
export function enableHorizonOutsideClickDismissal(root, collapseDashboardHorizon = () => {}) {
  root.addEventListener('click', (event) => {
    const target = event.target;
    if (!(target instanceof Element) || target.closest('[data-horizon-trigger]')) return;
    const expanded = [...root.querySelectorAll('.filter-bar-expanded')]
      .filter((element) => element instanceof HTMLElement);
    if (expanded.length === 0 || expanded.some((element) => element.contains(target))) return;
    for (const element of expanded) {
      element.dispatchEvent(new Event('dashboard-horizon-collapse'));
      element.classList.remove('filter-bar-expanded');
    }
    collapseDashboardHorizon();
  });
}

/**
 * @param {string} defaultRange
 * @param {string | undefined} referenceEnd
 * @param {() => void} onChange
 * @param {AbortSignal} [signal]
 * @param {number} [maxDays]
 */
function renderHorizonControl(defaultRange, referenceEnd, onChange, signal, maxDays) {
  const persisted = readHorizonSettings();
  let range = typeof persisted.range === 'string' ? persisted.range : defaultRange;
  if (range === 'All recorded') range = ALL_RECORDED;
  if (!TIME_RANGE_OPTIONS.includes(range) && range !== 'custom' && range !== ALL_RECORDED) range = ALL_RECORDED;
  if (maxDays && range === ALL_RECORDED) range = '30d';
  const persistedStart = typeof persisted.start === 'string' ? persisted.start : null;
  const persistedEnd = typeof persisted.end === 'string' ? persisted.end : null;
  if (range === 'custom' && !validTimeWindow(persistedStart, persistedEnd, maxDays)) range = maxDays ? '30d' : defaultRange;
  const initialWindow = relativeTimeWindow(
    range === 'custom' || range === ALL_RECORDED ? '1w' : range,
    referenceEnd
  );
  let persistedModes = MODE_OPTIONS;
  if (Array.isArray(persisted.modes)) {
    const validModes = [...new Set(persisted.modes.filter((mode) => MODE_OPTIONS.includes(mode)))];
    if (validModes.length > 0 || persisted.modes.length === 0) {
      persistedModes = validModes;
    }
  }

  const select = /** @type {HTMLSelectElement} */ (h(
    'select',
    { 'aria-label': TIME_WINDOW_SELECT_LABEL },
    ...TIME_RANGE_OPTIONS.map((value) => h('option', { value }, `Last ${formatDashboardHorizon(value)}`)),
    maxDays ? null : h('option', { value: ALL_RECORDED }, 'All time'),
    h('option', { value: 'custom' }, 'Custom range')
  ));
  select.value = range;
  const start = /** @type {HTMLInputElement} */ (h('input', {
    type: 'datetime-local',
    'aria-label': 'Window start time',
    value: localDateTimeValue(range === 'custom' ? persistedStart : initialWindow.start)
  }));
  const end = /** @type {HTMLInputElement} */ (h('input', {
    type: 'datetime-local',
    'aria-label': 'Window stop time',
    value: localDateTimeValue(range === 'custom' ? persistedEnd : initialWindow.end)
  }));
  const applyCustomRange = h('button', { type: 'button' }, 'Apply');
  const modeInputs = MODE_OPTIONS.map((mode) => /** @type {HTMLInputElement} */ (h('input', {
    type: 'checkbox',
    value: mode,
    checked: persistedModes.includes(mode)
  })));

  const value = () => {
    if (select.value === ALL_RECORDED) return undefined;
    if (select.value !== 'custom') return relativeTimeWindow(select.value, referenceEnd);
    const customStart = isoDateTimeValue(start.value);
    const customEnd = isoDateTimeValue(end.value);
    return validTimeWindow(customStart, customEnd, maxDays)
      ? { range: 'custom', start: customStart, end: customEnd }
      : undefined;
  };
  const modes = () => modeInputs.filter((input) => input.checked).map((input) => input.value);
  const persist = () => {
    const savedFilters = readHorizonSettings().filters;
    /** @type {{ range: string, modes: string[], filters?: string, start?: string, end?: string }} */
    const settings = {
      range: select.value,
      modes: modes(),
      ...(typeof savedFilters === 'string' && savedFilters ? { filters: savedFilters } : {})
    };
    if (select.value === 'custom') {
      const selected = value();
      if (selected) {
        settings.start = selected.start;
        settings.end = selected.end;
      }
    }
    try {
      globalThis.window?.localStorage?.setItem(HORIZON_FILTER_STORAGE_KEY, JSON.stringify(settings));
    } catch (error) {
      // The filters still work for the current page when storage is unavailable.
      debug({ event: 'persist-failed', reason: error instanceof Error ? error.name : 'unknown' });
    }
  };
  const validationMessage = h('span', { role: 'alert', hidden: true });
  const updateValidity = () => {
    const invalid = select.value === 'custom' && !value();
    start.setAttribute('aria-invalid', String(invalid));
    end.setAttribute('aria-invalid', String(invalid));
    validationMessage.hidden = !invalid;
    validationMessage.textContent = invalid
      ? `Choose a start before the stop${maxDays ? `, no more than ${maxDays} days apart` : ''}.`
      : '';
    return !invalid;
  };
  const customControls = h(
    'div',
    { className: 'time-window-custom', hidden: range !== 'custom' },
    renderLabeledControl('Start', start),
    renderLabeledControl('Stop', end),
    applyCustomRange,
    validationMessage
  );
  select.addEventListener('change', () => {
    customControls.hidden = select.value !== 'custom';
    if (select.value !== 'custom' && select.value !== ALL_RECORDED) {
      const selected = relativeTimeWindow(select.value, referenceEnd);
      start.value = localDateTimeValue(selected.start);
      end.value = localDateTimeValue(selected.end);
    }
    if (!updateValidity()) return;
    persist();
    debug({ event: 'range-applied', range: select.value });
    onChange();
  }, { signal });
  for (const input of [start, end]) {
    input.addEventListener('change', () => {
      select.value = 'custom';
      customControls.hidden = false;
      updateValidity();
    }, { signal });
  }
  applyCustomRange.addEventListener('click', () => {
    select.value = 'custom';
    customControls.hidden = false;
    if (!updateValidity()) return;
    persist();
    onChange();
  }, { signal });
  for (const input of modeInputs) {
    input.addEventListener('change', () => {
      if (!updateValidity()) return;
      persist();
      onChange();
    }, { signal });
  }

  return {
    element: h(
      'div',
      { className: 'time-window-control', 'aria-label': 'Evidence window' },
      renderLabeledControl('Window', select, { className: 'time-window-preset', visuallyHiddenLabel: true }),
      h(
        'fieldset',
        { className: 'mode-filter-control' },
        h('legend', null, 'Modes'),
        ...modeInputs.map((input, index) => h('label', null, input, MODE_OPTIONS[index]))
      ),
      customControls
    ),
    value,
    modes,
    valid: updateValidity
  };
}

/** @param {string} filters */
function persistFilterText(filters) {
  try {
    const settings = readHorizonSettings();
    if (filters) settings.filters = filters;
    else delete settings.filters;
    globalThis.window?.localStorage?.setItem(HORIZON_FILTER_STORAGE_KEY, JSON.stringify(settings));
  } catch {
    // The filters still work for the current page when storage is unavailable.
  }
}

/** @returns {{ range?: unknown, modes?: unknown, start?: unknown, end?: unknown, filters?: unknown }} */
function readHorizonSettings() {
  try {
    const value = JSON.parse(globalThis.window?.localStorage?.getItem(HORIZON_FILTER_STORAGE_KEY) ?? 'null');
    return value && typeof value === 'object' ? value : {};
  } catch (error) {
    debug({ event: 'read-settings-failed', reason: error instanceof Error ? error.name : 'unknown' });
    return {};
  }
}

/**
 * Reports whether the dashboard's persisted (or otherwise effective)
 * time-window range currently narrows results away from "All time". A table
 * view with zero rows uses this to distinguish "no data was ever recorded"
 * from "a time window filter is hiding recorded rows" and offer an
 * accessible {@link clearTimeWindowFilter} action instead of leaving
 * operators to guess why an otherwise populated source renders empty.
 * @param {string} [defaultRange]
 * @returns {boolean}
 */
export function isTimeWindowFilterActive(defaultRange = ALL_RECORDED) {
  const persisted = readHorizonSettings();
  const range = typeof persisted.range === 'string' && persisted.range.length > 0 ? persisted.range : defaultRange;
  return (range === 'All recorded' ? ALL_RECORDED : range) !== ALL_RECORDED;
}

/**
 * Resets the rendered time-window control (scoped to `root`, defaulting to
 * the whole document) to "All time" and dispatches the same `change` event
 * the control's own select emits, so the existing persistence and
 * re-render wiring in {@link renderHorizonControl} runs unchanged. A no-op
 * when no time-window control is present in `root`, or it is already
 * cleared. Callers that live inside one dashboard page should scope `root`
 * to that page's container so this only clears the visible control.
 * @param {ParentNode} [root]
 */
export function clearTimeWindowFilter(root = globalThis.document) {
  const select = root?.querySelector?.(`[aria-label="${TIME_WINDOW_SELECT_LABEL}"]`);
  if (!(select instanceof HTMLSelectElement) || select.value === ALL_RECORDED) return;
  select.value = [...select.options].some((option) => option.value === ALL_RECORDED) ? ALL_RECORDED : '30d';
  select.dispatchEvent(new Event('change', { bubbles: true }));
}

/** @param {string} range @param {ParentNode} [root] */
export function setTimeWindowRange(range, root = globalThis.document) {
  if (!TIME_RANGE_OPTIONS.includes(range) && range !== ALL_RECORDED) return;
  const select = root?.querySelector?.(`[aria-label="${TIME_WINDOW_SELECT_LABEL}"]`);
  if (!(select instanceof HTMLSelectElement)) return;
  select.value = range === ALL_RECORDED && ![...select.options].some((option) => option.value === range) ? '30d' : range;
  select.dispatchEvent(new Event('change', { bubbles: true }));
}

/**
 * Applies a custom time window through the rendered horizon controls so the
 * existing persistence and filter refresh behavior remains authoritative.
 * @param {string} start
 * @param {string} end
 * @param {ParentNode} [root]
 */
export function setTimeWindowFilter(start, end, root = globalThis.document) {
  if (!validTimeWindow(start, end)) return;
  const select = root?.querySelector?.(`[aria-label="${TIME_WINDOW_SELECT_LABEL}"]`);
  const startInput = root?.querySelector?.('[aria-label="Window start time"]');
  const endInput = root?.querySelector?.('[aria-label="Window stop time"]');
  if (!(select instanceof HTMLSelectElement)
      || !(startInput instanceof HTMLInputElement)
      || !(endInput instanceof HTMLInputElement)) return;
  select.value = 'custom';
  startInput.value = localDateTimeValue(start);
  endInput.value = localDateTimeValue(end);
  const apply = [...(root?.querySelectorAll?.('.time-window-control button') ?? [])]
    .find((button) => button.textContent === 'Apply');
  if (apply instanceof HTMLButtonElement) apply.click();
}

/** @param {string} range @param {string | undefined} referenceEnd @returns {TimeWindow} */
export function relativeTimeWindow(range, referenceEnd) {
  const parsedEnd = Date.parse(referenceEnd ?? '');
  const end = Number.isFinite(parsedEnd) ? parsedEnd : Date.now();
  let hours;
  try {
    hours = dashboardHorizonHours(range);
  } catch {
    hours = dashboardHorizonHours('1w');
  }
  const start = end - hours * 3_600_000;
  return { range, start: new Date(start).toISOString(), end: new Date(end).toISOString() };
}

/** @param {string | null | undefined} start @param {string | null | undefined} end @param {number} [maxDays] */
function validTimeWindow(start, end, maxDays) {
  const startTime = Date.parse(start ?? '');
  const endTime = Date.parse(end ?? '');
  return Number.isFinite(startTime) && Number.isFinite(endTime) && startTime < endTime
    && (!maxDays || endTime - startTime <= maxDays * 86_400_000);
}

/** @param {string | null | undefined} value */
function localDateTimeValue(value) {
  const instant = Date.parse(value ?? '');
  if (!Number.isFinite(instant)) return '';
  const date = new Date(instant);
  return new Date(instant - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

/** @param {string} value */
function isoDateTimeValue(value) {
  const instant = Date.parse(value);
  return Number.isFinite(instant) ? new Date(instant).toISOString() : '';
}

/**
 * @param {string} input
 * @returns {Map<string, string[]>}
 */
export function parseFilters(input) {
  /** @type {Map<string, Set<string>>} */
  const parsed = new Map();
  for (const token of input.trim().split(/\s+/)) {
    const separator = token.indexOf(':');
    if (separator < 1 || separator === token.length - 1) continue;
    const field = token.slice(0, separator);
    const value = token.slice(separator + 1);
    const values = parsed.get(field) ?? new Set();
    values.add(value);
    parsed.set(field, values);
  }
  return new Map([...parsed].map(([field, values]) => [field, [...values]]));
}
