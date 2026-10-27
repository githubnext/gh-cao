import { isSpuriousAbortAfterSuccessResponse } from "./dashboard-view-assessment.mjs";

export const deployedDashboardUrl = "https://githubnext.github.io/gh-cao/cao/";
export const DEPLOYED_REFRESH_TIMEOUT_MS = 600_000;
// IndexedDB and the service-worker cache together exceed 2 GiB in the deployed snapshot.
export const DEPLOYED_STORAGE_QUOTA_BYTES = 4 * 1024 ** 3;
export const populatedDashboardPages = [
  { pageId: "repositories", storeName: "repositories" },
  { pageId: "workflows", storeName: "workflows" },
  { pageId: "runs", storeName: "runs" },
];
export const populatedOverviewStations = [
  { href: "#page-workflows", detail: /^\d[\d,]*\/[1-9][\d,]* healthy workflows$/ },
  { href: "#page-repositories", detail: /^\d[\d,]*\/[1-9][\d,]* repositories reached$/ },
];
const deployedDashboardBase = new URL(deployedDashboardUrl);
const deployedActivityShardDirectories = new Set(["gh-aw-logs-runs", "gh-aw-logs-records"]);

export async function deployedStorageContext({
  playwright, browserName, headless, channel, launchOptions, contextOptions,
}, use) {
  // Origin quota overrides do not enlarge Chromium's memory-sized incognito pool.
  // An empty userDataDir creates a fresh disk-backed profile removed on close.
  const context = await playwright[browserName].launchPersistentContext("", {
    headless, channel, ...launchOptions, ...contextOptions,
  });
  try {
    await use(context);
  } finally {
    await context.close();
  }
}

export async function configureDeployedStorageQuota(page, url) {
  const session = await page.context().newCDPSession(page);
  try {
    // Keep the origin budget independent of the runner's available storage.
    await session.send("Storage.overrideQuotaForOrigin", {
      origin: new URL(url).origin,
      quotaSize: DEPLOYED_STORAGE_QUOTA_BYTES,
    });
    return session;
  } catch (error) {
    await session.detach();
    throw error;
  }
}

export function deployedActivityShardEntries(manifest, maximumShardCount = Number.POSITIVE_INFINITY) {
  const entries = Object.entries(manifest ?? {})
    .filter(([, hash]) => typeof hash === "string" && /^[a-f0-9]{64}$/i.test(hash));
  const select = (extension) => entries
    .filter(([name]) => {
      const [directory, filename, ...extraSegments] = name.split("/");
      return deployedActivityShardDirectories.has(directory)
        && extraSegments.length === 0
        && filename?.endsWith(extension);
    })
    .map(([sourceName, hash]) => ({
      sourceName,
      name: extension === ".json" ? sourceName.replace(/\.json$/, ".jsonl") : sourceName,
      hash,
    }))
    .sort((left, right) => {
      const leftPhase = left.name.startsWith("gh-aw-logs-runs/") ? 0 : 1;
      const rightPhase = right.name.startsWith("gh-aw-logs-runs/") ? 0 : 1;
      return leftPhase - rightPhase || left.name.localeCompare(right.name);
    });
  const current = select(".jsonl");
  const selected = current.some(({ name }) => name.startsWith("gh-aw-logs-runs/"))
    ? current
    : select(".json");
  if (!selected.some(({ name }) => name.startsWith("gh-aw-logs-runs/"))) {
    throw new Error("Deployed dashboard manifest contains no compacted run-information shards.");
  }
  if (maximumShardCount === Number.POSITIVE_INFINITY) return selected;
  if (!Number.isSafeInteger(maximumShardCount) || maximumShardCount < 2) {
    throw new Error("Dashboard preview shard limit must be at least 2 to include both phases.");
  }
  const records = selected.find(({ name }) => name.startsWith("gh-aw-logs-records/"));
  if (!records) throw new Error("Deployed dashboard manifest contains no compacted record shards.");
  const limited = selected.slice(0, maximumShardCount);
  if (!limited.includes(records)) limited[limited.length - 1] = records;
  return limited;
}

export function legacyPhaseJsonToJsonl(content) {
  const payload = JSON.parse(Buffer.isBuffer(content) ? content.toString("utf8") : String(content));
  if (!payload?.batch || typeof payload.batch !== "object" || !["runs", "records"].includes(payload.phase)) {
    throw new Error("Legacy deployed phase payload is invalid.");
  }
  const records = Object.entries(payload.batch).flatMap(([collection, values]) => {
    if (!Array.isArray(values)) throw new Error(`Legacy deployed phase collection ${collection} is invalid.`);
    return values.map((record) => ({ kind: "record", collection, record }));
  });
  return [
    {
      kind: "metadata",
      schemaVersion: payload.schemaVersion,
      ingestionVersion: 3,
      sourceRecords: payload.sourceRecords,
      phase: payload.phase,
      records: records.length,
    },
    ...records,
  ].map((line) => JSON.stringify(line)).join("\n") + "\n";
}

export function shouldIgnoreRequestFailure({ method, url, errorText, reloading = false, hadSuccessResponse = false }) {
  return isSpuriousAbortAfterSuccessResponse(errorText, hadSuccessResponse)
    || errorText === "net::ERR_ABORTED" && (
    reloading || isDeployedDashboardShardProbe({ method, url })
  );
}

export async function activateTableViewMode(activePage) {
  const tableMode = activePage.locator('[data-view-mode-value="table"]');
  if (await tableMode.count() === 0) return false;
  await tableMode.click();
  return true;
}

function isDeployedDashboardShardProbe({ method, url }) {
  if (method !== "HEAD" || typeof url !== "string") return false;
  try {
    const parsed = new URL(url);
    // Match flat deployed activity JSONL shard filenames in the run-information and record shard directories.
    return parsed.origin === deployedDashboardBase.origin
      && parsed.pathname.startsWith(deployedDashboardBase.pathname)
      && isFlatDeployedActivityShardPath(parsed.pathname.slice(deployedDashboardBase.pathname.length));
  } catch {
    return false;
  }
}

function isFlatDeployedActivityShardPath(relativePath) {
  const [directory, filename, ...extraSegments] = relativePath.split("/");
  return deployedActivityShardDirectories.has(directory)
    && extraSegments.length === 0
    && typeof filename === "string"
    && filename.length > 0
    && filename.endsWith(".jsonl");
}

export async function scrollRenderedViewsIntoView(activePage) {
  const viewHandles = await activePage.locator("[data-view-id]").elementHandles();
  try {
    for (const viewHandle of viewHandles) {
      try {
        await viewHandle.evaluate((element) => {
          element.scrollIntoView({ block: "center", inline: "nearest" });
          if (element.matches("[data-lazy-view]")) {
            element.focus({ preventScroll: true });
          }
        });
      } catch (error) {
        if (!isDetachedViewError(error)) throw error;
      }
    }
  } finally {
    await Promise.allSettled(viewHandles.map((viewHandle) => viewHandle.dispose()));
  }
}

function isDetachedViewError(error) {
  const name = error && typeof error === "object" && "name" in error
    ? String(error.name)
    : "";
  const message = error instanceof Error ? error.message : String(error);
  return name === "DetachedElementError"
    || /Element is not attached to the DOM|Execution context was destroyed/i.test(message);
}
