import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { authoritativeDashboard as dashboardDocument } from '../authoritative-dashboard.js';

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));
const overviewPage = dashboardDocument.dashboard.pages.find((/** @type {{ id?: string }} */ page) => page.id === 'overview');
if (!overviewPage) throw new Error('The authoritative dashboard must declare the Overview page.');

const metadata = {
  'source-id': 'overview-parity-fixture',
  'source-kind': 'fixture',
  'as-of': '2026-09-16T12:00:00Z',
  'retrieved-at': '2026-09-16T12:00:00Z',
  availability: 'available',
  completeness: 'complete',
  freshness: 'fresh'
};

/** @param {string} name @param {Record<string, unknown>[]} rows */
function source(name, rows) {
  return { source: name, rows, metadata };
}

const sources = {
  'overview-campaign-links': source('overview-campaign-links', [
    {
      campaign: 'aw-doctor',
      'campaign-name': 'AW Doctor',
      'campaign-icon': 'gear',
      'disabled-label': 'Disabled',
      'problem-indicator': 'alert',
      'problem-indicator-label': 'Current failing workflow or target partitions: 2',
      'campaign-dashboard-link': {
        'dashboard-href': '#page-campaign-insights?campaign=aw-doctor',
        'dashboard-label': 'View AW Doctor campaign dashboard'
      }
    },
    {
      campaign: 'dependabot',
      'campaign-name': 'Dependabot',
      'campaign-icon': 'dependabot',
      'disabled-label': '',
      'campaign-dashboard-link': {
        'dashboard-href': '#page-campaign-insights?campaign=dependabot',
        'dashboard-label': 'View Dependabot campaign dashboard'
      }
    }
  ]),
  'overview-outcome-summary': source('overview-outcome-summary', [{ 'useful-outputs': 2, 'delivered-repositories': 3 }]),
  'overview-run-summary': source('overview-run-summary', [{ 'successful-runs': 20, 'failed-runs': 2, 'active-runs': 4, 'active-live': 1, 'active-review': 3 }]),
  'overview-dispatch-summary': source('overview-dispatch-summary', [{ dispatches: 12, 'failed-dispatches': 1 }]),
  'overview-delivery-summary': source('overview-delivery-summary', [{ 'delivered-repositories': 3 }]),
  'overview-repository-coverage': source('overview-repository-coverage', [{ 'repository-coverage': 0.5, 'reached-repositories': 3, 'registered-repositories-total': 6 }]),
  'overview-value-summary': source('overview-value-summary', [{ 'value-gains': 1 }]),
  'overview-issue-station': source('overview-issue-station', [{
    value: 5, detail: 'Retained safe outputs', description: '5 retained issues and pull requests'
  }]),
  'overview-value-station': source('overview-value-station', [{
    value: 1, detail: 'Observed grader results', description: '1 observed grader results, not verified improvements'
  }]),
  'overview-factory-status': source('overview-factory-status', [{ 'factory-heading': 'Your factory has grader observations.' }]),
  'overview-header-presentation': source('overview-header-presentation', [{ heading: 'Your factory needs attention.' }]),
  'overview-factory-station': source('overview-factory-station', [{
    value: 0.5,
    'display-value': '50%',
    detail: '1/2 healthy workflows',
    description: '50% factory health',
    active: true
  }]),
  'overview-repository-station': source('overview-repository-station', [{
    value: 0.5,
    'display-value': '50%',
    detail: '3/6 repositories reached',
    description: '50% average repository coverage',
    active: true
  }]),
  'overview-registered-repository-summary': source('overview-registered-repository-summary', [{ 'registered-repositories': 6 }]),
  'overview-worker-summary': source('overview-worker-summary', [{ workers: 3 }]),
  'database-issue-count': source('database-issue-count', [{ issues: 5 }]),
  'overview-needs-attention-preview': source('overview-needs-attention-preview', [
    {
      kind: 'Repeated workflow failures',
      title: '.github/workflows/doctor.md',
      scope: 'githubnext/gh-cao',
      reason: '2 failed runs in the selected horizon',
      'failure-count': 2,
      action: 'Open latest failed run on GitHub',
      'observed-at': '2026-09-16T11:30:00Z',
      'evidence-link': {
        href: 'https://github.com/githubnext/gh-cao/actions/runs/42',
        label: 'View run 42'
      }
    },
    ...['101', '102'].map((id) => ({
      kind: 'Human review required',
      title: `Review output ${id}`,
      scope: 'githubnext/gh-cao',
      reason: 'Output awaits approval',
      action: 'Open review item on GitHub',
      'observed-at': '2026-09-16T10:00:00Z',
      'evidence-link': {
        href: `https://github.com/githubnext/gh-cao/issues/${id}`,
        label: `View issue ${id}`
      }
    }))
  ]),
  'campaign-inventory': source('campaign-inventory', [{
    campaign: 'aw-doctor',
    'campaign-name': 'AW Doctor',
    'campaign-dashboard-link': {
      'dashboard-href': '#page-campaign-insights?campaign=aw-doctor',
      'dashboard-label': 'View AW Doctor campaign dashboard'
    },
    workflows: 3,
    modes: ['review'],
    registration: ['active'],
    runs: 20,
    'grader-result': 7,
    dispatches: 12,
    aic: 42
  }]),
  'overview-rhythm': source('overview-rhythm', [{
    rhythm: {
      days: ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'].map((label, index) => ({
        label,
        date: `2026-09-${String(14 + index).padStart(2, '0')}`,
        current: index < 3 ? index + 1 : 0,
        previous: 7 - index,
        reached: index < 3
      }))
    }
  }])
};

test.beforeEach(async ({ page, context }) => {
  await context.route('http://dashboard.test/**', async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === '/' || pathname === '/index.html') {
      await route.fulfill({ contentType: 'text/html', body: '<div id="root"></div>' });
      return;
    }
    const filePath = join(siteRoot, pathname);
    if (!existsSync(filePath)) {
      await route.fulfill({ status: 404 });
      return;
    }
    await route.fulfill({
      contentType: pathname.endsWith('.json') ? 'application/json' : 'application/javascript',
      body: readFileSync(filePath)
    });
  });
  await page.goto('http://dashboard.test/#page-overview');
});

test('declarative Overview views preserve desktop and mobile behavior', async ({ page }) => {
  expect(overviewPage.views.every((/** @type {{ 'show-title'?: boolean, prompt?: string }} */ view) =>
    view['show-title'] === false && view.prompt === 'none')).toBe(true);
  /** @param {Record<string, unknown>} pageDefinition */
  const render = async (pageDefinition) => {
    await page.evaluate(async ({ documentModel, sourceData, presenterModuleUrl }) => {
      const { renderDashboard } = await import(presenterModuleUrl);
      const rendered = renderDashboard({
        document: documentModel,
        sources: sourceData
      });
      document.querySelector('#root')?.replaceChildren(rendered);
    }, {
      documentModel: {
        'language-version': dashboardDocument['language-version'],
        dashboard: {
          id: 'overview-parity',
          title: 'Overview parity',
          'card-templates': dashboardDocument.dashboard['card-templates'],
          pages: [pageDefinition, { id: 'other', kind: 'custom', title: 'Other', views: [] }]
        }
      },
      sourceData: sources,
      presenterModuleUrl: 'http://dashboard.test/src/presenter.js'
    });
    return page.locator('[data-page-id="overview"] > .custom-view-grid');
  };

  for (const viewport of [
    { width: 1440, height: 900, introColumns: 2, colorScheme: 'dark' },
    { width: 390, height: 844, introColumns: 1, colorScheme: 'light' }
  ]) {
    await page.setViewportSize(viewport);
    await page.emulateMedia({ colorScheme: /** @type {'dark'|'light'} */ (viewport.colorScheme) });
    const factory = await render(overviewPage);

    await expect(factory).toBeVisible();
    await expect(factory.locator(':scope > .custom-view > :is(h3, h4)')).toHaveCount(0);
    await expect(factory.locator('.semantic-prompt-action, .chart-prompt-action')).toHaveCount(0);
    await expect(factory.locator(':scope > [data-view-id="overview-header"]')).toHaveClass(/factory-intro/);
    await expect(factory.locator(':scope > [data-view-id="overview-floor"]')).toHaveClass(/factory-floor/);
    const stations = factory.locator('.factory-station');
    await expect(stations).toHaveCount(4);
    await expect(stations.locator(':scope > span:nth-child(2)')).toHaveText([
      'Factory health', 'Repository coverage', 'Issues & PRs', 'Grader observations'
    ]);
    await expect(stations.locator('strong')).toHaveText(['50%', '50%', '5', '1']);
    await expect(stations.nth(2).getByRole('link', { name: '5', exact: true })).toHaveAttribute('href', '#page-issues');
    await expect(stations.nth(3).getByRole('link', { name: '1', exact: true })).toHaveAttribute('href', '#page-graders');
    await expect(factory.locator('.factory-stations')).toHaveCSS('grid-template-columns',
      viewport.width > 700 ? /^([\d.]+px ){3}[\d.]+px$/ : /^[\d.]+px [\d.]+px$/);
    await expect(factory.locator(':scope > [data-view-id="overview-campaigns"]')).toHaveCount(0);
    await expect(factory.locator('.link-button-list')).toHaveCount(0);
    await expect(factory.getByRole('link', { name: /campaign dashboard/ })).toHaveCount(0);
    for (const view of await factory.locator(':scope > .custom-view').all()) {
      await expect(view).toHaveCSS('border-top-width', '0px');
      await expect(view).toHaveCSS('border-left-width', '0px');
    }
    await expect(factory.locator('.factory-floor')).toHaveCSS('border-bottom-width', '0px');
    const pageBounds = await page.locator('[data-page-id="overview"]').boundingBox();
    const mainBounds = await page.locator('main.dashboard-prototype').boundingBox();
    const introBounds = await factory.locator(':scope > .factory-intro').boundingBox();
    if (!pageBounds || !mainBounds) throw new Error('Overview and main bounds must be available');
    expect(pageBounds.x).toBeCloseTo(mainBounds.x, 0);
    expect(pageBounds.width).toBeCloseTo(mainBounds.width, 0);
    expect(pageBounds.y).toBeCloseTo(mainBounds.y, 0);
    expect(introBounds?.y).toBeCloseTo(mainBounds.y, 0);
    if (viewport.width > 700) {
      await expect(page.locator('.app-main > .top-nav')).toBeVisible();
    } else {
      await expect(page.locator('.mobile-page-header #page-title')).toBeVisible();
    }
    await expect(page.locator('.app-main > .report-footer')).toBeVisible();
    await expect(page.locator('[data-view-id="overview-header"] > :is(h3, h4)')).toHaveCount(0);
    for (const heading of await page.locator('.custom-view.page-section > :is(h3, h4)').all()) {
      await expect(heading).toHaveCSS('position', 'absolute');
      await expect(heading).toHaveCSS('clip', 'rect(0px, 0px, 0px, 0px)');
    }
    if (viewport.width > 700) {
      await expect(page.locator('.org-sidebar')).toBeVisible();
    } else {
      await expect(page.locator('.mobile-nav-menu')).toBeVisible();
    }
    await expect(factory.locator(':scope > .factory-intro + .factory-floor')).toHaveCount(1);
    const notifications = page.locator('[data-page-id="notifications"]');
    await expect(notifications).toHaveCount(0);
    await expect(notifications.locator('.entity-card-list-card')).toHaveCount(0);
    await expect(factory.getByRole('heading', { name: 'Your factory needs attention.' })).toBeVisible();
    await expect(factory.locator('.factory-running')).toHaveCount(0);
    await expect(factory.locator('.factory-rhythm-day')).toHaveCount(7);
    const headingPlacement = await factory.locator('.factory-rhythm').evaluate((rhythm) => {
      const title = rhythm.querySelector('.factory-rhythm-heading > span');
      const subtitle = rhythm.querySelector('.factory-rhythm-heading > strong');
      const legend = rhythm.querySelector('.factory-rhythm-legend');
      if (!title || !subtitle || !legend) throw new Error('The rhythm heading and legend must be present');
      const subtitleStyle = getComputedStyle(subtitle);
      const legendStyle = getComputedStyle(legend.querySelector('li') ?? legend);
      /** @param {CSSStyleDeclaration} style */
      const typography = (style) => ({
        fontFamily: style.fontFamily,
        fontSize: style.fontSize,
        fontStyle: style.fontStyle,
        fontWeight: style.fontWeight,
        color: style.color
      });
      return {
        title: title.textContent, subtitle: subtitle.textContent,
        subtitleTypography: typography(subtitleStyle),
        legendTypography: typography(legendStyle),
        titleBottom: title.getBoundingClientRect().bottom,
        subtitleTop: subtitle.getBoundingClientRect().top,
        subtitleBottom: subtitle.getBoundingClientRect().bottom,
        legendTop: legend.getBoundingClientRect().top
      };
    });
    expect(headingPlacement.title).toBe('Weekly rhythm');
    expect(headingPlacement.subtitle).toBe('Agentic Workflow runs');
    expect(headingPlacement.subtitleTypography).toEqual(headingPlacement.legendTypography);
    expect(headingPlacement.titleBottom).toBeLessThanOrEqual(headingPlacement.subtitleTop);
    expect(headingPlacement.subtitleBottom).toBeLessThanOrEqual(headingPlacement.legendTop);
    await expect(factory.locator('.factory-rhythm .graph-widget-y-axis-label')).toHaveCount(0);
    const rhythmBars = factory.locator('.factory-rhythm-bar-pair i:not([hidden])');
    await expect(rhythmBars).toHaveCount(10);
    await expect(factory.locator('.factory-rhythm-current[hidden]')).toHaveCount(4);
    await expect(factory.locator('.factory-rhythm-current[hidden]').first()).toHaveCSS('display', 'none');
    await expect(factory.locator('.factory-rhythm-day').nth(3))
      .toHaveAttribute('aria-label', 'Thu 2026-09-17: 4 workflow runs last week (day not yet reached).');
    const layeredDays = await factory.locator('.factory-rhythm-day').evaluateAll((days) => days.map((day) => {
      const previous = day.querySelector('.factory-rhythm-baseline');
      const current = day.querySelector('.factory-rhythm-current');
      if (!previous || !current) throw new Error('Both rhythm bars must be present');
      const previousBounds = previous.getBoundingClientRect();
      const currentBounds = current.getBoundingClientRect();
      return {
        previousBottom: previousBounds.bottom,
        currentBottom: currentBounds.bottom,
        previousLeft: previousBounds.left,
        previousRight: previousBounds.right,
        currentLeft: currentBounds.left,
        currentRight: currentBounds.right,
        currentLayer: getComputedStyle(current).zIndex
      };
    }));
    expect(layeredDays).toHaveLength(7);
    for (const layered of layeredDays.slice(0, 3)) {
      expect(layered.currentBottom).toBeCloseTo(layered.previousBottom, 0);
      expect(layered.currentLeft).toBeCloseTo(layered.previousLeft, 0);
      expect(layered.currentRight).toBeCloseTo(layered.previousRight, 0);
      expect(layered.currentLayer).toBe('1');
    }
    await expect(rhythmBars.first()).toHaveCSS('animation-name', 'factory-rhythm-bar-grow');
    expect(await rhythmBars.last().evaluate((element) => getComputedStyle(element).animationDelay)).toBe('0.21s');
    await expect(factory.locator('.factory-station')).toHaveCount(4);
    await expect(factory.locator('.factory-station').nth(0)).toContainText('Factory health50%1/2 healthy workflows');
    await expect(factory.locator('.factory-station').nth(0).getByRole('link')).toHaveAttribute('href', '#page-workflows');
    await expect(factory.locator('.factory-station').nth(1)).toContainText('Repository coverage50%3/6 repositories reached');
    expect(await factory.locator('.factory-station a').count()).toBeGreaterThan(0);
    expect(await page.locator('.factory-intro').evaluate((element) =>
      getComputedStyle(element).gridTemplateColumns.split(' ').filter(Boolean).length
    )).toBe(viewport.introColumns);
    expect(await page.locator('.factory-stations').evaluate((element) =>
      getComputedStyle(element).gridTemplateColumns.split(' ').filter(Boolean).length
    )).toBe(viewport.width > 700 ? 4 : 2);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    await page.screenshot({ path: `test-results/overview-${viewport.width}.png`, fullPage: true });
  }

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.locator('[data-nav-page-id="other"]').click();
  await expect(page.locator('.app-main > .top-nav')).toBeVisible();
  await expect(page.locator('.app-main > .report-footer')).toBeVisible();
  await expect(page.locator('main.dashboard-prototype')).toHaveCSS('padding-top', '24px');
});

test('disables Overview rhythm animation when reduced motion is preferred', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.evaluate(async ({ documentModel, sourceData, presenterModuleUrl }) => {
    const { renderDashboard } = await import(presenterModuleUrl);
    const rendered = renderDashboard({ document: documentModel, sources: sourceData });
    document.querySelector('#root')?.replaceChildren(rendered);
  }, {
    documentModel: {
      'language-version': dashboardDocument['language-version'],
      dashboard: {
        id: 'overview-reduced-motion',
        title: 'Overview reduced motion',
        'card-templates': dashboardDocument.dashboard['card-templates'],
        pages: [overviewPage]
      }
    },
    sourceData: sources,
    presenterModuleUrl: 'http://dashboard.test/src/presenter.js'
  });

  await expect(page.locator('.factory-rhythm-bar-pair i:not([hidden])').first()).toHaveCSS('animation-name', 'none');
});

test('renders the Overview structure before mixed page data resolves', async ({ page }) => {
  const immediate = await page.evaluate(async ({ documentModel, presenterModuleUrl, sourceStoreModuleUrl }) => {
    const [{ renderDashboard }, { configureSourceLoader }] = await Promise.all([
      import(presenterModuleUrl),
      import(sourceStoreModuleUrl)
    ]);
    let sourceLoadCalls = 0;
    let pageLoadCalls = 0;
    configureSourceLoader(() => {
      sourceLoadCalls += 1;
      return new Promise(() => {});
    });
    const rendered = renderDashboard({
      document: documentModel,
      sources: {},
      loadPageSources: () => {
        pageLoadCalls += 1;
        return new Promise(() => {});
      }
    });
    document.querySelector('#root')?.replaceChildren(rendered);
    const overview = rendered.querySelector('[data-page-id="overview"]');
    return {
      sourceLoadCalls,
      pageLoadCalls,
      header: Boolean(overview?.querySelector(':scope > .custom-view-grid > .factory-intro')),
      floor: Boolean(overview?.querySelector(':scope > .custom-view-grid > .factory-floor')),
      pageSkeletons: overview?.querySelectorAll('.dashboard-view-skeleton').length,
      pageBusy: overview?.getAttribute('aria-busy'),
      headerBusy: overview?.querySelector('.factory-intro')?.getAttribute('aria-busy'),
      floorBusy: overview?.querySelector('.factory-floor')?.getAttribute('aria-busy'),
      campaigns: Boolean(overview?.querySelector('.link-button-list-view')),
      campaignsBusy: overview?.querySelector('.link-button-list-view')?.getAttribute('aria-busy'),
      campaignSkeletonRows: overview?.querySelectorAll('.link-button-list-skeleton-row').length,
      headingPending: overview?.querySelectorAll('.factory-heading-pending').length,
      rhythmPending: overview?.querySelectorAll('.factory-rhythm-pending').length,
      stationsPending: overview?.querySelectorAll('.factory-station-pending').length
    };
  }, {
    documentModel: {
      'language-version': dashboardDocument['language-version'],
      dashboard: {
        id: 'overview-loading',
        title: 'Overview loading',
        pages: [overviewPage]
      }
    },
    presenterModuleUrl: 'http://dashboard.test/src/presenter.js',
    sourceStoreModuleUrl: 'http://dashboard.test/src/source-store.js'
  });

  expect(immediate).toEqual({
    sourceLoadCalls: 6,
    pageLoadCalls: 0,
    header: true,
    floor: true,
    campaigns: false,
    pageSkeletons: 0,
    pageBusy: null,
    headerBusy: null,
    floorBusy: null,
    campaignsBusy: undefined,
    campaignSkeletonRows: 0,
    headingPending: 1,
    rhythmPending: 1,
    stationsPending: 4
  });
});
