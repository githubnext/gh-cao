import { execFile, spawn } from "node:child_process";
import { mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { hashFileContents } from "../../activity/cli/files.mjs";
import { loadDashboardControlSettings } from "../../dashboard/site/scripts/build.mjs";
import { prepareDashboardPages } from "../../scripts/simulate-dashboard-pages.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const execute = promisify(execFile);

export async function startDashboardPagesPreview({ downloadData, basePath = "/gh-cao/", fixture = false, fixtureExperimental = false } = {}) {
  const scratch = join(root, ".tmp");
  await mkdir(scratch, { recursive: true });
  const directory = await mkdtemp(join(scratch, "pages-preview-"));
  const site = join(directory, "site");
  const executable = join(directory, process.platform === "win32" ? "cao-dashboard.exe" : "cao-dashboard");
  let child;
  let exit;
  let lines;
  let closed = false;
  const close = async () => {
    if (closed) return;
    closed = true;
    lines?.close();
    if (child && child.exitCode === null && child.signalCode === null) {
      child.kill("SIGTERM");
      const deadline = setTimeout(() => child.kill("SIGKILL"), 10_000);
      try { await exit; } finally { clearTimeout(deadline); }
    }
    await rm(directory, { recursive: true, force: true });
  };
  try {
    if (downloadData) {
      if (fixture) throw new Error("Choose downloaded evidence or an explicit fixture, not both.");
      const activityDir = join(directory, "activity");
      await downloadData(activityDir);
      const settings = await loadDashboardControlSettings(join(root, ".github/workflows/cao.json"));
      await writeFile(join(activityDir, "control-settings.json"), JSON.stringify(settings));
      const hashes = {};
      for (const phase of ["runs", "records"]) {
        const phaseDirectory = `gh-aw-logs-${phase}`;
        for (const name of await readdir(join(activityDir, phaseDirectory))) {
          if (/^[A-Za-z0-9._-]+\.jsonl$/.test(name)) {
            hashes[`${phaseDirectory}/${name}`] = await hashFileContents(join(activityDir, phaseDirectory, name));
          }
        }
      }
      await writeFile(join(activityDir, "payload-hashes.json"), JSON.stringify(hashes));
      await prepareDashboardPages({ activityDir, output: site, includeSqlite: false });
    } else {
      await prepareDashboardPages({ fixture, fixtureExperimental, output: site });
    }
    await execute("go", ["-C", "server", "build", "-o", executable, "./cmd/cao-dashboard"], {
      cwd: root, timeout: 180_000, maxBuffer: 1024 * 1024,
    });
    child = spawn(executable, ["serve-pages", "--site", resolve(site), "--listen", "127.0.0.1:0", "--base-path", basePath], {
      cwd: root, stdio: ["ignore", "pipe", "pipe"],
    });
    let stderr = "";
    child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr = (stderr + chunk).slice(-16_384); });
    exit = new Promise((accept) => child.once("close", (code, signal) => accept({ code, signal })));
    lines = createInterface({ input: child.stdout });
    const url = await new Promise((accept, reject) => {
      const deadline = setTimeout(() => reject(new Error(`Go Pages preview did not become ready. ${stderr}`)), 30_000);
      const fail = (error) => { clearTimeout(deadline); reject(error); };
      child.once("error", fail);
      void exit.then(({ code, signal }) => fail(new Error(`Go Pages preview exited (${signal ?? code}). ${stderr}`)));
      lines.on("line", (line) => {
        if (!line.startsWith("Pages preview: ")) return;
        clearTimeout(deadline);
        accept(line.slice("Pages preview: ".length).replace(/\/$/, ""));
      });
    });
    const response = await fetch(`${url}/`);
    if (!response.ok) throw new Error(`Go Pages preview readiness failed: HTTP ${response.status}.`);
    return { url, site, close };
  } catch (error) {
    await close();
    throw error;
  }
}
