const VERSION_PATTERN = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

export function parseVersion(value) {
  if (typeof value !== "string") return null;
  const match = value.trim().match(VERSION_PATTERN);
  if (!match) return null;
  const prerelease = match[4]?.split(".") || [];
  if (prerelease.some((part) => /^\d+$/.test(part) && part.length > 1 && part.startsWith("0"))) return null;
  return {
    parts: match.slice(1, 4).map(Number),
    prerelease,
    normalized: `v${match[1]}.${match[2]}.${match[3]}${match[4] ? `-${match[4]}` : ""}`,
  };
}

export function normalizeVersion(value) {
  return parseVersion(value)?.normalized || null;
}

export function compilerVersionFromLock(source) {
  const metadata = String(source || "").match(/^# gh-aw-metadata: (.+)$/m)?.[1];
  try {
    return normalizeVersion(metadata ? JSON.parse(metadata)?.compiler_version : null);
  } catch {
    return null;
  }
}

export function compareVersions(left, right) {
  const parsedLeft = parseVersion(left);
  const parsedRight = parseVersion(right);
  if (!parsedLeft || !parsedRight) return null;
  for (let index = 0; index < 3; index += 1) {
    const difference = parsedLeft.parts[index] - parsedRight.parts[index];
    if (difference !== 0) return difference;
  }
  if (parsedLeft.prerelease.length === 0 || parsedRight.prerelease.length === 0) {
    return parsedLeft.prerelease.length === parsedRight.prerelease.length
      ? 0
      : parsedLeft.prerelease.length === 0 ? 1 : -1;
  }
  for (let index = 0; index < Math.max(parsedLeft.prerelease.length, parsedRight.prerelease.length); index += 1) {
    const leftPart = parsedLeft.prerelease[index];
    const rightPart = parsedRight.prerelease[index];
    if (leftPart === undefined || rightPart === undefined) return leftPart === undefined ? -1 : 1;
    if (leftPart === rightPart) continue;
    const leftNumeric = /^\d+$/.test(leftPart);
    const rightNumeric = /^\d+$/.test(rightPart);
    if (leftNumeric && rightNumeric) return Number(leftPart) - Number(rightPart);
    if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1;
    return leftPart.localeCompare(rightPart);
  }
  return 0;
}

export function updateState(version, latestVersion) {
  const comparison = version && latestVersion ? compareVersions(version, latestVersion) : null;
  return comparison === null ? "unknown" : comparison < 0 ? "update-available" : "up-to-date";
}

export function revisionUpdateState(revision, latestRevision) {
  const installed = String(revision || "").trim().toLowerCase();
  const latest = String(latestRevision || "").trim().toLowerCase();
  if (!installed || !latest) return "unknown";
  return installed === latest ? "up-to-date" : "update-available";
}
