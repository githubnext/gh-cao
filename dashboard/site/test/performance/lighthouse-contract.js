/** @param {number[]} values */
export function median(values) {
  if (!values.length || values.some((value) => !Number.isFinite(value))) {
    throw new Error('A performance median requires finite measurements.');
  }
  const sorted = values.toSorted((left, right) => left - right);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
}

/**
 * @param {number} score
 * @param {Record<string, number>} metrics
 * @param {number} threshold
 * @param {Record<string, number>} budgets
 */
export function performanceFailures(score, metrics, threshold, budgets) {
  const failures = [];
  if (!Number.isFinite(score)) throw new Error('Lighthouse omitted its performance score.');
  if (score < threshold) failures.push(`score ${score.toFixed(2)} is below ${threshold.toFixed(2)}`);
  for (const [name, budget] of Object.entries(budgets)) {
    if (!Number.isFinite(metrics[name])) throw new Error(`Lighthouse omitted ${name}.`);
    if (metrics[name] > budget) failures.push(`${name} ${metrics[name].toFixed(3)} exceeds ${budget}`);
  }
  return failures;
}

/**
 * @param {'empty'|'full'} state
 * @param {Record<string, number>} counts
 * @param {number} shardCount
 * @param {{ runs: number, repositories: number }} [expected]
 */
export function assertCanonicalCounts(state, counts, shardCount, expected) {
  if (!Number.isSafeInteger(counts?.runs) || !Number.isSafeInteger(counts?.transactions)
      || Object.values(counts).some((count) => !Number.isSafeInteger(count) || count < 0)) {
    throw new Error('Canonical performance counts are incomplete or invalid.');
  }
  if (state === 'empty') {
    if (Object.entries(counts).some(([store, count]) =>
      store !== 'transactions' && count !== 0 && !(store === 'dailyRunCountState' && count === 1))) {
      throw new Error('The empty performance dataset contains canonical entities.');
    }
  } else if (state === 'full') {
    if (!counts.runs || !counts.repositories || !counts.tools || counts.transactions < shardCount
        || counts.runs !== expected?.runs || counts.repositories !== expected?.repositories) {
      throw new Error('The synthetic performance dataset is not completely ingested and populated.');
    }
  } else {
    throw new Error(`Unknown performance state: ${state}`);
  }
}
