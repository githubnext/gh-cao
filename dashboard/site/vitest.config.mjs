import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    pool: 'vmThreads',
    setupFiles: ['./test/setup.js'],
    projects: [
      {
        extends: true,
        test: {
          name: 'dashboard',
          environment: 'jsdom',
          include: ['test/unit/**/*.test.js'],
          exclude: [
            'test/unit/audit-curation.test.js', 'test/unit/dashboard-navigation.test.js',
            'test/unit/data-storage-shards.test.js', 'test/unit/detail-query-scale.test.js',
            'test/unit/native-streaming-aggregate.test.js', 'test/unit/workflow-value-view-contract.test.js'
          ]
        }
      },
      {
        extends: true,
        test: {
          name: 'dashboard-navigation',
          environment: 'jsdom',
          include: ['test/unit/dashboard-navigation.test.js'],
          // VM workers suppress diagnostics when the mocked localStorage getter throws.
          pool: 'forks'
        }
      },
      {
        extends: true,
        test: {
          name: 'audit-curation',
          include: [
            'test/unit/audit-curation.test.js', 'test/unit/data-storage-shards.test.js',
            'test/unit/detail-query-scale.test.js', 'test/unit/native-streaming-aggregate.test.js',
            'test/unit/workflow-value-view-contract.test.js'
          ],
          // Large fake-indexeddb scans and indexed shard mutations are slower in VM workers.
          pool: 'forks'
        }
      }
    ]
  }
});
