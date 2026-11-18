import { selectViewMode } from './helpers/smoke-fixtures.js';
import { authoritativeDashboard, buildPresenterModuleUrl, expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';
import { getPrimerStyles } from '../../src/styles.js';

registerSmokeRoutes();

for (const theme of ['light', 'dark']) {
  test(`run cards use semantic badges and place view prompts in the toolbar in ${theme} theme`, async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.evaluate(async ({ documentModel, presenterUrl, theme }) => {
      const { renderDashboard } = await import(presenterUrl);
      const runsPage = documentModel.dashboard.pages.find(
        /** @param {{ id: string }} candidate */ (candidate) => candidate.id === 'runs'
      );
      const runsView = runsPage.definition.views.find(
        /** @param {{ id: string }} candidate */ (candidate) => candidate.id === 'runs-runs-source'
      );
      runsView.prompt = 'always';
      runsPage.definition.views = [runsView];
      documentModel.dashboard.pages = [runsPage];
      const rows = [
        { 'run-status': 'completed', 'run-conclusion': 'success' },
        { 'run-status': 'completed', 'run-conclusion': 'failure' },
        { 'run-status': 'queued', 'run-conclusion': 'unknown' }
      ].map((status, index) => ({
        ...status,
        run: String(index + 1),
        workflow: `.github/workflows/workflow-${index + 1}.md`,
        'started-at': '2026-09-14T22:00:00Z',
        'run-link': {
          relation: 'run',
          href: `https://github.com/githubnext/gh-cao/actions/runs/${index + 1}`,
          label: `Run ${index + 1}`
        }
      }));
      const root = renderDashboard({
        document: documentModel,
        sources: {
          'runs-table': {
            source: 'runs-table',
            rows,
            metadata: {
              'source-id': 'run-card-fixture', 'source-kind': 'fixture',
              'as-of': '', 'retrieved-at': '',
              availability: 'available', completeness: 'complete', freshness: 'fresh'
            }
          }
        }
      });
      root.dataset.theme = theme;
      document.querySelector('#root')?.append(root);
    }, { documentModel: structuredClone(authoritativeDashboard), presenterUrl: buildPresenterModuleUrl(), theme });
    await page.addStyleTag({ content: getPrimerStyles() });

    const view = page.locator('[data-view-id="runs-runs-source"]');
    const prompt = view.getByRole('button', { name: 'Propose fix: Runs', exact: true });
    await expect(prompt).toBeVisible();
    await selectViewMode(page, 'card');
    const cardPrompt = view.locator('.mobile-table-card-toolbar').getByRole('button', { name: 'Propose fix: Runs', exact: true });
    await expect(cardPrompt).toBeVisible();

    const cards = view.locator('[data-mobile-card-list] .entity-card-list-card');
    await expect(cards).toHaveCount(3);
    await expect(cards.first()).toBeVisible();
    const badges = cards.locator('.issue-list-labels .status');
    await expect(badges).toHaveText(['completed', 'success', 'completed', 'failure', 'queued', 'unknown']);
    await expect(cards.nth(0).locator('.status-success')).toHaveCount(2);
    await expect(cards.nth(1).locator('.status-danger')).toHaveText('failure');
    await expect(cards.nth(2).locator('.status-attention')).toHaveText('queued');
    await expect(cards.nth(2).locator('.status-muted')).toHaveText('unknown');
    const colors = await badges.evaluateAll((elements) => elements.map((element) => getComputedStyle(element).color));
    expect(new Set(colors).size).toBe(4);

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(cards.first()).toBeVisible();
    await expect(cardPrompt).toBeVisible();
    await expect(view.getByRole('button', { name: 'Propose fix: Runs', exact: true })).toHaveCount(1);

    await page.setViewportSize({ width: 1280, height: 900 });
    await selectViewMode(page, 'table');
    await expect(prompt).toBeVisible();
    await expect(view.locator('tbody tr').first()).toBeVisible();
  });
}

test('native card lists hide view prompts without hiding declared card actions', async ({ page }) => {
  await page.evaluate(async ({ presenterUrl }) => {
    const { renderDashboard } = await import(presenterUrl);
    document.querySelector('#root')?.append(renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'card-prompts', title: 'Card prompts',
          pages: [{
            id: 'cards', kind: 'custom', title: 'Cards',
            views: [{
              id: 'cards-list', title: 'Cards', prompt: 'always',
              mark: 'list', list: { style: 'cards', icon: 'play' },
              data: { source: 'runs' },
              encoding: {
                columns: [{ field: 'run', type: 'nominal' }],
                actions: [{
                  presentation: 'copy-prompt', icon: 'comment', label: 'Inspect run',
                  intent: 'Inspect this run.', context: ['run']
                }]
              }
            }]
          }]
        }
      },
      sources: {
        runs: {
          source: 'runs', rows: [{ run: '42' }],
          metadata: {
            'source-id': 'card-prompt-fixture', 'source-kind': 'fixture',
            'as-of': '', 'retrieved-at': '',
            availability: 'available', completeness: 'complete', freshness: 'fresh'
          }
        }
      }
    }));
  }, { presenterUrl: buildPresenterModuleUrl() });
  await page.addStyleTag({ content: getPrimerStyles() });
  await expect(page.getByRole('button', { name: 'Create prompt for Cards', exact: true })).toHaveCount(0);
  const rowAction = page.getByRole('button', { name: 'Inspect run', exact: true });
  await expect(rowAction).toBeVisible();
  await rowAction.click();
  await expect(page.getByRole('dialog')).toBeVisible();
});
