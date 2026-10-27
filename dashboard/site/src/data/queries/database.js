import { ingestDashboardSources } from '../ingest/coordinator.js';
import databaseQueries from './database.json' with { type: 'json' };
import {
  CANONICAL_DATABASE_SCHEMA,
  CANONICAL_QUERY_INDEX_FIELDS,
  countCollections,
  queryCollection,
  queryCollectionCountGroups,
  readCollectionQueryKeys,
  readCollectionQueryKeyRecords,
  readCollections,
  readTransactions,
  scanCanonicalCollection
} from '../storage/indexeddb.js';
import { STORAGE_SHARD_STORE } from '../storage/shards.js';
import {
  createDashboardQueryBudget,
  DashboardQueryCancelledError,
  DASHBOARD_QUERY_LIMITS,
  dashboardQueryDefects,
  dashboardQueryIndex,
  executeDashboardQueries,
  queryInputNames,
  resolveDashboardQuerySources,
  unavailableResult
} from './declarative.js';
import { SERVER_SOURCE_VALUES, SYNTHETIC_SOURCE_FIELDS } from '../../specification.js';
import { isGitHubQuerySource } from '../github-query-sources.js';
import { SIMULATION_DAYS, simulationDaysSource } from './simulation-days.js';
import { createDebug } from '../../debug.js';
import { operationalValueEvidenceMetadata, operationalValueEvidenceRows } from '../model/operational-value-evidence.js';
import { DAILY_RUN_COUNT_STATE_ID } from '../model/daily-run-counts.js';
import { incrementalAggregate } from './incremental-aggregate.js';
import { evidencePacketSelection, readEvidencePackets } from './evidence-selection.js';
import { measurementRows, measurementSelection, readMeasurements } from './measurement-selection.js';
import { canonicalStatisticalWatermark, sampleStatisticalRuns, STATISTICAL_INPUT_LIMITS } from './statistical-sample.js';

const debugDatabase = createDebug('database');
const monotonicNow = () => globalThis.performance?.now() ?? Date.now();
const databaseQueryIndex = dashboardQueryIndex(databaseQueries);
const RUN_RECORD_STORES = new Set(['domains', 'tools', 'skills', 'friction', 'audits', 'issues']);
const DIRECT_EVIDENCE_SOURCES = new Set([
  'experiments', 'experiment-assignments', 'graders',
  'grader-observations', 'evals', 'eval-observations', 'workflow-value-collections'
]);
const EXTERNAL_SOURCES = new Set([
  ...SERVER_SOURCE_VALUES, 'configuration-policy', 'marketplace-registries'
]);
/** @type {Record<string, Set<string>>} */
const NATIVE_COUNT_FIELDS = {
  campaigns: new Set(['id', 'campaign']),
  repositories: new Set(['id', 'repository'])
};

/** @param {Record<string, unknown> | undefined} state */
function dailyRunCountMetadata(state) {
  const ready = state?.status === 'ready';
  return {
    'aggregate-version': 1,
    'aggregate-time-grain': 'utc-run-start-day',
    availability: ready ? 'available' : 'unavailable',
    completeness: ready && state.coverage === 'admitted'
      && !state.excludedRuns && !state.conflictedRuns ? 'complete' : 'partial',
    'aggregate-coverage': state?.coverage ?? 'absent',
    ...(state?.retentionCutoff ? {
      'aggregate-retention-start': state.retentionCutoff,
      'aggregate-retention-watermark': new Date(Number(state.retentionWatermark)).toISOString()
    } : {}),
    'excluded-runs': Number(state?.excludedRuns ?? 0),
    'conflicted-runs': Number(state?.conflictedRuns ?? 0),
    ...(!ready ? { 'query-diagnostic': 'Daily run count state is absent or backfill is incomplete.' }
      : state.coverage === 'retained-v44'
      ? { 'query-diagnostic': 'Backfill covers retained v44 runs only; previously evicted history is unavailable.' }
      : state.excludedRuns || state.conflictedRuns
      ? { 'query-diagnostic': 'Some runs have missing start/identity facts or unresolved equal-revision conflicts.' } : {})
  };
}
/**
 * @param {Record<string, unknown>} sources
 * @param {string} sourceName
 * @param {string} queryName
 * @param {boolean} available
 * @returns {import('../../presenter.js').SourceMetadata}
 */
function queryMetadata(sources, sourceName, queryName, available) {
  const input = sources[sourceName] && typeof sources[sourceName] === 'object'
    ? /** @type {{ metadata?: Record<string, unknown> }} */ (sources[sourceName])
    : {};
  const metadata = input.metadata ?? {};
  const asOf = typeof metadata['as-of'] === 'string' ? metadata['as-of'] : '';
  return /** @type {import('../../presenter.js').SourceMetadata} */ ({
    ...metadata,
    'source-id': queryName,
    'source-kind': 'database-query',
    'as-of': asOf,
    'retrieved-at': typeof metadata['retrieved-at'] === 'string' ? metadata['retrieved-at'] : asOf,
    availability: available ? 'available' : 'unavailable',
    completeness: available && ['complete', 'partial'].includes(String(metadata.completeness))
      ? metadata.completeness : 'unknown',
    freshness: available && ['fresh', 'stale'].includes(String(metadata.freshness))
      ? metadata.freshness : 'unknown'
  });
}

/** @param {Record<string, unknown>} sources */
function namedSources(sources) {
  return Object.fromEntries(Object.entries(sources).map(([name, source]) => [
    name,
    source && typeof source === 'object' && !Array.isArray(source)
      ? { source: name, ...source }
      : source
  ]));
}

/**
 * @param {string} queryName
 * @param {Record<string, import('../../presenter.js').LogicalSourceInput>} inputs
 * @param {Record<string, unknown>} sources
 * @param {string} metadataSource
 * @param {import('./declarative.js').QueryBudget} [budget]
 */
function executeDatabaseQuery(queryName, inputs, sources, metadataSource, budget) {
  const definition = databaseQueryIndex.get(queryName);
  if (!definition) throw new Error(`Missing database query: ${queryName}`);
  const result = executeDatabaseProjection(definition, inputs, budget);
  const unavailable = result.metadata.availability === 'unavailable';
  return /** @type {import('../../presenter.js').LogicalSourceInput} */ ({
    ...result,
    metadata: unavailable
      ? result.metadata
      : {
          ...(queryName === 'operational-value-evidence'
            ? { ...result.metadata, 'source-id': queryName, 'source-kind': 'database-query' }
            : queryMetadata(DIRECT_EVIDENCE_SOURCES.has(queryName) ? {} : sources, metadataSource, queryName, true)),
          availability: result.rows.length > 0 ? 'available' : 'empty',
          ...(queryName.startsWith('statistical-') && inputs.$statisticalMembership
            ? {
                'as-of': inputs.$statisticalMembership.metadata['as-of'],
                'statistical-frame': inputs.$statisticalMembership.metadata['statistical-frame'],
                'sample-frame': inputs.$statisticalMembership.metadata['statistical-frame']
              } : {})
        }
  });
}

/**
 * Canonical row-local mappings are adapters, not bounded view results. Partition
 * only their primary input; joins and the execution budget remain shared.
 * @param {import('./declarative.js').DashboardQuery} definition
 * @param {Record<string, import('../../presenter.js').LogicalSourceInput>} inputs
 * @param {import('./declarative.js').QueryBudget} [sharedBudget]
 * @returns {import('../../presenter.js').LogicalSourceInput}
 */
function executeDatabaseProjection(definition, inputs, sharedBudget) {
  const input = inputs[definition.from];
  const batchSize = DASHBOARD_QUERY_LIMITS['max-output-rows'];
  const rowLocal = !definition.union?.length
    && !definition.aggregate
    && !definition['temporal-series']
    && !definition.predict?.length
    && !definition.window?.length
    && !definition.facet
    && !definition['order-by']?.length
    && definition.limit === undefined;
  if (!rowLocal || !input || input.rows.length <= batchSize) {
    return executeDashboardQueries([definition], inputs, [definition.name], { budget: sharedBudget })[definition.name];
  }
  const budget = sharedBudget ?? createDashboardQueryBudget();
  /** @type {Record<string, unknown>[]} */
  const rows = [];
  /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */
  let result;
  for (let offset = 0; offset < input.rows.length; offset += batchSize) {
    result = executeDashboardQueries([definition], {
      ...inputs,
      [definition.from]: { ...input, rows: input.rows.slice(offset, offset + batchSize) }
    }, [definition.name], { budget })[definition.name];
    if (result.metadata.availability === 'unavailable') return result;
    for (const row of result.rows) rows.push(row);
  }
  if (!result) throw new Error(`Database projection produced no batches: ${definition.name}`);
  return {
    ...result,
    rows,
    metadata: { ...result.metadata, availability: rows.length > 0 ? 'available' : 'empty' }
  };
}

/**
 * @param {string} sourceName
 * @param {Record<string, unknown>[]} records
 * @param {Record<string, unknown>[]} runs
 * @param {Record<string, unknown>} sources
 * @param {import('./declarative.js').QueryBudget} [budget]
 */
function executeRecordQuery(sourceName, records, runs, sources, budget) {
  return executeDatabaseQuery(sourceName, {
    [`$${sourceName}`]: {
      source: `$${sourceName}`,
      rows: records,
      metadata: queryMetadata(sources, sourceName, sourceName, true)
    },
    $runs: {
      source: '$runs',
      rows: runs,
      metadata: queryMetadata(sources, 'runs', 'runs', true)
    }
  }, sources, sourceName, budget);
}

/**
 * Executes declarative queries with safe IndexedDB pushdown. Whole-table counts
 * use native count, while compatible filters read indexed candidates before
 * final execution by the query engine.
 *
 * @param {IDBFactory} indexedDB
 * @param {Record<string, unknown>} logicalSources
 * @param {unknown} definitions
 * @param {Iterable<string>} requested
 * @param {{ signal?: { aborted?: boolean }, budget?: import('./declarative.js').QueryBudget }} [options]
 * @returns {Promise<Record<string, import('../../presenter.js').LogicalSourceInput>>}
 */
export async function queryIndexedDatabaseSources(indexedDB, logicalSources, definitions, requested, options = {}) {
  const budget = options.budget ?? createDashboardQueryBudget(options);
  budget.checkpoint();
  const index = dashboardQueryIndex(definitions);
  requested = [...requested];
  if (resolveDashboardQuerySources(definitions, requested).includes('daily-run-counts')) {
    const states = await readCollections(indexedDB, ['dailyRunCountState']);
    const metadata = dailyRunCountMetadata(states.dailyRunCountState.find((state) => state.id === DAILY_RUN_COUNT_STATE_ID));
    logicalSources = {
      ...logicalSources,
      dailyRunCounts: { source: 'dailyRunCounts', rows: [], metadata },
      'daily-run-counts': { source: 'daily-run-counts', rows: [], metadata }
    };
  }
  const defects = dashboardQueryDefects(definitions);
  const countPlans = [...requested].flatMap((name) => {
    const definition = index.get(name);
    const plan = definition && !defects.has(name) ? nativeTableCountPlan(definition) : null;
    return plan ? [{ name, ...plan }] : [];
  });
  const nativeCounts = new Set(countPlans.map(({ name }) => name));
  /** @type {Promise<Record<string, Record<string, unknown>[]>> | undefined} */
  let runRead;
  const readRuns = async () => {
    runRead ??= readCollections(indexedDB, ['runs']);
    return (await runRead).runs ?? [];
  };
  /** @type {Map<unknown, Record<string, unknown>> | undefined} */
  let runsById;
  /** @type {Map<string, Promise<Record<string, Record<string, unknown>[]>>>} */
  const joinedReads = new Map();
  /** @type {Record<string, import('../../presenter.js').LogicalSourceInput>} */
  const streamed = {};
  /** @type {Map<string, import('../../presenter.js').LogicalSourceInput>} */
  const aggregateCache = new Map();
  for (const name of requested) {
    if (nativeCounts.has(name)) continue;
    const definition = index.get(name);
    const plan = definition && !defects.has(name) ? streamingAggregatePlan(index, definition) : null;
    if (!definition?.aggregate || !plan) continue;
    const stageNames = new Map(plan.queries.map((query, position) => [query.name, position]));
    const cacheKey = JSON.stringify([
      plan.source,
      plan.queries.map((query) => ({
        ...query, name: undefined, from: stageNames.get(query.from) ?? query.from
      })),
      { ...definition, name: undefined, from: undefined }
    ]);
    const cached = aggregateCache.get(cacheKey);
    if (cached) {
      streamed[name] = {
        ...cached, source: name,
        metadata: { ...cached.metadata, 'source-id': `${name}-query`, 'query-name': name }
      };
      continue;
    }
    const reducer = incrementalAggregate(definition.aggregate, budget);
    const needsRuns = !plan.direct || plan.projection.joins?.some((join) => join.source === '$runs');
    if (needsRuns && !runsById) {
      const runs = await readRuns();
      budget.spend(runs.length);
      runsById = new Map(runs.map((run) => [run.id, run]));
    }
    const runIndex = runsById;
    /** @type {Record<string, import('../../presenter.js').LogicalSourceInput>} */
    const joinedInputs = {};
    for (const join of plan.direct ? (plan.projection.joins ?? []) : []) {
      if (join.source === '$runs') continue;
      const store = /** @type {typeof import('../storage/indexeddb.js').ENTITY_STORES[number]} */ (join.source.slice(1));
      let read = joinedReads.get(store);
      if (!read) {
        read = readCollections(indexedDB, [store]);
        joinedReads.set(store, read);
      }
      const rows = (await read)[store] ?? [];
      budget.checkpoint();
      joinedInputs[join.source] = {
        source: join.source, rows,
        metadata: queryMetadata(logicalSources, store, store, true)
      };
    }
    let metadata = queryMetadata(logicalSources, plan.store, name, true);
    let unavailable = false;
    /** @param {Record<string, unknown>[]} records */
    const consume = (records) => {
      if (unavailable) return;
      budget.spend(records.length);
      const batchRuns = runIndex ? [...new Set(records.map((record) => record.runId))]
        .flatMap((id) => {
          const run = runIndex.get(id);
          return run ? [run] : [];
        }) : [];
      const projected = plan.direct
        ? executeDatabaseQuery(plan.source, {
            ...joinedInputs,
            [`$${plan.store}`]: {
              source: `$${plan.store}`, rows: records,
              metadata: queryMetadata(logicalSources, plan.store, plan.store, true)
            },
            ...(needsRuns ? { $runs: {
              source: '$runs', rows: batchRuns,
              metadata: queryMetadata(logicalSources, 'runs', 'runs', true)
            } } : {})
          }, logicalSources, plan.store, budget)
        : executeDatabaseQuery(plan.source, {
            [plan.projection.from]: executeRecordQuery(plan.store, records, batchRuns, logicalSources, budget)
          }, logicalSources, plan.store, budget);
      const input = executeDashboardQueries(plan.queries, {
        [plan.source]: projected
      }, [name], { budget })[name];
      metadata = input.metadata;
      if (metadata.availability === 'unavailable') { unavailable = true; return; }
      reducer.append(input.rows);
    };
    const missing = [logicalSources[plan.store]].find((source) => (
      source && typeof source === 'object'
      && /** @type {import('../../presenter.js').LogicalSourceInput} */ (source).metadata?.availability === 'unavailable'
    ));
    if (missing) {
      const input = /** @type {import('../../presenter.js').LogicalSourceInput} */ (missing);
      streamed[name] = executeDashboardQueries([definition], { [definition.from]: input }, [name], { budget })[name];
      continue;
    }
    try {
      await scanCanonicalCollection(indexedDB, plan.store, consume, { checkpoint: () => budget.checkpoint() });
    } catch (error) {
      if (error instanceof DashboardQueryCancelledError) throw error;
      streamed[name] = unavailableResult(definition, metadata, error instanceof Error ? error.message : String(error));
      continue;
    }
    const rows = unavailable ? [] : reducer.rows();
    streamed[name] = executeDashboardQueries([{
      ...definition, from: '$native-aggregate', filter: undefined, compute: undefined, aggregate: undefined
    }], { '$native-aggregate': { source: '$native-aggregate', rows, metadata } }, [name], { budget })[name];
    aggregateCache.set(cacheKey, streamed[name]);
  }
  /** @type {Map<string, ReturnType<typeof readCollectionQueryKeys>>} */
  const keyReads = new Map();
  const keySelectionPlans = [...requested].flatMap((name) => {
    const definition = index.get(name);
    if (!definition || defects.has(name)) return [];
    const plan = indexedRecordKeySelectionPlan(index, definition);
    return plan ? [{ name, plan }] : [];
  });
  const keySelections = await Promise.all(keySelectionPlans.map(async ({ name, plan }) => {
    const source = plan.source;
    const unavailable = [logicalSources[source], logicalSources[plan.store]]
      .map((input) => /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */ (input))
      .find((input) => input?.metadata?.availability === 'unavailable');
    if (unavailable) {
      return [name, executeRowLocalRecordGraph(plan.queries, { [source]: unavailable }, name, budget)];
    }
    const keyReadId = JSON.stringify([plan.store, plan.indexName, plan.exactKey]);
    let keyRead = keyReads.get(keyReadId);
    if (!keyRead) {
      keyRead = readCollectionQueryKeys(indexedDB, plan.store, plan.indexName, {
        maxKeys: DASHBOARD_QUERY_LIMITS['max-input-rows'], checkpoint: () => budget.spend(1),
        exactKey: plan.exactKey
      });
      keyReads.set(keyReadId, keyRead);
    }
    const keys = await keyRead;
    if (!keys) return null;
    /** @type {string[]} */
    const selectedKeys = [];
    const batchSize = Math.min(1000, DASHBOARD_QUERY_LIMITS['max-output-rows']);
    for (let offset = 0; offset < keys.length; offset += batchSize) {
      budget.checkpoint();
      const batch = keys.slice(offset, offset + batchSize);
      const projected = executeRecordQuery(plan.store, batch.map(({ row, key }) => ({
        ...row, id: key
      })), [], logicalSources);
      const keyed = {
        ...projected,
        rows: projected.rows.map((row, position) => ({ ...row, [plan.keyField]: batch[position].key }))
      };
      const inputs = plan.projection
        ? executeDashboardQueries([plan.projection], { [plan.projection.from]: keyed }, [source], { budget })[source]
        : keyed;
      const result = executeDashboardQueries(plan.keyQueries, { [source]: inputs }, [name], { budget })[name];
      if (result.metadata.availability === 'unavailable') return [name, result];
      for (const row of result.rows) selectedKeys.push(String(row[plan.keyField]));
    }
    const records = await readCollectionQueryKeyRecords(indexedDB, plan.store, plan.indexName, selectedKeys, {
      maxRows: DASHBOARD_QUERY_LIMITS['max-input-rows'], checkpoint: () => budget.checkpoint()
    });
    const runs = await readRuns();
    const projected = executeRecordQuery(plan.store, records, runs, logicalSources);
    const actual = plan.projection
      ? executeDatabaseQuery(source, { [plan.projection.from]: projected }, logicalSources, plan.store)
      : projected;
    return [name, executeRowLocalRecordGraph(plan.queries, {
      [source]: actual
    }, name, budget)];
  }));
  const recordPlans = [...requested].flatMap((name) => {
    if (Object.hasOwn(streamed, name)) return [];
    const definition = index.get(name);
    const flattened = definition && !defects.has(name)
      ? flattenIndexedRecordQuery(index, definition)
      : null;
    const plan = flattened && !(flattened.joins ?? []).some((join) => index.has(join.source))
      ? indexedRecordAggregatePlan(flattened) : null;
    return plan ? [{ name, definition: flattened, plan }] : [];
  });
  const recordAggregates = await Promise.all(recordPlans.map(async ({ name, definition, plan }) => {
    if (!definition?.aggregate) return null;
    const joinedAliases = new Set((definition.joins ?? []).flatMap((join) => (
      join.fields.map((field) => field.as ?? field.field)
    )));
    let weightField = '_indexed-count';
    while (joinedAliases.has(weightField)) weightField += '-value';
    /** @type {Record<string, import('../../presenter.js').LogicalSourceInput>} */
    const inputs = await queryDatabaseSources(indexedDB, logicalSources, [
      ...(definition.joins ?? []).map((join) => join.source)
    ]);
    const runs = await readRuns();
    for (const source of [definition.from, ...(definition.union ?? [])]) {
      const unavailable = /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */ (logicalSources[source]);
      if (unavailable?.metadata?.availability === 'unavailable') {
        inputs[source] = unavailable;
        continue;
      }
      const groups = await queryCollectionCountGroups(
        indexedDB,
        /** @type {typeof import('../storage/indexeddb.js').ENTITY_STORES[number]} */ (source),
        { ...plan, maxGroups: DASHBOARD_QUERY_LIMITS['max-input-rows'], checkpoint: () => budget.spend(1) }
      );
      if (!groups) return null;
      const projected = executeRecordQuery(source, groups, runs, logicalSources);
      inputs[source] = {
        ...projected,
        rows: projected.rows.map((row, position) => ({ ...row, [weightField]: groups[position].count }))
      };
    }
    const weighted = {
      ...definition,
      aggregate: {
        ...definition.aggregate,
        values: definition.aggregate.values.map((value) => (
          value.field === 'event' || value.field === 'id'
            ? { ...value, field: weightField, reducer: 'sum' }
            : value
        ))
      }
    };
    return [name, executeDashboardQueries([weighted], inputs, [name], { budget })[name]];
  }));
  const runSelectionCandidates = [...requested].flatMap((name) => {
    const definition = index.get(name);
    const plan = definition && !defects.has(name) ? indexedRecordRunSelectionPlan(index, definition) : null;
    return plan ? [{ name, plan }] : [];
  });
  const runSelectedNames = new Set(runSelectionCandidates.map(({ name }) => name));
  const runSelectionPlans = runSelectionCandidates.filter(({ plan }) => (
    !plan.queries.slice(0, -1).some((query) => runSelectedNames.has(query.name))
  ));
  /** @type {Map<string, import('../../presenter.js').LogicalSourceInput>} */
  const runIdentities = new Map();
  /** @type {Map<string, Promise<import('../../presenter.js').LogicalSourceInput>>} */
  const runSelectedReads = new Map();
  const runSelections = await Promise.all(runSelectionPlans.map(async ({ name, plan }) => {
    const unavailable = /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */ (logicalSources[plan.store]);
    if (unavailable?.metadata?.availability === 'unavailable') {
      return [name, executeRowLocalRecordGraph(plan.queries, { [plan.store]: unavailable }, name, budget)];
    }
    const runs = await readRuns();
    const orphan = executeRecordQuery(plan.store, [{ id: '' }], [], logicalSources, budget);
    const orphanSelection = executeDashboardQueries(plan.keyQueries, {
      [plan.store]: { ...orphan, rows: orphan.rows.map((row) => ({ ...row, [plan.keyField]: '' })) }
    }, [name], { budget })[name];
    if (orphanSelection.metadata.availability === 'unavailable' || orphanSelection.rows.length > 0) return null;
    let identities = runIdentities.get(plan.store);
    if (!identities) {
      identities = executeRecordQuery(plan.store, runs.map((run) => ({
        id: run.id, runId: run.id
      })), runs, logicalSources, budget);
      runIdentities.set(plan.store, identities);
    }
    const selected = executeDashboardQueries(plan.keyQueries, {
      [plan.store]: {
        ...identities,
        rows: identities.rows.map((row, position) => ({ ...row, [plan.keyField]: runs[position].id }))
      }
    }, [name], { budget })[name];
    if (selected.metadata.availability === 'unavailable') return [name, selected];
    const runIds = selected.rows.map((row) => row[plan.keyField]);
    const readKey = JSON.stringify([plan.store, runIds]);
    let read = runSelectedReads.get(readKey);
    if (!read) {
      read = (async () => {
        /** @type {Record<string, unknown>[]} */
        const records = [];
        for (let offset = 0; offset < runIds.length; offset += 32) {
          budget.checkpoint();
          const batch = await queryCollection(indexedDB, plan.store, [
            { op: 'filter', predicates: [{ field: 'runId', in: runIds.slice(offset, offset + 32) }] }
          ], { maxRows: DASHBOARD_QUERY_LIMITS['max-input-rows'] - records.length });
          for (const row of batch) records.push(row);
        }
        records.sort((left, right) => indexedDB.cmp(
          /** @type {IDBValidKey} */ (left.id), /** @type {IDBValidKey} */ (right.id)
        ));
        return executeRecordQuery(plan.store, records, runs, logicalSources, budget);
      })();
      runSelectedReads.set(readKey, read);
    }
    return [name, executeRowLocalRecordGraph(plan.queries, { [plan.store]: await read }, name, budget)];
  }));
  const recordSelectionPlans = [...requested].flatMap((name) => {
    if (runSelectedNames.has(name)) return [];
    const definition = index.get(name);
    const flattened = definition && !defects.has(name)
      ? flattenIndexedRecordQuery(index, definition) : null;
    const predicates = flattened?.filter?.predicates?.filter((predicate) => RECORD_RUN_FIELDS.has(predicate.field));
    if (!flattened || flattened.aggregate || flattened['temporal-series'] || flattened.predict?.length || flattened.window?.length
        || !(predicates?.length)
        || [flattened.from, ...(flattened.union ?? [])].some((source) => !RUN_RECORD_STORES.has(source))
        || (flattened.joins ?? []).some((join) => index.has(join.source))
        || predicates.some((predicate) => (
          predicate.optional || predicate.equals === 'unknown' || predicate.in?.includes('unknown')
        ))) return [];
    return [{ name, definition: flattened, predicates }];
  });
  const recordSelections = await Promise.all(recordSelectionPlans.map(async ({ name, definition, predicates }) => {
    const runs = await readRuns();
    const identities = executeRecordQuery(definition.from, runs.map((run) => ({
      id: run.id, runId: run.id
    })), runs, logicalSources);
    const selected = executeDashboardQueries([{
      name: 'indexed-run-selection', from: definition.from,
      filter: { predicates }, select: [{ field: 'event' }]
    }], { [definition.from]: identities }, ['indexed-run-selection'], { budget })['indexed-run-selection'];
    if (selected.metadata.availability === 'unavailable') return [name, selected];
    const runIds = selected.rows.map((row) => row.event);
    /** @type {Record<string, import('../../presenter.js').LogicalSourceInput>} */
    const inputs = await queryDatabaseSources(indexedDB, logicalSources, [
      ...(definition.joins ?? []).map((join) => join.source)
    ]);
    for (const source of [definition.from, ...(definition.union ?? [])]) {
      const unavailable = /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */ (logicalSources[source]);
      if (unavailable?.metadata?.availability === 'unavailable') {
        inputs[source] = unavailable;
        continue;
      }
      /** @type {Record<string, unknown>[]} */
      const records = [];
      for (let offset = 0; offset < runIds.length; offset += 32) {
        budget.checkpoint();
        const batch = await queryCollection(
          indexedDB,
          /** @type {typeof import('../storage/indexeddb.js').ENTITY_STORES[number]} */ (source),
          [{ op: 'filter', predicates: [{ field: 'runId', in: runIds.slice(offset, offset + 32) }] }],
          { maxRows: DASHBOARD_QUERY_LIMITS['max-input-rows'] - records.length }
        );
        for (const row of batch) records.push(row);
      }
      records.sort((left, right) => indexedDB.cmp(
        /** @type {IDBValidKey} */ (left.id), /** @type {IDBValidKey} */ (right.id)
      ));
      inputs[source] = executeRecordQuery(source, records, runs, logicalSources);
    }
    return [name, executeDashboardQueries([definition], inputs, [name], { budget })[name]];
  }));
  const filterPlans = [...requested].flatMap((name) => {
    const definition = index.get(name);
    const executable = definition && !defects.has(name)
      ? flattenIndexedRunQuery(index, definition)
      : null;
    const operators = executable
      ? indexedRunOperators(executable) ?? indexedRunAggregateOperators(executable)
      : null;
    return operators ? [{ name, definition: executable, operators }] : [];
  });
  const stores = [...new Set(countPlans.map(({ source }) => source))];
  debugDatabase({
    event: 'indexed-pushdown-resolved',
    countPlanCount: countPlans.length,
    filterPlanCount: filterPlans.length,
    storeCount: stores.length
  });
  /** @type {Record<string, number>} */
  const counts = stores.length > 0
    ? await countCollections(
        indexedDB,
        /** @type {typeof import('../storage/indexeddb.js').DATABASE_STORES[number][]} */ (stores)
      )
    : {};
  const counted = Object.fromEntries(countPlans.map(({ name, source, values, computedLiterals }) => {
    const input = /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */ (logicalSources[source]);
    if (input?.metadata?.availability === 'unavailable') {
      return [name, unavailableResult(
        /** @type {import('./declarative.js').DashboardQuery} */ (index.get(name)),
        input.metadata, `input source "${source}" is unavailable`, source
      )];
    }
    const metadata = queryMetadata(logicalSources, source, source, true);
    return [name, {
      source: name,
      rows: counts[source] === 0 && Object.keys(computedLiterals).length > 0
        ? []
        : [{
            ...computedLiterals,
            ...Object.fromEntries(values.map((value) => [value.as, counts[source]]))
          }],
      metadata: {
        ...metadata,
        'source-id': `${name}-query`,
        'source-kind': 'derived',
        availability: 'available',
        'query-name': name
      }
    }];
  }));
  const filtered = await Promise.all(filterPlans.map(async ({ name, definition, operators }) => {
    const records = await queryCollection(indexedDB, 'runs', operators);
    const runs = executeDatabaseQuery('runs', {
      $runs: {
        source: '$runs',
        rows: records,
        metadata: queryMetadata(logicalSources, 'runs', 'runs', true)
      },
      $workflows: {
        source: '$workflows',
        rows: [],
        metadata: queryMetadata(logicalSources, 'workflows', 'workflows', true)
      }
    }, logicalSources, 'runs');
    return [name, executeDashboardQueries([definition], { runs }, [name], { budget })[name]];
  }));
  budget.checkpoint();
  return {
    ...counted,
    ...Object.fromEntries(filtered),
    ...Object.fromEntries(recordAggregates.filter((entry) => entry !== null)),
    ...Object.fromEntries(recordSelections),
    ...Object.fromEntries(runSelections.filter((entry) => entry !== null)),
    ...Object.fromEntries(keySelections.filter((entry) => entry !== null)),
    ...streamed
  };
}

/** @param {import('./declarative.js').DashboardQuery} definition */
function nativeTableCountPlan(definition) {
  if (definition.from === STORAGE_SHARD_STORE) return null;
  const table = CANONICAL_DATABASE_SCHEMA[definition.from];
  const values = definition.aggregate?.values;
  const computedLiterals = Object.fromEntries((definition.compute ?? []).flatMap((computed) => (
    computed.function === 'literal' && computed.args.length === 1 && 'value' in computed.args[0]
      ? [[computed.as, computed.args[0].value]] : []
  )));
  const computedFields = Object.keys(computedLiterals);
  const groupedFields = definition.aggregate?.by ?? [];
  const nativeCountFields = NATIVE_COUNT_FIELDS[definition.from] ?? new Set([table?.keyPath]);
  if (!table || (computedFields.length === 0 && typeof table.keyPath !== 'string')
      || definition.union?.length || definition.joins?.length || definition.filter
      || computedFields.length !== (definition.compute?.length ?? 0)
      || groupedFields.length !== computedFields.length
      || groupedFields.some((field) => !Object.hasOwn(computedLiterals, field))
      || definition['temporal-series'] || definition.predict?.length || definition.window?.length || definition.facet
      || definition.select?.length || definition['order-by']?.length || definition.limit !== undefined
      || !values?.length || values.some((value) => (
        value.reducer !== 'count' || (computedFields.length === 0 && !nativeCountFields.has(value.field)) || value.filter
      ))) return null;
  return { source: definition.from, values, computedLiterals };
}

const RECORD_PROJECTIONS = [...RUN_RECORD_STORES].map((name) => databaseQueryIndex.get(name))
  .filter((query) => query !== undefined);

/**
 * Prove a row-local path from one canonical record projection to an aggregate.
 * Ordering, limiting, joins, and other non-local ancestors stay on the regular path.
 * @param {Map<string, import('./declarative.js').DashboardQuery>} index
 * @param {import('./declarative.js').DashboardQuery} definition
 */
function streamingAggregatePlan(index, definition) {
  if (definition.from === 'operational-value-evidence') return null;
  if (!definition.aggregate || definition.union?.length || definition.joins?.length
      || definition['temporal-series'] || definition.predict?.length || definition.window?.length || definition.facet
      || definition.aggregate.values.some((value) => !['count', 'distinct-count', 'sum'].includes(value.reducer))) return null;
  /** @type {import('./declarative.js').DashboardQuery[]} */
  const queries = [{ ...definition, aggregate: undefined, select: undefined, 'order-by': undefined, limit: undefined }];
  const seen = new Set([definition.name]);
  let source = definition.from;
  while (index.has(source)) {
    const parent = index.get(source);
    if (!parent || seen.has(source) || parent.aggregate || parent.union?.length || parent.joins?.length
        || parent['temporal-series'] || parent.predict?.length || parent.window?.length || parent.facet
        || parent['order-by']?.length || parent.limit !== undefined) return null;
    seen.add(source);
    queries.unshift(parent);
    source = parent.from;
  }
  if (RUN_RECORD_STORES.has(source)) return null;
  const projection = databaseQueryIndex.get(source);
  if (!projection) return null;
  if (projection.from === '$operationalValueEvidence') return null;
  const direct = projection.from.startsWith('$') && Object.hasOwn(CANONICAL_DATABASE_SCHEMA, projection.from.slice(1));
  if ((!RUN_RECORD_STORES.has(projection.from) && !direct) || projection.aggregate || projection.union?.length
      || (projection.joins ?? []).some((join) => !direct || !join.source.startsWith('$')
        || !Object.hasOwn(CANONICAL_DATABASE_SCHEMA, join.source.slice(1))
        || join.type !== 'left' || join.on.length !== 1 || join.on[0].right !== 'id')
      || projection['order-by']?.length || projection.limit !== undefined
      || projection['temporal-series'] || projection.predict?.length || projection.window?.length || projection.facet) return null;
  const stores = direct ? [projection.from.slice(1)] : RUN_RECORD_STORES.has(source) ? [source]
    : (projection?.stores ?? []).filter((store) => RUN_RECORD_STORES.has(store));
  if (stores.length !== 1) return null;
  if (direct && projection.from !== `$${stores[0]}`) return null;
  return { source, projection, direct, store: /** @type {typeof import('../storage/indexeddb.js').ENTITY_STORES[number]} */ (stores[0]), queries };
}
const RECORD_PROJECTED_FIELDS = new Set(RECORD_PROJECTIONS.flatMap((query) => (
  (query.select ?? []).map((field) => field.as ?? field.field)
)));
const RECORD_RUN_FIELDS = new Set([
  ...RECORD_PROJECTIONS.flatMap((query) => (
    (query.joins ?? []).flatMap((join) => join.fields.map((field) => field.as ?? field.field))
  )),
  'run'
]);

/** @typedef {Map<string, Set<string> | null>} RecordFieldLineage */
/**
 * @param {RecordFieldLineage} fields
 * @param {import('./declarative.js').DashboardQuery} definition
 */
function computedRecordLineage(fields, definition) {
  const result = new Map(fields);
  for (const computed of definition.compute ?? []) {
    const dependencies = computed.args.filter((arg) => 'field' in arg).map((arg) => (
      result.get(/** @type {{ field: string }} */ (arg).field)
    ));
    result.set(computed.as, dependencies.some((value) => !value)
      ? null : new Set(dependencies.flatMap((value) => [...(value ?? [])])));
  }
  return result;
}

/**
 * @param {RecordFieldLineage} fields
 * @param {import('./declarative.js').DashboardQuery} definition
 */
function selectedRecordLineage(fields, definition) {
  const computed = computedRecordLineage(fields, definition);
  return definition.select
    ? new Map(definition.select.map((field) => [field.as ?? field.field, computed.get(field.field) ?? null]))
    : computed;
}

/**
 * Evaluate run-derived predicates on canonical run identities, then read the
 * matching record partitions byRun. Computed keys use their authored expression,
 * never string parsing or an assumed inverse of concat.
 * @param {Map<string, import('./declarative.js').DashboardQuery>} index
 * @param {import('./declarative.js').DashboardQuery} definition
 */
function indexedRecordRunSelectionPlan(index, definition) {
  /** @type {import('./declarative.js').DashboardQuery[]} */
  const queries = [];
  const seen = new Set();
  let current = definition;
  while (true) {
    if (seen.has(current.name) || current.aggregate || current.joins?.length || current.union?.length
        || current['temporal-series'] || current.predict?.length || current.window?.length || current.facet
        || (queries.length > 0 && (current['order-by']?.length || current.limit !== undefined))) return null;
    seen.add(current.name);
    queries.unshift(current);
    const parent = index.get(current.from);
    if (!parent) break;
    current = parent;
  }
  if (!RUN_RECORD_STORES.has(current.from)) return null;
  const store = /** @type {typeof import('../storage/indexeddb.js').ENTITY_STORES[number]} */ (current.from);
  /** @type {RecordFieldLineage} */
  let fields = new Map([...RECORD_RUN_FIELDS].map((field) => [field, new Set([field])]));
  const used = new Set(queries.flatMap((query) => [
    ...(query.compute ?? []).map((computed) => computed.as),
    ...(query.select ?? []).map((field) => field.as ?? field.field)
  ]));
  let keyField = '_native-run-id';
  while (used.has(keyField)) keyField += '-value';
  let hasRunPredicate = false;
  const keyQueries = queries.map((query) => {
    const predicates = (query.filter?.predicates ?? []).filter((predicate) => {
      const lineage = fields.get(predicate.field);
      if (!lineage || predicate.optional) return false;
      if (lineage.size > 0) hasRunPredicate = true;
      return true;
    });
    fields = selectedRecordLineage(fields, query);
    return {
      ...query,
      filter: predicates.length ? { predicates } : undefined,
      select: query.select ? [...query.select, { field: keyField }] : undefined,
      'order-by': undefined, limit: undefined
    };
  });
  return hasRunPredicate ? { store, queries, keyQueries, keyField } : null;
}

/**
 * Plan raw tuple-key selection by tracing a row-local declarative graph. Never
 * invert computed labels: evaluate their original expressions on index tuples.
 * @param {Map<string, import('./declarative.js').DashboardQuery>} index
 * @param {import('./declarative.js').DashboardQuery} definition
 */
function indexedRecordKeySelectionPlan(index, definition) {
  /** @type {import('./declarative.js').DashboardQuery[]} */
  const queries = [];
  let current = definition;
  const seen = new Set();
  while (true) {
    if (seen.has(current.name) || current.aggregate || current.joins?.length || current.union?.length
        || current['temporal-series'] || current.predict?.length || current.window?.length
        || (queries.length > 0 && (current['order-by']?.length || current.limit !== undefined))) return null;
    seen.add(current.name);
    queries.unshift(current);
    const parent = index.get(current.from);
    if (!parent) break;
    current = parent;
  }
  const source = current.from;
  const projection = RUN_RECORD_STORES.has(source) ? undefined : databaseQueryIndex.get(source);
  const stores = RUN_RECORD_STORES.has(source) ? [source]
    : (projection?.stores ?? []).filter((store) => RUN_RECORD_STORES.has(store));
  if (stores.length !== 1 || (projection && (
    projection.from !== stores[0] || projection.joins?.length || projection.aggregate
    || projection.union?.length || projection['temporal-series'] || projection.predict?.length || projection.window?.length
  ))) return null;
  const store = /** @type {typeof import('../storage/indexeddb.js').ENTITY_STORES[number]} */ (stores[0]);
  const recordProjection = databaseQueryIndex.get(store);
  if (!recordProjection) return null;
  /** @type {Set<string>} */
  const rawFields = new Set([
    ...(recordProjection.select ?? []).map((field) => field.field),
    ...(recordProjection.compute ?? []).flatMap((computed) => computed.args.flatMap((arg) => (
      'field' in arg ? [arg.field] : []
    )))
  ]);
  /** @type {RecordFieldLineage} */
  const canonical = new Map([...rawFields].map((field) => [field, new Set([field])]));
  for (const field of RECORD_RUN_FIELDS) canonical.set(field, null);
  const recordFields = selectedRecordLineage(canonical, recordProjection);
  /** @type {Array<{ definition: import('./declarative.js').DashboardQuery, fields: RecordFieldLineage }>} */
  const stages = [];
  let fields = recordFields;
  if (projection) {
    stages.push({ definition: projection, fields });
    fields = selectedRecordLineage(fields, projection);
  }
  for (const query of queries) {
    stages.push({ definition: query, fields });
    fields = selectedRecordLineage(fields, query);
  }
  const scope = stages[stages.length - 1];
  const candidates = Object.keys(CANONICAL_DATABASE_SCHEMA[store].indexes)
    .filter((name) => Object.hasOwn(CANONICAL_QUERY_INDEX_FIELDS, name))
    .map((indexName) => ({
      indexName, covered: new Set(CANONICAL_QUERY_INDEX_FIELDS[indexName])
    }));
  const candidate = candidates.filter(({ covered }) => scope.definition.filter?.predicates?.some((predicate) => {
    const lineage = scope.fields.get(predicate.field);
    return lineage && [...lineage].every((field) => covered.has(field));
  })).sort((left, right) => {
    const score = (/** @type {Set<string>} */ covered) => stages.reduce((total, stage) => total + (
      stage.definition.filter?.predicates?.filter((predicate) => {
        const fields = stage.fields.get(predicate.field);
        return fields && [...fields].every((field) => covered.has(field));
      }).length ?? 0
    ), 0);
    return score(right.covered) - score(left.covered);
  })[0];
  if (!candidate) return null;
  const used = new Set(stages.flatMap(({ definition: stage }) => [
    ...(stage.compute ?? []).map((computed) => computed.as),
    ...(stage.select ?? []).map((field) => field.as ?? field.field)
  ]));
  let keyField = '_native-index-key';
  while (used.has(keyField)) keyField += '-value';
  const pruned = stages.map(({ definition: stage, fields: lineage }) => {
    const predicates = stage.filter?.predicates?.filter((predicate) => {
      const fields = lineage.get(predicate.field);
      return fields && [...fields].every((field) => candidate.covered.has(field));
    }) ?? [];
    return {
      ...stage,
      filter: predicates.length > 0 ? { predicates } : undefined,
      select: stage.select ? [...stage.select, { field: keyField }] : undefined,
      'order-by': undefined, limit: undefined
    };
  });
  return {
    source, store, indexName: candidate.indexName, keyField, queries,
    exactKey: exactRecordSelectionKey(recordProjection, stages, candidate.covered),
    projection: projection ? pruned[0] : undefined,
    keyQueries: projection ? pruned.slice(1) : pruned
  };
}

/**
 * Only invert unchanged scalar projections, never computed labels. Storage
 * additionally proves that all indexed tuples contain strings before using
 * this shortcut; coercible or structured values retain dictionary evaluation.
 * @param {import('./declarative.js').DashboardQuery} projection
 * @param {Array<{ definition: import('./declarative.js').DashboardQuery }>} stages
 * @param {Set<string>} covered
 */
function exactRecordSelectionKey(projection, stages, covered) {
  if (covered.size !== 1) return undefined;
  const [rawField] = covered;
  const computed = new Set((projection.compute ?? []).map((field) => field.as));
  let fields = new Set((projection.select ?? []).filter((field) => (
    field.field === rawField && !computed.has(field.field)
  )).map((field) => field.as ?? field.field));
  for (const { definition } of stages) {
    for (const field of definition.compute ?? []) fields.delete(field.as);
    const predicate = definition.filter?.predicates?.find((predicate) => (
      fields.has(predicate.field) && !predicate.optional
      && typeof predicate.equals === 'string' && !predicate.in
      && predicate.includes === undefined && predicate.gte === undefined && predicate.lt === undefined
    ));
    if (predicate) return JSON.stringify([predicate.equals]);
    if (definition.select) {
      fields = new Set(definition.select.filter((field) => fields.has(field.field))
        .map((field) => field.as ?? field.field));
    }
  }
  return undefined;
}

/**
 * Fuse only row-local ancestors with their selection, retaining one shared
 * budget. Sorting and limiting still operate once on the complete result.
 * @param {import('./declarative.js').DashboardQuery[]} queries
 * @param {Record<string, import('../../presenter.js').LogicalSourceInput>} inputs
 * @param {string} name
 * @param {import('./declarative.js').QueryBudget} budget
 */
function executeRowLocalRecordGraph(queries, inputs, name, budget) {
  const source = queries[0].from;
  const input = inputs[source];
  const final = queries[queries.length - 1];
  const rowLocal = queries.map((query) => ({ ...query, 'order-by': undefined, limit: undefined }));
  /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */
  let result;
  /** @type {Record<string, unknown>[]} */
  const rows = [];
  const batchSize = Math.min(1000, DASHBOARD_QUERY_LIMITS['max-output-rows']);
  for (let offset = 0; offset < Math.max(1, input.rows.length); offset += batchSize) {
    result = executeDashboardQueries(rowLocal, {
      [source]: { ...input, rows: input.rows.slice(offset, offset + batchSize) }
    }, [name], { budget })[name];
    if (result.metadata.availability === 'unavailable') return result;
    for (const row of result.rows) rows.push(row);
  }
  if (!result) throw new Error('Native row-local selection produced no result');
  return executeDashboardQueries([{
    name, from: '$native-selected-records',
    'order-by': final['order-by'], limit: final.limit
  }], { '$native-selected-records': { ...result, rows } }, [name], { budget })[name];
}

/**
 * Filter-only ancestors can be pushed down without changing the order of
 * joins, computation, or aggregation. Other chains remain canonical.
 * @param {Map<string, import('./declarative.js').DashboardQuery>} index
 * @param {import('./declarative.js').DashboardQuery} definition
 * @param {Set<string>} [seen]
 * @returns {import('./declarative.js').DashboardQuery | null}
 */
function flattenIndexedRecordQuery(index, definition, seen = new Set()) {
  if (RUN_RECORD_STORES.has(definition.from)) return definition;
  if (seen.has(definition.name)) return null;
  seen.add(definition.name);
  const parent = index.get(definition.from);
  if (!parent) return null;
  const flattened = flattenIndexedRecordQuery(index, parent, seen);
  if (!flattened || Object.keys(flattened).some((key) => (
    !['name', 'subject', 'objective', 'acceptance', 'description', 'from', 'filter'].includes(key)
  )) || flattened.filter?.search || definition.filter?.search) return null;
  const predicates = [...(flattened.filter?.predicates ?? []), ...(definition.filter?.predicates ?? [])];
  return {
    ...definition, from: flattened.from,
    ...(predicates.length ? { filter: { predicates } } : {})
  };
}

/**
 * Prove that native index groups are a lossless weighted input to an aggregate.
 * Run-owned identifiers come from the shared canonical projection. Joins retain
 * their declarative uniqueness checks; distinct event counts cannot cross joins.
 * @param {import('./declarative.js').DashboardQuery} definition
 */
function indexedRecordAggregatePlan(definition) {
  const sources = [definition.from, ...(definition.union ?? [])];
  if (sources.some((source) => !RUN_RECORD_STORES.has(source))
      || definition.compute?.length || definition['temporal-series']
      || definition.predict?.length || definition.window?.length || definition.facet || definition.filter?.search) return null;
  const values = definition.aggregate?.values;
  if (!Array.isArray(values) || values.length === 0) return null;
  if (!definition.aggregate?.by?.length && !definition.filter && !definition.union?.length
      && !definition.joins?.length && !definition.select?.length
      && !definition['order-by']?.length && definition.limit === undefined
      && values.every((value) => (
        value.field === CANONICAL_DATABASE_SCHEMA[definition.from].keyPath
        && value.reducer === 'count' && !value.filter
      ))) return null;
  const joinedFields = new Set((definition.joins ?? []).flatMap((join) => (
    join.fields.map((field) => field.as ?? field.field)
  )));
  if ([...joinedFields].some((field) => RECORD_PROJECTED_FIELDS.has(field))) return null;
  const identityValues = values.filter((value) => ['id', 'event'].includes(value.field));
  if (identityValues.length === 0
      || values.some((value) => (
        !['count', 'distinct-count'].includes(value.reducer)
        || (value.reducer === 'count' && !['id', 'event'].includes(value.field))
        || (value.reducer === 'distinct-count' && ['id', 'event'].includes(value.field)
          && (definition.joins?.length || definition.union?.length))
      ))) return null;
  const predicates = [
    ...(definition.filter?.predicates ?? []),
    ...values.flatMap((value) => value.filter?.predicates ?? [])
  ];
  const fields = new Set([
    ...(definition.aggregate?.by ?? []),
    ...predicates.map((predicate) => predicate.field),
    ...(definition.joins ?? []).flatMap((join) => join.on.map((pair) => pair.left)),
    ...values.filter((value) => !['id', 'event'].includes(value.field)).map((value) => value.field)
  ]);
  const recordFields = [...fields].filter((field) => !RECORD_RUN_FIELDS.has(field) && !joinedFields.has(field));
  if (recordFields.length === 0) return { index: 'byRun' };
  if (recordFields.some((field) => (
    !['event-type', 'event-status', 'event-summary'].includes(field)
  )) || sources.some((source) => (
    !CANONICAL_DATABASE_SCHEMA[source].indexes.byTypeStatusRunSummary
  ))) return null;
  // Prefix indexes omit absent type/status keys. Require mandatory selection
  // of both dimensions so omitted records provably cannot match this query.
  const selected = ['event-type', 'event-status'].map((field) => (
    definition.filter?.predicates?.find((predicate) => (
      predicate.field === field && !predicate.optional
      && (typeof predicate.equals === 'string'
        || (Array.isArray(predicate.in) && predicate.in.length > 0
          && predicate.in.every((value) => typeof value === 'string')))
    ))
  ));
  if (selected.some((predicate) => !predicate)) return null;
  // The canonical projection renames firewall types. Do not approximate that
  // computed field with the raw index when those renamed values are selected.
  const types = selected[0]?.in ?? [selected[0]?.equals];
  if (types.some((value) => ['firewall.request.allowed', 'firewall.request.blocked', 'unknown'].includes(String(value)))
      || selected[1]?.equals === 'unknown' || selected[1]?.in?.includes('unknown')) return null;
  return {
    index: 'byTypeStatusRun',
    nullableIndex: 'byTypeStatusRunSummary',
    predicates: selected.filter((predicate) => predicate !== undefined).map((predicate) => ({
      ...predicate,
      field: predicate.field === 'event-type' ? 'type' : 'status'
    }))
  };
}

const RUN_QUERY_FIELDS = new Map([
  ['run-conclusion', 'conclusion'],
  ['started-at', 'startedAt'],
  ['event', 'event']
]);
/** Canonical run key paths the storage layer can resolve through an index. */
const QUERYABLE_RUN_KEY_PATHS = new Set(['conclusion', 'event']);
/**
 * Fields a count aggregate may reduce while reading candidates from storage.
 * Every entry is produced by the `runs` projection without the workflow join,
 * so a pushed-down read can never change what the aggregate counts.
 */
const COUNTABLE_RUN_FIELDS = new Set(['id', 'run', ...RUN_QUERY_FIELDS.keys()]);

/**
 * Flattens query chains whose ancestors add only a run filter. This preserves
 * IndexedDB predicate pushdown for generated base queries while leaving general
 * query composition to the declarative engine.
 *
 * @param {Map<string, Record<string, any>>} index
 * @param {Record<string, any>} definition
 * @param {Set<string>} [seen]
 * @returns {Record<string, any> | null}
 */
function flattenIndexedRunQuery(index, definition, seen = new Set()) {
  if (definition.from === 'runs') return definition;
  if (seen.has(definition.name)) return null;
  const parent = index.get(definition.from);
  if (!parent) return null;
  seen.add(definition.name);
  /** @type {Record<string, any> | null} */
  const flattenedParent = flattenIndexedRunQuery(index, parent, seen);
  if (!flattenedParent) return null;
  const operationalParentKeys = Object.keys(flattenedParent)
    .filter((key) => !['name', 'subject', 'objective', 'acceptance', 'description', 'from', 'filter'].includes(key));
  if (operationalParentKeys.length > 0 || (flattenedParent.filter && definition.filter)) return null;
  return {
    ...definition,
    from: flattenedParent.from,
    ...(flattenedParent.filter ? { filter: flattenedParent.filter } : {})
  };
}

/** @param {Record<string, any>} definition */
function indexedRunOperators(definition) {
  if (definition.from !== 'runs'
      || definition.union?.length
      || definition.joins?.length
      || definition.compute?.length
      || definition.aggregate
      || definition['temporal-series']
      || definition.predict?.length || definition.window?.length || definition.facet
      || definition.select?.length
      || definition.filter?.search) return null;
  const predicates = definition.filter?.predicates;
  if (!Array.isArray(predicates)
      || !predicates.some((predicate) => predicate.field === 'run-conclusion')
      || predicates.some((predicate) => !RUN_QUERY_FIELDS.has(predicate.field))) return null;
  const order = definition['order-by'];
  if (order !== undefined
      && (!Array.isArray(order) || order.some((field) => !RUN_QUERY_FIELDS.has(field.field)))) return null;
  return [
    {
      op: /** @type {const} */ ('filter'),
      predicates: predicates.map((predicate) => ({
        ...predicate,
        field: RUN_QUERY_FIELDS.get(predicate.field)
      }))
    },
    ...(Array.isArray(order) ? [{
      op: /** @type {const} */ ('arrange'),
      by: order.map((field) => ({ ...field, field: RUN_QUERY_FIELDS.get(field.field) }))
    }] : []),
    ...(Number.isInteger(definition.limit) ? [{
      op: /** @type {const} */ ('slice'),
      limit: definition.limit
    }] : [])
  ];
}

/**
 * Plans an IndexedDB candidate read for counting queries whose predicates are
 * all indexable run fields, so declarative count aggregates over `runs` read
 * only matching records instead of the whole collection. The declarative
 * engine still executes the query itself; this only narrows what it reads.
 *
 * @param {Record<string, any>} definition
 */
function indexedRunAggregateOperators(definition) {
  if (definition.from !== 'runs'
      || definition.union?.length
      || definition.joins?.length
      || definition.compute?.length
      || definition['temporal-series']
      || definition.predict?.length || definition.window?.length || definition.facet
      || definition.select?.length
      || definition['order-by']?.length
      || definition.limit !== undefined
      || definition.filter?.search) return null;
  const values = definition.aggregate?.values;
  if (!Array.isArray(values) || values.length === 0) return null;
  const groupedBy = definition.aggregate?.by ?? [];
  if (!Array.isArray(groupedBy) || groupedBy.some((field) => !RUN_QUERY_FIELDS.has(field))) return null;
  const valuePredicates = values.flatMap((value) => {
    if (value?.reducer !== 'count') return [null];
    if (value.field !== undefined && !COUNTABLE_RUN_FIELDS.has(value.field)) return [null];
    const predicates = value.filter?.predicates;
    if (value.filter && (value.filter.search || !Array.isArray(predicates))) return [null];
    return predicates ?? [];
  });
  if (valuePredicates.some((predicate) => (
    !predicate || !RUN_QUERY_FIELDS.has(predicate.field)
  ))) return null;
  const predicates = definition.filter?.predicates;
  if (!Array.isArray(predicates)
      || predicates.length === 0
      || predicates.some((predicate) => !RUN_QUERY_FIELDS.has(predicate.field))
      || !predicates.some((predicate) => (
        QUERYABLE_RUN_KEY_PATHS.has(String(RUN_QUERY_FIELDS.get(predicate.field)))
      ))) return null;
  return [{
    op: /** @type {const} */ ('filter'),
    predicates: predicates.map((predicate) => ({
      ...predicate,
      field: RUN_QUERY_FIELDS.get(predicate.field)
    }))
  }];
}

/** @param {string} name */
function queryStores(name) {
  const configured = databaseQueryIndex.get(name)?.stores;
  return Array.isArray(configured)
    ? configured.filter((store) => typeof store === 'string')
    : [];
}

/**
 * Loads only database stores needed by requested JSON query definitions.
 *
 * @param {IDBFactory} indexedDB
 * @param {Record<string, unknown>} logicalSources
 * @param {string[]} sourceNames
 * @param {{ onMetrics?: (metrics: { databaseMs: number, projectionMs: number, totalMs: number, recordsRead: number, stores: string[] }) => void, checkpoint?: () => void, statisticalCapacity?: number, evidenceSelection?: ReturnType<typeof import('./evidence-selection.js').evidencePacketSelection>, measurementSelection?: ReturnType<typeof measurementSelection> }} [options]
 */
export async function queryDatabaseSources(indexedDB, logicalSources, sourceNames, options = {}) {
  const startedAt = monotonicNow();
  if (!Array.isArray(sourceNames) || sourceNames.some((name) => typeof name !== 'string')) {
    throw new TypeError('Dashboard source names must be an array of strings.');
  }
  const requested = new Set(sourceNames);
  const databaseRequested = resolveDashboardQuerySources(databaseQueries, requested)
    .filter((name) => databaseQueryIndex.has(name) && !Object.hasOwn(SYNTHETIC_SOURCE_FIELDS, name));
  const statisticalNames = new Set(databaseRequested.filter((name) => name.startsWith('statistical-')));
  const statisticalRequested = statisticalNames.size > 0;
  const exactStores = new Set(resolveDashboardQuerySources(databaseQueries, requested, statisticalNames)
    .filter((name) => !statisticalNames.has(name)).flatMap(queryStores));
  let statisticalError = '';
  if (statisticalRequested) {
    options.checkpoint?.();
    const counts = await countCollections(indexedDB, ['runs']);
    if (counts.runs > STATISTICAL_INPUT_LIMITS.rows) {
      statisticalError = 'Statistical candidate frame exceeds input row budget';
      debugDatabase({ event: 'statistical-source', status: 'unavailable', reason: 'input-budget', inputCount: counts.runs });
    }
  }
  const unavailableStatistical = new Set(statisticalError
    ? statisticalNames : []);
  const activeQueries = statisticalError
    ? resolveDashboardQuerySources(databaseQueries, requested, unavailableStatistical)
      .filter((name) => !unavailableStatistical.has(name)) : databaseRequested;
  const stores = [...new Set(activeQueries.flatMap(queryStores))];
  const transactionRequested = stores.includes('transactions');
  const collectionStores = /** @type {Array<typeof import('../storage/indexeddb.js').DATABASE_STORES[number]>} */ (
    stores.filter((name) => name !== 'transactions' && name !== 'operationalValueEvidence'
      && !(name === 'operationalValues' && databaseRequested.includes('workflow-value-measurements')))
  );
  if (stores.includes('dailyRunCounts')) collectionStores.push('dailyRunCountState');
  const databaseStartedAt = monotonicNow();
  /** @type {[Record<string, Record<string, unknown>[]>, Record<string, unknown>[]]} */
  const [collections, transactions] = await Promise.all([
    collectionStores.length > 0 ? readCollections(indexedDB, collectionStores, {
      checkpoint: options.checkpoint,
      ...(statisticalRequested ? { bounds: {
        stores: new Set(collectionStores.filter((store) => !exactStores.has(store))),
        rows: STATISTICAL_INPUT_LIMITS.rows, bytes: STATISTICAL_INPUT_LIMITS.bytes,
        onLimit: () => {
          statisticalError = 'Statistical source exceeds input budget';
          for (const name of statisticalNames) unavailableStatistical.add(name);
          debugDatabase({ event: 'statistical-source', status: 'unavailable', reason: 'input-budget' });
        }
      } } : {})
    }) : {},
    transactionRequested ? readTransactions(indexedDB) : []
  ]);
  if (stores.includes('operationalValueEvidence')) {
    collections.operationalValueEvidence = await readEvidencePackets(indexedDB, options.evidenceSelection ?? null, options.checkpoint);
  }
  if (stores.includes('operationalValues') && databaseRequested.includes('workflow-value-measurements')) {
    collections.operationalValues = await readMeasurements(indexedDB, options.measurementSelection ?? null, options.checkpoint);
  }
  if (collections.operationalValues) collections.operationalValues = measurementRows(collections.operationalValues);
  const databaseMs = monotonicNow() - databaseStartedAt;
  const sources = namedSources(logicalSources);
  /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */
  let statisticalMembership;
  if (statisticalRequested && !statisticalError) {
    const rawRuns = /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */ (sources.runs);
    const runMetadata = queryMetadata(sources, 'runs', 'runs', rawRuns?.metadata?.availability !== 'unavailable');
    try {
      const sample = await sampleStatisticalRuns(collections.runs ?? [], {
        capacity: options.statisticalCapacity,
        watermark: runMetadata['as-of'] || canonicalStatisticalWatermark(collections.runs ?? []),
        complete: runMetadata.completeness === 'complete'
          && runMetadata.availability !== 'unavailable',
        checkpoint: options.checkpoint
      });
      sample.frame['watermark-policy'] = runMetadata['as-of'] ? 'recorded-run-as-of' : 'canonical-run-max';
      statisticalMembership = {
        source: '$statisticalMembership',
        rows: sample.rows.map((row) => ({
          id: row.id, organization: row.owner, repository: row.repository, run: String(row.githubRunId),
          ...Object.fromEntries(Object.entries(row).filter(([field]) => field.startsWith('statistical-')))
        })),
        metadata: { ...runMetadata, 'as-of': sample.frame.watermark, 'statistical-frame': sample.frame }
      };
    } catch (error) {
      if (error instanceof DashboardQueryCancelledError) throw error;
      statisticalMembership = {
        source: '$statisticalMembership', rows: [],
        metadata: { ...runMetadata, availability: 'unavailable', error: String(error) }
      };
    }
  }
  /** @type {Record<string, import('../../presenter.js').LogicalSourceInput>} */
  const result = {};
  /** @type {Map<string, import('../../presenter.js').LogicalSourceInput>} */
  const resolved = new Map();
  const visiting = new Set();
  /** @param {string} name @returns {import('../../presenter.js').LogicalSourceInput} */
  const resolveSource = (name) => {
    const cached = resolved.get(name);
    if (cached) return cached;
    if (unavailableStatistical.has(name)) {
      return {
        source: name, rows: [],
        metadata: { ...queryMetadata(sources, 'runs', name, false), error: statisticalError }
      };
    }
    if (name === SIMULATION_DAYS) {
      return simulationDaysSource();
    }
    const logical = /** @type {import('../../presenter.js').LogicalSourceInput | undefined} */ (sources[name]);
    const definition = databaseQueryIndex.get(name);
    if (!definition) {
      if (logical && (EXTERNAL_SOURCES.has(name) || isGitHubQuerySource(name))) return logical;
      return {
        source: name,
        rows: [],
        metadata: {
          ...queryMetadata(sources, name, name, false),
          error: `No canonical database query or runtime source is registered for "${name}".`
        }
      };
    }
    if (visiting.has(name)) throw new Error(`Cyclic canonical database query: ${name}`);
    if (name === 'daily-run-counts') {
      const state = collections.dailyRunCountState?.find((record) => record.id === DAILY_RUN_COUNT_STATE_ID);
      const metadata = dailyRunCountMetadata(state);
      if (metadata.availability === 'unavailable') {
        return unavailableResult(definition, /** @type {import('../../presenter.js').SourceMetadata} */ ({
          ...queryMetadata(sources, name, name, false), ...metadata
        }),
          String(metadata['query-diagnostic']));
      }
      sources.dailyRunCounts = { metadata };
    }
    visiting.add(name);
    const inputs = Object.fromEntries(queryInputNames(definition).map((input) => {
      if (!input.startsWith('$')) return [input, resolveSource(input)];
      if (input === '$statisticalMembership' && statisticalMembership) return [input, statisticalMembership];
      const store = input.slice(1);
      if (!Object.hasOwn(collections, store) && store !== 'transactions') {
        throw new Error(`Canonical query "${name}" requires an undeclared store: ${store}`);
      }
      return [input, {
        source: input,
        rows: store === 'transactions' ? transactions
          : store === 'operationalValueEvidence' ? operationalValueEvidenceRows(collections[store]) : collections[store],
        metadata: store === 'operationalValueEvidence'
          ? { ...queryMetadata(sources, store, store, true), ...operationalValueEvidenceMetadata(collections[store]),
              'evidence-packets-read': collections[store].length,
              'evidence-scoped-read': Boolean(options.evidenceSelection?.length),
              'evidence-facts-materialized': collections[store].reduce((count, packet) => count
                + (Array.isArray(packet.facts) ? packet.facts.length : 0), 0) }
          : queryMetadata(sources, store, store, true)
      }];
    }));
    const projection = executeDatabaseQuery(name, inputs, sources, definition.from.replace(/^\$/, ''));
    resolved.set(name, projection);
    visiting.delete(name);
    return projection;
  };
  for (const name of requested) result[name] = resolveSource(name);
  const totalMs = monotonicNow() - startedAt;
  const recordsRead = Object.values(collections).reduce((total, records) => total + records.length, 0)
    + transactions.length;
  debugDatabase({
    event: 'query-sources-resolved',
    requestedCount: requested.size,
    databaseMs: Math.round(databaseMs),
    totalMs: Math.round(totalMs),
    recordsRead,
    storeCount: stores.length
  });
  options.onMetrics?.({
    databaseMs,
    projectionMs: Math.max(0, totalMs - databaseMs),
    totalMs,
    recordsRead,
    stores
  });
  return result;
}

/**
 * @param {IDBFactory} indexedDB
 * @param {Record<string, unknown>} sources
 * @param {{ ingest?: boolean, storage?: StorageManager, sourceNames?: string[], queries?: unknown[], signal?: AbortSignal, budget?: import('./declarative.js').QueryBudget, statisticalCapacity?: number }} [options]
 */
export async function loadDatabaseQuerySources(indexedDB, sources, options = {}) {
  if (options.ingest) await ingestDashboardSources(indexedDB, sources, { storage: options.storage, signal: options.signal });
  debugDatabase({ event: 'load-database-query-sources', ingested: Boolean(options.ingest), sourceCount: Object.keys(sources).length });
  const sourceNames = Array.isArray(options.sourceNames) ? options.sourceNames : Object.keys(sources);
  const queries = options.queries ?? [];
  const budget = options.budget ?? createDashboardQueryBudget({ signal: options.signal });
  const native = await queryIndexedDatabaseSources(
    indexedDB, sources, queries, resolveDashboardQuerySources(queries, sourceNames), { budget }
  );
  const nativeNames = new Set(Object.keys(native));
  const remaining = sourceNames.filter((name) => !nativeNames.has(name));
  const declared = dashboardQueryIndex(queries);
  const required = resolveDashboardQuerySources(queries, remaining, nativeNames)
    .filter((name) => !nativeNames.has(name) && !declared.has(name));
  const database = await queryDatabaseSources(indexedDB, sources, required, {
    checkpoint: () => budget.checkpoint(), statisticalCapacity: options.statisticalCapacity,
    evidenceSelection: evidencePacketSelection(queries, remaining),
    measurementSelection: measurementSelection(queries, remaining)
  });
  return {
    ...Object.fromEntries(sourceNames.filter((name) => nativeNames.has(name) || !declared.has(name)).map((name) => [
      name, native[name] ?? database[name]
    ])),
    ...executeDashboardQueries(queries, { ...database, ...native }, remaining, { budget })
  };
}
