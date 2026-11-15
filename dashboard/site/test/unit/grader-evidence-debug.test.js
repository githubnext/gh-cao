// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

const context = { runId: '42', attempt: 1 };
const SECRET = 'secret-grader-identity';

/** @param {string} path @param {unknown} body */
const file = (path, body) => ({ path, content: JSON.stringify(body) });

/**
 * Loads grader-evidence.js with a stubbed debug output.
 * @param {string} search
 */
async function loadGraderEvidenceWithDebug(search) {
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

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('grader-evidence debug logging', () => {
  it('is disabled by default when the debug query is absent', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('');

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', { version: 3 })
    ], context)).toThrow('Unsupported grader artifact document');

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs unsupported documents under the predictable category', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('?debug=grader-evidence');

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', { version: 3 })
    ], context)).toThrow('Unsupported grader artifact document');

    expect(output.debug).toHaveBeenCalledWith(
      '[cao:grader-evidence]',
      { event: 'reject', status: 'rejected', reason: 'unsupported-document' }
    );
  });

  it('logs multiple result artifacts without file paths', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('?debug=grader-evidence');
    const body = { version: 1, results: [] };

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', body),
      file('other-graders/grader_results.json', body)
    ], context)).toThrow('Multiple grader result artifacts found for one run');

    expect(output.debug).toHaveBeenCalledWith(
      '[cao:grader-evidence]',
      { event: 'reject', status: 'rejected', reason: 'multiple-results' }
    );
  });

  it('logs invalid run identity or results', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('?debug=grader-evidence');

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', { version: 1, run: { id: 'other' }, results: [] })
    ], context)).toThrow('Grader artifact run identity or results are invalid');

    expect(output.debug).toHaveBeenCalledWith(
      '[cao:grader-evidence]',
      { event: 'reject', status: 'rejected', reason: 'run-or-results-invalid' }
    );
  });

  it('logs invalid result identities and excludes sensitive values', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('?debug=grader-evidence');

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', {
        version: 1,
        results: [{ id: SECRET, value: 1 }, { id: SECRET, value: 2 }]
      })
    ], context)).toThrow('unique identities');

    expect(output.debug).toHaveBeenCalledWith(
      '[cao:grader-evidence]',
      { event: 'reject', status: 'rejected', reason: 'invalid-result-identity' }
    );
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain(SECRET);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('grader_results.json');
  });

  it('logs an invalid grader manifest', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('?debug=grader-evidence');

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', { version: 1, results: [] }),
      file('graders/grader_manifest.json', { version: 1, graders: 'nope' })
    ], context)).toThrow('Invalid grader manifest');

    expect(output.debug).toHaveBeenCalledWith(
      '[cao:grader-evidence]',
      { event: 'reject', status: 'rejected', reason: 'invalid-manifest' }
    );
  });

  it('logs multiple grader manifests', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('?debug=grader-evidence');
    const manifest = { version: 1, graders: [] };

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', { version: 1, results: [] }),
      file('graders/grader_manifest.json', manifest),
      file('graders/grader_manifest.json', manifest)
    ], context)).toThrow('Multiple grader manifests found for one artifact');

    expect(output.debug).toHaveBeenCalledWith(
      '[cao:grader-evidence]',
      { event: 'reject', status: 'rejected', reason: 'multiple-manifests' }
    );
  });

  it('does not enable the category through an unrelated debug filter', async () => {
    const { parseGraderArtifacts, output } = await loadGraderEvidenceWithDebug('?debug=data:query');

    expect(() => parseGraderArtifacts([
      file('graders/grader_results.json', { version: 3 })
    ], context)).toThrow();

    expect(output.debug).not.toHaveBeenCalled();
  });
});
