// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { renderWorkflowRoutePage } from '../../src/components/workflow-route-page.js';

const metadata = {
  'source-id': 'fixture',
  'source-kind': 'fixture',
  'as-of': '2026-08-31T20:00:00Z',
  'retrieved-at': '2026-08-31T20:01:00Z',
  completeness: /** @type {'complete'} */ ('complete'),
  freshness: /** @type {'fresh'} */ ('fresh'),
  availability: /** @type {'available'} */ ('available')
};

/** @param {string} [pageId] */
function context(pageId = 'workflow-detail') {
  return {
    pageId,
    title: 'Workflow reports',
    sourceNames: ['workflows', 'outcomes'],
    contextDetails: [],
    routeParameter: 'workflow',
    headingTag: /** @type {'h3'} */ ('h3'),
    sources: {
      workflows: {
        source: 'workflows',
        metadata,
        rows: [
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            campaign: 'ambient-context',
            'campaign-name': 'Ambient Context',
            workflow: '.github/workflows/ambient-context.md',
            'workflow-name': 'Ambient Context',
            'workflow-role': 'orchestrator',
            'rollout-mode': 'review',
            'workflow-link': {
              relation: 'workflow',
              href: 'https://github.com/githubnext/gh-cao/blob/HEAD/.github/workflows/ambient-context.md',
              label: 'View Ambient Context',
              'dashboard-href': '#page-workflow-runtime?workflow=githubnext%2Fgh-cao%3A.github%2Fworkflows%2Fambient-context.md',
              'dashboard-label': 'View Ambient Context workflow dashboard'
            }
          },
          {
            organization: 'other',
            repository: 'repository',
            workflow: '.github/workflows/other.md',
            'workflow-name': 'Other'
          },
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: '.github/workflows/release@prod.md',
            'workflow-name': 'Release production'
          }
        ]
      },
      outcomes: {
        source: 'outcomes',
        metadata,
        rows: [
          {
            organization: 'customer',
            repository: 'target',
            'runtime-repository': 'githubnext/gh-cao',
            workflow: '.github/workflows/ambient-context.md',
            'safe-output': 'report-closed',
            'outcome-title': 'Closed report',
            'outcome-summary': 'The durable report was resolved.',
            'outcome-category': 'pull-request',
            'outcome-status': 'closed',
            'rollout-mode': 'review',
            'observed-at': '2026-08-31T19:00:00Z'
          },
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: '.github/workflows/ambient-context.md',
            'safe-output': 'report-open',
            'outcome-title': 'Open report',
            'outcome-summary': 'The durable report is open.',
            'outcome-category': 'issue',
            'outcome-status': 'open',
            'rollout-mode': 'live',
            'observed-at': '2026-08-31T20:00:00Z'
          },
          {
            organization: 'other',
            repository: 'repository',
            workflow: '.github/workflows/other.md',
            'safe-output': 'other-report',
            'outcome-title': 'Other report'
          }
        ]
      }
    }
  };
}

describe('workflow detail route', () => {
  it('renders the selected workflow identity and report navigation', () => {
    const host = document.createElement('div');
    const allocation = vi.fn();
    host.addEventListener('dashboard-route-allocation', allocation);
    const rendered = renderWorkflowRoutePage(context());
    host.append(rendered);

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: 'workflow',
        value: 'githubnext/gh-cao:.github/workflows/ambient-context.md'
      }
    }));

    expect(rendered.dataset.workflow).toBe('githubnext/gh-cao:.github/workflows/ambient-context.md');
    expect(rendered.querySelector('.workflow-tabs')?.textContent).toBe('InsightsReportsRuns');
    expect(rendered.querySelector('.workflow-tabs a[href^="#page-workflow-value"]')).toBeNull();
    expect(rendered.querySelector('.workflow-tabs [aria-current="page"]')?.textContent).toBe('Reports');
    expect(rendered.querySelector('.workflow-tabs a:first-child')?.getAttribute('href')).toBe(
      '#page-workflow-runtime?workflow=githubnext%2Fgh-cao%3A.github%2Fworkflows%2Fambient-context.md'
    );
    expect(rendered.querySelector('.workflow-tabs a:last-child')?.getAttribute('href')).toBe(
      '#page-workflow-runs?workflow=githubnext%2Fgh-cao%3A.github%2Fworkflows%2Fambient-context.md'
    );
    expect([...rendered.querySelectorAll('.workflow-identity .workflow-badge')].map((badge) => badge.textContent)).toEqual([
      'Orchestrator',
      'Campaign · Ambient Context'
    ]);
    expect([...rendered.querySelectorAll('.workflow-identity .workflow-badge-operation')].map((badge) => badge.getAttribute('href'))).toEqual([
      '#page-campaign-insights?campaign=ambient-context'
    ]);
    expect(allocation.mock.calls.at(-1)?.[0]).toMatchObject({
      detail: {
        titleLink: {
          href: 'https://github.com/githubnext/gh-cao/blob/HEAD/.github/workflows/ambient-context.md',
          label: 'Open Ambient Context workflow on GitHub'
        }
      }
    });
    expect(rendered.querySelector('.workflow-identity > a')?.getAttribute('href')).toBe(
      'https://github.com/githubnext/gh-cao/blob/HEAD/.github/workflows/ambient-context.md'
    );
    expect(rendered.querySelector('.workflow-identity > a')?.textContent).toBe('View authored workflow');
    expect(rendered.querySelector('.workflow-identity > a')?.getAttribute('target')).toBe('_blank');
    expect(rendered.textContent).not.toContain('Open report');
    expect(allocation).toHaveBeenCalledOnce();
    expect(allocation.mock.calls[0][0].detail).toEqual({
      title: 'Ambient Context',
      description: 'Durable reports produced by .github/workflows/ambient-context.md in githubnext/gh-cao.',
      titleLink: {
        href: 'https://github.com/githubnext/gh-cao/blob/HEAD/.github/workflows/ambient-context.md',
        label: 'Open Ambient Context workflow on GitHub'
      },
      mode: 'review',
      navigationPage: 'repositories',
      breadcrumbs: [
        { label: 'Repositories', href: '#page-repositories' },
        {
          label: 'githubnext/gh-cao',
          href: '#page-repository-detail?repository=githubnext%2Fgh-cao'
        }
      ]
    });
  });

  it('renders Runs as the current workflow tab with declarative route composition', () => {
    const host = document.createElement('div');
    const allocation = vi.fn();
    host.addEventListener('dashboard-route-allocation', allocation);
    const rendered = renderWorkflowRoutePage({
      ...context('workflow-runs'),
      elementConfig: { body: 'runs' }
    });
    host.append(rendered);

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: 'workflow',
        value: 'githubnext/gh-cao:.github/workflows/ambient-context.md'
      }
    }));

    expect(rendered.querySelector('.workflow-tabs [aria-current="page"]')?.textContent).toBe('Runs');
    expect(rendered.querySelector('.workflow-tabs a:nth-child(2)')?.getAttribute('href')).toBe(
      '#page-workflow-detail?workflow=githubnext%2Fgh-cao%3A.github%2Fworkflows%2Fambient-context.md'
    );
    expect(allocation.mock.calls[0][0].detail.description).toBe(
      'Observed runs for .github/workflows/ambient-context.md in githubnext/gh-cao.'
    );
  });

  it('uses declarative route view ids to choose the runs composition', () => {
    const rendered = renderWorkflowRoutePage({
      ...context('custom-workflow-page'),
      element: 'workflow-runs',
      elementConfig: { body: 'runs' }
    });

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: 'workflow',
        value: 'githubnext/gh-cao:.github/workflows/ambient-context.md'
      }
    }));

    expect(rendered.querySelector('.workflow-tabs [aria-current="page"]')?.textContent).toBe('Runs');
  });

  it('uses the declarative route view id instead of page identity', () => {
    const rendered = renderWorkflowRoutePage({
      ...context('workflow-detail'),
      pageId: 'totally-custom-page',
      element: 'workflow-detail',
      elementConfig: { body: 'runs' }
    });

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: 'workflow',
        value: 'githubnext/gh-cao:.github/workflows/ambient-context.md'
      }
    }));

    expect(rendered.querySelector('.workflow-tabs [aria-current="page"]')?.textContent).toBe('Runs');
  });

  it('reuses the shared route page shell for workflow tabs and chrome', () => {
    const rendered = renderWorkflowRoutePage(context());

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: 'workflow',
        value: 'githubnext/gh-cao:.github/workflows/ambient-context.md'
      }
    }));

    expect(rendered.querySelector('[data-route-view]')).toBeNull();
    expect(rendered.querySelector('.workflow-tabs')).not.toBeNull();
    expect(rendered.querySelector('.workflow-identity')).not.toBeNull();
  });

  it('renders explicit empty states for missing and invalid workflow routes', () => {
    const rendered = renderWorkflowRoutePage(context());
    expect(rendered.textContent).toBe('Select a workflow to view its reports.');

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'workflow', value: '<invalid>' }
    }));
    expect(rendered.textContent).toBe('Select a workflow to view its reports.');

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: 'workflow',
        value: 'githubnext/gh-cao:.github/workflows/missing.md'
      }
    }));
    expect(rendered.textContent).toBe('Workflow not found.');

    rendered.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: 'workflow',
        value: 'githubnext/gh-cao:.github/workflows/release@prod.md'
      }
    }));
    expect(rendered.textContent).toBe('Workflow not found.');
    const updated = context();
    updated.sources.workflows.rows = [updated.sources.workflows.rows[2]];
    const selected = renderWorkflowRoutePage(updated);
    selected.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: { parameter: 'workflow', value: 'githubnext/gh-cao:.github/workflows/release@prod.md' }
    }));
    expect(selected.textContent).toContain('.github/workflows/release@prod.md');
  });
});
