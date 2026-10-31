import { createHash } from "node:crypto";
import { access, cp, mkdir, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build, transform } from "esbuild";
import { bundleDashboardFiles } from "../../report/bundle-dashboards.mjs";
import { configureSite } from "../../report/configure-site.mjs";
import { loadPolicyFile } from "../../../.github/workflows/shared/policy.mjs";
import { buildDashboardPageChunkPath, splitDashboardDocument } from "../src/dashboard-chunks.js";
import { generateDashboardLlms } from "./llms.mjs";
import { agentCatalog } from "../src/agent/catalog.js";
import { primerStyles } from "../src/styles-primer.js";
import { notificationStyles } from "../src/styles-notifications.js";
import { OCTICON_SPRITE } from "../src/octicon-sprite.js";
import octiconNames from "../src/octicon-names.json" with { type: "json" };

const siteRoot = new URL("../", import.meta.url);

export async function buildDashboardSite({
  destination,
  controlSettings,
  commitSha,
  caoVersion = process.env.CAO_VERSION,
  activityDataPath,
  githubRepository = process.env.GITHUB_REPOSITORY,
  githubServerUrl = process.env.GITHUB_SERVER_URL,
  repositoryRoot = new URL("../../../", import.meta.url),
}) {
  if (!destination) throw new Error("dashboard destination is required");
  if (!controlSettings || typeof controlSettings !== "object" || Array.isArray(controlSettings)) {
    throw new Error("resolved control settings are required");
  }

  const requestedDestinationPath = destination instanceof URL ? fileURLToPath(destination) : resolve(destination);
  const repositoryPath = repositoryRoot instanceof URL ? fileURLToPath(repositoryRoot) : resolve(repositoryRoot);

  await rm(requestedDestinationPath, { force: true, recursive: true });
  await mkdir(requestedDestinationPath, { recursive: true });
  const destinationPath = await realpath(requestedDestinationPath);
  await Promise.all([
    cp(new URL("index.html", siteRoot), join(destinationPath, "index.html")),
    cp(new URL("favicon.svg", siteRoot), join(destinationPath, "favicon.svg")),
    cp(new URL("apple-touch-icon.png", siteRoot), join(destinationPath, "apple-touch-icon.png")),
    cp(new URL("icon-192.png", siteRoot), join(destinationPath, "icon-192.png")),
    cp(new URL("icon-512.png", siteRoot), join(destinationPath, "icon-512.png")),
    cp(new URL("icon-maskable-512.png", siteRoot), join(destinationPath, "icon-maskable-512.png")),
    cp(new URL("manifest.webmanifest", siteRoot), join(destinationPath, "manifest.webmanifest")),
    cp(new URL("service-worker.js", siteRoot), join(destinationPath, "service-worker.js")),
    cp(new URL("dashboard-fragments", siteRoot), join(destinationPath, "dashboard-fragments"), { recursive: true }),
    cp(new URL("src/smells.svg", siteRoot), join(destinationPath, "smells.svg")),
    cp(new URL("src", siteRoot), join(destinationPath, "src"), { recursive: true }),
  ]);

  const indexPath = join(destinationPath, "index.html");
  const configuredIndex = configureSite(await readFile(indexPath, "utf8"), controlSettings);
  const versions = {
    caoVersion: caoVersion && caoVersion !== "dev" ? caoVersion : commitSha
      ? `0.0.0-main.${commitSha.slice(0, 12)}` : undefined,
    ghAwVersion: await loadDashboardGhAwVersion(repositoryPath),
  };
  await writeFile(indexPath, embedDashboardVersions(embedDashboardVersion(configuredIndex, commitSha), versions));

  const campaignDashboards = await findCampaignDashboards(repositoryPath, controlSettings);

  const dashboardPath = join(destinationPath, "dashboard.json");
  await bundleDashboardFiles(
    dashboardPath,
    campaignDashboards,
    fileURLToPath(new URL("dashboard.json", siteRoot)),
  );
  const dashboard = filterExperimentalDashboardViews(
    JSON.parse(await readFile(dashboardPath, "utf8")),
    controlSettings.web?.experimental === true,
  );
  if (githubRepository) {
    if (!/^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?\/[A-Za-z0-9._-]+$/.test(githubRepository)
        || githubRepository.includes("..")) {
      throw new Error("GITHUB_REPOSITORY must be a valid owner/repository slug");
    }
    dashboard.dashboard.repository = githubRepository;
  }
  if (githubServerUrl) {
    const url = new URL(githubServerUrl);
    if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash) {
      throw new Error("GITHUB_SERVER_URL must be an HTTPS URL without credentials, query, or fragment");
    }
    dashboard.dashboard["github-url-base"] = url.href.replace(/\/+$/, "");
  }
  await Promise.all([
    writeFile(join(destinationPath, "src/agent/queries.generated.json"), `${JSON.stringify(dashboard.dashboard.queries)}\n`),
    writeFile(join(destinationPath, "src/agent/catalog.generated.json"), `${JSON.stringify(agentCatalog(dashboard), null, 2)}\n`),
  ]);
  const splitDashboard = splitDashboardDocument({
    languageVersion: dashboard["language-version"],
    dashboard: dashboard.dashboard,
  });
  await writeFile(dashboardPath, `${JSON.stringify(splitDashboard.core)}\n`);
  await writeDashboardPageChunks(destinationPath, splitDashboard.pageChunks);
  await generateDashboardLlms({
    outputPath: join(destinationPath, "llms.txt"),
    activityDataPath,
  });
  await bundleSiteJavascript(destinationPath);
  await rm(join(destinationPath, "dashboard-fragments"), { force: true, recursive: true });
  await cacheBustSiteImports(destinationPath);

  for (const page of splitDashboard.core.dashboard?.pages ?? []) {
    if (typeof page?.id !== "string" || !page.id || requiresHashQueryParameter(page)) continue;
    const routeDirectory = join(destinationPath, page.id);
    await mkdir(routeDirectory, { recursive: true });
    await writeFile(join(routeDirectory, "index.html"), redirectDocument(page.id));
  }
  const appAssets = ["./", ...(await listFiles(destinationPath))
    .filter((file) => !file.endsWith(".map")
      && !file.endsWith(".jsonl")
      && !file.startsWith("gh-aw-logs-")
      && !["service-worker.js", "llms.txt", "payload-hashes.json", "inventory-sources.json"].includes(file))];
  const workerPath = join(destinationPath, "service-worker.js");
  await writeFile(workerPath, (await readFile(workerPath, "utf8"))
    .replace("const APP_ASSETS = [];", `const APP_ASSETS = ${JSON.stringify(appAssets)};`));

  async function writeDashboardPageChunks(destinationPath, pageChunks) {
    for (const [pageId, chunk] of pageChunks) {
      const relativeChunkPath = buildDashboardPageChunkPath(pageId);
      const absoluteChunkPath = join(destinationPath, relativeChunkPath);
      await mkdir(join(destinationPath, "dashboard-pages"), { recursive: true });
      await writeFile(absoluteChunkPath, `${JSON.stringify(chunk, null, 2)}\n`);
    }
  }
}

export async function loadDashboardControlSettings(settingsPath) {
  const resolvedPath = resolve(settingsPath);
  const document = JSON.parse(await readFile(resolvedPath, "utf8"));
  if (document && typeof document === "object" && !Array.isArray(document)
      && ("extends" in document || "control-plane" in document)) {
    return loadPolicyFile(resolvedPath)["control-plane"];
  }
  return document;
}

export function embedDashboardVersion(html, commitSha) {
  if (commitSha === undefined) return html;
  if (typeof commitSha !== "string" || !/^[0-9a-f]{40}$/.test(commitSha)) {
    throw new Error("dashboard commit SHA must be a 40-character lowercase hexadecimal string");
  }
  const declaration = '<meta name="dashboard-version" content="development">';
  if (!html.includes(declaration)) throw new Error("dashboard version declaration is missing");
  return html.replace(declaration, `<meta name="dashboard-version" content="${commitSha}">`);
}

async function loadDashboardGhAwVersion(repositoryPath) {
  try {
    return JSON.parse(await readFile(join(repositoryPath, ".github/workflows/cao.json"), "utf8"))["gh-aw-version"];
  } catch (error) {
    if (error?.code === "ENOENT") return undefined;
    throw error;
  }
}

export function embedDashboardVersions(html, { caoVersion, ghAwVersion }) {
  for (const [name, value] of [["cao-version", caoVersion], ["gh-aw-version", ghAwVersion]]) {
    if (value === undefined) continue;
    if (typeof value !== "string" || !/^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$/.test(value)) {
      throw new Error(`${name} must be a semantic version`);
    }
    const declaration = `<meta name="${name}" content="">`;
    if (!html.includes(declaration)) throw new Error(`${name} declaration is missing`);
    html = html.replace(declaration, `<meta name="${name}" content="${value}">`);
  }
  return html;
}

export function filterExperimentalDashboardViews(document, enabled = false) {
  if (enabled || !Array.isArray(document.dashboard?.navigation)) return document;
  const experimentalPageIds = new Set(
    document.dashboard.navigation
      .filter((section) => section?.experimental === true)
      .flatMap((section) => Array.isArray(section.pages) ? section.pages : []),
  );
  for (const page of document.dashboard.pages) {
    if (page.experimental === true) experimentalPageIds.add(page.id);
  }
  if (experimentalPageIds.size === 0) return document;
  return {
    ...document,
    dashboard: {
      ...document.dashboard,
      pages: document.dashboard.pages.filter((page) => !experimentalPageIds.has(page.id)),
      navigation: document.dashboard.navigation.filter((section) => section?.experimental !== true),
      ...(Array.isArray(document.dashboard.callouts) ? {
        callouts: document.dashboard.callouts.filter((callout) => !experimentalPageIds.has(callout?.["navigation-page"])),
      } : {}),
    },
  };
}

async function findCampaignDashboards(repositoryPath, controlSettings) {
  const campaignDashboards = [];
  for (const campaignName of Object.keys(controlSettings.campaigns ?? {}).toSorted()) {
    const source = join(repositoryPath, campaignName, "dashboard.json");
    await access(source).then(() => campaignDashboards.push(source)).catch((error) => {
      if (error?.code !== "ENOENT") throw error;
    });
  }
  return campaignDashboards;
}

async function bundleSiteJavascript(destinationPath) {
  const bundlePath = join(destinationPath, ".bundle");
  try {
    const stylesheet = await transform(`${primerStyles}\n${notificationStyles}`, {
      loader: "css", minify: true, legalComments: "none", target: "es2022",
    });
    await writeFile(join(destinationPath, "styles.css"), stylesheet.code);
    const supportedIcons = new Set(octiconNames);
    const inlineIcons = new Set(["question"]);
    for (const file of await listFiles(destinationPath)) {
      if (!/\.(js|json)$/.test(file) || file.endsWith("octicon-sprite.js") || file.endsWith("octicon-names.json")) continue;
      const source = await readFile(join(destinationPath, file), "utf8");
      for (const match of source.matchAll(/(['"])([a-z][a-z0-9-]*)\1/g)) {
        if (supportedIcons.has(match[2])) inlineIcons.add(match[2]);
      }
    }
    const iconLicense = OCTICON_SPRITE.match(/<!--[\s\S]*?-->/)?.[0];
    if (!iconLicense) throw new Error("The Octicon license is missing.");
    const inlineSprite = '<svg xmlns="http://www.w3.org/2000/svg"><defs>' + iconLicense
      + [...OCTICON_SPRITE.matchAll(/<symbol\b[^>]*id="octicon-([^"]+)"[^>]*>[\s\S]*?<\/symbol>/g)]
        .filter((symbol) => inlineIcons.has(symbol[1])).map((symbol) => symbol[0]).join("")
      + "</defs></svg>";
    await writeFile(join(destinationPath, "src/octicons.svg"), OCTICON_SPRITE);
    const bundleOptions = {
      absWorkingDir: destinationPath,
      nodePaths: [
        fileURLToPath(new URL("node_modules/", siteRoot)),
        fileURLToPath(new URL("../../node_modules/", siteRoot)),
      ],
      bundle: true,
      entryNames: "[name]",
      chunkNames: "chunk-[name]-[hash]",
      format: "esm",
      legalComments: "none",
      minify: true,
      outdir: bundlePath,
      platform: "browser",
      loader: { ".css": "text" },
      sourcemap: true,
      target: "es2022",
    };
    const [result] = await Promise.all([
      build({
        ...bundleOptions,
        entryPoints: { main: "src/main.js" },
        metafile: true,
        splitting: true,
        plugins: [{
          name: "linked-dashboard-styles",
          setup(builder) {
            builder.onLoad({ filter: /[/\\]src[/\\]styles\.js$/ }, () => ({
              contents: 'export function primerStylesheet() { return ""; } export function notificationStylesheet() { return ""; } export const getPrimerStyles = primerStylesheet;',
              loader: "js",
            }));
            builder.onLoad({ filter: /[/\\]src[/\\]octicon-sprite\.js$/ }, () => ({
              contents: `export const OCTICON_SPRITE = ${JSON.stringify(inlineSprite)};`,
              loader: "js",
            }));
          },
        }],
      }),
      build({ ...bundleOptions, entryPoints: { "data-worker": "src/data-worker.js" } }),
    ]);

    const startupOutput = Object.entries(result.metafile.outputs)
      .find(([, output]) => output.entryPoint === "src/dashboard-app.js")?.[0];
    if (!startupOutput) throw new Error("Dashboard application startup chunk is missing.");
    const collectStaticModules = (outputPath, modules = new Set()) => {
      if (modules.has(outputPath)) return modules;
      const output = result.metafile.outputs[outputPath];
      if (!output) throw new Error(`Dashboard startup dependency is missing: ${outputPath}`);
      modules.add(outputPath);
      for (const dependency of output.imports) {
        if (dependency.kind === "import-statement" && !dependency.external) collectStaticModules(dependency.path, modules);
      }
      return modules;
    };
    const startupModules = collectStaticModules(startupOutput);
    const mainOutput = join(bundlePath, "main.js");
    const mainModules = collectStaticModules(relative(destinationPath, mainOutput));
    const coreModules = [...startupModules].filter((path) => !mainModules.has(path));
    const corePaths = new Set(coreModules.map((path) => resolve(destinationPath, path)));
    const coalescedPath = join(destinationPath, ".coalesced");
    let coreFile;
    try {
      const exports = coreModules.map((path, index) => ({
        path,
        names: result.metafile.outputs[path].exports,
        prefix: `module${index}_`,
      }));
      // Re-export facades keep lazy modules on the same singleton instances.
      const coalesced = await build({
        ...bundleOptions,
        outdir: coalescedPath,
        entryNames: "chunk-dashboard-core-[hash]",
        metafile: true,
        stdin: {
          contents: exports.map(({ path, names, prefix }) => names.length
            ? `export { ${names.map((name) => `${name} as ${prefix}${name}`).join(", ")} } from "./${basename(path)}";`
            : `import "./${basename(path)}";`).join("\n"),
          resolveDir: bundlePath,
          sourcefile: "dashboard-core.js",
        },
        plugins: [{
          name: "preserve-lazy-dashboard-modules",
          setup(builder) {
            builder.onResolve({ filter: /^\.\// }, (args) => {
              const path = resolve(args.resolveDir || dirname(args.importer), args.path);
              if (args.kind === "dynamic-import" || !corePaths.has(path)) {
                return { path: `./${basename(path)}`, external: true };
              }
            });
          },
        }],
      });
      const coreOutput = Object.keys(coalesced.metafile.outputs).find((path) => path.endsWith(".js"));
      if (!coreOutput) throw new Error("Coalesced dashboard startup chunk is missing.");
      coreFile = basename(coreOutput);
      await cp(coalescedPath, bundlePath, { recursive: true });
      for (const { path, names, prefix } of exports) {
        const facade = await transform(names.length
          ? `export { ${names.map((name) => `${prefix}${name} as ${name}`).join(", ")} } from "./${coreFile}";`
          : `import "./${coreFile}";`, {
          loader: "js", minify: true, sourcemap: "external", sourcefile: basename(path),
        });
        await writeFile(resolve(destinationPath, path), `${facade.code}//# sourceMappingURL=${basename(path)}.map\n`);
        await writeFile(`${resolve(destinationPath, path)}.map`, facade.map);
      }
    } finally {
      await rm(coalescedPath, { force: true, recursive: true });
    }
    for (const sourceFile of (await listFiles(join(destinationPath, "src")))
      .filter((file) => file.endsWith(".js"))) {
      await rm(join(destinationPath, "src", sourceFile));
    }
    await cp(bundlePath, join(destinationPath, "src"), { recursive: true });
    const startupChunks = [coreFile, ...new Set([startupOutput, ...mainModules])]
      .filter((path) => !path.endsWith("/main.js"))
      .map((path) => basename(path));
    const preloads = [
      '    <link rel="stylesheet" href="./styles.css">',
      '    <link rel="preload" href="./dashboard.json" as="fetch" crossorigin>',
      '    <link rel="modulepreload" href="./src/main.js" fetchpriority="low">',
      ...startupChunks.map((chunk) => `    <link rel="modulepreload" href="./src/${chunk}" fetchpriority="low">`),
    ];
    const indexPath = join(destinationPath, "index.html");
    const index = await readFile(indexPath, "utf8");
    if (!index.includes("  </head>")) throw new Error("Dashboard document head is missing.");
    await writeFile(indexPath, index.replace("  </head>", `${preloads.join("\n")}\n  </head>`));
  } finally {
    await rm(bundlePath, { force: true, recursive: true });
  }
}

async function cacheBustSiteImports(destinationPath) {
  const siteFiles = await listFiles(destinationPath);
  const siteHash = createHash("sha256");

  for (const siteFile of siteFiles.toSorted()) {
    siteHash.update(siteFile).update("\0");
    siteHash.update(await readFile(join(destinationPath, siteFile))).update("\0");
  }
  const sha = siteHash.digest("hex");

  const indexPath = join(destinationPath, "index.html");
  const serviceWorkerPath = join(destinationPath, "service-worker.js");
  const index = await readFile(indexPath, "utf8");
  const serviceWorker = await readFile(serviceWorkerPath, "utf8");
  await Promise.all([
    writeFile(indexPath, index
      .replace(/(script\.src = ["'])(\.\/src\/main\.js)(["'];)/, `$1$2?sha=${sha}$3`)
      .replace('href="./src/main.js"', `href="./src/main.js?sha=${sha}"`)
      .replace('href="./styles.css"', `href="./styles.css?sha=${sha}"`)),
    writeFile(
      serviceWorkerPath,
      serviceWorker.replace("const VERSION = 'development';", `const VERSION = '${sha}';`),
    ),
  ]);
}

async function listFiles(directory, root = directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await listFiles(path, root));
    else if (entry.isFile()) files.push(relative(root, path));
  }
  return files;
}

function requiresHashQueryParameter(page) {
  const route = page.route;
  return typeof route === "object" && route !== null && typeof route["hash-query-parameter"] === "string";
}

function redirectDocument(pageId) {
  const hash = `#page-${encodeURIComponent(pageId)}`;
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta http-equiv="refresh" content="0; url=../${hash}" />
    <link rel="canonical" href="../" />
    <title>Central Agentic Ops Dashboard</title>
    <script>window.location.replace("../${hash}");</script>
  </head>
  <body>
    <p>Redirecting to the <a href="../${hash}">dashboard</a>.</p>
  </body>
</html>
`;
}

async function main([destination, settingsPath, commitSha, activityDataPath]) {
  const controlSettings = settingsPath ? await loadDashboardControlSettings(settingsPath) : {};
  await buildDashboardSite({
    destination: destination ?? new URL("dist/", siteRoot),
    controlSettings,
    commitSha,
    activityDataPath,
  });
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  await main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}