import path from "node:path";

export function runCompactJsonl({
  options,
  compactJsonlShards,
  DEFAULT_COMPACTED_JSONL_SHARD_BYTES,
  option,
  rejectUnknownOptions,
}) {
  rejectUnknownOptions(options, ["input-dir", "group", "max-bytes", "exclude-out-of-scope"]);
  const groups = options.group
    ? Array.isArray(options.group) ? options.group : [options.group]
    : [];
  return compactJsonlShards(
    path.resolve(option(options, "input-dir")),
    groups,
    option(options, "max-bytes", false) === undefined
      ? DEFAULT_COMPACTED_JSONL_SHARD_BYTES
      : Number(option(options, "max-bytes", false)),
    { excludeOutOfScope: option(options, "exclude-out-of-scope", false) === "true" },
  );
}
