import { afterEach, describe, expect, it, vi } from 'vitest';
import { coalesce, debounce } from '../../src/debounce.js';

it('coalesces continuous changes every five seconds and cancels pending work', () => {
  vi.useFakeTimers();
  try {
    const callback = vi.fn();
    const refresh = coalesce(callback, 5_000);
    refresh(1);
    vi.advanceTimersByTime(4_999);
    refresh(2);
    expect(callback).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(callback).toHaveBeenLastCalledWith(2);
    refresh(3);
    vi.advanceTimersByTime(5_000);
    expect(callback).toHaveBeenLastCalledWith(3);
    refresh(4);
    refresh.cancel();
    vi.advanceTimersByTime(5_000);
    expect(callback).toHaveBeenCalledTimes(2);
    expect(vi.getTimerCount()).toBe(0);
  } finally {
    vi.useRealTimers();
  }
});

describe('debounce', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('invokes the callback once with the latest arguments after the delay', () => {
    vi.useFakeTimers();
    const callback = vi.fn();
    const debounced = debounce(callback, 500);

    debounced('first');
    vi.advanceTimersByTime(200);
    debounced('second');
    vi.advanceTimersByTime(499);

    expect(callback).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(callback).toHaveBeenCalledOnce();
    expect(callback).toHaveBeenCalledWith('second');
  });

  it('supports cancelling a pending invocation', () => {
    vi.useFakeTimers();
    const callback = vi.fn();
    const debounced = debounce(callback, 500);

    debounced();
    debounced.cancel();
    vi.runAllTimers();

    expect(callback).not.toHaveBeenCalled();
  });
});
