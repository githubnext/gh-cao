import { createDebug } from './debug.js';

/** @typedef {{ label: string, placeholder?: string, 'debounce-ms': number, query: { command: 'gh search repos', fields: string[], limit: number }, mapping: { value: string, label: string, description?: string, target: string } }} DialogSelector */

const repositoryFields = ['fullName', 'description', 'name', 'owner', 'url', 'visibility', 'isArchived', 'isPrivate'];
const debugDialogSelector = createDebug('src:dialog-selector');
const identifier = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;

/**
 * Validate the closed, read-only lookup vocabulary shared by the presenter and
 * command service. Search text is always a separate argv value, never a command.
 * @param {unknown} input
 * @param {string[]} templateFields
 * @returns {asserts input is DialogSelector}
 */
export function assertDialogSelector(input, templateFields) {
  const selector = /** @type {Partial<DialogSelector> | null} */ (input);
  const object = (/** @type {unknown} */ value) => Boolean(value && typeof value === 'object' && !Array.isArray(value));
  const keys = (/** @type {object} */ value, /** @type {string[]} */ allowed) =>
    Object.keys(value).every((key) => allowed.includes(key));
  const invalid = !object(selector) || !selector
    || !keys(selector, ['label', 'placeholder', 'debounce-ms', 'query', 'mapping'])
    || typeof selector.label !== 'string' || !selector.label.trim()
    || (selector.placeholder !== undefined && (typeof selector.placeholder !== 'string' || !selector.placeholder.trim()))
    || typeof selector['debounce-ms'] !== 'number' || !Number.isInteger(selector['debounce-ms']) || selector['debounce-ms'] < 100 || selector['debounce-ms'] > 2000
    || !object(selector.query) || !selector.query || !keys(selector.query, ['command', 'fields', 'limit'])
    || selector.query.command !== 'gh search repos'
    || !Array.isArray(selector.query.fields) || selector.query.fields.length === 0
    || new Set(selector.query.fields).size !== selector.query.fields.length
    || selector.query.fields.some((field) => !repositoryFields.includes(field))
    || !Number.isInteger(selector.query.limit) || selector.query.limit < 1 || selector.query.limit > 50
    || !object(selector.mapping) || !selector.mapping || !keys(selector.mapping, ['value', 'label', 'description', 'target'])
    || ![selector.mapping.value, selector.mapping.label, selector.mapping.description].every((field, index) =>
      index === 2 && field === undefined || typeof field === 'string' && selector.query?.fields.includes(field))
    || !identifier.test(selector.mapping.target) || !templateFields.includes(selector.mapping.target);
  if (invalid) {
    debugDialogSelector({
      event: 'selector-rejected',
      status: 'invalid',
      templateFieldCount: templateFields.length,
      queryFieldCount: Array.isArray(selector?.query?.fields) ? selector.query.fields.length : 0
    });
    throw new Error('Dialog selector must declare a bounded gh search repos query, debounce, and field mapping to a command template value.');
  }
}
