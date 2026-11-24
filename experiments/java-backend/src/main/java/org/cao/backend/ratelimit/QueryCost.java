package org.cao.backend.ratelimit;

/**
 * Total cost of a completed query (specs/server-rate-limiting.md §3.1): the greatest of one,
 * execution seconds rounded up, row operations in 250,000 units, peak working rows in 100,000 units
 * and estimated peak working bytes in 16 MiB units, capped at the query bucket capacity.
 */
public final class QueryCost {
    private static final long OPERATION_UNIT = 250_000;
    private static final long ROW_UNIT = 100_000;
    private static final long BYTE_UNIT = 16L << 20;

    private QueryCost() { }

    /** Non-positive measurements count as zero; the result is always in {@code 1..capacity}. */
    public static int of(long durationMillis, long operations, long peakWorkingRows,
        long peakWorkingBytes, int capacity) {
        long cost = Math.max(1, ceil(durationMillis, 1000));
        cost = Math.max(cost, ceil(operations, OPERATION_UNIT));
        cost = Math.max(cost, ceil(peakWorkingRows, ROW_UNIT));
        cost = Math.max(cost, ceil(peakWorkingBytes, BYTE_UNIT));
        return (int) Math.min(cost, capacity);
    }

    private static long ceil(long value, long unit) {
        return value <= 0 ? 0 : (value - 1) / unit + 1;
    }
}
