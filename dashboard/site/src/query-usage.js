import { navigationIndicatorSourceNamesForPage } from './navigation-indicator.js';
import { createDebug } from './debug.js';
import { dashboardViewSourceNames } from './view-filter-contract.js';

const debugQueryUsage = createDebug('query-usage');

/**
 * Builds the dependency graph connecting rendered dashboard content to
 * declarative queries.
 *
 * @param {Record<string, unknown>} dashboard
 */
export function buildDashboardQueryUsageGraph(dashboard) {
  const queries = Array.isArray(dashboard.queries) ? dashboard.queries : [];
  const queryNames = new Set(queries.flatMap((query) => (
    isRecord(query) && typeof query.name === 'string' ? [query.name] : []
  )));
  /** @type {Map<string, Set<string>>} */
  const graph = new Map();
  const roots = new Set();
  /** @type {Map<string, string>} */
  const queryPaths = new Map();
  /** @type {Map<string, string>} */
  const labels = new Map();
  /** @type {Map<string, string>} */
  const reusableViewNodes = new Map();

  /** @param {string} name */
  const queryNode = (name) => `query:${name}`;
  /** @param {string} node */
  const addNode = (node) => {
    if (!graph.has(node)) graph.set(node, new Set());
    return node;
  };
  /** @param {string} node @param {unknown[]} names */
  const addQueryEdges = (node, names) => {
    const edges = graph.get(addNode(node)) ?? new Set();
    for (const name of names) {
      if (typeof name === 'string' && queryNames.has(name)) edges.add(queryNode(name));
    }
  };

  queries.forEach((query, index) => {
    if (!isRecord(query) || typeof query.name !== 'string') return;
    const node = addNode(queryNode(query.name));
    labels.set(node, `Query: ${query.name}`);
    queryPaths.set(query.name, `$.dashboard.queries[${index}].name`);
    addQueryEdges(node, queryInputNames(query));
  });

  if (Array.isArray(dashboard.views)) {
    dashboard.views.forEach((view, index) => {
      if (!isRecord(view)) return;
      const node = `view:$.dashboard.views[${index}]`;
      addQueryEdges(node, viewQueryNames(view));
      labels.set(node, `Reusable view: ${typeof view.id === 'string' ? view.id : index}`);
      if (typeof view.id === 'string') reusableViewNodes.set(view.id, node);
    });
  }

  if (Array.isArray(dashboard.pages)) {
    dashboard.pages.forEach((page, pageIndex) => {
      if (!isRecord(page)) return;
      const navigationIndicatorNames = navigationIndicatorSourceNamesForPage(page);
      if (navigationIndicatorNames.length > 0) {
        const node = addNode(`navigation-indicator:$.dashboard.pages[${pageIndex}].navigation-indicator`);
        labels.set(node, `Navigation indicator: ${nodeLabel(page, pageIndex)}`);
        roots.add(node);
        addQueryEdges(node, navigationIndicatorNames);
      }
      const definition = page.kind === 'built-in' && isRecord(page.definition)
        ? page.definition
        : page;
      if (!Array.isArray(definition.views)) return;
      definition.views.forEach((view, viewIndex) => {
        const path = `$.dashboard.pages[${pageIndex}]${definition === page ? '' : '.definition'}.views[${viewIndex}]`;
        const node = addNode(`view:${path}`);
        labels.set(node, `View: ${nodeLabel(page, pageIndex)} / ${nodeLabel(view, viewIndex)}`);
        roots.add(node);
        if (typeof view === 'string') {
          const reusableNode = reusableViewNodes.get(view);
          if (reusableNode) graph.get(node)?.add(reusableNode);
        } else if (isRecord(view)) {
          addQueryEdges(node, viewQueryNames(view));
        }
      });
      if (Array.isArray(definition.sections)) {
        definition.sections.forEach((section, sectionIndex) => {
          if (!isRecord(section)) return;
          const path = `$.dashboard.pages[${pageIndex}]${definition === page ? '' : '.definition'}.sections[${sectionIndex}]`;
          const node = addNode(`section:${path}`);
          labels.set(node, `Section: ${nodeLabel(page, pageIndex)} / ${nodeLabel(section, sectionIndex)}`);
          roots.add(node);
          addQueryEdges(node, [
            section['count-source'],
            ...(Array.isArray(section['count-sources']) ? section['count-sources'] : [])
          ]);
        });
      }
    });
  }

  if (isRecord(dashboard['data-status'])) {
    const node = addNode('data-status:$.dashboard.data-status');
    labels.set(node, 'Dashboard data status');
    roots.add(node);
    addQueryEdges(node, [dashboard['data-status']['freshness-source'], dashboard['data-status']['health-source'], dashboard['data-status']['ingestion-source']]);
  }

  if (Array.isArray(dashboard.callouts)) {
    dashboard.callouts.forEach((callout, index) => {
      if (!isRecord(callout) || !isRecord(callout['visible-when'])) return;
      const node = addNode(`callout:$.dashboard.callouts[${index}]`);
      labels.set(node, `Callout: ${nodeLabel(callout, index)}`);
      roots.add(node);
      addQueryEdges(node, [callout['visible-when'].source]);
    });
  }

  debugQueryUsage({ event: 'graph-built', queryCount: queryNames.size, nodeCount: graph.size, rootCount: roots.size });
  return { graph, roots, queryPaths, labels };
}

/**
 * Renders the analyzed view-to-query graph as a Mermaid flowchart.
 *
 * @param {Record<string, unknown>} dashboard
 */
export function renderDashboardQueryUsageGraph(dashboard) {
  const analysis = buildDashboardQueryUsageGraph(dashboard);
  const reachable = reachableNodes(analysis.graph, analysis.roots);
  const nodes = [...analysis.graph.keys()].toSorted();
  const nodeIds = new Map(nodes.map((node, index) => [node, `n${index}`]));
  const lines = ['flowchart LR'];

  for (const node of nodes) {
    lines.push(`  ${nodeIds.get(node)}["${escapeMermaidLabel(analysis.labels.get(node) ?? node)}"]`);
  }
  for (const node of nodes) {
    for (const dependency of [...(analysis.graph.get(node) ?? [])].toSorted()) {
      lines.push(`  ${nodeIds.get(node)} --> ${nodeIds.get(dependency)}`);
    }
  }

  const deadQueryIds = nodes
    .filter((node) => node.startsWith('query:') && !reachable.has(node))
    .map((node) => nodeIds.get(node));
  if (deadQueryIds.length > 0) {
    lines.push('  classDef dead fill:#ffebe9,stroke:#cf222e,color:#82071e');
    lines.push(`  class ${deadQueryIds.join(',')} dead`);
  }
  debugQueryUsage({ event: 'graph-rendered', nodeCount: nodes.length, deadQueryCount: deadQueryIds.length });
  return lines.join('\n');
}

/**
 * @param {Record<string, unknown>} dashboard
 * @returns {Array<{ name: string, path: string }>}
 */
export function findDeadDashboardQueries(dashboard) {
  const { graph, roots, queryPaths } = buildDashboardQueryUsageGraph(dashboard);
  const reachable = reachableNodes(graph, roots);

  const dead = [...queryPaths].flatMap(([name, path]) => (
    reachable.has(`query:${name}`) ? [] : [{ name, path }]
  ));
  debugQueryUsage({ event: 'dead-queries-found', totalQueries: queryPaths.size, deadCount: dead.length });
  return dead;
}

/** @param {Map<string, Set<string>>} graph @param {Set<string>} roots */
function reachableNodes(graph, roots) {
  const reachable = new Set(roots);
  const pending = [...roots];
  while (pending.length > 0) {
    const node = pending.pop();
    if (node === undefined) continue;
    for (const dependency of graph.get(node) ?? []) {
      if (reachable.has(dependency)) continue;
      reachable.add(dependency);
      pending.push(dependency);
    }
  }
  return reachable;
}

/** @param {Record<string, any>} value @param {number} fallback */
function nodeLabel(value, fallback) {
  return typeof value.id === 'string' ? value.id : String(fallback);
}

/** @param {string} value */
function escapeMermaidLabel(value) {
  return value.replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}

/** @param {Record<string, unknown>} query */
function queryInputNames(query) {
  return [
    query.from,
    ...(Array.isArray(query.union) ? query.union : []),
    ...(Array.isArray(query.joins)
      ? query.joins.flatMap((join) => isRecord(join) ? [join.source] : [])
      : [])
  ];
}

/** @param {Record<string, unknown>} view */
function viewQueryNames(view) {
  const names = dashboardViewSourceNames(view);
  if (isRecord(view.data)) names.push(view.data['partial-source']);
  if (isRecord(view.list) && isRecord(view.list.drill)) names.push(view.list.drill.query);
  return names;
}

/** @param {unknown} value @returns {value is Record<string, any>} */
function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
