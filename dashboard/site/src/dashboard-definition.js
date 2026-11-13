import { createDebug, diagnosticErrorName } from './debug.js';
import { pageCapabilityAvailable } from './view-availability.js';

const debugDefinition = createDebug('dashboard-definition');
/** @type {Promise<{ 'language-version': string, dashboard: import('./presenter.js').PresentableDashboard }> | undefined} */
let schemaRequest;
/** @typedef {{ page: import('./dashboard-chunks.js').DashboardPage, queries?: Array<Record<string, unknown>> }} PageChunk */
/** @type {Map<string, Promise<PageChunk>>} */
const pageRequests = new Map();

export function loadDashboardSchema() {
  if (schemaRequest) return schemaRequest;
  schemaRequest = fetch('./dashboard.json', { cache: 'no-store' }).then((response) => {
    debugDefinition({ event: 'schema-response', status: response.status, ok: response.ok });
    if (!response.ok) throw new Error(`Unable to load dashboard.json: ${response.status}`);
    return response.json();
  });
  void schemaRequest.catch(() => { schemaRequest = undefined; });
  return schemaRequest;
}

/** @param {string} path */
export function loadDashboardPageChunk(path) {
  let request = pageRequests.get(path);
  if (!request) {
    request = fetch(`./${path}`, { cache: 'reload' }).then((response) => {
      debugDefinition({ event: 'page-chunk-response', status: response.status, ok: response.ok });
      if (!response.ok) throw new Error(`Unable to load dashboard page "${path}": ${response.status}`);
      return response.json();
    });
    pageRequests.set(path, request);
    void request.catch(() => pageRequests.delete(path));
  }
  return request;
}

export function preloadDashboardDefinition() {
  const preload = loadDashboardSchema().then((schema) => {
    let pageId = 'overview';
    try {
      if (location.hash.startsWith('#page-')) pageId = decodeURIComponent(location.hash.slice(6).split('?')[0]);
    } catch {
      pageId = 'overview';
    }
    const pages = schema.dashboard?.pages?.filter(pageCapabilityAvailable);
    const page = Array.isArray(pages)
      ? pages.find((page) => page.id === pageId) ?? pages.find((page) => page.id === 'overview')
      : undefined;
    if (typeof page?.chunk === 'string') return loadDashboardPageChunk(page.chunk);
  });
  // The application consumes the same requests and owns user-visible failures.
  void preload.catch((error) => debugDefinition({
    event: 'preload-failed', errorName: diagnosticErrorName(error)
  }));
}
