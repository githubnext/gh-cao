/**
 * Dashboard Language Specification constants used by the validator.
 */

import octiconNames from './octicon-names.json' with { type: 'json' };
import { DASHBOARD_QUERY_LIMITS } from './data/queries/declarative.js';
import { GITHUB_QUERY_SOURCE_FIELDS } from './data/github-query-sources.js';
export {
  OUTCOME_DETAIL_SECTION_BODY_VALUES,
  CAMPAIGN_ROUTE_BODY_VALUES,
  WORKFLOW_ROUTE_BODY_VALUES,
  WORKFLOW_ROUTE_PAGE_BODY_VALUES
} from './components/route-body-specification.js';

export const LANGUAGE_VERSION = '0.1.0';

export const IDENTIFIER_PATTERN = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;

export const ROOT_KEYS = ['language-version', 'dashboard'];
export const DASHBOARD_KEYS = ['id', 'title', 'description', 'defaults', 'units', 'queries', 'card-templates', 'views', 'pages', 'github-url-base', 'repository', 'navigation', 'horizon', 'callouts', 'cli-actions', 'data-status'];
export const CARD_TEMPLATE_KEYS = ['id', 'icon', 'icon-field', 'status', 'title', 'subtitle', 'labels', 'details', 'detail-labels', 'timing', 'actions', 'drill'];
export const CARD_DETAIL_LABEL_VALUES = ['hidden', 'visible'];
export const CARD_TEMPLATE_ACTION_KEYS = ['action', 'context', 'when'];
export const CARD_STATUS_KEYS = ['field', 'fallback-field', 'title'];
export const DASHBOARD_HORIZON_KEYS = ['label', 'tooltip'];
export const CLI_ACTION_KEYS = ['id', 'level', 'verb', 'label', 'description', 'icon', 'command', 'preview-command', 'placement', 'arguments', 'copy-only', 'selector'];
export const CLI_ACTION_PLACEMENT_VALUES = ['toolbar', 'settings', 'view', 'row'];
export const CLI_ACTION_ARGUMENT_KEYS = ['id', 'label', 'description', 'type', 'flag', 'default'];
export const CLI_ACTION_ARGUMENT_TYPE_VALUES = ['boolean'];
export const MAX_CLI_ACTIONS = 20;
export const MAX_CLI_ACTION_ARGUMENTS = 10;
export const MAX_CLI_ACTION_COMMAND_LENGTH = 1000;

export const QUERY_KEYS = ['name', 'subject', 'objective', 'acceptance', 'description', 'parameters', 'from', 'union', 'time', 'joins', 'filter', 'compute', 'temporal-series', 'aggregate', 'predict', 'window', 'select', 'order-by', 'limit', 'facet'];
export const FACET_FIELD_KEYS = ['field', 'type', 'title', 'format'];
export const QUERY_PARAMETER_KEYS = ['name', 'type'];
export const QUERY_PARAMETER_TYPE_VALUES = ['number', 'string', 'boolean'];
export const QUERY_JOIN_KEYS = ['source', 'type', 'on', 'fields'];
export const QUERY_JOIN_TYPE_VALUES = ['inner', 'left', 'inner-many'];
export const QUERY_JOIN_ON_KEYS = ['left', 'right'];
export const QUERY_JOIN_FIELD_KEYS = ['field', 'as'];
export const QUERY_FILTER_KEYS = ['predicates'];
export const QUERY_PREDICATE_KEYS = ['field', 'equals', 'in', 'includes', 'gte', 'lt', 'optional'];
export const QUERY_COMPUTE_KEYS = ['as', 'function', 'args'];
export const QUERY_COMPUTE_ARGUMENT_KEYS = ['field', 'value', 'context', 'parameter'];
export const QUERY_TEMPORAL_SERIES_KEYS = ['time', 'series', 'shape', 'link', 'carry', 'point-carry', 'measures', 'maps', 'trend'];
export const QUERY_TEMPORAL_SERIES_MEASURE_KEYS = ['field', 'key', 'kind'];
export const QUERY_TEMPORAL_SERIES_MAP_KEYS = ['field', 'definitions', 'group', 'kind'];
export const QUERY_TEMPORAL_SERIES_TREND_KEYS = ['direction'];
export const QUERY_AGGREGATE_KEYS = ['by', 'values'];
export const QUERY_AGGREGATE_VALUE_KEYS = ['field', 'as', 'reducer', 'filter'];
export const QUERY_AGGREGATE_FILTER_PREDICATE_KEYS = ['field', 'equals', 'in'];
export const QUERY_PREDICT_KEYS = ['field', 'on', 'method', 'order', 'groupby', 'as'];
export const QUERY_WINDOW_KEYS = ['field', 'as', 'operation', 'order-by', 'groupby', 'frame', 'reducer', 'alignment', 'mode', 'time-field', 'unit'];
export const QUERY_REDUCER_VALUES = ['count', 'distinct-count', 'distinct-list', 'distinct-values', 'calendar-week-rhythm', 'latest-failure-streak', 'sum', 'mean', 'min', 'max', 'unique', 'median', 'p75', 'p90', 'p95'];
export const QUERY_SELECT_KEYS = ['field', 'as'];
export const QUERY_NUMERIC_REDUCER_VALUES = ['sum', 'mean', 'min', 'max', 'median', 'p75', 'p90', 'p95'];
export const INFERRED_FIELD_NAMES = ['campaign-link'];
export { COMPUTE_FUNCTION_ARITY, NUMERIC_COMPUTE_FUNCTIONS, PREDICTION_METHODS, TEXT_COMPUTE_FUNCTIONS } from './data-operations.js';
export { DASHBOARD_QUERY_LIMITS };
export const QUERY_MAX_JOINS = DASHBOARD_QUERY_LIMITS['max-joins'];
export const SITE_CALLOUT_KEYS = ['id', 'title', 'description', 'icon', 'navigation-page', 'visible-when'];
export const SITE_CALLOUT_VISIBILITY_KEYS = ['source', 'field', 'equals', 'older-than-hours'];
export const TOOLTIP_KEYS = ['label', 'description', 'icon'];
export const DEFAULTS_KEYS = ['scope', 'time', 'filters'];
export const UNIT_DEFINITION_KEYS = ['name', 'symbol', 'significant', 'format'];
export const UNIT_FORMAT_VALUES = ['aicc', 'duration', 'number', 'usd'];
export const NAVIGATION_SECTION_KEYS = ['label', 'pages', 'experimental', 'placement'];
export const NAVIGATION_INDICATOR_KEYS = ['label', 'any'];
export const BUILT_IN_PAGE_KEYS = ['id', 'kind', 'page', 'title', 'navigation-label', 'navigation-indicator', 'description', 'intent', 'subject', 'objective', 'acceptance', 'icon', 'class-name', 'experimental', 'filter-bar', 'view-mode-control', 'pull-refresh', 'mode-indicator', 'query-binding', 'requires', 'form', 'definition'];
export const CUSTOM_PAGE_KEYS = ['id', 'kind', 'title', 'navigation-label', 'navigation-indicator', 'description', 'intent', 'subject', 'objective', 'acceptance', 'icon', 'class-name', 'experimental', 'filter-bar', 'view-mode-control', 'pull-refresh', 'mode-indicator', 'retain-on-navigation', 'query-binding', 'requires', 'form', 'route', 'views', 'sections'];
export const PAGE_FORM_KEYS = ['title', 'description', 'update', 'fields'];
export const PAGE_FORM_UPDATE_KEYS = ['strategy', 'delay-ms'];
export const PAGE_FORM_UPDATE_STRATEGY_VALUES = ['debounce', 'throttle'];
export const PAGE_FORM_FIELD_KEYS = ['id', 'label', 'description', 'control', 'default', 'min', 'max', 'step', 'options'];
export const PAGE_FORM_CONTROL_VALUES = ['slider', 'checkbox', 'radio', 'text'];
export const PAGE_FORM_OPTION_KEYS = ['value', 'label'];
export const PAGE_FORM_MIN_DELAY_MS = 50;
export const PAGE_FORM_MAX_DELAY_MS = 2000;
export const PAGE_ROUTE_KEYS = ['hash-query-parameter', 'navigation-page', 'availability-view', 'availability-message', 'partial-message', 'title-format', 'tabs-class-name', 'tab', 'tabs', 'breadcrumbs'];
export const PAGE_ROUTE_TITLE_FORMAT_VALUES = ['title-case'];
export const PAGE_ROUTE_TAB_KEYS = ['id', 'label', 'icon', 'page'];
export const MAX_PAGE_ROUTE_TABS = 8;

export const VIEW_KEYS = ['id', 'title', 'show-title', 'description', 'subject', 'objective', 'acceptance', 'prompt', 'prompt-level', 'locked', 'requires', 'data', 'mark', 'element', 'config', 'callout', 'chart', 'facet', 'columns', 'treemap', 'scatter', 'layer', 'resolve', 'metric', 'list', 'card-drill', 'tree', 'layout', 'disclosure', 'disclosure-label', 'controls', 'filter-bar', 'lazy-list', 'column-summaries', 'empty-message', 'title-link', 'encoding'];
export const SCATTER_KEYS = ['cluster'];
export const VIEW_FILTER_BAR_KEYS = ['filters'];
export const VIEW_FILTER_CONTROL_KEYS = ['id', 'label', 'groups'];
export const VIEW_FILTER_GROUP_KEYS = ['label', 'field', 'source', 'value-field', 'label-field'];
export const VIEW_REQUIREMENT_KEYS = ['backend', 'message', 'on-unavailable'];
export const VIEW_BACKEND_VALUES = ['static', 'hosted'];
export const VIEW_DATA_KEYS = ['source', 'partial-source', 'sources', 'scope', 'time', 'filters', 'arguments', 'route-field', 'limit', 'order-by', 'source-metadata', 'query-context'];
export const VIEW_DATA_ARGUMENT_KEYS = ['name', 'field'];
export const VIEW_ELEMENT_CONFIG_KEYS = ['body', 'sections', 'stations', 'labels', 'animate', 'sources', 'browser-first-load', 'data-status', 'view-all-page', 'view-all-label', 'label-field', 'label-badge-field', 'link-field', 'icon-field', 'fallback-icon', 'indicator-field', 'indicator-label-field', 'empty-message', 'measure-source', 'content-field', 'path-field', 'base-link-field', 'actions', 'wizard'];
export const FACTORY_HEADER_SOURCE_ROLES = ['presentation', 'rhythm'];
export const FACTORY_FLOOR_SOURCE_ROLES = ['campaigns', 'repositories', 'issues', 'value-gains'];
export const VIEW_ELEMENT_ANIMATION_VALUES = ['number'];
export const FACTORY_FLOOR_STATION_VALUES = ['campaigns', 'repositories', 'issues', 'value-gains'];
export const PLURAL_TEXT_KEYS = ['singular', 'plural'];
export const PLURAL_LABEL_ELEMENTS = ['factory-floor'];
export const VIEW_TITLE_LINK_KEYS = ['href-field', 'identifier-field'];
export const CALLOUT_KEYS = ['label', 'icon'];
export const VIEW_MARK_VALUES = ['metric', 'table', 'list', 'chart', 'element', 'callout'];
export const VIEW_ELEMENT_VALUES = [
  'campaign-route',
  'workflow-route-page',
  'outcome-detail',
  'outcome-detail-section',
  'problem-detail',
  'entity-route',
  'configuration-policy',
  'measure-history',
  'workflow-value-report',
  'workflow-value-snapshot',
  'factory-header',
  'factory-floor',
  'all-campaign-memory',
  'link-button-list',
  'markdown',
  'query-editor',
  'command-terminal',
  'storage-info',
  'environment-defaults',
  'prompt-wizard'
];
export const VIEW_CHART_VALUES = ['area', 'bar', 'dot', 'heatmap', 'histogram', 'horizontal-bar', 'line', 'pie', 'scatter', 'swimlane', 'treemap'];
export const TREEMAP_KEYS = ['method', 'ratio', 'padding'];
export const TREEMAP_METHOD_VALUES = ['squarify', 'binary', 'slicedice'];
export const MAX_TREEMAP_LEAVES = 100;
export const VIEW_METRIC_KEYS = ['style', 'icon', 'tone', 'navigation-page', 'animate'];
export const VIEW_METRIC_STYLE_VALUES = ['card'];
export const VIEW_METRIC_TONE_VALUES = ['attention', 'danger', 'neutral', 'review'];
export const VIEW_METRIC_ANIMATION_VALUES = ['number'];
export const VIEW_LIST_KEYS = ['style', 'layout', 'appearance', 'icon', 'action', 'action-context', 'actions', 'card', 'drill', 'view-all'];
export const VIEW_LIST_STYLE_VALUES = ['cards', 'issues', 'entity-cards'];
export const VIEW_LIST_LAYOUT_VALUES = ['rows', 'grid'];
export const VIEW_LIST_APPEARANCE_VALUES = ['grouped', 'marketplace'];
export const VIEW_LIST_VIEW_ALL_KEYS = ['page', 'label'];
export const VIEW_LIST_DRILL_KEYS = ['type', 'field', 'page', 'query', 'title-field', 'arguments'];
export const VIEW_LIST_DRILL_TYPE_VALUES = ['external', 'query'];
export const VIEW_LIST_DRILL_ARGUMENT_KEYS = ['name', 'field'];
export const VIEW_LAYOUT_VALUES = ['full', 'full-view', 'half', 'third', 'horizontal'];
export const VIEW_DISCLOSURE_VALUES = ['essential', 'supplemental'];
export const VIEW_CONTROL_VALUES = ['interactive', 'filter-only', 'static'];
export const MAX_ESSENTIAL_VIEWS_PER_PAGE = 4;
export const GRAPHICAL_LAYOUT_EXEMPT_PAGE_IDS = new Set([
  'overview',
  'overview-failed-runs',
  'overview-blocked-work',
  'overview-awaiting-review',
  'overview-security-findings',
  'agent',
  'agents',
  'work',
  'work-tasks',
  'work-roadmap',
  'evidence',
  'insights',
  'query-editor',
  'commands',
  'governance',
  'command'
]);
export const VIEW_ENCODING_KEYS = ['value', 'columns', 'card', 'x', 'y', 'color', 'section', 'weight', 'reference', 'href', 'actions', 'facet', 'row', 'column'];
export const TABLE_ACTION_KEYS = ['intent', 'action', 'presentation', 'level', 'verb', 'icon', 'label', 'context', 'when'];
export const TABLE_ACTION_PRESENTATION_VALUES = ['copy-prompt', 'cli-action', 'external-link'];
export const TABLE_ACTION_WHEN_KEYS = ['field', 'equals'];
export const TREE_TABLE_KEYS = ['id-field', 'parent-field'];
export const FIELD_DEFINITION_KEYS = ['field', 'type', 'aggregate', 'time-unit', 'title', 'as', 'display', 'filter', 'format', 'unit'];
export const CARD_TIMING_FIELD_KEYS = [...FIELD_DEFINITION_KEYS, 'icon'];
export const FIELD_TYPE_VALUES = ['nominal', 'ordinal', 'quantitative', 'temporal'];
export const FIELD_DISPLAY_VALUES = ['text', 'status', 'grader-status', 'mode', 'active-state', 'label', 'ref', 'digest', 'outcome-link', 'run-link', 'repository-link', 'workflow-link', 'evidence-link'];
export const FIELD_FORMAT_VALUES = ['human-friendly-timestamp', 'workflow-relative-path', 'workflow-identity-label', 'workflow-run-url', 'shortened-url'];
export const AGGREGATE_VALUES = ['count', 'distinct-count', 'sum', 'mean', 'min', 'max', 'none'];
export const TIME_UNIT_VALUES = ['hour', 'day', 'week', 'month'];
export const LINK_RELATION_VALUES = [
  'organization',
  'repository',
  'workflow',
  'run',
  'issue',
  'pull-request',
  'evidence',
  'external'
];
export const LINK_OBJECT_KEYS = ['relation', 'href', 'label'];
export const RELATION_LINK_FIELD_RELATIONS = {
  'organization-link': 'organization',
  'repository-link': 'repository',
  'workflow-link': 'workflow',
  'issue-link': 'issue',
  'pull-request-link': 'pull-request',
  'run-link': 'run',
  'evidence-link': 'evidence',
  'external-link': 'external'
};
export const LINK_FIELD_NAMES = Object.keys(RELATION_LINK_FIELD_RELATIONS);
export const DASHBOARD_LINK_FIELD_NAMES = ['campaign-dashboard-link', 'repository-dashboard-link', 'workflow-dashboard-link', 'command-dashboard-link', 'routing-dashboard-link'];
export const DATASET_METADATA_KEYS = [
  'source-id',
  'source-kind',
  'as-of',
  'retrieved-at',
  'coverage-start',
  'coverage-end',
  'completeness',
  'freshness',
  'provenance-link',
  'availability'
];
export const DATASET_COMPLETENESS_VALUES = ['complete', 'partial', 'unknown'];
export const DATASET_FRESHNESS_VALUES = ['fresh', 'stale', 'unknown'];
export const DATASET_AVAILABILITY_VALUES = ['available', 'empty', 'unavailable'];
export const SCOPE_KEYS = ['organizations', 'repositories', 'workflows'];
export const TIME_KEYS = ['range', 'start', 'end'];

export const ORDER_BY_KEYS = ['field', 'direction'];
export const ORDER_DIRECTION_VALUES = ['asc', 'desc'];

export const FILTER_DIMENSION_VALUES = [
  'organization',
  'repository',
  'workflow',
  'campaign',
  'experiment',
  'variant',
  'workflow-role',
  'workflow-active',
  'admission-status',
  'check-status',
  'resource',
  'credential',
  'operation',
  'phase',
  'risk-status',
  'is-unhealthy',
  'is-current',
  'has-history',
  'attribution-status',
  'run-status',
  'run-conclusion',
  'lifecycle-state',
  'job',
  'job-status',
  'outcome-category',
  'outcome-state',
  'rollout-mode',
  'engine',
  'requested-model',
  'resolved-model',
  'status',
  'eval-result',
  'operational-grader-definition',
  'operational-value-definition',
  'finding-status',
  'finding-severity',
  'security-feature',
  'security-analysis',
  'security-status',
  'detection-state',
  'decision',
  'drift-state',
  'review-state',
  'protocol',
  'policy-rule-id',
  'firewall-enabled',
  'evidence-state'
];

export const PAGE_KIND_VALUES = ['built-in', 'custom'];
export const PAGE_ICON_VALUES = octiconNames;

export const BUILT_IN_PAGE_VALUES = [
  'overview',
  'organizations',
  'repositories',
  'campaigns',
  'workflows',
  'runs',
  'audits',
  'experiments',
  'graders',
  'evals',
  'usage',
  'engines-models',
  'operational-value',
  'findings',
  'issues'
];

export const BUILT_IN_PAGE_DEFINITION_KEYS = ['views', 'sections', 'data-state'];

export const BUILT_IN_PAGE_DATA_STATE_KEYS = ['availability'];
export const PAGE_SECTION_KEYS = ['id', 'title', 'description', 'layout', 'views', 'count-source', 'count-sources', 'count-field', 'count-label'];
export const PAGE_SECTION_LAYOUT_VALUES = ['full', 'wide', 'narrow', 'horizontal'];

export const BUILT_IN_PAGE_REQUIRED_SOURCES = {
  overview: ['repositories', 'workflows', 'runs', 'usage', 'findings'],
  organizations: ['organizations', 'repositories', 'workflows', 'runs', 'usage'],
  repositories: ['repository-activity'],
  campaigns: ['campaign-inventory'],
  workflows: ['workflows'],
  runs: ['runs'],
  audits: ['audits'],
  experiments: ['experiments'],
  graders: ['graders', 'grader-observations'],
  evals: ['evals', 'eval-observations'],
  usage: ['usage'],
  'engines-models': ['model-usage-summary', 'engine-usage-summary', 'run-aggregate-summary'],
  'operational-value': ['operational-values'],
  findings: ['findings'],
  issues: ['issues']
};

export const BUILT_IN_PAGE_REQUIRED_FIELDS = {
  overview: {
    repositories: ['repository'],
    workflows: ['workflow-active', 'rollout-mode'],
    runs: ['run-status', 'run-conclusion', 'repository', 'workflow'],
    usage: ['aic'],
    findings: ['observed-at', 'issue-link', 'pull-request-link', 'run-link']
  },
  organizations: {
    organizations: ['organization'],
    repositories: ['repository'],
    workflows: ['workflow'],
    runs: ['run'],
    usage: ['aic']
  },
  repositories: {
    'repository-activity': ['repository', 'runs', 'failure-summary', 'aic', 'workflows', 'status']
  },
  campaigns: {
    'campaign-inventory': ['campaign-name', 'workflows', 'roles', 'modes', 'registration', 'runs', 'aic']
  },
  workflows: {
    workflows: ['workflow-active', 'rollout-mode']
  },
  runs: {
    runs: ['run', 'run-status', 'run-conclusion', 'repository-coordinate', 'workflow', 'rollout-mode', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'started-at']
  },
  audits: {
    audits: ['code', 'event-type', 'event-summary', 'event-status', 'run', 'observed-at']
  },
  experiments: {
    experiments: ['experiment']
  },
  graders: {
    graders: ['grader'],
    'grader-observations': ['grader', 'run', 'value', 'status', 'observed-at']
  },
  evals: {
    evals: ['eval'],
    'eval-observations': ['eval', 'run', 'eval-result', 'requested-model', 'resolved-model', 'observed-at']
  },
  usage: {
    usage: ['input-tokens', 'output-tokens', 'cache-read-tokens', 'cache-write-tokens', 'reasoning-tokens', 'aic', 'estimated-usd', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'organization', 'repository', 'workflow', 'rollout-mode', 'observed-at']
  },
  'engines-models': {
    'model-usage-summary': ['model', 'engine', 'requested-model', 'runs', 'invocations', 'total-aic', 'estimated-usd', 'pricing'],
    'engine-usage-summary': ['engine', 'runs', 'invocations', 'total-aic', 'estimated-usd', 'min-engine-version', 'max-engine-version', 'models'],
    'run-aggregate-summary': ['engine', 'engine-version', 'requested-model', 'resolved-model', 'run-conclusion', 'runs', 'run-link']
  },
  'operational-value': {
    'operational-values': ['observed-at', 'operational-value', 'operational-value-definition', 'repository', 'campaign']
  },
  findings: {
    findings: ['finding-summary', 'finding-severity', 'finding-status', 'organization', 'repository', 'workflow', 'observed-at', 'issue-link', 'pull-request-link', 'run-link']
  },
  issues: {
    issues: ['github-entity-type', 'safe-output-type', 'event-summary', 'entity-url', 'repository', 'workflow', 'run', 'run-link', 'observed-at']
  }
};

export const TABLE_VALUES = [
  'routing-aggregates',
  'organizations',
  'campaigns',
  'repositories',
  'workflows',
  'runs',
  'overview-runs',
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues',
  'transactions',
  'admissions',
  'admission-checks',
  'run-performance',
  'job-performance',
  'safe-output-performance',
  'experiments',
  'experiment-assignments',
  'graders',
  'grader-observations',
  'evals',
  'eval-observations',
  'daily-run-counts',
  'usage',
  'mcp-calls',
  'mcp-servers',
  'security-observations',
  'detection-observations',
  'firewall-observations',
  'firewall-policy-rules',
  'coverage-diagnostics',
  'repository-coverage',
  'runtime-episode-summary',
  'runtime-attribution-gaps',
  'workflow-topology-summary',
  'standalone-workflows',
  'outcomes',
  'findings',
  'operational-values',
  'operational-value-evidence',
  'workflow-value-measurements',
  'workflow-value-collections',
  'github-api-rate-limits',
  'github-api-collector-health',
  'github-api-call-stacks',
  'configuration-summary',
  'configuration-policy',
  'configuration-actions',
  'overview-status',
  'overview-vitals',
  'overview-execution-health',
  'overview-attention',
  'overview-managed-campaigns',
  'overview-campaign-utilization',
  'readiness-activity',
  'readiness-checks',
  'readiness-summary',
  'readiness-signals',
  'security-summary',
  'security-signals',
  'value-summary',
  'value-signals',
  'value-workflows',
  'cost-summary',
  'cost-signals',
  'runtime-anomaly-readiness',
  'runtime-signals',
  'factory-rhythm-baseline',
  'dispatch-activation-summary',
  'campaign-dispatch-state',
  'repository-summary',
  'repository-detail-summary',
  'repository-workflow-status',
  'repository-workflow-usage',
  'repository-workflows',
  'model-usage-summary',
  'engine-usage-summary',
  'data-health-schema',
  'source-metadata',
  'work-items',
  'attention-signals',
  'agent-assignments',
  'agent-smells',
  'workflow-smells',
  'security-findings',
  'control-plane-smells',
  'evidence-records',
  'marketplace-packages'
];

export const STATISTICAL_FIELDS = [
  'statistical-frame-id', 'statistical-stratum', 'statistical-priority', 'statistical-population',
  'statistical-allocation', 'statistical-weight-valid', 'statistical-probability', 'statistical-weight'
];

export const TABLE_FIELDS = {
  'statistical-runs': [...STATISTICAL_FIELDS],
  'statistical-tools': [...STATISTICAL_FIELDS],
  'statistical-domains': [...STATISTICAL_FIELDS],
  'statistical-skills': [...STATISTICAL_FIELDS],
  'statistical-audits': [...STATISTICAL_FIELDS],
  'daily-run-counts': ['day', 'organization', 'repository', 'workflow', 'total', 'successful', 'failed', 'approval', 'pending', 'other'],
  'routing-aggregates': [
    'routing-aggregate-version', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'run-link', 'engine',
    'routing-run-key', 'observed-at', 'pick', 'unknown-pick-model', 'routing-dashboard-link',
    'paired', 'paired-main-aic', 'paired-baseline-aic', 'routing-state', 'pick-model', 'pick-effort',
    'routing-goal', 'routing-configured-mode', 'routing-mode', 'harness-status', 'routing-awf-version',
    'run-clock', 'selection-count', 'failure-count', 'routing-degraded', 'configuration-conflict',
    'warnings', 'agent-result', 'flagged-requests', 'deviated-requests', 'complete-aic', 'main-aic',
    'baseline-model', 'baseline-aic', 'baseline-complete', 'routing-failure-codes', 'routing-deviation-codes',
    'routing-resolution-codes', 'routing-upstream-codes', 'candidate-coverage',
    'routing-task-type', 'routing-scope', 'routing-complexity'
  ],
  'marketplace-packages': ['id', 'registry-id', 'registry-name', 'registry-precedence', 'package-name', 'package-description', 'publisher', 'repository', 'repository-link', 'path', 'package-ref', 'resolved-commit', 'package-version', 'package-icon', 'package-artwork', 'package-readme', 'package-readme-path', 'package-source', 'add-command', 'stars', 'forks'],
  organizations: ['organization', 'organization-name', 'observed-at', 'organization-link'],
  campaigns: ['id', 'campaign', 'campaign-name', 'campaign-description', 'campaign-icon', 'campaign-mode', 'campaign-enabled', 'campaign-registration', 'campaign-max-repositories', 'campaign-rollout-percent', 'campaign-monthly-ai-credit-budget', 'campaign-aic-allowance', 'campaign-worker-count', 'campaign-inventory-warnings', 'campaign-min-version', 'campaign-source', 'campaign-version', 'campaign-current-version', 'campaign-update-state', 'campaign-experimental', 'campaign-readme-path', 'campaign-readme', 'observed-at', 'campaign-link'],
  repositories: ['id', 'organization', 'repository', 'repository-name', 'repository-coordinate', 'rollout-mode', 'observed-at', 'organization-link', 'repository-link'],
  workflows: ['id', 'organization', 'repository', 'campaign', 'campaign-name', 'campaign-icon', 'campaign-readme-path', 'workflow', 'workflow-id', 'workflow-name', 'workflow-role', 'workflow-source-kind', 'workflow-source', 'workflow-source-version', 'workflow-source-current-version', 'workflow-source-update-state', 'workflow-active', 'workflow-registry-state', 'admission-status', 'admission-reason', 'gh-aw-version', 'gh-aw-current-version', 'gh-aw-version-label', 'gh-aw-update-state', 'rollout-mode', 'max-ai-credits', 'campaign-aic-allowance', 'campaign-worker-count', 'campaign-inventory-warnings', 'inventory-ready', 'created-at', 'updated-at', 'observed-at', 'organization-link', 'repository-link', 'workflow-link', 'external-link'],
  runs: ['id', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'run-title', 'target-repository', 'event', 'branch', 'head-sha', 'created-at', 'started-at', 'ended-at', 'updated-at', 'run-status', 'run-conclusion', 'classification', 'failure-kind', 'terminal-outcome', 'terminal-outcome-detail', 'duration', 'action-minutes', 'github-api-calls', 'safe-items-count', 'error-count', 'admission-status', 'admission-reason', 'failure-job', 'failure-message', 'failure-step', 'failure-log', 'failure-detail', 'resource', 'resource-reset-at', 'resource-wait-hours', 'rollout-mode', 'agent-id', 'agent-version', 'model-id', 'gh-aw-version', 'aic-total', 'input-tokens', 'output-tokens', 'cache-read-tokens', 'cache-write-tokens', 'reasoning-tokens', 'engine', 'engine-id', 'engine-version', 'requested-model', 'resolved-model', 'agent-runtime', 'firewall-version', 'gateway-version', 'organization-link', 'repository-link', 'workflow-link', 'run-link'],
  'overview-runs': ['id', 'organization', 'repository', 'workflow', 'workflow-name', 'workflow-role', 'campaign', 'campaign-name', 'run', 'target-repository', 'event', 'started-at', 'run-status', 'run-conclusion', 'rollout-mode'],
  domains: ['id', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'event', 'event-timestamp', 'event-source', 'event-type', 'event-summary', 'event-status', 'request-count', 'correlation-id', 'payload-ref', 'domain', 'decision', 'source-sequence', 'observed-at', 'run-link', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'model-id', 'rollout-mode', 'gh-aw-version', 'run-conclusion', 'agent-id', 'agent-version', 'agent-runtime', 'organization-link', 'repository-link', 'workflow-link'],
  tools: ['id', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'event', 'event-timestamp', 'event-source', 'event-type', 'event-summary', 'event-status', 'correlation-id', 'payload-ref', 'mcp-server', 'mcp-server-version', 'mcp-protocol-version', 'mcp-tool', 'request-bytes', 'response-bytes', 'tool-type', 'name', 'invocation-count', 'failed-count', 'latency-ms', 'source-sequence', 'observed-at', 'run-link', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'model-id', 'rollout-mode', 'gh-aw-version', 'run-conclusion', 'agent-id', 'agent-version', 'agent-runtime', 'organization-link', 'repository-link', 'workflow-link'],
  skills: ['id', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'event', 'event-timestamp', 'event-source', 'event-type', 'event-summary', 'event-status', 'correlation-id', 'payload-ref', 'tool-type', 'name', 'invocation-count', 'failed-count', 'activation-source', 'source-sequence', 'observed-at', 'run-link', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'model-id', 'rollout-mode', 'gh-aw-version', 'run-conclusion', 'agent-id', 'agent-version', 'agent-runtime', 'organization-link', 'repository-link', 'workflow-link'],
  friction: ['id', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'event', 'event-timestamp', 'event-source', 'event-type', 'event-summary', 'correlation-id', 'payload-ref', 'measurement-state', 'canonical-unit', 'total-events', 'total-occurrences', 'counted-occurrences', 'suppressed-occurrences', 'linked-invocations', 'unattributed-occurrences', 'aic', 'input-tokens', 'output-tokens', 'cache-read-tokens', 'cache-write-tokens', 'reasoning-tokens', 'total-tokens', 'turns', 'tool-calls', 'latency-ms', 'total-run-aic', 'friction-ratio', 'derived', 'events-truncated', 'decision', 'source-sequence', 'observed-at', 'run-link', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'model-id', 'rollout-mode', 'gh-aw-version', 'run-conclusion', 'agent-id', 'agent-version', 'agent-runtime', 'organization-link', 'repository-link', 'workflow-link', 'evidence-state', 'evidence-confidence', 'cost-grain', 'missing-reason'],
  audits: ['id', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'event', 'event-timestamp', 'event-source', 'event-type', 'event-summary', 'event-status', 'code', 'correlation-id', 'payload-ref', 'name', 'activation-source', 'measurement-state', 'total-events', 'linked-invocations', 'aic', 'input-tokens', 'output-tokens', 'cache-read-tokens', 'cache-write-tokens', 'reasoning-tokens', 'total-tokens', 'turns', 'tool-calls', 'events-truncated', 'decision', 'source-sequence', 'observed-at', 'run-link', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'model-id', 'rollout-mode', 'gh-aw-version', 'run-conclusion', 'agent-id', 'agent-version', 'agent-runtime', 'organization-link', 'repository-link', 'workflow-link', 'target-repo', 'target-organization', 'target-repository', 'target-workflow-path', 'optimizer-run-attempt', 'optimizer-workflow-path', 'optimizer-workflow-name', 'claim-run-id', 'claim-run-attempt', 'actor', 'opportunity-id', 'opportunity-kind', 'assignment-run', 'experiment', 'evidence-window-start', 'evidence-window-end', 'evidence-state', 'evidence-confidence', 'cost-grain', 'intervention-id', 'lifecycle-observation-id', 'previous-intervention-state', 'intervention-state', 'previous-recommendation-disposition', 'recommendation-disposition', 'supersedes-intervention-id', 'superseded-by-intervention-id', 'recommendation-churn-count', 'recommendation-churn-rate', 'control-variant', 'optimized-variant', 'proposed-savings-aic', 'missing-reason', 'implementation-change-id', 'implementation-pull-request-url', 'accepted-at', 'implementation-started-at', 'implementation-completed-at', 'rejected-at', 'superseded-at'],
  issues: ['id', 'organization', 'repository', 'workflow', 'run', 'run-attempt', 'event', 'event-timestamp', 'event-source', 'event-type', 'event-summary', 'event-status', 'correlation-id', 'payload-ref', 'safe-output-type', 'github-entity-type', 'is-pull-request', 'issue-state', 'issue-closed', 'issue-state-reason', 'issue-closed-at', 'issue-status-observed-at', 'number', 'name', 'source-sequence', 'observed-at', 'run-link', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'model-id', 'rollout-mode', 'gh-aw-version', 'run-conclusion', 'agent-id', 'agent-version', 'agent-runtime', 'organization-link', 'repository-link', 'workflow-link', 'safe-output-id', 'safe-output-url'],
  transactions: ['activity-status', 'id', 'kind', 'createdAt', 'payloadScope', 'payloadHash', 'payloadEtag', 'records', 'committedRecords', 'rawPayloadRecords', 'rawRuns', 'agenticRunRecords', 'agenticRuns', 'duplicateRawRunObservations', 'duplicateAgenticRunObservations', 'unenrichedRuns', 'error'],
  admissions: ['organization', 'repository', 'workflow', 'run', 'observed-at', 'campaign', 'workflow-role', 'worker', 'target-repository', 'admission-status', 'admission-reason', 'failed-check', 'github-api-status', 'github-api-remaining', 'github-api-required', 'github-api-reset-at', 'runner-disk-status', 'runner-disk-available-mb', 'runner-disk-required-mb', 'run-link'],
  'admission-checks': ['organization', 'repository', 'workflow', 'run', 'observed-at', 'campaign', 'workflow-role', 'worker', 'target-repository', 'admission-status', 'admission-reason', 'failed-check', 'check', 'check-order', 'check-status', 'github-api-status', 'github-api-remaining', 'github-api-required', 'github-api-reset-at', 'runner-disk-status', 'runner-disk-available-mb', 'runner-disk-required-mb', 'run-link'],
  'run-performance': ['organization', 'repository', 'workflow', 'run', 'started-at', 'run-conclusion', 'rollout-mode', 'run-duration-seconds', 'sandbox-runtime', 'engine', 'model', 'run-link'],
  'job-performance': ['organization', 'repository', 'workflow', 'run', 'run-attempt', 'started-at', 'run-conclusion', 'rollout-mode', 'job-id', 'job', 'job-status', 'job-conclusion', 'job-duration-seconds', 'runner', 'runner-name', 'runner-group', 'sandbox-runtime', 'engine', 'model', 'run-link'],
  experiments: ['organization', 'repository', 'campaign', 'workflow', 'experiment', 'experiment-name', 'control-variant', 'candidate-variant', 'primary-metric', 'primary-source', 'state', 'readiness', 'decision', 'normalized-effect', 'evidence-strength', 'last-observation', 'observed-at'],
  'experiment-assignments': ['organization', 'repository', 'campaign', 'workflow', 'run', 'experiment', 'variant', 'assignment-at', 'included', 'exclusion-reason', 'observed-at', 'assignment-link', 'artifact-link', 'trace-link'],
  graders: ['grader', 'grader-name', 'role', 'direction', 'unit', 'threshold', 'observed-at'],
  'grader-observations': ['organization', 'repository', 'workflow', 'run', 'experiment', 'variant', 'grader', 'grader-id', 'value', 'status', 'included', 'exclusion-reason', 'role', 'direction', 'unit', 'threshold', 'rollout-mode', 'maturity-status', 'baseline-value', 'delta-from-baseline', 'evaluator-digest', 'observed-at', 'run-link', 'evidence-link', 'grader-link'],
  evals: ['eval', 'eval-name', 'eval-question', 'requested-model', 'role', 'direction', 'observed-at'],
  'eval-observations': ['organization', 'repository', 'workflow', 'run', 'experiment', 'variant', 'eval', 'eval-result', 'status', 'included', 'exclusion-reason', 'role', 'direction', 'requested-model', 'resolved-model', 'rollout-mode', 'observed-at', 'evidence-link', 'eval-link'],
  usage: ['organization', 'repository', 'workflow', 'run', 'invocation', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'rollout-mode', 'input-tokens', 'output-tokens', 'cache-read-tokens', 'cache-write-tokens', 'reasoning-tokens', 'aic', 'estimated-usd', 'observed-at', 'organization-link', 'repository-link', 'workflow-link', 'run-link'],
  'mcp-calls': ['organization', 'repository', 'workflow', 'run', 'mcp-observation', 'mcp-server', 'mcp-server-version', 'mcp-protocol-version', 'mcp-tool', 'mcp-status', 'request-bytes', 'response-bytes', 'rollout-mode', 'engine-version', 'gh-aw-version', 'observed-at', 'run-link'],
  'mcp-servers': ['organization', 'repository', 'workflow', 'run', 'mcp-server-observation', 'mcp-server', 'mcp-server-version', 'mcp-protocol-version', 'mcp-status', 'tool-calls', 'failed-calls', 'total-response-bytes', 'max-response-bytes', 'rollout-mode', 'engine-version', 'gh-aw-version', 'observed-at', 'run-link'],
  'security-observations': ['organization', 'repository', 'workflow', 'run', 'security-observation', 'security-feature', 'security-analysis', 'security-signal', 'security-status', 'security-subject', 'security-count', 'observed-at', 'run-link'],
  'detection-observations': ['organization', 'repository', 'workflow', 'run', 'observed-at', 'run-link', 'rollout-mode', 'detection-expected', 'detection-applicable', 'detection-executed', 'verdict-available', 'usable-verdict-percent', 'detection-state', 'detection-state-label', 'detection-count', 'prompt-injection-detected', 'secret-leak-detected', 'malicious-patch-detected', 'inspection-warning-count', 'inspection-warning', 'detection-signal', 'attention-priority', 'job-status', 'job-conclusion', 'job-duration-seconds', 'runner', 'engine', 'requested-model', 'resolved-model'],
  'firewall-observations': ['organization', 'repository', 'workflow', 'run', 'firewall-observation', 'run-conclusion', 'rollout-mode', 'agent-id', 'agent-version', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'agent-runtime', 'observed-at', 'domain', 'decision', 'request-count', 'run-link', 'workflow-link'],
  'firewall-policy-rules': ['organization', 'repository', 'workflow', 'run', 'agent-id', 'agent-version', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'agent-runtime', 'observed-at', 'rule-id', 'rule-order', 'action', 'protocol', 'domain-pattern', 'description', 'hit-count', 'ssl-bump-enabled', 'dlp-enabled', 'host-access-enabled', 'policy-source', 'policy-manifest-identity', 'run-link', 'evidence-link'],
  'coverage-diagnostics': ['kind', 'title', 'effect', 'technical-detail', 'endpoint', 'rate-limit-reset', 'snapshot-age-seconds'],
  'repository-coverage': ['label', 'value'],
  'data-health-schema': ['source', 'schema'],
  'source-metadata': ['source', 'row-count', 'source-id', 'source-kind', 'as-of', 'retrieved-at', 'availability', 'completeness', 'freshness', 'collection-operation', 'collection-state', 'collection-progress', 'collection-reason', 'failure-class', 'collector-completed-at', 'coverage-start', 'coverage-end', 'requested-coverage-start', 'requested-coverage-end', 'coverage-expected', 'coverage-observed', 'snapshot-age-seconds', 'fallback-used'],
  'runtime-episode-summary': ['label', 'value'],
  'runtime-attribution-gaps': ['run', 'run-title', 'workflow', 'status', 'control-transition', 'reason-code', 'evidence', 'run-link'],
  outcomes: ['organization', 'repository', 'campaign', 'runtime-repository', 'workflow', 'workflow-name', 'run', 'run-conclusion', 'safe-output', 'safe-output-kind', 'outcome-number', 'outcome-title', 'outcome-summary', 'outcome-category', 'outcome-status', 'outcome-state', 'issue-state', 'issue-closed', 'issue-state-reason', 'issue-status', 'issue-closing-status', 'issue-status-detail', 'issue-closed-at', 'issue-status-observed-at', 'rollout-mode', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'published-at', 'observed-at', 'issue-link', 'pull-request-link', 'run-link', 'external-link', 'organization-link', 'repository-link', 'workflow-link'],
  'safe-output-performance': ['organization', 'repository', 'workflow', 'run', 'run-conclusion', 'rollout-mode', 'safe-output-kind', 'safe-output-label', 'safe-output-status', 'safe-output-count', 'observed-at', 'run-link'],
  findings: ['organization', 'repository', 'workflow', 'run', 'safe-output', 'code', 'finding-kind', 'finding-severity', 'finding-status', 'finding-summary', 'observed-at', 'engine', 'engine-version', 'requested-model', 'resolved-model', 'issue-link', 'pull-request-link', 'run-link', 'external-link', 'organization-link', 'repository-link', 'workflow-link'],
  'operational-values': ['id', 'organization', 'repository', 'repository-name', 'workflow', 'campaign', 'campaign-name', 'campaign-icon', 'evaluator-digest', 'cadence-days', 'window-start', 'window-end', 'measurement-kind', 'coverage-status', 'missing-reason', 'eligible-count', 'population-digest', 'evidence-digest', 'operational-value', 'operational-value-definition', 'operational-value-role', 'operational-value-name', 'operational-value-unit', 'operational-value-direction', 'rollup-numerator', 'rollup-denominator', 'maturity-status', 'adoption-at', 'evaluation-mode', 'workflow-slug', 'workflow-name', 'observed-at', 'organization-link', 'repository-link', 'campaign-link', 'run-link'],
  'workflow-value-collections': [
    'id', 'organization', 'repository', 'workflow', 'workflow-path', 'workflow-route', 'registration', 'collector',
    'collection-state', 'reason-code', 'reason', 'checked-at', 'policy-digest', 'last-attempt-at', 'last-attempt-outcome'
  ],
  'operational-value-evidence': [
    'report-id', 'workflow-route', 'snapshot-route', 'organization', 'repository', 'workflow', 'workflow-path', 'observed-at', 'window-start', 'window-end', 'contract-digest',
    'fact-kind', 'fact-id', 'parent-id', 'related-id', 'label', 'title', 'url', 'at', 'accepted-at', 'end-at',
    'value', 'ref', 'state', 'origin', 'scope', 'coverage-status', 'status', 'detail', 'goal', 'acceptance',
    'metric-id', 'metric-name', 'metric-unit', 'metric-direction', 'metric-version', 'metric-formula', 'result-label',
    'measurement-basis', 'snapshot-name', 'snapshot-unit', 'snapshot-direction', 'snapshot-id', 'snapshot-version',
    'snapshot-formula', 'outputs-available', 'repository-state-available', 'checks-available', 'items-available',
    'output-unit', 'implementation-unit', 'output-scope', 'output-coverage', 'workflow-name', 'cadence-days', 'maturation-days'
  ],
  'workflow-value-measurements': [
    'id', 'repository', 'workflow-route', 'campaign', 'workflow-slug', 'workflow-name', 'value-id', 'value',
    'metric-role', 'metric-name', 'metric-unit', 'metric-direction', 'evaluator-digest', 'observed-at',
    'window-start', 'window-end', 'measurement-kind', 'maturity-status', 'coverage-status', 'missing-reason',
    'evidence-digest', 'eligible-count', 'population-digest', 'cadence-days', 'evaluation-mode', 'adoption-at'
  ],
  'github-api-rate-limits': ['observation-id', 'operation-execution-id', 'observed-at', 'phase', 'operation', 'outcome', 'credential', 'credential-type', 'resource', 'bucket', 'maximum-lane', 'history-series', 'has-history', 'limit', 'used', 'remaining', 'remaining-percent', 'reset-at', 'minutes-to-reset', 'consumed-since-previous', 'burn-rate-per-minute', 'projected-remaining-at-reset', 'projected-exhaustion-at', 'runway-ratio', 'risk-status', 'risk-order', 'is-unhealthy', 'is-current', 'attribution-status', 'operation-consumed', 'run-link'],
  'github-api-collector-health': ['observed-at', 'operation-execution-id', 'phase', 'operation', 'outcome', 'credential', 'cache-hydrated', 'cache-bytes', 'cache-entries', 'cache-folders', 'rate-limit-error'],
  'github-api-call-stacks': ['observed-at', 'operation-execution-id', 'phase', 'operation', 'outcome', 'credential', 'stack-frame-id', 'stack-parent-id', 'stack-depth', 'stack-frame'],
  'configuration-summary': ['status', 'count'],
  'configuration-policy': ['path', 'document', 'raw', 'diagnostics'],
  'configuration-actions': ['action', 'path', 'current', 'recommended', 'prompt'],
  'readiness-activity': ['activity-hour', 'workflow-role', 'run-count'],
  'readiness-checks': ['check', 'readiness-state', 'detail'],
  'readiness-summary': ['label', 'value'],
  'readiness-signals': ['priority', 'urgency', 'count', 'tone', 'icon', 'kind', 'title', 'detail', 'evidence', 'action', 'navigation-page', 'run-link', 'external-link'],
  'security-summary': ['label', 'value'],
  'security-signals': ['priority', 'count', 'tone', 'icon', 'kind', 'title', 'detail', 'evidence', 'action', 'navigation-page', 'navigation-href', 'run-link', 'external-link'],
  'value-summary': ['label', 'value'],
  'value-signals': ['priority', 'count', 'tone', 'icon', 'kind', 'title', 'detail', 'evidence', 'action', 'navigation-page', 'run-link', 'external-link'],
  'value-workflows': ['organization', 'repository', 'workflow', 'run', 'operational-grader-definition', 'opportunities', 'mature-observations', 'mean-operational-grader', 'mean-baseline', 'observed-at', 'evidence-link', 'run-link', 'organization-link', 'repository-link', 'workflow-link'],
  'cost-summary': ['label', 'value'],
  'cost-signals': ['priority', 'count', 'tone', 'icon', 'kind', 'title', 'detail', 'evidence', 'action', 'navigation-page'],
  'runtime-anomaly-readiness': ['icon', 'title', 'detail'],
  'runtime-signals': ['priority', 'count', 'tone', 'icon', 'kind', 'title', 'detail', 'evidence', 'action', 'navigation-href'],
  'factory-rhythm-baseline': ['daily-averages', 'weeks'],
  'dispatch-activation-summary': ['label', 'value'],
  'campaign-dispatch-state': ['campaign', 'campaign-name', 'dispatch-runs', 'skipped', 'failed', 'succeeded', 'worker-dispatches', 'aic', 'agent', 'model'],
  'repository-summary': ['label', 'value', 'items'],
  'repository-activity': ['organization', 'repository', 'workflows', 'reports', 'evaluated-workflows', 'runs', 'failure-summary', 'aic', 'status', 'repository-link'],
  'repository-detail-summary': ['repository', 'workflows', 'latest-update', 'external-link'],
  'repository-workflow-status': ['repository', 'status', 'workflows'],
  'repository-workflow-usage': ['repository', 'workflow', 'invocation', 'aic', 'workflow-link'],
  'repository-workflows': ['repository', 'workflow', 'workflow-name', 'workflow-role', 'campaign-name', 'rollout-mode', 'workflow-active', 'observed-at', 'aic', 'workflow-link'],
  'model-usage-summary': ['model', 'resolved-model', 'engine', 'requested-model', 'runs', 'invocations', 'total-aic', 'estimated-usd', 'pricing'],
  'engine-usage-summary': ['engine', 'runs', 'invocations', 'total-aic', 'estimated-usd', 'min-engine-version', 'max-engine-version', 'models'],
  'run-aggregate-summary': ['engine', 'engine-version', 'requested-model', 'resolved-model', 'run-conclusion', 'runs', 'run-link'],
  'workflow-topology-summary': ['label', 'value'],
  'campaign-workflows': ['campaign', 'campaign-name', 'repository', 'workflow', 'workflow-name', 'workflow-role', 'rollout-mode', 'workflow-active', 'runs', 'aic', 'campaign-link', 'repository-link', 'workflow-link', 'external-link'],
  'standalone-workflows': ['repository', 'workflow', 'workflow-name', 'rollout-mode', 'workflow-active', 'runs', 'aic', 'repository-link', 'workflow-link'],
  'work-items': ['work-item-id', 'name', 'objective', 'organization', 'repository', 'workflow', 'workflow-name', 'workflow-icon', 'workflow-role', 'scope', 'domain', 'campaign', 'campaign-name', 'campaign-icon', 'work-type', 'lifecycle-state', 'phase', 'reason', 'reason-evidence-class', 'next-action', 'next-actor', 'safe-output-kind', 'waiting-on', 'waiting-since', 'owner', 'consequence-tier', 'verification-state', 'outcome-state', 'started-at', 'ended-at', 'observed-at', 'evidence-link', 'repository-link', 'run-link'],
  'attention-signals': ['attention-signal-id', 'signal-type', 'work-item-id', 'objective', 'scope', 'reason', 'action', 'expected-actor', 'age-seconds', 'consequence-tier', 'priority', 'observed-at', 'evidence-link', 'repository-link', 'run-link'],
  'agent-assignments': ['assignment-id', 'agent-id', 'agent-name', 'agent-icon', 'agent-description', 'permissions', 'agent-state', 'work-item-id', 'objective', 'assignment-state', 'handoff-state', 'dependency-state', 'conflict-state', 'run-count', 'total-runtime-seconds', 'last-observed-at', 'long-running', 'stale', 'observed-at', 'evidence-link', 'repository-link', 'run-link'],
  'agent-smells': ['smell-observation-id', 'smell-id', 'smell-name', 'smell-category', 'smell-severity', 'smell-summary', 'smell-evidence', 'smell-recommendation', 'organization', 'repository', 'workflow', 'run', 'observed-at', 'evidence-link', 'repository-link', 'workflow-link', 'run-link'],
  'workflow-smells': ['smell-observation-id', 'smell-id', 'smell-name', 'smell-category', 'smell-severity', 'smell-summary', 'smell-evidence', 'smell-recommendation', 'organization', 'repository', 'workflow', 'run', 'observed-at', 'evidence-link', 'repository-link', 'workflow-link', 'run-link'],
  'security-findings': ['smell-observation-id', 'smell-id', 'smell-name', 'smell-category', 'smell-severity', 'smell-summary', 'smell-evidence', 'smell-recommendation', 'organization', 'repository', 'workflow', 'run', 'observed-at', 'evidence-link', 'repository-link', 'workflow-link', 'run-link'],
  'control-plane-smells': ['smell-observation-id', 'smell-id', 'smell-name', 'smell-category', 'smell-severity', 'smell-summary', 'smell-evidence', 'smell-recommendation', 'organization', 'repository', 'workflow', 'run', 'observed-at', 'evidence-link', 'repository-link', 'workflow-link', 'run-link'],
  'evidence-records': ['evidence-id', 'evidence-class', 'evidence-kind', 'work-item-id', 'objective', 'claim', 'verification-state', 'provenance-state', 'source-revision', 'observed-at', 'evidence-link', 'repository-link', 'run-link']
};

TABLE_FIELDS.audits.push(
  'routing-stage', 'routing-schema', 'routing-status', 'routing-goal', 'routing-configured-mode',
  'routing-mode', 'routing-selected-model', 'routing-selected-effort', 'routing-requested-model',
  'routing-requested-effort', 'routing-applied-effort', 'routing-task-type', 'routing-scope',
  'routing-complexity', 'routing-purpose', 'routing-agent-name', 'routing-agent-id', 'routing-routed',
  'routing-outcome', 'routing-router-version', 'routing-awf-version', 'routing-classifier-model',
  'routing-classifier-effort', 'routing-classifier-attempts', 'routing-latency-ms', 'routing-degraded',
  'routing-degraded-reason', 'routing-enabled', 'routing-substitution', 'routing-effective-model',
  'routing-steering-type', 'routing-steering-threshold', 'routing-max-credits', 'routing-session-clock',
  'routing-deviations', 'routing-inventory-complete',
  'resolution-role', 'resolution-configured-model', 'resolution-resolved-model',
  'component', 'phase', 'telemetry-model', 'provider', 'effort', 'telemetry-engine',
  'duration-ms', 'exit-code', 'http-status', 'threshold', 'required'
);

for (const [name, source] of /** @type {const} */ ([
  ['statistical-runs', 'runs'], ['statistical-tools', 'tools'], ['statistical-domains', 'domains'],
  ['statistical-skills', 'skills'], ['statistical-audits', 'audits']
])) {
  TABLE_VALUES.push(name);
  TABLE_FIELDS[name] = [...TABLE_FIELDS[source], ...STATISTICAL_FIELDS];
}

TABLE_FIELDS['statistical-runs'].push('sample-stratum', 'sample-probability', 'sample-weight');

export const ROLLOUT_MODE_VALUES = ['review', 'live', 'unknown'];
export const DETECTION_STATE_VALUES = ['clean', 'threat', 'degraded', 'tooling-failure', 'skipped', 'unknown'];
export const WORKFLOW_ACTIVE_VALUES = ['true', 'false', 'unknown'];
export const WORKFLOW_ROLE_VALUES = ['orchestrator', 'worker', 'standalone'];
export const RUN_STATUS_VALUES = ['queued', 'in-progress', 'completed', 'unknown'];
export const RUN_CONCLUSION_VALUES = [
  'success',
  'failure',
  'cancelled',
  'timed-out',
  'action-required',
  'neutral',
  'skipped',
  'stale',
  'startup-failure',
  'unknown'
];
export const GRADER_STATUS_VALUES = ['pass', 'fail', 'error', 'unavailable'];
export const DISPATCH_STATUS_VALUES = [...RUN_CONCLUSION_VALUES, ...RUN_STATUS_VALUES];
export const EVAL_RESULT_VALUES = ['YES', 'NO', 'UNKNOWN'];
export const OUTCOME_STATE_VALUES = ['accepted', 'rejected', 'ignored', 'pending', 'lifecycle', 'lifecycle-close'];
export const FINDING_STATUS_VALUES = ['open', 'resolved', 'dismissed', 'unknown'];
export const FINDING_SEVERITY_VALUES = ['critical', 'high', 'medium', 'low', 'informational', 'unknown'];

export const SOURCE_ENTITY_IDENTIFIER_FIELDS = {
  organizations: ['organization'],
  repositories: ['repository'],
  workflows: ['workflow'],
  runs: ['run'],
  admissions: ['run'],
  'admission-checks': ['run', 'check'],
  'run-performance': ['run'],
  'job-performance': ['run', 'job'],
  'safe-output-performance': ['run', 'safe-output-kind'],
  experiments: ['experiment'],
  'experiment-assignments': ['run', 'experiment', 'variant'],
  graders: ['grader'],
  'grader-observations': ['grader', 'run'],
  evals: ['eval'],
  'eval-observations': ['eval', 'run'],
  usage: ['invocation'],
  'mcp-calls': ['mcp-observation'],
  'mcp-servers': ['mcp-server-observation'],
  'security-observations': ['security-observation'],
  'detection-observations': ['run'],
  'firewall-observations': ['firewall-observation'],
  'firewall-policy-rules': ['run', 'rule-id', 'domain-pattern'],
  'repository-coverage': ['label'],
  'runtime-episode-summary': ['label'],
  'runtime-episodes': ['run'],
  'runtime-attribution-gaps': ['run'],
  outcomes: ['safe-output'],
  findings: ['code'],
  'operational-values': ['operational-value-definition', 'observed-at'],
  'github-api-rate-limits': ['observation-id'],
  'github-api-collector-health': ['operation-execution-id', 'observed-at'],
  'repository-summary': ['label'],
  'repository-activity': ['repository'],
  'repository-detail-summary': ['repository'],
  'repository-workflow-status': ['repository', 'status'],
  'repository-workflow-usage': ['invocation'],
  'repository-workflows': ['repository', 'workflow'],
  'workflow-runs': ['workflow-route', 'run'],
  'workflow-reports': ['workflow-route', 'safe-output'],
  'campaign-reports': ['campaign', 'safe-output'],
  'work-items': ['work-item-id'],
  'attention-signals': ['attention-signal-id'],
  'agent-assignments': ['assignment-id'],
  'evidence-records': ['evidence-id']
};

export const SERVER_SOURCE_VALUES = ['collection-health', 'github-quota-usage', 'webhook-status'];

export const SERVER_SOURCE_FIELDS = {
  'webhook-status': [
    'configured', 'scope', 'received', 'in-flight', 'verified', 'accepted',
    'rejected', 'failed', 'duplicates', 'pings', 'last-received-at',
    'last-accepted-at', 'last-failure-at', 'last-ping-at'
  ],
  'collection-health': [
    'configured',
    'health',
    'health-revision',
    'queue-depth',
    'pending-tasks',
    'oldest-pending-age',
    'dead-letters',
    'backfill',
    'backfill-failures',
    'backfill-queued-run-tasks',
    'webhook-load',
    'collection-load',
    'failure-load',
    'last-projected',
    'last-webhook-at',
    'last-failure-at',
    'last-failure-code',
    'last-success-at',
    'webhook-received',
    'webhook-duplicate',
    'webhook-admission-failed',
    'task-queued',
    'task-coalesced',
    'collection-succeeded',
    'collection-failed',
    'collection-retried',
    'collection-dead-lettered'
  ],
  'github-quota-usage': [
    'observed-at',
    'scope',
    'bucket',
    'app',
    'installation',
    'resource',
    'buckets',
    'limit',
    'used',
    'reserved',
    'usage-percent'
  ]
};

export const SYNTHETIC_SOURCE_FIELDS = {
  'simulation-days': ['day', 'date'],
  'cli-commands': ['id', 'actionId', 'status', 'createdAt', 'startedAt', 'completedAt', 'error', 'exitCode', 'command', 'arguments', 'values', 'repository', 'campaign', 'package-source'],
  'cli-command-statuses': ['status'],
  'cli-command-summary': ['running', 'total', 'paused-reason', 'revision'],
  ...GITHUB_QUERY_SOURCE_FIELDS
};

export const QUERY_SOURCE_VALUES = [...TABLE_VALUES, ...SERVER_SOURCE_VALUES, ...Object.keys(SYNTHETIC_SOURCE_FIELDS)];
export const QUERY_SOURCE_FIELDS = { ...TABLE_FIELDS, ...SERVER_SOURCE_FIELDS, ...SYNTHETIC_SOURCE_FIELDS };

export const TEMPORAL_FIELD_NAMES = [
  'date',
  'observed-at',
  'started-at',
  'activity-hour',
  'ended-at',
  'requested-evidence-at',
  'evidence-cutoff',
  'window-start',
  'window-end',
  'maturity-at',
  'published-at',
  'reset-at',
  'projected-exhaustion-at',
  'evidence-horizon-start',
  'evidence-horizon-end',
  'requested-horizon-start',
  'requested-horizon-end',
  'last-successful-collection-at',
  'first-seen-at',
  'last-seen-at',
  'waiting-since'
];

export const ADDITIVE_MEASURE_FIELDS = [
  'input-tokens',
  'output-tokens',
  'cache-read-tokens',
  'cache-write-tokens',
  'reasoning-tokens',
  'aic',
  'security-count',
  'response-bytes',
  'tool-calls',
  'failed-calls',
  'total-response-bytes',
  'safe-output-count',
  'request-count',
  'hit-count'
];

export const NON_ADDITIVE_MEASURE_FIELDS = [
  'cadence-days',
  'eligible-count',
  'rollup-numerator',
  'rollup-denominator',
  'value',
  'operational-grader',
  'operational-value',
  'limit',
  'used',
  'remaining',
  'remaining-percent',
  'minutes-to-reset',
  'consumed-since-previous',
  'burn-rate-per-minute',
  'projected-remaining-at-reset',
  'runway-ratio',
  'operation-consumed',
  'max-response-bytes',
  'port',
  'policy-rule-order',
  'baseline-request-count',
  'request-volume-change',
  'evidence-coverage-percent',
  'review-priority'
];

export const ERROR_CODES = {
  invalidYamlSyntax: 'DLS-E001',
  invalidDocumentShape: 'DLS-E002',
  missingOrInvalidRequiredField: 'DLS-E003',
  unknownOrDuplicateKey: 'DLS-E004',
  nonCanonicalVocabularyOrIdentifier: 'DLS-E005',
  incompatibleMarkChannelTypeOrTimeUnit: 'DLS-E007',
  invalidLinkReference: 'DLS-E009',
  invalidScopeFilterTimeAggregationOrOrderReference: 'DLS-E010',
  invalidEntityRelationshipOrSourceGrain: 'DLS-E011',
  missingRequiredProvenanceOrDataStateMetadata: 'DLS-E012',
  invalidProgressiveDisclosureConfiguration: 'DLS-E013',
  invalidGraphicalNesting: 'DLS-E014',
  unusedQuery: 'DLS-E015'
};
