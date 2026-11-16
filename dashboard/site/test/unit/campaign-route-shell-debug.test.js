// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { resetSourceStore } from '../../src/source-store.js';

const metadata = {
  'source-id': 'fixture',
  'source-kind': 'fixture',
  'as-of': '2026-08-31T18:00:00Z',
  'retrieved-at': '2026-08-31T18:01:00Z',
  completeness: /** @type {'complete'} */ ('complete'),
  freshness: /** @type {'fresh'} */ ('fresh'),
  availability: /** @type {'available'} */ ('available')
};

const workflows = [{
  campaign: 'ambient-context',
  'campaign-name': 'Ambient Context',
  organization: 'githubnext',
  repository: 'gh-cao',
  workflow: '.github/workflows/ambient-context.md',
  'workflow-name': 'Ambient Context',
  'workflow-role': 'orchestrator',
  'rollout-mode': 'review'
}];

function context() {
  return {
    pageId: 'campaign-detail',
    title: 'Orchestrator and workers',
    sourceNames: ['workflows'],
    contextDetails: [],
    routeParameter: 'campaign',
    headingTag: /** @type {'h3'} */ ('h3'),
    sources: {
      workflows: { source: 'workflows', metadata, rows: workflows }
    }
  };
}

afterEach(() => {
  resetSourceStore();
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('campaign route shell debug logging', () => {
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
    const { renderCampaignRouteVariant } = await import('../../src/components/campaign-route-view.js');

    const rendered = renderCampaignRouteVariant(context(), 'overview');
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'campaign', value: 'ambient-context' }
    }));

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs matched, not-found, and loading route status under its predictable category when enabled', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=campaign-route-shell', output })
      };
    });
    vi.resetModules();
    const { renderCampaignRouteVariant } = await import('../../src/components/campaign-route-view.js');
    const { resetSourceStore: resetStoreForTest } = await import('../../src/source-store.js');

    const matched = renderCampaignRouteVariant(context(), 'overview');
    matched.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'campaign', value: 'ambient-context' }
    }));
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:campaign-route-shell]',
      { event: 'route-render', tab: 'overview', status: 'matched', workflowCount: 1 }
    );

    output.debug.mockClear();
    resetStoreForTest();
    const notFound = renderCampaignRouteVariant({
      ...context(),
      sources: { workflows: { source: 'workflows', metadata, rows: [] } }
    }, 'overview');
    notFound.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'campaign', value: 'missing' }
    }));
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:campaign-route-shell]',
      { event: 'route-render', tab: 'overview', status: 'not-found' }
    );

    output.debug.mockClear();
    resetStoreForTest();
    const { configureSourceLoader: configureLoaderForTest } = await import('../../src/source-store.js');
    /** @type {(source: import('../../src/presenter.js').LogicalSourceInput) => void} */
    let resolveWorkflows = () => {};
    configureLoaderForTest((name) => name === 'workflows'
      ? new Promise((resolve) => { resolveWorkflows = resolve; })
      : Promise.resolve({ source: name, metadata, rows: [] }));
    const loading = renderCampaignRouteVariant({
      ...context(),
      sources: {}
    }, 'overview');
    loading.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'campaign', value: 'ambient-context' }
    }));
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:campaign-route-shell]',
      { event: 'route-render', tab: 'overview', status: 'loading' }
    );
    resolveWorkflows({ source: 'workflows', metadata, rows: [] });
    resetStoreForTest();
  });

  it('logs partial-loading and empty source states under distinct statuses', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=campaign-route-shell', output })
      };
    });
    vi.resetModules();
    const { renderCampaignRouteVariant } = await import('../../src/components/campaign-route-view.js');
    const { resetSourceStore: resetStoreForTest } = await import('../../src/source-store.js');

    for (const [status, sourceMetadata] of /** @type {const} */ ([
      ['loading-partial', { ...metadata, completeness: 'partial' }],
      ['empty', { ...metadata, completeness: 'unknown', availability: 'empty' }]
    ])) {
      output.debug.mockClear();
      resetStoreForTest();
      const rendered = renderCampaignRouteVariant({
        ...context(),
        sources: { workflows: { source: 'workflows', metadata: sourceMetadata, rows: [] } }
      }, 'overview');
      rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
        detail: { parameter: 'campaign', value: 'ambient-context' }
      }));
      expect(output.debug).toHaveBeenCalledWith(
        '[cao:campaign-route-shell]',
        { event: 'route-render', tab: 'overview', status }
      );
    }
  });

  it('never logs sensitive payload content, only scalar route metadata', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=campaign-route-shell', output })
      };
    });
    vi.resetModules();
    const { renderCampaignRouteVariant } = await import('../../src/components/campaign-route-view.js');

    const rendered = renderCampaignRouteVariant(context(), 'overview');
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'campaign', value: 'ambient-context' }
    }));

    for (const call of output.debug.mock.calls) {
      const [, payload] = call;
      for (const value of Object.values(payload)) {
        expect(typeof value === 'string' || typeof value === 'number').toBe(true);
      }
      expect(JSON.stringify(payload)).not.toContain('workflows');
      expect(JSON.stringify(payload)).not.toContain('ambient-context.md');
    }
  });
});
