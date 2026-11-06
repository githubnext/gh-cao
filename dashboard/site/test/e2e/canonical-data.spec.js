import { test, expect } from '@playwright/test';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { ingestGhAwLogs as ingestNodeGhAwLogs } from '../../src/data/ingest/coordinator.js';
import { readCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { createSqliteIndexedDB, SqliteIDBKeyRange } from '../../src/data/storage/sqlite-indexeddb.js';
import { normalizedActivityShards } from './normalized-shard.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import immediateContract from '../fixtures/immediate-operations-contract.json' with { type: 'json' };

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));
const databaseName = 'gh-aw-cao-dashboard-data';
const shardName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
const recordShardName = `gh-aw-logs-records/logs-${'c'.repeat(64)}-${'d'.repeat(16)}.jsonl`;
const fixtureTimeOffset = Date.now() - Date.parse('2026-09-09T05:00:00Z');
/** @template T @param {T} value @returns {T} */
const currentFixtureDates = (value) => JSON.parse(JSON.stringify(value).replace(
  /2026-09-(?:08|09|10)T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z/g,
  (instant) => new Date(Date.parse(instant) + fixtureTimeOffset).toISOString()
));
const canonicalEntityTables = [
  'audits', 'campaigns', 'domains', 'evalObservations', 'evals', 'experimentAssignments',
  'experiments', 'friction', 'graderObservations', 'graders', 'issues', 'marketplacePackages',
  'operationalValueEvidence', 'operationalValues', 'repositories', 'runs', 'skills', 'tools',
  'workflowValueCollections', 'workflows'
];

function ghAwLogInput() {
  const fixtureRoot = join(siteRoot, 'test', 'fixtures', 'gh-aw-logs');
  const context = currentFixtureDates(JSON.parse(readFileSync(join(fixtureRoot, 'context.json'), 'utf8')));
  const paths = [
    'run-303/mcp-logs/gateway.jsonl',
    'run-303/sandbox/firewall/audit/audit.jsonl',
    'run-303/sandbox/agent/logs/copilot-session-state/session-505/events.jsonl'
  ];
  return {
    ...context,
    files: paths.map((path) => ({
      path,
      content: currentFixtureDates(readFileSync(join(fixtureRoot, path), 'utf8'))
        .replaceAll('"ts":1788926403', `"ts":${Math.floor((1788926403 * 1000 + fixtureTimeOffset) / 1000)}`)
    }))
  };
}

function databaseTables(generation = 'browser-generation', run = '12345') {
  return currentFixtureDates({
    campaigns: {
      rows: [{
        campaign: 'dashboard',
        'campaign-name': 'CAO Dashboard',
        'campaign-description': 'Deploy the CAO dashboard.',
        'campaign-icon': 'graph',
        'campaign-mode': 'review',
        'campaign-enabled': true,
        'campaign-worker-count': 1,
        'campaign-min-version': 'v0.89.3',
        'campaign-experimental': true,
        'observed-at': '2026-09-09T05:00:00Z'
      }],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    },
    repositories: {
      rows: [{ organization: 'githubnext', repository: 'gh-cao', 'observed-at': '2026-09-09T05:00:00Z' }],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    },
    workflows: {
      rows: [{
        organization: 'githubnext',
        repository: 'gh-cao',
        workflow: '.github/workflows/dashboard.md',
        campaign: 'dashboard',
        'campaign-name': 'CAO Dashboard',
        'workflow-role': 'worker',
        'rollout-mode': 'review',
        'workflow-active': 'true',
        'observed-at': '2026-09-09T05:00:00Z'
      }],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    },
    runs: {
      rows: [{
        organization: 'githubnext',
        repository: 'gh-cao',
        workflow: '.github/workflows/dashboard.md',
        run,
        'run-attempt': 2,
        'run-status': 'completed',
        'run-conclusion': 'failure',
        'started-at': '2026-09-09T04:00:00Z',
        'rollout-mode': 'review',
        'agent-id': 'copilot',
        'model-id': 'model-b',
        engine: 'copilot',
        'engine-version': '1.2.3',
        'requested-model': 'model-a',
        'resolved-model': 'model-b',
        'aic-total': 17,
        'repository-link': { href: 'https://github.com/githubnext/gh-cao' },
        'run-link': { href: `https://github.com/githubnext/gh-cao/actions/runs/${run}` }
      }],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    },
    audits: {
      rows: [
        {
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run,
          'run-attempt': 2, event: `event-${run}`,
          'event-timestamp': '2026-09-09T04:01:00Z', 'event-source': 'agent',
          'event-type': 'user.message', 'event-summary': 'Processed the dashboard request',
          'observed-at': '2026-09-09T05:00:00Z'
        },
        {
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run,
          'run-attempt': 2, session: `session-${run}`, event: `github-api-${run}`,
          'event-timestamp': '2026-09-09T04:02:00Z', 'event-source': 'github-api',
          'event-type': 'github-api.response', 'event-summary': 'GET /rate_limit',
          'event-status': '200', 'correlation-id': 'request-123',
          'observed-at': '2026-09-09T05:00:00Z'
        }
      ],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    },
    domains: {
      rows: [
        {
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run,
          'run-attempt': 2, event: `firewall-allowed-${run}`, 'event-source': 'firewall',
          'event-type': 'firewall.request.allowed', domain: 'api.github.com', decision: 'allowed', 'request-count': 4,
          'event-timestamp': '2026-09-09T04:02:00Z', 'observed-at': '2026-09-09T05:00:00Z'
        },
        {
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run,
          'run-attempt': 2, event: `firewall-blocked-${run}`, 'event-source': 'firewall',
          'event-type': 'firewall.request.blocked', domain: 'api.github.com', decision: 'denied', 'request-count': 2,
          'event-timestamp': '2026-09-09T04:02:01Z', 'observed-at': '2026-09-09T05:00:00Z'
        }
      ],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    },
    tools: {
      rows: [
        {
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run,
          'run-attempt': 2, event: `tool-call-${run}`,
          'event-timestamp': '2026-09-09T04:02:00Z', 'event-source': 'mcp',
          'event-type': 'mcp.tool_call', 'event-summary': 'github/search_issues', 'correlation-id': `call-${run}`,
          'observed-at': '2026-09-09T05:00:00Z'
        },
        {
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run,
          'run-attempt': 2, session: `session-${run}`, event: `tool-result-${run}`,
          'event-timestamp': '2026-09-09T04:02:01Z', 'event-source': 'mcp',
          'event-type': 'tool.execution_complete', 'event-status': 'success', 'correlation-id': `call-${run}`,
          'observed-at': '2026-09-09T05:00:00Z'
        }
      ],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    },
    issues: {
      rows: [{
        organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run,
        'run-attempt': 2, event: `report-${run}`, 'event-source': 'safe-output',
        'event-type': 'safe_output.created', 'event-status': 'created',
        'safe-output-type': 'create_issue', 'is-pull-request': false,
        'correlation-id': 'https://github.com/githubnext/gh-cao/issues/7',
        'event-timestamp': '2026-09-09T04:03:00Z', 'observed-at': '2026-09-09T05:00:00Z'
      }],
      metadata: { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': generation }
    }
  });
}

function canonicalWarningSources() {
  const sources = databaseTables();
  const issueRows = /** @type {Array<Record<string, unknown>>} */ (sources.issues.rows);
  issueRows.length = 0;
  issueRows.push(
    {
      organization: 'githubnext',
      repository: 'gh-cao',
      workflow: '.github/workflows/dashboard.md',
      run: '12345',
      'run-attempt': 2,
      session: 'session-12345',
      event: 'safe-output-12345',
      'event-timestamp': '2026-09-09T04:03:00Z',
      'event-source': 'safe-output',
      'event-type': 'safe_output.result',
      'event-summary': 'Created issue',
      'event-status': 'created',
      'correlation-id': 'https://github.com/githubnext/gh-cao/issues/7',
      'safe-output-type': 'create_issue',
      'github-entity-type': 'issue',
      'is-pull-request': false,
      'observed-at': '2026-09-09T05:00:00Z'
    }
  );
  const auditRows = /** @type {Array<Record<string, unknown>>} */ (sources.audits.rows);
  auditRows.push({
      organization: 'githubnext',
      repository: 'gh-cao',
      workflow: '.github/workflows/dashboard.md',
      run: '12345',
      'run-attempt': 2,
      session: 'session-12345',
      event: 'finding-12345',
      'event-timestamp': '2026-09-09T04:04:00Z',
      'event-source': 'audit',
      'event-type': 'audit.finding',
      code: 'prompt_injection_detected',
      'event-summary': 'Prompt injection detected',
      'event-status': 'high',
      'observed-at': '2026-09-09T05:00:00Z'
  });
  return currentFixtureDates(sources);
}

test.beforeEach(async ({ context, page }) => {
  await context.route('http://dashboard.test/**', async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === '/' || pathname === '/index.html') {
      await route.fulfill({ contentType: 'text/html', body: '<main>Canonical data test</main>' });
      return;
    }
    if (pathname === '/sources/manifest.json') {
      await route.fulfill({ status: 404, body: 'Not found' });
      return;
    }
    if (pathname === '/sources.json') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(databaseTables()) });
      return;
    }
    if (pathname === '/inventory-sources.json') {
      const sources = databaseTables();
      sources.campaigns.rows.push({
        campaign: 'repo-assist',
        'campaign-name': 'Repo Assist',
        'campaign-description': 'Review-first repository assistance.',
        'campaign-icon': 'gear',
        'campaign-mode': 'review',
        'campaign-enabled': true,
        'campaign-worker-count': 4,
        'campaign-min-version': 'v0.89.17',
        'campaign-experimental': true,
        'observed-at': '2026-09-09T05:00:00Z'
      });
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          campaigns: sources.campaigns,
          repositories: sources.repositories,
          workflows: sources.workflows
        })
      });
      return;
    }
    if (pathname === '/payload-hashes.json') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          [shardName]: 'a'.repeat(64),
          [recordShardName]: 'c'.repeat(64)
        })
      });
      return;
    }
    if (pathname === `/${shardName}` || pathname === `/${recordShardName}`) {
      await route.fulfill({
        contentType: 'application/x-ndjson',
        body: normalizedActivityShards(`${JSON.stringify(currentFixtureDates({ schema_version: 2, kind: 'run', run: {
          run_id: 12345,
          run_attempt: 1,
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow_name: 'Dashboard',
          workflow_path: '.github/workflows/dashboard.md',
          status: 'completed',
          conclusion: 'success',
          created_at: '2026-09-09T04:00:00Z',
          updated_at: '2026-09-09T04:05:00Z',
          url: 'https://github.com/githubnext/gh-cao/actions/runs/12345',
          audit: {
            mcp_tool_usage: {
              tool_calls: [
                {
                  tool_call_id: 'call-12345-1',
                  timestamp: '2026-09-09T04:02:00Z',
                  server_name: 'github',
                  tool_name: 'search_issues',
                  input_size: 42,
                  output_size: 128,
                  status: 'success'
                },
                {
                  tool_call_id: 'call-12345-2',
                  timestamp: '2026-09-09T04:03:00Z',
                  server_name: 'github',
                  tool_name: 'search_issues',
                  input_size: 42,
                  output_size: 128,
                  status: 'success'
                },
                {
                  tool_call_id: 'call-12345-3',
                  timestamp: '2026-09-09T04:04:00Z',
                  server_name: 'safe_outputs',
                  tool_name: 'create_issue',
                  input_size: 42,
                  output_size: 128,
                  status: 'success'
                }
              ]
            },
            skill_activations: [{
              name: 'reactive-ui',
              status: 'success',
              timestamp: '2026-09-09T04:04:30Z'
            }],
            firewall_analysis: {
              requests_by_domain: {
                'api.github.com:443': { allowed: 4, blocked: 2 },
                'objects.githubusercontent.com:443': { allowed: 3, blocked: 0 }
              }
            }
          }
        } }))}\n`)[pathname === `/${shardName}` ? 'runs' : 'records']
      });
      return;
    }
    const filePath = join(siteRoot, pathname);
    if (pathname === '/dashboard.json') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(authoritativeDashboard)
      });
      return;
    }
    if (existsSync(filePath)) {
      await route.fulfill({
        contentType: pathname.endsWith('.json') ? 'application/json' : 'application/javascript',
        body: readFileSync(filePath)
      });
    } else {
      await route.fulfill({ contentType: 'text/html', body: '<main>Canonical data test</main>' });
    }
  });
  await page.goto('http://dashboard.test/');
  await page.evaluate((name) => new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(name);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  }), databaseName);
});

test('native IndexedDB directly upserts and retains canonical data across reload', async ({ page }) => {
  test.slow();
  const sources = databaseTables();

  const first = await page.evaluate(async (sourceDocument) => {
    const coordinatorUrl = `${location.origin}/src/data/ingest/coordinator.js`;
    const queriesUrl = `${location.origin}/src/data/queries/index.js`;
    const [{ ingestDashboardSources }, { createCanonicalQueries }] = await Promise.all([
      import(coordinatorUrl),
      import(queriesUrl)
    ]);
    const result = await ingestDashboardSources(indexedDB, sourceDocument);
    const queries = createCanonicalQueries(indexedDB);
    const repositories = await queries.repositories.list();
    const workflows = await queries.workflows.forRepository(String(repositories[0].id));
    const runs = await queries.runs.forWorkflow(String(workflows[0].id));
    return { result, repositories, workflows, runs };
  }, sources);

  expect(first.result).toMatchObject({ updated: true });
  expect(first.repositories).toHaveLength(1);
  expect(first.workflows).toHaveLength(1);
  expect(first.runs[0].id).toBe('github:run:githubnext/gh-cao:12345');

  await page.reload();
  const retained = await page.evaluate(async () => {
    const queriesUrl = `${location.origin}/src/data/queries/index.js`;
    const { createCanonicalQueries } = await import(queriesUrl);
    const queries = createCanonicalQueries(indexedDB);
    return queries.runs.recentFailures();
  });

  expect(retained).toEqual([expect.objectContaining({ id: 'github:run:githubnext/gh-cao:12345' })]);

  const viewSources = await page.evaluate(async (sourceDocument) => {
    const databaseUrl = `${location.origin}/src/data/queries/database.js`;
    const { loadDatabaseQuerySources } = await import(databaseUrl);
    return loadDatabaseQuerySources(indexedDB, sourceDocument, {
      sourceNames: ['failed-runs', 'runs'],
      queries: [{
        name: 'failed-runs',
        from: 'runs',
        filter: { predicates: [{ field: 'run-conclusion', equals: 'failure' }] }
      }]
    });
  }, sources);
  expect(viewSources['failed-runs']).toMatchObject({
    source: 'failed-runs',
    rows: [{
      organization: 'githubnext',
      repository: 'gh-cao',
      run: '12345',
      'run-attempt': 2,
      'run-conclusion': 'failure'
    }],
    metadata: {
      'source-kind': 'derived',
      availability: 'available'
    }
  });
  expect(viewSources.runs).toMatchObject({
    source: 'runs',
    rows: [{
      organization: 'githubnext',
      repository: 'gh-cao',
      run: '12345',
      'run-attempt': 2,
      'run-conclusion': 'failure'
    }],
    metadata: {
      'source-kind': 'database-query',
      availability: 'available'
    }
  });
  expect(Object.keys(viewSources).sort()).toEqual(['failed-runs', 'runs']);
});

test('Evals renders persisted artifact answers from the real data worker after reload', async ({ page }) => {
  await page.evaluate(async () => {
    const { ingestGhAwLogs } = await import(`${location.origin}/src/data/ingest/coordinator.js`);
    const observedAt = new Date().toISOString();
    await ingestGhAwLogs(indexedDB, {
      observedAt,
      repository: { githubId: 1, owner: 'octo', name: 'api' },
      workflow: { githubId: 2, name: 'Worker', path: '.github/workflows/worker.lock.yml' },
      run: {
        githubRunId: 303, attempt: 1, createdAt: observedAt,
        url: 'https://github.com/octo/api/actions/runs/303'
      },
      files: [{
        path: 'usage/evals.jsonl',
        content: [
          { id: 'quality', answer: 'YES', runid: '303' },
          { id: 'correctness', answer: 'NO', runid: '303' },
          { id: 'uncertain', answer: 'UNKNOWN', runid: '303' }
        ].map((row) => JSON.stringify(row)).join('\n')
      }]
    });
  });
  await page.reload();
  const counts = await page.evaluate(async () => {
    const [{ readCanonicalBatch }, processor, presenter, document] = await Promise.all([
      import(`${location.origin}/src/data/storage/indexeddb.js`),
      import(`${location.origin}/src/data-processor.js`),
      import(`${location.origin}/src/presenter.js`),
      fetch(`${location.origin}/dashboard.json`).then((response) => response.json())
    ]);
    const dashboard = document.dashboard;
    const evals = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'evals');
    const sources = await processor.loadCanonicalDashboardPage(evals.views.map((/** @type {{ data: { source: string } }} */ view) => view.data.source), dashboard, undefined, {
      pageId: 'evals'
    });
    window.document.body.append(presenter.renderDashboard({
      document: { ...document, dashboard: {
        ...dashboard, pages: [evals], navigation: [{ label: 'Evidence', pages: ['evals'] }]
      } },
      sources
    }));
    const batch = await readCanonicalBatch(indexedDB);
    return { evals: batch.evals?.length, observations: batch.evalObservations?.length };
  });
  expect(counts).toEqual({ evals: 3, observations: 3 });
  await page.getByRole('button', { name: 'Table', exact: true }).click();
  const view = page.locator('[data-view-id="evals-statuses"]');
  await expect(view).toContainText('quality');
  await expect(view).toContainText('correctness');
  await expect(view).toContainText('uncertain');
  await expect(view).toContainText('UNKNOWN');
  await expect(view).toContainText('.github/workflows/worker.md');
  await expect(view.locator('td[data-field="run"] a[href="https://github.com/octo/api/actions/runs/303"]')).toHaveCount(3);
  await page.getByRole('button', { name: 'Chart', exact: true }).click();
  const chart = page.locator('[data-view-id="evals-answer-distribution"]');
  await expect(chart.locator('[data-chart-widget="horizontal-bar"]')).toHaveCount(1);
  await expect(chart.locator('.horizontal-bar-chart-section')).toHaveCount(3);
  for (const section of await chart.locator('.horizontal-bar-chart-section').all()) {
    await expect(section.locator('.horizontal-bar-chart-label')).toHaveText(['NO', 'UNKNOWN', 'YES']);
    const values = await section.locator('.horizontal-bar-chart-value').allTextContents();
    expect(values.reduce((sum, value) => sum + Number.parseFloat(value), 0)).toBe(100);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await chart.scrollIntoViewIfNeeded();
  await expect(chart).toBeVisible();
  expect(await chart.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
});

for (const width of [1200, 390]) {
  test(`Immediate mode renders canonical run evidence without fleet setup at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    const result = await page.evaluate(async (contract) => {
      const storage = await import(`${location.origin}/src/data/storage/indexeddb.js`);
      await storage.upsertCanonicalBatch(indexedDB, contract.batch);
      const processor = await import(`${location.origin}/src/data-processor.js`);
      const input = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
      const selected = input.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
      const payload = await processor.loadCanonicalDashboardPage([contract.source], input.dashboard, undefined, {
        pageId: contract.page, viewId: contract.view
      });
      const presenter = await import(`${location.origin}/src/presenter.js`);
      window.history.replaceState(null, '', `#page-${contract.page}`);
      document.body.append(presenter.renderDashboard({
        document: {
          ...input,
          dashboard: { ...input.dashboard, pages: [selected], navigation: [{ pages: [contract.page] }], callouts: undefined }
        },
        sources: payload
      }));
      return { names: Object.keys(payload), rows: Object.values(payload)[0].rows };
    }, immediateContract);
    expect(result.names).toEqual([`view:${immediateContract.page}:${immediateContract.view}:${immediateContract.source}`]);
    expect(result.rows).toHaveLength(1);
    await expect(page.getByRole('heading', { name: 'Immediate operations', exact: true })).toBeVisible();
    await expect(page.locator(`[data-view-id="${immediateContract.view}"]`)).toBeVisible();
    await expect(page.locator(`[data-view-id="${immediateContract.view}"]`)).toContainText('octo/api');
    await expect(page.locator('p[data-page-description]')).toHaveText('Inspect recent activity immediately without setup or long-running fleet history.');
  });
}

test('data worker returns only the canonical payload requested by a view', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const context = {
      githubUrlBase: 'https://github.com',
      pages: [],
      queries: [{
        name: 'failed-runs',
        from: 'runs',
        filter: { predicates: [{ field: 'run-conclusion', equals: 'failure' }] }
      }]
    };
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['failed-runs'],
      context
    );
    const navigated = await loadCanonicalDashboardPage(['failed-runs'], context);
    return { initial, navigated };
  });

  for (const payload of [result.initial, result.navigated]) {
    expect(Object.keys(payload)).toEqual(['failed-runs']);
    expect(payload['failed-runs']).toMatchObject({
      source: 'failed-runs',
      rows: [{ repository: 'gh-cao', run: '12345', 'run-conclusion': 'failure' }],
      metadata: { 'source-kind': 'derived' }
    });
  }
});

test('complete repository inventory subscriptions omit the redundant identity preview', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processor = await import(`${location.origin}/src/data-processor.js`);
    const dashboard = (await fetch(`${location.origin}/dashboard.json`).then((response) => response.json())).dashboard;
    await processor.loadCanonicalDashboardSources(
      `${location.origin}/sources.json`, ['repositories'], dashboard
    );
    const lifetime = new AbortController();
    const deliveries = /** @type {Array<{ repository: string, runs?: number, pending?: string }>} */ ([]);
    try {
      return await new Promise((resolve, reject) => {
        processor.subscribeCanonicalDashboardView(
          'repository-partial-result-test', ['repository-activity'], dashboard,
          (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ sources) => {
            const source = sources['view:repositories:repositories-activity:repository-activity'];
            if (!source) return;
            deliveries.push({
              repository: String(source.rows[0]?.repository),
              runs: /** @type {number | undefined} */ (source.rows[0]?.runs),
              pending: /** @type {string | undefined} */ (source.metadata['projection-state'])
            });
            if (deliveries.at(-1)?.pending === undefined) resolve(deliveries);
          },
          undefined,
          { pageId: 'repositories', viewId: 'repositories-activity', routeParameters: {}, signal: lifetime.signal, onError: reject }
        );
      });
    } finally {
      lifetime.abort();
    }
  });
  expect(result).toHaveLength(1);
  expect(result[0]).toMatchObject({ repository: 'githubnext/gh-cao', runs: 1, pending: undefined });
});

test('workflow route queries return bounded records and subscribed aggregates through the real worker', async ({ page, context }) => {
  const input = databaseTables();
  input.workflows.rows.push({ ...input.workflows.rows[0], repository: 'unrelated' });
  input.runs.rows.push({ ...input.runs.rows[0], repository: 'unrelated', run: '999', 'aic-total': 999 });
  const updated = databaseTables('updated-browser-generation', '12346');
  updated.runs.rows[0]['run-conclusion'] = 'success';
  updated.runs.rows[0]['aic-total'] = 23;
  await context.route('http://dashboard.test/element-sources.json', (route) => route.fulfill({
    contentType: 'application/json', body: JSON.stringify(input)
  }));
  await context.route('http://dashboard.test/updated-element-sources.json', (route) => route.fulfill({
    contentType: 'application/json', body: JSON.stringify(updated)
  }));

  const result = await page.evaluate(async () => {
    const processor = await import(`${location.origin}/src/data-processor.js`);
    const document = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const dashboard = document.dashboard;
    const pageId = 'workflow-runtime';
    const viewId = 'workflow-runtime-route';
    const view = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === pageId)
      .views.find((/** @type {{ id: string }} */ view) => view.id === viewId);
    const options = { pageId, viewId, routeParameters: { workflow: 'githubnext/gh-cao:.github/workflows/dashboard.md' } };
    const initial = await processor.loadCanonicalDashboardSources(
      `${location.origin}/element-sources.json`, view.data.sources, dashboard, undefined, options
    );
    const missing = await processor.loadCanonicalDashboardPage(view.data.sources, dashboard, undefined, {
      ...options, routeParameters: { workflow: 'githubnext/missing:.github/workflows/dashboard.md' }
    });
    const lifetime = new AbortController();
    /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>[]} */
    const deliveries = [];
    try {
      const refreshed = await new Promise((resolve, reject) => {
        processor.subscribeCanonicalDashboardView('bounded-runtime-test', view.data.sources, dashboard, (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ payload) => {
          deliveries.push(payload);
          if (deliveries.length === 1) {
            processor.refreshCanonicalDashboardSources(
              `${location.origin}/updated-element-sources.json`, view.data.sources, dashboard, undefined, options
            ).catch(reject);
          } else if (Object.values(payload).some((source) => source.rows[0]?.aic === 40)) {
            resolve(payload);
          }
        }, undefined, { ...options, signal: lifetime.signal, onError: reject });
      });
      return { initial, missing, subscribed: deliveries[0], refreshed };
    } finally {
      lifetime.abort();
    }
  });
  for (const payload of [result.initial, result.subscribed]) {
    const sources = Object.values(payload);
    expect(sources).toHaveLength(3);
    expect(sources.every((source) => source.rows.length === 1)).toBe(true);
    expect(sources.every((source) => source.metadata.availability === 'available')).toBe(true);
    expect(sources.flatMap((source) => source.rows)).toEqual(expect.arrayContaining([
      expect.objectContaining({ repository: 'gh-cao', workflow: '.github/workflows/dashboard.md' }),
      expect.objectContaining({ total: 1, failed: 1 }),
      expect.objectContaining({ aic: 17, 'telemetry-count': 1 })
    ]));
  }
  const refreshed = Object.values(result.refreshed);
  expect(refreshed.every((source) => source.rows.length === 1)).toBe(true);
  expect(refreshed.flatMap((source) => source.rows)).toEqual(expect.arrayContaining([
    expect.objectContaining({ total: 2, failed: 1, successful: 1 }),
    expect.objectContaining({ aic: 40, 'telemetry-count': 2 })
  ]));
  expect(Object.values(result.missing).every((source) => source.rows.length === 0)).toBe(true);
});

test('Domain runs returns bounded aliases and refreshes its abort-scoped subscription', async ({ page, context }) => {
  const initial = databaseTables();
  initial.domains.rows.push({ ...initial.domains.rows[0], event: 'unrelated-domain', domain: 'other.example' });
  await context.route('http://dashboard.test/domain-initial.json', (route) => route.fulfill({
    contentType: 'application/json', body: JSON.stringify(initial)
  }));
  await context.route('http://dashboard.test/domain-updated.json', (route) => route.fulfill({
    contentType: 'application/json', body: JSON.stringify(databaseTables('domain-refresh', '12346'))
  }));
  const result = await page.evaluate(async () => {
    const processor = await import(`${location.origin}/src/data-processor.js`);
    const { dashboard } = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const source = 'domain-run-records';
    const alias = 'view:domain-runs:domain-runs-table:domain-run-records';
    const options = {
      pageId: 'domain-runs', viewId: 'domain-runs-table',
      routeParameters: { domain: 'api.github.com' }
    };
    const pagination = { [alias]: { limit: 1 } };
    const initial = await processor.loadCanonicalDashboardSources(
      `${location.origin}/domain-initial.json`, [source], dashboard, pagination, options
    );
    const lifetime = new AbortController();
    let deliveries = 0;
    try {
      const refreshed = await new Promise((resolve, reject) => {
        processor.subscribeCanonicalDashboardView('domain-runs-refresh', [source], dashboard, (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ payload) => {
          deliveries++;
          if (deliveries === 1) {
            processor.refreshCanonicalDashboardSources(
              `${location.origin}/domain-updated.json`, [source], dashboard, pagination, options
            ).catch(reject);
          } else if (payload[alias]?.metadata['total-row-count'] === 4) resolve(payload);
        }, pagination, { ...options, signal: lifetime.signal, onError: reject });
      });
      return { initial, refreshed };
    } finally { lifetime.abort(); }
  });
  const alias = 'view:domain-runs:domain-runs-table:domain-run-records';
  for (const payload of [result.initial, result.refreshed]) {
    expect(Object.keys(payload)).toEqual([alias]);
    expect(payload[alias].rows).toHaveLength(1);
    expect(payload[alias].rows[0]).toMatchObject({
      domain: 'api.github.com', repository: 'gh-cao',
      'run-attempt': 2, 'run-link': { href: expect.stringMatching(/actions\/runs\/1234[56]/) }
    });
    expect(payload[alias].continuationToken).toBeTruthy();
  }
  expect(result.initial[alias].metadata['total-row-count']).toBe(2);
  expect(result.refreshed[alias].metadata['total-row-count']).toBe(4);
});

test('data worker avoids unavailable legacy boundaries on initial and navigated requests', async ({ context, page }) => {
  await context.route('http://dashboard.test/canonical-warning-sources.json', async (route) => {
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify(canonicalWarningSources())
    });
  });

  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const sourceNames = [
      'usage',
      'outcomes',
      'findings',
      'security-findings',
      'detection-observations',
      'work-items',
      'graders',
      'experiments',
      'evals',
      'eval-observations',
      'admissions',
      'safe-output-performance',
      'data-health-collections',
      'data-health-coverage'
    ];
    const dashboardContext = {
      githubUrlBase: dashboard.dashboard['github-url-base'],
      pages: dashboard.dashboard.pages,
      queries: dashboard.dashboard.queries
    };
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/canonical-warning-sources.json`,
      sourceNames,
      dashboardContext
    );
    const navigated = await loadCanonicalDashboardPage(sourceNames, dashboardContext);
    return { initial, navigated, sourceNames };
  });

  for (const payload of [result.initial, result.navigated]) {
    expect(Object.keys(payload).sort()).toEqual([...result.sourceNames].sort());
    for (const name of result.sourceNames.filter((name) => !name.startsWith('data-health-') && !['security-findings', 'admissions'].includes(name))) {
      expect(payload[name].metadata.availability).not.toBe('unavailable');
    }
    // `outcomes` is derived from the `issues` database table, so deleting the
    // published `outcomes` boundary still yields the issue row pushed above.
    expect(payload.outcomes.rows).toHaveLength(1);
    expect(payload.outcomes.rows[0]).toMatchObject({
      organization: 'githubnext',
      repository: 'gh-cao',
      run: '12345',
      'outcome-category': 'issue'
    });
    expect(payload['security-findings'].rows).toEqual([]);
    expect(payload['work-items'].rows).toEqual([
      expect.objectContaining({ 'lifecycle-state': 'blocked' })
    ]);
    expect(payload['security-findings'].metadata.availability).toBe('unavailable');
    expect(payload.admissions.metadata.availability).toBe('unavailable');
    expect(payload['detection-observations'].rows).toEqual([
      expect.objectContaining({ 'detection-signal': 'Prompt injection detected', 'detection-state': 'threat' })
    ]);
    expect(payload['data-health-collections'].metadata.availability).toBe('unavailable');
    expect(payload['data-health-coverage'].metadata.availability).toBe('unavailable');
  }
});

test('data worker reports an already ingested payload as unchanged', async ({ page }) => {
  const refreshes = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { refreshCanonicalDashboardSources } = await import(processorUrl);
    const context = {
      githubUrlBase: 'https://github.com',
      pages: [],
      queries: [{
        name: 'failed-runs',
        from: 'runs',
        filter: { predicates: [{ field: 'run-conclusion', equals: 'failure' }] }
      }]
    };
    return [
      await refreshCanonicalDashboardSources(
        `${location.origin}/sources.json`,
        ['failed-runs'],
        context
      ),
      await refreshCanonicalDashboardSources(
        `${location.origin}/sources.json`,
        ['failed-runs'],
        context
      )
    ];
  });

  expect(refreshes.map((refresh) => refresh.changed)).toEqual([true, false]);
  expect(refreshes[0].sources['failed-runs'].rows).toMatchObject([
    { repository: 'gh-cao', run: '12345' }
  ]);
});

test('data worker queries retained canonical data before downloading sources', async ({ page }) => {
  await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources } = await import(processorUrl);
    await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['failed-runs'],
      {
        githubUrlBase: 'https://github.com',
        pages: [],
        queries: [{
          name: 'failed-runs',
          from: 'runs',
          filter: { predicates: [{ field: 'run-conclusion', equals: 'failure' }] }
        }]
      }
    );
  });

  await page.reload();
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardPage, refreshCanonicalDashboardSources } = await import(processorUrl);
    const context = {
      githubUrlBase: 'https://github.com',
      pages: [],
      queries: [{
        name: 'failed-runs',
        from: 'runs',
        filter: { predicates: [{ field: 'run-conclusion', equals: 'failure' }] }
      }, {
        name: 'cached-run-totals',
        from: 'runs',
        aggregate: { values: [{ field: 'run', as: 'runs', reducer: 'distinct-count' }] }
      }, {
        name: 'overview-attention-domains',
        from: 'source-metadata',
        select: [
          { field: 'source', as: 'domain' },
          { field: 'row-count', as: 'value' }
        ]
      }]
    };
    const retained = await loadCanonicalDashboardPage(
      ['failed-runs', 'cached-run-totals', 'overview-attention-domains'],
      context
    );
    const refreshed = await refreshCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['failed-runs', 'cached-run-totals', 'overview-attention-domains'],
      context
    );
    return { retained, refreshed };
  });

  expect(result.retained['failed-runs']).toMatchObject({
    rows: [{ repository: 'gh-cao', run: '12345', 'run-conclusion': 'failure' }],
    metadata: { 'source-kind': 'derived' }
  });
  expect(result.retained['cached-run-totals']).toMatchObject({
    rows: [{ runs: 1 }],
    metadata: { 'source-kind': 'derived', 'query-name': 'cached-run-totals' }
  });
  expect(result.retained['overview-attention-domains']).toMatchObject({
    rows: [],
    metadata: { availability: 'unavailable', 'source-kind': 'derived' }
  });
  expect(result.refreshed.changed).toBe(false);
  expect(result.refreshed.sources['overview-attention-domains']).toBeDefined();
});

test('data worker executes declarative queries and returns only the derived projection', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const context = {
      githubUrlBase: 'https://github.com',
      pages: [],
      queries: [
        {
          name: 'run-totals',
          from: 'runs',
          aggregate: {
            by: ['organization', 'repository', 'workflow'],
            values: [{ field: 'run', as: 'runs', reducer: 'distinct-count' }]
          }
        },
        {
          name: 'workflow-run-inventory',
          from: 'workflows',
          joins: [{
            source: 'run-totals',
            type: 'left',
            on: [
              { left: 'organization', right: 'organization' },
              { left: 'repository', right: 'repository' },
              { left: 'workflow', right: 'workflow' }
            ],
            fields: [{ field: 'runs', as: 'observed-runs' }]
          }],
          compute: [{ as: 'total-runs', function: 'coalesce', args: [{ field: 'observed-runs' }, { value: 0 }] }],
          select: [{ field: 'workflow' }, { field: 'total-runs', as: 'runs' }],
          'order-by': [{ field: 'workflow', direction: 'asc' }]
        }
      ]
    };
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['workflow-run-inventory'],
      context
    );
    const navigated = await loadCanonicalDashboardPage(['workflow-run-inventory'], context);
    return { initial, navigated };
  });

  for (const payload of [result.initial, result.navigated]) {
    expect(Object.keys(payload)).toEqual(['workflow-run-inventory']);
    expect(payload['workflow-run-inventory']).toMatchObject({
      source: 'workflow-run-inventory',
      rows: [{ workflow: '.github/workflows/dashboard.md', runs: 1 }],
      metadata: { 'source-kind': 'derived', 'query-name': 'workflow-run-inventory', availability: 'available' }
    });
  }
});

test('data worker returns the Models & agents run distribution on initial and navigated requests', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const compilerUrl = `${location.origin}/src/data/queries/view-payload-compiler.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const { dashboardViewAliasName } = await import(compilerUrl);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const context = {
      githubUrlBase: 'https://github.com',
      pages: dashboard.dashboard.pages,
      queries: dashboard.dashboard.queries
    };
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['engines-models-usage'],
      context
    );
    const navigated = await loadCanonicalDashboardPage(['engines-models-usage'], context);
    const pageDefinition = context.pages.find((/** @type {any} */ candidate) => candidate.id === 'cost');
    const aicView = pageDefinition.views.find((/** @type {any} */ view) => view.id === 'engines-models-aic-insights');
    const aicAlias = dashboardViewAliasName(
      pageDefinition.id,
      aicView,
      pageDefinition.views.indexOf(aicView),
      'engines-models-usage'
    );
    const tableMode = await loadCanonicalDashboardPage(
      ['engines-models-usage'],
      context,
      undefined,
      { pageId: pageDefinition.id, queryContext: { viewMode: 'table' } }
    );
    return { initial, navigated, aicTable: tableMode[aicAlias] };
  });

  for (const payload of [result.initial, result.navigated]) {
    expect(Object.keys(payload)).toEqual(['engines-models-usage']);
    expect(payload['engines-models-usage']).toMatchObject({
      source: 'engines-models-usage',
      rows: [{
        summary: 'copilot / model-b',
        runs: 1,
        'average-aic-per-run': 17
      }],
      metadata: { 'source-kind': 'derived', 'query-name': 'engines-models-usage' }
    });
  }
  expect(result.aicTable).toMatchObject({
    rows: [{
      summary: 'copilot / model-b',
      runs: 1,
      'average-aic-per-run': 17
    }],
    metadata: { 'source-kind': 'derived' }
  });
});

test('grader scatter subscriptions recompute value/max when new canonical observations arrive', async ({ page, context }) => {
  /** @param {number} count */
  const cached = (count) => Array.from({ length: count }, (_, index) => JSON.stringify(currentFixtureDates({
    schema_version: 5, kind: 'run',
    run: {
      run_id: 501 + index, run_attempt: 1, organization: 'octo', repository: 'octo/api',
      workflow_name: 'Worker', workflow_path: '.github/workflows/worker.lock.yml',
      created_at: '2026-09-09T04:00:00Z', updated_at: `2026-09-09T04:0${index}:30Z`,
      status: 'completed', conclusion: 'success',
      graders: { results: [{ id: 'quality', value: 2 + index * 2, unit: 'score', status: 'observed' }] }
    }
  }))).join('\n');
  for (const [name, count] of [['first', 1], ['next', 2]]) {
    const shards = normalizedActivityShards(cached(Number(count)));
    await context.route(`http://dashboard.test/${name}/**`, async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path.endsWith('/payload-hashes.json')) {
        await route.fulfill({ contentType: 'application/json', body: JSON.stringify({
          [shardName]: createHash('sha256').update(shards.runs).digest('hex'),
          [recordShardName]: createHash('sha256').update(shards.records).digest('hex')
        }) });
      } else if (path.endsWith(shardName) || path.endsWith(recordShardName)) {
        await route.fulfill({
          contentType: 'application/x-ndjson', body: shards[path.endsWith(shardName) ? 'runs' : 'records']
        });
      } else {
        await route.fulfill({ status: 404, body: 'Not found' });
      }
    });
  }
  await page.goto('http://dashboard.test/?debug-eager-ingest=1');
  const result = await page.evaluate(async () => {
    const processor = await import(`${location.origin}/src/data-processor.js`);
    const { dashboard } = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const sources = ['grader-value-scatter'];
    const options = { pageId: 'graders', viewId: 'graders-value-scatter' };
    const initial = await processor.loadCanonicalDashboardSources(
      `${location.origin}/first/payload-hashes.json`, sources, dashboard, undefined, options
    );
    const lifetime = new AbortController();
    /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput> | undefined} */
    let subscribed;
    try {
      const refreshed = await new Promise((resolve, reject) => {
        processor.subscribeCanonicalDashboardView('grader-scatter-live', sources, dashboard, (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ payload) => {
          const rows = Object.values(payload).flatMap((source) => source.rows);
          if (!subscribed) {
            subscribed = payload;
            processor.refreshCanonicalDashboardSources(
              `${location.origin}/next/payload-hashes.json`, sources, dashboard, undefined, options
            ).catch(reject);
          } else if (rows.length === 2) resolve(payload);
        }, undefined, { ...options, signal: lifetime.signal, onError: reject });
      });
      return { initial, subscribed, refreshed };
    } finally {
      lifetime.abort();
    }
  });
  for (const payload of [result.initial, result.subscribed]) {
    expect(Object.values(payload).flatMap((source) => source.rows)).toMatchObject([
      { value: 2, 'max-grader-value': 2, 'normalized-value': 1 }
    ]);
  }
  expect(Object.values(result.refreshed).flatMap((source) => source.rows)).toMatchObject([
    { value: 2, 'max-grader-value': 4, 'normalized-value': 0.5 },
    { value: 4, 'max-grader-value': 4, 'normalized-value': 1 }
  ]);
});

test('data worker returns MCP tool totals without safe outputs calls on initial and navigated requests', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const compilerUrl = `${location.origin}/src/data/queries/view-payload-compiler.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const { dashboardViewAliasName } = await import(compilerUrl);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const context = {
      githubUrlBase: 'https://github.com',
      pages: dashboard.dashboard.pages,
      views: dashboard.dashboard.views,
      queries: dashboard.dashboard.queries
    };
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/payload-hashes.json`,
      ['mcp-tool-totals', 'mcp-top-tools'],
      context
    );
    const navigated = await loadCanonicalDashboardPage(
      ['mcp-tool-totals', 'mcp-top-tools'],
      context,
      undefined,
      { pageId: 'mcps' }
    );
    const pageDefinition = context.pages.find((/** @type {any} */ candidate) => candidate.id === 'mcps');
    const topToolsView = pageDefinition.views.find((/** @type {any} */ view) => view.id === 'mcp-top-tools');
    const topToolsAlias = dashboardViewAliasName(
      pageDefinition.id,
      topToolsView,
      pageDefinition.views.indexOf(topToolsView),
      'mcp-top-tools'
    );
    const base = await loadCanonicalDashboardPage(['mcp-calls'], context);
    const { executeDashboardQueries } = await import(`${location.origin}/src/data/queries/declarative.js`);
    const direct = executeDashboardQueries(dashboard.dashboard.queries, base, ['mcp-tool-calls', 'mcp-tool-totals']);
    return { initial, navigated, topToolsAlias, base, direct };
  });

  expect(result.base['mcp-calls'].rows).toHaveLength(3);
  expect(result.base['mcp-calls'].rows[0]).toMatchObject({
    'mcp-server': 'github',
    'mcp-tool': 'search_issues'
  });
  expect(result.direct['mcp-tool-calls'].rows).toHaveLength(3);
  expect(result.direct['mcp-tool-totals'].rows).toHaveLength(1);
  for (const payload of [result.initial]) {
    expect(Object.keys(payload)).toEqual(['mcp-tool-totals', 'mcp-top-tools']);
    expect(payload['mcp-tool-totals']).toMatchObject({
      source: 'mcp-tool-totals',
      rows: [{
        'mcp-tool-label': 'github/search_issues',
        'mcp-tool': 'search_issues',
        'mcp-server': 'github',
        calls: 2,
        workflows: 1
      }],
      metadata: { 'source-kind': 'derived', 'query-name': 'mcp-tool-totals' }
    });
    expect(payload['mcp-top-tools']).toMatchObject({
      source: 'mcp-top-tools',
      rows: [{ 'mcp-tool-label': 'github/search_issues', 'mcp-tool': 'search_issues', 'mcp-server': 'github', calls: 2, workflows: 1 }],
      metadata: { 'source-kind': 'derived', 'query-name': 'mcp-top-tools' }
    });
  }
  expect(result.navigated[result.topToolsAlias]).toMatchObject({
    rows: [{
      'mcp-tool-label': 'github/search_issues',
      'mcp-tool': 'search_issues',
      calls: 2
    }],
    metadata: { availability: 'available', 'source-kind': 'derived' }
  });
});

test('data worker returns skill invocation rankings on initial and navigated requests', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const context = {
      githubUrlBase: 'https://github.com',
      pages: dashboard.dashboard.pages,
      queries: dashboard.dashboard.queries
    };
    const sourceNames = ['skill-invocations-by-skill', 'skill-invocations-by-workflow'];
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/payload-hashes.json`,
      sourceNames,
      context
    );
    const navigated = await loadCanonicalDashboardPage(sourceNames, context, undefined, { pageId: 'skills' });
    return { initial, navigated };
  });

  expect(result.initial['skill-invocations-by-skill']).toMatchObject({
    rows: [{ skill: 'reactive-ui', invocations: 1 }],
    metadata: { availability: 'available' }
  });
  expect(result.initial['skill-invocations-by-workflow']).toMatchObject({
    rows: [{
      'workflow-coordinate': 'githubnext/gh-cao:.github/workflows/dashboard.md',
      invocations: 1
    }],
    metadata: { availability: 'available' }
  });
  expect(Object.keys(result.navigated)).toEqual([
    'view:skills:skills-most-invoked:skill-invocations-by-skill',
    'view:skills:skills-by-workflow:skill-invocations-by-workflow'
  ]);
  expect(result.navigated['view:skills:skills-most-invoked:skill-invocations-by-skill']).toMatchObject({
    rows: [{ skill: 'reactive-ui', invocations: 1 }],
    metadata: { availability: 'available' }
  });
  expect(result.navigated['view:skills:skills-by-workflow:skill-invocations-by-workflow']).toMatchObject({
    rows: [{
      'workflow-coordinate': 'githubnext/gh-cao:.github/workflows/dashboard.md',
      invocations: 1
    }],
    metadata: { availability: 'available' }
  });
});


test('data worker queries firewall summaries on initial and navigated requests', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const context = {
      githubUrlBase: 'https://github.com',
      pages: dashboard.dashboard.pages,
      queries: dashboard.dashboard.queries
    };
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['firewall-domain-totals', 'firewall-most-blocked-domains'],
      context
    );
    const navigated = await loadCanonicalDashboardPage(
      ['firewall-domain-totals', 'firewall-most-blocked-domains'],
      context,
      undefined,
      { pageId: 'firewall' }
    );
    return { initial, navigated };
  });

  expect(Object.keys(result.initial).sort()).toEqual([
    'firewall-domain-totals',
    'firewall-most-blocked-domains'
  ]);
  expect(result.initial['firewall-domain-totals']).toMatchObject({
    rows: [{ domain: 'api.github.com', run: 1, accepted: 4, blocked: 2 }],
    metadata: { 'source-kind': 'derived', 'query-name': 'firewall-domain-totals' }
  });
  expect(result.initial['firewall-most-blocked-domains']).toMatchObject({
    rows: [{ domain: 'api.github.com', run: 1, accepted: 4, blocked: 2 }],
    metadata: { 'source-kind': 'derived', 'query-name': 'firewall-most-blocked-domains' }
  });
  expect(result.navigated['view:firewall:security-firewall-most-blocked-domains:firewall-most-blocked-domains']).toMatchObject({
    rows: [{ domain: 'api.github.com', run: 1, accepted: 4, blocked: 2 }],
    metadata: { availability: 'available' }
  });
  expect(result.navigated['view:firewall:security-firewall-domains:firewall-domain-totals']).toMatchObject({
    rows: [{ domain: 'api.github.com', run: 1, accepted: 4, blocked: 2 }],
    metadata: { availability: 'available' }
  });
});

test('gh-aw logs audit populates the firewall domain query from canonical events', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources } = await import(processorUrl);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    return loadCanonicalDashboardSources(
      `${location.origin}/payload-hashes.json`,
      ['firewall-domain-totals'],
      {
        githubUrlBase: 'https://github.com',
        pages: dashboard.dashboard.pages,
        queries: dashboard.dashboard.queries
      }
    );
  });

  expect(result['firewall-domain-totals']).toMatchObject({
    rows: [
      { domain: 'api.github.com', run: 1, accepted: 4, blocked: 2 },
      { domain: 'objects.githubusercontent.com', run: 1, accepted: 3, blocked: 0 }
    ],
    metadata: { 'source-kind': 'derived', 'query-name': 'firewall-domain-totals' }
  });
});

test('data worker computes repository and campaign pages with request-scoped dashboard queries', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const context = {
      githubUrlBase: 'https://github.com',
      pages: dashboard.dashboard.pages,
      queries: dashboard.dashboard.queries
    };
    const initial = await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['repository-activity'],
      context
    );
    const horizon = await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`,
      ['repository-activity'],
      context,
      undefined,
      {
        pageId: 'repositories',
        queryContext: {
          timeWindow: {
            start: '2026-09-10T00:00:00Z',
            end: '2026-10-01T00:00:00Z'
          }
        }
      }
    );
    const navigated = await loadCanonicalDashboardPage(['campaign-inventory'], context);
    return { initial, horizon, navigated };
  });

  expect(Object.keys(result.initial)).toEqual(['repository-activity']);
  expect(result.initial['repository-activity']).toMatchObject({
    rows: [{
      repository: 'githubnext/gh-cao',
      workflows: 1,
      reports: 1,
      runs: 1,
      'failure-summary': '100% · 1 failed',
      aic: 17,
      status: 'Needs attention'
    }],
    metadata: { 'source-kind': 'derived', 'query-name': 'repository-activity' }
  });
  const horizonSource = result.horizon[Object.keys(result.horizon)[0]];
  expect(Object.keys(result.horizon)).not.toContain('repository-activity');
  expect(horizonSource).toMatchObject({
    rows: [{
      repository: 'githubnext/gh-cao',
      workflows: 1,
      reports: null,
      runs: 0,
      aic: 0
    }],
    metadata: { availability: 'available' }
  });
  expect(Object.keys(result.navigated)).toEqual(['campaign-inventory']);
  expect(result.navigated['campaign-inventory']).toMatchObject({
    rows: [{
      campaign: 'dashboard',
      'campaign-name': 'CAO Dashboard',
      workflows: 1,
      roles: 'worker',
      modes: 'review',
      registration: 'true',
      runs: 1,
      dispatches: 0,
      aic: 17
    }],
    metadata: { 'source-kind': 'derived', 'query-name': 'campaign-inventory' }
  });
});

test('deployed JSONL ingestion includes the published campaign inventory', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const { loadCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    return loadCanonicalDashboardSources(
      `${location.origin}/payload-hashes.json`,
      ['campaigns', 'workflows', 'campaign-inventory'],
      {
        githubUrlBase: 'https://github.com',
        pages: dashboard.dashboard.pages,
        queries: dashboard.dashboard.queries
      }
    );
  });

  expect(result.campaigns.rows).toHaveLength(2);
  expect(result.workflows.rows).toHaveLength(1);
  expect(result['campaign-inventory']).toMatchObject({
    rows: [
      {
        campaign: 'dashboard',
        'campaign-name': 'CAO Dashboard',
        workflows: 1,
        runs: 1,
        dispatches: 0
      },
      {
        campaign: 'repo-assist',
        'campaign-name': 'Repo Assist',
        workflows: 0,
        runs: 0,
        dispatches: 0
      }
    ]
  });
});

test('campaign Reports retains producing-worker attribution after Activity ingestion', async ({ page }) => {
  await page.route('http://dashboard.test/sources.json', async (route) => {
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(canonicalWarningSources()) });
  });
  const result = await page.evaluate(async () => {
    const { loadCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    const dashboard = await fetch(`${location.origin}/dashboard.json`).then((response) => response.json());
    const context = {
      githubUrlBase: 'https://github.com',
      pages: dashboard.dashboard.pages,
      queries: dashboard.dashboard.queries
    };
    await loadCanonicalDashboardSources(
      `${location.origin}/sources.json`, ['campaign-worker-issues'], context
    );
    return loadCanonicalDashboardSources(
      `${location.origin}/payload-hashes.json`,
      ['campaign-worker-issues', 'campaign-issue-tab-counts'], context
    );
  });
  expect(result['campaign-worker-issues']).toMatchObject({
    metadata: { availability: 'available' },
    rows: [{
      campaign: 'dashboard',
      workflow: '.github/workflows/dashboard.md',
      run: '12345',
      'issue-link': { href: 'https://github.com/githubnext/gh-cao/issues/7' }
    }]
  });
  expect(result['campaign-issue-tab-counts'].rows).toEqual([{ campaign: 'dashboard', items: 1 }]);
});

test('data worker derives work items and refuses retired report sources', async ({ page }) => {
  const result = await page.evaluate(async () => {
    const processorUrl = `${location.origin}/src/data-processor.js`;
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage } = await import(processorUrl);
    const context = { githubUrlBase: 'https://github.com', pages: [] };
    await loadCanonicalDashboardSources(`${location.origin}/sources.json`, ['work-items'], context);
    return loadCanonicalDashboardPage(['work-items', 'security-findings'], context);
  });

  expect(Object.keys(result)).toEqual(['work-items', 'security-findings']);
  expect(result['work-items']).toMatchObject({
    rows: [{ 'work-item-id': 'githubnext/gh-cao:.github/workflows/dashboard.md', 'lifecycle-state': 'blocked' }]
  });
  expect(result['security-findings']).toMatchObject({
    rows: [],
    metadata: { availability: 'unavailable', error: expect.stringContaining('No canonical database query') }
  });
});

test('Chromium ingests gh-aw artifacts as a Run and ordered run records', async ({ page }) => {
  const result = await page.evaluate(async (input) => {
    const coordinatorUrl = `${location.origin}/src/data/ingest/coordinator.js`;
    const queriesUrl = `${location.origin}/src/data/queries/index.js`;
    const [{ ingestGhAwLogs }, { createCanonicalQueries }] = await Promise.all([
      import(coordinatorUrl),
      import(queriesUrl)
    ]);
    const ingestion = await ingestGhAwLogs(indexedDB, input);
    const queries = createCanonicalQueries(indexedDB);
    const runs = await queries.runs.list();
    const runId = String(runs[0].id);
    const [domains, tools, audits, issues] = await Promise.all([
      queries.domains.forRun(runId),
      queries.tools.forRun(runId),
      queries.audits.forRun(runId),
      queries.issues.forRun(runId)
    ]);
    return { ingestion, runs, domains, tools, audits, issues };
  }, ghAwLogInput());

  expect(result.ingestion).toMatchObject({ updated: true });
  expect(result.runs[0].id).toBe('github:run:githubnext/gh-cao:303');
  expect(result.domains.map((/** @type {Record<string, unknown>} */ record) => [record.sequence, record.source, record.type])).toEqual([
    [0, 'firewall', 'firewall.http_access']
  ]);
  expect(result.tools.map((/** @type {Record<string, unknown>} */ record) => [record.sequence, record.source, record.type])).toEqual([
    [0, 'gateway', 'mcp.tool_call'],
    [1, 'agent', 'tool.execution_start'],
    [2, 'agent', 'tool.execution_complete']
  ]);
  expect(result.audits.map((/** @type {Record<string, unknown>} */ record) => [record.sequence, record.source, record.type])).toEqual([
    [0, 'agent', 'user.message'],
    [1, 'agent', 'assistant.message']
  ]);
  expect(result.issues).toEqual([]);
});

test('SQLite and browser IndexedDB ingestion produce identical populated tables', async ({ page }) => {
  const originalKeyRange = globalThis.IDBKeyRange;
  globalThis.IDBKeyRange = /** @type {typeof IDBKeyRange} */ (SqliteIDBKeyRange);
  const input = ghAwLogInput();
  const directory = mkdtempSync(join(tmpdir(), 'cao-ingestion-compliance-'));
  try {
    const sqliteIndexedDB = createSqliteIndexedDB(join(directory, 'dashboard.sqlite'));
    await ingestNodeGhAwLogs(sqliteIndexedDB, input, { now: Date.now() });
    const sqliteRows = await readCanonicalBatch(sqliteIndexedDB);

    const downloadPromise = page.waitForEvent('download');
    await page.evaluate(async (browserInput) => {
      const coordinatorUrl = `${location.origin}/src/data/ingest/coordinator.js`;
      const storageUrl = `${location.origin}/src/data/storage/indexeddb.js`;
      const [{ ingestGhAwLogs }, { readCanonicalBatch }] = await Promise.all([
        import(coordinatorUrl),
        import(storageUrl)
      ]);
      await ingestGhAwLogs(indexedDB, browserInput);
      const rows = await readCanonicalBatch(indexedDB);
      const url = URL.createObjectURL(new Blob([JSON.stringify(rows)], { type: 'application/json' }));
      const link = document.createElement('a');
      link.href = url;
      link.download = 'indexeddb-canonical-data.json';
      link.click();
    }, input);
    const download = await downloadPromise;
    const browserExportPath = await download.path();
    expect(browserExportPath).not.toBeNull();
    const browserRows = JSON.parse(readFileSync(/** @type {string} */ (browserExportPath), 'utf8'));

    for (const [backend, tables] of Object.entries({ SQLite: sqliteRows, IndexedDB: browserRows })) {
      expect(Object.keys(tables).sort()).toEqual(canonicalEntityTables);
      for (const table of ['repositories', 'workflows', 'runs', 'domains', 'tools', 'audits']) {
        expect(tables[table].length, `${backend} ${table} should contain compliance fixture data`).toBeGreaterThan(0);
      }
    }
    expect(browserRows).toEqual(sqliteRows);
  } finally {
    globalThis.IDBKeyRange = originalKeyRange;
    rmSync(directory, { recursive: true, force: true });
  }
});

test('deletion rebuilds derived state and fresh data is directly upserted', async ({ page }) => {
  const result = await page.evaluate(async ({ firstSources, replacementSources, name }) => {
    const coordinatorUrl = `${location.origin}/src/data/ingest/coordinator.js`;
    const storageUrl = `${location.origin}/src/data/storage/indexeddb.js`;
    const [{ ingestDashboardSources }, storage] = await Promise.all([
      import(coordinatorUrl),
      import(storageUrl)
    ]);
    await ingestDashboardSources(indexedDB, firstSources);
    await new Promise((resolve, reject) => {
      const request = indexedDB.deleteDatabase(name);
      request.onsuccess = () => resolve(undefined);
      request.onerror = () => reject(request.error);
    });
    const rebuilt = await ingestDashboardSources(indexedDB, firstSources);
    const replaced = await ingestDashboardSources(indexedDB, replacementSources);
    return {
      rebuilt,
      replaced,
      repositories: await storage.readCollection(indexedDB, 'repositories'),
      runs: await storage.readCollection(indexedDB, 'runs')
    };
  }, {
    firstSources: databaseTables('generation-a', '101'),
    replacementSources: databaseTables('generation-b', '202'),
    name: databaseName
  });

  expect(result.rebuilt).toMatchObject({ updated: true });
  expect(result.replaced).toMatchObject({ updated: true });
  expect(result.repositories).toEqual([
    expect.objectContaining({
      id: 'repository:githubnext%2Fgh-cao'
    })
  ]);
  expect(result.runs.map((/** @type {Record<string, unknown>} */ run) => run.id)).toEqual([
    'github:run:githubnext/gh-cao:101',
    'github:run:githubnext/gh-cao:202'
  ]);
});

test('invalid direct upserts are rejected before changing stored data', async ({ page }) => {
  const activeSources = databaseTables('generation-a', '101');
  const result = await page.evaluate(async (active) => {
    const coordinatorUrl = `${location.origin}/src/data/ingest/coordinator.js`;
    const normalizeUrl = `${location.origin}/src/data/normalize/index.js`;
    const storageUrl = `${location.origin}/src/data/storage/indexeddb.js`;
    const [{ ingestDashboardSources }, { normalize }, storage] = await Promise.all([
      import(coordinatorUrl), import(normalizeUrl), import(storageUrl)
    ]);
    await ingestDashboardSources(indexedDB, active);

    const invalid = normalize([]);
    invalid.workflows.push({
      id: 'workflow:missing-parent',
      repositoryId: 'repository:missing'
    });
    let error = '';
    try {
      await storage.upsertCanonicalBatch(indexedDB, invalid);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : String(cause);
    }
    return {
      error,
      repositories: await storage.readCollection(indexedDB, 'repositories'),
      workflows: await storage.readCollection(indexedDB, 'workflows')
    };
  }, activeSources);

  expect(result.error).toContain('Canonical relationship validation failed');
  expect(result.repositories).toHaveLength(1);
  expect(result.workflows).toHaveLength(1);
});