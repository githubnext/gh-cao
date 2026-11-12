import assert from "node:assert/strict";
import test from "node:test";
import { assertGoCoverage } from "../helpers/azure-local-go.mjs";

const packages = ["./internal/one", "./internal/two"];
const patterns = ["^TestFirst$/^postgres$", "^TestSecond$"];
const passes = [
  { Package: "./internal/one", Test: "TestFirst/postgres", Action: "pass" },
  { Package: "./internal/two", Test: "TestSecond", Action: "pass" },
];
const output = (events) => events.map((event) => JSON.stringify(event)).join("\n");

test("batched Azure evidence requires every selected test and package", () => {
  assertGoCoverage(output(passes), packages, patterns);
  assert.throws(() => assertGoCoverage(output(passes.slice(0, 1)), packages, patterns), /selector.*TestSecond/);
  assert.throws(() => assertGoCoverage(output(passes), [...packages, "./internal/missing"], patterns), /package.*missing/);
});

test("batched Azure evidence cannot substitute another adapter or skip services", () => {
  const wrongAdapter = [{ ...passes[0], Test: "TestFirst/memory" }, passes[1]];
  assert.throws(() => assertGoCoverage(output(wrongAdapter), packages, patterns), /selector.*postgres/);
  assert.throws(() => assertGoCoverage(output([
    ...passes, { Package: "./internal/one", Test: "TestFirst/postgres/required", Action: "skip" },
  ]), packages, patterns), /selected service coverage cannot skip/);
});
