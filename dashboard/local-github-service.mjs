import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { promisify } from "node:util";
import { DatabaseSync } from "node:sqlite";
import { compilerVersionFromLock } from "./lib/version.mjs";

const executeFile = promisify(execFile);
const freshMs = 300_000;
const staleMs = 86_400_000;
const maxEntries = 128;
const maxBytes = 512 * 1024;
const cacheControl = "private, max-age=300, stale-while-revalidate=86400";
const fields = {
  repository: ["full_name", "description", "html_url", "default_branch", "stargazers_count", "forks_count", "open_issues_count", "archived", "visibility"],
  release: ["tag_name", "name", "html_url", "published_at", "draft", "prerelease"],
  compiler: ["path", "sha", "gh_aw_version"],
};

class GitHubStorageError extends Error {}

function unavailable(status = 503, retry = 60) {
  return {
    status,
    headers: { "Cache-Control": "no-store", "Retry-After": String(Math.max(1, Math.ceil(retry))) },
    body: { error: status === 429 ? "GitHub rate limit reached." : "Live GitHub data is unavailable." },
  };
}

function secondsUntil(value, now) {
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) return seconds;
  const date = Date.parse(value);
  return Number.isFinite(date) ? Math.max(1, (date - now) / 1000) : 60;
}

/** Read-only local-preview adapter. Credentials remain in this server process. */
export function createLocalGitHubService({
  fetchImpl = globalThis.fetch,
  token = async () => (await executeFile("gh", ["auth", "token", "--hostname", "github.com"], {
    timeout: 10_000, maxBuffer: 16 * 1024, windowsHide: true,
  })).stdout.trim(),
  now = Date.now,
  databasePath = ":memory:",
} = {}) {
  const database = new DatabaseSync(databasePath);
  database.exec("PRAGMA busy_timeout=5000");
  database.exec(`CREATE TABLE IF NOT EXISTS github_resources (
    id TEXT PRIMARY KEY, body TEXT NOT NULL, etag TEXT NOT NULL,
    upstream_etag TEXT, checked_at INTEGER NOT NULL
  )`);
  const read = database.prepare("SELECT body, etag, upstream_etag AS upstreamEtag, checked_at AS checkedAt FROM github_resources WHERE id = ?");
  const remove = database.prepare("DELETE FROM github_resources WHERE id = ?");
  const write = database.prepare(`INSERT INTO github_resources VALUES (?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET body=excluded.body, etag=excluded.etag,
    upstream_etag=excluded.upstream_etag, checked_at=excluded.checked_at`);
  function save(key, entry) {
    let started = false;
    try {
      database.exec("BEGIN IMMEDIATE");
      started = true;
      write.run(key, JSON.stringify(entry.body), entry.etag, entry.upstreamEtag, entry.checkedAt);
      database.prepare("DELETE FROM github_resources WHERE checked_at < ?").run(now() - freshMs - staleMs);
      database.prepare(`DELETE FROM github_resources WHERE id IN (
        SELECT id FROM github_resources ORDER BY checked_at DESC, id LIMIT -1 OFFSET ?
      )`).run(maxEntries);
      database.exec("COMMIT");
    } catch (error) {
      if (started) database.exec("ROLLBACK");
      throw new GitHubStorageError("GitHub resource storage failed.", { cause: error });
    }
  }
  function invalidate(key) {
    try {
      remove.run(key);
    } catch (error) {
      throw new GitHubStorageError("GitHub resource invalidation failed.", { cause: error });
    }
  }
  const inFlight = new Map();
  let blockedUntil = 0;
  let credentialsBlockedUntil = 0;
  let tokenPromise;
  let closed = false;

  function cachedResponse(entry, stale = false) {
    return {
      status: 200,
      headers: {
        ETag: entry.etag,
        "Cache-Control": stale ? "private, max-age=0, stale-while-revalidate=86400" : cacheControl,
        Age: String(Math.max(0, Math.floor((now() - entry.checkedAt) / 1000))),
        ...(stale ? { Warning: '110 - "Response is stale"' } : {}),
        ...(stale && blockedUntil > now() ? { "Retry-After": String(Math.max(1, Math.ceil((blockedUntil - now()) / 1000))) } : {}),
      },
      body: entry.body,
    };
  }

  async function refresh(path, kind, credential, key, entry) {
    if (blockedUntil > now()) {
      return entry ? cachedResponse(entry, true) : unavailable(429, (blockedUntil - now()) / 1000);
    }
    try {
      // Caller-controlled resource coordinates may affect the path, never the origin.
      const url = new URL("https://api.github.com");
      url.pathname = path;
      const response = await fetchImpl(url, {
        method: "GET",
        headers: {
          Authorization: ["Bearer", credential].join(" "),
          Accept: "application/vnd.github+json",
          "X-GitHub-Api-Version": "2026-03-10",
          ...(entry?.upstreamEtag ? { "If-None-Match": entry.upstreamEtag } : {}),
        },
        redirect: "error",
        signal: AbortSignal.timeout(15_000),
      });
      if (response.headers.get("x-ratelimit-remaining") === "0") {
        const reset = Number(response.headers.get("x-ratelimit-reset")) * 1000;
        blockedUntil = Math.max(blockedUntil, Number.isFinite(reset) && reset > now() ? reset : now() + 60_000);
      }
      if (response.status === 304 && entry) {
        const updated = { ...entry, checkedAt: now() };
        save(key, updated);
        return cachedResponse(updated);
      }
      if (response.status === 429 || (response.status === 403
          && (response.headers.has("retry-after") || response.headers.get("x-ratelimit-remaining") === "0"))) {
        const delay = secondsUntil(response.headers.get("retry-after"), now()) * 1000;
        blockedUntil = Math.max(blockedUntil, now() + delay);
        return entry ? cachedResponse(entry, true) : unavailable(429, (blockedUntil - now()) / 1000);
      }
      if (!response.ok) {
        if (response.status === 404 || response.status === 401 || response.status === 403) invalidate(key);
        if (response.status === 404) return { status: 404, headers: { "Cache-Control": "no-store" }, body: { error: "GitHub resource not found." } };
        if (response.status === 401 || response.status === 403) blockedUntil = Math.max(blockedUntil, now() + 60_000);
        return entry && response.status >= 500 ? cachedResponse(entry, true) : unavailable();
      }
      if (Number(response.headers.get("content-length")) > maxBytes || !response.body) throw new Error("Invalid GitHub response.");
      const reader = response.body.getReader();
      const chunks = [];
      let size = 0;
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.byteLength;
          if (size > maxBytes) throw new Error("GitHub response exceeds limit.");
          chunks.push(value);
        }
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
      const raw = JSON.parse(Buffer.concat(chunks).toString("utf8"));
      if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new Error("Invalid GitHub resource.");
      let resource = raw;
      if (kind === "compiler") {
        const workflowPath = path.slice(path.indexOf("/contents/") + "/contents/".length);
        if (raw.type !== "file" || raw.path !== workflowPath || raw.encoding !== "base64"
            || typeof raw.content !== "string" || !/^[0-9a-f]{40,64}$/i.test(raw.sha)) {
          throw new Error("Invalid GitHub workflow content.");
        }
        const version = compilerVersionFromLock(Buffer.from(raw.content, "base64").toString("utf8"));
        if (!version) throw new Error("GitHub workflow has no valid compiler metadata.");
        resource = { path: raw.path, sha: raw.sha, gh_aw_version: version };
      }
      const body = Object.fromEntries(fields[kind].map((field) => [field, resource[field] ?? null]));
      for (const [field, value] of Object.entries(body)) {
        if (value === null && ["description", "name", "published_at"].includes(field)) continue;
        const type = ["archived", "draft", "prerelease"].includes(field) ? "boolean"
          : ["stargazers_count", "forks_count", "open_issues_count"].includes(field) ? "number" : "string";
        if (typeof value !== type || (type === "number" && (!Number.isSafeInteger(value) || value < 0))) {
          throw new Error("Invalid GitHub resource field.");
        }
      }
      const encoded = JSON.stringify(body);
      if (Buffer.byteLength(encoded) > 64 * 1024) throw new Error("GitHub resource exceeds cache limit.");
      const updated = {
        body,
        etag: `"${createHash("sha256").update(encoded).digest("hex")}"`,
        upstreamEtag: response.headers.get("etag"),
        checkedAt: now(),
      };
      save(key, updated);
      return cachedResponse(updated);
    } catch (error) {
      if (error instanceof GitHubStorageError) throw error;
      blockedUntil = Math.max(blockedUntil, now() + 30_000);
      return entry ? cachedResponse(entry, true) : unavailable();
    }
  }

  return {
    async close() {
      if (closed) return;
      closed = true;
      await Promise.allSettled(inFlight.values());
      database.close();
    },
    async request(pathname, ifNoneMatch) {
      if (closed) throw new Error("GitHub resource service is closed.");
      const match = /^\/api\/v1\/github\/repos\/([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))\/([A-Za-z0-9_.-]{1,100})(\/releases\/latest|\/contents\/\.github\/workflows\/[A-Za-z0-9_-][A-Za-z0-9_.-]{0,199}\.lock\.yml)?$/.exec(pathname);
      if (!match || [".", ".."].includes(match?.[2])) {
        return { status: 400, headers: { "Cache-Control": "no-store" }, body: { error: "Invalid GitHub resource." } };
      }
      if (credentialsBlockedUntil > now()) return unavailable();
      let credential;
      try {
        tokenPromise ??= Promise.resolve().then(token).finally(() => { tokenPromise = undefined; });
        credential = await tokenPromise;
        if (!credential) throw new Error("No credential.");
      } catch {
        credentialsBlockedUntil = now() + 60_000;
        return unavailable();
      }
      if (closed) throw new Error("GitHub resource service is closed.");
      const path = `/repos/${match[1].toLowerCase()}/${match[2].toLowerCase()}${match[3] ?? ""}`;
      const key = `${createHash("sha256").update(credential).digest("hex")}:${path}`;
      const stored = read.get(key);
      let entry = stored ? { ...stored, body: JSON.parse(stored.body) } : undefined;
      if (entry && now() - entry.checkedAt > freshMs + staleMs) {
        invalidate(key);
        entry = undefined;
      }
      let result;
      if (entry && now() - entry.checkedAt < freshMs) {
        result = cachedResponse(entry);
      } else {
        if (!inFlight.has(key)) {
          if (inFlight.size >= 16) return entry ? cachedResponse(entry, true) : unavailable(429);
          const kind = match[3]?.startsWith("/contents/") ? "compiler" : match[3] ? "release" : "repository";
          const pending = refresh(path, kind, credential, key, entry)
            .finally(() => inFlight.delete(key));
          inFlight.set(key, pending);
        }
        result = await inFlight.get(key);
      }
      if (result.status === 200 && !result.headers.Warning && typeof ifNoneMatch === "string"
          && ifNoneMatch.split(",").some((value) => value.trim() === "*" || value.trim().replace(/^W\//, "") === result.headers.ETag)) {
        return { status: 304, headers: result.headers };
      }
      return result;
    },
  };
}
