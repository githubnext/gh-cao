/**
 * Presenter for JSON-driven dashboard pages using GitHub Primer styling and elements.
 */

import { h } from './dom.js';
import { batch, effect, state, untracked } from './reactive.js';
import { getPrimerStyles } from './styles.js';
import { octicon } from './octicons.js';
import { renderDataStateMetrics } from './components/data-state.js';
import { titleCase } from './components/count-formatters.js';
import { formatMediumUtcDateTime, renderDashboardViewSkeleton, renderEmptyMessage } from './components/ui-primitives.js';
import { customViewAvailabilityMessage, renderCustomViewStateDetails, renderLayoutSectionChrome, renderPageSection, renderViewDisclosure } from './components/view-chrome.js';
import { externalAnchorAttrs, findLink } from './components/link-content.js';
import { elementBindsPageSources, elementHandlesEmptyRows, elementHandlesUnavailableSource, elementLoadsSourcesAsync, renderUiElement } from './components/ui-elements.js';
import { publishSource, sourceState } from './source-store.js';
import { renderDataView, renderPromptPreviewAction, supportsIncrementalChartContinuation } from './components/data-view.js';
import { renderPageLoadError } from './components/page-load-error.js';
import { declaredAgentTaskActionId } from './components/cli-actions.js';
import { effectiveViewSemantics } from './view-semantics.js';
import { semanticViewPrompt } from './semantic-view-prompt.js';
import { normalizeViewAction } from './action-model.js';
import { enableHorizonOutsideClickDismissal, renderFilterBar, renderViewModeControl, setTimeWindowFilter, setTimeWindowRange } from './components/filter-bar.js';
import { renderDashboardForm } from './components/dashboard-form.js';
import { bindSiteCallouts } from './components/site-callout.js';
import { renderDashboardHorizon } from './components/dashboard-horizon.js';
import { restoreDashboardTheme } from './components/theme-settings.js';
import { disconnectLazyViews, enableLazyViews, renderLazyView } from './components/lazy-view.js';
import { dispatchPageRoute } from './components/page-route.js';
import { enableFullViewScrollForwarding, syncFullViewMode as syncFullViewModeForPage } from './components/full-view-scroll.js';
import { DASHBOARD_RENDER_EVENT, emitDashboardDebugEvent } from './debug-events.js';
import { dashboardFormDefaultValues, dashboardViewAliasName } from './data/queries/view-payload-compiler.js';
import { dashboardHorizonHours, formatDashboardHorizon, formatDashboardHorizonHours, resolveDashboardHorizon } from './horizon.js';
import { sourceContinuation } from './data/continuation.js';
import { renderDashboardNavigation, enableDashboardNavigation, syncDashboardNavigationIndicators, syncDashboardNavigationAvailability, syncMobileViewModeToggle } from './components/dashboard-navigation.js';
import { renderDashboardHeader, renderRouteBreadcrumbs } from './components/dashboard-header.js';
import { renderAccountMenu } from './components/account-menu.js';
import { renderDashboardFooter } from './components/dashboard-footer.js';
import { renderDashboardFrame } from './components/dashboard-frame.js';
import { declaredRouteTabs, renderDeclaredRouteTabs, renderPageTabs } from './components/route-tabs.js';
import { buildChartPoints, prepareChartPoints, prepareTableRows, toViewText } from './components/view-data.js';
import { restoreCardListPagination } from './components/card-list-pagination.js';
import { enableDashboardKeyboardNavigation, updateWithViewTransition } from './components/dashboard-interactions.js';
import { enablePullRefresh } from './components/pull-refresh.js';
import { resolveModeIndicator } from './components/mode-indicator.js';
import { createDebug } from './debug.js';
import { navigationIndicatorSourceNames } from './navigation-indicator.js';
import { dashboardDataBackend, pageCapabilityAvailable, pageRequiredSource, pageSourceAvailable, viewBackendAvailable, viewBackendHidden, viewBackendUnavailableMessage } from './view-availability.js';
import { dashboardViewSourceNames as getViewSources, normalizeViewFilters, viewDataSourceNames, viewFilterControls, viewFilterSourceNames } from './view-filter-contract.js';
import { captureCardFilterState, renderCardFilterBar, restoreCardFilterState } from './components/card-filter-bar.js';

export { enableDashboardKeyboardNavigation, updateWithViewTransition };

const debugPerformance = createDebug('render:performance');
const debugNavigation = createDebug('render:navigation');
const debugRender = createDebug('render');
const monotonicNow = () => globalThis.performance?.now() ?? Date.now();
import {
  dashboardPageLazySourceNames as collectDashboardPageLazySourceNames,
  dashboardPageIsLoaded,
  dashboardPagePayload,
  dashboardPageSourcesAreIndependentlyBound,
  dashboardPageSourceNames as collectDashboardPageSourceNames,
  dashboardTableSourceNames as collectDashboardTableSourceNames,
} from './dashboard-chunks.js';

/**
 * @typedef {{ availability: 'available'|'empty'|'unavailable', completeness: 'complete'|'partial'|'unknown', freshness: 'fresh'|'stale'|'unknown' }} DataState
 */

/**
 * @typedef {{ 'source-id': string, 'source-kind': string, 'as-of': string, 'retrieved-at': string, 'coverage-start'?: string, 'coverage-end'?: string, completeness: DataState['completeness'], freshness: DataState['freshness'], availability?: DataState['availability'] } & Record<string, unknown>} SourceMetadata
 */

/**
 * @typedef {{ source: string, rows: Array<Record<string, unknown>>, metadata: SourceMetadata, continuationToken?: string, loadContinuation?: (token: string) => Promise<LogicalSourceInput> }} LogicalSourceInput
 */

/**
 * @typedef {{ id: string, title?: string, description?: string, layout: 'full'|'wide'|'narrow'|'horizontal', views: string[], ['count-source']?: string, ['count-sources']?: string[], ['count-field']?: string, ['count-label']?: string }} PresentablePageSection
 */

/**
 * @typedef {{ id: string, kind: 'built-in', page: string, title?: string, ['navigation-label']?: string, ['navigation-indicator']?: Record<string, unknown>, description?: string, requires?: { capability?: 'cli-queue'|'admin', source?: string, message?: string }, icon?: string, ['class-name']?: string, experimental?: boolean, ['filter-bar']?: boolean, ['view-mode-control']?: boolean, ['pull-refresh']?: boolean, ['mode-indicator']?: boolean, ['query-binding']?: 'view'|'page', form?: Record<string, unknown>, chunk?: string, ['source-names']?: string[], ['lazy-source-names']?: string[], ['table-source-names']?: string[], definition?: { views?: Array<unknown>, sections?: PresentablePageSection[], ['data-state']?: Record<string, boolean> } }} PresentableBuiltInPage
 */

/**
 * @typedef {{ id: string, kind: 'custom', title?: string, ['navigation-label']?: string, ['navigation-indicator']?: Record<string, unknown>, description?: string, requires?: { capability?: 'cli-queue'|'admin', source?: string, message?: string }, icon?: string, ['class-name']?: string, experimental?: boolean, ['filter-bar']?: boolean, ['view-mode-control']?: boolean, ['pull-refresh']?: boolean, ['mode-indicator']?: boolean, ['retain-on-navigation']?: boolean, ['query-binding']?: 'view'|'page', form?: Record<string, unknown>, chunk?: string, ['source-names']?: string[], ['lazy-source-names']?: string[], ['table-source-names']?: string[], route?: { ['hash-query-parameter']?: string, ['navigation-page']?: string, ['availability-view']?: string, ['availability-message']?: string, ['partial-message']?: string, ['title-format']?: 'title-case', breadcrumbs?: import('./components/dashboard-header.js').RouteBreadcrumb[] }, views: unknown[], sections?: PresentablePageSection[] }} PresentableCustomPage
 */

/**
 * @typedef {{ field: string, aggregate?: string, as?: string, direction?: string, display?: string } & Record<string, unknown>} TableField
 */

/**
 * @typedef {{ label?: string, pages?: string[], experimental?: boolean, placement?: 'bottom' }} PresentableNavigationSection
 */

/**
 * @typedef {{ id: string, title: string, description?: string, defaults?: Record<string, unknown>, units?: Record<string, { name: string, symbol: string, significant: number }>, queries?: Array<Record<string, unknown>>, views?: Array<Record<string, unknown>>, ['card-templates']?: Array<{ id: string, icon: string, title: TableField, subtitle?: TableField, labels: TableField[], details: TableField[] }>, callouts?: import('./components/site-callout.js').SiteCallout[], ['cli-actions']?: Array<{ id: string, label: string, description?: string, icon: string, command: string, 'preview-command'?: string, placement?: 'toolbar'|'settings'|'view'|'row', arguments?: Array<{ id: string, label: string, description?: string, type: 'boolean', flag: string, default?: boolean }> }>, pages: Array<PresentableBuiltInPage | PresentableCustomPage>, ['github-url-base']?: string, repository?: string, navigation?: PresentableNavigationSection[], horizon?: { label: string, tooltip: { label: string, description: string, icon?: string } } }} PresentableDashboard
 */

/**
 * @typedef {{ languageVersion: string, dashboard: PresentableDashboard & { ['data-status']?: import('./dashboard-data-status.js').DashboardStatusConfiguration } }} PresentationDocument
 */

/**
 * @typedef {{ signal: AbortSignal, onUpdate: (sources: Record<string, LogicalSourceInput>) => void, syncPageChrome?: () => void, routeParameters?: Record<string, string>, queryContext?: import('./data/queries/view-payload-compiler.js').GlobalQueryContext }} PageSourceLoadOptions
 */

/**
 * @typedef {((pageId: string, options: PageSourceLoadOptions) => Promise<Record<string, LogicalSourceInput>>) & { prepare?: (pageId: string) => Promise<void>, subscribeBackgroundSources?: (sourceNames: string[], options: PageSourceLoadOptions) => Promise<Record<string, LogicalSourceInput>>, subscribeViewSources?: (pageId: string, viewId: string, sourceNames: string[], options: PageSourceLoadOptions) => Promise<Record<string, LogicalSourceInput>> }} PageSourceLoader
 */

/**
 * @typedef {{ document: PresentationDocument, sources: Record<string, LogicalSourceInput>, commitSha?: string | null, caoVersion?: string | null, ghAwVersion?: string | null, prepared?: boolean, loading?: boolean, tableRowLimit?: number, loadPageSources?: PageSourceLoader }} PresentationInput
 */

/**
 * @typedef {'organization-link'|'repository-link'|'workflow-link'|'issue-link'|'pull-request-link'|'run-link'|'evidence-link'|'external-link'} LinkFieldName
 */

const DEFAULT_GITHUB_URL_BASE = 'https://github.com';
const TABLE_ROW_LIMIT = Symbol('table-row-limit');
const NAVIGATION_INDEX_STATE_KEY = 'centralAgenticOpsNavigationIndex';
const SCROLL_TOP_STATE_KEY = 'centralAgenticOpsScrollTop';
const SCROLL_PAGE_ID_STATE_KEY = 'centralAgenticOpsScrollPageId';
const SCROLL_PERSISTENCE_DELAY_MS = 250;
/** @type {WeakMap<HTMLElement, () => void>} */
const dashboardDisposals = new WeakMap();

/** @param {unknown} value */
function normalizeScrollTop(value) {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined;
}
/**
 * @param {PresentableBuiltInPage | PresentableCustomPage} page
 * @param {Array<Record<string, unknown>>} [reusableViews]
 * @returns {PresentableCustomPage}
 */
function getBuiltInPagePayload(page, reusableViews = []) {
  return /** @type {PresentableCustomPage} */ (dashboardPagePayload(page, reusableViews));
}

/**
 * @param {PresentationDocument} document
 * @param {string} pageId
 * @param {'chart'|'table'|'card'} [viewMode]
 * @returns {string[]}
 */
export function dashboardPageSourceNames(document, pageId, viewMode) {
  return collectDashboardPageSourceNames(document, pageId, viewMode);
}

/**
 * Returns sources rendered by lazy-list views without exposing pagination in
 * the dashboard query language.
 * @param {PresentationDocument} document
 * @param {string} pageId
 */
export function dashboardPageLazySourceNames(document, pageId) {
  return collectDashboardPageLazySourceNames(document, pageId);
}

/**
 * Maps each paginated view alias to the authored source and view that can
 * reproduce it for continuation requests.
 * @param {PresentationDocument} document
 * @param {string} pageId
 * @returns {Record<string, { sourceName: string, viewId: string }>}
 */
export function dashboardPagePaginatedSourceBindings(document, pageId) {
  const page = document.dashboard.pages.find((candidate) => candidate.id === pageId);
  if (!page || !pageCapabilityAvailable(page)) return {};
  const payload = getBuiltInPagePayload(page, document.dashboard.views);
  return Object.fromEntries((payload.views ?? []).flatMap((view, viewIndex) => {
    if (!isPlainObject(view)
        || !viewBackendAvailable(view, dashboardDataBackend())
        || typeof view.id !== 'string'
        || (view['lazy-list'] !== true && !supportsIncrementalChartContinuation(view))) {
      return [];
    }
    return viewDataSourceNames(view).map((sourceName, sourceIndex) => [
      dashboardViewAliasName(pageId, view, viewIndex, sourceName, sourceIndex),
      { sourceName, viewId: view.id }
    ]);
  }));
}

/** @param {PresentationDocument} document */
export function dashboardTableSourceNames(document) {
  return collectDashboardTableSourceNames(document);
}

/**
 * @param {PresentationInput} input
 * @returns {HTMLElement}
 */
export function renderDashboard(input) {
  const { document, sources: rawSources } = input;
  const pages = document.dashboard.pages.filter(pageCapabilityAvailable);
  const cardTemplates = Object.fromEntries((document.dashboard['card-templates'] ?? []).map((template) => [template.id, template]));
  const reusableViews = document.dashboard.views ?? [];
  const horizonRange = resolveDashboardHorizon(document.dashboard);
  const dataHorizon = resolveDataHorizon(rawSources);
  const githubUrlBase = typeof document.dashboard['github-url-base'] === 'string' && document.dashboard['github-url-base'].length > 0
    ? document.dashboard['github-url-base']
    : DEFAULT_GITHUB_URL_BASE;
  const dashboardRepository = typeof document.dashboard.repository === 'string' && document.dashboard.repository.length > 0
    ? document.dashboard.repository
    : null;
  const sources = rawSources;
  const orgName = inferOrganizationName(sources) || 'GitHub';
  const sidebarTitle = dashboardRepository?.split('/').at(-1) || orgName;
  const evaluatedAt = dataHorizon?.end ?? latestRetrievedAt(sources) ?? new Date().toISOString();
  const dashboardDefaults = {
    ...resolveDashboardDefaults(document.dashboard.defaults, horizonRange, evaluatedAt),
    [TABLE_ROW_LIMIT]: input.tableRowLimit
  };
  const dashboardHorizon = renderDashboardHorizon({
    dashboard: document.dashboard,
    initialValue: resolveDashboardHorizonViewModel(rawSources, dashboardDefaults, horizonRange, evaluatedAt),
    formatDate: formatReportDate,
    defaultRange: horizonRange,
    referenceEnd: evaluatedAt,
    onChange: (filters, timeWindow) => {
      root.dispatchEvent(new CustomEvent('dashboard-horizon-filters-change', {
        detail: { filters: Object.fromEntries(filters), timeWindow }
      }));
    }
  });

  const styleEl = h('style', null, getPrimerStyles());
  const skipLink = h('a', { href: '#main-content', className: 'skip-link' }, 'Skip to main content');

  const accountMenu = renderAccountMenu();
  const sidebar = renderDashboardNavigation(pages, sidebarTitle, document.dashboard.navigation);
  const initialPage = pages.find((page) => page.id !== 'configuration') ?? pages[0];
  const overviewPage = pages.find((page) => page.id === 'overview');
  const initialPageHref = initialPage ? `#page-${encodeURIComponent(initialPage.id)}` : '#main-content';
  const dashboardOwner = new AbortController();
  const navigationSources = state(sources);
  const appShell = renderDashboardFrame({
    navigation: sidebar,
    header: renderDashboardHeader({
      title: initialPage ? getPageTitle(initialPage) : '',
      description: initialPage?.description,
      experimental: initialPage?.experimental,
      overviewPageHref: overviewPage ? `#page-${encodeURIComponent(overviewPage.id)}` : initialPageHref,
      dashboardHorizon: dashboardHorizon.element,
      accountMenu,
      githubUrlBase,
      dashboardRepository
    }),
    callouts: bindSiteCallouts(document.dashboard.callouts, sources, input.loadPageSources, dashboardOwner.signal, pages),
    pages: pages.map((page) => renderPagePlaceholder(page)),
    footer: renderDashboardFooter({ evaluatedAt, commitSha: input.commitSha, caoVersion: input.caoVersion, ghAwVersion: input.ghAwVersion, githubUrlBase, dashboardRepository })
  });
  const root = h(
    'div',
    { className: 'dashboard-root' },
    styleEl,
    skipLink,
    appShell
  );
  void enableDashboardDomProvenanceWhenDebugging(root, document).catch((error) => {
    root.dataset.domProvenanceError = String(error?.message ?? error);
  });
  enableDashboardNavigation(root);
  syncDashboardNavigationIndicators(root, pages, sources);
  syncDashboardNavigationAvailability(root, pages, sources);
  restoreDashboardTheme(root);
  enableHorizonOutsideClickDismissal(root, dashboardHorizon.collapse);
  root.addEventListener('dashboard-time-window-change', (event) => {
    if (!(event instanceof CustomEvent)) return;
    setTimeWindowFilter(event.detail?.start, event.detail?.end, root);
  }, { signal: dashboardOwner.signal });
  root.addEventListener('dashboard-time-window-range-change', (event) => {
    if (!(event instanceof CustomEvent)) return;
    setTimeWindowRange(event.detail?.range, root);
  }, { signal: dashboardOwner.signal });
  enableResponsiveReportActions(root, dashboardOwner.signal);
  enableDeclaredPullRefresh(root, pages, dashboardOwner.signal);
  const disposeNavigation = enableDashboardPageNavigation(
    root,
    document.dashboard.title,
    (pageId, options) => {
      const pageStartedAt = monotonicNow();
      let renderCount = 0;
      const pageIndex = document.dashboard.pages.findIndex((candidate) => candidate.id === pageId);
      if (pageIndex < 0) return null;
      const resolvedPage = () => {
        const page = document.dashboard.pages[pageIndex];
        if (!page) throw new Error(`Dashboard page "${pageId}" is not available.`);
        return page;
      };
      const renderPreparedPage = () => {
        const pagePayload = getBuiltInPagePayload(resolvedPage(), reusableViews);
        const initialViewMode = defaultViewMode(pagePayload.views ?? []);
        const effectiveQueryContext = options.queryContext ?? (initialViewMode ? { viewMode: initialViewMode } : undefined);
        options.queryContext = effectiveQueryContext;
        const rendersBeforePageSources = dashboardPageSourcesAreIndependentlyBound(resolvedPage(), reusableViews);
        const liveSources = state(sources);
        const failures = state(/** @type {Record<string, string>} */ ({}));
        const loading = state((input.loading === true || Boolean(input.loadPageSources)) && !rendersBeforePageSources);
        /** @param {Record<string, LogicalSourceInput>} pageSources */
        const updateHorizon = (pageSources) => {
          if (options.signal?.aborted !== true) {
            dashboardHorizon.update(resolveDashboardHorizonViewModel(
              pageSources,
              dashboardDefaults,
              horizonRange,
              evaluatedAt,
              effectiveQueryContext?.timeWindow
            ));
          }
        };
        /** @param {Record<string, LogicalSourceInput>} pageSources */
        const render = (pageSources) => {
          const renderStartedAt = monotonicNow();
          const page = resolvedPage();
          if (!page) throw new Error(`Dashboard page "${pageId}" is not available.`);
          updateHorizon(pageSources);
          const rendered = renderPage(page, pageSources, isPlainObject(document.dashboard.units) ? document.dashboard.units : {}, dashboardDefaults, cardTemplates, reusableViews, effectiveQueryContext, document.dashboard.queries ?? [], {
            sources: liveSources,
            failures,
            loading,
            signal: options.signal
          }, { repository: document.dashboard.repository, githubUrlBase: document.dashboard['github-url-base'] });
          debugPerformance('page render', {
            pageId,
            phase: renderCount++ === 0 ? 'initial' : 'update',
            renderMs: monotonicNow() - renderStartedAt,
            elapsedMs: monotonicNow() - pageStartedAt,
            sourceRows: Object.fromEntries(Object.entries(pageSources).map(([name, source]) => [name, source.rows.length]))
          });
          return rendered;
        };
        if (input.loadPageSources) {
          if (rendersBeforePageSources) {
            return render(sources);
          }
          /** @param {Record<string, LogicalSourceInput>} pageSources @param {boolean} [replace] @param {string[][]} [aliases] */
          const publish = (pageSources, replace = false, aliases = []) => {
            if (options.signal.aborted) return;
            const merged = replace ? pageSources : { ...liveSources.get(), ...pageSources };
            for (const [alias, authored] of aliases) {
              if (pageSources[authored] && !pageSources[alias]) delete merged[alias];
            }
            updateHorizon(merged);
            batch(() => {
              liveSources.set(merged);
            });
          };
          const rendered = render(sources);
          if (input.loadPageSources.subscribeViewSources && pagePayload['query-binding'] !== 'page') {
            const payload = getBuiltInPagePayload(resolvedPage(), reusableViews);
            const bindings = (payload.views ?? []).flatMap((view, index) => {
              if (!isPlainObject(view) || !viewBackendAvailable(view, dashboardDataBackend())
                || (typeof view.element === 'string' && elementLoadsSourcesAsync(view.element))) return [];
              const names = getViewSources(view);
              if (!names.length) return [];
              const viewId = typeof view.id === 'string' ? view.id : `view-${index + 1}`;
              const aliases = names.map((name, sourceIndex) => [dashboardViewAliasName(pageId, view, index, name, sourceIndex), name]);
              return typeof view.element === 'string' && elementBindsPageSources(view.element)
                ? names.map((name, sourceIndex) => ({
                    id: `${viewId}:${sourceIndex}`,
                    viewId,
                    names: [name],
                    aliases: [aliases[sourceIndex]]
                  }))
                : [{ id: viewId, viewId, names, aliases }];
            });
            const remaining = collectDashboardPageSourceNames(document, pageId, effectiveQueryContext?.viewMode)
              .filter((name) => !bindings.some((binding) => binding.names.includes(name)));
            if (remaining.length) bindings.push({ id: 'page-chrome', viewId: 'page-chrome', names: remaining, aliases: [] });
            const subscribeViewSources = input.loadPageSources.subscribeViewSources;
            const subscriptions = bindings.map(({ id, viewId = id, names, aliases }) => {
              /** @param {Record<string, LogicalSourceInput>} next */
              const update = (next) => {
                if (options.signal.aborted) return;
                batch(() => {
                  failures.set((current) => {
                    const remaining = { ...current };
                    delete remaining[id];
                    return remaining;
                  });
                  publish(next, false, aliases);
                });
              };
              return subscribeViewSources(pageId, viewId, names, { ...options, onUpdate: update })
                .then(update).catch((error) => {
                  if (!options.signal.aborted) {
                    if (id !== viewId) {
                      for (const [alias] of aliases) {
                        sourceState(alias).set({ status: 'failed', origin: 'view', source: null });
                      }
                    } else {
                      failures.set((current) => ({
                        ...current,
                        [id]: error instanceof Error ? error.message : String(error)
                      }));
                    }
                  }
                  throw error;
                });
            });
            void Promise.allSettled(subscriptions).then((results) => {
              if (options.signal.aborted) return;
              for (const result of results) {
                if (result.status === 'rejected') debugRender('view source binding failed', {
                  pageId,
                  message: result.reason instanceof Error ? result.reason.message : String(result.reason)
                });
              }
              if (!input.loading) loading.set(false);
            });
          } else {
            options.onUpdate = (pageSources) => batch(() => {
              failures.set({});
              publish(pageSources, true);
            });
            void input.loadPageSources(pageId, options).then(options.onUpdate).catch((error) => {
              if (options.signal.aborted) return;
              const message = error instanceof Error ? error.message : String(error);
              failures.set(Object.fromEntries((pagePayload.views ?? []).flatMap((view) => (
                isPlainObject(view) && typeof view.id === 'string' && getViewSources(view).length
                  ? [[view.id, message]] : []
              ))));
              debugRender('page source binding failed', {
                pageId,
                message
              });
            }).finally(() => {
              if (!options.signal.aborted && !input.loading) loading.set(false);
            });
          }
          return rendered;
        }
        const renderedPage = render(sources);
        /** @param {HTMLElement} rendered */
        const annotate = (rendered) => {
          const page = resolvedPage();
          if (!page) return rendered;
          void annotateLazyPageDomWhenDebugging(root, rendered, page, pageIndex).catch((error) => {
            root.dataset.domProvenanceError = String(error?.message ?? error);
          });
          return rendered;
        };
        return renderedPage instanceof Promise ? renderedPage.then(annotate) : annotate(renderedPage);
      };
      if (!pageSourceAvailable(resolvedPage(), navigationSources.get())) {
        const page = resolvedPage();
        const unavailable = renderPagePlaceholder(page);
        unavailable.removeAttribute('data-page-pending');
        unavailable.dataset.pagePrerequisiteUnavailable = 'true';
        unavailable.append(renderCustomViewState(pageId, getPageTitle(page), pageRequiredSource(page) ?? '', 'unavailable',
          [], 'h2', page.requires?.message ?? 'Required data is unavailable.'));
        return unavailable;
      }
      const page = resolvedPage();
      return input.loadPageSources?.prepare && page && !dashboardPageIsLoaded(page)
        ? input.loadPageSources.prepare(pageId).then(() => {
            if (options.signal.aborted) {
              throw new DOMException('Dashboard page preparation was cancelled.', 'AbortError');
            }
            return renderPreparedPage();
          })
        : renderPreparedPage();
    },
    sidebar.dataset.defaultPageId,
    Boolean(input.loadPageSources),
    new Set((document.dashboard.queries ?? []).flatMap((query) => (
      isPlainObject(query) && typeof query.name === 'string' ? [query.name] : []
    )))
  );
  enableNavigationIndicatorUpdates(root, pages, input.loadPageSources, dashboardOwner.signal, (next) => {
    const changed = pages.filter((page) => pageSourceAvailable(page, navigationSources.get()) !== pageSourceAvailable(page, next))
      .map((page) => page.id);
    navigationSources.set(next);
    syncDashboardNavigationAvailability(root, pages, next);
    if (changed.length) root.dispatchEvent(new CustomEvent('dashboard-page-availability-change', { detail: changed }));
  });
  dashboardDisposals.set(root, () => {
    dashboardOwner.abort();
    disposeNavigation();
    dashboardHorizon.dispose();
  });
  return root;
}

/**
 * @param {HTMLElement} root
 * @param {Array<PresentableBuiltInPage | PresentableCustomPage>} pages
 * @param {PageSourceLoader | undefined} loadPageSources
 * @param {AbortSignal} signal
 * @param {(sources: Record<string, LogicalSourceInput>) => void} onUpdate
 */
function enableNavigationIndicatorUpdates(root, pages, loadPageSources, signal, onUpdate) {
  const sourceNames = navigationIndicatorSourceNames(pages);
  if (!loadPageSources?.subscribeBackgroundSources || sourceNames.length === 0) return;
  if (signal.aborted) return;
  // The loader resolves the first snapshot; later snapshots arrive through onUpdate.
  const update = (/** @type {Record<string, LogicalSourceInput>} */ sources) => {
    if (signal.aborted) return;
    syncDashboardNavigationIndicators(root, pages, sources);
    onUpdate(sources);
  };
  void loadPageSources.subscribeBackgroundSources(sourceNames, {
    signal,
    onUpdate: update
  })
    .then(update)
    .catch((error) => {
      if (signal.aborted || error?.name === 'AbortError') return;
      debugNavigation('indicator update failed', {
        error: error instanceof Error ? error.message : String(error)
      });
    });
}

/**
 * Enables the mobile pull-down gesture while any page declaring
 * `pull-refresh: true` is active and already scrolled to its top.
 * @param {HTMLElement} root
 * @param {Array<PresentableBuiltInPage | PresentableCustomPage>} pages
 * @param {AbortSignal} signal
 */
function enableDeclaredPullRefresh(root, pages, signal) {
  const pullRefreshPageIds = new Set(
    pages.filter((page) => page['pull-refresh'] === true).map((page) => page.id)
  );
  if (pullRefreshPageIds.size === 0) return;
  const scroller = root.querySelector('main.dashboard-prototype');
  const view = root.ownerDocument.defaultView;
  if (!(scroller instanceof HTMLElement) || !view) return;
  enablePullRefresh({
    scroller,
    view,
    isActive: () => [...root.querySelectorAll('.dashboard-page')].some(
      (page) => page instanceof HTMLElement && pullRefreshPageIds.has(page.dataset.pageId ?? '') && !page.hidden
    ),
    signal
  });
}

/** @param {HTMLElement} root */
export function disposeDashboard(root) {
  dashboardDisposals.get(root)?.();
  dashboardDisposals.delete(root);
  for (const page of root.querySelectorAll('.dashboard-page')) {
    if (page instanceof HTMLElement) disconnectLazyViews(page);
  }
}

/**
 * Lazily loads the debug-only DOM provenance module (never bundled into the
 * default dashboard load) and enables it only when `?debug=1` is present in
 * the page URL, so Playwright/agent analysis can opt in without imposing any
 * cost on regular dashboard visits.
 * @param {HTMLElement} root
 * @param {PresentationDocument} document
 */
async function enableDashboardDomProvenanceWhenDebugging(root, document) {
  if (!isDomProvenanceDebugRequested(root)) return;
  const { enableDashboardDomProvenance } = await import('./dom-provenance.js');
  enableDashboardDomProvenance(root, document, getBuiltInPagePayload  );
}

/**
 * Matches a sibling tab without crossing the declared route binding.
 * @param {HTMLElement} page
 * @param {string} pageId
 * @param {URLSearchParams} parameters
 * @returns {HTMLElement | null}
 */
function routeTabForPage(page, pageId, parameters) {
  const tabs = page.querySelector('[data-route-tabs]');
  if (!(tabs instanceof HTMLElement)) return null;
  return [...tabs.querySelectorAll('a')].find((link) => {
    const href = link.getAttribute('href') ?? '';
    const [route, query = ''] = href.split('?');
    if (!route.startsWith('#page-')) return false;
    try {
      return decodeURIComponent(route.slice('#page-'.length)) === pageId
        && queryParametersMatch(new URLSearchParams(query), parameters);
    } catch {
      return false;
    }
  }) ?? null;
}

/**
 * @param {HTMLElement} page
 * @param {string} pageId
 * @param {URLSearchParams} parameters
 * @returns {HTMLElement | null}
 */
function cloneRouteTabsForPage(page, pageId, parameters) {
  const target = routeTabForPage(page, pageId, parameters);
  if (!target) return null;
  const tabs = target.closest('[data-route-tabs]');
  if (!(tabs instanceof HTMLElement)) return null;
  const clone = /** @type {HTMLElement} */ (tabs.cloneNode(true));
  for (const link of clone.querySelectorAll('a')) {
    if (link.getAttribute('href') === target.getAttribute('href')) {
      link.setAttribute('aria-current', 'page');
    } else {
      link.removeAttribute('aria-current');
    }
  }
  return clone;
}

/**
 * @param {URLSearchParams} left
 * @param {URLSearchParams} right
 */
function queryParametersMatch(left, right) {
  /** @param {URLSearchParams} parameters */
  const entries = (parameters) => [...parameters.entries()]
    .sort(([leftKey, leftValue], [rightKey, rightValue]) => (
      leftKey.localeCompare(rightKey) || leftValue.localeCompare(rightValue)
    ));
  return JSON.stringify(entries(left)) === JSON.stringify(entries(right));
}

/**
 * @param {HTMLElement} page
 * @param {HTMLElement | null} routeTabs
 */
function showPageSkeleton(page, routeTabs) {
  const declaredRouteTabs = page.querySelector(':scope > .route-tab-navigation, :scope > [data-route-tabs]');
  const preservedRouteTabs = routeTabs ?? (declaredRouteTabs instanceof HTMLElement ? declaredRouteTabs : null);
  page.replaceChildren(...(preservedRouteTabs ? [preservedRouteTabs, renderPageSkeleton()] : [renderPageSkeleton()]));
  page.setAttribute('aria-busy', 'true');
  page.setAttribute('aria-label', 'Loading view');
}

/**
 * @param {HTMLElement} root
 * @returns {boolean}
 */
function isDomProvenanceDebugRequested(root) {
  const search = root.ownerDocument.defaultView?.location.search ?? '';
  return new URLSearchParams(search).get('debug') === '1';
}

/**
 * @param {HTMLElement} root
 * @param {Element} renderedPage
 * @param {PresentableBuiltInPage | PresentableCustomPage} page
 * @param {number} pageIndex
 */
async function annotateLazyPageDomWhenDebugging(root, renderedPage, page, pageIndex) {
  if (!isDomProvenanceDebugRequested(root)) return;
  const { annotatePageDom } = await import('./dom-provenance.js');
  annotatePageDom(renderedPage, page, pageIndex, getBuiltInPagePayload);
}

/**
 * @param {Record<string, LogicalSourceInput>} sources
 * @returns {string | null}
 */
function inferOrganizationName(sources) {
  for (const source of Object.values(sources)) {
    if (Array.isArray(source?.rows)) {
      for (const row of source.rows) {
        if (typeof row?.organization === 'string' && row.organization.length > 0) {
          return row.organization;
        }
      }
    }
  }
  return null;
}

/**
 * Keeps global dashboard controls inside the mobile hamburger menu while
 * preserving the single control instances and their filter-bar event
 * relationships. On narrow viewports the page title moves into the compact
 * mobile header row (replacing the app brand) so the page
 * no longer shows a full-width secondary header that repeats the current page
 * title, matching the title bar used by the GitHub mobile app. The factory name
 * stays visible as a secondary line below that page title.
 * @param {HTMLElement} root
 * @param {AbortSignal} signal
 */
function enableResponsiveReportActions(root, signal) {
  const actions = root.querySelector('.report-actions');
  const mobileSlot = root.querySelector('.mobile-nav-menu-actions');
  const desktopSlot = actions?.parentElement;
  const overviewHeader = root.querySelector('.overview-header');
  const mobileHeaderSlot = root.querySelector('.mobile-page-header');
  const headerDesktopSlot = overviewHeader?.parentElement;
  const view = root.ownerDocument.defaultView;
  const media = view?.matchMedia?.('(max-width: 700px)');
  if (!(actions instanceof HTMLElement) || !(mobileSlot instanceof HTMLElement) || !desktopSlot || !media) return;

  const placeActions = () => {
    const destination = media.matches ? mobileSlot : desktopSlot;
    if (actions.parentElement !== destination) destination.append(actions);
    if (overviewHeader instanceof HTMLElement && mobileHeaderSlot instanceof HTMLElement && headerDesktopSlot) {
      if (media.matches) {
        if (overviewHeader.parentElement !== mobileHeaderSlot) mobileHeaderSlot.prepend(overviewHeader);
      } else if (overviewHeader.parentElement !== headerDesktopSlot) {
        headerDesktopSlot.prepend(overviewHeader);
      }
      const headerDescription = overviewHeader.querySelector('[data-page-description]');
      if (headerDescription instanceof HTMLElement) {
        if (media.matches) headerDescription.setAttribute('aria-hidden', 'true');
        else headerDescription.removeAttribute('aria-hidden');
      }
    }
  };
  placeActions();
  media.addEventListener?.('change', placeActions, { signal });
  const observer = new MutationObserver(placeActions);
  observer.observe(root, { attributes: true, attributeFilter: ['class'] });
  signal.addEventListener('abort', () => observer.disconnect(), { once: true });
}

/**
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {Record<string, unknown>} dashboardDefaults
 * @param {string} horizonRange
 * @param {string} fallbackEvaluatedAt
 * @param {{ start?: string, end?: string }} [timeWindow]
 * @returns {{ available: boolean, evaluatedAt: string, duration: string, start: string, end: string }}
 */
function resolveDashboardHorizonViewModel(sources, dashboardDefaults, horizonRange, fallbackEvaluatedAt, timeWindow) {
  const selectedStart = Date.parse(timeWindow?.start ?? '');
  const selectedEnd = Date.parse(timeWindow?.end ?? '');
  if (Number.isFinite(selectedStart) && Number.isFinite(selectedEnd) && selectedEnd > selectedStart) {
    return {
      available: true,
      evaluatedAt: new Date(selectedEnd).toISOString(),
      duration: formatDashboardHorizonHours(Math.ceil((selectedEnd - selectedStart) / 3_600_000)),
      start: new Date(selectedStart).toISOString(),
      end: new Date(selectedEnd).toISOString()
    };
  }
  const dataHorizon = resolveDataHorizon(sources);
  const evaluatedAt = dataHorizon?.end ?? latestRetrievedAt(sources) ?? fallbackEvaluatedAt;
  const duration = dataHorizon
    ? formatDashboardHorizonHours(dataHorizon.hours)
    : formatDashboardHorizon(horizonRange);
  const start = dataHorizon?.start ?? (isPlainObject(dashboardDefaults.time) && typeof dashboardDefaults.time.start === 'string'
    ? dashboardDefaults.time.start
    : new Date(new Date(evaluatedAt).getTime() - dashboardHorizonHours(horizonRange) * 3_600_000).toISOString());
  const end = dataHorizon?.end ?? (isPlainObject(dashboardDefaults.time) && typeof dashboardDefaults.time.end === 'string'
    ? dashboardDefaults.time.end
    : evaluatedAt);
  return { available: true, evaluatedAt, duration, start, end };
}

/**
 * Resolves the shared coverage window across non-empty temporal sources. Sources
 * without valid coverage bounds are treated as timeless and do not constrain the window.
 * @param {Record<string, LogicalSourceInput>} sources
 * @returns {{ start: string, end: string, hours: number } | null}
 */
function resolveDataHorizon(sources) {
  const windows = Object.values(sources)
    .filter((source) => Array.isArray(source?.rows) && source.rows.length > 0)
    .map((source) => ({
      start: Date.parse(source.metadata?.['coverage-start'] ?? ''),
      end: Date.parse(source.metadata?.['coverage-end'] ?? '')
    }))
    .filter(({ start, end }) => Number.isFinite(start) && Number.isFinite(end) && end > start);
  if (windows.length === 0) return null;

  const start = Math.max(...windows.map((window) => window.start));
  const end = Math.min(...windows.map((window) => window.end));
  const hours = Math.ceil((end - start) / 3_600_000);
  return hours > 0
    ? { start: new Date(start).toISOString(), end: new Date(end).toISOString(), hours }
    : null;
}

/**
 * @param {Record<string, LogicalSourceInput>} sources
 * @returns {string | null}
 */
function latestRetrievedAt(sources) {
  return Object.values(sources)
    .map((source) => source?.metadata?.['retrieved-at'])
    .filter((value) => typeof value === 'string' && Number.isFinite(Date.parse(value)))
    .sort((left, right) => Date.parse(right) - Date.parse(left))[0] ?? null;
}

/**
 * @param {string} value
 * @returns {string}
 */
function formatReportDate(value) {
  return formatMediumUtcDateTime(new Date(value));
}

/**
 * @param {PresentableBuiltInPage | PresentableCustomPage} page
 * @returns {HTMLElement}
 */
function renderPagePlaceholder(page) {
  const payload = page.kind === 'built-in' ? getBuiltInPagePayload(page) : page;
  const pageClassName = typeof payload['class-name'] === 'string' && payload['class-name'].length > 0
    ? ` ${payload['class-name']}`
    : '';
  const routeParameter = typeof payload.route?.['hash-query-parameter'] === 'string'
    ? payload.route['hash-query-parameter']
    : undefined;
  const routeNavigationPage = typeof payload.route?.['navigation-page'] === 'string'
    ? payload.route['navigation-page']
    : undefined;
  return h('section', {
    className: `dashboard-page${pageClassName}`,
    id: `page-${page.id}`,
    'data-page-kind': 'custom',
    'data-page-name': page.id,
    'data-page-id': page.id,
    'data-page-title': getPageTitle(page),
    'data-page-description': payload.description ?? '',
    'data-page-experimental': page.experimental === true ? 'true' : undefined,
    'data-page-mode-indicator': page['mode-indicator'] === true ? 'true' : undefined,
    'data-route-parameter': routeParameter,
    'data-route-navigation-page': routeNavigationPage,
    'data-route-title-format': payload.route?.['title-format'],
    'data-route-breadcrumbs': payload.route?.breadcrumbs ? JSON.stringify(payload.route.breadcrumbs) : undefined,
    'data-retain-on-navigation': payload['retain-on-navigation'] === true ? 'true' : undefined,
    'data-page-pending': ''
  });
}

/**
 * @returns {HTMLElement}
 */
function renderPageSkeleton() {
  return renderDashboardViewSkeleton();
}

/**
 * @param {PresentableBuiltInPage | PresentableCustomPage} page
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {Record<string, { name: string, symbol: string, significant: number }>} units
 * @param {Record<string, unknown>} dashboardDefaults
 * @param {Record<string, { id: string, icon: string, title: TableField, subtitle?: TableField, labels: TableField[], details: TableField[] }>} cardTemplates
 * @param {Array<Record<string, unknown>>} reusableViews
 * @param {PageSourceLoadOptions['queryContext']} [queryContext]
 * @param {Array<Record<string, unknown>>} [queries]
 * @param {{ sources: import('./reactive.js').State<Record<string, LogicalSourceInput>>, failures: import('./reactive.js').State<Record<string, string>>, loading: import('./reactive.js').State<boolean>, signal: AbortSignal }} [binding]
 * @param {{ repository?: string, githubUrlBase?: string }} [promptContext]
 * @returns {HTMLElement}
 */
function renderPage(page, sources, units, dashboardDefaults, cardTemplates, reusableViews, queryContext, queries = [], binding = undefined, promptContext = {}) {
  const title = getPageTitle(page);
  const payload = getBuiltInPagePayload(page, reusableViews);
  return renderCustomPage(payload, title, sources, units, dashboardDefaults, cardTemplates, true, queryContext, queries, binding, promptContext);
}

/**
 * Renders an isolated, validated page with the normal declarative renderer,
 * without installing another dashboard shell or global navigation.
 * @param {PresentationDocument} document
 * @param {{ sources: import('./reactive.js').State<Record<string, LogicalSourceInput>>, failures: import('./reactive.js').State<Record<string, string>>, loading: import('./reactive.js').State<boolean>, signal: AbortSignal }} binding
 * @param {PageSourceLoadOptions['queryContext']} [queryContext]
 */
export function renderDashboardPagePreview(document, binding, queryContext) {
  const page = document.dashboard.pages[0];
  if (!page || document.dashboard.pages.length !== 1 || page.kind !== 'custom') {
    throw new Error('A dashboard preview requires exactly one custom page.');
  }
  const templates = Object.fromEntries((document.dashboard['card-templates'] ?? []).map((template) => [template.id, template]));
  const root = renderPage(page, binding.sources.get(), document.dashboard.units ?? {},
    document.dashboard.defaults ?? {}, templates, [], queryContext, document.dashboard.queries ?? [], binding,
    { repository: document.dashboard.repository, githubUrlBase: document.dashboard['github-url-base'] });
  enableLazyViews(root);
  binding.signal.addEventListener('abort', () => disconnectLazyViews(root), { once: true });
  return root;
}

/**
 * @param {PresentableCustomPage} page
 * @param {string} title
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {Record<string, { name: string, symbol: string, significant: number }>} units
 * @param {Record<string, unknown>} dashboardDefaults
 * @param {Record<string, { id: string, icon: string, title: TableField, subtitle?: TableField, labels: TableField[], details: TableField[] }>} cardTemplates
 * @param {boolean} [withFilterBar]
 * @param {PageSourceLoadOptions['queryContext']} [queryContext]
 * @param {Array<Record<string, unknown>>} [queries]
 * @param {{ sources: import('./reactive.js').State<Record<string, LogicalSourceInput>>, failures: import('./reactive.js').State<Record<string, string>>, loading: import('./reactive.js').State<boolean>, signal: AbortSignal }} [binding]
 * @param {{ repository?: string, githubUrlBase?: string }} [promptContext]
 * @returns {HTMLElement}
 */
function renderCustomPage(page, title, sources, units, dashboardDefaults, cardTemplates, withFilterBar = true, queryContext, queries = [], binding = undefined, promptContext = {}) {
  const effectiveDashboardDefaults = inventoryPage(page.id)
    ? { ...dashboardDefaults, time: undefined }
    : dashboardDefaults;
  const views = Array.isArray(page.views)
    ? page.views.map((view) => applyDashboardDefaults(view, effectiveDashboardDefaults))
    : [];
  const routeParameter = typeof page.route?.['hash-query-parameter'] === 'string'
    ? page.route['hash-query-parameter']
    : undefined;
  const routeNavigationPage = typeof page.route?.['navigation-page'] === 'string'
    ? page.route['navigation-page']
    : undefined;
  const hiddenViewIds = new Set(views.flatMap((view) =>
    viewBackendHidden(view, dashboardDataBackend()) && isPlainObject(view) && typeof view.id === 'string'
      ? [view.id] : []));
  const visibleViews = views.filter((view) => !viewBackendHidden(view, dashboardDataBackend()));
  const viewModes = availableViewModes(visibleViews);
  const viewModeControlEnabled = page['view-mode-control'] !== false;
  const selectedViewMode = /** @type {'chart'|'table'|'card'|undefined} */ (
    viewModes.includes(viewModeControlEnabled ? queryContext?.viewMode ?? 'chart' : 'chart')
      ? viewModeControlEnabled ? queryContext?.viewMode ?? 'chart' : 'chart'
      : defaultViewMode(visibleViews)
  );
  const sections = Array.isArray(page.sections)
    ? page.sections.map((section) => ({
      ...section, views: section.views.filter((id) => !hiddenViewIds.has(id))
    })).filter((section) => section.views.length > 0)
    : [];
  const standaloneCalloutViewIds = new Set(sections.flatMap((section) => {
    if (!Array.isArray(section.views) || section.views.length !== 1) return [];
    const viewId = section.views[0];
    const view = views.find((candidate) => isPlainObject(candidate) && candidate.id === viewId);
    return isPlainObject(view) && view.mark === 'callout' ? [viewId] : [];
  }));
  /** @type {Map<string, LogicalSourceInput>} */
  const pageSources = new Map();
  for (const view of views) {
    for (const sourceName of getViewSources(view)) {
      if (sources[sourceName]) {
        pageSources.set(sourceName, sources[sourceName]);
      }
    }
  }
  const renderedViews = views.map((view, index) => {
    if (viewBackendHidden(view, dashboardDataBackend())) return null;
    const viewSourceNames = getViewSources(view);
    const isPageBound = isPlainObject(view) && typeof view.element === 'string' && elementBindsPageSources(view.element);
    const isSelfBound = isPageBound || (isPlainObject(view) && typeof view.element === 'string' && elementLoadsSourcesAsync(view.element));
    /** @param {Record<string, LogicalSourceInput>} current */
    const viewIsPending = (current) => viewBackendAvailable(view, dashboardDataBackend()) && binding?.loading.get() === true
      && viewSourceNames.some((name, sourceIndex) => (
        !current[resolveViewSourceName(current, page.id, view, index, name, sourceIndex)]
        || current[resolveViewSourceName(current, page.id, view, index, name, sourceIndex)]?.metadata?.availability === 'unavailable'
      ));
    const viewId = isPlainObject(view) && typeof view.id === 'string' ? view.id : '';
    const headingTag = sections.length > 0 && !standaloneCalloutViewIds.has(viewId) ? 'h3' : 'h2';
    const layout = isPlainObject(view) && typeof view.layout === 'string' ? view.layout : 'full';
    const disclosure = isPlainObject(view) && view.disclosure === 'supplemental' ? 'supplemental' : 'essential';
    const isRouteView = Boolean(
      routeParameter
      && isPlainObject(view)
      && (
        view.mark === 'element'
        || typeof view.element === 'string'
        || (isPlainObject(view.data) && typeof view.data['route-field'] === 'string')
      )
    );
    const isNavigationCompositeView = isPlainObject(view) && view.mark === 'element';
    const render = (viewSources = sources, pending = false) => {
      const readySources = isSelfBound && binding?.loading.get()
        ? Object.fromEntries(Object.entries(viewSources).filter(([, source]) => source.metadata?.availability !== 'unavailable'))
        : viewSources;
      const failure = binding?.failures.get()[viewId];
      let rendered = failure && viewBackendAvailable(view, dashboardDataBackend())
        ? renderCustomViewState(page.id, getViewTitle(view, index), viewSourceNames.join(', ') || null,
            'unavailable', [`Data query failure: ${failure}`], headingTag, 'This view cannot be shown because its data query failed.')
        : pending && viewSourceNames.length > 0 && !isSelfBound && (!isPlainObject(view) || view.mark !== 'callout')
        ? renderPageSection(page.id, getViewTitle(view, index), [
            ...(isPlainObject(view) && view.mark === 'chart' && view.chart === 'pie' && typeof view.description === 'string'
              ? [h('p', { className: 'view-description' }, view.description)] : []),
            renderDashboardViewSkeleton(),
            h('span', { className: 'sr-only' }, `Loading ${getViewTitle(view, index)}`)
          ], headingTag)
        : renderCustomView(page.id, view, index, readySources, units, cardTemplates, headingTag, routeParameter, queryContext);
      const fullPie = isPlainObject(view) && view.mark === 'chart' && view.chart === 'pie' && view.layout === 'full';
      const pieState = fullPie ? rendered.querySelector(':scope > .view-state-card') : null;
      if (fullPie) rendered.classList.add('chart-view', 'chart-view-pie');
      if (pieState && isPlainObject(view)) {
        if (typeof view.description === 'string') {
          pieState.before(h('p', { className: 'view-description' }, view.description));
        }
        const layout = h('div', { className: 'pie-chart-layout' });
        pieState.before(layout);
        layout.append(pieState);
      }
      if ((pending && isPlainObject(view) && view.mark === 'chart' && view.chart === 'pie') || pieState) {
        const heading = rendered.querySelector(':scope > :is(h2, h3, h4)');
        if (heading) {
          const titleRow = h('div', { className: 'chart-prompt-heading' });
          heading.before(titleRow);
          titleRow.append(heading);
        }
      }
      if (pending && !isSelfBound) rendered.setAttribute('aria-busy', 'true');
      const filterControls = viewFilterControls(view);
      if (filterControls.length > 0 && viewBackendAvailable(view, dashboardDataBackend())) {
        const filterSources = Object.fromEntries(viewFilterSourceNames(view).flatMap((name) => {
          const sourceIndex = viewSourceNames.indexOf(name);
          const source = viewSources[resolveViewSourceName(viewSources, page.id, view, index, name, sourceIndex)];
          return source ? [[name, source]] : [];
        }));
        const filterBar = renderCardFilterBar({
          pageId: page.id,
          viewId,
          controls: filterControls,
          sources: filterSources,
          selected: queryContext?.viewFilters?.[viewId],
          pending,
          onChange: (filters) => {
            root.dispatchEvent(new CustomEvent('dashboard-query-context-change', {
              bubbles: true,
              detail: {
                pageId: page.id,
                queryContext: {
                  ...queryContext,
                  viewMode: selectedViewMode,
                  viewFilters: { ...queryContext?.viewFilters, [viewId]: filters }
                }
              }
            }));
          }
        });
        const cardToolbar = selectedViewMode === 'card'
          ? rendered.querySelector('.mobile-table-card-toolbar') : null;
        if (cardToolbar) {
          cardToolbar.querySelector('.mobile-table-card-toolbar-title')?.remove();
          cardToolbar.append(filterBar);
        } else rendered.prepend(filterBar);
      }
      if ((!pending || isSelfBound) && isPlainObject(view) && viewBackendAvailable(view, dashboardDataBackend())) {
        const semantics = effectiveViewSemantics(view, queries);
        if (view.prompt === 'always' || (view.prompt !== 'none'
          && semantics.subject && semantics.objective && semantics.acceptance)) {
          const selectedSources = Object.fromEntries(getViewSources(view).flatMap((sourceName, sourceIndex) => {
            const source = viewSources[resolveViewSourceName(viewSources, page.id, view, index, sourceName, sourceIndex)];
            return source ? [[sourceName, source]] : [];
          }));
          const action = normalizeViewAction(view, semantics);
          action.viewTitle = getViewTitle(view, index);
          action.actionId = declaredAgentTaskActionId();
          const prompt = renderPromptPreviewAction(
            action,
            () => {
              const routeValues = Object.fromEntries(new URLSearchParams(globalThis.location?.hash.split('?')[1] ?? ''));
              return semanticViewPrompt({
                pageId: page.id,
                pageContract: page,
                ...promptContext,
                viewId: view.id ?? `view-${index + 1}`,
                title: getViewTitle(view, index),
                semantics,
                queryParameters: {
                  ...dashboardFormDefaultValues(page.form),
                  ...(queryContext?.formValues ?? {}),
                  ...Object.fromEntries((Array.isArray(view.data?.arguments) ? view.data.arguments : [])
                    .flatMap((/** @type {{ name?: string, field?: string }} */ argument) => typeof argument?.name === 'string' && typeof argument.field === 'string'
                      && routeValues[argument.name] !== undefined
                      ? [[argument.name, routeValues[argument.name]]] : [])),
                  ...(routeParameter && routeValues[routeParameter] ? { [routeParameter]: routeValues[routeParameter] } : {})
                },
                filters: { ...queryContext, viewFilters: view.data?.filters ?? {} },
                scope: view.data?.scope ?? dashboardDefaults.scope ?? {},
                sources: selectedSources,
                level: action.level
              });
            }
          );
          prompt.classList.add(view.mark === 'chart' ? 'chart-prompt-action' : 'semantic-prompt-action');
          const section = rendered.matches('.page-section') ? rendered : null;
          const cardToolbar = selectedViewMode === 'card'
            ? section?.querySelector('.mobile-table-card-toolbar') : null;
          if (section && cardToolbar) {
            section.classList.add('semantic-prompt-view');
            cardToolbar.append(prompt);
          } else if (section) {
            section.classList.add('semantic-prompt-view');
            const heading = section.querySelector('h2, h3, h4');
            if (heading && view.mark === 'chart') {
              const titleRow = h('div', { className: 'chart-prompt-heading' });
              heading.before(titleRow);
              titleRow.append(heading, prompt);
            } else if (heading) heading.after(prompt);
            else section.prepend(prompt);
          } else {
            rendered = h('div', { className: 'semantic-prompt-view' },
              h('div', { className: 'semantic-prompt-heading' },
                view.mark === 'callout' ? null : h(headingTag, null, getViewTitle(view, index)),
                prompt),
              rendered);
          }
        }
      }
      suppressSupplementalTableHeading(rendered, view, index);
      if (isPlainObject(view) && view['show-title'] === false) {
        const headings = rendered.matches('.page-section')
          ? rendered.querySelectorAll(
            ':scope > :is(h2, h3, h4), :scope > .chart-prompt-heading > :is(h2, h3, h4),'
            + ' :scope > .chart-prompt-heading > h4,'
            + ' :scope > .pie-chart-card > :is(h2, h3, h4),'
            + ' :scope > .pie-chart-card > .chart-prompt-heading > :is(h2, h3, h4),'
            + ' :scope > .pie-chart-card > .chart-prompt-heading > h4,'
            + ' :scope > .chart-horizontal-card > .chart-horizontal-copy > :is(h2, h3, h4),'
            + ' :scope > .chart-horizontal-card > .chart-horizontal-copy > h4,'
            + ' :scope > .chart-horizontal-card > .chart-horizontal-copy > .chart-prompt-heading > :is(h2, h3, h4),'
            + ' :scope > .chart-horizontal-card > .chart-horizontal-copy > .chart-prompt-heading > h4'
          )
          : rendered.querySelectorAll(
            ':scope > .semantic-prompt-heading > :is(h2, h3, h4),'
            + ' :scope > .metric-card-widget-label, :scope > .dashboard-callout-heading > div > :is(h2, h3, h4),'
            + ' :scope > .dashboard-callout-heading > div > h4,'
            + ' :scope > .metric-card-widget > .metric-card-widget-label,'
            + ' :scope > .dashboard-callout > .dashboard-callout-heading > div > :is(h2, h3, h4),'
            + ' :scope > .dashboard-callout > .dashboard-callout-heading > div > h4'
          );
        for (const heading of headings) {
          const labelledRegion = heading.closest('[aria-labelledby]');
          if (labelledRegion?.getAttribute('aria-labelledby') === heading.id) {
            labelledRegion.removeAttribute('aria-labelledby');
            labelledRegion.setAttribute('aria-label', getViewTitle(view, index));
          } else if (!labelledRegion) {
            const region = heading.closest('.metric-card-widget') ?? rendered;
            if (region === rendered && rendered.matches('.semantic-prompt-view')) rendered.setAttribute('role', 'group');
            region.setAttribute('aria-label', getViewTitle(view, index));
          }
          heading.remove();
        }
      }
      if (disclosure === 'essential') {
        rendered.classList.add('custom-view');
        rendered.setAttribute('data-view-layout', layout);
      }
      rendered.setAttribute('data-disclosure', disclosure);
      if (isPlainObject(view) && typeof view.mark === 'string') rendered.setAttribute('data-view-mark', view.mark);
      if (isPlainObject(view) && view['lazy-list'] === true) {
        rendered.setAttribute('data-view-lazy-list', '');
      }
      if (!isNavigationCompositeView && disclosure === 'essential') rendered.dataset.viewModeContent = viewModeForView(view);
      return rendered;
    };
    let rendered = isRouteView
      || !viewBackendAvailable(view, dashboardDataBackend())
      || (binding && viewIsPending(binding.sources.get()))
      || index === 0
      || (isPlainObject(view) && (view.mark === 'callout' || (
        typeof view.element === 'string' && elementLoadsSourcesAsync(view.element)
      )))
      ? render(binding?.sources.get() ?? sources, viewIsPending(binding?.sources.get() ?? sources))
      : renderLazyView({
        label: getViewTitle(view, index),
        headingLevel: headingTag,
        minHeight: isPlainObject(view) && view.mark === 'chart' ? 280
          : layout === 'half' || layout === 'third' ? 180 : 280,
        render: () => {
          const current = binding?.sources.get() ?? sources;
          const next = render(current, viewIsPending(current));
          if (disclosure !== 'supplemental') rendered = next;
          return next;
        }
      });
    if (binding && viewBackendAvailable(view, dashboardDataBackend())
      && viewSourceNames.length > 0 && (!isSelfBound || isPageBound)) {
      let initial = true;
      let previousSources = binding.sources.get();
      let previousPending = viewIsPending(previousSources);
      let previousFailure = binding.failures.get()[viewId];
      effect(() => {
        const current = binding.sources.get();
        const failure = binding.failures.get()[viewId];
        const pending = viewIsPending(current);
        if (initial) {
          initial = false;
          return;
        }
        const changed = previousFailure !== failure || previousPending !== pending || viewSourceNames.some((name, sourceIndex) => {
          const resolved = resolveViewSourceName(current, page.id, view, index, name, sourceIndex);
          return current[resolved] !== previousSources[resolved];
        });
        previousSources = current;
        previousPending = pending;
        previousFailure = failure;
        if (isPageBound) {
          if (!changed) return;
          batch(() => {
            for (const [sourceIndex, name] of viewSourceNames.entries()) {
              const resolved = resolveViewSourceName(current, page.id, view, index, name, sourceIndex);
              const key = dashboardViewAliasName(page.id, view, index, name, sourceIndex);
              const source = current[resolved];
              if (source) publishSource(name, source, key);
              else if (!pending && untracked(() => sourceState(key).get().status !== 'failed')) {
                sourceState(key).set({ status: 'missing', origin: 'view', source: null });
              }
            }
          });
          return;
        }
        const target = disclosure === 'supplemental'
          ? rendered.querySelector(':scope > :not(summary)')
          : rendered;
        if (!(target instanceof HTMLElement) || !changed || target.hasAttribute('data-lazy-view')) return;
        const filterState = captureCardFilterState(target);
        const replacement = render(current, pending);
        for (const attribute of ['data-view-id', 'data-view-layout', 'data-disclosure', 'data-view-mode-content', 'data-view-lazy-list', 'data-section-id', 'data-section-layout']) {
          if (target.hasAttribute(attribute)) replacement.setAttribute(attribute, target.getAttribute(attribute) ?? '');
        }
        if (target.classList.contains('custom-view')) replacement.classList.add('custom-view');
        const active = target.ownerDocument.activeElement;
        const focusedId = active instanceof HTMLElement && target.contains(active) ? active.id : '';
        if (target.parentNode) target.replaceWith(replacement);
        restoreCardListPagination(target, replacement);
        restoreCardFilterState(replacement, filterState);
        if (disclosure !== 'supplemental') rendered = replacement;
        if (focusedId) {
          const focusTarget = [...replacement.querySelectorAll('[id]')].find((node) => node.id === focusedId);
          if (focusTarget instanceof HTMLElement) focusTarget.focus({ preventScroll: true });
        }
        const owningPage = replacement.closest('.dashboard-page');
        if (owningPage instanceof HTMLElement) enableLazyViews(owningPage);
        if (isRouteView && owningPage instanceof HTMLElement && routeParameter) {
          dispatchPageRoute(replacement, routeParameter, owningPage.dataset.routeValue ?? '');
        }
      }, { signal: binding.signal });
    }
    rendered.classList.add('custom-view');
    rendered.setAttribute('data-view-id', viewId || `view-${index + 1}`);
    if (isPlainObject(view) && typeof view.mark === 'string') rendered.setAttribute('data-view-mark', view.mark);
    rendered.setAttribute('data-view-layout', layout);
    rendered.setAttribute('data-disclosure', disclosure);
    if (isPlainObject(view) && view['lazy-list'] === true) {
      rendered.setAttribute('data-view-lazy-list', '');
    }
    if (!isNavigationCompositeView && disclosure === 'essential') rendered.dataset.viewModeContent = viewModeForView(view);
    if (disclosure === 'essential') {
      return rendered;
    }

    rendered.classList.remove('custom-view');
    rendered.removeAttribute('data-view-layout');
    rendered = renderViewDisclosure(rendered, layout, disclosure, getViewTitle(view, index));
    return rendered;
  });
  const renderedViewsById = new Map(views.flatMap((view, index) => renderedViews[index]
    ? [[isPlainObject(view) && typeof view.id === 'string' ? view.id : `view-${index + 1}`, renderedViews[index]]]
    : []));
  const renderedContent = sections.length > 0
    ? h(
      'div',
      { className: 'page-layout-grid' },
      ...sections.map((section) => renderLayoutSection(page.id, section, renderedViewsById, sources))
    )
    : h('div', { className: 'custom-view-grid' }, ...renderedViews.filter((view) => view !== null));
  if (binding) {
    for (const section of sections) {
      if (!section['count-source'] && !section['count-sources']?.length) continue;
      const sectionRoot = [...renderedContent.querySelectorAll('[data-section-id]')]
        .find((element) => element.getAttribute('data-section-id') === section.id);
      if (!(sectionRoot instanceof HTMLElement) || !sectionRoot.classList.contains('layout-section')) continue;
      let initial = true;
      const countNames = (section['count-sources'] ?? [section['count-source']]).filter((name) => typeof name === 'string');
      let previous = binding.sources.get();
      effect(() => {
        const current = binding.sources.get();
        if (initial) { initial = false; return; }
        const changed = countNames.some((name) => current[name] !== previous[name]);
        previous = current;
        if (!changed) return;
        const oldChrome = sectionRoot.querySelector(':scope > :first-child');
        if (oldChrome) oldChrome.replaceWith(renderLayoutSectionChrome(page.id, section, layoutSectionCount(section, current)));
      }, { signal: binding.signal });
    }
  }
  const routeTabs = declaredRouteTabs(page.route);
  const renderedRouteTabs = routeTabs && routeParameter
    ? renderDeclaredRouteTabs({
      routeParameter,
      currentTab: routeTabs.currentTab,
      tabs: routeTabs.tabs,
      className: routeTabs.className
    })
    : routeTabs ? renderPageTabs(routeTabs) : null;
  const pageClassName = typeof page['class-name'] === 'string' && page['class-name'].length > 0
    ? ` ${page['class-name']}`
    : '';

  /** @type {HTMLElement} */
  let root;
  /**
   * @param {Map<string, string[]>} filters
   * @param {{ start?: string, end?: string } | undefined} timeWindow
   * @param {'chart'|'table'|'card'|undefined} viewMode
   */
  const dispatchQueryContextChange = (filters, timeWindow, viewMode) => {
      root.dispatchEvent(new CustomEvent('dashboard-query-context-change', {
        bubbles: true,
        detail: {
          pageId: page.id,
          queryContext: {
            filters: Object.fromEntries([...filters.entries()]),
            ...(page.id === 'readiness' || !timeWindow
              ? {}
              : { timeWindow: { start: timeWindow.start, end: timeWindow.end } }),
            ...(viewMode ? { viewMode } : {}),
            ...(queryContext?.viewFilters ? { viewFilters: queryContext.viewFilters } : {}),
            ...(queryContext?.formValues ? { formValues: queryContext.formValues } : {})
          }
        }
      }));
    };
  let currentFilters = new Map(Object.entries(queryContext?.filters ?? {}));
  let currentTimeWindow = queryContext?.timeWindow;
  const filterBar = withFilterBar && page['filter-bar'] === true
    ? renderFilterBar((filters, timeWindow) => {
      currentFilters = filters;
      currentTimeWindow = timeWindow;
      dispatchQueryContextChange(currentFilters, currentTimeWindow, selectedViewMode);
    }, {
      restoreSettings: false,
      signal: binding?.signal,
      maxDays: 30,
      defaultRange: isPlainObject(dashboardDefaults.time) && typeof dashboardDefaults.time.range === 'string'
        ? dashboardDefaults.time.range
        : 'all',
      referenceEnd: queryContext?.timeWindow?.end ?? latestSourceCoverageEnd(page.id === 'readiness'
        ? [sources.runs, sources.findings, sources.outcomes]
        : [...pageSources.values()])
    })
    : null;
  const viewModeControl = viewModeControlEnabled && viewModes.length > 1
    ? renderViewModeControl(viewModes, selectedViewMode, (viewMode) => {
      dispatchQueryContextChange(currentFilters, currentTimeWindow, viewMode);
    })
    : null;
  const pageChrome = filterBar || viewModeControl
    ? h('div', { className: 'page-chrome' }, filterBar, viewModeControl)
    : null;
  const parameterForm = isPlainObject(page.form)
    ? renderDashboardForm(page.form, queryContext?.formValues, (formValues) => {
        root.dispatchEvent(new CustomEvent('dashboard-query-context-change', {
          bubbles: true,
          detail: {
            pageId: page.id,
            queryContext: {
              ...queryContext,
              formValues
            }
          }
        }));
      }, page.id)
    : null;
  root = h(
    'section',
    {
      className: `dashboard-page${pageClassName}`,
      id: `page-${page.id}`,
      'data-page-kind': 'custom',
      'data-page-name': page.id,
      'data-page-id': page.id,
      'data-page-title': title,
      'data-page-description': page.description ?? '',
      'data-page-experimental': page.experimental === true ? 'true' : undefined,
      'data-route-parameter': routeParameter,
      'data-route-navigation-page': routeNavigationPage,
      'data-route-title-format': page.route?.['title-format'],
      'data-route-breadcrumbs': page.route?.breadcrumbs ? JSON.stringify(page.route.breadcrumbs) : undefined,
      'data-retain-on-navigation': page['retain-on-navigation'] === true ? 'true' : undefined,
      'data-view-mode': selectedViewMode
    },
    renderedRouteTabs,
    pageChrome,
    parameterForm,
    ...(visibleViews.length > 0
      ? [renderHiddenDataStateMetrics(summarizeDataState(pageSources)), renderedContent]
      : [h('p', null, 'No custom views available.')])
  );
  const availabilityViewId = page.route?.['availability-view'];
  const availabilityIndex = views.findIndex((view) => isPlainObject(view) && view.id === availabilityViewId);
  if (availabilityIndex >= 0 && routeNavigationPage) {
    const availabilityView = views[availabilityIndex];
    const sourceName = getViewSources(availabilityView)[0];
    const failure = h('div', { className: 'view-state-card', 'data-view-state': 'unavailable', role: 'alert' },
      octicon('alert'),
      h('div', { className: 'view-state-card-body' },
       h('h2', null, `${title} unavailable`),
       h('p', null, page.route?.['availability-message'] ?? 'This item could not be loaded. It may have been removed or temporarily unavailable.'),
       h('div', { className: 'package-recovery-actions' },
         h('button', {
           type: 'button',
           className: 'button',
           onclick: () => root.dispatchEvent(new CustomEvent('dashboard-retry-page', { bubbles: true }))
         }, 'Retry'),
         h('a', { className: 'button', href: `#page-${routeNavigationPage}` }, `Back to ${titleCase(routeNavigationPage)}`)
       )
      )
    );
    const partial = h('div', { className: 'view-state-card', role: 'status' },
      octicon('info'),
      h('div', { className: 'view-state-card-body' },
        h('p', null, page.route?.['partial-message'] ?? 'Some information could not be retrieved. Available details are shown below.')
      ));
    root.insertBefore(failure, renderedContent);
    root.insertBefore(partial, renderedContent);
    const updateAvailability = () => {
      const current = binding?.sources.get() ?? sources;
      const resolved = resolveViewSourceName(current, page.id, availabilityView, availabilityIndex, sourceName, 0);
      const source = current[resolved];
      const pending = binding?.loading.get() === true;
      const unavailable = !pending && (!source || source.metadata?.availability === 'unavailable'
       || !Array.isArray(source.rows) || source.rows.length === 0);
      const incomplete = !unavailable && !pending && source?.metadata?.completeness === 'partial';
      failure.hidden = !unavailable;
      partial.hidden = !incomplete;
      if (unavailable) renderedContent.remove();
      else if (!renderedContent.isConnected) root.append(renderedContent);
      if (renderedRouteTabs) renderedRouteTabs.hidden = unavailable;
    };
    if (binding) effect(updateAvailability, { signal: binding.signal });
    else updateAvailability();
  }
  return root;
}

/**
 * A `mark: element` view is navigation/composite chrome (for example the `campaign-route`
 * summary), not a chart/table/card representation of page content, so it must not create a
 * phantom "chart" mode or gate data loading for the page's actual content views. A
 * `disclosure: supplemental` view is an independently-collapsible detail panel toggled by its
 * own `<details>` summary, not part of the page's primary chart/table/card content, so it must
 * not gate which modes are offered either.
 * @param {Array<unknown>} views
 * @returns {Array<unknown>}
 */
function contentViewsForModeSelection(views) {
  return views.filter((view) => (
    !(isPlainObject(view) && (view.mark === 'element' || view.disclosure === 'supplemental'))
  ));
}

/** @param {Array<unknown>} views @returns {Array<'chart'|'table'|'card'>} */
function availableViewModes(views) {
  const modes = new Set(contentViewsForModeSelection(views).map(viewModeForView));
  if (modes.has('table')) modes.add('card');
  const orderedModes = /** @type {const} */ (['chart', 'card', 'table']);
  return orderedModes.filter((mode) => modes.has(mode));
}

/** @param {Array<unknown>} views @returns {'chart'|'table'|'card'|undefined} */
function defaultViewMode(views) {
  const modes = new Set(contentViewsForModeSelection(views).map(viewModeForView));
  if (modes.has('chart')) return 'chart';
  if (modes.has('table')) return 'table';
  if (modes.has('card')) return 'card';
  return undefined;
}

/** @param {unknown} view @returns {'chart'|'table'|'card'} */
function viewModeForView(view) {
  if (!isPlainObject(view)) return 'chart';
  if (view.mark === 'table') return 'table';
  if (view.mark === 'list') return 'card';
  return 'chart';
}

/**
 * @param {Array<LogicalSourceInput | undefined>} sources
 * @returns {string | undefined}
 */
function latestSourceCoverageEnd(sources) {
  return sources
    .flatMap((source) => [source?.metadata?.['coverage-end'], source?.metadata?.['as-of'], source?.metadata?.['retrieved-at']])
    .filter((value) => typeof value === 'string' && Number.isFinite(Date.parse(value)))
    .map(String)
    .toSorted((left, right) => Date.parse(right) - Date.parse(left))[0];
}

/**
 * @param {PresentableBuiltInPage | PresentableCustomPage} page
 * @returns {string}
 */
function getPageTitle(page) {
  return typeof page.title === 'string' && page.title.length > 0
    ? page.title
    : titleCase(page.id);
}

/**
 * @param {DataState} effectiveState
 * @returns {HTMLElement}
 */
function renderHiddenDataStateMetrics(effectiveState) {
  const metrics = renderDataStateMetrics(effectiveState);
  metrics.hidden = true;
  return metrics;
}

/**
 * @param {string} pageId
 * @param {PresentablePageSection} section
 * @param {Map<string, HTMLElement>} renderedViews
 * @param {Record<string, LogicalSourceInput>} sources
 * @returns {HTMLElement}
 */
function renderLayoutSection(pageId, section, renderedViews, sources) {
  const headingId = `${pageId}-${section.id}-layout-heading`;
  const count = layoutSectionCount(section, sources);
  const sectionViews = section.views.map((viewId) => renderedViews.get(viewId)
    ?? renderEmptyMessage(`View unavailable: ${viewId}`, { 'data-missing-view-id': viewId }));
  if (sectionViews.length === 1 && sectionViews[0].classList.contains('dashboard-callout')) {
    sectionViews[0].setAttribute('data-section-id', section.id);
    sectionViews[0].setAttribute('data-section-layout', section.layout);
    return sectionViews[0];
  }

    return h(
    'section',
    {
      className: 'layout-section',
      'data-section-id': section.id,
      'data-section-layout': section.layout,
      'aria-labelledby': headingId
    },
    renderLayoutSectionChrome(pageId, section, count),
    h(
      'div',
      { className: 'custom-view-grid' },
      ...sectionViews
    )
  );
}

/** @param {PresentablePageSection} section @param {Record<string, LogicalSourceInput>} sources */
function layoutSectionCount(section, sources) {
  const countSourceNames = Array.isArray(section['count-sources'])
    ? section['count-sources']
    : section['count-source'] ? [section['count-source']] : [];
  const countField = typeof section['count-field'] === 'string' ? section['count-field'] : null;
  const countSources = countSourceNames.map((sourceName) => sources[sourceName]).filter(Boolean);
  const countValues = countField
    ? countSources.flatMap((source) => {
        if (source.metadata?.availability === 'unavailable') return [];
        const value = Number(source.rows?.[0]?.[countField]);
        return Number.isFinite(value) ? [value] : [];
      })
    : [];
  return countField
    ? countValues.length > 0 ? countValues.reduce((total, value) => total + value, 0) : null
    : countSources.length === 1 && Array.isArray(countSources[0]?.rows)
      ? countSources[0].rows.length
      : null;
}

/**
 * Shows a single dashboard page and keeps sidebar state synchronized with the URL hash.
 * @param {HTMLElement} root
 * @param {string} dashboardTitle
 * @param {(pageId: string, options: PageSourceLoadOptions & { renderUpdate: (page: HTMLElement) => void }) => HTMLElement | Promise<HTMLElement> | null} [renderPageById]
 * @param {string} [defaultPageId]
 * @param {boolean} [reloadPopulatedPages]
 * @param {Set<string>} [knownQueries]
 * @returns {() => void}
 */
export function enableDashboardPageNavigation(root, dashboardTitle = '', renderPageById, defaultPageId = '', reloadPopulatedPages = false, knownQueries = new Set()) {
  const pages = [...root.querySelectorAll('.dashboard-page')]
    .filter((page) => page instanceof HTMLElement);
  /** @type {Map<string, { details: boolean[], scrollTop: number }>} */
  const pageState = new Map();
  let activePageId = '';
  let activationRevision = 0;
  let pageOwner = new AbortController();
  const navigationOwner = new AbortController();
  /** @type {number | undefined} */
  let pendingScrollTop;
  /** @type {string | undefined} */
  let pendingScrollPageId;
  /** @type {number | null} */
  let scrollPersistenceTimer = null;
  let cancelPendingScrollPersistence = () => {};
  const disposeNavigation = () => {
    activationRevision += 1;
    navigationOwner.abort();
    pageOwner.abort();
    cancelPendingScrollPersistence();
  };
  /** @type {Map<string, NonNullable<PageSourceLoadOptions['queryContext']>>} */
  const pageQueryContext = new Map();
  /** @type {Pick<NonNullable<PageSourceLoadOptions['queryContext']>, 'filters' | 'timeWindow'> | undefined} */
  let horizonQueryContext;
  let pendingQueryFocusId = '';
  const overviewPage = pages.find((page) => page.dataset.pageId === 'overview');
  const links = [...root.querySelectorAll('[data-nav-page-id], [data-mobile-nav-page-id]')]
    .filter((link) => link instanceof HTMLAnchorElement);
  const breadcrumbPage = root.querySelector('[data-breadcrumb-page]');
  const breadcrumbRoot = root.querySelector('[data-breadcrumb-root]');
  const breadcrumbDashboard = root.querySelector('[data-breadcrumb-dashboard]');
  const pageTitle = root.querySelector('#page-title');
  const pageTitleLink = root.querySelector('[data-page-title-link]');
  const pageExperimental = root.querySelector('.title-area [data-page-experimental]');
  /** @param {HTMLElement | undefined} page */
  const syncPageExperimental = (page) => {
    if (pageExperimental instanceof HTMLElement) pageExperimental.hidden = page?.dataset.pageExperimental !== 'true';
  };
  const pageDescription = root.querySelector('.overview-header [data-page-description]');
  const pageMode = root.querySelector('[data-page-mode]');
  const pageScroller = root.querySelector('main.dashboard-prototype');
  const scrollTop = () => pageScroller instanceof HTMLElement
    ? pageScroller.scrollTop
    : root.ownerDocument.scrollingElement?.scrollTop ?? root.ownerDocument.documentElement.scrollTop;
  /** @param {string} pageId */
  const savedScrollTop = (pageId) => {
    const state = root.ownerDocument.defaultView?.history.state;
    return state?.[SCROLL_PAGE_ID_STATE_KEY] === pageId
      ? normalizeScrollTop(state[SCROLL_TOP_STATE_KEY])
      : undefined;
  };
  cancelPendingScrollPersistence = () => {
    const view = root.ownerDocument.defaultView;
    if (scrollPersistenceTimer !== null) view?.clearTimeout(scrollPersistenceTimer);
    scrollPersistenceTimer = null;
  };
  const commitScrollTop = () => {
    cancelPendingScrollPersistence();
    const view = root.ownerDocument.defaultView;
    if (!view) return;
    const state = view.history.state && typeof view.history.state === 'object' ? view.history.state : {};
    view.history.replaceState({
      ...state,
      [SCROLL_PAGE_ID_STATE_KEY]: activePageId,
      [SCROLL_TOP_STATE_KEY]: scrollTop()
    }, '', view.location.href);
  };
  const persistScrollTop = () => {
    const view = root.ownerDocument.defaultView;
    if (!view) return;
    cancelPendingScrollPersistence();
    scrollPersistenceTimer = view.setTimeout(() => {
      scrollPersistenceTimer = null;
      if (!navigationOwner.signal.aborted) commitScrollTop();
    }, SCROLL_PERSISTENCE_DELAY_MS);
  };
  if (pageScroller instanceof HTMLElement) {
    pageScroller.addEventListener('scroll', persistScrollTop, { passive: true, signal: navigationOwner.signal });
  }
  /** @param {HTMLElement | undefined} page */
  const syncFullViewMode = (page) => {
    syncFullViewModeForPage(root, page);
    syncMobileViewModeToggle(root);
  };
  const defaultBreadcrumbs = [breadcrumbRoot, breadcrumbDashboard].map((link) => ({
    label: link?.textContent ?? '',
    href: link instanceof HTMLAnchorElement ? link.getAttribute('href') ?? '' : '',
    hidden: link instanceof HTMLElement ? link.hidden : false
  }));
  if (pages.length === 0 || links.length === 0) {
    return disposeNavigation;
  }

  /**
   * @param {string} pageId
   * @param {string} [routeTitle]
   * @param {string} [routeDescription]
   */
  const primePageChrome = (pageId, routeTitle = '', routeDescription = '') => {
    const page = pages.find((candidate) => candidate.dataset.pageId === pageId);
    const title = (page?.dataset.routeBreadcrumbs ? '' : routeTitle.trim()) || page?.dataset.pageTitle || '';
    const description = routeDescription.trim() || page?.dataset.pageDescription || '';
    if (breadcrumbPage) breadcrumbPage.textContent = title;
    if (pageTitle) pageTitle.textContent = title;
    syncPageExperimental(page);
    updateDocumentTitle(root.ownerDocument, title, dashboardTitle);
    renderPageTitleLink(pageTitleLink, null);
    if (pageDescription) {
      pageDescription.textContent = description;
      pageDescription.toggleAttribute('hidden', description.length === 0);
    }
    renderPageMode(pageMode, '');
  };

  root.addEventListener('dashboard-route-allocation', (event) => {
    if (!(event instanceof CustomEvent) || !(event.target instanceof Element)) return;
    const page = event.target.closest('.dashboard-page');
    if (!(page instanceof HTMLElement) || page.hidden) return;
    const title = typeof event.detail?.title === 'string' ? event.detail.title.trim() : '';
    const description = typeof event.detail?.description === 'string' ? event.detail.description.trim() : '';
    if (title && !page.dataset.routeBreadcrumbs) {
      if (breadcrumbPage) breadcrumbPage.textContent = title;
      if (pageTitle) pageTitle.textContent = title;
      updateDocumentTitle(root.ownerDocument, title, dashboardTitle);
    }
    renderPageTitleLink(pageTitleLink, event.detail?.titleLink);
    const hasAllocatedBreadcrumbs = !page.dataset.routeBreadcrumbs && Array.isArray(event.detail?.breadcrumbs);
    const breadcrumbs = hasAllocatedBreadcrumbs ? event.detail.breadcrumbs : [];
    for (const [index, link] of [breadcrumbRoot, breadcrumbDashboard].entries()) {
      const breadcrumb = breadcrumbs[index];
      if (!(link instanceof HTMLAnchorElement)) continue;
      if (!breadcrumb || typeof breadcrumb.label !== 'string' || typeof breadcrumb.href !== 'string' || !breadcrumb.href.startsWith('#page-')) {
        if (hasAllocatedBreadcrumbs) link.hidden = true;
        continue;
      }
      link.hidden = false;
      link.textContent = breadcrumb.label;
      link.href = breadcrumb.href;
    }
    if (pageDescription && description) {
      pageDescription.textContent = description;
      pageDescription.removeAttribute('hidden');
    }
    const mode = event.detail?.mode === 'review' || event.detail?.mode === 'live'
      ? event.detail.mode
      : '';
    renderPageMode(pageMode, mode);
    const navigationPage = typeof event.detail?.navigationPage === 'string'
      ? event.detail.navigationPage
      : page.dataset.routeNavigationPage ?? '';
    const activeNavigationPageId = determineActiveNavigationPageId(
      page.dataset.pageId,
      navigationPage,
      links,
      availableIds,
      page.dataset.routeValue ?? ''
    );
    if (activeNavigationPageId) {
      updateNavigationLinks(links, activeNavigationPageId);
    }
  }, { signal: navigationOwner.signal });

  const availableIds = /** @type {Set<string>} */ (
    new Set(pages.map((page) => page.dataset.pageId).filter((pageId) => typeof pageId === 'string'))
  );
  const routeFromHash = () => {
    const hash = root.ownerDocument.defaultView?.location.hash ?? '';
    if (!hash.startsWith('#page-')) return null;
    try {
      const route = hash.slice('#page-'.length);
      const queryIndex = route.indexOf('?');
      const pageId = decodeURIComponent(queryIndex === -1 ? route : route.slice(0, queryIndex));
      if (!availableIds.has(pageId)) return null;
      return {
        pageId,
        parameters: new URLSearchParams(queryIndex === -1 ? '' : route.slice(queryIndex + 1))
      };
    } catch {
      return null;
    }
  };
  /**
   * @param {string} pageId
   * @param {URLSearchParams} [parameters]
   */
  const navigationTransitionKind = (pageId, parameters = new URLSearchParams()) => {
    const activePage = pages.find((candidate) => candidate.dataset.pageId === activePageId);
    return activePage && pageId !== activePageId && routeTabForPage(activePage, pageId, parameters)
      ? 'tab' : 'page';
  };
  /**
   * Defers expensive rendering until the lightweight title and skeleton update
   * has had an opportunity to paint.
   * @param {() => void} populate
   */
  const schedulePopulation = (populate) => {
    const view = root.ownerDocument.defaultView;
    if (typeof view?.requestAnimationFrame === 'function') {
      view.requestAnimationFrame(() => view.requestAnimationFrame(populate));
      return;
    }
    if (view) {
      view.setTimeout(populate, 0);
    } else {
      setTimeout(populate, 0);
    }
  };
  /**
   * @param {string} pageId
   * @param {URLSearchParams} [parameters]
   * @param {boolean} [deferPopulation]
   * @param {string} [provisionalTitle]
   * @param {string} [provisionalDescription]
   */
  const activate = (
    pageId,
    parameters = new URLSearchParams(),
    deferPopulation = false,
    provisionalTitle = '',
    provisionalDescription = ''
  ) => {
     if (navigationOwner.signal.aborted) return;
     const revision = ++activationRevision;
    const reactivatingPage = activePageId === pageId;
    const requestedScrollTop = pendingScrollPageId === pageId
      ? pendingScrollTop
      : savedScrollTop(pageId);
    pendingScrollTop = undefined;
    pendingScrollPageId = undefined;
    pageOwner.abort();
    pageOwner = new AbortController();
    let pagePopulated = false;
    /**
     * @param {HTMLElement | undefined} page
     */
    const restoreScroll = (page) => {
      const sectionId = parameters.get('section')?.trim();
      const section = sectionId ? root.ownerDocument.getElementById(sectionId) : null;
      if (section && page?.contains(section)) {
        section.scrollIntoView?.();
      } else {
        const savedTop = requestedScrollTop ?? pageState.get(pageId)?.scrollTop ?? savedScrollTop(pageId);
        if (savedTop === undefined) return;
        const scrollingElement = pageScroller instanceof HTMLElement
          ? pageScroller
          : root.ownerDocument.scrollingElement ?? root.ownerDocument.documentElement;
        scrollingElement.scrollTop = savedTop;
      }
    };
    let populationDeferred = false;
    /** @type {HTMLElement | null} */
    let retainedRouteTabs = null;
    if (activePageId && activePageId !== pageId) {
      const activePage = pages.find((candidate) => candidate.dataset.pageId === activePageId);
      if (activePage) {
        retainedRouteTabs = cloneRouteTabsForPage(activePage, pageId, parameters);
        pageState.set(activePageId, {
          details: [...activePage.querySelectorAll('details')].map((details) => details.open),
          scrollTop: scrollTop()
        });
        disconnectLazyViews(activePage);
        if (activePage.dataset.retainOnNavigation !== 'true') {
          activePage.replaceChildren();
          activePage.removeAttribute('aria-busy');
          activePage.setAttribute('data-page-pending', '');
        }
      }
    }
    activePageId = pageId;
    const routeParameters = Object.fromEntries([...parameters.entries()].map(([key, value]) => [key, value]));
    const queryContext = horizonQueryContext
      ? {
          ...pageQueryContext.get(pageId),
          ...horizonQueryContext,
          timeWindow: pageId === 'readiness' ? undefined : horizonQueryContext.timeWindow
        }
      : pageQueryContext.get(pageId);
    const pageIndex = pages.findIndex((candidate) => candidate.dataset.pageId === pageId);
    const pendingPage = pages[pageIndex];
    const pendingRouteParameter = pendingPage?.dataset.routeParameter ?? '';
    const pendingRouteValue = pendingRouteParameter
      ? parameters.get(pendingRouteParameter)?.trim() ?? ''
      : '';
    const routeValueChanged = reactivatingPage
      && Boolean(pendingRouteParameter)
      && (pendingPage?.dataset.routeValue ?? '') !== pendingRouteValue;
    if (pendingPage && (
      pendingPage.hasAttribute('data-page-pending')
      || pendingPage.dataset.retainOnNavigation === 'true'
      || reloadPopulatedPages
      || routeValueChanged
    )) {
      const populate = () => {
        if (revision !== activationRevision || activePageId !== pageId) return;
        let currentPage = pages[pageIndex];
        if (!currentPage) return;
        /** @param {HTMLElement} renderedPage */
        const replacePage = (renderedPage) => {
          if (revision !== activationRevision || activePageId !== pageId || !currentPage.parentNode) return;
          const routeInitialized = currentPage.hasAttribute('data-route-value');
          const filterState = captureCardFilterState(currentPage);
          const detailsState = pageState.get(pageId)?.details ?? [];
          [...renderedPage.querySelectorAll('details')].forEach((details, index) => {
            if (detailsState[index] !== undefined) details.open = detailsState[index];
          });
          const active = currentPage.ownerDocument.activeElement;
          const focusedId = pendingQueryFocusId
            || (active instanceof HTMLElement && currentPage.contains(active) ? active.id : '');
          pendingQueryFocusId = '';
          renderedPage.dataset.routeValue = currentPage.dataset.routeValue ?? '';
          currentPage.replaceWith(renderedPage);
          restoreCardListPagination(currentPage, renderedPage);
          restoreCardFilterState(renderedPage, filterState);
          pages[pageIndex] = renderedPage;
          currentPage = renderedPage;
          if (focusedId) {
            const focusTarget = [...renderedPage.querySelectorAll('[id]')].find((node) => node.id === focusedId);
            if (focusTarget instanceof HTMLElement) focusTarget.focus({ preventScroll: true });
          }
          enableLazyViews(renderedPage);
          emitDashboardDebugEvent(root.ownerDocument, DASHBOARD_RENDER_EVENT, {
            kind: 'page',
            pageId,
            status: 'completed'
          });
          pagePopulated = true;
          syncFullViewMode(renderedPage);
          if (routeInitialized) {
            dispatchPageRoute(renderedPage, renderedPage.dataset.routeParameter ?? '', renderedPage.dataset.routeValue);
          }
          const shouldRestoreScroll = deferPopulation
            || requestedScrollTop !== undefined
            || savedScrollTop(pageId) !== undefined;
          if (shouldRestoreScroll) {
            restoreScroll(renderedPage);
            if (requestedScrollTop !== undefined || savedScrollTop(pageId) !== undefined) {
              root.ownerDocument.defaultView?.requestAnimationFrame(() => {
                if (revision === activationRevision && activePageId === pageId) restoreScroll(renderedPage);
              });
            }
          }
        };
        const rendered = renderPageById?.(pageId, {
          signal: pageOwner.signal,
          onUpdate: () => {},
          syncPageChrome: () => {
            syncFullViewMode(currentPage);
          },
          routeParameters,
          queryContext,
          renderUpdate: replacePage
        });
        if (!rendered) return;
        if (rendered instanceof Promise) {
          if (pendingPage.hasAttribute('data-page-pending')) {
            showPageSkeleton(pendingPage, retainedRouteTabs);
          }
          void rendered.then(replacePage).catch((error) => {
            if (revision !== activationRevision || activePageId !== pageId || !currentPage.parentNode) return;
            debugRender('page render failed', {
              pageId,
              message: error instanceof Error ? error.message : String(error)
            });
            emitDashboardDebugEvent(root.ownerDocument, DASHBOARD_RENDER_EVENT, {
              kind: 'page',
              pageId,
              status: 'failed',
              message: error instanceof Error ? error.message : String(error)
            });
            currentPage.setAttribute('data-page-pending', '');
            currentPage.replaceChildren(renderPageLoadError(error, () => activate(pageId, parameters)));
            currentPage.removeAttribute('aria-busy');
            currentPage.removeAttribute('aria-label');
          });
        } else {
          replacePage(rendered);
        }
      };
      if (deferPopulation && pendingPage.dataset.retainOnNavigation !== 'true') {
        populationDeferred = true;
        showPageSkeleton(pendingPage, retainedRouteTabs);
        schedulePopulation(populate);
      } else {
        populate();
      }
    }
    for (const crumb of root.querySelectorAll('[data-route-breadcrumb]')) crumb.remove();
    for (const [index, link] of [breadcrumbRoot, breadcrumbDashboard].entries()) {
      if (!(link instanceof HTMLAnchorElement)) continue;
      link.hidden = defaultBreadcrumbs[index].hidden;
      link.textContent = defaultBreadcrumbs[index].label;
      if (defaultBreadcrumbs[index].href) {
        link.setAttribute('href', defaultBreadcrumbs[index].href);
      } else {
        link.removeAttribute('href');
      }
    }
    for (const page of pages) {
      const isActive = page.dataset.pageId === pageId;
      page.hidden = !isActive;
    }
    if (breadcrumbDashboard instanceof HTMLAnchorElement && pageId === overviewPage?.dataset.pageId) {
      breadcrumbDashboard.hidden = true;
    }
    const page = pages.find((candidate) => candidate.dataset.pageId === pageId);
    syncFullViewMode(page);
    const routeNavigationPage = page?.dataset.routeNavigationPage;
    const routeParameter = page?.dataset.routeParameter;
    const routeValue = routeParameter ? parameters.get(routeParameter)?.trim() ?? '' : '';
    if (page) page.dataset.routeValue = routeValue;
    const activeNavigationPageId = determineActiveNavigationPageId(pageId, routeNavigationPage, links, availableIds, page?.dataset.routeValue ?? '');
    if (activeNavigationPageId) {
      updateNavigationLinks(links, activeNavigationPageId);
    }
    if (routeNavigationPage && availableIds.has(routeNavigationPage)) {
      const navigationLink = links.find((link) => getNavigationPageId(link) === routeNavigationPage);
      if (breadcrumbRoot instanceof HTMLAnchorElement && navigationLink) {
        breadcrumbRoot.hidden = false;
        breadcrumbRoot.textContent = navigationLink.textContent ?? routeNavigationPage;
        breadcrumbRoot.href = `#page-${routeNavigationPage}`;
      }
      if (breadcrumbDashboard instanceof HTMLAnchorElement) breadcrumbDashboard.hidden = true;
    }
    const queryTitle = resolveQueryDrillPageTitle(parameters, knownQueries);
    const formattedRouteTitle = page?.dataset.routeTitleFormat === 'title-case'
      ? titleCase(routeValue)
      : routeValue;
    const title = page?.dataset.routeBreadcrumbs
      ? page.dataset.pageTitle || ''
      : queryTitle || provisionalTitle.trim() || formattedRouteTitle || page?.dataset.pageTitle || '';
    if (page?.dataset.routeBreadcrumbs) {
      const breadcrumbs = /** @type {import('./components/dashboard-header.js').RouteBreadcrumb[]} */ (JSON.parse(page.dataset.routeBreadcrumbs));
      for (const link of [breadcrumbRoot, breadcrumbDashboard]) {
        if (link instanceof HTMLElement) link.hidden = true;
      }
      breadcrumbRoot?.parentElement?.append(...renderRouteBreadcrumbs({
        breadcrumbs,
        pages: pages.map(candidate => ({
          id: candidate.dataset.pageId ?? '', title: candidate.dataset.pageTitle ?? '',
          titleFormat: candidate.dataset.routeTitleFormat
        })),
        parameters, activePage: pageId
      }));
    }
    const description = provisionalDescription.trim() || page?.dataset.pageDescription || '';
    if (breadcrumbPage) breadcrumbPage.textContent = title;
    if (pageTitle) pageTitle.textContent = title;
    syncPageExperimental(page);
    updateDocumentTitle(root.ownerDocument, title, dashboardTitle);
    renderPageTitleLink(pageTitleLink, null);
    if (pageDescription) {
      pageDescription.textContent = description;
      pageDescription.toggleAttribute('hidden', description.length === 0);
    }
    const mode = page?.dataset.pageModeIndicator === 'true'
      ? resolveModeIndicator(root.ownerDocument.defaultView?.location.search ?? '')
      : '';
    renderPageMode(pageMode, mode);
    if (page && !populationDeferred) dispatchPageRoute(page, routeParameter ?? '', routeValue);
    if (page && !populationDeferred && !pagePopulated) {
      emitDashboardDebugEvent(root.ownerDocument, DASHBOARD_RENDER_EVENT, {
        kind: 'page',
        pageId,
        status: 'completed'
      });
    }
    if (!populationDeferred) restoreScroll(page);
  };

  root.addEventListener('dashboard-page-availability-change', (event) => {
    if (!(event instanceof CustomEvent) || !Array.isArray(event.detail)) return;
    for (const page of pages) {
      if (event.detail.includes(page.dataset.pageId)) page.setAttribute('data-page-pending', '');
    }
    if (!event.detail.includes(activePageId)) return;
    activate(activePageId, routeFromHash()?.parameters);
  }, { signal: navigationOwner.signal });
  const initialRoute = routeFromHash();
  const initialPageId = availableIds.has(defaultPageId) ? defaultPageId : pages[0].dataset.pageId ?? '';
  activate(initialRoute?.pageId ?? initialPageId, initialRoute?.parameters);
  const defaultView = root.ownerDocument.defaultView;
  const historyBack = root.querySelector('.mobile-history-back');
  const browserNavigation = /** @type {(EventTarget & {
   *   currentEntry?: { index: number } | null,
   *   entries?: () => Array<{ index: number, url: string | null }>
   * }) | undefined} */ (
    /** @type {Window & { navigation?: unknown }} */ (defaultView)?.navigation
  );
  const initialNavigationIndex = defaultView?.history.state?.[NAVIGATION_INDEX_STATE_KEY];
  let navigationIndex = Number.isSafeInteger(initialNavigationIndex) && initialNavigationIndex >= 0
    ? initialNavigationIndex
    : 0;
  /** @type {'forward'|'backward'|undefined} */
  let pendingNavigationDirection;
  /** @type {string | undefined} */
  let pendingNavigationHash;
  const previousEntryIsDashboard = () => {
    const currentEntry = browserNavigation?.currentEntry;
    const entries = browserNavigation?.entries?.();
    if (!defaultView || !currentEntry || !entries) return navigationIndex > 0;
    const previousEntry = entries.find((entry) => entry.index === currentEntry.index - 1);
    if (!previousEntry?.url) return false;
    try {
      const previousUrl = new URL(previousEntry.url);
      const currentUrl = defaultView.location;
      return previousUrl.origin === currentUrl.origin
        && previousUrl.pathname === currentUrl.pathname
        && previousUrl.search === currentUrl.search;
    } catch {
      return false;
    }
  };
  const declaredParentLink = () => {
    const parent = [...root.querySelectorAll('a[data-route-breadcrumb]')].at(-1);
    return parent instanceof HTMLAnchorElement ? parent : undefined;
  };
  const logicalParentPageId = () => {
    const declaredParent = declaredParentLink()?.dataset.navPageId;
    if (declaredParent && availableIds.has(declaredParent)) return declaredParent;
    const pageId = routeFromHash()?.pageId ?? activePageId;
    const parentPageId = pages.find((page) => page.dataset.pageId === pageId)
      ?.dataset.routeNavigationPage;
    return parentPageId && availableIds.has(parentPageId) ? parentPageId : undefined;
  };
  const syncHistoryBack = () => {
    if (!(historyBack instanceof HTMLButtonElement)) return;
    const parentPageId = logicalParentPageId()
      ?? (!previousEntryIsDashboard() ? initialPageId : undefined);
    historyBack.hidden = (routeFromHash()?.pageId ?? activePageId) === initialPageId;
    const parentLink = parentPageId
      ? declaredParentLink() ?? links.find((link) => getNavigationPageId(link) === parentPageId)
      : undefined;
    const label = parentPageId
      ? `Back to ${parentLink?.textContent?.trim() || parentPageId.replaceAll('-', ' ')}`
      : 'Go back';
    historyBack.setAttribute('aria-label', label);
    historyBack.title = label;
  };
  root.addEventListener('dashboard-route-parent-change', (event) => {
    if (!(event instanceof CustomEvent) || !(event.target instanceof Element)) return;
    const page = event.target.closest('.dashboard-page');
    if (!(page instanceof HTMLElement) || page.hidden) return;
    const navigationPage = typeof event.detail?.navigationPage === 'string'
      ? event.detail.navigationPage
      : '';
    page.dataset.routeNavigationPage = navigationPage;
    syncHistoryBack();
  }, { signal: navigationOwner.signal });
  root.addEventListener('dashboard-history-push', (event) => {
    if (!(event instanceof CustomEvent) || !(event.target instanceof Element) || !defaultView) return;
    const page = event.target.closest('.dashboard-page');
    if (!(page instanceof HTMLElement) || page.hidden) return;
    event.preventDefault();
    const detailState = isPlainObject(event.detail?.state) ? event.detail.state : {};
    const currentState = isPlainObject(defaultView.history.state) ? defaultView.history.state : {};
    navigationIndex += 1;
    defaultView.history.pushState(
      { ...currentState, ...detailState, [NAVIGATION_INDEX_STATE_KEY]: navigationIndex },
      '',
      defaultView.location.href
    );
    syncHistoryBack();
    defaultView.dispatchEvent(new CustomEvent('dashboard-history-change'));
  }, { signal: navigationOwner.signal });
  if (defaultView && initialNavigationIndex !== navigationIndex) {
    const state = defaultView.history.state && typeof defaultView.history.state === 'object'
      ? defaultView.history.state
      : {};
    defaultView.history.replaceState(
      { ...state, [NAVIGATION_INDEX_STATE_KEY]: navigationIndex },
      '',
      defaultView.location.href
    );
  }
  syncHistoryBack();
  historyBack?.addEventListener('click', () => {
    const parentPageId = logicalParentPageId()
      ?? (!previousEntryIsDashboard() ? initialPageId : undefined);
    if (!parentPageId) {
      defaultView?.history.back();
      return;
    }
    commitScrollTop();
    navigationIndex += 1;
    const parentHref = declaredParentLink()?.getAttribute('href') ?? `#page-${parentPageId}`;
    defaultView?.history.pushState(
      { [NAVIGATION_INDEX_STATE_KEY]: navigationIndex },
      '',
      parentHref
    );
    activate(parentPageId, new URLSearchParams(parentHref.split('?', 2)[1] ?? ''), true);
    syncHistoryBack();
    if (pageTitle instanceof HTMLElement) pageTitle.focus();
  }, { signal: navigationOwner.signal });
  root.addEventListener('click', (event) => {
    if (!(event.target instanceof Element)) return;
    const link = event.target.closest('[data-nav-page-id], [data-mobile-nav-page-id]');
    if (!(link instanceof HTMLAnchorElement)) return;
    event.preventDefault();
    if (link.getAttribute('aria-disabled') === 'true') return;
    // Current route tabs already show the active view; sidebar links remain navigable.
    if (link.getAttribute('aria-current') === 'page' && link.closest('[data-route-tabs]')) return;
    pendingNavigationDirection = undefined;
    pendingNavigationHash = undefined;
    const pageId = getNavigationPageId(link);
    if (!pageId || !availableIds.has(pageId)) return;
    const transitionKind = navigationTransitionKind(pageId, new URLSearchParams(link.hash.split('?', 2)[1] ?? ''));
    const provisionalTitle = link.dataset.routeTitle ?? '';
    const provisionalDescription = link.dataset.routeDescription ?? '';
    commitScrollTop();
    navigationIndex += 1;
    defaultView?.history.pushState({ [NAVIGATION_INDEX_STATE_KEY]: navigationIndex }, '', link.href);
    syncHistoryBack();
    defaultView?.dispatchEvent(new CustomEvent('dashboard-history-change'));
    primePageChrome(pageId, provisionalTitle, provisionalDescription);
    const navigate = () => {
      activate(pageId, routeFromHash()?.parameters, true, provisionalTitle, provisionalDescription);
      syncHistoryBack();
    };
    if (pages.find((candidate) => candidate.dataset.pageId === pageId)?.dataset.retainOnNavigation === 'true'
      || pages.find((candidate) => candidate.dataset.pageId === activePageId)?.dataset.retainOnNavigation === 'true') {
      navigate();
    } else {
      updateWithViewTransition(
        root.ownerDocument,
        navigate,
        'forward',
        transitionKind
      );
    }
    if (pageTitle instanceof HTMLElement) pageTitle.focus();
  }, { signal: navigationOwner.signal });
  root.addEventListener('dashboard-query-context-change', (event) => {
    if (!(event instanceof CustomEvent)) return;
    const detail = isPlainObject(event.detail) ? event.detail : {};
    const pageId = typeof detail.pageId === 'string' && detail.pageId
      ? detail.pageId
      : activePageId;
    if (!pageId || pageId !== activePageId || !availableIds.has(pageId)) return;
    const nextContext = normalizeDashboardQueryContext(detail.queryContext);
    if (sameDashboardQueryContext(pageQueryContext.get(pageId), nextContext)) return;
    if (horizonQueryContext) horizonQueryContext.filters = nextContext?.filters;
    const focused = root.ownerDocument.activeElement;
    pendingQueryFocusId = focused instanceof HTMLElement && root.contains(focused) ? focused.id : '';
    if (nextContext) pageQueryContext.set(pageId, nextContext);
    else pageQueryContext.delete(pageId);
    pages.find((page) => page.dataset.pageId === pageId)?.setAttribute('data-page-pending', '');
    const route = routeFromHash();
    activate(route?.pageId ?? pageId, route?.parameters ?? new URLSearchParams(), true);
  }, { signal: navigationOwner.signal });
  root.addEventListener('dashboard-horizon-filters-change', (event) => {
    if (!(event instanceof CustomEvent) || !activePageId) return;
    horizonQueryContext = {
      filters: event.detail.filters,
      timeWindow: event.detail.timeWindow
    };
    for (const page of pages) page.setAttribute('data-page-pending', '');
    root.dispatchEvent(new CustomEvent('dashboard-query-context-change', {
      detail: {
        pageId: activePageId,
        queryContext: {
          ...pageQueryContext.get(activePageId),
          timeWindow: activePageId === 'readiness' ? undefined : horizonQueryContext.timeWindow,
          filters: event.detail.filters
        }
      }
    }));
  }, { signal: navigationOwner.signal });
  root.addEventListener('dashboard-retry-page', () => {
    const route = routeFromHash();
    const page = pages.find((candidate) => candidate.dataset.pageId === activePageId);
    page?.setAttribute('data-page-pending', '');
    activate(activePageId, route?.parameters ?? new URLSearchParams(), true);
  }, { signal: navigationOwner.signal });

  const disposeFullViewScrollForwarding = enableFullViewScrollForwarding(root, defaultView);
  /** @param {PopStateEvent} event */
  const onPopState = (event) => {
    if (!root.isConnected) {
      defaultView?.removeEventListener('hashchange', onHashChange);
      defaultView?.removeEventListener('popstate', onPopState);
      browserNavigation?.removeEventListener('currententrychange', syncHistoryBack);
      return;
    }
    const index = event.state?.[NAVIGATION_INDEX_STATE_KEY];
    const nextNavigationIndex = Number.isSafeInteger(index) && index >= 0 ? index : 0;
    pendingNavigationDirection = nextNavigationIndex < navigationIndex
      ? 'backward'
      : nextNavigationIndex > navigationIndex
        ? 'forward'
        : undefined;
    pendingNavigationHash = defaultView?.location.hash;
    pendingScrollTop = normalizeScrollTop(event.state?.[SCROLL_TOP_STATE_KEY]);
    pendingScrollPageId = typeof event.state?.[SCROLL_PAGE_ID_STATE_KEY] === 'string'
      ? event.state[SCROLL_PAGE_ID_STATE_KEY]
      : undefined;
    navigationIndex = nextNavigationIndex;
    syncHistoryBack();
  };
  const onHashChange = () => {
    if (!root.isConnected) {
      defaultView?.removeEventListener('hashchange', onHashChange);
      defaultView?.removeEventListener('popstate', onPopState);
      browserNavigation?.removeEventListener('currententrychange', syncHistoryBack);
      return;
    }
    syncHistoryBack();
    const route = routeFromHash();
    const navigationDirection = pendingNavigationHash === defaultView?.location.hash
      ? pendingNavigationDirection
      : undefined;
    pendingNavigationDirection = undefined;
    pendingNavigationHash = undefined;
    primePageChrome(route?.pageId ?? initialPageId);
    const historyScrollTop = pendingScrollPageId === route?.pageId ? pendingScrollTop : undefined;
    const targetId = route?.pageId ?? initialPageId;
    const transitionKind = navigationTransitionKind(targetId, route?.parameters);
    const navigate = () => {
      activate(targetId, route?.parameters, true);
      syncHistoryBack();
    };
    if (pages.find((candidate) => candidate.dataset.pageId === targetId)?.dataset.retainOnNavigation === 'true'
      || pages.find((candidate) => candidate.dataset.pageId === activePageId)?.dataset.retainOnNavigation === 'true') navigate();
    else updateWithViewTransition(root.ownerDocument, navigate, navigationDirection, transitionKind);
    if (historyScrollTop !== undefined && !route?.parameters.has('section')) {
      const scrollingElement = pageScroller instanceof HTMLElement
        ? pageScroller
        : root.ownerDocument.scrollingElement ?? root.ownerDocument.documentElement;
      scrollingElement.scrollTop = historyScrollTop;
    }
    if (pageTitle instanceof HTMLElement) pageTitle.focus();
  };
  browserNavigation?.addEventListener('currententrychange', syncHistoryBack, { signal: navigationOwner.signal });
  defaultView?.addEventListener('popstate', onPopState, { signal: navigationOwner.signal });
  defaultView?.addEventListener('hashchange', onHashChange, { signal: navigationOwner.signal });
  return () => {
    disposeFullViewScrollForwarding();
    disposeNavigation();
  };
}

/**
 * @param {URLSearchParams} parameters
 * @param {Set<string>} knownQueries
 */
export function resolveQueryDrillPageTitle(parameters, knownQueries) {
  const queryName = parameters.get('query')?.trim() ?? '';
  return knownQueries.has(queryName) ? parameters.get('title')?.trim() ?? '' : '';
}

/**
 * @param {Document} ownerDocument
 * @param {string} pageTitle
 * @param {string} dashboardTitle
 */
function updateDocumentTitle(ownerDocument, pageTitle, dashboardTitle) {
  ownerDocument.title = [pageTitle, dashboardTitle]
    .map((part) => part.trim())
    .filter(Boolean)
    .join(' · ');
}

/**
 * @param {HTMLAnchorElement[]} links
 * @param {string} pageId
 */
function updateNavigationLinks(links, pageId) {
  for (const link of links) {
    const isActive = getNavigationPageId(link) === pageId;
    link.classList.toggle('active', isActive);
    if (isActive) {
      link.setAttribute('aria-current', 'page');
      const section = link.closest('.nav-section');
      if (section instanceof HTMLDetailsElement) section.open = true;
    }
    else link.removeAttribute('aria-current');
  }
}

/**
 * Prefer the currently displayed page when it is a direct sidebar destination.
 * For route-driven drilldowns, fall back to the route parent so breadcrumb and
 * context links still stay highlighted without overriding an explicit sidebar
 * page selection such as Notifications.
 * @param {string | undefined} pageId
 * @param {string | undefined} routeNavigationPage
 * @param {HTMLAnchorElement[]} links
 * @param {Set<string>} availableIds
 * @param {string} [routeValue]
 * @returns {string | undefined}
 */
function determineActiveNavigationPageId(pageId, routeNavigationPage, links, availableIds, routeValue = '') {
  const hasRouteNavigationLink = routeNavigationPage && availableIds.has(routeNavigationPage) && links.some((link) => getNavigationPageId(link) === routeNavigationPage);
  if (hasRouteNavigationLink && routeValue.length > 0) {
    return routeNavigationPage;
  }
  const hasPageLink = pageId && availableIds.has(pageId) && links.some((link) => getNavigationPageId(link) === pageId);
  if (hasPageLink) {
    return pageId;
  }
  if (hasRouteNavigationLink) {
    return routeNavigationPage;
  }
  return undefined;
}

/**
 * @param {HTMLAnchorElement} link
 * @returns {string}
 */
function getNavigationPageId(link) {
  return link.dataset.navPageId ?? link.dataset.mobileNavPageId ?? '';
}

/** @param {string} pageId */
function inventoryPage(pageId) {
  return pageId === 'agents' || pageId === 'work' || pageId === 'work-tasks' || pageId === 'work-roadmap';
}

/**
 * @param {Element | null} pageMode
 * @param {string} mode
 */
function renderPageMode(pageMode, mode) {
  if (!(pageMode instanceof HTMLElement)) return;
  pageMode.replaceChildren();
  pageMode.className = `mode-indicator${mode ? ` mode-${mode}` : ''}`;
  pageMode.hidden = !mode;
  if (mode) pageMode.append(octicon(mode === 'review' ? 'beaker' : 'rocket'), titleCase(mode));
}

/**
 * Resolves the worker-produced payload for one authored view source.
 * Raw sources remain a fixture-only fallback for direct presenter tests.
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {string} pageId
 * @param {unknown} view
 * @param {number} viewIndex
 * @param {string} sourceName
 * @param {number} sourceIndex
 */
function resolveViewSourceName(sources, pageId, view, viewIndex, sourceName, sourceIndex) {
  const alias = dashboardViewAliasName(pageId, view, viewIndex, sourceName, sourceIndex);
  return sources[alias] ? alias : sourceName;
}

/** @param {unknown} value @returns {PageSourceLoadOptions['queryContext']} */
function normalizeDashboardQueryContext(value) {
  if (!isPlainObject(value)) return undefined;
  const filters = isPlainObject(value.filters)
    ? Object.fromEntries(Object.entries(value.filters).flatMap(([field, values]) => {
        const normalized = Array.isArray(values)
          ? values.filter((entry) => typeof entry === 'string').map(String)
          : [];
        return normalized.length > 0 ? [[field, normalized]] : [];
      }))
    : undefined;
  const search = isPlainObject(value.search)
    && Array.isArray(value.search.fields)
    && typeof value.search.query === 'string'
    ? {
        fields: value.search.fields.filter((field) => typeof field === 'string' && field.trim()).map(String),
        query: value.search.query.trim()
      }
    : undefined;
  const orderBy = Array.isArray(value.orderBy)
    ? value.orderBy.flatMap((ordering) => {
        if (!isPlainObject(ordering) || typeof ordering.field !== 'string' || !ordering.field.trim()) return [];
        const direction = ordering.direction === 'asc' || ordering.direction === 'desc' ? ordering.direction : undefined;
        return [{ field: ordering.field, ...(direction ? { direction } : {}) }];
      })
    : [];
  const timeWindow = isPlainObject(value.timeWindow)
    ? {
        start: typeof value.timeWindow.start === 'string' ? value.timeWindow.start : undefined,
        end: typeof value.timeWindow.end === 'string' ? value.timeWindow.end : undefined
      }
    : undefined;
  const viewMode = value.viewMode === 'chart' || value.viewMode === 'table' || value.viewMode === 'card'
    ? value.viewMode
    : undefined;
  const formValues = isPlainObject(value.formValues)
    ? Object.fromEntries(Object.entries(value.formValues).filter(([, entry]) => (
        ['string', 'number', 'boolean'].includes(typeof entry)
        && (typeof entry !== 'number' || Number.isFinite(entry))
      )))
    : undefined;
  const viewFilters = normalizeViewFilters(value.viewFilters);
  return {
    ...(filters && Object.keys(filters).length > 0 ? { filters } : {}),
    ...(search && search.fields.length > 0 && search.query ? { search } : {}),
    ...(orderBy.length > 0 ? { orderBy } : {}),
    ...(timeWindow?.start || timeWindow?.end ? { timeWindow } : {}),
    ...(viewMode ? { viewMode } : {}),
    ...(viewFilters ? { viewFilters } : {}),
    ...(formValues && Object.keys(formValues).length > 0 ? { formValues } : {})
  };
}

/** @param {PageSourceLoadOptions['queryContext']} left @param {PageSourceLoadOptions['queryContext']} right */
function sameDashboardQueryContext(left, right) {
  return JSON.stringify(left ?? {}) === JSON.stringify(right ?? {});
}

/**
 * @param {Array<Record<string, unknown>>} rows
 * @returns {DataState['availability']}
 */
function inferAvailability(rows) {
  return rows.length > 0 ? 'available' : 'empty';
}

/**
 * @param {Map<string, LogicalSourceInput>} pageSources
 * @returns {DataState}
 */
function summarizeDataState(pageSources) {
  const sourceInputs = [...pageSources.values()];
  const metadata = sourceInputs.map((source) => source.metadata);
  const availabilities = sourceInputs.map((source) => source.metadata.availability ?? inferAvailability(source.rows));
  return {
    availability: availabilities.includes('unavailable')
      ? 'unavailable'
      : availabilities.length === 0 || availabilities.every((value) => value === 'empty')
        ? 'empty'
        : 'available',
    completeness: metadata.some((value) => value.completeness === 'partial')
      ? 'partial'
      : metadata.length > 0 && metadata.every((value) => value.completeness === 'complete')
        ? 'complete'
        : 'unknown',
    freshness: metadata.some((value) => value.freshness === 'stale')
      ? 'stale'
      : metadata.length > 0 && metadata.every((value) => value.freshness === 'fresh')
        ? 'fresh'
        : 'unknown'
  };
}

/**
 * @param {string} pageId
 * @param {unknown} view
 * @param {number} index
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {Record<string, { name: string, symbol: string, significant: number }>} units
 * @param {Record<string, { id: string, icon: string, title: TableField, subtitle?: TableField, labels: TableField[], details: TableField[] }>} cardTemplates
 * @param {'h2'|'h3'|'h4'} [headingTag]
 * @param {string} [routeParameter]
 * @param {PageSourceLoadOptions['queryContext']} [queryContext]
 * @returns {HTMLElement}
 */
function renderCustomView(pageId, view, index, sources, units, cardTemplates, headingTag = 'h3', routeParameter, queryContext) {
  const fallbackTitle = `View ${index + 1}`;
  if (!isPlainObject(view)) {
    return renderCustomViewState(pageId, fallbackTitle, null, 'unavailable', ['Invalid custom view definition.'], headingTag);
  }

  const title = getViewTitle(view, index);
  const unavailableMessage = viewBackendUnavailableMessage(view, dashboardDataBackend());
  if (unavailableMessage) {
    const rendered = renderCustomViewState(pageId, title, getViewSources(view).join(', ') || null, 'unavailable', [
      `Dashboard backend: ${dashboardDataBackend()}.`
    ], headingTag, unavailableMessage);
    rendered.setAttribute('data-view-backend-unavailable', '');
    return rendered;
  }

  /** @type {string[]} */
  const contextDetails = [];

  if (view.mark === 'element') {
    return renderElementView(pageId, title, view, index, sources, contextDetails, headingTag, routeParameter, queryContext);
  }
  if (view.mark === 'callout') {
    return renderCalloutView(pageId, view, title, headingTag);
  }

  const authoredSourceName = getViewSources(view)[0] ?? null;
  if (!authoredSourceName) {
    return renderCustomViewState(pageId, title, null, 'unavailable', ['Source unavailable.'], headingTag);
  }

  const resolvedSourceName = resolveViewSourceName(sources, pageId, view, index, authoredSourceName, 0);

  const sourceInput = sources[resolvedSourceName];
  if (!sourceInput || !Array.isArray(sourceInput.rows)) {
    return renderCustomViewState(pageId, title, authoredSourceName, 'unavailable', [`Source unavailable: ${authoredSourceName}`], headingTag);
  }

  const filteredRows = sourceInput.rows;
  const metadata = sourceInput.metadata;
  const state = sourceInput.metadata?.availability ?? inferAvailability(filteredRows);
  const emptyMessage = typeof view['empty-message'] === 'string' ? view['empty-message'] : undefined;
  const isMetricCard = view.mark === 'metric'
    && isPlainObject(view.metric)
    && view.metric.style === 'card';

  if (state !== 'available' && view.mark !== 'list' && !(state === 'empty' && view.mark === 'table') && !isMetricCard) {
    return renderCustomViewState(
      pageId,
      title,
      authoredSourceName,
      state,
      contextDetails,
      headingTag,
      state === 'empty' ? emptyMessage : undefined,
      state === 'unavailable' ? metadata?.['query-error'] : undefined,
      state === 'unavailable' ? metadata?.['query-diagnostic'] : undefined
    );
  }

  if (filteredRows.length === 0 && view.mark !== 'table' && view.mark !== 'list' && !isMetricCard) {
    return renderCustomViewState(pageId, title, authoredSourceName, 'empty', contextDetails, headingTag, emptyMessage);
  }

  // Swimlanes consume the same paginated run source as their companion lazy
  // table, but render each continuation page as it arrives.
  const sourcePage = view['lazy-list'] === true || supportsIncrementalChartContinuation(view)
    ? sourceContinuation(sourceInput)
    : undefined;
  const rendered = renderDataView(typeof view.mark === 'string' ? view.mark : '', {
    pageId,
    title,
    view,
    sourceName: authoredSourceName,
    rows: filteredRows,
    sourceTruncated: sourceInput.continuationToken !== undefined
      || Number(metadata['total-row-count']) > filteredRows.length,
    rowLimit: Number(/** @type {Record<PropertyKey, unknown>} */ (view.data ?? {})[TABLE_ROW_LIMIT]),
    metadata,
    contextDetails,
    headingTag,
    units,
    cardTemplates,
    prepareTableRows,
    buildChartPoints,
    prepareChartPoints,
    toText: toViewText,
    continuation: sourcePage ? {
      ...sourcePage,
      load: async (token) => {
        const next = await sourcePage.load(token);
        return {
          rows: next?.rows ?? [],
          continuationToken: next?.continuationToken
        };
      }
    } : undefined
  });
  if (rendered) return rendered;

  return renderCustomViewState(pageId, title, authoredSourceName, 'unavailable', [...contextDetails, 'Unsupported view mark.'], headingTag);
}

/**
 * @param {string} pageId
 * @param {Record<string, unknown>} view
 * @param {string} title
 * @param {'h2'|'h3'|'h4'} headingTag
 * @returns {HTMLElement}
 */
function renderCalloutView(pageId, view, title, headingTag) {
  const definition = isPlainObject(view.callout) ? view.callout : {};
  const viewId = typeof view.id === 'string' ? view.id : 'callout';
  const headingId = `${pageId}-${viewId}-callout-heading`;
  return h(
    'aside',
    { className: 'dashboard-callout', role: 'note', 'aria-labelledby': headingId },
    h(
      'div',
      { className: 'dashboard-callout-heading' },
      octicon(typeof definition.icon === 'string' ? definition.icon : 'info'),
      h(
        'div',
        null,
        h('span', { className: 'scope-kicker' }, typeof definition.label === 'string' ? definition.label : 'Note'),
        h(headingTag, { id: headingId }, title)
      )
    ),
    h('p', null, typeof view.description === 'string' ? view.description : '')
  );
}

/**
 * @param {unknown} view
 * @param {number} index
 * @returns {string}
 */
function getViewTitle(view, index) {
  if (isPlainObject(view)) {
    if (
      view.disclosure === 'supplemental'
      && typeof view['disclosure-label'] === 'string'
      && view['disclosure-label'].length > 0
    ) {
      return view['disclosure-label'];
    }

    if (typeof view.title === 'string' && view.title.length > 0) {
      return view.title;
    }
    if (typeof view.id === 'string' && view.id.length > 0) {
      return titleCase(view.id);
    }
  }
  return `View ${index + 1}`;
}

/**
 * @param {HTMLElement} rendered
 * @param {unknown} view
 * @param {number} index
 */
function suppressSupplementalTableHeading(rendered, view, index) {
  if (!isPlainObject(view) || view.mark !== 'table' || view.disclosure !== 'supplemental') return;
  const section = rendered.matches('.page-section') ? rendered : rendered.querySelector('.page-section');
  if (!(section instanceof HTMLElement)) return;
  section.querySelector(':scope > h3, :scope > h4')?.remove();
  section.removeAttribute('aria-labelledby');
  section.setAttribute('aria-label', getViewTitle(view, index));
}

/**
 * @param {string} pageId
 * @param {string} title
 * @param {Record<string, unknown>} view
 * @param {number} viewIndex
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {string[]} contextDetails
 * @param {'h2'|'h3'|'h4'} headingTag
 * @param {string} [routeParameter]
 * @param {PageSourceLoadOptions['queryContext']} [queryContext]
 * @returns {HTMLElement}
 */
function renderElementView(pageId, title, view, viewIndex, sources, contextDetails, headingTag, routeParameter, queryContext) {
  const elementName = typeof view.element === 'string' ? view.element : '';
  const sourceNames = getViewSources(view);
  const viewData = isPlainObject(view.data) ? view.data : undefined;
  if (sourceNames.length === 0 && !['query-editor', 'command-terminal', 'storage-info', 'environment-defaults', 'prompt-wizard'].includes(elementName)) {
    return renderCustomViewState(pageId, title, null, 'unavailable', [...contextDetails, 'No sources declared for element view.'], headingTag);
  }

  const selectedSources = Object.fromEntries(sourceNames.flatMap((sourceName, sourceIndex) => {
    const resolvedName = resolveViewSourceName(sources, pageId, view, viewIndex, sourceName, sourceIndex);
    const source = sources[resolvedName];
    return source && Array.isArray(source.rows)
      ? [[sourceName, source]]
      : [];
  }));
  const hashParameters = new URLSearchParams(globalThis.location?.hash.split('?')[1] ?? '');
  const declaredParameters = [
    routeParameter,
    ...(Array.isArray(viewData?.arguments) ? viewData.arguments.flatMap((argument) => (
      isPlainObject(argument) && typeof argument.name === 'string' ? [argument.name] : []
    )) : [])
  ].filter((parameter) => typeof parameter === 'string');
  const routeParameters = Object.fromEntries(declaredParameters.flatMap((parameter) => (
    hashParameters.has(parameter) ? [[parameter, hashParameters.get(parameter) ?? '']] : []
  )));

  if (sourceNames.length === 1) {
    const sourceName = sourceNames[0];
    const source = selectedSources[sourceName];
    if (!source
        && !elementLoadsSourcesAsync(elementName)
        && !elementHandlesUnavailableSource(elementName)) {
      return renderCustomViewState(pageId, title, sourceName, 'unavailable', contextDetails, headingTag);
    }
    const state = source?.metadata?.availability ?? (source ? inferAvailability(source.rows) : 'unavailable');
    if (state !== 'available'
        && !(state === 'empty' && elementHandlesEmptyRows(elementName))
        && !(state === 'unavailable' && elementLoadsSourcesAsync(elementName))
        && !(state === 'unavailable' && elementHandlesUnavailableSource(elementName))) {
      return renderCustomViewState(pageId, title, sourceName, state, contextDetails, headingTag);
    }
    if (source && source.rows.length === 0 && !elementHandlesEmptyRows(elementName)) {
      return renderCustomViewState(pageId, title, sourceName, 'empty', contextDetails, headingTag);
    }
  }

  const rendered = renderUiElement(elementName, {
    pageId,
    title,
    description: typeof view.description === 'string' ? view.description : undefined,
    sourceNames,
    sources: selectedSources,
    contextDetails,
    scope: isPlainObject(viewData?.scope) ? viewData.scope : undefined,
    time: isPlainObject(viewData?.time) ? viewData.time : undefined,
    titleLink: isPlainObject(view['title-link']) ? view['title-link'] : undefined,
    routeParameter,
    routeParameters,
    queryContext,
    viewId: typeof view.id === 'string' ? view.id : undefined,
    viewIndex,
    sourcesSubscribed: elementBindsPageSources(elementName),
    elementConfig: isPlainObject(view.config) ? view.config : undefined,
    headingTag
  });
  if (!rendered) {
    return renderCustomViewState(pageId, title, null, 'unavailable', [...contextDetails, 'Unsupported UI element.'], headingTag);
  }
  return ['summary-grid', 'readiness-verdict'].includes(elementName)
    ? renderPageSection(pageId, title, [rendered], headingTag, typeof view.description === 'string' ? view.description : undefined)
    : rendered;
}

/**
 * @param {Element | null} target
 * @param {unknown} candidate
 */
function renderPageTitleLink(target, candidate) {
  if (!(target instanceof HTMLAnchorElement)) return;
  const link = findLink({ link: candidate }, 'link');
  if (!link || link.href.startsWith('#')) {
    target.hidden = true;
    target.removeAttribute('href');
    target.removeAttribute('target');
    target.removeAttribute('rel');
    target.removeAttribute('aria-label');
    target.removeAttribute('title');
    target.replaceChildren();
    return;
  }
  target.hidden = false;
  const attrs = externalAnchorAttrs(link.href, link.label);
  for (const [name, value] of Object.entries(attrs)) {
    if (value === undefined) target.removeAttribute(name);
    else target.setAttribute(name, value);
  }
  target.removeAttribute('aria-label');
  target.title = link.label;
  target.replaceChildren(
    octicon('mark-github'),
    h('span', { className: 'sr-only' }, link.label)
  );
}

/**
 * @param {string} pageId
 * @param {string} title
 * @param {string | null} sourceName
 * @param {'available'|'empty'|'unavailable'} availability
 * @param {string[]} contextDetails
 * @param {'h2'|'h3'|'h4'} [headingTag]
 * @param {string} [message]
 * @param {unknown} [queryError]
 * @param {unknown} [queryDiagnostic]
 * @returns {HTMLElement}
 */
function renderCustomViewState(pageId, title, sourceName, availability, contextDetails, headingTag = 'h3', message, queryError, queryDiagnostic) {
  const queryFailed = typeof queryDiagnostic === 'string'
    && queryDiagnostic.length <= 1024
    && queryDiagnostic.startsWith('$.dashboard.queries[');
  return renderPageSection(pageId, title, [
    h(
      'div',
      {
        className: 'view-state-card',
        'data-view-state': availability,
        role: availability === 'unavailable' ? 'alert' : 'status'
      },
      octicon(availability === 'unavailable' ? 'alert' : 'info'),
      h(
        'div',
        { className: 'view-state-card-body' },
        h(
          'p',
          { className: 'view-state-message', 'data-view-availability': availability },
          message ?? (queryFailed
            ? 'This view cannot be shown because its data query failed.'
            : customViewAvailabilityMessage(availability))
        ),
        ...renderCustomViewStateDetails(sourceName, contextDetails, queryError, queryDiagnostic)
      )
    )
  ], headingTag);
}

/**
 * @param {unknown} view
 * @param {Record<string, unknown>} dashboardDefaults
 * @returns {unknown}
 */
function applyDashboardDefaults(view, dashboardDefaults) {
  if (!isPlainObject(view) || !isPlainObject(view.data)) return view;
  const data = view.data;
  const time = data.time ?? dashboardDefaults.time;
  return {
    ...view,
    data: {
      ...dashboardDefaults,
      ...data,
      scope: data.scope ?? dashboardDefaults.scope,
      time: resolveViewTime(time, dashboardDefaults.time),
      filters: data.filters ?? dashboardDefaults.filters
    }
  };
}

/**
 * @param {unknown} time
 * @param {unknown} dashboardTime
 * @returns {unknown}
 */
function resolveViewTime(time, dashboardTime) {
  if (!isPlainObject(time) || typeof time.range !== 'string') return time;
  if (!isPlainObject(dashboardTime) || typeof dashboardTime.end !== 'string') return time;
  const evaluatedAt = Date.parse(dashboardTime.end);
  if (!Number.isFinite(evaluatedAt)) return time;
  let hours;
  try {
    hours = dashboardHorizonHours(time.range);
  } catch {
    return time;
  }
  return {
    start: new Date(evaluatedAt - hours * 3_600_000).toISOString(),
    end: dashboardTime.end
  };
}

/**
 * @param {unknown} defaults
 * @param {string} horizonRange
 * @param {string} evaluatedAt
 * @returns {Record<string, unknown>}
 */
function resolveDashboardDefaults(defaults, horizonRange, evaluatedAt) {
  const configured = isPlainObject(defaults) ? defaults : {};
  if (!isPlainObject(configured.time)) {
    return { ...configured, time: { end: evaluatedAt } };
  }
  const evaluatedAtMs = Date.parse(evaluatedAt);
  const start = new Date(evaluatedAtMs - dashboardHorizonHours(horizonRange) * 3_600_000).toISOString();
  return {
    ...configured,
    time: { start, end: evaluatedAt }
  };
}

/**
 * @param {unknown} value
 * @returns {value is Record<string, any>}
 */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
