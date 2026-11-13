import { readFile, realpath } from "node:fs/promises";
import { isAbsolute, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

export const bundledResources = Object.freeze({
  queryEngine: "dashboard/site/src/data/queries/declarative.js",
  site: "dashboard/site",
  dataSpecification: "specs/dashboard-data.md",
  languageSpecification: "docs/dashboard-language-specification.md",
  queryDesignerSkill: ".github/skills/generate-dashboard-ir/SKILL.md",
  intentAuthoringSkill: ".github/skills/author-dashboard-intent/SKILL.md",
  dashboardAuthoringSkill: ".github/skills/dashboard-authoring/SKILL.md",
  declarativeChartsGuide: ".github/skills/generate-dashboard-ir/references/declarative-charts.md",
  builtInDashboard: "dashboard/site/dashboard.json",
});

const pluginRootUrl = new URL("../../../", import.meta.url);

export async function resolveBundledResource(name) {
  const root = await realpath(fileURLToPath(pluginRootUrl));
  const withinRoot = async (url) => {
    const path = await realpath(fileURLToPath(url));
    const relativePath = relative(root, path);
    if (isAbsolute(relativePath) || relativePath === ".." || relativePath.startsWith(`..${sep}`)) {
      throw new Error("A bundled CAO dashboard resource resolves outside the plugin root.");
    }
    return path;
  };
  let manifestPath;
  try {
    manifestPath = await withinRoot(new URL("plugin.json", pluginRootUrl));
  } catch (error) {
    if (error?.code !== "ENOENT" && error?.code !== "ENOTDIR") throw error;
    throw new Error("The CAO dashboard extension requires the complete Central Agentic Ops plugin bundle.");
  }
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  if (!manifest || typeof manifest !== "object" || Array.isArray(manifest)
      || manifest.name !== "central-agentic-ops"
      || manifest.$schema !== "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json") {
    throw new Error("The CAO dashboard extension requires a valid Central Agentic Ops plugin manifest.");
  }
  if (!Object.hasOwn(bundledResources, name)) {
    throw new Error("Unknown bundled CAO dashboard resource.");
  }
  return withinRoot(new URL(bundledResources[name], pluginRootUrl));
}
