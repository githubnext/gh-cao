import { ACTION_LEVELS } from './action-model.js';
import { ERROR_CODES, IDENTIFIER_PATTERN } from './specification.js';
import { createDebug } from './debug.js';
import { environmentDefaultsCommand } from './environment-defaults.js';

const debugActionValidator = createDebug('action-validator');

/**
 * @param {string} command
 * @returns {string[] | null}
 */
export function parseCliActionTokens(command) {
  const tokens = [];
  let token = '';
  let quote = null;
  let escaping = false;
  let tokenStarted = false;
  for (const character of command) {
    if (escaping) {
      token += character;
      tokenStarted = true;
      escaping = false;
    } else if (character === '\\' && quote !== "'") {
      escaping = true;
      tokenStarted = true;
    } else if (quote) {
      if (character === quote) quote = null;
      else token += character;
      tokenStarted = true;
    } else if (character === "'" || character === '"') {
      quote = character;
      tokenStarted = true;
    } else if (/\s/.test(character)) {
      if (tokenStarted) {
        tokens.push(token);
        token = '';
        tokenStarted = false;
      }
    } else {
      token += character;
      tokenStarted = true;
    }
  }
  if (escaping || quote) return null;
  if (tokenStarted) tokens.push(token);
  return tokens;
}

/**
 * @param {string[]} args
 * @returns {boolean}
 */
export function validWorkflowDispatchArguments(args) {
  const valid = workflowDispatchArgumentsAreValid(args);
  debugActionValidator({ event: 'workflow-dispatch-arguments-checked', status: valid ? 'valid' : 'invalid', argumentCount: args.length });
  return valid;
}

/**
 * @param {string[]} args
 * @returns {boolean}
 */
function workflowDispatchArgumentsAreValid(args) {
  if (!args[0] || args[0].startsWith('-')) return false;
  const repositoryPattern =
    /^(?:[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+|\{\{[a-z][a-z0-9-]*\}\})$/;
  const inputPattern = /^[A-Za-z_][A-Za-z0-9_-]*=.*$/s;
  for (let index = 1; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === '--repo' || argument === '-R') {
      if (!repositoryPattern.test(args[index + 1] ?? '')) return false;
      index += 1;
    } else if (argument.startsWith('--repo=')) {
      if (!repositoryPattern.test(argument.slice('--repo='.length))) return false;
    } else if (argument === '--ref') {
      if (!args[index + 1] || args[index + 1].startsWith('-')) return false;
      index += 1;
    } else if (argument.startsWith('--ref=')) {
      if (argument.length === '--ref='.length) return false;
    } else if (argument === '--raw-field' || argument === '-f') {
      if (!inputPattern.test(args[index + 1] ?? '')) return false;
      index += 1;
    } else if (argument.startsWith('--raw-field=')) {
      if (!inputPattern.test(argument.slice('--raw-field='.length))) return false;
    } else {
      return false;
    }
  }
  return true;
}

/** @param {{ command?: string, arguments?: unknown[] }} action */
export function isReadOnlyCliAction(action) {
  if (action.arguments?.length || typeof action.command !== 'string') return false;
  const tokens = parseCliActionTokens(action.command);
  const allowed = environmentDefaultsCommand(action.command)?.operation === 'get' || Boolean(tokens && (
    tokens[0] === 'gh' && tokens[1] === 'cao' && tokens[2] === 'modernization'
      && tokens[4] === '--repo' && (tokens[5] === '{{repository}}' || /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(tokens[5] ?? ''))
      && (tokens[3] === 'status' && tokens.length === 6
        || tokens[3] === 'preview' && tokens.length === 10 && tokens[6] === '--action'
          && ['assess', 'approve-plan', 'run', 'verify', '{{operation}}'].includes(tokens[7])
          && tokens[8] === '--mode' && ['review', 'live', '{{mode}}'].includes(tokens[9]))
    ||
    tokens[0] === 'gh' && tokens[1] === 'aw'
      && ['status', 'list', 'logs', 'version'].includes(tokens[2]) && tokens.length === 3
    || tokens[0] === 'gh' && tokens[1] === 'cao' && (
      ['query', 'query-info', 'prompt'].includes(tokens[2])
        && tokens.length === 4 && IDENTIFIER_PATTERN.test(tokens[3])
    )
  ));
  debugActionValidator({ event: 'read-only-cli-check', status: allowed ? 'allowed' : 'rejected', tokenCount: tokens?.length ?? 0 });
  return allowed;
}

/**
 * @param {unknown} level
 * @param {string} path
 * @param {import('./validator.js').ValidationError[]} errors
 */
export function validateActionLevel(level, path, errors) {
  if (level !== undefined && !ACTION_LEVELS.some((allowed) => allowed === level)) {
    errors.push({
      code: ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      message: 'action level must be ui, explore, propose, or operate.',
      path
    });
  } else if (level === 'ui') {
    errors.push({
      code: ERROR_CODES.nonCanonicalVocabularyOrIdentifier,
      message: 'The ui level is reserved for native dashboard and account-session controls; prompts, CLI commands, and external links cannot use it.',
      path
    });
  }
  if (errors.length > 0 && errors.at(-1)?.path === path) {
    debugActionValidator({ event: 'action-level-rejected', reason: level === 'ui' ? 'ui-reserved' : 'unknown-level' });
  }
}
