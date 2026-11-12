import databaseQueries from '../site/src/data/queries/database.json' with { type: 'json' };
import { dashboardQueryOutputFields } from '../site/src/data/queries/declarative.js';
import { executeCanonicalQuery } from './canonical-query.mjs';

const DATABASE_SOURCE_TABLES = databaseSourceTables(databaseQueries);
const DATABASE_SOURCE_FIELDS = databaseSourceFields(databaseQueries);
const LARGE_MATERIALIZED_OUTPUT_FIELD_UNITS = 256;

/**
 * Analyze the static row-read complexity of every Dashboard Language query.
 *
 * @param {unknown} document
 * @param {{ tableCounts?: Record<string, number> }} [options]
 */
export function analyzeDashboardComplexity(document, { tableCounts } = {}) {
  if (!isRecord(document) || !isRecord(document.dashboard)) {
    throw new Error('Dashboard document must contain a dashboard object');
  }
  const definitions = (Array.isArray(document.dashboard.queries) ? document.dashboard.queries : [])
    .filter((query) => isRecord(query) && typeof query.name === 'string');
  const consumers = dashboardQueryConsumers(document.dashboard, definitions);
  const estimates = queryComplexityEstimates(
    new Map(definitions.map((query) => [query.name, query])),
    normalizedTableWeights(tableCounts)
  );
  const ranking = estimates.summary['computation-pressure'].map((query) => ({
    ...query,
    'used-by': consumers.get(query.name) ?? []
  }));
  return {
    queries: definitions.length,
    summary: estimates.summary,
    ranking,
    inventory: definitions.map((query) => ({
      name: query.name,
      rank: estimates.ranks.get(query.name),
      'used-by': consumers.get(query.name) ?? [],
      ...estimates.queries.get(query.name)
    }))
  };
}

/**
 * Render a complexity report for a pull request or terminal.
 *
 * @param {ReturnType<typeof analyzeDashboardComplexity>} analysis
 * @param {{ limit?: number, queryId?: string }} [options]
 */
export function formatDashboardComplexityMarkdown(analysis, { limit, queryId } = {}) {
  const selectedRanking = queryId === undefined
    ? analysis.ranking
    : analysis.ranking.filter((query) => query.name === queryId);
  const ranking = limit === undefined ? selectedRanking : selectedRanking.slice(0, limit);
  const sourceCoefficients = Object.entries(analysis.summary['source-coefficients'])
    .toSorted((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]));
  const rows = ranking.map((query) => [
    query.rank,
    markdownCode(query.name),
    formatConsumers(query['used-by']),
    query['total-row-read-units'],
    query['direct-row-read-units'],
    query['dependency-row-read-units'],
    query['output-field-count'] ?? 'unknown',
    query['total-materialized-field-units'] ?? 'unknown',
    query.warnings.length > 0 ? query.warnings.join('<br>') : '—',
    query.class === 'linear-row-reads-with-n-log-n-sort' ? 'linear + sort' : 'linear'
  ].join(' | '));
  const sources = sourceCoefficients.length === 0
    ? 'none'
    : sourceCoefficients.map(([source, coefficient]) => (
        `${markdownCode(source)} ${formatCoefficient(coefficient)}`
      )).join(', ');
  const tableCounts = Object.entries(analysis.summary['database-table-row-counts'] ?? {})
    .toSorted(([left], [right]) => left.localeCompare(right));
  return [
    '### Dashboard query complexity',
    '',
    `Estimated **${analysis.summary['materialize-all-row-read-units']} normalized row-read units** to materialize all ${analysis.queries} queries once with shared dependencies reused.`,
    `Estimated **${analysis.summary['materialize-all-field-units']} normalized materialized field-units** across the same graph; this relative estimate uses output row counts and inferred field counts, not field-value byte sizes.`,
    ...(queryId === undefined ? [] : ['', `Selected query: ${markdownCode(queryId)}.`]),
    '',
    `Database table coefficients: ${sources}.`,
    ...(tableCounts.length === 0
      ? []
      : [
          '',
          `Deployed table rows: ${tableCounts.map(([table, count]) => (
            `${markdownCode(table)} ${Number(count).toLocaleString('en-US')}`
          )).join(', ')}.`
        ]),
    '',
    '| Rank | Query | Used by | Total | Direct | Dependencies | Output fields | Materialized field units | Warnings | Complexity |',
    '| ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | --- | --- |',
    ...rows.map((row) => `| ${row} |`),
    ...(queryId === undefined && ranking.length < analysis.ranking.length
      ? ['', `_Showing ${ranking.length} of ${analysis.ranking.length} queries._`]
      : []),
    '',
    `Model: ${analysis.summary.model}. ${analysis.summary.assumptions}`
  ].join('\n');
}

/**
 * @param {Record<string, any>} dashboard
 * @param {Record<string, any>[]} definitions
 */
function dashboardQueryConsumers(dashboard, definitions) {
  const consumers = new Map(definitions.map((query) => [query.name, new Set()]));
  for (const query of definitions) {
    for (const dependency of queryInputNames(query)) {
      consumers.get(dependency)?.add(`query:${query.name}`);
    }
  }
  for (const view of Array.isArray(dashboard.views) ? dashboard.views : []) {
    if (!isRecord(view)) continue;
    addViewConsumers(
      view,
      `view:${typeof view.id === 'string' ? view.id : 'anonymous'}`,
      consumers
    );
  }
  for (const [pageIndex, page] of (Array.isArray(dashboard.pages) ? dashboard.pages : []).entries()) {
    if (!isRecord(page)) continue;
    const pageId = typeof page.id === 'string' ? page.id : String(pageIndex);
    const definition = page.kind === 'built-in' && isRecord(page.definition) ? page.definition : page;
    for (const [viewIndex, view] of (Array.isArray(definition.views) ? definition.views : []).entries()) {
      if (!isRecord(view)) continue;
      addViewConsumers(
        view,
        `page:${pageId}/view:${typeof view.id === 'string' ? view.id : viewIndex}`,
        consumers
      );
    }
    for (const [sectionIndex, section] of (Array.isArray(definition.sections) ? definition.sections : []).entries()) {
      if (!isRecord(section)) continue;
      for (const name of [
        section['count-source'],
        ...(Array.isArray(section['count-sources']) ? section['count-sources'] : [])
      ]) {
        if (typeof name === 'string') consumers.get(name)?.add(`page:${pageId}/section:${sectionIndex}`);
      }
    }
  }
  for (const [index, callout] of (Array.isArray(dashboard.callouts) ? dashboard.callouts : []).entries()) {
    const source = isRecord(callout) && isRecord(callout['visible-when'])
      ? callout['visible-when'].source
      : undefined;
    if (typeof source === 'string') consumers.get(source)?.add(`callout:${index}`);
  }
  return new Map([...consumers].map(([name, labels]) => [name, [...labels].toSorted()]));
}

/** @param {Record<string, any>} view @param {string} label @param {Map<string, Set<string>>} consumers */
function addViewConsumers(view, label, consumers) {
  for (const name of viewQueryNames(view)) consumers.get(name)?.add(label);
}

/** @param {Record<string, any>} view */
function viewQueryNames(view) {
  const names = [];
  if (isRecord(view.data)) {
    if (typeof view.data.source === 'string') names.push(view.data.source);
    if (Array.isArray(view.data.sources)) {
      names.push(...view.data.sources.filter((name) => typeof name === 'string'));
    }
  }
  if (isRecord(view.list) && isRecord(view.list.drill) && typeof view.list.drill.query === 'string') {
    names.push(view.list.drill.query);
  }
  return names;
}

/**
 * Static upper-bound row-read and output-size model matching the declarative executor:
 * - every database table is weighted by its deployed row count relative to the
 *   largest table, or one when deployed counts are unavailable;
 * - filters, joins, aggregates, and limits do not reduce the upper-bound row count;
 * - joins cannot expand the left side because duplicate right keys fail closed;
 * - dependencies are materialized once and reused within one execution batch.
 * - output field counts use the database query projection and declarative query schema.
 *
 * @param {Map<string, Record<string, any>>} index
 * @param {{ weights: Map<string, number>, counts: Record<string, number> | undefined }} tableWeights
 */
function queryComplexityEstimates(index, tableWeights) {
  const direct = new Map();
  const visiting = new Set();

  /** @param {string} source */
  const sourceOutput = (source) => {
    if (!index.has(source)) {
      const table = DATABASE_SOURCE_TABLES.get(source);
      return table === undefined
        ? new Map()
        : new Map([[table, tableWeights.weights.get(table) ?? 1]]);
    }
    return estimate(source).output;
  };
  /** @param {string} name */
  const estimate = (name) => {
    if (direct.has(name)) return direct.get(name);
    if (visiting.has(name)) return emptyComplexityEstimate();
    const query = index.get(name);
    if (!query) return emptyComplexityEstimate();
    visiting.add(name);

    const stageReads = {};
    let output = new Map();
    for (const source of [query.from, ...(Array.isArray(query.union) ? query.union : [])]) {
      if (typeof source === 'string') output = addCoefficients(output, sourceOutput(source));
    }
    stageReads.from = coefficientsObject(output);
    let reads = new Map(output);

    for (const join of Array.isArray(query.joins) ? query.joins : []) {
      if (!isRecord(join) || typeof join.source !== 'string') continue;
      const joined = sourceOutput(join.source);
      const joinReads = addCoefficients(output, joined);
      stageReads[`join:${join.source}`] = coefficientsObject(joinReads);
      reads = addCoefficients(reads, joinReads);
    }
    for (const [stage, cost] of queryOperatorCosts(query)) {
      const operatorReads = scaleCoefficients(output, cost);
      stageReads[stage] = coefficientsObject(operatorReads);
      reads = addCoefficients(reads, operatorReads);
    }

    visiting.delete(name);
    const fields = dashboardQueryOutputFields(query, (source) => (
      index.has(source) ? estimate(source).fields : DATABASE_SOURCE_FIELDS.get(source)
    ));
    const outputFieldCount = fields?.length;
    const materializedFieldUnits = outputFieldCount === undefined
      ? undefined
      : normalizedCoefficient(coefficientTotal(output) * outputFieldCount);
    const value = {
      reads,
      output,
      fields,
      outputFieldCount,
      materializedFieldUnits,
      'stage-reads': stageReads,
      class: query['order-by'] ? 'linear-row-reads-with-n-log-n-sort' : 'linear-row-reads'
    };
    direct.set(name, value);
    return value;
  };
  for (const name of index.keys()) estimate(name);

  const queries = new Map();
  for (const [name, query] of index) {
    const dependencies = transitiveQueryDependencies(query, index);
    let total = new Map(direct.get(name)?.reads ?? []);
    for (const dependency of dependencies) {
      total = addCoefficients(total, direct.get(dependency)?.reads ?? new Map());
    }
    const own = direct.get(name) ?? emptyComplexityEstimate();
    const directRowReads = normalizedCoefficient(coefficientTotal(own.reads));
    const totalRowReads = normalizedCoefficient(coefficientTotal(total));
    const dependencyFieldUnits = [...dependencies].reduce((sum, dependency) => (
      sum + (direct.get(dependency)?.materializedFieldUnits ?? 0)
    ), 0);
    const totalMaterializedFieldUnits = own.materializedFieldUnits === undefined
      ? undefined
      : normalizedCoefficient(own.materializedFieldUnits + dependencyFieldUnits);
    const warnings = totalMaterializedFieldUnits !== undefined
        && totalMaterializedFieldUnits >= LARGE_MATERIALIZED_OUTPUT_FIELD_UNITS
      ? [`Potentially large materialized output: ${totalMaterializedFieldUnits} normalized field-units (threshold ${LARGE_MATERIALIZED_OUTPUT_FIELD_UNITS}); project or aggregate before materializing wide rows.`]
      : [];
    queries.set(name, {
      model: tableWeights.counts ? 'deployment-weighted-upper-bound' : 'normalized-upper-bound',
      assumptions: tableWeights.counts
        ? 'Database tables are weighted by deployed row counts normalized to the largest table; selectivity is 1; query dependencies materialize once per batch.'
        : 'Each database table has weight 1; selectivity is 1; query dependencies materialize once per batch.',
      class: own.class,
      'output-field-count': own.outputFieldCount,
      'direct-row-read-units': directRowReads,
      'dependency-row-read-units': normalizedCoefficient(totalRowReads - directRowReads),
      'total-row-read-units': totalRowReads,
      'output-row-units': normalizedCoefficient(coefficientTotal(own.output)),
      'direct-materialized-field-units': own.materializedFieldUnits,
      'dependency-materialized-field-units': normalizedCoefficient(dependencyFieldUnits),
      'total-materialized-field-units': totalMaterializedFieldUnits,
      warnings,
      'source-coefficients': coefficientsObject(total),
      'direct-source-coefficients': coefficientsObject(own.reads),
      'stage-row-reads': own['stage-reads']
    });
  }

  let graphReads = new Map();
  const stageTotals = {};
  for (const value of direct.values()) {
    graphReads = addCoefficients(graphReads, value.reads);
    for (const [stage, coefficients] of Object.entries(value['stage-reads'])) {
      stageTotals[stage] = (stageTotals[stage] ?? 0)
        + coefficientTotal(new Map(Object.entries(coefficients)));
    }
  }
  const ranked = [...queries].toSorted((left, right) => (
    score(right[1]) - score(left[1])
    || right[1]['total-row-read-units'] - left[1]['total-row-read-units']
    || right[1]['direct-row-read-units'] - left[1]['direct-row-read-units']
    || left[0].localeCompare(right[0])
  ));
  const ranks = new Map(ranked.map(([name], index_) => [name, index_ + 1]));
  const pressure = ranked.map(([name, value], index_) => ({
    rank: index_ + 1,
    name,
    score: score(value),
    'total-row-read-units': value['total-row-read-units'],
    'direct-row-read-units': value['direct-row-read-units'],
    'dependency-row-read-units': value['dependency-row-read-units'],
    'output-field-count': value['output-field-count'],
    'total-materialized-field-units': value['total-materialized-field-units'],
    warnings: value.warnings,
    class: value.class
  }));
  return {
    queries,
    ranks,
    summary: {
      model: tableWeights.counts ? 'deployment-weighted-upper-bound' : 'normalized-upper-bound',
      assumptions: tableWeights.counts
        ? 'Database tables are weighted by deployed row counts normalized to the largest table; selectivity is 1; all queries materialize once with shared dependencies reused.'
        : 'Each database table has weight 1; selectivity is 1; all queries materialize once with shared dependencies reused.',
      'materialize-all-row-read-units': normalizedCoefficient(coefficientTotal(graphReads)),
      'source-coefficients': coefficientsObject(graphReads),
      ...(tableWeights.counts ? { 'database-table-row-counts': tableWeights.counts } : {}),
      'stage-row-read-units': Object.fromEntries(
        Object.entries(stageTotals)
          .map(([stage, total]) => [stage, normalizedCoefficient(total)])
          .toSorted(([left], [right]) => left.localeCompare(right))
      ),
      'materialize-all-field-units': normalizedCoefficient([...direct.values()]
        .reduce((total, value) => total + (value.materializedFieldUnits ?? 0), 0)),
      'memory-pressure-definition': `Estimated output row units × inferred output fields; this is a relative field-unit estimate, not bytes, and does not model individual field value sizes. A warning is emitted at ${LARGE_MATERIALIZED_OUTPUT_FIELD_UNITS} field-units.`,
      'computation-pressure-definition': 'Candidate score is the maximum of dependency-amortized row-read units and materialized field-units; dependencies materialize once.',
      'computation-pressure': pressure
    }
  };
}

function emptyComplexityEstimate() {
  return {
    reads: new Map(),
    output: new Map(),
    fields: undefined,
    outputFieldCount: undefined,
    materializedFieldUnits: undefined,
    'stage-reads': {},
    class: 'linear-row-reads'
  };
}

/** @param {Record<string, any>} value */
function score(value) {
  return Math.max(
    value['total-row-read-units'] ?? 0,
    value['total-materialized-field-units'] ?? 0
  );
}

/** @param {Record<string, any>} query */
function queryOperatorCosts(query) {
  const costs = [];
  if (Array.isArray(query.filter?.predicates) && query.filter.predicates.length > 0) costs.push(['filter', 1]);
  if (Array.isArray(query.compute) && query.compute.length > 0) costs.push(['compute', 1]);
  if (query['temporal-series'] !== undefined) costs.push(['temporal-series', 1]);
  if (isRecord(query.aggregate) && Array.isArray(query.aggregate.values)) {
    const aggregateCost = 1 + query.aggregate.values.reduce((cost, value) => (
      cost + (isRecord(value) && isRecord(value.filter) && Array.isArray(value.filter.predicates)
        ? 1 + value.filter.predicates.reduce((total, predicate) => (
            total + 1 + (isRecord(predicate) && Array.isArray(predicate.in) ? predicate.in.length : 0)
          ), 0)
        : 0)
    ), 0);
    costs.push(['aggregate', aggregateCost]);
  }
  if (Array.isArray(query.predict) && query.predict.length > 0) {
    const predictionCost = query.predict.reduce((cost, prediction) => {
      if (!isRecord(prediction)) return cost;
      const predictors = Array.isArray(prediction.on) ? prediction.on.length : 1;
      const terms = prediction.method === 'quad'
        ? 3
        : prediction.method === 'poly' ? Number(prediction.order ?? 3) + 1 : predictors + 1;
      return cost + terms * terms;
    }, 0);
    costs.push(['predict', predictionCost]);
  }
  if (Array.isArray(query.select) && query.select.length > 0) costs.push(['select', 1]);
  if (Array.isArray(query['order-by']) && query['order-by'].length > 0) costs.push(['order-by', 1]);
  if (typeof query.limit === 'number') costs.push(['limit', 1]);
  return costs;
}

/** @param {Record<string, any>} query @param {Map<string, Record<string, any>>} index */
function transitiveQueryDependencies(query, index) {
  const resolved = new Set();
  const pending = queryInputNames(query).filter((name) => index.has(name));
  while (pending.length > 0) {
    const name = pending.pop();
    if (resolved.has(name)) continue;
    resolved.add(name);
    const dependency = index.get(name);
    if (dependency) pending.push(...queryInputNames(dependency).filter((input) => index.has(input)));
  }
  return resolved;
}

/** @param {Record<string, any>} query */
function queryInputNames(query) {
  const names = [];
  if (typeof query.from === 'string') names.push(query.from);
  if (Array.isArray(query.union)) {
    names.push(...query.union.filter((name) => typeof name === 'string'));
  }
  if (Array.isArray(query.joins)) {
    names.push(...query.joins
      .filter((join) => isRecord(join) && typeof join.source === 'string')
      .map((join) => join.source));
  }
  return [...new Set(names)];
}

/** @param {Map<string, number>} left @param {Map<string, number>} right */
function addCoefficients(left, right) {
  const result = new Map(left);
  for (const [source, coefficient] of right) {
    result.set(source, (result.get(source) ?? 0) + Number(coefficient));
  }
  return result;
}

/** @param {Map<string, number>} coefficients @param {number} scale */
function scaleCoefficients(coefficients, scale) {
  return new Map([...coefficients].map(([source, coefficient]) => [source, coefficient * scale]));
}

/** @param {Map<string, number>} coefficients */
function coefficientTotal(coefficients) {
  return [...coefficients.values()].reduce((total, coefficient) => total + Number(coefficient), 0);
}

/** @param {Map<string, number>} coefficients */
function coefficientsObject(coefficients) {
  return Object.fromEntries([...coefficients]
    .map(([source, coefficient]) => [source, normalizedCoefficient(coefficient)])
    .toSorted(([left], [right]) => left.localeCompare(right)));
}

/** @param {Record<string, any>[]} definitions */
function databaseSourceTables(definitions) {
  const sources = new Map();
  for (const definition of definitions) {
    if (!isRecord(definition) || typeof definition.name !== 'string') continue;
    const stores = Array.isArray(definition.stores)
      ? definition.stores.filter((table) => typeof table === 'string')
      : [];
    const directTable = typeof definition.from === 'string' && definition.from.startsWith('$')
      ? definition.from.slice(1)
      : stores.find((table) => !['repositories', 'workflows', 'runs'].includes(table));
    if (directTable && stores.includes(directTable)) sources.set(definition.name, directTable);
  }
  return sources;
}

/** @param {Record<string, any>[]} definitions */
function databaseSourceFields(definitions) {
  const sources = new Map();
  for (const definition of definitions) {
    if (!isRecord(definition) || typeof definition.name !== 'string') continue;
    const fields = Array.isArray(definition.select)
      ? [...new Set(definition.select
        .filter((field) => isRecord(field) && (typeof field.as === 'string' || typeof field.field === 'string'))
        .map((field) => typeof field.as === 'string' ? field.as : field.field))]
      : undefined;
    if (!fields) continue;
    sources.set(definition.name, fields);
  }
  return sources;
}

/** @param {Record<string, number> | undefined} counts */
function normalizedTableWeights(counts) {
  if (counts === undefined) return { weights: new Map(), counts: undefined };
  const entries = Object.entries(counts);
  if (entries.some(([, count]) => !Number.isFinite(Number(count)) || Number(count) < 0)) {
    throw new TypeError('Database table row counts must be non-negative finite numbers');
  }
  const maximum = Math.max(1, ...entries.map(([, count]) => Number(count)));
  return {
    weights: new Map(entries.map(([table, count]) => [table, Number(count) / maximum])),
    counts: Object.fromEntries(entries.toSorted(([left], [right]) => left.localeCompare(right)))
  };
}

export async function readDashboardTableCounts() {
  const tables = [...new Set([...DATABASE_SOURCE_TABLES.values()].flat())];
  return Object.fromEntries(await Promise.all(tables.map(async (table) => {
    const source = await executeCanonicalQuery({
      name: `table-count:${table}`,
      from: table,
      aggregate: { values: [{ field: 'id', as: 'records', reducer: 'count' }] }
    });
    if (source.metadata.availability === 'unavailable') {
      throw new Error(`Canonical table count is unavailable: ${table}`);
    }
    return [table, Number(source.rows[0]?.records ?? 0)];
  })));
}

/** @param {number} value */
function normalizedCoefficient(value) {
  return Number(Number(value).toFixed(6));
}

/** @param {number} value */
function formatCoefficient(value) {
  return normalizedCoefficient(value).toLocaleString('en-US', { maximumFractionDigits: 6 });
}

/** @param {string} value */
function markdownCode(value) {
  const safe = String(value).replaceAll('`', "'").replaceAll('|', '\\|').replaceAll('\n', ' ');
  return `\`${safe}\``;
}

/** @param {string[]} consumers */
function formatConsumers(consumers) {
  return consumers.length === 0 ? '—' : consumers.map(markdownCode).join('<br>');
}

/** @param {unknown} value */
function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
