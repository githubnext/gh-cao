import { webcrypto } from 'node:crypto';
import { expect, it, vi } from 'vitest';
import { sampleStatisticalRuns } from '../../src/data/queries/statistical-sample.js';

it('calibrates inclusion frequencies and expansion estimates across fixed independent seeds', async () => {
  vi.stubGlobal('crypto', webcrypto);
  try {
    const rows = Array.from({ length: 20 }, (_, index) => ({
      id: `github:run:example/control:${index + 1}`,
      status: 'completed',
      conclusion: index < 18 ? 'success' : 'failure',
      startedAt: '2026-10-22T12:00:00.000Z',
      measurement: index + 1
    }));
    const inclusionCounts = new Map(rows.map((row) => [row.id, 0]));
    const repetitions = 96;
    let totalEstimate = 0;
    for (let seed = 0; seed < repetitions; seed++) {
      const sample = await sampleStatisticalRuns(rows, {
        watermark: '2026-10-23T12:00:00.000Z',
        capacity: 4,
        complete: true,
        seed: `quality-calibration-${seed}`
      });
      expect(sample.rows).toHaveLength(4);
      expect(sample.frame['full-population-estimable']).toBe(true);
      for (const row of sample.rows) {
        const id = String(row.id);
        inclusionCounts.set(id, (inclusionCounts.get(id) ?? 0) + 1);
        totalEstimate += Number(row.measurement) * Number(row['statistical-weight']);
      }
    }
    for (const row of rows) {
      const expected = repetitions * (row.conclusion === 'failure' ? 1 / 2 : 3 / 18);
      expect(Math.abs((inclusionCounts.get(row.id) ?? 0) - expected)).toBeLessThan(18);
    }
    const populationTotal = rows.reduce((sum, row) => sum + row.measurement, 0);
    expect(Math.abs(totalEstimate / repetitions - populationTotal) / populationTotal).toBeLessThan(0.08);
  } finally {
    vi.unstubAllGlobals();
  }
});
