import { state } from './validator-state.js';
import { DASHBOARD_QUERY_LIMITS, COMPUTE_FUNCTION_ARITY, NUMERIC_COMPUTE_FUNCTIONS, QUERY_AGGREGATE_FILTER_PREDICATE_KEYS, QUERY_AGGREGATE_KEYS, QUERY_AGGREGATE_VALUE_KEYS, QUERY_COMPUTE_ARGUMENT_KEYS, QUERY_COMPUTE_KEYS, QUERY_TEMPORAL_SERIES_KEYS, QUERY_TEMPORAL_SERIES_MAP_KEYS, QUERY_TEMPORAL_SERIES_MEASURE_KEYS, QUERY_TEMPORAL_SERIES_TREND_KEYS, QUERY_FILTER_KEYS, QUERY_JOIN_FIELD_KEYS, QUERY_JOIN_KEYS, QUERY_JOIN_ON_KEYS, QUERY_JOIN_TYPE_VALUES, QUERY_KEYS, QUERY_PAGE_COMPARATOR_VALUES, QUERY_PAGE_KEYS, QUERY_PAGE_REVISION_VALUES, QUERY_PARAMETER_KEYS, QUERY_PARAMETER_TYPE_VALUES, QUERY_MAX_JOINS, QUERY_PREDICATE_KEYS, QUERY_PREDICT_KEYS, QUERY_REDUCER_VALUES, QUERY_NUMERIC_REDUCER_VALUES, QUERY_SELECT_KEYS, PREDICTION_METHODS, INFERRED_FIELD_NAMES, DATASET_AVAILABILITY_VALUES, DATASET_COMPLETENESS_VALUES, DATASET_FRESHNESS_VALUES, DATASET_METADATA_KEYS, ERROR_CODES, LINK_FIELD_NAMES, FILTER_DIMENSION_VALUES, ORDER_BY_KEYS, ORDER_DIRECTION_VALUES, SCOPE_KEYS, QUERY_SOURCE_FIELDS, QUERY_SOURCE_VALUES, TEMPORAL_FIELD_NAMES, TIME_KEYS } from './specification.js';
import { executeDashboardQueries, resolveDashboardQuerySources } from './data/queries/declarative.js';
import { SIMULATION_DAYS, simulationDaysSource } from './data/queries/simulation-days.js';
import { compileDashboardViewPayloadQueries } from './data/queries/view-payload-compiler.js';
import { validateQueryWindow } from './query-window-validator.js';
import { dashboardViewSourceNames } from './view-filter-contract.js';
import { validateEnumeratedFilterValue, validateEnumeratedMetadataValue, validateLinkObject, rejectSensitiveStringsInObject, SEMANTIC_FILTER_VALUE_SETS, validateRequiredIdentifier, validateStringField, validateSemanticMetadataLength, validateOptionalStringField, validateNonEmptyStringSequence, isRfc3339Timestamp, validateObjectKeys, createError, isPlainObject, isAggregateFilterLiteral, getValueNodeByKey, getSequenceItemNode } from './validator-common.js';
import { resolveReusablePageViews } from './validator-state.js';
import { createDebug } from './debug.js';
import { validFacetDefinition } from './data/queries/facet.js';
import { DATABASE_QUERY_SOURCE_TABLES } from './specification.js';

/** @typedef {import('./validator.js').ValidationError} ValidationError */

const debugValidatorQueries = createDebug('validator-queries');

/**
 * @param {unknown} entries
 * @param {unknown} entriesNode
 * @param {string} path
 * @param {string[]} allowedKeys
 * @param {string[]} fieldKeys
 * @param {(field: unknown, path: string) => void} requireField
 * @param {ValidationError[]} errors
 */
function validateTemporalSeriesEntries(entries, entriesNode, path, allowedKeys, fieldKeys, requireField, errors) {
  if (entries === undefined) return;
  if (!Array.isArray(entries) || entries.length === 0 || entries.length > 64) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series entries must contain 1 to 64 definitions.', path));
    return;
  }
  for (const [index, entry] of entries.entries()) {
    const entryPath = `${path}[${index}]`;
    if (!isPlainObject(entry)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series entry must be a mapping.', entryPath));
      continue;
    }
    validateObjectKeys(getSequenceItemNode(entriesNode, index), allowedKeys, entryPath, errors);
    for (const key of fieldKeys) {
      if (key !== 'field' && entry[key] === undefined) continue;
      validateStringField(entry[key], `${entryPath}.${key}`, true, errors);
      requireField(entry[key], `${entryPath}.${key}`);
    }
    validateRequiredIdentifier(entry.kind, `${entryPath}.kind`, 'temporal-series kind', errors);
  }
}

/**
 * @param {unknown} parameters
 * @param {unknown} parametersNode
 * @param {string} path
 * @param {ValidationError[]} errors
 * @returns {Map<string, string>}
 */
function validateQueryParameters(parameters, parametersNode, path, errors) {
  const declared = new Map();
  if (parameters === undefined) return declared;
  if (!Array.isArray(parameters) || parameters.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'query parameters must be a non-empty sequence.', path));
    return declared;
  }
  parameters.forEach((parameter, index) => {
    const parameterPath = `${path}[${index}]`;
    if (!isPlainObject(parameter)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'query parameter must be a mapping.', parameterPath));
      return;
    }
    validateObjectKeys(getSequenceItemNode(parametersNode, index), QUERY_PARAMETER_KEYS, parameterPath, errors);
    validateRequiredIdentifier(parameter.name, `${parameterPath}.name`, 'query parameter name', errors);
    if (typeof parameter.type !== 'string' || !QUERY_PARAMETER_TYPE_VALUES.includes(parameter.type)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        `query parameter type must be one of ${QUERY_PARAMETER_TYPE_VALUES.join(', ')}.`,
        `${parameterPath}.type`
      ));
    }
    if (typeof parameter.name === 'string') {
      if (declared.has(parameter.name)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'query parameter names must be unique.', `${parameterPath}.name`));
      } else if (typeof parameter.type === 'string') {
        declared.set(parameter.name, parameter.type);
      }
    }
  });
  return declared;
}

/**
 * @param {Record<string, unknown>} reference
 * @param {string} path
 * @param {Map<string, string>} parameters
 * @param {ValidationError[]} errors
 */
function validateParameterReference(reference, path, parameters, errors) {
  if (Object.keys(reference).length !== 1 || typeof reference.parameter !== 'string') {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'parameter reference must contain exactly one parameter name.', path));
    return;
  }
  if (!parameters.has(reference.parameter)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      `parameter reference "${reference.parameter}" must name a parameter declared by this query.`,
      `${path}.parameter`
    ));
  }
}

/**
 * Validates declarative query definitions and derives each query's static
 * output field schema so view encodings, filters, and order-by references can
 * be checked before execution.
 *
 * @param {unknown} queries
 * @param {unknown} queriesNode
 * @param {ValidationError[]} errors
 * @returns {Map<string, string[] | undefined>}
 */
export function validateQueries(queries, queriesNode, errors) {
  /** @type {Map<string, string[] | undefined>} */
  const declared = new Map();
  if (queries === undefined) return declared;
  if (!Array.isArray(queries) || queries.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'queries must be a non-empty sequence of query definitions.',
      '$.dashboard.queries'
    ));
    return declared;
  }

  for (const [index, query] of queries.entries()) {
    const path = `$.dashboard.queries[${index}]`;
    const queryNode = getSequenceItemNode(queriesNode, index);
    const errorCountBeforeQuery = errors.length;
    if (!isPlainObject(query)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'query must be a mapping.', path));
      debugValidatorQueries({ operation: 'validate-query', index, status: 'invalid' });
      continue;
    }
    validateObjectKeys(queryNode, QUERY_KEYS, path, errors);
    validateRequiredIdentifier(query.name, `${path}.name`, 'query name', errors);
    validateStringField(query.subject, `${path}.subject`, true, errors);
    if (query.objective !== undefined) validateStringField(query.objective, `${path}.objective`, true, errors);
    if (query.acceptance !== undefined) validateStringField(query.acceptance, `${path}.acceptance`, true, errors);
    validateSemanticMetadataLength(query, path, errors);
    validateOptionalStringField(query.description, `${path}.description`, errors);
    const parameters = validateQueryParameters(
      query.parameters,
      getValueNodeByKey(queryNode, 'parameters'),
      `${path}.parameters`,
      errors
    );
    if (query.time !== undefined) {
      validateTime(getValueNodeByKey(queryNode, 'time'), query.time, `${path}.time`, errors);
    }
    const name = typeof query.name === 'string' ? query.name : null;
    if (name && (QUERY_SOURCE_VALUES.includes(name) || declared.has(name))) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'query name must be unique and must not shadow a database table name.',
        `${path}.name`
      ));
    }
    const fields = validateQueryClauses(query, queryNode, path, declared, errors);
    if (name) {
      declared.set(name, fields);
      state.declaredQueryParameters.set(name, parameters);
      const inputs = [
        query.from,
        ...(Array.isArray(query.union) ? query.union : []),
        ...(Array.isArray(query.joins) ? query.joins.map((join) => join?.source) : [])
      ];
      const tables = new Set();
      for (const input of inputs) {
        if (typeof input !== 'string') continue;
        if (QUERY_SOURCE_VALUES.includes(input)) tables.add(input);
        if (DATABASE_QUERY_SOURCE_TABLES[input]) tables.add(DATABASE_QUERY_SOURCE_TABLES[input]);
        for (const table of state.declaredQueryTables.get(input) ?? []) tables.add(table);
      }
      state.declaredQueryTables.set(name, tables);
    }
    debugValidatorQueries({
      operation: 'validate-query',
      index,
      status: errors.length === errorCountBeforeQuery ? 'ok' : 'invalid'
    });
  }
  return declared;
}

/**
 * @param {Record<string, unknown>} query
 * @param {unknown} queryNode
 * @param {string} path
 * @param {Map<string, string[] | undefined>} declared
 * @param {ValidationError[]} errors
 * @returns {string[] | undefined}
 */
function validateQueryClauses(query, queryNode, path, declared, errors) {
  const parameters = new Map(Array.isArray(query.parameters)
    ? query.parameters.flatMap((parameter) => (
        isPlainObject(parameter)
          && typeof parameter.name === 'string'
          && typeof parameter.type === 'string'
          ? [[parameter.name, parameter.type]]
          : []
      ))
    : []);
  /** @param {unknown} source @param {string} sourcePath */
  const inputFields = (source, sourcePath) => {
    validateStringField(source, sourcePath, true, errors);
    if (typeof source !== 'string') return undefined;
    if (!QUERY_SOURCE_VALUES.includes(source) && !declared.has(source)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'query inputs must name one registered data source or one previously declared query.',
        sourcePath
      ));
      return undefined;
    }
    return QUERY_SOURCE_FIELDS[/** @type {keyof typeof QUERY_SOURCE_FIELDS} */ (source)] ?? declared.get(source);
  };

  const fromFields = inputFields(query.from, `${path}.from`);
  /** @type {Array<string[] | undefined>} */
  const unionFields = [];
  if (query.union !== undefined) {
    if (!Array.isArray(query.union) || query.union.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'query union must be a non-empty sequence of source names.',
        `${path}.union`
      ));
    } else {
      query.union.forEach((source, index) => {
        unionFields.push(inputFields(source, `${path}.union[${index}]`));
      });
    }
  }
  /** @type {string[] | undefined} */
  let fields = fromFields
    ? [...new Set([...fromFields, ...unionFields.flatMap((candidate) => candidate ?? [])])]
    : undefined;
  /** @param {unknown} field @param {string} fieldPath */
  const requireField = (field, fieldPath) => {
    if (typeof field === 'string' && fields && !fields.includes(field)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'query field references must name a field produced by the preceding clause.',
        fieldPath
      ));
    }
  };
  /** @param {unknown} alias @param {string} aliasPath */
  const declareField = (alias, aliasPath) => {
    if (typeof alias !== 'string' || !fields) return;
    if (fields.includes(alias)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'query output names must be unique across joined sources and computed fields.',
        aliasPath
      ));
      return;
    }
    fields.push(alias);
  };
  /**
   * Rejects field references that the canonical schema cannot satisfy: fields
   * materialized only after query execution, structured link fields used as
   * scalars, and temporal fields used as numeric measures.
   *
   * @param {unknown} field
   * @param {string} fieldPath
   * @param {'read' | 'scalar' | 'numeric'} use
   */
  const requireSchemaType = (field, fieldPath, use) => {
    if (typeof field !== 'string') return;
    if (INFERRED_FIELD_NAMES.includes(field)) {
      errors.push(createError(
        ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
        `query fields must exist in the canonical schema; "${field}" is derived after query execution.`,
        fieldPath
      ));
      return;
    }
    if (use !== 'read' && LINK_FIELD_NAMES.includes(field)) {
      errors.push(createError(
        ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
        `query operators require a scalar field; "${field}" is a structured link field.`,
        fieldPath
      ));
      return;
    }
    if (use === 'numeric' && TEMPORAL_FIELD_NAMES.includes(field)) {
      errors.push(createError(
        ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
        `numeric query operators require a numeric field; "${field}" is a timestamp field.`,
        fieldPath
      ));
    }
  };

  if (query.joins !== undefined) {
    const joinsNode = getValueNodeByKey(queryNode, 'joins');
    if (!Array.isArray(query.joins) || query.joins.length === 0 || query.joins.length > QUERY_MAX_JOINS) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `joins must be a sequence of 1 to ${QUERY_MAX_JOINS} join definitions.`,
        `${path}.joins`
      ));
    } else {
      for (const [index, join] of query.joins.entries()) {
        const joinPath = `${path}.joins[${index}]`;
        if (!isPlainObject(join)) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'join must be a mapping.', joinPath));
          continue;
        }
        validateObjectKeys(getSequenceItemNode(joinsNode, index), QUERY_JOIN_KEYS, joinPath, errors);
        const joinedFields = inputFields(join.source, `${joinPath}.source`);
        if (join.type !== undefined && (typeof join.type !== 'string' || !QUERY_JOIN_TYPE_VALUES.includes(join.type))) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            `join type must be one of ${QUERY_JOIN_TYPE_VALUES.join(', ')}.`,
            `${joinPath}.type`
          ));
        }
        if (!Array.isArray(join.on) || join.on.length === 0) {
          errors.push(createError(
            ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
            'join on must declare at least one equality key pair.',
            `${joinPath}.on`
          ));
        } else {
          for (const [pairIndex, pair] of join.on.entries()) {
            const pairPath = `${joinPath}.on[${pairIndex}]`;
            if (!isPlainObject(pair)) {
              errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'join key must be a mapping.', pairPath));
              continue;
            }
            validateObjectKeys(
              getSequenceItemNode(getValueNodeByKey(getSequenceItemNode(joinsNode, index), 'on'), pairIndex),
              QUERY_JOIN_ON_KEYS,
              pairPath,
              errors
            );
            validateStringField(pair.left, `${pairPath}.left`, true, errors);
            validateStringField(pair.right, `${pairPath}.right`, true, errors);
            requireField(pair.left, `${pairPath}.left`);
            requireSchemaType(pair.left, `${pairPath}.left`, 'scalar');
            requireSchemaType(pair.right, `${pairPath}.right`, 'scalar');
            if (typeof pair.right === 'string' && joinedFields && !joinedFields.includes(pair.right)) {
              errors.push(createError(
                ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
                'join key must name a field declared by the joined source.',
                `${pairPath}.right`
              ));
            }
          }
        }
        if (!Array.isArray(join.fields) || join.fields.length === 0) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'join fields must declare the aliased fields imported from the joined source.',
            `${joinPath}.fields`
          ));
          continue;
        }
        for (const [fieldIndex, field] of join.fields.entries()) {
          const fieldPath = `${joinPath}.fields[${fieldIndex}]`;
          if (!isPlainObject(field)) {
            errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'join field must be a mapping.', fieldPath));
            continue;
          }
          validateObjectKeys(
            getSequenceItemNode(getValueNodeByKey(getSequenceItemNode(joinsNode, index), 'fields'), fieldIndex),
            QUERY_JOIN_FIELD_KEYS,
            fieldPath,
            errors
          );
          validateStringField(field.field, `${fieldPath}.field`, true, errors);
          validateStringField(field.as, `${fieldPath}.as`, true, errors);
          if (typeof field.field === 'string' && joinedFields && !joinedFields.includes(field.field)) {
            errors.push(createError(
              ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
              'join field must name a field declared by the joined source.',
              `${fieldPath}.field`
            ));
          }
          requireSchemaType(field.field, `${fieldPath}.field`, 'read');
          declareField(field.as, `${fieldPath}.as`);
        }
      }
    }
  }

  if (query.filter !== undefined) {
    const filterPath = `${path}.filter`;
    if (!isPlainObject(query.filter)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'filter must be a mapping.', filterPath));
    } else {
      validateObjectKeys(getValueNodeByKey(queryNode, 'filter'), QUERY_FILTER_KEYS, filterPath, errors);
      if (!Array.isArray(query.filter.predicates) || query.filter.predicates.length === 0) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'filter predicates must be a non-empty sequence.',
          `${filterPath}.predicates`
        ));
      } else {
        for (const [index, predicate] of query.filter.predicates.entries()) {
          const predicatePath = `${filterPath}.predicates[${index}]`;
          if (!isPlainObject(predicate)) {
            errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'predicate must be a mapping.', predicatePath));
            continue;
          }
          validateObjectKeys(
            getSequenceItemNode(getValueNodeByKey(getValueNodeByKey(queryNode, 'filter'), 'predicates'), index),
            QUERY_PREDICATE_KEYS,
            predicatePath,
            errors
          );
          validateStringField(predicate.field, `${predicatePath}.field`, true, errors);
          requireField(predicate.field, `${predicatePath}.field`);
          requireSchemaType(predicate.field, `${predicatePath}.field`, 'scalar');
          const hasEquality = predicate.equals !== undefined;
          const hasSet = predicate.in !== undefined;
          const hasIncludes = predicate.includes !== undefined;
          const hasBounds = predicate.gte !== undefined || predicate.lt !== undefined;
          if (Number(hasEquality) + Number(hasSet) + Number(hasIncludes) + Number(hasBounds) !== 1) {
            errors.push(createError(
              ERROR_CODES.missingOrInvalidRequiredField,
              'predicate must declare exactly one of equals, in, includes, or comparable bounds.',
              predicatePath
            ));
          }
          for (const key of ['equals', 'gte', 'lt']) {
            const operand = predicate[key];
            if (isPlainObject(operand)) {
              validateParameterReference(operand, `${predicatePath}.${key}`, parameters, errors);
            }
          }
        }
      }
    }
  }

  if (query.compute !== undefined) {
    const computeNode = getValueNodeByKey(queryNode, 'compute');
    if (!Array.isArray(query.compute) || query.compute.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'compute must be a non-empty sequence of computed-field definitions.',
        `${path}.compute`
      ));
    } else {
      for (const [index, computed] of query.compute.entries()) {
        const computePath = `${path}.compute[${index}]`;
        if (!isPlainObject(computed)) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'computed field must be a mapping.', computePath));
          continue;
        }
        validateObjectKeys(getSequenceItemNode(computeNode, index), QUERY_COMPUTE_KEYS, computePath, errors);
        validateStringField(computed.as, `${computePath}.as`, true, errors);
        const arity = typeof computed.function === 'string'
          ? COMPUTE_FUNCTION_ARITY[/** @type {keyof typeof COMPUTE_FUNCTION_ARITY} */ (computed.function)]
          : undefined;
        if (!arity) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            `computed field function must be one of ${Object.keys(COMPUTE_FUNCTION_ARITY).join(', ')}.`,
            `${computePath}.function`
          ));
        }
        if (!Array.isArray(computed.args) || (arity && (computed.args.length < arity[0] || computed.args.length > arity[1]))) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            arity
              ? `computed field "${String(computed.function)}" accepts between ${arity[0]} and ${arity[1]} arguments.`
              : 'computed field args must be a sequence.',
            `${computePath}.args`
          ));
        } else {
          for (const [argIndex, argument] of computed.args.entries()) {
            const argumentPath = `${computePath}.args[${argIndex}]`;
            if (!isPlainObject(argument)) {
              errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'computed argument must be a mapping.', argumentPath));
              continue;
            }
            validateObjectKeys(
              getSequenceItemNode(getValueNodeByKey(getSequenceItemNode(computeNode, index), 'args'), argIndex),
              QUERY_COMPUTE_ARGUMENT_KEYS,
              argumentPath,
              errors
            );
            const hasField = argument.field !== undefined;
            const hasValue = argument.value !== undefined;
            const hasContext = argument.context !== undefined;
            const hasParameter = argument.parameter !== undefined;
            if (Number(hasField) + Number(hasValue) + Number(hasContext) + Number(hasParameter) !== 1) {
              errors.push(createError(
                ERROR_CODES.missingOrInvalidRequiredField,
                'computed argument must declare exactly one of field, value, context, or parameter.',
                argumentPath
              ));
              continue;
            }
            if (hasField) {
              validateStringField(argument.field, `${argumentPath}.field`, true, errors);
              requireField(argument.field, `${argumentPath}.field`);
              requireSchemaType(
                argument.field,
                `${argumentPath}.field`,
                typeof computed.function !== 'string' ? 'read'
                  : computed.function === 'timestamp' ? 'scalar'
                    : NUMERIC_COMPUTE_FUNCTIONS.includes(computed.function) ? 'numeric'
                    : ['coalesce', 'dashboard-link', 'link-href'].includes(computed.function) ? 'read' : 'scalar'
              );
            } else if (hasContext && (typeof argument.context !== 'string' || !['time-start', 'time-end'].includes(argument.context))) {
              errors.push(createError(
                ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
                'computed argument context must be time-start or time-end.',
                `${argumentPath}.context`
              ));
            } else if (hasParameter) {
              validateParameterReference(argument, argumentPath, parameters, errors);
            } else if (hasValue && argument.value !== null && !['string', 'number', 'boolean'].includes(typeof argument.value)) {
              errors.push(createError(
                ERROR_CODES.missingOrInvalidRequiredField,
                'computed argument value must be a string, number, boolean, or null literal.',
                `${argumentPath}.value`
              ));
            }

          }
        }
        declareField(computed.as, `${computePath}.as`);
      }
    }
  }

  if (query['temporal-series'] !== undefined) {
    const seriesPath = `${path}.temporal-series`;
    const seriesNode = getValueNodeByKey(queryNode, 'temporal-series');
    const definition = query['temporal-series'];
    if (!isPlainObject(definition)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series must be a mapping.', seriesPath));
    } else {
      validateObjectKeys(seriesNode, QUERY_TEMPORAL_SERIES_KEYS, seriesPath, errors);
      for (const key of ['time', 'series']) {
        validateStringField(definition[key], `${seriesPath}.${key}`, true, errors);
        requireField(definition[key], `${seriesPath}.${key}`);
      }
      const shape = definition.shape;
      if (shape !== undefined
          && (typeof shape !== 'string' || !['tidy', 'groups', 'panels'].includes(shape))) {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'temporal-series shape must be tidy, groups, or panels.', `${seriesPath}.shape`));
      }
      if (definition.link !== undefined) {
        validateStringField(definition.link, `${seriesPath}.link`, true, errors);
        requireField(definition.link, `${seriesPath}.link`);
        if (shape !== 'panels') {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series link requires panels.', `${seriesPath}.link`));
        }
      }
      const carry = definition.carry;
      if (carry !== undefined && (!Array.isArray(carry) || carry.length === 0 || carry.length > 16)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series carry must contain 1 to 16 fields.', `${seriesPath}.carry`));
      } else if (Array.isArray(carry)) {
        carry.forEach((field, index) => {
          validateStringField(field, `${seriesPath}.carry[${index}]`, true, errors);
          requireField(field, `${seriesPath}.carry[${index}]`);
        });
      }
      const pointCarry = definition['point-carry'];
      if (pointCarry !== undefined) {
        if (shape !== 'panels' || !Array.isArray(pointCarry) || pointCarry.length === 0 || pointCarry.length > 16) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series point-carry requires panels and 1 to 16 fields.', `${seriesPath}.point-carry`));
        } else {
          pointCarry.forEach((field, index) => {
            validateStringField(field, `${seriesPath}.point-carry[${index}]`, true, errors);
            requireField(field, `${seriesPath}.point-carry[${index}]`);
          });
        }
      }
      const trend = definition.trend;
      if (trend !== undefined) {
        const trendPath = `${seriesPath}.trend`;
        const trendNode = getValueNodeByKey(seriesNode, 'trend');
        if (shape !== 'groups') {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series trend requires groups.', trendPath));
        }
        if (!isPlainObject(trend)) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series trend must be a mapping.', trendPath));
        } else {
          validateObjectKeys(trendNode, QUERY_TEMPORAL_SERIES_TREND_KEYS, trendPath, errors);
          validateStringField(trend.direction, `${trendPath}.direction`, true, errors);
          requireField(trend.direction, `${trendPath}.direction`);
        }
      }
      const measures = definition.measures;
      const maps = definition.maps;
      if ((!Array.isArray(measures) || measures.length === 0) && (!Array.isArray(maps) || maps.length === 0)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'temporal-series must declare at least one measure or map.', seriesPath));
      }
      validateTemporalSeriesEntries(measures, getValueNodeByKey(seriesNode, 'measures'), `${seriesPath}.measures`, QUERY_TEMPORAL_SERIES_MEASURE_KEYS, ['field', 'key'], requireField, errors);
      validateTemporalSeriesEntries(maps, getValueNodeByKey(seriesNode, 'maps'), `${seriesPath}.maps`, QUERY_TEMPORAL_SERIES_MAP_KEYS, ['field', 'definitions', 'group'], requireField, errors);
      fields = fields
        ? ['groups', 'panels'].includes(String(definition.shape))
          ? [
              ...(Array.isArray(carry) ? carry.filter((field) => typeof field === 'string') : []),
              'metric',
              'metric-key',
              'metric-name',
              'metric-kind',
              'metric-group',
              definition.shape === 'panels' ? 'series' : 'points',
              ...(isPlainObject(trend)
                ? ['trend-start-value', 'trend-end-value', 'trend-delta', 'trend-relative-percent', 'trend-observed-direction', 'trend-assessment', 'trend-observation-count']
                : [])
            ]
          : [...(Array.isArray(carry) ? carry.filter((field) => typeof field === 'string') : []), 'time', 'series', 'metric', 'metric-key', 'metric-name', 'metric-kind', 'metric-group', 'value']
        : undefined;
    }
  }

  if (query.aggregate !== undefined) {
    const aggregatePath = `${path}.aggregate`;
    const aggregateNode = getValueNodeByKey(queryNode, 'aggregate');
    if (!isPlainObject(query.aggregate)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'aggregate must be a mapping.', aggregatePath));
    } else {
      validateObjectKeys(aggregateNode, QUERY_AGGREGATE_KEYS, aggregatePath, errors);
      /** @type {string[]} */
      const grouped = [];
      if (query.aggregate.by !== undefined) {
        if (!Array.isArray(query.aggregate.by)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'aggregate by must be a sequence of grouping fields.',
            `${aggregatePath}.by`
          ));
        } else {
          for (const [index, field] of query.aggregate.by.entries()) {
            validateStringField(field, `${aggregatePath}.by[${index}]`, true, errors);
            requireField(field, `${aggregatePath}.by[${index}]`);
            requireSchemaType(field, `${aggregatePath}.by[${index}]`, 'scalar');
            if (typeof field === 'string') grouped.push(field);
          }
        }
      }
      if (!Array.isArray(query.aggregate.values)
          || query.aggregate.values.length === 0
          || query.aggregate.values.length > DASHBOARD_QUERY_LIMITS['max-aggregate-values']) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          `aggregate values must be a sequence of 1 to ${DASHBOARD_QUERY_LIMITS['max-aggregate-values']} definitions.`,
          `${aggregatePath}.values`
        ));
      } else {
        for (const [index, value] of query.aggregate.values.entries()) {
          const valuePath = `${aggregatePath}.values[${index}]`;
          if (!isPlainObject(value)) {
            errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'aggregate value must be a mapping.', valuePath));
            continue;
          }
          validateObjectKeys(
            getSequenceItemNode(getValueNodeByKey(aggregateNode, 'values'), index),
            QUERY_AGGREGATE_VALUE_KEYS,
            valuePath,
            errors
          );
          validateStringField(value.field, `${valuePath}.field`, true, errors);
          validateStringField(value.as, `${valuePath}.as`, true, errors);
          requireField(value.field, `${valuePath}.field`);
          requireSchemaType(
            value.field,
            `${valuePath}.field`,
            value.reducer === 'unique' ? 'read' : typeof value.reducer === 'string' && QUERY_NUMERIC_REDUCER_VALUES.includes(value.reducer)
              ? 'numeric'
              : 'scalar'
          );
          if (typeof value.reducer !== 'string' || !QUERY_REDUCER_VALUES.includes(value.reducer)) {
            errors.push(createError(
              ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
              `aggregate reducer must be one of ${QUERY_REDUCER_VALUES.join(', ')}.`,
              `${valuePath}.reducer`
            ));
          }
          if (value.filter !== undefined) {
            const filterPath = `${valuePath}.filter`;
            const filterNode = getValueNodeByKey(
              getSequenceItemNode(getValueNodeByKey(aggregateNode, 'values'), index),
              'filter'
            );
            if (!isPlainObject(value.filter)) {
              errors.push(createError(
                ERROR_CODES.missingOrInvalidRequiredField,
                'aggregate filter must be a mapping.',
                filterPath
              ));
            } else {
              validateObjectKeys(filterNode, QUERY_FILTER_KEYS, filterPath, errors);
              const predicates = value.filter.predicates;
              if (!Array.isArray(predicates)
                  || predicates.length === 0
                  || predicates.length > DASHBOARD_QUERY_LIMITS['max-aggregate-filter-predicates']) {
                errors.push(createError(
                  ERROR_CODES.missingOrInvalidRequiredField,
                  `aggregate filter predicates must be a sequence of 1 to ${DASHBOARD_QUERY_LIMITS['max-aggregate-filter-predicates']} definitions.`,
                  `${filterPath}.predicates`
                ));
              } else {
                for (const [predicateIndex, predicate] of predicates.entries()) {
                  const predicatePath = `${filterPath}.predicates[${predicateIndex}]`;
                  if (!isPlainObject(predicate)) {
                    errors.push(createError(
                      ERROR_CODES.missingOrInvalidRequiredField,
                      'aggregate filter predicate must be a mapping.',
                      predicatePath
                    ));
                    continue;
                  }
                  validateObjectKeys(
                    getSequenceItemNode(getValueNodeByKey(filterNode, 'predicates'), predicateIndex),
                    QUERY_AGGREGATE_FILTER_PREDICATE_KEYS,
                    predicatePath,
                    errors
                  );
                  validateStringField(predicate.field, `${predicatePath}.field`, true, errors);
                  requireField(predicate.field, `${predicatePath}.field`);
                  requireSchemaType(predicate.field, `${predicatePath}.field`, 'scalar');
                  const hasEquals = Object.hasOwn(predicate, 'equals');
                  const hasIn = Object.hasOwn(predicate, 'in');
                  if (Number(hasEquals) + Number(hasIn) !== 1) {
                    errors.push(createError(
                      ERROR_CODES.missingOrInvalidRequiredField,
                      'aggregate filter predicate must declare exactly one of equals or in.',
                      predicatePath
                    ));
                  } else if (hasEquals && !isAggregateFilterLiteral(predicate.equals)) {
                    errors.push(createError(
                      ERROR_CODES.missingOrInvalidRequiredField,
                      'aggregate filter equals must be a string, number, or boolean literal.',
                      `${predicatePath}.equals`
                    ));
                  } else if (hasIn && (
                    !Array.isArray(predicate.in)
                    || predicate.in.length === 0
                    || predicate.in.length > DASHBOARD_QUERY_LIMITS['max-predicate-alternatives']
                    || predicate.in.some((candidate) => !isAggregateFilterLiteral(candidate))
                  )) {
                    errors.push(createError(
                      ERROR_CODES.missingOrInvalidRequiredField,
                      `aggregate filter in must contain 1 to ${DASHBOARD_QUERY_LIMITS['max-predicate-alternatives']} string, number, or boolean literals.`,
                      `${predicatePath}.in`
                    ));
                  }
                }
              }
            }
          }
          if (typeof value.as === 'string' && grouped.includes(value.as)) {
            errors.push(createError(
              ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
              'query output names must be unique across joined sources and computed fields.',
              `${valuePath}.as`
            ));
          }

          if (typeof value.as === 'string') grouped.push(value.as);
        }
      }
      fields = fields ? grouped : undefined;
    }
  }

  if (query.predict !== undefined) {
    const predictNode = getValueNodeByKey(queryNode, 'predict');
    if (!Array.isArray(query.predict) || query.predict.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'predict must be a non-empty sequence of prediction definitions.',
        `${path}.predict`
      ));
    } else {
      for (const [index, prediction] of query.predict.entries()) {
        const predictionPath = `${path}.predict[${index}]`;
        if (!isPlainObject(prediction)) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'prediction must be a mapping.', predictionPath));
          continue;
        }
        validateObjectKeys(getSequenceItemNode(predictNode, index), QUERY_PREDICT_KEYS, predictionPath, errors);
        validateStringField(prediction.field, `${predictionPath}.field`, true, errors);
        validateStringField(prediction.as, `${predictionPath}.as`, true, errors);
        requireField(prediction.field, `${predictionPath}.field`);
        requireSchemaType(prediction.field, `${predictionPath}.field`, 'numeric');

        const predictors = typeof prediction.on === 'string'
          ? [prediction.on]
          : Array.isArray(prediction.on) ? prediction.on : [];
        if (predictors.length === 0 || predictors.length > 8) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'prediction on must be one field or a sequence of 1 to 8 predictor fields.',
            `${predictionPath}.on`
          ));
        }
        for (const [predictorIndex, predictor] of predictors.entries()) {
          const predictorPath = Array.isArray(prediction.on)
            ? `${predictionPath}.on[${predictorIndex}]`
            : `${predictionPath}.on`;
          validateStringField(predictor, predictorPath, true, errors);
          requireField(predictor, predictorPath);
          requireSchemaType(predictor, predictorPath, 'numeric');
        }
        if (new Set(predictors).size !== predictors.length) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'prediction on must not contain duplicate predictor fields.',
            `${predictionPath}.on`
          ));
        }

        const method = prediction.method ?? 'linear';
        if (typeof method !== 'string' || !PREDICTION_METHODS.includes(method)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            `prediction method must be one of ${PREDICTION_METHODS.join(', ')}.`,
            `${predictionPath}.method`
          ));
        } else if (method !== 'linear' && predictors.length !== 1) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `prediction method ${method} requires exactly one predictor field.`,
            `${predictionPath}.on`
          ));
        }
        if (prediction.order !== undefined) {
          if (method !== 'poly' || !Number.isSafeInteger(prediction.order)
              || Number(prediction.order) < 1 || Number(prediction.order) > 10) {
            errors.push(createError(
              ERROR_CODES.missingOrInvalidRequiredField,
              'prediction order is allowed only for poly and must be an integer from 1 to 10.',
              `${predictionPath}.order`
            ));
          }
        }

        if (prediction.groupby !== undefined) {
          if (!Array.isArray(prediction.groupby) || prediction.groupby.length === 0) {
            errors.push(createError(
              ERROR_CODES.missingOrInvalidRequiredField,
              'prediction groupby must be a non-empty sequence of grouping fields.',
              `${predictionPath}.groupby`
            ));
          } else {
            for (const [groupIndex, groupField] of prediction.groupby.entries()) {
              const groupPath = `${predictionPath}.groupby[${groupIndex}]`;
              validateStringField(groupField, groupPath, true, errors);
              requireField(groupField, groupPath);
              requireSchemaType(groupField, groupPath, 'scalar');
            }
          }
        }
        declareField(prediction.as, `${predictionPath}.as`);
      }
    }
  }

  if (query.window !== undefined) {
    validateQueryWindow(query.window, getValueNodeByKey(queryNode, 'window'), path, errors, {
      isPlainObject, getValueNodeByKey, getSequenceItemNode, validateObjectKeys,
      validateStringField, createError, requireField, requireSchemaType, declareField
    });
  }

  if (query.select !== undefined) {
    const selectNode = getValueNodeByKey(queryNode, 'select');
    if (!Array.isArray(query.select) || query.select.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'select must be a non-empty sequence of projected fields.',
        `${path}.select`
      ));
    } else {
      /** @type {string[]} */
      const projected = [];
      for (const [index, field] of query.select.entries()) {
        const selectPath = `${path}.select[${index}]`;
        if (!isPlainObject(field)) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'select entry must be a mapping.', selectPath));
          continue;
        }
        validateObjectKeys(getSequenceItemNode(selectNode, index), QUERY_SELECT_KEYS, selectPath, errors);
        validateStringField(field.field, `${selectPath}.field`, true, errors);
        validateOptionalStringField(field.as, `${selectPath}.as`, errors);
        requireField(field.field, `${selectPath}.field`);
        requireSchemaType(field.field, `${selectPath}.field`, 'read');
        const alias = typeof field.as === 'string' ? field.as : field.field;
        if (typeof alias === 'string') {
          if (projected.includes(alias)) {
            errors.push(createError(
              ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
              'select output names must be unique.',
              `${selectPath}.as`
            ));
          } else {
            projected.push(alias);
          }
        }
      }
      fields = fields ? projected : undefined;
    }
  }

  if (query['order-by'] !== undefined) {
    if (!Array.isArray(query['order-by']) || query['order-by'].length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'order-by must be a non-empty sequence of ordering clauses.',
        `${path}.order-by`
      ));
    } else {
      for (const [index, clause] of query['order-by'].entries()) {
        const clausePath = `${path}.order-by[${index}]`;
        if (!isPlainObject(clause)) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'order-by clause must be a mapping.', clausePath));
          continue;
        }
        validateObjectKeys(
          getSequenceItemNode(getValueNodeByKey(queryNode, 'order-by'), index),
          ORDER_BY_KEYS,
          clausePath,
          errors
        );
        validateStringField(clause.field, `${clausePath}.field`, true, errors);
        requireField(clause.field, `${clausePath}.field`);
        requireSchemaType(clause.field, `${clausePath}.field`, 'scalar');
        if (clause.direction !== undefined
            && (typeof clause.direction !== 'string' || !ORDER_DIRECTION_VALUES.includes(clause.direction))) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            `order-by direction must be one of ${ORDER_DIRECTION_VALUES.join(', ')}.`,
            `${clausePath}.direction`
          ));
        }
      }
    }
  }

  if (query.page !== undefined) {
    const pagePath = `${path}.page`;
    const pageNode = getValueNodeByKey(queryNode, 'page');
    if (!isPlainObject(query.page)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'page must be a mapping.',
        pagePath
      ));
    } else {
      validateObjectKeys(pageNode, QUERY_PAGE_KEYS, pagePath, errors);
      const keys = query.page.keys;
      if (!Array.isArray(keys) || keys.length === 0 || keys.length > 4
          || keys.some((field) => typeof field !== 'string' || field.trim().length === 0)
          || new Set(keys).size !== keys.length) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'page.keys must contain one to four unique non-empty field names.',
          `${pagePath}.keys`
        ));
      } else {
        for (const [index, field] of keys.entries()) {
          requireField(field, `${pagePath}.keys[${index}]`);
          requireSchemaType(field, `${pagePath}.keys[${index}]`, 'scalar');
        }
      }
      if (typeof query.page.revision !== 'string'
          || !QUERY_PAGE_REVISION_VALUES.includes(query.page.revision)) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          `page.revision must be one of ${QUERY_PAGE_REVISION_VALUES.join(', ')}.`,
          `${pagePath}.revision`
        ));
      }
      if (typeof query.page.comparator !== 'string'
          || !QUERY_PAGE_COMPARATOR_VALUES.includes(query.page.comparator)) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          `page.comparator must be one of ${QUERY_PAGE_COMPARATOR_VALUES.join(', ')}.`,
          `${pagePath}.comparator`
        ));
      }
      const order = Array.isArray(query['order-by']) ? query['order-by'] : [];
      if (!Array.isArray(keys) || order.length !== keys.length
          || order.some((clause, index) => !isPlainObject(clause)
            || clause.field !== keys[index]
            || (clause.direction ?? 'asc') !== 'asc')) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'page.keys must exactly match an ascending order-by sequence.',
          pagePath
        ));
      }
      if (query.limit !== undefined || query.facet !== undefined) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'a paged query must not declare limit or facet.',
          pagePath
        ));
      }
    }
  }

  if (query.limit !== undefined
      && (!Number.isSafeInteger(query.limit) || Number(query.limit) <= 0
        || Number(query.limit) > DASHBOARD_QUERY_LIMITS['max-output-rows'])) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      `limit must be a positive integer no greater than ${DASHBOARD_QUERY_LIMITS['max-output-rows']}.`,
      `${path}.limit`
    ));
  }
  if (query.facet !== undefined && !validFacetDefinition(query.facet)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'facet requires a field or row/column fields and an output as.', `${path}.facet`));
  } else if (validFacetDefinition(query.facet)) {
    for (const field of [query.facet.field, query.facet.row, query.facet.column]) {
      if (field) requireField(field, `${path}.facet`);
    }
    return ['facet-field', 'facet-row', 'facet-column', 'facet-row-index', 'facet-column-index', query.facet.as];
  }

  return fields;
}

/**
 * @param {unknown} source
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateSource(source, path, errors) {
  validateStringField(source, path, true, errors);
  if (typeof source === 'string' && !QUERY_SOURCE_VALUES.includes(source) && !state.declaredQueries.has(source)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'source must name one registered data source or one declared query.',
      path
    ));
  }
}

/**
 * Compile and execute every authored view-query graph against available empty
 * canonical sources. This catches query graphs that are structurally valid in
 * isolation but become unavailable after page-scoped aliases are introduced.
 *
 * @param {Record<string, unknown>} dashboard
 * @param {ValidationError[]} errors
 */
export function validateViewQueryMaterialization(dashboard, errors) {
    if (!Array.isArray(dashboard.pages) || !Array.isArray(dashboard.queries)) return;
    const queries = dashboard.queries;
    /** @type {import('./presenter.js').SourceMetadata} */
    const metadata = {
      'source-id': 'validator',
      'source-kind': 'fixture',
      'as-of': '1970-01-01T00:00:00.000Z',
      'retrieved-at': '1970-01-01T00:00:00.000Z',
      completeness: 'complete',
      freshness: 'fresh',
      availability: 'empty'
    };
    /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */
    const querySources = Object.fromEntries(QUERY_SOURCE_VALUES.map((name) => [
      name,
      { source: name, rows: [], metadata }
    ]));
    querySources[SIMULATION_DAYS] = simulationDaysSource();

    dashboard.pages.forEach((page, pageIndex) => {
      const resolvedPage = resolveReusablePageViews(page);
      if (!isPlainObject(resolvedPage) || typeof resolvedPage.id !== 'string') return;
      const views = resolvedPage.kind === 'built-in' && isPlainObject(resolvedPage.definition)
        ? resolvedPage.definition.views
        : resolvedPage.views;
      if (!Array.isArray(views)) return;
      const requested = [...new Set(views.flatMap((view) => {
        if (!isPlainObject(view) || !isPlainObject(view.data)) return [];
        return dashboardViewSourceNames(view);
      }))].filter((name) => state.declaredQueries.has(name));
      if (requested.length === 0) return;

      try {
        const required = new Set(resolveDashboardQuerySources(queries, requested));
        const scopedQueries = queries.filter((query) => (
          isPlainObject(query) && typeof query.name === 'string' && required.has(query.name)
        ));
        const declared = executeDashboardQueries(scopedQueries, querySources, requested);
        const payload = compileDashboardViewPayloadQueries(resolvedPage, resolvedPage.id, {
          queries: scopedQueries,
          sourceNames: requested
        });
        const materialized = executeDashboardQueries(
          payload.queries,
          { ...querySources, ...declared },
          payload.aliases
        );
        const errorCountBeforeMaterialization = errors.length;
        for (const alias of payload.aliases) {
          if (materialized[alias]?.metadata?.availability !== 'unavailable') continue;
          errors.push(createError(
            ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
            `view query source "${alias}" materializes as unavailable.`,
            `$.dashboard.pages[${pageIndex}].views`
          ));
        }
        debugValidatorQueries({
          operation: 'materialize-view-queries',
          pageIndex,
          aliasCount: payload.aliases.length,
          status: errors.length === errorCountBeforeMaterialization ? 'ok' : 'invalid'
        });
      } catch (error) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          `page-scoped view queries must materialize successfully: ${error instanceof Error ? error.message : String(error)}`,
          `$.dashboard.pages[${pageIndex}]`
        ));
        debugValidatorQueries({ operation: 'materialize-view-queries', pageIndex, status: 'exception' });
      }
    });
}

/**
 * Resolves the declared field schema of a database table or of a derived
 * query. Returns `undefined` when the schema cannot be derived statically, in
 * which case field references are not checked.
 * @param {string} sourceName
 * @returns {string[] | undefined}
 */
export function sourceFieldNames(sourceName) {
  return QUERY_SOURCE_FIELDS[/** @type {keyof typeof QUERY_SOURCE_FIELDS} */ (sourceName)]
    ?? state.declaredQueries.get(sourceName)
    ?? undefined;
}

/**
 * @param {unknown} sources
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateSourceSequence(sources, path, errors) {
  if (!Array.isArray(sources) || sources.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'sources must be a non-empty sequence of database table or query names.',
      path
    ));
    return;
  }
  const seen = new Set();
  for (const [index, source] of sources.entries()) {
    validateSource(source, `${path}[${index}]`, errors);
    if (typeof source === 'string') {
      if (seen.has(source)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'sources must not contain duplicate source names.',
          `${path}[${index}]`
        ));
      }
      seen.add(source);
    }
  }
}

/**
 * @param {unknown} data
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateSemanticFieldLiterals(data, path, errors) {
  if (!isPlainObject(data)) {
    return;
  }

  validateFilterLiteralSet(data.filters, `${path}.filters`, errors);
}

/**
 * @param {unknown} contextNode
 * @param {Record<string, unknown>} context
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateContext(contextNode, context, path, errors) {
  validateScope(getValueNodeByKey(contextNode, 'scope'), context.scope, `${path}.scope`, errors);
  validateTime(getValueNodeByKey(contextNode, 'time'), context.time, `${path}.time`, errors);
  validateFilters(getValueNodeByKey(contextNode, 'filters'), context.filters, `${path}.filters`, errors);
  validateLimit(context.limit, `${path}.limit`, errors);
  validateOrderBy(getValueNodeByKey(contextNode, 'order-by'), context['order-by'], `${path}.order-by`, errors);
}

/**
 * @param {unknown} scopeNode
 * @param {unknown} scope
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateScope(scopeNode, scope, path, errors) {
  if (scope === undefined) {
    return;
  }

  if (!isPlainObject(scope)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'scope must be a mapping.',
      path
    ));
    return;
  }

  validateObjectKeys(scopeNode, SCOPE_KEYS, path, errors);
  for (const key of SCOPE_KEYS) {
    const value = scope[key];
    if (value !== undefined) {
      validateNonEmptyStringSequence(value, `${path}.${key}`, `${key} must be a non-empty sequence of non-empty strings.`, errors);
    }
  }
}

/**
 * @param {unknown} timeNode
 * @param {unknown} time
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateTime(timeNode, time, path, errors) {
  if (time === undefined) {
    return;
  }

  if (!isPlainObject(time)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'time must be a mapping.',
      path
    ));
    return;
  }

  validateObjectKeys(timeNode, TIME_KEYS, path, errors);

  const range = time.range;
  const start = time.start;
  const end = time.end;

  if (range !== undefined) {
    if (typeof range !== 'string' || (range !== 'all' && !/^[1-9][0-9]*(h|d|w)$/.test(range))) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'time.range must be all or match ^[1-9][0-9]*(h|d|w)$.',
        `${path}.range`
      ));
    }

    if (start !== undefined || end !== undefined) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'time.range must not appear with time.start or time.end.',
        path
      ));
    }
    return;
  }

  if (start !== undefined && !isRfc3339Timestamp(start)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'time.start must be an RFC 3339 timestamp.',
      `${path}.start`
    ));
  }

  if (end !== undefined && !isRfc3339Timestamp(end)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'time.end must be an RFC 3339 timestamp.',
      `${path}.end`
    ));
  }

  if (typeof start === 'string' && typeof end === 'string' && isRfc3339Timestamp(start) && isRfc3339Timestamp(end)) {
    if (Date.parse(start) >= Date.parse(end)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'time.start must precede time.end.',
        path
      ));
    }
  }
}

/**
 * @param {unknown} filtersNode
 * @param {unknown} filters
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateFilters(filtersNode, filters, path, errors) {
  if (filters === undefined) {
    return;
  }

  if (!isPlainObject(filters)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'filters must be a mapping.',
      path
    ));
    return;
  }

  validateObjectKeys(filtersNode, FILTER_DIMENSION_VALUES, path, errors);
  for (const [key, value] of Object.entries(filters)) {
    validateFilterValue(value, `${path}.${key}`, errors);
  }
}

/**
 * @param {unknown} value
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateFilterValue(value, path, errors) {
  if (Array.isArray(value)) {
    if (value.length === 0) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'filter sequences must be non-empty.',
        path
      ));
      return;
    }

    for (const [index, item] of value.entries()) {
      if (typeof item !== 'string' || item.length === 0) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'filter values must be non-empty strings or non-empty sequences of non-empty strings.',
          `${path}[${index}]`
        ));
      }
    }
    return;
  }

  if (typeof value !== 'string' || value.length === 0) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'filter values must be non-empty strings or non-empty sequences of non-empty strings.',
      path
    ));
  }
}

/**
 * @param {unknown} limit
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateLimit(limit, path, errors) {
  if (limit === undefined) {
    return;
  }

  if (typeof limit !== 'number' || !Number.isInteger(limit) || limit <= 0) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'limit must be a positive integer.',
      path
    ));
  }
}

/**
 * @param {unknown} orderByNode
 * @param {unknown} orderBy
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateOrderBy(orderByNode, orderBy, path, errors) {
  if (orderBy === undefined) {
    return;
  }

  if (!Array.isArray(orderBy) || orderBy.length === 0) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'order-by must be a non-empty sequence.',
      path
    ));
    return;
  }

  for (const [index, clause] of orderBy.entries()) {
    const clausePath = `${path}[${index}]`;
    const clauseNode = getSequenceItemNode(orderByNode, index);
    if (!isPlainObject(clause)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'order-by entries must be mappings.',
        clausePath
      ));
      continue;
    }

    validateObjectKeys(clauseNode, ORDER_BY_KEYS, clausePath, errors);
    validateStringField(clause.field, `${clausePath}.field`, true, errors);
    validateStringField(clause.direction, `${clausePath}.direction`, true, errors);
    if (typeof clause.direction === 'string' && !ORDER_DIRECTION_VALUES.includes(clause.direction)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'order-by.direction must be exactly "asc" or "desc".',
        `${clausePath}.direction`
      ));
    }
  }
}

/**
 * @param {unknown} filters
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateFilterLiteralSet(filters, path, errors) {
  if (!isPlainObject(filters)) {
    return;
  }

  for (const [field, allowedValues] of Object.entries(SEMANTIC_FILTER_VALUE_SETS)) {
    const value = filters[field];
    if (value !== undefined) {
      validateEnumeratedFilterValue(value, allowedValues, `${path}.${field}`, errors);
    }
  }
}

/**
 * @param {unknown} dataNode
 * @param {unknown} data
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateDatasetMetadata(dataNode, data, path, errors) {
  if (!isPlainObject(data)) {
    return;
  }

  const metadata = data['source-metadata'];
  if (metadata === undefined) {
    return;
  }

  const metadataPath = `${path}.source-metadata`;
  if (!isPlainObject(metadata)) {
    errors.push(createError(
      ERROR_CODES.missingRequiredProvenanceOrDataStateMetadata,
      'source-metadata must be a mapping when provided.',
      metadataPath
    ));
    return;
  }

  rejectSensitiveStringsInObject(metadata, metadataPath, errors);

  const metadataNode = getValueNodeByKey(dataNode, 'source-metadata');
  validateObjectKeys(metadataNode, DATASET_METADATA_KEYS, metadataPath, errors);

  for (const key of ['source-id', 'source-kind', 'as-of', 'retrieved-at', 'completeness', 'freshness']) {
    validateStringField(metadata[key], `${metadataPath}.${key}`, true, errors);
  }

  for (const key of ['coverage-start', 'coverage-end']) {
    if (metadata[key] !== undefined && !isRfc3339Timestamp(metadata[key])) {
      errors.push(createError(
        ERROR_CODES.missingRequiredProvenanceOrDataStateMetadata,
        `${key} must be an RFC 3339 timestamp when provided.`,
        `${metadataPath}.${key}`
      ));
    }
  }

  for (const key of ['as-of', 'retrieved-at']) {
    if (metadata[key] !== undefined && !isRfc3339Timestamp(metadata[key])) {
      errors.push(createError(
        ERROR_CODES.missingRequiredProvenanceOrDataStateMetadata,
        `${key} must be an RFC 3339 timestamp.`,
        `${metadataPath}.${key}`
      ));
    }
  }

  if (
    typeof metadata['coverage-start'] === 'string' &&
    typeof metadata['coverage-end'] === 'string' &&
    isRfc3339Timestamp(metadata['coverage-start']) &&
    isRfc3339Timestamp(metadata['coverage-end']) &&
    Date.parse(metadata['coverage-start']) >= Date.parse(metadata['coverage-end'])
  ) {
    errors.push(createError(
      ERROR_CODES.missingRequiredProvenanceOrDataStateMetadata,
      'coverage-start must precede coverage-end.',
      metadataPath
    ));
  }

  validateEnumeratedMetadataValue(
    metadata.completeness,
    DATASET_COMPLETENESS_VALUES,
    `${metadataPath}.completeness`,
    'completeness',
    errors
  );
  validateEnumeratedMetadataValue(
    metadata.freshness,
    DATASET_FRESHNESS_VALUES,
    `${metadataPath}.freshness`,
    'freshness',
    errors
  );

  if (metadata['provenance-link'] !== undefined) {
    validateLinkObject(metadata['provenance-link'], `${metadataPath}.provenance-link`, 'provenance-link', errors, {
      code: ERROR_CODES.missingRequiredProvenanceOrDataStateMetadata
    });
  }

  if (metadata.availability !== undefined) {
    validateEnumeratedMetadataValue(
      metadata.availability,
      DATASET_AVAILABILITY_VALUES,
      `${metadataPath}.availability`,
      'availability',
      errors
    );
  }
}
