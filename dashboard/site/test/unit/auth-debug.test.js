import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.doUnmock('../../src/rate-limit-notification.js');
  vi.unstubAllGlobals();
  vi.resetModules();
});

/**
 * Loads auth.js with a stubbed debug output so assertions can inspect emitted
 * metadata without depending on module state left over from other tests.
 * @param {string} search
 */
async function loadAuthWithDebug(search) {
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
  vi.doMock('../../src/rate-limit-notification.js', () => ({ updateRateLimitNotification: vi.fn() }));
  vi.resetModules();
  const module = await import('../../src/auth.js');
  return { ...module, output };
}

/** @param {string} cookie */
function fakeDocument(cookie = '') {
  return /** @type {Document} */ (/** @type {unknown} */ ({ cookie, querySelector: () => null }));
}

/** @param {string} mode */
function fakeAuthModeDocument(mode) {
  return /** @type {Document} */ (/** @type {unknown} */ ({
    cookie: '',
    querySelector: (/** @type {string} */ selector) => (selector === 'meta[name="cao-auth-mode"]'
      ? { getAttribute: () => mode }
      : null)
  }));
}

describe('auth debug logging', () => {
  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const { usesGitHubAuthentication, csrfToken, csrfHeaders, output } = await loadAuthWithDebug('');

    usesGitHubAuthentication(fakeAuthModeDocument('github'));
    csrfToken(fakeDocument('cao_csrf=abc123'));
    csrfHeaders({}, fakeDocument(''));

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs mode, token, and header resolution under the predictable category name derived from the filename', async () => {
    const { usesGitHubAuthentication, csrfToken, csrfHeaders, output } = await loadAuthWithDebug('?debug=auth');

    expect(usesGitHubAuthentication(fakeAuthModeDocument('github'))).toBe(true);
    expect(output.debug).toHaveBeenCalledWith('[cao:auth]', { event: 'mode-resolved', mode: 'github' });

    expect(usesGitHubAuthentication(fakeAuthModeDocument('token'))).toBe(false);
    expect(output.debug).toHaveBeenCalledWith('[cao:auth]', { event: 'mode-resolved', mode: 'token' });

    expect(csrfToken(fakeDocument(''))).toBe('');
    expect(output.debug).toHaveBeenCalledWith('[cao:auth]', { event: 'csrf-token-resolved', found: false });

    expect(csrfToken(fakeDocument('cao_csrf=abc123'))).toBe('abc123');
    expect(output.debug).toHaveBeenCalledWith('[cao:auth]', { event: 'csrf-token-resolved', found: true });

    csrfHeaders({ Accept: 'application/json' }, fakeDocument(''));
    expect(output.debug).toHaveBeenCalledWith('[cao:auth]', { event: 'csrf-headers-applied', applied: false });

    csrfHeaders({ Accept: 'application/json' }, fakeDocument('cao_csrf=abc123'));
    expect(output.debug).toHaveBeenCalledWith('[cao:auth]', { event: 'csrf-headers-applied', applied: true });
  });

  it('never logs the raw cookie value or token, only scalar metadata', async () => {
    const { csrfToken, csrfHeaders, output } = await loadAuthWithDebug('?debug=auth');

    csrfToken(fakeDocument('cao_csrf=do-not-log-this-secret-token'));
    csrfHeaders({}, fakeDocument('cao_csrf=do-not-log-this-secret-token'));

    for (const call of output.debug.mock.calls) {
      const [, payload] = call;
      expect(JSON.stringify(payload)).not.toContain('do-not-log-this-secret');
      for (const value of Object.values(payload)) {
        expect(value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean').toBe(true);
      }
    }
  });

  it('is disabled by default for session renewal (no debug output)', async () => {
    document.cookie = 'cao_csrf=abc123; Path=/';
    const { ensureCsrfToken, output } = await loadAuthWithDebug('');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 200 }));

    await ensureCsrfToken();

    expect(output.debug).not.toHaveBeenCalled();
    document.cookie = 'cao_csrf=; Max-Age=0; Path=/';
  });

  it('logs session renewal completion with status, outcome, and coarse duration', async () => {
    document.cookie = 'cao_csrf=; Max-Age=0; Path=/';
    const { ensureCsrfToken, output } = await loadAuthWithDebug('?debug=auth');
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => {
      document.cookie = 'cao_csrf=renewed-token; Path=/';
      return { ok: true, status: 200 };
    }));

    await ensureCsrfToken();

    const call = output.debug.mock.calls.find(([, payload]) => payload.event === 'session-renewal-completed');
    expect(call).toBeTruthy();
    const [, payload] = /** @type {[string, Record<string, unknown>]} */ (call);
    expect(payload).toMatchObject({ event: 'session-renewal-completed', status: 200, renewed: true });
    expect(typeof payload.durationMs).toBe('number');
    document.cookie = 'cao_csrf=; Max-Age=0; Path=/';
  });

  it('logs session renewal failure with a sanitized error name when the request throws', async () => {
    document.cookie = 'cao_csrf=; Max-Age=0; Path=/';
    const { ensureCsrfToken, output } = await loadAuthWithDebug('?debug=auth');
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));

    await expect(ensureCsrfToken()).rejects.toThrow();

    const call = output.debug.mock.calls.find(([, payload]) => payload.event === 'session-renewal-failed');
    expect(call).toBeTruthy();
    const [, payload] = /** @type {[string, Record<string, unknown>]} */ (call);
    expect(payload).toEqual({ event: 'session-renewal-failed', errorName: 'TypeError', durationMs: expect.any(Number) });
    expect(JSON.stringify(payload)).not.toContain('Failed to fetch');
  });

  it('does not log principal or invalidation transitions by default', async () => {
    const { setGitHubResourcePrincipal, invalidateGitHubResourceAuthentication, output } = await loadAuthWithDebug('');

    setGitHubResourcePrincipal('octocat');
    invalidateGitHubResourceAuthentication();

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs principal changes and invalidations without the principal identity', async () => {
    const { setGitHubResourcePrincipal, invalidateGitHubResourceAuthentication, githubResourcePrincipal, output } = await loadAuthWithDebug('?debug=auth');

    setGitHubResourcePrincipal('private-login-name');
    setGitHubResourcePrincipal('private-login-name');
    invalidateGitHubResourceAuthentication();

    expect(githubResourcePrincipal()).toBe('private-login-name');
    expect(output.debug.mock.calls.map(([category, payload]) => [category, payload])).toEqual([
      ['[cao:auth]', { event: 'resource-principal-changed', hadPrincipal: false, hasPrincipal: true }],
      ['[cao:auth]', { event: 'resource-authentication-invalidated', resume: true }],
      ['[cao:auth]', { event: 'resource-authentication-invalidated', resume: false }]
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('private-login-name');
  });

  it('can be filtered to exclude auth logging', async () => {
    const { invalidateGitHubResourceAuthentication, output } = await loadAuthWithDebug('?debug=*,-auth');

    invalidateGitHubResourceAuthentication();

    expect(output.debug).not.toHaveBeenCalled();
  });
});
