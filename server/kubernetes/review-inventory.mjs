import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { parsePolicy } from "../../.github/workflows/shared/policy.mjs";

function validateIdentity(repository, revision) {
  if (!/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/.test(repository) ||
      !/^[a-f0-9]{40}$/.test(revision)) {
    throw new Error("An explicit GitHub repository and immutable commit SHA are required.");
  }
}

export function reviewedBareInventory(raw, repository, revision) {
  validateIdentity(repository, revision);
  const document = parsePolicy(raw);
  const control = document["control-plane"];
  for (const name of ["campaigns", "workflows", "operations"]) {
    const entries = control?.[name];
    if (entries === undefined && name !== "campaigns") continue;
    if (!entries || Array.isArray(entries) || typeof entries !== "object" || Object.keys(entries).length) {
      throw new Error("Bare monitoring requires explicit empty campaigns and no declared workflows or operations.");
    }
  }
  const sources = Object.fromEntries(["campaigns", "repositories", "workflows"].map((source) => [
    source, { source, rows: [] },
  ]));
  sources["configuration-policy"] = {
    source: "configuration-policy",
    rows: [{
      path: ".github/workflows/cao.json", raw, document,
      diagnostics: [{ severity: "valid", title: "Production CAO policy validation passed" }],
    }],
  };
  return `${JSON.stringify({
    version: 1, sourceRepository: repository, sourceSHA: revision, policySHA: revision,
    controlRepository: repository, policyResolution: "available", sources,
  })}\n`;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [repository, revision, output] = process.argv.slice(2);
  if (!output || process.argv.length !== 5) {
    throw new Error("Usage: node server/kubernetes/review-inventory.mjs OWNER/REPO COMMIT_SHA OUTPUT_JSON");
  }
  validateIdentity(repository, revision);
  const raw = execFileSync("gh", [
    "api", "--hostname", "github.com", "-H", "Accept: application/vnd.github.raw+json",
    `repos/${repository}/contents/.github/workflows/cao.json?ref=${revision}`,
  ], { encoding: "utf8", maxBuffer: 16 << 20 });
  const content = reviewedBareInventory(raw, repository, revision);
  writeFileSync(output, content, { flag: "wx", mode: 0o600 });
  console.log(JSON.stringify({ sourceRepository: repository, sourceSHA: revision,
    inventoryDigest: createHash("sha256").update(content).digest("hex") }));
}
