import { createDebug } from './debug.js';

const debugRetry = createDebug('retry');

/**
 * Runs an asynchronous operation again after transient failures.
 *
 * @template T
 * @param {(attempt: number) => Promise<T>} operation
 * @param {{ attempts?: number, delayMs?: number, signal?: AbortSignal }} [options]
 * @returns {Promise<T>}
 */
export async function withRetries(operation, options = {}) {
  const attempts = options.attempts ?? 3;
  const delayMs = options.delayMs ?? 250;
  if (!Number.isSafeInteger(attempts) || attempts < 1) {
    throw new TypeError('Retry attempts must be a positive integer.');
  }
  if (!Number.isFinite(delayMs) || delayMs < 0) {
    throw new TypeError('Retry delay must be a non-negative number.');
  }

  for (let attempt = 1; ; attempt += 1) {
    options.signal?.throwIfAborted();
    try {
      const result = await operation(attempt);
      if (attempt > 1) debugRetry({ event: 'succeeded', attempt, attempts });
      return result;
    } catch (error) {
      if (options.signal?.aborted) throw error;
      const errorName = error instanceof Error ? error.name : 'UnknownError';
      if (attempt >= attempts) {
        debugRetry({ event: 'exhausted', attempt, attempts, errorName });
        throw error;
      }
      const backoffMs = delayMs * (2 ** (attempt - 1));
      debugRetry({ event: 'retrying', attempt, attempts, backoffMs, errorName });
      await new Promise((resolve) => {
        setTimeout(resolve, backoffMs);
      });
    }
  }
}
