import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { browserFirstLoad, showBrowserFirstLoad, recordPreparationEvent, recordPreparationStorage, resetPreparationDiagnostics } from '../../src/browser-first-load.js';
import { mountFirstLoadOverlay } from '../../src/components/first-load-overlay.js';
import { renderFactoryHeader } from '../../src/components/factory-header.js';
import { scopedStorageKey } from '../../src/storage-scope.js';
import { createFirstLoadMessagePicker, FIRST_LOAD_MESSAGES, FIRST_LOAD_MESSAGE_INTERVAL_MS } from '../../src/components/first-load-messages.js';

let owner = new AbortController();

beforeEach(() => {
  owner = new AbortController();
  document.body.replaceChildren();
  resetPreparationDiagnostics();
  browserFirstLoad.set({ status: 'loading', dismissed: false });
});

afterEach(() => {
  owner.abort();
  browserFirstLoad.set({ status: 'inactive', dismissed: false });
  localStorage.removeItem(scopedStorageKey('central-agentic-ops.dashboard.theme'));
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('browser first-load presentation', () => {
  it('contains exactly 100 distinct messages and visits every message once per randomized cycle', () => {
    expect(FIRST_LOAD_MESSAGES).toHaveLength(100);
    expect(new Set(FIRST_LOAD_MESSAGES).size).toBe(100);
    for (const message of FIRST_LOAD_MESSAGES) {
      expect(message).toBe(message.trim());
      expect(message.length).toBeLessThanOrEqual(72);
      expect(message).toMatch(/^[\x20-\x7e]+$/);
    }
    vi.spyOn(Math, 'random').mockReturnValue(0);
    const next = createFirstLoadMessagePicker();
    const firstCycle = Array.from({ length: 100 }, next);
    const secondCycle = Array.from({ length: 100 }, next);
    expect(new Set(firstCycle).size).toBe(100);
    expect(new Set(secondCycle).size).toBe(100);
    expect(firstCycle.toSorted()).toEqual(FIRST_LOAD_MESSAGES.toSorted());
    expect(secondCycle[0]).not.toBe(firstCycle.at(-1));
    expect(firstCycle).not.toEqual(FIRST_LOAD_MESSAGES);
  });

  it('rotates at six-second intervals without progress updates restarting the timer', () => {
    vi.useFakeTimers();
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    const message = document.querySelector('.first-load-message');
    const original = message?.textContent;
    expect(FIRST_LOAD_MESSAGES).toContain(original);
    expect(message?.getAttribute('aria-live')).toBe('off');
    vi.advanceTimersByTime(FIRST_LOAD_MESSAGE_INTERVAL_MS / 2);
    browserFirstLoad.set({ status: 'loading', dismissed: false, completed: 1, total: 5 });
    vi.advanceTimersByTime(FIRST_LOAD_MESSAGE_INTERVAL_MS / 2);
    expect(message?.textContent).not.toBe(original);
    expect(document.querySelector('[role="status"]')?.textContent).toContain('Importing activity files');
  });

  it('pauses rotation when dismissed or failed and tears down permanently when complete', () => {
    vi.useFakeTimers();
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    const message = document.querySelector('.first-load-message');
    browserFirstLoad.set({ status: 'loading', dismissed: true });
    const original = message?.textContent;
    vi.advanceTimersByTime(FIRST_LOAD_MESSAGE_INTERVAL_MS * 2);
    expect(message?.textContent).toBe(original);
    expect(vi.getTimerCount()).toBe(0);
    showBrowserFirstLoad();
    vi.advanceTimersByTime(FIRST_LOAD_MESSAGE_INTERVAL_MS);
    expect(message?.textContent).not.toBe(original);
    browserFirstLoad.set({ status: 'failed', dismissed: false });
    expect(message?.hasAttribute('hidden')).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
    browserFirstLoad.set({ status: 'inactive', dismissed: false });
    expect(vi.getTimerCount()).toBe(0);
    expect(document.querySelector('dialog')).toBeNull();
    browserFirstLoad.set({ status: 'loading', dismissed: false });
    expect(document.querySelector('dialog')).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
    owner.abort();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('shows the same live ingestion message, history and action as the bottom notification', () => {
    vi.useFakeTimers();
    const cancel = vi.fn();
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    browserFirstLoad.set((current) => ({ ...current, ingestion: {
      id: 'ingestion-progress-1',
      message: '750 KB/1.5 MB · 3s remaining',
      detailsSubtitle: 'Downloading and processing a local copy.',
      details: ['Preparing data... +0s', 'Parsing overall: 1,000 rec. +3s'],
      actions: [{ label: 'Cancel', run: cancel }]
    } }));
    const dialog = document.querySelector('dialog');
    const details = dialog?.querySelector('.first-load-ingestion-details');
    expect(dialog?.querySelector('.first-load-message')?.textContent).toBe('750 KB/1.5 MB · 3s remaining');
    expect(details?.hasAttribute('hidden')).toBe(false);
    details?.querySelector('summary')?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(details?.querySelector('.dashboard-notification-details-subtitle')?.textContent).toBe('Downloading and processing a local copy.');
    expect([...details?.querySelectorAll('li') ?? []].map((item) => item.textContent)).toEqual([
      'Preparing data... +0s', 'Parsing overall: 1,000 rec. +3s'
    ]);
    details?.querySelector('button')?.click();
    expect(cancel).toHaveBeenCalledOnce();
    browserFirstLoad.set((current) => ({ ...current, ingestion: {
      ...current.ingestion, id: 'ingestion-progress-1', message: '1.5 MB/1.5 MB · 0s remaining',
      details: ['Storing 1,000/1,000 rec. +4s'], actions: []
    } }));
    expect(dialog?.querySelector('.first-load-message')?.textContent).toBe('1.5 MB/1.5 MB · 0s remaining');
    expect(details?.querySelectorAll('li')).toHaveLength(1);
    expect(details?.querySelector('button')).toBeNull();
    browserFirstLoad.set((current) => ({ ...current, ingestion: undefined }));
    expect(details?.hasAttribute('hidden')).toBe(true);
    browserFirstLoad.set({ status: 'inactive', dismissed: false });
    expect(document.querySelector('.first-load-ingestion-details')).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not mount after data is already loaded or its owner is aborted', () => {
    vi.useFakeTimers();
    browserFirstLoad.set({ status: 'inactive', dismissed: false });
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    expect(document.querySelector('dialog')).toBeNull();
    browserFirstLoad.set({ status: 'loading', dismissed: false });
    owner.abort();
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    expect(document.querySelector('dialog')).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('loads mascot artwork relative to a dashboard deployed under a subpath', () => {
    const base = document.createElement('base');
    base.href = '/gh-cao/';
    document.head.append(base);
    try {
      mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
      expect([...document.querySelectorAll('.first-load-agentic-rider')].map((image) =>
        new URL(/** @type {HTMLImageElement} */ (image).src).pathname
      )).toEqual(['duck', 'octocat', 'copilot'].map((name) => `/gh-cao/src/assets/loaders/${name}.svg`));
    } finally {
      base.remove();
    }
  });

  it('explains the import, reports all preparation stages, and dismisses without cancelling', () => {
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    const dialog = document.querySelector('dialog');
    expect(dialog?.open).toBe(true);
    expect(dialog?.getAttribute('aria-label')).toBe('Preparing your dashboard');
    expect(dialog?.querySelector('header')?.textContent).toBe('Central Agentic Ops');
    expect(dialog?.querySelector('.first-load-brand svg')?.getAttribute('aria-hidden')).toBe('true');
    expect(dialog?.querySelector('.first-load-eyebrow')?.textContent).toBe('Welcome to CAO');
    expect(dialog?.querySelector('h2')?.textContent).toContain('Your dashboard is taking shape.');
    expect(dialog?.textContent).toContain('build a local database');
    expect(dialog?.querySelector('.first-load-description .first-load-compact-copy')?.textContent)
      .toBe('Bringing your campaign activity together for a first look.');
    const about = dialog?.querySelector('.first-load-about');
    expect(about?.hasAttribute('open')).toBe(false);
    expect(about?.querySelector('summary')?.textContent).toBe('About this preparation');
    expect(about?.textContent).toContain('build a local database');
    expect(about?.querySelector('.first-load-steps')).not.toBeNull();
    const serverOption = dialog?.querySelector('.first-load-server-option');
    expect(serverOption?.textContent).toContain('deploy a CAO backend server');
    expect(serverOption?.textContent).toContain('avoid this browser import');
    expect([...serverOption?.querySelectorAll('a') ?? []].map((link) => [link.textContent, link.href])).toEqual([
      ['deployment options', 'https://githubnext.github.io/gh-cao/deployment/']
    ]);
    for (const link of serverOption?.querySelectorAll('a') ?? []) {
      expect(link.target).toBe('_blank');
      expect(link.rel).toBe('noopener noreferrer');
      expect(link.getAttribute('aria-label')).toBe(`${link.textContent} (opens in a new tab)`);
    }
    expect(dialog?.querySelector('.first-load-background')?.getAttribute('aria-hidden')).toBe('true');
    expect(dialog?.querySelector('.first-load-background')?.childElementCount).toBe(0);
    expect(dialog?.querySelector('progress')).toBeNull();
    const loader = dialog?.querySelector('.first-load-agentic-loader');
    expect(loader?.getAttribute('aria-hidden')).toBe('true');
    expect(loader?.querySelectorAll('.first-load-agentic-rhythm i')).toHaveLength(13);
    expect([...loader?.querySelectorAll('.first-load-agentic-rider') ?? []].map((image) =>
      image.className
    )).toEqual(['first-load-agentic-rider first-load-agentic-rider-duck',
      'first-load-agentic-rider first-load-agentic-rider-octocat',
      'first-load-agentic-rider first-load-agentic-rider-copilot']);
    expect([...loader?.querySelectorAll('img') ?? []].map((image) =>
      new URL(/** @type {HTMLImageElement} */ (image).src).pathname
    )).toEqual(['duck', 'octocat', 'copilot'].map((name) => `/src/assets/loaders/${name}.svg`));
    browserFirstLoad.set({ status: 'loading', dismissed: false, completed: 2, total: 5 });
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Importing activity files');
    browserFirstLoad.set({ status: 'loading', dismissed: false, completed: 2.5, total: 5, stage: 'maintenance' });
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Applying retention limits');
    browserFirstLoad.set({ status: 'loading', dismissed: false, completed: 3, total: 5, stage: 'inventory' });
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Preparing inventory');
    browserFirstLoad.set({ status: 'loading', dismissed: false, completed: 4, total: 5, stage: 'queries' });
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Refreshing dashboard queries');
    const browse = dialog?.querySelector('.first-load-browse');
    if (!(browse instanceof HTMLButtonElement)) throw new Error('Browse button is missing.');
    expect(browse.textContent).toBe('Explore data');
    expect(dialog?.querySelector('.first-load-close')).toBeNull();
    expect(dialog?.querySelectorAll('button')).toHaveLength(3);
    expect(dialog?.textContent).toContain('The import continues as you explore.');
    browse.click();
    expect(dialog?.open).toBe(false);
    expect(browserFirstLoad.get().status).toBe('loading');
    browserFirstLoad.set((current) => ({ ...current, completed: 3 }));
    expect(dialog?.open).toBe(false);
    showBrowserFirstLoad();
    expect(dialog?.open).toBe(true);
    expect(dialog?.querySelector('header')?.textContent).toBe('Central Agentic Ops');
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Refreshing dashboard queries');
    browserFirstLoad.set({ status: 'inactive', dismissed: false });
    expect(dialog?.open).toBe(false);
    expect(document.querySelector('dialog')).toBeNull();
    owner.abort();
    expect(document.querySelector('dialog')).toBeNull();
  });

  it('explains a database upgrade without claiming this is a first visit', () => {
    browserFirstLoad.set({ status: 'loading', dismissed: false, reason: 'upgrade', oldVersion: 36, newVersion: 37 });
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    const dialog = document.querySelector('dialog');
    expect(dialog?.open).toBe(true);
    expect(dialog?.querySelector('.first-load-eyebrow')?.textContent).toBe('Dashboard update');
    expect(dialog?.querySelector('h2')?.textContent).toContain('Updating your dashboard');
    expect(dialog?.querySelector('.first-load-description')?.textContent).toContain('refreshing your browser copy');
    expect(dialog?.textContent).not.toContain('The first import can take');
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Updating the local database');
    expect(dialog?.querySelector('.first-load-reason')?.textContent).toContain('needs a newer browser database format');
    expect(dialog?.querySelector('.first-load-reason button')?.textContent).toContain('Copy preparation details');
    browserFirstLoad.set({ status: 'loading', dismissed: false, reason: 'upgrade', stage: 'files', completed: 1, total: 5 });
    expect(dialog?.querySelector('[role="status"]')?.textContent).toContain('Importing activity files');
    browserFirstLoad.set({ status: 'failed', dismissed: false, reason: 'upgrade' });
    expect(dialog?.querySelector('h2')?.textContent).toContain('dashboard update could not finish');
  });

  it('explains a missing snapshot and copies the full preparation log and storage diagnostics', async () => {
    browserFirstLoad.set({ status: 'loading', dismissed: false, reason: 'missing-snapshot', stage: 'files', completed: 2, total: 5 });
    recordPreparationStorage({ persisted: false, quota: 1000, usage: 100 });
    for (let index = 0; index < 216; index += 1) {
      recordPreparationEvent('ingestion', { message: `Shard ${index + 1} committed.` });
    }
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    const dialog = document.querySelector('dialog');
    expect(dialog?.querySelector('.first-load-reason')?.textContent).toContain('no completed local copy yet');
    const copy = /** @type {HTMLButtonElement | null} */ (dialog?.querySelector('.first-load-reason .first-load-details-link'));
    expect(copy?.type).toBe('button');
    expect(copy?.textContent).toContain('Copy preparation details');
    copy?.click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledOnce());
    const diagnostics = JSON.parse(writeText.mock.calls[0][0]);
    expect(diagnostics).toMatchObject({
      event: 'dashboard-browser-database-repopulation',
      reason: 'missing-snapshot',
      status: 'loading',
      stage: 'files',
      completed: 2,
      total: 5,
      schemaVersionBefore: null,
      schemaVersionAfter: null,
      storage: { persisted: false, quota: 1000, usage: 100 },
      ingestion: null
    });
    expect(diagnostics.log).toHaveLength(217);
    expect(diagnostics.log[1]).toMatchObject({ event: 'ingestion', details: { message: 'Shard 1 committed.' } });
    expect(diagnostics.log.at(-1)).toMatchObject({ event: 'ingestion', details: { message: 'Shard 216 committed.' } });
    expect(diagnostics.log.every((/** @type {{ timestamp: string }} */ entry) => Number.isFinite(Date.parse(entry.timestamp)))).toBe(true);
    await vi.waitFor(() => expect(dialog?.querySelector('.first-load-copy-status')?.textContent).toBe('Preparation details copied.'));
    browserFirstLoad.set({ status: 'failed', dismissed: false, reason: 'upgrade', oldVersion: 36, newVersion: 37 });
    expect(dialog?.querySelector('.first-load-reason')?.textContent).toContain('newer browser database format');
    expect(dialog?.querySelector('.first-load-reason button')).toBe(copy);
    copy?.click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledTimes(2));
    expect(JSON.parse(writeText.mock.calls[1][0])).toMatchObject({
      reason: 'upgrade', status: 'failed', schemaVersionBefore: 36, schemaVersionAfter: 37
    });
  });

  it('reports when copying diagnostics is unavailable', async () => {
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    /** @type {HTMLButtonElement | null} */ (document.querySelector('.first-load-details-link'))?.click();
    await vi.waitFor(() => expect(document.querySelector('.first-load-copy-status')?.textContent).toBe('Could not copy preparation details.'));
  });

  it('keeps import duration honest without duplicating the managed storage notice', () => {
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    recordPreparationStorage({ persisted: false, quota: null, usage: null });
    const dialog = document.querySelector('dialog');
    expect(dialog?.querySelector('.first-load-storage-warning')).toBeNull();
    expect(dialog?.querySelector('.first-load-duration')?.textContent).toContain('download the data again');
    recordPreparationStorage({ persisted: true, quota: 1000, usage: 100 });
    expect(dialog?.textContent).not.toContain('data will be lost when the private session ends');
    expect(dialog?.querySelector('.first-load-duration')?.textContent).toContain('Future visits reuse cached data');
  });

  it('supports Escape dismissal, honest failure output and retry', () => {
    const retry = vi.fn();
    mountFirstLoadOverlay({ document, signal: owner.signal, retry });
    const dialog = document.querySelector('dialog');
    const cancel = new Event('cancel', { cancelable: true });
    dialog?.dispatchEvent(cancel);
    expect(cancel.defaultPrevented).toBe(true);
    expect(dialog?.open).toBe(false);
    browserFirstLoad.set({ status: 'failed', dismissed: false });
    expect(dialog?.open).toBe(true);
    expect(dialog?.textContent).toContain('first import could not finish');
    expect(dialog?.querySelector('.first-load-description')?.textContent).toContain('Your browser copy is not ready yet.');
    expect(dialog?.querySelector('.first-load-message')?.hasAttribute('hidden')).toBe(true);
    expect(dialog?.querySelector('.first-load-duration')?.hasAttribute('hidden')).toBe(true);
    expect(dialog?.querySelector('.first-load-server-option')?.textContent).toContain('deploy a CAO backend server');
    expect(dialog?.querySelector('progress')).toBeNull();
    expect(dialog?.querySelector('.first-load-agentic-loader')?.hasAttribute('hidden')).toBe(true);
    const button = [...dialog?.querySelectorAll('button') ?? []].find((candidate) => candidate.textContent === 'Retry import');
    button?.click();
    expect(retry).toHaveBeenCalledOnce();
    browserFirstLoad.set({ status: 'loading', dismissed: false });
    expect(dialog?.querySelector('.first-load-duration')?.hasAttribute('hidden')).toBe(false);
    expect(dialog?.querySelector('.first-load-agentic-loader')?.hasAttribute('hidden')).toBe(false);
  });

  it('restores the selected theme when opening and reopening the body-owned overlay', () => {
    const key = scopedStorageKey('central-agentic-ops.dashboard.theme');
    localStorage.setItem(key, 'dark');
    mountFirstLoadOverlay({ document, signal: owner.signal, retry: vi.fn() });
    const dialog = document.querySelector('dialog');
    expect(dialog?.dataset.theme).toBe('dark');
    browserFirstLoad.set({ status: 'loading', dismissed: true });
    localStorage.setItem(key, 'light');
    showBrowserFirstLoad();
    expect(dialog?.dataset.theme).toBe('light');
  });

  it('suppresses idle and rhythm throughout the initial import, including after dismissal and failure', () => {
    const sources = {
      status: { rows: () => [{ heading: 'Your factory is idle.' }], pending: () => false, unavailable: () => false },
      rhythm: { rows: () => [], pending: () => false, unavailable: () => false }
    };
    const header = renderFactoryHeader(sources, owner, { presentation: 'status', rhythm: 'rhythm' }, true);
    expect(header.querySelector('h2')?.textContent).toBe('Your dashboard is taking shape.');
    expect(header.querySelector('h2')?.getAttribute('aria-busy')).toBe('true');
    expect(header.querySelector('.factory-rhythm')?.hasAttribute('hidden')).toBe(true);
    expect(header.querySelectorAll('button')).toHaveLength(0);
    browserFirstLoad.set({ status: 'loading', dismissed: true });
    expect(header.textContent).not.toContain('idle');
    expect(header.querySelectorAll('button')).toHaveLength(0);
    browserFirstLoad.set({ status: 'failed', dismissed: true });
    expect(header.querySelector('h2')?.textContent).toBe('Your dashboard import is incomplete.');
    expect(header.querySelector('h2')?.hasAttribute('aria-busy')).toBe(false);
    expect(header.querySelector('p')?.textContent).toBe('Factory status is not available yet.');
    browserFirstLoad.set({ status: 'inactive', dismissed: false });
    expect(header.querySelector('h2')?.textContent).toBe('Your factory is idle.');
    expect(header.querySelector('.factory-rhythm')?.hasAttribute('hidden')).toBe(false);
    expect(header.querySelectorAll('button')).toHaveLength(0);
    owner.abort();
    browserFirstLoad.set({ status: 'loading', dismissed: false });
    expect(header.querySelector('h2')?.textContent).toBe('Your factory is idle.');
  });

  it('does not apply the first-load presentation without declarative opt-in', () => {
    const source = { rows: () => [{ heading: 'Observed campaign status' }], pending: () => false, unavailable: () => false };
    const header = renderFactoryHeader({ status: source, rhythm: source }, owner, { presentation: 'status', rhythm: 'rhythm' });
    expect(header.querySelector('h2')?.textContent).toBe('Observed campaign status');
    expect(header.querySelectorAll('button')).toHaveLength(0);
  });
});
