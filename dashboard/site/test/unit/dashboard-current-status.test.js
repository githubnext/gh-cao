import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderDashboardCurrentStatus } from '../../src/components/refresh-error.js';
import { updateRateLimitNotification } from '../../src/rate-limit-notification.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { splitDashboardDocument } from '../../src/dashboard-chunks.js';
import { validateDashboardDocument } from '../../src/validator.js';
import contract from '../fixtures/overview-ingestion-status-contract.json' with { type: 'json' };

const now = Date.parse('2026-10-16T12:00:00Z');
const configuration = contract.indicator;

/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'indicator-test', 'source-kind': 'derived',
  'as-of': new Date(now).toISOString(), 'retrieved-at': new Date(now).toISOString(),
  completeness: 'complete', freshness: 'fresh', availability: 'available'
};
/** @param {unknown} [timestamp] */
const evidence = (timestamp = now) => ({ source: configuration['freshness-source'], rows: [{ 'latest-data-at': timestamp }], metadata });
const collection = (health = 'healthy', configured = true) => ({
  source: configuration['health-source'], rows: [{ health, configured }], metadata
});

/** @param {Record<string, unknown>} [fields] */
const ingestion = (fields = {}) => ({
  source: configuration['ingestion-source'],
  rows: [{
    'status-name': 'Paused', 'status-label': 'Data ingestion: Paused', 'status-icon': 'clock', 'status-tone': 'attention',
    status: 'paused', activity: 'Idle', repository: 'octo/api', 'scanned-hours': 48, 'target-hours': 168,
    'api-remaining': 999, 'api-reserve': 1000,
    'next-attempt-at': contract.local.transaction.nextAttemptAt,
    'api-reset-at': contract.local.transaction.apiResetAt,
    'observed-at': contract.local.transaction.createdAt,
    message: contract.local.transaction.ingestionMessage,
    ...fields
  }],
  metadata
});

/** @param {{ timestamp?: unknown, health?: string, configured?: boolean } & NonNullable<Parameters<typeof renderDashboardCurrentStatus>[1]>} [options] */
function renderStatus({ timestamp = now, health = 'healthy', configured = true, ...options } = {}) {
  const status = renderDashboardCurrentStatus({ createdAt: new Date(now).toISOString() }, {
    configuration,
    sources: {
      [configuration['freshness-source']]: evidence(timestamp),
      [configuration['health-source']]: collection(health, configured)
    },
    ...options
  });
  document.body.append(status);
  return status;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
});

afterEach(async () => {
  updateRateLimitNotification('/api/v1/query', 200);
  document.body.replaceChildren();
  document.head.querySelector('meta[name="dashboard-data-backend"]')?.remove();
  await Promise.resolve();
  vi.useRealTimers();
});

function hosted() {
  const meta = document.createElement('meta');
  meta.name = 'dashboard-data-backend';
  meta.content = 'server-http';
  document.head.append(meta);
}

describe('global upstream ingestion indicator', () => {
  it('keeps declared status queries in the shell and validates their output fields', () => {
    expect(authoritativeDashboard.dashboard['data-status']).toEqual(configuration);
    const { core } = splitDashboardDocument(authoritativeDashboard);
    expect(core.dashboard.queries?.map((query) => query.name)).toEqual(expect.arrayContaining([
      configuration['freshness-source'], configuration['health-source'], configuration['ingestion-source']
    ]));
    const document = structuredClone(authoritativeDashboard);
    document.dashboard['data-status']['freshness-source'] = configuration['health-source'];
    expect(validateDashboardDocument(JSON.stringify(document)).ok).toBe(false);
    const invalidIngestion = structuredClone(authoritativeDashboard);
    invalidIngestion.dashboard['data-status']['ingestion-source'] = configuration['health-source'];
    expect(validateDashboardDocument(JSON.stringify(invalidIngestion)).ok).toBe(false);
    for (const threshold of [0, -1, '24']) {
      const invalid = structuredClone(authoritativeDashboard);
      invalid.dashboard['data-status']['stale-after-hours'] = threshold;
      expect(validateDashboardDocument(JSON.stringify(invalid)).ok).toBe(false);
    }
  });

  it('uses upstream evidence time instead of the new browser snapshot time', () => {
    const status = renderStatus({ timestamp: now - 24 * 3_600_000 });
    expect(status.dataset.tone).toBe('attention');
    expect(status.querySelector('.octicon-alert')).not.toBeNull();
    expect(status.querySelector('button')?.getAttribute('aria-label')).toBe('Dashboard data is stale');
    expect(status.textContent).toContain('Inactivity alone does not prove ingestion is broken');
  });

  it('turns amber at the exact 24-hour boundary without a database update', async () => {
    const status = renderStatus({ timestamp: now - 23 * 3_600_000 });
    expect(status.dataset.tone).toBe('success');
    expect(status.querySelector('.octicon-check-circle-fill')).not.toBeNull();
    await vi.advanceTimersByTimeAsync(3_600_000);
    expect(status.dataset.tone).toBe('attention');
    expect(status.querySelector('.octicon-alert')).not.toBeNull();
    status.remove();
    await Promise.resolve();
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each([null, 'invalid', now + 60_000])('keeps missing, invalid, or future evidence unknown: %s', (timestamp) => {
    const status = renderStatus({ timestamp });
    expect(status.dataset.tone).toBe('muted');
    expect(status.querySelector('.octicon-question')).not.toBeNull();
    expect(status.querySelector('.octicon-check-circle-fill')).toBeNull();
  });

  it.each([
    ['healthy', 'success', 'check-circle-fill'],
    ['recovering', 'attention', 'alert'],
    ['degraded', 'danger', 'stop']
  ])('renders reported %s collection health without deriving it from counters', (health, tone, icon) => {
    hosted();
    const status = renderStatus({ health });
    expect(status.dataset.tone).toBe(tone);
    expect(status.querySelector(`.octicon-${icon}`)).not.toBeNull();
  });

  it('gives reported failures priority over stale evidence', () => {
    hosted();
    const status = renderStatus({ health: 'degraded', timestamp: now - 48 * 3_600_000 });
    expect(status.dataset.tone).toBe('danger');
    expect(status.textContent).toContain('unresolved failure');
  });

  it('does not treat unconfigured or inaccessible collection health as healthy', () => {
    hosted();
    const status = renderStatus({ configured: false });
    expect(status.dataset.tone).toBe('muted');
    expect(status.textContent).toContain('could not be verified');
  });

  it('shows refresh errors and rate limiting as danger, then recovers', () => {
    const failed = renderStatus({ failed: true });
    expect(failed.dataset.tone).toBe('danger');
    const status = renderStatus();
    updateRateLimitNotification('/api/v1/query', 429);
    expect(status.dataset.tone).toBe('danger');
    expect(status.querySelector('.octicon-stop')).not.toBeNull();
    expect(status.querySelector('button')?.getAttribute('aria-label')).toBe('Dashboard is rate limited');
    updateRateLimitNotification('/api/v1/query', 200);
    expect(status.dataset.tone).toBe('success');
  });

  it('subscribes independently to freshness and hosted health and stops updates on removal', async () => {
    hosted();
    const updates = new Map();
    /** @type {AbortSignal[]} */
    const signals = [];
    const loader = Object.assign(vi.fn(), {
      subscribeBackgroundSources: vi.fn(async ([name], { signal, onUpdate }) => {
        updates.set(name, onUpdate);
        signals.push(signal);
        return { [name]: name === configuration['freshness-source'] ? evidence() : collection() };
      })
    });
    const status = renderStatus({ loadPageSources: loader });
    await Promise.resolve();
    const button = status.querySelector('button');
    updates.get(configuration['health-source'])({ [configuration['health-source']]: collection('degraded') });
    expect(status.dataset.tone).toBe('danger');
    expect(status.querySelector('button')).toBe(button);
    updates.get(configuration['health-source'])({ [configuration['health-source']]: collection() });
    expect(status.dataset.tone).toBe('success');
    status.remove();
    await Promise.resolve();
    expect(signals.every((signal) => signal.aborted)).toBe(true);
    updates.get(configuration['health-source'])({ [configuration['health-source']]: collection('degraded') });
    expect(status.dataset.tone).toBe('success');
  });

  it('never requests hosted health from a static dashboard', async () => {
    const loader = Object.assign(vi.fn(), {
      subscribeBackgroundSources: vi.fn(async (names) => {
        expect(names).toEqual([configuration['freshness-source']]);
        return { [configuration['freshness-source']]: evidence() };
      })
    });
    renderStatus({ loadPageSources: loader });
    expect(loader.subscribeBackgroundSources.mock.calls).toHaveLength(1);
    expect(loader.subscribeBackgroundSources.mock.calls[0][0]).toEqual([configuration['freshness-source']]);
  });

  it('updates local ingestion in the tooltip while refreshing and stops on removal', async () => {
    hosted();
    const updates = new Map();
    /** @type {AbortSignal[]} */
    const signals = [];
    const initial = {
      [configuration['freshness-source']]: evidence(),
      [configuration['health-source']]: collection('healthy', false),
      [configuration['ingestion-source']]: ingestion()
    };
    const loader = Object.assign(vi.fn(), {
      subscribeBackgroundSources: vi.fn(async ([name], { signal, onUpdate }) => {
        updates.set(name, onUpdate);
        signals.push(signal);
        return { [name]: initial[name] };
      })
    });
    const status = renderStatus({ refreshing: true, sources: initial, loadPageSources: loader });
    await Promise.resolve();
    const button = status.querySelector('button');
    expect(button?.getAttribute('aria-label')).toBe('Data ingestion: Paused');
    expect(status.dataset.tone).toBe('attention');
    expect(status.querySelector('progress')?.getAttribute('value')).toBe('48');
    expect(status.querySelector('progress')?.getAttribute('max')).toBe('168');
    expect(status.querySelector('.ingestion-status-history')?.textContent).toContain('48 / 168 h');
    expect(status.querySelector('.ingestion-status-budget')?.getAttribute('aria-label')).toBe('API points remaining: 999');
    expect(status.querySelector('details')?.open).toBe(false);
    const details = status.querySelector('details');
    if (!details) throw new Error('Missing ingestion diagnostics disclosure');
    details.open = true;
    details.dispatchEvent(new Event('toggle'));
    const collecting = ingestion({
      'status-name': 'Collecting', 'status-label': 'Data ingestion: Collecting', 'status-icon': 'sync', 'status-tone': 'muted',
      'scanned-hours': 72, 'api-remaining': undefined
    });
    updates.get(configuration['ingestion-source'])({ [configuration['ingestion-source']]: collecting });
    expect(status.querySelector('button')).toBe(button);
    expect(button?.getAttribute('aria-label')).toBe('Data ingestion: Collecting');
    expect(status.querySelector('progress')?.getAttribute('value')).toBe('72');
    expect(status.querySelector('.ingestion-status-budget')?.getAttribute('aria-label')).toBe('API points remaining: Unknown');
    expect(status.querySelector('details')?.open).toBe(true);
    status.remove();
    await Promise.resolve();
    expect(signals.every((signal) => signal.aborted)).toBe(true);
    updates.get(configuration['ingestion-source'])({ [configuration['ingestion-source']]: ingestion() });
    expect(button?.getAttribute('aria-label')).toBe('Data ingestion: Collecting');
  });

  it('shows running or idle state and forces a local ingestion start', async () => {
    hosted();
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ requested: true }), { status: 202, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetchMock);
    const updates = new Map();
    const initial = { [configuration['ingestion-source']]: ingestion() };
    const loader = Object.assign(vi.fn(), {
      subscribeBackgroundSources: vi.fn(async ([name], { onUpdate }) => {
        updates.set(name, onUpdate);
        return { [name]: initial[name] };
      })
    });
    const status = renderStatus({ sources: initial, loadPageSources: loader });
    await Promise.resolve();
    expect(status.querySelector('.ingestion-status-state')?.getAttribute('data-state')).toBe('idle');
    expect(status.querySelector('.ingestion-status-state')?.textContent).toBe('Idle');
    const start = /** @type {HTMLButtonElement} */ (status.querySelector('.ingestion-status-start'));
    expect(start.disabled).toBe(false);
    expect(start.textContent).toBe('Start ingestion now');
    start.click();
    expect(status.querySelector('.ingestion-status-start')?.textContent).toBe('Starting…');
    await vi.waitFor(() => expect(status.querySelector('.ingestion-status-start')?.textContent).toBe('Ingestion requested'));
    expect(fetchMock).toHaveBeenCalledWith('http://localhost:3000/api/v1/ingestion/start', expect.objectContaining({ method: 'POST' }));
    updates.get(configuration['ingestion-source'])({ [configuration['ingestion-source']]: ingestion({
      'status-name': 'Collecting', 'status-icon': 'sync', 'status-tone': 'muted', status: 'collecting', activity: 'Running'
    }) });
    expect(status.querySelector('.ingestion-status-state')?.textContent).toBe('Running');
    const running = /** @type {HTMLButtonElement} */ (status.querySelector('.ingestion-status-start'));
    expect(running.disabled).toBe(true);
    expect(running.textContent).toBe('Ingestion running');
    updates.get(configuration['ingestion-source'])({ [configuration['ingestion-source']]: ingestion() });
    expect(status.querySelector('.ingestion-status-start')?.textContent).toBe('Start ingestion now');
    vi.unstubAllGlobals();
  });

  it('places a compact ingestion indicator beside the mobile menu', async () => {
    hosted();
    const header = document.createElement('div');
    const menu = document.createElement('details');
    menu.className = 'mobile-nav-menu';
    header.append(menu);
    document.body.append(header);
    const status = renderStatus({
      mobileHeader: header,
      sources: { [configuration['ingestion-source']]: ingestion({ 'status-icon': 'sync', 'status-tone': 'muted', status: 'collecting', activity: 'Running' }) }
    });
    const indicator = /** @type {HTMLButtonElement} */ (header.querySelector('.mobile-ingestion-indicator'));
    expect(indicator.nextElementSibling).toBe(menu);
    expect(indicator.dataset.state).toBe('running');
    expect(indicator.getAttribute('aria-label')).toBe('Data ingestion: Paused (running)');
    expect(indicator.classList.contains('mobile-ingestion-indicator-refreshing')).toBe(true);
    indicator.click();
    expect(menu.open).toBe(true);
    status.remove();
    await Promise.resolve();
    expect(header.querySelector('.mobile-ingestion-indicator')).toBeNull();
  });

  it('does not present unavailable ingestion as observed progress', () => {
    hosted();
    const status = renderStatus({
      configured: false,
      sources: {
        [configuration['freshness-source']]: evidence(),
        [configuration['health-source']]: collection('healthy', false),
        [configuration['ingestion-source']]: {
          ...ingestion(), metadata: { ...metadata, availability: 'unavailable' }
        }
      }
    });
    expect(status.querySelector('progress')).toBeNull();
    expect(status.querySelector('button')?.getAttribute('aria-label')).toBe('Data ingestion status is unknown');
  });

  it('keeps missing progress and quota unknown instead of drawing a zero-value bar', () => {
    hosted();
    const status = renderStatus({
      sources: {
        [configuration['ingestion-source']]: ingestion({
          'scanned-hours': undefined, 'target-hours': undefined, 'api-remaining': undefined
        })
      }
    });
    expect(status.querySelector('progress')).toBeNull();
    expect(status.textContent).toContain('History unavailable');
    expect(status.querySelector('.ingestion-status-budget')?.getAttribute('aria-label')).toBe('API points remaining: Unknown');
  });
});
