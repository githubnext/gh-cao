export function isGithubRateLimitError(message) {
  return /HTTP (?:403|429):[^\n]*(?:API rate limit exceeded|secondary rate limit)|(?:API rate limit exceeded|secondary rate limit)[^\n]*\(HTTP (?:403|429)\)|API rate limit exceeded for (?:user|installation) ID/i.test(message);
}

export function normalizeRateLimit(document) {
  const resources = {};
  for (const [name, value] of Object.entries(document?.resources || {})) {
    if (![value?.limit, value?.remaining, value?.reset].every(Number.isSafeInteger)) continue;
    resources[name] = {
      limit: value.limit,
      used: Number.isSafeInteger(value.used) ? value.used : Math.max(0, value.limit - value.remaining),
      remaining: value.remaining,
      resetAt: new Date(value.reset * 1000).toISOString(),
    };
  }
  return resources;
}
