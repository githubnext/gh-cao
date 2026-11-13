import { describe, expect, it, vi } from 'vitest';
import {
  cliActionTemplateFields,
  cliActionTemplateContext,
  validateCliActionUrl,
  renderCliActionCommand
} from '../../src/cli-action-template.js';

describe('CLI action command templates', () => {
  it('quotes wildcard enrollment patterns without widening exact repository tokens', () => {
    expect(renderCliActionCommand('gh cao repos add {{repository-pattern}}', { 'repository-pattern': 'octo/service-*' }))
      .toBe("gh cao repos add 'octo/service-*'");
    expect(renderCliActionCommand('gh cao repos add {{repository-pattern}}', { 'repository-pattern': 'octo/repo' }))
      .toBe('gh cao repos add octo/repo');
    expect(() => renderCliActionCommand('gh aw update --repo {{repository}}', { repository: 'octo/*' })).toThrow();
    for (const value of ['-owner/*', 'owner/*;echo', "owner/'*", 'owner/*\n', 'owner/* --flag', 'owner/$(id)']) {
      expect(() => renderCliActionCommand('gh cao repos add {{repository-pattern}}', { 'repository-pattern': value })).toThrow();
    }
  });
  it('separates host-owned identity from row fields and preserves deployment paths', () => {
    const command = "gh aw upgrade --repo {{repository}} --pr-message 'Requested by {{cao-repository}} ({{cao-repository-url}}). See {{cao-url}}#page-maintenance.'";
    const context = cliActionTemplateContext({
      repository: 'acme/control', githubUrlBase: 'https://git.example.com/enterprise',
      caoUrl: 'https://ops.example.com/central'
    });
    expect(cliActionTemplateFields(command)).toEqual(['repository']);
    expect(cliActionTemplateFields(command, { includeContext: true }))
      .toEqual(['repository', 'cao-repository', 'cao-repository-url', 'cao-url']);
    expect(renderCliActionCommand(command, {
      repository: 'octo/target', 'cao-url': 'https://attacker.example/'
    }, context)).toBe("gh aw upgrade --repo octo/target --pr-message 'Requested by acme/control (https://git.example.com/enterprise/acme/control). See https://ops.example.com/central/#page-maintenance.'");
  });

  it('fails closed when host context is missing instead of accepting a row override', () => {
    expect(() => renderCliActionCommand('gh aw update --pr-message {{cao-url}}', {
      'cao-url': 'https://attacker.example/'
    })).toThrow('"cao-url" is missing or invalid');
  });

  it.each([
    'http://ops.example.com/', 'https://user:secret@ops.example.com/', 'https://ops.example.com/?token=secret',
    'https://ops.example.com/#page-maintenance', "https://ops.example.com/'", 'https://ops.example.com/"',
    'https://ops.example.com/$(whoami)', 'https://ops.example.com/\n', 'https://ops.example.com/)injected',
    'https://ops.example.com/\\escape', 'https://ops.example.com/' + 'a'.repeat(2048)
  ])('rejects unsafe CAO URLs without echoing their contents', (value) => {
    expect(() => validateCliActionUrl(value)).toThrow('CAO URL must be an HTTPS URL');
    expect(() => renderCliActionCommand('gh aw update --pr-message {{cao-url}}', {}, { 'cao-url': value }))
      .toThrow();
  });

  it('lists unique template fields and renders a repository slug', () => {
    const command = 'gh aw update --repo {{repository}} --source {{repository}}';

    expect(cliActionTemplateFields(command)).toEqual(['repository']);
    expect(renderCliActionCommand(command, { repository: 'octo/example' }))
      .toBe('gh aw update --repo octo/example --source octo/example');
  });

  it('renders immutable marketplace package coordinates', () => {
    expect(renderCliActionCommand('gh cao add {{package-source}}', {
      'package-source': 'octo/packages/demo@0123456789abcdef'
    })).toBe('gh cao add octo/packages/demo@0123456789abcdef');
  });

  it.each([
    [{}, 'missing or invalid'],
    [{ repository: 'octo/example --force' }, 'not a safe command token'],
    [{ repository: 'octo' }, 'not a safe command token'],
    [{ repository: '-/example' }, 'not a safe command token']
  ])('rejects unsafe repository template values', (values, message) => {
    expect(() => renderCliActionCommand(
      'gh aw update --repo {{repository}}',
      values
    )).toThrow(message);
  });

  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '', output })
      };
    });
    vi.resetModules();
    const { renderCliActionCommand: renderWithoutDebug } = await import('../../src/cli-action-template.js');

    renderWithoutDebug('gh aw update --repo {{repository}}', { repository: 'octo/example' });
    expect(() => renderWithoutDebug('gh aw update --repo {{repository}}', {})).toThrow();

    expect(output.debug).not.toHaveBeenCalled();

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('logs only scalar metadata under its predictable category when enabled', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=cli-action-template', output })
      };
    });
    vi.resetModules();
    const { renderCliActionCommand: renderWithDebug } = await import('../../src/cli-action-template.js');

    renderWithDebug('gh aw update --repo {{repository}}', { repository: 'octo/example' });
    expect(output.debug).toHaveBeenCalledWith('[cao:cli-action-template]', { event: 'render-completed', fieldCount: 1 });

    expect(() => renderWithDebug('gh aw update --repo {{repository}}', {}))
      .toThrow('missing or invalid');
    expect(output.debug).toHaveBeenCalledWith('[cao:cli-action-template]', {
      event: 'render-rejected',
      field: 'repository',
      reason: 'missing-or-invalid'
    });

    expect(() => renderWithDebug('gh aw update --repo {{repository}}', { repository: 'octo' }))
      .toThrow('not a safe command token');
    expect(output.debug).toHaveBeenCalledWith('[cao:cli-action-template]', {
      event: 'render-rejected',
      field: 'repository',
      reason: 'unsafe-token'
    });

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });
});
