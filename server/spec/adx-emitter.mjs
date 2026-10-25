import { createTypeSpecLibrary, emitFile, getNamespaceFullName, navigateProgram } from '@typespec/compiler';

export const $lib = createTypeSpecLibrary({ name: 'cao-adx', diagnostics: {} });
export const namespace = 'Cao.ADX';
const sourceKey = Symbol.for('cao-adx.source');

export function $source(context, target, collection) {
  context.program.stateMap(sourceKey).set(target, collection);
}

const kinds = new Map([
  ['string', 'string'], ['int64', 'long'], ['float64', 'real'],
  ['boolean', 'bool'], ['utcDateTime', 'datetime']
]);

function kind(type) {
  if (type.kind === 'Union') {
    const values = [...type.variants.values()].map((variant) => variant.type)
      .filter((type) => !(type.kind === 'Intrinsic' && type.name === 'null'));
    if (values.length === 1) return kind(values[0]);
  }
  if (type.kind === 'Scalar' && kinds.has(type.name)) return kinds.get(type.name);
  if (type.kind === 'String') return 'string';
  if (type.kind === 'Number' && Number.isSafeInteger(type.value)) return 'long';
  if (type.kind === 'Model' && type.name === 'Array' && type.indexer?.value.name === 'string') return 'dynamic';
  throw new Error(`Unsupported ADX type: ${type.kind} ${type.name ?? ''}`);
}

function properties(model) {
  return [...(model.baseModel ? properties(model.baseModel) : []), ...model.properties.values()];
}

export async function $onEmit(context) {
  const models = new Map();
  navigateProgram(context.program, {
    model(model) {
      if (getNamespaceFullName(model.namespace) === namespace) models.set(model.name, model);
    }
  });
  const families = [...models.values()].filter((model) => context.program.stateMap(sourceKey).has(model));
  const manifest = models.get('SessionVersions');
  const version = models.get('Identity')?.properties.get('ContractVersion')?.type;
  if (!families.length || !manifest || version?.kind !== 'Number' || !Number.isSafeInteger(version.value) || version.value < 1) {
    throw new Error('ADX requires fact tables, a ready manifest, and a positive literal contract version');
  }
  const go = [
    '// Code generated from server/spec/adx.tsp. DO NOT EDIT.', '', 'package adxingest', '',
    `const contractVersion = ${version.value}`, '', 'var families = []family{'
  ];
  for (const model of families) {
    const collection = context.program.stateMap(sourceKey).get(model);
    if (!/^\$[a-zA-Z][a-zA-Z0-9]*$/.test(collection) || model.baseModel?.name !== 'Observation') {
      throw new Error(`Invalid ADX source table ${model.name}`);
    }
    go.push(`\t{${JSON.stringify(model.name)}, ${JSON.stringify(collection)}, []field{`);
    for (const property of model.properties.values()) {
      const fieldKind = kind(property.type);
      if (fieldKind === 'dynamic' || !property.optional || !/^[A-Z][a-zA-Z0-9]*$/.test(property.name)) {
        throw new Error(`ADX source fields must be optional named scalars: ${model.name}.${property.name}`);
      }
      const source = property.name[0].toLowerCase() + property.name.slice(1);
      go.push(`\t\t{${JSON.stringify(source)}, ${JSON.stringify(fieldKind)}},`);
    }
    go.push('\t}},');
    const count = manifest.properties.get(`${model.name}Count`);
    if (!count || kind(count.type) !== 'long' || count.optional) throw new Error(`Missing ADX manifest count for ${model.name}`);
  }
  go.push('}', '');

  const kql = ['// Generated from server/spec/adx.tsp. Do not edit.'];
  for (const model of [...families, manifest]) {
    if (!/^[A-Z][a-zA-Z0-9]*$/.test(model.name)) throw new Error('Invalid ADX table name');
    const columns = properties(model).map((property) => ({ name: property.name, kind: kind(property.type) }));
    if (new Set(columns.map((column) => column.name)).size !== columns.length) throw new Error(`Duplicate ADX columns: ${model.name}`);
    const mapping = columns.map(({ name }) => ({ column: name, Properties: { Path: `$.${name}` } }));
    kql.push(
      `.create-merge table ${model.name} (${columns.map(({ name, kind }) => `${name}:${kind}`).join(', ')});`,
      `.create-or-alter table ${model.name} ingestion json mapping 'cao_v1' '${JSON.stringify(mapping)}';`,
      `.alter table ${model.name} policy streamingingestion enable;`,
      `.alter-merge table ${model.name} policy retention softdelete = 7d recoverability = enabled;`
    );
  }
  kql.push(`.create-or-alter function cao_ReadySessions() {
    SessionVersions
    | summarize arg_max(SourceRevision, *) by Namespace, RunId
};`);
  for (const model of families) {
    kql.push(`.create-or-alter function cao_${model.name}() {
    ${model.name}
    | summarize take_any(*) by Namespace, RunId, RunAttempt, ProjectionId, RowDigest
    | join kind=inner (cao_ReadySessions() | project Namespace, RunId, ProjectionId) on Namespace, RunId, ProjectionId
};`);
  }
  await emitFile(context.program, { path: `${context.emitterOutputDir}/schema.gen.go`, content: go.join('\n') });
  await emitFile(context.program, { path: `${context.emitterOutputDir}/schema.gen.kql`, content: kql.join('\n') + '\n' });
}
