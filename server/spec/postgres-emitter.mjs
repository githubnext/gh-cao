import { createTypeSpecLibrary, emitFile, getNamespaceFullName, navigateProgram } from '@typespec/compiler';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';

export const $lib = createTypeSpecLibrary({ name: 'cao-postgres', diagnostics: {} });
export const namespace = 'Cao.Postgres';
const parentKey = Symbol.for('cao-postgres.parent');
const partitionKey = Symbol.for('cao-postgres.run-partition');
const sampledKey = Symbol.for('cao-postgres.sampled-bundle');
export function $sampledBundle(context, target) {
  context.program.stateSet(sampledKey).add(target);
}
export function $parent(context, target, entity, required = true) {
  context.program.stateMap(parentKey).set(target, { entity, required });
}
export function $runPartition(context, target) {
  context.program.stateSet(partitionKey).add(target);
}

const snake = (field) => field.replace(/-/g, '_').replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`);
const kinds = new Map([
  ['string', ['', 'TEXT']], ['decimal', ['numeric', 'NUMERIC']],
  ['int64', ['numeric', 'BIGINT']], ['boolean', ['boolean', 'BOOLEAN']],
  ['utcDateTime', ['timestamp', 'TIMESTAMPTZ']], ['bytes', ['', 'BYTEA']]
]);
const inputNames = new Map([
  ['securityFindings', '$security-findings'], ['outcomePerformance', '$outcomes'],
  ['transactions', '$transactions'], ['workItems', 'work-items']
]);
const runtimeNames = new Map([
  ['cliCommands', 'cli-commands'], ['cliCommandSummary', 'cli-command-summary'],
  ['cliCommandStatuses', 'cli-command-statuses'],
  ['collectionHealth', 'collection-health'], ['githubQuotaUsage', 'github-quota-usage'],
  ['webhookStatus', 'webhook-status'],
  ['valueCollectionCosts', 'value-collection-costs'],
  ['simulationDays', 'simulation-days']
]);

export async function $onEmit(context) {
  const inventoryContract = JSON.parse(readFileSync(new URL('../../dashboard/site/src/data/queries/ingestion.json', import.meta.url)));
  const tables = [];
  const systems = [];
  const extensions = [];
  const children = [];
  const statistical = [];
  const parents = new Map();
  navigateProgram(context.program, {
    model(model) {
      const namespace = getNamespaceFullName(model.namespace);
      if (!['Cao.Postgres', 'Cao.Postgres.Inputs', 'Cao.Postgres.Storage', 'Cao.Postgres.Runtime', 'Cao.Postgres.Storage.Statistical', 'Cao.Postgres.Daily'].includes(namespace)) return;
      const columns = [];
      for (const property of model.properties.values()) {
        if (property.type.kind === 'Model' && property.type.name === 'Array') {
          const member = property.type.indexer?.value;
          if (member?.kind !== 'Model' || getNamespaceFullName(member.namespace) !== 'Cao.Postgres.Values') {
            throw new Error(`Storage arrays require typed child values: ${model.name}.${property.name}`);
          }
          const childColumns = [
            { field: 'id', name: 'id', kind: '', sql: 'TEXT' },
            { field: 'packetId', name: 'packet_id', kind: '', sql: 'TEXT' }
          ];
          for (const field of member.properties.values()) {
            const kind = field.type.kind === 'Scalar' && kinds.get(field.type.name);
            if (!kind) throw new Error(`Storage child requires native types: ${member.name}.${field.name}`);
            childColumns.push({ field: field.name, name: snake(field.name), kind: kind[0], sql: kind[1] });
          }
          const collection = `${model.name}.${property.name}`;
          children.push({ collection, name: `${snake(model.name)}_${snake(property.name)}`,
            source: `$${collection}`, columns: childColumns, partitioned: false,
            canonical: false, runtime: false, owner: model.name, field: property.name });
          parents.set(collection, [['packetId', model.name, false]]);
          continue;
        }
        const relationship = context.program.stateMap(parentKey).get(property);
        if (relationship) {
          const declared = parents.get(model.name) ?? [];
          declared.push([property.name, relationship.entity, !relationship.required]);
          parents.set(model.name, declared);
        }
        if (property.type.kind === 'Model' && getNamespaceFullName(property.type.namespace) === 'Cao.Postgres.Values') {
          columns.push({ field: property.name, name: `${snake(property.name)}_object`, kind: 'object', sql: 'BOOLEAN' });
          for (const member of property.type.properties.values()) {
            const kind = member.type.kind === 'Scalar' && kinds.get(member.type.name);
            if (!kind) throw new Error(`Unsupported object member ${model.name}.${property.name}.${member.name}`);
            columns.push({ field: `${property.name}.${member.name}`, name: `${snake(property.name)}_${snake(member.name)}`, kind: kind[0], sql: kind[1] });
          }
          continue;
        }
        const kind = property.type.kind === 'Scalar' && kinds.get(property.type.name);
        if (!kind) throw new Error(`Storage requires an explicit native type: ${model.name}.${property.name}`);
        columns.push({ field: property.name, name: snake(property.name), kind: kind[0], sql: kind[1], required: !property.optional });
      }
      const table = { collection: model.name, name: snake(model.name), columns, partitioned: context.program.stateSet(partitionKey).has(model),
        sampled: context.program.stateSet(sampledKey).has(model) };
      if (namespace === 'Cao.Postgres.Storage.Statistical') statistical.push(table);
      else if (namespace === 'Cao.Postgres.Daily') extensions.push(table);
      else if (namespace === 'Cao.Postgres.Storage') systems.push(table);
      else {
        table.runtime = namespace === 'Cao.Postgres.Runtime' || model.name === 'marketplacePackages';
        table.canonical = namespace === 'Cao.Postgres' && !table.runtime;
        table.source = inputNames.get(model.name) ?? runtimeNames.get(model.name) ?? `$${model.name}`;
        tables.push(table);
      }
    }
  });
  tables.push(...children);
  tables.sort((a, b) => a.collection.localeCompare(b.collection));
  const ordered = [];
  const visit = (table) => {
    if (ordered.includes(table)) return;
    for (const [, parent] of parents.get(table.collection) ?? []) {
      visit(tables.find((candidate) => candidate.collection === parent));
    }
    ordered.push(table);
  };
  for (const table of tables) visit(table);
  tables.splice(0, tables.length, ...ordered);
  const statements = [
    '-- Generated from server/spec/storage.tsp. Do not edit.',
    '-- Fresh database only. Opaque artifacts remain external.',
    `-- Inventory normalization contract: ${createHash('sha256').update(JSON.stringify(inventoryContract.observations.filter((o) => ['campaign','repository','workflow'].includes(o.kind)))).digest('hex')}`
  ];
  for (const table of systems) {
    const constraints = table.name === 'cao_github_resources' ? [
      'PRIMARY KEY (namespace, id)',
      'CHECK (octet_length(id) <= 512)',
      'CHECK (octet_length(etag) <= 4096 AND octet_length(upstream_etag) <= 4096)'
    ] : table.name === 'cao_acquisition_scopes' ? [
      'PRIMARY KEY (namespace, repository)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      "CHECK (repository ~ '^[a-z0-9][a-z0-9-]*/[a-z0-9._-]+$' AND octet_length(repository) <= 256)",
      'CHECK (installation_id >= 0 AND epoch > 0)'
    ] : table.name === 'cao_acquisition_attempts' ? [
      'PRIMARY KEY (namespace, repository, run_id, attempt)',
      'FOREIGN KEY (namespace, repository) REFERENCES cao_acquisition_scopes(namespace, repository) ON DELETE CASCADE',
      'CHECK (run_id > 0 AND attempt > 0 AND epoch > 0 AND generation > 0)',
      'CHECK ((token IS NULL) = (lease_until IS NULL))',
      "CHECK (token IS NULL OR token ~ '^[a-f0-9]{32}$')",
      "CHECK (digest IS NULL OR digest ~ '^[a-f0-9]{64}$')",
      "CHECK (consumed_digest IS NULL OR consumed_digest ~ '^[a-f0-9]{64}$')",
      'CHECK ((digest IS NULL) = (content IS NULL) AND (digest IS NULL) = (published_at IS NULL))',
      'CHECK (octet_length(content) <= 33554432)'
    ] : table.name === 'cao_acquisition_outcomes' ? [
      'PRIMARY KEY (namespace, repository, url)',
      'FOREIGN KEY (namespace, repository) REFERENCES cao_acquisition_scopes(namespace, repository) ON DELETE CASCADE',
      'CHECK (epoch > 0)',
      'CHECK (octet_length(url) BETWEEN 1 AND 2048)',
      'CHECK (octet_length(content) BETWEEN 1 AND 32768)'
    ] : table.name === 'cao_acquisition_value_hints' ? [
      'PRIMARY KEY (namespace, repository)',
      'FOREIGN KEY (namespace, repository) REFERENCES cao_acquisition_scopes(namespace, repository) ON DELETE CASCADE',
      'CHECK (epoch > 0 AND dirty >= 0 AND processed >= 0 AND processed <= dirty)',
      "CHECK (attempt_day IS NULL OR attempt_day ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$')"
    ] : table.name === 'cao_acquisition_inventory_config' ? [
      'PRIMARY KEY (namespace)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      "CHECK (source_repository ~ '^[a-z0-9][a-z0-9-]*/[a-z0-9._-]+$' AND octet_length(source_repository) <= 256)",
      "CHECK (source_sha ~ '^[a-f0-9]{40}$' AND policy_sha ~ '^[a-f0-9]{40}$')",
      "CHECK (snapshot_digest ~ '^[a-f0-9]{64}$')"
    ] : table.name === 'cao_acquisition_inventory' ? [
      'PRIMARY KEY (namespace, repository)',
      'FOREIGN KEY (namespace, repository) REFERENCES cao_acquisition_scopes(namespace, repository) ON DELETE CASCADE',
      'CHECK (installation_id > 0 AND epoch > 0 AND generation > 0)',
      'CHECK (published_generation >= 0 AND published_generation <= generation)',
      "CHECK (snapshot_digest ~ '^[a-f0-9]{64}$')",
      'CHECK ((content IS NULL) = (observed_at IS NULL))',
      'CHECK (octet_length(content) <= 16777216)'
    ] : table.name === 'cao_archive_sources' ? [
      'PRIMARY KEY (namespace, destination, repository, installation_id, epoch)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      "CHECK (repository ~ '^[a-z0-9][a-z0-9-]*/[a-z0-9._-]+$' AND octet_length(repository) <= 256)",
      'CHECK (octet_length(destination) BETWEEN 1 AND 512)',
      'CHECK (installation_id > 0 AND epoch > 0)',
      'CHECK (NOT cleanup_pending OR withdrawn)'
    ] : table.name === 'cao_trace_objects' ? [
      'PRIMARY KEY (namespace, destination, key)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      'CHECK (octet_length(destination) BETWEEN 1 AND 512)',
      "CHECK (key ~ '^(ledger[.]json|[a-f0-9]{64}[.](trace|dataset|mining))$')",
      'CHECK (octet_length(content) <= 134217728)'
    ] : table.name === 'cao_value_proof_files' ? [
      'PRIMARY KEY (namespace, repository, workflow, path)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      "CHECK (repository ~ '^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$')",
      "CHECK (workflow ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$')",
      'CHECK (octet_length(repository) <= 256 AND octet_length(workflow) <= 256)',
      "CHECK (digest ~ '^[a-f0-9]{64}$')",
      'CHECK (octet_length(path) BETWEEN 1 AND 512)',
      'CHECK (octet_length(content) <= 16777216)'
    ] : table.name === 'cao_value_collection_costs' ? [
      'PRIMARY KEY (namespace, installation_id, repository, workflow, day)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      'CHECK (installation_id > 0)',
      "CHECK (repository ~ '^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$')",
      "CHECK (workflow ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$')",
      'CHECK (octet_length(repository) <= 256 AND octet_length(workflow) <= 256)',
      'CHECK (octet_length(collector) BETWEEN 1 AND 512)',
      "CHECK (day = date_trunc('day', day AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')",
      'CHECK (attempts > 0 AND successes >= 0 AND failures >= 0 AND successes + failures = attempts)',
      'CHECK (api_requests >= 0 AND core_requests >= 0 AND search_requests >= 0 AND graphql_requests >= 0)',
      'CHECK (api_requests = core_requests + search_requests + graphql_requests)',
      'CHECK (cache_hits >= 0 AND preflight_requests >= 0 AND preflight_requests <= core_requests)',
      'CHECK (graphql_points_observed >= 0 AND graphql_requests_unverified >= 0 AND graphql_requests_unverified <= graphql_requests)',
      'CHECK (wall_ms >= 0 AND max_wall_ms >= 0 AND max_wall_ms <= wall_ms AND output_bytes >= 0 AND transport_bytes >= 0)',
      "CHECK ((failures = 0 AND last_failure_reason IS NULL) OR (failures > 0 AND last_failure_reason IN ('api-budget','budget-unavailable','timeout','output-budget','collector-failed')))"
    ] : table.name === 'cao_value_publications' ? [
      'PRIMARY KEY (namespace, packet_id)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      "CHECK (digest ~ '^[a-f0-9]{64}$')"
    ] : table.name === 'cao_contract' ? ['PRIMARY KEY (digest)'] : table.name === 'cao_state' ? ['PRIMARY KEY (namespace)'] : table.name === 'cao_repository_lifecycle' ? [
      'PRIMARY KEY (namespace, id)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      "CHECK (lifecycle IN ('active','archived','deleted'))"
    ] : [
      'PRIMARY KEY (namespace, collection)',
      'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE',
      `CHECK (collection IN (${tables.filter((t) => !t.runtime && !t.owner).map((t) => `'${t.source}'`).join(',')}))`,
      "CHECK (availability IN ('available','empty','unavailable'))",
      "CHECK (completeness IN ('complete','partial','unknown'))",
      "CHECK (freshness IN ('current','stale','unknown'))"
    ];
    statements.push(`CREATE TABLE IF NOT EXISTS ${table.name} (\n${table.columns.map((c) => `  ${c.name} ${c.sql}${c.required ? ' NOT NULL' : ''}${table.name === 'cao_acquisition_inventory' && c.name === 'published_generation' ? ' DEFAULT 0' : ''}`).join(',\n')},\n  ${constraints.join(',\n  ')}\n);`);
    if (table.name === 'cao_repository_lifecycle') statements.push('CREATE INDEX IF NOT EXISTS cao_repository_lifecycle_coordinate ON cao_repository_lifecycle (namespace, owner, name);');
    if (table.name === 'cao_github_resources') statements.push('CREATE INDEX IF NOT EXISTS cao_github_resources_expiry ON cao_github_resources (namespace, validated_at, id);');
    if (table.name === 'cao_acquisition_attempts') statements.push('CREATE INDEX IF NOT EXISTS cao_acquisition_pending ON cao_acquisition_attempts (namespace, repository, run_id, attempt) WHERE digest IS DISTINCT FROM consumed_digest;');
    if (table.name === 'cao_archive_sources') {
      statements.push('CREATE INDEX IF NOT EXISTS cao_archive_source_repository ON cao_archive_sources (namespace, repository);');
      statements.push('CREATE INDEX IF NOT EXISTS cao_archive_source_cleanup ON cao_archive_sources (namespace, destination, repository, installation_id, epoch) WHERE cleanup_pending;');
    }
    if (table.name === 'cao_value_collection_costs') {
      statements.push('CREATE INDEX IF NOT EXISTS cao_value_collection_costs_recent ON cao_value_collection_costs (namespace, day DESC, repository, workflow);');
      statements.push('CREATE INDEX IF NOT EXISTS cao_value_collection_costs_installation ON cao_value_collection_costs (namespace, installation_id, day DESC);');
    }
  }
  const go = ['// Code generated from server/spec/storage.tsp. DO NOT EDIT.', '', 'package postgresx', '', 'var entityTables = map[string]entityTable{'];
  for (const table of tables) {
    if (!table.runtime) {
      const constraints = [
        `PRIMARY KEY (namespace, id${table.partitioned ? ', run_at' : ''})`,
        `UNIQUE (namespace, ordinal${table.partitioned ? ', run_at' : ''})`,
        `CHECK (bit_length(present_fields) = ${table.columns.length})`,
        'FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE'
      ];
      for (const [field, parent, optional] of parents.get(table.collection) ?? []) {
        if (!table.columns.some((c) => c.field === field)) throw new Error(`Missing relationship ${table.collection}.${field}`);
        if (!optional) constraints.push(`CHECK (${snake(field)} IS NOT NULL AND ${snake(field)} <> '')`);
        const parentPartitioned = tables.find((candidate) => candidate.collection === parent)?.partitioned;
        constraints.push(`FOREIGN KEY (namespace, ${snake(field)}${parentPartitioned ? ', run_at' : ''}) REFERENCES ${snake(parent)}(namespace, id${parentPartitioned ? ', run_at' : ''}) DEFERRABLE INITIALLY DEFERRED`);
      }
      statements.push(`CREATE TABLE IF NOT EXISTS ${table.name} (
  namespace TEXT NOT NULL,
  ordinal BIGINT NOT NULL CHECK (ordinal >= 0),
  present_fields BIT VARYING NOT NULL,
${table.columns.map((c) => `  ${c.name} ${c.sql}${c.field === 'id' ? " NOT NULL CHECK (id <> '')" : ''}`).join(',\n')},
${table.partitioned ? '  run_at TIMESTAMPTZ NOT NULL,\n' : ''}
  ${constraints.join(',\n  ')}
)${table.partitioned ? ' PARTITION BY RANGE (run_at)' : ''};`);
      for (const [field] of parents.get(table.collection) ?? []) {
        statements.push(`CREATE INDEX IF NOT EXISTS ${table.name}_${snake(field)} ON ${table.name} (namespace, ${snake(field)}, ordinal);`);
      }
      if (table.partitioned) statements.push(`CREATE INDEX IF NOT EXISTS ${table.name}_identity ON ${table.name} (namespace, id);`);
      if (table.collection === 'runs') {
        statements.push(`CREATE INDEX IF NOT EXISTS runs_statistical_watermark ON runs
  (namespace, greatest(started_at, created_at, completed_at, updated_at) DESC);`);
        statements.push(`CREATE INDEX IF NOT EXISTS runs_statistical_candidates ON runs
  (namespace, status, coalesce(started_at, created_at), id COLLATE "C");`);
      }
    }
    go.push(`\t"${table.source}": {name: "${table.name}", runtime: ${table.runtime}, canonical: ${table.canonical}, partitioned: ${table.partitioned},${table.owner ? ' internal: true,' : ''} columns: []entityColumn{`);
    for (const column of table.columns) {
      go.push(`\t\t{field: "${column.field}", name: "${column.name}", kind: "${column.kind}", sql: "${column.sql}"},`);
    }
    const owned = children.filter((child) => child.owner === table.collection);
    go.push(owned.length ? `\t}, children: []entityChild{${owned
      .map((child) => `{field: "${child.field}", source: "${child.source}"}`).join(',')}}},` : '\t}},');
  }
  go.push('}', '');
  // Private snapshots are typed copies, not canonical inputs or TTL partitions.
  go.push('var statisticalTables = map[string]entityTable{');
  for (const table of [...tables.filter((t) => t.sampled), ...statistical]) {
    const name = `cao_sample_${table.name}`;
    const source = table.source ?? `$${table.collection}`;
    const constraints = (parents.get(table.collection) ?? []).map(([field, parent]) => {
      if (!tables.find((t) => t.collection === parent)?.sampled) throw new Error(`Unsampled bundle parent ${parent}`);
      return `FOREIGN KEY (namespace, ${snake(field)}) REFERENCES cao_sample_${snake(parent)}(namespace, id) DEFERRABLE INITIALLY DEFERRED`;
    });
    statements.push(`CREATE TABLE IF NOT EXISTS ${name} (
  namespace TEXT NOT NULL REFERENCES cao_state(namespace) ON DELETE CASCADE,
  ordinal BIGINT NOT NULL CHECK (ordinal >= 0),
  present_fields BIT VARYING NOT NULL CHECK (bit_length(present_fields) = ${table.columns.length}),
${table.columns.map((c) => `  ${c.name} ${c.sql}${c.field === 'id' ? " NOT NULL CHECK (id <> '')" : statistical.includes(table) && c.required ? ' NOT NULL' : ''}`).join(',\n')},
${table.partitioned ? '  run_at TIMESTAMPTZ NOT NULL,\n' : ''}
  PRIMARY KEY (namespace, id),
  UNIQUE (namespace, ordinal)${constraints.length ? ',\n  ' + constraints.join(',\n  ') : ''}
);`);
    for (const [field] of parents.get(table.collection) ?? []) {
      statements.push(`CREATE INDEX IF NOT EXISTS ${name}_${snake(field)} ON ${name} (namespace, ${snake(field)}, ordinal);`);
    }
    go.push(`\t"${source}": {name: "${name}", internal: true, partitioned: ${table.partitioned}, columns: []entityColumn{`);
    for (const column of table.columns) {
      go.push(`\t\t{field: "${column.field}", name: "${column.name}", kind: "${column.kind}", sql: "${column.sql}"},`);
    }
    go.push('\t}},');
  }
  go.push('}', '');
  for (const table of systems.filter((t) => t.sampled)) {
    statements.push(`CREATE TABLE IF NOT EXISTS cao_sample_${table.name} (
${table.columns.map((c) => `  ${c.name} ${c.sql}${c.required ? ' NOT NULL' : ''}`).join(',\n')},
  PRIMARY KEY (namespace, collection),
  FOREIGN KEY (namespace) REFERENCES cao_state(namespace) ON DELETE CASCADE
);`);
  }
  const literal = (value) => value === null ? 'nil' : JSON.stringify(value);
  go.push('var inventoryBindings = map[string][]inventoryBinding{');
  for (const [source, kind] of [['campaigns','campaign'],['repositories','repository'],['workflows','workflow']]) {
    const table = tables.find((t) => t.source === `$${source}`);
    const observation = inventoryContract.observations.find((o) => o.kind === kind);
    go.push(`\t"${source}": {`);
    for (const [field, mapping] of Object.entries(observation.data)) {
      if (!table.columns.some((column) => column.field === field)) continue;
      const fields = (mapping.fields ?? [mapping.field]).filter((field) => !field.startsWith('_'));
      if (!fields.length) continue; // Coordinate normalization owns identity fields.
      const options = [`field: ${literal(field)}`, `inputs: []string{${fields.map(literal).join(',')}}`];
      if (Object.hasOwn(mapping,'default')) options.push(`defaultPresent: true, defaultValue: ${literal(mapping.default)}`);
      if (mapping.trim) options.push('trim: true');
      if (mapping.omitEmpty) options.push('omitEmpty: true');
      if (Object.hasOwn(mapping,'equals')) options.push(`equalsPresent: true, equals: ${literal(mapping.equals)}`);
      if (Object.hasOwn(mapping,'notEquals')) options.push(`notEqualsPresent: true, notEquals: ${literal(mapping.notEquals)}`);
      go.push(`\t\t{${options.join(', ')}},`);
    }
    go.push('\t},');
  }
  go.push('}', '');
  await emitFile(context.program, { path: `${context.emitterOutputDir}/schema.sql`, content: `${statements.join('\n\n')}\n` });
  await emitFile(context.program, { path: `${context.emitterOutputDir}/schema.gen.go`, content: go.join('\n') });
  const extensionSQL = ['-- Generated from server/spec/daily-counts.tsp. Do not edit.',
    'CREATE TABLE IF NOT EXISTS cao_daily_contract (digest TEXT PRIMARY KEY);'];
  for (const table of extensions) {
    const constraints = ['PRIMARY KEY (namespace, id)'];
    if (table.collection === 'dailyRunCounts') {
      constraints.push('UNIQUE (namespace, day, repository_id, workflow_id)',
        'CHECK (total >= 0 AND successful >= 0 AND failed >= 0 AND approval >= 0 AND pending >= 0 AND other >= 0)',
        'CHECK (total = successful + failed + approval + pending + other)');
    }
    extensionSQL.push(`CREATE TABLE IF NOT EXISTS ${table.name} (\n${table.columns.map((c) => `  ${c.name} ${c.sql}${c.required ? ' NOT NULL' : ''}`).join(',\n')},\n  ${constraints.join(',\n  ')}\n);`);
  }
  await emitFile(context.program, { path: `${context.emitterOutputDir}/daily-schema.sql`, content: `${extensionSQL.join('\n\n')}\n` });
}
