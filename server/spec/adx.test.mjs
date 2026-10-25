import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { compile, getNamespaceFullName, navigateProgram, NodeHost } from '@typespec/compiler';
import { $onEmit } from './adx-emitter.mjs';

const entry = fileURLToPath(new URL('adx.tsp', import.meta.url));
const program = await compile(NodeHost, entry, { noEmit: true });
const canonical = await compile(NodeHost, fileURLToPath(new URL('storage.tsp', import.meta.url)), { noEmit: true });
assert.deepEqual(program.diagnostics, []);
assert.deepEqual(canonical.diagnostics, []);

function models(program, namespace) {
  const result = new Map();
  navigateProgram(program, {
    model(model) {
      if (getNamespaceFullName(model.namespace) === namespace) result.set(model.name, model);
    }
  });
  return result;
}

const declarations = models(program, 'Cao.ADX');
const sources = program.stateMap(Symbol.for('cao-adx.source'));
const facts = [...declarations.values()].filter((model) => sources.has(model));
const storage = models(canonical, 'Cao.Postgres');

function scalar(type) {
  if (type.kind === 'Union') return scalar([...type.variants.values()].find((variant) => variant.type.name !== 'null').type);
  return type.name;
}

function properties(model) {
  return [...(model.baseModel ? properties(model.baseModel) : []), ...model.properties.values()];
}

test('ADX declares five typed canonical fact families and one explicit ready manifest', () => {
  assert.deepEqual(facts.map((model) => model.name).sort(),
    ['EvalObservations', 'FirewallEvents', 'GraderObservations', 'SessionFacts', 'ToolEvents']);
  const manifest = declarations.get('SessionVersions');
  assert.equal(manifest.baseModel.name, 'Identity');
  assert.equal(declarations.get('Identity').properties.get('ContractVersion').type.value, 1);
  assert.equal(manifest.properties.get('Coverage').type.value, 'retained-canonical-snapshot');
  assert.equal(manifest.properties.get('UsageObservations').type.value, 'unavailable');
  for (const model of facts) {
    assert.equal(model.baseModel.name, 'Observation');
    const source = storage.get(sources.get(model).slice(1));
    assert.ok(source, `missing canonical source for ${model.name}`);
    assert.equal(manifest.properties.get(`${model.name}Count`).type.name, 'int64');
    for (const property of model.properties.values()) {
      const field = property.name[0].toLowerCase() + property.name.slice(1);
      const canonicalField = source.properties.get(field);
      assert.ok(canonicalField, `missing canonical field ${sources.get(model)}.${field}`);
      assert.equal(property.optional, true, `preserve absent evidence: ${model.name}.${property.name}`);
      assert.equal(property.type.kind, 'Union');
      assert.ok([...property.type.variants.values()].some((variant) => variant.type.name === 'null'));
      const sourceType = scalar(canonicalField.type);
      assert.equal(scalar(property.type), sourceType === 'decimal' ? 'float64' : sourceType,
        `canonical/ADX type mismatch: ${model.name}.${property.name}`);
    }
  }
});

test('generated Go allowlist and KQL exactly reproduce the TypeSpec source', async () => {
  const directory = mkdtempSync(fileURLToPath(new URL('.adx-spec-test-', import.meta.url)));
  try {
    await $onEmit({ program, emitterOutputDir: directory, options: {} });
    const go = execFileSync('gofmt', [`${directory}/schema.gen.go`], { encoding: 'utf8' });
    const kql = readFileSync(`${directory}/schema.gen.kql`, 'utf8');
    assert.equal(go, readFileSync(new URL('../internal/adxingest/schema.gen.go', import.meta.url), 'utf8'));
    assert.equal(kql, readFileSync(new URL('../internal/adxingest/schema.gen.kql', import.meta.url), 'utf8'));
    for (const model of [...facts, declarations.get('SessionVersions')]) {
      const columns = properties(model).map((property) => property.name);
      const definition = kql.split(`.create-merge table ${model.name} (`)[1]?.split(');')[0];
      assert.deepEqual(definition.split(', ').map((column) => column.split(':')[0]), columns);
      const mapping = JSON.parse(kql.split(`table ${model.name} ingestion json mapping 'cao_v1' '`)[1].split("';")[0]);
      assert.deepEqual(mapping, columns.map((name) => ({ column: name, Properties: { Path: `$.${name}` } })));
    }
    assert.ok(kql.indexOf('function cao_ReadySessions()') < kql.indexOf('function cao_SessionFacts()'));
    assert.match(kql, /on Namespace, RunId, ProjectionId/);
    assert.doesNotMatch(kql, /skipvalidation|on Namespace, RunId, RunAttempt|purge|external table|materialized-view/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('emitter rejects opaque source evidence and missing manifest counts', async () => {
  const original = readFileSync(entry, 'utf8');
  for (const [before, after] of [
    ['InputTokens?: int64 | null;', 'InputTokens?: unknown;'],
    ['SessionFactsCount: int64;', 'SessionFactsCount?: int64;']
  ]) {
    const host = {
      ...NodeHost,
      async readFile(path) {
        const file = await NodeHost.readFile(path);
        return path === entry ? { ...file, text: original.replace(before, after) } : file;
      }
    };
    const invalid = await compile(host, entry, { noEmit: true });
    assert.deepEqual(invalid.diagnostics, []);
    await assert.rejects($onEmit({ program: invalid, emitterOutputDir: '.', options: {} }),
      /Unsupported ADX type|Missing ADX manifest count/);
  }
});
