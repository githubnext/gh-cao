import { ADDITIVE_MEASURE_FIELDS, AGGREGATE_VALUES, DASHBOARD_LINK_FIELD_NAMES, ERROR_CODES, LINK_FIELD_NAMES, FIELD_DEFINITION_KEYS, FIELD_DISPLAY_VALUES, FIELD_FORMAT_VALUES, FIELD_TYPE_VALUES, NON_ADDITIVE_MEASURE_FIELDS, SOURCE_ENTITY_IDENTIFIER_FIELDS, TEMPORAL_FIELD_NAMES, TIME_UNIT_VALUES, VIEW_ENCODING_KEYS } from './specification.js';
import { sourceFieldNames } from './validator-queries.js';
import { validateStringField, validateOptionalStringField, validateObjectKeys, createError, isPlainObject, getValueNodeByKey, getSequenceItemNode } from './validator-common.js';
import { createDebug } from './debug.js';
import { chartFacet } from './chart-facet.js';
import { state } from './validator-state.js';

/** @typedef {import('./validator.js').ValidationError} ValidationError */

const debugValidatorEncoding = createDebug('validator-encoding');


/**
 * @param {unknown} encodingNode
 * @param {unknown} encoding
 * @param {unknown} mark
 * @param {unknown} chart
 * @param {string | null} sourceName
 * @param {unknown} data
 * @param {string} viewPath
 * @param {ValidationError[]} errors
 * @param {unknown} [facet]
 * @param {boolean} [layered]
 */
export function validateEncoding(encodingNode, encoding, mark, chart, sourceName, data, viewPath, errors, facet, layered = false) {
  const errorCountBeforeValidation = errors.length;
  const markValue = typeof mark === 'string' ? mark : null;

  if (mark === 'element' || mark === 'callout') {
    if (encoding !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `${mark} views must not declare encoding.`,
        `${viewPath}.encoding`
      ));
    }
    debugValidatorEncoding({ operation: 'validate-encoding', mark: markValue, status: errors.length === errorCountBeforeValidation ? 'ok' : 'invalid' });
    return;
  }

  if (!isPlainObject(encoding)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'encoding must be a mapping.',
      `${viewPath}.encoding`
    ));
    debugValidatorEncoding({ operation: 'validate-encoding', mark: markValue, status: 'invalid' });
    return;
  }

  validateObjectKeys(encodingNode, VIEW_ENCODING_KEYS, `${viewPath}.encoding`, errors);
  if (encoding.card !== undefined) {
    validateStringField(encoding.card, `${viewPath}.encoding.card`, true, errors);
    if (mark !== 'table' || typeof encoding.card !== 'string' || !state.declaredCardTemplates.has(encoding.card)) {
      errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'encoding.card requires a table and a declared dashboard card template.', `${viewPath}.encoding.card`));
    }
  }

  /** @type {Map<string, string>} */
  const aggregateOutputIds = new Map();
  if (markValue !== 'chart' && encoding.weight !== undefined) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'weight encoding is supported only by chart views.',
      `${viewPath}.encoding.weight`
    ));
  }
  const displayForbiddenChannels = ['list', 'table'].includes(markValue ?? '')
    ? ['href']
    : ['value', 'x', 'y', 'color', 'section', 'weight', 'reference', 'href'];
  for (const channel of displayForbiddenChannels) {
    if (isPlainObject(encoding[channel]) && encoding[channel].display !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'display is allowed only on table column field definitions.',
        `${viewPath}.encoding.${channel}.display`
      ));
    }
  }
  const filterForbiddenChannels = ['list', 'table'].includes(markValue ?? '')
    ? ['href']
    : ['value', 'x', 'y', 'color', 'section', 'weight', 'reference', 'href'];
  for (const channel of filterForbiddenChannels) {
    if (isPlainObject(encoding[channel]) && encoding[channel].filter !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'filter is allowed only on table column field definitions.',
        `${viewPath}.encoding.${channel}.filter`
      ));
    }
  }

  if (markValue === 'metric') {
    validateMetricEncoding(encodingNode, encoding, sourceName, `${viewPath}.encoding`, aggregateOutputIds, errors);
  } else if (markValue === 'table') {
    validateTableEncoding(encodingNode, encoding, sourceName, `${viewPath}.encoding`, aggregateOutputIds, errors, 'table');
  } else if (markValue === 'list') {
    validateTableEncoding(encodingNode, encoding, sourceName, `${viewPath}.encoding`, aggregateOutputIds, errors, 'list');
  } else if (markValue === 'chart') {
    validateChartEncoding(encodingNode, encoding, chart, sourceName, `${viewPath}.encoding`, aggregateOutputIds, errors, layered);
    validateChartWidget(encoding, chart, viewPath, errors);
  }

  validateOrderByReferences(data, encoding, aggregateOutputIds, sourceName, viewPath, errors, facet);

  debugValidatorEncoding({ operation: 'validate-encoding', mark: markValue, status: errors.length === errorCountBeforeValidation ? 'ok' : 'invalid' });
}

/**
 * @param {Record<string, unknown>} encoding
 * @param {unknown} chart
 * @param {string} viewPath
 * @param {ValidationError[]} errors
 */
function validateChartWidget(encoding, chart, viewPath, errors) {
  if (chart === undefined) {
    return;
  }
  if (Array.isArray(encoding.y)) {
    if (chart !== 'line') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'multiple y fields are supported only by line charts.',
        `${viewPath}.encoding.y`
      ));
    }
    if (encoding.y.length < 2 || encoding.y.length > 8) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'multiple-measure line charts must encode between two and eight y fields.',
        `${viewPath}.encoding.y`
      ));
    }
    if (encoding.color !== undefined) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'multiple-measure line charts must not also encode color.',
        `${viewPath}.encoding.color`
      ));
    }
  }
  if (['dot', 'line', 'scatter'].includes(String(chart)) && isPlainObject(encoding.x) && encoding.x.type !== undefined && encoding.x.type !== 'temporal') {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      `${chart} chart x encoding must be temporal when explicitly typed.`,
      `${viewPath}.encoding.x.type`
    ));
  }
  if (
    chart === 'area'
    && isPlainObject(encoding.x)
    && encoding.x.type !== undefined
    && !['ordinal', 'temporal'].includes(String(encoding.x.type))
  ) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'area chart x encoding must be ordinal or temporal when explicitly typed.',
      `${viewPath}.encoding.x.type`
    ));
  }
  if (chart !== 'dot' && encoding.reference !== undefined) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'reference encoding is supported only by dot charts.',
      `${viewPath}.encoding.reference`
    ));
  }
  if (['pie', 'treemap'].includes(String(chart)) && isPlainObject(encoding.x) && encoding.x.type !== undefined && !['nominal', 'ordinal'].includes(String(encoding.x.type))) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      `${chart} chart x encoding must be nominal or ordinal when explicitly typed.`,
      `${viewPath}.encoding.x.type`
    ));
  }
  if (chart === 'histogram' && isPlainObject(encoding.x) && encoding.x.type !== undefined && !['nominal', 'ordinal'].includes(String(encoding.x.type))) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'histogram chart x encoding must be nominal or ordinal when explicitly typed.',
      `${viewPath}.encoding.x.type`
    ));
  }
  if (chart === 'horizontal-bar' && isPlainObject(encoding.x)) {
    const xField = typeof encoding.x.field === 'string' ? encoding.x.field : null;
    const xType = encoding.x.type;
    const hasNonCategoricalIntrinsicType = xField !== null && (
      TEMPORAL_FIELD_NAMES.includes(xField)
      || ADDITIVE_MEASURE_FIELDS.includes(xField)
      || NON_ADDITIVE_MEASURE_FIELDS.includes(xField)
    );
    if (
      (xType !== undefined && !['nominal', 'ordinal'].includes(String(xType)))
      || (xType === undefined && hasNonCategoricalIntrinsicType)
    ) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'horizontal-bar chart x encoding must be nominal or ordinal.',
        `${viewPath}.encoding.x.type`
      ));
    }
  }
  if (encoding.section !== undefined) {
    if (chart !== 'horizontal-bar' && chart !== 'treemap') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'section encoding is supported only by horizontal-bar and treemap charts.',
        `${viewPath}.encoding.section`
      ));
    } else if (
      isPlainObject(encoding.section)
      && encoding.section.type !== undefined
      && !['nominal', 'ordinal'].includes(String(encoding.section.type))
    ) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        `${chart} chart section encoding must be nominal or ordinal when explicitly typed.`,
        `${viewPath}.encoding.section.type`
      ));
    }
  }
  if (chart === 'treemap') {
    for (const channel of ['x', 'y', 'color', 'section']) {
      const definition = encoding[channel];
      if (!isPlainObject(definition)) continue;
      if (definition.aggregate !== undefined && definition.aggregate !== 'none') {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'treemap fields must use query-produced values; declare aggregation in dashboard.queries.',
          `${viewPath}.encoding.${channel}.aggregate`
        ));
      }
      if (['color', 'section'].includes(channel) && definition.type !== undefined
          && !['nominal', 'ordinal'].includes(String(definition.type))) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          `treemap ${channel} encoding must be nominal or ordinal when explicitly typed.`,
          `${viewPath}.encoding.${channel}.type`
        ));
      }
    }
  }
  if (chart === 'histogram') {
    for (const channel of ['color', 'href']) {
      if (encoding[channel] !== undefined) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          `histogram charts must not encode ${channel}.`,
          `${viewPath}.encoding.${channel}`
        ));
      }
    }
  }
  if (chart === 'heatmap') {
    for (const channel of ['x', 'y']) {
      if (
        isPlainObject(encoding[channel])
        && encoding[channel].type !== undefined
        && !['nominal', 'ordinal'].includes(String(encoding[channel].type))
      ) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          `heatmap chart ${channel} encoding must be nominal or ordinal when explicitly typed.`,
          `${viewPath}.encoding.${channel}.type`
        ));
      }
    }
    if (!isPlainObject(encoding.color)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'heatmap charts must encode quantitative color.',
        `${viewPath}.encoding.color`
      ));
    } else {
      if (encoding.color.type !== undefined && encoding.color.type !== 'quantitative') {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'heatmap chart color encoding must be quantitative when explicitly typed.',
          `${viewPath}.encoding.color.type`
        ));
      }
      if (encoding.color.aggregate === undefined || encoding.color.aggregate === 'none') {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'heatmap chart color encoding must aggregate each discrete cell.',
          `${viewPath}.encoding.color.aggregate`
        ));
      }
    }
  }
  if (chart === 'swimlane') {
    if (isPlainObject(encoding.x) && encoding.x.type !== undefined && encoding.x.type !== 'temporal') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'swimlane chart x encoding must be temporal when explicitly typed.',
        `${viewPath}.encoding.x.type`
      ));
    }
    if (isPlainObject(encoding.x) && encoding.x['time-unit'] !== undefined) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'swimlane chart x encoding must preserve individual timestamps and must not declare time-unit.',
        `${viewPath}.encoding.x.time-unit`
      ));
    }
    if (isPlainObject(encoding.y) && encoding.y.aggregate !== undefined && encoding.y.aggregate !== 'none') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'swimlane chart y encoding must preserve individual observations and must not aggregate.',
        `${viewPath}.encoding.y.aggregate`
      ));
    }
  }
}

/**
 * @param {unknown} encodingNode
 * @param {Record<string, unknown>} encoding
 * @param {string | null} sourceName
 * @param {string} path
 * @param {Map<string, string>} aggregateOutputIds
 * @param {ValidationError[]} errors
 */
function validateMetricEncoding(encodingNode, encoding, sourceName, path, aggregateOutputIds, errors) {
  validateRequiredFieldDefinition(getValueNodeByKey(encodingNode, 'value'), encoding.value, sourceName, `${path}.value`, aggregateOutputIds, errors);

  const valueFieldDefinition = isPlainObject(encoding.value) ? encoding.value : null;
  if (valueFieldDefinition && valueFieldDefinition['time-unit'] !== undefined) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'metric value encoding must not declare time-unit.',
      `${path}.value.time-unit`
    ));
  }

  if (valueFieldDefinition && valueFieldDefinition.type !== undefined && valueFieldDefinition.type !== 'quantitative') {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'metric value encoding must be quantitative when explicitly typed.',
      `${path}.value.type`
    ));
  }

  for (const forbiddenChannel of ['columns', 'x', 'y', 'color']) {
    if (encoding[forbiddenChannel] !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `metric views must not encode ${forbiddenChannel}.`,
        `${path}.${forbiddenChannel}`
      ));
    }
  }

  if (encoding.href !== undefined) {
    validateHrefFieldDefinition(getValueNodeByKey(encodingNode, 'href'), encoding.href, sourceName, `${path}.href`, aggregateOutputIds, errors);
  }
}

/**
 * @param {unknown} encodingNode
 * @param {Record<string, unknown>} encoding
 * @param {string | null} sourceName
 * @param {string} path
 * @param {Map<string, string>} aggregateOutputIds
 * @param {ValidationError[]} errors
 * @param {'list'|'table'} viewKind
 */
function validateTableEncoding(encodingNode, encoding, sourceName, path, aggregateOutputIds, errors, viewKind) {
  if (!Array.isArray(encoding.columns) || encoding.columns.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      `${viewKind} views must encode a non-empty columns sequence.`,
      `${path}.columns`
    ));
  } else {
    const columnsNode = getValueNodeByKey(encodingNode, 'columns');
    for (const [index, column] of encoding.columns.entries()) {
      validateFieldDefinition(
        getSequenceItemNode(columnsNode, index),
        column,
        sourceName,
        `${path}.columns[${index}]`,
        aggregateOutputIds,
        errors
      );
    }
  }

  for (const forbiddenChannel of ['value', 'x', 'y', 'color']) {
    if (encoding[forbiddenChannel] !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `${viewKind} views must not encode ${forbiddenChannel}.`,
        `${path}.${forbiddenChannel}`
      ));
    }
  }

  if (encoding.href !== undefined) {
    validateHrefFieldDefinition(getValueNodeByKey(encodingNode, 'href'), encoding.href, sourceName, `${path}.href`, aggregateOutputIds, errors);
  }
}

/**
 * @param {unknown} encodingNode
 * @param {Record<string, unknown>} encoding
 * @param {unknown} chart
 * @param {string | null} sourceName
 * @param {string} path
 * @param {Map<string, string>} aggregateOutputIds
 * @param {ValidationError[]} errors
 * @param {boolean} [layered]
 */
function validateChartEncoding(encodingNode, encoding, chart, sourceName, path, aggregateOutputIds, errors, layered = false) {
  if (chart !== 'rule' || encoding.x !== undefined) {
    validateRequiredFieldDefinition(getValueNodeByKey(encodingNode, 'x'), encoding.x, sourceName, `${path}.x`, aggregateOutputIds, errors);
  }
  const yNode = getValueNodeByKey(encodingNode, 'y');
  const yDefinitions = Array.isArray(encoding.y) ? encoding.y : [encoding.y];
  for (const [index, definition] of yDefinitions.entries()) {
    const definitionPath = Array.isArray(encoding.y) ? `${path}.y[${index}]` : `${path}.y`;
    validateRequiredFieldDefinition(
      Array.isArray(encoding.y) ? getSequenceItemNode(yNode, index) : yNode,
      definition,
      sourceName,
      definitionPath,
      aggregateOutputIds,
      errors
    );
  }

  if (isPlainObject(encoding.x) && encoding.x.type !== undefined && !['nominal', 'ordinal', 'temporal'].includes(String(encoding.x.type))) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'chart x encoding must use a nominal, ordinal, or temporal type when explicitly typed.',
      `${path}.x.type`
    ));
  }

  if (isPlainObject(encoding.x) && encoding.x['time-unit'] !== undefined) {
    const xFieldName = typeof encoding.x.field === 'string' ? encoding.x.field : null;
    const xType = typeof encoding.x.type === 'string' ? encoding.x.type : null;
    if (xType !== 'temporal' && (!xFieldName || !TEMPORAL_FIELD_NAMES.includes(xFieldName))) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'chart x time-unit requires a temporal field.',
        `${path}.x.time-unit`
      ));
    }
  }

  if (encoding.value !== undefined) {
    if (chart !== 'scatter') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'value encoding is supported only by scatter charts and metrics.',
        `${path}.value`
      ));
    } else {
      validateFieldDefinition(getValueNodeByKey(encodingNode, 'value'), encoding.value, sourceName, `${path}.value`, aggregateOutputIds, errors);
      if (isPlainObject(encoding.value) && encoding.value.type !== 'quantitative') {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'scatter value encoding must declare type quantitative.', `${path}.value.type`));
      }
      for (const channel of ['x', 'y', 'color', 'value']) {
        const definition = encoding[channel];
        if (isPlainObject(definition) && definition.aggregate !== undefined && definition.aggregate !== 'none') {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
            'bubble chart fields must use query-produced values; declare aggregation in dashboard.queries.',
            `${path}.${channel}.aggregate`));
        }
      }
    }
  }

  if (encoding.columns !== undefined) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'chart views must not encode columns.',
      `${path}.columns`
    ));
  }

  if (encoding.color !== undefined) {
    validateFieldDefinition(getValueNodeByKey(encodingNode, 'color'), encoding.color, sourceName, `${path}.color`, aggregateOutputIds, errors);
  }

  if (encoding.section !== undefined) {
    validateFieldDefinition(getValueNodeByKey(encodingNode, 'section'), encoding.section, sourceName, `${path}.section`, aggregateOutputIds, errors);
  }

  if (encoding.weight !== undefined) {
    validateFieldDefinition(getValueNodeByKey(encodingNode, 'weight'), encoding.weight, sourceName, `${path}.weight`, aggregateOutputIds, errors);
    if (chart !== 'swimlane') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'weight encoding is supported only by swimlane charts.',
        `${path}.weight`
      ));
    }
    if (isPlainObject(encoding.weight) && encoding.weight.type !== undefined && encoding.weight.type !== 'quantitative') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'swimlane weight encoding must be quantitative when explicitly typed.',
        `${path}.weight.type`
      ));
    }
  }

  if (encoding.reference !== undefined) {
    validateFieldDefinition(getValueNodeByKey(encodingNode, 'reference'), encoding.reference, sourceName, `${path}.reference`, aggregateOutputIds, errors);
    if (isPlainObject(encoding.reference) && encoding.reference.type !== undefined && encoding.reference.type !== 'quantitative') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'chart reference encoding must be quantitative when explicitly typed.',
        `${path}.reference.type`
      ));
    }
    if (isPlainObject(encoding.reference) && encoding.reference.aggregate !== undefined) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'chart reference encoding must not declare an aggregate.',
        `${path}.reference.aggregate`
      ));
    }
  }

  if (encoding.href !== undefined) {
    validateHrefFieldDefinition(getValueNodeByKey(encodingNode, 'href'), encoding.href, sourceName, `${path}.href`, aggregateOutputIds, errors);
  }

  if (isPlainObject(encoding.x) && encoding.x.aggregate !== undefined) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'chart x encoding must not declare an aggregate.',
      `${path}.x.aggregate`
    ));
  }

  for (const [index, definition] of yDefinitions.entries()) {
    if (
      isPlainObject(definition)
      && definition.type !== undefined
      && (['heatmap', 'swimlane'].includes(String(chart))
        ? !['nominal', 'ordinal'].includes(String(definition.type))
        : definition.type !== 'quantitative')
    ) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        ['heatmap', 'swimlane'].includes(String(chart))
          ? `${chart} chart y encoding must be nominal or ordinal when explicitly typed.`
          : 'chart y encoding must be quantitative when explicitly typed.',
        Array.isArray(encoding.y) ? `${path}.y[${index}].type` : `${path}.y.type`
      ));
    }
  }

  const xType = isPlainObject(encoding.x) && typeof encoding.x.type === 'string' ? encoding.x.type : null;
  const xFieldName = isPlainObject(encoding.x) && typeof encoding.x.field === 'string' ? encoding.x.field : null;
  const xIsTemporal = xType === 'temporal' || (xType === null && xFieldName !== null && TEMPORAL_FIELD_NAMES.includes(xFieldName));
  const xHasTimeUnit = isPlainObject(encoding.x) && encoding.x['time-unit'] !== undefined;
  const expectedDefault = xIsTemporal ? 'line' : 'bar';

  if (
    expectedDefault === 'line'
    && !layered
    && !['dot', 'scatter', 'swimlane'].includes(String(chart))
    && !xHasTimeUnit
    && !Array.isArray(encoding.y)
  ) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      `Temporal chart x requires encoding.x.time-unit. Set it to ${TIME_UNIT_VALUES.join(', ')} to match the intended UTC grouping; for example, add "time-unit": "day" inside encoding.x. Setting chart: line or pre-bucketing the source query does not replace this encoding property.`,
      `${path}.x`
    ));
  }
}

/**
 * @param {unknown} fieldNode
 * @param {unknown} fieldDefinition
 * @param {string | null} sourceName
 * @param {string} path
 * @param {Map<string, string>} aggregateOutputIds
 * @param {ValidationError[]} errors
 */
function validateRequiredFieldDefinition(fieldNode, fieldDefinition, sourceName, path, aggregateOutputIds, errors) {
  if (fieldDefinition === undefined) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      `${path.split('.').at(-1)} is required.`,
      path
    ));
    return;
  }

  validateFieldDefinition(fieldNode, fieldDefinition, sourceName, path, aggregateOutputIds, errors);
}

/**
 * @param {unknown} fieldNode
 * @param {unknown} fieldDefinition
 * @param {string | null} sourceName
 * @param {string} path
 * @param {Map<string, string>} aggregateOutputIds
 * @param {ValidationError[]} errors
 */
function validateFieldDefinition(fieldNode, fieldDefinition, sourceName, path, aggregateOutputIds, errors) {
  if (!isPlainObject(fieldDefinition)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'field definitions must be mappings.',
      path
    ));
    return;
  }

  validateObjectKeys(fieldNode, FIELD_DEFINITION_KEYS, path, errors);
  validateStringField(fieldDefinition.field, `${path}.field`, true, errors);
  validateOptionalStringField(fieldDefinition.title, `${path}.title`, errors);
  if (fieldDefinition.unit !== undefined) {
    validateStringField(fieldDefinition.unit, `${path}.unit`, true, errors);
  }
  if (fieldDefinition.filter !== undefined && typeof fieldDefinition.filter !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'filter must be a boolean.',
      `${path}.filter`
    ));
  }

  const aggregate = fieldDefinition.aggregate ?? 'none';
  if (fieldDefinition.aggregate !== undefined) {
    validateStringField(fieldDefinition.aggregate, `${path}.aggregate`, true, errors);
    if (typeof fieldDefinition.aggregate === 'string' && !AGGREGATE_VALUES.includes(fieldDefinition.aggregate)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'aggregate must use one canonical aggregate value.',
        `${path}.aggregate`
      ));
    }
  }

  if (fieldDefinition.type !== undefined) {
    validateStringField(fieldDefinition.type, `${path}.type`, true, errors);
    if (typeof fieldDefinition.type === 'string' && !FIELD_TYPE_VALUES.includes(fieldDefinition.type)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'type must use one canonical field type.',
        `${path}.type`
      ));
    }
  }

  if (fieldDefinition.display !== undefined) {
    validateStringField(fieldDefinition.display, `${path}.display`, true, errors);
    if (typeof fieldDefinition.display === 'string' && !FIELD_DISPLAY_VALUES.includes(fieldDefinition.display)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'display must use one canonical field display value.',
        `${path}.display`
      ));
    }
  }

  if (fieldDefinition.format !== undefined) {
    validateStringField(fieldDefinition.format, `${path}.format`, true, errors);
    const format = typeof fieldDefinition.format === 'string' ? fieldDefinition.format : null;
    if (format !== null && !FIELD_FORMAT_VALUES.includes(format)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'format must use one canonical field format value.',
        `${path}.format`
      ));
    }
    const fieldName = typeof fieldDefinition.field === 'string' ? fieldDefinition.field : null;
    const intrinsicallyNominalOrOrdinal = fieldName !== null
      && !LINK_FIELD_NAMES.includes(fieldName)
      && !TEMPORAL_FIELD_NAMES.includes(fieldName)
      && !ADDITIVE_MEASURE_FIELDS.includes(fieldName)
      && !NON_ADDITIVE_MEASURE_FIELDS.includes(fieldName)
      && aggregate === 'none';
    const intrinsicallyTemporal = fieldName !== null
      && TEMPORAL_FIELD_NAMES.includes(fieldName)
      && aggregate === 'none';
    if (
      format !== null
      && FIELD_FORMAT_VALUES.includes(format)
      && (
        (
          format === 'human-friendly-timestamp'
          && (
            (typeof fieldDefinition.type === 'string' && fieldDefinition.type !== 'temporal')
            || (fieldDefinition.type === undefined && !intrinsicallyTemporal)
          )
        )
        || (
          format !== 'human-friendly-timestamp'
          && (
            (typeof fieldDefinition.type === 'string' && !['nominal', 'ordinal'].includes(fieldDefinition.type))
            || (fieldDefinition.type === undefined && !intrinsicallyNominalOrOrdinal)
          )
        )
      )
    ) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        format === 'human-friendly-timestamp'
          ? 'human-friendly-timestamp format requires a temporal field.'
          : `${format} format requires a nominal or ordinal field.`,
        `${path}.format`
      ));
    }
    if (['workflow-run-url', 'shortened-url'].includes(format ?? '') && !path.includes('.columns[')) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        `${format} format may be used only on table columns.`,
        `${path}.format`
      ));
    }
  }

  if (fieldDefinition['time-unit'] !== undefined) {
    validateStringField(fieldDefinition['time-unit'], `${path}.time-unit`, true, errors);
    if (typeof fieldDefinition['time-unit'] === 'string' && !TIME_UNIT_VALUES.includes(fieldDefinition['time-unit'])) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'time-unit must use one canonical time unit.',
        `${path}.time-unit`
      ));
    }
  }

  if (fieldDefinition.as !== undefined) {
    validateStringField(fieldDefinition.as, `${path}.as`, true, errors);
    if (aggregate === 'none') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'field definitions with aggregate none must not include as.',
        `${path}.as`
      ));
    }
  }

  if (fieldDefinition['time-unit'] !== undefined) {
    const fieldName = typeof fieldDefinition.field === 'string' ? fieldDefinition.field : null;
    if (!fieldName || !TEMPORAL_FIELD_NAMES.includes(fieldName)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'time-unit may be used only with a temporal field.',
        `${path}.time-unit`
      ));
    }
  }

  const fieldName = typeof fieldDefinition.field === 'string' ? fieldDefinition.field : null;
  if (fieldName && sourceName) {
    const sourceFields = sourceFieldNames(sourceName);
    if (sourceFields && !sourceFields.includes(fieldName)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'field must exist in the selected source.',
        `${path}.field`
      ));
    }
  }

  if (fieldName && typeof aggregate === 'string' && AGGREGATE_VALUES.includes(aggregate)) {
    validateAggregateCompatibility(fieldName, aggregate, path, errors);
    if (aggregate !== 'none') {
      const outputId = typeof fieldDefinition.as === 'string' ? fieldDefinition.as : `${aggregate}-${fieldName}`;
      const existingPath = aggregateOutputIds.get(outputId);
      if (existingPath) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'aggregate output identifiers must be unique within a view.',
          path
        ));
      } else {
        aggregateOutputIds.set(outputId, path);
      }
    }
  }
}

/**
 * @param {unknown} fieldNode
 * @param {unknown} fieldDefinition
 * @param {string | null} sourceName
 * @param {string} path
 * @param {Map<string, string>} aggregateOutputIds
 * @param {ValidationError[]} errors
 */
function validateHrefFieldDefinition(fieldNode, fieldDefinition, sourceName, path, aggregateOutputIds, errors) {
  validateFieldDefinition(fieldNode, fieldDefinition, sourceName, path, aggregateOutputIds, errors);

  if (!isPlainObject(fieldDefinition)) {
    return;
  }

  const fieldName = typeof fieldDefinition.field === 'string' ? fieldDefinition.field : null;
  if (!fieldName) {
    return;
  }

  if (!LINK_FIELD_NAMES.includes(fieldName) && !DASHBOARD_LINK_FIELD_NAMES.includes(fieldName)) {
    errors.push(createError(
      ERROR_CODES.invalidLinkReference,
      'href.field must reference one relation-specific link field or one declared campaign, repository, workflow, or command dashboard link field.',
      `${path}.field`
    ));
  }
}

/**
 * @param {string} fieldName
 * @param {string} aggregate
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateAggregateCompatibility(fieldName, aggregate, path, errors) {
  if (aggregate === 'sum' && !ADDITIVE_MEASURE_FIELDS.includes(fieldName)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'sum is allowed only for raw-token measures and aic.',
      `${path}.aggregate`
    ));
  }

  if (NON_ADDITIVE_MEASURE_FIELDS.includes(fieldName) && !['none', 'mean', 'min', 'max'].includes(aggregate)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'value and operational-value support only none, mean, min, or max.',
      `${path}.aggregate`
    ));
  }
}

/**
 * @param {unknown} data
 * @param {unknown} encoding
 * @param {Map<string, string>} aggregateOutputIds
 * @param {string | null} sourceName
 * @param {string} viewPath
 * @param {ValidationError[]} errors
 * @param {unknown} facet
 */
function validateOrderByReferences(data, encoding, aggregateOutputIds, sourceName, viewPath, errors, facet) {
  if (!isPlainObject(data) || !Array.isArray(data['order-by']) || !sourceName) {
    return;
  }

  const declaredFields = sourceFieldNames(sourceName);
  if (!declaredFields) {
    return;
  }
  const sourceFieldSet = new Set(declaredFields);
  const entityIdSet = new Set(SOURCE_ENTITY_IDENTIFIER_FIELDS[/** @type {keyof typeof SOURCE_ENTITY_IDENTIFIER_FIELDS} */ (sourceName)] ?? []);
  const unaggregatedOutputFields = new Set();
  if (isPlainObject(encoding)) {
    const definitions = [
      encoding.x,
      ...(Array.isArray(encoding.y) ? encoding.y : [encoding.y]),
      encoding.color,
      encoding.section,
      ...(Array.isArray(encoding.columns) ? encoding.columns : []),
      ...Object.values(chartFacet({ encoding, facet }) ?? {}),
    ];
    for (const definition of definitions) {
      if (
        isPlainObject(definition)
        && typeof definition.field === 'string'
        && (definition.aggregate === undefined || definition.aggregate === 'none')
      ) {
        unaggregatedOutputFields.add(definition.field);
      }
    }
  }

  for (const [index, clause] of data['order-by'].entries()) {
    if (!isPlainObject(clause) || typeof clause.field !== 'string') {
      continue;
    }

    const fieldPath = `${viewPath}.data.order-by[${index}].field`;
    const fieldName = clause.field;
    const matchesAggregateOutput = aggregateOutputIds.has(fieldName);
    const matchesSourceField = sourceFieldSet.has(fieldName);

    if (matchesAggregateOutput && matchesSourceField) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'order-by.field must resolve to exactly one output field at the post-aggregation grain.',
        fieldPath
      ));
      continue;
    }

    if (matchesAggregateOutput) {
      continue;
    }

    if (!matchesSourceField || (!unaggregatedOutputFields.has(fieldName) && !entityIdSet.has(fieldName))) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'order-by.field must reference one unique aggregate output identifier or one source field valid at the output grain.',
        fieldPath
      ));
    }
  }
}
