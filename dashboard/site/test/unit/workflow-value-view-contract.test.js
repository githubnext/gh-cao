// @vitest-environment node
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { IDBFactory } from 'fake-indexeddb';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ingestCachedGhAwJsonl } from '../../src/data/ingest/coordinator.js';
import { processDataRequest } from '../../src/data-worker.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import ledgerFixture from '../fixtures/operational-value-ledger.json' with { type: 'json' };
import contract from '../fixtures/workflow-value-view-contract.json' with { type: 'json' };
import { evidencePacketSelection, readEvidencePackets } from '../../src/data/queries/evidence-selection.js';

const dashboard = authoritativeDashboard.dashboard;
const repository = resolve('../..');
/** @type {string} */
let directory;
let sequence = 0;

beforeEach(() => {
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse(ledgerFixture.observedAt));
  mkdirSync(join(repository, '.tmp'), { recursive: true });
  directory = mkdtempSync(join(repository, '.tmp', 'workflow-value-contract-'));
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  rmSync(directory, { recursive: true, force: true });
});

/**
 * Exercise the supported skill CLI, original-evidence adapter, normalization,
 * canonical storage and worker queries, never hand-made logical source aliases.
 * @param {Record<string, unknown>} [ledger]
 */
async function publish(ledger = structuredClone(ledgerFixture)) {
  const stem = join(directory, `report-${sequence++}`);
  writeFileSync(`${stem}.json`, JSON.stringify(ledger));
  execFileSync(process.execPath, [
    '.github/skills/add-operational-value/scripts/render-pulse.mjs', `${stem}.json`, `${stem}.html`
  ], { cwd: repository, stdio: 'pipe' });
  const content = readFileSync(`${stem}-evidence.jsonl`, 'utf8');
  await ingestCachedGhAwJsonl(indexedDB, content, {
    now: Date.parse(String(ledger.observedAt)), payloadScope: stem
  });
  return JSON.parse(content).operational_value_evidence;
}

/** @param {string} pageId @param {Record<string, string>} parameters */
async function execute(pageId, parameters) {
  const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === pageId);
  const payload = compileDashboardViewPayloadQueries(page, pageId, {
    queries: dashboard.queries, routeParameters: parameters
  });
  const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'query-canonical-dashboard', sourceNames: payload.aliases,
    context: { queries: payload.queries, pages: dashboard.pages }
  }));
  for (const alias of payload.aliases) expect(results[alias].metadata.availability, alias).not.toBe('unavailable');
  return payload.aliases.map(alias => results[alias]);
}

/** @param {'decrease' | 'increase'} [direction] */
function fileLedger(direction = 'decrease') {
  const ledger = structuredClone(ledgerFixture);
  ledger.repositoryState.metric.unit = 'files';
  ledger.repositoryState.metric.direction = direction;
  ledger.repositoryState.observations[0].value = 16;
  ledger.repositoryState.observations[1].value = 54;
  for (const snapshot of ledger.repositoryState.observations) {
    snapshot.items = Array.from({ length: snapshot.value }, (_, index) => ({
      label: `pkg/file-${index}.go`,
      url: `https://github.com/octo/example/blob/${snapshot.ref}/pkg/file-${index}.go`
    }));
  }
  return ledger;
}

describe('native workflow value production contract', () => {
  it('does not narrow an overridden or limited canonical identity projection', () => {
    for (const projection of [
      { limit: 1 },
      { compute: [{ as: 'report-id', function: 'literal', args: [{ value: 'changed' }] }] },
      { select: [{ field: 'other', as: 'report-id' }] }
    ]) {
      expect(evidencePacketSelection([
        { name: 'operational-value-evidence', from: '$operationalValueEvidence', ...projection },
        { name: 'selected', from: 'operational-value-evidence',
          filter: { predicates: [{ field: 'report-id', equals: 'changed' }] } }
      ], ['selected'])).toBeNull();
    }
  });

  it('bounds exact native report reads while 1000 unrelated workflows remain stored', async () => {
    const packet = await publish(fileLedger());
    for (let batch = 0; batch < 40; batch++) {
      const lines = Array.from({ length: 25 }, (_, offset) => {
        const other = structuredClone(packet);
        other.repository = `load/repo-${batch * 25 + offset}`;
        other.id = packet.id.replace('octo%2Fexample', encodeURIComponent(other.repository));
        return JSON.stringify({ schema_version: 2, kind: 'operational_value_evidence', operational_value_evidence: other });
      }).join('\n');
      await ingestCachedGhAwJsonl(indexedDB, lines, { now: Date.parse(packet.observedAt), payloadScope: `load-${batch}` });
    }
    for (const [pageId, parameters] of [
      [contract.reportPage, { report: packet.id }],
      [contract.page, { workflow: contract.route }],
    ]) {
      const page = dashboard.pages.find((/** @type {{id: string}} */ page) => page.id === pageId);
      const compiled = compileDashboardViewPayloadQueries(page, String(pageId), {
        queries: dashboard.queries, routeParameters: /** @type {Record<string, string>} */ (parameters)
      });
      const selection = evidencePacketSelection(compiled.queries, compiled.aliases);
      expect(selection).toHaveLength(1);
      const selected = await readEvidencePackets(indexedDB, selection);
      expect(selected).toHaveLength(1);
      expect(selected[0].facts).toHaveLength(packet.facts.length);
      const sources = await processDataRequest({ operation: 'query-canonical-dashboard',
        sourceNames: compiled.aliases, context: { queries: compiled.queries, pages: dashboard.pages } });
      for (const source of Object.values(/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (sources))) {
        expect(source.metadata.availability).not.toBe('unavailable');
      }
    }
    await expect(readEvidencePackets(indexedDB, null)).rejects.toThrow(/max-input-rows/);
    await expect(readEvidencePackets(indexedDB, [{ field: 'id', value: packet.id }],
      () => { throw new Error('cancelled before read'); })).rejects.toThrow('cancelled before read');
  }, 30000);

  it('declares a shared card, native report, complete source contract and exact-item route', () => {
    const value = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'value');
    expect(value).toMatchObject({ title: 'Value', icon: 'trophy' });
    expect(value.views[0]).toMatchObject({ data: { source: 'value-registered-collections' }, list: { card: contract.collectionCard } });
    expect(value.views[1]).toMatchObject({ data: { source: contract.query }, list: { card: contract.card } });
    expect(dashboard.navigation.flatMap((/** @type {{ pages: string[] }} */ group) => group.pages)).toContain('value');
    expect(value.route?.tabs).toBeUndefined();
    expect(value.views[0]['empty-message']).not.toContain('Repository measurements');
    for (const id of ['operational-value', 'operational-value-evidence', 'operational-value-repository']) {
      expect(dashboard.pages.some((/** @type {{ id: string }} */ page) => page.id === id)).toBe(false);
      expect(dashboard.navigation.flatMap((/** @type {{ pages: string[] }} */ group) => group.pages)).not.toContain(id);
      expect(JSON.stringify(dashboard)).not.toContain(`#page-${id}`);
      expect(JSON.stringify(dashboard)).not.toContain(`"page":"${id}"`);
    }
    expect(dashboard.queries.some((/** @type {{ name: string }} */ query) =>
      query.name.startsWith('operational-value-'))).toBe(false);
    const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
    expect(page.views[0]).toMatchObject({
      id: contract.collectionView, data: { source: contract.collectionQuery, arguments: [{ name: 'workflow', field: 'workflow-route' }] },
      mark: 'list', list: { card: contract.collectionCard }
    });
    expect(page.views[1]).toMatchObject({
      id: contract.view, data: { source: contract.query, 'route-field': 'workflow-route' },
      mark: 'list', list: { card: contract.card, drill: { page: contract.reportPage } }
    });
    const report = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.reportPage);
    expect(report.views[0]).toMatchObject({
      mark: 'element', element: contract.reportElement, data: { sources: contract.reportSources }
    });
    const snapshot = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.snapshotPage);
    expect(snapshot.views[0]).toMatchObject({
      element: contract.snapshotElement, data: { arguments: contract.snapshotArguments }
    });
  });

  it('resolves lower-is-better movement, all report evidence and exact snapshot items from emitted canonical facts', async () => {
    const packet = await publish(fileLedger());
    const [, cards] = await execute(contract.page, { workflow: contract.route });
    expect(cards.rows).toHaveLength(1);
    expect(cards.rows[0]).toMatchObject({
      'change-label': contract.headline, movement: 'Worse', 'reference-value': 16, 'current-value': 54,
      'direction-label': '↓ Lower is better', 'output-count': 1, 'accepted-implementations': 1,
      'native-result': 1, 'complete-report': false, 'recent-result': null, 'previous-result': null
    });
    const sources = await execute(contract.reportPage, { report: packet.id });
    expect(sources[0].rows).toEqual(cards.rows.map(({ 'older-report': _order, ...row }) => row));
    expect(sources[1].rows[0].url).toContain('/blob/abc123/.github/workflows/verify-bugs.md');
    expect(sources[4].rows).toHaveLength(1);
    expect(sources[5].rows[0]['implementation-url']).toBe('https://github.com/octo/example/pull/2');
    const latest = sources[6].rows.at(-1);
    expect(latest).toBeDefined();
    const snapshotRoute = new URL(String(latest?.['snapshot-href']), 'https://dashboard.example/').hash.split('?')[1];
    const snapshotSources = await execute(contract.snapshotPage, Object.fromEntries(new URLSearchParams(snapshotRoute)));
    expect(snapshotSources[0].rows).toHaveLength(1);
    expect(snapshotSources[1].rows).toHaveLength(54);
    expect(snapshotSources[1].rows[0].url).toMatch(/^https:\/\/github.com\/octo\/example\/blob\/abc123\/pkg\/file-\d+.go$/);
    expect((await execute(contract.page, { workflow: 'octo/other:.github/workflows/verify-bugs.md' }))[1].rows).toEqual([]);
  });

  it('interprets the same native movement using the frozen desired direction', async () => {
    await publish(fileLedger('increase'));
    expect((await execute(contract.page, { workflow: contract.route }))[1].rows[0]).toMatchObject({
      'change-label': '↑ +38 files · Better', movement: 'Better', 'direction-label': '↑ Higher is better'
    });
  });

  it('deduplicates accepted value across many output-to-implementation links and retains negative effects', async () => {
    const ledger = structuredClone(ledgerFixture);
    ledger.events[0].value = -1;
    ledger.outputs.items.push({ ...ledger.outputs.items[0], id: 'issue:3', url: 'https://github.com/octo/example/issues/3' });
    ledger.events[0].lineage.push({ ...ledger.events[0].lineage[0], outputId: 'issue:3' });
    const packet = await publish(ledger);
    const sources = await execute(contract.reportPage, { report: packet.id });
    expect(sources[0].rows[0]).toMatchObject({ 'output-count': 2, 'accepted-implementations': 1, 'native-result': -1 });
    expect(sources[4].rows).toHaveLength(2);
  });

  it('distinguishes complete zero recent delivery from missing coverage', async () => {
    const ledger = structuredClone(ledgerFixture);
    ledger.coverage[0].status = 'complete';
    ledger.outputs.items[0].createdAt = '2026-08-01T00:00:00Z';
    ledger.events[0].at = ledger.events[0].acceptance.at = '2026-09-01T00:00:00Z';
    ledger.events[0].observedAt = '2026-09-02T00:00:00Z';
    ledger.events[0].lineage[0].at = '2026-08-31T00:00:00Z';
    await publish(ledger);
    expect((await execute(contract.page, { workflow: contract.route }))[1].rows[0]).toMatchObject({
      'complete-report': true, 'recent-result': 0, 'previous-result': 1
    });
  });

  it('keeps singleton state and unchanged state distinct', async () => {
    const ledger = structuredClone(ledgerFixture);
    ledger.repositoryState.observations.splice(0, 1);
    await publish(ledger);
    expect((await execute(contract.page, { workflow: contract.route }))[1].rows[0]).toMatchObject({
      'current-value': 0, 'native-change': null, movement: 'Comparison unavailable'
    });
    ledger.repositoryState.observations.unshift({ ...ledger.repositoryState.observations[0], at: '2026-10-01T00:00:00Z' });
    ledger.observedAt = '2026-10-20T12:01:00Z';
    await publish(ledger);
    expect((await execute(contract.page, { workflow: contract.route }))[1].rows[0]).toMatchObject({
      'native-change': 0, movement: 'Unchanged', 'change-label': 'Unchanged'
    });
  });

  it('keeps absent output, snapshot and quality evidence unknown', async () => {
    await publish({ ...structuredClone(ledgerFixture), outputs: undefined, repositoryState: undefined, checks: undefined });
    const [, cards] = await execute(contract.page, { workflow: contract.route });
    expect(cards.rows[0]).toMatchObject({
      'output-count': null, 'receipt-outputs': null, 'current-value': null,
      'native-change': null, movement: 'Comparison unavailable', 'direction-label': 'Direction not retained',
      'outputs-available': false, 'checks-available': false
    });
  });

  it('reports known empty cohorts as zero only with explicit complete coverage', async () => {
    const ledger = structuredClone(ledgerFixture);
    ledger.events = [];
    ledger.outputs.items = [];
    ledger.coverage[0].status = 'complete';
    await publish(ledger);
    const [, cards] = await execute(contract.page, { workflow: contract.route });
    expect(cards.rows[0]).toMatchObject({
      'output-count': 0, 'receipt-outputs': 0, 'accepted-implementations': 0,
      'native-result': 0, 'complete-report': true, 'recent-result': 0, 'previous-result': 0
    });
  });

  it('does not substitute an older measured version for a newly published incomplete packet', async () => {
    const older = await publish(fileLedger());
    const newer = structuredClone(ledgerFixture);
    newer.observedAt = '2026-10-20T12:01:00Z';
    newer.metric.version = 'v2';
    newer.repositoryState.observations.splice(0, 1);
    await publish(newer);
    const [, cards] = await execute(contract.page, { workflow: contract.route });
    expect(cards.rows).toHaveLength(1);
    expect(cards.rows[0]).toMatchObject({ 'metric-version': 'v2', movement: 'Comparison unavailable' });
    expect((await execute(contract.reportPage, { report: older.id }))[0].rows[0]['change-label']).toBe(contract.headline);
  });

  it('publishes real registration decisions through the producer, canonical storage and worker before any packet exists', async () => {
    const policy = join(directory, 'cao.json');
    writeFileSync(policy, JSON.stringify({ version: 1, 'gh-aw-version': 'v0.93.1', 'control-plane': {
      scope: { 'allowed-repositories': ['octo/example'] }, activity: { repositories: ['octo/example'] },
      campaigns: { activity: { 'workflow-values': [{
        repository: 'octo/example', workflow: 'verify-bugs', collector: 'absent-by-design.mjs'
      }] } }
    } }));
    const healthOutput = join(directory, 'health.jsonl');
    execFileSync(process.execPath, ['activity/workflow-values.mjs',
      '--root', directory, '--policy', policy, '--database', join(directory, 'unused.sqlite'),
      '--output', join(directory, 'packets.jsonl'), '--workspace', join(directory, 'proof'),
      '--repository', 'octo/example', '--healthOutput', healthOutput, '--defer', 'api-budget'
    ], { cwd: repository, stdio: 'pipe' });
    const checkedAt = JSON.parse(readFileSync(healthOutput, 'utf8')).workflow_value_collection.observedAt;
    await ingestCachedGhAwJsonl(indexedDB, readFileSync(healthOutput, 'utf8'), {
      payloadScope: 'first-health', now: Date.parse(checkedAt)
    });
    const [health, reports] = await execute(contract.page, { workflow: contract.route });
    expect(health.rows).toHaveLength(1);
    expect(health.rows[0]).toMatchObject({
      registration: 'enabled', 'collection-state': 'deferred', 'reason-code': 'api-budget',
      'checked-at': checkedAt, 'captured-at': null, 'evidence-cutoff': null, 'evidence-state': 'No retained capture'
    });
    expect(health.rows[0]['last-attempt-at']).toBeUndefined();
    expect(reports.rows).toEqual([]);
    expect((await execute('value', {}))[0].rows).toEqual(health.rows);
    const packet = await publish();
    const [retained] = await execute(contract.page, { workflow: contract.route });
    expect(retained.rows[0]).toMatchObject({
      'collection-state': 'deferred', 'captured-at': packet.observedAt,
      'evidence-cutoff': packet.windowEnd, 'evidence-state': 'Retained evidence available'
    });
    expect((await execute(contract.page, { workflow: 'octo/other:.github/workflows/verify-bugs.md' }))[0].rows).toEqual([]);
  });
});
