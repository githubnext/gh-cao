// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  HORIZON_FILTER_STORAGE_KEY,
  clearTimeWindowFilter,
  enableHorizonOutsideClickDismissal,
  isTimeWindowFilterActive,
  relativeTimeWindow,
  renderFilterBar,
  setTimeWindowFilter,
  setTimeWindowRange
} from '../../src/components/filter-bar.js';

afterEach(() => {
  document.body.replaceChildren();
  window.history.replaceState(null, '', '/');
  window.localStorage.clear();
});

describe('time-window filter bar', () => {
  it('defaults to all time', async () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange);
    document.body.append(filterBar);
    await Promise.resolve();

    expect(/** @type {HTMLSelectElement} */ (
      filterBar.querySelector('[aria-label="Time window"]')
    ).value).toBe('all');
    expect(onChange).not.toHaveBeenCalled();
  });

  it('anchors relative windows to the latest source timestamp', () => {
    expect(relativeTimeWindow('24h', '2026-09-04T12:00:00Z')).toEqual({
      range: '24h',
      start: '2026-09-03T12:00:00.000Z',
      end: '2026-09-04T12:00:00.000Z'
    });
  });

  it('cancels pending filter updates and stops interaction when its owner is disposed', async () => {
    const lifetime = new AbortController();
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, { signal: lifetime.signal });
    document.body.append(filterBar);
    const search = /** @type {HTMLInputElement} */ (filterBar.querySelector('[aria-label="Current filters"]'));
    search.value = 'repository:api';
    search.dispatchEvent(new Event('input'));
    lifetime.abort();
    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    select.value = '30d';
    select.dispatchEvent(new Event('change'));
    await new Promise((resolve) => setTimeout(resolve, 550));
    expect(onChange).not.toHaveBeenCalled();
    expect(JSON.parse(window.localStorage.getItem(HORIZON_FILTER_STORAGE_KEY) ?? '{}').range).toBeUndefined();
  });

  it.each(['1h', '6h', '24h', '3d', '1w', '2w', '4w', '30d'])(
    'applies and restores the %s preset without exposing custom date inputs',
    async (range) => {
      const onChange = vi.fn();
      const filterBar = renderFilterBar(onChange, { referenceEnd: '2026-10-16T12:00:00Z' });
      document.body.append(filterBar);
      setTimeWindowRange(range, filterBar);

      expect(onChange).toHaveBeenLastCalledWith(
        new Map([['mode', ['review', 'live', 'unknown']]]),
        relativeTimeWindow(range, '2026-10-16T12:00:00Z')
      );
      expect(filterBar.querySelector('.time-window-custom')?.hasAttribute('hidden')).toBe(true);
      const restored = renderFilterBar(vi.fn());
      expect(/** @type {HTMLSelectElement} */ (
        restored.querySelector('[aria-label="Time window"]')
      ).value).toBe(range);
      await Promise.resolve();
    }
  );

  it('only exposes custom dates for a custom range', () => {
    const filterBar = renderFilterBar(vi.fn(), { defaultRange: '1w' });
    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    const custom = filterBar.querySelector('.time-window-custom');
    select.value = 'custom';
    select.dispatchEvent(new Event('change'));
    expect(custom?.hasAttribute('hidden')).toBe(false);
    select.value = 'all';
    select.dispatchEvent(new Event('change'));
    expect(custom?.hasAttribute('hidden')).toBe(true);
  });

  it('keeps page search settings when changing the compact horizon picker', () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, { showFilters: false });
    document.body.append(filterBar);
    expect(filterBar.querySelector('[aria-label="Current filters"]')).toBeNull();
    window.localStorage.setItem(HORIZON_FILTER_STORAGE_KEY, JSON.stringify({ filters: 'run:month' }));

    setTimeWindowRange('30d', filterBar);
    expect(onChange.mock.calls.at(-1)?.[0]).toEqual(
      new Map([['run', ['month']], ['mode', ['review', 'live', 'unknown']]])
    );
    expect(JSON.parse(window.localStorage.getItem(HORIZON_FILTER_STORAGE_KEY) ?? '{}').filters).toBe('run:month');
  });

  it.each(['all', '90d', '180d', '365d'])('caps a persisted %s horizon at 30 days', async (range) => {
    window.localStorage.setItem(HORIZON_FILTER_STORAGE_KEY, JSON.stringify({ range }));
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, { maxDays: 30, defaultRange: '1w' });
    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    expect(select.value).toBe('30d');
    expect([...select.options].map((option) => option.value)).not.toContain('all');
    await Promise.resolve();
    const selected = onChange.mock.calls.at(-1)?.[1];
    expect(Date.parse(selected.end) - Date.parse(selected.start)).toBe(30 * 86_400_000);
  });

  it('rejects custom horizons over 30 days and accepts exactly 30 days', () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, { maxDays: 30, defaultRange: '1w' });
    setTimeWindowFilter('2026-09-01T00:00:00Z', '2026-10-02T00:00:00Z', filterBar);
    expect(onChange).not.toHaveBeenCalled();
    expect(filterBar.querySelector('[role="alert"]')?.textContent).toContain('no more than 30 days');
    expect(filterBar.querySelector('[aria-invalid="true"]')).not.toBeNull();
    setTimeWindowFilter('2026-09-01T00:00:00Z', '2026-10-01T00:00:00Z', filterBar);
    expect(onChange).toHaveBeenCalledOnce();
  });

  it('emits preset and custom start/end windows', async () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, {
      defaultRange: '24h',
      referenceEnd: '2026-09-04T12:00:00Z'
    });
    document.body.append(filterBar);
    await Promise.resolve();

    expect(onChange).not.toHaveBeenCalled();

    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    select.value = '6h';
    select.dispatchEvent(new Event('change'));
    expect(onChange).toHaveBeenLastCalledWith(new Map([['mode', ['review', 'live', 'unknown']]]), {
      range: '6h',
      start: '2026-09-04T06:00:00.000Z',
      end: '2026-09-04T12:00:00.000Z'
    });

    const start = /** @type {HTMLInputElement} */ (filterBar.querySelector('[aria-label="Window start time"]'));
    const end = /** @type {HTMLInputElement} */ (filterBar.querySelector('[aria-label="Window stop time"]'));
    start.value = '2026-09-04T08:00';
    end.value = '2026-09-04T10:00';
    start.dispatchEvent(new Event('change'));
    [...filterBar.querySelectorAll('button')].find((button) => button.textContent === 'Apply')?.click();

    const selected = onChange.mock.calls.at(-1)?.[1];
    expect(selected.range).toBe('custom');
    expect(Date.parse(selected.end) - Date.parse(selected.start)).toBe(2 * 3_600_000);
    expect(JSON.parse(window.localStorage.getItem(HORIZON_FILTER_STORAGE_KEY) ?? '{}')).toMatchObject({
      range: 'custom',
      modes: ['review', 'live', 'unknown']
    });
  });

  it('keeps filters interactive when localStorage is unavailable', async () => {
    const storageDescriptor = Object.getOwnPropertyDescriptor(window, 'localStorage');
    Object.defineProperty(window, 'localStorage', { configurable: true, value: undefined });
    try {
      const onChange = vi.fn();
      const filterBar = renderFilterBar(onChange, {
        defaultRange: '24h',
        referenceEnd: '2026-09-04T12:00:00Z'
      });
      document.body.append(filterBar);
      await Promise.resolve();

      const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
      select.value = '6h';
      select.dispatchEvent(new Event('change'));
      const filterInput = /** @type {HTMLInputElement} */ (filterBar.querySelector('[aria-label="Current filters"]'));
      filterInput.value = 'repository:gh-cao';
      filterInput.dispatchEvent(new Event('input'));

      await vi.waitFor(() => {
        expect(onChange).toHaveBeenLastCalledWith(
          new Map([
            ['repository', ['gh-cao']],
            ['mode', ['review', 'live', 'unknown']]
          ]),
          {
            range: '6h',
            start: '2026-09-04T06:00:00.000Z',
            end: '2026-09-04T12:00:00.000Z'
          }
        );
      });
    } finally {
      if (storageDescriptor) Object.defineProperty(window, 'localStorage', storageDescriptor);
    }
  });

  it('toggles tuning controls from horizon toggle events and collapses on Escape', () => {
    const filterBar = renderFilterBar(vi.fn(), { defaultRange: '24h' });
    const expanded = vi.fn();
    filterBar.addEventListener('dashboard-horizon-expanded', (event) => expanded(/** @type {CustomEvent} */ (event).detail));
    document.body.append(filterBar);

    expect(filterBar.querySelector('.count-badge')?.textContent).toBe('3');
    expect(filterBar.classList.contains('filter-bar-expanded')).toBe(false);

    filterBar.dispatchEvent(new Event('dashboard-horizon-toggle'));
    expect(filterBar.classList.contains('filter-bar-expanded')).toBe(true);
    expect(expanded).toHaveBeenLastCalledWith(true);

    filterBar.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(filterBar.classList.contains('filter-bar-expanded')).toBe(false);
    expect(expanded).toHaveBeenLastCalledWith(false);
  });

  it('keeps the filter count badge text and accessible label in sync as filters change', async () => {
    const filterBar = renderFilterBar(vi.fn(), { defaultRange: '24h' });
    document.body.append(filterBar);
    const badge = filterBar.querySelector('.count-badge');

    expect(badge?.textContent).toBe('3');
    expect(badge?.getAttribute('aria-label')).toBe('3 filters');

    const filterInput = /** @type {HTMLInputElement} */ (filterBar.querySelector('[aria-label="Current filters"]'));
    filterInput.value = 'repository:gh-cao';
    filterInput.dispatchEvent(new Event('input'));
    await Promise.resolve();

    expect(badge?.textContent).toBe('4');
    expect(badge?.getAttribute('aria-label')).toBe('4 filters');
  });

  it('closes the horizon controls when clicking outside the filter bar but not on its trigger', () => {
    const filterBar = renderFilterBar(vi.fn(), { defaultRange: '24h' });
    const trigger = document.createElement('button');
    trigger.dataset.horizonTrigger = '';
    trigger.addEventListener('click', () => filterBar.dispatchEvent(new Event('dashboard-horizon-toggle')));
    const dashboard = document.createElement('main');
    dashboard.append(filterBar, trigger, document.createElement('button'));
    document.body.append(dashboard);
    const collapseDashboardHorizon = vi.fn();
    enableHorizonOutsideClickDismissal(dashboard, collapseDashboardHorizon);

    trigger.click();
    filterBar.querySelector('[aria-label="Current filters"]')?.dispatchEvent(
      new MouseEvent('click', { bubbles: true })
    );
    expect(filterBar.classList.contains('filter-bar-expanded')).toBe(true);

    dashboard.lastElementChild?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(filterBar.classList.contains('filter-bar-expanded')).toBe(false);
    expect(collapseDashboardHorizon).toHaveBeenCalledOnce();

    trigger.click();
    expect(filterBar.classList.contains('filter-bar-expanded')).toBe(true);
    trigger.click();
    expect(filterBar.classList.contains('filter-bar-expanded')).toBe(false);
  });


  it('reports no active time filter by default', () => {
    expect(isTimeWindowFilterActive()).toBe(false);
  });

  it('reports an active time filter once a narrower range is persisted', async () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, { defaultRange: '24h' });
    document.body.append(filterBar);
    await Promise.resolve();

    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    select.value = '6h';
    select.dispatchEvent(new Event('change'));

    expect(isTimeWindowFilterActive('24h')).toBe(true);
  });

  it('is a no-op when clearing a time filter that is already "All time"', async () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange);
    document.body.append(filterBar);
    await Promise.resolve();

    clearTimeWindowFilter(document);

    expect(onChange).not.toHaveBeenCalled();
  });

  it('clears an active time filter and re-applies the change through the rendered control', async () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, {
      defaultRange: '24h',
      referenceEnd: '2026-09-04T12:00:00Z'
    });
    document.body.append(filterBar);
    await Promise.resolve();

    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    select.value = '6h';
    select.dispatchEvent(new Event('change'));
    expect(isTimeWindowFilterActive('24h')).toBe(true);
    onChange.mockClear();

    clearTimeWindowFilter(document);

    expect(select.value).toBe('all');
    expect(onChange).toHaveBeenLastCalledWith(new Map([['mode', ['review', 'live', 'unknown']]]), undefined);
    expect(isTimeWindowFilterActive('24h')).toBe(false);
    expect(JSON.parse(window.localStorage.getItem(HORIZON_FILTER_STORAGE_KEY) ?? '{}')).toMatchObject({ range: 'all' });
  });

  it('sets a custom time window through the rendered control', async () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange);
    document.body.append(filterBar);
    await Promise.resolve();

    setTimeWindowFilter('2026-09-09T00:00:00.000Z', '2026-09-10T00:00:00.000Z', document);

    expect(/** @type {HTMLSelectElement} */ (
      filterBar.querySelector('[aria-label="Time window"]')
    ).value).toBe('custom');
    expect(onChange).toHaveBeenLastCalledWith(new Map([['mode', ['review', 'live', 'unknown']]]), {
      range: 'custom',
      start: '2026-09-09T00:00:00.000Z',
      end: '2026-09-10T00:00:00.000Z'
    });
    expect(JSON.parse(window.localStorage.getItem(HORIZON_FILTER_STORAGE_KEY) ?? '{}')).toMatchObject({
      range: 'custom',
      start: '2026-09-09T00:00:00.000Z',
      end: '2026-09-10T00:00:00.000Z'
    });
  });

  it('restores a preset time window through the rendered control', async () => {
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, { referenceEnd: '2026-09-11T12:00:00Z' });
    document.body.append(filterBar);
    await Promise.resolve();

    setTimeWindowRange('1w', document);

    expect(/** @type {HTMLSelectElement} */ (
      filterBar.querySelector('[aria-label="Time window"]')
    ).value).toBe('1w');
    expect(onChange).toHaveBeenLastCalledWith(new Map([['mode', ['review', 'live', 'unknown']]]), {
      range: '1w',
      start: '2026-09-04T12:00:00.000Z',
      end: '2026-09-11T12:00:00.000Z'
    });
  });

  it('shares persisted horizon and mode settings across filter bars', async () => {
    const firstChange = vi.fn();
    const first = renderFilterBar(firstChange, {
      defaultRange: '24h',
      referenceEnd: '2026-09-04T12:00:00Z'
    });
    document.body.append(first);
    await Promise.resolve();

    const modes = [...first.querySelectorAll('.mode-filter-control input')];
    /** @type {HTMLInputElement} */ (modes[1]).click();
    const select = /** @type {HTMLSelectElement} */ (first.querySelector('[aria-label="Time window"]'));
    select.value = '6h';
    select.dispatchEvent(new Event('change'));
    const filterInput = /** @type {HTMLInputElement} */ (first.querySelector('[aria-label="Current filters"]'));
    filterInput.value = 'repository:gh-cao';
    filterInput.dispatchEvent(new Event('input'));

    const secondChange = vi.fn();
    const second = renderFilterBar(secondChange, {
      defaultRange: '1w',
      referenceEnd: '2026-09-04T12:00:00Z'
    });
    document.body.append(second);
    await Promise.resolve();

    expect(/** @type {HTMLSelectElement | null} */ (
      second.querySelector('[aria-label="Time window"]')
    )?.value).toBe('6h');
    expect(/** @type {HTMLInputElement | null} */ (
      second.querySelector('[aria-label="Current filters"]')
    )?.value).toBe('repository:gh-cao');
    expect([...second.querySelectorAll('.mode-filter-control input')].map(
      (input) => /** @type {HTMLInputElement} */ (input).checked
    )).toEqual([true, false, true]);
    expect(secondChange).toHaveBeenLastCalledWith(
      new Map([
        ['repository', ['gh-cao']],
        ['mode', ['review', 'unknown']]
      ]),
      {
        range: '6h',
        start: '2026-09-04T06:00:00.000Z',
        end: '2026-09-04T12:00:00.000Z'
      }
    );

    for (const input of second.querySelectorAll('.mode-filter-control input')) {
      const checkbox = /** @type {HTMLInputElement} */ (input);
      if (checkbox.checked) checkbox.click();
    }
    expect(secondChange.mock.calls.at(-1)?.[0]).toEqual(new Map([
      ['repository', ['gh-cao']],
      ['mode', []]
    ]));
    expect(JSON.parse(window.localStorage.getItem(HORIZON_FILTER_STORAGE_KEY) ?? '{}').modes).toEqual([]);
  });

  it('falls back to default modes when persisted modes array contains only invalid entries', async () => {
    window.localStorage.setItem(
      HORIZON_FILTER_STORAGE_KEY,
      JSON.stringify({ range: '24h', modes: ['corrupted_mode', 'invalid'] })
    );
    const onChange = vi.fn();
    const filterBar = renderFilterBar(onChange, {
      defaultRange: '24h',
      referenceEnd: '2026-09-04T12:00:00Z'
    });
    document.body.append(filterBar);
    await Promise.resolve();

    expect(onChange).not.toHaveBeenCalled();
  });

  it('stays silent by default and logs only scalar metadata under its predictable category', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=filter-bar', output })
      };
    });
    vi.resetModules();
    const { renderFilterBar: renderFilterBarWithDebug } = await import('../../src/components/filter-bar.js');

    const onChange = vi.fn();
    const filterBar = renderFilterBarWithDebug(onChange, {
      defaultRange: '24h',
      referenceEnd: '2026-09-04T12:00:00Z'
    });
    document.body.append(filterBar);
    await Promise.resolve();

    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    select.value = '6h';
    select.dispatchEvent(new Event('change'));

    expect(output.debug).toHaveBeenCalledWith('[cao:filter-bar]', { event: 'range-applied', range: '6h' });

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('logs a read-settings failure with a sanitized error name, not the raw error or stored value', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=filter-bar', output })
      };
    });
    vi.resetModules();
    const { renderFilterBar: renderFilterBarWithDebug } = await import('../../src/components/filter-bar.js');

    const getItemSpy = vi.spyOn(window.localStorage.__proto__, 'getItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError');
    });

    const onChange = vi.fn();
    const filterBar = renderFilterBarWithDebug(onChange, { defaultRange: '24h' });
    document.body.append(filterBar);
    await Promise.resolve();

    expect(output.debug).toHaveBeenCalledWith('[cao:filter-bar]', { event: 'read-settings-failed', reason: 'SecurityError' });

    getItemSpy.mockRestore();
    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '', output })
      };
    });
    vi.resetModules();
    const { renderFilterBar: renderFilterBarWithoutDebug } = await import('../../src/components/filter-bar.js');

    const onChange = vi.fn();
    const filterBar = renderFilterBarWithoutDebug(onChange, {
      defaultRange: '24h',
      referenceEnd: '2026-09-04T12:00:00Z'
    });
    document.body.append(filterBar);
    await Promise.resolve();

    const select = /** @type {HTMLSelectElement} */ (filterBar.querySelector('[aria-label="Time window"]'));
    select.value = '6h';
    select.dispatchEvent(new Event('change'));

    expect(output.debug).not.toHaveBeenCalled();

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });
});