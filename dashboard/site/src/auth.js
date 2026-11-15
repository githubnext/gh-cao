import { createDebug, diagnosticErrorName } from './debug.js';
import { updateRateLimitNotification } from './rate-limit-notification.js';

const CSRF_COOKIE_NAME = 'cao_csrf';

const debugAuth = createDebug('auth');
let resourcePrincipal = '';

export function githubResourcePrincipal() {
  return resourcePrincipal;
}

/** Clears worker-owned private resource results at an authentication boundary. */
/** @param {boolean} [resume] */
export function invalidateGitHubResourceAuthentication(resume = false) {
  debugAuth({ event: 'resource-authentication-invalidated', resume });
  globalThis.dispatchEvent?.(new CustomEvent('cao-authentication-change', { detail: { resume } }));
}

/** @param {string} principal */
export function setGitHubResourcePrincipal(principal) {
  if (resourcePrincipal === principal) return;
  debugAuth({ event: 'resource-principal-changed', hadPrincipal: resourcePrincipal !== '', hasPrincipal: principal !== '' });
  resourcePrincipal = principal;
  invalidateGitHubResourceAuthentication(true);
}

export function usesGitHubAuthentication(document = globalThis.document) {
  const oauth = document
    ?.querySelector?.('meta[name="cao-auth-mode"]')
    ?.getAttribute('content') === 'github';
  debugAuth({ event: 'mode-resolved', mode: oauth ? 'github' : 'token' });
  return oauth;
}

export function csrfToken(document = globalThis.document) {
  const prefix = `${CSRF_COOKIE_NAME}=`;
  const cookie = document?.cookie
    ?.split(';')
    .map((entry) => entry.trim())
    .find((entry) => entry.startsWith(prefix));
  debugAuth({ event: 'csrf-token-resolved', found: Boolean(cookie) });
  if (!cookie) return '';
  return decodeURIComponent(cookie.slice(prefix.length));
}

export function csrfHeaders(headers = {}, document = globalThis.document) {
  const token = csrfToken(document);
  const applied = Boolean(token);
  debugAuth({ event: 'csrf-headers-applied', applied });
  return applied ? { ...headers, 'X-CSRF-Token': token } : headers;
}

/** @param {AbortSignal} [signal] */
export async function ensureCsrfToken(signal) {
  if (csrfToken()) return;
  const startedAt = Date.now();
  let response;
  try {
    response = await fetch('/api/auth/session', {
      cache: 'no-store',
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
      signal
    });
  } catch (error) {
    debugAuth({ event: 'session-renewal-failed', errorName: diagnosticErrorName(error), durationMs: Date.now() - startedAt });
    throw error;
  }
  updateRateLimitNotification('/api/auth/session', response.status);
  const renewed = response.ok && Boolean(csrfToken());
  debugAuth({ event: 'session-renewal-completed', status: response.status, renewed, durationMs: Date.now() - startedAt });
  if (!renewed) {
    throw new Error('GitHub authentication cookie could not be renewed');
  }
}
