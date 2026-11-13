import { spawn } from "node:child_process";
import path from "node:path";

const npmCommand = process.platform === "win32" ? process.execPath : "npm";
const npmExecutable = process.env.npm_execpath
  ?? path.join(path.dirname(process.execPath), "node_modules", "npm", "bin", "npm-cli.js");
const npmArguments = (...arguments_) => process.platform === "win32"
  ? [npmExecutable, ...arguments_]
  : arguments_;

const checks = [
  {
    name: "lint",
    command: npmCommand,
    arguments: npmArguments("run", "lint"),
  },
  {
    name: "CAO typecheck",
    command: npmCommand,
    arguments: npmArguments("run", "typecheck:cao"),
  },
  {
    name: "runtime packaging and provenance tests",
    command: process.execPath,
    arguments: [
      "--test",
      "tests/unit/cao-runtime-artifact.test.mjs",
    ],
  },
  {
    name: "streamed installer test",
    command: process.execPath,
    arguments: [
      "--test",
      "--test-name-pattern",
      "^(installer fixture archive contains every tracked runtime source and no generated files|streamed install-gh-cao\\.sh with .* installs the control plane and initializes policy)$",
      "tests/unit/install-script.test.mjs",
    ],
  },
  {
    name: "fresh runtime installation test",
    command: npmCommand,
    arguments: npmArguments("run", "test:runtime-install"),
  },
];

function runCheck(check) {
  console.log(`[runtime validation] Starting ${check.name}`);
  return new Promise((resolve, reject) => {
    const child = spawn(check.command, check.arguments, { stdio: "inherit" });
    child.once("error", (error) => {
      reject(new Error(`${check.name} failed to start: ${error.message}`, { cause: error }));
    });
    child.once("exit", (code, signal) => {
      if (code === 0) {
        console.log(`[runtime validation] Passed ${check.name}`);
        resolve();
      } else {
        reject(new Error(signal
          ? `${check.name} terminated by ${signal}`
          : `${check.name} exited with ${code}`));
      }
    });
  });
}

const results = await Promise.allSettled(checks.map(runCheck));
const failures = results.filter((result) => result.status === "rejected");
if (failures.length > 0) {
  for (const failure of failures) console.error(`[runtime validation] ${failure.reason.message}`);
  process.exitCode = 1;
} else {
  console.log(`[runtime validation] Passed all ${checks.length} checks`);
}
