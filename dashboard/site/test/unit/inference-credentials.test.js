import { describe, expect, it } from 'vitest';
import { renderInferenceCredentials } from '../../src/components/inference-credentials.js';
import { processDataRequest } from '../../src/data-worker.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const sourceName = 'inference-credential-repositories';
/** @type {NonNullable<import('../../src/presenter.js').LogicalSourceInput['metadata']>} */
const metadata = {
  'source-id': 'workflow-inventory', 'source-kind': 'test',
  'as-of': '2026-11-27T00:00:00Z', 'retrieved-at': '2026-11-27T00:00:00Z',
  availability: 'available', completeness: 'complete', freshness: 'fresh'
};

/** @param {import('../../src/presenter.js').LogicalSourceInput} [source] */
const render = (source) => renderInferenceCredentials({
  pageId: 'inference-credentials', title: 'Credential setup', headingTag: 'h2',
  sourceNames: [sourceName], sources: source ? { [sourceName]: source } : {},
  contextDetails: [], githubUrlBase: 'https://example.ghe.com'
});

describe('inference credentials', () => {
  it('uses the worker query to detect distinct engines per repository, with unknown evidence', async () => {
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      processDataRequest({
        operation: 'execute-dashboard-queries', queries: authoritativeDashboard.dashboard.queries,
        sourceNames: [sourceName],
        sources: { workflows: { source: 'workflows', metadata, rows: [
          { organization: 'octo', repository: 'agents', workflow: '.github/workflows/triage.md', 'workflow-engine': 'copilot' },
          { organization: 'octo', repository: 'agents', workflow: '.github/workflows/review.lock.yml', 'workflow-engine': 'claude' },
          { organization: 'octo', repository: 'agents', workflow: '.github/workflows/missing.md' },
          { organization: 'octo', repository: 'agents', workflow: '.github/workflows/ordinary.yml' },
          { organization: 'octo', repository: 'other', workflow: '.github/workflows/docs.md', 'workflow-engine': 'codex' }
        ] } }
      })
    );
    expect(result[sourceName].rows).toEqual([
      { 'repository-coordinate': 'octo/agents', engines: 'claude, copilot', 'unknown-engine-count': 1 },
      { 'repository-coordinate': 'octo/other', engines: 'codex', 'unknown-engine-count': 0 }
    ]);
    const root = render(result[sourceName]);
    document.body.append(root);
    try {
      const select = root.querySelector('select');
      if (!select) throw new Error('Missing repository selector');
      expect(root.querySelectorAll('a')).toHaveLength(0);
      select.selectedIndex = 1;
      select.dispatchEvent(new Event('change'));
      await Promise.resolve();
      expect(root.textContent).toContain('1 workflow(s) have no engine metadata');
      expect(root.textContent).toContain('Organization billing (no secret)');
      expect(root.textContent).toContain('GitHub App for inference');
      expect(root.textContent).toContain('CAO token broker');
      expect(root.querySelector('a[href="https://example.ghe.com/octo/agents/settings/secrets/actions/new"]')).not.toBeNull();
      expect(root.textContent).not.toContain('OPENAI_API_KEY');
      select.selectedIndex = 2;
      select.dispatchEvent(new Event('change'));
      await Promise.resolve();
      expect(root.textContent).toContain('OPENAI_API_KEY');
      expect(root.textContent).toContain('GH_AW_GITHUB_READ_APP_PRIVATE_KEY');
      expect(root.textContent).not.toContain('ANTHROPIC_API_KEY');
      expect(root.querySelector('input, form')).toBeNull();
    } finally {
      root.remove();
    }
  });

  it('does not present unavailable or partial evidence as complete', () => {
    expect(render().textContent).toContain('inventory is unavailable');
    expect(render({ source: sourceName, rows: [], metadata: { ...metadata, completeness: 'partial' } }).textContent)
      .toContain('may be incomplete');
  });
});
