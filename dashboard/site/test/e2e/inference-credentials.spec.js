import { expect, test } from '@playwright/test';
import { writeServerArtifact } from './server-fixture.js';

const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

test('Updates offers repository and bulk credential setup for the selected owner', async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  const preview = await startDashboardServer({
    repository: 'acme/control',
    prepareSnapshot: async (/** @type {string} */ destination) => {
      const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
      await writeServerArtifact(destination, { sources: {
        repositories: { metadata, rows: [
          { organization: 'acme', repository: 'control' },
          { organization: 'other', repository: 'tools' }
        ] },
        workflows: { metadata, rows: [
          { organization: 'acme', repository: 'control', workflow: '.github/workflows/review.md', 'workflow-engine': 'copilot' },
          { organization: 'acme', repository: 'control', workflow: '.github/workflows/docs.md', 'workflow-engine': 'claude' },
          { organization: 'other', repository: 'tools', workflow: '.github/workflows/maintain.md', 'workflow-engine': 'codex' }
        ] }
      } });
    }
  });
  try {
    await page.setExtraHTTPHeaders(preview.headers);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-inference-credentials`);
      await expect(page.getByRole('heading', { name: 'Inference credentials', level: 1 })).toBeVisible();
      const select = page.getByRole('combobox', { name: 'Workflow repository' });
      await select.selectOption({ label: 'acme/control' });
      await page.getByText('Share credentials across acme repositories', { exact: true }).click();
      await expect(page.getByText('Selected repositories', { exact: true })).toBeVisible();
      await page.getByText('GitHub Copilot', { exact: true }).click();
      await page.getByText('Fine-grained personal access token', { exact: true }).click();
      await page.getByText('Read-only GitHub App access', { exact: true }).click();
      await expect(page.getByRole('link', { name: 'Add COPILOT_GITHUB_TOKEN in GitHub' }))
        .toHaveAttribute('href', 'https://github.com/acme/control/settings/secrets/actions/new');
      await expect(page.getByRole('link', { name: 'Share COPILOT_GITHUB_TOKEN with selected repositories' }))
        .toHaveAttribute('href', 'https://github.com/organizations/acme/settings/secrets/actions');
      await expect(page.getByRole('link', { name: 'Share GH_AW_GITHUB_READ_APP_PRIVATE_KEY with selected repositories' }))
        .toHaveAttribute('href', 'https://github.com/organizations/acme/settings/secrets/actions');
      await expect(page.getByRole('link', { name: 'Open Actions variables' })).toBeVisible();
      await expect(page.getByRole('link', { name: 'Open organization Actions variables' }))
        .toHaveAttribute('href', 'https://github.com/organizations/acme/settings/variables/actions');
      const screenshot = testInfo.outputPath(`inference-credentials-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`inference-credentials-${width}`, { path: screenshot, contentType: 'image/png' });
      await select.selectOption({ label: 'other/tools' });
      await page.getByText('OpenAI (Codex)', { exact: true }).click();
      await expect(page.getByRole('link', { name: 'Add OPENAI_API_KEY in GitHub' }))
        .toHaveAttribute('href', 'https://github.com/other/tools/settings/secrets/actions/new');
      await expect(page.getByRole('link', { name: 'Share OPENAI_API_KEY with selected repositories' }))
        .toHaveAttribute('href', 'https://github.com/organizations/other/settings/secrets/actions');
      await expect(page.getByText('GitHub Copilot', { exact: true })).toHaveCount(0);
    }
  } finally {
    await page.goto('about:blank').catch(() => {});
    await preview.close();
  }
});
