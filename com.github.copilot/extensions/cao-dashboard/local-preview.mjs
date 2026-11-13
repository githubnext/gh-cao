import { spawn } from "node:child_process";
import { realpath } from "node:fs/promises";
import { createInterface } from "node:readline";

/**
 * @param {{
 *   workingDirectory: string,
 *   repository?: string,
 *   ghExecutable?: string,
 *   startupTimeout?: number,
 * }} options
 */
export async function startLocalDashboardPreview({
  workingDirectory,
  repository,
  ghExecutable = "gh",
  startupTimeout = 360_000,
}) {
  const workspace = await realpath(workingDirectory);
  if (repository !== undefined
      && (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository) || repository.includes(".."))) {
    throw new Error("The dashboard repository must be an exact OWNER/REPO.");
  }
  const child = spawn(ghExecutable, [
    "cao", "dashboard", "--json", "--exit-on-stdin-close",
    "--listen", "127.0.0.1:0",
    ...(repository ? ["--repo", repository] : []),
  ], { cwd: workspace, stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
  // Neither diagnostics nor capability URLs belong on the extension's stdio.
  child.stderr.resume();
  const lines = createInterface({ input: child.stdout });
  let failure;
  let stopped = false;
  let finished = false;
  const exited = new Promise((resolve) => {
    child.once("error", (error) => { failure = error; finished = true; resolve(); });
    child.once("exit", (code) => {
      finished = true;
      failure ??= new Error(`gh cao dashboard exited (${code}). Install or update the gh-cao extension and check gh authentication and Docker/Podman availability.`);
      resolve();
    });
  });
  child.stdin.on("error", (error) => {
    failure = error;
    child.kill("SIGTERM");
  });
  let closing;
  const close = () => {
    closing ??= (async () => {
      stopped = true;
      child.stdin.end();
      const terminate = setTimeout(() => {
        if (!finished) child.kill("SIGTERM");
      }, 10_000);
      const kill = setTimeout(() => {
        if (!finished) child.kill("SIGKILL");
      }, 20_000);
      terminate.unref();
      kill.unref();
      try { await exited; }
      finally {
        clearTimeout(terminate);
        clearTimeout(kill);
        lines.close();
      }
    })();
    return closing;
  };
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), startupTimeout);
  try {
    const readiness = (async () => {
      for await (const line of lines) {
        let ready;
        try { ready = JSON.parse(line); }
        catch { throw new Error("gh cao dashboard returned an invalid readiness message."); }
        const url = new URL(ready.url);
        if (url.protocol !== "http:" || url.hostname !== "127.0.0.1"
            || !url.port || url.pathname !== "/" || url.searchParams.size !== 1
            || !/^[A-Za-z0-9_-]{43}$/.test(url.searchParams.get("access_token") ?? "")
            || url.hash !== "#page-overview" || url.username || url.password) {
          throw new Error("gh cao dashboard returned an invalid loopback URL.");
        }
        const response = await fetch(url, { signal: controller.signal, redirect: "error" });
        await response.body?.cancel();
        if (!response.ok) throw new Error("The local CAO dashboard failed its readiness check.");
        return { url: url.href, close };
      }
      await exited;
      throw failure ?? new Error("gh cao dashboard stopped before startup.");
    })();
    return await Promise.race([
      readiness,
      exited.then(() => { throw failure ?? new Error("gh cao dashboard stopped before startup."); }),
      new Promise((_, reject) => controller.signal.addEventListener("abort", () => {
        reject(new Error("Timed out starting gh cao dashboard. Check gh authentication and Docker/Podman availability."));
      }, { once: true })),
    ]);
  } catch (error) {
    await close();
    throw error;
  } finally {
    clearTimeout(timer);
    if (stopped) controller.abort();
  }
}
