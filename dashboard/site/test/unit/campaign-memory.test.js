// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  renderAllCampaignMemory,
  renderCampaignMemory,
} from '../../src/components/campaign-memory.js';
import { dashboardViewAliasName } from '../../src/data/queries/view-payload-compiler.js';
import { publishSource, resetSourceStore } from '../../src/source-store.js';
import { DataRequestWorker } from '../data-request-worker.js';

const memoryApi = vi.hoisted(() => ({
  list: vi.fn(),
  listAll: vi.fn(),
  read: vi.fn(),
}));
vi.mock('../../src/data-processor.js', async (importOriginal) => ({
  ...await importOriginal(),
  listRepositoryMemory: memoryApi.list,
  listRepositoryMemoryCampaigns: memoryApi.listAll,
  readRepositoryMemoryFile: memoryApi.read,
}));

beforeEach(() => {
  resetSourceStore();
  vi.stubGlobal('Worker', DataRequestWorker);
});
afterEach(() => {
  resetSourceStore();
  memoryApi.list.mockReset();
  memoryApi.listAll.mockReset();
  memoryApi.read.mockReset();
  document.body.replaceChildren();
});

describe('campaign repository memory', () => {
  it('browses every campaign memory in place', async () => {
    memoryApi.listAll
      .mockResolvedValueOnce([{
        branch: 'memory/ambient-context',
        commit: 'a'.repeat(40),
        files: [{ path: 'notes/ambient.md', oid: 'b'.repeat(40), size: 9 }],
        omitted: {},
      }, {
        branch: 'memory/security-review',
        commit: 'c'.repeat(40),
        files: [
          { path: 'security.md', oid: 'd'.repeat(40), size: 10 },
          { path: 'transactions.jsonl', oid: 'e'.repeat(40), size: 22 },
        ],
        omitted: {},
      }]);
    memoryApi.read
      .mockResolvedValueOnce({ content: '# Ambient' })
      .mockResolvedValueOnce({ content: '# Security' })
      .mockResolvedValueOnce({ content: '{"id":1}\n{"id":2,"ok":true}\n' });

    const rendered = renderAllCampaignMemory({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [
            { campaign: 'ambient-context', 'campaign-name': 'Ambient Context' },
            { campaign: 'security-review', 'campaign-name': 'Security Review' },
          ],
          metadata: {
            'source-id': 'campaign-memory-test',
            'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z',
            'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available',
          },
        },
      },
      contextDetails: [],
      headingTag: 'h3',
    });
    document.body.append(rendered);

    await vi.waitFor(() => expect(memoryApi.listAll).toHaveBeenCalledTimes(1));
    const firstCampaign = /** @type {HTMLDetailsElement} */ (
      rendered.querySelector('.cao-memory-campaign-branch')
    );
    expect(firstCampaign.open).toBe(false);
    expect(rendered.querySelector('pre')).toBeNull();
    expect(memoryApi.read).not.toHaveBeenCalled();
    expect(rendered.querySelector('.cao-memory-layout')?.getAttribute('data-memory-view')).toBe('browser');
    firstCampaign.open = true;
    await vi.waitFor(() => expect(firstCampaign.querySelector('.campaign-memory-file')).not.toBeNull());
    /** @type {HTMLButtonElement} */ (firstCampaign.querySelector('.campaign-memory-file')).click();
    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent).toBe('# Ambient'));
    expect(rendered.querySelector('.cao-memory-layout')?.getAttribute('data-memory-view')).toBe('file');
    expect(document.activeElement).toBe(rendered.querySelector('.cao-memory-file-content'));
    expect([...rendered.querySelectorAll('.cao-memory-campaign')].map((node) => node.textContent))
      .toEqual(['Ambient Context', 'Security Review']);
    expect(rendered.querySelector('.cao-memory-tree')).not.toBeNull();
    expect(rendered.querySelector('.cao-memory-file-content')).not.toBeNull();
    expect(rendered.querySelector('.campaign-memory-directory > summary')?.textContent).toBe('notes');
    expect(rendered.querySelector('.campaign-memory-file')?.textContent).toContain('ambient.md');
    expect(rendered.querySelector('.cao-memory-campaign > .octicon-chevron-right')).not.toBeNull();
    expect(rendered.querySelector('.campaign-memory-directory > summary > .octicon-chevron-right')).not.toBeNull();
    expect(rendered.querySelector('.campaign-memory-file .octicon-markdown')).not.toBeNull();
    expect(rendered.querySelector('.campaign-memory-file')?.getAttribute('aria-label'))
      .toBe('notes/ambient.md, 9 B');
    expect(rendered.querySelector('.campaign-memory-file')?.getAttribute('title'))
      .toBe('notes/ambient.md (9 B)');
    expect(rendered.querySelector('.campaign-memory-file small')).toBeNull();

    const secondCampaign = /** @type {HTMLDetailsElement} */ (
      rendered.querySelectorAll('.cao-memory-campaign-branch')[1]
    );
    secondCampaign.open = true;
    await vi.waitFor(() => expect(secondCampaign.querySelector('.campaign-memory-file')).not.toBeNull());
    /** @type {HTMLButtonElement} */ (secondCampaign.querySelector('.campaign-memory-file')).click();
    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent).toBe('# Security'));
    expect(rendered.querySelector('.cao-memory-layout')?.getAttribute('data-memory-view')).toBe('file');
    expect(document.activeElement).toBe(rendered.querySelector('.cao-memory-file-content'));
    /** @type {HTMLButtonElement} */ (secondCampaign.querySelectorAll('.campaign-memory-file')[1]).click();
    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent)
      .toBe('{\n  "id": 1\n}\n{\n  "id": 2,\n  "ok": true\n}\n'));
    /** @type {HTMLButtonElement} */ (rendered.querySelector('[data-tab-value="table"]')).click();
    await vi.waitFor(() => expect([...rendered.querySelectorAll('table tbody tr')].map((row) => row.textContent))
      .toEqual(['11', '22true']));
    /** @type {HTMLButtonElement} */ (rendered.querySelector('[data-tab-value="raw"]')).click();
    expect(rendered.querySelector('.memory-mobile-back')).toBeNull();
    publishSource('campaign-memory-campaigns', {
      source: 'campaign-memory-campaigns',
      rows: [
        { campaign: 'ambient-context', 'campaign-name': 'Ambient Context' },
        { campaign: 'security-review', 'campaign-name': 'Security Review' },
      ],
      metadata: {
        'source-id': 'campaign-memory-refresh',
        'source-kind': 'fixture',
        'as-of': '2026-09-26T01:00:00Z',
        'retrieved-at': '2026-09-26T01:00:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available',
      },
    }, dashboardViewAliasName('memory', { id: 'campaign-memory-browser' }, 0, 'campaign-memory-campaigns', 0));
    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent)
      .toBe('{\n  "id": 1\n}\n{\n  "id": 2,\n  "ok": true\n}\n'));
    expect(location.hash).toBe('');
    expect(memoryApi.listAll.mock.calls[0][0]).toEqual(['ambient-context', 'security-review']);
    expect(memoryApi.read.mock.calls.map(([campaign, path]) => [campaign, path])).toEqual([
      ['ambient-context', 'notes/ambient.md'],
      ['security-review', 'security.md'],
      ['security-review', 'transactions.jsonl'],
    ]);
  });

  it('navigates expanded memory branches with one tab stop and activates files', async () => {
    memoryApi.listAll.mockResolvedValue([
      { branch: 'memory/empty', commit: 'a'.repeat(40), files: [], omitted: {} },
      {
        branch: 'memory/available', commit: 'b'.repeat(40), omitted: {},
        files: [
          { path: 'notes/deep/first.md', oid: 'c'.repeat(40), size: 5 },
          { path: 'notes/second.md', oid: 'd'.repeat(40), size: 6 },
          { path: 'root.md', oid: 'e'.repeat(40), size: 7 },
        ],
      },
      {
        branch: 'memory/other', commit: 'f'.repeat(40), omitted: {},
        files: [{ path: 'other.md', oid: '1'.repeat(40), size: 8 }],
      },
    ]);
    memoryApi.read.mockResolvedValue({ content: '# File' });
    const rendered = renderAllCampaignMemory({
      pageId: 'memory', title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [
            { campaign: 'empty', 'campaign-name': 'Empty' },
            { campaign: 'available', 'campaign-name': 'Available' },
            { campaign: 'other', 'campaign-name': 'Other' },
          ],
          metadata: {
            'source-id': 'memory-test', 'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z', 'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete', freshness: 'fresh', availability: 'available',
          },
        },
      },
      contextDetails: [], headingTag: 'h3',
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelectorAll('.campaign-memory-file')).toHaveLength(4));

    const tree = /** @type {HTMLElement} */ (rendered.querySelector('[role="tree"]'));
    const [empty, available, other] = [...tree.querySelectorAll('.cao-memory-campaign')];
    const [notes, deep] = [...tree.querySelectorAll('.campaign-memory-directory > summary')];
    const [first, second, rootFile, otherFile] = [...tree.querySelectorAll('.campaign-memory-file')];
    /** @param {string} key */
    const press = (key) => document.activeElement?.dispatchEvent(
      new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
    );
    expect(empty?.getAttribute('aria-disabled')).toBe('true');
    expect(available?.getAttribute('tabindex')).toBe('0');
    /** @type {HTMLElement} */ (available).focus();
    press('ArrowDown');
    expect(document.activeElement).toBe(other);
    press('ArrowUp');
    expect(document.activeElement).toBe(available);
    press('ArrowRight');
    expect(available?.getAttribute('aria-expanded')).toBe('true');
    press('ArrowRight');
    expect(document.activeElement).toBe(notes);
    press('ArrowDown');
    expect(document.activeElement).toBe(deep);
    press('ArrowRight');
    expect(document.activeElement).toBe(first);
    press('ArrowDown');
    expect(document.activeElement).toBe(second);
    press('ArrowDown');
    expect(document.activeElement).toBe(rootFile);
    press('ArrowLeft');
    expect(document.activeElement).toBe(available);
    press('End');
    expect(document.activeElement).toBe(other);
    press('Home');
    expect(document.activeElement).toBe(available);
    expect(tree.querySelectorAll('[tabindex="0"]')).toHaveLength(1);
    /** @type {HTMLElement} */ (notes).focus();
    press('ArrowLeft');
    expect(notes?.getAttribute('aria-expanded')).toBe('false');
    press('ArrowDown');
    expect(document.activeElement).toBe(rootFile);
    press('ArrowUp');
    expect(document.activeElement).toBe(notes);
    press('ArrowRight');
    press('ArrowDown');
    press('ArrowDown');
    /** @type {HTMLButtonElement} */ (first).click();
    await vi.waitFor(() => expect(memoryApi.read).toHaveBeenCalledWith(
      'available', 'notes/deep/first.md', expect.any(AbortSignal)
    ));
    expect(first.getAttribute('aria-current')).toBe('true');
    expect(otherFile.getAttribute('tabindex')).toBe('-1');
  });

  it('preserves file-pane focus when an all-campaign file finishes loading', async () => {
    memoryApi.listAll.mockResolvedValue([{
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [{ path: 'notes/ambient.md', oid: 'b'.repeat(40), size: 9 }],
      omitted: {},
    }]);
    /** @type {(value: { content: string }) => void} */
    let finishRead = () => {};
    memoryApi.read
      .mockReturnValueOnce(new Promise((resolve) => {
        finishRead = resolve;
      }));
    const rendered = renderAllCampaignMemory({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [{ campaign: 'ambient-context', 'campaign-name': 'Ambient Context' }],
          metadata: {
            'source-id': 'campaign-memory-test',
            'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z',
            'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available',
          },
        },
      },
      contextDetails: [],
      headingTag: 'h3',
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelector('.campaign-memory-file')).not.toBeNull());

    /** @type {HTMLDetailsElement} */ (rendered.querySelector('.cao-memory-campaign-branch')).open = true;
    /** @type {HTMLButtonElement} */ (rendered.querySelector('.campaign-memory-file')).click();
    await vi.waitFor(() => expect(memoryApi.read).toHaveBeenCalledTimes(1));
    const content = /** @type {HTMLElement} */ (rendered.querySelector('.cao-memory-file-content'));
    content.focus();
    finishRead({ content: '# Ambient' });

    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent).toBe('# Ambient'));
    expect(document.activeElement).toBe(content);
  });

  it('cancels an in-flight all-campaign file fetch when another file is selected', async () => {
    memoryApi.listAll.mockResolvedValue([{
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [
        { path: 'notes/first.md', oid: 'b'.repeat(40), size: 9 },
        { path: 'notes/second.md', oid: 'c'.repeat(40), size: 9 },
      ],
      omitted: {},
    }]);
    /** @type {(value: { content: string }) => void} */
    let finishFirstRead = () => {};
    memoryApi.read
      .mockImplementationOnce((_campaign, _path, signal) => new Promise((resolve, reject) => {
        finishFirstRead = resolve;
        signal.addEventListener('abort', () => reject(Object.assign(new Error('Aborted'), { name: 'AbortError' })));
      }))
      .mockResolvedValueOnce({ content: '# Second' });

    const rendered = renderAllCampaignMemory({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [{ campaign: 'ambient-context', 'campaign-name': 'Ambient Context' }],
          metadata: {
            'source-id': 'campaign-memory-test',
            'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z',
            'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available',
          },
        },
      },
      contextDetails: [],
      headingTag: 'h3',
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelectorAll('.campaign-memory-file')).toHaveLength(2));

    /** @type {HTMLDetailsElement} */ (rendered.querySelector('.cao-memory-campaign-branch')).open = true;
    const [firstButton, secondButton] = [...rendered.querySelectorAll('.campaign-memory-file')];
    /** @type {HTMLButtonElement} */ (firstButton).click();
    await vi.waitFor(() => expect(memoryApi.read).toHaveBeenCalledTimes(1));
    expect(firstButton.getAttribute('aria-current')).toBe('true');

    /** @type {HTMLButtonElement} */ (secondButton).click();
    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent).toBe('# Second'));
    expect(firstButton.hasAttribute('aria-current')).toBe(false);
    expect(secondButton.getAttribute('aria-current')).toBe('true');

    // The superseded fetch resolving afterwards must not overwrite the newer content.
    finishFirstRead({ content: '# First' });
    await Promise.resolve();
    expect(rendered.querySelector('pre')?.textContent).toBe('# Second');
  });

  it('shows campaigns without memory entries as non-interactive disabled rows', async () => {
    memoryApi.listAll.mockResolvedValue([null,
      { branch: 'memory/empty', commit: 'a'.repeat(40), files: [], omitted: {} },
      {
        branch: 'memory/available',
        commit: 'b'.repeat(40),
        files: [{ path: 'notes.md', oid: 'c'.repeat(40), size: 5 }],
        omitted: {},
      }]);
    const rendered = renderAllCampaignMemory({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [
            { campaign: 'missing', 'campaign-name': 'Missing' },
            { campaign: 'empty', 'campaign-name': 'Empty' },
            { campaign: 'available', 'campaign-name': 'Available' },
          ],
          metadata: {
            'source-id': 'campaign-memory-test',
            'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z',
            'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available',
          },
        },
      },
      contextDetails: [],
      headingTag: 'h3',
    });
    document.body.append(rendered);

    await vi.waitFor(() => expect(rendered.querySelectorAll('.cao-memory-campaign-disabled')).toHaveLength(2));
    expect([...rendered.querySelectorAll('.cao-memory-campaign-disabled')].map((row) => row.textContent))
      .toEqual(['Missing', 'Empty']);
    expect([...rendered.querySelectorAll('.cao-memory-campaign-disabled')].every((row) =>
      row.getAttribute('aria-disabled') === 'true' && row.querySelector('summary') === null
    )).toBe(true);
    expect(rendered.querySelectorAll('.cao-memory-campaign-branch')).toHaveLength(1);
    expect(rendered.querySelector('.cao-memory-campaign-branch')?.hasAttribute('open')).toBe(false);
    expect(memoryApi.read).not.toHaveBeenCalled();
    expect(memoryApi.listAll).toHaveBeenCalledTimes(1);
  });

  it('restores the campaign browser instead of an empty mobile file pane', async () => {
    window.history.replaceState({ caoMemoryViewer: 'all-campaigns', baseline: true }, '', window.location.href);
    memoryApi.listAll.mockResolvedValue([{
      branch: 'memory/available',
      commit: 'a'.repeat(40),
      files: [{ path: 'notes.md', oid: 'b'.repeat(40), size: 5 }],
      omitted: {},
    }]);
    const rendered = renderAllCampaignMemory({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [{ campaign: 'available', 'campaign-name': 'Available' }],
          metadata: {
            'source-id': 'campaign-memory-test',
            'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z',
            'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available',
          },
        },
      },
      contextDetails: [],
      headingTag: 'h3',
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelector('.campaign-memory-file')).not.toBeNull());

    expect(rendered.querySelector('.cao-memory-layout')?.getAttribute('data-memory-view')).toBe('browser');
    expect(window.history.state).toEqual({ baseline: true });
    expect(memoryApi.read).not.toHaveBeenCalled();
    window.history.replaceState(null, '', window.location.href);
  });

  it('keeps available campaigns browsable when another campaign fails to load', async () => {
    memoryApi.listAll.mockResolvedValue([
      { error: new Error('Temporarily unavailable') },
      {
        branch: 'memory/available',
        commit: 'a'.repeat(40),
        files: [{ path: 'notes.md', oid: 'b'.repeat(40), size: 5 }],
        omitted: {},
      },
    ]);
    const rendered = renderAllCampaignMemory({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [
            { campaign: 'unavailable', 'campaign-name': 'Unavailable' },
            { campaign: 'available', 'campaign-name': 'Available' },
          ],
          metadata: {
            'source-id': 'campaign-memory-test',
            'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z',
            'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available',
          },
        },
      },
      contextDetails: [],
      headingTag: 'h3',
    });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelector('.campaign-memory-file')).not.toBeNull());

    const branches = rendered.querySelectorAll('.cao-memory-campaign-branch');
    expect(branches[0].textContent).toContain('Temporarily unavailable');
    expect(branches[1].querySelector('.campaign-memory-file')?.textContent).toContain('notes.md');
    expect(rendered.querySelector('.cao-memory-campaign-disabled')).toBeNull();
  });

  it('renders an honest empty state when no campaigns are registered', () => {
    const rendered = renderAllCampaignMemory({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {},
      contextDetails: [],
      headingTag: 'h3',
    });

    expect(rendered.textContent).toBe('No campaigns are registered.');
    expect(memoryApi.list).not.toHaveBeenCalled();
  });

  it('shares JSONL tabs with the campaign viewer and preserves them across pane navigation', async () => {
    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context', commit: 'a'.repeat(40), omitted: {},
      files: [
        { path: 'records.jsonl', oid: 'b'.repeat(40), size: 9 },
        { path: 'notes.md', oid: 'c'.repeat(40), size: 7 },
      ],
    });
    memoryApi.read.mockImplementation((_campaign, path) =>
      Promise.resolve({ content: path === 'records.jsonl' ? '{"id":1}\n' : '# Notes' }));
    const root = renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(root);
    await vi.waitFor(() => expect(root.querySelector('[data-tab-value="table"]')).not.toBeNull());
    /** @type {HTMLButtonElement} */ (root.querySelector('[data-tab-value="table"]')).click();
    await vi.waitFor(() => expect(root.querySelector('table tbody')?.textContent).toBe('11'));
    const viewer = root.querySelector('.memory-file-viewer');
    /** @type {HTMLButtonElement} */ (root.querySelector('.campaign-memory-file')).click();
    expect(root.querySelector('.memory-file-viewer')).toBe(viewer);
    expect(root.querySelector('[data-tab-value="table"]')?.getAttribute('aria-selected')).toBe('true');
    /** @type {HTMLButtonElement} */ (root.querySelector('[data-tab-value="raw"]')).click();
    expect(root.querySelector('[data-tab-value="raw"]')?.getAttribute('aria-selected')).toBe('true');
    /** @type {HTMLButtonElement} */ (root.querySelectorAll('.campaign-memory-file')[1]).click();
    await vi.waitFor(() => expect(root.querySelector('pre')?.textContent).toBe('# Notes'));
    expect(root.querySelector('[role="tablist"]')).toBeNull();
  });

  it('loads the static manifest and browses campaign files', async () => {
    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [
        { path: 'notes/first.json', oid: 'b'.repeat(40), size: 14 },
        { path: 'summary.md', oid: 'c'.repeat(40), sha256: 'e'.repeat(64), size: 8 },
      ],
      omitted: {
        fileLimit: 0, fileSize: 0, extension: 0, nesting: 0, unsafePath: 0, unsupportedType: 0
      },
    });
    memoryApi.read
      .mockResolvedValueOnce({ content: '{"answer":42}\n' })
      .mockResolvedValueOnce({ content: '# Summary' });

    const rendered = renderCampaignMemory({
      campaignId: 'ambient-context',
      campaignName: 'Ambient Context',
    });
    document.body.append(rendered);

    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent).toBe('{\n  "answer": 42\n}\n'));
    expect(rendered.querySelector('.campaign-memory-layout')?.getAttribute('data-memory-view')).toBe('browser');
    expect(rendered.querySelector('.campaign-memory-branch')?.textContent).toContain('memory/ambient-context');
    expect([...rendered.querySelectorAll('.campaign-memory-file span')].map((node) => node.textContent))
      .toEqual(['notes/first.json', 'summary.md']);

    rendered.querySelectorAll('button')[1].click();
    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent).toBe('# Summary'));
    expect(rendered.querySelector('.campaign-memory-layout')?.getAttribute('data-memory-view')).toBe('file');
    await vi.waitFor(() => expect(document.activeElement).toBe(rendered.querySelector('.campaign-memory-content')));
    expect(rendered.querySelector('.memory-mobile-back')).toBeNull();
    expect(memoryApi.read.mock.calls.map(([campaign, path]) => [campaign, path])).toEqual([
      ['ambient-context', 'notes/first.json'],
      ['ambient-context', 'summary.md'],
    ]);
  });

  it('uses content icons without implying diff status and retains full file metadata', async () => {
    const files = [
      { path: 'notes/README.MD', icon: 'markdown', size: 9, sizeLabel: '9 B' },
      { path: 'notes/data.json', icon: 'file-code', size: 1024, sizeLabel: '1.0 KiB' },
      { path: 'notes/events.jsonl', icon: 'file-code', size: 1024 ** 2, sizeLabel: '1.0 MiB' },
      { path: 'config/settings.yaml', icon: 'file-code', size: 0, sizeLabel: '0 B' },
      { path: 'config/settings.YML', icon: 'file-code', size: 5, sizeLabel: '5 B' },
      { path: 'notes/plain.txt', icon: 'file', size: 8, sizeLabel: '8 B' },
      { path: 'notes/LICENSE', icon: 'file', size: 12, sizeLabel: '12 B' },
    ];
    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: files.map(({ path, size }) => ({ path, size, oid: 'b'.repeat(40) })),
      omitted: {},
    });
    memoryApi.read.mockResolvedValue({ content: '# Memory' });
    const rendered = renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(rendered);

    await vi.waitFor(() => expect(rendered.querySelectorAll('.campaign-memory-file')).toHaveLength(files.length));
    [...rendered.querySelectorAll('.campaign-memory-file')].forEach((button, index) => {
      const { path, icon, sizeLabel } = files[index];
      expect(button.querySelector(`.octicon-${icon}`)?.getAttribute('aria-hidden')).toBe('true');
      expect(button.querySelector('svg')?.childElementCount).toBeGreaterThan(0);
      expect(button.querySelector('.memory-file-name')?.textContent).toBe(path);
      expect(button.getAttribute('title')).toBe(`${path} (${sizeLabel})`);
      expect(button.getAttribute('aria-label')).toBe(`${path}, ${sizeLabel}`);
      expect(button.querySelector('small')).toBeNull();
    });
    expect(rendered.querySelector('[class*="octicon-diff-"]')).toBeNull();
  });

  it('uses browser history and the app chrome parent on mobile', async () => {
    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [{ path: 'notes/mobile.md', oid: 'b'.repeat(40), size: 9 }],
      omitted: {},
    });
    memoryApi.read.mockResolvedValue({ content: '# Mobile' });
    const originalMatchMedia = window.matchMedia;
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn(() => ({ matches: true }))
    });
    window.history.replaceState({ baseline: true }, '', window.location.href);
    const page = document.createElement('section');
    page.className = 'dashboard-page';
    page.dataset.routeNavigationPage = 'campaigns';
    page.addEventListener('dashboard-route-parent-change', (event) => {
      if (event instanceof CustomEvent) page.dataset.routeNavigationPage = event.detail.navigationPage;
    });
    const rendered = renderCampaignMemory({
      campaignId: 'ambient-context',
      campaignName: 'Ambient Context',
    });
    page.append(rendered);
    document.body.append(page);
    await vi.waitFor(() => expect(rendered.querySelector('.campaign-memory-file')).not.toBeNull());

    /** @type {HTMLButtonElement} */ (rendered.querySelector('.campaign-memory-file')).click();
    await vi.waitFor(() => expect(page.dataset.routeNavigationPage).toBe(''));
    expect(window.history.state).toMatchObject({ baseline: true, caoMemoryViewer: 'campaign:ambient-context' });
    expect(rendered.querySelector('.memory-mobile-back')).toBeNull();

    window.history.replaceState({ baseline: true }, '', window.location.href);
    window.dispatchEvent(new CustomEvent('dashboard-history-change'));
    await vi.waitFor(() => expect(page.dataset.routeNavigationPage).toBe('campaigns'));
    expect(rendered.querySelector('.campaign-memory-layout')?.getAttribute('data-memory-view')).toBe('browser');
    Object.defineProperty(window, 'matchMedia', { configurable: true, value: originalMatchMedia });
    window.history.replaceState(null, '', window.location.href);
  });

  it('renders honest empty and invalid-manifest states', async () => {
    memoryApi.list.mockResolvedValueOnce(null);
    const empty = renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(empty);
    await vi.waitFor(() => expect(empty.textContent).toContain(
      'No repository-memory branch has been published'
    ));
    empty.remove();

    memoryApi.list.mockRejectedValueOnce(new Error('Repository-memory manifest is invalid.'));
    const invalid = renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(invalid);
    await vi.waitFor(() => expect(invalid.textContent).toContain('Repository-memory manifest is invalid.'));
  });

  it('renders empty branches and warns when excluded files make the view incomplete', async () => {
    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [],
      omitted: {
        fileLimit: 1,
        fileSize: 2,
        totalSize: 1,
        extension: 3,
        nesting: 0,
        unsafePath: 0,
        invalidContent: 1,
        unsupportedType: 0,
      },
    });
    const rendered = renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(rendered);

    await vi.waitFor(() => expect(rendered.textContent).toContain(
      'The repository-memory branch contains no supported files.'
    ));
    expect(rendered.querySelector('.campaign-memory-warning')?.textContent).toContain(
      'This view does not represent the entire memory branch'
    );
    expect(rendered.querySelector('.campaign-memory-warning')?.textContent).toContain(
      '1 excluded by the file-count limit, 2 excluded by the file-size limit'
    );
    expect(rendered.querySelector('.campaign-memory-warning')?.textContent).toContain(
      '3 excluded by unsupported file extensions'
    );
    expect(rendered.querySelector('.campaign-memory-warning')?.textContent).toContain(
      '1 excluded by invalid text content'
    );
    expect(rendered.querySelector('.campaign-memory-warning')?.textContent).toContain(
      '1 excluded by the total-size limit'
    );
  });

  it('rejects file metadata outside the publication limits', async () => {
    memoryApi.list.mockRejectedValue(new Error('Campaign repository-memory file metadata is invalid.'));
    const rendered = renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(rendered);

    await vi.waitFor(() => expect(rendered.textContent).toContain(
      'Campaign repository-memory file metadata is invalid.'
    ));
  });

  it('stops reading files that exceed the size limit', async () => {
    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [{ path: 'large.txt', oid: 'b'.repeat(40), sha256: 'c'.repeat(64), size: 10 }],
      omitted: {
        fileLimit: 0, fileSize: 0, extension: 0, nesting: 0, unsafePath: 0, unsupportedType: 0
      },
    });
    memoryApi.read.mockRejectedValue(new Error('Memory file exceeds the published size limit.'));
    const rendered = renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(rendered);

    await vi.waitFor(() => expect(rendered.textContent).toContain(
      'Memory file exceeds the published size limit.'
    ));
  });

  it('stays silent by default and logs only scalar metadata under its predictable category', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=campaign-memory', output })
      };
    });
    vi.resetModules();
    const { renderCampaignMemory: renderCampaignMemoryWithDebug } = await import('../../src/components/campaign-memory.js');

    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [{ path: 'notes/first.json', oid: 'b'.repeat(40), size: 14 }],
      omitted: {
        fileLimit: 0, fileSize: 0, extension: 0, nesting: 0, unsafePath: 0, unsupportedType: 0
      },
    });
    memoryApi.read.mockResolvedValueOnce({ content: '{"answer":42}\n' });

    const rendered = renderCampaignMemoryWithDebug({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(rendered);

    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith(
      '[cao:campaign-memory]',
      { operation: 'list-manifest', status: 'ready', fileCount: 1 }
    ));
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith(
      '[cao:campaign-memory]',
      { operation: 'read-file', status: 'ready', contentLength: '{"answer":42}\n'.length }
    ));

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }

    rendered.remove();
    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('logs a read-file error with only the error name, never the message', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=campaign-memory', output })
      };
    });
    vi.resetModules();
    const { renderCampaignMemory: renderCampaignMemoryWithDebug } = await import('../../src/components/campaign-memory.js');

    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [{ path: 'large.txt', oid: 'b'.repeat(40), sha256: 'c'.repeat(64), size: 10 }],
      omitted: {
        fileLimit: 0, fileSize: 0, extension: 0, nesting: 0, unsafePath: 0, unsupportedType: 0
      },
    });
    memoryApi.read.mockRejectedValue(new Error('Memory file exceeds the published size limit.'));

    const rendered = renderCampaignMemoryWithDebug({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(rendered);

    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith(
      '[cao:campaign-memory]',
      { operation: 'read-file', status: 'error', errorName: 'Error' }
    ));
    const loggedValues = output.debug.mock.calls.flatMap((call) => Object.values(call[1]));
    expect(loggedValues.some((value) => typeof value === 'string' && value.includes('published size limit'))).toBe(false);

    rendered.remove();
    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
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
    const { renderCampaignMemory: renderCampaignMemoryWithoutDebug } = await import('../../src/components/campaign-memory.js');

    memoryApi.list.mockResolvedValue({
      branch: 'memory/ambient-context',
      commit: 'a'.repeat(40),
      files: [{ path: 'notes/first.json', oid: 'b'.repeat(40), size: 14 }],
      omitted: {
        fileLimit: 0, fileSize: 0, extension: 0, nesting: 0, unsafePath: 0, unsupportedType: 0
      },
    });
    memoryApi.read.mockResolvedValueOnce({ content: '{"answer":42}\n' });

    const rendered = renderCampaignMemoryWithoutDebug({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
    document.body.append(rendered);
    await vi.waitFor(() => expect(rendered.querySelector('pre')?.textContent).toBe('{\n  "answer": 42\n}\n'));

    expect(output.debug).not.toHaveBeenCalled();

    rendered.remove();
    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  describe('failure diagnostics', () => {
    const SENSITIVE = 'secret-token-ghp_123 /private/path.md';
    const campaignContext = () => /** @type {import('../../src/components/ui-elements.js').ElementRenderContext} */ (/** @type {unknown} */ ({
      pageId: 'memory',
      title: 'Campaign memory',
      sourceNames: ['campaign-memory-campaigns'],
      sources: {
        'campaign-memory-campaigns': {
          source: 'campaign-memory-campaigns',
          rows: [{ campaign: 'ambient-context', 'campaign-name': 'Ambient Context' }],
          metadata: {
            'source-id': 'campaign-memory-test',
            'source-kind': 'fixture',
            'as-of': '2026-09-26T00:00:00Z',
            'retrieved-at': '2026-09-26T00:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available',
          },
        },
      },
      contextDetails: [],
      headingTag: 'h3',
    }));

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
      return { output, module: await import('../../src/components/campaign-memory.js') };
    }

    afterEach(() => {
      vi.doUnmock('../../src/debug.js');
      vi.resetModules();
    });

    it('logs a single-campaign manifest failure with only the error name', async () => {
      const { output, module } = await loadWithDebug('?debug=campaign-memory');
      memoryApi.list.mockRejectedValue(new TypeError(SENSITIVE));

      const rendered = module.renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
      document.body.append(rendered);

      await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith(
        '[cao:campaign-memory]',
        { operation: 'list-manifest', status: 'error', errorName: 'TypeError' }
      ));
      expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret-token');
    });

    it('logs an all-campaign listing failure with a count and sanitized error name', async () => {
      const { output, module } = await loadWithDebug('?debug=campaign-memory');
      const failure = new Error(SENSITIVE);
      failure.name = 'CustomSecretError';
      memoryApi.listAll.mockRejectedValue(failure);

      const rendered = module.renderAllCampaignMemory(campaignContext());
      document.body.append(rendered);

      await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith(
        '[cao:campaign-memory]',
        { operation: 'list-campaigns', status: 'error', campaignCount: 1, errorName: 'UnknownError' }
      ));
      expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret-token');
      expect(JSON.stringify(output.debug.mock.calls)).not.toContain('CustomSecretError');
    });

    it('logs an all-campaign file read failure without the path or message', async () => {
      const { output, module } = await loadWithDebug('?debug=campaign-memory');
      memoryApi.listAll.mockResolvedValue([{
        branch: 'memory/ambient-context',
        commit: 'a'.repeat(40),
        files: [{ path: 'private/path.md', oid: 'b'.repeat(40), size: 5 }],
        omitted: {},
      }]);
      memoryApi.read.mockRejectedValue(new RangeError(SENSITIVE));

      const rendered = module.renderAllCampaignMemory(campaignContext());
      document.body.append(rendered);
      await vi.waitFor(() => expect(rendered.querySelector('.campaign-memory-file')).not.toBeNull());
      /** @type {HTMLButtonElement} */ (rendered.querySelector('.campaign-memory-file')).click();

      await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith(
        '[cao:campaign-memory]',
        { operation: 'read-file', status: 'error', scope: 'all-campaigns', errorName: 'RangeError' }
      ));
      const logged = JSON.stringify(output.debug.mock.calls);
      expect(logged).not.toContain('secret-token');
      expect(logged).not.toContain('private/path.md');
    });

    it('emits nothing for the failures when the category is not selected or is excluded', async () => {
      for (const search of ['', '?debug=other-category', '?debug=*,-campaign-memory']) {
        const { output, module } = await loadWithDebug(search);
        memoryApi.list.mockRejectedValue(new Error(SENSITIVE));
        memoryApi.listAll.mockRejectedValue(new Error(SENSITIVE));

        const single = module.renderCampaignMemory({ campaignId: 'ambient-context', campaignName: 'Ambient Context' });
        const all = module.renderAllCampaignMemory(campaignContext());
        document.body.append(single, all);
        await vi.waitFor(() => expect(single.textContent).toContain(SENSITIVE));
        await vi.waitFor(() => expect(all.textContent).toContain(SENSITIVE));

        expect(output.debug.mock.calls.filter(([prefix]) => prefix === '[cao:campaign-memory]')).toEqual([]);
        single.remove();
        all.remove();
      }
    });
  });
});
