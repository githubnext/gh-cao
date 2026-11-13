// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadWithDebug(search) {
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
  const module = await import('../../src/data/adapters/grader-evidence.js');
  return { ...module, output };
}

const context = { runId: '303', attempt: 1 };
const resultsPath = 'graders/grader_results.json';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('grader-evidence debug logging', () => {
  it('is disabled by default', async () => {
    const { parseGraderArtifacts, output } = await loadWithDebug('');
    expect(() => parseGraderArtifacts([
      { path: resultsPath, content: JSON.stringify({ version: 3, results: [] }) }
    ], context)).toThrow('Unsupported grader artifact document');
    parseGraderArtifacts([], context);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs stable rejection reasons without artifact content or paths', async () => {
    const { parseGraderArtifacts, output } = await loadWithDebug('?debug=grader-evidence');
    const secret = 'PRIVATE_RESULT_ID';
    const attempt = (/** @type {unknown} */ content) =>
      () => parseGraderArtifacts([{ path: resultsPath, content: JSON.stringify(content) }], context);
    expect(attempt({ version: 3, results: [secret] })).toThrow('Unsupported grader artifact document');
    expect(attempt({ version: 2, run: { id: '404' }, results: [] })).toThrow('run identity or results are invalid');
    expect(attempt({ version: 2, results: [{ id: secret, value: 1 }, { id: secret, value: 2 }] }))
      .toThrow('unique identities');
    expect(() => parseGraderArtifacts([
      { path: resultsPath, content: JSON.stringify({ version: 2, results: [] }) },
      { path: 'other/grader_results.json', content: '{}' }
    ], context)).toThrow('Multiple grader result artifacts');

    expect(output.debug.mock.calls).toEqual([
      ['[cao:grader-evidence]', { event: 'reject', reason: 'unsupported-document' }],
      ['[cao:grader-evidence]', { event: 'reject', reason: 'invalid-run-or-results' }],
      ['[cao:grader-evidence]', { event: 'reject', reason: 'invalid-result-entry' }],
      ['[cao:grader-evidence]', { event: 'reject', reason: 'multiple-results' }]
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain(secret);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('grader_results');
  });

  it('logs scalar counts when no artifact is present', async () => {
    const { parseGraderArtifacts, output } = await loadWithDebug('?debug=grader-evidence');
    expect(parseGraderArtifacts([], context, { results: [{ id: 'a' }, { id: 'b' }] }))
      .toEqual({ results: [{ id: 'a' }, { id: 'b' }] });
    expect(output.debug).toHaveBeenCalledWith('[cao:grader-evidence]', {
      event: 'project', status: 'no-artifact', sessionResults: 2
    });
  });

  it('is selectable by wildcard and excluded by a negative pattern', async () => {
    const enabled = await loadWithDebug('?debug=grader-*');
    enabled.parseGraderArtifacts([], context);
    expect(enabled.output.debug).toHaveBeenCalledTimes(1);
    const excluded = await loadWithDebug('?debug=*,-grader-evidence');
    excluded.parseGraderArtifacts([], context);
    expect(excluded.output.debug).not.toHaveBeenCalled();
  });
});
