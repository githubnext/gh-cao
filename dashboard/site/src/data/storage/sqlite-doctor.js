import { randomUUID } from 'node:crypto';
import { rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { backup, DatabaseSync } from 'node:sqlite';
import { relationshipErrors } from '../model/schema.js';
import { pruneCanonicalRecord } from '../model/fields.js';
import { DAILY_RUN_COUNT_STORES, dailyRunCountCutoff, evictDailyRunCounts } from '../model/daily-run-counts.js';
import {
  CANONICAL_DATABASE_SCHEMA,
  DATABASE_NAME,
  DATABASE_STORES,
  DATABASE_VERSION,
  ENTITY_STORES,
  openCanonicalDatabase,
  prepareCanonicalRecord
} from './indexeddb.js';
import { STORAGE_SHARD_STORE } from './shards.js';
import { mergeRetainedRecords, OPERATIONAL_VALUE_RETENTION_MS, RETENTION_WINDOW_DAYS } from './retention.js';
import {
  createSqliteRelationalTables,
  createSqliteIndexedDB,
  SqliteIDBKeyRange,
  SQLITE_INDEXEDDB_METADATA_SCHEMA,
  SQLITE_RELATIONAL_STORES
} from './sqlite-indexeddb.js';
import { createDebug } from '../../debug.js';
import { identifier, sql } from './sql.js';

const debug = createDebug('sqlite-doctor');

const DAY_MS = 24 * 60 * 60 * 1000;
const EXPECTED_STORES = Object.keys(CANONICAL_DATABASE_SCHEMA);
// Derived projections (e.g. daily overview aggregates) are not part of the
// canonical batch shape the doctor scans/repairs: they are reconstructable
// derived state (INV-004), not canonical entities, so a repair pass is
// allowed to drop and lazily rebuild them on the next ingestion rather than
// preserving them across `writeCanonicalDatabase`.
const DERIVED_STORES = EXPECTED_STORES.filter(
  (store) => !/** @type {readonly string[]} */ (DATABASE_STORES).includes(store)
);

/** @typedef {{ id: string, kind: string, createdAt: string, [field: string]: unknown }} DoctorTransaction */

/** @returns {import('../model/schema.js').CanonicalBatch} */
function emptyBatch() {
  return /** @type {import('../model/schema.js').CanonicalBatch} */ (
    /** @type {unknown} */ (Object.fromEntries(ENTITY_STORES.map((store) => [store, []])))
  );
}

/** @param {Record<string, unknown> | undefined} row */
function firstValue(row) {
  return row ? Object.values(row)[0] : null;
}

/** @param {string} filename @param {{ readOnly?: boolean }} [options] */
function openConnection(filename, options = {}) {
  const connection = new DatabaseSync(filename, options);
  connection.exec('PRAGMA busy_timeout = 5000;');
  return connection;
}

/** @param {DatabaseSync} connection */
function foreignKeyDiagnostics(connection) {
  let foreignKeyViolations = 0;
  let outOfScopeForeignKeyViolations = 0;
  for (const violation of connection.prepare('PRAGMA foreign_key_check').all()) {
    const table = String(violation.table);
    const rowId = Number(violation.rowid);
    let databaseName = null;
    const query = table === '__idb_stores'
      ? connection.prepare('SELECT database_name FROM __idb_stores WHERE rowid = ?')
      : table === '__idb_indexes'
        ? connection.prepare('SELECT database_name FROM __idb_indexes WHERE rowid = ?')
        : table === '__idb_records'
          ? connection.prepare('SELECT database_name FROM __idb_records WHERE rowid = ?')
          : null;
    if (query && Number.isInteger(rowId)) databaseName = query.get(rowId)?.database_name;
    if (databaseName === DATABASE_NAME) foreignKeyViolations += 1;
    else outOfScopeForeignKeyViolations += 1;
  }
  return { foreignKeyViolations, outOfScopeForeignKeyViolations };
}

/** @param {string} filename */
function sqliteDiagnostics(filename) {
  const connection = openConnection(filename, { readOnly: true });
  try {
    const integrity = connection.prepare('PRAGMA integrity_check').all()
      .map((row) => String(firstValue(row)));
    return {
      integrity,
      ...foreignKeyDiagnostics(connection),
      pageCount: Number(firstValue(connection.prepare('PRAGMA page_count').get()) ?? 0),
      freePages: Number(firstValue(connection.prepare('PRAGMA freelist_count').get()) ?? 0)
    };
  } finally {
    connection.close();
  }
}

/** @param {string} filename */
function schemaDiagnostics(filename) {
  const connection = openConnection(filename, { readOnly: true });
  try {
    return schemaDiagnosticsFromConnection(connection);
  } finally {
    connection.close();
  }
}

/** @param {DatabaseSync} connection */
function schemaDiagnosticsFromConnection(connection) {
  let version;
  let stores;
  let indexes;
  try {
    version = Number(firstValue(connection.prepare(`
      SELECT version FROM __idb_databases WHERE name = ?
    `).get(DATABASE_NAME)) ?? 0);
    stores = connection.prepare(`
      SELECT name, key_path FROM __idb_stores
      WHERE database_name = ? ORDER BY name
    `).all(DATABASE_NAME);
    indexes = connection.prepare(`
      SELECT store_name, name, key_path FROM __idb_indexes
      WHERE database_name = ? ORDER BY store_name, name
    `).all(DATABASE_NAME);
  } catch (error) {
    return {
      version: 0,
      stores: [],
      issues: [`IndexedDB metadata is unavailable: ${error instanceof Error ? error.message : String(error)}`]
    };
  }
  const issues = [];
  if (version !== DATABASE_VERSION) issues.push(`database version is ${version}; expected ${DATABASE_VERSION}`);
  const actualStores = stores.map((row) => String(row.name));
  for (const [store, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
    const row = stores.find((candidate) => candidate.name === store);
    if (!row) issues.push(`missing object store ${store}`);
    else if (String(row.key_path) !== JSON.stringify(definition.keyPath)) {
      issues.push(`invalid key path for ${store}`);
    }
  }
  for (const store of actualStores) {
    if (!EXPECTED_STORES.includes(store)) issues.push(`unexpected object store ${store}`);
  }
  for (const [store, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
    const expected = definition.indexes;
    for (const [name, keyPath] of Object.entries(expected)) {
      const row = indexes.find((candidate) => candidate.store_name === store && candidate.name === name);
      if (!row) issues.push(`missing index ${store}.${name}`);
      else if (String(row.key_path) !== JSON.stringify(keyPath)) issues.push(`invalid key path for ${store}.${name}`);
    }
    for (const row of indexes.filter((candidate) => candidate.store_name === store)) {
      if (!(String(row.name) in expected)) issues.push(`unexpected index ${store}.${String(row.name)}`);
    }
  }
  for (const [store, definition] of Object.entries(SQLITE_RELATIONAL_STORES)) {
    const table = store.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`);
    const columns = new Set(connection.prepare(`PRAGMA table_info(${table})`).all().map((row) => String(row.name)));
    const expected = [
      'database_name', 'id', 'observed_at', 'provenance',
      ...Object.keys(definition).map((name) => name.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`))
    ];
    for (const field of expected) {
      if (!columns.has(field)) issues.push(`missing ${table} table column ${field}`);
    }
    for (const field of columns) {
      if (!expected.includes(field)) issues.push(`unexpected ${table} table column ${field}`);
    }
  }
  return { version, stores: actualStores, issues };
}

/** @param {string} filename */
function scanRecords(filename) {
  const connection = openConnection(filename, { readOnly: true });
  try {
    return scanRecordsFromConnection(connection);
  } finally {
    connection.close();
  }
}

/** @param {DatabaseSync} connection */
function scanRecordsFromConnection(connection) {
  let rows;
  try {
    rows = connection.prepare(`
      SELECT store_name, record_key, value FROM __idb_records
      WHERE database_name = ? ORDER BY store_name, record_key
    `).all(DATABASE_NAME);
  } catch (error) {
    return {
      batch: emptyBatch(),
      transactions: /** @type {DoctorTransaction[]} */ ([]),
      invalid: /** @type {{ store: string, recordKey: string, reason: string }[]} */ ([]),
      error: `record storage is unavailable: ${error instanceof Error ? error.message : String(error)}`
    };
  }
  const batch = emptyBatch();
  const transactions = /** @type {DoctorTransaction[]} */ ([]);
  const invalid = /** @type {{ store: string, recordKey: string, reason: string }[]} */ ([]);
  for (const row of rows) {
    const store = String(row.store_name);
    if (!EXPECTED_STORES.includes(store)) {
      invalid.push({
        store,
        recordKey: String(row.record_key),
        reason: 'record belongs to an unexpected object store'
      });
      continue;
    }
    // Derived projection records (e.g. daily overview aggregates) are
    // reconstructable derived state, not canonical entities: they are
    // intentionally excluded from the canonical batch/relationship checks
    // below and are dropped (and lazily rebuilt) whenever a repair rewrites
    // the database.
    if (DERIVED_STORES.includes(store)) continue;
    let key;
    let value;
    let reason = '';
    try {
      key = JSON.parse(String(row.record_key));
    } catch {
      reason = 'invalid record key JSON';
    }
    try {
      value = JSON.parse(String(row.value));
    } catch {
      reason ||= 'invalid record JSON';
    }
    if (!reason && (!value || typeof value !== 'object' || Array.isArray(value))) {
      reason = 'record is not an object';
    }
    if (!reason && (typeof value.id !== 'string' || !value.id)) reason = 'record has no string id';
    if (!reason && key !== value.id) reason = 'record key does not match id';
    if (!reason && store === 'transactions'
      && (typeof value.kind !== 'string' || typeof value.createdAt !== 'string')) {
      reason = 'transaction metadata is invalid';
    }
    if (reason) {
      invalid.push({ store, recordKey: String(row.record_key), reason });
    } else if (store === 'transactions' && value.id !== 'workflow-subject-state') {
      transactions.push(/** @type {DoctorTransaction} */ (value));
    } else {
      batch[/** @type {keyof import('../model/schema.js').CanonicalBatch} */ (store)]?.push(value);
    }
  }
  return { batch, transactions, invalid, error: null };
}

/** @param {import('../model/schema.js').CanonicalBatch} batch */
function counts(batch) {
  return Object.fromEntries(ENTITY_STORES.map((store) => [store, batch[store]?.length ?? 0]));
}

/** @param {{ store: string, reason: string }[]} invalid */
function summarizeInvalid(invalid) {
  const summary = /** @type {Record<string, number>} */ ({});
  for (const row of invalid) {
    const key = `${row.store}: ${row.reason}`;
    summary[key] = (summary[key] ?? 0) + 1;
  }
  return summary;
}

/** @param {DoctorTransaction[]} transactions @param {number} horizon */
function retainedTransactions(transactions, horizon) {
  return transactions.filter((transaction) => {
    if (transaction.kind === 'ingest-normalized-jsonl'
        && typeof transaction.payloadHash === 'string') {
      return true;
    }
    const timestamp = Date.parse(String(transaction.createdAt ?? ''));
    return Number.isFinite(timestamp) && timestamp >= horizon;
  });
}

/**
 * @param {import('../model/schema.js').CanonicalBatch} left
 * @param {import('../model/schema.js').CanonicalBatch} right
 */
function sameBatch(left, right) {
  return ENTITY_STORES.every((store) => JSON.stringify(
    [...(left[store] ?? [])].sort((a, b) => String(a.id).localeCompare(String(b.id)))
  ) === JSON.stringify(
    [...(right[store] ?? [])].sort((a, b) => String(a.id).localeCompare(String(b.id)))
  ));
}

/**
 * @param {DatabaseSync} connection
 * @param {string} filename
 * @param {string} checkedAt
 */
async function createBackup(connection, filename, checkedAt) {
  const backupPath = `${filename}.doctor-backup-${checkedAt.replaceAll(/[^0-9]/g, '')}-${randomUUID()}.sqlite`;
  try {
    await backup(connection, backupPath);
    return backupPath;
  } catch (error) {
    await rm(backupPath, { force: true });
    throw Object.assign(
      new Error(`backup failed at ${backupPath}: ${error instanceof Error ? error.message : String(error)}`),
      { doctorStage: 'backup' }
    );
  }
}

/** @param {string} filename @param {string} checkedAt */
async function backupDatabase(filename, checkedAt) {
  const connection = openConnection(filename, { readOnly: true });
  try {
    return await createBackup(connection, filename, checkedAt);
  } finally {
    connection.close();
  }
}

/**
 * @param {DatabaseSync} connection
 * @param {import('../model/schema.js').CanonicalBatch} batch
 * @param {DoctorTransaction[]} transactions
 */
function writeCanonicalDatabase(connection, batch, transactions) {
  // Detail repair cannot reconstruct receipts for runs already detail-evicted.
  const protectedCounts = connection.prepare(`
    SELECT store_name, record_key, value FROM __idb_records
    WHERE database_name = ? AND store_name IN (?, ?, ?)
  `).all(DATABASE_NAME, ...DAILY_RUN_COUNT_STORES);
  for (const table of ['__idb_records', '__idb_indexes', '__idb_stores']) {
    const query = sql`DELETE FROM ${identifier(table)} WHERE database_name = ${DATABASE_NAME}`;
    connection.prepare(query.text).run(...query.values);
  }
  connection.prepare('DELETE FROM __idb_databases WHERE name = ?').run(DATABASE_NAME);
  connection.prepare('INSERT INTO __idb_databases (name, version) VALUES (?, ?)').run(
    DATABASE_NAME,
    DATABASE_VERSION
  );
  const insertStore = connection.prepare(`
    INSERT INTO __idb_stores (database_name, name, key_path) VALUES (?, ?, ?)
  `);
  const insertIndex = connection.prepare(`
    INSERT INTO __idb_indexes (database_name, store_name, name, key_path) VALUES (?, ?, ?, ?)
  `);
  for (const [store, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
    insertStore.run(DATABASE_NAME, store, JSON.stringify(definition.keyPath));
    for (const [name, keyPath] of Object.entries(definition.indexes)) {
      insertIndex.run(DATABASE_NAME, store, name, JSON.stringify(keyPath));
    }
  }
  const insertRecord = connection.prepare(`
    INSERT INTO __idb_records (database_name, store_name, record_key, value) VALUES (?, ?, ?, ?)
  `);
  for (const row of protectedCounts) {
    insertRecord.run(DATABASE_NAME, row.store_name, row.record_key, row.value);
  }
  /** @type {Map<string, import('./shards.js').Shard>} */
  const shards = new Map();
  const totals = { id: 'totals', bytes: 0, count: 0 };
  for (const store of ENTITY_STORES) {
    for (const record of batch[store] ?? []) {
      const prepared = prepareCanonicalRecord(store, pruneCanonicalRecord(store, record));
      insertRecord.run(DATABASE_NAME, store, JSON.stringify(record.id), JSON.stringify(prepared));
      const storage = prepared._storage;
      const shard = shards.get(storage.shard) ?? {
        id: storage.shard, storeName: store, day: Number(storage.shard.split(':').at(-1)),
        bytes: 0, count: 0, dirty: store === 'audits'
      };
      shard.bytes += storage.bytes;
      shard.count += 1;
      shards.set(shard.id, shard);
      totals.bytes += storage.bytes;
      totals.count += 1;
    }
  }
  for (const shard of [...shards.values(), totals]) {
    insertRecord.run(DATABASE_NAME, STORAGE_SHARD_STORE, JSON.stringify(shard.id), JSON.stringify(shard));
  }
  for (const transaction of transactions) {
    insertRecord.run(
      DATABASE_NAME,
      'transactions',
      JSON.stringify(transaction.id),
      JSON.stringify(transaction)
    );
  }
}

/**
 * @param {string} filename
 * @param {string} checkedAt
 * @param {number} now
 * @param {number} retentionWindowMs
 * @param {{ runs: number, operationalValues: number }} retentionWindowMsByStore
 */
async function repairCanonicalDatabase(filename, checkedAt, now, retentionWindowMs, retentionWindowMsByStore) {
  const connection = openConnection(filename);
  let backupPath = null;
  let candidateBackupPath = null;
  let doctorStage = 'backup';
  let locked = false;
  try {
    connection.exec('PRAGMA foreign_keys = ON;');
    for (let attempt = 1; attempt <= 3; attempt += 1) {
      const dataVersion = Number(firstValue(connection.prepare('PRAGMA data_version').get()));
      doctorStage = 'backup';
      candidateBackupPath = await createBackup(connection, filename, checkedAt);
      doctorStage = 'lock';
      connection.exec('BEGIN EXCLUSIVE;');
      locked = true;
      if (Number(firstValue(connection.prepare('PRAGMA data_version').get())) === dataVersion) {
        backupPath = candidateBackupPath;
        candidateBackupPath = null;
        doctorStage = 'repair';
        break;
      }
      connection.exec('ROLLBACK;');
      locked = false;
      await rm(candidateBackupPath, { force: true });
      candidateBackupPath = null;
    }
    if (!locked) throw new Error('database changed while creating a repair backup');
    connection.exec(SQLITE_INDEXEDDB_METADATA_SCHEMA);
    const scanned = scanRecordsFromConnection(connection);
    const schema = schemaDiagnosticsFromConnection(connection);
    const horizon = now - retentionWindowMs;
    const repairedBatch = mergeRetainedRecords(scanned.batch, emptyBatch(), {
      now,
      retentionWindowMs,
      retentionWindowMsByStore,
      includePreviousInReference: true,
      preserveUnreferencedParents: true
    });
    const transactions = retainedTransactions(scanned.transactions, horizon);
    writeCanonicalDatabase(connection, repairedBatch, transactions);
    createSqliteRelationalTables(connection);
    connection.exec('COMMIT;');
    locked = false;
    return {
      backupPath,
      scanned,
      schema,
      repairedBatch,
      transactions,
      relationshipIssues: relationshipErrors(scanned.batch)
    };
  } catch (error) {
    if (locked) {
      try {
        connection.exec('ROLLBACK;');
      } catch {
        // The transaction may already have been rolled back by SQLite.
      }
    }
    if (candidateBackupPath) await rm(candidateBackupPath, { force: true });
    throw Object.assign(error instanceof Error ? error : new Error(String(error)), {
      backupPath,
      doctorStage
    });
  } finally {
    connection.close();
  }
}

/**
 * @param {string} filename
 * @param {string[]} actions
 * @param {string[]} errors
 */
function maintainSqlite(filename, actions, errors) {
  let connection;
  try {
    connection = openConnection(filename);
  } catch (error) {
    errors.push(`open database for repair: ${error instanceof Error ? error.message : String(error)}`);
    return;
  }
  try {
    for (const statement of ['REINDEX', 'PRAGMA optimize', 'VACUUM']) {
      try {
        connection.exec(`${statement};`);
        actions.push(statement.toLowerCase().replace('pragma ', ''));
      } catch (error) {
        errors.push(`${statement}: ${error instanceof Error ? error.message : String(error)}`);
      }
    }
  } finally {
    connection.close();
  }
}

/**
 * Diagnoses and repairs the canonical SQLite-backed IndexedDB database.
 *
 * @param {string} filename
 * @param {{ now?: number, ttlDays?: number | 'all', runTtlDays?: number | 'all' }} [options]
 */
export async function doctorSqliteDatabase(filename, options = {}) {
  const databasePath = path.resolve(filename);
  const databaseStat = await stat(databasePath);
  if (!databaseStat.isFile()) throw new Error(`SQLite database is not a file: ${databasePath}`);
  const now = Number.isFinite(options.now) ? Number(options.now) : Date.now();
  const retainAll = options.ttlDays === 'all';
  const ttlDays = retainAll
    ? 'all'
    : Number.isFinite(options.ttlDays) ? Number(options.ttlDays) : 7;
  const retentionWindowMs = retainAll ? Number.MAX_SAFE_INTEGER : Number(ttlDays) * DAY_MS;
  if (!retainAll && (Number(ttlDays) <= 0 || !Number.isFinite(retentionWindowMs))) {
    throw new TypeError('TTL days must produce a finite window greater than zero');
  }
  const retainAllRuns = options.runTtlDays === 'all';
  const runTtlDays = retainAllRuns
    ? 'all'
    : Number.isFinite(options.runTtlDays) ? Number(options.runTtlDays)
      : options.ttlDays === undefined ? RETENTION_WINDOW_DAYS : ttlDays;
  const runRetentionWindowMs = retainAllRuns
    ? Number.MAX_SAFE_INTEGER
    : runTtlDays === 'all' ? Number.MAX_SAFE_INTEGER : Number(runTtlDays) * DAY_MS;
  if (!retainAllRuns
    && (Number(runTtlDays) <= 0 || !Number.isFinite(runRetentionWindowMs))) {
    throw new TypeError('Run TTL days must produce a finite window greater than zero');
  }
  const retentionWindowMsByStore = {
    runs: runRetentionWindowMs,
    operationalValues: Math.max(OPERATIONAL_VALUE_RETENTION_MS, runRetentionWindowMs)
  };
  const checkedAt = new Date(now).toISOString();
  const actions = /** @type {string[]} */ ([]);
  const errors = /** @type {string[]} */ ([]);
  let backupPath = null;
  let beforeSqlite;

  try {
    beforeSqlite = sqliteDiagnostics(databasePath);
  } catch (error) {
    const initialDiagnosticError = error instanceof Error ? error.message : String(error);
    try {
      backupPath = await backupDatabase(databasePath, checkedAt);
    } catch (backupError) {
      errors.push(`initial diagnostics: ${initialDiagnosticError}`);
      errors.push(`backup: ${backupError instanceof Error ? backupError.message : String(backupError)}`);
      return {
        command: 'repair-database',
        database: databasePath,
        checkedAt,
        ttlDays,
        healthy: false,
        error: 'Database repair requires a successful backup',
        repairs: { actions, errors, backup: backupPath }
      };
    }
    maintainSqlite(databasePath, actions, errors);
    try {
      beforeSqlite = sqliteDiagnostics(databasePath);
    } catch (afterError) {
      errors.push(`initial diagnostics: ${initialDiagnosticError}`);
      errors.push(`diagnostics after repair: ${afterError instanceof Error ? afterError.message : String(afterError)}`);
      return {
        command: 'repair-database',
        database: databasePath,
        checkedAt,
        ttlDays,
        healthy: false,
        error: afterError instanceof Error ? afterError.message : String(afterError),
        repairs: { actions, errors, backup: backupPath }
      };
    }
    actions.push('recover-sqlite-diagnostics');
  }

  if (beforeSqlite.integrity.some((result) => result !== 'ok')) {
    try {
      backupPath = await backupDatabase(databasePath, checkedAt);
    } catch (error) {
      errors.push(`backup: ${error instanceof Error ? error.message : String(error)}`);
      return {
        command: 'repair-database',
        database: databasePath,
        checkedAt,
        ttlDays,
        healthy: false,
        before: { sqlite: beforeSqlite },
        error: 'Database repair requires a successful backup',
        repairs: { actions, errors, backup: backupPath }
      };
    }
    maintainSqlite(databasePath, actions, errors);
    const afterSqlite = sqliteDiagnostics(databasePath);
    if (afterSqlite.integrity.some((result) => result !== 'ok')) {
      return {
        command: 'repair-database',
        database: databasePath,
        checkedAt,
        ttlDays,
        healthy: false,
        before: { sqlite: beforeSqlite },
        after: { sqlite: afterSqlite },
        repairs: { actions, errors, backup: backupPath }
      };
    }
  }

  const beforeSchema = schemaDiagnostics(databasePath);
  const scanned = scanRecords(databasePath);
  const relationshipIssues = relationshipErrors(scanned.batch);
  const horizon = now - retentionWindowMs;
  const repairedBatch = mergeRetainedRecords(scanned.batch, emptyBatch(), {
    now,
    retentionWindowMs,
    retentionWindowMsByStore,
    includePreviousInReference: true,
    preserveUnreferencedParents: true
  });
  const transactions = retainedTransactions(scanned.transactions, horizon);
  const transactionRowsRemoved = scanned.transactions.length - transactions.length;
  const canonicalRowsRemoved = ENTITY_STORES.reduce(
    (total, store) => total + (scanned.batch[store]?.length ?? 0) - (repairedBatch[store]?.length ?? 0),
    0
  );
  const canonicalDataChanged = !sameBatch(scanned.batch, repairedBatch);
  const needsRepair = beforeSchema.issues.length > 0
    || beforeSqlite.foreignKeyViolations > 0
    || Boolean(scanned.error)
    || scanned.invalid.length > 0
    || relationshipIssues.length > 0
    || canonicalDataChanged
    || transactionRowsRemoved > 0;
  let invalidRecordsRemoved = scanned.invalid.length;
  let repairedCanonicalRows = canonicalRowsRemoved;
  let repairedTransactionRows = transactionRowsRemoved;

  debug('diagnosed database', {
    event: 'diagnosed',
    needsRepair,
    schemaIssueCount: beforeSchema.issues.length,
    foreignKeyViolations: beforeSqlite.foreignKeyViolations,
    invalidRecordCount: scanned.invalid.length
  });

  if (needsRepair) {
    let repair;
    try {
      if ([44, 45].includes(beforeSchema.version) && !scanned.error && scanned.invalid.length === 0) {
        backupPath = await backupDatabase(databasePath, checkedAt);
        const keyRange = /** @type {typeof IDBKeyRange} */ (/** @type {unknown} */ (SqliteIDBKeyRange));
        const database = await openCanonicalDatabase(createSqliteIndexedDB(databasePath), keyRange);
        database.close();
        actions.push('backfill-daily-run-counts');
      }
      repair = await repairCanonicalDatabase(
        databasePath,
        checkedAt,
        now,
        retentionWindowMs,
        retentionWindowMsByStore
      );
      backupPath ??= repair.backupPath;
    } catch (error) {
      backupPath ??= error && typeof error === 'object' && 'backupPath' in error
        ? /** @type {{ backupPath: string | null }} */ (error).backupPath
        : null;
      const doctorStage = error && typeof error === 'object' && 'doctorStage' in error
        ? /** @type {{ doctorStage: string }} */ (error).doctorStage
        : 'repair';
      errors.push(`repair: ${error instanceof Error ? error.message : String(error)}`);
      return {
        command: 'repair-database',
        database: databasePath,
        checkedAt,
        ttlDays,
        healthy: false,
        before: {
          sizeBytes: databaseStat.size,
          sqlite: beforeSqlite,
          schema: beforeSchema,
          counts: counts(scanned.batch),
          transactions: scanned.transactions.length,
          invalidRecords: summarizeInvalid(scanned.invalid),
          relationshipErrors: relationshipIssues,
          recordError: scanned.error
        },
        error: doctorStage === 'backup'
          ? 'Database repair requires a successful backup'
          : doctorStage === 'lock'
            ? 'Database repair could not acquire an exclusive lock'
            : 'Database repair failed and was rolled back',
        repairs: { actions, errors, backup: backupPath }
      };
    }
    const lockedBatch = repair.scanned.batch;
    invalidRecordsRemoved = repair.scanned.invalid.length;
    repairedCanonicalRows = ENTITY_STORES.reduce(
      (total, store) => total
        + (lockedBatch[store]?.length ?? 0)
        - (repair.repairedBatch[store]?.length ?? 0),
      0
    );
    repairedTransactionRows = repair.scanned.transactions.length - repair.transactions.length;
    if (repair.schema.issues.length > 0 || beforeSqlite.foreignKeyViolations > 0) {
      actions.push('rebuild-schema');
    }
    if (scanned.error) actions.push('rebuild-storage');
    if (invalidRecordsRemoved > 0) actions.push('remove-invalid-records');
    if (repairedCanonicalRows > 0) actions.push('prune-expired-or-orphaned-records');
    else if (!sameBatch(lockedBatch, repair.repairedBatch)) actions.push('normalize-canonical-records');
    if (repairedTransactionRows > 0) actions.push('prune-expired-transactions');
    maintainSqlite(databasePath, actions, errors);
    debug('repaired database', {
      event: 'repaired',
      actionCount: actions.length,
      errorCount: errors.length,
      invalidRecordsRemoved,
      canonicalRecordsRemoved: repairedCanonicalRows,
      transactionsRemoved: repairedTransactionRows
    });
  }

  try {
    const keyRange = /** @type {typeof IDBKeyRange} */ (/** @type {unknown} */ (SqliteIDBKeyRange));
    const database = await openCanonicalDatabase(createSqliteIndexedDB(databasePath), keyRange);
    try {
      const state = await new Promise((resolve, reject) => {
        const request = database.transaction('dailyRunCountState').objectStore('dailyRunCountState').get('daily-run-counts');
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error);
      });
      const cutoff = dailyRunCountCutoff(Math.max(Number(state?.retentionWatermark ?? -Infinity), now));
      if (state?.status === 'ready' && state.retentionCutoff !== cutoff) {
        const counts = await Promise.all(['dailyRunCounts', 'runCountContributions'].map((name) =>
          new Promise((resolve, reject) => {
            const request = database.transaction(name).objectStore(name).count();
            request.onsuccess = () => resolve(request.result);
            request.onerror = () => reject(request.error);
          })));
        if (counts.some((count) => Number(count) > 0)) {
          backupPath ??= await backupDatabase(databasePath, checkedAt);
          actions.push('maintain-daily-count-retention');
        }
      }
      await evictDailyRunCounts(database, now, undefined, keyRange);
    } finally {
      database.close();
    }
  } catch (error) {
    errors.push(`daily count retention: ${error instanceof Error ? error.message : String(error)}`);
  }
  const afterSqlite = sqliteDiagnostics(databasePath);
  const afterSchema = schemaDiagnostics(databasePath);
  const afterScan = scanRecords(databasePath);
  const afterRelationshipIssues = relationshipErrors(afterScan.batch);
  const healthy = afterSqlite.integrity.every((result) => result === 'ok')
    && afterSqlite.foreignKeyViolations === 0
    && afterSchema.issues.length === 0
    && afterScan.invalid.length === 0
    && afterRelationshipIssues.length === 0
    && !afterScan.error
    && errors.length === 0;

  debug('completed doctor run', { event: 'completed', healthy, actionCount: actions.length, errorCount: errors.length });

  return {
    command: 'repair-database',
    database: databasePath,
    checkedAt,
    ttlDays,
    runTtlDays,
    healthy,
    before: {
      sizeBytes: databaseStat.size,
      sqlite: beforeSqlite,
      schema: beforeSchema,
      counts: counts(scanned.batch),
      transactions: scanned.transactions.length,
      invalidRecords: summarizeInvalid(scanned.invalid),
      relationshipErrors: relationshipIssues,
      recordError: scanned.error
    },
    after: {
      sizeBytes: (await stat(databasePath)).size,
      sqlite: afterSqlite,
      schema: afterSchema,
      counts: counts(afterScan.batch),
      transactions: afterScan.transactions.length,
      invalidRecords: summarizeInvalid(afterScan.invalid),
      relationshipErrors: afterRelationshipIssues,
      recordError: afterScan.error
    },
    repairs: {
      actions,
      errors,
      backup: backupPath,
      invalidRecordsRemoved,
      canonicalRecordsRemoved: repairedCanonicalRows,
      transactionsRemoved: repairedTransactionRows
    }
  };
}
