import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, delimiter, join } from "node:path";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { parse } from "yaml";

import { generatedJobs, stepBlock, workflow } from "./workflow-contract.helpers.mjs";

const releaseConfig = parse(/^---\n([\s\S]*?)\n---/.exec(workflow("release.md"))[1]);
const extensionAssets = [
  "gh-cao-darwin-amd64", "gh-cao-darwin-arm64", "gh-cao-linux-amd64", "gh-cao-linux-arm64",
  "gh-cao-windows-amd64.exe", "gh-cao-windows-arm64.exe", "checksums.txt",
];

test("tag and version-named draft exist before runtime assets are attached", () => {
  const source = workflow("release.md");
  const prepareRelease = stepBlock(source, "Generate draft release notes");
  const jobs = generatedJobs(workflow("release.lock.yml"));

  assert.match(prepareRelease, /github\.rest\.git\.createRef/);
  assert.match(prepareRelease, /ref: `refs\/tags\/\$\{releaseTag\}`/);
  assert.match(prepareRelease, /sha: context\.sha/);
  assert.match(prepareRelease, /github\.rest\.repos\.createRelease/);
  assert.match(prepareRelease, /tag_name: releaseTag/);
  assert.match(prepareRelease, /name: releaseTag/);
  assert.match(prepareRelease, /draft: true/);
  assert.match(prepareRelease, /prerelease: false/);
  assert.match(prepareRelease, /make_latest: 'false'/);
  assert.match(prepareRelease, /if \(release\.tag_name !== releaseTag\)/);
  assert.match(prepareRelease, /core\.setOutput\('release_tag', releaseTag\)/);
  assert.ok(
    prepareRelease.indexOf("github.rest.git.createRef") < prepareRelease.indexOf("github.rest.repos.createRelease"),
    "git tag must be created before the draft release",
  );
  assert.ok(jobs.get("attach-runtime-assets")?.needs.includes("prepare-release"));
});

test("release attaches an exact-SHA attested runtime before publishing", () => {
  const source = workflow("release.md");
  const attach = stepBlock(source, "Attach verified runtime assets to draft release");
  const jobs = generatedJobs(workflow("release.lock.yml"));

  assert.match(source, /attach-runtime-assets:[\s\S]*needs: \[prepare-release, build-extension\]/);
  assert.match(source, /permissions:[\s\S]*actions: read[\s\S]*attestations: read[\s\S]*contents: write/);
  assert.match(attach, /cao-runtime\.yml\/runs\?head_sha=\$\{REVISION\}&event=push/);
  assert.match(attach, /conclusion == \\"success\\"/);
  assert.match(attach, /gh run download/);
  assert.match(attach, /sha256sum --check/);
  assert.match(attach, /gh attestation verify/);
  assert.match(attach, /--source-digest "\$\{REVISION\}"/);
  assert.match(attach, /--signer-digest "\$\{REVISION\}"/);
  assert.match(attach, /--deny-self-hosted-runners/);
  assert.match(attach, /\.draft == true[\s\S]*\.prerelease == false/);
  assert.match(attach, /target_commitish == \$revision/);
  assert.match(attach, /gh release upload/);
  assert.match(attach, /length == 11/);
  assert.match(attach, /sha256sum --check --strict checksums\.txt/);
  assert.match(attach, /actual_checksums.*expected_checksums/);
  assert.match(attach, /"\$\{archive\}" "\$\{checksum\}" "\$\{azure_archive\}" "\$\{azure_checksum\}" "\$\{binary_paths\[@\]\}"/);
  assert.match(attach, /binary_paths\+=\("\$\{binary_dir\}\/\$\{asset\}"\)/);
  for (const asset of extensionAssets) {
    assert.ok(attach.includes(`"${asset}"`), `release must require ${asset}`);
  }
  assert.deepEqual(jobs.get("attach-runtime-assets")?.needs, ["build-extension", "prepare-release"]);
  assert.ok(jobs.get("publish-release")?.needs.includes("attach-runtime-assets"));
});

test("release verifies the attested Azure deployment assets before uploading them beside the runtime", () => {
  const attach = stepBlock(workflow("release.md"), "Attach verified runtime assets to draft release");
  const download = attach.indexOf('--name "${azure_name}"');
  const checksum = attach.indexOf('sha256sum --check "${azure_name}.tgz.sha256"');
  const verify = attach.indexOf('gh attestation verify "${azure_archive}"');
  const upload = attach.indexOf("gh release upload");
  assert.match(attach, /azure_name="cao-azure-\$\{REVISION\}"/);
  assert.ok(download > 0 && download < checksum && checksum < verify && verify < upload,
    "Azure assets must be downloaded, checksummed, and attestation-verified before upload");
  const verification = attach.slice(verify, attach.indexOf("\n\n", verify));
  for (const flag of [
    '--repo "${GITHUB_REPOSITORY}"',
    '--signer-workflow "${GITHUB_REPOSITORY}/.github/workflows/cao-runtime.yml"',
    '--source-digest "${REVISION}"',
    '--signer-digest "${REVISION}"',
    "--deny-self-hosted-runners",
  ]) assert.ok(verification.includes(flag), `Azure attestation verification requires ${flag}`);
  assert.match(attach, /\[\[ "\$\(find "\$\{azure_dir\}" -maxdepth 1 -type f \| wc -l\)" -eq 2 \]\]/);
  assert.match(attach, /--arg azure_archive "\$\{azure_name\}\.tgz"/);
  assert.match(attach, /\$azure_archive, \$azure_checksum/);
  assert.match(attach, /echo "azure_archive=\$\{azure_name\}\.tgz"/);
});

test("release builds versioned binaries read-only from the exact authorized source without caches", () => {
  const source = workflow("release.md");
  const jobs = generatedJobs(workflow("release.lock.yml"));
  const build = jobs.get("build-extension");

  assert.deepEqual(build?.needs, ["resolve-version"]);
  assert.match(build.block, /permissions:\n\s+contents: read/);
  assert.doesNotMatch(build.block, /contents: write|GH_TOKEN|cache: true/);
  assert.match(build.block, /ref: \$\{\{ github.sha \}\}/);
  assert.match(build.block, /persist-credentials: false/);
  assert.match(build.block, /go-version-file: go.mod/);
  assert.match(build.block, /cache: false/);
  assert.match(stepBlock(source, "Check extension"), /make lint test/);
  assert.match(stepBlock(source, "Build extension release binaries"), /RELEASE_TAG: \$\{\{ needs.resolve-version.outputs.release_tag \}\}/);
  assert.match(stepBlock(source, "Build extension release binaries"), /bash scripts\/build-release\.sh "\$RELEASE_TAG" "\$RUNNER_TEMP\/release-binaries"/);
  assert.match(stepBlock(source, "Verify embedded release version"), /\/gh-cao-linux-amd64" --version\)" = "gh cao version \$\{RELEASE_TAG\}"/);
  const upload = stepBlock(source, "Upload extension release binaries");
  const download = stepBlock(source, "Download extension release binaries");
  assert.match(upload, /name: release-binaries-\$\{\{ github.run_id \}\}/);
  assert.match(download, /name: release-binaries-\$\{\{ github.run_id \}\}/);
  assert.match(upload, /if-no-files-found: error/);
});

test("release publishes the completed draft under the exact version tag", () => {
  const source = workflow("release.md");
  const publish = stepBlock(source, "Publish verified draft as prerelease");
  const jobs = generatedJobs(workflow("release.lock.yml"));

  assert.match(source, /publish-release:[\s\S]*needs: \[prepare-release, attach-runtime-assets\]/);
  assert.match(source, /publish-release:[\s\S]*permissions:[\s\S]*contents: write/);
  assert.match(publish, /RELEASE_ID: \$\{\{ needs\.prepare-release\.outputs\.release_id \}\}/);
  assert.match(publish, /RELEASE_TAG: \$\{\{ needs\.prepare-release\.outputs\.release_tag \}\}/);
  assert.match(publish, /RUNTIME_ARCHIVE: \$\{\{ needs\.attach-runtime-assets\.outputs\.runtime_archive \}\}/);
  assert.match(publish, /draft\.tag_name !== releaseTag/);
  assert.match(publish, /draft\.target_commitish !== revision/);
  assert.match(publish, /draft\.draft !== true/);
  assert.match(publish, /github\.rest\.git\.getRef/);
  assert.match(publish, /releaseRef\.object\.sha !== revision/);
  assert.match(publish, /listReleaseAssets/);
  for (const asset of extensionAssets) {
    assert.ok(publish.includes(`'${asset}'`), `publication must require ${asset}`);
  }
  assert.match(publish, /github\.rest\.repos\.updateRelease/);
  assert.match(publish, /tag_name: releaseTag/);
  assert.match(publish, /draft: false/);
  assert.match(publish, /prerelease: true/);
  assert.match(publish, /make_latest: 'false'/);
  assert.match(publish, /published\.tag_name !== releaseTag/);
  assert.match(publish, /published\.immutable !== true/);
  assert.match(publish, /releaseByTag\.id !== releaseId/);
  assert.deepEqual(jobs.get("publish-release")?.needs, ["attach-runtime-assets", "prepare-release"]);
  assert.ok(jobs.get("agent")?.needs.includes("publish-release"));
});

test("release context paginates before filtering complete API responses", () => {
  const fetchReleaseContext = stepBlock(workflow("release.md"), "Fetch release context");

  assert.doesNotMatch(fetchReleaseContext, /gh api[^\n]*--slurp/);
  assert.equal(
    fetchReleaseContext.match(/fetch_paginated_json [^\n]*(?:\\\n[^\n]*)?\|\n\s+jq --slurp/g)?.length,
    2,
    "each API request should paginate before local jq slurps its responses",
  );
});

test("paginated release evidence retries incomplete responses without accepting partial evidence", async (t) => {
  const step = releaseConfig.steps.find(({ name }) => name === "Fetch release context");
  const functionSource = step.run.slice(step.run.indexOf("fetch_paginated_json()"), step.run.indexOf("RELEASE_ID="));
  for (const scenario of [
    "complete", "empty array", "retry empty", "retry truncated", "retry partial", "empty", "truncated", "partial", "object",
  ]) {
    await t.test(scenario, (t) => {
      const directory = mkdtempSync(join(tmpdir(), "cao-release-context-"));
      t.after(() => rmSync(directory, { recursive: true, force: true }));
      const bin = join(directory, "bin");
      mkdirSync(bin);
      const countPath = join(directory, "attempts");
      writeFileSync(join(bin, "gh"), `#!/usr/bin/env node
import { existsSync, readFileSync, writeFileSync } from "node:fs";
const count = existsSync(process.env.TEST_COUNT) ? Number(readFileSync(process.env.TEST_COUNT, "utf8")) + 1 : 1;
writeFileSync(process.env.TEST_COUNT, String(count));
const scenario = process.env.TEST_SCENARIO;
if (process.argv.slice(2).join(" ") !== "api --paginate /repos/acme/gh-cao/releases?per_page=100") process.exit(2);
if (scenario.startsWith("retry ") && count > 1) {
  console.log('[{"id":2}]\\n[{"id":3}]');
} else if (scenario.endsWith("empty")) {
  console.error("unexpected end of JSON input");
  process.exit(1);
} else if (scenario.endsWith("truncated")) {
  process.stdout.write('[{"id":1}');
} else if (scenario.endsWith("partial")) {
  console.log('[{"id":1}]');
  process.exit(1);
} else {
  console.log(scenario === "empty array" ? "[]" : scenario === "object" ? "{}" : '[{"id":2}]\\n[{"id":3}]');
}
`, { mode: 0o755 });
      writeFileSync(join(bin, "sleep"), "#!/bin/sh\nexit 0\n", { mode: 0o755 });
      const result = spawnSync("bash", ["-c", `set -euo pipefail
${functionSource.replace("/tmp/gh-aw/agent/release-data/api-pages.json", join(directory, "api-pages.json"))}
fetch_paginated_json '/repos/acme/gh-cao/releases?per_page=100' | jq --slurp 'add'
`], {
        encoding: "utf8",
        timeout: 10_000,
        env: {
          ...process.env, PATH: `${bin}${delimiter}${process.env.PATH}`,
          TEST_COUNT: countPath, TEST_SCENARIO: scenario,
        },
      });
      const succeeds = ["complete", "empty array"].includes(scenario) || scenario.startsWith("retry ");
      assert.equal(result.status === 0, succeeds, result.stderr);
      assert.equal(Number(readFileSync(countPath, "utf8")), scenario.startsWith("retry ") ? 2 : succeeds ? 1 : 3);
      if (succeeds) {
        assert.deepEqual(JSON.parse(result.stdout), scenario === "empty array" ? [] : [{ id: 2 }, { id: 3 }]);
      } else {
        assert.doesNotMatch(result.stdout, /"id"/, "partial responses must not reach consumers");
      }
    });
  }
});

test("release context fetches tags before local git inspection", () => {
  const fetchReleaseContext = stepBlock(workflow("release.md"), "Fetch release context");
  const fetchPreviousTag = fetchReleaseContext.indexOf('fetch_release_tag "$PREVIOUS_TAG"');
  const fetchReleaseTag = fetchReleaseContext.indexOf('fetch_release_tag "$RELEASE_TAG"');
  const verifyPreviousTag = fetchReleaseContext.indexOf('git rev-parse --verify "refs/tags/$PREVIOUS_TAG"');
  const verifyReleaseTag = fetchReleaseContext.indexOf('git rev-parse --verify "refs/tags/$RELEASE_TAG"');

  assert.match(fetchReleaseContext, /fetch --force origin "refs\/tags\/\$tag:refs\/tags\/\$tag"/);
  assert.ok(fetchPreviousTag !== -1, "previous release tag must be fetched when present");
  assert.ok(fetchReleaseTag !== -1, "new draft release tag must be fetched");
  assert.ok(fetchPreviousTag < verifyPreviousTag, "previous tag must be fetched before verification");
  assert.ok(fetchReleaseTag < verifyReleaseTag, "release tag must be fetched before verification");
});

test("agent consumes and verifies the published prerelease identity", () => {
  const source = workflow("release.md");
  const fetchReleaseContext = stepBlock(source, "Fetch release context");

  assert.match(source, /prepare-release:[\s\S]*outputs:[\s\S]*release_tag: \$\{\{ steps\.release\.outputs\.release_tag \}\}/);
  assert.match(fetchReleaseContext, /RELEASE_TAG: \$\{\{ needs\.publish-release\.outputs\.release_tag \}\}/);
  assert.match(fetchReleaseContext, /RUNTIME_ARCHIVE: \$\{\{ needs\.attach-runtime-assets\.outputs\.runtime_archive \}\}/);
  assert.match(fetchReleaseContext, /gh api "\/repos\/\$GITHUB_REPOSITORY\/releases\/\$RELEASE_ID"/);
  assert.match(fetchReleaseContext, /\.tag_name == \$tag[\s\S]*\.draft == false[\s\S]*\.prerelease == true[\s\S]*\.immutable == true/);
  assert.match(fetchReleaseContext, /test "\$RUNTIME_ARCHIVE" = "cao-runtime-\$\{GITHUB_SHA\}\.tgz"/);
  assert.match(source, /- `tag`: `\$\{\{ needs\.publish-release\.outputs\.release_tag \}\}`/);
  assert.doesNotMatch(source, /safeoutputs\/publish_release/);
});

test("release uploads all verified binaries as individual release assets, not only Actions artifacts", async (t) => {
  for (const scenario of [
    "complete", "missing", "empty", "symlink", "corrupt", "unexpected", "bad manifest",
    "azure missing", "azure corrupt", "azure unattested",
  ]) {
    await t.test(scenario, (t) => {
      const directory = mkdtempSync(join(tmpdir(), "cao-release-assets-"));
      t.after(() => rmSync(directory, { recursive: true, force: true }));
      const revision = "a".repeat(40);
      const archive = `cao-runtime-${revision}.tgz`;
      const azureArchive = `cao-azure-${revision}.tgz`;
      const bin = join(directory, "bin");
      const binaries = join(directory, "release-binaries");
      const runtime = join(directory, "release-runtime");
      const azure = join(directory, "release-azure");
      for (const path of [bin, binaries, runtime, azure]) mkdirSync(path);
      const digest = (content) => createHash("sha256").update(content).digest("hex");
      writeFileSync(join(runtime, archive), "runtime archive");
      writeFileSync(join(runtime, `${archive}.sha256`), `${digest("runtime archive")}  ${archive}\n`);
      writeFileSync(join(azure, azureArchive), "azure archive");
      writeFileSync(join(azure, `${azureArchive}.sha256`), `${digest("azure archive")}  ${azureArchive}\n`);
      if (scenario === "azure missing") rmSync(join(azure, azureArchive));
      if (scenario === "azure corrupt") writeFileSync(join(azure, azureArchive), "tampered");
      for (const asset of extensionAssets.slice(0, -1)) writeFileSync(join(binaries, asset), asset);
      writeFileSync(join(binaries, "checksums.txt"), extensionAssets.slice(0, -1)
        .map((asset) => `${digest(asset)}  ${asset}\n`).join(""));
      if (scenario === "missing") rmSync(join(binaries, extensionAssets[0]));
      if (scenario === "empty") writeFileSync(join(binaries, extensionAssets[0]), "");
      if (scenario === "symlink") {
        const target = join(directory, "linked-binary");
        writeFileSync(target, extensionAssets[0]);
        rmSync(join(binaries, extensionAssets[0]));
        symlinkSync(target, join(binaries, extensionAssets[0]));
      }
      if (scenario === "corrupt") writeFileSync(join(binaries, extensionAssets[0]), "corrupt");
      if (scenario === "unexpected") writeFileSync(join(binaries, "extra-file"), "unexpected");
      if (scenario === "bad manifest") {
        writeFileSync(join(binaries, "checksums.txt"), extensionAssets.slice(0, -1)
          .map(() => `${digest(extensionAssets[0])}  ${extensionAssets[0]}\n`).join(""));
      }
      const uploads = join(directory, "uploads.json");
      writeFileSync(join(bin, "gh"), `#!/usr/bin/env node
import { basename } from "node:path";
import { readFileSync, statSync, writeFileSync } from "node:fs";
const args = process.argv.slice(2);
const { REVISION, RELEASE_TAG, TEST_UPLOADS, TEST_REJECT_ATTESTATION } = process.env;
if (args[0] === "api") {
  const endpoint = args[1];
  if (endpoint.includes("/actions/workflows/")) console.log("42");
  else if (endpoint.endsWith("/releases/123/assets?per_page=100")) {
    const paths = JSON.parse(readFileSync(TEST_UPLOADS, "utf8"));
    console.log(JSON.stringify(paths.map(path => ({name: basename(path), state: "uploaded", size: statSync(path).size}))));
  } else if (endpoint.endsWith("/releases/123")) {
    console.log(JSON.stringify({draft: true, prerelease: false, tag_name: RELEASE_TAG, target_commitish: REVISION, assets: []}));
  } else if (endpoint.includes("/git/ref/tags/")) console.log("commit\\t" + REVISION);
  else throw new Error("Unexpected API request: " + endpoint);
} else if (args[0] === "release" && args[1] === "upload") {
  if (args[2] !== RELEASE_TAG || args[3] !== "--repo" || args[4] !== "acme/gh-cao") throw new Error("Unexpected upload");
  writeFileSync(TEST_UPLOADS, JSON.stringify(args.slice(5)));
} else if (args[0] === "attestation" && args[1] === "verify") {
  if (TEST_REJECT_ATTESTATION && basename(args[2]) === TEST_REJECT_ATTESTATION) process.exit(1);
} else if (!(args[0] === "run" && args[1] === "download")) {
  throw new Error("Unexpected gh command: " + args.join(" "));
}
`, { mode: 0o755 });
      const step = releaseConfig.jobs["attach-runtime-assets"].steps.find(({ id }) => id === "attach");
      const result = spawnSync("bash", ["-c", step.run], {
        encoding: "utf8",
        timeout: 10_000,
        env: {
          ...process.env,
          PATH: `${bin}${delimiter}${process.env.PATH}`,
          GH_TOKEN: "",
          GITHUB_REPOSITORY: "acme/gh-cao",
          GITHUB_OUTPUT: join(directory, "outputs"),
          GITHUB_STEP_SUMMARY: join(directory, "summary"),
          RUNNER_TEMP: directory,
          REVISION: revision,
          RELEASE_TAG: "v1.2.3",
          RELEASE_ID: "123",
          TEST_UPLOADS: uploads,
          TEST_REJECT_ATTESTATION: scenario === "azure unattested" ? azureArchive : "",
        },
      });
      if (scenario !== "complete") {
        assert.notEqual(result.status, 0, "invalid binaries must prevent release uploads");
        assert.equal(existsSync(uploads), false);
        return;
      }
      assert.equal(result.status, 0, result.stderr);
      assert.deepEqual(
        JSON.parse(readFileSync(uploads, "utf8")).map((path) => basename(path)).sort(),
        [archive, `${archive}.sha256`, azureArchive, `${azureArchive}.sha256`, ...extensionAssets].sort(),
      );
      assert.equal(
        readFileSync(join(directory, "outputs"), "utf8"),
        `runtime_archive=${archive}\nazure_archive=${azureArchive}\n`,
      );
    });
  }
});

test("publication refuses incomplete or unusable extension release assets", async (t) => {
  const revision = "a".repeat(40);
  const archive = `cao-runtime-${revision}.tgz`;
  const azureArchive = `cao-azure-${revision}.tgz`;
  const expectedAssets = [archive, `${archive}.sha256`, azureArchive, `${azureArchive}.sha256`, ...extensionAssets]
    .map((name) => ({ name, state: "uploaded", size: 123 }));
  const step = releaseConfig.jobs["publish-release"].steps.find(({ id }) => id === "publish");
  const release = {
    id: 123, tag_name: "v1.2.3", name: "v1.2.3", target_commitish: revision,
    draft: true, prerelease: false,
  };
  const ref = { object: { type: "commit", sha: revision } };
  const scenarios = [
    ["complete", expectedAssets],
    ...[azureArchive, `${azureArchive}.sha256`, ...extensionAssets]
      .map((asset) => [`missing ${asset}`, expectedAssets.filter(({ name }) => name !== asset)]),
    ["legacy filenames", expectedAssets.map((asset) => ({ ...asset, name: asset.name.replace(/^gh-cao-/, "") }))],
    ["empty binary", expectedAssets.map((asset) => ({ ...asset, size: asset.name === extensionAssets[0] ? 0 : asset.size }))],
    ["incomplete upload", expectedAssets.map((asset) => ({ ...asset, state: asset.name === extensionAssets[0] ? "new" : asset.state }))],
  ];
  for (const [name, assets] of scenarios) {
    await t.test(name, async () => {
      const updates = [];
      const published = { ...release, draft: false, prerelease: true, immutable: true };
      const execute = () => runInNewContext(`(async () => { ${step.with.script} })()`, {
        process: { env: { RELEASE_ID: "123", RELEASE_TAG: "v1.2.3", RUNTIME_ARCHIVE: archive, AZURE_ARCHIVE: azureArchive, REVISION: revision } },
        context: { repo: { owner: "acme", repo: "gh-cao" } },
        github: {
          paginate: async () => assets,
          rest: {
            repos: {
              getRelease: async () => ({ data: updates.length ? published : release }),
              getReleaseByTag: async () => ({ data: published }),
              listReleaseAssets() {},
              updateRelease: async (update) => { updates.push(update); },
            },
            git: { getRef: async () => ({ data: ref }) },
          },
        },
        core: {
          setOutput() {},
          summary: {
            addHeading() { return this; }, addRaw() { return this; },
            addEOL() { return this; }, addLink() { return this; }, async write() {},
          },
        },
      });
      if (name === "complete") {
        await execute();
        assert.equal(updates.length, 1);
        assert.equal(updates[0].prerelease, true);
        assert.equal(updates[0].make_latest, "false");
      } else {
        await assert.rejects(execute, /does not contain the verified runtime and extension assets/);
        assert.equal(updates.length, 0, "invalid assets must keep the release unpublished");
      }
    });
  }
});
