import { createDebug } from './debug.js';
import { REPOSITORY_PATTERN } from './dialog-selector.js';

const TEMPLATE_TOKEN_PATTERN = /\{\{([a-z][a-z0-9]*(?:-[a-z0-9]+)*)\}\}/g;
const REPOSITORY_SLUG_PATTERN = /^[A-Za-z0-9_.][A-Za-z0-9_.-]*\/[A-Za-z0-9_.][A-Za-z0-9_.-]*$/;
const SAFE_TOKEN_PATTERN = /^[A-Za-z0-9_.][A-Za-z0-9_.:/@-]*$/;
const CONTEXT_FIELDS = new Set(['cao-url', 'cao-repository', 'cao-repository-url']);
const SAFE_URL_PATTERN = /^https:\/\/[A-Za-z0-9._~:/%+-]+$/;

const debugCliActionTemplate = createDebug('cli-action-template');

/** @param {string} command @param {{ includeContext?: boolean }} [options] */
export function cliActionTemplateFields(command, options = {}) {
  return [...new Set([...command.matchAll(TEMPLATE_TOKEN_PATTERN)].map((match) => match[1]))]
    .filter((field) => options.includeContext || !CONTEXT_FIELDS.has(field));
}

/** @param {string} value */
export function validateCliActionUrl(value) {
  if (value.length > 2048 || !SAFE_URL_PATTERN.test(value)) {
    throw new Error('CAO URL must be an HTTPS URL without credentials, query, fragment, or command delimiters.');
  }
  const url = new URL(value);
  if (url.username || url.password || url.search || url.hash) {
    throw new Error('CAO URL must be an HTTPS URL without credentials, query, fragment, or command delimiters.');
  }
  return url.href.replace(/\/?$/, '/');
}

/**
 * @param {{ repository?: string, githubUrlBase?: string, caoUrl?: string }} options
 * @returns {Record<string, string>}
 */
export function cliActionTemplateContext({ repository, githubUrlBase = 'https://github.com', caoUrl }) {
  /** @type {Record<string, string>} */
  const context = {};
  if (repository !== undefined) {
    if (!REPOSITORY_SLUG_PATTERN.test(repository)) throw new Error('CAO repository must be an OWNER/REPOSITORY name.');
    context['cao-repository'] = repository;
    context['cao-repository-url'] = `${validateCliActionUrl(githubUrlBase)}${repository}`;
  }
  if (caoUrl !== undefined) context['cao-url'] = validateCliActionUrl(caoUrl);
  return context;
}

/**
 * @param {string} command
 * @param {Record<string, unknown>} values
 * @param {Record<string, unknown>} context Host-owned CAO identity, separate from row values.
 */
export function renderCliActionCommand(command, values = {}, context = {}) {
  let fieldCount = 0;
  const rendered = command.replace(TEMPLATE_TOKEN_PATTERN, (_match, field) => {
    fieldCount += 1;
    const contextual = CONTEXT_FIELDS.has(field);
    const value = contextual ? context[field] : values[field];
    const urlField = field === 'cao-url' || field === 'cao-repository-url';
    if (typeof value !== 'string' || value.length === 0 || value.length > (urlField ? 2048 : 200)) {
      debugCliActionTemplate({ event: 'render-rejected', field, reason: 'missing-or-invalid' });
      throw new Error(`CLI action template value "${field}" is missing or invalid.`);
    }
    const pattern = field === 'repository-pattern' ? REPOSITORY_PATTERN : urlField ? SAFE_URL_PATTERN
      : field === 'repository' || field === 'cao-repository' ? REPOSITORY_SLUG_PATTERN : SAFE_TOKEN_PATTERN;
    if (!pattern.test(value)) {
      debugCliActionTemplate({ event: 'render-rejected', field, reason: 'unsafe-token' });
      throw new Error(`CLI action template value "${field}" is not a safe command token.`);
    }
    if (urlField) validateCliActionUrl(value);
    return field === 'repository-pattern' && value.includes('*') ? `'${value}'` : value;
  });
  debugCliActionTemplate({ event: 'render-completed', fieldCount });
  return rendered;
}
