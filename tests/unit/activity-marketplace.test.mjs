import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import test from "node:test";

import { parsePolicy } from "../../.github/workflows/shared/policy.mjs";
import { parsePackageManifest } from "../../dashboard/report/package-manifest.mjs";

const SHA = "a".repeat(40);

function response(payload, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() {
      return payload;
    },
  };
}

function registry(id, overrides = {}) {
  return {
    id,
    repository: "example/packages",
    ref: "main",
    auth: { type: "none" },
    ...overrides,
  };
}

function registryFetch({
  manifest = "name: Demo\ndescription: Example\nincludes:\n  - demo.md\n",
  readme = "# Demo\n\nExample package readme.\n",
  readmeUnavailable = false,
  fail = false,
  repository = { private: false, visibility: "public", stargazers_count: 12, forks_count: 3 },
} = {}) {
  const calls = [];
  const fetchImpl = async (url, init = {}) => {
    calls.push({ url, init });
    if (fail) return response({}, 503);
    if (url.includes("/commits/")) return response({ sha: SHA });
    if (url.endsWith("/repos/example/packages")) return response(repository);
    if (url.includes("/git/trees/")) {
      return response({
        tree: [
          { type: "blob", path: "demo/aw.yml", sha: "blob" },
          { type: "blob", path: "demo/README.md", sha: "readme", size: readme.length },
        ],
      });
    }
    if (url.endsWith("/git/blobs/readme")) {
      return readmeUnavailable
        ? response({}, 404)
        : response({ encoding: "base64", content: Buffer.from(readme).toString("base64") });
    }
    if (url.includes("/git/blobs/")) {
      return response({ encoding: "base64", content: Buffer.from(manifest).toString("base64") });
    }
    throw new Error(`unexpected URL: ${url}`);
  };
  return { calls, fetchImpl };
}

test("package manifests normalize immutable coordinates and the canonical add command", () => {
  const normalized = parsePackageManifest("name: Demo\nversion: v1\nincludes:\n  - workflow.md\n", {
    registryId: "official",
    registryName: "Official",
    precedence: 0,
    repository: "example/packages",
    path: "demo/aw.yml",
    ref: "main",
    resolvedCommit: SHA,
  });
  assert.equal(normalized.id, "example/packages/demo");
  assert.equal(normalized.source, `example/packages/demo@${SHA}`);
  assert.equal(normalized["add-command"], `gh cao add example/packages/demo@${SHA}`);
  assert.deepEqual(normalized["repository-link"], {
    relation: "repository",
    href: "https://github.com/example/packages",
    label: "Open example/packages on GitHub",
  });
  assert.deepEqual(normalized.contents, ["workflow.md"]);
  assert.equal(normalized.icon, "workflow");
});

test("package manifest icons normalize gh-aw Octicons without changing other values", () => {
  for (const [icon, expected] of [
    ['":shield:"', "shield"],
    ["':meter:'", "meter"],
    ["codescan", "codescan"],
    ["icons/logo.svg", "icons/logo.svg"],
    ['""', "workflow"],
  ]) {
    const normalized = parsePackageManifest(`name: Demo\nicon: ${icon}\n`, {
      registryId: "official",
      registryName: "Official",
      precedence: 0,
      repository: "example/packages",
      path: "demo/aw.yml",
      ref: "main",
      resolvedCommit: SHA,
    });
    assert.equal(normalized.icon, expected, `icon: ${icon}`);
  }
});

test("marketplace repository links use the configured GitHub Enterprise host", () => {
  const normalized = parsePackageManifest("name: Demo\n", {
    registryId: "enterprise",
    registryName: "Enterprise",
    precedence: 0,
    repository: "example/packages",
    apiUrl: "https://github.example/api/v3",
    path: "demo/aw.yml",
    ref: "main",
    resolvedCommit: SHA,
  });
  assert.equal(normalized["repository-link"].href, "https://github.example/example/packages");
});

test("workflow metadata stays within frontmatter and normalizes individual add commands", () => {
  const coordinates = {
    registryId: "agentics", registryName: "Agentics workflows", precedence: 1,
    repository: "githubnext/agentics", path: "workflows/ci-doctor.md",
    format: "workflows", ref: "main", resolvedCommit: SHA,
  };
  const body = "# CI Failure Doctor\n\nInvestigate failed CI.\n\nname: Not metadata\nprivate: true";
  const pkg = parsePackageManifest(`---\ndescription: |\n  Investigates failed runs.\n  Reports remediation.\non:\n  workflow_dispatch:\n---\n${body}`, coordinates);
  assert.equal(pkg.name, "CI Failure Doctor");
  assert.equal(pkg.description, "Investigates failed runs.\nReports remediation.");
  assert.equal(pkg.id, "githubnext/agentics/workflows/ci-doctor.md");
  assert.equal(pkg.source, `githubnext/agentics/workflows/ci-doctor.md@${SHA}`);
  assert.equal(pkg["add-command"], `gh aw add ${pkg.source}`);
  assert.deepEqual(pkg.contents, ["workflows/ci-doctor.md"]);
  assert.equal(pkg.readme, body);
  assert.equal(pkg["readme-path"], "workflows/ci-doctor.md");
  for (const [metadata, expectedName, expectedDescription] of [
    ["name: Explicit name\ndescription: >-\n  Fold this\n  description.\non: push", "Explicit name", "Fold this description."],
    ["description: Simple description\non: push", "ci-doctor", "Simple description"],
  ]) {
    const result = parsePackageManifest(`---\n${metadata}\n---\nPrompt`, coordinates);
    assert.equal(result.name, expectedName);
    assert.equal(result.description, expectedDescription);
  }
  for (const source of ["# Not a workflow", "---\ntools:\n  bash: true\n---\n# Shared", "---\nprivate: true\non: push\n---\n# Private"]) {
    assert.equal(parsePackageManifest(source, coordinates), null);
  }
});













test("policy permits removing the official registry and validates registry auth", () => {
  const policy = parsePolicy(JSON.stringify({
    version: 1,
    "gh-aw-version": "v1.0.0",
    "control-plane": {
      marketplace: {
        registries: [registry("private", { auth: { type: "pat", secret: "REGISTRY_PAT" } })],
      },
    },
  }));
  assert.deepEqual(policy["control-plane"].marketplace.registries.map(({ id }) => id), ["private"]);
  assert.throws(() => parsePolicy(JSON.stringify({
    version: 1,
    "gh-aw-version": "v1.0.0",
    "control-plane": {
      marketplace: {
        registries: [registry("private", { auth: { type: "pat", secret: "literal-token" } })],
      },
    },
  })), /invalid value/);
  assert.throws(() => parsePolicy(JSON.stringify({
    version: 1,
    "gh-aw-version": "v1.0.0",
    "control-plane": {
      marketplace: {
        registries: [registry("private", { "verified-publisher": true })],
      },
    },
  })), /unknown key.*verified-publisher/);
});
