import { state } from './validator-state.js';
import { DATABASE_QUERY_SOURCE_TABLES } from './specification.js';
import { BUILT_IN_PAGE_KEYS, BUILT_IN_PAGE_VALUES, CUSTOM_PAGE_KEYS, BUILT_IN_PAGE_DATA_STATE_KEYS, BUILT_IN_PAGE_DEFINITION_KEYS, ERROR_CODES, IDENTIFIER_PATTERN, NAVIGATION_INDICATOR_KEYS, NAVIGATION_SECTION_KEYS, PAGE_ROUTE_KEYS, PAGE_ROUTE_TITLE_FORMAT_VALUES, PAGE_ROUTE_TAB_KEYS, MAX_PAGE_ROUTE_TABS, PAGE_ICON_VALUES, PAGE_KIND_VALUES, PAGE_SECTION_KEYS, PAGE_SECTION_LAYOUT_VALUES, QUERY_SOURCE_VALUES, TOOLTIP_KEYS, BUILT_IN_PAGE_REQUIRED_SOURCES, BUILT_IN_PAGE_REQUIRED_FIELDS, VIEW_ELEMENT_VALUES } from './specification.js';
import { validateView, validateViewFilterBar, validateViewDataArguments, validateListDrill, validateTableActions, validateProgressiveDisclosure, validateGraphicalLayout, validatePageForm } from './validator-views.js';
import { validateSource, sourceFieldNames, validateSourceSequence } from './validator-queries.js';
import { validateRequiredIdentifier, validateStringField, validateSemanticMetadataLength, validateOptionalStringField, validateObjectKeys, createError, isPlainObject, getValueNodeByKey, getSequenceItemNode } from './validator-common.js';
import { createDebug } from './debug.js';

/** @typedef {import('./validator.js').ValidationError} ValidationError */

const debugValidatorPages = createDebug('validator-pages');


/**
 * @param {unknown} tooltip
 * @param {unknown} tooltipNode
 * @param {string} path
 * @param {ValidationError[]} errors
 */
export function validateTooltip(tooltip, tooltipNode, path, errors) {
  if (!isPlainObject(tooltip)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'tooltip must be a mapping.', path));
    return;
  }
  validateObjectKeys(tooltipNode, TOOLTIP_KEYS, path, errors);
  validateStringField(tooltip.label, `${path}.label`, true, errors);
  validateStringField(tooltip.description, `${path}.description`, true, errors);
  validateOptionalStringField(tooltip.icon, `${path}.icon`, errors);
  if (typeof tooltip.icon === 'string' && !PAGE_ICON_VALUES.includes(tooltip.icon)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'tooltip icon must use one canonical icon value.',
      `${path}.icon`
    ));
  }
}

/**
 * @param {unknown} navigation
 * @param {unknown} navigationNode
 * @param {Set<string>} pageIds
 * @param {ValidationError[]} errors
 */
export function validateNavigation(navigation, navigationNode, pageIds, errors) {
  const path = '$.dashboard.navigation';
  if (!Array.isArray(navigation) || navigation.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'navigation must be a non-empty sequence of sidebar sections.',
      path
    ));
    return;
  }

  /** @type {Set<string>} */
  const referencedPageIds = new Set();
  navigation.forEach((section, index) => {
    const sectionPath = `${path}[${index}]`;
    const sectionNode = getSequenceItemNode(navigationNode, index);
    if (!isPlainObject(section)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'navigation section must be a mapping.', sectionPath));
      return;
    }

    validateObjectKeys(sectionNode, NAVIGATION_SECTION_KEYS, sectionPath, errors);
    validateOptionalStringField(section.label, `${sectionPath}.label`, errors);
    if (section.label === '') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'navigation section label must be a non-empty string when present.',
        `${sectionPath}.label`
      ));
    }
    if (section.experimental !== undefined && typeof section.experimental !== 'boolean') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'navigation section experimental must be a boolean.',
        `${sectionPath}.experimental`
      ));
    }
    if (section.placement !== undefined && section.placement !== 'bottom') {
      const found = typeof section.placement === 'string'
        ? `"${section.placement}"`
        : JSON.stringify(section.placement);
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        `navigation section placement must be "bottom" when present; found ${found}.`,
        `${sectionPath}.placement`
      ));
    }
    if (section.placement === 'bottom' && typeof section.label !== 'string') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'navigation section placement requires a non-empty label.',
        `${sectionPath}.label`
      ));
    }

    if (!Array.isArray(section.pages) || section.pages.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'navigation section pages must reference at least one declared page id.',
        `${sectionPath}.pages`
      ));
      return;
    }

    section.pages.forEach((pageId, pageIndex) => {
      const pageIdPath = `${sectionPath}.pages[${pageIndex}]`;
      if (typeof pageId !== 'string' || !pageIds.has(pageId)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'navigation section page must reference a declared dashboard page id.',
          pageIdPath
        ));
        return;
      }
      if (referencedPageIds.has(pageId)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'each dashboard page may appear in only one navigation section.',
          pageIdPath
        ));
      }
      referencedPageIds.add(pageId);
    });
  });

}

/**
 * @param {unknown} page
 * @param {unknown} pageNode
 * @param {string} path
 * @param {Set<string>} pageIds
 * @param {ValidationError[]} errors
 */
export function validatePage(page, pageNode, path, pageIds, errors) {
  if (!isPlainObject(page)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'page must be a mapping.', path));
    return;
  }

  validateStringField(page.kind, `${path}.kind`, true, errors);
  if (typeof page.kind === 'string' && !PAGE_KIND_VALUES.includes(page.kind)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'kind must be exactly "built-in" or "custom".',
      `${path}.kind`
    ));
  }

  validateRequiredIdentifier(page.id, `${path}.id`, 'page id', errors);
  if (typeof page.id === 'string') {
    if (pageIds.has(page.id)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'page id must be unique within dashboard.pages.',
        `${path}.id`
      ));
    }
    pageIds.add(page.id);
  }

  validateOptionalStringField(page.title, `${path}.title`, errors);
  if (page.requires !== undefined) {
    const requiresPath = `${path}.requires`;
    if (!isPlainObject(page.requires)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'page requires must be a mapping.', requiresPath));
    } else {
      validateObjectKeys(getValueNodeByKey(pageNode, 'requires'), ['capability', 'source', 'message'], requiresPath, errors);
      if (page.requires.capability === undefined && page.requires.source === undefined) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
          'page requires must declare a capability or source.', requiresPath));
      }
      if (page.requires.capability !== undefined && page.requires.capability !== 'cli-queue' && page.requires.capability !== 'admin') {
        errors.push(createError(ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          'page requires capability must be cli-queue or admin.', `${requiresPath}.capability`));
      }
      if (page.requires.source !== undefined) validateSource(page.requires.source, `${requiresPath}.source`, errors);
      validateOptionalStringField(page.requires.message, `${requiresPath}.message`, errors);
    }
  }
  validateOptionalStringField(page['navigation-label'], `${path}.navigation-label`, errors);
  validateNavigationIndicator(
    page['navigation-indicator'],
    getValueNodeByKey(pageNode, 'navigation-indicator'),
    `${path}.navigation-indicator`,
    errors
  );
  validateOptionalStringField(page.description, `${path}.description`, errors);
  for (const field of ['intent', 'subject', 'objective', 'acceptance']) {
    if (page[field] !== undefined) {
      validateStringField(page[field], `${path}.${field}`, true, errors);
      if (typeof page[field] === 'string' && !page[field].trim()) {
        errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
          `${field} must not be whitespace only.`, `${path}.${field}`));
      }
    }
  }
  if (typeof page.intent === 'string' && [...page.intent].length > 8000) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
      'Page intent must be at most 8000 characters.', `${path}.intent`));
  }
  validateSemanticMetadataLength(page, path, errors);
  if (page['class-name'] !== undefined) {
    validateRequiredIdentifier(page['class-name'], `${path}.class-name`, 'page class name', errors);
  }
  if (page.experimental !== undefined && typeof page.experimental !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'experimental must be a Boolean when present.',
      `${path}.experimental`
    ));
  }
  if (page['filter-bar'] !== undefined && typeof page['filter-bar'] !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'filter-bar must be a Boolean when present.',
      `${path}.filter-bar`
    ));
  }
  if (page['view-mode-control'] !== undefined && typeof page['view-mode-control'] !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'view-mode-control must be a Boolean when present.',
      `${path}.view-mode-control`
    ));
  }
  if (page['pull-refresh'] !== undefined && typeof page['pull-refresh'] !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'pull-refresh must be a Boolean when present.',
      `${path}.pull-refresh`
    ));
  }
  if (page['mode-indicator'] !== undefined && typeof page['mode-indicator'] !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'mode-indicator must be a Boolean when present.',
      `${path}.mode-indicator`
    ));
  }
  if (page['retain-on-navigation'] !== undefined && typeof page['retain-on-navigation'] !== 'boolean') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'retain-on-navigation must be a Boolean when present.',
      `${path}.retain-on-navigation`
    ));
  }
  validatePageForm(page.form, getValueNodeByKey(pageNode, 'form'), `${path}.form`, errors);
  if (page['query-binding'] !== undefined && page['query-binding'] !== 'view' && page['query-binding'] !== 'page') {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'query-binding must be "view" or "page" when present.',
      `${path}.query-binding`
    ));
  }
  if (page.icon !== undefined) {
    validateStringField(page.icon, `${path}.icon`, true, errors);
    if (typeof page.icon === 'string' && !PAGE_ICON_VALUES.includes(page.icon)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'icon must use one canonical page icon value.',
        `${path}.icon`
      ));
    }
  }
  if (page.kind === 'built-in') {
    validateObjectKeys(pageNode, BUILT_IN_PAGE_KEYS, path, errors);
    const errorCountBeforePage = errors.length;
    validateBuiltInPage(page, path, errors);
    debugValidatorPages({
      operation: 'validate-page',
      kind: 'built-in',
      status: errors.length === errorCountBeforePage ? 'ok' : 'invalid'
    });
    return;
  }

  if (page.kind === 'custom') {
    validateObjectKeys(pageNode, CUSTOM_PAGE_KEYS, path, errors);
    const errorCountBeforePage = errors.length;
    validateCustomPage(page, pageNode, path, errors);
    debugValidatorPages({
      operation: 'validate-page',
      kind: 'custom',
      status: errors.length === errorCountBeforePage ? 'ok' : 'invalid'
    });
    return;
  }

  debugValidatorPages({ operation: 'validate-page', kind: 'unknown', status: 'invalid' });
  validateObjectKeys(pageNode, [...BUILT_IN_PAGE_KEYS, ...CUSTOM_PAGE_KEYS], path, errors);
}

/**
 * @param {unknown} indicator
 * @param {unknown} indicatorNode
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateNavigationIndicator(indicator, indicatorNode, path, errors) {
  if (indicator === undefined) return;
  if (!isPlainObject(indicator)) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'navigation-indicator must be a mapping.', path));
    return;
  }
  validateObjectKeys(indicatorNode, NAVIGATION_INDICATOR_KEYS, path, errors);
  validateStringField(indicator.label, `${path}.label`, true, errors);
  if (!Array.isArray(indicator.any) || indicator.any.length === 0) {
    errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'navigation-indicator any must be a non-empty sequence.', `${path}.any`));
    return;
  }
  indicator.any.forEach((source, index) => {
    validateStringField(source, `${path}.any[${index}]`, true, errors);
  });
}

/**
 * @param {Record<string, unknown>} page
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateBuiltInPage(page, path, errors) {
  validateStringField(page.page, `${path}.page`, true, errors);
  if (typeof page.page === 'string' && !BUILT_IN_PAGE_VALUES.includes(page.page)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'page must use one of the canonical built-in page names.',
      `${path}.page`
    ));
    return;
  }

  if (typeof page.page !== 'string') {
    return;
  }

  if (page.title === '') {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'built-in page title must be a non-empty string when present.',
      `${path}.title`
    ));
  }

  if (errors.some((error) => error.path.startsWith(path))) {
    return;
  }

  validateBuiltInPageContent(
    /** @type {keyof typeof BUILT_IN_PAGE_REQUIRED_SOURCES} */ (page.page),
    page,
    path,
    errors
  );
}

/**
 * @param {keyof typeof BUILT_IN_PAGE_REQUIRED_SOURCES} pageName
 * @param {Record<string, unknown>} page
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateBuiltInPageContent(pageName, page, path, errors) {
  const requiredSources = BUILT_IN_PAGE_REQUIRED_SOURCES[pageName];
  if (!requiredSources) {
    return;
  }

  const definition = page.definition;
  if (!isPlainObject(definition)) {
    for (const sourceName of requiredSources) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `built-in page "${pageName}" requires declarative definitions for source "${sourceName}".`,
        `${path}.definition`
      ));
    }
    return;
  }

  validateBuiltInPageDefinition(pageName, definition, path, errors);
}

/**
 * @param {keyof typeof BUILT_IN_PAGE_REQUIRED_SOURCES} pageName
 * @param {Record<string, unknown>} definition
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateBuiltInPageDefinition(pageName, definition, path, errors) {
  validateBuiltInPageDefinitionKeys(definition, path, errors);
  validateBuiltInPageDataState(definition['data-state'], path, errors);

  if (!Array.isArray(definition.views) || definition.views.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'built-in page definition must contain a non-empty views sequence.',
      `${path}.definition.views`
    ));
    return;
  }

  validatePageSections(
    definition.sections,
    definition.views,
    `${path}.definition.sections`,
    'built-in page definition',
    'definition view',
    errors
  );
  validateProgressiveDisclosure(definition.views, `${path}.definition.views`, errors);
  validateGraphicalLayout(
    definition.views,
    `${path}.definition.views`,
    errors,
    pageName
  );

  /** @type {Map<string, Set<string>>} */
  const sourceFieldCoverage = new Map();
  for (const [index, view] of definition.views.entries()) {
    if (!isPlainObject(view)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'built-in page definition view must be a mapping.',
        `${path}.definition.views[${index}]`
      ));
      continue;
    }

    const viewPath = `${path}.definition.views[${index}]`;
    if (view.chart === 'treemap' || view.treemap !== undefined) {
      validateView(view, undefined, viewPath, new Set(), errors);
    }
    if (view.subject !== undefined) validateStringField(view.subject, `${viewPath}.subject`, true, errors);
    if (view.objective !== undefined) validateStringField(view.objective, `${viewPath}.objective`, true, errors);
    if (view.acceptance !== undefined) validateStringField(view.acceptance, `${viewPath}.acceptance`, true, errors);
    validateSemanticMetadataLength(view, viewPath, errors);

    const data = view.data;
    if (!isPlainObject(data)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'built-in page definition view must contain a data mapping.',
        `${path}.definition.views[${index}].data`
      ));
      continue;
    }
    validateViewFilterBar(view, undefined, viewPath, typeof data.source === 'string' ? data.source : null, errors);

    if (isPlainObject(view.list) && view.list.style === 'entity-cards') {
      if (typeof view.list.card !== 'string' || !state.declaredCardTemplates.has(view.list.card)) {
        errors.push(createError(
          ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
          'list.card must reference a declared dashboard card template.',
          `${viewPath}.list.card`
        ));
      }
      validateListDrill(view.list.drill, undefined, `${viewPath}.list`, view.list.style, errors);
    }
    if (view.element !== undefined && view.mark !== 'element') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'element is allowed only when mark is "element".',
        `${viewPath}.element`
      ));
    }
    if (view.mark === 'element') {
      if (typeof view.element !== 'string' || !VIEW_ELEMENT_VALUES.includes(view.element)) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          'element must use one canonical UI element value.',
          `${viewPath}.element`
        ));
      }
      if (!Array.isArray(data.sources) || data.sources.length === 0) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'element views must declare a non-empty data.sources sequence.',
          `${viewPath}.data.sources`
        ));
        continue;
      }
      if (data.source !== undefined) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'element views must use data.sources instead of data.source.',
          `${viewPath}.data.source`
        ));
      }
      if (view.encoding !== undefined) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'element views must not declare encoding.',
          `${viewPath}.encoding`
        ));
      }
      const seenSources = new Set();
      for (const sourceName of data.sources) {
        if (typeof sourceName !== 'string' || (!QUERY_SOURCE_VALUES.includes(sourceName) && !state.declaredQueries.has(sourceName))) {
          errors.push(createError(
            ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
            'source must name one Section 5.1 database table or one declared query.',
            `${viewPath}.data.sources`
          ));
          continue;
        }
        if (seenSources.has(sourceName)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'sources must not contain duplicate source names.',
            `${viewPath}.data.sources`
          ));
        }
        seenSources.add(sourceName);
        const coverageSources = new Set([sourceName, ...(state.declaredQueryTables.get(sourceName) ?? [])]);
        for (const coverageSource of coverageSources) {
          sourceFieldCoverage.set(coverageSource, new Set(getBuiltInRequiredFields(pageName, coverageSource)));
        }
      }
      continue;
    }

    if (typeof data.source !== 'string') {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'built-in page definition view must contain one canonical data.source.',
        `${path}.definition.views[${index}].data.source`
      ));
      continue;
    }
    validateViewDataArguments(data.arguments, undefined, `${viewPath}.data.arguments`, data.source, errors);

    const coverageSources = new Set([data.source, ...(state.declaredQueryTables.get(data.source) ?? [])]);
    for (const coverageSource of coverageSources) {
      if (!sourceFieldCoverage.has(coverageSource)) {
        sourceFieldCoverage.set(coverageSource, new Set());
      }
      collectBuiltInDefinitionFieldCoverage(view.encoding, sourceFieldCoverage.get(coverageSource));
    }

    validateTableActions(
      view.encoding,
      undefined,
      view.mark,
      data.source,
      `${viewPath}.encoding.actions`,
      errors
    );
  }

  for (const [source, table] of Object.entries(DATABASE_QUERY_SOURCE_TABLES)) {
    const fields = sourceFieldCoverage.get(source);
    if (fields) sourceFieldCoverage.set(table, new Set([...(sourceFieldCoverage.get(table) ?? []), ...fields]));
  }
  for (const sourceName of BUILT_IN_PAGE_REQUIRED_SOURCES[pageName]) {
    if (!sourceFieldCoverage.has(sourceName)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `built-in page "${pageName}" definition must include at least one view for source "${sourceName}".`,
        `${path}.definition.views`
      ));
      continue;
    }

    const requiredFields = getBuiltInRequiredFields(pageName, sourceName);
    const coveredFields = sourceFieldCoverage.get(sourceName) ?? new Set();
    for (const fieldName of requiredFields) {
      if (!coveredFields.has(fieldName)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          `built-in page "${pageName}" definition must expose field "${fieldName}" for source "${sourceName}".`,
          `${path}.definition.views`
        ));
      }
    }
  }
}

/**
 * @param {unknown} sections
 * @param {unknown[]} views
 * @param {string} sectionsPath
 * @param {string} ownerLabel
 * @param {string} viewLabel
 * @param {ValidationError[]} errors
 */
function validatePageSections(sections, views, sectionsPath, ownerLabel, viewLabel, errors) {
  if (sections === undefined) return;
  if (!Array.isArray(sections) || sections.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      `${ownerLabel} sections must be a non-empty sequence.`,
      sectionsPath
    ));
    return;
  }

  const declaredViewIds = views
    .filter(isPlainObject)
    .map((view) => view.id)
    .filter((id) => typeof id === 'string');
  /** @type {string[]} */
  const referencedViewIds = [];
  const referencedViewIdSet = new Set();
  const sectionIds = new Set();

  sections.forEach((section, index) => {
    const sectionPath = `${sectionsPath}[${index}]`;
    if (!isPlainObject(section)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'built-in page layout section must be a mapping.',
        sectionPath
      ));
      return;
    }
    for (const key of Object.keys(section)) {
      if (!PAGE_SECTION_KEYS.includes(key)) {
        errors.push(createError(
          ERROR_CODES.unknownOrDuplicateKey,
          `Unknown key "${key}" is not allowed at ${sectionPath}.`,
          `${sectionPath}.${key}`
        ));
      }
    }
    validateRequiredIdentifier(section.id, `${sectionPath}.id`, 'layout section id', errors);
    if (typeof section.id === 'string') {
      if (sectionIds.has(section.id)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'layout section id must be unique within definition.sections.',
          `${sectionPath}.id`
        ));
      }
      sectionIds.add(section.id);
    }
    validateOptionalStringField(section.title, `${sectionPath}.title`, errors);
    validateOptionalStringField(section.description, `${sectionPath}.description`, errors);
    if (section['count-source'] !== undefined || section['count-sources'] !== undefined
        || section['count-field'] !== undefined || section['count-label'] !== undefined) {
      if (section['count-source'] !== undefined) {
        validateSource(section['count-source'], `${sectionPath}.count-source`, errors);
      }
      if (section['count-sources'] !== undefined) {
        validateSourceSequence(section['count-sources'], `${sectionPath}.count-sources`, errors);
      }
      if ((section['count-source'] === undefined) === (section['count-sources'] === undefined)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'layout section count summary must declare exactly one of count-source or count-sources.',
          sectionPath
        ));
      }
      if (section['count-sources'] !== undefined && section['count-field'] === undefined) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'count-field is required when count-sources is declared.',
          `${sectionPath}.count-field`
        ));
      }
      if (section['count-field'] !== undefined) {
        const countField = section['count-field'];
        validateRequiredIdentifier(countField, `${sectionPath}.count-field`, 'section count field', errors);
        for (const source of Array.isArray(section['count-sources']) ? section['count-sources'] : []) {
          const fields = typeof source === 'string' ? sourceFieldNames(source) : undefined;
          if (typeof countField === 'string' && fields && !fields.includes(countField)) {
            errors.push(createError(
              ERROR_CODES.invalidScopeFilterTimeAggregationOrOrderReference,
              'count-field must be declared by every count-sources entry.',
              `${sectionPath}.count-field`
            ));
            break;
          }
        }
      }
      validateStringField(section['count-label'], `${sectionPath}.count-label`, true, errors);
    }
    if (typeof section.layout !== 'string' || !PAGE_SECTION_LAYOUT_VALUES.includes(section.layout)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'layout section must use one canonical full, wide, narrow, or horizontal layout value.',
        `${sectionPath}.layout`
      ));
    }
    if (!Array.isArray(section.views) || section.views.length === 0) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'layout section must reference at least one view.',
        `${sectionPath}.views`
      ));
      return;
    }
    section.views.forEach((viewId, viewIndex) => {
      const viewPath = `${sectionPath}.views[${viewIndex}]`;
      if (typeof viewId !== 'string' || !declaredViewIds.includes(viewId)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          `layout section view must reference a declared ${viewLabel} id.`,
          viewPath
        ));
        return;
      }
      if (referencedViewIdSet.has(viewId)) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          `each ${viewLabel} may appear in only one layout section.`,
          viewPath
        ));
      }
      referencedViewIds.push(viewId);
      referencedViewIdSet.add(viewId);
    });
  });

  if (declaredViewIds.join('\0') !== referencedViewIds.join('\0')) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      `layout sections must reference every ${viewLabel} exactly once and preserve view order.`,
      sectionsPath
    ));
  }
}

/**
 * @param {keyof typeof BUILT_IN_PAGE_REQUIRED_FIELDS} pageName
 * @param {string} sourceName
 * @returns {string[]}
 */
function getBuiltInRequiredFields(pageName, sourceName) {
  const pageFields = BUILT_IN_PAGE_REQUIRED_FIELDS[pageName];
  if (!pageFields || !Object.hasOwn(pageFields, sourceName)) {
    return [];
  }

  return /** @type {string[]} */ (pageFields[/** @type {keyof typeof pageFields} */ (sourceName)]);
}

/**
 * @param {Record<string, unknown>} definition
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateBuiltInPageDefinitionKeys(definition, path, errors) {
  for (const key of Object.keys(definition)) {
    if (!BUILT_IN_PAGE_DEFINITION_KEYS.includes(key)) {
      errors.push(createError(
        ERROR_CODES.unknownOrDuplicateKey,
        `Unknown key "${key}" is not allowed at ${path}.definition.`,
        `${path}.definition.${key}`
      ));
    }
  }
}

/**
 * @param {unknown} dataState
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateBuiltInPageDataState(dataState, path, errors) {
  const dataStatePath = `${path}.definition.data-state`;
  if (!isPlainObject(dataState)) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'built-in page definition must expose availability state.',
      dataStatePath
    ));
    return;
  }

  for (const key of Object.keys(dataState)) {
    if (!BUILT_IN_PAGE_DATA_STATE_KEYS.includes(key)) {
      errors.push(createError(
        ERROR_CODES.unknownOrDuplicateKey,
        `Unknown key "${key}" is not allowed at ${dataStatePath}.`,
        `${dataStatePath}.${key}`
      ));
    }
  }

  for (const key of BUILT_IN_PAGE_DATA_STATE_KEYS) {
    if (dataState[key] !== true) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        `built-in page definition must expose ${key} state with canonical boolean true.`,
        `${dataStatePath}.${key}`
      ));
    }
  }
}

/**
 * @param {unknown} encoding
 * @param {Set<string> | undefined} coveredFields
 */
function collectBuiltInDefinitionFieldCoverage(encoding, coveredFields) {
  if (!isPlainObject(encoding) || !coveredFields) {
    return;
  }

  for (const channel of ['value', 'x', 'y', 'color', 'href']) {
    collectFieldDefinitionCoverage(encoding[channel], coveredFields);
  }

  if (Array.isArray(encoding.columns)) {
    for (const column of encoding.columns) {
      collectFieldDefinitionCoverage(column, coveredFields);
    }
  }
}

/**
 * @param {unknown} fieldDefinition
 * @param {Set<string>} coveredFields
 */
function collectFieldDefinitionCoverage(fieldDefinition, coveredFields) {
  if (!isPlainObject(fieldDefinition) || typeof fieldDefinition.field !== 'string') {
    return;
  }

  coveredFields.add(fieldDefinition.field);
}

/**
 * Validates the optional tab set of a routed custom page.
 * @param {Record<string, unknown>} route
 * @param {string} routePath
 * @param {ValidationError[]} errors
 */
function validateRouteTabs(route, routePath, errors) {
  if (route.tabs === undefined) {
    if (route.tab !== undefined) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'route tab requires a route tabs sequence.',
        `${routePath}.tab`
      ));
    }
    return;
  }
  if (!Array.isArray(route.tabs) || route.tabs.length === 0 || route.tabs.length > MAX_PAGE_ROUTE_TABS) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      `route tabs must be a sequence of 1 to ${MAX_PAGE_ROUTE_TABS} tabs.`,
      `${routePath}.tabs`
    ));
    return;
  }
  const identifiers = new Set();
  for (const [index, tab] of route.tabs.entries()) {
    const tabPath = `${routePath}.tabs[${index}]`;
    if (!isPlainObject(tab)) {
      errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'route tab must be a mapping.', tabPath));
      continue;
    }
    for (const key of Object.keys(tab)) {
      if (!PAGE_ROUTE_TAB_KEYS.includes(key)) {
        errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, `Unknown key "${key}" is not allowed at ${tabPath}.`, `${tabPath}.${key}`));
      }
    }
    validateRequiredIdentifier(tab.id, `${tabPath}.id`, 'route tab id', errors);
    validateRequiredIdentifier(tab.page, `${tabPath}.page`, 'route tab page', errors);
    validateStringField(tab.label, `${tabPath}.label`, true, errors);
    validateStringField(tab.icon, `${tabPath}.icon`, true, errors);
    if (typeof tab.icon === 'string' && !PAGE_ICON_VALUES.includes(tab.icon)) {
      errors.push(createError(
        ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
        'route tab icon must use one canonical Octicon name.',
        `${tabPath}.icon`
      ));
    }
    if (typeof tab.id === 'string') {
      if (identifiers.has(tab.id)) {
        errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, 'route tab ids must be unique.', `${tabPath}.id`));
      }
      identifiers.add(tab.id);
    }
  }
  if (route.tab !== undefined && (typeof route.tab !== 'string' || !identifiers.has(route.tab))) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'route tab must name one declared route tab id.',
      `${routePath}.tab`
    ));
  }
}

/**
 * @param {Record<string, unknown>} page
 * @param {unknown} pageNode
 * @param {string} path
 * @param {ValidationError[]} errors
 */
function validateCustomPage(page, pageNode, path, errors) {
  if (page.title === undefined && typeof page.id === 'string' && !IDENTIFIER_PATTERN.test(page.id)) {
    errors.push(createError(
      ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      'custom page title default requires a canonical page id.',
      `${path}.id`
    ));
  }

  if (page.route !== undefined) {
    const routePath = `${path}.route`;
    if (!isPlainObject(page.route)) {
      errors.push(createError(
        ERROR_CODES.missingOrInvalidRequiredField,
        'route must be a mapping.',
        routePath
      ));
    } else {
      validateObjectKeys(getValueNodeByKey(pageNode, 'route'), PAGE_ROUTE_KEYS, routePath, errors);
      if (page.route['hash-query-parameter'] === undefined && page.route['navigation-page'] === undefined && page.route.tabs === undefined && page.route.breadcrumbs === undefined) {
        errors.push(createError(
          ERROR_CODES.missingOrInvalidRequiredField,
          'route must declare hash-query-parameter, navigation-page, tabs, or breadcrumbs.',
          routePath
        ));
      }
      if (page.route['hash-query-parameter'] !== undefined) {
        validateRequiredIdentifier(
          page.route['hash-query-parameter'],
          `${routePath}.hash-query-parameter`,
          'route hash query parameter',
          errors
        );
      }
      if (page.route['navigation-page'] !== undefined) {
        validateRequiredIdentifier(
          page.route['navigation-page'],
          `${routePath}.navigation-page`,
          'route navigation page',
          errors
        );
      }
      if (page.route.breadcrumbs !== undefined) {
        const breadcrumbs = page.route.breadcrumbs;
        if (!Array.isArray(breadcrumbs) || breadcrumbs.length === 0 || breadcrumbs.length > MAX_PAGE_ROUTE_TABS) {
          errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
            `route breadcrumbs must be a sequence of 1 to ${MAX_PAGE_ROUTE_TABS} entries.`, `${routePath}.breadcrumbs`));
        } else {
          const breadcrumbPages = new Set();
          for (const [index, breadcrumb] of breadcrumbs.entries()) {
            const breadcrumbPath = `${routePath}.breadcrumbs[${index}]`;
            if (!isPlainObject(breadcrumb)) {
              errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField, 'route breadcrumb must be a mapping.', breadcrumbPath));
              continue;
            }
            for (const key of Object.keys(breadcrumb)) {
              if (!['page', 'parameters'].includes(key)) {
                errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, `Unknown route breadcrumb key "${key}".`, `${breadcrumbPath}.${key}`));
              }
            }
            validateRequiredIdentifier(breadcrumb.page, `${breadcrumbPath}.page`, 'breadcrumb page', errors);
            if (breadcrumbPages.has(breadcrumb.page)) {
              errors.push(createError(ERROR_CODES.unknownOrDuplicateKey, 'route breadcrumb pages must be unique.', `${breadcrumbPath}.page`));
            }
            breadcrumbPages.add(breadcrumb.page);
            if (breadcrumb.parameters !== undefined) {
              if (!Array.isArray(breadcrumb.parameters) || breadcrumb.parameters.length === 0
                || breadcrumb.parameters.length > MAX_PAGE_ROUTE_TABS || new Set(breadcrumb.parameters).size !== breadcrumb.parameters.length) {
                errors.push(createError(ERROR_CODES.missingOrInvalidRequiredField,
                  'breadcrumb parameters must be a non-empty sequence of unique route parameter names.', `${breadcrumbPath}.parameters`));
              } else {
                for (const [parameterIndex, parameter] of breadcrumb.parameters.entries()) {
                  validateRequiredIdentifier(parameter, `${breadcrumbPath}.parameters[${parameterIndex}]`, 'breadcrumb parameter', errors);
                }
              }
            }
          }
        }
      }
      if (page.route['availability-view'] !== undefined) {
        if (page.route['navigation-page'] === undefined) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'route availability-view requires navigation-page for recovery.',
            `${routePath}.navigation-page`
          ));
        }
        validateRequiredIdentifier(
          page.route['availability-view'],
          `${routePath}.availability-view`,
          'route availability view',
          errors
        );
        const availabilityViewId = page.route['availability-view'];
        if (!Array.isArray(page.views) || !page.views.some((view) => isPlainObject(view) && view.id === availabilityViewId)) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            'route availability-view must reference a view on this page.',
            `${routePath}.availability-view`
          ));
        }
      }
      for (const key of ['availability-message', 'partial-message']) {
        if (page.route[key] !== undefined && (typeof page.route[key] !== 'string' || !page.route[key].trim())) {
          errors.push(createError(
            ERROR_CODES.missingOrInvalidRequiredField,
            `${key} must be a non-empty string when present.`,
            `${routePath}.${key}`
          ));
        }
      }
      const routeTitleFormat = page.route['title-format'];
      if (routeTitleFormat !== undefined
          && (typeof routeTitleFormat !== 'string'
            || !PAGE_ROUTE_TITLE_FORMAT_VALUES.includes(routeTitleFormat))) {
        errors.push(createError(
          ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
          `route title-format must be one of: ${PAGE_ROUTE_TITLE_FORMAT_VALUES.join(', ')}.`,
          `${routePath}.title-format`
        ));
      }
      if (page.route['tabs-class-name'] !== undefined) {
        validateRequiredIdentifier(
          page.route['tabs-class-name'],
          `${routePath}.tabs-class-name`,
          'route tabs class name',
          errors
        );
      }
      validateRouteTabs(page.route, routePath, errors);
    }
  }

  if (!Array.isArray(page.views) || page.views.length === 0) {
    errors.push(createError(
      ERROR_CODES.missingOrInvalidRequiredField,
      'custom pages must contain a non-empty views sequence.',
      `${path}.views`
    ));
    return;
  }

  /** @type {Set<string>} */
  const viewIds = new Set();
  const viewsNode = getValueNodeByKey(pageNode, 'views');
  page.views.forEach((view, index) => {
    validateView(
      view,
      getSequenceItemNode(viewsNode, index),
      `${path}.views[${index}]`,
      viewIds,
      errors
    );
  });
  validateProgressiveDisclosure(page.views, `${path}.views`, errors);
  validatePageSections(page.sections, page.views, `${path}.sections`, 'custom page', 'page view', errors);
  validateGraphicalLayout(
    page.views,
    `${path}.views`,
    errors,
    typeof page.id === 'string' ? page.id : undefined
  );
}
