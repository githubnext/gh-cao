import { afterEach, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';
import { processDataRequest } from '../../src/data-worker.js';
import { upsertCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { dashboardPageAllSourceNames } from '../../src/dashboard-chunks.js';
import { authoritativeDashboard as document } from '../authoritative-dashboard.js';
import {
  composeImmediateDashboard,
  immediateDashboardAuditFingerprint,
  immediateDashboardSourceDependencies
} from '../../../report/compose-immediate-dashboard.mjs';
import { resolveDashboardQuerySources } from '../../src/data/queries/declarative.js';
import { validateDashboardDocument } from '../../src/validator.js';
import contract from '../fixtures/local-dashboard-contract.json' with { type: 'json' };
import profile from '../../dashboard-fragments/immediate-profile.json' with { type: 'json' };

afterEach(() => vi.unstubAllGlobals());

it('serves the shared run table through the canonical worker without fleet sources', async () => {
  const page = document.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
  const view = page.definition.views.find((/** @type {{ id: string }} */ view) => view.id === contract.view);
  expect(view.encoding.columns.map((/** @type {{ field: string }} */ column) => column.field)).toEqual(contract.fields);
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  await upsertCanonicalBatch(indexedDB, contract.batch);
  const payload = compileDashboardViewPayloadQueries(page, contract.page, {
    queries: document.dashboard.queries, viewId: contract.view, sourceNames: [contract.source]
  });

  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'query-canonical-dashboard',
    sourceNames: [...payload.aliases, ...contract.sources.filter((name) => name !== contract.source)],
    context: { queries: [...document.dashboard.queries, ...payload.queries], pages: document.dashboard.pages }
  }));
  expect(payload.aliases).toHaveLength(1);
  const source = result[payload.aliases[0]];
  expect(source.metadata.availability).toBe('available');
  expect(source.rows).toHaveLength(1);
  expect(source.rows[0]).toMatchObject({ run: '123', 'run-status': 'in_progress', 'repository-coordinate': 'octo/api' });
  for (const name of contract.sources.filter((name) => name !== contract.source)) {
    expect(result[name].metadata.availability).not.toBe('unavailable');
  }
});

it('preserves normal pages, navigation, templates and drill-downs with local acquisition queries', async () => {
  const immediate = composeImmediateDashboard(document, profile);
  const context = { languageVersion: immediate['language-version'], dashboard: immediate.dashboard };
  const validation = validateDashboardDocument(JSON.stringify(immediate));
  expect(validation, JSON.stringify(validation)).toMatchObject({ ok: true });
  expect(immediate.dashboard.pages.map((page) => page.id)).toEqual(document.dashboard.pages.filter(
    (/** @type {{ id: string }} */ page) => !profile['excluded-pages'].includes(page.id)).map(
    (/** @type {{ id: string }} */ page) => page.id));
  expect((immediate.dashboard.navigation ?? []).flatMap((section) => section.pages)).not.toContain('configuration');
  expect((immediate.dashboard.navigation ?? []).flatMap((section) => section.pages)).not.toContain('indexing');
  expect(document.dashboard.pages.some((/** @type {{ id: string }} */ page) => page.id === 'configuration')).toBe(true);
  expect(document.dashboard.pages.some((/** @type {{ id: string }} */ page) => page.id === 'indexing')).toBe(true);
  const overview = immediate.dashboard.pages.find((page) => page.id === 'overview');
  expect(overview?.title).toBe('Overview');
  expect(overview?.['class-name']).toBe('immediate-evidence-page');
  expect(immediate.dashboard['data-status']).toEqual(profile['data-status']);
  expect(immediate.dashboard.callouts ?? []).toEqual(document.dashboard.callouts.filter(
    (/** @type {{ ['navigation-page']: string }} */ callout) => !profile['excluded-pages'].includes(callout['navigation-page'])));
  const names = immediate.dashboard.pages.flatMap((page) =>
    dashboardPageAllSourceNames(context, page.id ?? '', 'all'));
  const dependencies = resolveDashboardQuerySources(immediate.dashboard.queries, names);
  expect(dependencies).toContain('daily-run-counts');
  expect(dependencies).not.toContain('campaigns');
  expect(dependencies).not.toContain('outcomes');
  expect(dependencies).not.toContain('work-items');
  expect(dependencies).toContain('tools');
  expect(dependencies).toContain('audits');
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  const batch = structuredClone(contract.batch);
  const now = new Date().toISOString();
  batch.runs[0].startedAt = now;
  batch.runs[0].createdAt = now;
  await upsertCanonicalBatch(indexedDB, batch);
  for (const page of immediate.dashboard.pages.filter((page) => page.id === 'runs')) {
    const sources = dashboardPageAllSourceNames(context, page.id ?? '', 'all');
    const payload = compileDashboardViewPayloadQueries(page, page.id ?? '', {
      queries: immediate.dashboard.queries, sourceNames: sources
    });
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard',
      sourceNames: payload.aliases,
      context: { queries: [...(immediate.dashboard.queries ?? []), ...payload.queries], pages: immediate.dashboard.pages }
    }));
    for (const name of payload.aliases) {
      expect(result[name].metadata.availability).toBe('available');
      expect(result[name].rows.length).toBeGreaterThan(0);
    }
  }
});

it.each(['page', 'view', 'query', 'query-input', 'action'])('fails closed when the audited %s inventory changes', (kind) => {
  const changed = structuredClone(document);
  if (kind === 'page') changed.dashboard.pages.push({ id: 'unaudited', views: [] });
  if (kind === 'view') changed.dashboard.pages[0].views.push({ id: 'unaudited', data: { source: 'runs' } });
  if (kind === 'query') changed.dashboard.queries.push({ name: 'unaudited', from: 'runs' });
  if (kind === 'query-input') changed.dashboard.queries[0].from = 'collection-health';
  if (kind === 'action') changed.dashboard['cli-actions'].push({ id: 'unaudited' });
  expect(() => composeImmediateDashboard(changed, profile)).toThrow(/inventory changed/);
});

it('rejects unsupported dependencies even after explicitly acknowledging an inventory change', () => {
  const changed = structuredClone(document);
  const repository = changed.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'repository-detail');
  repository.views.push({ id: 'unauthorized-health', data: { source: 'collection-health' } });
  expect(() => composeImmediateDashboard(changed, {
    ...profile,
    'audited-dashboard-sha256': immediateDashboardAuditFingerprint(changed)
  })).toThrow(/Unsupported immediate dashboard sources: collection-health/);
});

it('audits every retained query and view through production canonical dependencies', () => {
  const immediate = composeImmediateDashboard(document, profile);
  const context = { languageVersion: immediate['language-version'], dashboard: immediate.dashboard };
  for (const page of immediate.dashboard.pages) {
    const dependencies = immediateDashboardSourceDependencies(immediate.dashboard,
      dashboardPageAllSourceNames(context, page.id ?? '', 'all'));
    expect(dependencies.stores.every((name) => profile['supported-stores'].includes(name)), page.id).toBe(true);
    expect(dependencies.external.every((name) => profile['supported-external-sources'].includes(name)), page.id).toBe(true);
  }
  for (const query of immediate.dashboard.queries ?? []) {
    const dependencies = immediateDashboardSourceDependencies(immediate.dashboard, [String(query.name)]);
    expect(dependencies.stores.every((name) => profile['supported-stores'].includes(name)), String(query.name)).toBe(true);
    expect(dependencies.external.every((name) => profile['supported-external-sources'].includes(name)), String(query.name)).toBe(true);
  }
});

it('removes all omitted navigation, tab, card, action and query-link destinations', () => {
  const immediate = composeImmediateDashboard(document, profile);
  const ids = new Set(immediate.dashboard.pages.map((page) => page.id));
  const actions = new Set(profile['included-cli-actions']);
  /** @param {unknown} value @param {string} [key] */
  const check = (value, key = '') => {
    if (typeof value === 'string') {
      for (const match of value.matchAll(/#page-([a-z0-9-]+)/g)) expect(ids.has(match[1]), value).toBe(true);
      if (['page', 'navigation-page'].includes(key)) expect(ids.has(value), value).toBe(true);
      if (key === 'action') expect(actions.has(value), value).toBe(true);
    } else if (Array.isArray(value)) {
      if (key === 'pages') {
        for (const entry of value.filter((entry) => typeof entry === 'string')) expect(ids.has(entry), entry).toBe(true);
      }
      if (key === 'actions') {
        for (const entry of value.filter((entry) => typeof entry === 'string')) expect(actions.has(entry), entry).toBe(true);
      }
      value.forEach((entry) => check(entry, key));
    } else if (value && typeof value === 'object') {
      for (const [childKey, child] of Object.entries(value)) check(child, childKey);
    }
  };
  check(immediate.dashboard);
});

it('exposes only native upgrade plans with supported flags and default PR publication', () => {
  const immediate = composeImmediateDashboard(document, profile);
  const actions = /** @type {Array<Record<string, any>>} */ (immediate.dashboard['cli-actions']);
  expect(actions.map((action) => action.id)).toEqual(profile['included-cli-actions']);
  for (const action of actions) {
    expect(action.command).toBe('gh aw upgrade --repo {{repository}}');
    expect(action.command).not.toMatch(/cao-|pr-message|auto-merge/);
    expect(action.arguments.map((/** @type {{ flag: string }} */ arg) => arg.flag)).toEqual([
      '--create-pull-request', '--pre-releases'
    ]);
    expect(action.arguments[0].default).toBe(true);
  }
  const maintenance = immediate.dashboard.pages.find((page) => page.id === 'maintenance');
  expect(maintenance?.['navigation-indicator']).toEqual(profile['page-overrides'].maintenance['navigation-indicator']);
  expect(maintenance?.route).toMatchObject({ tabs: [{ page: 'maintenance' }] });
  expect(maintenance?.views).toMatchObject([{
    list: { action: 'upgrade-repository', 'action-context': ['repository'] }
  }]);
});

it('preserves remote lock-workflow maintenance rows without fabricating compiler provenance', async () => {
  const immediate = composeImmediateDashboard(document, profile);
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  const batch = structuredClone(contract.batch);
  batch.workflows[0].path = '.github/workflows/agent.lock.yml';
  await upsertCanonicalBatch(indexedDB, batch);
  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'query-canonical-dashboard',
    sourceNames: ['maintenance-repositories'],
    context: { queries: immediate.dashboard.queries, pages: immediate.dashboard.pages }
  }));
  expect(result['maintenance-repositories'].rows).toEqual([expect.objectContaining({
    repository: 'octo/api',
    'gh-aw-version': 'unknown',
    'gh-aw-current-version': 'unknown',
    'upgrade-state': 'Upgrade status unavailable',
    'upgrade-action-visible': false
  })]);
});

it('counts only 30 retained runs, never claiming health or verified value for 371 observed workflow identities', async () => {
  const immediate = composeImmediateDashboard(document, profile);
  const overview = immediate.dashboard.pages.find((page) => page.id === 'overview');
  expect(overview?.views).toMatchObject([
    { id: 'overview-retained-run-count', mark: 'metric' }
  ]);
  expect(overview?.description).toContain('Collection is partial');
  expect(overview?.description).toContain('remain unknown');
  const queryNames = (immediate.dashboard.queries ?? []).map((query) => query.name);
  for (const name of [
    'overview-header-presentation', 'overview-factory-status', 'overview-factory-station',
    'overview-workflow-health-summary', 'overview-value-station', 'overview-issue-station'
  ]) expect(queryNames).not.toContain(name);
  const context = { languageVersion: immediate['language-version'], dashboard: immediate.dashboard };
  const dependencies = immediateDashboardSourceDependencies(immediate.dashboard,
    dashboardPageAllSourceNames(context, 'overview', 'all'));
  expect(dependencies.stores).toEqual(['runs', 'workflows']);
  expect(dependencies.external).toEqual([]);
  expect(immediate.dashboard.pages.some((page) => page.id === 'graders')).toBe(true);
  expect(immediate.dashboard.pages.some((page) => page.id === 'runs')).toBe(true);
  expect(document.dashboard.pages.find((/** @type {{ id: string }} */ page) =>
    page.id === 'overview').views.map((/** @type {{ id: string }} */ view) => view.id))
    .toEqual(['overview-header', 'overview-floor']);
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  const batch = /** @type {import('../../src/data/model/schema.js').CanonicalBatch} */ (structuredClone(contract.batch));
  const workflow = batch.workflows[0], run = batch.runs[0];
  batch.workflows = Array.from({ length: 371 }, (_, index) => ({
    ...workflow,
    id: `${workflow.id}:${index}`,
    path: `.github/workflows/agent-${index}.lock.yml`
  }));
  batch.runs = Array.from({ length: 30 }, (_, index) => ({
    ...run,
    id: `github:run:octo/api:${123 + index}`,
    githubRunId: String(123 + index),
    workflowId: batch.workflows[index].id,
    workflowPath: batch.workflows[index].path
  }));
  await upsertCanonicalBatch(indexedDB, batch);
  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'query-canonical-dashboard',
    sourceNames: ['immediate-retained-run-count'],
    context: { queries: immediate.dashboard.queries, pages: immediate.dashboard.pages }
  }));
  expect(result['immediate-retained-run-count'].rows).toEqual([{ 'retained-runs': 30 }]);
});

it('executes every retained page source through the canonical worker without synthetic JavaScript sources', async () => {
  const immediate = composeImmediateDashboard(document, profile);
  const context = { languageVersion: immediate['language-version'], dashboard: immediate.dashboard };
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  const batch = structuredClone(contract.batch);
  batch.runs[0].startedAt = new Date().toISOString();
  batch.runs[0].createdAt = batch.runs[0].startedAt;
  await upsertCanonicalBatch(indexedDB, batch);
  for (const page of immediate.dashboard.pages) {
    const names = dashboardPageAllSourceNames(context, page.id ?? '', 'all');
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard',
      sourceNames: names,
      context: { queries: immediate.dashboard.queries, pages: immediate.dashboard.pages }
    }));
    for (const name of names) {
      expect(result[name], `${page.id}/${name}`).toBeDefined();
      const dependencies = immediateDashboardSourceDependencies(immediate.dashboard, [name]);
      if (dependencies.external.some((source) => source.startsWith('cli-'))) {
        expect(result[name].metadata.availability, `${page.id}/${name}`).toBe('unavailable');
        if (result[name].metadata['query-error']) {
          expect(result[name].metadata['query-error']).toMatchObject({ code: 'input-unavailable' });
        }
      } else {
        expect(result[name].metadata, `${page.id}/${name}`).not.toHaveProperty('query-error');
        expect(result[name].metadata.availability, `${page.id}/${name}`).not.toBe('unavailable');
      }
    }
  }
});

it('keeps normalized safe-output links and lifecycle without campaign or hosted admission evidence', async () => {
  const immediate = composeImmediateDashboard(document, profile);
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  const batch = /** @type {import('../../src/data/model/schema.js').CanonicalBatch} */ (structuredClone(contract.batch));
  batch.issues = [
    { id: 'output:issue', runId: batch.runs[0].id, type: 'safe_output.result',
      timestamp: batch.runs[0].startedAt, source: 'safe-output', summary: 'Created issue',
      correlationId: 'https://github.com/octo/api/issues/10', safeOutputType: 'create_issue',
      isPullRequest: false, number: 10 }
  ];
  await upsertCanonicalBatch(indexedDB, batch);
  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'query-canonical-dashboard',
    sourceNames: ['immediate-outcomes', 'immediate-work-items'],
    context: { queries: immediate.dashboard.queries, pages: immediate.dashboard.pages }
  }));
  expect(result['immediate-outcomes'].rows).toEqual([expect.objectContaining({
    'outcome-title': 'Created issue',
    'issue-link': { href: 'https://github.com/octo/api/issues/10', label: 'View issue' },
    'pull-request-link': null,
    'issue-status': 'Unknown',
    campaign: null
  })]);
  expect(result['immediate-work-items'].rows).toEqual([expect.objectContaining({
    'lifecycle-state': 'active', phase: 'in-progress', domain: 'unknown'
  })]);
});
