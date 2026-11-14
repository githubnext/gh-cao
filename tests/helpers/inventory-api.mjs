import assert from "node:assert/strict";
import { generateKeyPairSync, verify } from "node:crypto";
import { createServer } from "node:http";
import { getOctokit } from "@actions/github";

export async function inventoryApi({
  policy,
  repositories = ["acme/control", "acme/private-a", "acme/private-b"],
  failures = {},
  mintFailure,
  revokeFailure,
  outputPermission = "write",
  metadata = {},
  readPermissions = { actions: "read", contents: "read" },
} = {}) {
  const { privateKey, publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const privatePem = privateKey.export({ type: "pkcs8", format: "pem" });
  const calls = [];
  const tokens = new Map([
    ["control-token", new Set(["acme/control"])],
    ["workflow-token", new Set(["acme/control"])],
    ["inventory-pat", new Set(repositories)],
    ["control-pat", new Set(["acme/control"])],
  ]);
  const owners = [...new Set(repositories.map(repository => repository.split("/")[0]))];
  const minted = [];
  const revoked = [];
  const sha = "1".repeat(40);
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, "http://localhost");
    const token = (request.headers.authorization || "").replace(/^(token|bearer) /i, "");
    let jwt;
    if (token.split(".").length === 3) {
      const [header, payload, signature] = token.split(".");
      assert.ok(verify("RSA-SHA256", Buffer.from(`${header}.${payload}`), publicKey, Buffer.from(signature, "base64url")));
      jwt = JSON.parse(Buffer.from(payload, "base64url"));
      assert.ok(jwt.exp > Date.now() / 1000 && jwt.exp - jwt.iat <= 600);
    }
    let body = "";
    for await (const chunk of request) body += chunk;
    const data = body ? JSON.parse(body) : {};
    calls.push({ method: request.method, path: url.pathname, query: Object.fromEntries(url.searchParams), token: jwt ? `app:${jwt.iss}` : token, data });
    const send = (status, value, headers = {}) => {
      response.writeHead(status, { "content-type": "application/json", ...headers });
      response.end(JSON.stringify(value));
    };
    if (url.pathname === "/rate_limit") {
      return send(200, { resources: { core: { limit: 5000, remaining: 4999, reset: 2_000_000_000 } } });
    }
    if (request.method === "DELETE" && url.pathname === "/installation/token" && tokens.has(token)) {
      if (revokeFailure) return send(revokeFailure, { message: "never-disclose cleanup failure" });
      revoked.push(token);
      tokens.delete(token);
      return send(204, null);
    }
    const installation = /^\/repos\/([^/]+\/[^/]+)\/installation$/.exec(url.pathname);
    if (installation && jwt) {
      const repository = installation[1];
      if (!repositories.includes(repository)) return send(404, { message: "never-disclose installation" });
      return send(200, {
        id: owners.indexOf(repository.split("/")[0]) + 1,
        permissions: jwt.iss === "write-app" ? { issues: outputPermission, actions: outputPermission } : { contents: "read", actions: "read" },
      });
    }
    const mint = /^\/app\/installations\/(\d+)\/access_tokens$/.exec(url.pathname);
    if (request.method === "POST" && mint && jwt?.iss === "read-app") {
      assert.ok(Array.isArray(data.repositories) && data.repositories.length > 0 && data.repositories.length <= 500);
      assert.deepEqual(data.permissions, readPermissions);
      const owner = owners[Number(mint[1]) - 1];
      const scope = data.repositories.map(name => `${owner}/${name}`);
      if (mintFailure) return send(mintFailure, { message: "never-disclose mint failure" });
      if (scope.some(repository => !repositories.includes(repository))) return send(422, { message: "never-disclose inaccessible enrollment" });
      const created = `inventory-${minted.length + 1}`;
      tokens.set(created, new Set(scope));
      minted.push({ token: created, repositories: scope, permissions: data.permissions });
      return send(201, { token: created, expires_at: new Date(Date.now() + 3600_000).toISOString() });
    }
    const repo = /^\/repos\/([^/]+\/[^/]+)(.*)$/.exec(url.pathname);
    if (!repo || !tokens.get(token)?.has(repo[1])) return send(404, { message: "never-disclose out-of-token-scope" });
    const repository = repo[1];
    const failure = failures[repository];
    if (failure && !repo[2]) return send(failure.status, { message: "never-disclose response body" }, failure.headers);
    if (repo[2] === "/contents/.github/workflows/cao.json") {
      if (url.searchParams.get("ref") !== sha) return send(404, {});
      return send(200, { content: Buffer.from(JSON.stringify(policy)).toString("base64") });
    }
    if (repo[2].startsWith("/contents/")) {
      assert.equal(repository, "acme/control");
      assert.equal(url.searchParams.get("ref"), sha);
      return send(200, { content: Buffer.from("---\nsafe-outputs:\n  dispatch-workflow:\n    workflows: [dependabot-update-planner]\n---\n").toString("base64") });
    }
    if (repo[2] === "/actions/workflows") return send(200, { workflows: [{
      id: 100, name: "Planner", path: ".github/workflows/dependabot-update-planner.lock.yml", state: "active",
    }] });
    if (repo[2] === "/branches") return send(200, []);
    if (repo[2] === "/collaborators/developer/permission") return send(200, {
      permission: "write", user: { login: "developer", type: "User" },
    });
    if (!repo[2]) return send(200, {
      id: repositories.indexOf(repository) + 1,
      full_name: repository, archived: false, disabled: false, private: true,
      pushed_at: "2026-01-01T00:00:00Z", default_branch: "main",
      ...metadata[repository],
    });
    send(404, { message: "never-disclose unexpected request" });
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const baseUrl = `http://127.0.0.1:${server.address().port}`;
  return {
    sha, calls, minted, revoked, privatePem,
    getOctokit: (token, options = {}) => getOctokit(token, { ...options, baseUrl }),
    close: () => new Promise((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve());
      server.closeAllConnections();
    }),
  };
}
