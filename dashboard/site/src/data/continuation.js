import { createDebug } from '../debug.js';

const debugContinuation = createDebug('continuation');

export const CONTINUATION_PAGE_SIZE = 25;

/**
 * @param {string[]} sourceNames
 * @param {number} [limit]
 */
export function continuationRequests(sourceNames, limit = CONTINUATION_PAGE_SIZE) {
  validateLimit(limit);
  return Object.fromEntries(sourceNames.map((sourceName) => [
    sourceName,
    { limit }
  ]));
}

/**
 * Adds validated, source-bound continuation loaders without exposing token
 * mechanics to presenters.
 *
 * @param {Record<string, import('../presenter.js').LogicalSourceInput>} sources
 * @param {string[]} sourceNames
 * @param {(sourceNames: string[], pagination: Record<string, { limit: number, continuationToken?: string }>) => Promise<Record<string, import('../presenter.js').LogicalSourceInput>>} loadPage
 * @param {number} [limit]
 */
export function bindSourceContinuations(
  sources,
  sourceNames,
  loadPage,
  limit = CONTINUATION_PAGE_SIZE
) {
  validateLimit(limit);
  return Object.fromEntries(Object.entries(sources).map(([name, source]) => {
    if (!sourceNames.includes(name) || source.continuationToken === undefined) {
      return [name, source];
    }
    const pageContract = validatePage(name, source);
    /** @type {string | undefined} */
    let currentToken = source.continuationToken;
    /** @type {Promise<import('../presenter.js').LogicalSourceInput> | undefined} */
    let inFlight;
    return [name, {
      ...source,
      /** @param {string} continuationToken */
      loadContinuation: (continuationToken) => {
        if (continuationToken !== currentToken) {
          debugContinuation({ event: 'stale-token-rejected', source: name });
          return Promise.reject(new TypeError(`Continuation token for "${name}" is not current.`));
        }
        if (inFlight) return inFlight;
        inFlight = loadPage(
          [name],
          { [name]: { limit, continuationToken } }
        ).then((nextSources) => {
          const next = nextSources[name];
          if (!next) throw new TypeError(`Continuation response is missing source "${name}".`);
          validatePage(name, next, pageContract);
          currentToken = next.continuationToken;
          debugContinuation({
            event: 'page-loaded',
            source: name,
            rowCount: next.rows.length,
            hasMore: next.continuationToken !== undefined
          });
          return next;
        }).finally(() => {
          inFlight = undefined;
        });
        return inFlight;
      }
    }];
  }));
}

/**
 * Drains every remaining query continuation for a source so callers that
 * cannot page rows in incrementally -- charts, which plot every row of
 * their source rather than paging it on scroll -- always see the complete
 * result set instead of only the first paginated chunk.
 *
 * @param {import('../presenter.js').LogicalSourceInput} source
 * @returns {Promise<import('../presenter.js').LogicalSourceInput>}
 */
export async function drainSourceContinuation(source) {
  let page = sourceContinuation(source);
  if (!page) return source;
  const rows = [...source.rows];
  /** @type {string | undefined} */
  let token = page.token;
  const load = page.load;
  let pagesLoaded = 0;
  while (token) {
    const next = await load(token);
    if (Array.isArray(next.rows)) rows.push(...next.rows);
    token = next.continuationToken;
    pagesLoaded += 1;
  }
  debugContinuation({ event: 'drain-completed', source: source.source, pagesLoaded, rowCount: rows.length });
  return { ...source, rows, continuationToken: undefined };
}

/** @param {import('../presenter.js').LogicalSourceInput} source */
export function sourceContinuation(source) {
  if (source.continuationToken === undefined) return undefined;
  if (typeof source.loadContinuation !== 'function') return undefined;
  const { totalRows } = validatePage(source.source, source);
  return {
    token: source.continuationToken,
    totalRows,
    load: source.loadContinuation
  };
}

/**
 * @param {string} name
 * @param {import('../presenter.js').LogicalSourceInput} source
 * @param {{ mode: 'counted'|'bounded', totalRows?: number }} [expected]
 */
function validatePage(name, source, expected) {
  if (source.source !== name || !Array.isArray(source.rows)) {
    throw new TypeError(`Invalid continuation response for source "${name}".`);
  }
  if (source.continuationToken !== undefined
      && (typeof source.continuationToken !== 'string' || source.continuationToken.length === 0)) {
    throw new TypeError(`Invalid continuation token for source "${name}".`);
  }
  const rawTotalRows = source.metadata?.['total-row-count'];
  if (rawTotalRows !== undefined) {
    const totalRows = Number(rawTotalRows);
    if (!Number.isSafeInteger(totalRows) || totalRows < source.rows.length) {
      throw new TypeError(`Invalid continuation row count for source "${name}".`);
    }
    if (expected?.mode === 'bounded'
        || (expected?.totalRows !== undefined && totalRows !== expected.totalRows)) {
      throw new TypeError(`Continuation data for "${name}" is stale.`);
    }
    return { mode: /** @type {'counted'} */ ('counted'), totalRows };
  }

  const pageSize = Number(source.metadata?.['page-size']);
  const pageRowCount = Number(source.metadata?.['page-row-count']);
  const hasMore = source.metadata?.['has-more'];
  if (!Number.isSafeInteger(pageSize) || pageSize <= 0
      || !Number.isSafeInteger(pageRowCount) || pageRowCount !== source.rows.length
      || pageRowCount > pageSize || typeof hasMore !== 'boolean'
      || hasMore !== (source.continuationToken !== undefined)) {
    throw new TypeError(`Invalid continuation row count for source "${name}".`);
  }
  if (expected?.mode === 'counted') {
    throw new TypeError(`Continuation data for "${name}" is stale.`);
  }
  return { mode: /** @type {'bounded'} */ ('bounded') };
}

/** @param {number} limit */
function validateLimit(limit) {
  if (!Number.isSafeInteger(limit) || limit <= 0) {
    throw new TypeError('Continuation page size must be a positive integer.');
  }
}
