import { authoritativeDashboard, buildPresenterModuleUrl, expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';
import { getPrimerStyles } from '../../src/styles.js';
import { fileURLToPath } from 'node:url';
import { writeCanvasArtifact } from './canvas-fixture.js';

const { startCanvasServer } = await import(new URL('../../../canvas-server.mjs', import.meta.url).href);

registerSmokeRoutes();

for (const width of [390, 1280]) {
  test(`JSON wizard supports Campaign design, keyboard navigation and prompt copy at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    const wizardPage = structuredClone(authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'wizard'));
    const configuration = /** @type {import('../../src/prompt-wizard.js').PromptWizard} */ (wizardPage.views[0].config.wizard);
    const engine = configuration.steps.flatMap((step) => step.fields).find((field) => field.id === 'engine');
    if (!engine?.options?.[1]) throw new Error('Expected an alternative engine.');
    const alternativeEngine = engine.options[1];
    alternativeEngine.defaults = { artifact: '' };
    await page.evaluate(() => { window.location.hash = 'page-wizard'; });
    await page.setContent(`
      <div id="root"></div>
      <script type="module">
        import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
        document.querySelector('#root').append(renderDashboard({
          document: { languageVersion: '0.1.0', dashboard: {
            id: 'wizard-browser-contract', title: 'Workflow design',
            pages: [${JSON.stringify(wizardPage)}],
            navigation: [{ pages: ['wizard'] }]
          } }, sources: {}
        }));
      </script>
    `);
    await page.addStyleTag({ content: getPrimerStyles() });
    const root = page.locator('.prompt-wizard');
    await expect(root).toBeVisible();
    await expect(page.locator('.title-area [data-page-experimental]')).toBeVisible();
    const tabs = root.locator('.prompt-wizard-progress button');
    await expect(tabs).toHaveCount(3);
    await expect(tabs.nth(2)).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Review', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeHidden();
    await expect(page.getByLabel('What should the agent do? (optional)')).toBeHidden();
    await expect(page.getByLabel('Workflow or campaign name (required)')).toHaveCount(0);
    const choices = page.locator('.prompt-wizard-cards .entity-card-list-marketplace');
    await expect(choices.locator('.prompt-wizard-card')).toHaveCount(35);
    await page.getByRole('button', { name: 'Issue Triage', exact: true }).click();
    await expect(page.getByLabel('What do you want to generate? (required)')).toBeFocused();
    await expect(choices).toBeHidden();
    await page.getByLabel('What do you want to generate? (required)').selectOption('campaign');
    await page.getByLabel('What should the agent do? (optional)').fill('Label new issues and leave uncertain reports for human review.');
    await page.getByLabel('Intent and acceptance criteria (optional)').fill('Never close issues. Use the existing label taxonomy.');
    await page.getByLabel('Intent and acceptance criteria (optional)').press('Tab');
    await page.getByRole('button', { name: 'Next', exact: true }).click();
    await expect(page.getByLabel('Campaign target repositories (required)')).toBeFocused();
    await expect(tabs.nth(2)).toHaveAttribute('aria-current', 'step');
    await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeHidden();
    await page.getByLabel('Per-run scope and limits (optional)').fill('One dispatched target, one issue per worker.');
    await page.getByRole('button', { name: 'Generate', exact: true }).click();
    await expect(page.getByRole('dialog')).not.toBeVisible();
    await expect(page.getByLabel('Campaign target repositories (required)')).toBeFocused();
    await expect(root.getByRole('alert')).toContainText('Cannot generate prompt.');
    await expect(root.getByRole('alert')).toContainText('Step 3: Scope and behavior');
    await expect(page.getByLabel('Campaign target repositories (required)')).toHaveAccessibleDescription('Campaign target repositories is required.');
    await expect(root.locator('.prompt-wizard-field-error').filter({ hasText: 'Campaign target repositories is required.' })).toBeInViewport();
    await page.getByLabel('Campaign target repositories (required)').fill('acme/one\nacme/two');
    await expect(page.getByRole('checkbox', { name: 'add comment', exact: false })).toBeChecked();
    await expect(page.getByRole('checkbox', { name: 'add labels', exact: false })).toBeChecked();
    await expect(page.getByRole('region', { name: 'Review your design', exact: true })).toHaveCount(0);
    const generate = page.getByRole('button', { name: 'Generate', exact: true });
    await expect(page.locator('.prompt-wizard-actions')).toContainText('Generate');
    const navigation = page.locator('.prompt-wizard-actions');
    const [generateBox, navigationBox] = await Promise.all([generate.boundingBox(), navigation.boundingBox()]);
    if (!generateBox || !navigationBox) throw new Error('Scope and behavior navigation must be visible.');
    expect(Math.abs(generateBox.x + generateBox.width - navigationBox.x - navigationBox.width)).toBeLessThanOrEqual(2);
    await page.getByRole('button', { name: 'Generate', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Generate prompt preview' });
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('.table-intent-preview')).toContainText('acme/one');
    await expect(dialog.locator('.table-intent-preview')).toContainText('default to review mode');
    await expect(dialog.locator('.table-intent-preview')).not.toContainText('recommended_triggers');
    await expect(dialog.locator('.table-intent-preview')).toContainText('Create a Pull Request for the following task:');
    await expect(dialog.getByRole('button', { name: 'Copy prompt' })).toBeVisible();
    const bounds = await dialog.boundingBox();
    expect(bounds?.width).toBeLessThanOrEqual(width);
    await page.keyboard.press('Escape');
    await expect(dialog).not.toBeVisible();
    await expect(page.getByRole('button', { name: 'Generate', exact: true })).toBeFocused();
    await expect(page.getByLabel('Campaign target repositories (required)')).toHaveValue('acme/one\nacme/two');
    await tabs.nth(1).focus();
    await tabs.nth(1).press('Enter');
    await expect(page.getByLabel('What do you want to generate? (required)')).toBeFocused();
    await expect(page.getByLabel('What should the agent do? (optional)')).toHaveValue('Label new issues and leave uncertain reports for human review.');
    await tabs.nth(0).focus();
    await tabs.nth(0).press('Space');
    const selected = page.getByRole('button', { name: 'Issue Triage', exact: true });
    await expect(selected).toHaveAttribute('aria-pressed', 'true');
    await expect(selected).toBeFocused();
    await selected.press('Enter');
    await expect(page.getByLabel('What do you want to generate? (required)')).toBeFocused();
    await page.getByRole('button', { name: 'Next', exact: true }).click();
    await page.getByLabel('Agent engine (required)').selectOption(alternativeEngine.value);
    await page.getByRole('button', { name: 'Generate', exact: true }).click();
    await expect(dialog).not.toBeVisible();
    await expect(page.getByLabel('What do you want to generate? (required)')).toBeFocused();
    await expect(tabs.nth(1)).toContainText('needs attention');
    await expect(root.getByRole('alert')).toContainText('Step 2: Describe your goal');
    await expect(root.locator('.prompt-wizard-field-error').filter({ hasText: 'What do you want to generate? is required.' })).toBeInViewport();
    await page.getByLabel('What do you want to generate? (required)').selectOption('workflow');
    await page.getByRole('button', { name: 'Next', exact: true }).click();
    await page.getByRole('button', { name: 'Generate', exact: true }).click();
    await expect(dialog).toBeVisible();
    await page.keyboard.press('Escape');
    expect(await root.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  });
}

test.describe('populated dashboard wizard', () => {
  for (const { width, app } of [
    { width: 390, app: false }, { width: 594, app: false }, { width: 1280, app: false },
    { width: 390, app: true }, { width: 1280, app: true },
  ]) {
    test(`full-site workflow prompt ${app ? 'starts an app session' : 'copies'} without command execution at ${width}px`, async ({ page }, testInfo) => {
      /** @type {Array<{ id: string, command: string, input?: string }>} */
      const commands = [];
      /** @type {string[]} */
      const sessions = [];
      const server = await startCanvasServer({
        workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
        repository: 'acme/control', port: 0,
        approveCliAction: async () => true,
        cliQueuePath: testInfo.outputPath('.cao', 'cli-actions.sqlite'),
        cliQueueAdmission: async () => ({ concurrency: 1 }),
        output: () => {}, requestOutput: () => {}, traceOutput: () => {},
        startSession: app ? async (/** @type {string} */ prompt) => { sessions.push(prompt); } : undefined,
        executeCliAction: async (/** @type {{ id: string, command: string, input?: string }} */ request) => {
          commands.push({ id: request.id, command: request.command, input: request.input });
          return { ok: true, exitCode: 0 };
        },
        downloadData: async (/** @type {string} */ destination) => {
          const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
          await writeCanvasArtifact(destination, { sources: {
            repositories: { metadata, rows: [{ organization: 'acme', repository: 'control' }] },
            workflows: { metadata, rows: [{ organization: 'acme', repository: 'control', workflow: 'demo.md', 'workflow-name': 'Demo' }] }
          } });
        }
      });
      try {
        await page.addInitScript(() => {
          IDBFactory.prototype.open = () => { throw new Error('Go canvas must not open browser IndexedDB.'); };
          Object.defineProperty(navigator, 'clipboard', {
            value: { writeText: async (/** @type {string} */ text) => { Reflect.set(window, 'copiedWizardPrompt', text); } }
          });
        });
        await page.setViewportSize({ width, height: 900 });
        await page.goto(`${server.url}/#page-wizard`);
        await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveAttribute('content', 'server-http');
        await expect(page.locator('.prompt-wizard')).toBeVisible();
        await expect(page.locator('.title-area [data-page-experimental]')).toBeVisible();
        const experimentalNavigation = page.locator('.primary-nav [data-nav-section="Experimental"]');
        await expect(experimentalNavigation).toHaveAttribute('open', '');
        await expect(experimentalNavigation.getByRole('link', {
          name: 'Workflow wizard, Experimental', exact: true, includeHidden: true
        })).toHaveCount(1);
        await expect(page.locator('.first-load-overlay')).toHaveCount(0);
        await expect(page.getByRole('button', { name: 'Custom automation', exact: true })).toBeVisible();
        const cardContent = page.locator('.prompt-wizard-card .issue-list-card-content').first();
        const description = page.locator('.prompt-wizard-card .issue-list-card-subtitle').first();
        await expect(description).toHaveText('Design an automation from your own requirements.');
        expect(await cardContent.evaluate((element) => getComputedStyle(element).gridColumnStart)).toBe('1');
        expect(await cardContent.evaluate((element) => getComputedStyle(element).gridRowStart)).toBe('2');
        await expect(page.getByText('Use this type', { exact: true })).toHaveCount(0);
        const typeScreenshot = testInfo.outputPath(`workflow-wizard-types-${width}.png`);
        await page.screenshot({ path: typeScreenshot, fullPage: false });
        await testInfo.attach(`Workflow type marketplace (${width}px)`, { path: typeScreenshot, contentType: 'image/png' });
        await page.getByRole('button', { name: 'Issue Triage', exact: true }).click();
        const purpose = page.getByLabel('What should the agent do? (optional)');
        const acceptance = page.getByLabel('Intent and acceptance criteria (optional)');
        await expect(purpose).not.toHaveAttribute('required');
        await expect(acceptance).not.toHaveAttribute('required');
        await expect(purpose).toHaveValue('');
        await expect(acceptance).toHaveValue('');
        const goalScreenshot = testInfo.outputPath(`workflow-wizard-optional-${width}.png`);
        await page.screenshot({ path: goalScreenshot, fullPage: false });
        await testInfo.attach(`Optional scenario refinements (${width}px)`, { path: goalScreenshot, contentType: 'image/png' });
        await page.getByRole('button', { name: 'Next', exact: true }).click();
        await expect(page.getByLabel('Campaign target repositories (required)')).toBeHidden();
        await expect(page.getByRole('checkbox', { name: 'add comment', exact: false })).toBeChecked();
        await expect(page.getByRole('checkbox', { name: 'add labels', exact: false })).toBeChecked();
        const scope = page.getByLabel('Per-run scope and limits (optional)');
        const constraints = page.getByLabel('Safety constraints and non-goals (optional)');
        await expect(scope).not.toHaveAttribute('required');
        await expect(constraints).not.toHaveAttribute('required');
        await expect(scope).toHaveValue('');
        await constraints.fill('');
        const engineBottom = await page.getByLabel('Agent engine (required)').evaluate((element) => element.getBoundingClientRect().bottom);
        const scopeTop = await scope.evaluate((element) => element.getBoundingClientRect().top);
        const scopeBottom = await scope.evaluate((element) => element.getBoundingClientRect().bottom);
        const constraintsTop = await constraints.evaluate((element) => element.getBoundingClientRect().top);
        expect(scopeTop).toBeGreaterThan(engineBottom);
        expect(constraintsTop).toBeGreaterThan(scopeBottom);
        const capabilities = page.getByRole('group', { name: 'Optional capabilities', exact: true });
        await expect(capabilities).toContainText('Remember across runs');
        await expect(capabilities).toContainText('avoid duplicates');
        await expect(capabilities).toContainText('upload-assets safe output');
        await expect(capabilities).toContainText('Use Playwright');
        await expect(capabilities).toContainText('inspect workflow sources and run evidence');
        await expect(capabilities).toContainText('symbol lookup, references and diagnostics');
        const actions = page.locator('.prompt-wizard-actions');
        await expect(actions).toHaveCSS('position', 'static');
        expect(await actions.evaluate((element) => element === element.parentElement?.lastElementChild)).toBe(true);
        const fieldsBottom = await constraints.evaluate((element) => element.getBoundingClientRect().bottom);
        const actionsTop = await actions.evaluate((element) => element.getBoundingClientRect().top);
        expect(actionsTop).toBeGreaterThan(fieldsBottom);
        await expect(page.getByText('Upstream dataset license', { exact: true })).toHaveCount(0);
        await expect(page.getByRole('link', { name: 'Patterns from gh-aw-wizard', exact: true })).toHaveCount(0);
        const behaviorScreenshot = testInfo.outputPath(`workflow-wizard-bottom-${width}.png`);
        await page.screenshot({ path: behaviorScreenshot, fullPage: true });
        await testInfo.attach(`Optional behavior and bottom navigation (${width}px)`, { path: behaviorScreenshot, contentType: 'image/png' });
        await expect(page.locator('.prompt-wizard-progress button')).toHaveCount(3);
        await expect(page.getByRole('button', { name: 'Review', exact: true })).toHaveCount(0);
        await expect(page.getByRole('button', { name: 'Generate', exact: true })).toBeVisible();
        await page.locator('.prompt-wizard-progress button').nth(1).click();
        await expect(page.getByLabel('What do you want to generate? (required)')).toBeFocused();
        await page.getByRole('button', { name: 'Next', exact: true }).click();
        const manualTrigger = page.getByRole('checkbox', { name: 'workflow dispatch', exact: false });
        await manualTrigger.uncheck();
        await page.getByRole('button', { name: 'Generate', exact: true }).click();
        await expect(page.getByRole('dialog')).not.toBeVisible();
        await expect(page.getByRole('checkbox', { name: 'issues when a new issue is opened', exact: true })).toBeFocused();
        const triggerError = page.locator('.prompt-wizard-field-error').filter({ hasText: 'When should it run? is required.' });
        await expect(triggerError).toBeInViewport();
        await expect(manualTrigger).toHaveAttribute('aria-invalid', 'true');
        const inlineErrorScreenshot = testInfo.outputPath(`workflow-wizard-inline-error-${width}.png`);
        await page.screenshot({ path: inlineErrorScreenshot, fullPage: false });
        await testInfo.attach(`Visible trigger error (${width}px)`, { path: inlineErrorScreenshot, contentType: 'image/png' });
        const validationSummary = page.locator('.prompt-wizard-errors');
        await expect(validationSummary).toContainText('Cannot generate prompt.');
        await expect(validationSummary).toContainText('When should it run? is required.');
        await validationSummary.scrollIntoViewIfNeeded();
        const validationScreenshot = testInfo.outputPath(`workflow-wizard-validation-${width}.png`);
        await page.screenshot({ path: validationScreenshot, fullPage: false });
        await testInfo.attach(`Actionable validation summary (${width}px)`, { path: validationScreenshot, contentType: 'image/png' });
        await validationSummary.getByRole('button', { name: 'Fix When should it run? - Step 3: Scope and behavior', exact: true }).click();
        await expect(triggerError).toBeInViewport();
        await manualTrigger.check();
        await expect(validationSummary).toBeHidden();
        await expect(manualTrigger).not.toHaveAttribute('aria-invalid');
        await page.getByRole('button', { name: 'Generate', exact: true }).click();
        expect(commands).toEqual([]);
        expect(sessions).toEqual([]);
        await expect(page.getByRole('button', { name: 'Start agent task', exact: true })).toHaveCount(0);
        expect(await page.evaluate(() => Reflect.get(window, 'copiedWizardPrompt'))).toBeUndefined();
        const screenshot = testInfo.outputPath(`workflow-wizard-prompt-${width}.png`);
        await page.screenshot({ path: screenshot, fullPage: true });
        await testInfo.attach(`Workflow wizard prompt (${width}px)`, { path: screenshot, contentType: 'image/png' });
        let copied;
        if (app) {
          await expect(page.getByRole('dialog').getByRole('button', { name: 'Copy prompt', exact: true })).toHaveCount(0);
          await page.getByRole('dialog').getByRole('button', { name: 'Start session', exact: true }).click();
          await expect(page.getByRole('dialog')).toContainText('Session started in the Copilot app.');
          expect(sessions).toHaveLength(1);
          expect(await page.evaluate(() => Reflect.get(window, 'copiedWizardPrompt'))).toBeUndefined();
          copied = sessions[0];
        } else {
          await page.getByRole('dialog').getByRole('button', { name: 'Copy prompt', exact: true }).click();
          await expect(page.getByRole('dialog')).toContainText('Prompt copied.');
          copied = await page.evaluate(() => Reflect.get(window, 'copiedWizardPrompt'));
        }
        expect(copied).toMatch(/^Create a Pull Request for the following task:\n/);
        expect(copied).toContain('Classify and label new issues');
        expect(copied).toContain('infer them from the selected scenario');
        expect(copied).toContain('fixed safety and rollout boundaries below always apply');
        expect(copied).toContain('Infer a concise title and kebab-case file name');
        expect(copied).toContain('`add-comment` safe output');
        for (const forbidden of ['Historical pattern provenance', 'top_repos', 'anti_patterns', 'recommended_tools', '"config"', 'User design (JSON)']) {
          expect(copied).not.toContain(forbidden);
        }
        expect(copied).not.toMatch(/campaign/i);
        expect(commands).toEqual([]);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      } finally {
        await page.goto('about:blank');
        await server.close();
      }
    });
  }
});
