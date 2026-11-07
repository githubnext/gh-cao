import { describe, expect, it, vi } from 'vitest';
import { withRetries } from '../../src/retry.js';

describe('withRetries', () => {
  it('retries failures and returns the first successful result', async () => {
    const operation = vi.fn()
      .mockRejectedValueOnce(new TypeError('network failure'))
      .mockRejectedValueOnce(new Error('temporary failure'))
      .mockResolvedValue('refreshed');

    await expect(withRetries(operation, { delayMs: 0 })).resolves.toBe('refreshed');
    expect(operation.mock.calls.map(([attempt]) => attempt)).toEqual([1, 2, 3]);
  });

  it('throws the last failure after exhausting attempts', async () => {
    const operation = vi.fn().mockRejectedValue(new Error('still unavailable'));

    await expect(withRetries(operation, { attempts: 2, delayMs: 0 }))
      .rejects.toThrow('still unavailable');
    expect(operation).toHaveBeenCalledTimes(2);
  });

  it('does not retry after the refresh is cancelled', async () => {
    const controller = new AbortController();
    const failure = new DOMException('Refresh cancelled', 'AbortError');
    const operation = vi.fn().mockImplementation(() => {
      controller.abort();
      return Promise.reject(failure);
    });

    await expect(withRetries(operation, { signal: controller.signal, delayMs: 0 }))
      .rejects.toBe(failure);
    expect(operation).toHaveBeenCalledOnce();
  });
});
