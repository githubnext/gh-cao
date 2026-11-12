import { state } from './validator-state.js';
import { parseAllDocuments } from 'yaml';
import { isReadOnlyCliAction, parseCliActionTokens, validateActionLevel, validWorkflowDispatchArguments } from './action-validator.js';
import { CARD_TEMPLATE_ACTION_KEYS, CARD_DETAIL_LABEL_VALUES, CARD_STATUS_KEYS, CARD_TEMPLATE_KEYS, CARD_TIMING_FIELD_KEYS, DASHBOARD_KEYS, DASHBOARD_HORIZON_KEYS, DEFAULTS_KEYS, CLI_ACTION_KEYS, CLI_ACTION_ARGUMENT_KEYS, CLI_ACTION_ARGUMENT_TYPE_VALUES, CLI_ACTION_PLACEMENT_VALUES, ERROR_CODES, FIELD_DEFINITION_KEYS, FIELD_DISPLAY_VALUES, FIELD_FORMAT_VALUES, IDENTIFIER_PATTERN, LANGUAGE_VERSION, MAX_CLI_ACTIONS, MAX_CLI_ACTION_ARGUMENTS, MAX_CLI_ACTION_COMMAND_LENGTH, PAGE_ICON_VALUES, SITE_CALLOUT_KEYS, SITE_CALLOUT_VISIBILITY_KEYS, QUERY_SOURCE_VALUES, TABLE_ACTION_WHEN_KEYS, UNIT_DEFINITION_KEYS, UNIT_FORMAT_VALUES } from './specification.js';
import { cliActionTemplateFields } from './cli-action-template.js';
import { assertDialogSelector } from './dialog-selector.js';
import { compileDashboardQueryTypes } from './query-type-checker.js';
import { findDeadDashboardQueries } from './query-usage.js';
import { validateTooltip, validateNavigation, validatePage } from './validator-pages.js';
import { validateView, validateListDrill, validateQueryDrillReferences } from './validator-views.js';
import { validateQueries, validateSource, validateViewQueryMaterialization, sourceFieldNames, validateContext } from './validator-queries.js';
import { isSafeGithubUrlBase, isSafeRepositorySlug, validateRequiredIdentifier, validateStringField, validateOptionalStringField, validateObjectKeys, createError, isPlainObject, getValueNodeByKey, getSequenceItemNode } from './validator-common.js';
import { resolveReusablePageViews } from './validator-state.js';
import { createDebug } from './debug.js';

/** @typedef {import('./validator.js').ValidationError} ValidationError */

const debugValidatorDashboard = createDebug('validator-dashboard');


/**
 * @param {string} source
 * @param {ValidationError[]} errors
 * @returns {import('yaml').Document.Parsed[] | null}
 */
export function parseDocuments(source, errors) {
  /** @param {'ok' | 'yaml-syntax' | 'document-count' | 'exception'} status @param {number} [documentCount] */
  const report = (status, documentCount) => {
    debugValidatorDashboard({ operation: 'parse-documents', status, ...(documentCount !== undefined ? { documentCount } : {}) });
  };
  try {
    const documents = parseAllDocuments(source, {
      uniqueKeys: false,
      merge: false
    });
    if (documents.some((document) => document.errors.length > 0)) {
      errors.push(createError(
        ERROR_CODES.invalidYamlSyntax,
        'Dashboard document must be valid YAML 1.2.',
        '$'
      ));
      report('yaml-syntax');
      return null;
    }

    if (documents.length !== 1) {
      errors.push(createError(
        ERROR_CODES.invalidDocumentShape,
        'Dashboard document must contain exactly one YAML document.',
        '$'
      ));
      report('document-count', documents.length);
      return null;
    }

    report('ok');
    return documents;
  } catch {
    errors.push(createError(
      ERROR_CODES.invalidYamlSyntax,
      'Dashboard document must be valid YAML 1.2.',
      '$'
    ));
    report('exception');
    return null;
  }
}

/**
 * @param {unknown} value
 * @param {ValidationError[]} errors
 */
export function validateLanguageVersion(value, errors) {
  if (typeof value !== 'string') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'language-version must be the quoted string "0.1.0".',
      '$.language-version'
    ));
    return;
  }

  if (value !== LANGUAGE_VERSION) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'language-version must use the exact canonical value "0.1.0".',
      '$.language-version'
    ));
  }
}

/**
 * @param {unknown} templates
 * @param {unknown} templatesNode
 * @param {ValidationError[]} errors
 */
function validateCardTemplates(templates, templatesNode, errors) {
  const ids = new Set();
  if (templates === undefined) return ids;
  if (!Array.isArray(templates) || templates.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card-templates must be a non-empty sequence.', '$.dashboard.card-templates'));
    return ids;
  }

  /**
   * @param {unknown} actions
   * @param {unknown} actionsNode
   * @param {string} path
   * @param {ValidationError[]} errors
   */
  function validateCardTemplateActions(actions, actionsNode, path, errors) {
    if (actions === undefined) return;
    if (!Array.isArray(actions) || actions.length === 0) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template actions must be a non-empty sequence.', path));
      return;
    }
    actions.forEach((action, index) => {
      const actionPath = `${path}[${index}]`;
      const actionNode = getSequenceItemNode(actionsNode, index);
      if (!isPlainObject(action)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template action must be a mapping.', actionPath));
        return;
      }
      validateObjectKeys(actionNode, CARD_TEMPLATE_ACTION_KEYS, actionPath, errors);
      validateRequiredIdentifier(action.action, `${actionPath}.action`, 'card template CLI action reference', errors);
      const declaredAction = typeof action.action === 'string' ? state.declaredCliActions.get(action.action) : undefined;
      if (typeof action.action === 'string' && !declaredAction) {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'card template action must reference a declared dashboard CLI action.', `${actionPath}.action`));
      } else if (declaredAction?.placement !== 'row') {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'card template action must reference a row-placed dashboard CLI action.', `${actionPath}.action`));
      }
      const context = action.context;
      if (!Array.isArray(context) || context.length === 0) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template action context must be a non-empty sequence of source fields.', `${actionPath}.context`));
      } else {
        context.forEach((field, fieldIndex) => validateRequiredIdentifier(
          field,
          `${actionPath}.context[${fieldIndex}]`,
          'card template action context field',
          errors
        ));
        const command = declaredAction?.command;
        const templateFields = typeof command === 'string' ? cliActionTemplateFields(command) : [];
        if (templateFields.some((field) => !context.includes(field))) {
          errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'card template action context must include every command template field.', `${actionPath}.context`));
        }
      }
      if (action.when === undefined) return;
      if (!isPlainObject(action.when)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template action when must be a mapping.', `${actionPath}.when`));
        return;
      }
      validateObjectKeys(getValueNodeByKey(actionNode, 'when'), TABLE_ACTION_WHEN_KEYS, `${actionPath}.when`, errors);
      validateRequiredIdentifier(action.when.field, `${actionPath}.when.field`, 'card template action when field', errors);
      if (!Object.hasOwn(action.when, 'equals') || ['object', 'function', 'symbol'].includes(typeof action.when.equals)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template action when equals must be a scalar.', `${actionPath}.when.equals`));
      }
    });
  }
  templates.forEach((template, index) => {
    const path = `$.dashboard.card-templates[${index}]`;
    const templateNode = getSequenceItemNode(templatesNode, index);
    if (!isPlainObject(template)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template must be a mapping.', path));
      return;
    }
    validateObjectKeys(templateNode, CARD_TEMPLATE_KEYS, path, errors);
    validateRequiredIdentifier(template.id, `${path}.id`, 'card template id', errors);
    validateStringField(template.icon, `${path}.icon`, true, errors);
    if (typeof template.icon === 'string' && !PAGE_ICON_VALUES.includes(template.icon)) {
      errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'card template icon must use one canonical Octicon name.', `${path}.icon`));
    }
    if (template['icon-field'] !== undefined) {
      validateRequiredIdentifier(template['icon-field'], `${path}.icon-field`, 'card template icon field', errors);
    }
    if (typeof template.id === 'string') {
      if (ids.has(template.id)) errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, 'card template id must be unique.', `${path}.id`));
      ids.add(template.id);
    }
    validateCardTemplateField(template.title, getValueNodeByKey(templateNode, 'title'), `${path}.title`, errors);
    if (template.subtitle !== undefined) {
      validateCardTemplateField(template.subtitle, getValueNodeByKey(templateNode, 'subtitle'), `${path}.subtitle`, errors);
    }
    if (template['detail-labels'] !== undefined && !CARD_DETAIL_LABEL_VALUES.includes(String(template['detail-labels']))) {
      errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'card template detail-labels must be hidden or visible.', `${path}.detail-labels`));
    }
    validateCardTemplateStatus(template.status, getValueNodeByKey(templateNode, 'status'), `${path}.status`, errors);
    validateCardTemplateTiming(template.timing, getValueNodeByKey(templateNode, 'timing'), `${path}.timing`, errors);
    validateCardTemplateActions(template.actions, getValueNodeByKey(templateNode, 'actions'), `${path}.actions`, errors);
    validateListDrill(template.drill, getValueNodeByKey(templateNode, 'drill'), path, 'entity-cards', errors);
    for (const key of ['labels', 'details']) {
      const fields = template[key];
      if (!Array.isArray(fields) || (key === 'details' && fields.length === 0)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, `card template ${key} must be ${key === 'details' ? 'a non-empty' : 'an'} sequence.`, `${path}.${key}`));
        continue;
      }
      fields.forEach((field, fieldIndex) => validateCardTemplateField(
        field,
        getSequenceItemNode(getValueNodeByKey(templateNode, key), fieldIndex),
        `${path}.${key}[${fieldIndex}]`,
        errors
      ));
    }
  });
  return ids;
}

/**
 * A card template status declaration names the field whose observed value
 * selects the card's status icon and tone, with an optional fallback field for
 * entities whose terminal value is not yet observed.
 * @param {unknown} status
 * @param {unknown} statusNode
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateCardTemplateStatus(status, statusNode, path, errors) {
  if (status === undefined) return;
  if (!isPlainObject(status)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template status must be a mapping.', path));
    return;
  }
  validateObjectKeys(statusNode, CARD_STATUS_KEYS, path, errors);
  validateRequiredIdentifier(status.field, `${path}.field`, 'card template status field', errors);
  if (status['fallback-field'] !== undefined) {
    validateRequiredIdentifier(status['fallback-field'], `${path}.fallback-field`, 'card template status fallback field', errors);
  }
  validateOptionalStringField(status.title, `${path}.title`, errors);
}

/**
 * A card template timing declaration is an ordered sequence of icon-labeled
 * field definitions presented beside the card, such as the start time and the
 * elapsed duration of a run.
 * @param {unknown} timing
 * @param {unknown} timingNode
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateCardTemplateTiming(timing, timingNode, path, errors) {
  if (timing === undefined) return;
  if (!Array.isArray(timing) || timing.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template timing must be a non-empty sequence.', path));
    return;
  }
  timing.forEach((field, index) => {
    const fieldPath = `${path}[${index}]`;
    const fieldNode = getSequenceItemNode(timingNode, index);
    validateCardTemplateField(field, fieldNode, fieldPath, errors, CARD_TIMING_FIELD_KEYS);
    if (!isPlainObject(field)) return;
    validateStringField(field.icon, `${fieldPath}.icon`, true, errors);
    if (typeof field.icon === 'string' && !PAGE_ICON_VALUES.includes(field.icon)) {
      errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'card template timing icon must use one canonical Octicon name.', `${fieldPath}.icon`));
    }
  });
}

/**
 * @param {unknown} field
 * @param {unknown} fieldNode
 * @param {string} path
 * @param {ValidationError[]} errors
 * @param {string[]} [allowedKeys]
 */
function validateCardTemplateField(field, fieldNode, path, errors, allowedKeys = FIELD_DEFINITION_KEYS) {
  if (!isPlainObject(field)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'card template field must be a mapping.', path));
    return;
  }

  validateObjectKeys(fieldNode, allowedKeys, path, errors);
  validateRequiredIdentifier(field.field, `${path}.field`, 'card template field', errors);
  validateOptionalStringField(field.title, `${path}.title`, errors);
  if (field.display !== undefined && (typeof field.display !== 'string' || !FIELD_DISPLAY_VALUES.includes(field.display))) {
    errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'card template field display must use one canonical display value.', `${path}.display`));
  }

  if (field.format !== undefined && (typeof field.format !== 'string' || !FIELD_FORMAT_VALUES.includes(field.format))) {
    errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'card template field format must use one canonical format value.', `${path}.format`));
  }
}

/**
 * @param {unknown} views
 * @param {unknown} viewsNode
 * @param {ValidationError[]} errors
 */
function validateReusableViews(views, viewsNode, errors) {
  const definitions = new Map();
  if (views === undefined) return definitions;
  if (!Array.isArray(views) || views.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'views must be a non-empty sequence.', '$.dashboard.views'));
    return definitions;
  }
  const ids = new Set();
  views.forEach((view, index) => {
    const path = `$.dashboard.views[${index}]`;
    validateView(view, getSequenceItemNode(viewsNode, index), path, ids, errors);
    if (isPlainObject(view) && typeof view.id === 'string' && !definitions.has(view.id)) {
      definitions.set(view.id, view);
    }
  });
  return definitions;
}















/**
 * @param {Record<string, unknown>} dashboard
 * @param {unknown} dashboardNode
 * @param {ValidationError[]} errors
 */
export function validateDashboard(dashboard, dashboardNode, errors) {
  const errorCountBeforeDashboard = errors.length;
  validateObjectKeys(dashboardNode, DASHBOARD_KEYS, '$.dashboard', errors);

  validateRequiredIdentifier(dashboard.id, '$.dashboard.id', 'dashboard id', errors);
  validateStringField(dashboard.title, '$.dashboard.title', true, errors);
  validateOptionalStringField(dashboard.description, '$.dashboard.description', errors);

  if (dashboard.horizon !== undefined) {
    if (!isPlainObject(dashboard.horizon)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'horizon must be a mapping.',
        '$.dashboard.horizon'
      ));
    } else {
      validateObjectKeys(
        getValueNodeByKey(dashboardNode, 'horizon'),
        DASHBOARD_HORIZON_KEYS,
        '$.dashboard.horizon',
        errors
      );
      validateStringField(dashboard.horizon.label, '$.dashboard.horizon.label', true, errors);
      validateTooltip(
        dashboard.horizon.tooltip,
        getValueNodeByKey(getValueNodeByKey(dashboardNode, 'horizon'), 'tooltip'),
        '$.dashboard.horizon.tooltip',
        errors
      );
    }

  }

  if (dashboard['github-url-base'] !== undefined && !isSafeGithubUrlBase(dashboard['github-url-base'])) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'github-url-base must be an absolute HTTPS URL without credentials, query, or fragment.',
      '$.dashboard.github-url-base'
    ));
  }

  if (dashboard.repository !== undefined && !isSafeRepositorySlug(dashboard.repository)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'repository must be a non-empty owner/repo slug identifying the GitHub repository hosting the dashboard.',
      '$.dashboard.repository'
    ));
  }

  if (dashboard.defaults !== undefined) {
    if (!isPlainObject(dashboard.defaults)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'defaults must be a mapping.',
        '$.dashboard.defaults'
      ));
    } else {
      const defaultsNode = getValueNodeByKey(dashboardNode, 'defaults');
      validateObjectKeys(
        defaultsNode,
        DEFAULTS_KEYS,
        '$.dashboard.defaults',
        errors
      );
      validateContext(defaultsNode, dashboard.defaults, '$.dashboard.defaults', errors);
    }
  }

  state.declaredQueries = validateQueries(dashboard.queries, getValueNodeByKey(dashboardNode, 'queries'), errors);
  const queryTypes = compileDashboardQueryTypes(dashboard.queries);
  for (const queryError of queryTypes.errors) {
    const existingIndex = errors.findIndex((candidate) => (
      candidate.code === queryError.code && candidate.path === queryError.path
    ));
    if (existingIndex === -1) {
      errors.push(queryError);
    } else {
      errors[existingIndex] = queryError;
    }
  }
  state.declaredQueries = queryTypes.queryFields;
  state.declaredQueryFieldTypes = queryTypes.queryFieldTypes;
  state.declaredQueryTables = queryTypes.queryTables;
  for (const query of findDeadDashboardQueries(dashboard)) {
    errors.push(createError(
      ERROR_CODES.unusedQuery,
      `query "${query.name}" is not used by a view, callout, data status, or another retained query.`,
      query.path
    ));
  }
  validateCliActions(dashboard['cli-actions'], getValueNodeByKey(dashboardNode, 'cli-actions'), errors);
  state.declaredCardTemplates = validateCardTemplates(
    dashboard['card-templates'],
    getValueNodeByKey(dashboardNode, 'card-templates'),
    errors
  );
  state.declaredViews = validateReusableViews(
    dashboard.views,
    getValueNodeByKey(dashboardNode, 'views'),
    errors
  );
  const unitIds = validateUnits(dashboard.units, getValueNodeByKey(dashboardNode, 'units'), errors);
  validateSiteCallouts(dashboard.callouts, getValueNodeByKey(dashboardNode, 'callouts'), errors);
  validateDataStatus(dashboard['data-status'], getValueNodeByKey(dashboardNode, 'data-status'), errors);

  if (!Array.isArray(dashboard.pages) || dashboard.pages.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'pages must be a non-empty sequence.',
      '$.dashboard.pages'
    ));
    debugValidatorDashboard({
      operation: 'validate-dashboard',
      status: 'invalid',
      reason: 'missing-pages',
      errorCount: errors.length - errorCountBeforeDashboard
    });
    return;
  }

  /**
   * @param {unknown} actions
   * @param {unknown} actionsNode
   * @param {ValidationError[]} errors
   */
  function validateCliActions(actions, actionsNode, errors) {
    if (actions === undefined) return;
    if (!Array.isArray(actions) || actions.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'cli-actions must be a non-empty sequence.',
        '$.dashboard.cli-actions'
      ));
      return;
    }
    if (actions.length > MAX_CLI_ACTIONS) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `cli-actions is limited to ${MAX_CLI_ACTIONS} actions.`,
        '$.dashboard.cli-actions'
      ));
    }

    const ids = new Set();
    actions.forEach((action, index) => {
      const path = `$.dashboard.cli-actions[${index}]`;
      const actionNode = getSequenceItemNode(actionsNode, index);
      if (!isPlainObject(action)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'CLI action must be a mapping.',
          path
        ));
        return;
      }
      validateObjectKeys(actionNode, CLI_ACTION_KEYS, path, errors);
      validateRequiredIdentifier(action.id, `${path}.id`, 'CLI action id', errors);
      if (typeof action.id === 'string') {
        if (ids.has(action.id)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action id must be unique within dashboard.cli-actions.',
            `${path}.id`
          ));
        }
        ids.add(action.id);
        state.declaredCliActions.set(action.id, action);
      }
      validateActionLevel(action.level, `${path}.level`, errors);
      validateOptionalStringField(action.verb, `${path}.verb`, errors);
      if (action.label !== undefined || action.level === undefined || action.level === 'operate') {
        validateStringField(action.label, `${path}.label`, true, errors);
      }
      validateOptionalStringField(action.description, `${path}.description`, errors);
      if (action.icon !== undefined) validateStringField(action.icon, `${path}.icon`, true, errors);
      if (typeof action.icon === 'string' && !PAGE_ICON_VALUES.includes(action.icon)) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          'CLI action icon must use one canonical icon value.',
          `${path}.icon`
        ));
      }
      if (action.placement !== undefined) {
        validateStringField(action.placement, `${path}.placement`, true, errors);
        if (typeof action.placement === 'string' && !CLI_ACTION_PLACEMENT_VALUES.includes(action.placement)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'CLI action placement must use toolbar, settings, view, or row.',
            `${path}.placement`
          ));
        }
        if (action['copy-only'] !== undefined && typeof action['copy-only'] !== 'boolean') {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action copy-only must be a boolean.',
            `${path}.copy-only`
          ));
        }
      }
      validateStringField(action.command, `${path}.command`, true, errors);
      if (action['preview-command'] !== undefined) {
        validateStringField(action['preview-command'], `${path}.preview-command`, true, errors);
        const previewTokens = typeof action['preview-command'] === 'string'
          ? parseCliActionTokens(action['preview-command']) : null;
        const commandTokens = typeof action.command === 'string' ? parseCliActionTokens(action.command) : null;
        if (!previewTokens?.length || !commandTokens
          || typeof action['preview-command'] === 'string' && (
            action['preview-command'].length > MAX_CLI_ACTION_COMMAND_LENGTH
            || /[\r\n\0]/.test(action['preview-command'])
          )
          || previewTokens.some((token, index) => token !== commandTokens[index])) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action preview-command must be a non-empty, single-line token prefix of command.',
            `${path}.preview-command`));
        }
      }
      if (action.selector !== undefined) {
        try {
          assertDialogSelector(action.selector, typeof action.command === 'string' ? cliActionTemplateFields(action.command) : []);
        } catch (error) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, error instanceof Error ? error.message : 'Invalid dialog selector.', `${path}.selector`));
        }
        if (action.level !== 'operate' || action.placement !== 'view' || action['copy-only'] === true) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'Selector actions must be executable, view-placed operate actions.', `${path}.selector`));
        }
      }
      if (typeof action.command === 'string') {
        if (action.command.length > MAX_CLI_ACTION_COMMAND_LENGTH) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `CLI action command is limited to ${MAX_CLI_ACTION_COMMAND_LENGTH} characters.`,
            `${path}.command`
          ));
        }
        const commandTokens = parseCliActionTokens(action.command);
        const isGhAwCommand =
          commandTokens?.[0] === 'gh' && commandTokens[1] === 'aw' && commandTokens.length >= 3;
        const isCaoCommand =
          commandTokens?.[0] === 'gh' && commandTokens[1] === 'cao' && commandTokens.length >= 3;
        const isWorkflowDispatchCommand =
          commandTokens?.[0] === 'gh'
          && commandTokens[1] === 'workflow'
          && commandTokens[2] === 'run';
        const isAgentTaskCreateCommand =
          commandTokens?.length === 5
          && commandTokens[0] === 'gh'
          && commandTokens[1] === 'agent-task'
          && commandTokens[2] === 'create'
          && commandTokens[3] === '--from-file'
          && commandTokens[4] === '-';
        if (action.level === 'explore' && !isReadOnlyCliAction(action)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'Explore CLI actions must use a supported read-only command.',
            `${path}.command`
          ));
        }
        if (!isCaoCommand && !isGhAwCommand && !isWorkflowDispatchCommand && !isAgentTaskCreateCommand) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action command must start with "gh cao", "gh aw", "gh workflow run", or be "gh agent-task create --from-file -".',
            `${path}.command`
          ));
        }
        if (isAgentTaskCreateCommand && action.placement !== 'row') {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'Agent-task CLI actions must use row placement.',
            `${path}.placement`
          ));
        }
        if (
          isWorkflowDispatchCommand
          && !validWorkflowDispatchArguments(commandTokens.slice(3))
        ) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action workflow dispatch command has an invalid workflow or option.',
            `${path}.command`
          ));
        }
        if (
          isWorkflowDispatchCommand
          && Array.isArray(action.arguments)
          && action.arguments.length > 0
        ) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action workflow dispatch commands do not support boolean arguments.',
            `${path}.arguments`
          ));
        }
        if (/[\r\n]/.test(action.command) || action.command.includes('\0')) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action command must be a single line without null characters.',
            `${path}.command`
          ));
        }
        if (/[!;&|`$<>]/.test(action.command)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action command must not contain shell control operators.',
            `${path}.command`
          ));
        }
        const withoutTemplates = action.command.replace(/\{\{[a-z][a-z0-9]*(?:-[a-z0-9]+)*\}\}/g, '');
        if (/[{}]/.test(withoutTemplates)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action command contains an invalid template token.',
            `${path}.command`
          ));
        }
      }
      validateCliActionArguments(
        action.arguments,
        getValueNodeByKey(actionNode, 'arguments'),
        `${path}.arguments`,
        errors
      );
    });
  }

  /**
   * @param {unknown} actionArguments
   * @param {unknown} argumentsNode
   * @param {string} path
   * @param {ValidationError[]} errors
   */
  function validateCliActionArguments(actionArguments, argumentsNode, path, errors) {
    if (actionArguments === undefined) return;
    if (!Array.isArray(actionArguments) || actionArguments.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'CLI action arguments must be a non-empty sequence.',
        path
      ));
      return;
    }
    if (actionArguments.length > MAX_CLI_ACTION_ARGUMENTS) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `CLI action arguments are limited to ${MAX_CLI_ACTION_ARGUMENTS} controls.`,
        path
      ));
    }
    const ids = new Set();
    const flags = new Set();
    actionArguments.forEach((argument, index) => {
      const argumentPath = `${path}[${index}]`;
      const argumentNode = getSequenceItemNode(argumentsNode, index);
      if (!isPlainObject(argument)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'CLI action argument must be a mapping.',
          argumentPath
        ));
        return;
      }
      validateObjectKeys(argumentNode, CLI_ACTION_ARGUMENT_KEYS, argumentPath, errors);
      validateRequiredIdentifier(argument.id, `${argumentPath}.id`, 'CLI action argument id', errors);
      if (typeof argument.id === 'string') {
        if (ids.has(argument.id)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action argument id must be unique within the action.',
            `${argumentPath}.id`
          ));
        }
        ids.add(argument.id);
      }
      validateStringField(argument.label, `${argumentPath}.label`, true, errors);
      validateOptionalStringField(argument.description, `${argumentPath}.description`, errors);
      validateStringField(argument.type, `${argumentPath}.type`, true, errors);
      if (typeof argument.type === 'string' && !CLI_ACTION_ARGUMENT_TYPE_VALUES.includes(argument.type)) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          'CLI action argument type must be boolean.',
          `${argumentPath}.type`
        ));
      }
      validateStringField(argument.flag, `${argumentPath}.flag`, true, errors);
      if (typeof argument.flag === 'string') {
        if (!/^--[a-z0-9]+(?:-[a-z0-9]+)*$/.test(argument.flag)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action argument flag must be a canonical long option.',
            `${argumentPath}.flag`
          ));
        }
        if (flags.has(argument.flag)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'CLI action argument flag must be unique within the action.',
            `${argumentPath}.flag`
          ));
        }
        flags.add(argument.flag);
      }
      if (argument.default !== undefined && typeof argument.default !== 'boolean') {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'Boolean CLI action argument default must be a boolean.',
          `${argumentPath}.default`
        ));
      }
    });
  }

  /** @type {Set<string>} */
  const pageIds = new Set();
  dashboard.pages.forEach((page, index) => {
    if (isPlainObject(page)) {
      const configuredViews = page.kind === 'built-in' && isPlainObject(page.definition)
        ? page.definition.views
        : page.views;
      if (Array.isArray(configuredViews)) {
        configuredViews.forEach((view, viewIndex) => {
          if (typeof view === 'string' && !state.declaredViews.has(view)) {
            errors.push(createError(
              ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
              'page view must reference a declared reusable dashboard view.',
              `$.dashboard.pages[${index}]${page.kind === 'built-in' ? '.definition' : ''}.views[${viewIndex}]`
            ));
          }
        });
      }
    }
    validatePage(resolveReusablePageViews(page), getSequenceItemNode(getValueNodeByKey(dashboardNode, 'pages'), index), `$.dashboard.pages[${index}]`, pageIds, errors);
  });
  validateViewQueryMaterialization(dashboard, errors);
  (Array.isArray(dashboard['card-templates']) ? dashboard['card-templates'] : []).forEach((template, index) => {
    if (!isPlainObject(template) || !isPlainObject(template.drill)) return;
    const fields = [
      template.title,
      template.subtitle,
      template.status,
      ...(Array.isArray(template.labels) ? template.labels : []),
      ...(Array.isArray(template.details) ? template.details : []),
      ...(Array.isArray(template.timing) ? template.timing : [])
    ].flatMap((field) => (
      isPlainObject(field) && typeof field.field === 'string' ? [field.field] : []
    ));
    validateQueryDrillReferences(
      template.drill,
      `$.dashboard.card-templates[${index}].drill`,
      dashboard,
      pageIds,
      state.declaredQueries,
      fields,
      errors
    );
  });
  dashboard.pages.forEach((page, index) => {
    page = resolveReusablePageViews(page);
    if (!isPlainObject(page)) return;
    if (isPlainObject(page.route) && typeof page.route['navigation-page'] === 'string') {
      const navigationPage = page.route['navigation-page'];
      if (IDENTIFIER_PATTERN.test(navigationPage)) {
        if (navigationPage === page.id) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'route navigation-page must reference a different dashboard page.',
            `$.dashboard.pages[${index}].route.navigation-page`
          ));
        } else if (!pageIds.has(navigationPage)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'route navigation-page must reference a declared dashboard page id.',
            `$.dashboard.pages[${index}].route.navigation-page`
          ));
        }

      }
    }
    if (isPlainObject(page.route) && Array.isArray(page.route.tabs)) {
      page.route.tabs.forEach((tab, tabIndex) => {
        if (!isPlainObject(tab) || typeof tab.page !== 'string' || !IDENTIFIER_PATTERN.test(tab.page)) return;
        if (!pageIds.has(tab.page)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'route tab page must reference a declared dashboard page id.',
            `$.dashboard.pages[${index}].route.tabs[${tabIndex}].page`
          ));
        }
      });
    }
    if (isPlainObject(page.route) && Array.isArray(page.route.breadcrumbs)) {
      page.route.breadcrumbs.forEach((breadcrumb, breadcrumbIndex) => {
        if (!isPlainObject(breadcrumb) || typeof breadcrumb.page !== 'string' || !IDENTIFIER_PATTERN.test(breadcrumb.page)) return;
        if (!pageIds.has(breadcrumb.page)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'route breadcrumb page must reference a declared dashboard page id.',
            `$.dashboard.pages[${index}].route.breadcrumbs[${breadcrumbIndex}].page`
          ));
        }
      });
    }
    const views = page.kind === 'built-in' && isPlainObject(page.definition)
      ? page.definition.views
      : page.views;
    if (Array.isArray(views)) {
      views.forEach((view, viewIndex) => {
        const viewPath = page.kind === 'built-in'
          ? `$.dashboard.pages[${index}].definition.views[${viewIndex}]`
          : `$.dashboard.pages[${index}].views[${viewIndex}]`;
        const navigationPage = isPlainObject(view) && isPlainObject(view.metric)
          ? view.metric['navigation-page']
          : undefined;
        if (typeof navigationPage === 'string' && IDENTIFIER_PATTERN.test(navigationPage) && !pageIds.has(navigationPage)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'metric navigation-page must reference a declared dashboard page id.',
            `${viewPath}.metric.navigation-page`
          ));
        }
        const viewAllPage = isPlainObject(view) && isPlainObject(view.list) && isPlainObject(view.list['view-all'])
          ? view.list['view-all'].page
          : undefined;
        if (typeof viewAllPage === 'string' && IDENTIFIER_PATTERN.test(viewAllPage) && !pageIds.has(viewAllPage)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'list view-all page must reference a declared dashboard page id.',
            `${viewPath}.list.view-all.page`
          ));
        }
        const drill = isPlainObject(view) && isPlainObject(view['card-drill'])
          ? view['card-drill']
          : isPlainObject(view) && isPlainObject(view.list) && isPlainObject(view.list.drill)
            ? view.list.drill
          : null;
        const drillPath = isPlainObject(view) && isPlainObject(view['card-drill'])
          ? `${viewPath}.card-drill`
          : `${viewPath}.list.drill`;
        if (drill?.type === 'query') {
          const sourceName = isPlainObject(view.data) && typeof view.data.source === 'string'
            ? view.data.source
            : null;
          const fields = sourceName ? sourceFieldNames(sourceName) : null;
          validateQueryDrillReferences(drill, drillPath, dashboard, pageIds, state.declaredQueries, fields, errors);
        }
      });
    }
  });
  if (Array.isArray(dashboard.callouts)) {
    dashboard.callouts.forEach((callout, index) => {
      if (!isPlainObject(callout) || typeof callout['navigation-page'] !== 'string') return;
      if (IDENTIFIER_PATTERN.test(callout['navigation-page']) && !pageIds.has(callout['navigation-page'])) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'callout navigation-page must reference a declared dashboard page id.',
          `$.dashboard.callouts[${index}].navigation-page`
        ));
      }
    });
  }
  validateUnitReferences(dashboard.pages, unitIds, errors);

  if (dashboard.navigation !== undefined) {
    validateNavigation(dashboard.navigation, getValueNodeByKey(dashboardNode, 'navigation'), pageIds, errors);
  }

  debugValidatorDashboard({
    operation: 'validate-dashboard',
    status: errors.length === errorCountBeforeDashboard ? 'ok' : 'invalid',
    pageCount: dashboard.pages.length,
    errorCount: errors.length - errorCountBeforeDashboard
  });

  /**
   * @param {unknown} callouts
   * @param {unknown} calloutsNode
   * @param {ValidationError[]} errors
   */
  function validateSiteCallouts(callouts, calloutsNode, errors) {
    if (callouts === undefined) return;
    if (!Array.isArray(callouts) || callouts.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'callouts must be a non-empty sequence.',
        '$.dashboard.callouts'
      ));
      return;
    }
    const calloutIds = new Set();
    callouts.forEach((callout, index) => {
      const path = `$.dashboard.callouts[${index}]`;
      const calloutNode = getSequenceItemNode(calloutsNode, index);
      if (!isPlainObject(callout)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'callout must be a mapping.', path));
        return;
      }
      validateObjectKeys(calloutNode, SITE_CALLOUT_KEYS, path, errors);
      validateRequiredIdentifier(callout.id, `${path}.id`, 'callout id', errors);
      if (typeof callout.id === 'string' && calloutIds.has(callout.id)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'callout id must be unique within dashboard.callouts.', `${path}.id`));
      } else if (typeof callout.id === 'string') {
        calloutIds.add(callout.id);
      }
      validateStringField(callout.title, `${path}.title`, true, errors);
      validateStringField(callout.description, `${path}.description`, true, errors);
      validateOptionalStringField(callout.icon, `${path}.icon`, errors);
      if (callout['navigation-page'] !== undefined) {
        validateRequiredIdentifier(callout['navigation-page'], `${path}.navigation-page`, 'navigation-page', errors);
      }
      if (typeof callout.icon === 'string' && !PAGE_ICON_VALUES.includes(callout.icon)) {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier, 'callout icon must use one canonical icon value.', `${path}.icon`));
      }
      validateSiteCalloutVisibility(callout['visible-when'], getValueNodeByKey(calloutNode, 'visible-when'), `${path}.visible-when`, errors);
    });
  }

  /**
   * @param {unknown} status
   * @param {unknown} statusNode
   * @param {ValidationError[]} errors
   */
  function validateDataStatus(status, statusNode, errors) {
    if (status === undefined) return;
    const path = '$.dashboard.data-status';
    if (!isPlainObject(status)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'data-status must be a mapping.', path));
      return;
    }
    validateObjectKeys(statusNode, ['freshness-source', 'health-source', 'ingestion-source', 'stale-after-hours'], path, errors);
    /** @type {Array<[string, string[]]>} */
    const bindings = [
      ['freshness-source', ['latest-data-at']],
      ['health-source', ['configured', 'health']],
      ['ingestion-source', ['status-label', 'status-icon', 'status-tone', 'message', 'repository', 'scanned-hours', 'target-hours', 'api-remaining', 'api-reserve', 'api-reset-at', 'next-attempt-at', 'observed-at']]
    ];
    for (const [key, fields] of bindings) {
      const source = status[key];
      if (key !== 'freshness-source' && source === undefined) continue;
      validateSource(source, `${path}.${key}`, errors);
      if (typeof source === 'string' && !state.declaredQueries.has(source)) {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'data-status sources must reference declared queries.', `${path}.${key}`));
      }
      if (typeof source === 'string' && state.declaredQueries.has(source)
          && fields.some((field) => !sourceFieldNames(source)?.includes(field))) {
        errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, `data-status source must expose ${fields.join(', ')}.`, `${path}.${key}`));
      }
    }
    if (typeof status['stale-after-hours'] !== 'number' || !Number.isFinite(status['stale-after-hours']) || status['stale-after-hours'] <= 0) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'stale-after-hours must be positive and finite.', `${path}.stale-after-hours`));
    }
  }

  /**
   * @param {unknown} visibility
   * @param {unknown} visibilityNode
   * @param {string} path
   * @param {ValidationError[]} errors
   */
  function validateSiteCalloutVisibility(visibility, visibilityNode, path, errors) {
    if (visibility === undefined) return;
    if (!isPlainObject(visibility)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'visible-when must be a mapping.', path));
      return;
    }
    validateObjectKeys(visibilityNode, SITE_CALLOUT_VISIBILITY_KEYS, path, errors);
    validateSource(visibility.source, `${path}.source`, errors);
    validateStringField(visibility.field, `${path}.field`, true, errors);
    if (
      typeof visibility.source === 'string'
      && (QUERY_SOURCE_VALUES.includes(visibility.source) || state.declaredQueries.has(visibility.source))
      && typeof visibility.field === 'string'
      && !sourceFieldNames(visibility.source)?.includes(visibility.field)
    ) {
      errors.push(createError(ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference, 'visible-when field must be declared by visible-when source.', `${path}.field`));
    }
    const hasAge = Object.hasOwn(visibility, 'older-than-hours');
    if (hasAge) {
      if (Object.hasOwn(visibility, 'equals') || typeof visibility['older-than-hours'] !== 'number'
          || !Number.isFinite(visibility['older-than-hours']) || visibility['older-than-hours'] <= 0) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
          'visible-when older-than-hours must be positive and finite, and cannot be combined with equals.', `${path}.older-than-hours`));
      }
    } else if (!Object.hasOwn(visibility, 'equals') || ['object', 'function', 'symbol'].includes(typeof visibility.equals)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'visible-when equals must be a scalar.', `${path}.equals`));
    }
  }

  /**
   * @param {unknown} units
   * @param {unknown} unitsNode
   * @param {ValidationError[]} errors
   * @returns {Set<string>}
   */
  function validateUnits(units, unitsNode, errors) {
    const unitIds = new Set();
    if (units === undefined) return unitIds;
    if (!isPlainObject(units) || Object.keys(units).length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'units must be a non-empty mapping of unit identifiers to definitions.',
        '$.dashboard.units'
      ));
      return unitIds;
    }

    for (const [unitId, definition] of Object.entries(units)) {
      const path = `$.dashboard.units.${unitId}`;
      if (!IDENTIFIER_PATTERN.test(unitId)) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          'unit identifiers must use canonical kebab-case.',
          path
        ));
      } else {
        unitIds.add(unitId);
      }
      if (!isPlainObject(definition)) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'unit definitions must be mappings.', path));
        continue;
      }

      validateObjectKeys(getValueNodeByKey(unitsNode, unitId), UNIT_DEFINITION_KEYS, path, errors);
      validateStringField(definition.name, `${path}.name`, true, errors);
      validateStringField(definition.symbol, `${path}.symbol`, true, errors);
      if (typeof definition.significant !== 'number' || !Number.isFinite(definition.significant) || definition.significant <= 0) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'unit significant must be a finite positive number.',
          `${path}.significant`
        ));
      }
      if (definition.format !== undefined) {
        validateStringField(definition.format, `${path}.format`, true, errors);
        if (typeof definition.format === 'string' && !UNIT_FORMAT_VALUES.includes(definition.format)) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'unit format must use one canonical unit format value.',
            `${path}.format`
          ));
        }
        if (definition.format === 'duration' && (definition.symbol !== 's' || definition.significant !== 1)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'duration units must use symbol "s" and significant 1.',
            path
          ));
        }
        if (definition.format === 'bytes' && (definition.symbol !== 'B' || definition.significant !== 1)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'byte units must use symbol "B" and significant 1.',
            path
          ));
        }
        if (definition.format === 'usd' && (definition.symbol !== 'USD' || definition.significant !== 0.001)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'USD units must use symbol "USD" and significant 0.001.',
            path
          ));
        }
        if (definition.format === 'aicc' && (definition.name !== 'AICc($)' || definition.significant !== 2)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'AIC cost units must use name "AICc($)" and significant 2.',
            path
          ));
        }
      }
    }
    return unitIds;
  }

  /**
   * @param {unknown[]} pages
   * @param {Set<string>} unitIds
   * @param {ValidationError[]} errors
   */
  function validateUnitReferences(pages, unitIds, errors) {
    pages.forEach((page, pageIndex) => {
      if (!isPlainObject(page)) return;
      const views = page.kind === 'built-in' && isPlainObject(page.definition)
        ? page.definition.views
        : page.views;
      if (!Array.isArray(views)) return;
      const viewsPath = page.kind === 'built-in'
        ? `$.dashboard.pages[${pageIndex}].definition.views`
        : `$.dashboard.pages[${pageIndex}].views`;
      views.forEach((view, viewIndex) => {
        if (!isPlainObject(view) || !isPlainObject(view.encoding)) return;
        for (const [channel, value] of Object.entries(view.encoding)) {
          const definitions = channel === 'columns' && Array.isArray(value) ? value : [value];
          definitions.forEach((definition, definitionIndex) => {
            if (!isPlainObject(definition) || definition.unit === undefined) return;
            const path = `${viewsPath}[${viewIndex}].encoding.${channel}${channel === 'columns' ? `[${definitionIndex}]` : ''}.unit`;
            if (typeof definition.unit === 'string' && !unitIds.has(definition.unit)) {
              errors.push(createError(
                ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
                'unit must reference a unit declared by dashboard.units.',
                path
              ));
            }
          });
        }
      });
    });
  }
}
