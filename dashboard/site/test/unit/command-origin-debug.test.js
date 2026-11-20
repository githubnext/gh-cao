// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  window.location.hash = '';
  vi.resetModules();
});

/** @param {string} search */
async function load(search) {
  const debugFn = vi.fn();
  const output = /** @type {Pick<Console, 'debug'>} */ ({ debug: debugFn });
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output }) };
  });
  vi.resetModules();
  return { debugFn, ...(await import('../../src/command-origin.js')) };
}

describe('command-origin debug logging', () => {
  it('is silent without a matching debug query and preserves results', async () => {
    const { debugFn, commandOriginRoute } = await load('');
    expect(commandOriginRoute('#page-overview')).toBe('#page-overview');
    expect(commandOriginRoute('https://example.invalid/#page-x')).toBe('');
    expect(debugFn).not.toHaveBeenCalled();
  });

  it('does not log accepted, empty, or missing routes', async () => {
    const { debugFn, commandOriginRoute } = await load('?debug=command-origin');
    expect(commandOriginRoute('#page-overview?a=b')).toBe('#page-overview?a=b');
    expect(commandOriginRoute('')).toBe('');
    expect(commandOriginRoute(undefined)).toBe('');
    expect(debugFn).not.toHaveBeenCalled();
  });

  it('logs rejection metadata under its predictable category without the route text', async () => {
    const { debugFn, commandOriginRoute, currentCommandOrigin } = await load('?debug=command-origin');
    expect(commandOriginRoute('#page-secret-token?token=SECRET value')).toBe('');
    expect(debugFn).toHaveBeenCalledWith('[cao:command-origin]', {
      operation: 'validate-route', status: 'rejected', reason: 'invalid-format', length: 37
    });
    expect(commandOriginRoute({ secret: 'SECRET' })).toBe('');
    expect(debugFn).toHaveBeenCalledWith('[cao:command-origin]', {
      operation: 'validate-route', status: 'rejected', reason: 'not-string', length: 0
    });
    window.location.hash = '#bad-SECRET!<>';
    expect(currentCommandOrigin()).toBe('');
    expect(JSON.stringify(debugFn.mock.calls)).not.toMatch(/SECRET|token/);
  });

  it('is excluded by a negated pattern', async () => {
    const { debugFn, commandOriginRoute } = await load('?debug=*,-command-origin');
    commandOriginRoute('nope');
    expect(debugFn).not.toHaveBeenCalled();
  });
});
