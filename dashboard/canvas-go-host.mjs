import { spawn, execFile } from "node:child_process";
import { once } from "node:events";
import { access } from "node:fs/promises";
import { isAbsolute, join } from "node:path";
import { createInterface } from "node:readline";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const executeFile = promisify(execFile);
const catalogRoot = fileURLToPath(new URL("../", import.meta.url));

export async function prepareCanvasGoExecutable(directory) {
  let executable = process.env.CAO_DASHBOARD_EXECUTABLE;
  if (executable) {
    if (!isAbsolute(executable)) {
      throw new Error("CAO_DASHBOARD_EXECUTABLE must be an absolute path.");
    }
    await access(executable);
  } else {
    executable = join(directory, process.platform === "win32" ? "cao-dashboard.exe" : "cao-dashboard");
    try {
      await executeFile("go", ["-C", join(catalogRoot, "server"), "build", "-o", executable, "./cmd/cao-dashboard"], {
        timeout: 180_000, maxBuffer: 1024 * 1024, windowsHide: true,
      });
    } catch {
      throw new Error("Cannot build the canvas Go host. Install Go for the bundled server module, or set CAO_DASHBOARD_EXECUTABLE to a compatible cao-dashboard binary.");
    }
  }
  return executable;
}

export async function startCanvasGoHost({ directory, site, source, config, host, port, executable, postgresURL, namespace }) {
  executable ??= await prepareCanvasGoExecutable(directory);
  postgresURL ??= process.env.CAO_POSTGRES_URL;
  if (!postgresURL) throw new Error("Canvas development requires CAO_POSTGRES_URL.");
  const child = spawn(executable, [
    "serve", "--site", site, "--source", source, "--listen", `${host.includes(":") ? `[${host}]` : host}:${port}`,
    "--canvas-config", config,
    ...(namespace ? ["--redis-namespace", namespace] : []),
    "--database-queries", join(catalogRoot, "dashboard/site/src/data/queries/database.json"),
    "--dashboard-queries", join(catalogRoot, "dashboard/site/src/agent/queries.generated.json"),
  ], { cwd: directory, stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
    env: { ...process.env, CAO_POSTGRES_URL: postgresURL,
      ...(namespace ? { CAO_OPERATIONAL_NAMESPACE: namespace } : {}) } });
  // Never forward child output: it may contain local capability URLs.
  child.stderr.resume();
  let failure;
  let stopped = false;
  child.stdin.on("error", (error) => {
    if (!stopped) {
      failure = new Error("Canvas Go host control channel failed.", { cause: error });
      child.kill("SIGTERM");
    }
  });
  const exited = new Promise((accept) => {
    child.once("error", (error) => { failure = error; accept(); });
    child.once("exit", (code) => {
      if (!stopped && !failure) failure = new Error(`Canvas Go host exited (${code}).`);
      accept();
    });
  });
  const lines = createInterface({ input: child.stdout });
  const close = async () => {
    if (stopped) return exited;
    stopped = true;
    child.stdin.end();
    const deadline = setTimeout(() => child.kill("SIGKILL"), 5_000);
    deadline.unref();
    try { await exited; }
    finally { clearTimeout(deadline); lines.close(); }
  };
  try {
    const timeout = AbortSignal.timeout(30_000);
    const ready = await Promise.race([
      once(lines, "line", { signal: timeout }).then(([line]) => {
        try { return JSON.parse(line); }
        catch { throw new Error("Canvas Go host returned an invalid readiness message."); }
      }),
      exited.then(() => { throw failure ?? new Error("Canvas Go host stopped before startup."); }),
    ]);
    const url = new URL(ready.url);
    const expectedHost = host.includes(":") ? `[${host}]` : host;
    if (url.protocol !== "http:" || url.hostname !== expectedHost
        || !/^\/[a-f0-9]{48}$/.test(url.pathname) || url.search || url.hash || url.username || url.password) {
      throw new Error("Canvas Go host returned an invalid loopback URL.");
    }
    const response = await fetch(`${url}/api/v1/refresh`, {
      method: "POST", headers: { Origin: url.origin }, signal: timeout,
    });
    if (!response.ok) throw new Error("Canvas Go host failed its canonical readiness check.");
    return { url: ready.url, close };
  } catch (error) {
    await close();
    throw error;
  }
}
