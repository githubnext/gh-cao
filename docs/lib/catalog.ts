import type { MarkdownInstance } from "astro";
import { parse } from "yaml";
import rootManifestSource from "../../aw.yml?raw";

type CampaignReadme = MarkdownInstance<Record<string, unknown>>;

type CampaignManifest = {
  name?: unknown;
  description?: unknown;
  "min-version"?: unknown;
  private?: unknown;
  experimental?: unknown;
  includes?: unknown;
};

export type CatalogEntry = {
  slug: string;
  name: string;
  description: string;
  minVersion: string;
  private: boolean;
  builtin: boolean;
  experimental: boolean;
  includes: string[];
  manifestFile: string;
  manifestPath: string;
  readmePath?: string;
  ReadmeContent?: CampaignReadme["Content"];
};

const manifests = import.meta.glob<string>("../../*/aw.{yml,yaml}", {
  query: "?raw",
  import: "default",
  eager: true,
  exhaustive: true,
});
const readmes = import.meta.glob<CampaignReadme>("../../*/README.md", {
  eager: true,
  exhaustive: true,
});
const rootManifest = parse(rootManifestSource) as CampaignManifest;

function requiredString(value: unknown, field: string, manifestPath: string): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`${manifestPath} must define a non-empty ${field}`);
  }
  return value.trim();
}

function optionalBoolean(value: unknown, field: string, manifestPath: string): boolean {
  if (value !== undefined && typeof value !== "boolean") {
    throw new Error(`${manifestPath} ${field} must be a boolean`);
  }
  return value === true;
}

function workflowList(value: unknown, manifestPath: string): string[] {
  if (!Array.isArray(value)) {
    throw new Error(`${manifestPath} must define includes as a list of workflow paths or mappings`);
  }

  return value.map((item, index) => {
    if (typeof item === "string") return item;
    if (typeof item !== "object" || item === null || Array.isArray(item)) {
      throw new Error(`${manifestPath} includes[${index}] must be a workflow path or mapping`);
    }

    const mapping = item as Record<string, unknown>;
    requiredString(mapping.source, `includes[${index}].source`, manifestPath);
    return requiredString(mapping.destination, `includes[${index}].destination`, manifestPath);
  });
}

function builtinCampaignSlugs(root: CampaignManifest): Set<string> {
  const includes = workflowList(root.includes, "../../aw.yml");
  return new Set(
    includes
      .map((entry) => entry.match(/^([^/]+)\/aw\.ya?ml$/)?.[1])
      .filter((slug): slug is string => Boolean(slug)),
  );
}

const builtinSlugs = builtinCampaignSlugs(rootManifest);

const campaignEntries: CatalogEntry[] = Object.entries(manifests)
  .map(([manifestPath, source]) => {
    const slug = manifestPath.split("/").at(-2);
    if (!slug) throw new Error(`Could not derive a campaign slug from ${manifestPath}`);
    const manifestFile = manifestPath.split("/").at(-1);
    if (!manifestFile) throw new Error(`Could not derive a manifest filename from ${manifestPath}`);
    const campaignPath = manifestPath.replace(/^\.\.\//, "").replace(/\/aw\.ya?ml$/, "");

    const manifest = parse(source) as CampaignManifest;
    const readmeSourcePath = `../../${campaignPath}/README.md`;
    const readme = readmes[readmeSourcePath];

    return {
      slug,
      name: requiredString(manifest.name, "name", manifestPath),
      description: requiredString(manifest.description, "description", manifestPath),
      minVersion: requiredString(manifest["min-version"], "min-version", manifestPath),
      private: optionalBoolean(manifest.private, "private", manifestPath),
      builtin: builtinSlugs.has(slug),
      experimental: optionalBoolean(manifest.experimental, "experimental", manifestPath),
      includes: workflowList(manifest.includes, manifestPath),
      manifestFile,
      manifestPath: `${campaignPath}/${manifestFile}`,
      readmePath: readme ? `${campaignPath}/README.md` : undefined,
      ReadmeContent: readme?.Content,
    };
  });

export const catalogEntries = campaignEntries
  .filter((entry) => !entry.private)
  .sort((left, right) => {
    const advisoryRank = (entry: CatalogEntry) => /advisor(y|ies)?/i.test(entry.name) ? 1 : 0;
    return advisoryRank(left) - advisoryRank(right) || left.name.localeCompare(right.name);
  });