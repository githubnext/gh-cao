/**
 * Registry for JSON-selected dashboard UI elements.
 */

import { renderEntityRoute } from './entity-route.js';
import { renderFactoryFloorElement } from './factory-floor.js';
import { renderFactoryHeaderElement } from './factory-header.js';
import { renderLinkButtonList } from './link-button-list.js';
import { renderMarkdownElement } from './markdown.js';
import { renderProblemDetail } from './problem-detail.js';
import { renderLazyView } from './lazy-view.js';
import { createFactoryScope } from './factory-elements.js';
import { h } from '../dom.js';
import { createDebug } from '../debug.js';
import { renderCommandTerminal } from './command-terminal.js';
import { renderStorageInfo } from './storage-info.js';

const debugUiElements = createDebug('ui-elements');

/**
 * @typedef {{
 *   pageId: string,
 *   title: string,
 *   description?: string,
 *   sourceNames: string[],
 *   sources: Record<string, import('../presenter.js').LogicalSourceInput>,
 *   contextDetails: string[],
 *   scope?: Record<string, unknown>,
 *   time?: Record<string, unknown>,
 *   routeParameter?: string,
 *   routeParameters?: Record<string, string>,
 *   queryContext?: { filters?: Record<string, string[]>, search?: { fields: string[], query: string }, orderBy?: Array<{ field: string, direction?: 'asc'|'desc' }>, timeWindow?: { start?: string, end?: string }, viewMode?: 'chart'|'table'|'card' },
 *   titleLink?: Record<string, unknown>,
 *   element?: string,
 *   viewId?: string,
 *   viewIndex?: number,
 *   sourcesSubscribed?: boolean,
 *   elementConfig?: { wizard?: import('../prompt-wizard.js').PromptWizard, actions?: Record<string, string>, body?: string, sections?: string[], stations?: string[], section?: string, labels?: Record<string, unknown>, animate?: string, 'browser-first-load'?: boolean, 'data-status'?: boolean, 'view-all-page'?: string, 'view-all-label'?: string, 'label-field'?: string, 'label-badge-field'?: string, 'link-field'?: string, 'icon-field'?: string, 'fallback-icon'?: string, 'indicator-field'?: string, 'indicator-label-field'?: string, 'empty-message'?: string, 'measure-source'?: 'operational-value'|'operational-grader', 'content-field'?: string, 'path-field'?: string, 'base-link-field'?: string },
 *   headingTag: 'h2'|'h3'|'h4'
 * }} ElementRenderContext
 */
export {};

/** @type {Map<string, (context: ElementRenderContext) => HTMLElement | null>} */
const ELEMENT_RENDERERS = new Map([
  ['problem-detail', renderProblemDetail],
  ['entity-route', renderEntityRoute],
  ['factory-header', renderFactoryHeaderElement],
  ['factory-floor', renderFactoryFloorElement],
  ['link-button-list', renderLinkButtonList],
  ['markdown', renderMarkdownElement],
  ['command-terminal', renderCommandTerminal],
  ['storage-info', renderStorageInfo]
]);

/** Elements that load declared sources independently of the active page subscription. */
const ASYNC_SOURCE_ELEMENTS = new Set(['factory-header', 'factory-floor', 'all-campaign-memory', 'link-button-list', 'measure-history']);

/** Elements that retain their DOM while the active page publishes subscribed results. */
const PAGE_BOUND_ELEMENTS = new Set(['problem-detail', 'campaign-route', 'workflow-value-report', 'workflow-value-snapshot']);

/** @param {string} name */
export function elementBindsPageSources(name) {
  return PAGE_BOUND_ELEMENTS.has(name);
}

/**
 * Reports whether an element loads its declared sources on its own.
 * @param {string} name
 * @returns {boolean}
 */
export function elementLoadsSourcesAsync(name) {
  return ASYNC_SOURCE_ELEMENTS.has(name);
}

const EMPTY_AWARE_ELEMENTS = new Set([
  'campaign-route',
  'workflow-route-page',
  'outcome-detail',
  'problem-detail',
  'configuration-policy',
  'entity-route',
  'factory-header',
  'factory-floor',
  'all-campaign-memory',
  'link-button-list',
  'markdown',
  'measure-history',
  'workflow-value-report',
  'workflow-value-snapshot',
  'command-terminal',
  'storage-info',
  'environment-defaults',
  'prompt-wizard'
]);
const UNAVAILABLE_AWARE_ELEMENTS = new Set(['configuration-policy', 'problem-detail', 'campaign-route', 'workflow-value-report', 'workflow-value-snapshot']);

/**
 * Builds a lazy element renderer that dynamically imports a module on first
 * use and delegates rendering to it, avoiding an eagerly bundled dependency
 * for elements that are not always present on a page.
 * @template {Record<string, unknown>} Module
 * @param {() => Promise<Module>} importModule
 * @param {(module: Module, context: ElementRenderContext) => HTMLElement | null} render
 * @returns {(context: ElementRenderContext, signal?: AbortSignal) => Promise<HTMLElement | null>}
 */
function lazyElementRenderer(importModule, render) {
  /** @type {Promise<Module> | undefined} */
  let module;
  return async (context, signal) => {
    const loaded = await (module ??= importModule());
    signal?.throwIfAborted();
    return render(loaded, context);
  };
}

/** @type {Map<string, (context: ElementRenderContext, signal?: AbortSignal) => Promise<HTMLElement | null>>} */
const LAZY_ELEMENT_RENDERERS = new Map([
  ['prompt-wizard-lazy', lazyElementRenderer(
    () => import('./prompt-wizard.js'),
    ({ renderPromptWizard }, context) => renderPromptWizard(context)
  )],
  ['workflow-value-report-lazy', lazyElementRenderer(
    () => import('./workflow-value-report.js'),
    ({ renderWorkflowValueReport }, context) => renderWorkflowValueReport(context)
  )],
  ['workflow-value-snapshot-lazy', lazyElementRenderer(
    () => import('./workflow-value-report.js'),
    ({ renderWorkflowValueSnapshot }, context) => renderWorkflowValueSnapshot(context)
  )],
  ['environment-defaults-lazy', lazyElementRenderer(
    () => import('./environment-defaults.js'),
    ({ renderEnvironmentDefaults }, context) => renderEnvironmentDefaults(context)
  )],
  ['configuration-policy-lazy', lazyElementRenderer(
    () => import('./configuration-view.js'),
    ({ renderConfigurationView }, context) => renderConfigurationView(context)
  )],
  ['all-campaign-memory-lazy', lazyElementRenderer(
    () => import('./campaign-memory.js'),
    ({ renderAllCampaignMemory }, context) => renderAllCampaignMemory(context)
  )],
  ['measure-history-lazy', lazyElementRenderer(
    () => import('./measure-history.js'),
    ({ renderBoundMeasureHistory }, context) => renderBoundMeasureHistory(context)
  )],
  ['outcome-detail-lazy', lazyElementRenderer(
    () => import('./outcome-detail.js'),
    ({ renderOutcomeDetail }, context) => renderOutcomeDetail(context)
  )],
  ['outcome-detail-section-lazy', lazyElementRenderer(
    () => import('./outcome-detail-sections.js'),
    ({ isOutcomeDetailSectionConfig, renderOutcomeDetailSection }, context) => {
      const outcome = context.sources[context.sourceNames[0]]?.rows[0];
      return outcome && isOutcomeDetailSectionConfig(context.elementConfig)
        ? renderOutcomeDetailSection(outcome, context.elementConfig.body) : null;
    }
  )],
  ['campaign-route-lazy', lazyElementRenderer(
    () => import('./campaign-route-view.js'),
    ({ renderCampaignRouteView }, context) => renderCampaignRouteView(context)
  )],
  ['workflow-route-page-lazy', lazyElementRenderer(
    () => import('./workflow-route-page.js'),
    ({ renderWorkflowRoutePage }, context) => renderWorkflowRoutePage(context)
  )]
]);

/**
 * @param {string} name
 * @param {ElementRenderContext} context
 * @returns {HTMLElement | null}
 */
export function renderUiElement(name, context) {
  const lazyRenderer = LAZY_ELEMENT_RENDERERS.get(`${name}-lazy`);
  if (lazyRenderer) {
    const scope = createFactoryScope();
    const root = h('div', { className: 'dashboard-lazy-element' }, renderLazyView({
      label: context.title,
      headingLevel: context.headingTag,
      render: async () => {
        const element = await renderUiElementAsync(name, context, scope.signal);
        if (!element) throw new Error(`UI element "${name}" could not be rendered.`);
        return element;
      }
    }));
    scope.bind(root);
    return root;
  }
  const renderer = ELEMENT_RENDERERS.get(name);
  if (!renderer) {
    debugUiElements({ event: 'unregistered-element', name });
    return null;
  }
  return renderer({ ...context, element: name }) ?? null;
}

/**
 * @param {string} name
 * @param {ElementRenderContext} context
 * @param {AbortSignal} [signal]
 * @returns {Promise<HTMLElement | null>}
 */
export async function renderUiElementAsync(name, context, signal) {
  const lazyRenderer = LAZY_ELEMENT_RENDERERS.get(`${name}-lazy`);
  if (lazyRenderer) {
    debugUiElements({ event: 'lazy-render-started', name });
    const started = globalThis.performance?.now() ?? Date.now();
    const element = await lazyRenderer({ ...context, element: name }, signal);
    const durationMs = Math.round((globalThis.performance?.now() ?? Date.now()) - started);
    debugUiElements({ event: 'lazy-render-completed', name, durationMs, rendered: element != null });
    return element;
  }
  return renderUiElement(name, context);
}

/**
 * @param {string} name
 * @returns {boolean}
 */
export function elementHandlesEmptyRows(name) {
  return EMPTY_AWARE_ELEMENTS.has(name);
}

/**
 * @param {string} name
 * @returns {boolean}
 */
export function elementHandlesUnavailableSource(name) {
  return UNAVAILABLE_AWARE_ELEMENTS.has(name);
}
