import {
  queryCollection,
  readCollection,
  readIndex,
  readRecord,
  readTransactions
} from '../storage/indexeddb.js';
import { createDebug } from '../../debug.js';

const debugQueries = createDebug('queries:index');

/** @param {IDBFactory} indexedDB */
export function createCanonicalQueries(indexedDB) {
  return {
    campaigns: {
      list: () => readCollection(indexedDB, 'campaigns'),
      getBySlug: async (/** @type {string} */ slug) => {
        const match = (await readIndex(indexedDB, 'campaigns', 'bySlug', [slug]))[0] ?? null;
        debugQueries({ event: 'campaigns.get-by-slug', found: match !== null });
        return match;
      }
    },
    repositories: {
      list: () => readCollection(indexedDB, 'repositories'),
      get: (/** @type {string} */ id) => readRecord(indexedDB, 'repositories', id)
    },
    workflows: {
      list: () => readCollection(indexedDB, 'workflows'),
      forRepository: (/** @type {string} */ repositoryId) =>
        readIndex(indexedDB, 'workflows', 'byRepository', [repositoryId])
    },
    runs: {
      list: () => readCollection(indexedDB, 'runs'),
      forRepository: (/** @type {string} */ repositoryId) =>
        readIndex(indexedDB, 'runs', 'byRepository', [repositoryId]),
      forWorkflow: (/** @type {string} */ workflowId) =>
        readIndex(indexedDB, 'runs', 'byWorkflow', [workflowId]),
      recentFailures: async () => {
        const rows = await queryCollection(indexedDB, 'runs', [
          {
            op: 'filter',
            predicates: [{
              field: 'conclusion',
              in: ['failure', 'startup-failure', 'stale', 'timed-out']
            }]
          },
          { op: 'arrange', by: [{ field: 'startedAt', direction: 'desc' }] }
        ]);
        debugQueries({ event: 'runs.recent-failures', count: rows.length });
        return rows;
      }
    },
    domains: runLinkedQueries(indexedDB, 'domains'),
    tools: runLinkedQueries(indexedDB, 'tools'),
    skills: runLinkedQueries(indexedDB, 'skills'),
    friction: runLinkedQueries(indexedDB, 'friction'),
    audits: runLinkedQueries(indexedDB, 'audits'),
    issues: runLinkedQueries(indexedDB, 'issues'),
    outputLifecycles: {
      list: () => readCollection(indexedDB, 'outputLifecycles'),
      forWorkflow: (/** @type {string} */ workflowId) =>
        readIndex(indexedDB, 'outputLifecycles', 'byWorkflow', [workflowId]),
      forRun: (/** @type {string} */ runId) =>
        readIndex(indexedDB, 'outputLifecycles', 'byRun', [runId])
    },
    operationalValues: {
      list: () => readCollection(indexedDB, 'operationalValues'),
      forRepository: (/** @type {string} */ repositoryId) =>
        readIndex(indexedDB, 'operationalValues', 'byRepository', [repositoryId])
    },
    marketplacePackages: {
      list: () => readCollection(indexedDB, 'marketplacePackages'),
      forRegistry: (/** @type {string} */ registryId) =>
        readIndex(indexedDB, 'marketplacePackages', 'byRegistry', [registryId])
    },
    transactions: {
      list: () => readTransactions(indexedDB)
    }
  };
}

/** @param {IDBFactory} indexedDB @param {'domains' | 'tools' | 'skills' | 'friction' | 'audits' | 'issues'} collection */
function runLinkedQueries(indexedDB, collection) {
  return {
    list: () => readCollection(indexedDB, collection),
    forRun: async (/** @type {string} */ runId) => {
      const rows = (
        await readIndex(indexedDB, collection, 'byRun', [runId])
      ).sort((left, right) => Number(left.sequence) - Number(right.sequence));
      debugQueries({ event: 'run-linked.for-run', collection, count: rows.length });
      return rows;
    }
  };
}