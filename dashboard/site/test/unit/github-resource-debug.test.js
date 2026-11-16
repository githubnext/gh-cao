// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';

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
  const module = await import('../../src/data/github-resource.js');
  return { ...module, output };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('github-resource debug logging', () => {
  it('is disabled by default and preserves results', async () => {
    const { githubResourcePath, output } = await load('');
    expect(githubResourcePath({ owner: 'Secret-Owner', repo: 'Secret-Repo' })).toBe('repos/secret-owner/secret-repo');
    expect(() => githubResourcePath({ owner: 'a/b', repo: 'x' })).toThrow('Invalid GitHub resource.');
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs only scalar outcomes and never the owner or repository', async () => {
    const { githubResourcePath, output } = await load('?debug=github-resource');
    expect(githubResourcePath({ owner: 'Secret-Owner', repo: 'Secret-Repo' })).toBe('repos/secret-owner/secret-repo');
    expect(githubResourcePath({ owner: 'Secret-Owner', repo: 'Secret-Repo', resource: 'latest-release' }))
      .toBe('repos/secret-owner/secret-repo/releases/latest');
    expect(() => githubResourcePath({ owner: 'secret/owner', repo: 'secret-repo' })).toThrow(TypeError);
    expect(output.debug.mock.calls).toEqual([
      ['[cao:github-resource]', { event: 'path-resolved', status: 'resolved', resource: 'repository' }],
      ['[cao:github-resource]', { event: 'path-resolved', status: 'resolved', resource: 'latest-release' }],
      ['[cao:github-resource]', { event: 'path-rejected', status: 'invalid-resource' }]
    ]);
    expect(JSON.stringify(output.debug.mock.calls).toLowerCase()).not.toContain('secret');
  });

  it('supports category exclusion', async () => {
    const { githubResourcePath, output } = await load('?debug=*,-github-resource');
    githubResourcePath({ owner: 'a', repo: 'b' });
    expect(output.debug).not.toHaveBeenCalled();
  });
});
