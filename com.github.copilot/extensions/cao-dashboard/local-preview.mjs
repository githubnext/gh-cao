import { cp, mkdtemp, realpath, rm } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, sep } from "node:path";
import { pathToFileURL } from "node:url";
import { resolveBundledResource } from "./bundled-resources.mjs";

/**
 * @param {{
 *   workingDirectory: string,
 *   repository?: string,
 *   ghExecutable?: string,
 *   postgresURL?: string,
 *   namespace?: string,
 *   generateQuery?: typeof import('./query-designer.mjs').generateDashboardQuery,
 *   enhanceQueryIntent?: typeof import('./query-designer.mjs').enhanceQueryEditorIntent,
 *   startSession?: (prompt: string) => Promise<void>,
 *   approveCliAction: (action: { id: string, command: string, input?: string }) => Promise<boolean>,
 *   executeCliAction: (action: {
 *     id: string, command: string, input?: string, signal?: AbortSignal,
 *     onOutput: (event: { stream: "stdout" | "stderr", data: string }) => void,
 *   }) => Promise<unknown>,
 * }} options
 */
export async function startLocalDashboardPreview({
  workingDirectory,
  repository,
  executeCliAction,
  approveCliAction,
  ghExecutable = "gh",
  postgresURL = process.env.CAO_POSTGRES_URL,
  namespace = process.env.CAO_DATABASE_NAMESPACE,
  generateQuery,
  enhanceQueryIntent,
  startSession,
}) {
  const localServerPath = await resolveBundledResource("canvasServer");
  const localServer = await import(pathToFileURL(localServerPath).href);
  if (typeof localServer.startCanvasServer !== "function") {
    throw new Error(
      "dashboard/canvas-server.mjs does not export startCanvasServer.",
    );
  }

  const workspace = await realpath(workingDirectory);
  const dashboardDirectory = dirname(await realpath(localServerPath));
  const dashboardRelativePath = relative(workspace, dashboardDirectory);
  const isWorkspaceDashboard = dashboardRelativePath === ""
    || (!isAbsolute(dashboardRelativePath)
      && dashboardRelativePath !== ".."
      && !dashboardRelativePath.startsWith(`..${sep}`));
  let stagedDirectory;
  let siteRoot = await resolveBundledResource("site");
  try {
    if (!isWorkspaceDashboard) {
      stagedDirectory = await mkdtemp(join(workspace, ".cao-dashboard-preview-"));
      const stagedSite = join(stagedDirectory, "site");
      const sourceSite = siteRoot;
      await cp(sourceSite, stagedSite, {
        recursive: true,
        filter: (source) => !["node_modules", "dist", "test", "test-results", "scripts", ".tmp"]
          .includes(relative(sourceSite, source).split(sep)[0]),
      });
      siteRoot = stagedSite;
    }
    const preview = await localServer.startCanvasServer({
      workingDirectory: workspace,
      siteRoot,
      catalogRoot: isWorkspaceDashboard ? dirname(dashboardDirectory) : workspace,
      repository,
      ghExecutable,
      postgresURL,
      namespace,
      generateQuery,
      enhanceQueryIntent,
      startSession,
      executeCliAction,
      approveCliAction,
      host: "127.0.0.1",
      port: 0,
      output: (...values) => console.error(...values),
      requestOutput: () => {},
      traceOutput: () => {},
    });
    return {
      url: preview.url,
      async close() {
        await preview.close();
        if (stagedDirectory) {
          await rm(stagedDirectory, { recursive: true, force: true });
        }
      },
    };
  } catch (error) {
    if (stagedDirectory) {
      await rm(stagedDirectory, { recursive: true, force: true });
    }
    throw error;
  }
}
