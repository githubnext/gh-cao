import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

export async function openServerProcess(executable, args, { env = process.env } = {}) {
  const child = spawn(executable, args, { env, stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
  child.stderr.resume();
  const lines = createInterface({ input: child.stdout });
  const pending = [];
  let closed = false;
  let failure;
  const rejectAll = (error) => {
    failure = error;
    for (const waiter of pending.splice(0)) waiter.reject(error);
  };
  child.once("error", () => rejectAll(new Error("Cannot start the PostgreSQL service adapter.")));
  const exited = new Promise((resolve) => child.once("close", (code) => {
    lines.close();
    if (!closed || code !== 0) rejectAll(new Error("The PostgreSQL service adapter stopped."));
    resolve(code);
  }));
  lines.on("line", (line) => {
    const waiter = pending.shift();
    if (!waiter || Buffer.byteLength(line) > 16 << 20) {
      rejectAll(new Error("Invalid PostgreSQL service response."));
      child.kill("SIGTERM");
      return;
    }
    try { waiter.resolve(JSON.parse(line)); }
    catch { waiter.reject(new Error("Invalid PostgreSQL service response.")); }
  });
  const response = () => new Promise((resolve, reject) => {
    if (failure) { reject(failure); return; }
    const timer = setTimeout(() => {
      rejectAll(new Error("PostgreSQL service request timed out."));
      child.kill("SIGTERM");
    }, 30_000);
    pending.push({
      resolve: (value) => { clearTimeout(timer); resolve(value); },
      reject: (error) => { clearTimeout(timer); reject(error); },
    });
  });
  const ready = await response();
  if (ready?.ready !== true) {
    child.kill("SIGTERM");
    throw new Error("PostgreSQL service adapter did not become ready.");
  }
  return {
    async request(request) {
      if (closed) throw new Error("PostgreSQL service adapter is closed.");
      const content = `${JSON.stringify(request)}\n`;
      if (Buffer.byteLength(content) > 1 << 20) throw new Error("PostgreSQL service request is too large.");
      const result = response();
      child.stdin.write(content, (error) => {
        if (error) rejectAll(new Error("PostgreSQL service control channel failed."));
      });
      const value = await result;
      if (typeof value?.error === "string") throw new Error(value.error);
      return value.value;
    },
    async close() {
      if (closed) return exited;
      closed = true;
      child.stdin.end();
      const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
      try {
        if (await exited !== 0) throw new Error("PostgreSQL service adapter did not stop cleanly.");
      } finally { clearTimeout(timer); }
    },
  };
}
