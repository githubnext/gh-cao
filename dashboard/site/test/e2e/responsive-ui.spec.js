import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { authoritativeDashboard as dashboardDocument } from '../authoritative-dashboard.js';

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));

test.beforeEach(async ({ page, context }) => {
  await context.route('http://dashboard.test/**', async (route) => {
    const url = new URL(route.request().url());
    const pathname = url.pathname;

    if (pathname === '/' || pathname === '/index.html') {
      await route.fulfill({ contentType: 'text/html', body: '<div id="root"></div>' });
      return;
    }

    const filePath = join(siteRoot, pathname);
    if (existsSync(filePath)) {
      const content = readFileSync(filePath);
      const mime = pathname.endsWith('.json')
        ? 'application/json'
        : pathname.endsWith('.svg')
          ? 'image/svg+xml'
          : 'application/javascript';
      await route.fulfill({ contentType: mime, body: content });
    } else {
      await route.fulfill({ status: 404 });
    }
  });

  await page.goto('http://dashboard.test/');
});

test('mobile typography uses a larger base font size without changing desktop sizing', async ({ page }) => {
  await page.evaluate(async (stylesUrl) => {
    const { getPrimerStyles } = await import(stylesUrl);
    const styles = document.createElement('style');
    styles.textContent = getPrimerStyles();
    document.head.append(styles);
  }, 'http://dashboard.test/src/styles.js');

  await page.setViewportSize({ width: 1280, height: 800 });
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize)).toBe('16px');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize)).toBe('18px');
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).fontSize)).toBe('18px');
});

test('footer omits data update details and keeps versions within the viewport', async ({ page }) => {
  await page.evaluate(async ([stylesUrl, footerUrl]) => {
    const [{ getPrimerStyles }, { renderDashboardFooter }] = await Promise.all([
      import(stylesUrl),
      import(footerUrl)
    ]);
    const styles = document.createElement('style');
    styles.textContent = getPrimerStyles();
    document.head.append(styles);
    /** @type {HTMLElement} */ (document.querySelector('#root')).append(renderDashboardFooter({
      evaluatedAt: '2026-10-07T21:47:00Z',
      caoVersion: '0.0.0-main.a7a23e16c68c',
      ghAwVersion: 'v0.91.1',
      commitSha: 'a7a23e1'.padEnd(40, '0'),
      githubUrlBase: 'https://github.com',
      dashboardRepository: 'githubnext/gh-cao'
    }));
  }, ['http://dashboard.test/src/styles.js', 'http://dashboard.test/src/components/dashboard-footer.js']);

  const footer = page.locator('.report-footer');
  const status = footer.locator('.report-footer-status');
  const versions = footer.locator('.report-footer-versions');
  await page.setViewportSize({ width: 1200, height: 800 });
  await expect(status).toHaveCount(0);
  await expect(footer.locator('time')).toHaveCount(0);
  await expect(footer.locator('.report-footer-provenance')).toHaveCount(0);

  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 800 });
    await expect(status).toBeHidden();
    await expect(versions).toBeVisible();
    await expect(versions).toContainText('CAO 0.0.0-main.a7a23e16c68c');
    await expect(versions).toContainText('gh-aw v0.91.1');
    await expect(versions).toContainText('Dashboard a7a23e1');
    const versionBox = await versions.boundingBox();
    expect(versionBox).not.toBeNull();
    if (!versionBox) throw new Error('Expected footer versions to have a layout box');
    expect(versionBox.x).toBeGreaterThanOrEqual(0);
    expect(versionBox.x + versionBox.width).toBeLessThanOrEqual(width);
  }
});

test('mobile prompt actions are accessible icon buttons with usable approval', async ({ page }) => {
  await page.evaluate(async (moduleUrls) => {
    const [{ getPrimerStyles }, { renderCliActions }, { renderPromptPreviewAction }, { normalizeAction }] = await Promise.all(
      moduleUrls.map((url) => import(url))
    );
    const styles = document.createElement('style');
    styles.textContent = getPrimerStyles();
    document.head.append(styles);
    const root = /** @type {HTMLElement} */ (document.querySelector('#root'));
    root.append(renderCliActions([
      { id: 'refresh', level: 'operate', label: 'Refresh the retained evidence for this repository',
        verb: 'refresh', icon: 'sync', command: 'gh aw compile', placement: 'settings' },
      { id: 'inspect', level: 'explore', label: 'Investigate a longer description of the current problem',
        command: 'gh aw status', placement: 'settings' }
    ], { presentation: 'settings', canExecute: true }));
    const promptAction = renderPromptPreviewAction(normalizeAction({ level: 'propose', subject: 'Observed errors',
      objective: 'Propose a fix', acceptance: 'Explain measurable impact' },
    { id: 'proposal', type: 'prompt' }), () => 'Subject: Observed errors\nObjective: Propose a fix\nAcceptance: Explain measurable impact');
    promptAction.classList.add('chart-prompt-action');
    root.append(promptAction);
  }, [
    'http://dashboard.test/src/styles.js',
    'http://dashboard.test/src/components/cli-actions.js',
    'http://dashboard.test/src/components/data-view.js',
    'http://dashboard.test/src/action-model.js'
  ]);
  for (const viewport of [{ width: 375, height: 812 }, { width: 667, height: 375 }]) {
    await page.setViewportSize(viewport);
    const triggers = page.locator('.cli-action-trigger, .table-intent-button');
    await expect(triggers).toHaveCount(3);
    await expect(triggers.last()).toHaveAccessibleName('Propose fix');
    await expect(triggers.last().locator('span')).toBeHidden();
    await expect(triggers.last()).toHaveCSS('width', '44px');
    await expect(triggers.last().locator('svg')).toBeVisible();
    for (const trigger of await triggers.all()) {
      await expect(trigger).toBeVisible();
      const box = await trigger.boundingBox();
      expect(box?.height).toBeGreaterThanOrEqual(44);
      expect(box?.x).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(viewport.width);
    }
    await triggers.first().click();
    const dialog = page.locator('.cli-action-dialog[open]');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(/Confirm the exact effect/)).toHaveCount(0);
    await expect(dialog.locator('.cli-action-command')).toHaveText('gh aw compile');
    await expect(dialog.locator('.cli-action-confirm')).toBeVisible();
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await triggers.last().click();
    await expect(page.locator('.table-intent-dialog[open]')).toContainText('Proposal request preview');
    await page.locator('.table-intent-dialog-close').click();
  }
});

test('native UI actions stay separate from agent operations and preserve local reset confirmation', async ({ page }) => {
  await page.route('http://dashboard.test/api/auth/session', (route) => route.fulfill({
    contentType: 'application/json', body: JSON.stringify({ login: 'octocat' })
  }));
  await page.route('http://dashboard.test/auth/logout', (route) => route.fulfill({ status: 204 }));
  await page.evaluate(async (moduleUrls) => {
    const meta = document.createElement('meta');
    meta.name = 'cao-auth-mode';
    meta.content = 'github';
    document.head.append(meta);
    document.cookie = 'cao_csrf=test-csrf; Path=/';
    localStorage.setItem('central-agentic-ops.dashboard.theme', 'dark');
    const [{ getPrimerStyles }, { renderResetDashboardControl }, { renderAccountMenu }] = await Promise.all(
      moduleUrls.map((url) => import(url))
    );
    const styles = document.createElement('style');
    styles.textContent = getPrimerStyles();
    document.head.append(styles);
    const root = /** @type {HTMLElement} */ (document.querySelector('#root'));
    root.append(renderResetDashboardControl({
      reload: () => { root.dataset.reset = 'complete'; }
    }));
    const menu = renderAccountMenu({ navigate: (/** @type {string} */ url) => { root.dataset.navigation = url; } });
    if (menu) root.append(menu);
  }, [
    'http://dashboard.test/src/styles.js',
    'http://dashboard.test/src/components/reset-dashboard-control.js',
    'http://dashboard.test/src/components/account-menu.js'
  ]);
  const clear = page.getByRole('button', { name: 'Reset local data', exact: true });
  const logout = page.getByRole('button', { name: 'Log out', exact: true });
  for (const viewport of [{ width: 390, height: 844 }, { width: 1280, height: 800 }]) {
    await page.setViewportSize(viewport);
    await expect(clear).toHaveAttribute('data-action-level', 'ui');
    await clear.click();
    const dialog = page.locator('.reset-dashboard-dialog[open]');
    await expect(dialog).toContainText('This action cannot be undone.');
    expect(await page.evaluate(() => localStorage.getItem('central-agentic-ops.dashboard.theme'))).toBe('dark');
    await expect(page.locator('.cli-action-dialog, .table-intent-dialog')).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(clear).toBeFocused();
  }
  await clear.click();
  await page.locator('.reset-dashboard-dialog[open]').getByRole('button', { name: 'Reset', exact: true }).click();
  await expect(page.locator('#root')).toHaveAttribute('data-reset', 'complete');
  expect(await page.evaluate(() => localStorage.getItem('central-agentic-ops.dashboard.theme'))).toBeNull();
  await page.locator('.reset-dashboard-dialog-close').click();
  await page.locator('.account-menu summary').click();
  await expect(logout).toHaveAttribute('data-action-level', 'ui');
  await expect(page.getByRole('button', { name: 'Use another GitHub account' })).toHaveAttribute('data-action-level', 'ui');
  await logout.click();
  await expect(page.locator('#root')).toHaveAttribute('data-navigation', '/auth/logged-out');
  await expect(page.locator('.cli-action-dialog, .table-intent-dialog')).toHaveCount(0);
});

test('tooltips near the viewport edge stay visible', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 600 });
  await page.evaluate(async (moduleUrls) => {
    const [{ getPrimerStyles }, { renderTooltip }] = await Promise.all(
      moduleUrls.map((url) => import(url))
    );
    const styles = document.createElement('style');
    styles.textContent = getPrimerStyles();
    document.head.append(styles);
    const anchor = document.createElement('div');
    anchor.style.cssText = 'position: fixed; left: 8px; top: 100px';
    anchor.append(renderTooltip({
      id: 'edge-tooltip',
      label: 'Tooltip details',
      icon: document.createTextNode('?'),
      content: document.createTextNode('Tooltip content.')
    }));
    document.body.append(anchor);
  }, ['http://dashboard.test/src/styles.js', 'http://dashboard.test/src/components/ui-primitives.js']);

  const tooltip = page.locator('.tooltip-help');
  await tooltip.hover();
  const bounds = await tooltip.locator('.tooltip-content').boundingBox();

  expect(bounds?.x).toBeGreaterThanOrEqual(0);
  expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(390);
});

test('mobile title bar keeps the dashboard subtitle adjacent to the page title', async ({ page }) => {
  const adjacentTitleGapTolerancePx = 2;
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(async (presenterModuleUrl) => {
    const { renderDashboard } = await import(presenterModuleUrl);
    document.querySelector('#root')?.append(renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'mobile-title-dashboard',
          title: 'gh-cao',
          repository: 'githubnext/gh-cao',
          pages: [
            { id: 'overview', kind: 'custom', title: 'Overview', description: 'Operational dashboard', views: [], sections: [] }
          ]
        }
      },
      sources: {}
    }));
  }, 'http://dashboard.test/src/presenter.js');

  const mobileHeader = page.locator('.mobile-page-header');
  await expect(mobileHeader.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  await expect(mobileHeader.locator('.mobile-brand-name')).toHaveText('gh-cao');
  await expect(mobileHeader.locator('[data-page-description]')).toHaveAttribute('aria-hidden', 'true');

  const titleGap = await mobileHeader.evaluate((element) => {
    const title = element.querySelector('h1')?.getBoundingClientRect();
    const subtitle = element.querySelector('.mobile-brand-name')?.getBoundingClientRect();
    if (!title || !subtitle) return Number.POSITIVE_INFINITY;
    return subtitle.top - title.bottom;
  });
  expect(titleGap).toBeLessThanOrEqual(adjacentTitleGapTolerancePx);
});

test('mobile title bar stays anchored to the top while the page scrolls', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(async (presenterModuleUrl) => {
    const { renderDashboard } = await import(presenterModuleUrl);
    document.querySelector('#root')?.append(renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'mobile-sticky-title-dashboard',
          title: 'gh-cao',
          repository: 'githubnext/gh-cao',
          pages: [
            { id: 'overview', kind: 'custom', title: 'Overview', description: 'Operational dashboard', views: [], sections: [] }
          ]
        }
      },
      sources: {}
    }));
    const filler = document.createElement('div');
    filler.style.height = '3000px';
    document.querySelector('.dashboard-page:not([hidden])')?.append(filler);
  }, 'http://dashboard.test/src/presenter.js');

  const header = page.locator('.org-sidebar');
  await expect(header.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  await page.evaluate(() => window.scrollTo({ top: 1200, behavior: 'instant' }));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(1000);
  await expect.poll(() => header.evaluate((element) => element.getBoundingClientRect().top)).toBe(0);
  await expect(header.getByRole('heading', { name: 'Overview', level: 1 })).toBeInViewport();

  const skipLink = page.locator('.skip-link');
  await skipLink.focus();
  await expect.poll(() => skipLink.evaluate((element) => element.getBoundingClientRect().top)).toBe(8);
  await expect.poll(() => skipLink.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
    return hit !== null && element.contains(hit);
  })).toBe(true);
});

test('notification filters and view controls are hidden on mobile', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(async ({ documentModel, presenterModuleUrl }) => {
    const { renderDashboard } = await import(presenterModuleUrl);
    document.querySelector('#root')?.append(renderDashboard({
      document: documentModel,
      sources: {
        'overview-needs-attention': {
          source: 'overview-needs-attention',
          rows: [{
            title: 'Failed workflow',
            reason: 'Two runs failed.',
            tone: 'critical'
          }],
          metadata: {
            'source-id': 'fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-20T00:00:00Z',
            'retrieved-at': '2026-09-20T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    }));
  }, {
    documentModel: {
      'language-version': dashboardDocument['language-version'],
      dashboard: {
        id: 'notifications-mobile',
        title: 'Notifications',
        'card-templates': dashboardDocument.dashboard['card-templates'],
        pages: [dashboardDocument.dashboard.pages.find(
          /** @param {{ id?: string }} page */
          (page) => page.id === 'notifications'
        )]
      }
    },
    presenterModuleUrl: 'http://dashboard.test/src/presenter.js'
  });

  const notifications = page.locator('[data-page-id="notifications"]');
  await expect(notifications).toHaveClass(/notifications-page/);
  await expect(notifications.locator(':scope > .page-chrome > .filter-bar')).toBeHidden();
  await expect(notifications.locator('.view-mode-control')).toBeHidden();
  await expect(notifications.locator('.entity-card-list-status-danger .octicon-x-circle-fill')).toBeVisible();
});

test('the desktop view mode chrome is fully hidden on mobile for non-notification pages', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(async ({ documentModel, presenterModuleUrl }) => {
    const { renderDashboard } = await import(presenterModuleUrl);
    document.querySelector('#root')?.append(renderDashboard({
      document: documentModel,
      sources: {}
    }));
  }, {
    documentModel: {
      'language-version': dashboardDocument['language-version'],
      dashboard: {
        id: 'issues-mobile',
        title: 'Issues',
        'card-templates': dashboardDocument.dashboard['card-templates'],
        pages: [dashboardDocument.dashboard.pages.find(
          /** @param {{ id?: string }} page */
          (page) => page.id === 'issues'
        )]
      }
    },
    presenterModuleUrl: 'http://dashboard.test/src/presenter.js'
  });

  const issuesPage = page.locator('[data-page-id="issues"]');
  await expect(issuesPage).not.toHaveClass(/notifications-page/);
  await expect(issuesPage.locator(':scope > .page-chrome')).toBeHidden();
  await expect(issuesPage.locator('.view-mode-control')).toBeHidden();
  await expect(page.locator('.mobile-view-mode-toggle')).toBeVisible();
});

test('notifications move in at the lower right and center on mobile', async ({ page }) => {
  await page.setContent(`
    <style id="notification-styles"></style>
    <script type="module">
      import { getPrimerStyles } from 'http://dashboard.test/src/styles.js';
      import { publishNotification } from 'http://dashboard.test/src/notification-service.js';
      document.querySelector('#notification-styles').textContent = getPrimerStyles();
      publishNotification({ message: 'Dashboard refreshed.', duration: 0 });
    </script>
  `);

  const notifications = page.locator('.dashboard-notifications');
  await expect(notifications).toBeVisible();
  await expect(notifications).toHaveCSS('right', '16px');
  await expect(notifications).toHaveCSS('width', '480px');
  await expect(page.locator('.dashboard-notification')).toHaveCSS('opacity', '1');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(notifications).toHaveCSS('width', '358px');
  const bounds = await notifications.boundingBox();
  expect(bounds).not.toBeNull();
  expect(Math.abs((bounds?.x ?? 0) + (bounds?.width ?? 0) / 2 - 195)).toBeLessThan(1);
});

test('full-view content keeps a responsive horizontal inset', async ({ page }) => {
  const styles = await page.evaluate(async (stylesUrl) => {
    const { getPrimerStyles } = await import(stylesUrl);
    return getPrimerStyles();
  }, 'http://dashboard.test/src/styles.js');
  await page.setContent(`
    <style>${styles}</style>
    <div class="dashboard-root dashboard-full-view">
      <div class="app-shell">
        <aside class="org-sidebar"></aside>
        <div class="app-main">
          <div class="top-nav"><div class="shell">Campaigns</div></div>
          <main class="dashboard-prototype">
            <div class="report-body" data-page-content>Campaigns content</div>
          </main>
        </div>
      </div>
    </div>
  `);

  const contentLayout = () => page.locator('main.dashboard-prototype').evaluate((main) => {
    const content = main.querySelector('[data-page-content]');
    if (!(content instanceof HTMLElement)) throw new Error('Expected page content.');
    const mainBounds = main.getBoundingClientRect();
    const contentBounds = content.getBoundingClientRect();
    return {
      left: contentBounds.left - mainBounds.left,
      right: mainBounds.right - contentBounds.right,
      top: contentBounds.top - mainBounds.top,
      bottom: mainBounds.bottom - contentBounds.bottom,
      scrollbarGutter: getComputedStyle(main).scrollbarGutter
    };
  });

  // The full-view modifier replaces the default stable gutter while preserving full-height content.
  await page.setViewportSize({ width: 1200, height: 800 });
  await expect(page.locator('.org-sidebar')).toHaveCSS('margin-bottom', '0px');
  await expect.poll(contentLayout).toEqual({
    left: 24,
    right: 24,
    top: 0,
    bottom: 0,
    scrollbarGutter: 'auto'
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('.org-sidebar')).toHaveCSS('margin-bottom', '8px');
  expect(await page.locator('.app-shell').evaluate((shell) => {
    const sidebar = shell.querySelector('.org-sidebar');
    const main = shell.querySelector('.app-main');
    if (!sidebar || !main) throw new Error('Expected sidebar and main content.');
    return main.getBoundingClientRect().top - sidebar.getBoundingClientRect().bottom;
  })).toBe(8);
  await expect.poll(contentLayout).toEqual({
    left: 14,
    right: 14,
    top: 0,
    bottom: 0,
    scrollbarGutter: 'auto'
  });
  await page.locator('.dashboard-root').evaluate((root) => root.classList.add('dashboard-full-view-scrolled'));
  await expect(page.locator('.org-sidebar')).toHaveCSS('margin-bottom', '0px');
});

test('full-view chart, card, and table content share the page inset', async ({ page }) => {
  const styles = await page.evaluate(async (stylesUrl) => {
    const { getPrimerStyles } = await import(stylesUrl);
    return getPrimerStyles();
  }, 'http://dashboard.test/src/styles.js');
  await page.setContent(`
    <style>${styles}</style>
    <div class="dashboard-root dashboard-full-view">
      <div class="app-shell">
        <aside class="org-sidebar"></aside>
        <div class="app-main">
          <main class="dashboard-prototype">
            <div class="custom-view-grid">
              <section class="custom-view chart-view-swimlane"><div data-view-content>Chart</div></section>
              <section class="custom-view"><div data-view-content>Cards</div></section>
              <section class="custom-view" data-view-layout="full-view"><div class="table-region"><div data-view-content>Table</div></div></section>
            </div>
          </main>
        </div>
      </div>
    </div>
  `);

  const [contentInsets, expectedInset] = await Promise.all([
    page.locator('[data-view-content]').evaluateAll((elements) => (
      elements.map((element) => element.getBoundingClientRect().x)
    )),
    page.locator('main.dashboard-prototype').evaluate((main) => {
      const pageInset = Number.parseFloat(getComputedStyle(main).getPropertyValue('--dashboard-page-padding-inline'));
      return main.getBoundingClientRect().x + (pageInset * 2);
    })
  ]);

  expect(contentInsets).toEqual([expectedInset, expectedInset, expectedInset]);
});

test('full-view chart mode scrolls without shrinking or overlapping chart sections', async ({ page }) => {
  const styles = await page.evaluate(async (stylesUrl) => {
    const { getPrimerStyles } = await import(stylesUrl);
    return getPrimerStyles();
  }, 'http://dashboard.test/src/styles.js');
  await page.setContent(`
    <style>${styles}</style>
    <div class="dashboard-root dashboard-full-view">
      <div class="app-shell">
        <aside class="org-sidebar"></aside>
        <div class="app-main">
          <main class="dashboard-prototype">
            <div class="report-body"><div class="dashboard-pages">
              <section class="dashboard-page" data-view-mode="chart">
                <div class="page-chrome">View mode</div>
                <div class="custom-view-grid">
                  <section class="custom-view chart-view-pie" data-view-layout="full" data-view-mode-content="chart">
                      <div class="chart-prompt-heading"><h3>Dispatches by campaign</h3></div>
                      <p class="view-description">Observed dispatches handled by each campaign.</p>
                      <div class="pie-chart-layout">
                        <div class="chart-widget"><svg viewBox="0 0 100 100"></svg></div>
                        <ul class="chart-legend chart-legend-pie">
                          ${'<li><i></i><span>Campaign</span><strong>100</strong><small>10%</small></li>'.repeat(10)}
                        </ul>
                      </div>
                  </section>
                  <section class="custom-view" data-view-mode-content="chart">
                    <div class="chart-prompt-heading"><h3>Repository coverage by campaign</h3></div>
                    <div class="chart-widget horizontal-bar-chart-widget">
                      <ul class="horizontal-bar-chart-list">
                        ${'<li class="horizontal-bar-chart-row"><span>Campaign</span><span class="horizontal-bar-chart-track"></span><span>100</span></li>'.repeat(10)}
                      </ul>
                    </div>
                  </section>
                  <section class="custom-view" data-view-layout="full-view" data-view-mode-content="table">
                    <div class="table-region"><div class="table-scroll">Campaign inventory</div></div>
                  </section>
                </div>
              </section>
            </div></div>
          </main>
        </div>
      </div>
    </div>
  `);

  const dashboardPage = page.locator('.dashboard-page');
  const grid = page.locator('.custom-view-grid');
  const charts = page.locator('[data-view-mode-content="chart"]');
  for (const width of [1440, 580, 390]) {
    await page.setViewportSize({ width, height: 780 });
    await expect(grid).toHaveCSS('overflow-y', 'auto');
    const [first, second] = await charts.evaluateAll((elements) => elements.map((element) => ({
      top: element.getBoundingClientRect().top,
      bottom: element.getBoundingClientRect().bottom,
      contentBottom: element.lastElementChild?.getBoundingClientRect().bottom,
      headingTop: element.querySelector('h3')?.getBoundingClientRect().top,
      headingLeft: element.querySelector('h3')?.getBoundingClientRect().left
    })));
    expect(first.contentBottom).toBeLessThanOrEqual(first.bottom + 1);
    expect(first.headingTop).toBeCloseTo(first.top, 0);
    expect(first.headingLeft).toBeGreaterThan(0);
    expect(first.headingLeft).toBe(second.headingLeft);
    expect(second.top).toBeGreaterThanOrEqual(first.bottom + 15);
    await grid.evaluate((element) => { element.scrollTop = element.scrollHeight; });
    await expect(charts.last().locator('.horizontal-bar-chart-row').last()).toBeInViewport();
    await grid.evaluate((element) => { element.scrollTop = 0; });
  }
  await dashboardPage.evaluate((element) => { element.setAttribute('data-view-mode', 'table'); });
  await expect(grid).toHaveCSS('overflow-y', 'hidden');
  await expect(charts.first()).toBeHidden();
  await expect(page.locator('.table-scroll')).toBeVisible();
});

test('mobile table and card modes use the full viewport width', async ({ page }) => {
  const styles = await page.evaluate(async (stylesUrl) => {
    const { getPrimerStyles } = await import(stylesUrl);
    return getPrimerStyles();
  }, 'http://dashboard.test/src/styles.js');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.setContent(`
    <style>${styles}</style>
    <div class="dashboard-root dashboard-full-view">
      <div class="app-shell">
        <aside class="org-sidebar"></aside>
        <div class="app-main">
          <main class="dashboard-prototype">
            <section class="dashboard-page" data-view-mode="table">
              <div class="custom-view-grid">
                <section class="custom-view" data-view-layout="full-view">
                  <div data-view-mode-content="table"><div class="table-region">Table</div></div>
                  <div data-mobile-card-list>Cards</div>
                </section>
              </div>
            </section>
          </main>
        </div>
      </div>
    </div>
  `);

  const table = page.locator('.table-region');
  const cards = page.locator('[data-mobile-card-list]');
  /** @param {import('@playwright/test').Locator} element */
  const horizontalBounds = async (element) => element.evaluate((node) => {
    const bounds = node.getBoundingClientRect();
    return { left: bounds.left, right: bounds.right };
  });

  await expect(table).toBeVisible();
  expect(await horizontalBounds(table)).toEqual({ left: 0, right: 390 });
  await page.locator('.dashboard-page').evaluate((element) => {
    element.setAttribute('data-view-mode', 'card');
  });
  await expect(cards).toBeVisible();
  expect(await horizontalBounds(cards)).toEqual({ left: 0, right: 390 });
});

const horizontalBarFixtureLabel = 'extremely-long-dependabot-update-planner.md';
const horizontalBarDesktopFixtureSuffix = 'planner.md';

/**
 * @param {import('@playwright/test').Page} page
 * @param {{ labels?: string[], sections?: string[], stageWidth?: number }} [options]
 */
async function renderHorizontalBarFixture(page, {
  labels = [horizontalBarFixtureLabel],
  sections = [],
  stageWidth = 220
} = {}) {
  await page.setContent(`
    <style id="dashboard-styles"></style>
    <main class="chart-stage" style="width: ${stageWidth}px"></main>
    <script type="module">
      import { getPrimerStyles } from 'http://dashboard.test/src/styles.js';
      import { renderChartWidget } from 'http://dashboard.test/src/components/chart-elements.js';
      document.querySelector('#dashboard-styles').textContent = getPrimerStyles();
      const labels = ${JSON.stringify(labels)};
      const sections = ${JSON.stringify(sections)};
      document.querySelector('.chart-stage').append(renderChartWidget(
        'horizontal-bar',
        labels.map((label, index) => ({ x: label, y: 27 - index, section: sections[index] })),
        [{ name: 'value', className: 'chart-series-1' }]
      ));
    </script>
  `);
}

/**
 * @param {import('@playwright/test').Locator} row
 * @param {string} suffix
 * @returns {Promise<{
 *   overflowed: boolean,
 *   overflowAmount: number,
 *   firstCharClipDistance: number,
 *   suffixLeft: number,
 *   suffixRight: number,
 *   labelLeft: number,
 *   labelRight: number,
 *   text: string,
 *   textOverflowed: boolean,
 *   textRight: number
 * }>}
 */
async function measureHorizontalBarLabel(row, suffix) {
  return row.evaluate((
    /** @type {Element} */ rowElement,
    /** @type {string} */ expectedSuffix
  ) => {
    const labelElement = rowElement.querySelector('.horizontal-bar-chart-label');
    const textElement = rowElement.querySelector('.horizontal-bar-chart-label-text');
    const textNode = [...(textElement?.childNodes ?? [])].find((node) => node.nodeType === Node.TEXT_NODE);
    if (!labelElement || typeof labelElement.getBoundingClientRect !== 'function') {
      throw new Error('Expected horizontal bar label element.');
    }
    if (!textNode) {
      throw new Error('Expected horizontal bar label text node.');
    }
    if (!textElement || typeof textElement.getBoundingClientRect !== 'function') {
      throw new Error('Expected horizontal bar label text element.');
    }
    const text = textNode.textContent ?? '';
    const suffixStart = text.lastIndexOf(expectedSuffix);
    if (suffixStart < 0) throw new Error('Expected label suffix.');
    const suffixRange = document.createRange();
    suffixRange.setStart(textNode, suffixStart);
    suffixRange.setEnd(textNode, suffixStart + expectedSuffix.length);
    const firstCharRange = document.createRange();
    firstCharRange.setStart(textNode, 0);
    firstCharRange.setEnd(textNode, 1);
    const labelBounds = labelElement.getBoundingClientRect();
    const textBounds = textElement.getBoundingClientRect();
    const suffixBounds = suffixRange.getBoundingClientRect();
    const firstCharBounds = firstCharRange.getBoundingClientRect();
    return {
      overflowed: labelElement.scrollWidth > labelElement.clientWidth,
      overflowAmount: labelElement.scrollWidth - labelElement.clientWidth,
      firstCharClipDistance: labelBounds.left - firstCharBounds.right,
      suffixLeft: suffixBounds.left,
      suffixRight: suffixBounds.right,
      labelLeft: labelBounds.left,
      labelRight: labelBounds.right,
      text: textElement.textContent ?? '',
      textOverflowed: textElement.scrollWidth > textElement.clientWidth,
      textRight: textBounds.right
    };
  }, suffix);
}

test('mobile horizontal bar labels preserve distinguishing prefixes without overflowing', async ({ page }) => {
  const labels = [
    'alpha-team-extremely-long-shared-health.md',
    'beta-team-extremely-long-shared-health.md'
  ];
  await page.setViewportSize({ width: 390, height: 844 });
  await renderHorizontalBarFixture(page, { labels });

  const renderedLabels = page.locator('.horizontal-bar-chart-label');
  await expect(renderedLabels).toHaveCount(2);
  await expect(renderedLabels.nth(0)).toHaveAttribute('title', labels[0]);
  await expect(renderedLabels.nth(1)).toHaveAttribute('aria-label', labels[1]);
  await expect(renderedLabels.nth(1)).toHaveAttribute('tabindex', '0');
  await expect(renderedLabels.nth(0)).toHaveCSS('direction', 'ltr');
  await expect(renderedLabels.nth(0).locator('.horizontal-bar-chart-label-text')).toHaveCSS('white-space', 'normal');
  await expect.poll(() => renderedLabels.evaluateAll((elements) => (
    elements.every((element) => element.scrollWidth <= element.clientWidth)
  ))).toBe(true);
  expect(await renderedLabels.evaluateAll((elements) => elements.map((element, index) => {
    const text = element.querySelector('.horizontal-bar-chart-label-text')?.firstChild;
    if (!text) return false;
    const range = document.createRange();
    range.setStart(text, 0);
    range.setEnd(text, index === 0 ? 'alpha-team'.length : 'beta-team'.length);
    const fragmentBounds = range.getBoundingClientRect();
    const labelBounds = element.getBoundingClientRect();
    return fragmentBounds.left >= labelBounds.left && fragmentBounds.right <= labelBounds.right;
  }))).toEqual([true, true]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('mobile horizontal bar sections keep repository context outside concise workflow labels', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await renderHorizontalBarFixture(page, {
    labels: ['dependabot-update-planner.md', 'dependabot-update-worker.md', 'deploy.md'],
    sections: ['githubnext/gh-cao', 'githubnext/gh-cao', 'octo/app']
  });

  await expect(page.locator('.horizontal-bar-chart-section-title'))
    .toHaveText(['githubnext/gh-cao', 'octo/app']);
  await expect(page.locator('.horizontal-bar-chart-label-text'))
    .toHaveText(['dependabot-update-planner.md', 'dependabot-update-worker.md', 'deploy.md']);
  await expect.poll(() => page.locator('.horizontal-bar-chart-label-text').evaluateAll((elements) => (
    elements.every((element) => element.scrollWidth <= element.clientWidth)
  ))).toBe(true);
});

test('desktop horizontal bar labels keep standard end truncation', async ({ page }) => {
  const expectedVisiblePrefix = 'extremely';
  await page.setViewportSize({ width: 900, height: 700 });
  await renderHorizontalBarFixture(page);

  const firstRow = page.locator('.horizontal-bar-chart-row').first();
  const label = firstRow.locator('.horizontal-bar-chart-label');
  await expect(label).toBeVisible();
  await expect(label).toHaveCSS('direction', 'ltr');
  await expect(label.locator('.horizontal-bar-chart-label-text')).toHaveCSS('display', 'block');

  const labelRendering = await measureHorizontalBarLabel(firstRow, horizontalBarDesktopFixtureSuffix);

  expect(labelRendering.text.startsWith(expectedVisiblePrefix)).toBe(true);
  expect(labelRendering.textOverflowed).toBe(true);
  expect(labelRendering.suffixRight).toBeGreaterThan(labelRendering.textRight);
});

test('large-screen horizontal bars show complete labels with shared prefixes', async ({ page }) => {
  const labels = [
    '.github/workflows/dependabot-update-planner.md',
    '.github/workflows/dependabot-update-worker.md'
  ];
  await page.setViewportSize({ width: 1400, height: 900 });
  await renderHorizontalBarFixture(page, { labels, stageWidth: 1000 });

  const renderedLabels = page.locator('.horizontal-bar-chart-label-text');
  await expect(renderedLabels).toHaveCount(labels.length);
  await expect(renderedLabels.nth(0)).toHaveText(labels[0]);
  await expect(renderedLabels.nth(1)).toHaveText(labels[1]);
  await expect.poll(() => renderedLabels.evaluateAll((elements) => (
    elements.every((element) => element.scrollWidth <= element.clientWidth)
  ))).toBe(true);
});

test('issue card labels stay compact with centered text and balanced padding', async ({ page }) => {
  await page.setContent(`
    <style id="dashboard-styles"></style>
    <ul class="issue-list-labels" style="width: 240px; height: 80px">
      <li>unknown</li>
    </ul>
    <script type="module">
      import { getPrimerStyles } from 'http://dashboard.test/src/styles.js';
      document.querySelector('#dashboard-styles').textContent = getPrimerStyles();
    </script>
  `);

  const label = page.locator('.issue-list-labels li');
  await expect(label).toHaveCSS('height', '20px');
  await expect(label).toHaveCSS('padding-left', '9px');
  await expect(label).toHaveCSS('padding-right', '9px');
  await expect(label).toHaveCSS('text-align', 'center');

  const centers = await label.evaluate((element) => {
    const labelBounds = element.getBoundingClientRect();
    const range = document.createRange();
    range.selectNodeContents(element);
    const textBounds = range.getBoundingClientRect();
    return {
      labelX: labelBounds.x + labelBounds.width / 2,
      labelY: labelBounds.y + labelBounds.height / 2,
      textX: textBounds.x + textBounds.width / 2,
      textY: textBounds.y + textBounds.height / 2,
    };
  });

  expect(Math.abs(centers.textX - centers.labelX)).toBeLessThanOrEqual(1);
  expect(Math.abs(centers.textY - centers.labelY)).toBeLessThanOrEqual(1);
});

test('campaign card actions wrap together on narrow screens', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(async (moduleUrls) => {
    const [stylesUrl, dataViewUrl, cliActionsUrl] = moduleUrls;
    const [{ getPrimerStyles }, { renderDataView }, { setDeclaredCliActions }] = await Promise.all([
      import(stylesUrl),
      import(dataViewUrl),
      import(cliActionsUrl)
    ]);
    document.head.append(Object.assign(document.createElement('style'), { textContent: getPrimerStyles() }));
    setDeclaredCliActions([
      { id: 'update', label: 'Update', icon: 'sync', command: 'gh aw update {{campaign}}', placement: 'row' },
      { id: 'live', label: 'Switch to live', icon: 'play', command: 'gh aw mode live {{campaign}}', placement: 'row' },
      { id: 'enable', label: 'Enable', icon: 'play', command: 'gh aw enable {{campaign}}', placement: 'row' },
      { id: 'disable', label: 'Disable', icon: 'stop', command: 'gh aw disable {{campaign}}', placement: 'row' }
    ], { canExecute: false });
    const view = renderDataView('list', {
      pageId: 'maintenance',
      title: 'Campaigns',
      view: {
        mark: 'list',
        list: { style: 'cards', icon: 'goal' },
        encoding: {
          columns: [{ field: 'campaign-name', title: 'Campaign' }],
          actions: [
            { action: 'update', presentation: 'cli-action', icon: 'sync', label: 'Update', context: ['campaign'] },
            { action: 'live', presentation: 'cli-action', icon: 'play', label: 'Switch to live', context: ['campaign'] },
            { action: 'enable', presentation: 'cli-action', icon: 'play', label: 'Enable', context: ['campaign'] },
            { action: 'disable', presentation: 'cli-action', icon: 'stop', label: 'Disable', context: ['campaign'] }
          ]
        }
      },
      sourceName: 'campaigns',
      rows: [{ campaign: 'aw-optimization', 'campaign-name': 'Optimization' }],
      metadata: { 'source-id': 'fixture', 'source-kind': 'fixture', 'as-of': '2026-09-18T00:00:00Z', 'retrieved-at': '2026-09-18T00:00:00Z', completeness: 'complete', freshness: 'fresh', availability: 'available' },
      contextDetails: [],
      headingTag: 'h3',
      prepareTableRows: (/** @type {Array<Record<string, unknown>>} */ rows) => rows,
      buildChartPoints: () => [],
      prepareChartPoints: () => [],
      toText: String
    });
    document.body.append(view);
  }, [
    'http://dashboard.test/src/styles.js',
    'http://dashboard.test/src/components/data-view.js',
    'http://dashboard.test/src/components/cli-actions.js'
  ]);

  const actions = page.locator('.document-list-card-actions');
  await expect(actions).toHaveCount(1);
  await expect(actions.locator('.table-cli-action-control')).toHaveCount(4);
  const [actionsBox, controlBox] = await Promise.all([
    actions.boundingBox(),
    actions.locator('.table-cli-action-control').first().boundingBox()
  ]);
  expect(actionsBox).not.toBeNull();
  expect(controlBox).not.toBeNull();
  if (actionsBox === null || controlBox === null) throw new Error('Expected visible campaign actions.');
  const maxWrappedRows = 2;
  const actionGap = 6;
  expect(actionsBox.height).toBeLessThanOrEqual(controlBox.height * maxWrappedRows + actionGap);
});

test('mobile chart cards keep content close to the viewport edges', async ({ page }) => {
  const mobilePageInsetPx = 14;
  const mobileCardGutterPx = 12;
  // Chart content may only be inset by the page padding plus the narrowed mobile card gutter.
  const maxContentInsetPx = mobilePageInsetPx + mobileCardGutterPx;
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(async (presenterModuleUrl) => {
    const { renderDashboard } = await import(presenterModuleUrl);
    document.querySelector('#root')?.append(renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'mobile-chart-dashboard',
          title: 'gh-cao',
          pages: [{
            id: 'cost',
            kind: 'custom',
            title: 'Cost',
            views: [{
              id: 'cost-per-campaign',
              title: 'Cost per campaign',
              data: { source: 'usage' },
              mark: 'chart',
              chart: 'pie',
              encoding: {
                x: { field: 'campaign', type: 'nominal', title: 'Campaign' },
                y: { field: 'aic', type: 'quantitative', aggregate: 'sum', title: 'AIC cost' }
              }
            }]
          }]
        }
      },
      sources: {
        usage: {
          source: 'usage',
          rows: [
            { campaign: 'eu-cra', aic: 19_255 },
            { campaign: 'cao-evolution', aic: 7657 }
          ],
          metadata: {
            'source-id': 'mobile-chart-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-01T03:00:00Z',
            'retrieved-at': '2026-09-01T03:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      }
    }));
  }, 'http://dashboard.test/src/presenter.js');

  const card = page.locator('.pie-chart-card');
  await expect(card).toBeVisible();
  await expect(card).toHaveCSS('padding-left', `${mobileCardGutterPx}px`);
  await expect(card).toHaveCSS('padding-right', `${mobileCardGutterPx}px`);

  const heading = card.getByRole('heading', { name: 'Cost per campaign' });
  const headingBox = await heading.boundingBox();
  expect(headingBox).not.toBeNull();
  if (headingBox === null) throw new Error('Expected a visible chart heading.');
  const viewportWidth = page.viewportSize()?.width ?? 0;
  expect(headingBox.x).toBeLessThanOrEqual(maxContentInsetPx);
  expect(viewportWidth - (headingBox.x + headingBox.width)).toBeLessThanOrEqual(maxContentInsetPx);
});
