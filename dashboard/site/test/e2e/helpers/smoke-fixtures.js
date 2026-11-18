import { readFileSync, existsSync } from 'node:fs';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect, devices } from '@playwright/test';
import { DATABASE_NAME } from '../../../src/data/storage/indexeddb.js';
import { authoritativeDashboard } from '../../authoritative-dashboard.js';

const siteRoot = fileURLToPath(new URL('../../..', import.meta.url));
export { authoritativeDashboard };

export function registerSmokeRoutes() {
  test.beforeEach(async ({ page, context }) => {
    await context.route('http://dashboard.test/**', async (route) => {
      const url = new URL(route.request().url());
      const pathname = url.pathname;

      if (pathname === '/' || pathname === '/index.html') {
        await route.fulfill({ contentType: 'text/html', body: '<div id="root"></div>' });
        return;
      }

      const filePath = join(
        siteRoot,
        pathname.startsWith('/report/') || pathname.startsWith('/lib/') ? '..' : '.',
        pathname
      );
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
}

export { assert, DATABASE_NAME, devices, expect, readFileSync, test };

export function buildPresenterModuleUrl() {
  return 'http://dashboard.test/src/presenter.js';
}

/** @param {import('@playwright/test').Page} page @param {'chart'|'card'|'table'} mode */
export async function selectViewMode(page, mode) {
  const pageId = await page.evaluate(() => location.hash.match(/^#page-([a-z0-9-]+)/)?.[1]);
  const active = pageId ? page.locator(`[data-page-id="${pageId}"]`) : page.locator('.dashboard-page:visible');
  await expect(active).toBeVisible();
  for (let attempt = 0; attempt < 3; attempt++) {
    await expect(active).not.toHaveAttribute('data-page-pending', '');
    await expect(active).toHaveAttribute('data-view-mode', /^(chart|card|table)$/);
    const previous = await active.getAttribute('data-view-mode');
    if (previous === mode) return;
    const modes = (await active.getAttribute('data-view-modes'))?.split(' ') ?? [];
    const next = modes[(modes.indexOf(previous ?? '') + 1) % modes.length];
    await page.locator('.mobile-view-mode-toggle').click();
    await expect(active).toHaveAttribute('data-view-mode', next);
  }
  await expect(active).toHaveAttribute('data-view-mode', mode);
}

/**
 * @param {string} pageId
 * @param {Record<string, unknown>} [overrides]
 */
export function builtInPage(pageId, overrides = {}) {
  const template = authoritativeDashboard.dashboard.pages.find((/** @type {{ kind?: string, page?: string }} */ page) => (
    page.kind === 'built-in' && page.page === pageId
  ));
  assert(template, `Missing built-in page template for ${pageId}`);
  return {
    ...template,
    ...overrides,
    definition: template.definition,
  };
}

/**
 * @param {number} actual
 * @param {number} expected
 * @param {number} tolerance
 */
export function expectLayoutWithin(actual, expected, tolerance) {
  expect(Math.abs(actual - expected)).toBeLessThan(tolerance);
}

/**
 * Full-view table filters live inside the table scroller but remain contained
 * within the visible viewport instead of owning a separate horizontal scrollbar;
 * wrapping is allowed on narrow screens so controls stay reachable.
 * @param {import('@playwright/test').Locator} tableFilter The `.table-scroll > .table-filter` element.
 */
export async function expectTableFilterIsContained(tableFilter) {
  await expect.poll(async () => tableFilter.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
}

/**
 * Lazy-list windows move one page at a time when the user reaches the top edge;
 * each step must keep compact full-view mode active and keep the rendered
 * two-page, 50-row window intact.
 * @param {import('@playwright/test').Locator} scroll Table scroll region to move to the top edge.
 * @param {import('@playwright/test').Locator} view Full-view table view containing the rendered lazy rows.
 * @param {import('@playwright/test').Locator} root Dashboard root expected to remain in compact full-view mode.
 * @param {string} expectedFirstRowText Text fragment expected in the first rendered row after the paging step.
 */
export async function pageUpAndExpectCompactWindow(scroll, view, root, expectedFirstRowText) {
  await scroll.evaluate((element) => {
    element.scrollTop = 0;
    element.dispatchEvent(new Event('scroll'));
  });
  await expect(view.locator('tbody > tr').first()).toContainText(expectedFirstRowText);
  await expect(root).toHaveClass(/dashboard-full-view-scrolled/);
  await expect(view.locator('tbody > tr')).toHaveCount(50);
}

/** @param {import('@playwright/test').Page} page @param {string} title */
export async function hydrateView(page, title) {
  const placeholder = page.getByRole('region', { name: `Loading ${title}` });
  if (await placeholder.count() === 0) return;
  await placeholder.first().scrollIntoViewIfNeeded().catch(() => {});
  await expect(placeholder).toHaveCount(0);
}
