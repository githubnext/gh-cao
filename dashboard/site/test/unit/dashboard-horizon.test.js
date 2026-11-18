import { beforeEach, describe, expect, it, vi } from 'vitest';
import { bindStatusToHorizon, renderDashboardHorizon } from '../../src/components/dashboard-horizon.js';

/** @returns {import('../../src/presenter.js').PresentableDashboard} */
function makeDashboard() {
  return /** @type {any} */ ({
    horizon: { label: 'Horizon', tooltip: { label: 'Horizon', description: 'Evidence coverage.' } }
  });
}

describe('dashboard horizon', () => {
  beforeEach(() => {
    document.body.replaceChildren();
  });

  it('renders a skeleton while unavailable and resolves once available', () => {
    const horizon = renderDashboardHorizon({
      dashboard: makeDashboard(),
      initialValue: { available: false, evaluatedAt: '', duration: '', start: '', end: '' },
      formatDate: (value) => value
    });
    document.body.append(horizon.element);

    expect(horizon.element.getAttribute('aria-label')).toBe('Horizon unavailable');

    horizon.update({ available: true, evaluatedAt: '2024-01-02T00:00:00Z', duration: '3 days', start: '2024-01-01T00:00:00Z', end: '2024-01-02T00:00:00Z' });

    expect(horizon.element.getAttribute('aria-label')).toBe('Horizon 3 days');
    expect(horizon.element.dataset.dashboardEvaluatedAt).toBe('2024-01-02T00:00:00Z');

    horizon.dispose();
  });

  it('owns an accessible picker independently of the active page filter bar', () => {
    const onChange = vi.fn();
    const horizon = renderDashboardHorizon({
      dashboard: makeDashboard(),
      initialValue: {
        available: true, evaluatedAt: '2026-10-16T12:00:00Z', duration: '1 week',
        start: '2026-10-09T12:00:00Z', end: '2026-10-16T12:00:00Z'
      },
      defaultRange: '1w',
      referenceEnd: '2026-10-16T12:00:00Z',
      formatDate: (value) => value,
      onChange
    });
    const status = document.createElement('div');
    const toggle = document.createElement('button');
    toggle.className = 'tooltip-trigger';
    status.append(toggle);
    document.body.append(horizon.element, status);
    bindStatusToHorizon(status, horizon.element);
    expect(horizon.element.querySelector('.horizon-toggle')).toBeNull();
    expect(horizon.element.getAttribute('aria-label')).toBe('Horizon 1 week');
    toggle.click();
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    const select = /** @type {HTMLSelectElement} */ (horizon.element.querySelector('[aria-label="Time window"]'));
    expect(select.value).toBe('1w');
    select.value = '30d';
    select.dispatchEvent(new Event('change'));
    expect(onChange).toHaveBeenLastCalledWith(
      new Map([['mode', ['review', 'live', 'unknown']]]),
      { range: '30d', start: '2026-09-16T12:00:00.000Z', end: '2026-10-16T12:00:00.000Z' }
    );
    expect(horizon.element.querySelector('details')?.open).toBe(false);
    horizon.collapse();
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    toggle.click();
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    select.focus();
    horizon.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(toggle);
    status.classList.add('tooltip-mobile-popup');
    toggle.click();
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    horizon.dispose();
    status.remove();
    window.localStorage.clear();
  });

  it('stays silent by default and logs only scalar metadata under its predictable category', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=dashboard-horizon', output })
      };
    });
    vi.resetModules();
    const { renderDashboardHorizon: renderDashboardHorizonWithDebug } = await import('../../src/components/dashboard-horizon.js');

    const horizon = renderDashboardHorizonWithDebug({
      dashboard: makeDashboard(),
      initialValue: { available: false, evaluatedAt: '', duration: '', start: '', end: '' },
      formatDate: (value) => value
    });
    document.body.append(horizon.element);

    horizon.update({ available: true, evaluatedAt: '2024-01-02T00:00:00Z', duration: '3 days', start: '2024-01-01T00:00:00Z', end: '2024-01-02T00:00:00Z' });
    expect(output.debug).toHaveBeenCalledWith('[cao:dashboard-horizon]', { event: 'resolved', duration: '3 days' });

    horizon.element.dispatchEvent(new Event('dashboard-horizon-toggle'));
    expect(output.debug).toHaveBeenCalledWith('[cao:dashboard-horizon]', { event: 'toggled', expanded: true });

    horizon.dispose();
    expect(output.debug).toHaveBeenCalledWith('[cao:dashboard-horizon]', { event: 'disposed' });

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }

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
    const { renderDashboardHorizon: renderDashboardHorizonWithoutDebug } = await import('../../src/components/dashboard-horizon.js');

    const horizon = renderDashboardHorizonWithoutDebug({
      dashboard: makeDashboard(),
      initialValue: { available: false, evaluatedAt: '', duration: '', start: '', end: '' },
      formatDate: (value) => value
    });
    document.body.append(horizon.element);

    horizon.update({ available: true, evaluatedAt: '2024-01-02T00:00:00Z', duration: '3 days', start: '2024-01-01T00:00:00Z', end: '2024-01-02T00:00:00Z' });
    horizon.element.dispatchEvent(new Event('dashboard-horizon-toggle'));
    horizon.dispose();

    expect(output.debug).not.toHaveBeenCalled();

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });
});
