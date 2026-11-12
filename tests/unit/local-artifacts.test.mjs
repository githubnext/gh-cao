import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

const root = new URL("../../", import.meta.url);

test("disposable local artifacts and SQLite sidecars stay ignored", () => {
  const paths = [
    ".copilot/desktop-runs-frame.png",
    ".queue-test-artifacts/queue-example/queue.sqlite",
    ".queue-test-artifacts/queue-example/queue.sqlite.owner.sqlite",
    ".queue-test-artifacts/queue-example/queue.sqlite.owner.sqlite-wal",
    ".review-events/cached-v2-example.json",
    ".review-runs/cached-v2-example.json",
    ".cao-review.sqlite",
    ".cao-review.sqlite-journal",
    ".cao-review.sqlite-wal",
    ".cao-review.sqlite-shm",
    "_diff_presenter.txt",
  ];
  const result = spawnSync("git", ["check-ignore", "--no-index", "--stdin"], {
    cwd: root, encoding: "utf8", input: `${paths.join("\n")}\n`,
  });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(result.stdout.trim().split("\n"), paths);
});

test("local artifact ignores do not hide fixtures, assets, or the Copilot plugin", () => {
  const result = spawnSync("git", ["check-ignore", "--no-index", "--stdin"], {
    cwd: root, encoding: "utf8",
    input: [
      "tests/fixtures/example.sqlite",
      "tests/fixtures/example.sqlite-wal",
      "tests/fixtures/example.png",
      "public/example.png",
      "com.github.copilot/extensions/cao-dashboard/extension.mjs",
      "tests/fixtures/.cao-review.sqlite",
      "tests/fixtures/_diff_presenter.txt",
      "",
    ].join("\n"),
  });
  assert.equal(result.status, 1, result.stderr);
  assert.equal(result.stdout, "");
});
