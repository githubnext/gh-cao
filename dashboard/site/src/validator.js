import { createDebug } from './debug.js';
import { ERROR_CODES, PAGE_ICON_VALUES, ROOT_KEYS, WORKFLOW_ROLE_VALUES } from './specification.js';
import { parseDocuments, validateLanguageVersion, validateDashboard } from './validator-dashboard.js';
import { validateObjectKeys, createError, isPlainObject, getValueNodeByKey } from './validator-common.js';
import { state } from './validator-state.js';


const debugValidator = createDebug('validator');

/**
 * @typedef {{ code: string, message: string, path: string }} ValidationError
 */

/**
 * @typedef {{ ok: true, value: DashboardDocument, errors: [] } | { ok: false, errors: ValidationError[] }} ValidationResult
 */

/**
 * @typedef {{ languageVersion: string, dashboard: DashboardConfig }} DashboardDocument
 */

/**
 * @typedef {{ id: string, title: string, description?: string, defaults?: DashboardDefaults, units?: Record<string, UnitDefinition>, callouts?: SiteCallout[], pages: Array<BuiltInPage | CustomPage> }} DashboardConfig
 */

/**
 * @typedef {import('./components/site-callout.js').SiteCallout} SiteCallout
 */

/**
 * @typedef {{ name: string, symbol: string, significant: number, format?: string }} UnitDefinition
 */

/**
 * @typedef {{ scope?: Record<string, unknown>, time?: Record<string, unknown>, filters?: Record<string, unknown> }} DashboardDefaults
 */

/**
 * @typedef {{ id: string, kind: 'built-in', page: string, title?: string, description?: string }} BuiltInPage
 */

/**
 * @typedef {{ id: string, title?: string, description?: string, layout: 'full'|'wide'|'narrow', views: string[] }} PageSection
 */

/**
 * @typedef {{ id: string, kind: 'custom', title?: string, description?: string, route?: { 'hash-query-parameter': string }, views: unknown[], sections?: PageSection[] }} CustomPage
 */

/**
 * @param {string} source
 * @returns {ValidationResult}
 */
export function validateDashboardDocument(source) {
  /** @type {ValidationError[]} */
  const errors = [];

  /** @param {ValidationResult} result */
  const finish = (result) => {
    debugValidator({ operation: 'validate-dashboard-document', status: result.ok ? 'ok' : 'invalid', errorCount: result.errors.length });
    return result;
  };

  const documents = parseDocuments(source, errors);
  if (!documents) {
    return finish({ ok: false, errors });
  }

  const [document] = documents;
  if (!document) {
    return finish({ ok: false, errors });
  }

  const root = document.toJS({ mapAsMap: false });
  if (!isPlainObject(root)) {
    errors.push(createError(
      ERROR_CODES.invalidDocumentShape,
      'Dashboard document must contain exactly one YAML document whose root is a mapping.',
      '$'
    ));
    return finish({ ok: false, errors });
  }

  validateObjectKeys(document.contents, ROOT_KEYS, '$', errors);

  validateLanguageVersion(root['language-version'], errors);
  const dashboard = root.dashboard;
  if (!isPlainObject(dashboard)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'dashboard must be a mapping.',
      '$.dashboard'
    ));
    return finish({ ok: false, errors });
  }

  try {
    validateDashboard(dashboard, getValueNodeByKey(document.contents, 'dashboard'), errors);
  } finally {
    state.declaredQueries = new Map();
    state.declaredQueryFieldTypes = new Map();
    state.declaredCardTemplates = new Set();
    state.declaredCardDefinitions = new Map();
    state.declaredQueryTables = new Map();
    state.declaredQueryParameters = new Map();
    state.declaredCliActions = new Map();
  }

  if (errors.length > 0) {
    return finish({ ok: false, errors });
  }

  return finish({
    ok: true,
    value: {
      languageVersion: /** @type {string} */ (root['language-version']),
      dashboard: /** @type {DashboardConfig} */ (dashboard)
    },
    errors: []
  });
}

/**
 * Validate runtime logical-source relationships that cannot be expressed in the dashboard document.
 *
 * @param {Record<string, { rows?: unknown[] } | undefined>} sources
 * @returns {{ ok: true, errors: [] } | { ok: false, errors: ValidationError[] }}
 */
export function validateLogicalSources(sources) {
  /** @type {ValidationError[]} */
  const errors = [];
  const workflowRows = sources.workflows?.rows;
  if (workflowRows === undefined) return { ok: true, errors: [] };
  if (!Array.isArray(workflowRows)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'workflows.rows must be a sequence.',
      '$.sources.workflows.rows'
    ));
    return { ok: false, errors };
  }

  /** @type {Map<string, Array<{ row: Record<string, unknown>, index: number }>>} */
  const campaignRows = new Map();
  for (const [index, candidate] of workflowRows.entries()) {
    const path = `$.sources.workflows.rows[${index}]`;
    if (!isPlainObject(candidate)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'Each workflows row must be a mapping.',
        path
      ));
      continue;
    }

    const role = candidate['workflow-role'];
    if (typeof role !== 'string' || !WORKFLOW_ROLE_VALUES.includes(role)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'workflow-role must use orchestrator, worker, or standalone.',
        `${path}.workflow-role`
      ));
    }
    const campaignId = candidate.campaign;
    const hasCampaign = typeof campaignId === 'string' && campaignId.length > 0;
    if ((role === 'orchestrator' || role === 'worker') && !hasCampaign) {
      errors.push(createError(
        ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
        'An orchestrator or worker workflow must identify its campaign.',
        `${path}.campaign`
      ));
    }
    if (role === 'standalone' && campaignId != null) {
      errors.push(createError(
        ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
        'A standalone workflow must not identify a campaign.',
        `${path}.campaign`
      ));
    }

    const campaignIcon = candidate['campaign-icon'];
    if (campaignIcon !== undefined && (typeof campaignIcon !== 'string' || !PAGE_ICON_VALUES.includes(campaignIcon))) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'campaign-icon must name a canonical Octicon.',
        `${path}.campaign-icon`
      ));
    }

    validateNonNegativeSourceMeasure(candidate['max-ai-credits'], `${path}.max-ai-credits`, errors);
    validateNonNegativeSourceMeasure(candidate['campaign-aic-allowance'], `${path}.campaign-aic-allowance`, errors);

    if (hasCampaign && (role === 'orchestrator' || role === 'worker')) {
      const key = sourceEntityKey(candidate, 'campaign');
      const rows = campaignRows.get(key) ?? [];
      rows.push({ row: candidate, index });
      campaignRows.set(key, rows);
    }
  }

  for (const rows of campaignRows.values()) {
    const workflowAllowances = new Map(rows
      .filter(({ row }) => typeof row.workflow === 'string' && isNonNegativeFiniteNumber(row['max-ai-credits']))
      .map(({ row }) => [sourceEntityKey(row, 'workflow'), /** @type {number} */ (row['max-ai-credits'])]));
    const expectedAllowance = [...workflowAllowances.values()].reduce((total, value) => total + value, 0);
    for (const { row, index } of rows) {
      const allowance = row['campaign-aic-allowance'];
      if (isNonNegativeFiniteNumber(allowance) && !numbersEqual(allowance, expectedAllowance)) {
        errors.push(createError(
          ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
          'campaign-aic-allowance must equal the sum of available per-run workflow limits.',
          `$.sources.workflows.rows[${index}].campaign-aic-allowance`
        ));
      }
    }
  }

  const result = errors.length > 0
    ? /** @type {{ ok: false, errors: ValidationError[] }} */ ({ ok: false, errors })
    : /** @type {{ ok: true, errors: [] }} */ ({ ok: true, errors: [] });
  debugValidator({ operation: 'validate-logical-sources', status: result.ok ? 'ok' : 'invalid', errorCount: result.errors.length });
  return result;
}

/**
 * @param {unknown} value
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateNonNegativeSourceMeasure(value, path, errors) {
  if (value == null) return;
  if (!isNonNegativeFiniteNumber(value)) {
    errors.push(createError(
      ERROR_CODES.invalidEntityRelationshipOrSourceGrain,
      'Configured AI Credit limits must be finite non-negative numbers.',
      path
    ));
  }
}

/**
 * @param {unknown} value
 * @returns {value is number}
 */
function isNonNegativeFiniteNumber(value) {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0;
}

/**
 * @param {Record<string, unknown>} row
 * @param {string} field
 * @returns {string}
 */
function sourceEntityKey(row, field) {
  return JSON.stringify([
    String(row.organization ?? ''),
    String(row.repository ?? ''),
    String(row[field] ?? '')
  ]);
}

/**
 * @param {number} left
 * @param {number} right
 * @returns {boolean}
 */
function numbersEqual(left, right) {
  return Math.abs(left - right) <= Number.EPSILON * Math.max(1, Math.abs(left), Math.abs(right));
}
