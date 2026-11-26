import { createDebug } from './debug.js';

const debugCommandOrigin = createDebug('command-origin');

// Mirrors operational.ValidCLICommandOrigin: only bounded in-dashboard hash
// routes may be recorded as, or navigated to from, a command's origin.
const COMMAND_ORIGIN = /^#page-[a-z0-9][a-z0-9-]{0,127}(?:\?[A-Za-z0-9._~%&=+:,@/!*'()-]{0,1000})?$/;

/**
 * @param {unknown} value
 * @returns {string} The validated route, or an empty string.
 */
export function commandOriginRoute(value) {
  if (typeof value === 'string' && COMMAND_ORIGIN.test(value)) return value;
  if (value !== undefined && value !== '') {
    debugCommandOrigin({
      operation: 'validate-route',
      status: 'rejected',
      reason: typeof value === 'string' ? 'invalid-format' : 'not-string',
      length: typeof value === 'string' ? value.length : 0
    });
  }
  return '';
}

/** @returns {string} The current dashboard route when it is a valid command origin. */
export function currentCommandOrigin() {
  return commandOriginRoute(globalThis.location?.hash);
}
