import { createServer } from "node:http";
import { execFile } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import {
  appendFile,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  rename,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { createReadStream, watch } from "node:fs";
import { isIP } from "node:net";
import { createRequire } from "node:module";
import { promisify } from "node:util";
import {
  basename,
  dirname,
  extname,
  isAbsolute,
  join,
  relative,
  resolve,
  sep,
} from "node:path";
import { fileURLToPath } from "node:url";
import { bundleDashboardFiles, loadDashboardSource } from "./report/bundle-dashboards.mjs";
import { buildDashboardPageChunkPath, splitDashboardDocument } from "./site/src/dashboard-chunks.js";
import { assertQueryEditorAuthoring, assertQueryEditorEnhancement, assertQueryEditorIntent, maximumQueryEditorRequestBytes } from "./query-editor-contract.mjs";
import queryEditorDocument from "./site/canvas-query-editor.json" with { type: "json" };
import { InvalidCustomViewDocumentError, loadLocalCustomViews, materializeLocalCustomView, saveLocalCustomView } from "./local-custom-views.mjs";
import { composeDashboardDocuments } from "./report/compose-dashboard-documents.mjs";
import { startCanvasGoHost } from "./canvas-go-host.mjs";
import { createLocalGitHubService } from "./local-github-service.mjs";
import { lookupDialogOptions } from "./dialog-selector.mjs";
import { createCliActionQueue } from "./cli-action-queue.mjs";
import { createCliActionApi } from "./cli-action-api.mjs";
import { cliActionTemplateContext, cliActionTemplateFields, validateCliActionUrl } from "./site/src/cli-action-template.js";
import { createCliActionAdmission } from "./cli-action-quota.mjs";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const executeFile = promisify(execFile);
const defaultCatalogRoot = resolve(scriptDirectory, "..");
const socketEndpoint = "/__dashboard_socket";
const dataArtifactName = "cao-activity-index";
const trustedActivityWorkflowPaths = new Set([
  ".github/workflows/cao-activity.yml",
]);
const contentTypes = new Map([
  [".avif", "image/avif"],
  [".css", "text/css; charset=utf-8"],
  [".gif", "image/gif"],
  [".html", "text/html; charset=utf-8"],
  [".ico", "image/x-icon"],
  [".jpeg", "image/jpeg"],
  [".jpg", "image/jpeg"],
  [".js", "text/javascript; charset=utf-8"],
  [".json", "application/json; charset=utf-8"],
  [".md", "text/markdown; charset=utf-8"],
  [".mjs", "text/javascript; charset=utf-8"],
  [".png", "image/png"],
  [".svg", "image/svg+xml"],
  [".webp", "image/webp"],
]);
const redactedTextExtensions = new Set([
  ".css", ".html", ".js", ".json", ".jsonl", ".md", ".mjs", ".svg", ".txt", ".yaml", ".yml",
]);
function sendJson(response, statusCode, value) {
  response.writeHead(statusCode, {
    "Cache-Control": "no-store",
    "Content-Type": "application/json; charset=utf-8",
  });
  response.end(JSON.stringify(value));
}

async function readJsonRequest(request, maximumBytes = 8192) {
  const tooLarge = () => Object.assign(new Error("Request body is too large."), { code: "REQUEST_TOO_LARGE" });
  if (Number(request.headers["content-length"]) > maximumBytes) throw tooLarge();
  const chunks = [];
  let total = 0;
  for await (const chunk of request) {
    total += chunk.length;
    if (total > maximumBytes) throw tooLarge();
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

async function existingDirectories(paths) {
  const directories = [];
  for (const path of paths) {
    const entry = await stat(path).catch(() => null);
    if (entry?.isDirectory()) directories.push(path);
  }
  return directories;
}

async function canonicalPath(path) {
  const absolutePath = resolve(path);
  try {
    return await realpath(absolutePath);
  } catch (error) {
    if (error?.code !== "ENOENT" || dirname(absolutePath) === absolutePath) throw error;
    return join(await canonicalPath(dirname(absolutePath)), basename(absolutePath));
  }
}

async function campaignDashboardPaths(catalogRoot) {
  if (!catalogRoot) return [];
  const paths = [];
  const catalogEntries = await readdir(catalogRoot, { withFileTypes: true }).catch((error) => {
    if (error?.code === "ENOENT") return [];
    throw error;
  });
  for (const entry of catalogEntries) {
    if (entry.name.startsWith(".")) continue;
    const path = join(catalogRoot, entry.name, "dashboard.json");
    if (!(await stat(path).catch(() => null))?.isFile()) continue;
    const resolvedPath = await realpath(path);
    if (!isWithin(catalogRoot, resolvedPath)) {
      throw new Error("Dashboard server paths must remain within the workspace.");
    }
    paths.push(resolvedPath);
  }
  return paths.toSorted();
}

async function downloadDashboardData(destination, repository, ghExecutable, workingDirectory) {
  const options = { cwd: workingDirectory };
  const repositoryPath = repository || "{owner}/{repo}";
  let defaultBranch;
  let controlRepository;
  let runId;
  try {
    const repositoryResult = await executeFile(ghExecutable, [
      "api",
      `repos/${repositoryPath}`,
      "--jq",
      "[.default_branch, .full_name] | @tsv",
    ], options);
    [defaultBranch, controlRepository] = repositoryResult.stdout.trim().split("\t");
    const result = await executeFile(ghExecutable, [
      "api",
      `repos/${repositoryPath}/actions/artifacts?name=${dataArtifactName}&per_page=100`,
      "--jq",
      "[.artifacts[] | select(.expired == false)] | sort_by(.created_at) | last | .workflow_run.id // empty",
    ], options);
    runId = result.stdout.trim();
  } catch (error) {
    if (error && typeof error === "object" && "code" in error && error.code === "ENOENT") {
      throw new Error("GitHub CLI is required to download dashboard data.");
    }
    throw new Error("Unable to query the dashboard data artifact. Authenticate GitHub CLI with Actions read access.");
  }
  if (!/^[1-9][0-9]*$/.test(runId)) {
    throw new Error(`No current ${dataArtifactName} artifact is available. Run CAO Activity first.`);
  }
  if (!defaultBranch) throw new Error("Unable to determine the repository default branch.");

  let provenance;
  try {
    const result = await executeFile(ghExecutable, [
      "api",
      `repos/${repositoryPath}/actions/runs/${runId}`,
      "--jq",
      "[.conclusion, .head_branch, .path] | @tsv",
    ], options);
    provenance = result.stdout.trim().split("\t");
  } catch {
    throw new Error(`Unable to verify ${dataArtifactName} from workflow run ${runId}.`);
  }
  if (provenance[0] !== "success"
      || provenance[1] !== defaultBranch
      || !trustedActivityWorkflowPaths.has(provenance[2])) {
    throw new Error(`The latest ${dataArtifactName} artifact is not from a successful trusted CAO Activity workflow run.`);
  }

  const arguments_ = ["run", "download", runId, "--name", dataArtifactName, "--dir", destination];
  if (repository) arguments_.push("--repo", repository);
  try {
    await executeFile(ghExecutable, arguments_, options);
  } catch {
    throw new Error(`Unable to download ${dataArtifactName} from workflow run ${runId}.`);
  }
  return { repository: controlRepository };
}

async function validateCanonicalActivityData(root) {
  const entries = await readdir(root, { withFileTypes: true });
  const names = new Set(entries.filter((entry) => entry.isFile()).map((entry) => entry.name));
  if (!names.has("gh-aw-logs.sqlite") || !names.has("payload-hashes.json")) {
    throw new Error("The CAO Activity artifact requires gh-aw-logs.sqlite and payload-hashes.json at its root. Run CAO Activity to republish it.");
  }
  const snapshot = join(root, "gh-aw-logs.sqlite");
  if (!isWithin(await realpath(root), await realpath(snapshot))) {
    throw new Error("The canonical dashboard snapshot must remain inside the downloaded artifact.");
  }
  const hashes = JSON.parse(await readFile(join(root, "payload-hashes.json"), "utf8"));
  if (hashes.synthetic === true) {
    throw new Error("Synthetic Pages demo data cannot be used as an operational Activity snapshot.");
  }
  const expected = hashes["gh-aw-logs.sqlite"];
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(snapshot)) hash.update(chunk);
  if (!/^[a-f0-9]{64}$/.test(expected ?? "") || hash.digest("hex") !== expected) {
    throw new Error("The canonical dashboard SQLite snapshot checksum does not match its manifest.");
  }
  return root;
}

async function sourceSignature(paths) {
  const entries = await Promise.all(paths.map(async (path) => `${path}\0${await readFile(path, "utf8")}`));
  return entries.join("\n");
}

function isLoopbackHost(host) {
  const unbracketed = host.startsWith("[") && host.endsWith("]") ? host.slice(1, -1) : host;
  if (unbracketed === "localhost" || unbracketed === "::1") return true;
  if (isIP(unbracketed) !== 4) return false;
  return unbracketed.split(".")[0] === "127";
}

const secretKeyPattern = /(?:^|[-_])(api[-_]?key|authorization|client[-_]?secret|password|private[-_]?key|secret|token)(?:$|[-_])/i;
const secretValuePatterns = [
  /\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b/g,
  /\bBearer\s+[A-Za-z0-9._~+/-]+=*/gi,
  /\bAKIA[A-Z0-9]{16}\b/g,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g,
];

function redactSecretValues(value) {
  return secretValuePatterns.reduce(
    (redacted, pattern) => redacted.replace(pattern, "[REDACTED]"),
    value,
  );
}

function redactJsonSecrets(source) {
  const redact = (value, key = "") => {
    if (secretKeyPattern.test(key)) return "[REDACTED]";
    if (Array.isArray(value)) return value.map((entry) => redact(entry));
    if (value && typeof value === "object") {
      return Object.fromEntries(Object.entries(value).map(([name, entry]) => [name, redact(entry, name)]));
    }
    if (typeof value !== "string") return value;
    return redactSecretValues(value);
  };
  return JSON.stringify(redact(JSON.parse(source)), null, 2);
}

function browserSafeFileContent(path, content) {
  const extension = extname(path).toLowerCase();
  if (extension === ".json") return redactJsonSecrets(content.toString("utf8"));
  if (redactedTextExtensions.has(extension)) {
    return redactSecretValues(content.toString("utf8"));
  }
  return content;
}

async function writeCanvasAsset(root, path, content) {
  const destination = join(root, path);
  await mkdir(dirname(destination), { recursive: true });
  const staging = `${destination}.tmp`;
  await writeFile(staging, content);
  await rename(staging, destination);
}

async function stageCanvasAssets(source, destination, root = source) {
  await mkdir(destination, { recursive: true });
  for (const entry of await readdir(source, { withFileTypes: true })) {
    if (entry.name.startsWith(".") || ["node_modules", "dist", "test", "test-results", "scripts", "public",
      "sources", "memory", "gh-aw-logs-runs", "gh-aw-logs-records"].includes(entry.name)) continue;
    const input = join(source, entry.name);
    const output = join(destination, entry.name);
    if (entry.isDirectory()) await stageCanvasAssets(input, output, root);
    else if (entry.isFile() && contentTypes.has(extname(entry.name).toLowerCase())
      && !["sources.json", "inventory-sources.json", "payload-hashes.json"].includes(entry.name)) {
      const canonical = await realpath(input);
      if (!isWithin(root, canonical)) throw new Error("Canvas assets must remain inside the bundled site.");
      await writeFile(output, browserSafeFileContent(input, await readFile(input)));
    }
  }
}

function errorMetadata(error) {
  return {
    name: error instanceof Error ? error.name : typeof error,
    code: error && typeof error === "object" && "code" in error
      ? String(error.code)
      : undefined,
  };
}

function createTraceRecorder({ traceFile, output = console.log }) {
  let writes = Promise.resolve();
  return {
    record(source, event, { traceId, details = {} } = {}) {
      const entry = {
        timestamp: new Date().toISOString(),
        source,
        event,
        ...(traceId ? { traceId } : {}),
        details: JSON.parse(redactJsonSecrets(JSON.stringify(details))),
      };
      const line = JSON.stringify(entry);
      output(`[dashboard-trace] ${line}`);
      if (traceFile) {
        writes = writes.then(() => appendFile(traceFile, `${line}\n`, "utf8"));
      }
      return entry;
    },
    async flush() {
      await writes;
    },
  };
}

function isWithin(root, candidate) {
  const path = relative(root, candidate);
  return path === "" || (path !== ".." && !path.startsWith(`..${sep}`) && !isAbsolute(path));
}

function websocketTextFrame(content) {
  const payload = Buffer.from(content);
  let header;
  if (payload.length < 126) {
    header = Buffer.from([0x81, payload.length]);
  } else if (payload.length <= 0xffff) {
    header = Buffer.allocUnsafe(4);
    header[0] = 0x81;
    header[1] = 126;
    header.writeUInt16BE(payload.length, 2);
  } else {
    header = Buffer.allocUnsafe(10);
    header[0] = 0x81;
    header[1] = 127;
    header.writeBigUInt64BE(BigInt(payload.length), 2);
  }
  return Buffer.concat([header, payload]);
}

function websocketCloseFrame() {
  return Buffer.from([0x88, 0x00]);
}

function websocketPongFrame(content) {
  const header = Buffer.from([0x8a, content.length]);
  return Buffer.concat([header, content]);
}

function readWebsocketFrames(buffer) {
  const messages = [];
  let offset = 0;
  while (buffer.length - offset >= 2) {
    const first = buffer[offset];
    const second = buffer[offset + 1];
    const final = (first & 0x80) !== 0;
    const opcode = first & 0x0f;
    const masked = (second & 0x80) !== 0;
    let length = second & 0x7f;
    let headerLength = 2;
    if (length === 126) {
      if (buffer.length - offset < 4) break;
      length = buffer.readUInt16BE(offset + 2);
      headerLength = 4;
    } else if (length === 127) {
      if (buffer.length - offset < 10) break;
      const extendedLength = buffer.readBigUInt64BE(offset + 2);
      if (extendedLength > 16_384n) throw new Error("WebSocket message is too large.");
      length = Number(extendedLength);
      headerLength = 10;
    }
    if (!masked || !final || length > 16_384) throw new Error("Unsupported WebSocket frame.");
    if (buffer.length - offset < headerLength + 4 + length) break;
    const maskOffset = offset + headerLength;
    const payloadOffset = maskOffset + 4;
    const payload = Buffer.allocUnsafe(length);
    for (let index = 0; index < length; index += 1) {
      payload[index] = buffer[payloadOffset + index] ^ buffer[maskOffset + (index % 4)];
    }
    messages.push({ opcode, payload });
    offset = payloadOffset + length;
  }
  return { messages, remaining: buffer.subarray(offset) };
}

/**
 * Starts the local dashboard server.
 *
 * @param {{
 *   siteRoot?: string,
 *   catalogRoot?: string | null,
 *   repository?: string,
 *   caoUrl?: string,
 *   ghExecutable?: string,
 *   downloadData?: (destination: string, repository?: string, ghExecutable?: string, workingDirectory?: string) => Promise<void | { repository?: string }>,
 *   generateQuery?: (intent: import('./query-editor-contract.mjs').QueryEditorIntent, options: { signal: AbortSignal }) => Promise<{ document: string }>,
 *   enhanceQueryIntent?: (request: import('./query-editor-contract.mjs').QueryEditorEnhancement, options: { signal: AbortSignal }) => Promise<import('./query-editor-contract.mjs').QueryEditorAuthoring>,
 *   startSession?: (prompt: string) => Promise<void>,
 *   executeCliAction?: (action: { id: string, command: string, input?: string, signal?: AbortSignal, onOutput: (event: { stream: 'stdout'|'stderr', data: string }) => void }) => Promise<unknown>,
 *   approveCliAction?: (action: { id: string, command: string, input?: string }) => Promise<boolean>,
 *   cliQueuePath?: string,
 *   cliQueueConcurrency?: number,
 *   cliQueueTimeoutMs?: number,
 *   cliQueueAcknowledgeRecovery?: boolean,
 *   cliQueueAdmission?: (command: object) => Promise<{ concurrency: number, reason?: string }>,
 *   lookupSelector?: typeof lookupDialogOptions,
 *   traceFile?: string,
 *   traceOutput?: (message: string) => void,
 *   requestOutput?: (message: string) => void,
 *   output?: (...values: unknown[]) => void,
 *   workingDirectory?: string,
 *   host?: string,
 *   port?: number,
 * }} options
 */
export async function startCanvasServer({
  siteRoot = join(scriptDirectory, "site"),
  catalogRoot = defaultCatalogRoot,
  repository,
  caoUrl = process.env.CAO_DASHBOARD_URL,
  ghExecutable = "gh",
  downloadData = downloadDashboardData,
  executeCliAction,
  approveCliAction,
  cliQueuePath,
  cliQueueConcurrency = 4,
  cliQueueTimeoutMs = 15 * 60 * 1000,
  cliQueueAcknowledgeRecovery = false,
  cliQueueAdmission,
  lookupSelector = lookupDialogOptions,
  generateQuery,
  enhanceQueryIntent,
  startSession,
  allowMissingOrigin = false,
  traceFile,
  traceOutput = console.log,
  requestOutput = console.log,
  output = console.log,
  workingDirectory = process.cwd(),
  host = "127.0.0.1",
  port = 0,
} = {}) {
  if (!isLoopbackHost(host)) {
    throw new Error("Canvas development requires a loopback host. Use the Go server for dashboard serving.");
  }
  if (approveCliAction !== undefined && typeof executeCliAction !== "function") {
    throw new Error("Canvas mode requires a CLI action executor.");
  }
  if (executeCliAction !== undefined && typeof approveCliAction !== "function") {
    throw new Error("Canvas mode requires trusted CLI action approval.");
  }
  let terminalBundle;
  if (executeCliAction) {
    const { build } = createRequire(new URL("./site/package.json", import.meta.url))("esbuild");
    const result = await build({
      entryPoints: [join(scriptDirectory, "site/src/components/cli-terminal-runtime.js")],
      bundle: true, write: false, platform: "browser", format: "esm",
      target: "es2023", minify: true, loader: { ".css": "text" },
    });
    terminalBundle = result.outputFiles[0].text;
  }
  let queryEditorValidationBundle;
  if (typeof generateQuery === "function") {
    const { build } = createRequire(new URL("./site/package.json", import.meta.url))("esbuild");
    const result = await build({
      entryPoints: [join(scriptDirectory, "site/src/data/query-editor.js")],
      bundle: true, write: false, platform: "browser", format: "esm",
      target: "es2023", minify: true,
    });
    queryEditorValidationBundle = result.outputFiles[0].text;
  }
  const resolvedWorkingDirectory = await realpath(workingDirectory);
  if (caoUrl !== undefined) caoUrl = validateCliActionUrl(caoUrl);
  const resolvedTraceFile = traceFile ? resolve(resolvedWorkingDirectory, traceFile) : null;
  if (resolvedTraceFile && !isWithin(resolvedWorkingDirectory, resolvedTraceFile)) {
    throw new Error("Dashboard trace file must remain within the workspace.");
  }
  if (resolvedTraceFile) {
    await mkdir(dirname(resolvedTraceFile), { recursive: true });
    await writeFile(resolvedTraceFile, "", "utf8");
  }
  const trace = createTraceRecorder({ traceFile: resolvedTraceFile, output: traceOutput });
  const resolvedSiteRoot = await realpath(siteRoot);
  const resolvedCatalogRoot = catalogRoot ? await canonicalPath(catalogRoot) : null;
  if (!isWithin(resolvedWorkingDirectory, resolvedSiteRoot)
      || (resolvedCatalogRoot && !isWithin(resolvedWorkingDirectory, resolvedCatalogRoot))) {
    output("Dashboard server configuration rejected.", {
      reason: "dashboard path is outside the workspace",
    });
    throw new Error("Dashboard server paths must remain within the workspace.");
  }
  await campaignDashboardPaths(resolvedCatalogRoot);
  const baseDashboardPath = join(resolvedSiteRoot, "dashboard.json");
  const temporaryDirectory = await mkdtemp(join(resolvedWorkingDirectory, ".cao-dashboard-preview-"));
  const bundledDashboardPath = join(temporaryDirectory, "dashboard.json");
  const dashboardDataDirectory = join(temporaryDirectory, "data");
  const servedSite = join(temporaryDirectory, "site");
  let canonicalDataDirectory;
  let goHost;
  const githubService = createLocalGitHubService({
    databasePath: join(temporaryDirectory, "github-resources.sqlite"),
    token: async () => (await executeFile(ghExecutable, ["auth", "token", "--hostname", "github.com"], {
      cwd: resolvedWorkingDirectory, timeout: 10_000, maxBuffer: 16 * 1024, windowsHide: true,
    })).stdout.trim(),
  });
  try {
    const downloaded = await downloadData(dashboardDataDirectory, repository, ghExecutable, resolvedWorkingDirectory);
    repository ??= downloaded?.repository;
    canonicalDataDirectory = await validateCanonicalActivityData(dashboardDataDirectory);
    await stageCanvasAssets(resolvedSiteRoot, servedSite);
    if (terminalBundle) await writeCanvasAsset(servedSite, "src/components/cli-terminal-runtime.js", terminalBundle);
    if (queryEditorValidationBundle) await writeCanvasAsset(servedSite, "src/data/query-editor.js", queryEditorValidationBundle);
  } catch (error) {
    await githubService.close();
    await rm(temporaryDirectory, { recursive: true, force: true });
    throw error;
  }
  const sockets = new Set();
  const capability = randomBytes(24).toString("hex");
  const routePrefix = `/${capability}`;
  const socketPath = `${routePrefix}${socketEndpoint}`;
  const watchers = new Map();
  let dashboardContent = "";
  let cliTemplateContext = {};
  /** @type {Map<string, string>} */
  let dashboardPageChunkContent = new Map();
  let signature = "";
  let refreshTimer;
  let refreshPromise = Promise.resolve();
  let refreshRetryCount = 0;
  let closed = false;
  let cliQueue;
  let sessionStarting = false;
  let cliAdmission;
  /** @type {AbortController | null} */
  let queryGeneration = null;
  /** @type {Promise<{ document: string } | import('./query-editor-contract.mjs').QueryEditorAuthoring> | null} */
  let queryGenerationPending = null;
  /** @type {Promise<{ id: string, path: string }> | null} */
  let customViewSavePending = null;

  const broadcastDashboard = (traceId) => {
    output("Broadcasting dashboard preview update.", { socketCount: sockets.size });
    trace.record("server", "preview.broadcast", {
      traceId,
      details: { socketCount: sockets.size },
    });
    const message = traceId
      ? JSON.stringify({ type: "dashboard-update", traceId, dashboard: JSON.parse(dashboardContent) })
      : dashboardContent;
    const frame = websocketTextFrame(message);
    for (const socket of sockets) socket.write(frame);
  };

  const rebuild = async (notify = true, traceId, forceNotify = false) => {
    output("Checking dashboard sources for updates.");
    const campaignPaths = await campaignDashboardPaths(resolvedCatalogRoot);
    const dashboardSources = await Promise.all(
      [baseDashboardPath, ...campaignPaths].map((source) => loadDashboardSource(source)),
    );
    const localViews = await loadLocalCustomViews(resolvedWorkingDirectory);
    const editableDashboardPaths = [...dashboardSources.flatMap(({ sourcePaths }) => sourcePaths), ...localViews.map((view) => view.path)];
    const nextSignature = await sourceSignature(editableDashboardPaths);
    if (nextSignature === signature) {
      if (notify && forceNotify) broadcastDashboard(traceId);
      return editableDashboardPaths;
    }

    await bundleDashboardFiles(bundledDashboardPath, campaignPaths, baseDashboardPath);
    const additions = localViews.map(({ id, document }) => materializeLocalCustomView(document, id));
    let dashboardDocument = composeDashboardDocuments(JSON.parse(await readFile(bundledDashboardPath, "utf8")), additions);
    for (const addition of additions) {
      if (addition.dashboard["card-templates"]?.length) {
        (dashboardDocument.dashboard["card-templates"] ??= []).push(...addition.dashboard["card-templates"]);
      }
      if (addition.dashboard.units) Object.assign(dashboardDocument.dashboard.units ??= {}, addition.dashboard.units);
    }
    if (repository) dashboardDocument.dashboard.repository = repository;
    const needsCaoUrl = (dashboardDocument.dashboard["cli-actions"] ?? []).some((action) =>
      cliActionTemplateFields(action.command, { includeContext: true }).includes("cao-url"));
    if (needsCaoUrl && caoUrl === undefined) {
      output("CAO command backlinks are unavailable. Set --cao-url or CAO_DASHBOARD_URL to the operational dashboard URL.");
    }
    cliTemplateContext = cliActionTemplateContext({
      repository: dashboardDocument.dashboard.repository,
      githubUrlBase: dashboardDocument.dashboard["github-url-base"],
      caoUrl,
    });
    if (typeof generateQuery === "function") {
      if (dashboardDocument.dashboard.pages.some((page) => page.id === "query-editor")) {
        throw new Error("The query-editor page ID is reserved for canvas authoring.");
      }
      dashboardDocument = composeDashboardDocuments(dashboardDocument, [queryEditorDocument]);
    }
    const splitDashboard = splitDashboardDocument({
      languageVersion: dashboardDocument["language-version"],
      dashboard: dashboardDocument.dashboard,
    });
    dashboardContent = redactJsonSecrets(JSON.stringify(splitDashboard.core));
    dashboardPageChunkContent = new Map(
      [...splitDashboard.pageChunks.entries()].map(([pageId, chunk]) => [
        `/${buildDashboardPageChunkPath(pageId)}`,
        redactJsonSecrets(JSON.stringify(chunk)),
      ]),
    );
    await Promise.all([...dashboardPageChunkContent].map(([path, content]) =>
      writeCanvasAsset(servedSite, path.slice(1), content)));
    await writeCanvasAsset(servedSite, "dashboard.json", dashboardContent);
    signature = nextSignature;
    output("Dashboard preview rebuilt.", {
      bundledDashboardPath,
      editableDashboardPaths,
      notify,
    });
    trace.record("server", "preview.rebuilt", {
      traceId,
      details: {
        bundledDashboardPath,
        editableDashboardPaths,
        notify,
      },
    });
    if (notify) broadcastDashboard(traceId);
    return editableDashboardPaths;
  };

  const refreshWatchers = async (dashboardPaths) => {
    if (closed) return;
    const candidates = new Set([
      resolvedSiteRoot,
      ...dashboardPaths.map(dirname),
    ]);
    if (resolvedCatalogRoot) candidates.add(resolvedCatalogRoot);
    for (const directory of await existingDirectories(candidates)) {
      if (watchers.has(directory)) continue;
      const watcher = watch(directory, () => scheduleRefresh());
      watcher.on("error", (error) => {
        output(`Dashboard watcher failed for ${directory}: ${error.message}`);
        watcher.close();
        watchers.delete(directory);
      });
      watchers.set(directory, watcher);
      output("Watching dashboard source directory.", { directory });
    }
  };

  const refresh = async () => {
    if (closed) return;
    try {
      const campaignPaths = await rebuild();
      await refreshWatchers(campaignPaths);
      refreshRetryCount = 0;
    } catch (error) {
      output(`Dashboard update failed: ${error instanceof Error ? error.message : String(error)}`);
      if (refreshRetryCount < 4) {
        refreshRetryCount += 1;
        clearTimeout(refreshTimer);
        refreshTimer = setTimeout(scheduleRefresh, refreshRetryCount * 100);
      }
    }
  };

  function scheduleRefresh() {
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshPromise = refreshPromise.then(refresh, refresh);
    }, 75);
  }

  try {
    const initialCampaignPaths = await rebuild(false);
    await refreshWatchers(initialCampaignPaths);
  } catch (error) {
    for (const watcher of watchers.values()) watcher.close();
    await goHost?.close();
    await githubService.close();
    await rm(temporaryDirectory, { recursive: true, force: true });
    throw error;
  }

  let expectedAuthority = null;
  let publicAuthority = null;
  let boundHost;
  const isAllowedHost = (host) => host === expectedAuthority;
  const isAllowedOrigin = (origin) => {
    if (typeof origin !== "string") return false;
    try {
      const parsed = new URL(origin);
      return (parsed.protocol === "http:" || parsed.protocol === "https:")
        && parsed.host === publicAuthority;
    } catch {
      return false;
    }
  };
  try {
    if (executeCliAction) {
      const queuePath = await canonicalPath(cliQueuePath ?? join(resolvedWorkingDirectory, ".cao", "cli-actions.sqlite"));
      if (!isWithin(resolvedWorkingDirectory, queuePath)) {
        throw new Error("CLI queue database must remain within the workspace.");
      }
      cliAdmission = cliQueueAdmission ? null : createCliActionAdmission({
        workingDirectory: resolvedWorkingDirectory, ghExecutable, maxConcurrency: cliQueueConcurrency,
      });
      cliQueue = await createCliActionQueue({
        filename: queuePath,
        maxConcurrency: cliQueueConcurrency,
        timeoutMs: cliQueueTimeoutMs,
        acknowledgeRecovery: cliQueueAcknowledgeRecovery,
        admission: cliQueueAdmission ?? cliAdmission.admission,
        execute: ({ actionId, command, input, signal, onOutput }) => executeCliAction({
          id: actionId, command, ...(input === undefined ? {} : { input }), signal, onOutput,
        }),
      });
    }
  } catch (error) {
    await cliAdmission?.close();
    for (const watcher of watchers.values()) watcher.close();
    await goHost?.close();
    await githubService.close();
    await rm(temporaryDirectory, { recursive: true, force: true });
    throw error;
  }
  const handleCliAction = createCliActionApi({
    queue: cliQueue, getDashboard: () => JSON.parse(dashboardContent),
    getTemplateContext: () => cliTemplateContext,
    approve: approveCliAction,
    isAllowedOrigin, readJsonRequest, sendJson,
  });
  const server = createServer(async (request, response) => {
    const requestStarted = performance.now();
    let requestPath = "<outside-preview>";
    response.once("finish", () => {
      const duration = Math.max(0, Math.round(performance.now() - requestStarted));
      requestOutput(`${request.method || "GET"} ${requestPath} ${response.statusCode} ${duration}ms`);
    });
    try {
      if (!expectedAuthority || !request.headers.host || !isAllowedHost(request.headers.host)
          || /^[A-Za-z][A-Za-z0-9+.-]*:\/\//.test(request.url || "")) {
        response.writeHead(400).end("Bad request\n");
        return;
      }
      const url = new URL(request.url || "/", "http://localhost");
      if (url.pathname !== routePrefix && !url.pathname.startsWith(`${routePrefix}/`)) {
        response.writeHead(404).end("Not found\n");
        return;
      }
      requestPath = `${url.pathname.slice(routePrefix.length) || "/"}${url.search}`;
      let pathname;
      try {
        pathname = decodeURIComponent(url.pathname.slice(routePrefix.length) || "/");
      } catch {
        response.writeHead(400).end("Bad request\n");
        return;
      }
      if (pathname === "/__copilot_session") {
        if (typeof startSession !== "function") { response.writeHead(404).end("Not found\n"); return; }
        if (request.method !== "POST") { response.writeHead(405, { Allow: "POST" }).end(); return; }
        if (!isAllowedOrigin(request.headers.origin)
            || !String(request.headers["content-type"] ?? "").toLowerCase().startsWith("application/json")) {
          response.writeHead(403).end("Forbidden\n");
          return;
        }
        let body;
        try {
          body = await readJsonRequest(request, 600_100);
          if (!body || typeof body !== "object" || Array.isArray(body)
              || Object.keys(body).length !== 1 || typeof body.prompt !== "string"
              || !body.prompt.trim() || body.prompt.length > 100_000) {
            throw new Error("Provide a non-empty prompt of at most 100000 characters.");
          }
        } catch (error) {
          sendJson(response, error?.code === "REQUEST_TOO_LARGE" ? 413 : 400, {
            error: error instanceof Error ? error.message : "Invalid session request.",
          });
          return;
        }
        if (sessionStarting) {
          sendJson(response, 409, { error: "A session is already starting. Wait before retrying." });
          return;
        }
        sessionStarting = true;
        try {
          await startSession(body.prompt);
          sendJson(response, 200, { ok: true });
        } catch (error) {
          output("Copilot session start failed.", { errorName: error instanceof Error ? error.name : "unknown" });
          sendJson(response, 503, { error: "Could not start the Copilot session. Check the app session for details." });
        } finally {
          sessionStarting = false;
        }
        return;
      }
      if (pathname === "/__query_designer" || pathname === "/__query_designer/enhance") {
        const enhance = pathname.endsWith("/enhance");
        if (enhance ? typeof enhanceQueryIntent !== "function" : typeof generateQuery !== "function") {
          response.writeHead(404).end("Not found\n");
          return;
        }
        if (request.method !== "POST") {
          response.writeHead(405, { Allow: "POST" }).end();
          return;
        }
        if (!isAllowedOrigin(request.headers.origin)
            || !String(request.headers["content-type"] ?? "").toLowerCase().startsWith("application/json")) {
          response.writeHead(403).end("Forbidden\n");
          return;
        }
        let intent;
        try {
          intent = await readJsonRequest(request, maximumQueryEditorRequestBytes);
          if (enhance) assertQueryEditorEnhancement(intent);
          else assertQueryEditorIntent(intent);
        } catch (error) {
          sendJson(response, error?.code === "REQUEST_TOO_LARGE" ? 413 : 400, {
            error: error instanceof Error ? error.message : "Invalid query designer request.",
          });
          return;
        }
        if (queryGeneration) {
          sendJson(response, 409, { error: "Another query is being generated. Cancel it or wait before retrying." });
          return;
        }
        const controller = new AbortController();
        queryGeneration = controller;
        const cancel = () => controller.abort();
        response.once("close", cancel);
        const timeout = setTimeout(cancel, 120000);
        try {
          queryGenerationPending = enhance
            ? enhanceQueryIntent(intent, { signal: controller.signal })
            : generateQuery(intent, { signal: controller.signal });
          const result = await queryGenerationPending;
          controller.signal.throwIfAborted();
          if (enhance) assertQueryEditorAuthoring(result);
          if (!response.destroyed && !closed) sendJson(response, 200, result);
        } catch (error) {
          output("Query designer request failed.", { errorName: error instanceof Error ? error.name : "unknown" });
          if (!response.destroyed && !closed) {
            sendJson(response, controller.signal.aborted ? 408 : 503, {
              error: controller.signal.aborted ? "Query generation timed out or was cancelled."
                : "Query generation failed. Check Copilot authentication and try again.",
            });
          }
        } finally {
          clearTimeout(timeout);
          response.removeListener("close", cancel);
          queryGeneration = null;
          queryGenerationPending = null;
        }
        return;
      }
      if (pathname === "/__custom_views") {
        if (typeof generateQuery !== "function") {
          response.writeHead(404).end("Not found\n");
          return;
        }
        if (request.method !== "POST") {
          response.writeHead(405, { Allow: "POST" }).end();
          return;
        }
        if (!isAllowedOrigin(request.headers.origin)
            || !String(request.headers["content-type"] ?? "").toLowerCase().startsWith("application/json")) {
          response.writeHead(403).end("Forbidden\n");
          return;
        }
        let draft;
        try {
          draft = await readJsonRequest(request, maximumQueryEditorRequestBytes);
          if (!draft || typeof draft !== "object" || Array.isArray(draft)
              || Object.keys(draft).length !== 1 || typeof draft.document !== "string") {
            throw new Error("Save a complete Dashboard Language document; paths and identifiers are assigned by CAO.");
          }
        } catch (error) {
          sendJson(response, error?.code === "REQUEST_TOO_LARGE" ? 413 : 400, { error: error instanceof Error ? error.message : "Invalid custom view request." });
          return;
        }
        if (customViewSavePending) {
          sendJson(response, 409, { error: "Another custom view is being saved. Wait before retrying." });
          return;
        }
        try {
          customViewSavePending = (async () => {
            const saved = await saveLocalCustomView(resolvedWorkingDirectory, draft.document);
            const paths = await rebuild(false);
            await refreshWatchers(paths);
            return saved;
          })();
          const saved = await customViewSavePending;
          if (!response.destroyed && !closed) sendJson(response, 201, { ...saved, dashboard: JSON.parse(dashboardContent) });
        } catch (error) {
          output("Custom view save failed.", { errorName: error instanceof Error ? error.name : "unknown" });
          if (!response.destroyed && !closed) {
            sendJson(response, error instanceof InvalidCustomViewDocumentError ? 400 : 503, {
              error: error instanceof InvalidCustomViewDocumentError ? error.message
                : "Could not save or load the custom view. Check workspace storage and try again.",
            });
          }
        } finally {
          customViewSavePending = null;
        }
        return;
      }
      if (pathname.startsWith("/api/v1/github/")) {
        if (request.method !== "GET") {
          response.writeHead(405, { Allow: "GET" }).end();
          return;
        }
        if ((request.headers.origin && !isAllowedOrigin(request.headers.origin))
            || request.headers["sec-fetch-site"] === "cross-site") {
          sendJson(response, 403, { error: "Forbidden." });
          return;
        }
        const result = await githubService.request(pathname, request.headers["if-none-match"]);
        for (const [name, value] of Object.entries(result.headers)) response.setHeader(name, value);
        if (result.status === 304) response.writeHead(304).end();
        else {
          response.writeHead(result.status, { "Content-Type": "application/json; charset=utf-8" });
          response.end(JSON.stringify(result.body));
        }
        return;
      }
      if (pathname === "/__dialog_selector") {
        if (request.method !== "POST") { response.writeHead(405, { Allow: "POST" }).end(); return; }
        if (!isAllowedOrigin(request.headers.origin)
          || !String(request.headers["content-type"] ?? "").toLowerCase().startsWith("application/json")) {
          response.writeHead(403).end("Forbidden\n"); return;
        }
        let payload;
        try { payload = await readJsonRequest(request, 4096); }
        catch { sendJson(response, 400, { error: "Invalid selector request." }); return; }
        if (!payload || typeof payload !== "object" || Array.isArray(payload)
          || Object.keys(payload).some((key) => !["id", "text"].includes(key))
          || typeof payload.id !== "string" || typeof payload.text !== "string"
          || payload.text.trim().length < 2 || payload.text.length > 200 || /[\u0000-\u001f]/.test(payload.text)) {
          sendJson(response, 400, { error: "Invalid selector request." }); return;
        }
        const action = JSON.parse(dashboardContent).dashboard?.["cli-actions"]?.find((candidate) => candidate.id === payload.id);
        if (!action?.selector || action["copy-only"] === true) {
          sendJson(response, 404, { error: "Dialog selector is not declared by this dashboard." }); return;
        }
        const lifetime = new AbortController();
        response.once("close", () => lifetime.abort());
        try {
          const options = await lookupSelector(action, payload.text, {
            workingDirectory: resolvedWorkingDirectory, ghExecutable, signal: lifetime.signal,
          });
          if (!response.destroyed) sendJson(response, 200, { options });
        } catch (error) {
          output("Dialog selector lookup failed.", errorMetadata(error));
          if (!response.destroyed) sendJson(response, 502, { error: "Repository search failed. Check GitHub CLI authentication and try again." });
        }
        return;
      }
      if (await handleCliAction(request, response, pathname, url)) return;
      response.writeHead(404).end("Not found\n");
    } catch (error) {
      output(`Dashboard request failed: ${error instanceof Error ? error.message : String(error)}`);
      if (!response.headersSent) response.writeHead(500);
      response.end("Internal server error\n");
    }
  });
  server.on("upgrade", (request, socket) => {
    const key = request.headers["sec-websocket-key"];
    if (!expectedAuthority
        || !isAllowedHost(request.headers.host)
        || (!request.headers.origin && !allowMissingOrigin)
        || (request.headers.origin && !isAllowedOrigin(request.headers.origin))
        || request.url !== socketPath
        || request.headers.upgrade?.toLowerCase() !== "websocket"
        || request.headers["sec-websocket-version"] !== "13"
        || typeof key !== "string"
        || Buffer.from(key, "base64").length !== 16) {
      socket.end("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n");
      return;
    }
    const accept = createHash("sha1")
      .update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
      .digest("base64");
    socket.write([
      "HTTP/1.1 101 Switching Protocols",
      "Upgrade: websocket",
      "Connection: Upgrade",
      `Sec-WebSocket-Accept: ${accept}`,
      "\r\n",
    ].join("\r\n"));
    sockets.add(socket);
    output("Dashboard preview socket connected.", { socketCount: sockets.size });
    let incoming = Buffer.alloc(0);
    let removed = false;
    const remove = () => {
      if (removed) return;
      removed = true;
      sockets.delete(socket);
      output("Dashboard preview socket disconnected.", { socketCount: sockets.size });
    };
    socket.on("data", (data) => {
      try {
        incoming = Buffer.concat([incoming, data]);
        const parsed = readWebsocketFrames(incoming);
        incoming = parsed.remaining;
        for (const frame of parsed.messages) {
          if (frame.opcode === 0x8) {
            if (!socket.writableEnded) socket.end(websocketCloseFrame());
          } else if (frame.opcode === 0x9) {
            socket.write(websocketPongFrame(frame.payload));
          } else {
            throw new Error("Unsupported WebSocket opcode.");
          }
        }
      } catch (error) {
        output("Dashboard preview socket command failed.", errorMetadata(error));
        socket.end(websocketCloseFrame());
      }
    });
    socket.on("close", remove);
    socket.on("error", remove);
    socket.resume();
  });

  try {
    await new Promise((accept, reject) => {
      server.once("error", reject);
      server.listen(0, host, () => {
        const address = server.address();
        if (!address || typeof address === "string") {
          reject(new Error("dashboard server did not bind to a TCP port"));
          return;
        }
        expectedAuthority = `${address.address.includes(":") ? `[${address.address}]` : address.address}:${address.port}`;
        boundHost = address.address;
        output("Dashboard server listening.", {
          authority: expectedAuthority,
        });
        trace.record("server", "server.ready", {
          details: {
            authority: expectedAuthority,
            traceFile: resolvedTraceFile,
          },
        });
        accept();
      });
    });
  } catch (error) {
    for (const watcher of watchers.values()) watcher.close();
    await cliQueue?.close();
    await cliAdmission?.close();
    await goHost?.close();
    await githubService.close();
    await rm(temporaryDirectory, { recursive: true, force: true });
    throw error;
  }
  try {
    const indexPath = join(servedSite, "index.html");
    const index = await readFile(indexPath, "utf8");
    const injections = `<meta name="dashboard-github-api-base" content="${routePrefix}/">`
      + (typeof startSession === "function" ? `<meta name="dashboard-copilot-session" content="${routePrefix}/__copilot_session">` : "")
      + (caoUrl === undefined ? "" : `<meta name="cao-url" content="${caoUrl}">`)
      + (cliQueue ? "<script>globalThis.__CLI_QUEUE__ = true; globalThis.__LOCAL_CLI_QUEUE__ = true;</script>" : "");
    const headEnd = index.toLowerCase().indexOf("</head>");
    await writeCanvasAsset(servedSite, "index.html", headEnd < 0 ? injections + index
      : index.slice(0, headEnd) + injections + index.slice(headEnd));
    const config = join(temporaryDirectory, "canvas.json");
    await writeFile(config, JSON.stringify({
      prefix: routePrefix, adapterUrl: `http://${expectedAuthority}${routePrefix}`,
      memoryDirectory: canonicalDataDirectory,
    }), { mode: 0o600 });
    goHost = await startCanvasGoHost({
      directory: temporaryDirectory, site: servedSite,
      sqlite: join(canonicalDataDirectory, "gh-aw-logs.sqlite"), config, host: boundHost, port,
    });
    publicAuthority = new URL(goHost.url).host;
  } catch (error) {
    server.closeAllConnections();
    await new Promise((accept) => server.close(accept));
    for (const watcher of watchers.values()) watcher.close();
    await cliQueue?.close();
    await cliAdmission?.close();
    await githubService.close();
    await rm(temporaryDirectory, { recursive: true, force: true });
    throw error;
  }
  return {
    url: goHost.url,
    async close() {
      if (closed) return;
      closed = true;
      const queueShutdown = cliQueue?.close();
      queryGeneration?.abort();
      await goHost.close();
      output("Stopping dashboard server.");
      clearTimeout(refreshTimer);
      for (const watcher of watchers.values()) watcher.close();
      for (const socket of sockets) socket.end(websocketCloseFrame());
      const shutdown = new Promise((accept, reject) => server.close((error) => error ? reject(error) : accept()));
      server.closeAllConnections();
      await shutdown;
      await queueShutdown;
      await cliAdmission?.close();
      if (queryGenerationPending) await Promise.allSettled([queryGenerationPending]);
      if (customViewSavePending) await Promise.allSettled([customViewSavePending]);
      await refreshPromise;
      await githubService.close();
      await rm(temporaryDirectory, { recursive: true, force: true });
      trace.record("server", "server.stopped");
      await trace.flush();
      output("Dashboard server stopped.");
    },
  };
}
