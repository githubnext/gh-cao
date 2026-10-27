import { DatabaseSync } from 'node:sqlite';
import { createDebug } from '../../debug.js';
import { identifier, sql } from './sql.js';
import { attachSqliteOverlay } from './sqlite-overlay.js';

const debug = createDebug('sqlite-indexeddb');

export const SQLITE_INDEXEDDB_METADATA_SCHEMA = `
  PRAGMA foreign_keys = ON;
  CREATE TABLE IF NOT EXISTS __idb_databases (
    name TEXT PRIMARY KEY,
    version INTEGER NOT NULL
  );
  CREATE TABLE IF NOT EXISTS __idb_stores (
    database_name TEXT NOT NULL,
    name TEXT NOT NULL,
    key_path TEXT NOT NULL,
    PRIMARY KEY (database_name, name),
    FOREIGN KEY (database_name) REFERENCES __idb_databases(name) ON DELETE CASCADE
  );
  CREATE TABLE IF NOT EXISTS __idb_indexes (
    database_name TEXT NOT NULL,
    store_name TEXT NOT NULL,
    name TEXT NOT NULL,
    key_path TEXT NOT NULL,
    PRIMARY KEY (database_name, store_name, name),
    FOREIGN KEY (database_name, store_name)
      REFERENCES __idb_stores(database_name, name) ON DELETE CASCADE
  );
  CREATE TABLE IF NOT EXISTS __idb_records (
    database_name TEXT NOT NULL,
    store_name TEXT NOT NULL,
    record_key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (database_name, store_name, record_key),
    FOREIGN KEY (database_name, store_name)
      REFERENCES __idb_stores(database_name, name) ON DELETE CASCADE
  );
`;

export const SQLITE_RELATIONAL_STORES = Object.freeze({
  campaigns: {
    slug: 'TEXT', name: 'TEXT', description: 'TEXT', icon: 'TEXT',
    mode: 'TEXT', enabled: 'INTEGER', experimental: 'INTEGER',
    version: 'TEXT', currentVersion: 'TEXT', minVersion: 'TEXT', updateState: 'TEXT',
    readmePath: 'TEXT', readme: 'TEXT', workerCount: 'INTEGER', inventoryWarnings: 'INTEGER',
    maxRepositories: 'INTEGER', rolloutPercent: 'REAL', aiCreditAllowance: 'REAL',
    monthlyAiCreditBudget: 'REAL', campaignLink: 'TEXT', intelligenceDeclaration: 'TEXT'
  },
  experiments: { workflowId: 'TEXT', name: 'TEXT', firstObservedAt: 'TEXT', lastObservedAt: 'TEXT' },
  experimentAssignments: { runId: 'TEXT', experimentId: 'TEXT', variant: 'TEXT',
    included: 'INTEGER', exclusionReason: 'TEXT', auditId: 'TEXT' },
  graders: { workflowId: 'TEXT', name: 'TEXT', sourceGraderId: 'TEXT',
    direction: 'TEXT', unit: 'TEXT', threshold: 'REAL',
    firstObservedAt: 'TEXT', lastObservedAt: 'TEXT' },
  graderObservations: { runId: 'TEXT', graderId: 'TEXT', value: 'REAL', status: 'TEXT',
    experimentId: 'TEXT', variant: 'TEXT',
    evaluatorDigest: 'TEXT', timestamp: 'TEXT',
    included: 'INTEGER', exclusionReason: 'TEXT', auditId: 'TEXT' },
  evals: { workflowId: 'TEXT', name: 'TEXT', sourceEvalId: 'TEXT',
    firstObservedAt: 'TEXT', lastObservedAt: 'TEXT' },
  evalObservations: { runId: 'TEXT', evalId: 'TEXT', experimentId: 'TEXT', variant: 'TEXT',
    evalResult: 'TEXT', status: 'TEXT',
    timestamp: 'TEXT',
    included: 'INTEGER', exclusionReason: 'TEXT', auditId: 'TEXT' }
});

/** @param {DatabaseSync} connection @param {string} databaseName @param {string} [store] */
function clearRelationalStores(connection, databaseName, store) {
  for (const name of store ? [store] : Object.keys(SQLITE_RELATIONAL_STORES)) {
    if (!Object.hasOwn(SQLITE_RELATIONAL_STORES, name)) continue;
    const table = name.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`);
    const query = sql`DELETE FROM ${identifier(table)} WHERE database_name = ${databaseName}`;
    connection.prepare(query.text).run(...query.values);
  }
}

/** Creates derived mirrors inside the caller's transaction. @param {DatabaseSync} connection */
export function createSqliteRelationalTables(connection) {
  const sqlValue = (/** @type {string} */ field, value = 'NEW.value') => {
    const observed = `json_extract(${value}, '$.observedAt')`;
    return field === 'firstObservedAt' || field === 'lastObservedAt'
      ? `coalesce(json_extract(${value}, '$.${field}'), ${observed})`
      : `json_extract(${value}, '$.${field}')`;
  };
  for (const [store, definition] of Object.entries(SQLITE_RELATIONAL_STORES)) {
    const fields = Object.keys(definition);
    const table = store.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`);
    const columns = fields.map((field) => field.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`));
    const indexed = columns.filter((column) => ['slug', 'workflow_id', 'run_id', 'experiment_id', 'grader_id', 'eval_id'].includes(column));
    const required = new Set(store === 'campaigns' ? []
      : store.endsWith('Observations') || store === 'experimentAssignments'
      ? ['run_id', store === 'experimentAssignments' ? 'experiment_id'
        : store === 'graderObservations' ? 'grader_id' : 'eval_id']
      : ['workflow_id']);
    const existing = new Set(connection.prepare(`PRAGMA table_info(${table})`).all().map((row) => row.name));
    const expected = ['database_name', 'id', ...columns, 'observed_at', 'provenance'];
    const rebuild = existing.size > 0
      && (existing.size !== expected.length || expected.some((column) => !existing.has(column)));
    if (rebuild) {
      connection.exec(`
        DROP TRIGGER IF EXISTS ${table}_insert;
        DROP TRIGGER IF EXISTS ${table}_update;
        DROP TRIGGER IF EXISTS ${table}_delete;
        DROP TABLE ${table};
      `);
    }
    const observationRequired = store === 'campaigns' ? '' : ' NOT NULL';
    connection.exec(`
      CREATE TABLE IF NOT EXISTS ${table} (
        database_name TEXT NOT NULL,
        id TEXT NOT NULL,
        ${columns.map((column, index) => `${column} ${/** @type {Record<string, string>} */ (definition)[fields[index]]}${required.has(column) ? ' NOT NULL' : ''},`).join('\n')}
        observed_at TEXT${observationRequired},
        provenance TEXT${observationRequired},
        PRIMARY KEY (database_name, id)
      );
    `);
    connection.exec(`
      ${indexed.map((column) => `CREATE INDEX IF NOT EXISTS ${table}_${column} ON ${table}(database_name, ${column});`).join('\n')}
      CREATE TRIGGER IF NOT EXISTS ${table}_insert AFTER INSERT ON __idb_records
      WHEN NEW.store_name = '${store}'
      BEGIN
        INSERT INTO ${table} (database_name, id, ${columns.join(', ')}, observed_at, provenance)
        VALUES (NEW.database_name, json_extract(NEW.value, '$.id'),
          ${fields.map((field) => sqlValue(field)).join(', ')},
          json_extract(NEW.value, '$.observedAt'),
          json_extract(NEW.value, '$.provenance'));
      END;
      CREATE TRIGGER IF NOT EXISTS ${table}_update AFTER UPDATE OF value ON __idb_records
      WHEN NEW.store_name = '${store}'
      BEGIN
        UPDATE ${table} SET
          ${fields.map((field, index) => `${columns[index]} = ${sqlValue(field)},`).join('\n')}
          observed_at = json_extract(NEW.value, '$.observedAt'),
          provenance = json_extract(NEW.value, '$.provenance')
        WHERE database_name = NEW.database_name AND id = json_extract(NEW.value, '$.id');
      END;
      CREATE TRIGGER IF NOT EXISTS ${table}_delete AFTER DELETE ON __idb_records
      WHEN OLD.store_name = '${store}'
      BEGIN
        DELETE FROM ${table} WHERE database_name = OLD.database_name AND id = json_extract(OLD.value, '$.id');
      END;
    `);
    if (existing.size === 0 || rebuild) {
      connection.exec(`
        INSERT INTO ${table} (database_name, id, ${columns.join(', ')}, observed_at, provenance)
        SELECT database_name, json_extract(value, '$.id'),
          ${fields.map((field) => sqlValue(field, 'value')).join(', ')},
          json_extract(value, '$.observedAt'), json_extract(value, '$.provenance')
        FROM __idb_records WHERE store_name = '${store}';
      `);
    }
  }
}

/** @typedef {string | string[]} KeyPath */

/** @param {string} filename @param {{ snapshotPath: string, snapshotRevision: string }} [overlay] */
function createConnection(filename, overlay) {
  const connection = new DatabaseSync(filename);
  connection.function('__idb_key_valid', { deterministic: true }, (key) => (
    key === null ? 0 : Number(isValidJsonKey(JSON.parse(String(key))))
  ));
  connection.function('__idb_key_compare', { deterministic: true }, (left, right) => (
    compareKeys(JSON.parse(String(left)), JSON.parse(String(right)))
  ));
  connection.function('__idb_key_sort', { deterministic: true }, (key) => sortableKey(JSON.parse(String(key))));
  connection.exec('PRAGMA busy_timeout = 5000;');
  connection.exec(SQLITE_INDEXEDDB_METADATA_SCHEMA);
  connection.exec('BEGIN IMMEDIATE;');
  try {
    createSqliteRelationalTables(connection);
    if (overlay) attachSqliteOverlay(connection, overlay);
    connection.exec('COMMIT;');
  } catch (error) {
    connection.exec('ROLLBACK;');
    connection.close();
    throw error;
  }
  return connection;
}

/**
 * @template T
 * @param {T} value
 * @returns {T}
 */
function clone(value) {
  return /** @type {T} */ (JSON.parse(JSON.stringify(value)));
}

/** @param {unknown} value */
function encodeKey(value) {
  const encoded = JSON.stringify(value);
  if (encoded === undefined) throw new TypeError('IndexedDB keys must be JSON-serializable');
  return encoded;
}

/**
 * @param {unknown} value
 * @param {KeyPath} keyPath
 * @returns {unknown}
 */
function valueAtKeyPath(value, keyPath) {
  if (Array.isArray(keyPath)) return keyPath.map((path) => valueAtKeyPath(value, path));
  return String(keyPath).split('.').reduce((current, part) => (
    current && typeof current === 'object'
      ? /** @type {Record<string, unknown>} */ (current)[part]
      : undefined
  ), value);
}

/** @param {unknown} value */
function keyRank(value) {
  if (typeof value === 'number') return 1;
  if (value instanceof Date) return 2;
  if (typeof value === 'string') return 3;
  if (Array.isArray(value)) return 4;
  return 5;
}

/**
 * @param {unknown} left
 * @param {unknown} right
 * @returns {number}
 */
function compareKeys(left, right) {
  if (Object.is(left, right)) return 0;
  const rankDifference = keyRank(left) - keyRank(right);
  if (rankDifference !== 0) return rankDifference;
  if (Array.isArray(left) && Array.isArray(right)) {
    for (let index = 0; index < Math.min(left.length, right.length); index += 1) {
      const difference = /** @type {number} */ (compareKeys(left[index], right[index]));
      if (difference !== 0) return difference;
    }
    return left.length - right.length;
  }
  if (typeof left === 'number' && typeof right === 'number') return left - right;
  if (left instanceof Date && right instanceof Date) return left.getTime() - right.getTime();
  const leftText = String(left);
  const rightText = String(right);
  return leftText < rightText ? -1 : leftText > rightText ? 1 : 0;
}

/** @param {unknown} value @returns {boolean} */
function isValidJsonKey(value) {
  return (typeof value === 'number' && Number.isFinite(value))
    || typeof value === 'string'
    || (Array.isArray(value) && value.every(isValidJsonKey));
}

/** @param {unknown} key @returns {Uint8Array} */
function sortableKey(key) {
  if (typeof key === 'number' && Number.isFinite(key)) {
    const bytes = new Uint8Array(9);
    bytes[0] = 1;
    new DataView(bytes.buffer).setFloat64(1, key === 0 ? 0 : key);
    const negative = bytes[1] >= 128;
    for (let index = 1; index < bytes.length; index++) {
      bytes[index] ^= negative ? 255 : index === 1 ? 128 : 0;
    }
    return bytes;
  }
  if (typeof key === 'string') {
    const bytes = new Uint8Array(2 + 3 * key.length);
    bytes[0] = 3;
    for (let index = 0; index < key.length; index++) {
      const unit = key.charCodeAt(index);
      bytes.set([1, unit >> 8, unit & 255], 1 + index * 3);
    }
    return bytes;
  }
  if (Array.isArray(key)) {
    const parts = key.map(sortableKey);
    const bytes = new Uint8Array(2 + parts.reduce((size, part) => size + part.length, 0));
    bytes[0] = 4;
    let offset = 1;
    for (const part of parts) { bytes.set(part, offset); offset += part.length; }
    return bytes;
  }
  throw new TypeError('Invalid IndexedDB key');
}

/**
 * @param {object} target
 * @param {string} name
 * @param {Record<string, unknown>} [event]
 */
function emit(target, name, event = { target }) {
  const listener = Reflect.get(target, name);
  if (typeof listener === 'function') listener(event);
}

class SqliteIDBRequest {
  constructor() {
    this.result = undefined;
    this.error = null;
    this.onsuccess = null;
    this.onerror = null;
  }

  /** @param {any} result */
  succeed(result) {
    this.result = result;
    queueMicrotask(() => emit(this, 'onsuccess'));
  }

  /** @param {unknown} error */
  fail(error) {
    this.error = error instanceof Error ? error : new Error(String(error));
    queueMicrotask(() => emit(this, 'onerror'));
  }
}

class SqliteIDBOpenRequest extends SqliteIDBRequest {
  constructor() {
    super();
    this.onblocked = null;
    this.onupgradeneeded = null;
    this.transaction = /** @type {SqliteIDBTransaction | null} */ (null);
  }
}

class SqliteDOMStringList {
  /** @param {string[]} values */
  constructor(values) {
    this.values = [...values].sort();
  }

  /** @param {string} value */
  contains(value) {
    return this.values.includes(String(value));
  }

  /** @param {number} index */
  item(index) {
    return this.values[index] ?? null;
  }

  get length() {
    return this.values.length;
  }

  [Symbol.iterator]() {
    return this.values[Symbol.iterator]();
  }
}

class SqliteIDBIndex {
  /**
   * @param {SqliteIDBTransaction | null} transaction
   * @param {string} storeName
   * @param {string} name
   * @param {KeyPath} keyPath
   */
  constructor(transaction, storeName, name, keyPath) {
    this.transaction = transaction;
    this.storeName = storeName;
    this.name = name;
    this.keyPath = keyPath;
  }

  /** @param {unknown} [query] @param {number} [count] */
  getAll(query, count) {
    if (!this.transaction) throw new Error('Index is not associated with a transaction');
    const transaction = this.transaction;
    return transaction.runRequest(() => {
      const selection = this.selection(query);
      return transaction.database.connection.prepare(`${selection.text}
        SELECT record_key, value, index_key FROM indexed_records WHERE ${selection.predicate}
        ORDER BY __idb_key_sort(index_key), __idb_key_sort(record_key) LIMIT ?
      `).all(...selection.values, count ?? -1)
        .map((row) => JSON.parse(String(row.value)));
    });
  }

  /** @param {unknown} [query] */
  getAllKeys(query) {
    if (!this.transaction) throw new Error('Index is not associated with a transaction');
    const transaction = this.transaction;
    return transaction.runRequest(() => {
      const selection = this.selection(query);
      return transaction.database.connection.prepare(`${selection.text}
        SELECT record_key FROM indexed_records WHERE ${selection.predicate}
        ORDER BY __idb_key_sort(index_key), __idb_key_sort(record_key)
      `).all(...selection.values).map(row => JSON.parse(String(row.record_key)));
    });
  }

  /** @param {unknown} [query] */
  count(query) {
    if (!this.transaction) throw new Error('Index is not associated with a transaction');
    const transaction = this.transaction;
    return transaction.runRequest(() => {
      const selection = this.selection(query);
      const row = /** @type {{ count: number }} */ (transaction.database.connection.prepare(`${selection.text}
        SELECT count(*) AS count FROM indexed_records WHERE ${selection.predicate}
      `).get(...selection.values));
      return Number(row.count);
    });
  }

  /** @param {unknown} query */
  selection(query) {
    if (!this.transaction) throw new Error('Index is not associated with a transaction');
    const paths = (Array.isArray(this.keyPath) ? this.keyPath : [this.keyPath])
      .map((path) => `$${path.split('.').map((part) => `.${JSON.stringify(part)}`).join('')}`);
    // The JSON -> operator retains types (including booleans and arrays), unlike
    // json_extract. Only extracted keys, never event documents, reach callbacks.
    const keyExpression = Array.isArray(this.keyPath)
      ? `json_array(${paths.map(() => 'value -> ?').join(', ')})`
      : 'value -> ?';
    const values = [...paths, this.transaction.database.name, this.storeName];
    const predicates = ['__idb_key_valid(index_key) = 1'];
    /** @param {unknown} key @param {string} operator */
    const bound = (key, operator) => {
      if (!isValidJsonKey(key)) {
        const error = new TypeError('Invalid IndexedDB key');
        error.name = 'DataError';
        throw error;
      }
      predicates.push(`__idb_key_compare(index_key, ?) ${operator} 0`);
      values.push(encodeKey(key));
    };
    if (query instanceof SqliteIDBKeyRange) {
      if (query.lower !== undefined) bound(query.lower, query.lowerOpen ? '>' : '>=');
      if (query.upper !== undefined) bound(query.upper, query.upperOpen ? '<' : '<=');
    } else if (query !== undefined && query !== null) {
      bound(query, '=');
    }
    return {
      text: `WITH indexed_records AS (
        SELECT record_key, value, ${keyExpression} AS index_key
        FROM __idb_records WHERE database_name = ? AND store_name = ?
      )`,
      values,
      predicate: predicates.join(' AND ')
    };
  }
}

class SqliteIDBObjectStore {
  /**
   * @param {SqliteIDBDatabase} database
   * @param {string} name
   * @param {KeyPath} keyPath
   * @param {SqliteIDBTransaction | null} [transaction]
   */
  constructor(database, name, keyPath, transaction = null) {
    this.database = database;
    this.name = name;
    this.keyPath = keyPath;
    this.transaction = transaction;
  }

  get indexNames() {
    const rows = this.database.connection.prepare(`
      SELECT name
      FROM __idb_indexes
      WHERE database_name = ? AND store_name = ?
      ORDER BY name
    `).all(this.database.name, this.name);
    return new SqliteDOMStringList(rows.map((row) => String(
      /** @type {{ name: string }} */ (row).name
    )));
  }

  /**
   * @param {string} name
   * @param {KeyPath} keyPath
   */
  createIndex(name, keyPath) {
    this.database.connection.prepare(`
      INSERT INTO __idb_indexes (database_name, store_name, name, key_path)
      VALUES (?, ?, ?, ?)
    `).run(this.database.name, this.name, String(name), JSON.stringify(keyPath));
    return new SqliteIDBIndex(
      this.transaction,
      this.name,
      String(name),
      keyPath
    );
  }

  /** @param {string} name */
  index(name) {
    if (!this.transaction) throw new Error('Object store is not associated with a transaction');
    const row = /** @type {{ key_path: string } | undefined} */ (this.database.connection.prepare(`
      SELECT key_path
      FROM __idb_indexes
      WHERE database_name = ? AND store_name = ? AND name = ?
    `).get(this.database.name, this.name, String(name)));
    if (!row) throw new Error(`IndexedDB index does not exist: ${this.name}.${String(name)}`);
    return new SqliteIDBIndex(
      this.transaction,
      this.name,
      String(name),
      JSON.parse(String(row.key_path))
    );
  }

  /** @param {Record<string, unknown>} value */
  put(value) {
    return this.requireTransaction().runRequest(() => {
      const record = clone(value);
      const key = valueAtKeyPath(record, this.keyPath);
      if (key === undefined) throw new TypeError(`Record is missing key path ${String(this.keyPath)}`);
      this.database.connection.prepare(`
        INSERT INTO main.__idb_records (database_name, store_name, record_key, value)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(database_name, store_name, record_key)
        DO UPDATE SET value = excluded.value
      `).run(this.database.name, this.name, encodeKey(key), JSON.stringify(record));
      return clone(key);
    });
  }

  /** @param {unknown} key */
  get(key) {
    return this.requireTransaction().runRequest(() => {
      const row = this.database.connection.prepare(`
        SELECT value
        FROM __idb_records
        WHERE database_name = ? AND store_name = ? AND record_key = ?
      `).get(this.database.name, this.name, encodeKey(key));
      return row ? JSON.parse(String(row.value)) : undefined;
    });
  }

  /** @param {unknown} [query] @param {number} [count] */
  getAll(query, count) {
    return this.requireTransaction().runRequest(() => {
      const predicates = ['database_name = ?', 'store_name = ?'];
      const values = [this.database.name, this.name];
      /** @param {unknown} key @param {string} operator */
      const bound = (key, operator) => {
        if (!isValidJsonKey(key)) throw new TypeError('Invalid IndexedDB key');
        predicates.push(`__idb_key_compare(record_key, ?) ${operator} 0`);
        values.push(encodeKey(key));
      };
      if (query instanceof SqliteIDBKeyRange) {
        if (query.lower !== undefined) bound(query.lower, query.lowerOpen ? '>' : '>=');
        if (query.upper !== undefined) bound(query.upper, query.upperOpen ? '<' : '<=');
      } else if (query !== undefined && query !== null) {
        bound(query, '=');
      }
      return this.database.connection.prepare(`
        SELECT value FROM __idb_records WHERE ${predicates.join(' AND ')}
        ORDER BY __idb_key_sort(record_key) LIMIT ?
      `).all(...values, count ?? -1).map((row) => JSON.parse(String(row.value)));
    });
  }

  /** @param {unknown} [query] */
  count(query) {
    return this.requireTransaction().runRequest(() => {
      const selection = this.keySelection(query);
      const row = /** @type {{ count: number }} */ (this.database.connection.prepare(`
        SELECT count(*) AS count
        FROM __idb_records
        WHERE ${selection.predicate}
      `).get(...selection.values));
      return Number(row.count);
    });
  }

  /** @param {unknown} [query] */
  getAllKeys(query) {
    return this.requireTransaction().runRequest(() => {
      const selection = this.keySelection(query);
      return this.database.connection.prepare(`
        SELECT record_key FROM __idb_records WHERE ${selection.predicate}
        ORDER BY __idb_key_sort(record_key)
      `).all(...selection.values).map(row => JSON.parse(String(row.record_key)));
    });
  }

  /** @param {unknown} query */
  keySelection(query) {
    const predicates = ['database_name = ?', 'store_name = ?'];
    const values = [this.database.name, this.name];
    /** @param {unknown} key @param {string} operator */
    const bound = (key, operator) => {
      if (!isValidJsonKey(key)) throw new TypeError('Invalid IndexedDB key');
      predicates.push(operator === '=' ? 'record_key = ?' : `__idb_key_compare(record_key, ?) ${operator} 0`);
      values.push(encodeKey(key));
    };
    if (query instanceof SqliteIDBKeyRange) {
      if (query.lower !== undefined) bound(query.lower, query.lowerOpen ? '>' : '>=');
      if (query.upper !== undefined) bound(query.upper, query.upperOpen ? '<' : '<=');
    } else if (query !== undefined && query !== null) {
      bound(query, '=');
    }
    return { predicate: predicates.join(' AND '), values };
  }

  /** @param {unknown} key */
  delete(key) {
    return this.requireTransaction().runRequest(() => {
      this.database.markOverlayDeleted(this.name, encodeKey(key));
      this.database.connection.prepare(`
        DELETE FROM main.__idb_records
        WHERE database_name = ? AND store_name = ? AND record_key = ?
      `).run(this.database.name, this.name, encodeKey(key));
      return undefined;
    });
  }

  clear() {
    return this.requireTransaction().runRequest(() => {
      this.database.markOverlayDeleted(this.name);
      this.database.connection.prepare(`
        DELETE FROM main.__idb_records WHERE database_name = ? AND store_name = ?
      `).run(this.database.name, this.name);
      return undefined;
    });
  }

  requireTransaction() {
    if (!this.transaction) throw new Error('Object store is not associated with a transaction');
    return this.transaction;
  }
}

class SqliteIDBTransaction {
  /**
   * @param {SqliteIDBDatabase} database
   * @param {string[]} storeNames
   * @param {'readonly' | 'readwrite' | 'versionchange'} mode
   */
  constructor(database, storeNames, mode) {
    this.database = database;
    this.storeNames = new Set(storeNames);
    this.mode = mode;
    this.error = null;
    this.onabort = /** @type {(() => void) | null} */ (null);
    this.oncomplete = /** @type {(() => void) | null} */ (null);
    this.onerror = /** @type {(() => void) | null} */ (null);
    this.active = true;
    this.aborted = false;
    if (mode === 'readwrite') database.connection.exec('BEGIN IMMEDIATE;');
    database.activeTransactions.add(this);
    if (mode !== 'versionchange') setImmediate(() => this.finish());
  }

  /** @param {string} name */
  objectStore(name) {
    const storeName = String(name);
    if (this.mode !== 'versionchange' && !this.storeNames.has(storeName)) {
      throw new Error(`Object store is outside this transaction: ${storeName}`);
    }
    return this.database.objectStore(storeName, this);
  }

  /** @param {'abort' | 'complete' | 'error'} name @param {() => void} listener */
  addEventListener(name, listener) {
    this[`on${name}`] = listener;
  }

  /** @param {() => any} operation */
  runRequest(operation) {
    const request = new SqliteIDBRequest();
    if (!this.active || this.aborted) {
      request.fail(new Error('IndexedDB transaction is inactive'));
      return request;
    }
    try {
      request.succeed(operation());
    } catch (error) {
      this.error = error instanceof Error ? error : new Error(String(error));
      request.fail(this.error);
    }
    return request;
  }

  abort() {
    if (!this.active || this.aborted) {
      throw new DOMException('IndexedDB transaction is inactive', 'InvalidStateError');
    }
    this.aborted = true;
    queueMicrotask(() => this.finish());
  }

  finish() {
    if (!this.active) return;
    this.active = false;
    try {
      if (this.mode === 'readwrite') {
        this.database.connection.exec(this.error || this.aborted ? 'ROLLBACK;' : 'COMMIT;');
      }
    } catch (error) {
      this.error = error instanceof Error ? error : new Error(String(error));
    }
    debug({
      event: 'transaction-finished',
      mode: this.mode,
      storeCount: this.storeNames.size,
      outcome: this.error || this.aborted ? 'rolled-back' : 'committed'
    });
    if (this.error || this.aborted) {
      if (this.error) emit(this, 'onerror');
      emit(this, 'onabort');
    } else {
      emit(this, 'oncomplete');
    }
    this.database.releaseTransaction(this);
  }
}

class SqliteIDBDatabase {
  /**
   * @param {DatabaseSync} connection
   * @param {string} name
   * @param {number} version
   * @param {boolean} [overlay]
   */
  constructor(connection, name, version, overlay = false) {
    this.connection = connection;
    this.name = name;
    this.version = version;
    this.overlay = overlay;
    this.activeTransactions = /** @type {Set<SqliteIDBTransaction>} */ (new Set());
    this.closeRequested = false;
    this.upgradeTransaction = /** @type {SqliteIDBTransaction | null} */ (null);
  }

  /** @param {string} store @param {string} [key] */
  markOverlayDeleted(store, key) {
    if (!this.overlay) return;
    this.connection.prepare(`
      INSERT OR IGNORE INTO main.__cao_overlay_deleted (database_name,store_name,record_key)
      SELECT database_name,store_name,record_key FROM __idb_records
      WHERE database_name=? AND store_name=? ${key === undefined ? '' : 'AND record_key=?'}
    `).run(this.name, store, ...(key === undefined ? [] : [key]));
  }

  get objectStoreNames() {
    const rows = this.connection.prepare(`
      SELECT name
      FROM __idb_stores
      WHERE database_name = ?
      ORDER BY name
    `).all(this.name);
    return new SqliteDOMStringList(rows.map((row) => String(
      /** @type {{ name: string }} */ (row).name
    )));
  }

  /**
   * @param {string} name
   * @param {{ keyPath?: KeyPath }} [options]
   */
  createObjectStore(name, options = {}) {
    const storeName = String(name);
    const keyPath = options.keyPath;
    if (typeof keyPath !== 'string' && !Array.isArray(keyPath)) {
      throw new TypeError('SQLite IndexedDB stores require a keyPath');
    }
    this.connection.prepare(`
      INSERT INTO __idb_stores (database_name, name, key_path)
      VALUES (?, ?, ?)
    `).run(this.name, storeName, JSON.stringify(keyPath));
    return new SqliteIDBObjectStore(this, storeName, keyPath, this.upgradeTransaction);
  }

  /** @param {string} name */
  deleteObjectStore(name) {
    clearRelationalStores(this.connection, this.name, String(name));
    this.connection.prepare(`
      DELETE FROM __idb_stores
      WHERE database_name = ? AND name = ?
    `).run(this.name, String(name));
  }

  /**
   * @param {string | string[]} storeNames
   * @param {'readonly' | 'readwrite'} [mode]
   */
  transaction(storeNames, mode = 'readonly') {
    const names = Array.isArray(storeNames) ? storeNames.map(String) : [String(storeNames)];
    for (const name of names) this.objectStore(name);
    if (mode !== 'readonly' && mode !== 'readwrite') {
      throw new TypeError(`Unsupported IndexedDB transaction mode: ${String(mode)}`);
    }
    return new SqliteIDBTransaction(this, names, mode);
  }

  /**
   * @param {string} name
   * @param {SqliteIDBTransaction | null} [transaction]
   */
  objectStore(name, transaction = null) {
    const row = /** @type {{ key_path: string } | undefined} */ (this.connection.prepare(`
      SELECT key_path
      FROM __idb_stores
      WHERE database_name = ? AND name = ?
    `).get(this.name, String(name)));
    if (!row) throw new Error(`IndexedDB object store does not exist: ${String(name)}`);
    return new SqliteIDBObjectStore(
      this,
      String(name),
      JSON.parse(String(row.key_path)),
      transaction
    );
  }

  /** @param {string} storeName */
  records(storeName) {
    const query = sql`
      SELECT record_key, value
      FROM __idb_records
      WHERE database_name = ${this.name} AND store_name = ${storeName}
    `;
    return this.connection.prepare(query.text).all(...query.values).map((row) => ({
      key: JSON.parse(String(/** @type {{ record_key: string }} */ (row).record_key)),
      value: JSON.parse(String(/** @type {{ value: string }} */ (row).value))
    }));
  }

  close() {
    this.closeRequested = true;
    this.closeConnectionIfIdle();
  }

  /** @param {SqliteIDBTransaction} transaction */
  releaseTransaction(transaction) {
    this.activeTransactions.delete(transaction);
    this.closeConnectionIfIdle();
  }

  closeConnectionIfIdle() {
    if (this.closeRequested && this.activeTransactions.size === 0) this.connection.close();
  }
}

export class SqliteIDBKeyRange {
  /**
   * @param {unknown} lower
   * @param {unknown} upper
   * @param {boolean} lowerOpen
   * @param {boolean} upperOpen
   */
  constructor(lower, upper, lowerOpen, upperOpen) {
    this.lower = lower;
    this.upper = upper;
    this.lowerOpen = lowerOpen;
    this.upperOpen = upperOpen;
  }

  /**
   * @param {unknown} lower
   * @param {unknown} upper
   * @param {boolean} [lowerOpen]
   * @param {boolean} [upperOpen]
   */
  static bound(lower, upper, lowerOpen = false, upperOpen = false) {
    if (compareKeys(lower, upper) > 0) throw new TypeError('Lower bound must not exceed upper bound');
    return new SqliteIDBKeyRange(lower, upper, lowerOpen, upperOpen);
  }

  /** @param {unknown} value */
  static only(value) {
    return new SqliteIDBKeyRange(value, value, false, false);
  }

  /**
   * @param {unknown} lower
   * @param {boolean} [open]
   */
  static lowerBound(lower, open = false) {
    return new SqliteIDBKeyRange(lower, undefined, open, false);
  }

  /**
   * @param {unknown} upper
   * @param {boolean} [open]
   */
  static upperBound(upper, open = false) {
    return new SqliteIDBKeyRange(undefined, upper, false, open);
  }
}

export class SqliteIndexedDBFactory {
  /** @param {string} filename @param {{ snapshotPath: string, snapshotRevision: string }} [overlay] */
  constructor(filename, overlay) {
    if (typeof filename !== 'string' || !filename.trim()) {
      throw new TypeError('SQLite database filename is required');
    }
    this.filename = filename;
    this.overlay = overlay;
  }

  /**
   * @param {string} name
   * @param {number} [version]
   */
  open(name, version) {
    const request = new SqliteIDBOpenRequest();
    queueMicrotask(() => {
      let connection;
      try {
        connection = createConnection(this.filename, this.overlay);
        const databaseName = String(name);
        const row = /** @type {{ version: number } | undefined} */ (connection.prepare(`
          SELECT version FROM __idb_databases WHERE name = ?
        `).get(databaseName));
        const oldVersion = row ? Number(row.version) : 0;
        const requestedVersion = version === undefined ? (oldVersion || 1) : Number(version);
        if (!Number.isInteger(requestedVersion) || requestedVersion < 1) {
          throw new TypeError('IndexedDB version must be a positive integer');
        }
        if (requestedVersion < oldVersion) {
          const error = new Error(`Requested version ${requestedVersion} is older than ${oldVersion}`);
          error.name = 'VersionError';
          throw error;
        }

        if (this.overlay && requestedVersion !== oldVersion) {
          throw new Error('SQLite overlays cannot migrate the deployed snapshot schema');
        }
        const database = new SqliteIDBDatabase(connection, databaseName, requestedVersion, Boolean(this.overlay));
        if (requestedVersion > oldVersion) {
          connection.exec('BEGIN IMMEDIATE;');
          try {
            if (oldVersion === 0) {
              connection.prepare(`
                INSERT INTO __idb_databases (name, version) VALUES (?, 0)
              `).run(databaseName);
            }
            request.result = database;
            database.upgradeTransaction = new SqliteIDBTransaction(database, [], 'versionchange');
            request.transaction = database.upgradeTransaction;
            emit(request, 'onupgradeneeded', {
              target: request,
              oldVersion,
              newVersion: requestedVersion
            });
            if (database.upgradeTransaction.error) throw database.upgradeTransaction.error;
            connection.prepare(`
              UPDATE __idb_databases SET version = ? WHERE name = ?
            `).run(requestedVersion, databaseName);
            connection.exec('COMMIT;');
            database.upgradeTransaction.finish();
            database.upgradeTransaction = null;
            request.transaction = null;
          } catch (error) {
            connection.exec('ROLLBACK;');
            throw error;
          }
        }
        debug({
          event: 'database-opened',
          oldVersion,
          newVersion: requestedVersion,
          upgraded: requestedVersion > oldVersion
        });
        request.succeed(database);
      } catch (error) {
        if (connection) connection.close();
        debug({ event: 'database-open-failed', error: error instanceof Error ? error.name : 'Error' });
        request.fail(error);
      }
    });
    return request;
  }

  /** @param {string} name */
  deleteDatabase(name) {
    const request = new SqliteIDBOpenRequest();
    queueMicrotask(() => {
      let connection;
      try {
        if (this.overlay) throw new Error('SQLite overlays cannot delete the deployed database');
        connection = createConnection(this.filename);
        connection.exec('BEGIN IMMEDIATE;');
        try {
          clearRelationalStores(connection, String(name));
          connection.prepare('DELETE FROM __idb_databases WHERE name = ?').run(String(name));
          connection.exec('COMMIT;');
        } catch (error) {
          connection.exec('ROLLBACK;');
          throw error;
        }
        connection.close();
        request.succeed(undefined);
      } catch (error) {
        if (connection) connection.close();
        request.fail(error);
      }
    });
    return request;
  }

  async databases() {
    const connection = createConnection(this.filename, this.overlay);
    try {
      return connection.prepare(`
        SELECT name, version FROM __idb_databases ORDER BY name
      `).all().map((row) => ({
        name: String(/** @type {{ name: string }} */ (row).name),
        version: Number(/** @type {{ version: number }} */ (row).version)
      }));
    } finally {
      connection.close();
    }
  }

  /**
   * @param {unknown} first
   * @param {unknown} second
   */
  cmp(first, second) {
    return compareKeys(first, second);
  }
}

/**
 * @param {string} filename
 * @param {{ snapshotPath: string, snapshotRevision: string }} [overlay]
 * @returns {IDBFactory & SqliteIndexedDBFactory}
 */
export function createSqliteIndexedDB(filename, overlay) {
  return /** @type {IDBFactory & SqliteIndexedDBFactory} */ (
    /** @type {unknown} */ (new SqliteIndexedDBFactory(filename, overlay))
  );
}

/**
 * @param {string} filename
 * @param {{ snapshotPath: string, snapshotRevision: string }} [overlay]
 * @returns {IDBFactory & SqliteIndexedDBFactory}
 */
export function installSqliteIndexedDB(filename, overlay) {
  const indexedDB = createSqliteIndexedDB(filename, overlay);
  globalThis.indexedDB = indexedDB;
  globalThis.IDBKeyRange = /** @type {typeof IDBKeyRange} */ (
    /** @type {unknown} */ (SqliteIDBKeyRange)
  );
  return indexedDB;
}
