import { debuglog } from 'node:util';

const DEBUG_PREFIX = 'cao';

/**
 * Creates a category-scoped debug logger for the activity CLI, built on
 * Node's built-in `util.debuglog`. Logging is a no-op unless the category
 * (e.g. `cao:ingest`) matches a `NODE_DEBUG` pattern, so it is safe to call
 * unconditionally in hot paths without an explicit enabled-check.
 *
 * @param {string} category
 */
export function createDebug(category) {
  return debuglog(`${DEBUG_PREFIX}:${category}`);
}
