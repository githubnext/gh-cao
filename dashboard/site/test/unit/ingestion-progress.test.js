import { afterEach, describe, expect, it, vi } from 'vitest';
import { estimateRemainingTime, startIngestionProgress } from '../../src/ingestion-progress.js';

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('data-worker ingestion progress', () => {
  it('predicts remaining processing time with a linear byte model', () => {
    expect(estimateRemainingTime(250_000, 1_000_000, 2_000)).toBe(6_000);
    expect(estimateRemainingTime(0, 1_000_000, 2_000)).toBeNull();
    expect(estimateRemainingTime(1_000_000, 1_000_000, 2_000)).toBeNull();
  });

  it('estimates time from import progress when shard byte sizes are unavailable', () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();
    progress.reportImportProgress(0, 7, 'files');
    progress.setWorkload(undefined);
    vi.advanceTimersByTime(2_000);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toContain('Estimating time remaining');

    progress.update({ bytesProcessed: 0, recordsIngested: 10 });
    progress.reportImportProgress(1, 7, 'files');
    vi.advanceTimersByTime(1_000);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toBe('0 B · 18s remaining');

    progress.reportImportProgress(3, 7, 'inventory');
    vi.advanceTimersByTime(1_000);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toBe('0 B · 6s remaining');
    progress.complete();
  });

  it('excludes reused cached shards from measured import throughput', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();
    progress.reportImportProgress(4, 7, 'files');
    progress.setWorkload(undefined);
    vi.advanceTimersByTime(1_000);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toContain('Estimating time remaining');

    progress.reportImportProgress(5, 7, 'files');
    vi.advanceTimersByTime(1_000);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toBe('0 B · 4s remaining');
    progress.complete();
  });

  it('assumes similar shard sizes and revises the average time as shards finish', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();
    progress.reportImportProgress(0, 7, 'files');
    progress.setWorkload(undefined);

    vi.advanceTimersByTime(1_500);
    progress.reportImportProgress(1, 7, 'files');
    vi.advanceTimersByTime(500);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toBe('0 B · 12s remaining');

    vi.advanceTimersByTime(7_500);
    progress.reportImportProgress(2, 7, 'files');
    vi.advanceTimersByTime(500);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toBe('0 B · 25s remaining');

    vi.advanceTimersByTime(500);
    progress.reportImportProgress(3, 7, 'files');
    vi.advanceTimersByTime(500);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toBe('0 B · 15s remaining');
    progress.complete();
  });

  it('keeps estimating through inventory, maintenance, and query preparation after all bytes are processed', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();
    progress.reportImportProgress(0, 5, 'files');
    progress.setWorkload(2_048);
    vi.advanceTimersByTime(2_000);
    progress.update({ bytesProcessed: 2_048, recordsIngested: 10 });
    for (const [completed, stage] of /** @type {const} */ ([
      [2, 'inventory'], [3, 'maintenance'], [4, 'queries']
    ])) {
      progress.reportImportProgress(completed, 5, stage);
      vi.advanceTimersByTime(1_000);
      const message = postMessage.mock.lastCall?.[0]?.notification.message;
      expect(message).toMatch(/^2\.0 KB\/2\.0 KB · \d+s remaining$/);
      expect(message).not.toContain('0s remaining');
    }
    progress.complete();
  });

  it('preserves byte throughput when the records phase expands the workload', () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();
    progress.setWorkload(1_024);
    vi.advanceTimersByTime(2_000);
    progress.update({ bytesProcessed: 1_024, recordsIngested: 10 });
    progress.setWorkload(4_096);
    vi.advanceTimersByTime(1_000);
    expect(postMessage.mock.lastCall?.[0]?.notification.message).toBe('1.0 KB/4.1 KB · 9s remaining');
    progress.complete();
  });

  it('immediately reports preparation before the first JSONL record is read', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();

    expect(postMessage).toHaveBeenNthCalledWith(2, {
      type: 'notification',
      notification: expect.objectContaining({
        message: 'Preparing data...',
        icon: 'download',
        detailsSubtitle: expect.stringContaining('local copy'),
        details: ['Preparing data... +0s'],
        actions: [
          { label: 'Cancel', operation: 'cancel-data-ingestion', placement: 'details', requestId: undefined }
        ],
        duration: 0
      })
    });
    progress.complete();
  });

  it('stops publishing after completion', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();

    progress.complete();
    vi.advanceTimersByTime(1_000);

    expect(postMessage).toHaveBeenCalledTimes(4);
    expect(postMessage).toHaveBeenNthCalledWith(4, {
      type: 'notification',
      notification: expect.objectContaining({ dismiss: true })
    });
  });

  it('reports the storage phase so long writes never freeze the notification', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();

    progress.setWorkload(1_500_000);
    progress.update({ bytesProcessed: 750_000, recordsIngested: 1_000, totalBytes: 1_500_000 });
    progress.log('Normalizing parsed records.');
    progress.store({ storedRecords: 250, totalRecords: 1_000 });
    vi.advanceTimersByTime(3_000);

    expect(postMessage).toHaveBeenLastCalledWith({
      type: 'notification',
      notification: expect.objectContaining({
        message: '750 KB/1.5 MB · 3s remaining',
        icon: 'download',
        details: [
          'Preparing data... +0s',
          'Parsing overall: 1,000 rec, 750 KB/1.5 MB. +0s',
          'Normalizing parsed records. +0s',
          'Storing 250/1,000 rec. +3s'
        ],
        duration: 0
      })
    });

    progress.store({ storedRecords: 1_000, totalRecords: 1_000 });
    vi.advanceTimersByTime(1_000);
    expect(postMessage).toHaveBeenLastCalledWith({
      type: 'notification',
      notification: expect.objectContaining({
        message: '750 KB/1.5 MB · 4s remaining'
      })
    });

    progress.complete();
    expect(postMessage).toHaveBeenLastCalledWith({
      type: 'notification',
      notification: expect.objectContaining({ dismiss: true })
    });
  });

  it('publishes import state through post-shard preparation without reaching 100% early', () => {
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();

    progress.reportImportProgress(0, 7, 'files');
    progress.reportImportProgress(4, 7, 'maintenance');
    progress.reportImportProgress(4.5, 7, 'maintenance');
    progress.reportImportProgress(5, 7, 'inventory');
    progress.reportImportProgress(6, 7, 'queries');

    expect(postMessage).toHaveBeenNthCalledWith(3, {
      type: 'loading-progress',
      state: { id: expect.stringMatching(/^ingestion-progress-/), phase: 'update', completed: 0, total: 7, stage: 'files' }
    });
    expect(postMessage).toHaveBeenNthCalledWith(4, {
      type: 'loading-progress',
      state: { id: expect.stringMatching(/^ingestion-progress-/), phase: 'update', completed: 4, total: 7, stage: 'maintenance' }
    });
    const updates = postMessage.mock.calls.map(([message]) => message)
      .filter((message) => message.type === 'loading-progress' && message.state.phase === 'update');
    expect(updates.map(({ state }) => state.completed)).toEqual([0, 4, 4.5, 5, 6]);
    expect(updates.every(({ state }) => state.completed < state.total)).toBe(true);
    progress.complete();
  });

  it('retains only the latest one hundred distinct progress messages', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();

    for (let index = 1; index <= 105; index += 1) progress.log(`Step ${index}`);
    progress.log('Step 105');
    vi.advanceTimersByTime(3_000);

    const notification = postMessage.mock.lastCall?.[0]?.notification;
    expect(notification.details).toHaveLength(100);
    expect(notification.details[0]).toBe('Step 6 +0s');
    expect(notification.details.at(-1)).toBe('Step 105 +3s');
    progress.complete();
  });

  it('shows progress immediately, updates every second, and dismisses it when complete', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.start();

    expect(postMessage).toHaveBeenNthCalledWith(2, {
      type: 'notification',
      notification: expect.objectContaining({
        id: expect.stringMatching(/^ingestion-progress-/),
        message: 'Preparing data...',
        icon: 'download',
        duration: 0
      })
    });

    progress.setWorkload(2_048);
    progress.update({ bytesProcessed: 1_024, recordsIngested: 42, totalBytes: 2_048 });
    vi.advanceTimersByTime(1_000);

    expect(postMessage).toHaveBeenCalledWith({
      type: 'notification',
      notification: expect.objectContaining({
        id: expect.stringMatching(/^ingestion-progress-/),
        message: '1.0 KB/2.0 KB · 1s remaining',
        icon: 'download',
        duration: 0
      })
    });

    progress.update({ bytesProcessed: 2_048, recordsIngested: 84, totalBytes: 2_048 });
    vi.advanceTimersByTime(1_000);
    expect(postMessage).toHaveBeenLastCalledWith({
      type: 'notification',
      notification: expect.objectContaining({
        message: '2.0 KB/2.0 KB · 0s remaining'
      })
    });

    progress.complete();
    expect(postMessage).toHaveBeenLastCalledWith({
      type: 'notification',
      notification: expect.objectContaining({ dismiss: true })
    });
    vi.advanceTimersByTime(1_000);
    expect(postMessage.mock.calls.filter(([message]) => message.type !== 'preparation-log')).toHaveLength(6);
  });

  it('keeps preparation diagnostics without displaying notifications when no work is required', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });

    progress.log('Checking the published payload identity.');
    vi.advanceTimersByTime(3_000);
    progress.complete();

    expect(postMessage).toHaveBeenCalledExactlyOnceWith({
      type: 'preparation-log', event: { message: 'Checking the published payload identity.' }
    });
  });

  it('exports every preparation step without truncating it to the notification history', () => {
    vi.useFakeTimers();
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.log('Checking cached shards.');
    progress.start();
    for (let index = 1; index <= 216; index += 1) progress.log(`Shard ${index} committed.`);
    progress.complete();
    progress.log('Ignored after completion.');
    const log = postMessage.mock.calls.map(([message]) => message)
      .filter((message) => message.type === 'preparation-log');
    expect(log).toHaveLength(217);
    expect(log[0].event.message).toBe('Checking cached shards.');
    expect(log.at(-1).event.message).toBe('Shard 216 committed.');
  });

  it('retains parsing and storage diagnostics even before the notification starts', () => {
    const postMessage = vi.fn();
    const progress = startIngestionProgress({ postMessage });
    progress.update({ bytesProcessed: 100, recordsIngested: 10 });
    progress.store({ storedRecords: 10, totalRecords: 10 });
    for (let index = 0; index < 110; index += 1) progress.log(`Step ${index}`);
    progress.complete();
    const messages = postMessage.mock.calls.map(([message]) => message.event.message);
    expect(messages).toHaveLength(112);
    expect(messages[0]).toContain('Parsing overall: 10 rec');
    expect(messages[1]).toContain('Storing 10/10 rec');
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
    const { startIngestionProgress: startIngestionProgressWithoutDebug } = await import('../../src/ingestion-progress.js');

    const postMessage = vi.fn();
    const progress = startIngestionProgressWithoutDebug({ postMessage });
    progress.start();
    progress.setWorkload(1_000);
    progress.complete();

    expect(output.debug).not.toHaveBeenCalled();

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('logs only scalar metadata for started, workload-set, and completed transitions under its predictable category', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=ingestion-progress', output })
      };
    });
    vi.resetModules();
    const { startIngestionProgress: startIngestionProgressWithDebug } = await import('../../src/ingestion-progress.js');

    const postMessage = vi.fn();
    const progress = startIngestionProgressWithDebug({ postMessage });
    progress.start();
    expect(output.debug).toHaveBeenCalledWith('[cao:ingestion-progress]', {
      event: 'started',
      id: expect.stringMatching(/^ingestion-progress-/)
    });

    progress.setWorkload(2_048);
    expect(output.debug).toHaveBeenCalledWith('[cao:ingestion-progress]', {
      event: 'workload-set',
      id: expect.stringMatching(/^ingestion-progress-/),
      totalBytes: 2_048
    });

    progress.complete();
    expect(output.debug).toHaveBeenCalledWith('[cao:ingestion-progress]', {
      event: 'completed',
      id: expect.stringMatching(/^ingestion-progress-/),
      started: true
    });

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });
});
