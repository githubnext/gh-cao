import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderOutcomeDetail } from '../../src/components/outcome-detail.js';
import { renderOutcomeDetailSection } from '../../src/components/outcome-detail-sections.js';

const metadata = {
  'source-id': 'outcomes-fixture',
  'source-kind': 'fixture',
  'as-of': '2026-08-31T17:00:00Z',
  'retrieved-at': '2026-08-31T17:00:00Z',
  completeness: /** @type {'complete'} */ ('complete'),
  freshness: /** @type {'fresh'} */ ('fresh'),
  availability: /** @type {'available'} */ ('available')
};

function context() {
  return {
    pageId: 'outcome-detail',
    title: 'Outcome',
    sourceNames: ['outcomes'],
    contextDetails: [],
    routeParameter: 'outcome',
    titleLink: {
      'href-field': 'external-link',
      'identifier-field': 'outcome-number'
    },
    headingTag: /** @type {'h3'} */ ('h3'),
    sources: {
      outcomes: {
        source: 'outcomes',
        metadata,
        rows: [{
          workflow: '.github/workflows/daily.md',
          'workflow-name': 'Daily review',
          'safe-output': 'outcome-1',
          'outcome-number': 1,
          'outcome-title': 'Parity verification sweep',
          'outcome-summary': 'Summary fallback',
          'outcome-body-html': '<h2>Summary</h2><ul><li>Passed</li></ul><table><thead><tr><th scope="colgroup">Checks</th></tr></thead></table><script>window.bad = true</script><a href="javascript:alert(1)" onclick="alert(1)">unsafe</a>',
          'outcome-category': 'pull-request',
          'outcome-status': 'lifecycle',
          'outcome-state': 'lifecycle',
          'delivery-state': 'delivered',
          'delivery-signal': 'safe_output.result',
          'delivered-at': '2026-08-31T01:26:00Z',
          'adoption-state': 'lifecycle',
          'adoption-signal': 'lifecycle',
          'adoption-observed-at': '2026-08-31T01:49:00Z',
          'adoption-source': 'github-api',
          'rule-version': 'gh-aw-v1.2.3-adoption-v1',
          'survival-state': 'unknown',
          'outcome-warning': 'Warning',
          'rollout-mode': 'live',
          'published-at': '2026-08-31T01:26:00Z',
          'observed-at': '2026-08-31T01:49:00Z',
          'workflow-link': {
            relation: 'workflow',
            href: 'https://github.com/octo/repo/blob/main/daily.md',
            label: 'View workflow',
            'dashboard-href': '#page-workflow-runtime?workflow=octo%2Frepo%3A.github%2Fworkflows%2Fdaily.md',
            'dashboard-label': 'View workflow dashboard'
          },
          'external-link': { relation: 'external', href: 'https://github.com/octo/repo/pull/1', label: 'View output' },
          'run-link': { relation: 'run', href: 'https://github.com/octo/repo/actions/runs/1', label: 'View run' }
        }]
      }
    }
  };
}

describe('outcome detail', () => {
  it('DLS-SAFE-012 allocates a routed outcome and renders reusable metadata and sanitized Markdown', () => {
    const allocation = vi.fn();
    const rendered = renderOutcomeDetail(context());
    rendered.addEventListener('dashboard-route-allocation', allocation);
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'outcome-1' }
    }));

    expect(rendered.dataset.outcome).toBe('outcome-1');
    expect(rendered.querySelector('.markdown-body')?.textContent).toContain('SummaryPassedChecksunsafe');
    expect(rendered.querySelector('.markdown-body script')).toBeNull();
    expect(rendered.querySelector('.markdown-body a')?.hasAttribute('href')).toBe(false);
    expect(rendered.querySelector('.markdown-body a')?.hasAttribute('onclick')).toBe(false);
    expect(rendered.querySelector('.markdown-body th')?.getAttribute('scope')).toBe('colgroup');
    expect(rendered.querySelector('.status')?.textContent).toBe('Delivered');
    expect(rendered.querySelector('.outcome-meta')?.textContent).toContain('AdoptionLifecycle');
    expect(rendered.querySelector('.outcome-meta')?.textContent).toContain('WarningWarning');
    expect(rendered.querySelector('.mode-badge')?.textContent).toBe('Live');
    expect(rendered.querySelector('.outcome-meta')?.textContent).toContain('Pull Request');
    expect(rendered.querySelector('.outcome-meta')?.textContent).toContain('Daily review');
    expect(rendered.querySelectorAll('.outcome-meta a')).toHaveLength(3);
    expect(rendered.querySelector('.outcome-meta a[href^="#page-workflow-runtime"]')?.hasAttribute('target')).toBe(false);
    expect(allocation).toHaveBeenCalledWith(expect.objectContaining({
      detail: {
        title: 'Parity verification sweep',
        description: 'Daily review · Pull Request · Lifecycle',
        titleLink: {
          href: 'https://github.com/octo/repo/pull/1',
          label: 'Open #1 on GitHub'
        }
      }
    }));
  });

  it('renders an explicit empty state for an unknown outcome', () => {
    const rendered = renderOutcomeDetail(context());
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'missing' }
    }));

    expect(rendered.textContent).toBe('Outcome not found.');
  });

  it('renders reusable outcome detail sections independently', () => {
    const outcome = context().sources.outcomes.rows[0];

    const discussion = renderOutcomeDetailSection(outcome, 'discussion');
    const metadataSection = renderOutcomeDetailSection(outcome, 'metadata');

    expect(discussion?.className).toBe('discussion-post');
    expect(discussion?.querySelector('.markdown-body')?.textContent).toContain('SummaryPassedChecksunsafe');
    expect(metadataSection?.className).toBe('outcome-meta');
    expect(metadataSection?.textContent).toContain('DeliveryDelivered');
    expect(metadataSection?.textContent).toContain('AdoptionLifecycle');
    expect(metadataSection?.querySelectorAll('a')).toHaveLength(3);
  });
});

describe('outcome detail debug logging', () => {
  afterEach(() => {
    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('stays silent by default and logs only scalar metadata under its predictable category', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=outcome-detail', output })
      };
    });
    vi.resetModules();
    const { renderOutcomeDetail: renderOutcomeDetailWithDebug } = await import('../../src/components/outcome-detail.js');

    const rendered = renderOutcomeDetailWithDebug(context());
    expect(output.debug).toHaveBeenCalledWith('[cao:outcome-detail]', { event: 'initialized', pageId: 'outcome-detail', rowCount: 1 });

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'outcome-1' }
    }));
    expect(output.debug).toHaveBeenCalledWith('[cao:outcome-detail]', { event: 'matched', pageId: 'outcome-detail' });

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'missing' }
    }));
    expect(output.debug).toHaveBeenCalledWith('[cao:outcome-detail]', { event: 'not-found', pageId: 'outcome-detail' });

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }
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
    const { renderOutcomeDetail: renderOutcomeDetailWithoutDebug } = await import('../../src/components/outcome-detail.js');

    const rendered = renderOutcomeDetailWithoutDebug(context());
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'outcome-1' }
    }));

    expect(output.debug).not.toHaveBeenCalled();
  });
});

describe('outcome detail allocation debug logging', () => {
  afterEach(() => {
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
    const { renderOutcomeDetail: renderOutcomeDetailWithoutDebug } = await import('../../src/components/outcome-detail.js');

    const rendered = renderOutcomeDetailWithoutDebug(context());
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'outcome-1' }
    }));

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs allocation with predictable category and scalar metadata, not the resolved title or link', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=outcome-detail:allocation', output })
      };
    });
    vi.resetModules();
    const { renderOutcomeDetail: renderOutcomeDetailWithDebug } = await import('../../src/components/outcome-detail.js');

    const rendered = renderOutcomeDetailWithDebug(context());
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'outcome-1' }
    }));

    expect(output.debug).toHaveBeenCalledWith('[cao:outcome-detail:allocation]', {
      event: 'allocated',
      usedTitleFallback: false,
      hasTitleLink: true
    });
    for (const call of output.debug.mock.calls) {
      for (const value of Object.values(call[1])) {
        expect(value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean').toBe(true);
      }
    }
  });

  it('reports a used title fallback and a missing title link when neither is available', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=outcome-detail:allocation', output })
      };
    });
    vi.resetModules();
    const { renderOutcomeDetail: renderOutcomeDetailWithDebug } = await import('../../src/components/outcome-detail.js');

    const bareContext = context();
    bareContext.sources.outcomes.rows[0]['outcome-title'] = '';
    bareContext.sources.outcomes.rows[0]['external-link'] = /** @type {any} */ (null);
    const rendered = renderOutcomeDetailWithDebug(bareContext);
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'outcome-1' }
    }));

    expect(output.debug).toHaveBeenCalledWith('[cao:outcome-detail:allocation]', {
      event: 'allocated',
      usedTitleFallback: true,
      hasTitleLink: false
    });
  });

  it('only logs under the "outcome-detail:allocation" category, independent of the parent "outcome-detail" category', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=outcome-detail', output })
      };
    });
    vi.resetModules();
    const { renderOutcomeDetail: renderOutcomeDetailWithDebug } = await import('../../src/components/outcome-detail.js');

    const rendered = renderOutcomeDetailWithDebug(context());
    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'outcome', value: 'outcome-1' }
    }));

    expect(output.debug).not.toHaveBeenCalledWith('[cao:outcome-detail:allocation]', expect.anything());
  });
});
