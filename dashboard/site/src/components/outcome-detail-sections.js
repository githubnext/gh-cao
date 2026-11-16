/**
 * Reusable outcome-detail section composition primitives.
 */

import { h } from '../dom.js';
import { renderModeBadge, renderStatusBadge } from './badge.js';
import { findLink, renderExternalLinkOrFallback } from './link-content.js';
import { formatUtcDateTime, isPlainObject, isSafeHttpsUrl, renderIconSpan } from './ui-primitives.js';
import { text, titleCase } from './count-formatters.js';
import { renderMetadataSection } from './view-chrome.js';
import { createDebug } from '../debug.js';

const debugOutcomeDetailSections = createDebug('outcome-detail-sections');

/**
 * @typedef {'discussion'|'metadata'} OutcomeDetailSectionBody
 */

/** @type {Readonly<Record<OutcomeDetailSectionBody, (outcome: Record<string, unknown>) => HTMLElement>>} */
const OUTCOME_DETAIL_SECTION_RENDERERS = {
  discussion: renderOutcomeDiscussionSection,
  metadata: renderOutcomeMetadataSection
};

/**
 * @param {Record<string, unknown>} outcome
 * @param {unknown} body
 * @returns {HTMLElement | null}
 */
export function renderOutcomeDetailSection(outcome, body) {
  if (body !== 'discussion' && body !== 'metadata') {
    debugOutcomeDetailSections({ event: 'unknown-section-body', requested: typeof body === 'string' ? body : typeof body });
    return null;
  }
  return OUTCOME_DETAIL_SECTION_RENDERERS[body](outcome);
}

/**
 * @param {Record<string, unknown>} outcome
 * @returns {HTMLElement}
 */
function renderOutcomeDiscussionSection(outcome) {
  const body = sanitizedMarkdownNodes(text(outcome['outcome-body-html']));
  return h(
    'article',
    { className: 'discussion-post' },
    h(
      'header',
      null,
      renderIconSpan('post-avatar', 'mark-github', { ariaHidden: true, tag: 'div' }),
      h(
        'div',
        null,
        h('strong', null, 'github-actions[bot]'),
        h(
          'p',
          null,
          'published ',
          formatUtcDateTime(outcome['published-at']),
          ' · updated ',
          formatUtcDateTime(outcome['observed-at'])
        )
      )
    ),
    h(
      'div',
      { className: 'markdown-body' },
      ...(body.length > 0 ? body : [h('p', null, text(outcome['outcome-summary']) || 'No report content was provided.')])
    )
  );
}

/**
 * @param {Record<string, unknown>} outcome
 * @returns {HTMLElement}
 */
function renderOutcomeMetadataSection(outcome) {
  const sourceLink = findLink(outcome, 'external-link')
    ?? findLink(outcome, 'issue-link')
    ?? findLink(outcome, 'pull-request-link');
  const runLink = findLink(outcome, 'run-link');
  const workflowLink = findLink(outcome, 'workflow-link');
  const workflowName = text(outcome['workflow-name']) || text(outcome.workflow) || 'Unknown workflow';
  const deliveryState = text(outcome['delivery-state']) || (outcome['published-at'] ? 'delivered' : 'unknown');
  const adoptionState = text(outcome['adoption-state'])
    || text(outcome['outcome-status']) || text(outcome['outcome-state']) || 'unknown';
  const lifecycleSources = [
    text(outcome['delivery-source']),
    text(outcome['adoption-source'])
  ].filter(Boolean).map((value) => titleCase(value.replaceAll('_', ' ')));
  debugOutcomeDetailSections({
    event: 'metadata-resolved',
    hasSourceLink: sourceLink != null,
    hasRunLink: runLink != null,
    hasWorkflowLink: workflowLink != null
  });

  return h(
    'aside',
    { className: 'outcome-meta', 'aria-label': 'Outcome metadata' },
    renderMetadataSection('Delivery', renderStatusBadge(titleCase(deliveryState))),
    renderMetadataSection('Adoption', renderStatusBadge(titleCase(adoptionState))),
    renderMetadataSection(
      'Adoption signal',
      h('p', null, titleCase(text(outcome['adoption-signal']).replaceAll('_', ' ')) || 'Unknown')
    ),
    renderMetadataSection('Delivered', h('p', null, formatUtcDateTime(outcome['delivered-at'] ?? outcome['published-at']))),
    renderMetadataSection(
      'Adopted',
      h('p', null, outcome['adoption-at'] ? formatUtcDateTime(outcome['adoption-at']) : 'Not observed')
    ),
    renderMetadataSection(
      'Survival',
      renderStatusBadge(titleCase(text(outcome['survival-state']) || 'unknown'))
    ),
    ...(outcome['reversal-signal']
      ? [renderMetadataSection(
          'Reversal',
          h('p', null, titleCase(text(outcome['reversal-signal']).replaceAll('_', ' ')))
        )]
      : []),
    renderMetadataSection('Rule', h('p', null, text(outcome['rule-version']) || 'Unknown')),
    ...(text(outcome['outcome-warning']) === 'Warning'
      ? [renderMetadataSection('Warning', renderStatusBadge('Warning'))]
      : []),
    renderMetadataSection('Mode', renderModeBadge(titleCase(text(outcome['rollout-mode'])))),
    renderMetadataSection('Category', h('p', null, titleCase(text(outcome['outcome-category'])))),
    renderMetadataSection(
      'Workflow',
      h(
        'p',
        null,
        renderExternalLinkOrFallback(workflowLink, workflowName, workflowName)
      )
    ),
    renderMetadataSection(
      'Provenance',
      h(
        'p',
        null,
        renderExternalLinkOrFallback(sourceLink, 'View source'),
        sourceLink && runLink ? h('br') : null,
        renderExternalLinkOrFallback(runLink, 'View workflow run'),
        !sourceLink && !runLink ? lifecycleSources.join(' · ') || 'Unavailable' : null
      )
    )
  );
}

const ALLOWED_MARKDOWN_TAGS = new Set([
  'A', 'BLOCKQUOTE', 'BR', 'CODE', 'DEL', 'DETAILS', 'DIV', 'EM',
  'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'HR', 'IMG', 'INPUT', 'LI',
  'OL', 'P', 'PRE', 'S', 'SPAN', 'STRONG', 'SUMMARY', 'TABLE', 'TBODY',
  'TD', 'TH', 'THEAD', 'TR', 'UL'
]);
const DROPPED_MARKDOWN_TAGS = new Set([
  'BUTTON', 'EMBED', 'FORM', 'IFRAME', 'MATH', 'OBJECT', 'OPTION',
  'SCRIPT', 'SELECT', 'STYLE', 'SVG', 'TEXTAREA'
]);
const ALLOWED_MARKDOWN_CLASSES = new Set([
  'contains-task-list',
  'markdown-alert',
  'markdown-alert-caution',
  'markdown-alert-important',
  'markdown-alert-note',
  'markdown-alert-tip',
  'markdown-alert-title',
  'markdown-alert-warning',
  'task-list-item'
]);

/**
 * @param {string} html
 * @returns {Node[]}
 */
function sanitizedMarkdownNodes(html) {
  if (!html) return [];
  const parsed = new DOMParser().parseFromString(html, 'text/html');
  const stats = { dropped: 0 };
  const nodes = [...parsed.body.childNodes]
    .map((node) => cloneSafeMarkdownNode(node, stats))
    .filter((node) => node !== null);
  debugOutcomeDetailSections({
    event: 'discussion-sanitized',
    outputNodes: nodes.length,
    droppedNodes: stats.dropped
  });
  return nodes;
}

/**
 * @param {Node} node
 * @param {{ dropped: number }} stats
 * @returns {Node | null}
 */
function cloneSafeMarkdownNode(node, stats) {
  if (node.nodeType === Node.TEXT_NODE) return document.createTextNode(node.textContent ?? '');
  if (!(node instanceof HTMLElement) || DROPPED_MARKDOWN_TAGS.has(node.tagName)) {
    stats.dropped += 1;
    return null;
  }

  if (!ALLOWED_MARKDOWN_TAGS.has(node.tagName)) {
    const fragment = document.createDocumentFragment();
    for (const child of node.childNodes) {
      const safeChild = cloneSafeMarkdownNode(child, stats);
      if (safeChild) fragment.append(safeChild);
    }
    return fragment;
  }

  const clone = document.createElement(node.tagName.toLowerCase());
  copySafeAttributes(node, clone);
  for (const child of node.childNodes) {
    const safeChild = cloneSafeMarkdownNode(child, stats);
    if (safeChild) clone.append(safeChild);
  }
  return clone;
}

/**
 * @param {HTMLElement} source
 * @param {HTMLElement} target
 */
function copySafeAttributes(source, target) {
  const safeClasses = [...source.classList].filter((name) => ALLOWED_MARKDOWN_CLASSES.has(name));
  if (safeClasses.length > 0) target.className = safeClasses.join(' ');

  if (source.tagName === 'A') {
    const href = source.getAttribute('href');
    if (href && isSafeHttpsUrl(href)) {
      target.setAttribute('href', href);
      target.setAttribute('target', '_blank');
      target.setAttribute('rel', 'noopener noreferrer');
    }
  } else if (source.tagName === 'IMG') {
    const src = source.getAttribute('src');
    if (src && isSafeHttpsUrl(src)) target.setAttribute('src', src);
    target.setAttribute('alt', source.getAttribute('alt') ?? '');
    target.setAttribute('loading', 'lazy');
  } else if (source.tagName === 'INPUT' && source.getAttribute('type') === 'checkbox') {
    target.setAttribute('type', 'checkbox');
    target.setAttribute('disabled', '');
    if (source.hasAttribute('checked')) target.setAttribute('checked', '');
  } else if (source.tagName === 'DETAILS' && source.hasAttribute('open')) {
    target.setAttribute('open', '');
  } else if (source.tagName === 'TH') {
    const scope = source.getAttribute('scope');
    if (['row', 'col', 'rowgroup', 'colgroup'].includes(scope ?? '')) target.setAttribute('scope', scope ?? '');
  }

  if (source.tagName === 'TD' || source.tagName === 'TH') {
    for (const attribute of ['colspan', 'rowspan']) {
      const value = source.getAttribute(attribute);
      if (value && /^[1-9]\d{0,2}$/.test(value)) target.setAttribute(attribute, value);
    }
  }
}

/**
 * @param {unknown} value
 * @returns {value is { body: OutcomeDetailSectionBody }}
 */
export function isOutcomeDetailSectionConfig(value) {
  return isPlainObject(value)
    && typeof value.body === 'string'
    && (value.body === 'discussion' || value.body === 'metadata');
}
