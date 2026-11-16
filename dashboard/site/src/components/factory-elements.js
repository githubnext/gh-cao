/**
 * Shared data adapters for the declaratively composed factory elements.
 */

import { batch } from '../reactive.js';
import { publishSource, requestSource, sourceState } from '../source-store.js';
import { dashboardViewAliasName } from '../data/queries/view-payload-compiler.js';
import { createDebug } from '../debug.js';

const debugFactoryElements = createDebug('factory-elements');

/** @typedef {Record<string, unknown>} Row */
/** @typedef {{ rows: () => Row[], source: () => LogicalSourceInput | undefined, pending: () => boolean, empty: () => boolean, unavailable: () => boolean }} SourceBinding */
/** @typedef {import('../presenter.js').LogicalSourceInput} LogicalSourceInput */
/** @typedef {Record<string, SourceBinding>} SourceBindings */
/**
 * Resolves the actual source name bound to each canonical metric role,
 * applying any `config.sources` overrides declared on the view.
 * @param {Record<string, string>} defaults
 * @param {Record<string, unknown> | undefined} config
 * @returns {Record<string, string>}
 */
export function resolveFactorySourceNames(defaults, config) {
  const overrides = config && typeof config === 'object' && config.sources && typeof config.sources === 'object'
    ? /** @type {Record<string, unknown>} */ (config.sources)
    : {};
  return Object.fromEntries(Object.entries(defaults).map(([role, defaultName]) => {
    const override = overrides[role];
    return [role, typeof override === 'string' && override ? override : defaultName];
  }));
}

/**
 * Binds each declared query independently so the element can render before all
 * worker results settle and update only the widgets that consume each result.
 * @param {Record<string, import('../presenter.js').LogicalSourceInput>} sources
 * @param {string[]} names
 * @param {{ pageId?: string, viewId?: string, viewIndex?: number, sourceNames?: string[], routeParameters?: Record<string, string>, queryContext?: import('./ui-elements.js').ElementRenderContext['queryContext'] }} [request]
 * @param {{ refreshViewSources?: boolean, requestMissingSources?: boolean, preserveSubscribedSources?: boolean, bindingScope?: string }} [options]
 * @returns {SourceBindings}
 */
export function bindFactorySources(sources, names, request, options) {
  let requestedCount = 0;
  batch(() => {
    for (const [sourceIndex, name] of names.entries()) {
      const source = sources[name];
      const declaredSourceIndex = request?.sourceNames?.indexOf(name) ?? -1;
      const effectiveSourceIndex = declaredSourceIndex >= 0 ? declaredSourceIndex : sourceIndex;
      const viewKey = request?.pageId && request.viewId
        ? dashboardViewAliasName(request.pageId, { id: request.viewId }, request.viewIndex ?? 0, name, effectiveSourceIndex)
        : name;
      const bindingKey = options?.bindingScope ? `${viewKey}:${encodeURIComponent(options.bindingScope)}` : viewKey;
      const entry = sourceState(bindingKey);
      const status = entry.get().status;
      const settledSubscription = options?.preserveSubscribedSources === true && (status === 'ready' || status === 'failed');
      if (source && Array.isArray(source.rows) && !settledSubscription) publishSource(name, source, bindingKey);
      if (!source && options?.requestMissingSources === false) {
        if (entry.get().status === 'idle') entry.set({ status: 'loading', origin: 'view', source: null });
      } else if (!source || options?.refreshViewSources === true) {
        requestedCount += 1;
        requestSource(name, {
          ...request,
          sourceIndex: effectiveSourceIndex,
          bindingKey,
          refreshViewSource: options?.refreshViewSources === true
        });
      }
    }
  });
  debugFactoryElements({
    event: 'bound',
    pageId: request?.pageId,
    viewId: request?.viewId,
    sourceCount: names.length,
    requestedCount,
    refresh: options?.refreshViewSources === true
  });
  return Object.fromEntries(names.map((name) => {
    const declaredSourceIndex = request?.sourceNames?.indexOf(name) ?? -1;
    const effectiveSourceIndex = declaredSourceIndex >= 0 ? declaredSourceIndex : names.indexOf(name);
    const viewKey = request?.pageId && request.viewId
      ? dashboardViewAliasName(request.pageId, { id: request.viewId }, request.viewIndex ?? 0, name, effectiveSourceIndex)
      : name;
    const bindingKey = options?.bindingScope ? `${viewKey}:${encodeURIComponent(options.bindingScope)}` : viewKey;
    const entryState = sourceState(bindingKey);
    return [name, {
      rows: () => entryState.get().source?.rows ?? [],
      source: () => entryState.get().source ?? undefined,
      pending: () => entryState.get().status === 'loading',
      empty: () => entryState.get().source?.metadata?.availability === 'empty',
      unavailable: () => {
        const entry = entryState.get();
        return entry.status === 'failed' || entry.source?.metadata?.availability === 'unavailable';
      }
    }];
  }));
}

/**
 * Renders one JSON-selected factory element from its declared query payloads,
 * sharing the common role-resolution, source-binding, and abort-scope wiring
 * that every factory element entry point (header, floor, ...) otherwise
 * repeats.
 * @param {import('./ui-elements.js').ElementRenderContext} context
 * @param {Record<string, string>} defaultSources
 * @param {(roleNames: Record<string, string>) => string[]} selectSourceNames
 * @param {(sources: SourceBindings, scope: ReturnType<typeof createFactoryScope>, roleNames: Record<string, string>) => HTMLElement} render
 * @returns {HTMLElement}
 */
export function renderFactoryElement(context, defaultSources, selectSourceNames, render) {
  const roleNames = resolveFactorySourceNames(defaultSources, context.elementConfig);
  const names = selectSourceNames(roleNames);
  const subscribed = new Set(context.subscribedSourceNames ?? []);
  const request = {
    pageId: context.pageId,
    viewId: context.viewId,
    viewIndex: context.viewIndex,
    sourceNames: context.sourceNames,
    routeParameters: context.routeParameters,
    queryContext: context.queryContext
  };
  const sources = {
    ...bindFactorySources(
      context.sources,
      names.filter((name) => subscribed.has(name)),
      request,
      { requestMissingSources: false, preserveSubscribedSources: true }
    ),
    ...bindFactorySources(
      context.sources,
      names.filter((name) => !subscribed.has(name)),
      request
    )
  };
  const scope = createFactoryScope();
  const rendered = render(sources, scope, roleNames);
  scope.bind(rendered);
  return rendered;
}

/**
 * Creates an abort-scoped lifetime for one independently loaded element.
 * @param {{ signal?: AbortSignal }} [options]
 */
export function createFactoryScope({ signal } = {}) {
  const lifetime = new AbortController();
  if (signal?.aborted) {
    lifetime.abort();
  } else {
    signal?.addEventListener('abort', () => lifetime.abort(), { once: true, signal: lifetime.signal });
  }
  return {
    signal: lifetime.signal,
    /** @param {Element} element */
    bind(element) {
      if (typeof MutationObserver !== 'function') return;
      let wasConnected = element.isConnected;
      const observer = new MutationObserver((records) => {
        if (element.isConnected) {
          wasConnected = true;
        } else if (wasConnected || records.some((record) => (
          [...record.addedNodes].some((node) => node === element || (node instanceof Element && node.contains(element)))
        ))) {
          lifetime.abort();
        }
      });
      observer.observe(element.ownerDocument, { childList: true, subtree: true });
      lifetime.signal.addEventListener('abort', () => {
        observer.disconnect();
        debugFactoryElements({ event: 'scope-aborted' });
      }, { once: true });
    }
  };
}
