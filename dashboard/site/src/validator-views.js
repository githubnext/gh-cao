import { state } from './validator-state.js';
import { isReadOnlyCliAction, validateActionLevel } from './action-validator.js';
import { CALLOUT_KEYS, ERROR_CODES, FIELD_DEFINITION_KEYS, LINK_FIELD_NAMES, FACTORY_FLOOR_STATION_VALUES, FACTORY_HEADER_SOURCE_ROLES, FACTORY_FLOOR_SOURCE_ROLES, GRAPHICAL_LAYOUT_EXEMPT_PAGE_IDS, IDENTIFIER_PATTERN, MAX_ESSENTIAL_VIEWS_PER_PAGE, PAGE_FORM_KEYS, PAGE_FORM_UPDATE_KEYS, PAGE_FORM_UPDATE_STRATEGY_VALUES, PAGE_FORM_FIELD_KEYS, PAGE_FORM_CONTROL_VALUES, PAGE_FORM_OPTION_KEYS, PAGE_FORM_MIN_DELAY_MS, PAGE_FORM_MAX_DELAY_MS, PAGE_ICON_VALUES, QUERY_SOURCE_VALUES, TABLE_ACTION_KEYS, TABLE_ACTION_PRESENTATION_VALUES, TABLE_ACTION_WHEN_KEYS, TREE_TABLE_KEYS, VIEW_DATA_KEYS, VIEW_DATA_ARGUMENT_KEYS, VIEW_CHART_VALUES, VIEW_CONTROL_VALUES, VIEW_LIST_DRILL_ARGUMENT_KEYS, VIEW_LIST_DRILL_KEYS, VIEW_LIST_DRILL_TYPE_VALUES, VIEW_LIST_LAYOUT_VALUES, VIEW_LIST_APPEARANCE_VALUES, VIEW_LIST_VIEW_ALL_KEYS, VIEW_DISCLOSURE_VALUES, VIEW_ELEMENT_CONFIG_KEYS, VIEW_ELEMENT_ANIMATION_VALUES, VIEW_ELEMENT_VALUES, PLURAL_LABEL_ELEMENTS, PLURAL_TEXT_KEYS, VIEW_KEYS, VIEW_REQUIREMENT_KEYS, VIEW_BACKEND_VALUES, VIEW_LAYOUT_VALUES, VIEW_LIST_KEYS, VIEW_LIST_STYLE_VALUES, VIEW_MARK_VALUES, VIEW_METRIC_KEYS, VIEW_METRIC_ANIMATION_VALUES, VIEW_METRIC_STYLE_VALUES, VIEW_METRIC_TONE_VALUES, VIEW_TITLE_LINK_KEYS } from './specification.js';
import { OUTCOME_DETAIL_SECTION_BODY_VALUES, CAMPAIGN_ROUTE_BODY_VALUES, WORKFLOW_ROUTE_BODY_VALUES } from './components/route-body-specification.js';
import { cliActionTemplateFields } from './cli-action-template.js';
import { environmentDefaultsCommand } from './environment-defaults.js';
import { parsePromptWizard } from './prompt-wizard.js';
import { validateViewFilterBar as validateViewFilterBarContract } from './view-filter-validator.js';
import { validateSource, sourceFieldNames, validateSourceSequence, validateSemanticFieldLiterals, validateContext, validateDatasetMetadata } from './validator-queries.js';
import { validateEncoding } from './validator-encoding.js';
import { validateChartFacet } from './validator-facet.js';
import { validateRequiredIdentifier, validateStringField, validateSemanticMetadataLength, validateOptionalStringField, validateObjectKeys, createError, isPlainObject, getMappingItems, getValueNodeByKey, getSequenceItemNode } from './validator-common.js';
import { resolveReusablePageViews } from './validator-state.js';
import { createDebug } from './debug.js';
import { ChartLayerError, resolveChartLayers } from './chart-layer-specification.js';
import { MAX_TREEMAP_LEAVES, TREEMAP_KEYS, TREEMAP_METHOD_VALUES, SCATTER_KEYS } from './specification.js';

/** @typedef {import('./validator.js').ValidationError} ValidationError */

const debugValidatorViews = createDebug('validator-views');

/**
 * @param {unknown} labels
 * @param {unknown} labelsNode
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validatePluralLabels(labels, labelsNode, path, errors) {
  if (!isPlainObject(labels) || Object.keys(labels).length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'config.labels must be a non-empty mapping of plural text variables.',
      path
    ));
    return;
  }
  for (const [labelId, text] of Object.entries(labels)) {
    const labelPath = `${path}.${labelId}`;
    if (!IDENTIFIER_PATTERN.test(labelId)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'plural text identifiers must use canonical kebab-case.',
        labelPath
      ));
    }
    if (!isPlainObject(text)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'a plural text variable must be a mapping with singular and plural text.',
        labelPath
      ));
      continue;
    }
    validateObjectKeys(getValueNodeByKey(labelsNode, labelId), PLURAL_TEXT_KEYS, labelPath, errors);
    for (const key of Object.keys(text)) {
      if (!PLURAL_TEXT_KEYS.includes(key) && getMappingItems(labelsNode) === undefined) {
        errors.push(createError(
          ERROR_CODES.unknownOrDuplicateKey,
          `Unknown key "${key}" is not allowed.`,
          `${labelPath}.${key}`
        ));
      }
    }
    for (const key of PLURAL_TEXT_KEYS) {
      validateStringField(text[key], `${labelPath}.${key}`, true, errors);
    }
  }
}

/** @param {Record<string, unknown>} view @param {string} path @param {ValidationError[]} errors */
export function validateViewActions(view, path, errors) {
  if (view.actions === undefined) return;
  if (typeof view.mark !== 'string' || !['table', 'chart', 'list'].includes(view.mark)
    || !Array.isArray(view.actions) || view.actions.length === 0
    || new Set(view.actions).size !== view.actions.length) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
      'actions requires a data mark and a non-empty sequence of unique view-placed CLI action references.', `${path}.actions`));
    return;
  }
  for (const [index, id] of view.actions.entries()) {
    if (typeof id !== 'string' || state.declaredCliActions.get(id)?.placement !== 'view') {
      errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'actions must reference a declared view-placed CLI action.', `${path}.actions[${index}]`));
    }
  }
}

/**
 * @param {unknown} view
 * @param {unknown} viewNode
 * @param {string} path
 * @param {Set<string>} viewIds
 * @param {ValidationError[]} errors
 */
export function validateView(view, viewNode, path, viewIds, errors) {
  if (!isPlainObject(view)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'view must be a mapping.',
      path
    ));
    debugValidatorViews({ operation: 'validate-view', mark: null, status: 'invalid' });
    return;
  }

  const errorCountBeforeView = errors.length;
  validateChartFacet(view, viewNode, path, errors);
  if (view.scatter !== undefined) {
    const scatterPath = `${path}.scatter`;
    if (view.mark !== 'chart' || view.chart !== 'scatter') {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'scatter options are allowed only on scatter charts.', scatterPath));
    }
    if (!isPlainObject(view.scatter)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'scatter must be a mapping.', scatterPath));
    } else {
      const scatterNode = getValueNodeByKey(viewNode, 'scatter');
      validateObjectKeys(scatterNode, SCATTER_KEYS, scatterPath, errors);
      if (!getMappingItems(scatterNode)) {
        for (const key of Object.keys(view.scatter)) {
          if (!SCATTER_KEYS.includes(key)) {
            errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, `Unknown key "${key}" is not allowed.`, `${scatterPath}.${key}`));
          }
        }
      }
      if (view.scatter.cluster !== undefined && typeof view.scatter.cluster !== 'boolean') {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'scatter cluster must be a Boolean when present.', `${scatterPath}.cluster`));
      }
    }
  }
  if (view.treemap !== undefined) {
    const treemapPath = `${path}.treemap`;
    if (view.mark !== 'chart' || view.chart !== 'treemap') {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'treemap options are allowed only on treemap charts.', treemapPath));
    }
    if (!isPlainObject(view.treemap)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'treemap must be a mapping.', treemapPath));
    } else {
      const treemapNode = getValueNodeByKey(viewNode, 'treemap');
      validateObjectKeys(treemapNode, TREEMAP_KEYS, treemapPath, errors);
      if (!getMappingItems(treemapNode)) {
        for (const key of Object.keys(view.treemap)) {
          if (!TREEMAP_KEYS.includes(key)) {
            errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, `Unknown key "${key}" is not allowed.`, `${treemapPath}.${key}`));
          }
        }
      }
      if (view.treemap.method !== undefined && !TREEMAP_METHOD_VALUES.includes(String(view.treemap.method))) {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'treemap method must be squarify, binary, or slicedice.', `${treemapPath}.method`));
      }
      for (const [key, minimum, maximum] of [['ratio', 1, 5], ['padding', 0, 10]]) {
        const value = view.treemap[String(key)];
        if (value !== undefined && (typeof value !== 'number' || !Number.isFinite(value) || value < Number(minimum) || value > Number(maximum))) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, `treemap ${key} must be a finite number from ${minimum} to ${maximum}.`, `${treemapPath}.${key}`));
        }
      }
    }
  }
  if (view.mark === 'chart' && view.chart === 'treemap'
      && (!isPlainObject(view.data) || !Number.isSafeInteger(view.data.limit) || Number(view.data.limit) < 1 || Number(view.data.limit) > MAX_TREEMAP_LEAVES)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, `treemap charts must declare data.limit from 1 to ${MAX_TREEMAP_LEAVES}.`, `${path}.data.limit`));
  }

  if (view.title === undefined && typeof view.id === 'string' && !IDENTIFIER_PATTERN.test(view.id)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'view title default requires a canonical view id.',
      `${path}.id`
    ));
  }

  validateObjectKeys(viewNode, view.mark === 'chart' ? VIEW_KEYS : [...VIEW_KEYS, 'views'], path, errors);
  validateViewActions(view, path, errors);
  if (view.requires !== undefined) {
    const requiresPath = `${path}.requires`;
    if (!isPlainObject(view.requires)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'requires must be a mapping.', requiresPath));
    } else {
      validateObjectKeys(getValueNodeByKey(viewNode, 'requires'), VIEW_REQUIREMENT_KEYS, requiresPath, errors);
      if (typeof view.requires.backend !== 'string' || !VIEW_BACKEND_VALUES.includes(view.requires.backend)) {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'requires backend must be static or hosted.', `${requiresPath}.backend`));
      }
      if (view.requires['on-unavailable'] !== undefined
        && view.requires['on-unavailable'] !== 'hide'
        && view.requires['on-unavailable'] !== 'message') {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          'requires on-unavailable must be hide or message.', `${requiresPath}.on-unavailable`));
      }
      if (view.requires.message !== undefined || view.requires['on-unavailable'] !== 'hide') {
        validateStringField(view.requires.message, `${requiresPath}.message`, true, errors);
      }
    }
  }
  validateRequiredIdentifier(view.id, `${path}.id`, 'view id', errors);
  if (typeof view.id === 'string') {
    if (viewIds.has(view.id)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'view id must be unique within page.views.',
        `${path}.id`
      ));
    }
    viewIds.add(view.id);
  }

  validateOptionalStringField(view.title, `${path}.title`, errors);
  if (view['show-title'] !== undefined && typeof view['show-title'] !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'show-title must be a Boolean when present.',
      `${path}.show-title`
    ));
  }
  validateStringField(view['disclosure-label'], `${path}.disclosure-label`, false, errors);
  if (
    view['disclosure-label'] !== undefined
    && view.disclosure !== 'supplemental'
  ) {
    errors.push(createError(
      ERROR_CODES.invalidProgressiveDisclosureConfiguration,
      'disclosure-label is allowed only on supplemental views.',
      `${path}.disclosure-label`
    ));
  }
  validateOptionalStringField(view.description, `${path}.description`, errors);
  if (view.locked !== undefined && typeof view.locked !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'locked must be a boolean.',
      `${path}.locked`
    ));
  }
  if (view.subject !== undefined) {
    validateStringField(view.subject, `${path}.subject`, true, errors);
  }
  if (view.objective !== undefined) validateStringField(view.objective, `${path}.objective`, true, errors);
  if (view.acceptance !== undefined) validateStringField(view.acceptance, `${path}.acceptance`, true, errors);
  validateSemanticMetadataLength(view, path, errors);
  if (view.prompt !== undefined && (typeof view.prompt !== 'string' || !['auto', 'none', 'always'].includes(view.prompt))) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'prompt must be auto, none, or always.',
      `${path}.prompt`
    ));
  }
  if (view['prompt-level'] !== undefined) validateActionLevel(view['prompt-level'], `${path}.prompt-level`, errors);
  validateCallout(
    view.callout,
    getValueNodeByKey(viewNode, 'callout'),
    view.mark,
    view.title,
    view.description,
    `${path}.callout`,
    errors
  );
  if (view['empty-message'] !== undefined) {
    validateStringField(view['empty-message'], `${path}.empty-message`, true, errors);
  }

  if (view.controls !== undefined) {
    validateStringField(view.controls, `${path}.controls`, true, errors);
    if (typeof view.controls === 'string' && !VIEW_CONTROL_VALUES.includes(view.controls)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'controls must use one canonical interactive, filter-only, or static value.',
        `${path}.controls`
      ));
    }
    if (view.mark !== 'table') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'controls is allowed only when mark is "table".',
        `${path}.controls`
      ));
    }
  }

  if (isPlainObject(view.encoding) && view.encoding.card !== undefined && view['lazy-list'] !== true) {
    errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'encoding.card is allowed only on lazy table views.', `${path}.encoding.card`));
  }
  if (view['lazy-list'] !== undefined) {
    if (typeof view['lazy-list'] !== 'boolean') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'lazy-list must be a boolean.',
        `${path}.lazy-list`
      ));
    }
    const supportsLazyList = view.mark === 'table' && !(view['lazy-list'] === true && view.controls === 'static')
      || view.mark === 'list' && isPlainObject(view.list) && view.list.style === 'entity-cards';
    if (!supportsLazyList) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'lazy-list is allowed only on interactive table views or entity-card lists.',
        `${path}.lazy-list`
      ));
    }
  }

  if (view['column-summaries'] !== undefined) {
    if (typeof view['column-summaries'] !== 'boolean') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'column-summaries must be a boolean.',
        `${path}.column-summaries`
      ));
    }
    if (view.mark !== 'table') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'column-summaries is allowed only when mark is "table".',
        `${path}.column-summaries`
      ));
    }
  }

  if (view.tree !== undefined) {
    const treePath = `${path}.tree`;
    if (!isPlainObject(view.tree)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'tree must be a mapping.',
        treePath
      ));
    } else {
      validateObjectKeys(getValueNodeByKey(viewNode, 'tree'), TREE_TABLE_KEYS, treePath, errors);
      validateRequiredIdentifier(view.tree['id-field'], `${treePath}.id-field`, 'tree id field', errors);
      validateRequiredIdentifier(view.tree['parent-field'], `${treePath}.parent-field`, 'tree parent field', errors);
      const treeSource = isPlainObject(view.data) && typeof view.data.source === 'string'
        ? view.data.source
        : null;
      const sourceFields = treeSource
        ? sourceFieldNames(treeSource)
        : null;
      for (const field of [view.tree['id-field'], view.tree['parent-field']]) {
        if (typeof field === 'string' && sourceFields && !sourceFields.includes(field)) {
          errors.push(createError(
            ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
            'tree fields must be declared by data.source.',
            treePath
          ));
        }
      }
      if (view.tree['id-field'] === view.tree['parent-field']) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'tree id-field and parent-field must be different.',
          treePath
        ));
      }
    }
    if (view.mark !== 'table') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'tree is allowed only when mark is "table".',
        treePath
      ));
    }
    if (view.controls !== 'static') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'tree tables must use static controls to preserve hierarchy.',
        `${path}.controls`
      ));
    }
  }

  if (view['empty-message'] !== undefined && !['chart', 'list', 'table'].includes(String(view.mark))) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'empty-message is allowed only when mark is "chart", "list", or "table".',
      `${path}.empty-message`
    ));
  }

  validateTitleLink(
    view['title-link'],
    getValueNodeByKey(viewNode, 'title-link'),
    view.mark,
    view.data,
    `${path}.title-link`,
    errors
  );

  validateStringField(view.mark, `${path}.mark`, true, errors);
  if (typeof view.mark === 'string' && !VIEW_MARK_VALUES.includes(view.mark)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'mark must use one canonical custom-view mark value.',
      `${path}.mark`
    ));
  }

  if (view.element !== undefined) {
    validateStringField(view.element, `${path}.element`, true, errors);
    if (typeof view.element === 'string' && !VIEW_ELEMENT_VALUES.includes(view.element)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'element must use one canonical UI element value.',
        `${path}.element`
      ));
    }
    if (view.mark !== 'element') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'element is allowed only when mark is "element".',
        `${path}.element`
      ));
    }
  } else if (view.mark === 'element') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'element views must name one canonical UI element.',
      `${path}.element`
    ));
  }

  if (view.element === 'environment-defaults' && view.config === undefined) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
      'environment-defaults requires config.actions with scoped CLI action references.', `${path}.config`));
  }
  if (view.element === 'prompt-wizard' || isPlainObject(view.config) && view.config.wizard !== undefined) {
    try {
      if (view.element !== 'prompt-wizard') throw new Error('config.wizard is supported only on prompt-wizard.');
      parsePromptWizard(isPlainObject(view.config) ? view.config.wizard : undefined);
      if (!isPlainObject(view.data) || !Array.isArray(view.data.sources) || view.data.sources.length) {
        throw new Error('prompt-wizard is interaction-only and requires data.sources: [].');
      }
    } catch (error) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
        error instanceof Error ? error.message : 'Invalid prompt-wizard configuration.', `${path}.config.wizard`));
    }
  }
  if (view.config !== undefined) {
    if (!isPlainObject(view.config)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'config must be a mapping.',
        `${path}.config`
      ));
    } else if (view.mark !== 'element') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'config is allowed only when mark is "element".',
        `${path}.config`
      ));
    } else {
      const configNode = getValueNodeByKey(viewNode, 'config');
      validateObjectKeys(configNode, VIEW_ELEMENT_CONFIG_KEYS, `${path}.config`, errors);
      const defaultsActions = view.config.actions;
      if ((view.config.actions !== undefined || view.element === 'environment-defaults') && (view.element !== 'environment-defaults'
        || !isPlainObject(defaultsActions)
        || Object.keys(defaultsActions).length !== 6
        || ['get-repo', 'update-repo', 'get-org', 'update-org', 'get-ent', 'update-ent']
          .some((key) => typeof defaultsActions[key] !== 'string' || !IDENTIFIER_PATTERN.test(defaultsActions[key])))) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
          'environment-defaults config.actions must map get/update for repo, org, and ent to CLI action IDs.',
          `${path}.config.actions`));
      } else if (view.element === 'environment-defaults' && isPlainObject(view.config.actions)) {
        for (const [key, id] of Object.entries(view.config.actions)) {
          const action = typeof id === 'string' ? state.declaredCliActions.get(id) : undefined;
          const parsed = typeof action?.command === 'string' ? environmentDefaultsCommand(action.command) : null;
          const [operation, scope] = key.split('-');
          const args = action?.arguments;
          const validUpdate = operation !== 'update' || typeof action?.command === 'string'
            && !action.command.split(' ').includes('--dry-run')
            && Array.isArray(args) && args.length === 1 && isPlainObject(args[0])
            && args[0].id === 'dry-run' && args[0].flag === '--dry-run'
            && args[0].type === 'boolean' && args[0].default === true;
          if (!parsed || parsed.operation !== operation || parsed.scope !== scope
            || action?.placement !== 'view' || action['copy-only'] === true
            || action.level !== (operation === 'get' ? 'explore' : 'operate') || !validUpdate) {
            errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
              'Defaults actions must reference matching view-placed scoped gh aw env commands.',
              `${path}.config.actions.${key}`));
          }
        }
      }
      if ((view.element === 'workflow-route-page' || view.element === 'campaign-route' || view.element === 'outcome-detail-section') && view.config.body !== undefined) {
        validateStringField(view.config.body, `${path}.config.body`, true, errors);
       const allowedBodies = view.element === 'workflow-route-page'
         ? WORKFLOW_ROUTE_BODY_VALUES
         : view.element === 'campaign-route'
           ? CAMPAIGN_ROUTE_BODY_VALUES
           : OUTCOME_DETAIL_SECTION_BODY_VALUES;
       if (typeof view.config.body === 'string' && !allowedBodies.includes(view.config.body)) {
         errors.push(createError(
           ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
           `${view.element} config.body must use one canonical route body value.`,
           `${path}.config.body`
         ));
       }
      } else if (view.config.body !== undefined) {
       errors.push(createError(
         ERROR_CODES.missingOrInvalidRequiredField,
         'config.body is supported only for the workflow-route-page, campaign-route, and outcome-detail-section elements.',
         `${path}.config.body`
       ));
      }
      if (view.config['view-all-page'] !== undefined) {
        validateStringField(view.config['view-all-page'], `${path}.config.view-all-page`, true, errors);
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'config.view-all-page is not supported by a registered element.',
          `${path}.config.view-all-page`
        ));
      }
      if (view.config['view-all-label'] !== undefined) {
        validateStringField(view.config['view-all-label'], `${path}.config.view-all-label`, true, errors);
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'config.view-all-label is not supported by a registered element.',
          `${path}.config.view-all-label`
        ));
      }
      if (view.config.sections !== undefined) {
       errors.push(createError(
         ERROR_CODES.missingOrInvalidRequiredField,
         'config.sections is not supported by a registered element.',
         `${path}.config.sections`
       ));
      }
      if (view.config.stations !== undefined) {
        if (view.element !== 'factory-floor') {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'config.stations is supported only for the factory-floor element.',
            `${path}.config.stations`
          ));
        } else if (!Array.isArray(view.config.stations) || view.config.stations.length === 0) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'factory-floor config.stations must be a non-empty list.',
            `${path}.config.stations`
          ));
        } else {
          const seenStations = new Set();
          for (let index = 0; index < view.config.stations.length; index += 1) {
            const station = view.config.stations[index];
            validateStringField(station, `${path}.config.stations[${index}]`, true, errors);
            if (seenStations.has(station)) {
              errors.push(createError(
                ERROR_CODES.unknownOrDuplicateKey,
                'factory-floor config.stations values must be unique.',
                `${path}.config.stations[${index}]`
              ));
            }
            seenStations.add(station);
            if (typeof station === 'string' && !FACTORY_FLOOR_STATION_VALUES.includes(station)) {
              errors.push(createError(
                ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
                'factory-floor config.stations must use canonical station values.',
                `${path}.config.stations[${index}]`
              ));
            }
          }
        }
      }
      if (view.config.labels !== undefined) {
        if (!PLURAL_LABEL_ELEMENTS.includes(String(view.element))) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `config.labels is supported only for the ${PLURAL_LABEL_ELEMENTS.join(', ')} element.`,
            `${path}.config.labels`
          ));
        } else {
          validatePluralLabels(view.config.labels, getValueNodeByKey(getValueNodeByKey(viewNode, 'config'), 'labels'), `${path}.config.labels`, errors);
        }
      }
      if (view.config.animate !== undefined) {
        if (view.element !== 'factory-floor') {
          errors.push(createError(
            ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
            'config.animate is supported only for the factory-floor element.',
            `${path}.config.animate`
          ));
        }
        validateStringField(view.config.animate, `${path}.config.animate`, false, errors);
        if (typeof view.config.animate === 'string' && !VIEW_ELEMENT_ANIMATION_VALUES.includes(view.config.animate)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'config.animate must use one canonical element animation value.',
            `${path}.config.animate`
          ));
        }
      }
      for (const key of ['browser-first-load', 'data-status']) {
        if (view.config[key] !== undefined && (view.element !== 'factory-header' || typeof view.config[key] !== 'boolean')) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `config.${key} must be a boolean on the factory-header element.`,
            `${path}.config.${key}`
          ));
        }
      }
      if (view.config.sources !== undefined) {
        if (view.element !== 'factory-header' && view.element !== 'factory-floor') {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'config.sources is supported only for the factory-header and factory-floor elements.',
            `${path}.config.sources`
          ));
        } else if (!isPlainObject(view.config.sources) || Object.keys(view.config.sources).length === 0) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `${view.element} config.sources must be a non-empty mapping of metric roles to source names.`,
            `${path}.config.sources`
          ));
        } else {
          const allowedRoles = view.element === 'factory-header' ? FACTORY_HEADER_SOURCE_ROLES : FACTORY_FLOOR_SOURCE_ROLES;
          const sourcesNode = getValueNodeByKey(getValueNodeByKey(viewNode, 'config'), 'sources');
          validateObjectKeys(sourcesNode, allowedRoles, `${path}.config.sources`, errors);
          for (const [role, sourceName] of Object.entries(view.config.sources)) {
            validateStringField(sourceName, `${path}.config.sources.${role}`, true, errors);
          }
        }
      }
      const linkButtonConfigKeys = ['label-field', 'label-badge-field', 'link-field', 'icon-field', 'fallback-icon', 'empty-message'];
      for (const key of linkButtonConfigKeys) {
        if (view.config[key] === undefined) continue;
        validateStringField(view.config[key], `${path}.config.${key}`, true, errors);
        if (
          view.element !== 'link-button-list'
          && !(key === 'empty-message' && (view.element === 'measure-history' || view.element === 'markdown'))
        ) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `config.${key} is supported only for the link-button-list element.`,
            `${path}.config.${key}`
          ));
        }
      }
      if (view.config['measure-source'] !== undefined) {
        validateStringField(view.config['measure-source'], `${path}.config.measure-source`, true, errors);
        if (view.element !== 'measure-history') {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'config.measure-source is supported only for the measure-history element.',
            `${path}.config.measure-source`
          ));
        } else if (!['operational-value', 'operational-grader'].includes(String(view.config['measure-source']))) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'measure-history config.measure-source must use a canonical measure source value.',
            `${path}.config.measure-source`
          ));
        }
      }
      for (const key of ['content-field', 'path-field', 'base-link-field']) {
        if (view.config[key] === undefined) continue;
        validateStringField(view.config[key], `${path}.config.${key}`, true, errors);
        if (view.element !== 'markdown') {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `config.${key} is supported only for the markdown element.`,
            `${path}.config.${key}`
          ));
        }
      }
      if (view.element === 'markdown' && view.config['content-field'] === undefined) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'markdown requires config.content-field.',
          `${path}.config.content-field`
        ));
      }
      if (view.element === 'link-button-list') {
        for (const key of ['label-field', 'link-field', 'fallback-icon']) {
          if (view.config[key] === undefined) {
            errors.push(createError(
              ERROR_CODES.missingOrInvalidRequiredField,
              `link-button-list config.${key} is required.`,
              `${path}.config.${key}`
            ));
          }
        }
      }
    }
  } else if (view.element === 'link-button-list' || view.element === 'markdown') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      `${view.element} requires config.`,
      `${path}.config`
    ));
  }

  if (view.list !== undefined) {
    const listPath = `${path}.list`;
    if (!isPlainObject(view.list)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list must be a list widget mapping.', listPath));
    } else {
      validateObjectKeys(getValueNodeByKey(viewNode, 'list'), VIEW_LIST_KEYS, listPath, errors);
      validateStringField(view.list.style, `${listPath}.style`, true, errors);
      if (typeof view.list.style === 'string' && !VIEW_LIST_STYLE_VALUES.includes(view.list.style)) {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, `list.style must be one of ${VIEW_LIST_STYLE_VALUES.join(', ')}.`, `${listPath}.style`));
      }
      if (view.list.layout !== undefined) {
        validateStringField(view.list.layout, `${listPath}.layout`, true, errors);
        if (typeof view.list.layout === 'string' && !VIEW_LIST_LAYOUT_VALUES.includes(view.list.layout)) {
          errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, `list.layout must be one of ${VIEW_LIST_LAYOUT_VALUES.join(', ')}.`, `${listPath}.layout`));
        }
      }
      if (view.list.appearance !== undefined) {
        validateStringField(view.list.appearance, `${listPath}.appearance`, true, errors);
        if (view.list.style !== 'entity-cards') {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list.appearance is supported only for entity-cards lists.', `${listPath}.appearance`));
        } else if (typeof view.list.appearance === 'string' && !VIEW_LIST_APPEARANCE_VALUES.includes(view.list.appearance)) {
          errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, `list.appearance must be one of ${VIEW_LIST_APPEARANCE_VALUES.join(', ')}.`, `${listPath}.appearance`));
        }
      }
      validateStringField(view.list.icon, `${listPath}.icon`, true, errors);
      if (typeof view.list.icon === 'string' && !PAGE_ICON_VALUES.includes(view.list.icon)) {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'list icon must use one canonical Octicon name.', `${listPath}.icon`));
      }
      if (view.list.actions !== undefined && (!Array.isArray(view.list.actions)
        || view.list.actions.length === 0 || new Set(view.list.actions).size !== view.list.actions.length)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list.actions must be a non-empty sequence of unique action references.', `${listPath}.actions`));
      }
      const actionReferences = [
        ...(view.list.action !== undefined ? [{ id: view.list.action, path: `${listPath}.action` }] : []),
        ...(Array.isArray(view.list.actions) ? view.list.actions.map((id, index) => ({ id, path: `${listPath}.actions[${index}]` })) : [])
      ];
      if (view.list['action-context'] !== undefined) {
        const fields = view.list['action-context'];
        if (typeof view.list.action !== 'string' || !Array.isArray(fields) || fields.length === 0
          || fields.some((field) => typeof field !== 'string' || field.length === 0)
          || new Set(fields).size !== fields.length || view['lazy-list'] === true
          || state.declaredCliActions.get(view.list.action)?.selector) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
            'list.action-context requires one primary action, unique context fields, and a complete non-paginated list.',
            `${listPath}.action-context`));
        } else {
          const sourceName = isPlainObject(view.data) && typeof view.data.source === 'string' ? view.data.source : '';
          const available = sourceFieldNames(sourceName);
          const action = state.declaredCliActions.get(view.list.action);
          if (fields.some((field) => !available?.includes(field))
            || (typeof action?.command === 'string' && cliActionTemplateFields(action.command).some((field) => !fields.includes(field)))) {
            errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
              'list.action-context must reference source fields and supply every command template field.',
              `${listPath}.action-context`));
          }
        }
      }
      for (const reference of actionReferences) {
        validateRequiredIdentifier(reference.id, reference.path, 'list action reference', errors);
        const declaredAction = typeof reference.id === 'string'
          ? state.declaredCliActions.get(reference.id)
          : undefined;
        if (typeof reference.id === 'string' && !declaredAction) {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'list action must reference a declared dashboard CLI action.', reference.path));
        } else if (declaredAction?.placement !== 'view') {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'list actions must reference a view-placed dashboard CLI action.', reference.path));
        }
      }
      if (view.list.card !== undefined) {
        validateStringField(view.list.card, `${listPath}.card`, true, errors);
        if (typeof view.list.card === 'string' && !state.declaredCardTemplates.has(view.list.card)) {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'list.card must reference a declared dashboard card template.', `${listPath}.card`));
        }
      }
      if (view.list.style === 'entity-cards' && view.list.card === undefined) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'entity-cards lists must declare a reusable card definition.', `${listPath}.card`));
      } else if (view.list.style !== 'entity-cards' && view.list.card !== undefined) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list.card is supported only for entity-cards lists.', `${listPath}.card`));
      }
      validateListDrill(view.list.drill, getValueNodeByKey(getValueNodeByKey(viewNode, 'list'), 'drill'), listPath, view.list.style, errors);
      validateListViewAll(view.list['view-all'], getValueNodeByKey(getValueNodeByKey(viewNode, 'list'), 'view-all'), listPath, view.list.style, errors);
    }
    if (view.mark !== 'list') {
      errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'list is allowed only when mark is "list".', listPath));
    }

  } else if (view.mark === 'list') {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list views must declare a list widget mapping.', `${path}.list`));
  }

  if (view['card-drill'] !== undefined) {
    const drillPath = `${path}.card-drill`;
    validateListDrill(view['card-drill'], getValueNodeByKey(viewNode, 'card-drill'), path, 'entity-cards', errors);
    if (view.mark !== 'table' || view['lazy-list'] !== true) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'card-drill is allowed only on lazy table views.',
        drillPath
      ));
    }
  }

  if (view.chart !== undefined) {
    validateStringField(view.chart, `${path}.chart`, true, errors);
    if (typeof view.chart === 'string' && !VIEW_CHART_VALUES.includes(view.chart)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'chart must use one canonical chart widget value.',
        `${path}.chart`
      ));
    }
    if (view.mark !== 'chart') {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'chart is allowed only when mark is "chart".',
        `${path}.chart`
      ));
    }
  }

  if (view.metric !== undefined) {
      const metricPath = `${path}.metric`;
      if (!isPlainObject(view.metric)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'metric must be a metric widget mapping.',
          metricPath
        ));
      } else {
        validateObjectKeys(getValueNodeByKey(viewNode, 'metric'), VIEW_METRIC_KEYS, metricPath, errors);
        validateStringField(view.metric.style, `${metricPath}.style`, true, errors);
        if (typeof view.metric.style === 'string' && !VIEW_METRIC_STYLE_VALUES.includes(view.metric.style)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'metric style must use one canonical metric widget value.',
            `${metricPath}.style`
          ));
        }
        validateStringField(view.metric.animate, `${metricPath}.animate`, false, errors);
        if (typeof view.metric.animate === 'string' && !VIEW_METRIC_ANIMATION_VALUES.includes(view.metric.animate)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'metric animate must use one canonical metric animation value.',
            `${metricPath}.animate`
          ));
        }
        if (view.metric.animate !== undefined && view.metric.style !== 'card') {
          errors.push(createError(
            ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
            'metric animate is supported only for card metric widgets.',
            `${metricPath}.animate`
          ));
        }
        validateStringField(view.metric.icon, `${metricPath}.icon`, true, errors);
        if (typeof view.metric.icon === 'string' && !PAGE_ICON_VALUES.includes(view.metric.icon)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'metric icon must use one canonical Octicon name.',
            `${metricPath}.icon`
          ));
        }
        validateStringField(view.metric.tone, `${metricPath}.tone`, true, errors);
        if (typeof view.metric.tone === 'string' && !VIEW_METRIC_TONE_VALUES.includes(view.metric.tone)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'metric tone must use one canonical metric tone value.',
            `${metricPath}.tone`
          ));
        }
        validateRequiredIdentifier(
          view.metric['navigation-page'],
          `${metricPath}.navigation-page`,
          'metric navigation page',
          errors
        );
      }

      if (view.mark !== 'metric') {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'metric is allowed only when mark is "metric".',
          metricPath
        ));
      }
  }

  if (view.layout !== undefined) {
    validateStringField(view.layout, `${path}.layout`, true, errors);
    if (typeof view.layout === 'string' && !VIEW_LAYOUT_VALUES.includes(view.layout)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'layout must use one canonical structural layout hint.',
        `${path}.layout`
      ));
    }
  }

  /** @type {string | null} */
  let sourceName = null;
  if (view.mark === 'callout') {
    if (view.data !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'callout views must not declare data.',
        `${path}.data`
      ));
    }
  } else if (!isPlainObject(view.data)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'data must be a mapping.',
      `${path}.data`
    ));
  } else {
    const dataNode = getValueNodeByKey(viewNode, 'data');
    validateObjectKeys(dataNode, VIEW_DATA_KEYS, `${path}.data`, errors);
    if (view.data['query-context'] !== undefined && typeof view.data['query-context'] !== 'boolean') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'data.query-context must be a boolean.',
        `${path}.data.query-context`
      ));
    }
    if (view.mark === 'element') {
      if (!(typeof view.element === 'string' && ['query-editor', 'command-terminal', 'storage-info', 'environment-defaults', 'prompt-wizard'].includes(view.element) && Array.isArray(view.data.sources) && view.data.sources.length === 0)) {
        validateSourceSequence(view.data.sources, `${path}.data.sources`, errors);
      }
      if (view.data.source !== undefined) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'element views must use data.sources instead of data.source.',
          `${path}.data.source`
        ));
      }
      validateStringField(view.data['route-field'], `${path}.data.route-field`, false, errors);
      if (typeof view.data['route-field'] === 'string' && Array.isArray(view.data.sources)) {
        for (const source of view.data.sources) {
          if (typeof source === 'string' && !sourceFieldNames(source)?.includes(view.data['route-field'])) {
            errors.push(createError(
              ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
              'route-field must name a field declared by every data.sources entry.',
              `${path}.data.route-field`
            ));
          }
        }
      }
      for (const key of ['order-by', 'source-metadata']) {
        if (view.data[key] !== undefined) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `element views must not declare data.${key}.`,
            `${path}.data.${key}`
          ));
        }
      }
      validateViewDataArguments(
        view.data.arguments,
        getValueNodeByKey(dataNode, 'arguments'),
        `${path}.data.arguments`,
        Array.isArray(view.data.sources) ? view.data.sources.filter((name) => typeof name === 'string') : null,
        errors
      );
    } else {
      validateSource(view.data.source, `${path}.data.source`, errors);
      if (view.data['partial-source'] !== undefined) {
        validateSource(view.data['partial-source'], `${path}.data.partial-source`, errors);
        if (view.mark !== 'table' || view.data['partial-source'] === view.data.source) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'partial-source requires a table view and a distinct source.',
            `${path}.data.partial-source`
          ));
        }
      }
      if (typeof view.data.source === 'string'
          && (QUERY_SOURCE_VALUES.includes(view.data.source) || state.declaredQueries.has(view.data.source))) {
        sourceName = view.data.source;
      }
      if (view.data.sources !== undefined) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'metric, table, list, and chart views must use data.source instead of data.sources.',
          `${path}.data.sources`
        ));
      }
      if (view.data['route-field'] !== undefined) {
        validateStringField(view.data['route-field'], `${path}.data.route-field`, true, errors);
        if (
          sourceName
          && typeof view.data['route-field'] === 'string'
          && !sourceFieldNames(sourceName)?.includes(view.data['route-field'])
        ) {
          errors.push(createError(
            ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
            'route-field must name a field declared by data.source.',
            `${path}.data.route-field`
          ));
        }
      }
      validateViewDataArguments(
        view.data.arguments,
        getValueNodeByKey(dataNode, 'arguments'),
        `${path}.data.arguments`,
        sourceName,
        errors
      );
    }
    validateContext(dataNode, view.data, `${path}.data`, errors);
  }

  validateSemanticFieldLiterals(view.data, `${path}.data`, errors);
  validateDatasetMetadata(getValueNodeByKey(viewNode, 'data'), view.data, `${path}.data`, errors);
  if (
    ['heatmap', 'horizontal-bar'].includes(String(view.chart))
    && (!isPlainObject(view.data) || !Number.isInteger(view.data.limit) || Number(view.data.limit) > 100)
  ) {
    const chartName = view.chart === 'horizontal-bar' ? 'horizontal-bar' : 'heatmap';
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      `${chartName} charts must declare data.limit no greater than 100.`,
      `${path}.data.limit`
    ));
  }
  if (view.layer !== undefined) {
    if (view.mark !== 'chart') {
      errors.push(createError(ERROR_CODES.incompatibleMarkChannelTypeOrTimeUnit, 'layer is allowed only on chart views.', `${path}.layer`));
    }
    try {
      const layers = resolveChartLayers(view);
      /** @param {Record<string, unknown>} specification @param {unknown} node @param {string} nodePath @param {boolean} [root] */
      const validateLayerKeys = (specification, node, nodePath, root = false) => {
        const encodingNode = getValueNodeByKey(node, 'encoding');
        validateObjectKeys(encodingNode, root ? ['x', 'y', 'color', 'href', 'facet', 'row', 'column'] : ['x', 'y', 'color', 'href'], `${nodePath}.encoding`, errors);
        for (const channel of ['x', 'y', 'color', 'href']) {
          validateObjectKeys(getValueNodeByKey(encodingNode, channel), FIELD_DEFINITION_KEYS, `${nodePath}.encoding.${channel}`, errors);
        }
        if (!Array.isArray(specification.layer)) return;
        specification.layer.forEach((child, index) => {
          const childNode = getSequenceItemNode(getValueNodeByKey(node, 'layer'), index);
          const childPath = `${nodePath}.layer[${index}]`;
          validateObjectKeys(childNode, ['chart', 'encoding', 'layer'], childPath, errors);
          validateLayerKeys(child, childNode, childPath);
        });
      };
      validateLayerKeys(view, viewNode, path, true);
      validateObjectKeys(getValueNodeByKey(viewNode, 'resolve'), ['scale'], `${path}.resolve`, errors);
      validateObjectKeys(getValueNodeByKey(getValueNodeByKey(viewNode, 'resolve'), 'scale'), ['y'], `${path}.resolve.scale`, errors);
      for (const layer of layers) {
        validateEncoding(null, layer.encoding, 'chart', layer.chart,
          sourceName, view.data, `${path}.${layer.path}`, errors, undefined, true);
      }
    } catch (error) {
      if (!(error instanceof ChartLayerError)) throw error;
      errors.push(createError(ERROR_CODES.incompatibleMarkChannelTypeOrTimeUnit, error.message, `${path}.${error.path}`));
    }
  } else {
    if (view.resolve !== undefined) {
      errors.push(createError(ERROR_CODES.incompatibleMarkChannelTypeOrTimeUnit, 'resolve requires layer.', `${path}.resolve`));
    }
    validateEncoding(getValueNodeByKey(viewNode, 'encoding'), view.encoding, view.mark, view.chart, sourceName, view.data, path, errors, view.facet);
  }
  validateViewFilterBar(view, viewNode, path, sourceName, errors);
  validateTableActions(
    view.encoding,
    getValueNodeByKey(viewNode, 'encoding'),
    view.mark,
    sourceName,
    `${path}.encoding.actions`,
    errors
  );
  debugValidatorViews({
    operation: 'validate-view',
    mark: typeof view.mark === 'string' ? view.mark : null,
    status: errors.length === errorCountBeforeView ? 'ok' : 'invalid'
  });
}

/**
 * @param {Record<string, unknown>} view
 * @param {unknown} viewNode
 * @param {string} path
 * @param {string | null} sourceName
 * @param {ValidationError[]} errors
 */
export function validateViewFilterBar(view, viewNode, path, sourceName, errors) {
  validateViewFilterBarContract(view, viewNode, path, sourceName, errors, {
    isPlainObject, getValueNodeByKey, getSequenceItemNode, getMappingItems,
    validateObjectKeys, validateRequiredIdentifier, validateStringField,
    validateSource, sourceFieldNames, createError
  });
}

/**
 * @param {unknown} args
 * @param {unknown} argsNode
 * @param {string} path
 * @param {string | string[] | null} sourceName
 * @param {ValidationError[]} errors
 */
export function validateViewDataArguments(args, argsNode, path, sourceName, errors) {
  if (args === undefined) return;
  if (!Array.isArray(args) || args.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'data.arguments must be a non-empty sequence.', path));
    return;
  }
  const names = new Set();
  const sourceNames = Array.isArray(sourceName) ? sourceName : sourceName ? [sourceName] : [];
  const sourceFields = sourceNames.map((name) => sourceFieldNames(name));
  const fields = sourceFields.length > 0 && sourceFields.every(Array.isArray)
    ? sourceFields.reduce((shared, candidate) => shared.filter((field) => candidate.includes(field)), [...sourceFields[0]])
    : null;
  for (const [index, argument] of args.entries()) {
    const argumentPath = `${path}[${index}]`;
    if (!isPlainObject(argument)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'data argument must be a mapping.', argumentPath));
      continue;
    }
    validateObjectKeys(getSequenceItemNode(argsNode, index), VIEW_DATA_ARGUMENT_KEYS, argumentPath, errors);
    validateRequiredIdentifier(argument.name, `${argumentPath}.name`, 'data argument name', errors);
    validateRequiredIdentifier(argument.field, `${argumentPath}.field`, 'data argument field', errors);
    if (typeof argument.name === 'string' && names.has(argument.name)) {
      errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, 'data argument names must be unique.', `${argumentPath}.name`));
    }
    names.add(argument.name);
    if (fields && typeof argument.field === 'string' && !fields.includes(argument.field)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        `data argument field must be declared by ${sourceNames.length > 1 ? 'every data.sources entry' : 'data.source'}.`,
        `${argumentPath}.field`
      ));
    }
  }
}
































/**
 * @param {unknown} drill
 * @param {unknown} drillNode
 * @param {string} listPath
 * @param {unknown} style
 * @param {ValidationError[]} errors
 */
export function validateListDrill(drill, drillNode, listPath, style, errors) {
  const path = `${listPath}.drill`;
  if (drill === undefined) return;
  if (style !== 'entity-cards') {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list.drill is supported only for entity-cards lists.', path));
    return;
  }
  if (!isPlainObject(drill)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list.drill must be a mapping.', path));
    return;
  }
  validateObjectKeys(drillNode, VIEW_LIST_DRILL_KEYS, path, errors);
  validateStringField(drill.type, `${path}.type`, true, errors);
  if (typeof drill.type === 'string' && !VIEW_LIST_DRILL_TYPE_VALUES.includes(drill.type)) {
    errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, `list.drill.type must be one of ${VIEW_LIST_DRILL_TYPE_VALUES.join(', ')}.`, `${path}.type`));
  }
  if (drill.type === 'external') {
    validateRequiredIdentifier(drill.field, `${path}.field`, 'external drill field', errors);
    if (drill.page !== undefined || drill.query !== undefined || drill['title-field'] !== undefined || drill.arguments !== undefined) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'external drill behavior supports only field.', path));
    }
    return;
  }
  if (drill.type !== 'query') return;
  validateRequiredIdentifier(drill.page, `${path}.page`, 'query drill page', errors);
  validateRequiredIdentifier(drill.query, `${path}.query`, 'query drill query', errors);
  validateRequiredIdentifier(drill['title-field'], `${path}.title-field`, 'query drill title field', errors);
  if (!Array.isArray(drill.arguments) || drill.arguments.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'query drill behavior requires non-empty arguments.', `${path}.arguments`));
    return;
  }
  const names = new Set();
  const argumentsNode = getValueNodeByKey(drillNode, 'arguments');
  for (const [index, argument] of drill.arguments.entries()) {
    const argumentPath = `${path}.arguments[${index}]`;
    if (!isPlainObject(argument)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'query drill argument must be a mapping.', argumentPath));
      continue;
    }
    validateObjectKeys(getSequenceItemNode(argumentsNode, index), VIEW_LIST_DRILL_ARGUMENT_KEYS, argumentPath, errors);
    validateRequiredIdentifier(argument.name, `${argumentPath}.name`, 'query drill argument name', errors);
    validateRequiredIdentifier(argument.field, `${argumentPath}.field`, 'query drill argument field', errors);
    if (typeof argument.name === 'string' && names.has(argument.name)) {
      errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, 'query drill argument names must be unique.', `${argumentPath}.name`));
    }
    names.add(argument.name);
  }
}

/**
 * @param {Record<string, any>} drill
 * @param {string} drillPath
 * @param {Record<string, any>} dashboard
 * @param {Set<string>} pageIds
 * @param {Map<string, string[] | undefined>} declaredQueries
 * @param {string[] | null | undefined} fields
 * @param {ValidationError[]} errors
 */
export function validateQueryDrillReferences(drill, drillPath, dashboard, pageIds, declaredQueries, fields, errors) {
  if (drill.type !== 'query') return;
  if (typeof drill.page === 'string' && IDENTIFIER_PATTERN.test(drill.page) && !pageIds.has(drill.page)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'query drill page must reference a declared dashboard page id.',
      `${drillPath}.page`
    ));
  }
  if (typeof drill.query === 'string' && IDENTIFIER_PATTERN.test(drill.query) && !declaredQueries.has(drill.query)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'query drill query must reference a declared dashboard query.',
      `${drillPath}.query`
    ));
  }
  for (const [field, fieldPath] of [
    [drill['title-field'], `${drillPath}.title-field`],
    ...(Array.isArray(drill.arguments)
      ? drill.arguments.map((argument, argumentIndex) => [
          isPlainObject(argument) ? argument.field : undefined,
          `${drillPath}.arguments[${argumentIndex}].field`
        ])
      : [])
  ]) {
    if (fields && typeof field === 'string' && !fields.includes(field)) {
      errors.push(createError(
        ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
        'query drill field must be declared by the current card or view data source.',
        String(fieldPath)
      ));
    }
  }
  const targetPage = resolveReusablePageViews(/** @type {unknown[]} */ (dashboard.pages)
    .find((candidate) => isPlainObject(candidate) && candidate.id === drill.page));
  const targetViews = isPlainObject(targetPage)
    ? targetPage.kind === 'built-in' && isPlainObject(targetPage.definition)
      ? targetPage.definition.views
      : targetPage.views
    : undefined;
  const argumentNames = new Set(Array.isArray(drill.arguments)
    ? drill.arguments.flatMap((argument) => (
        isPlainObject(argument) && typeof argument.name === 'string' ? [argument.name] : []
      ))
    : []);
  const destinationBindsQuery = Array.isArray(targetViews) && targetViews.some((targetView) => {
    if (!isPlainObject(targetView) || !isPlainObject(targetView.data)) return false;
    const bindsQuery = targetView.data.source === drill.query
      || (Array.isArray(targetView.data.sources) && targetView.data.sources.includes(drill.query));
    if (!bindsQuery) return false;
    const boundNames = new Set(Array.isArray(targetView.data.arguments)
      ? targetView.data.arguments.flatMap((argument) => (
          isPlainObject(argument) && typeof argument.name === 'string' ? [argument.name] : []
        ))
      : []);
    return [...argumentNames].every((name) => boundNames.has(name));
  });
  if (
    typeof drill.page === 'string'
    && pageIds.has(drill.page)
    && typeof drill.query === 'string'
    && declaredQueries.has(drill.query)
    && !destinationBindsQuery
  ) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'query drill destination must bind the declared query and every drill argument.',
      drillPath
    ));
  }
}

/**
 * @param {unknown} viewAll
 * @param {unknown} viewAllNode
 * @param {string} listPath
 * @param {unknown} style
 * @param {ValidationError[]} errors
 */
function validateListViewAll(viewAll, viewAllNode, listPath, style, errors) {
  const path = `${listPath}.view-all`;
  if (viewAll === undefined) return;
  if (style !== 'entity-cards') {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list.view-all is supported only for entity-cards lists.', path));
    return;
  }
  if (!isPlainObject(viewAll)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'list.view-all must be a mapping.', path));
    return;
  }
  validateObjectKeys(viewAllNode, VIEW_LIST_VIEW_ALL_KEYS, path, errors);
  validateRequiredIdentifier(viewAll.page, `${path}.page`, 'list view-all page', errors);
  if (viewAll.label !== undefined) {
    validateStringField(viewAll.label, `${path}.label`, true, errors);
  }
}

/**
 * @param {unknown} encoding
 * @param {unknown} encodingNode
 * @param {unknown} mark
 * @param {string | null} sourceName
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateTableActions(encoding, encodingNode, mark, sourceName, path, errors) {
  if (!isPlainObject(encoding) || encoding.actions === undefined) return;
  if (!['list', 'table'].includes(String(mark))) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'actions is allowed only when mark is "list" or "table".', path));
    return;
  }
  if (!Array.isArray(encoding.actions) || encoding.actions.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'actions must be a non-empty sequence.', path));
    return;
  }
  encoding.actions.forEach((action, index) => {
    const actionPath = `${path}[${index}]`;
    const actionNode = getSequenceItemNode(getValueNodeByKey(encodingNode, 'actions'), index);
    if (!isPlainObject(action)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'action must be a mapping.', actionPath));
      return;
    }
    validateObjectKeys(actionNode, TABLE_ACTION_KEYS, actionPath, errors);
    validateActionLevel(action.level, `${actionPath}.level`, errors);
    validateOptionalStringField(action.verb, `${actionPath}.verb`, errors);
    validateStringField(action.presentation, `${actionPath}.presentation`, true, errors);
    if (typeof action.presentation === 'string' && !TABLE_ACTION_PRESENTATION_VALUES.includes(action.presentation)) {
      errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'action presentation must be copy-prompt, cli-action, or external-link.', `${actionPath}.presentation`));
    }
    if (action.presentation === 'cli-action') {
      validateRequiredIdentifier(action.action, `${actionPath}.action`, 'CLI action reference', errors);
      const declaredAction = typeof action.action === 'string'
        ? state.declaredCliActions.get(action.action)
        : undefined;
      if (typeof action.action === 'string' && !declaredAction) {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'cli-action must reference a declared dashboard CLI action.', `${actionPath}.action`));
      } else if (declaredAction?.placement !== 'row') {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'cli-action must reference a row-placed dashboard CLI action.', `${actionPath}.action`));
      } else if (declaredAction.command === 'gh agent-task create --from-file -') {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'agent-task actions must use copy-prompt presentation.', `${actionPath}.action`));
      }
      if (action.level === 'explore' && declaredAction && !isReadOnlyCliAction(declaredAction)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
          'Explore CLI actions must use a supported read-only command.', `${actionPath}.action`));
      }
      if (action.intent !== undefined) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'cli-action table actions must not declare intent.', `${actionPath}.intent`));
      }
    } else {
      validateStringField(action.intent, `${actionPath}.intent`, true, errors);
      if (action.presentation === 'copy-prompt' && action.action !== undefined) {
        validateRequiredIdentifier(action.action, `${actionPath}.action`, 'Prompt CLI action reference', errors);
        const declaredAction = typeof action.action === 'string'
          ? state.declaredCliActions.get(action.action)
          : undefined;
        if (typeof action.action === 'string' && !declaredAction) {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'copy-prompt must reference a declared dashboard CLI action.', `${actionPath}.action`));
        } else if (declaredAction?.placement !== 'row') {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'copy-prompt must reference a row-placed dashboard CLI action.', `${actionPath}.action`));
        } else if (declaredAction.command !== 'gh agent-task create --from-file -') {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'copy-prompt CLI action must use "gh agent-task create --from-file -".', `${actionPath}.action`));
        }
      } else if (action.action !== undefined) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'external-link table actions must not declare action.', `${actionPath}.action`));
      }
    }
    if (action.presentation === 'external-link' && (
      !Array.isArray(action.context)
      || action.context.length !== 1
    )) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'external-link action context must contain exactly one link field.',
        `${actionPath}.context`
      ));
    }
    if (action.icon !== undefined) validateStringField(action.icon, `${actionPath}.icon`, true, errors);
    if (typeof action.icon === 'string' && !PAGE_ICON_VALUES.includes(action.icon)) {
      errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'action icon must use one canonical icon value.', `${actionPath}.icon`));
    }
    if (action.label !== undefined || action.level === 'operate'
      || action.level === undefined && action.presentation === 'cli-action') {
      validateStringField(action.label, `${actionPath}.label`, true, errors);
    }
    if (!Array.isArray(action.context) || action.context.length === 0) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'action context must be a non-empty sequence of source fields.', `${actionPath}.context`));
    } else {
      const contextFields = new Set();
      action.context.forEach((field, fieldIndex) => {
        const fieldPath = `${actionPath}.context[${fieldIndex}]`;
        validateStringField(field, fieldPath, true, errors);
        if (typeof field !== 'string') return;
        if (contextFields.has(field)) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'action context fields must be unique.', fieldPath));
        }
        contextFields.add(field);
        if (sourceName && !sourceFieldNames(sourceName)?.includes(field)) {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'action context field must be declared by data.source.', fieldPath));
        }
        if (action.presentation === 'external-link' && !LINK_FIELD_NAMES.includes(field)) {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'external-link action context must reference a link field.', fieldPath));
        }
      });
      if (action.presentation === 'cli-action' && typeof action.action === 'string') {
        const command = state.declaredCliActions.get(action.action)?.command;
        const templateFields = typeof command === 'string' ? cliActionTemplateFields(command) : [];
        const contextFieldNames = action.context.filter((field) => typeof field === 'string');
        if (typeof command !== 'string' || cliActionTemplateFields(command, { includeContext: true }).length === 0
          || templateFields.some((field) => !contextFieldNames.includes(field))) {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'cli-action context must include every command template field.', `${actionPath}.context`));
        }
      }
    }
    if (action.when === undefined) return;
    if (!isPlainObject(action.when)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'action when must be a mapping.', `${actionPath}.when`));
      return;
    }
    validateObjectKeys(getValueNodeByKey(actionNode, 'when'), TABLE_ACTION_WHEN_KEYS, `${actionPath}.when`, errors);
    validateStringField(action.when.field, `${actionPath}.when.field`, true, errors);
    if (typeof action.when.field === 'string' && sourceName && !sourceFieldNames(sourceName)?.includes(action.when.field)) {
      errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'action when field must be declared by data.source.', `${actionPath}.when.field`));
    }
    if (!Object.hasOwn(action.when, 'equals') || ['object', 'function', 'symbol'].includes(typeof action.when.equals)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'action when equals must be a scalar.', `${actionPath}.when.equals`));
    }
  });
}

/**
 * @param {unknown} callout
 * @param {unknown} calloutNode
 * @param {unknown} mark
 * @param {unknown} title
 * @param {unknown} description
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateCallout(callout, calloutNode, mark, title, description, path, errors) {
  if (mark !== 'callout') {
    if (callout !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'callout is allowed only when mark is "callout".',
        path
      ));
    }
    return;
  }
  if (!isPlainObject(callout)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'callout views must contain a callout mapping.',
      path
    ));
    return;
  }
  validateObjectKeys(calloutNode, CALLOUT_KEYS, path, errors);
  validateStringField(callout.label, `${path}.label`, true, errors);
  validateStringField(callout.icon, `${path}.icon`, true, errors);
  if (typeof callout.icon === 'string' && !PAGE_ICON_VALUES.includes(callout.icon)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'callout icon must use one canonical icon value.',
      `${path}.icon`
    ));
  }
  validateStringField(title, path.replace(/\.callout$/, '.title'), true, errors);
  validateStringField(description, path.replace(/\.callout$/, '.description'), true, errors);
}

/**
 * @param {unknown} titleLink
 * @param {unknown} titleLinkNode
 * @param {unknown} mark
 * @param {unknown} data
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateTitleLink(titleLink, titleLinkNode, mark, data, path, errors) {
  if (titleLink === undefined) return;
  if (!isPlainObject(titleLink)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'title-link must be a mapping.', path));
    return;
  }
  validateObjectKeys(titleLinkNode, VIEW_TITLE_LINK_KEYS, path, errors);
  validateStringField(titleLink['href-field'], `${path}.href-field`, true, errors);
  validateStringField(titleLink['identifier-field'], `${path}.identifier-field`, true, errors);
  if (mark !== 'element') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'title-link is allowed only when mark is "element".',
      path
    ));
    return;
  }
  if (!isPlainObject(data) || !Array.isArray(data.sources)) return;
  const hrefField = titleLink['href-field'];
  const identifierField = titleLink['identifier-field'];
  if (typeof hrefField === 'string' && !LINK_FIELD_NAMES.includes(hrefField)) {
    errors.push(createError(
      ERROR_CODES.invalidLinkReference,
      'title-link href-field must name a relation-specific link field.',
      `${path}.href-field`
    ));
  }
  if (typeof identifierField === 'string' && LINK_FIELD_NAMES.includes(identifierField)) {
    errors.push(createError(
      ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
      'title-link identifier-field must name a scalar source field.',
      `${path}.identifier-field`
    ));
  }
  if (typeof hrefField !== 'string' || typeof identifierField !== 'string') return;
  const hasCompatibleSource = data.sources.some((sourceName) => (
    typeof sourceName === 'string'
    && sourceFieldNames(sourceName)?.includes(hrefField)
    && sourceFieldNames(sourceName)?.includes(identifierField)
  ));
  if (!hasCompatibleSource) {
    errors.push(createError(
      ERROR_CODES.invalidLinkReference,
      'title-link fields must be declared by the same selected source.',
      path
    ));
  }
}

/**
 * @param {unknown[]} views
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateProgressiveDisclosure(views, path, errors) {
  const validViews = views.filter(isPlainObject);
  for (const [index, view] of views.entries()) {
    if (isPlainObject(view)) {
      validateDisclosureValue(view.disclosure, `${path}[${index}].disclosure`, errors);
      if (
        view.disclosure === 'supplemental'
        && view.mark === 'table'
        && Object.hasOwn(view, 'title')
      ) {
        errors.push(createError(
          ERROR_CODES.invalidProgressiveDisclosureConfiguration,
          'A supplemental table uses its derived view name as the disclosure label and must not declare a title.',
          `${path}[${index}].title`
        ));
      }
    }
  }

  if (!validViews.some((view) => Object.hasOwn(view, 'disclosure'))) {
    return;
  }

  const essentialCount = validViews.filter((view) => (
    view.disclosure === undefined || view.disclosure === 'essential'
  )).length;
  if (essentialCount < 1 || essentialCount > MAX_ESSENTIAL_VIEWS_PER_PAGE) {
    errors.push(createError(
      ERROR_CODES.invalidProgressiveDisclosureConfiguration,
      `A page must expose between 1 and ${MAX_ESSENTIAL_VIEWS_PER_PAGE} essential views initially; found ${essentialCount}. Mark non-essential views as "supplemental".`,
      path
    ));
  }
  debugValidatorViews({
    operation: 'validate-progressive-disclosure',
    essentialCount,
    viewCount: validViews.length,
    status: essentialCount < 1 || essentialCount > MAX_ESSENTIAL_VIEWS_PER_PAGE ? 'invalid' : 'ok'
  });
}

/**
 * @param {unknown[]} views
 * @param {string} viewsPath
 * @param {ValidationError[]} errors
 * @param {string | undefined} pageId
 */
export function validateGraphicalLayout(views, viewsPath, errors, pageId) {
  if (pageId !== undefined && GRAPHICAL_LAYOUT_EXEMPT_PAGE_IDS.has(pageId)) return;
  const validViews = views.filter(isPlainObject);
  const unlockedTables = validViews.filter((view) => view.locked !== true && view.mark === 'table');
  const defaultOpenTables = unlockedTables.filter((view) => view.disclosure !== 'supplemental');
  for (const table of defaultOpenTables.slice(1)) {
    const index = views.indexOf(table);
    errors.push(createError(
      ERROR_CODES.invalidProgressiveDisclosureConfiguration,
      'Only one table may be open by default on a page. Mark additional tables as "supplemental".',
      `${viewsPath}[${index}].disclosure`
    ));
  }
  if (unlockedTables.length === 1 && defaultOpenTables.length === 0) {
    const index = views.indexOf(unlockedTables[0]);
    errors.push(createError(
      ERROR_CODES.invalidProgressiveDisclosureConfiguration,
      '"supplemental" disclosure is reserved for additional tables beyond a page\u2019s first; a page\u2019s only table must not hide its content behind disclosure.',
      `${viewsPath}[${index}].disclosure`
    ));
  }

  for (const [index, view] of views.entries()) {
    if (!isPlainObject(view) || view.locked === true || view.mark === 'chart' || !Object.hasOwn(view, 'views')) continue;
    errors.push(createError(
      ERROR_CODES.invalidGraphicalNesting,
      'Views are top-level boxes and must not contain nested views.',
      `${viewsPath}[${index}].views`
    ));
  }
}

/**
 * @param {unknown} disclosure
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateDisclosureValue(disclosure, path, errors) {
  if (disclosure === undefined) {
    return;
  }
  if (typeof disclosure !== 'string' || !VIEW_DISCLOSURE_VALUES.includes(disclosure)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'disclosure must be exactly "essential" or "supplemental".',
      path
    ));
  }
}





























































/**
 * @param {unknown} form
 * @param {unknown} formNode
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validatePageForm(form, formNode, path, errors) {
  if (form === undefined) return;
  if (!isPlainObject(form)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'page form must be a mapping.', path));
    return;
  }
  validateObjectKeys(formNode, PAGE_FORM_KEYS, path, errors);
  validateOptionalStringField(form.title, `${path}.title`, errors);
  validateOptionalStringField(form.description, `${path}.description`, errors);
  if (form.update !== undefined) {
    if (!isPlainObject(form.update)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'form update must be a mapping.', `${path}.update`));
    } else {
      validateObjectKeys(getValueNodeByKey(formNode, 'update'), PAGE_FORM_UPDATE_KEYS, `${path}.update`, errors);
      if (typeof form.update.strategy !== 'string' || !PAGE_FORM_UPDATE_STRATEGY_VALUES.includes(form.update.strategy)) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          `form update strategy must be one of ${PAGE_FORM_UPDATE_STRATEGY_VALUES.join(', ')}.`,
          `${path}.update.strategy`
        ));
      }
      const delay = form.update['delay-ms'];
      if (typeof delay !== 'number' || !Number.isInteger(delay) || delay < PAGE_FORM_MIN_DELAY_MS || delay > PAGE_FORM_MAX_DELAY_MS) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          `form update delay-ms must be an integer from ${PAGE_FORM_MIN_DELAY_MS} to ${PAGE_FORM_MAX_DELAY_MS}.`,
          `${path}.update.delay-ms`
        ));
      }
    }
  }
  if (!Array.isArray(form.fields) || form.fields.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'page form fields must be a non-empty sequence.', `${path}.fields`));
    return;
  }
  const ids = new Set();
  for (const [index, field] of form.fields.entries()) {
    const fieldPath = `${path}.fields[${index}]`;
    if (!isPlainObject(field)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'form field must be a mapping.', fieldPath));
      continue;
    }
    validateObjectKeys(
      getSequenceItemNode(getValueNodeByKey(formNode, 'fields'), index),
      PAGE_FORM_FIELD_KEYS,
      fieldPath,
      errors
    );
    validateRequiredIdentifier(field.id, `${fieldPath}.id`, 'form field id', errors);
    validateStringField(field.label, `${fieldPath}.label`, true, errors);
    validateOptionalStringField(field.description, `${fieldPath}.description`, errors);
    if (typeof field.id === 'string') {
      const fieldId = field.id;
      if (ids.has(fieldId)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'form field ids must be unique.', `${fieldPath}.id`));
      }
      ids.add(fieldId);
      const matchingTypes = [...state.declaredQueryParameters.values()]
        .flatMap((parameters) => parameters.has(fieldId) ? [parameters.get(fieldId)] : [])
        .filter((type) => typeof type === 'string');
      if (matchingTypes.length === 0) {
        errors.push(createError(ERROR_CODES.unusedQuery, `form field "${fieldId}" is not declared by any dashboard query.`, `${fieldPath}.id`));
      }
      const expectedType = field.control === 'slider' ? 'number'
        : field.control === 'checkbox' ? 'boolean'
          : typeof field.default;
      if (matchingTypes.some((type) => type !== expectedType)) {
        errors.push(createError(
          ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
          `form field "${fieldId}" type must match every query parameter with that name.`,
          fieldPath
        ));
      }
    }
    if (typeof field.control !== 'string' || !PAGE_FORM_CONTROL_VALUES.includes(field.control)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        `form field control must be one of ${PAGE_FORM_CONTROL_VALUES.join(', ')}.`,
        `${fieldPath}.control`
      ));
      continue;
    }
    if (field.control === 'slider') {
      const defaultValue = typeof field.default === 'number' ? field.default : NaN;
      const min = typeof field.min === 'number' ? field.min : NaN;
      const max = typeof field.max === 'number' ? field.max : NaN;
      const step = typeof field.step === 'number' ? field.step : NaN;
      if (![defaultValue, min, max, step].every(Number.isFinite)
          || min >= max || step <= 0 || defaultValue < min || defaultValue > max) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'slider requires finite min, max, step, and default values with min < max, step > 0, and default inside the range.',
          fieldPath
        ));
      }
    } else if (field.control === 'checkbox') {
      if (typeof field.default !== 'boolean') {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'checkbox default must be Boolean.', `${fieldPath}.default`));
      }
    } else if (field.control === 'radio') {
      validateRadioField(field, getSequenceItemNode(getValueNodeByKey(formNode, 'fields'), index), fieldPath, errors);
    } else if (field.control === 'text') {
      if (typeof field.default !== 'string' || ['min', 'max', 'step', 'options'].some((key) => field[key] !== undefined)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
          'text fields require a string default and cannot declare numeric bounds or options.', fieldPath));
      }
    }
  }
}

/** @param {Record<string, unknown>} field @param {unknown} fieldNode @param {string} fieldPath @param {ValidationError[]} errors */
function validateRadioField(field, fieldNode, fieldPath, errors) {
  if (!['string', 'number', 'boolean'].includes(typeof field.default)
      || !Array.isArray(field.options) || field.options.length < 2 || field.options.length > 12) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'radio fields require a scalar default and between 2 and 12 options.',
      fieldPath
    ));
    return;
  }
  /** @type {unknown[]} */
  const values = [];
  field.options.forEach((option, optionIndex) => {
    const optionPath = `${fieldPath}.options[${optionIndex}]`;
    if (!isPlainObject(option)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'radio option must be a mapping.', optionPath));
      return;
    }
    validateObjectKeys(
      getSequenceItemNode(getValueNodeByKey(fieldNode, 'options'), optionIndex),
      PAGE_FORM_OPTION_KEYS,
      optionPath,
      errors
    );
    validateStringField(option.label, `${optionPath}.label`, true, errors);
    if (!['string', 'number', 'boolean'].includes(typeof option.value) || typeof option.value !== typeof field.default) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'radio option values must be scalar and match the default type.', `${optionPath}.value`));
    }
    values.push(option.value);
  });
  if (!values.some((value) => value === field.default)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'radio default must match one declared option value.', `${fieldPath}.default`));
  }
  if (new Set(values.map((value) => `${typeof value}:${String(value)}`)).size !== values.length) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'radio option values must be unique.', `${fieldPath}.options`));
  }
}
