import { createWriteStream } from "node:fs";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { pipeline } from "node:stream/promises";
import {
  deployedActivityShardEntries,
  legacyPhaseJsonToJsonl,
} from "./dashboard-deployed-refresh-helpers.mjs";

const memoryDownloadConcurrency = 8;

export async function downloadDeployedDashboardData(
  destination,
  sourceUrl,
  fetcher = fetch,
  maximumShardCount = Number.POSITIVE_INFINITY,
) {
  const inventoryUrl = new URL("inventory-sources.json", sourceUrl);
  const [manifestResponse, inventoryResponse] = await Promise.all([
    fetcher(sourceUrl),
    fetcher(inventoryUrl),
  ]);
  if (!manifestResponse.ok) {
    throw new Error(`Unable to download deployed dashboard data: HTTP ${manifestResponse.status}.`);
  }
  if (!inventoryResponse.ok) {
    throw new Error(
      `Unable to download deployed dashboard inventory: HTTP ${inventoryResponse.status}.`,
    );
  }
  const manifest = await manifestResponse.json();
  if (!inventoryResponse.body) throw new Error("Deployed dashboard inventory response has no body.");
  await mkdir(destination, { recursive: true });
  await pipeline(inventoryResponse.body, createWriteStream(join(destination, "inventory-sources.json")));
  const memoryRoot = new URL("memory/", sourceUrl);
  const memoryManifestResponse = await fetcher(new URL("manifest.json", memoryRoot));
  if (!memoryManifestResponse.ok && memoryManifestResponse.status !== 404) {
    throw new Error(`Unable to download deployed repository memory: HTTP ${memoryManifestResponse.status}.`);
  }
  const memoryManifest = memoryManifestResponse.ok
    ? await memoryManifestResponse.json()
    : { version: 1, campaigns: [] };
  const memoryDirectory = join(destination, "memory");
  await mkdir(memoryDirectory, { recursive: true });
  await writeFile(join(memoryDirectory, "manifest.json"), `${JSON.stringify(memoryManifest)}\n`);
  const memoryFiles = [];
  for (const campaign of Array.isArray(memoryManifest.campaigns) ? memoryManifest.campaigns : []) {
    if (
      typeof campaign?.campaign !== "string"
      || !/^[a-z0-9](?:[a-z0-9._-]{0,99})$/.test(campaign.campaign)
      || !Array.isArray(campaign.files)
    ) continue;
    const campaignRoot = new URL(`${campaign.campaign}/`, memoryRoot);
    for (const file of campaign.files) {
      if (
        typeof file?.path !== "string"
        || !file.path
        || file.path.startsWith("/")
        || file.path.includes("\\")
      ) continue;
      const fileUrl = new URL(file.path, campaignRoot);
      if (fileUrl.origin !== campaignRoot.origin || !fileUrl.href.startsWith(campaignRoot.href)) continue;
      const relativePath = decodeURIComponent(
        fileUrl.pathname.slice(campaignRoot.pathname.length),
      );
      if (
        !relativePath
        || relativePath.startsWith("/")
        || relativePath.includes("\\")
        || relativePath.split("/").some((segment) => !segment || segment === "." || segment === "..")
      ) continue;
      memoryFiles.push({
        campaign: campaign.campaign,
        path: file.path,
        url: fileUrl,
        relativePath,
      });
    }
  }
  let nextMemoryFile = 0;
  await Promise.all(Array.from({ length: Math.min(memoryDownloadConcurrency, memoryFiles.length) }, async () => {
    while (nextMemoryFile < memoryFiles.length) {
      const file = memoryFiles[nextMemoryFile];
      nextMemoryFile += 1;
      const response = await fetcher(file.url);
      if (!response.ok || !response.body) {
        throw new Error(`Unable to download deployed repository memory file: ${file.path}.`);
      }
      const destinationPath = join(memoryDirectory, file.campaign, file.relativePath);
      await mkdir(dirname(destinationPath), { recursive: true });
      await pipeline(response.body, createWriteStream(destinationPath));
    }
  }));
  for (const { name, sourceName } of deployedActivityShardEntries(manifest, maximumShardCount)) {
    const response = await fetcher(new URL(sourceName, sourceUrl));
    if (!response.ok || !response.body) throw new Error(`Unable to download deployed dashboard shard: ${sourceName}.`);
    await mkdir(dirname(join(destination, name)), { recursive: true });
    if (sourceName.endsWith(".json")) {
      await writeFile(join(destination, name), legacyPhaseJsonToJsonl(Buffer.from(await response.arrayBuffer())));
    } else {
      await pipeline(response.body, createWriteStream(join(destination, name)));
    }
  }
}
