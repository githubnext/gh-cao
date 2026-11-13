import { afterEach, describe, expect, it, vi } from 'vitest';
import commands from '../../dashboard-fragments/commands.json' with { type: 'json' };
import { cliQueueOperation, configureCliQueue, queryCliQueue } from '../../src/data/cli-queue.js';
import { processDataRequest } from '../../src/data-worker.js';
import { cliQueueAvailable, enqueueCliCommand } from '../../src/data-processor.js';
import { disposeDashboard, renderDashboard } from '../../src/presenter.js';

afterEach(() => {
  configureCliQueue(false);
  vi.unstubAllGlobals();
});

describe('worker command queue', () => {
  it('declares a single full-width lazy card table with server-backed filtering', () => {
    expect(commands.pages[0].route).toEqual({ 'navigation-page': 'overview' });
    expect(commands.pages[0].views).toHaveLength(1);
    expect(commands.pages[0].views[0]).toMatchObject({
      id: 'commands-history', mark: 'table', layout: 'full', 'lazy-list': true,
      'show-title': false,
      prompt: 'none',
      controls: 'filter-only', 'column-summaries': false,
      'card-drill': { type: 'external', field: 'command-dashboard-link' }
    });
  });

  it('declares full-view command output with Commands as its Back navigation parent', () => {
    expect(commands.pages[1].route).toEqual({
      'hash-query-parameter': 'command', 'navigation-page': 'commands'
    });
    expect(commands.pages[1].views[0]).toMatchObject({
      id: 'command-terminal', element: 'command-terminal', layout: 'full-view'
    });
  });

  it('returns from command output through Commands to Overview without cycling through history', () => {
    const previousUrl = window.location.href;
    window.history.replaceState({ centralAgenticOpsNavigationIndex: 4 }, '', '#page-command?command=42');
    const root = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'command-back', title: 'Command navigation',
          navigation: [{ label: 'Main', pages: ['overview'] }],
          pages: [
            { id: 'overview', kind: 'custom', title: 'Overview', views: [] },
            ...commands.pages.map((page) => ({
              id: page.id, kind: /** @type {const} */ ('custom'), title: page.title, route: page.route, views: []
            }))
          ]
        }
      },
      sources: {}
    });
    document.body.append(root);
    try {
      const back = /** @type {HTMLButtonElement} */ (root.querySelector('.mobile-history-back'));
      expect(back.getAttribute('aria-label')).toMatch(/^Back to Commands$/i);
      back.click();
      expect(window.location.hash).toBe('#page-commands');
      expect(back.getAttribute('aria-label')).toBe('Back to Overview');
      back.click();
      expect(window.location.hash).toBe('#page-overview');
      expect(back.hidden).toBe(true);
    } finally {
      disposeDashboard(root);
      root.remove();
      window.history.replaceState({}, '', previousUrl);
    }
  });

  it('omits the declared interaction-only route without local queue support', () => {
    const previousUrl = window.location.href;
    window.history.replaceState({}, '', '#page-command?command=42');
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const root = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: { id: 'command-contract', title: 'Command route', pages: [
          /** @type {import('../../src/presenter.js').PresentableCustomPage} */ (commands.pages[1])
        ] }
      },
      sources: {}
    });
    document.body.append(root);
    try {
      expect(root.querySelector('.command-terminal')).toBeNull();
      expect(root.querySelector('[data-page-id="command"]')).toBeNull();
      expect(fetch).not.toHaveBeenCalled();
    } finally {
      disposeDashboard(root);
      root.remove();
      window.history.replaceState({}, '', previousUrl);
    }
  });

  it('fails closed without advertised local authority and never fetches', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    expect(cliQueueAvailable()).toBe(false);
    await expect(enqueueCliCommand({ id: 'compile' })).rejects.toThrow('unavailable');
    await expect(cliQueueOperation({ action: 'list' })).rejects.toThrow('unavailable');
    const result = await queryCliQueue({
      sourceNames: ['command-queue'], context: { pages: [], queries: commands.queries }
    });
    expect(result['command-queue'].metadata.availability).toBe('unavailable');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('runs real declared queries over server pages with authoritative counts', async () => {
    configureCliQueue(true);
    const states = ['queued', 'running', 'completed', 'failed', 'canceled', 'timed-out'];
    const fetch = vi.fn().mockResolvedValue(Response.json({
      commands: states.map((status, index) => ({ id: `cmd-${index}`, actionId: 'compile', status })),
      total: 106, running: 8, revision: 20, pausedReason: 'quota'
    }));
    vi.stubGlobal('fetch', fetch);
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard',
      sourceNames: ['command-queue', 'cli-command-summary'],
      context: { pages: [], queries: commands.queries },
      pagination: { 'command-queue': { limit: 6 } },
      queryContext: { filters: { status: ['queued', 'running'] } }
    }));
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String(fetch.mock.calls[0][0])).toContain('status=queued%2Crunning&limit=6&offset=0');
    expect(result['command-queue'].rows.map((row) => row.status)).toEqual(states);
    expect(result['command-queue'].rows[0]['command-dashboard-link']).toEqual(expect.objectContaining({ 'dashboard-href': '#page-command?command=cmd-0' }));
    expect(result['cli-command-summary'].rows[0]).toMatchObject({ running: 8, total: 106, 'paused-reason': 'quota' });
    expect(result['command-queue'].metadata['total-row-count']).toBe(106);
    expect(JSON.parse(atob(result['command-queue'].continuationToken ?? ''))).toEqual({ offset: 6 });
  });

  it('sends continuation offset to the server rather than loading the queue into the browser', async () => {
    configureCliQueue(true);
    const fetch = vi.fn().mockResolvedValue(Response.json({ commands: [], total: 30, running: 0, revision: 1 }));
    vi.stubGlobal('fetch', fetch);
    await queryCliQueue({
      sourceNames: ['command-queue'], context: { pages: [], queries: commands.queries },
      pagination: { 'command-queue': { limit: 10, continuationToken: btoa(JSON.stringify({ offset: 20 })) } }
    });
    expect(String(fetch.mock.calls[0][0])).toContain('limit=10&offset=20');
  });

  it('does not treat inherited object properties as queue sources', async () => {
    for (const name of ['__proto__', 'constructor']) {
      await expect(queryCliQueue({
        sourceNames: [name, 'cli-commands'],
        context: { pages: [], queries: [] }
      })).rejects.toThrow('source is unavailable');
    }
    expect(Object.prototype).not.toHaveProperty('continuationToken');
  });

  it('compiles the production history view over numeric command identities and epoch timestamps', async () => {
    configureCliQueue(true);
    const fetch = vi.fn().mockResolvedValue(Response.json({
      commands: [{
        id: 42, actionId: 'compile', command: 'gh aw upgrade --repo octo/repo --force',
        repository: 'octo/repo', status: 'running', createdAt: 1792022400000
      }],
      total: 1, running: 1, revision: 20
    }));
    vi.stubGlobal('fetch', fetch);
    const result = await queryCliQueue({
      sourceNames: ['command-queue'], context: { pages: commands.pages, queries: commands.queries },
      pageId: 'commands', viewId: 'commands-history',
      queryContext: { filters: { status: ['running'] } }
    });
    const output = Object.values(result)[0];
    expect(output.metadata.availability).toBe('available');
    expect(output.rows).toHaveLength(1);
    expect(output.rows[0]).toMatchObject({
      status: 'running', createdAt: '2026-10-15T00:00:00.000Z',
      'command-title': 'gh aw upgrade --repo octo/repo --force',
      'command-target': 'octo/repo',
      'command-dashboard-link': {
        'dashboard-href': '#page-command?command=42',
        'dashboard-label': 'gh aw upgrade --repo octo/repo --force'
      }
    });
  });

  it.each([
    [{ campaign: 'maintenance' }, 'maintenance'],
    [{ 'package-source': 'owner/catalog/pkg' }, 'owner/catalog/pkg'],
    [{}, 'Local control repository']
  ])('resolves the declared target from %j', async (target, expected) => {
    configureCliQueue(true);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({
      commands: [{ id: 1, actionId: 'compile', command: 'gh aw compile', ...target }],
      total: 1, running: 0, revision: 1
    })));
    const result = await queryCliQueue({
      sourceNames: ['command-queue'], context: { pages: [], queries: commands.queries }
    });
    expect(result['command-queue'].rows[0]['command-target']).toBe(expected);
  });

  it('enqueues only declared action arguments and returns a durable ID through worker dispatch', async () => {
    configureCliQueue(true);
    const fetch = vi.fn().mockResolvedValue(Response.json({ id: 'durable-1', status: 'queued' }, { status: 202 }));
    vi.stubGlobal('fetch', fetch);
    const command = { id: 'compile', arguments: {}, values: {}, idempotencyKey: 'approved-once' };
    expect(await processDataRequest({ operation: 'cli-queue', action: 'enqueue', command })).toEqual({ id: 'durable-1', status: 'queued' });
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(command);
    expect(fetch.mock.calls[0][1].method).toBe('POST');
  });

  it('replays sequenced NDJSON once and keeps cancellation separate from stream detachment', async () => {
    configureCliQueue(true);
    const completion = { type: 'complete', result: { ok: false, exitCode: 1 }, command: { id: 'durable-1', status: 'failed' } };
    const events = [
      { type: 'output', sequence: 1, stream: 'stdout', data: 'old' },
      { type: 'output', sequence: 2, stream: 'stderr', data: 'new\n' },
      { type: 'output', sequence: 2, stream: 'stderr', data: 'duplicate' },
      { type: 'status', command: { id: 'durable-1', status: 'running' } },
      completion
    ];
    const fetch = vi.fn().mockResolvedValue(new Response(events.map((event) => JSON.stringify(event)).join('\n')));
    vi.stubGlobal('fetch', fetch);
    const notify = vi.fn();
    expect(await cliQueueOperation({ action: 'attach', commandId: 'durable-1', after: 1 }, undefined, notify)).toEqual(completion);
    expect(notify.mock.calls.map(([event]) => event)).toEqual([events[1], events[3]]);
    expect(String(fetch.mock.calls[0][0])).toContain('/durable-1/output?after=1');
    expect(fetch.mock.calls[0][1].method).toBeUndefined();
  });

  it('routes explicit cancel and retry to separate POST endpoints', async () => {
    configureCliQueue(true);
    const fetch = vi.fn().mockImplementation(async () => Response.json({ id: 'durable-1' }));
    vi.stubGlobal('fetch', fetch);
    await cliQueueOperation({ action: 'cancel', commandId: 42 });
    await cliQueueOperation({ action: 'retry', commandId: 'durable-1', idempotencyKey: 'retry-once' });
    expect(String(fetch.mock.calls[0][0])).toContain('/42/cancel');
    expect(String(fetch.mock.calls[1][0])).toContain('/durable-1/retry');
    expect(fetch.mock.calls.every(([, options]) => options.method === 'POST')).toBe(true);
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ idempotencyKey: 'retry-once' });
  });
});
