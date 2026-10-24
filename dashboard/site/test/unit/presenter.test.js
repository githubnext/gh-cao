// @vitest-environment jsdom
import { webcrypto } from 'node:crypto';
import { IDBFactory } from 'fake-indexeddb';
import { beforeEach, describe, expect, it, onTestFinished, vi } from 'vitest';
import { loadDatabaseQuerySources } from '../../src/data/queries/database.js';
import { DataRequestWorker } from '../data-request-worker.js';
import { renderDashboard as renderDashboardView, disposeDashboard, enableDashboardKeyboardNavigation, enableDashboardPageNavigation, dashboardPageLazySourceNames, resolveQueryDrillPageTitle } from '../../src/presenter.js';
import { processDataRequest } from '../../src/data-worker.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { TABLE_FIELDS } from '../../src/specification.js';
import { composeDashboardDocuments } from '../../../report/compose-dashboard-documents.mjs';
import { campaignDashboardSources } from '../campaign-dashboard-documents.js';
import { applyDashboardQueries } from '../workflow-inventory-query.js';
import { resolveBuiltInPages } from '../../src/dashboard-chunks.js';
import { authoritativeDashboard as builtInDashboardDocument } from '../authoritative-dashboard.js';
import { DashboardServerError } from '../../src/remote-data-backend.js';
import { resetSourceStore } from '../../src/source-store.js';
import { HORIZON_FILTER_STORAGE_KEY } from '../../src/components/filter-bar.js';

const campaignDashboardDocuments = campaignDashboardSources.map((source) => JSON.parse(source));
const authoritativeDashboardDocument = composeDashboardDocuments(
  builtInDashboardDocument,
  campaignDashboardDocuments
);

/** @param {Parameters<typeof renderDashboardView>[0]} input */
function renderDashboard(input) {
  const resolvedDocument = resolveBuiltInPages(input.document, authoritativeDashboardDocument);
  const sources = { ...input.sources };
  if (Object.keys(input.sources).length > 0) {
    sources['source-metadata'] = {
      source: 'source-metadata',
      rows: Object.keys(TABLE_FIELDS).map((source) => {
        const value = sources[source];
        const rows = Array.isArray(value?.rows) ? value.rows : [];
        return {
          source,
          'row-count': rows.length,
          ...value?.metadata,
          availability: value?.metadata?.availability ?? (value ? (rows.length > 0 ? 'available' : 'empty') : 'unavailable'),
          completeness: value?.metadata?.completeness ?? 'unknown',
          freshness: value?.metadata?.freshness ?? 'unknown'
        };
      }),
      metadata: {
        'source-id': 'source-metadata',
        'source-kind': 'fixture',
        'as-of': '',
        'retrieved-at': '',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      }
    };
  }
  const evaluatedAt = Object.values(sources)
    .flatMap((source) => [source?.metadata?.['coverage-end'], source?.metadata?.['as-of']])
    .filter((value) => typeof value === 'string' && Number.isFinite(Date.parse(value)))
    .sort()
    .at(-1);
  const range = /** @type {{ time?: { range?: unknown } }} */ (input.document.dashboard.defaults ?? {}).time?.range;
  const rangeMatch = typeof range === 'string' ? /^([1-9][0-9]*)(h|d|w)$/.exec(range) : null;
  const rangeHours = rangeMatch
    ? Number(rangeMatch[1]) * ({ h: 1, d: 24, w: 168 }[rangeMatch[2]] ?? 0)
    : 0;
  const queryContext = evaluatedAt && rangeHours > 0
    ? {
        timeWindow: {
          start: new Date(Date.parse(evaluatedAt) - rangeHours * 3_600_000).toISOString(),
          end: evaluatedAt
        }
      }
    : undefined;
  const routeParameters = Object.fromEntries(new URLSearchParams(window.location.hash.split('?')[1] ?? ''));
  const queries = /** @type {Array<Record<string, unknown>>} */ (
    /** @type {{ queries?: unknown }} */ (input.document.dashboard).queries
    ?? authoritativeDashboardDocument.dashboard.queries
  );
  const executableQueries = queries.filter((query) => typeof query.name === 'string' && !sources[query.name]);
  /** @param {import('../../src/presenter.js').PresentableBuiltInPage | import('../../src/presenter.js').PresentableCustomPage} page */
  const pagePayload = (page) => page.kind === 'built-in'
    ? {
        ...authoritativeDashboardDocument.dashboard.pages.find((/** @type {{ kind: string, page?: string }} */ candidate) => (
          candidate.kind === 'built-in' && candidate.page === page.page
        )),
        id: page.id
      }
    : page;
  for (const page of resolvedDocument.dashboard.pages) {
    const payload = pagePayload(page);
    const compiled = compileDashboardViewPayloadQueries(payload, page.id, {
      queries: executableQueries,
      evaluatedAt,
      queryContext,
      routeParameters
    });
    Object.assign(sources, processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...executableQueries, ...compiled.queries],
      sources,
      sourceNames: compiled.aliases
    }));
  }
  return renderDashboardView({ ...input, document: resolvedDocument, sources });
}

/** @param {HTMLElement} rendered @param {string} pageId */
async function activatePage(rendered, pageId) {
  const link = /** @type {HTMLAnchorElement | null} */ (rendered.querySelector(`[data-nav-page-id="${pageId}"]`));
  expect(link).not.toBeNull();
  link?.click();
  await vi.waitFor(() => {
    expect(rendered.querySelector(`[data-page-id="${pageId}"]`)?.hasAttribute('data-page-pending')).toBe(false);
  });
  rendered.ownerDocument.defaultView?.history.replaceState(null, '', '/');
  return rendered.querySelector(`[data-page-id="${pageId}"]`);
}
beforeEach(() => {
  vi.stubGlobal('Worker', DataRequestWorker);
});

describe('declarative view title visibility', () => {
  it('keeps the view and its accessible title without rendering its title heading', () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        id: 'view-titles', title: 'View titles',
        pages: [{
          id: 'overview', kind: 'custom', title: 'Overview',
          views: [
            { id: 'quiet', title: 'Quiet title', 'show-title': false, mark: 'metric',
              data: { source: 'runs' }, encoding: { value: { field: 'count', aggregate: 'count' } } },
            { id: 'visible', title: 'Visible title', mark: 'metric',
              data: { source: 'runs' }, encoding: { value: { field: 'count', aggregate: 'count' } } }
          ]
        }]
      }
    });
    const rendered = renderDashboardView({
      document,
      sources: {
        runs: {
          source: 'runs', rows: [{ count: 3 }],
          metadata: { 'source-id': 'runs', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
            availability: 'available', completeness: 'complete', freshness: 'fresh' }
        }
      }
    });
    const quiet = rendered.querySelector('[data-view-id="quiet"]');
    expect(quiet?.querySelector(':scope > :is(h2, h3, h4)')).toBeNull();
    expect(quiet?.hasAttribute('aria-labelledby')).toBe(false);
    expect(quiet?.getAttribute('aria-label')).toBe('Quiet title');
    expect(quiet?.querySelector('.metric-value')?.textContent).toBe('1');
    expect(rendered.querySelector('[data-view-id="visible"] > h2')?.textContent).toBe('Visible title');
    disposeDashboard(rendered);
  });

  it('hides a chart title even when its prompt action moves the heading', () => {
    for (const [chart, layout] of [['bar', 'full'], ['pie', 'full'], ['pie', 'horizontal'], ['bar', 'horizontal']]) {
      const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ ({
        languageVersion: '0.1.0',
        dashboard: {
          id: 'chart-titles', title: 'Chart titles',
          pages: [{
            id: 'overview', kind: 'custom', title: 'Overview',
            views: [{ id: 'quiet-chart', title: 'Quiet chart', 'show-title': false, prompt: 'always',
              mark: 'chart', chart, layout, data: { source: 'runs' },
              encoding: { x: { field: 'workflow', type: 'nominal' }, y: { field: 'count', type: 'quantitative' } } }]
          }]
        }
      });
      const rendered = renderDashboardView({
        document,
        sources: {
          runs: {
            source: 'runs', rows: [{ workflow: 'daily', count: 3 }],
            metadata: { 'source-id': 'runs', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
              availability: 'available', completeness: 'complete', freshness: 'fresh' }
          }
        }
      });
      const view = rendered.querySelector('[data-view-id="quiet-chart"]');
      expect(view?.querySelector('.chart-prompt-heading > :is(h2, h3, h4)'), `${chart} ${layout}`).toBeNull();
      expect(view?.getAttribute('aria-label'), `${chart} ${layout}`).toBe('Quiet chart');
      expect(view?.querySelector('.table-intent-button')?.getAttribute('aria-label')).toBe('Propose fix: Quiet chart');
      if (chart === 'pie') {
        expect(view?.querySelector('.pie-chart-card') !== null).toBe(layout === 'horizontal');
        expect(view?.querySelector(':scope > .pie-chart-layout') !== null).toBe(layout === 'full');
      }
      disposeDashboard(rendered);
    }
  });

  it('omits card and callout titles without omitting their prompt actions', () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        id: 'quiet-cards', title: 'Quiet cards',
        pages: [{
          id: 'overview', kind: 'custom', title: 'Overview',
          views: [
            { id: 'card', title: 'Card title', 'show-title': false, prompt: 'always',
              mark: 'metric', metric: { style: 'card' }, data: { source: 'runs' },
              encoding: { value: { field: 'count', aggregate: 'count' } } },
            { id: 'notice', title: 'Notice title', 'show-title': false, prompt: 'always',
              mark: 'callout', description: 'Read this note.' }
          ]
        }]
      }
    });
    const rendered = renderDashboardView({
      document,
      sources: {
        runs: {
          source: 'runs', rows: [{ count: 3 }],
          metadata: { 'source-id': 'runs', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
            availability: 'available', completeness: 'complete', freshness: 'fresh' }
        }
      }
    });
    const card = rendered.querySelector('[data-view-id="card"]');
    expect(card?.querySelector('.semantic-prompt-heading > :is(h2, h3, h4)')).toBeNull();
    expect(card?.getAttribute('role')).toBe('group');
    expect(card?.getAttribute('aria-label')).toBe('Card title');
    expect(card?.querySelector('.metric-card-widget')?.getAttribute('aria-label')).toBe('Card title');
    expect(card?.querySelector('.table-intent-button')).not.toBeNull();
    const notice = rendered.querySelector('[data-view-id="notice"]');
    expect(notice?.querySelector('.dashboard-callout-heading :is(h2, h3, h4)')).toBeNull();
    expect(notice?.querySelector('.dashboard-callout')?.getAttribute('aria-label')).toBe('Notice title');
    expect(notice?.querySelector('.table-intent-button')).not.toBeNull();
    disposeDashboard(rendered);
  });
});

describe('semantic view prompt action', () => {
  it.each(['auto', 'always', 'none'])('suppresses %s prompts while each view is loading', async (prompt) => {
    const metadata = /** @type {const} */ ({
      'source-id': 'runs', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
      availability: 'available', completeness: 'complete', freshness: 'fresh'
    });
    const views = [
      { id: 'chart', title: 'Chart', mark: 'chart', chart: 'bar',
        data: { source: 'chart-data' },
        encoding: { x: { field: 'workflow', type: 'nominal' }, y: { field: 'count', type: 'quantitative' } } },
      { id: 'table', title: 'Table', mark: 'table', data: { source: 'table-data' },
        encoding: { columns: [{ field: 'workflow' }] } },
      { id: 'metric', title: 'Metric', mark: 'metric', data: { source: 'metric-data' },
        encoding: { value: { field: 'count', aggregate: 'count' } } }
    ].map((view) => ({
      ...view, prompt, subject: 'Show runs', objective: 'Investigate failures', acceptance: 'Runs pass'
    }));
    const snapshots = new Map();
    const loadPageSources = /** @type {import('../../src/presenter.js').PageSourceLoader} */ (vi.fn(() => {
      throw new Error('Views must subscribe independently.');
    }));
    loadPageSources.subscribeViewSources = vi.fn((_pageId, viewId) => new Promise((resolve) => snapshots.set(viewId, resolve)));
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'loading-prompts', title: 'Loading prompts',
          pages: [{ id: 'overview', kind: 'custom', title: 'Overview', views }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(snapshots.size).toBe(views.length));
    for (const view of views) {
      const section = rendered.querySelector(`[data-view-id="${view.id}"]`);
      expect(section?.getAttribute('aria-busy')).toBe('true');
      expect(section?.querySelector('.dashboard-view-skeleton')).not.toBeNull();
      expect(section?.querySelector('.table-intent-button')).toBeNull();
    }
    for (const [index, view] of views.entries()) {
      const name = view.data.source;
      snapshots.get(view.id)({ [name]: { source: name, rows: [{ workflow: 'daily', count: 3 }], metadata } });
      await vi.waitFor(() => {
        const section = rendered.querySelector(`[data-view-id="${view.id}"]`);
        expect(section?.hasAttribute('aria-busy')).toBe(false);
        expect(section?.querySelector('.dashboard-view-skeleton')).toBeNull();
        expect(Boolean(section?.querySelector('.table-intent-button'))).toBe(prompt !== 'none');
      });
      for (const pendingView of views.slice(index + 1)) {
        expect(rendered.querySelector(`[data-view-id="${pendingView.id}"] .table-intent-button`)).toBeNull();
      }
    }
    disposeDashboard(rendered);
    rendered.remove();
  });

  it('retains the prompt control when a page-bound element settles without replacing its DOM', async () => {
    window.history.replaceState({}, '', '#page-overview?problem=runtime-failure');
    /** @type {((sources: Record<string, import('../../src/presenter.js').LogicalSourceInput>) => void) | undefined} */
    let resolveSources;
    const loadPageSources = /** @type {import('../../src/presenter.js').PageSourceLoader} */ (
      () => new Promise((resolve) => { resolveSources = resolve; })
    );
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'page-bound-prompts', title: 'Page-bound prompts',
          pages: [{
            id: 'overview', kind: 'custom', title: 'Overview',
            route: { 'hash-query-parameter': 'problem' },
            views: [{
              id: 'problem', title: 'Runtime problem', prompt: 'always',
              mark: 'element', element: 'problem-detail', data: { sources: ['problem-data'] }
            }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(resolveSources).toBeTypeOf('function'));
    const view = rendered.querySelector('[data-view-id="problem"]');
    const prompt = view?.querySelector('.semantic-prompt-action');
    expect(prompt).not.toBeNull();
    expect(view?.querySelector('.problem-detail')?.getAttribute('aria-busy')).toBe('true');
    if (!resolveSources) throw new Error('Page source loader did not initialize.');
    resolveSources({
      'problem-data': {
        source: 'problem-data', rows: [{ 'problem-title': 'Runtime failure', 'status-detail': 'Needs investigation' }],
        metadata: {
          'source-id': 'problem-data', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
          availability: 'available', completeness: 'complete', freshness: 'fresh'
        }
      }
    });
    await vi.waitFor(() => expect(view?.querySelector('.problem-detail')?.getAttribute('aria-busy')).toBe('false'));
    expect(rendered.querySelector('[data-view-id="problem"]')).toBe(view);
    expect(view?.querySelector('.semantic-prompt-action')).toBe(prompt);
    disposeDashboard(rendered);
    rendered.remove();
    window.history.replaceState({}, '', '/');
  });

  it('automatically exposes the shared preview on a chart with composed semantics', () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        id: 'semantic-dashboard',
        title: 'Semantic dashboard',
        queries: [
          { name: 'health', from: 'runs', subject: 'Show health', objective: 'Investigate failures', acceptance: 'Runs pass' }
        ],
        pages: [{
          id: 'overview', kind: 'custom', title: 'Overview',
          views: [{
            id: 'health-chart', title: 'Health chart', mark: 'chart', chart: 'bar',
            data: { source: 'health' },
            subject: 'Compare workflow health',
            encoding: { x: { field: 'workflow', type: 'nominal' }, y: { field: 'count', type: 'quantitative' } }
          }]
        }]
      }
    });
    const rendered = renderDashboardView({
      document,
      sources: {
        health: {
          source: 'health',
          rows: [{ workflow: 'daily', count: 3 }],
          metadata: { 'source-id': 'health', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
            availability: 'available', completeness: 'complete', freshness: 'fresh' }
        }
      }
    });
    const button = /** @type {HTMLButtonElement | null} */ (rendered.querySelector('[data-view-id="health-chart"] .table-intent-button'));
    expect(button?.getAttribute('aria-label')).toBe('Propose fix: Health chart');
    const heading = rendered.querySelector('[data-view-id="health-chart"] .chart-prompt-heading');
    expect(heading?.querySelector('h2, h3, h4')?.textContent).toBe('Health chart');
    expect(heading?.querySelector('.chart-prompt-action .table-intent-button')).toBe(button);
    expect(button?.querySelector('.octicon-git-pull-request')).not.toBeNull();
    button?.click();
    const preview = rendered.querySelector('.table-intent-preview')?.textContent ?? '';
    expect(preview).toContain('Subject:\nShow health\n\nCompare workflow health');
    expect(preview).toContain('Investigate failures');
    expect(preview).toContain('Runs pass');
    expect(preview).toContain('Named CAO query IDs: health');
    expect(preview).toContain('"workflow": "daily"');
    disposeDashboard(rendered);
  });

  it('applies prompt modes to chart types without changing the composed prompt', () => {
    for (const chart of ['bar', 'pie']) {
      for (const [prompt, annotated, expected] of [
        [undefined, true, true],
        ['auto', false, false],
        ['none', true, false],
        ['always', false, true]
      ]) {
        const view = {
          id: 'test-chart', title: 'Test chart', mark: 'chart', chart,
          data: { source: 'runs' },
          encoding: { x: { field: 'workflow', type: 'nominal' }, y: { field: 'count', type: 'quantitative' } },
          ...(prompt === undefined ? {} : { prompt })
        };
        const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ ({
          languageVersion: '0.1.0',
          dashboard: {
            id: 'prompt-modes', title: 'Prompt modes',
            queries: annotated ? [{ name: 'runs', subject: 'Show runs', objective: 'Investigate', acceptance: 'Verified' }] : [],
            pages: [{ id: 'overview', kind: 'custom', title: 'Overview', views: [view] }]
          }
        });
        const rendered = renderDashboardView({ document, sources: {} });
        const button = rendered.querySelector('[data-view-id="test-chart"] .table-intent-button');
        expect(Boolean(button), `${chart} ${prompt ?? 'default'} annotated=${annotated}`).toBe(expected);
        if (expected) {
          expect(button?.getAttribute('aria-label')).toBe('Propose fix: Test chart');
          expect(rendered.querySelector('[data-view-id="test-chart"] .chart-prompt-heading > .chart-prompt-action .table-intent-button')).toBe(button);
        }
        if (prompt === 'always') {
          button?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
          expect(rendered.querySelector('.table-intent-preview')?.textContent).toContain('View: test-chart');
        }
        disposeDashboard(rendered);
      }
    }
  });

  it('preserves the existing heading of an annotated callout', () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        id: 'semantic-callout',
        title: 'Semantic callout',
        pages: [{
          id: 'overview', kind: 'custom', title: 'Overview',
          views: [{
            id: 'alert', title: 'Review required', description: 'Investigate the alert.',
            subject: 'Show an alert.', objective: 'Investigate it.', acceptance: 'Alert resolved.',
            data: { source: 'runs' }, mark: 'callout', callout: { label: 'Alert' }
          }]
        }]
      }
    });
    const rendered = renderDashboardView({ document, sources: {} });
    const callout = rendered.querySelector('[data-view-id="alert"]');
    expect(callout?.querySelectorAll('.dashboard-callout-heading :is(h2, h3, h4)')).toHaveLength(1);
    expect(callout?.querySelector('aside')?.getAttribute('aria-labelledby')).toBe('overview-alert-callout-heading');
    const prompt = callout?.querySelector('.semantic-prompt-action .table-intent-button');
    expect(prompt?.getAttribute('aria-label')).toBe('Propose fix: Review required');
    expect(prompt?.querySelector('span')?.textContent).toBe('Propose fix');
    disposeDashboard(rendered);
  });
});

describe('dashboard DOM provenance', () => {

  it('renders independently bound elements without registering a page-wide query', () => {
    const loadPageSources = vi.fn(() => new Promise(() => {}));
    const rendered = renderDashboardView({
      document: authoritativeDashboardDocument,
      sources: {},
      loadPageSources
    });
    const overviewBefore = rendered.querySelector('[data-page-id="overview"]');
    expect(overviewBefore?.querySelector('.factory-intro')).not.toBeNull();

    expect(rendered.querySelector('[data-page-id="overview"]')).toBe(overviewBefore);
    const floor = rendered.querySelector('[data-view-id="overview-floor"]');
    expect(floor?.classList.contains('factory-floor')).toBe(true);
    expect(floor?.querySelector('.semantic-prompt-heading')).toBeNull();
    expect(floor?.querySelector('.semantic-prompt-action')).toBeNull();
    expect(rendered.querySelector('[data-view-id="overview-campaigns"]')).toBeNull();
    expect(loadPageSources).not.toHaveBeenCalledWith('maintenance', expect.anything());
    disposeDashboard(rendered);
  });

  it('subscribes navigation indicators to their page sources', async () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ (/** @type {unknown} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        title: 'Indicator dashboard',
        pages: [
          { id: 'overview', kind: 'custom', title: 'Overview', views: [] },
          {
            id: 'maintenance',
            kind: 'custom',
            title: 'Updates',
            views: [],
            'navigation-indicator': {
              label: 'updates available',
              any: ['maintenance-campaign-updates']
            }
          }
        ],
        navigation: [
          { pages: ['overview'] },
          { label: 'Updates', placement: 'bottom', pages: ['maintenance'] }
        ]
      }
    }));
    const metadata = /** @type {const} */ ({
      'source-id': 'campaigns',
      'source-kind': 'fixture',
      'as-of': '',
      'retrieved-at': '',
      completeness: 'complete',
      freshness: 'fresh',
      availability: 'available'
    });
    /** @type {string[]} */
    const subscriptionOrder = [];
    const loadSources = vi.fn((sourceNames) => {
      subscriptionOrder.push('background');
      expect(sourceNames).toEqual(['maintenance-campaign-updates']);
      return Promise.resolve(/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ ({
        'maintenance-campaign-updates': {
          source: 'maintenance-campaign-updates',
          rows: [{ 'campaign-update-state': 'update-available' }],
          metadata
        }
      }));
    });
    const loadPageSources = /** @type {import('../../src/presenter.js').PageSourceLoader} */ (vi.fn((pageId) => {
      subscriptionOrder.push(`page:${pageId}`);
      const result = pageId === 'maintenance'
        ? { campaigns: { source: 'campaigns', rows: [{ 'campaign-update-state': 'update-available' }], metadata } }
        : {};
      return Promise.resolve(/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (result));
    }));
    loadPageSources.subscribeBackgroundSources = loadSources;

    const rendered = renderDashboardView({ document, sources: {}, loadPageSources });

    expect(subscriptionOrder.slice(0, 2)).toEqual(['page:overview', 'background']);
    expect(rendered.querySelector('[data-nav-page-id="maintenance"]')?.getAttribute('aria-label'))
      .toBe('Updates');
    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-nav-page-id="maintenance"]')?.getAttribute('aria-label'))
        .toBe('Updates, updates available');
    });
    expect(loadPageSources).not.toHaveBeenCalledWith('maintenance', expect.anything());
    expect(loadSources).toHaveBeenCalledWith(['maintenance-campaign-updates'], expect.objectContaining({
      signal: expect.any(AbortSignal),
      onUpdate: expect.any(Function)
    }));
    disposeDashboard(rendered);
  });

  it('loads a page chunk before mounting its independently bound elements', async () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ (/** @type {unknown} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        id: 'chunked-binding-dashboard',
        title: 'Chunked binding dashboard',
        pages: [{
          id: 'overview',
          kind: /** @type {'custom'} */ ('custom'),
          title: 'Overview',
          chunk: 'dashboard-pages/overview.json',
          'independent-source-bindings': true
        }]
      }
    }));
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (
      () => new Promise(() => {})
    );
    loadPageSources.prepare = async () => {
      Object.assign(document.dashboard.pages[0], {
        views: [{
          id: 'overview-header',
          title: 'Overview header',
          data: {
            sources: [
              'overview-outcome-summary',
              'overview-run-summary',
              'overview-factory-status',
              'overview-rhythm'
            ]
          },
          mark: 'element',
          element: 'factory-header'
        }]
      });
    };

    const rendered = renderDashboardView({ document, sources: {}, loadPageSources });

    await vi.waitFor(() => {
      expect(rendered.querySelector('.factory-intro')).not.toBeNull();
    });
    expect(rendered.querySelector('[data-page-id="overview"]')?.getAttribute('aria-busy')).not.toBe('true');
    disposeDashboard(rendered);
  });

  it('does not mount or query a prepared page after its navigation lifetime ends', async () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ (/** @type {unknown} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        id: 'cancelled-chunk-dashboard',
        title: 'Cancelled chunk dashboard',
        pages: [{
          id: 'overview',
          kind: 'custom',
          title: 'Overview',
          chunk: 'dashboard-pages/overview.json',
          'independent-source-bindings': true
        }]
      }
    }));
    let finishPreparation = () => {};
    const loadPageSources = vi.fn(
      /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (
        () => new Promise(() => {})
      )
    );
    loadPageSources.prepare = () => new Promise((resolve) => {
      finishPreparation = () => {
        Object.assign(document.dashboard.pages[0], {
          views: [{
            id: 'overview-header',
            data: {
              sources: [
                'overview-outcome-summary',
                'overview-run-summary',
                'overview-factory-status',
                'overview-rhythm'
              ]
            },
            mark: 'element',
            element: 'factory-header'
          }]
        });
        resolve();
      };
    });

    const rendered = renderDashboardView({ document, sources: {}, loadPageSources });
    disposeDashboard(rendered);
    finishPreparation();
    await Promise.resolve();
    await Promise.resolve();

    expect(loadPageSources).not.toHaveBeenCalled();
    expect(rendered.querySelector('.factory-intro')).toBeNull();
  });

  it('renders independently bound elements while an ordinary view awaits its source', async () => {
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'mixed-binding-dashboard',
          title: 'Mixed binding dashboard',
          pages: [{
            id: 'overview',
            kind: 'custom',
            title: 'Overview',
            views: [
              {
                id: 'overview-header',
                title: 'Overview header',
                data: { sources: ['overview-run-summary'] },
                mark: 'element',
                element: 'factory-header'
              },
              {
                id: 'runs-table',
                title: 'Runs',
                data: { source: 'runs' },
                mark: 'table',
                encoding: { columns: [{ field: 'run' }] }
              }
            ]
          }]
        }
      },
      sources: {},
      loadPageSources: () => new Promise(() => {})
    });

    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-page-id="overview"] .factory-intro')).not.toBeNull();
    });
    expect(rendered.querySelector('[data-view-id="runs-table"] .dashboard-view-skeleton')).not.toBeNull();
    expect(rendered.querySelector('[data-page-id="overview"]')?.getAttribute('aria-busy')).not.toBe('true');
    disposeDashboard(rendered);
  });

  it('renders sections before their count sources arrive', async () => {
    let deliverCount = () => {};
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (
      () => new Promise(() => {})
    );
    loadPageSources.subscribeViewSources = (_pageId, _viewId, _sourceNames) => new Promise((resolve) => {
      deliverCount = () => resolve({
        'overview-count': {
          source: 'overview-count',
          rows: [{ count: 1 }, { count: 2 }],
          metadata: { 'source-id': 'overview-count', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '', availability: 'available', completeness: 'complete', freshness: 'fresh' }
        }
      });
    });
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'section-count-dashboard',
          title: 'Section count dashboard',
          pages: [{
            id: 'overview',
            kind: 'custom',
            title: 'Overview',
            views: [{
              id: 'overview-header',
              title: 'Overview header',
              data: { sources: ['overview-run-summary'] },
              mark: 'element',
              element: 'factory-header'
            }],
            sections: [{
              id: 'overview-section',
              title: 'Overview',
              layout: 'full',
              views: ['overview-header'],
              'count-source': 'overview-count',
              'count-label': 'items'
            }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });

    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-page-id="overview"] .layout-section')).not.toBeNull();
    });
    const intro = rendered.querySelector('.factory-intro');
    expect(intro).not.toBeNull();
    await vi.waitFor(() => expect(deliverCount).not.toBeNull());
    deliverCount();
    await vi.waitFor(() => expect(rendered.querySelector('[data-section-id="overview-section"] .layout-section-header')?.textContent).toContain('2 items'));
    expect(rendered.querySelector('.factory-intro')).toBe(intro);
    disposeDashboard(rendered);
  });

  it('binds each view independently and retains unaffected view nodes', async () => {
    const callbacks = new Map();
    const resolveSnapshots = new Map();
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (vi.fn(() => {
      throw new Error('Page-wide subscriptions must not be used.');
    }));
    loadPageSources.subscribeViewSources = vi.fn((pageId, viewId, names, options) => {
      callbacks.set(viewId, options.onUpdate);
      return new Promise((resolve) => resolveSnapshots.set(viewId, resolve));
    });
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'view-binding-dashboard',
          title: 'View bindings',
          pages: [{
            id: 'overview',
            kind: 'custom',
            title: 'Overview',
            views: [
              { id: 'first', title: 'First', mark: 'table', data: { source: 'first-data' }, encoding: { columns: [{ field: 'name' }] } },
              { id: 'second', title: 'Second', mark: 'table', data: { source: 'second-data' }, encoding: { columns: [{ field: 'name' }] } }
            ]
          }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(resolveSnapshots.size).toBe(2));
    expect(loadPageSources).not.toHaveBeenCalled();
    expect(rendered.querySelector('[data-view-id="first"] .dashboard-view-skeleton')).not.toBeNull();
    expect(rendered.querySelector('[data-view-id="second"] .dashboard-view-skeleton')).not.toBeNull();
    expect(rendered.querySelector('[data-view-id="first"]')?.getAttribute('aria-busy')).toBe('true');

    /** @param {string} name @param {string} value */
    const source = (name, value) => ({
      source: name,
      rows: [{ name: value }],
      metadata: { availability: 'available', completeness: 'complete', freshness: 'fresh' }
    });
    resolveSnapshots.get('first')({ 'first-data': source('first-data', 'one') });
    await vi.waitFor(() => expect(rendered.querySelector('[data-view-id="first"]')?.textContent).toContain('one'));
    const firstView = rendered.querySelector('[data-view-id="first"]');
    expect(firstView?.hasAttribute('aria-busy')).toBe(false);
    expect(rendered.querySelector('[data-view-id="second"] .dashboard-view-skeleton')).not.toBeNull();

    resolveSnapshots.get('second')({ 'second-data': source('second-data', 'two') });
    await vi.waitFor(() => expect(rendered.querySelector('[data-view-id="second"]')?.textContent).toContain('two'));
    expect(rendered.querySelector('[data-view-id="first"]')).toBe(firstView);
    callbacks.get('second')({ 'second-data': source('second-data', 'updated') });
    expect(rendered.querySelector('[data-view-id="second"]')?.textContent).toContain('updated');
    expect(rendered.querySelector('[data-view-id="first"]')).toBe(firstView);
    disposeDashboard(rendered);
    callbacks.get('second')({ 'second-data': source('second-data', 'stale') });
    expect(rendered.textContent).not.toContain('stale');
    rendered.remove();
  });

  it('keeps an opted-in page on one abort-scoped subscription and publishes refreshes to all its views', async () => {
    const metadata = /** @type {const} */ ({
      'source-id': 'shared', 'source-kind': 'fixture',
      'as-of': '2026-10-19T00:00:00Z', 'retrieved-at': '2026-10-19T00:00:00Z',
      availability: 'available', completeness: 'complete', freshness: 'fresh'
    });
    /** @param {number} value */
    const snapshot = (value) => ({
      shared: { source: 'shared', metadata, rows: [{ value }] }
    });
    /** @type {import('../../src/presenter.js').PageSourceLoadOptions | undefined} */
    let owner;
    const loadPageSources = /** @type {import('../../src/presenter.js').PageSourceLoader} */ (vi.fn((_id, options) => {
      owner = options;
      return Promise.resolve(snapshot(1));
    }));
    loadPageSources.subscribeViewSources = vi.fn(() => {
      throw new Error('Page-bound views must not subscribe independently.');
    });
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'shared-bindings', title: 'Shared bindings',
          pages: [{
            id: 'overview', kind: 'custom', title: 'Overview', 'query-binding': 'page',
            views: ['first', 'second'].map(id => ({
              id, title: id, mark: 'metric', data: { source: 'shared' },
              encoding: { value: { field: 'value', aggregate: 'max' } }
            }))
          }]
        }
      },
      sources: {}, loadPageSources
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelectorAll('[data-metric-value="value"]')).toHaveLength(2));
    expect(loadPageSources).toHaveBeenCalledTimes(1);
    expect(loadPageSources.subscribeViewSources).not.toHaveBeenCalled();
    expect(owner?.signal.aborted).toBe(false);
    owner?.onUpdate(snapshot(2));
    expect([...rendered.querySelectorAll('[data-metric-value="value"]')].map(node => node.textContent)).toEqual(['2', '2']);
    disposeDashboard(rendered);
    expect(owner?.signal.aborted).toBe(true);
    owner?.onUpdate(snapshot(3));
    expect([...rendered.querySelectorAll('[data-metric-value="value"]')].map(node => node.textContent)).toEqual(['2', '2']);
    rendered.remove();
  });

  it('shows page-bound query failures instead of leaving loading skeletons', async () => {
    const loadPageSources = /** @type {import('../../src/presenter.js').PageSourceLoader} */ (vi.fn(() => Promise.reject(new Error('Query unavailable.'))));
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'failed-bindings', title: 'Failed bindings',
          pages: [{
            id: 'overview', kind: 'custom', title: 'Overview', 'query-binding': 'page',
            views: [{ id: 'summary', title: 'Summary', mark: 'metric', data: { source: 'shared' },
              encoding: { value: { field: 'value', aggregate: 'max' } } }]
          }]
        }
      },
      sources: {}, loadPageSources
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelector('[data-view-id="summary"]')?.textContent).toContain('Query unavailable.'));
    expect(rendered.querySelector('.dashboard-view-skeleton')).toBeNull();
    disposeDashboard(rendered);
    rendered.remove();
  });

  it('keeps the campaign shell live when an independent tab count times out', async () => {
    resetSourceStore();
    window.history.replaceState({}, '', '#page-campaign-insights?campaign=ambient-context');
    const sourceNames = [
      'workflows', 'campaign-insight-tab-counts', 'campaign-problem-tab-counts', 'campaign-issue-tab-counts'
    ];
    const requests = new Map();
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (vi.fn(() => {
      throw new Error('The campaign shell must not start a page-wide query.');
    }));
    loadPageSources.subscribeViewSources = vi.fn((_pageId, viewId, names, options) => new Promise((resolve, reject) => {
      requests.set(names[0], { viewId, names, options, resolve, reject });
    }));
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'campaign-navigation', title: 'Campaign navigation',
          pages: [{
            id: 'campaign-insights', kind: 'custom', title: 'Campaign',
            route: { 'hash-query-parameter': 'campaign' },
            views: [{
              id: 'campaign-insights-navigation', title: 'Campaign insights',
              mark: 'element', element: 'campaign-route', config: { body: 'insights' },
              data: { sources: sourceNames, arguments: [{ name: 'campaign', field: 'campaign' }] }
            }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    const metadata = /** @type {const} */ ({
      availability: 'available', completeness: 'complete', freshness: 'fresh'
    });
    /** @param {string} name @param {Array<Record<string, unknown>>} rows */
    const deliver = (name, rows) => {
      const request = requests.get(name);
      const alias = `view:campaign-insights:campaign-insights-navigation:${name}${sourceNames.indexOf(name) ? `-${sourceNames.indexOf(name) + 1}` : ''}`;
      request.resolve({ [alias]: { source: name, rows, metadata } });
      return request;
    };
    try {
      await vi.waitFor(() => expect(requests.size).toBe(4));
      expect([...requests.values()].map(({ viewId, names }) => [viewId, names])).toEqual(
        sourceNames.map((name) => ['campaign-insights-navigation', [name]])
      );
      const workflows = deliver('workflows', [{
        campaign: 'ambient-context', 'campaign-name': 'Ambient Context', 'workflow-role': 'orchestrator'
      }]);
      await vi.waitFor(() => expect(rendered.querySelector('.campaign-tabs [aria-current="page"]')?.textContent).toBe('Insights'));
      const shell = rendered.querySelector('[data-view-id="campaign-insights-navigation"]');
      expect(shell?.textContent).not.toContain('This view cannot be shown');
      requests.get('campaign-problem-tab-counts').reject(new Error('dashboard queries exceeded the max-duration-ms limit of 60000'));
      await vi.waitFor(() => expect([...rendered.querySelectorAll('.campaign-tabs a')]
        .some((link) => link.textContent?.includes('Failures (count unavailable)'))).toBe(true));
      deliver('campaign-insight-tab-counts', [{ campaign: 'ambient-context', items: 2 }]);
      deliver('campaign-issue-tab-counts', [{ campaign: 'ambient-context', items: 1 }]);
      await vi.waitFor(() => expect([...rendered.querySelectorAll('.campaign-tabs .count-badge')]
        .map((badge) => badge.textContent)).toEqual(['2', '1']));
      expect(rendered.querySelector('[data-view-id="campaign-insights-navigation"]')).toBe(shell);
      workflows.options.onUpdate({
        'view:campaign-insights:campaign-insights-navigation:workflows': {
          source: 'workflows', metadata, rows: [{ campaign: 'ambient-context', 'campaign-name': 'Updated Campaign' }]
        }
      });
      await vi.waitFor(() => expect(rendered.querySelector('.campaign-tabs')?.getAttribute('aria-label')).toContain('Updated Campaign'));
      expect(shell?.textContent).not.toContain('This view cannot be shown');
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
      resetSourceStore();
      window.history.replaceState({}, '', '/');
    }
  });

  it('preserves an unavailable campaign shell when its workflow subscription fails', async () => {
    resetSourceStore();
    window.history.replaceState({}, '', '#page-campaign-workflows?campaign=ambient-context');
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (vi.fn(() => {
      throw new Error('The campaign shell must not start a page-wide query.');
    }));
    loadPageSources.subscribeViewSources = vi.fn(() => Promise.reject(new Error('dashboard queries exceeded the max-duration-ms limit of 60000')));
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'unavailable-campaign', title: 'Unavailable campaign',
          pages: [{
            id: 'campaign-workflows', kind: 'custom', title: 'Campaign',
            route: { 'hash-query-parameter': 'campaign' },
            views: [{
              id: 'campaign-workflow-navigation', title: 'Campaign workflows',
              mark: 'element', element: 'campaign-route', config: { body: 'workflows' },
              data: { sources: ['workflows'] }
            }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    try {
      await vi.waitFor(() => expect(rendered.querySelector('.campaign-workflows')?.textContent).toContain('Campaign data is unavailable.'));
      expect(rendered.querySelector('.campaign-workflows')?.textContent).not.toContain('Campaign not found.');
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
      resetSourceStore();
      window.history.replaceState({}, '', '/');
    }
  });

  it('updates nested section views from separate queries without replacing sibling UX', async () => {
    const requests = new Map();
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (vi.fn(() => {
      throw new Error('Nested views must not use the page-wide subscription.');
    }));
    loadPageSources.subscribeViewSources = vi.fn((_pageId, viewId, names, options) => {
      requests.set(viewId, { names, options });
      return new Promise(() => {});
    });
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'nested-query-bindings',
          title: 'Nested query bindings',
          queries: [
            { name: 'run-counts', from: 'runs', aggregate: { values: [{ field: 'name', as: 'count', reducer: 'count' }] } }
          ],
          pages: [{
            id: 'overview', kind: 'custom', title: 'Overview',
            views: [
              { id: 'successes', title: 'Successes', mark: 'table', data: { source: 'runs', filters: { conclusion: 'success' } }, encoding: { columns: [{ field: 'name' }] } },
              { id: 'failures', title: 'Failures', mark: 'table', disclosure: 'supplemental', data: { source: 'runs', filters: { conclusion: 'failure' } }, encoding: { columns: [{ field: 'name' }] } }
            ],
            sections: [{
              id: 'results', title: 'Results', layout: 'full',
              views: ['successes', 'failures'],
              'count-source': 'run-counts', 'count-field': 'count', 'count-label': 'runs'
            }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    try {
      await vi.waitFor(() => expect(requests.size).toBe(3));
      expect(loadPageSources).not.toHaveBeenCalled();
      expect(requests.get('successes').names).toEqual(['runs']);
      expect(requests.get('failures').names).toEqual(['runs']);
      expect(requests.get('page-chrome').names).toEqual(['run-counts']);
      const section = rendered.querySelector('[data-section-id="results"]');
      const disclosure = section?.querySelector('.view-disclosure');
      expect(section?.querySelector('[data-view-id="successes"] .dashboard-view-skeleton')).not.toBeNull();
      expect(disclosure?.querySelector('.dashboard-view-skeleton')).not.toBeNull();
      /** @param {string} name @param {Array<Record<string, unknown>>} rows */
      const source = (name, rows) => ({
        source: name, rows,
        metadata: { availability: 'available', completeness: 'complete', freshness: 'fresh' }
      });

      requests.get('failures').options.onUpdate({
        'view:overview:failures:runs': source('runs', [{ name: 'failed run' }])
      });
      await vi.waitFor(() => expect(disclosure?.textContent).toContain('failed run'));
      expect(section?.querySelector('[data-view-id="successes"] .dashboard-view-skeleton')).not.toBeNull();
      expect(section?.querySelector('.view-disclosure')).toBe(disclosure);
      const failuresContent = disclosure?.querySelector(':scope > :not(summary)');

      requests.get('page-chrome').options.onUpdate({
        'run-counts': source('run-counts', [{ count: 2 }])
      });
      await vi.waitFor(() => expect(section?.querySelector('.layout-section-header')?.textContent).toContain('2 runs'));
      expect(disclosure?.querySelector(':scope > :not(summary)')).toBe(failuresContent);
      const sectionHeader = section?.querySelector('.layout-section-header');

      requests.get('successes').options.onUpdate({
        'view:overview:successes:runs': source('runs', [{ name: 'successful run' }])
      });
      await vi.waitFor(() => expect(section?.querySelector('[data-view-id="successes"]')?.textContent).toContain('successful run'));
      expect(section?.querySelector('.layout-section-header')).toBe(sectionHeader);
      expect(section?.querySelector('.view-disclosure')).toBe(disclosure);
      expect(disclosure?.querySelector(':scope > :not(summary)')).toBe(failuresContent);
      expect(section?.querySelectorAll('.view-disclosure')).toHaveLength(1);
      expect(section?.querySelector('[data-view-id="successes"]')?.textContent).not.toContain('failed run');
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
    }
  });

  it('keeps a source-bound callout as the section root while its source loads', async () => {
    let update = () => {};
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (
      () => new Promise(() => {})
    );
    loadPageSources.subscribeViewSources = (_pageId, _viewId, _names, options) => {
      update = () => options.onUpdate({
        runs: {
          source: 'runs', rows: [{ name: 'A run' }],
          metadata: { 'source-id': 'runs', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '', availability: 'available', completeness: 'complete', freshness: 'fresh' }
        }
      });
      return new Promise(() => {});
    };
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'callout-query-bindings', title: 'Callout query bindings',
          pages: [{
            id: 'overview', kind: 'custom', title: 'Overview',
            views: [{ id: 'notice', title: 'Notice', mark: 'callout', data: { source: 'runs' }, callout: { label: 'Note' } }],
            sections: [{ id: 'notice-section', title: 'Notice', layout: 'full', views: ['notice'] }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    try {
      await vi.waitFor(() => expect(rendered.querySelector('[data-section-id="notice-section"]')).not.toBeNull());
      expect(rendered.querySelector('[data-section-id="notice-section"]')?.classList.contains('dashboard-callout')).toBe(true);
      expect(rendered.querySelector('[data-section-id="notice-section"] .dashboard-view-skeleton')).toBeNull();
      update();
      await vi.waitFor(() => expect(rendered.querySelector('[data-section-id="notice-section"]')?.classList.contains('dashboard-callout')).toBe(true));
      expect(rendered.querySelector('[data-section-id="notice-section"]')?.classList.contains('layout-section')).toBe(false);
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
    }
  });

  it('updates a supplemental view inside its existing disclosure', async () => {
    let update = () => {};
    const loadPageSources = /** @type {NonNullable<Parameters<typeof renderDashboardView>[0]['loadPageSources']>} */ (
      () => new Promise(() => {})
    );
    loadPageSources.subscribeViewSources = (_pageId, _viewId, _sourceNames, options) => {
      update = () => options.onUpdate({
        'more-data': {
          source: 'more-data',
          rows: [{ name: 'Updated' }],
          metadata: { 'source-id': 'more-data', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '', availability: 'available', completeness: 'complete', freshness: 'fresh' }
        }
      });
      return new Promise(() => {});
    };
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'disclosure-bindings',
          title: 'Disclosure bindings',
          pages: [{
            id: 'overview', kind: 'custom', title: 'Overview',
            views: [{
              id: 'more', title: 'More', mark: 'table', disclosure: 'supplemental',
              data: { source: 'more-data' }, encoding: { columns: [{ field: 'name' }] }
            }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelector('.view-disclosure')).not.toBeNull());
    const disclosure = /** @type {HTMLDetailsElement} */ (rendered.querySelector('.view-disclosure'));
    disclosure.open = true;
    update();
    await vi.waitFor(() => expect(disclosure.textContent).toContain('Updated'));
    expect(rendered.querySelectorAll('.view-disclosure')).toHaveLength(1);
    expect(disclosure.querySelector('details')).toBeNull();
    expect(disclosure.open).toBe(true);
    disposeDashboard(rendered);
    rendered.remove();
  });

  it('requests a refresh after pulling down from the top of Overview', () => {
    const rendered = renderDashboardView({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    document.body.replaceChildren(rendered);
    const scroller = rendered.querySelector('main.dashboard-prototype');
    const onRefresh = vi.fn();
    window.addEventListener('dashboard-refresh-request', onRefresh);
    /** @param {string} type @param {number} clientY */
    const touch = (type, clientY) => {
      const event = new Event(type, { bubbles: true });
      Object.defineProperty(event, 'touches', {
        value: type === 'touchend' ? [] : [{ clientY }]
      });
      scroller?.dispatchEvent(event);
    };

    touch('touchstart', 100);
    touch('touchmove', 180);
    expect(rendered.querySelector('.overview-pull-refresh')?.textContent).toBe('Release to refresh');
    touch('touchend', 180);

    expect(onRefresh).toHaveBeenCalledOnce();
    window.removeEventListener('dashboard-refresh-request', onRefresh);
    disposeDashboard(rendered);
  });

  it('shows the mode badge only for a page declaring mode-indicator: true', async () => {
    const rendered = renderDashboardView({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    document.body.replaceChildren(rendered);
    try {
      window.location.hash = '#page-overview';
      window.history.replaceState(null, '', `${window.location.pathname}?mode=review${window.location.hash}`);
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      await vi.waitFor(() => {
        expect(rendered.querySelector('[data-page-id="overview"]')?.hasAttribute('hidden')).toBe(false);
      });
      expect(rendered.querySelector('[data-page-mode]')?.hasAttribute('hidden')).toBe(true);

      window.location.hash = '#page-campaigns';
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      await vi.waitFor(() => {
        expect(rendered.querySelector('[data-page-id="campaigns"]')?.hasAttribute('hidden')).toBe(false);
      });
      const pageMode = rendered.querySelector('[data-page-mode]');
      expect(pageMode?.hasAttribute('hidden')).toBe(false);
      expect(pageMode?.textContent).toBe('Review');
    } finally {
      disposeDashboard(rendered);
      window.history.replaceState(null, '', '/');
    }
  });

  it('reports the paginated source shared by the runs page views', () => {
    const lazySourceNames = dashboardPageLazySourceNames(authoritativeDashboardDocument, 'runs');
    expect(lazySourceNames).toContain('runs-table');
  });

  it('selects a route drilldown parent when its route has a value', async () => {
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="overview" href="#page-overview">Overview</a>
      <a data-nav-page-id="repositories" href="#page-repositories">Repositories</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-overview" data-page-id="overview"></section>
        <section class="dashboard-page" id="page-repositories" data-page-id="repositories"></section>
        <section class="dashboard-page" id="page-repository-detail" data-page-id="repository-detail" data-route-navigation-page="repositories" data-route-parameter="repository"></section>
      </main>
    `;
    document.body.append(root);
    window.history.replaceState(null, '', '/#page-repository-detail?repository=octo-org%2Fplatform');
    try {
      const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', () => null, 'overview');
      await vi.waitFor(() => {
        expect(root.querySelector('[data-nav-page-id="repositories"]')?.getAttribute('aria-current')).toBe('page');
      });
      disposeNavigation();
    } finally {
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('keeps Settings useful while the policy source is unavailable', async () => {
    const rendered = renderDashboardView({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    const page = await activatePage(rendered, 'configuration');

    await vi.waitFor(() => expect(page?.querySelector('.configuration-view')).not.toBeNull());
    expect(page?.textContent).toContain('The policy cannot be edited until it contains valid JSON.');
    expect(page?.textContent).not.toContain('Affected source: configuration-policy');
  });

  it('maps every rendered element and dynamic descendant to its owning JSON view when ?debug=1 is set', async () => {
    window.history.pushState(null, '', '?debug=1');
    try {
      const rendered = renderDashboard({
        document: {
          languageVersion: '0.1.0',
          dashboard: {
            id: 'provenance-dashboard',
            title: 'Provenance dashboard',
            pages: [{
              id: 'trace',
              kind: 'custom',
              title: 'Trace',
              views: [
                {
                  id: 'summary',
                  title: 'Summary',
                  mark: 'element',
                  element: 'link-button-list',
                  config: { 'label-field': 'metric-name', 'link-field': 'metric-link' },
                  data: { source: 'summary' }
                },
                {
                  id: 'total',
                  title: 'Total',
                  mark: 'metric',
                  data: { source: 'summary' },
                  encoding: { value: { field: 'value', aggregate: 'sum' } }
                }
              ],
              sections: [{
                id: 'main',
                title: 'Main',
                layout: 'full',
                views: ['summary', 'total']
              }]
            }]
          }
        },
        sources: {
          summary: {
            source: 'summary',
            rows: [{
              'metric-kind': 'primary',
              'metric-name': 'runs',
              'metric-link': { href: '#page-trace', label: 'View runs' },
              points: [{ x: '2026-09-07T18:00:00Z', y: 2, color: 'Runs', key: 'runs-1' }]
            }],
            metadata: {
              'source-id': 'summary-fixture',
              'source-kind': 'fixture',
              'as-of': '2026-09-07T18:00:00Z',
              'retrieved-at': '2026-09-07T18:01:00Z',
              completeness: 'complete',
              freshness: 'fresh',
              availability: 'available'
            }
          }
        }
      });

      const page = rendered.querySelector('[data-page-id="trace"]');
      const section = page?.querySelector('[data-section-id="main"]');
      const summary = page?.querySelector('[data-view-id="summary"]');
      const metric = page?.querySelector('[data-view-id="total"]');
      await vi.waitFor(() => {
        expect(rendered.getAttribute('data-json-path')).toBe('$.dashboard');
      });
      expect(rendered.querySelector('[data-nav-page-id="trace"]')?.getAttribute('data-json-path')).toBe('$.dashboard.pages[0]');
      expect(page?.getAttribute('data-json-path')).toBe('$.dashboard.pages[0]');
      expect(section?.getAttribute('data-json-path')).toBe('$.dashboard.pages[0].sections[0]');
      expect(summary?.getAttribute('data-json-path')).toBe('$.dashboard.pages[0].views[0]');
      expect(summary?.querySelector('h2')?.getAttribute('data-js-view')).toBe('link-button-list');
      expect(metric?.querySelector('.metric-value')?.getAttribute('data-json-path')).toBe('$.dashboard.pages[0].views[1]');
      await vi.waitFor(() => {
        expect([...rendered.querySelectorAll('*')].every((element) => element.hasAttribute('data-json-path'))).toBe(true);
      });

      const dynamicChild = rendered.ownerDocument.createElement('span');
      summary?.append(dynamicChild);
      await vi.waitFor(() => {
        expect(dynamicChild.getAttribute('data-json-path')).toBe('$.dashboard.pages[0].views[0]');
        expect(dynamicChild.getAttribute('data-js-view')).toBe('link-button-list');
      });

      const replacementSection = rendered.ownerDocument.createElement('section');
      replacementSection.setAttribute('data-section-id', 'main');
      const replacementView = rendered.ownerDocument.createElement('article');
      replacementView.className = 'custom-view';
      replacementView.setAttribute('data-view-id', 'summary');
      replacementView.append(rendered.ownerDocument.createElement('dt'));
      replacementSection.append(replacementView);
      page?.replaceChildren(replacementSection);
      await vi.waitFor(() => {
        expect(replacementSection.getAttribute('data-json-path')).toBe('$.dashboard.pages[0].sections[0]');
        expect(replacementView.getAttribute('data-json-path')).toBe('$.dashboard.pages[0].views[0]');
        expect(replacementView.querySelector('dt')?.getAttribute('data-js-view')).toBe('link-button-list');
      });
    } finally {
      window.history.pushState(null, '', '/');
    }
  });

  it('surfaces a dom-provenance-error data attribute when the debug-only loader fails', async () => {
    vi.resetModules();
    vi.doMock('../../src/dom-provenance.js', () => {
      return {
        enableDashboardDomProvenance: () => {
          throw new Error('provenance module failed to load');
        },
        annotatePageDom: () => {
          throw new Error('provenance module failed to load');
        }
      };
    });
    const { renderDashboard: renderDashboardWithFailingProvenance } = await import('../../src/presenter.js');
    window.history.pushState(null, '', '?debug=1');
    try {
      const rendered = renderDashboardWithFailingProvenance({
        document: {
          languageVersion: '0.1.0',
          dashboard: {
            id: 'provenance-error-dashboard',
            title: 'Provenance error dashboard',
            pages: [{
              id: 'trace',
              kind: 'custom',
              title: 'Trace',
              views: [],
              sections: []
            }]
          }
        },
        sources: {}
      });

      await vi.waitFor(() => {
        expect(rendered.dataset.domProvenanceError).toContain('provenance module failed to load');
      });
    } finally {
      window.history.pushState(null, '', '/');
      vi.doUnmock('../../src/dom-provenance.js');
      vi.resetModules();
    }
  });

  it('does not annotate the dashboard when ?debug=1 is absent', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'no-provenance-dashboard',
          title: 'No provenance dashboard',
          pages: [{
            id: 'trace',
            kind: 'custom',
            title: 'Trace',
            views: [],
            sections: []
          }]
        }
      },
      sources: {}
    });

    await new Promise((resolveDelay) => setTimeout(resolveDelay, 0));
    expect(rendered.hasAttribute('data-json-path')).toBe(false);
  });

  it('keeps pie chart headings and authored descriptions in loading skeletons', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'pending-chart-dashboard',
          title: 'Costs',
          pages: [{
            id: 'cost',
            kind: 'custom',
            title: 'Cost',
            views: [{
              id: 'cost-by-campaign',
              title: 'Cost by campaign',
              description: 'Compare AI Credits consumed by each campaign.',
              mark: 'chart',
              chart: 'pie',
              layout: 'full',
              data: { source: 'usage' },
              encoding: {
                x: { field: 'campaign', type: 'nominal' },
                y: { field: 'credits', type: 'quantitative' }
              }
            }]
          }]
        }
      },
      sources: {},
      loading: true
    });

    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-view-id="cost-by-campaign"] .dashboard-view-skeleton')).not.toBeNull();
    });
    const view = rendered.querySelector('[data-view-id="cost-by-campaign"]');
    expect(view?.classList.contains('chart-view-pie')).toBe(true);
    expect(view?.querySelector(':scope > .chart-prompt-heading > :is(h2, h3, h4)')?.textContent)
      .toBe('Cost by campaign');
    expect(view?.querySelector(':scope > .view-description')?.textContent)
      .toBe('Compare AI Credits consumed by each campaign.');
    expect(view?.getAttribute('aria-busy')).toBe('true');
    expect(view?.querySelector('.table-intent-button')).toBeNull();
  });

  it.each(['empty', 'unavailable'])('retains full-width pie chrome for %s evidence', async (availability) => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'pie-states', title: 'Costs',
          pages: [{
            id: 'cost', kind: 'custom', title: 'Cost',
            views: [{
              id: 'cost-chart', title: 'Cost by campaign',
              description: 'Compare AI Credits consumed by each campaign.',
              mark: 'chart', chart: 'pie', layout: 'full', data: { source: 'usage' },
              encoding: {
                x: { field: 'campaign', type: 'nominal' },
                y: { field: 'credits', type: 'quantitative' }
              }
            }]
          }]
        }
      },
      sources: {
        usage: {
          source: 'usage', rows: [],
          metadata: {
            availability: availability === 'empty' ? 'empty' : 'unavailable',
            'source-id': 'usage', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
            completeness: 'complete', freshness: 'fresh'
          }
        }
      }
    });
    await vi.waitFor(() => {
      const view = rendered.querySelector('[data-view-id="cost-chart"]');
      expect(view?.classList.contains('chart-view-pie')).toBe(true);
      expect(view?.querySelector(':scope > .chart-prompt-heading h2')?.textContent).toBe('Cost by campaign');
      expect(view?.querySelector(':scope > .view-description')?.textContent)
        .toBe('Compare AI Credits consumed by each campaign.');
      expect(view?.querySelector(`:scope > .pie-chart-layout > [data-view-state="${availability}"]`))
        .not.toBeNull();
    });
  });

  it('shows a view loading skeleton instead of unavailable source errors during initial load', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'initial-load-dashboard',
          title: 'Initial Load',
          pages: [{
            id: 'repositories',
            kind: 'custom',
            title: 'Repositories',
            views: [{
              id: 'repository-activity',
              title: 'Repository Activity',
              mark: 'table',
              data: { source: 'repository-activity' }
            }],
            sections: [{
              id: 'main',
              title: 'Main',
              layout: 'full',
              views: ['repository-activity']
            }]
          }]
        }
      },
      sources: {},
      loading: true
    });

    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-page-id="repositories"] .page-layout-grid')).not.toBeNull();
    });
    const page = rendered.querySelector('[data-page-id="repositories"]');
    expect(page?.getAttribute('aria-busy')).not.toBe('true');
    expect(page?.getAttribute('aria-label')).toBeNull();
    expect(page?.querySelector('[role="status"]')).toBeNull();
    expect(page?.querySelector('.dashboard-view-skeleton')).not.toBeNull();
    expect(page?.querySelector('.agentic-loader')).toBeNull();
    expect(page?.textContent).not.toContain('This view cannot be shown because its data source is unavailable.');
  });

  it('renders page data that arrives before the initial dashboard snapshot completes', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'partial-load-dashboard',
          title: 'Partial Load',
          pages: [{
            id: 'repositories',
            kind: 'custom',
            title: 'Repositories',
            views: [{
              id: 'repository-activity',
              title: 'Repository Activity',
              mark: 'table',
              data: { source: 'repository-activity' },
              encoding: {
                columns: [{ field: 'repository', type: 'nominal', title: 'Repository' }]
              }
            }],
            sections: [{
              id: 'main',
              title: 'Main',
              layout: 'full',
              views: ['repository-activity']
            }]
          }]
        }
      },
      sources: {},
      loading: true,
      loadPageSources: async () => ({
        'repository-activity': {
          source: 'repository-activity',
          rows: [{ repository: 'githubnext/gh-cao' }],
          metadata: {
            'source-id': 'repository-activity',
            'source-kind': 'fixture',
            'as-of': '',
            'retrieved-at': '',
            completeness: 'partial',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      })
    });
    document.body.append(rendered);
    await activatePage(rendered, 'repositories');
    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-page-id="repositories"] td[data-field="repository"]')?.textContent).toBe('githubnext/gh-cao');
    });
    expect(rendered.querySelector('[data-page-id="repositories"] .dashboard-view-skeleton')).toBeNull();
    rendered.remove();
  });

  it('renders Overview composition while its components load independently', async () => {
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {},
      loading: true
    });
    document.body.append(rendered);
    const page = await activatePage(rendered, 'overview');
    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-page-id="overview"] .custom-view-grid')).not.toBeNull();
    });
    expect(page?.getAttribute('aria-busy')).not.toBe('true');
    expect(page?.textContent).not.toContain('Loading dashboard data…');
    rendered.remove();
  });

});

describe('presenter built-in and custom pages', () => {
  it('renders a declarative page form before the page views', () => {
    const document = /** @type {import('../../src/presenter.js').PresentationDocument} */ (/** @type {unknown} */ ({
      languageVersion: '0.1.0',
      dashboard: {
        id: 'simulator-dashboard',
        title: 'Simulator dashboard',
        pages: [{
          id: 'simulator',
          kind: 'custom',
          title: 'Simulator',
          form: {
            title: 'Scenario',
            fields: [
              { id: 'multiplier', label: 'Multiplier', control: 'slider', default: 1, min: 0, max: 4, step: 0.25 }
            ]
          },
          views: [{
            id: 'usage',
            data: { source: 'usage' },
            mark: 'metric',
            encoding: { value: { field: 'aic', aggregate: 'sum' } }
          }]
        }]
      }
    }));
    const rendered = renderDashboard({
      document,
      sources: {
        usage: {
          source: 'usage',
          rows: [{ aic: 2 }],
          metadata: {
            'source-id': 'usage',
            'source-kind': 'fixture',
            'as-of': '2026-09-24T00:00:00Z',
            'retrieved-at': '2026-09-24T00:00:00Z',
            availability: 'available',
            completeness: 'complete',
            freshness: 'fresh'
          }
        }
      }
    });
    const page = rendered.querySelector('[data-page-id="simulator"]');
    expect(page?.querySelector(':scope > .dashboard-parameter-form')).not.toBeNull();
    const slider = page?.querySelector('input[type="range"]');
    expect(slider).toBeInstanceOf(HTMLInputElement);
    expect(/** @type {HTMLInputElement} */ (slider).value).toBe('1');
    disposeDashboard(rendered);
  });

  it('resolves reusable view IDs referenced by custom pages', () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'reusable-view-dashboard',
          title: 'Reusable view dashboard',
          views: [{
            id: 'reusable-total',
            title: 'Reusable total',
            mark: 'metric',
            data: { source: 'summary' },
            encoding: {
              value: { field: 'value', aggregate: 'sum' }
            }
          }],
          pages: [{
            id: 'custom',
            kind: 'custom',
            title: 'Custom',
            views: ['reusable-total']
          }]
        }
      },
      sources: {
        summary: {
          source: 'summary',
          rows: [{ value: 7 }],
          metadata: {
            'source-id': 'summary-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-16T18:00:00Z',
            'retrieved-at': '2026-09-16T18:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    const view = rendered.querySelector('[data-view-id="reusable-total"]');
    expect(view?.textContent).toContain('Reusable total');
    expect(view?.querySelector('[data-metric-value="value"]')?.textContent).toBe('7');
    expect(view?.textContent).not.toContain('Invalid custom view definition.');
  });

  it('renders the most-blocked domains pie chart and drills domain cards to workflows', async () => {
    const metadata = /** @type {const} */ ({
      'source-id': 'firewall-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-09-05T11:00:00Z',
      'retrieved-at': '2026-09-05T11:05:00Z',
      completeness: 'partial',
      freshness: 'fresh',
      availability: 'available'
    });
    const rows = [
      { domain: 'api.github.com', run: 4, accepted: 12, blocked: 1 },
      { domain: 'new.example', run: 2, accepted: 3, blocked: 0 },
      { domain: 'blocked.example', run: 1, accepted: 0, blocked: 3_177_281 },
      { domain: 'changed.example', run: 2, accepted: 1, blocked: 2 }
    ];
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {
        'firewall-most-blocked-domains': { source: 'firewall-most-blocked-domains', rows, metadata },
        'firewall-domain-totals': { source: 'firewall-domain-totals', rows, metadata },
        'firewall-policy-rules': { source: 'firewall-policy-rules', rows: [], metadata }
      }
    });

    const page = await activatePage(rendered, 'firewall');
    expect(page?.querySelector('[data-view-id="security-firewall-most-blocked-domains"] [data-chart-widget="pie"]')).not.toBeNull();
    const prompt = page?.querySelector('[data-view-id="security-firewall-most-blocked-domains"] .table-intent-button');
    expect(prompt?.getAttribute('aria-label')).toBe('Propose fix: Most blocked domains');
    expect(page?.querySelector('.chart-view-pie > .chart-prompt-heading > .chart-prompt-action .table-intent-button')).toBe(prompt);
    prompt?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(page?.querySelector('.table-intent-preview')?.textContent).toContain('Prioritize investigation of the most blocked domains');
    expect(page?.querySelector('.table-intent-preview')?.textContent).toContain('query firewall-domain-workflows with --param domain=DOMAIN');
    expect(page?.querySelector('.table-intent-preview')?.textContent).toContain('Named CAO query IDs: firewall-domain-totals, firewall-most-blocked-domains');
    expect(page?.querySelector('.table-intent-preview')?.textContent).toContain('Record domain/workflow, time, blocked/allowed counts and disposition');
    expect(page?.querySelector('[data-chart-category="blocked.example"]')).not.toBeNull();
    expect(page?.querySelector('[data-view-id="security-firewall-most-blocked-domains"] .chart-legend-pie strong')?.textContent).toBe('3,177,281');
    expect(page?.querySelector('[data-view-layout="full-view"]')).not.toBeNull();
    const text = page?.textContent ?? '';
    expect(text).toContain('api.github.com');
    expect(text).toContain('new.example');
    expect(text).toContain('blocked.example');
    expect(text).toContain('changed.example');
    expect(text).toContain('Allowed');
    expect(text).toContain('Blocked');
    const firewallCard = page?.querySelector('[data-view-id="security-firewall-domains"] .entity-card-list-card');
    expect(firewallCard?.querySelector('.entity-card-list-title')?.textContent).toBe('blocked.example');
    expect([...firewallCard?.querySelectorAll('.entity-card-list-metric') ?? []].map((metric) => metric.textContent))
      .toEqual(['0Allowed', '3177281Blocked', '1Runs']);
    expect(firewallCard?.querySelector('[data-card-drill="query"]')?.getAttribute('href'))
      .toBe('#page-domain-insights?query=domain-entity-insights&title=blocked.example&domain=blocked.example');
    expect(text).not.toContain('firewall failure');
    rendered.remove();
  });

  it('renders the configured empty state when the firewall data binding is empty', async () => {
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {
        'firewall-most-blocked-domains': {
          source: 'firewall-most-blocked-domains',
          rows: [],
          metadata: {
            'source-id': 'firewall-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-05T11:00:00Z',
            'retrieved-at': '2026-09-05T11:05:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'empty'
          }
        },
        'firewall-domain-totals': {
          source: 'firewall-domain-totals',
          rows: [],
          metadata: {
            'source-id': 'firewall-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-05T11:00:00Z',
            'retrieved-at': '2026-09-05T11:05:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'empty'
          }
        }
      }
    });

    const page = await activatePage(rendered, 'firewall');
    const view = page?.querySelector('[data-view-id="security-firewall-domains"]');
    expect(view?.getAttribute('data-view-layout')).toBe('full-view');
    expect(view?.textContent).toContain(
      'No observed firewall domains are available for this selection.'
    );
    rendered.remove();
  });


  it('renders agent and model sample composition before the exact full-view lazy table', async ({ onTestFinished }) => {
    const metadata = {
      'source-id': 'usage-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-09-02T12:00:00Z',
      'retrieved-at': '2026-09-02T12:01:00Z',
      completeness: /** @type {'complete'} */ ('complete'),
      freshness: /** @type {'fresh'} */ ('fresh'),
      availability: /** @type {'available'} */ ('available')
    };
    const previousDatabase = globalThis.indexedDB;
    const previousCrypto = globalThis.crypto;
    const clock = vi.spyOn(Date, 'now').mockReturnValue(Date.parse(metadata['as-of']));
    vi.stubGlobal('indexedDB', new IDBFactory());
    vi.stubGlobal('crypto', webcrypto);
    onTestFinished(() => {
      vi.stubGlobal('indexedDB', previousDatabase);
      vi.stubGlobal('crypto', previousCrypto);
      clock.mockRestore();
    });
    /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */
    const sources = {
        runs: {
          source: 'runs',
          rows: [
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/daily.yml', run: '1001', 'run-status': 'completed', 'started-at': '2026-09-02T09:00:00Z', 'run-conclusion': 'success', engine: 'copilot', 'engine-version': '0.87.6', 'requested-model': 'gpt-5.6-sol', 'resolved-model': 'gpt-5.6-sol', 'run-link': { relation: 'run', href: 'https://github.com/github/gh-cao/actions/runs/1001', label: 'View run 1001' } },
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/review.yml', run: '1002', 'run-status': 'completed', 'started-at': '2026-09-02T10:00:00Z', 'run-conclusion': 'failure', engine: 'copilot', 'engine-version': '0.87.9', 'requested-model': 'gpt-5.6-sol', 'resolved-model': 'gpt-5.6-sol', 'run-link': { relation: 'run', href: 'https://github.com/github/gh-cao/actions/runs/1002', label: 'View run 1002' } },
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/audit.yml', run: '1003', 'run-status': 'completed', 'started-at': '2026-09-02T11:00:00Z', 'run-conclusion': 'success', engine: 'pi', 'engine-version': '1.2.0', 'requested-model': 'claude-sonnet-5', 'resolved-model': 'claude-sonnet-5', 'run-link': { relation: 'run', href: 'https://github.com/github/gh-cao/actions/runs/1003', label: 'View run 1003' } }
          ],
          metadata
        },
        'engines-models-usage': {
          source: 'engines-models-usage',
          rows: [
            {
              summary: 'copilot / gpt-5.6-sol',
              runs: 2,
              'minimum-aic-per-run': 4,
              'average-aic-per-run': 5,
              'maximum-aic-per-run': 6,
              'minimum-input-tokens-per-run': 100,
              'average-input-tokens-per-run': 150,
              'maximum-input-tokens-per-run': 200,
              'minimum-output-tokens-per-run': 30,
              'average-output-tokens-per-run': 45,
              'maximum-output-tokens-per-run': 60
            },
            {
              summary: 'pi / claude-sonnet-5',
              runs: 1,
              'minimum-aic-per-run': 8,
              'average-aic-per-run': 8,
              'maximum-aic-per-run': 8,
              'minimum-input-tokens-per-run': 300,
              'average-input-tokens-per-run': 300,
              'maximum-input-tokens-per-run': 300
            }
          ],
          metadata
        },
        outcomes: { source: 'outcomes', rows: [], metadata }
    };
    const sampled = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'load-dashboard-query-sources', ingest: true,
        sources, sourceNames: ['statistical-runs'], queries: authoritativeDashboardDocument.dashboard.queries
      })
    );
    expect(sampled['statistical-runs'].metadata['statistical-frame']).toMatchObject({ 'selected-count': 3 });
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: { ...sources, ...sampled }
    });
    onTestFinished(() => disposeDashboard(rendered));

    const page = await activatePage(rendered, 'engines-models');
    expect(page?.querySelectorAll('[data-view-layout="full-view"]')).toHaveLength(1);
    expect(page?.querySelector('[data-lazy-list]')).not.toBeNull();
    expect(page?.querySelectorAll('[data-chart-widget="pie"]')).toHaveLength(1);
    expect(page?.querySelectorAll('[data-chart-widget="horizontal-bar"]')).toHaveLength(1);
    expect(page?.querySelector('[data-view-id="engines-models-distribution"] + [data-view-id="engines-models-cost"]')).not.toBeNull();
    expect(page?.querySelector('[data-view-id="engines-models-aic-insights"]')).toBeNull();
    expect(page?.querySelector('[data-view-id="engines-models-token-insights"]')).not.toBeNull();
    expect([...(page?.querySelectorAll('[data-view-id="engines-models-cost"] .horizontal-bar-chart-label') ?? [])].map((value) => value.textContent))
      .toEqual(['pi / claude-sonnet-5', 'copilot / gpt-5.6-sol']);
    expect([...(page?.querySelectorAll('[data-view-id="engines-models-cost"] .horizontal-bar-chart-value') ?? [])].map((value) => value.textContent))
      .toEqual(['$0.08', '$0.06']);
    expect(page?.getAttribute('data-page-title')).toBe('Models & Agents');
    expect(page?.textContent).toContain('copilot');
    expect(page?.textContent).toContain('gpt-5.6-sol');
    expect(page?.textContent).toContain('claude-sonnet-5');
    expect(page?.querySelector('[data-view-id="engines-models-token-insights"]')?.getAttribute('aria-description')).toContain('Telemetry is partial');
    expect(page?.textContent).not.toContain('Requested model');
    expect(page?.querySelector('.view-mode-control')).not.toBeNull();
    expect(page?.textContent).not.toContain('Agent event');
    expect(page?.textContent).not.toContain('Summary');
    expect(page?.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(page?.getAttribute('data-view-mode')).toBe('chart');
    const costPage = await activatePage(rendered, 'cost');
    const aicInsights = costPage?.querySelector('[data-view-id="engines-models-aic-insights"]');
    expect(aicInsights?.getAttribute('data-view-layout')).toBe('full-view');
    expect(aicInsights?.hasAttribute('data-view-lazy-list')).toBe(true);
    expect(aicInsights?.closest('.view-disclosure')).toBeNull();
    expect(aicInsights?.querySelector('[data-lazy-list]')).not.toBeNull();
    expect(aicInsights?.querySelector('table')).not.toBeNull();
  });

  it('renders skill analytics with workflow drill-through in chart and card views', async () => {
    const metadata = {
      'source-id': 'skills-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-09-02T12:00:00Z',
      'retrieved-at': '2026-09-02T12:01:00Z',
      completeness: /** @type {'complete'} */ ('complete'),
      freshness: /** @type {'fresh'} */ ('fresh'),
      availability: /** @type {'available'} */ ('available')
    };
    const workflowLink = {
      'dashboard-href': '#page-workflow-runtime?workflow=githubnext%2Fgh-cao%3A.github%2Fworkflows%2Freview.md',
      'dashboard-label': 'View githubnext/gh-cao:.github/workflows/review.md workflow dashboard'
    };
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {
        'skill-invocations-by-skill': {
          source: 'skill-invocations-by-skill',
          rows: [{ skill: 'reactive-ui', invocations: 3 }],
          metadata
        },
        'skill-invocations-by-workflow': {
          source: 'skill-invocations-by-workflow',
          rows: [{
            'workflow-coordinate': 'githubnext/gh-cao:.github/workflows/review.md',
            'workflow-link': workflowLink,
            invocations: 3
          }],
          metadata
        },
        'skill-workflow-inventory': {
          source: 'skill-workflow-inventory',
          rows: [{
            skill: 'reactive-ui',
            workflow: '.github/workflows/review.md',
            'repository-coordinate': 'githubnext/gh-cao',
            'workflow-link': workflowLink,
            invocations: 3
          }],
          metadata
        },
        outcomes: { source: 'outcomes', rows: [], metadata }
      }
    });

    const page = await activatePage(rendered, 'skills');
    expect(page?.querySelectorAll('[data-chart-widget="horizontal-bar"]')).toHaveLength(2);
    expect(page?.querySelector('[data-view-id="skills-workflow-inventory"] table')).not.toBeNull();
    expect(page?.querySelector('[data-view-id="skills-workflow-inventory"] [data-mobile-card-list] [data-card-drill]')?.getAttribute('href'))
      .toBe(workflowLink['dashboard-href']);
  });


  it('renders indexing trends, database table counts, and transaction cards', async () => {
    const backend = document.createElement('meta');
    backend.name = 'dashboard-data-backend';
    backend.content = 'server-http';
    document.head.append(backend);
    const metadata = {
      'source-id': 'transactions-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-09-02T12:00:00Z',
      'retrieved-at': '2026-09-02T12:01:00Z',
      completeness: /** @type {'complete'} */ ('complete'),
      freshness: /** @type {'fresh'} */ ('fresh'),
      availability: /** @type {'available'} */ ('available')
    };
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {
        transactions: {
          source: 'transactions',
          rows: [{
            id: 'ingest-jsonl:current:logs-1',
            kind: 'ingest-jsonl',
            createdAt: '2026-09-02T12:00:00Z',
            payloadScope: 'https://dashboard.example/gh-aw-logs-shards/logs-1.jsonl',
            payloadHash: 'sha256:abc123',
            payloadEtag: 'etag-1',
            records: 10,
            committedRecords: 9,
            rawPayloadRecords: 8,
            rawRuns: 8,
            agenticRunRecords: 7,
            agenticRuns: 6,
            duplicateRawRunObservations: 1,
            duplicateAgenticRunObservations: 2,
            unenrichedRuns: 3,
            'activity-status': 'success',
            error: ''
          }],
          metadata
        },
        'indexing-database-table-counts': {
          source: 'indexing-database-table-counts',
          rows: [{ table: 'transactions', records: 1 }],
          metadata
        },
        'indexing-ingestion-runs': {
          source: 'indexing-ingestion-runs',
          rows: [
            { 'shard-day': '2026-09-01', 'workflow-runs': 6, 'run-count-status': 'Reported run count' },
            { 'shard-day': '2026-09-02', 'workflow-runs': 8, 'run-count-status': 'Reported run count' }
          ],
          metadata
        },
        'ingestion-health': {
          source: 'ingestion-health',
          rows: [{
            configured: true,
            health: 'healthy',
            'queue-depth': 12,
            'pending-tasks': 3,
            'dead-letters': 0,
            'webhook-received': 90,
            'webhook-duplicate': 2,
            'webhook-admission-failed': 0,
            'collection-succeeded': 34,
            'collection-failed': 1,
            'collection-retried': 1,
            'collection-dead-lettered': 0,
            'last-failure-code': 'collection',
            'last-failure-at': '2026-09-02T11:00:00Z',
            'last-success-at': '2026-09-02T12:00:00Z',
            'last-webhook-at': '2026-09-02T12:00:00Z',
            'last-projected': '2026-09-02T12:00:00Z',
            backfill: 0
          }],
          metadata
        },
        'ingestion-queue-sizes': {
          source: 'ingestion-queue-sizes',
          rows: [
            { queue: 'Ready and scheduled', tasks: 12 },
            { queue: 'In flight', tasks: 3 },
            { queue: 'Backfill admitted (total)', tasks: 4 }
          ],
          metadata
        },
        'github-api-usage': {
          source: 'github-api-usage',
          rows: [
            { 'observed-at': '2026-09-02T11:00:00Z', scope: 'aggregate', bucket: 'all buckets', limit: 10000, used: 2500, 'usage-percent': 25 },
            { 'observed-at': '2026-09-02T12:00:00Z', scope: 'aggregate', bucket: 'all buckets', limit: 10000, used: 4000, 'usage-percent': 40 },
            { 'observed-at': '2026-09-02T11:00:00Z', scope: 'bucket', bucket: 'collector/123/core', limit: 5000, used: 1000, 'usage-percent': 20 },
            { 'observed-at': '2026-09-02T12:00:00Z', scope: 'bucket', bucket: 'collector/123/core', limit: 5000, used: 3000, 'usage-percent': 60 }
          ],
          metadata
        }
      }
    });

    document.body.append(rendered);
    try {
      window.location.hash = '#page-indexing';
      await vi.waitFor(() => expect(rendered.querySelector('[data-page-id="indexing"]')?.hasAttribute('data-page-pending')).toBe(false));
      const page = rendered.querySelector('[data-page-id="indexing"]');
      expect(page?.querySelectorAll('[data-view-id]')).toHaveLength(10);
      expect(page?.querySelector('[data-view-id="indexing-daily-run-counts"] [data-view-availability="unavailable"]')).not.toBeNull();
      expect(page?.querySelector('[data-view-id="ingestion-webhook-status"] [data-view-availability="unavailable"]')).not.toBeNull();
      expect(page?.querySelectorAll('[data-chart-widget="bar"]')).toHaveLength(0);
      expect(page?.querySelector('[data-view-id="ingestion-storage"]')?.textContent).toContain('Storage engine unknown');
      expect(page?.querySelector('[data-view-id="indexing-size"]')).toBeNull();
      expect(page?.querySelectorAll('[data-chart-widget="scatter"] .scatter-chart-point')).toHaveLength(2);
      expect(page?.querySelectorAll('[data-view-id="github-api-usage"] [data-chart-widget="line"]')).toHaveLength(1);
      expect(page?.querySelector('[data-view-id="github-api-usage"]')?.textContent).toContain('collector/123/core');
      expect(page?.querySelectorAll('[data-chart-widget="horizontal-bar"]')).toHaveLength(2);
      expect(page?.querySelector('[data-view-id="ingestion-queue-sizes"]')?.textContent).toContain('Backfill admitted (total)');
      expect(page?.textContent).not.toContain('Local database');
      expect(page?.textContent).toContain('ingest-jsonl');
      expect(page?.querySelector('[data-view-id="ingestion-health-status"]')?.textContent).toContain('healthy');
      const transactions = page?.querySelector('[data-view-id="transaction-entries"]');
      expect(transactions?.querySelector('.entity-card-list-card')).not.toBeNull();
      expect(transactions?.textContent).toContain('Committed records');
      expect(transactions?.textContent).toContain('Payload hash');
      expect(transactions?.querySelector('input[type="search"]')).toBeNull();
    } finally {
      backend.remove();
      rendered.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('explains when engine and model usage data is missing', async () => {
    const metadata = {
      'source-id': 'usage-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-09-02T12:00:00Z',
      'retrieved-at': '2026-09-02T12:01:00Z',
      completeness: /** @type {'complete'} */ ('complete'),
      freshness: /** @type {'fresh'} */ ('fresh'),
      availability: /** @type {'empty'} */ ('empty')
    };
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {
        runs: { source: 'runs', rows: [], metadata },
        'engines-models-usage': { source: 'engines-models-usage', rows: [], metadata },
        outcomes: { source: 'outcomes', rows: [], metadata }
      }
    });

    const page = await activatePage(rendered, 'engines-models');
    expect(page?.textContent).toContain('No agent or model usage metadata is available.');
    expect(page?.querySelector('[data-lazy-list]')).not.toBeNull();
    rendered.remove();
  });

  it('renders the JSON-declared workflow chart and inventory table', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'workflow-topology-dashboard',
        title: 'Workflow Topology',
        pages: [{
          id: 'workflows',
          kind: /** @type {'built-in'} */ ('built-in'),
          page: 'workflows',
          title: 'Workflows',
          icon: 'rocket'
        }]
      }
    };

    const rendered = renderDashboard({
      document,
      sources: applyDashboardQueries({
        runs: {
          source: 'runs',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dependabot.yml', run: '1', 'aic-total': 12 },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dependabot.yml', run: '2', 'aic-total': 18 },
            { organization: 'github', repository: 'target-service', workflow: '.github/workflows/ci.yml', run: '3', 'aic-total': 5 }
          ],
          metadata: {
            'source-id': 'workflow-runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'dependabot', 'campaign-name': 'Dependabot', workflow: '.github/workflows/dependabot.yml', 'workflow-name': 'Dependabot', 'workflow-role': 'orchestrator', 'workflow-active': 'true', 'rollout-mode': 'review' },
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'dependabot', 'campaign-name': 'Dependabot', workflow: '.github/workflows/dependabot-update-planner.yml', 'workflow-name': 'Dependabot / Update Planner', 'workflow-role': 'worker', 'workflow-active': 'true', 'rollout-mode': 'review' },
            { organization: 'github', repository: 'target-service', workflow: '.github/workflows/ci.yml', 'workflow-name': 'CI', 'workflow-role': 'standalone', 'workflow-active': 'true', 'rollout-mode': 'live' }
          ],
          metadata: {
            'source-id': 'workflow-topology-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        usage: {
          source: 'usage',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dependabot.yml', aic: 12 },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dependabot.yml', aic: 18 },
            { organization: 'github', repository: 'target-service', workflow: '.github/workflows/ci.yml', aic: 5 }
          ],
          metadata: {
            'source-id': 'workflow-usage-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        historical: {
          source: 'historical',
          rows: [{ record: 'older-window' }],
          metadata: {
            'source-id': 'historical-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-01T12:00:00Z',
            'retrieved-at': '2026-09-01T12:00:00Z',
            'coverage-start': '2026-01-01T00:00:00Z',
            'coverage-end': '2026-09-01T12:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }, ['workflow-list', 'workflow-aic-per-run'])
    });

    const page = rendered.querySelector('[data-page-name="workflows"]');
    expect(globalThis.document.title).toBe('Workflows · Workflow Topology');
    expect(page?.getAttribute('data-page-description')).toBe('Select a declared workflow for CAO operational review.');
    expect(page?.querySelector('.view-metadata-summary')).toBeNull();
    expect(rendered.querySelector('.horizon-summary [aria-label="Data status"]')).toBeNull();
    expect(rendered.querySelector('.filter-tuning-controls .horizon-details [aria-label="Data status"]')).toBeNull();
    expect(page?.querySelector('[data-chart-widget="horizontal-bar"]')).not.toBeNull();
    expect([...(page?.querySelectorAll('[data-view-id="workflows-by-aic-per-run"] .horizontal-bar-chart-label') ?? [])].map((value) => value.textContent))
      .toEqual(['dependabot.yml (githubnext/gh-cao)', 'ci.yml (github/target-service)']);
    expect([...(page?.querySelectorAll('[data-view-id="workflows-by-aic-per-run"] .horizontal-bar-chart-value') ?? [])].map((value) => value.textContent))
      .toEqual(['15', '5']);
    expect(page?.querySelector('[data-view-id="workflows-by-aic-per-run"] .horizontal-bar-chart-label a')?.getAttribute('href'))
      .toContain('#page-workflow-runtime?workflow=');
    expect(page?.querySelector('[data-view-id="workflows-inventory"][data-view-layout="full-view"]')).not.toBeNull();
    const rocket = rendered.querySelector('[data-nav-page-id="workflows"] .octicon-rocket');
    expect(rocket?.classList.contains('octicon-rocket')).toBe(true);
    expect(rocket?.querySelector('path')).not.toBeNull();
  });

  it('uses declared workflow identities for inventory navigation', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'workflow-topology-links-dashboard',
        title: 'Workflow Topology Links',
        pages: [
          {
            id: 'workflows',
            kind: /** @type {'built-in'} */ ('built-in'),
            page: 'workflows',
            title: 'Workflows'
          },
          {
            id: 'repository-detail',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Repository',
            route: { 'hash-query-parameter': 'repository' },
            views: []
          },
          {
            id: 'workflow-runtime',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Workflow runtime',
            route: { 'hash-query-parameter': 'workflow' },
            views: []
          }
        ]
      }
    };

    const rendered = renderDashboard({
      document,
      sources: applyDashboardQueries({
        runs: {
          source: 'runs',
          rows: [],
          metadata: {
            'source-id': 'workflow-runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        usage: {
          source: 'usage',
          rows: [],
          metadata: {
            'source-id': 'workflow-usage-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'dependabot', 'campaign-name': 'Dependabot', workflow: '.github/workflows/dependabot.yml', 'workflow-name': 'Dependabot', 'workflow-role': 'orchestrator', 'workflow-active': 'true', 'rollout-mode': 'live' },
            { organization: 'github', repository: 'target-service', workflow: '.github/workflows/ci.yml', 'workflow-name': 'CI', 'workflow-role': 'standalone', 'workflow-active': 'true', 'rollout-mode': 'unknown' }
          ],
          metadata: {
            'source-id': 'workflow-topology-links-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }, ['workflow-list'])
    });

    const links = [...rendered.querySelectorAll('[data-page-name="workflows"] .chart-legend-pie a')]
      .map((link) => link.getAttribute('href'));
    expect(links).toHaveLength(0);
    expect(rendered.querySelector('[data-page-name="workflows"] table')).not.toBeNull();
    expect(rendered.querySelector('[data-page-name="workflows"] table a')?.getAttribute('href'))
      .toMatch(/^#page-workflow-runtime\?workflow=/);
    expect([...rendered.querySelectorAll('[data-page-name="workflows"] table a')]
      .every((link) => !(link.parentElement instanceof HTMLAnchorElement))).toBe(true);
  });

  it('DLS-LINK-006 DLS-LINK-007 renders worker-provided entity links in table columns and honours explicit link overrides', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'entity-link-table-dashboard',
        title: 'Entity Link Table',
        'github-url-base': 'https://github.example.com',
        pages: [
          {
            id: 'repositories',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Repositories',
            views: [
              {
                id: 'repositories-table',
                title: 'Repositories',
                data: { source: 'repositories' },
                mark: 'table',
                encoding: {
                  columns: [
                    { field: 'organization' },
                    { field: 'repository' }
                  ]
                }
              }
            ]
          }
        ]
      }
    };

    const rendered = renderDashboard({
      document,
      sources: {
        repositories: {
          source: 'repositories',
          rows: [
            {
              organization: 'octo-org',
              repository: 'platform',
              'organization-link': { relation: 'organization', href: 'https://github.example.com/octo-org', label: 'octo-org' },
              'repository-link': { relation: 'repository', href: 'https://github.example.com/octo-org/platform', label: 'platform' }
            },
            {
              organization: 'octo-org',
              repository: 'overridden',
              'repository-link': { relation: 'repository', href: 'https://example.com/custom', label: 'Custom link' }
            }
          ],
          metadata: {
            'source-id': 'repositories-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    const table = rendered.querySelector('table');
    const links = [...(table?.querySelectorAll('tbody a') ?? [])];
    const derivedOrganizationLink = links.find((link) => link.getAttribute('href') === 'https://github.example.com/octo-org');
    expect(derivedOrganizationLink).toBeDefined();
    const derivedRepositoryLink = links.find((link) => link.getAttribute('href') === 'https://github.example.com/octo-org/platform');
    expect(derivedRepositoryLink).toBeDefined();
    const overriddenRepositoryLink = links.find((link) => link.getAttribute('href') === 'https://example.com/custom');
    expect(overriddenRepositoryLink).toBeDefined();
    expect(links.some((link) => link.getAttribute('href') === 'https://github.example.com/octo-org/overridden')).toBe(false);
  });

  it('DLS-SAFE-011 omits repository actions when repository is absent', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'no-repository-dashboard',
        title: 'No Repository',
        pages: [{ id: 'usage', kind: /** @type {'built-in'} */ ('built-in'), page: 'usage', title: 'Usage' }]
      }
    };

    const rendered = renderDashboard({ document, sources: {} });

    expect(rendered.querySelector('.refresh-button')).toBeNull();
    expect(rendered.querySelector('.account-menu')).toBeNull();
    expect(rendered.querySelector('.report-footer .refresh-button')).toBeNull();
    expect(rendered.querySelector('.report-footer-status')).toBeNull();
    expect(rendered.querySelector('.repository-link')).toBeNull();
  });

  it('renders the dashboard commit SHA linked to the same repository as the chrome icon', () => {
    const commitSha = '0123456789abcdef0123456789abcdef01234567';
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'versioned-dashboard',
        title: 'Versioned Dashboard',
        'github-url-base': 'https://github.example.com',
        repository: 'octo-org/agentic-operations',
        pages: [{ id: 'usage', kind: /** @type {'built-in'} */ ('built-in'), page: 'usage', title: 'Usage' }]
      }
    };

    const rendered = renderDashboard({ document, sources: {}, commitSha, caoVersion: '1.2.3', ghAwVersion: 'v0.91.1' });

    expect(rendered.querySelector('.report-footer-cao-version')?.textContent).toBe('CAO 1.2.3');
    expect(rendered.querySelector('.report-footer-gh-aw-version')?.textContent).toBe('gh-aw v0.91.1');
    expect(rendered.querySelector('.report-footer-version')?.textContent).toBe('Dashboard 0123456');
    expect(rendered.querySelector('.report-footer-version')?.getAttribute('title')).toBe(commitSha);
    expect(rendered.querySelector('.report-footer-version a')?.getAttribute('href')).toBe(
      `https://github.example.com/octo-org/agentic-operations/commit/${commitSha}`
    );
    expect(rendered.querySelector('.repository-link')?.getAttribute('href')).toBe('https://github.example.com/octo-org/agentic-operations');
  });

  it('DLS-DOC-012 DLS-SAFE-011 renders a labeled GitHub repository link resolved against a custom github-url-base', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'repository-dashboard',
        title: 'Repository Dashboard',
        'github-url-base': 'https://github.example.com',
        repository: 'octo-org/agentic-operations',
        pages: [{ id: 'usage', kind: /** @type {'built-in'} */ ('built-in'), page: 'usage', title: 'Usage' }]
      }
    };

    localStorage.setItem('central-agentic-ops.dashboard.theme', 'dark');
    const rendered = renderDashboard({ document, sources: {} });
    globalThis.document.body.append(rendered);

    expect(rendered.querySelector('.refresh-button')).toBeNull();
    expect(rendered.querySelector('.account-menu')).toBeNull();
    expect(rendered.querySelector('.report-actions [data-theme-value]')).toBeNull();
    expect(rendered.dataset.theme).toBe('dark');
    const repositoryLink = rendered.querySelector('.repository-link');
    expect(repositoryLink).not.toBeNull();
    expect(repositoryLink?.getAttribute('href')).toBe('https://github.example.com/octo-org/agentic-operations');
    expect(repositoryLink?.getAttribute('aria-label')).toBe('View octo-org/agentic-operations on GitHub');
    expect(repositoryLink?.getAttribute('title')).toBe('View octo-org/agentic-operations on GitHub');
    localStorage.clear();
    expect(rendered.querySelector('.sidebar-brand > span')?.textContent).toBe('agentic-operations');
    expect(rendered.querySelector('.mobile-page-header .mobile-brand-name')?.textContent).toBe('agentic-operations');
    rendered.remove();
  });

  it('routes repository entity links to the repository detail view while retaining GitHub Actions links', async () => {
    window.history.replaceState(null, '', '/#page-repositories');
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'repository-routing-dashboard',
          title: 'Repository routing',
          pages: [
            {
              id: 'repositories',
              kind: /** @type {'custom'} */ ('custom'),
              title: 'Repositories',
              views: [{
                id: 'repository-list',
                title: 'Repositories',
                data: { source: 'repositories' },
                mark: 'table',
                encoding: {
                  columns: [{ field: 'repository' }],
                  href: { field: 'repository-link', type: 'nominal' }
                }
              }]
            },
            {
              id: 'repository-detail',
              kind: /** @type {'custom'} */ ('custom'),
              title: 'Repository',
              route: { 'hash-query-parameter': 'repository', 'navigation-page': 'repositories' },
              views: [{
                id: 'repository-workflow-count',
                title: 'Authored workflows',
                data: { source: 'workflows', 'route-field': 'repository-slug' },
                mark: 'metric',
                encoding: {
                  value: { field: 'workflow', type: 'quantitative', aggregate: 'count' },
                  href: { field: 'external-link', type: 'nominal' }
                }
              }]
            }
          ]
        }
      },
      sources: {
        repositories: {
          source: 'repositories',
          rows: [{
            organization: 'octo-org',
            repository: 'platform',
            'repository-slug': 'octo-org/platform',
            'repository-link': {
              relation: 'repository',
              'dashboard-href': '#page-repository-detail?repository=octo-org%2Fplatform',
              'dashboard-label': 'platform'
            }
          }],
          metadata: {
            'source-id': 'repositories-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        workflows: {
          source: 'workflows',
          rows: [{
            organization: 'octo-org',
            repository: 'platform',
            workflow: '.github/workflows/review.md',
            'workflow-name': 'Review',
            'workflow-active': 'true',
            'external-link': { href: 'https://github.com/octo-org/platform/actions', label: 'Actions' }
          }],
          metadata: {
            'source-id': 'workflows-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });
    document.body.append(rendered);

    const repositoryLink = rendered.querySelector('[data-page-id="repositories"] tbody a');
    expect(repositoryLink?.getAttribute('href')).toBe('#page-repository-detail?repository=octo-org%2Fplatform');
    expect(repositoryLink?.getAttribute('target')).toBeNull();

    window.history.replaceState(null, '', `/${repositoryLink?.getAttribute('href')}`);
    window.dispatchEvent(new Event('hashchange'));

    expect(rendered.querySelector('[data-page-id="repository-detail"]')?.hasAttribute('hidden')).toBe(false);
    await vi.waitFor(() => {
      expect(rendered.querySelector('[data-page-id="repository-detail"]')?.hasAttribute('data-page-pending')).toBe(false);
    });
    expect(rendered.querySelector('[data-nav-page-id="repositories"]')?.getAttribute('aria-current')).toBe('page');
    rendered.remove();
    window.history.replaceState(null, '', '/');
  });


  it('restores a saved theme before the Settings page is rendered', () => {
    try {
      localStorage.setItem('central-agentic-ops.dashboard.theme', 'dark');
      const rendered = renderDashboard({
        document: authoritativeDashboardDocument,
        sources: {}
      });

      expect(rendered.dataset.theme).toBe('dark');
    } finally {
      localStorage.clear();
    }
  });

  it('does not turn agent smells into Home notifications', async () => {
    window.history.replaceState(null, '', '/#page-agents');
    const metadata = /** @type {const} */ ({
      'source-id': 'notification-agent-smell-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-09-07T09:00:00Z',
      'retrieved-at': '2026-09-07T09:01:00Z',
      completeness: 'complete',
      freshness: 'fresh',
      availability: 'available'
    });
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {
        'attention-signals': { source: 'attention-signals', rows: [], metadata },
        'overview-failed-run-count': { source: 'overview-failed-run-count', rows: [{ count: 0 }], metadata },
        'overview-blocked-work-count': { source: 'overview-blocked-work-count', rows: [{ count: 0 }], metadata },
        'overview-awaiting-review-count': { source: 'overview-awaiting-review-count', rows: [{ count: 0 }], metadata },
        'overview-security-finding-count': {
          source: 'overview-security-finding-count',
          rows: [],
          metadata: { ...metadata, availability: 'unavailable' }
        },
        outcomes: { source: 'outcomes', rows: [], metadata },
        'safe-output-performance': { source: 'safe-output-performance', rows: [], metadata },
        usage: { source: 'usage', rows: [], metadata },
        runs: { source: 'runs', rows: [], metadata },
        'overview-runs': { source: 'overview-runs', rows: [], metadata },
        repositories: { source: 'repositories', rows: [], metadata },
        'work-items': { source: 'work-items', rows: [], metadata },
        workflows: {
          source: 'workflows',
          rows: [{
            organization: 'githubnext', repository: 'gh-aw', 'workflow-role': 'standalone',
            workflow: '.github/workflows/review.md', 'workflow-name': 'Review agent',
            'workflow-active': 'false', 'observed-at': '2020-01-01T00:00:00Z'
          }],
          metadata
        },
        'agent-assignments': { source: 'agent-assignments', rows: [], metadata },
        'security-observations': {
          source: 'security-observations',
          rows: [{
            organization: 'githubnext', repository: 'gh-aw', workflow: '.github/workflows/review.lock.yml',
            'security-feature': 'threat-detection', 'security-analysis': 'summary',
            'security-signal': 'Malicious patch', 'security-status': 'detected',
            'observed-at': '2020-01-01T00:00:00Z'
          }],
          metadata
        }
      }
    });
    document.body.append(rendered);

    const page = await activatePage(rendered, 'overview');
    expect(page?.querySelector(':scope > .custom-view-grid')).not.toBeNull();
    expect(page?.querySelectorAll(':scope > .custom-view-grid > .custom-view')).toHaveLength(2);
    expect(page?.querySelectorAll('.factory-station')).toHaveLength(4);
    expect(page?.querySelector('.factory-intro h2')?.textContent).toBe('Your factory is idle.');
    expect(page?.querySelector('.factory-station')?.textContent).toContain('1/1 healthy workflows');
    expect(page?.querySelector('.link-button-list-view')).toBeNull();
    expect(page?.querySelector('.notifications-inbox')).toBeNull();
    expect(page?.querySelector('.factory-status')).toBeNull();
    expect(page?.querySelector('.factory-all-clear')).toBeNull();
    expect(page?.querySelector('.home-attention-detail')).toBeNull();
    expect(page?.textContent).not.toContain('Malicious patch detected');
    rendered.remove();
  });




  it('renders conditional site-wide callouts and remembers dismissal only in memory', async () => {
    window.localStorage.clear();
    sessionStorage.clear();
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'callout-dashboard',
        title: 'Callout Dashboard',
        callouts: [
          {
            id: 'operator-message',
            title: 'Operator message',
            description: 'A message for every dashboard user.',
            icon: 'megaphone',
            'navigation-page': 'usage'
          },
          {
            id: 'rate-limit-message',
            title: 'Dashboard data is partial',
            description: 'Some data could not be downloaded.',
            icon: 'alert',
            'navigation-page': 'coverage',
            'visible-when': {
              source: 'coverage-diagnostics',
              field: 'kind',
              equals: 'github-api-rate-limit-403'
            }
          }
        ],
        pages: [{
          id: 'usage',
          kind: /** @type {'built-in'} */ ('built-in'),
          page: 'usage',
          title: 'Usage'
        }]
      }
    };
    const sources = {
      'coverage-diagnostics': {
        source: 'coverage-diagnostics',
        rows: [{ kind: 'github-api-rate-limit-403' }],
        metadata: {
          'source-id': 'coverage-diagnostics-fixture',
          'source-kind': 'fixture',
          'as-of': '2026-09-02T23:00:00Z',
          'retrieved-at': '2026-09-02T23:00:00Z',
          completeness: /** @type {'complete'} */ ('complete'),
          freshness: /** @type {'fresh'} */ ('fresh'),
          availability: /** @type {'available'} */ ('available')
        }
      }
    };

    const rendered = renderDashboard({ document, sources });
    const notices = globalThis.document;
    await vi.waitFor(() => expect(notices.querySelectorAll('.dashboard-notification:not(.dashboard-notification-exit)')).toHaveLength(2));
    expect(notices.querySelector('[data-notification-id="site-callout:rate-limit-message"]')?.textContent).toContain('Dashboard data is partial');
    const detailsLink = /** @type {HTMLAnchorElement | null} */ (
      notices.querySelector('[data-notification-id="site-callout:rate-limit-message"] .dashboard-notification-link')
    );
    expect(detailsLink?.getAttribute('href')).toBe('#page-coverage');
    expect(detailsLink?.textContent).toBe('View coverage');
    const navigationLink = /** @type {HTMLAnchorElement | null} */ (
      notices.querySelector('[data-notification-id="site-callout:operator-message"] .dashboard-notification-link')
    );
    expect(navigationLink?.getAttribute('href')).toBe('#page-usage');
    expect(navigationLink?.textContent).toBe('View Usage');
    const dismiss = /** @type {HTMLButtonElement | null} */ (
      notices.querySelector('[data-notification-id="site-callout:operator-message"] .dashboard-notification-dismiss')
    );
    const storageBeforeDismiss = { ...window.localStorage };
    const sessionBeforeDismiss = { ...sessionStorage };
    expect(dismiss?.getAttribute('aria-label')).toBe('Dismiss Operator message');
    dismiss?.click();
    expect(notices.querySelector('[data-notification-id="site-callout:operator-message"]:not(.dashboard-notification-exit)')).toBeNull();
    expect({ ...window.localStorage }).toEqual(storageBeforeDismiss);
    expect({ ...sessionStorage }).toEqual(sessionBeforeDismiss);

    const rerendered = renderDashboard({ document, sources });
    disposeDashboard(rendered);
    expect(notices.querySelector('[data-notification-id="site-callout:operator-message"]:not(.dashboard-notification-exit)')).toBeNull();
    await vi.waitFor(() => expect(notices.querySelector('[data-notification-id="site-callout:rate-limit-message"]:not(.dashboard-notification-exit)')).not.toBeNull());

    const complete = renderDashboard({ document, sources: {} });
    disposeDashboard(rerendered);
    await vi.waitFor(() => expect(notices.querySelector('[data-notification-id="site-callout:rate-limit-message"]:not(.dashboard-notification-exit)')).toBeNull());
    disposeDashboard(complete);
  });

  it('collapses the sidebar to icons and restores the persisted display mode', () => {
    window.localStorage.clear();
    try {
      const rendered = renderDashboard({
        document: authoritativeDashboardDocument,
        sources: {}
      });
      const toggle = /** @type {HTMLButtonElement | null} */ (rendered.querySelector('.sidebar-toggle'));
      const shell = rendered.querySelector('.app-shell');
      const overviewLink = rendered.querySelector('[data-nav-page-id="overview"]');

      expect(toggle?.getAttribute('aria-label')).toBe('Collapse navigation');
      expect(toggle?.getAttribute('aria-expanded')).toBe('true');
      expect(toggle?.querySelector('.octicon-sidebar-expand')).not.toBeNull();
      expect(overviewLink?.getAttribute('title')).toBe('Overview');

      toggle?.click();

      expect(shell?.classList.contains('sidebar-collapsed')).toBe(true);
      expect(toggle?.getAttribute('aria-label')).toBe('Expand navigation');
      expect(toggle?.getAttribute('aria-expanded')).toBe('false');
      expect(toggle?.querySelector('.octicon-sidebar-collapse')).not.toBeNull();
      expect(window.localStorage.getItem('central-agentic-ops.dashboard.sidebar-collapsed')).toBe('true');

      const restored = renderDashboard({
        document: authoritativeDashboardDocument,
        sources: {}
      });
      expect(restored.querySelector('.app-shell')?.classList.contains('sidebar-collapsed')).toBe(true);
      expect(restored.querySelector('.sidebar-toggle')?.getAttribute('aria-label')).toBe('Expand navigation');
    } finally {
      window.localStorage.clear();
    }
  });

  it('keeps the sidebar interactive when localStorage is unavailable', () => {
    const storageDescriptor = Object.getOwnPropertyDescriptor(window, 'localStorage');
    Object.defineProperty(window, 'localStorage', { configurable: true, value: undefined });
    try {
      const rendered = renderDashboard({
        document: authoritativeDashboardDocument,
        sources: {}
      });
      const toggle = /** @type {HTMLButtonElement | null} */ (rendered.querySelector('.sidebar-toggle'));
      const shell = rendered.querySelector('.app-shell');

      expect(shell?.classList.contains('sidebar-collapsed')).toBe(false);
      toggle?.click();
      expect(shell?.classList.contains('sidebar-collapsed')).toBe(true);
      expect(toggle?.getAttribute('aria-label')).toBe('Expand navigation');
    } finally {
      if (storageDescriptor) Object.defineProperty(window, 'localStorage', storageDescriptor);
    }
  });

  it('renders ordered view modes in page chrome and emits worker query context', async () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'mobile-view-mode-dashboard',
        title: 'Mobile View Mode',
        pages: [{
          id: 'overview',
          kind: /** @type {'custom'} */ ('custom'),
          title: 'Overview',
          views: [{ id: 'summary', title: 'Summary', data: { source: 'runs' }, mark: 'metric', encoding: { value: { field: 'run-count', aggregate: 'sum' } } }]
        }, {
          id: 'runs',
          kind: /** @type {'custom'} */ ('custom'),
          title: 'Runs',
          views: [
            {
              id: 'runs-chart',
              title: 'Run trend',
              data: { source: 'runs' },
              mark: 'chart',
              chart: 'line',
              encoding: {
                x: { field: 'started-at', type: 'temporal' },
                y: { field: 'run-count', type: 'quantitative' }
              }
            },
            {
              id: 'runs-table',
              title: 'Runs',
              data: { source: 'runs' },
              mark: 'table',
              controls: 'interactive',
              'lazy-list': true,
              layout: 'full-view',
              encoding: {
                columns: [{ field: 'run' }]
              }
            }
          ]
        }]
      }
    };
    const sources = /** @type {Parameters<typeof renderDashboard>[0]['sources']} */ ({
      runs: {
        source: 'runs',
        rows: [{ run: '1', 'run-count': 1, 'started-at': '2026-09-16T10:00:00Z' }],
        metadata: {
          'source-id': 'runs-fixture',
          'source-kind': 'fixture',
          'as-of': '2026-09-16T10:00:00Z',
          'retrieved-at': '2026-09-16T10:00:00Z',
          availability: 'available',
          completeness: 'complete',
          freshness: 'fresh'
        }
      }
    });

    const rendered = renderDashboard({ document, sources });
    expect(rendered.querySelector('[data-page-id="overview"] .filter-bar')).toBeNull();

    const page = await activatePage(rendered, 'runs');
    const chrome = page?.querySelector(':scope > .page-chrome');
    const modeButtons = [...chrome?.querySelectorAll('[data-view-mode-value]') ?? []];
    /** @type {unknown[]} */
    const contexts = [];
    rendered.addEventListener('dashboard-query-context-change', (event) => {
      if (event instanceof CustomEvent) contexts.push(event.detail);
    });

    expect(page?.querySelector(':scope > .filter-bar')).toBeNull();
    expect(chrome?.querySelector('.filter-bar')).toBeNull();
    expect(modeButtons.map((button) => button.textContent)).toEqual(['Chart', 'Cards', 'Table']);
    expect(modeButtons[0]?.getAttribute('aria-pressed')).toBe('true');
    expect(page?.querySelector('[data-view-id="runs-chart"]')?.getAttribute('data-view-mode-content')).toBe('chart');
    expect(page?.querySelector('[data-view-id="runs-table"]')?.getAttribute('data-view-mode-content')).toBe('table');
    expect(rendered.querySelector('.mobile-view-mode-toggle')).not.toBeNull();
    /** @type {HTMLButtonElement} */ (modeButtons[1]).click();
    expect(contexts.at(-1)).toMatchObject({ pageId: 'runs', queryContext: { viewMode: 'card' } });
  });

  it('exposes the same ordered view modes on production chart and table pages', async () => {
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });

    for (const pageId of ['repositories', 'mcps', 'firewall', 'runs']) {
      const page = await activatePage(rendered, pageId);
      const buttons = [...page?.querySelectorAll(':scope > .page-chrome [data-view-mode-value]') ?? []];
      expect(buttons.map((button) => button.textContent), pageId).toEqual(['Chart', 'Cards', 'Table']);
      expect(buttons.map((button) => button.getAttribute('aria-pressed')), pageId).toEqual(['true', 'false', 'false']);
    }

  });

  it('keeps the default presentation without view-mode controls when disabled', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'fixed-view-mode-dashboard',
          title: 'Fixed View Mode',
          pages: [{
            id: 'runs',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Runs',
            'view-mode-control': false,
            views: [
              {
                id: 'runs-chart',
                title: 'Run trend',
                data: { source: 'runs' },
                mark: 'chart',
                chart: 'line',
                encoding: {
                  x: { field: 'started-at', type: 'temporal' },
                  y: { field: 'run-count', type: 'quantitative' }
                }
              },
              {
                id: 'runs-table',
                title: 'Runs',
                data: { source: 'runs' },
                mark: 'table',
                encoding: { columns: [{ field: 'run' }] }
              }
            ]
          }]
        }
      },
      sources: {
        runs: {
          source: 'runs',
          rows: [{ run: '1', 'run-count': 1, 'started-at': '2026-09-16T10:00:00Z' }],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-16T10:00:00Z',
            'retrieved-at': '2026-09-16T10:00:00Z',
            availability: 'available',
            completeness: 'complete',
            freshness: 'fresh'
          }
        }
      }
    });

    const page = await activatePage(rendered, 'runs');
    expect(page?.getAttribute('data-view-mode')).toBe('chart');
    expect(page?.querySelector('.view-mode-control')).toBeNull();
    expect(page?.querySelector('[data-view-id="runs-chart"]')?.getAttribute('data-view-mode-content')).toBe('chart');
    expect(page?.querySelector('[data-view-id="runs-table"]')?.getAttribute('data-view-mode-content')).toBe('table');
  });

  it('omits the view-mode control for any page composed only of element-mark views, not only the built-in overview page', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'element-only-dashboard',
          title: 'Element Only',
          pages: [{
            id: 'custom-element-page',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Custom Element Page',
            views: [{
              id: 'custom-element-note',
              title: 'Note',
              data: { source: 'notes' },
              mark: 'element',
              element: 'markdown',
              config: { body: 'A page composed only of element-mark views.' }
            }]
          }]
        }
      },
      sources: {}
    });

    const page = await activatePage(rendered, 'custom-element-page');
    expect(page?.querySelector('.view-mode-control')).toBeNull();
    expect(page?.hasAttribute('data-view-mode')).toBe(false);
  });

  it('omits page chrome when a page has only one card view mode', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'cards-only-dashboard',
          title: 'Cards only',
          pages: [{
            id: 'maintenance',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Updates',
            views: [{
              id: 'campaigns',
              title: 'Campaigns',
              data: { source: 'campaigns' },
              mark: 'list',
              list: { style: 'cards', icon: 'goal' },
              encoding: { columns: [{ field: 'campaign-name' }] }
            }]
          }]
        }
      },
      sources: {
        campaigns: {
          source: 'campaigns',
          rows: [],
          metadata: {
            'source-id': 'campaigns-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-16T10:00:00Z',
            'retrieved-at': '2026-09-16T10:00:00Z',
            availability: 'available',
            completeness: 'complete',
            freshness: 'fresh'
          }
        }
      }
    });

    const page = await activatePage(rendered, 'maintenance');
    expect(page?.querySelector(':scope > .page-chrome')).toBeNull();
    expect(page?.getAttribute('data-view-mode')).toBe('card');
  });

  it('omits page chrome when a page has only one chart view mode', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'chart-only-dashboard',
          title: 'Chart only',
          pages: [{
            id: 'summary',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Summary',
            views: [{
              id: 'summary-chart',
              title: 'Summary chart',
              data: { source: 'summary' },
              mark: 'chart',
              chart: 'pie',
              encoding: {
                x: { field: 'label', type: 'nominal' },
                y: { field: 'count', type: 'quantitative' }
              }
            }]
          }]
        }
      },
      sources: {
        summary: {
          source: 'summary',
          rows: [{ label: 'Available', count: 1 }],
          metadata: {
            'source-id': 'summary-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-16T10:00:00Z',
            'retrieved-at': '2026-09-16T10:00:00Z',
            availability: 'available',
            completeness: 'complete',
            freshness: 'fresh'
          }
        }
      }
    });

    const page = await activatePage(rendered, 'summary');
    expect(page?.querySelector(':scope > .page-chrome')).toBeNull();
    expect(page?.getAttribute('data-view-mode')).toBe('chart');
  });

  it('includes the template default view mode in the initial page source request', async () => {
    const loadPageSources = vi.fn(
      /** @type {NonNullable<Parameters<typeof renderDashboard>[0]['loadPageSources']>} */ (async () => ({}))
    );
    renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'initial-view-mode',
          title: 'Initial view mode',
          pages: [{
            id: 'runs',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Runs',
            views: [
              { id: 'trend', mark: 'chart', data: { source: 'run-trend' } },
              { id: 'rows', mark: 'table', data: { source: 'runs' } }
            ]
          }]
        }
      },
      sources: {},
      loadPageSources
    });

    await vi.waitFor(() => expect(loadPageSources).toHaveBeenCalled());
    const initialOptions = loadPageSources.mock.calls.at(0)?.[1];
    expect(initialOptions?.queryContext).toEqual({ viewMode: 'chart' });
  });

  it('switches a standalone full-view lazy table between table and card-list modes', () => {
    window.localStorage.clear();
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'mobile-table-card-dashboard',
        title: 'Mobile Table Cards',
        pages: [{
          id: 'repositories',
          kind: /** @type {'custom'} */ ('custom'),
          title: 'Repositories',
          views: [{
            id: 'repositories-table',
            title: 'Repositories',
            data: { source: 'repositories' },
            mark: 'table',
            controls: 'interactive',
            'lazy-list': true,
            layout: 'full-view',
            encoding: {
              columns: [
                { field: 'repository-coordinate', type: 'nominal' },
                { field: 'organization', type: 'nominal' }
              ]
            }
          }]
        }],
        'card-templates': [{
          id: 'repository',
          icon: 'repo',
          title: { field: 'repository-coordinate' },
          labels: [],
          details: [{ field: 'organization' }]
        }]
      }
    };
    const rendered = renderDashboard({
      document,
      sources: {
        repositories: {
          source: 'repositories',
          rows: [{ 'repository-coordinate': 'githubnext/gh-cao', organization: 'githubnext' }],
          metadata: {
            'source-id': 'repositories-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-16T10:00:00Z',
            'retrieved-at': '2026-09-16T10:00:00Z',
            availability: 'available',
            completeness: 'complete',
            freshness: 'fresh'
          }
        }
      }
    });
    const modeButtons = [...rendered.querySelectorAll('[data-view-mode-value]')];

    expect(rendered.querySelector('[data-page-id="repositories"]')?.getAttribute('data-view-mode')).toBe('table');
    expect(modeButtons.map((button) => button.textContent)).toEqual(['Cards', 'Table']);
    expect(modeButtons[1]?.getAttribute('aria-pressed')).toBe('true');
    expect(rendered.querySelector('[data-mobile-card-list] .entity-card-list-card')?.textContent).toContain('githubnext/gh-cao');
  });



  it('keeps logical parent navigation available when a secondary page is loaded directly', () => {
    window.history.replaceState(null, '', '/#page-overview-failed-runs');
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    const back = /** @type {HTMLButtonElement | null} */ (rendered.querySelector('.mobile-history-back'));

    expect(back?.hidden).toBe(false);
    expect(back?.getAttribute('aria-label')).toBe('Back to Overview');
    back?.click();

    expect(window.location.hash).toBe('#page-overview');
    expect(rendered.querySelector('[data-page-id="overview"]')?.hasAttribute('hidden')).toBe(false);
    window.history.replaceState(null, '', '/');
  });



  it('returns directly loaded primary pages to Overview without the Navigation API', () => {
    window.history.replaceState(null, '', '/#page-cost');
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    const back = /** @type {HTMLButtonElement} */ (rendered.querySelector('.mobile-history-back'));
    expect(back.hidden).toBe(false);
    expect(back.getAttribute('aria-label')).toBe('Back to Overview');
    back.click();
    expect(window.location.hash).toBe('#page-overview');
    expect(rendered.querySelector('[data-page-id="overview"]')?.hasAttribute('hidden')).toBe(false);
    expect(back.hidden).toBe(true);
    window.history.replaceState(null, '', '/');
  });

  it('hides back on Overview even when dashboard history exists', () => {
    window.history.replaceState({ centralAgenticOpsNavigationIndex: 2 }, '', '/#page-overview');
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    expect(rendered.querySelector('.mobile-history-back')?.hasAttribute('hidden')).toBe(true);
    window.history.replaceState(null, '', '/');
  });

  it('keeps back visible without dashboard history and falls back to Overview', () => {
    window.history.replaceState(null, '', '/#page-cost');
    let currentIndex = 1;
    const entries = [
      { index: 0, url: 'https://github.com/githubnext/gh-cao' },
      { index: 1, url: window.location.href },
      { index: 2, url: `${window.location.origin}/#page-security` }
    ];
    const navigation = new EventTarget();
    Object.defineProperties(navigation, {
      currentEntry: { get: () => ({ index: currentIndex }) },
      entries: { value: () => entries }
    });
    Object.defineProperty(window, 'navigation', { configurable: true, value: navigation });

    try {
      const rendered = renderDashboard({
        document: authoritativeDashboardDocument,
        sources: {}
      });
      const back = /** @type {HTMLButtonElement | null} */ (rendered.querySelector('.mobile-history-back'));

      expect(back?.hidden).toBe(false);
      expect(back?.getAttribute('aria-label')).toBe('Back to Overview');
      currentIndex = 2;
      navigation.dispatchEvent(new Event('currententrychange'));
      expect(back?.hidden).toBe(false);
      expect(back?.getAttribute('aria-label')).toBe('Go back');
      currentIndex = 1;
      navigation.dispatchEvent(new Event('currententrychange'));
      expect(back?.hidden).toBe(false);
      const historyBack = vi.spyOn(window.history, 'back');
      back?.click();
      expect(historyBack).not.toHaveBeenCalled();
      historyBack.mockRestore();
      expect(window.location.hash).toBe('#page-overview');
      expect(back?.hidden).toBe(true);
    } finally {
      delete /** @type {Window & { navigation?: unknown }} */ (window).navigation;
      window.history.replaceState(null, '', '/');
    }
  });

  it('lets full-screen child views delegate back navigation to the app chrome', () => {
    window.history.replaceState(null, '', '/#page-runs');
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    document.body.append(rendered);
    const page = /** @type {HTMLElement} */ (rendered.querySelector('.dashboard-page:not([hidden])'));
    const back = /** @type {HTMLButtonElement} */ (rendered.querySelector('.mobile-history-back'));

    page.dispatchEvent(new CustomEvent('dashboard-route-parent-change', {
      bubbles: true,
      detail: { navigationPage: 'cost' }
    }));
    expect(back.hidden).toBe(false);
    expect(back.getAttribute('aria-label')).toBe('Back to Cost');

    page.dispatchEvent(new CustomEvent('dashboard-route-parent-change', {
      bubbles: true,
      detail: { navigationPage: '' }
    }));
    expect(back.hidden).toBe(false);
    expect(back.getAttribute('aria-label')).toBe('Back to Overview');
    window.history.replaceState(null, '', '/');
  });

  it('registers full-screen child history with app navigation', () => {
    window.history.replaceState(null, '', '/#page-runs');
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    document.body.append(rendered);
    const page = /** @type {HTMLElement} */ (rendered.querySelector('.dashboard-page:not([hidden])'));
    const back = /** @type {HTMLButtonElement} */ (rendered.querySelector('.mobile-history-back'));

    page.dispatchEvent(new CustomEvent('dashboard-history-push', {
      bubbles: true,
      cancelable: true,
      detail: { state: { caoMemoryViewer: 'test' } }
    }));

    expect(window.history.state).toMatchObject({
      caoMemoryViewer: 'test',
      centralAgenticOpsNavigationIndex: 1
    });
    expect(back.hidden).toBe(false);
    expect(back.getAttribute('aria-label')).toBe('Go back');
    window.history.replaceState(null, '', '/');
  });

  it('closes the mobile view menu on Escape and restores focus to its toggle', () => {
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    document.body.append(rendered);
    const menu = /** @type {HTMLDetailsElement | null} */ (rendered.querySelector('.mobile-nav-menu'));
    const summary = menu?.querySelector('summary');

    menu?.setAttribute('open', '');
    menu?.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));

    expect(menu?.hasAttribute('open')).toBe(false);
    expect(rendered.ownerDocument.activeElement).toBe(summary);
    rendered.remove();
  });



  it('applies the JSON horizon without database counts', () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'horizon-dashboard',
          title: 'Horizon Dashboard',
          horizon: {
            label: 'Data horizon',
            tooltip: {
              label: 'Data horizon details',
              description: 'Data is included from the start up to the exclusive end.',
              icon: 'question'
            }
          },
          defaults: { time: { range: '1w' } },
          pages: [{
            id: 'runs',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Runs',
            views: [{
              id: 'recent-runs',
              title: 'Recent runs',
              data: { source: 'runs' },
              mark: 'table',
              encoding: { columns: [{ field: 'run' }] }
            }]
          }]
        }
      },
      sources: {
        runs: {
          source: 'runs',
          rows: [
            { run: 'recent', 'observed-at': '2026-08-30T12:00:00Z' },
            { run: 'expired', 'observed-at': '2026-08-20T12:00:00Z' },
            { run: 'timeless' }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-01T12:00:00Z',
            'retrieved-at': '2026-09-01T12:00:00Z',
            'coverage-start': '2026-08-30T12:30:00Z',
            'coverage-end': '2026-09-01T12:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    const table = rendered.querySelector('.custom-table');
    expect(table?.textContent).toContain('recent');
    expect(table?.textContent).toContain('timeless');
    expect(table?.textContent).not.toContain('expired');
    expect(rendered.querySelector('.dashboard-horizon')?.getAttribute('data-dashboard-evaluated-at')).toBe('2026-09-01T12:00:00.000Z');
    expect(rendered.querySelector('.horizon-toggle')?.getAttribute('aria-label')).toContain('2 days');
    expect(rendered.querySelector('.dashboard-horizon .horizon-details')?.textContent).toBe(
      'Coverage detailsData is included from the start up to the exclusive end.StartAug 30, 2026, 12:30 PM UTCEndSep 1, 2026, 12:00 PM UTCDuration2 days'
    );
    expect(rendered.querySelector('.dashboard-horizon .horizon-details time:first-of-type')?.getAttribute('datetime')).toBe('2026-08-30T12:30:00.000Z');
    expect(rendered.querySelectorAll('.dashboard-horizon .horizon-details time')[1]?.getAttribute('datetime')).toBe('2026-09-01T12:00:00.000Z');
    expect(rendered.querySelector('.horizon-tooltip-counts')).toBeNull();
  });

  it('keeps a persisted display horizon stable while source-free page queries bind', async () => {
    const previous = window.localStorage.getItem(HORIZON_FILTER_STORAGE_KEY);
    window.localStorage.setItem(HORIZON_FILTER_STORAGE_KEY, JSON.stringify({ range: '30d' }));
    let clock = Date.parse('2026-10-15T08:00:00Z');
    const now = vi.spyOn(Date, 'now').mockImplementation(() => clock += 1000);
    const loadPageSources = vi.fn(async (_pageId, _options) => {
      if (loadPageSources.mock.calls.length > 4) window.localStorage.removeItem(HORIZON_FILTER_STORAGE_KEY);
      return {};
    });
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'stable-horizon', title: 'Stable horizon',
          defaults: { time: { range: '1w' } },
          pages: [{
            id: 'runs', kind: 'custom', title: 'Runs', 'filter-bar': true,
            views: [{ id: 'runs-table', data: { source: 'runs' }, mark: 'table',
              encoding: { columns: [{ field: 'run' }] } }]
          }]
        }
      },
      sources: {}, loadPageSources
    });
    document.body.append(rendered);
    try {
      await vi.waitFor(() => expect(loadPageSources).toHaveBeenCalledTimes(2));
      await new Promise((resolve) => setTimeout(resolve, 10));
      expect(loadPageSources).toHaveBeenCalledTimes(2);
      const window = loadPageSources.mock.calls.at(-1)?.[1].queryContext?.timeWindow;
      expect(Date.parse(window.end) - Date.parse(window.start)).toBe(30 * 86_400_000);
      expect(rendered.querySelector('.horizon-toggle')?.getAttribute('aria-label')).toContain('30 days');
      expect([...rendered.querySelectorAll('.dashboard-horizon time')].map((time) => time.getAttribute('datetime')))
        .toEqual([window.start, window.end]);
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
      now.mockRestore();
      if (previous === null) globalThis.window.localStorage.removeItem(HORIZON_FILTER_STORAGE_KEY);
      else globalThis.window.localStorage.setItem(HORIZON_FILTER_STORAGE_KEY, previous);
    }
  });

  it('renders the configured Horizon immediately and updates it when page sources load', async () => {
    let publishUpdate = () => {};
    const loadPageSources = vi.fn(async (_pageId, options) => {
      publishUpdate = () => options.onUpdate({
        runs: {
          source: 'runs',
          rows: [{ run: '1', 'observed-at': '2026-09-01T11:00:00Z' }],
          metadata: {
            'source-id': 'runs',
            'source-kind': 'canonical-query',
            'as-of': '2026-09-01T12:00:00Z',
            'retrieved-at': '2026-09-01T12:00:00Z',
            'coverage-start': '2026-08-31T12:00:00Z',
            'coverage-end': '2026-09-01T12:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      });
      return {};
    });
    const rendered = renderDashboardView({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'horizon-dashboard',
          title: 'Horizon dashboard',
          defaults: { time: { range: '1w' } },
          pages: [{
            id: 'runs',
            kind: 'custom',
            title: 'Runs',
            views: [{
              id: 'runs-table',
              data: { source: 'runs' },
              mark: 'table',
              encoding: { columns: [{ field: 'run' }] }
            }]
          }]
        }
      },
      sources: {},
      loadPageSources
    });

    expect(rendered.querySelector('.dashboard-horizon-skeleton')).toBeNull();
    expect(rendered.querySelector('.horizon-toggle')?.getAttribute('aria-label')).toContain('1 week');
    await vi.waitFor(() => expect(loadPageSources).toHaveBeenCalled());
    publishUpdate();

    const horizon = rendered.querySelector('.dashboard-horizon');
    expect(horizon?.classList.contains('dashboard-horizon-skeleton')).toBe(false);
    expect(horizon?.querySelector('.horizon-toggle')?.getAttribute('aria-label')).toContain('1 day');
    expect(horizon?.getAttribute('data-dashboard-evaluated-at')).toBe('2026-09-01T12:00:00.000Z');
    expect(rendered.querySelector('.dashboard-horizon .horizon-details')).not.toBeNull();

    optionsOnUpdateWithNoSources();
    expect(horizon?.classList.contains('dashboard-horizon-skeleton')).toBe(false);

    function optionsOnUpdateWithNoSources() {
      const latestOptions = loadPageSources.mock.calls.at(-1)?.[1];
      expect(latestOptions).toBeDefined();
      latestOptions.onUpdate({});
    }
  });



  it('DLS-VIEW-018 DLS-VIEW-019 DLS-VIEW-020 progressively discloses supplemental views in source order', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'progressive-disclosure-dashboard',
        title: 'Progressive Disclosure',
        pages: [
          {
            id: 'runs',
            kind: /** @type {'custom'} */ ('custom'),
            views: [
              {
                id: 'run-count',
                title: 'Run count',
                data: { source: 'runs' },
                mark: 'metric',
                encoding: { value: { field: 'run', aggregate: 'count' } }
              },
              {
                id: 'completed-runs',
                title: 'Completed runs',
                disclosure: 'supplemental',
                data: { source: 'runs', filters: { 'run-status': 'completed' } },
                mark: 'metric',
                encoding: { value: { field: 'run', aggregate: 'count' } }
              }
            ]
          }
        ]
      }
    };

    const rendered = renderDashboard({
      document,
      sources: {
        runs: {
          source: 'runs',
          rows: [
            { run: '1', 'run-status': 'completed' },
            { run: '2', 'run-status': 'queued' }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    const views = rendered.querySelectorAll('[data-page-id="runs"] > .custom-view-grid > .custom-view');
    expect(views).toHaveLength(2);
    expect(views[0]?.getAttribute('data-disclosure')).toBe('essential');
    const supplemental = /** @type {HTMLDetailsElement} */ (views[1]);
    expect(supplemental.tagName).toBe('DETAILS');
    expect(supplemental.getAttribute('data-disclosure')).toBe('supplemental');
    expect(supplemental.open).toBe(false);
    expect(supplemental.querySelector('summary')?.textContent).toContain('Completed runs');
    const supplementalContent = supplemental.querySelector(':scope > .page-section');
    expect(supplementalContent?.textContent).toContain('1');
    expect(supplementalContent?.classList.contains('custom-view')).toBe(false);
    expect(supplementalContent?.hasAttribute('data-view-layout')).toBe(false);
  });



  it('DLS-PAGE-014 DLS-PAGE-015 renders mode-filtered campaign AIC utilization and campaign-run trends', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'campaigns-dashboard',
        title: 'Campaigns Dashboard',
        pages: [{
          id: 'campaigns',
          kind: /** @type {'built-in'} */ ('built-in'),
          page: 'campaigns',
          title: 'Campaigns',
          description: 'Activity from centrally managed campaigns.',
          definition: {
            'data-state': { availability: true },
            views: [
              { id: 'campaign-workflows', data: { source: 'workflows' } },
              { id: 'campaign-runs', data: { source: 'runs' } },
              { id: 'campaign-outcomes', data: { source: 'outcomes' } },
              { id: 'campaign-usage', data: { source: 'usage' } },
              { id: 'campaigns-utilization', title: 'Campaign AIC utilization', data: { sources: ['workflows', 'usage'] }, mark: 'element', element: 'campaign-utilization' },
              { id: 'campaigns-run-trend', title: 'All runs over time', data: { sources: ['workflows', 'runs', 'outcomes'] }, mark: 'element', element: 'campaign-run-trend' },
              { id: 'campaigns-summary', title: 'All output by campaign', data: { sources: ['workflows', 'usage', 'findings', 'outcomes', 'runs'] }, mark: 'element', element: 'campaign-summary-table' }
            ]
          }
        }]
      }
    };
    const metadata = {
      'source-id': 'campaigns-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-08-29T20:00:00Z',
      'retrieved-at': '2026-08-29T20:01:00Z',
      completeness: /** @type {'complete'} */ ('complete'),
      freshness: /** @type {'fresh'} */ ('fresh'),
      availability: /** @type {'available'} */ ('available')
    };
    const rendered = renderDashboard({
      document,
      sources: {
        campaigns: {
          source: 'campaigns',
          rows: [
            { campaign: 'daily-ops', 'campaign-name': 'Daily Ops' },
            { campaign: 'empty-ops', 'campaign-name': 'Empty Ops' }
          ],
          metadata
        },
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'github', repository: 'gh-cao', campaign: 'daily-ops', 'campaign-name': 'Daily Ops', 'campaign-icon': 'workflow', workflow: '.github/workflows/daily.md', 'workflow-role': 'orchestrator', 'rollout-mode': 'review', 'max-ai-credits': 100, 'campaign-aic-allowance': 250, 'campaign-inventory-warnings': 2 },
            { organization: 'github', repository: 'gh-cao', campaign: 'daily-ops', 'campaign-name': 'Daily Ops', 'campaign-icon': 'workflow', workflow: '.github/workflows/daily-worker.md', 'workflow-role': 'worker', 'rollout-mode': 'review', 'max-ai-credits': 150, 'campaign-aic-allowance': 250, 'campaign-inventory-warnings': 2 },
            { organization: 'github', repository: 'gh-cao', campaign: 'empty-ops', 'campaign-name': 'Empty Ops', workflow: '.github/workflows/empty.md', 'workflow-role': 'orchestrator', 'rollout-mode': 'live', 'max-ai-credits': 80, 'inventory-ready': true }
          ],
          metadata
        },
        runs: {
          source: 'runs',
          rows: [
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/daily.md', run: '1', 'started-at': '2026-08-28T10:00:00Z', 'run-conclusion': 'success', 'rollout-mode': 'review', 'aic-total': 10 },
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/daily-worker.md', run: '2', 'started-at': '2026-08-29T10:00:00Z', 'run-conclusion': 'failure', 'rollout-mode': 'live', 'aic-total': 30 },
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/unmanaged.md', run: '3', 'started-at': '2026-08-29T11:00:00Z', 'run-conclusion': 'cancelled', 'rollout-mode': 'review' }
          ],
          metadata
        },
        outcomes: {
          source: 'outcomes',
          rows: [
            { campaign: 'daily-ops', run: '1', 'run-conclusion': 'success', 'rollout-mode': 'review', 'published-at': '2026-08-28T10:00:00Z', 'observed-at': '2026-08-28T10:00:00Z' },
            { campaign: 'daily-ops', run: '2', 'rollout-mode': 'live', 'published-at': '2026-08-29T10:00:00Z', 'observed-at': '2026-08-29T10:05:00Z' },
            { campaign: 'daily-ops', run: '2', 'run-conclusion': 'failure', 'rollout-mode': 'live', 'published-at': '2026-08-29T10:00:00Z', 'observed-at': '2026-08-29T10:06:00Z' },
            { campaign: 'daily-ops', run: 'old', 'run-conclusion': 'success', 'rollout-mode': 'review', 'published-at': '2026-07-01T10:00:00Z', 'observed-at': '2026-07-01T10:00:00Z' }
          ],
          metadata
        },
        usage: {
          source: 'usage',
          rows: [
            { workflow: '.github/workflows/daily.md', run: '1', invocation: 'a', aic: 4, 'rollout-mode': 'review' },
            { workflow: '.github/workflows/daily.md', run: '1', invocation: 'b', aic: 6, 'rollout-mode': 'review' },
            { workflow: '.github/workflows/daily-worker.md', run: '2', invocation: 'c', aic: 30, 'rollout-mode': 'live' }
          ],
          metadata: { ...metadata, completeness: /** @type {'partial'} */ ('partial') }
        },
        findings: {
          source: 'findings',
          rows: [
            { workflow: '.github/workflows/daily-worker.md', run: '2', finding: 'warning-1', 'finding-kind': 'authored-warning', 'observed-at': '2026-08-29T10:05:00Z' },
            { workflow: '.github/workflows/daily-worker.md', run: '2', finding: 'warning-2', 'finding-kind': 'authored-warning', 'observed-at': '2026-08-29T10:06:00Z' }
          ],
          metadata
        }
      }
    });

    const campaignsPage = rendered.querySelector('[data-page-name="campaigns"]');
    expect(campaignsPage?.querySelector('[data-view-layout="full-view"]')).not.toBeNull();
    expect(campaignsPage?.querySelector('[data-table-filter]')).not.toBeNull();
    const campaignSummaryRows = [...(campaignsPage?.querySelectorAll('.custom-table tbody tr') ?? [])];
    expect(campaignSummaryRows).toHaveLength(2);
    expect(campaignSummaryRows[0]?.textContent).toContain('Daily Ops');
    expect(campaignSummaryRows[0]?.textContent).toContain('40');
    expect(campaignSummaryRows[1]?.textContent).toContain('Empty Ops');
    expect(/** @type {HTMLElement | null} */ (campaignsPage?.querySelector('.data-state-summary'))?.hidden).toBe(true);

  });

  it('DLS-SEM-022 DLS-SEM-023 DLS-PAGE-014 DLS-PAGE-015 keeps campaigns repository-scoped and distinguishes unknown or unavailable telemetry', () => {
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'repository-scoped-campaigns',
        title: 'Repository-scoped campaigns',
        pages: [{
          id: 'campaigns',
          kind: /** @type {'built-in'} */ ('built-in'),
          page: 'campaigns',
          title: 'Campaigns'
        }]
      }
    };
    const metadata = {
      'source-id': 'campaigns-fixture',
      'source-kind': 'fixture',
      'as-of': '2026-08-29T20:00:00Z',
      'retrieved-at': '2026-08-29T20:01:00Z',
      completeness: /** @type {'complete'|'unknown'} */ ('complete'),
      freshness: /** @type {'fresh'} */ ('fresh'),
      availability: /** @type {'available'|'unavailable'} */ ('available')
    };
    const workflows = {
      source: 'workflows',
      rows: [
        { organization: 'octo-org', repository: 'alpha', campaign: 'daily-ops', 'campaign-name': 'Daily Ops', workflow: '.github/workflows/daily.md', 'workflow-role': 'orchestrator', 'max-ai-credits': 100, 'campaign-aic-allowance': 100 },
        { organization: 'octo-org', repository: 'beta', campaign: 'daily-ops', 'campaign-name': 'Daily Ops', workflow: '.github/workflows/daily.md', 'workflow-role': 'orchestrator', 'max-ai-credits': 200, 'campaign-aic-allowance': 999 }
      ],
      metadata
    };
    const runs = {
      source: 'runs',
      rows: [
        { organization: 'octo-org', repository: 'alpha', workflow: '.github/workflows/daily.md', run: '1', 'started-at': '2026-08-29T10:00:00Z', 'run-conclusion': 'success', 'rollout-mode': 'review', 'aic-total': 10 },
        { organization: 'octo-org', repository: 'beta', workflow: '.github/workflows/daily.md', run: '2', 'started-at': '2026-08-29T11:00:00Z', 'run-conclusion': 'failure', 'rollout-mode': 'review', 'aic-total': 20 }
      ],
      metadata
    };
    const usage = {
      source: 'usage',
      rows: [
        { organization: 'octo-org', repository: 'alpha', workflow: '.github/workflows/daily.md', run: '1', invocation: 'a', aic: 10, 'rollout-mode': 'review' },
        { organization: 'octo-org', repository: 'beta', workflow: '.github/workflows/daily.md', run: '2', invocation: 'b', aic: 20, 'rollout-mode': 'review' }
      ],
      metadata: { ...metadata, completeness: /** @type {'unknown'} */ ('unknown') }
    };

    const campaigns = {
      source: 'campaigns',
      rows: [
        { campaign: 'daily-ops', 'campaign-name': 'Daily Ops' }
      ],
      metadata
    };
    const rendered = renderDashboard({ document, sources: { campaigns, workflows, runs, usage } });
    const campaignsPage = rendered.querySelector('[data-page-name="campaigns"]');
    const rows = [...(campaignsPage?.querySelectorAll('.custom-table tbody tr') ?? [])];
    expect(rows).toHaveLength(1);
    expect(rows[0]?.textContent).toContain('30');
    expect(campaignsPage?.querySelector('[data-table-filter]')).not.toBeNull();

    const unavailable = renderDashboard({
      document,
      sources: {
        campaigns,
        workflows,
        runs: { ...runs, rows: [], metadata: { ...metadata, availability: /** @type {'unavailable'} */ ('unavailable'), completeness: /** @type {'unknown'} */ ('unknown') } },
        usage
      }
    });
    const unavailableCampaignsPage = unavailable.querySelector('[data-page-name="campaigns"]');
    expect(unavailableCampaignsPage?.querySelector('.custom-table')).not.toBeNull();
  });


  it('DLS-PAGE-002 DLS-PAGE-006 DLS-PAGE-008 DLS-PAGE-009 DLS-PAGE-010 DLS-PAGE-011 DLS-PAGE-012 DLS-PAGE-013 DLS-PAGE-014 renders built-in sections in authoritative dashboard.json view order grouped by declared source instead of hard-coded section index positions', async () => {
    const clock = vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-08-29T20:01:00Z'));
    onTestFinished(() => clock.mockRestore());
    /** @type {import('../../src/presenter.js').PresentationInput['document']} */
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'view-order-dashboard',
        title: 'View Order Dashboard',
        pages: [
          {
            id: 'runs',
            kind: /** @type {'built-in'} */ ('built-in'),
            page: 'runs',
            title: 'Runs',
            definition: {
              'data-state': {
                availability: true
              },
              views: [
                { id: 'runs-table', title: 'Runs Inventory First', data: { source: 'runs' } },
                { id: 'runs-status', title: 'Run Status Second', data: { source: 'runs' } },
                { id: 'outcome-counts', title: 'Outcome Counts Third', data: { source: 'outcomes' } },
                { id: 'run-conclusions', title: 'Run Conclusions Fourth', data: { source: 'runs' } }
              ]
            }
          }
        ]
      }
    };

    const rendered = renderDashboard({
      document,
      sources: await loadDatabaseQuerySources(new IDBFactory(), {
        runs: {
          source: 'runs',
          rows: [
            {
              organization: 'github',
              repository: 'gh-cao',
              workflow: '.github/workflows/daily.yml',
              run: '1001',
              'run-status': 'completed',
              'run-conclusion': 'success',
              'rollout-mode': 'live',
              engine: 'actions',
              'requested-model': 'gpt-4o',
              'resolved-model': 'gpt-4.1',
              'started-at': '2026-08-29T10:00:00Z'
            }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        outcomes: {
          source: 'outcomes',
          rows: [
            {
              run: '1001',
              'outcome-state': 'accepted'
            }
          ],
          metadata: {
            'source-id': 'outcomes-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }, {
        ingest: true, queries: authoritativeDashboardDocument.dashboard.queries,
        sourceNames: ['runs-table', 'runs-daily-conclusions']
      })
    });

    const headings = [...rendered.querySelectorAll('[data-page-id="runs"] .page-section > h2, [data-page-id="runs"] .page-section > .chart-prompt-heading > h2')].map((element) => element.textContent);
    expect(headings).toEqual(['Runs in the last week', 'Runs']);
    expect(rendered.querySelectorAll('[data-page-id="runs"] [data-chart-widget="area"]')).toHaveLength(1);
    expect(rendered.querySelectorAll('[data-page-id="runs"] .custom-table')).toHaveLength(1);
    expect(rendered.querySelector('[data-page-id="runs"]')?.getAttribute('data-page-kind')).toBe('custom');
  });

  it('DLS-SAFE-003 DLS-SAFE-004 DLS-SAFE-007 DLS-SAFE-010 renders non-empty accessible names and inert text labels while preserving only safe https external link attributes', () => {
    /** @type {import('../../src/presenter.js').PresentationInput['document']} */
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'findings-dashboard',
        title: 'Security Dashboard',
        pages: [
          {
            id: 'finding-review',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Findings',
            views: [{
              id: 'finding-review-table',
              data: { source: 'findings' },
              mark: 'table',
              encoding: {
                columns: [
                  { field: 'finding-summary', type: 'nominal' },
                  { field: 'issue-link', type: 'nominal' }
                ],
                href: { field: 'issue-link', type: 'nominal' }
              }
            }]
          }
        ]
      }
    };

    const rendered = renderDashboard({
      document,
      sources: {
        findings: {
          source: 'findings',
          rows: [
            {
              finding: 'unsafe-html',
              'finding-summary': '<img src=x onerror=alert(1)>',
              'finding-severity': 'critical',
              'finding-status': 'open',
              organization: 'github',
              repository: 'gh-cao',
              workflow: '.github/workflows/daily.yml',
              'observed-at': '2026-08-29T12:00:00Z',
              'issue-link': {
                relation: 'issue',
                href: 'https://example.com/issues/1',
                label: 'Issue 1 label'
              }
            }
          ],
          metadata: {
            'source-id': 'findings-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    expect(rendered.querySelector('#page-title')?.textContent).toBe('Findings');
    expect(rendered.querySelector('.sidebar-brand > span')?.textContent).toBe('github');
    expect(rendered.querySelector('[data-page-id="finding-review"] .custom-table thead')?.textContent).toContain('Issue Link');

    const summaryCell = rendered.querySelector('[data-page-id="finding-review"] .custom-table tbody td');
    expect(summaryCell?.textContent).toContain('<img src=x onerror=alert(1)>');
    expect(summaryCell?.querySelector('img')).toBeNull();

    const issueLink = rendered.querySelector('[data-page-id="finding-review"] .custom-table tbody a');
    expect(issueLink?.getAttribute('href')).toBe('https://example.com/issues/1');
    expect(issueLink?.getAttribute('aria-label')).toBe('Issue 1 label');
    expect(issueLink?.getAttribute('target')).toBe('_blank');
    expect(issueLink?.getAttribute('rel')).toBe('noopener noreferrer');
    expect(issueLink?.textContent).toBe('<img src=x onerror=alert(1)>');
  });

  it('DLS-VIEW-013 DLS-VIEW-014 DLS-VIEW-015 DLS-SAFE-006 renders custom views with available, empty, and unavailable states while exposing only context-permitted observations and links', () => {
    /** @type {import('../../src/presenter.js').PresentationInput['document']} */
    const document = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'custom-dashboard',
        title: 'Custom Dashboard',
        pages: [
          {
            id: 'custom-views',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Custom Views',
            views: [
              {
                id: 'total-aic',
                title: 'Total AI Credits',
                data: {
                  source: 'usage',
                  filters: {
                    'rollout-mode': ['review', 'live']
                  }
                },
                mark: 'metric',
                encoding: {
                  value: {
                    field: 'aic',
                    type: 'quantitative',
                    aggregate: 'sum'
                  }
                }
              },
              {
                id: 'findings-table',
                title: 'Findings Table',
                data: {
                  source: 'findings',
                  scope: {
                    repositories: ['gh-cao']
                  },
                  time: {
                    start: '2026-08-29T00:00:00Z',
                    end: '2026-08-30T00:00:00Z'
                  }
                },
                mark: 'table',
                encoding: {
                  columns: [
                    { field: 'finding-summary' },
                    { field: 'finding-severity' },
                    { field: 'finding-status' }
                  ],
                  href: {
                    field: 'pull-request-link'
                  }
                }
              },
              {
                id: 'daily-runs',
                title: 'Daily Runs',
                data: {
                  source: 'runs'
                },
                mark: 'chart',
                encoding: {
                  x: {
                    field: 'started-at',
                    type: 'temporal',
                    'time-unit': 'day'
                  },
                  y: {
                    field: 'run',
                    type: 'quantitative',
                    aggregate: 'count'
                  },
                  color: {
                    field: 'run-conclusion',
                    type: 'nominal'
                  },
                  href: {
                    field: 'run-link'
                  }
                }
              },
              {
                id: 'empty-usage',
                title: 'Empty Usage',
                data: {
                  source: 'empty-usage'
                },
                mark: 'metric',
                encoding: {
                  value: {
                    field: 'aic',
                    type: 'quantitative',
                    aggregate: 'sum'
                  }
                }
              },
              {
                id: 'missing-source',
                title: 'Missing Source',
                data: {
                  source: 'missing-source'
                },
                mark: 'table',
                encoding: {
                  columns: [
                    { field: 'finding-summary' }
                  ]
                }
              },
              {
                id: 'missing-element-source',
                title: 'Missing Element Source',
                mark: 'element',
                element: 'control-plane-status'
              }
            ]
          }
        ]
      }
    };

    const rendered = renderDashboard({
      document,
      sources: {
        usage: {
          source: 'usage',
          rows: [
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/daily.yml', run: '1001', engine: 'actions', 'requested-model': 'gpt-4o', 'resolved-model': 'gpt-4.1', 'rollout-mode': 'live', aic: 2, 'observed-at': '2026-08-29T10:00:00Z' },
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/daily.yml', run: '1002', engine: 'actions', 'requested-model': 'gpt-4o', 'resolved-model': 'gpt-4.1', 'rollout-mode': 'review', aic: 3, 'observed-at': '2026-08-29T11:00:00Z' }
          ],
          metadata: {
            'source-id': 'usage-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        findings: {
          source: 'findings',
          rows: [
            {
              code: 'finding-1',
              organization: 'github',
              repository: 'gh-cao',
              'observed-at': '2026-08-29T12:00:00Z',
              'finding-summary': 'Unsafe dependency',
              'finding-severity': 'high',
              'finding-status': 'open',
              'pull-request-link': {
                relation: 'pull-request',
                href: 'https://example.com/pull/1',
                label: 'PR 1'
              }
            },
            {
              code: 'finding-2',
              organization: 'github',
              repository: 'other-repo',
              'observed-at': '2026-08-29T13:00:00Z',
              'finding-summary': 'Out of scope finding',
              'finding-severity': 'medium',
              'finding-status': 'resolved',
              'pull-request-link': {
                relation: 'pull-request',
                href: 'https://example.com/pull/2',
                label: 'PR 2'
              }
            },
            {
              code: 'finding-3',
              organization: 'github',
              repository: 'gh-cao',
              'observed-at': '2026-08-30T01:00:00Z',
              'finding-summary': 'Out of range finding',
              'finding-severity': 'low',
              'finding-status': 'open'
            }
          ],
          metadata: {
            'source-id': 'findings-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'partial',
            freshness: 'stale',
            availability: 'available'
          }
        },
        runs: {
          source: 'runs',
          rows: [
            {
              run: '1001',
              'started-at': '2026-08-29T10:00:00Z',
              'run-conclusion': 'success',
              'run-link': { relation: 'run', href: 'https://github.com/github/central-agentic-ops/actions/runs/1001', label: 'Run 1001' }
            },
            {
              run: '1002',
              'started-at': '2026-08-29T11:00:00Z',
              'run-conclusion': 'failure',
              'run-link': { relation: 'run', href: 'https://github.com/github/central-agentic-ops/actions/runs/1002', label: 'Run 1002' }
            }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        'empty-usage': {
          source: 'empty-usage',
          rows: [],
          metadata: {
            'source-id': 'empty-usage-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'unknown',
            freshness: 'unknown',
            availability: 'empty'
          }
        }
      }
    });

    expect(rendered.querySelector('#page-title')?.textContent).toBe('Custom Views');

    const metricSection = [...rendered.querySelectorAll('.page-section')].find((section) => section.textContent?.includes('Total AI Credits'));
    expect(metricSection?.querySelector('[data-metric-value="aic"]')?.textContent).toBe('5');
    expect(metricSection?.textContent).not.toContain('Source: usage');
    expect(metricSection?.textContent).not.toContain('Filters:');

    const tableSection = [...rendered.querySelectorAll('.page-section')].find((section) => section.textContent?.includes('Findings Table'));
    const tableRows = tableSection ? tableSection.querySelectorAll('.custom-table tbody tr') : null;
    expect(tableRows).toHaveLength(1);
    const linkedCell = tableRows?.[0]?.querySelector('a');
    expect(linkedCell?.textContent).toBe('Unsafe dependency');
    expect(linkedCell?.getAttribute('aria-label')).toBe('PR 1');
    expect(tableSection?.textContent).not.toContain('Scope:');
    expect(tableSection?.textContent).not.toContain('Time:');
    expect(tableSection?.textContent).not.toContain('Out of scope finding');
    expect(tableSection?.textContent).not.toContain('Out of range finding');

    const chartSection = [...rendered.querySelectorAll('.page-section')].find((section) => section.textContent?.includes('Daily Runs'));
    const chartLegendLabels = chartSection ? [...chartSection.querySelectorAll('[data-chart-legend="visual"] li span')] : [];
    expect(chartSection?.querySelector('.chart-default')).toBeNull();
    expect(chartSection?.querySelector('[data-chart-legend="text"]')).toBeNull();
    expect(chartSection?.querySelectorAll('[data-chart-legend="visual"] li')).toHaveLength(2);
    expect(chartLegendLabels.map((item) => item.textContent)).toEqual(['failure', 'success']);
    expect(chartSection?.querySelector('.table-region')).toBeNull();
    expect(chartSection?.querySelectorAll('.view-source')).toHaveLength(0);

    /** @param {string} title */
    const sectionWithHeading = (title) => [...rendered.querySelectorAll('.page-section')]
      .find((section) => section.querySelector(':scope > :is(h2, h3, h4)')?.textContent === title);
    const emptySection = sectionWithHeading('Empty Usage');
    const emptyCard = emptySection?.querySelector('.view-state-card[data-view-state="empty"]');
    expect(emptyCard?.getAttribute('role')).toBe('status');
    expect(emptyCard?.querySelector('.octicon-info')).not.toBeNull();
    expect(emptySection?.querySelector('[data-view-availability="empty"]')?.textContent).toBe('No observations matched the effective context.');
    expect(emptySection?.textContent).toContain('Affected source: empty-usage');

    const unavailableSection = sectionWithHeading('Missing Source');
    const unavailableCard = unavailableSection?.querySelector('.view-state-card[data-view-state="unavailable"]');
    expect(unavailableCard?.getAttribute('role')).toBe('alert');
    expect(unavailableCard?.querySelector('.octicon-alert')).not.toBeNull();
    expect(unavailableSection?.querySelector('[data-view-availability="unavailable"]')?.textContent).toBe('This view cannot be shown because its data query failed.');
    expect(unavailableSection?.textContent).toContain('Affected source: missing-source');
    expect(unavailableSection?.textContent).toContain('Query diagnostic: $.dashboard.queries[view:custom-views:missing-source:missing-source]: input source "missing-source" is unavailable.');

    const missingElementSourceSection = sectionWithHeading('Missing Element Source');
    expect(missingElementSourceSection?.querySelector('[data-view-availability="unavailable"]')?.textContent).toBe('This view cannot be shown because its data source is unavailable.');
    expect(missingElementSourceSection?.textContent).toContain('No sources declared for element view.');
  });

  it('DLS-SAFE-007 DLS-SAFE-008 enables keyboard navigation across labeled page sections without relying on color alone', () => {
    /** @type {import('../../src/presenter.js').PresentationInput['document']} */
    const dashboardDocument = {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'runs-dashboard',
        title: 'Runs Dashboard',
        pages: [
          {
            id: 'keyboard-navigation',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Keyboard Navigation',
            views: [
              { id: 'runs-source', data: { source: 'runs' } },
              { id: 'outcomes-source', data: { source: 'outcomes' } }
            ]
          }
        ]
      }
    };

    const rendered = renderDashboard({
      document: dashboardDocument,
      sources: {
        runs: {
          source: 'runs',
          rows: [
            {
              organization: 'github',
              repository: 'gh-cao',
              workflow: '.github/workflows/daily.yml',
              run: '1001',
              'run-status': 'completed',
              'run-conclusion': 'success',
              'rollout-mode': 'live',
              engine: 'actions',
              'requested-model': 'gpt-4o',
              'resolved-model': 'gpt-4.1',
              'started-at': '2026-08-29T10:00:00Z',
              'run-link': {
                relation: 'run',
                href: 'https://example.com/runs/1001',
                label: 'Run 1001'
              }
            }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        outcomes: {
          source: 'outcomes',
          rows: [
            {
              run: '1001',
              'outcome-state': 'accepted'
            }
          ],
          metadata: {
            'source-id': 'outcomes-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    rendered.ownerDocument.body.append(rendered);
    enableDashboardKeyboardNavigation(rendered);

    const sections = rendered.querySelectorAll('[data-page-id="keyboard-navigation"] .page-section');
    expect(sections).toHaveLength(2);
    expect(sections[0]?.getAttribute('aria-labelledby')).toContain('keyboard-navigation-runs-source-heading');
    expect([...sections].map((section) => section.getAttribute('aria-labelledby'))).toEqual([
      'keyboard-navigation-runs-source-heading',
      'keyboard-navigation-outcomes-source-heading'
    ]);

    const firstSection = /** @type {HTMLElement} */ (sections[0]);
    const secondSection = /** @type {HTMLElement} */ (sections[1]);

    firstSection.focus();
    firstSection.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    expect(rendered.ownerDocument.activeElement).toBe(secondSection);

    secondSection.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }));
    expect(rendered.ownerDocument.activeElement).toBe(firstSection);
  });

  it('DLS-VIEW-005 DLS-VIEW-006 renders explicit line and pie widgets in the requested structural layout', () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'chart-dashboard',
          title: 'Chart Dashboard',
          pages: [{
            id: 'charts',
            kind: /** @type {'custom'} */ ('custom'),
            views: [
              {
                id: 'run-trend',
                title: 'Run Trend',
                data: { source: 'runs' },
                mark: 'chart',
                chart: 'line',
                layout: 'half',
                encoding: {
                  x: { field: 'started-at', type: 'temporal' },
                  y: { field: 'run-count', type: 'quantitative', aggregate: 'none' }
                }
              },
              {
                id: 'conclusions',
                title: 'Conclusions',
                description: 'Run conclusions grouped across the selected window.',
                data: { source: 'runs' },
                mark: 'chart',
                chart: 'pie',
                layout: 'half',
                encoding: {
                  x: { field: 'run-conclusion', type: 'nominal' },
                  y: { field: 'run', type: 'quantitative', aggregate: 'count' }
                }
              }
            ]
          }]
        }
      },
      sources: {
        runs: {
          source: 'runs',
          rows: [
            { organization: 'octo-org', repository: 'repo', run: '1', 'run-count': 2, 'started-at': '2026-08-28T00:00:00Z', 'run-conclusion': 'success' },
            { organization: 'octo-org', repository: 'repo', run: '2', 'run-count': 3, 'started-at': '2026-08-29T00:00:00Z', 'run-conclusion': 'failure' }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    expect(rendered.querySelectorAll('.custom-view-grid > [data-view-layout="half"]')).toHaveLength(2);
    expect(rendered.querySelector('[data-chart-widget="line"] polyline')?.getAttribute('points')).not.toBe('');
    expect(rendered.querySelectorAll('[data-chart-widget="line"] [role="img"][tabindex="0"]')).toHaveLength(2);
    expect(rendered.querySelector('[data-chart-widget="line"] [role="img"][tabindex="0"]')?.getAttribute('aria-label')).toContain(': 2');
    expect(rendered.querySelectorAll('[data-chart-widget="line"] .point-tooltip')).toHaveLength(2);
    expect(rendered.querySelector('[data-chart-widget="line"] .point-tooltip')?.getAttribute('aria-hidden')).toBe('true');
    expect(rendered.querySelectorAll('[data-chart-widget="pie"] [data-chart-category]')).toHaveLength(2);
    expect(rendered.querySelector('[data-chart-widget="pie"] svg')?.getAttribute('aria-label')).toContain('Pie chart:');
    expect(rendered.querySelector('.chart-view-pie .view-description')?.textContent).toContain('Run conclusions grouped');
    expect(rendered.querySelector('.chart-view-pie .pie-chart-layout')).not.toBeNull();
  });

  it('shows one hash-addressable page at a time and updates active navigation without scrolling', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'page-navigation',
          title: 'Page Navigation',
          pages: [
            {
              id: 'first',
              kind: /** @type {'custom'} */ ('custom'),
              title: 'First',
              description: 'First page description',
              views: [{
                id: 'first-details',
                title: 'First details',
                disclosure: 'supplemental',
                data: { source: 'runs' },
                mark: 'metric',
                encoding: { value: { field: 'run', aggregate: 'count' } }
              }]
            },
            { id: 'second', kind: /** @type {'custom'} */ ('custom'), title: 'Second', description: 'Second page description', experimental: true, views: [] }
          ]
        }
      },
      sources: {
        runs: {
          source: 'runs',
          rows: [{ run: '1' }],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-30T08:00:00Z',
            'retrieved-at': '2026-08-30T08:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });
    rendered.ownerDocument.body.append(rendered);

    const first = /** @type {HTMLElement} */ (rendered.querySelector('#page-first'));
    const second = /** @type {HTMLElement} */ (rendered.querySelector('#page-second'));
    const firstLink = /** @type {HTMLAnchorElement} */ (rendered.querySelector('[data-nav-page-id="first"]'));
    const secondLink = /** @type {HTMLAnchorElement} */ (rendered.querySelector('[data-nav-page-id="second"]'));
    const firstDetails = /** @type {HTMLDetailsElement} */ (first.querySelector('details'));
    expect(first.hidden).toBe(false);
    expect(second.hidden).toBe(true);
    expect(first.hasAttribute('data-page-pending')).toBe(false);
    expect(second.hasAttribute('data-page-pending')).toBe(true);
    firstDetails.open = true;
    const pageScroller = /** @type {HTMLElement} */ (rendered.querySelector('main.dashboard-prototype'));
    pageScroller.scrollTop = 320;
    expect(/** @type {HTMLElement | null} */ (rendered.querySelector('[data-breadcrumb-root]'))?.hidden).toBe(true);
    expect(rendered.querySelector('[data-breadcrumb-root]')?.hasAttribute('href')).toBe(false);
    expect(rendered.querySelector('[data-breadcrumb-dashboard]')?.getAttribute('href')).toBe('#page-first');
    expect(rendered.querySelector('[data-breadcrumb-dashboard]')?.textContent).toBe('Overview');
    expect(/** @type {HTMLElement} */ (rendered.querySelector('[data-breadcrumb-dashboard]'))?.hidden).toBe(true);
    expect(rendered.querySelector('#page-title')?.textContent).toBe('First');
    expect(rendered.querySelector('[data-breadcrumb-page]')?.textContent).toBe('First');
    expect(rendered.querySelector('[data-page-description]')?.textContent).toBe('First page description');
    expect(/** @type {HTMLElement} */ (rendered.querySelector('.title-area [data-page-experimental]')).hidden).toBe(true);
    expect(rendered.ownerDocument.title).toBe('First · Page Navigation');
    first.dispatchEvent(new CustomEvent('dashboard-route-allocation', {
      bubbles: true,
      detail: {
        title: 'Linked issue',
        titleLink: {
          href: 'https://github.com/octo/repo/issues/42',
          label: 'Open #42 on GitHub'
        }
      }
    }));
    const titleLink = /** @type {HTMLAnchorElement} */ (rendered.querySelector('[data-page-title-link]'));
    expect(titleLink.hidden).toBe(false);
    expect(titleLink.textContent).toBe('Open #42 on GitHub');
    expect(titleLink.getAttribute('href')).toBe('https://github.com/octo/repo/issues/42');
    expect(titleLink.getAttribute('target')).toBe('_blank');
    expect(titleLink.getAttribute('rel')).toBe('noopener noreferrer');
    expect(titleLink.getAttribute('aria-label')).toBeNull();
    expect(titleLink.getAttribute('title')).toBe('Open #42 on GitHub');
    expect(titleLink.querySelector('.octicon-mark-github')).not.toBeNull();
    expect(rendered.ownerDocument.title).toBe('Linked issue · Page Navigation');

    secondLink.dataset.routeTitle = 'Canonical second';
    secondLink.dataset.routeDescription = 'Canonical second description';
    secondLink.click();

    expect(first.hidden).toBe(true);
    expect(first.hasAttribute('data-page-pending')).toBe(true);
    expect(first.childElementCount).toBe(0);
    expect(rendered.querySelector('#page-second')).toBe(second);
    expect(second.hidden).toBe(false);
    expect(second.hasAttribute('data-page-pending')).toBe(true);
    expect(second.getAttribute('aria-busy')).toBe('true');
    expect(second.querySelector('.dashboard-view-skeleton')).not.toBeNull();
    expect(second.getAttribute('aria-label')).toBe('Loading view');
    expect(secondLink.getAttribute('aria-current')).toBe('page');
    expect(rendered.ownerDocument.defaultView?.location.hash).toBe('#page-second');
    expect(rendered.querySelector('#page-title')?.textContent).toBe('Canonical second');
    expect(rendered.querySelector('[data-breadcrumb-page]')?.textContent).toBe('Canonical second');
    expect(rendered.querySelector('[data-page-description]')?.textContent).toBe('Canonical second description');
    expect(/** @type {HTMLElement} */ (rendered.querySelector('.title-area [data-page-experimental]')).hidden).toBe(false);
    const experimentalBadge = rendered.querySelector('.title-area [data-page-experimental]');
    expect(experimentalBadge?.getAttribute('aria-label')).toBe('Experimental');
    expect(experimentalBadge?.querySelector('.octicon-beaker')).not.toBeNull();
    expect(rendered.ownerDocument.title).toBe('Canonical second · Page Navigation');
    expect(titleLink.hidden).toBe(true);
    expect(titleLink.hasAttribute('href')).toBe(false);
    expect(rendered.ownerDocument.activeElement).toBe(rendered.querySelector('#page-title'));
    await vi.waitFor(() => {
      expect(rendered.querySelector('#page-second')).not.toBe(second);
    });
    const renderedSecond = /** @type {HTMLElement} */ (rendered.querySelector('#page-second'));
    expect(renderedSecond.hidden).toBe(false);
    expect(renderedSecond.hasAttribute('data-page-pending')).toBe(false);
    expect(renderedSecond.hasAttribute('aria-busy')).toBe(false);
    expect(renderedSecond.dataset.pageExperimental).toBe('true');
    expect(/** @type {HTMLElement} */ (rendered.querySelector('.title-area [data-page-experimental]')).hidden).toBe(false);
    expect(rendered.querySelector('#page-title')?.textContent).toBe('Canonical second');
    expect(rendered.querySelector('[data-page-description]')?.textContent).toBe('Canonical second description');

    pageScroller.scrollTop = 80;
    firstLink.click();
    expect(/** @type {HTMLElement} */ (rendered.querySelector('.title-area [data-page-experimental]')).hidden).toBe(true);

    expect(renderedSecond.hasAttribute('data-page-pending')).toBe(true);
    expect(renderedSecond.childElementCount).toBe(0);
    expect(rendered.querySelector('#page-first .dashboard-view-skeleton')).not.toBeNull();
    await vi.waitFor(() => {
      expect(rendered.querySelector('#page-first .dashboard-view-skeleton')).toBeNull();
    });
    const rehydratedFirst = /** @type {HTMLElement} */ (rendered.querySelector('#page-first'));
    expect(/** @type {HTMLDetailsElement | null} */ (rehydratedFirst.querySelector('details'))?.open).toBe(true);
    expect(pageScroller.scrollTop).toBe(320);
    rendered.ownerDocument.defaultView?.history.replaceState(null, '', '/');
  });

  it('hides the experimental badge before any navigation occurs', () => {
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    rendered.ownerDocument.body.append(rendered);

    try {
      const experimentalBadge = /** @type {HTMLElement} */ (rendered.querySelector('.title-area [data-page-experimental]'));
      expect(rendered.querySelector('#page-title')?.textContent).toBe('Overview');
      expect(experimentalBadge.hidden).toBe(true);
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
    }
  });

  it('does not show the experimental badge when Overview is active', async () => {
    const rendered = renderDashboard({
      document: authoritativeDashboardDocument,
      sources: {}
    });
    rendered.ownerDocument.body.append(rendered);

    try {
      await activatePage(rendered, 'overview');

      expect(rendered.querySelector('#page-title')?.textContent).toBe('Overview');
      const experimentalBadge = /** @type {HTMLElement} */ (rendered.querySelector('.title-area [data-page-experimental]'));
      expect(experimentalBadge.hidden).toBe(true);
      const experimentalIcon = experimentalBadge.querySelector('.octicon-beaker');
      if (!(experimentalIcon instanceof SVGElement)) throw new Error('Expected the experimental icon to render.');
      expect(experimentalIcon.getAttribute('aria-hidden')).toBe('true');
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
    }
  });

  it('replaces a failed asynchronous page render with an accessible error message', async () => {
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="first" href="#page-first">First</a>
      <a data-nav-page-id="second" href="#page-second">Second</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first" data-page-pending></section>
        <section class="dashboard-page" id="page-second" data-page-id="second" data-page-pending></section>
      </main>
    `;
    document.body.append(root);
    /** @param {string} pageId */
    const renderPage = (pageId) => {
      if (pageId === 'second') return Promise.reject(new Error('Page rendering failed.'));
      const page = document.createElement('section');
      page.className = 'dashboard-page';
      page.id = `page-${pageId}`;
      page.dataset.pageId = pageId;
      return page;
    };
    try {
      enableDashboardPageNavigation(root, 'Dashboard', renderPage, 'first');
      await vi.waitFor(() => {
        expect(root.querySelector('#page-first')?.hasAttribute('data-page-pending')).toBe(false);
      });

      /** @type {HTMLAnchorElement} */ (root.querySelector('[data-nav-page-id="second"]')).click();

      await vi.waitFor(() => {
        expect(root.querySelector('#page-second .page-load-error h2')?.textContent).toBe('Unable to load this page');
      });
      const page = /** @type {HTMLElement} */ (root.querySelector('#page-second'));
      expect(page.getAttribute('aria-busy')).toBeNull();
      expect(page.getAttribute('aria-label')).toBeNull();
      expect(page.querySelector('.page-load-error')?.getAttribute('role')).toBe('alert');
      expect(page.querySelector('.page-load-error button')?.textContent).toBe('Try again');
    } finally {
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('explains a coded server limit with its query ID and retries the page', async () => {
    const root = document.createElement('div');
    root.innerHTML = `<a data-nav-page-id="campaigns" href="#page-campaigns">Campaigns</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first" data-page-pending></section>
        <section class="dashboard-page" id="page-campaigns" data-page-id="campaigns" data-page-pending></section>
      </main>`;
    document.body.append(root);
    let attempts = 0;
    const renderPage = vi.fn((pageId) => {
      if (pageId === 'campaigns' && ++attempts === 1) {
        return Promise.reject(new DashboardServerError('limit', 'query_plan_too_large', 'campaign-repository-coverage'));
      }
        const page = document.createElement('section');
        page.id = `page-${pageId}`;
        page.className = 'dashboard-page';
        page.dataset.pageId = pageId;
        page.textContent = `${pageId} loaded`;
        return page;
    });
    const dispose = enableDashboardPageNavigation(root, 'Dashboard', renderPage, 'first');
    try {
      await vi.waitFor(() => expect(root.querySelector('#page-first')?.hasAttribute('data-page-pending')).toBe(false));
      /** @type {HTMLAnchorElement} */ (root.querySelector('[data-nav-page-id="campaigns"]')).click();
      await vi.waitFor(() => expect(root.querySelector('.page-load-error-detail')?.textContent)
        .toBe('Query: campaign-repository-coverage'));
      expect(root.querySelector('.page-load-error h2')?.textContent).toContain('more data');
      /** @type {HTMLButtonElement} */ (root.querySelector('.page-load-error button')).click();
      await vi.waitFor(() => expect(root.querySelector('#page-campaigns')?.textContent).toBe('campaigns loaded'));
      expect(attempts).toBe(2);
    } finally {
      dispose();
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('dispatches initialized route state after an asynchronous page render', async () => {
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="first" href="#page-first">First</a>
      <a data-nav-page-id="second" href="#page-second?campaign=optimization">Second</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first" data-page-pending></section>
        <section class="dashboard-page" id="page-second" data-page-id="second" data-page-pending data-route-parameter="campaign"></section>
      </main>
    `;
    document.body.append(root);
    /** @type {((page: HTMLElement) => void) | undefined} */
    let resolveSecond;
    /** @param {string} pageId */
    const renderPage = (pageId) => {
      if (pageId !== 'second') return null;
      return new Promise((resolve) => {
        resolveSecond = resolve;
      });
    };
    try {
      const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', renderPage, 'first');
      /** @type {HTMLAnchorElement} */ (root.querySelector('[data-nav-page-id="second"]')).click();
      await vi.waitFor(() => expect(resolveSecond).toBeDefined());

      const page = document.createElement('section');
      page.className = 'dashboard-page';
      page.id = 'page-second';
      page.dataset.pageId = 'second';
      page.dataset.routeParameter = 'campaign';
      const routeView = document.createElement('div');
      routeView.dataset.routeView = '';
      routeView.addEventListener('dashboard-route-change', (event) => {
        if (event instanceof CustomEvent) routeView.dataset.campaign = event.detail.value;
      });
      page.append(routeView);
      resolveSecond?.(page);

      await vi.waitFor(() => {
        expect(root.querySelector('#page-second [data-route-view]')?.getAttribute('data-campaign')).toBe('optimization');
      });
      disposeNavigation();
    } finally {
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('reloads a populated route page when its route value changes', async () => {
    window.history.replaceState(null, '', '/#page-campaign?campaign=optimization');
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="campaign" href="#page-campaign?campaign=optimization">Campaign</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-campaign" data-page-id="campaign" data-page-pending data-route-parameter="campaign"></section>
      </main>
    `;
    document.body.append(root);
    /** @type {string[]} */
    const renderedCampaigns = [];
    const renderPage = vi.fn((pageId, options) => {
      const campaign = options.routeParameters?.campaign ?? '';
      renderedCampaigns.push(campaign);
      const page = document.createElement('section');
      page.className = 'dashboard-page';
      page.id = `page-${pageId}`;
      page.dataset.pageId = pageId;
      page.dataset.routeParameter = 'campaign';
      page.textContent = campaign;
      return page;
    });
    try {
      const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', renderPage, 'campaign');
      await vi.waitFor(() => {
        expect(root.querySelector('#page-campaign')?.textContent).toBe('optimization');
      });

      window.location.hash = '#page-campaign?campaign=dependabot';

      await vi.waitFor(() => {
        expect(root.querySelector('#page-campaign')?.textContent).toBe('dependabot');
      });
      expect(renderedCampaigns).toEqual(['optimization', 'dependabot']);
      disposeNavigation();
    } finally {
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('keeps route tabs visible and uses a tab transition while a sibling view loads', async () => {
    /** @type {Array<string | undefined>} */
    const transitions = [];
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      value: vi.fn((callback) => {
        transitions.push(document.documentElement.dataset.navigationTransition);
        callback();
      })
    });
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="first" href="#page-first?campaign=ambient-context">First</a>
      <a data-nav-page-id="second" href="#page-second?campaign=ambient-context">Second</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first">
          <nav data-route-tabs aria-label="Ambient Context views">
            <a href="#page-first?campaign=ambient-context" aria-current="page">Overview</a>
            <a href="#page-second?campaign=ambient-context">Workflows</a>
            <a href="#page-third?campaign=ambient-context">Runs</a>
          </nav>
          <p>Overview content</p>
        </section>
        <section class="dashboard-page" id="page-second" data-page-id="second" data-page-pending></section>
      </main>
    `;
    document.body.append(root);
    const renderPage = vi.fn((pageId) => pageId === 'second'
      ? new Promise(() => {})
      : null);
    try {
      const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', renderPage, 'first');
      /** @type {HTMLAnchorElement} */ (root.querySelector('[data-nav-page-id="second"]')).click();

      await vi.waitFor(() => {
        expect(root.querySelector('#page-second .dashboard-view-skeleton')).not.toBeNull();
      });
      const pendingTabs = root.querySelector('#page-second [data-route-tabs]');
      expect(pendingTabs?.textContent?.replace(/\s/g, '')).toBe('OverviewWorkflowsRuns');
      expect(pendingTabs?.querySelector('[aria-current="page"]')?.textContent?.trim()).toBe('Workflows');
      expect(root.querySelector('#page-second')?.getAttribute('aria-busy')).toBe('true');
      expect(transitions).toEqual(['tab']);
      window.history.replaceState(null, '', '/#page-first?campaign=ambient-context');
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      expect(transitions.at(-1)).toBe('tab');
      window.history.replaceState(null, '', '/#page-second?campaign=different');
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      expect(transitions.at(-1)).toBe('page');
      disposeNavigation();
    } finally {
      Reflect.deleteProperty(document, 'startViewTransition');
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('does not navigate when the current route tab is clicked', () => {
    const root = document.createElement('div');
    root.innerHTML = `
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first">
          <nav data-route-tabs aria-label="Ambient Context views">
            <a data-nav-page-id="first" href="#page-first?campaign=ambient-context" aria-current="page">Overview</a>
          </nav>
        </section>
      </main>
    `;
    document.body.append(root);
    const renderPage = vi.fn(() => null);
    try {
      const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', renderPage, 'first');
      const currentTab = /** @type {HTMLAnchorElement} */ (root.querySelector('[aria-current="page"]'));
      const click = new MouseEvent('click', { bubbles: true, cancelable: true });
      currentTab.dispatchEvent(click);

      expect(click.defaultPrevented).toBe(true);
      expect(renderPage).not.toHaveBeenCalled();
      disposeNavigation();
    } finally {
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('animates query drills forward and browser back navigation backward', () => {
    /** @type {Array<string | undefined>} */
    const directions = [];
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      value: vi.fn((callback) => {
        directions.push(document.documentElement.dataset.navigationDirection);
        callback();
        return { finished: Promise.resolve() };
      })
    });
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="first" href="#page-first">First</a>
      <a data-card-drill="query" data-nav-page-id="second" href="#page-second?query=items&title=Item">Item</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first"></section>
        <section class="dashboard-page" id="page-second" data-page-id="second"></section>
      </main>
    `;
    document.body.append(root);
    try {
      const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', () => null, 'first');
      /** @type {HTMLAnchorElement} */ (root.querySelector('[data-card-drill="query"]')).click();

      window.history.replaceState(
        { centralAgenticOpsNavigationIndex: 0 },
        '',
        '/#page-first'
      );
      window.dispatchEvent(new PopStateEvent('popstate', {
        state: { centralAgenticOpsNavigationIndex: 0 }
      }));
      window.dispatchEvent(new HashChangeEvent('hashchange'));

      expect(directions.at(0)).toBe('forward');
      expect(directions.at(-1)).toBe('backward');
      disposeNavigation();
    } finally {
      root.remove();
      Reflect.deleteProperty(document, 'startViewTransition');
      delete document.documentElement.dataset.navigationDirection;
      window.history.replaceState(null, '', '/');
    }
  });

  it('persists page scroll positions for reload and browser history restoration', async () => {
    const createRoot = () => {
      const root = document.createElement('div');
      root.innerHTML = `
        <a data-nav-page-id="first" href="#page-first">First</a>
        <a data-nav-page-id="second" href="#page-second">Second</a>
        <main class="dashboard-prototype">
          <section class="dashboard-page" id="page-first" data-page-id="first"></section>
          <section class="dashboard-page" id="page-second" data-page-id="second"></section>
        </main>
      `;
      document.body.append(root);
      return root;
    };
    const firstRoot = createRoot();
    try {
      const disposeFirstNavigation = enableDashboardPageNavigation(firstRoot, 'Dashboard', () => null, 'first');
      const firstScroller = /** @type {HTMLElement} */ (firstRoot.querySelector('main.dashboard-prototype'));
      firstScroller.scrollTop = 240;
      firstScroller.dispatchEvent(new Event('scroll'));
      await vi.waitFor(() => {
        expect(window.history.state?.centralAgenticOpsScrollTop).toBe(240);
      });
      disposeFirstNavigation();
      firstRoot.remove();

      const reloadedRoot = createRoot();
      const disposeReloadedNavigation = enableDashboardPageNavigation(reloadedRoot, 'Dashboard', () => null, 'first');
      const reloadedScroller = /** @type {HTMLElement} */ (reloadedRoot.querySelector('main.dashboard-prototype'));
      expect(reloadedScroller.scrollTop).toBe(240);

      /** @type {HTMLAnchorElement} */ (reloadedRoot.querySelector('[data-nav-page-id="second"]')).click();
      await vi.waitFor(() => {
        expect(reloadedRoot.querySelector('[data-page-id="second"]')?.hasAttribute('hidden')).toBe(false);
      });
      reloadedScroller.scrollTop = 80;
      reloadedScroller.dispatchEvent(new Event('scroll'));
      window.history.replaceState(
        {
          centralAgenticOpsNavigationIndex: 0,
          centralAgenticOpsScrollPageId: 'first',
          centralAgenticOpsScrollTop: 240
        },
        '',
        '/#page-first'
      );
      window.dispatchEvent(new PopStateEvent('popstate', {
        state: {
          centralAgenticOpsNavigationIndex: 0,
          centralAgenticOpsScrollPageId: 'first',
          centralAgenticOpsScrollTop: 240
        }
      }));
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      expect(reloadedScroller.scrollTop).toBe(240);
      disposeReloadedNavigation();
      reloadedRoot.remove();
    } finally {
      window.history.replaceState(null, '', '/');
    }
  });

  it('debounces scroll history writes and flushes pending scroll on navigation', () => {
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="first" href="#page-first">First</a>
      <a data-nav-page-id="second" href="#page-second">Second</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first"></section>
        <section class="dashboard-page" id="page-second" data-page-id="second"></section>
      </main>
    `;
    document.body.append(root);
    const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', () => null, 'first');
    const scroller = /** @type {HTMLElement} */ (root.querySelector('main.dashboard-prototype'));
    const replaceState = vi.spyOn(window.history, 'replaceState');
    replaceState.mockClear();
    vi.useFakeTimers();
    try {
      for (let position = 1; position <= 150; position += 1) {
        scroller.scrollTop = position;
        scroller.dispatchEvent(new Event('scroll'));
        vi.advanceTimersByTime(80);
      }
      expect(replaceState).not.toHaveBeenCalled();
      vi.advanceTimersByTime(250);
      expect(replaceState).toHaveBeenCalledTimes(1);
      expect(window.history.state).toMatchObject({
        centralAgenticOpsScrollPageId: 'first',
        centralAgenticOpsScrollTop: 150
      });

      scroller.scrollTop = 200;
      scroller.dispatchEvent(new Event('scroll'));
      /** @type {HTMLAnchorElement} */ (root.querySelector('[data-nav-page-id="second"]')).click();
      expect(replaceState).toHaveBeenCalledTimes(2);
      expect(replaceState.mock.calls[1][0]).toMatchObject({
        centralAgenticOpsScrollPageId: 'first',
        centralAgenticOpsScrollTop: 200
      });
      vi.advanceTimersByTime(250);
      expect(replaceState).toHaveBeenCalledTimes(2);

      scroller.dispatchEvent(new Event('scroll'));
      disposeNavigation();
      vi.advanceTimersByTime(250);
      expect(replaceState).toHaveBeenCalledTimes(2);
    } finally {
      disposeNavigation();
      vi.useRealTimers();
      root.remove();
      replaceState.mockRestore();
      window.history.replaceState(null, '', '/');
    }
  });

  it('aborts the active page subscription when the dashboard is disposed', async () => {
    /** @type {AbortSignal | undefined} */
    let pageSignal;
    const loadPageSources = vi.fn(async (_pageId, options) => {
      pageSignal = options.signal;
      return {};
    });
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'disposable-dashboard',
          title: 'Disposable Dashboard',
          pages: [{
            id: 'overview',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Overview',
            views: []
          }]
        }
      },
      sources: {},
      loadPageSources
    });

    await vi.waitFor(() => expect(pageSignal).toBeDefined());
    expect(pageSignal?.aborted).toBe(false);

    disposeDashboard(rendered);

    expect(pageSignal?.aborted).toBe(true);
  });

  it('ignores a pending view transition after the dashboard is disposed', () => {
    /** @type {(() => void) | undefined} */
    let transitionUpdate;
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      value: vi.fn((callback) => {
        transitionUpdate = callback;
        return { finished: Promise.resolve() };
      })
    });
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'transition-disposal-dashboard',
          title: 'Transition Disposal Dashboard',
          pages: [
            { id: 'first', kind: /** @type {'custom'} */ ('custom'), title: 'First', views: [] },
            { id: 'second', kind: /** @type {'custom'} */ ('custom'), title: 'Second', views: [] }
          ]
        }
      },
      sources: {}
    });
    document.body.append(rendered);
    try {
      /** @type {HTMLAnchorElement | null} */ (
        rendered.querySelector('[data-nav-page-id="second"]')
      )?.click();
      expect(transitionUpdate).toBeDefined();

      disposeDashboard(rendered);
      transitionUpdate?.();

      expect(rendered.querySelector('[data-page-id="second"]')?.hasAttribute('data-page-pending')).toBe(true);
    } finally {
      rendered.remove();
      Reflect.deleteProperty(document, 'startViewTransition');
      window.history.replaceState(null, '', '/');
    }
  });

  it('removes page navigation handlers when navigation is disposed', async () => {
    /** @type {FrameRequestCallback[]} */
    const animationFrames = [];
    const requestAnimationFrame = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrames.push(callback);
      return animationFrames.length;
    });
    const root = document.createElement('div');
    root.innerHTML = `
      <a data-nav-page-id="first" href="#page-first">First</a>
      <a data-nav-page-id="second" href="#page-second">Second</a>
      <main class="dashboard-prototype">
        <section class="dashboard-page" id="page-first" data-page-id="first" data-page-pending></section>
        <section class="dashboard-page" id="page-second" data-page-id="second" data-page-pending></section>
      </main>
    `;
    document.body.append(root);
    const renderPage = vi.fn((pageId) => {
      const page = document.createElement('section');
      page.className = 'dashboard-page';
      page.id = `page-${pageId}`;
      page.dataset.pageId = pageId;
      return page;
    });
    try {
      const disposeNavigation = enableDashboardPageNavigation(root, 'Dashboard', renderPage, 'first');
      expect(renderPage).toHaveBeenCalledOnce();

      root.querySelector('[data-nav-page-id="second"]')?.dispatchEvent(
        new MouseEvent('click', { bubbles: true, cancelable: true })
      );
      expect(animationFrames).toHaveLength(1);
      disposeNavigation();
      while (animationFrames.length > 0) {
        animationFrames.shift()?.(0);
      }
      window.history.replaceState(null, '', '/#page-second');
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      root.querySelector('[data-nav-page-id="second"]')?.dispatchEvent(
        new MouseEvent('click', { bubbles: true, cancelable: true })
      );
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 0));

      expect(renderPage).toHaveBeenCalledOnce();
    } finally {
      requestAnimationFrame.mockRestore();
      root.remove();
      window.history.replaceState(null, '', '/');
    }
  });

  it('uses only declared query drill titles at every navigation depth', () => {
    const knownQueries = new Set(['issue-events']);
    expect(resolveQueryDrillPageTitle(
      new URLSearchParams('query=issue-events&title=Issue+42&issue-id=42'),
      knownQueries
    )).toBe('Issue 42');
    expect(resolveQueryDrillPageTitle(
      new URLSearchParams('query=issue-events&title=Issue+43&issue-id=43'),
      knownQueries
    )).toBe('Issue 43');
    expect(resolveQueryDrillPageTitle(
      new URLSearchParams('query=unknown&title=Untrusted'),
      knownQueries
    )).toBe('');
  });


  it('DLS-SAFE-004 DLS-SAFE-008 DLS-SAFE-009 renders accessible bars, visual chart legends, and rejects unsafe runtime links', () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'bar-dashboard',
          title: 'Bar Dashboard',
          pages: [{
            id: 'bars',
            kind: /** @type {'custom'} */ ('custom'),
            views: [
              {
                id: 'bar-chart',
                data: { source: 'runs' },
                mark: 'chart',
                chart: 'bar',
                encoding: {
                  x: { field: 'run-conclusion', type: 'nominal' },
                  y: { field: 'run', type: 'quantitative', aggregate: 'count' },
                  color: { field: 'run-conclusion', type: 'nominal' }
                }
              },
              {
                id: 'unsafe-link',
                data: { source: 'runs' },
                mark: 'table',
                encoding: {
                  columns: [{ field: 'run' }],
                  href: { field: 'run-link' }
                }
              }
            ]
          }]
        }
      },
      sources: {
        runs: {
          source: 'runs',
          rows: [
            { run: '1', 'run-conclusion': 'success', 'run-link': { href: 'javascript:alert(1)', label: 'Unsafe' } },
            { run: '2', 'run-conclusion': 'failure', 'run-link': { href: 'https://example.com/runs/2', label: 'Run 2' } }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    expect(rendered.querySelectorAll('[data-chart-widget="bar"] rect[role="img"]')).toHaveLength(2);
    expect(rendered.querySelector('[data-chart-widget="bar"] rect')?.getAttribute('aria-label')).toContain('failure');
    expect(rendered.querySelector('[data-chart-legend="visual"]')?.getAttribute('class')).toContain('chart-legend-bar');
    expect([...rendered.querySelectorAll('[data-chart-legend="visual"] li span')].map((item) => item.textContent)).toEqual(['failure', 'success']);
    expect(rendered.querySelectorAll('.custom-table a')).toHaveLength(1);
    expect(rendered.querySelector('.custom-table a')?.textContent).toBe('2');
  });

  it('DLS-SAFE-004 rejects runtime links with embedded credentials, ftp schemes, and blank labels while preserving safe links', () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'credential-link-dashboard',
          title: 'Credential Link Dashboard',
          pages: [{
            id: 'credential-links',
            kind: /** @type {'custom'} */ ('custom'),
            views: [
              {
                id: 'credential-links-table',
                title: 'Credential Links Table',
                data: { source: 'runs' },
                mark: 'table',
                encoding: {
                  columns: [{ field: 'run' }],
                  href: { field: 'run-link' }
                }
              },
              {
                id: 'credential-links-metric',
                title: 'Credential Links Metric',
                data: { source: 'runs' },
                mark: 'metric',
                encoding: {
                  value: { field: 'run', type: 'nominal', aggregate: 'count' },
                  href: { field: 'run-link' }
                }
              }
            ]
          }]
        }
      },
      sources: {
        runs: {
          source: 'runs',
          rows: [
            { run: '1', 'run-link': { href: 'https://user:secret@example.com/runs/1', label: 'Credentialed Run' } },
            { run: '2', 'run-link': { href: 'ftp://example.com/runs/2', label: 'FTP Run' } },
            { run: '3', 'run-link': { href: 'https://example.com/runs/3', label: '   ' } },
            { run: '4', 'run-link': { href: 'https://example.com/runs/4', label: 'Run 4' } }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    const safeLinks = rendered.querySelectorAll('.custom-table a, .metric-link a');
    expect(safeLinks).toHaveLength(2);
    expect([...safeLinks].map((link) => link.textContent)).toEqual(['4', 'Run 4']);
    expect([...safeLinks].every((link) => !String(link.getAttribute('href')).includes('user:secret@'))).toBe(true);
    expect([...safeLinks].every((link) => String(link.getAttribute('href')).startsWith('https://example.com/runs/4'))).toBe(true);
    expect(rendered.textContent).not.toContain('Credentialed Run');
    expect(rendered.textContent).not.toContain('FTP Run');
    expect(rendered.textContent).toContain('Run 4');
  });

  it('DLS-AGG-008 DLS-VIEW-003 renders report-style aggregate rankings in declared order before applying limit', async () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'ranked-usage',
          title: 'Ranked usage',
          pages: [{
            id: 'usage',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Usage',
            views: [{
              id: 'repository-usage',
              title: 'Repository usage',
              data: {
                source: 'usage',
                'order-by': [{ field: 'total-aic', direction: 'desc' }],
                limit: 2
              },
              mark: 'table',
              encoding: {
                columns: [
                  { field: 'repository', type: 'nominal' },
                  { field: 'aic', type: 'quantitative', aggregate: 'sum', as: 'total-aic', title: 'Total AIC' }
                ]
              }
            }]
          }]
        }
      },
      sources: {
        usage: {
          source: 'usage',
          rows: [
            { repository: 'charlie', aic: 2 },
            { repository: 'alpha', aic: 4 },
            { repository: 'bravo', aic: 3 },
            { repository: 'charlie', aic: 4 },
            { repository: 'alpha', aic: 1 }
          ],
          metadata: {
            'source-id': 'aic-usage',
            'source-kind': 'report-artifact',
            'as-of': '2026-08-30T12:00:00Z',
            'retrieved-at': '2026-08-30T12:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    const rows = [...rendered.querySelectorAll('.custom-table tbody tr')];
    expect(rows).toHaveLength(2);
    expect(rows.map((row) => row.textContent)).toEqual(['charlie6', 'alpha5']);
    const filter = /** @type {HTMLInputElement} */ (rendered.querySelector('.table-filter input'));
    expect(filter).toBeTruthy();
    expect(filter.closest('label')?.textContent).toContain('Filter Repository usage');
    filter.value = 'alpha';
    filter.dispatchEvent(new Event('input'));
    await vi.waitFor(() => expect(rows.map((row) => row.hasAttribute('hidden'))).toEqual([true, false]));
    expect(rendered.querySelector('.table-filter-result')?.textContent).toBe('Showing 1 of 1 result');
    expect(rendered.querySelector('.freshness')).toBeNull();
  });

  it('renders report-style semantic badges through the generic table presenter', () => {
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'workflow-status',
          title: 'Workflow status',
          pages: [{
            id: 'workflows',
            kind: /** @type {'custom'} */ ('custom'),
            title: 'Workflows',
            views: [{
              id: 'workflow-statuses',
              title: 'Workflow statuses',
              data: { source: 'workflows' },
              mark: 'table',
              encoding: {
                columns: [
                  { field: 'workflow', type: 'nominal' },
                  { field: 'workflow-active', type: 'nominal', display: 'active-state' },
                  { field: 'rollout-mode', type: 'nominal', display: 'mode' },
                  { field: 'run-conclusion', type: 'nominal', display: 'status' }
                ]
              }
            }]
          }]
        }
      },
      sources: {
        workflows: {
          source: 'workflows',
          rows: [{
            workflow: 'review',
            'workflow-active': 'true',
            'rollout-mode': 'review',
            'run-conclusion': 'failure'
          }],
          metadata: {
            'source-id': 'deployed-workflows',
            'source-kind': 'report-artifact',
            'as-of': '2026-08-30T12:00:00Z',
            'retrieved-at': '2026-08-30T12:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });

    expect(rendered.querySelector('.custom-table .status-success')?.textContent).toBe('Active');
    expect(rendered.querySelector('.custom-table .mode-review')?.textContent).toBe('review');
    expect(rendered.querySelector('.custom-table .status-danger')?.textContent).toBe('failure');
  });

  it('routes and reallocates a JSON-selected repository workflow view from a hash query argument', () => {
    window.history.replaceState(null, '', '/#page-repository-detail?repository=octo-org%2Focto-repo');
    const rendered = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'repository-detail-dashboard',
          title: 'Repository detail',
          pages: [
            {
              id: 'repository-detail',
              kind: /** @type {'custom'} */ ('custom'),
              title: 'Repository',
              route: { 'hash-query-parameter': 'repository' },
              views: [{
                id: 'repository-workflows',
                title: 'Agentic workflows',
                data: {
                  source: 'workflows',
                  'route-field': 'repository-slug'
                },
                mark: 'table',
                controls: 'static',
                encoding: {
                  columns: [
                    { field: 'workflow-name', type: 'nominal', title: 'Workflow' },
                    { field: 'workflow-active', type: 'nominal', title: 'State', display: 'active-state' }
                  ],
                  href: { field: 'workflow-link', type: 'nominal' }
                }
              }]
            },
            {
              id: 'workflow-runtime',
              kind: /** @type {'custom'} */ ('custom'),
              title: 'Workflow runtime',
              route: { 'hash-query-parameter': 'workflow' },
              views: []
            }
          ]
        }
      },
      sources: {
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'octo-org', repository: 'octo-repo', 'repository-slug': 'octo-org/octo-repo', workflow: '.github/workflows/review.md', 'workflow-name': 'Review', 'workflow-role': 'standalone', 'workflow-active': 'true', 'observed-at': '2026-08-29T10:00:00Z', 'workflow-link': { relation: 'workflow', 'dashboard-href': '#page-workflow-runtime?workflow=octo-org%2Focto-repo%3A.github%2Fworkflows%2Freview.md', 'dashboard-label': 'Review' } },
            { organization: 'other-org', repository: 'other-repo', 'repository-slug': 'other-org/other-repo', workflow: '.github/workflows/other.md', 'workflow-name': 'Other', 'workflow-role': 'standalone', 'workflow-active': 'true', 'observed-at': '2026-08-29T10:00:00Z', 'workflow-link': { relation: 'workflow', 'dashboard-href': '#page-workflow-runtime?workflow=other-org%2Fother-repo%3A.github%2Fworkflows%2Fother.md', 'dashboard-label': 'Other' } }
          ],
          metadata: {
            'source-id': 'workflows-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    });
    document.body.append(rendered);

    const repositoryView = rendered.querySelector('[data-view-id="repository-workflows"]');
    expect(repositoryView?.textContent).toContain('Review');
    expect(repositoryView?.textContent).not.toContain('Other');
    expect(rendered.querySelector('#page-title')?.textContent).toBe('octo-org/octo-repo');
    expect(rendered.ownerDocument.title).toBe('octo-org/octo-repo · Repository detail');
    expect(rendered.querySelector('[data-breadcrumb-page]')?.textContent).toBe('octo-org/octo-repo');
    expect(repositoryView?.querySelector('tbody a')?.getAttribute('href')).toBe('#page-workflow-runtime?workflow=octo-org%2Focto-repo%3A.github%2Fworkflows%2Freview.md');
    expect(repositoryView?.querySelector('tbody a')?.getAttribute('target')).toBeNull();

    rendered.remove();
    window.history.replaceState(null, '', '/');
  });
});
