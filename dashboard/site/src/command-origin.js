// Mirrors operational.ValidCLICommandOrigin: only bounded in-dashboard hash
// routes may be recorded as, or navigated to from, a command's origin.
const COMMAND_ORIGIN = /^#page-[a-z0-9][a-z0-9-]{0,127}(?:\?[A-Za-z0-9._~%&=+:,@/!*'()-]{0,1000})?$/;

/**
 * @param {unknown} value
 * @returns {string} The validated route, or an empty string.
 */
export function commandOriginRoute(value) {
  return typeof value === 'string' && COMMAND_ORIGIN.test(value) ? value : '';
}

/** @returns {string} The current dashboard route when it is a valid command origin. */
export function currentCommandOrigin() {
  return commandOriginRoute(globalThis.location?.hash);
}
