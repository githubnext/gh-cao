// @vitest-environment node
import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const resource = { owner: 'secret-owner', repo: 'secret-repo' };
const configuration = { baseUrl: 'https://dashboard.example/api/v1/github/', cacheIdentity: 'secret-principal:session' };
const repository = { full_name: 'secret-owner/secret-repo', description: 'private description text' };
/** @type {AbortController[]} */
let controllers = [];

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  const { createGitHubDataService } = await import('../../src/data/github-service.js');
  const { deleteCanonicalDatabase } = await import('../../src/data/storage/indexeddb.js');
  await deleteCanonicalDatabase(indexedDB);
  return { output, createGitHubDataService };
}

/** @param {{ subscribe: Function }} service */
function subscribe(service) {
  const controller = new AbortController();
  controllers.push(controller);
  const listener = vi.fn();
  service.subscribe(resource, configuration, listener, { signal: controller.signal });
  return listener;
}

beforeEach(() => { controllers = []; });
afterEach(() => {
  for (const controller of controllers) controller.abort();
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
  vi.restoreAllMocks();
});

describe('github service debug logging', () => {
  it('is silent by default and when another category is selected', async () => {
    for (const search of ['', '?debug=data:query']) {
      const { output, createGitHubDataService } = await load(search);
      const service = createGitHubDataService({
        fetch: vi.fn(async () => Response.json(repository, { headers: { ETag: '"v1"' } }))
      });
      const listener = subscribe(service);
      await vi.waitFor(() => expect(listener).toHaveBeenCalledWith(expect.objectContaining({ source: 'network' })));
      service.invalidateAuthentication();
      expect(output.debug).not.toHaveBeenCalled();
    }
  });

  it('logs refresh, failure and invalidation transitions under github-service without payload data', async () => {
    const { output, createGitHubDataService } = await load('?debug=github-service');
    const service = createGitHubDataService({
      fetch: vi.fn(async () => Response.json(repository, { headers: { ETag: '"v1"' } }))
    });
    const listener = subscribe(service);
    await vi.waitFor(() => expect(listener).toHaveBeenCalledWith(expect.objectContaining({ source: 'network' })));
    expect(output.debug).toHaveBeenCalledWith('[cao:github-service]', {
      event: 'refresh-succeeded', status: 200, notModified: false, staleResponse: false, rateLimited: false
    });

    service.invalidateAuthentication();
    expect(output.debug).toHaveBeenLastCalledWith('[cao:github-service]', {
      event: 'authentication-invalidated', generation: 1, subscriptionCount: 1
    });

    const failing = createGitHubDataService({
      fetch: vi.fn(async () => new Response('private body', { status: 503 })),
      retryBaseMs: 60_000
    });
    const failingListener = subscribe(failing);
    await vi.waitFor(() => expect(failingListener).toHaveBeenCalledWith(expect.objectContaining({ error: 'http-503' })));
    expect(output.debug).toHaveBeenCalledWith('[cao:github-service]', expect.objectContaining({
      event: 'refresh-failed', reason: 'http-503', attempt: 1, retryable: true
    }));

    const logged = JSON.stringify(output.debug.mock.calls);
    for (const sensitive of ['secret-owner', 'secret-repo', 'secret-principal', 'private', 'dashboard.example']) {
      expect(logged).not.toContain(sensitive);
    }
    for (const [prefix, payload] of output.debug.mock.calls) {
      expect(prefix).toBe('[cao:github-service]');
      for (const value of Object.values(payload)) {
        expect(['string', 'number', 'boolean']).toContain(typeof value);
      }
    }
  });
});
