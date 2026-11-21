import { h } from '../dom.js';

const maximumItems = 50;
/** @type {Record<string, string>} */
const fieldLabels = {
  schema: 'Schema', id: 'ID', objective: 'Objective', repository: 'Repository',
  base_branch: 'Base branch', base_sha: 'Baseline SHA', title: 'Title',
  acceptance: 'Acceptance criteria', depends_on: 'Depends on', specialists: 'Specialists',
  integration_acceptance: 'Integration acceptance'
};

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function isObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** @param {string} key */
function label(key) {
  return Object.hasOwn(fieldLabels, key) ? fieldLabels[key] : key.replaceAll('_', ' ');
}

/** @param {unknown} value @param {number} [depth] @returns {HTMLElement} */
function renderValue(value, depth = 0) {
  if (value === undefined) return h('span', { className: 'modernization-description' }, 'Not provided');
  if (value === null) return h('code', {}, 'null');
  if (typeof value !== 'object') return h('span', {}, String(value));
  if (depth >= 4) return h('p', { className: 'modernization-description' }, 'Nested content is available in the JSON tab.');
  if (Array.isArray(value)) {
    if (!value.length) return h('span', { className: 'modernization-description' }, 'None listed');
    return h('div', {},
      h('ul', {}, ...value.slice(0, maximumItems).map(item => h('li', {}, renderValue(item, depth + 1)))),
      overflowNote(value.length));
  }
  if (!Object.keys(value).length) return h('code', {}, '{}');
  return renderFields(/** @type {Record<string, unknown>} */ (value), [], depth + 1);
}

/** @param {number} count */
function overflowNote(count) {
  return count > maximumItems ? h('p', { className: 'modernization-notice' },
    `Showing the first ${maximumItems} of ${count} entries. The JSON tab retains every entry.`) : null;
}

/**
 * @param {Record<string, unknown>} object
 * @param {string[]} excluded
 * @param {number} [depth]
 */
function renderFields(object, excluded, depth = 0) {
  const fields = Object.entries(object).filter(([key]) => !excluded.includes(key));
  return h('div', {},
    h('dl', { className: 'modernization-plan-fields' }, ...fields.slice(0, maximumItems).flatMap(([key, value]) => [
      h('dt', {}, label(key)),
      h('dd', {}, key === 'base_sha' && typeof value === 'string' ? h('code', {}, value) : renderValue(value, depth))
    ])), overflowNote(fields.length));
}

/**
 * Presentation of unsaved form input only. Native validation owns plan semantics.
 * @param {string} text
 */
export function renderModernizationPlanOverview(text, approved = false) {
  if (!text.trim()) return h('p', { className: 'modernization-description', role: 'status' },
    'Import a plan or open the JSON tab to start drafting.');
  /** @type {unknown} */
  let plan;
  try {
    plan = JSON.parse(text);
  } catch (error) {
    if (!(error instanceof SyntaxError)) throw error;
    return h('p', { className: 'modernization-error', role: 'alert' },
      'The plan JSON is incomplete or invalid. Open the JSON tab to correct it; your text is unchanged.');
  }
  if (!isObject(plan)) return h('p', { className: 'modernization-error', role: 'alert' },
    'The plan must be a JSON object. Open the JSON tab to review the input; your text is unchanged.');
  return h('div', { className: 'modernization-plan-overview' },
    h('header', { className: 'modernization-plan-intro' },
      h('h5', {}, typeof plan.id === 'string' ? plan.id : 'Plan ID not provided'),
      h('p', { className: 'modernization-description' }, approved
        ? 'Currently approved plan, as reported by the native readiness check. Edits become a new draft that needs approval.'
        : 'Draft preview only. Native validation is required before approval.'),
      h('div', { className: 'modernization-plan-objective' }, renderValue(plan.objective)),
      renderFields(plan, ['objective', 'repositories', 'tasks', 'integration_acceptance', ...(typeof plan.id === 'string' ? ['id'] : [])])),
    h('section', { 'aria-label': 'Plan repositories' },
      h('h5', {}, 'Repositories'),
      Array.isArray(plan.repositories) ? h('div', {},
        h('ul', { className: 'modernization-plan-repositories' }, ...plan.repositories.slice(0, maximumItems).map(repository => h('li', {},
          isObject(repository) ? h('div', {},
            h('strong', {}, renderValue(repository.repository)),
            renderFields(repository, ['repository'])) : renderValue(repository)))),
        !plan.repositories.length ? renderValue(plan.repositories) : null,
        overflowNote(plan.repositories.length)) : renderValue(plan.repositories)),
    h('section', { 'aria-label': 'Plan tasks' },
      h('h5', {}, 'Tasks'),
      Array.isArray(plan.tasks) ? h('div', {},
        h('ol', { className: 'modernization-plan-tasks' }, ...plan.tasks.slice(0, maximumItems).map(task => h('li', {},
          isObject(task) ? h('article', {},
            h('h6', {}, h('code', {}, renderValue(task.id)), ' ', renderValue(task.title)),
            h('div', { className: 'modernization-description' }, renderValue(task.repository)),
            h('div', { className: 'modernization-plan-objective' }, renderValue(task.objective)),
            renderFields(task, ['id', 'title', 'repository', 'objective'])) : renderValue(task)))),
        !plan.tasks.length ? renderValue(plan.tasks) : null,
        overflowNote(plan.tasks.length)) : renderValue(plan.tasks)),
    h('section', { 'aria-label': 'Plan integration acceptance' },
      h('h5', {}, 'Integration acceptance'),
      renderValue(plan.integration_acceptance)));
}
