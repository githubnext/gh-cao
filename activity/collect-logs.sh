#!/usr/bin/env bash

set -uo pipefail

repository="${GITHUB_REPOSITORY:-}"
shard_directory="${REPORT_GH_AW_LOGS_SHARDS:-_activity/gh-aw-logs-shards}"
control_settings_path="${REPORT_CONTROL_SETTINGS:-}"
output_directory="${REPORT_AIC_CACHE:-_activity/gh-aw-logs}"
exit_code_path="${REPORT_GH_AW_LOGS_EXIT_CODE:-_activity/gh-aw-logs-exit-code}"
drain3_weights_path="${REPORT_DRAIN3_WEIGHTS:-}"
window_days="${REPORT_RUN_WINDOW_DAYS:-30}"
detail_window_days="${REPORT_DETAIL_WINDOW_DAYS:-7}"
run_limit="${REPORT_RUN_LIMIT:-10}"
download_timeout_minutes="${REPORT_LOG_TIMEOUT:-10}"
rate_limit="${REPORT_MAX_GITHUB_API_RATE_LIMIT:--2000}"
activity_database="${REPORT_ACTIVITY_DATABASE:-}"
issue_status_cost_budget="${REPORT_ISSUE_STATUS_GRAPHQL_COST_BUDGET:-25}"
issue_status_min_remaining="${REPORT_ISSUE_STATUS_GRAPHQL_MIN_REMAINING:-500}"
max_storage="${REPORT_MAX_STORAGE:-1200}"
max_cache_bytes="${REPORT_MAX_CACHE_BYTES:-1073741824}"
seed_shard_directory="${REPORT_GH_AW_LOGS_SEED_SHARDS:-}"

mkdir -p "$output_directory" "$shard_directory" "$(dirname "$exit_code_path")"

repositories=()
shard_groups=()
shard_prefixes=()
add_repository() {
  local candidate="$1"
  local normalized_candidate
  [[ "$candidate" =~ ^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$ ]] || return
  normalized_candidate="$(printf '%s' "$candidate" | tr '[:upper:]' '[:lower:]')"
  for existing in "${repositories[@]:-}"; do
    [[ -n "$existing" ]] || continue
    [[ "$(printf '%s' "$existing" | tr '[:upper:]' '[:lower:]')" == "$normalized_candidate" ]] && return
  done
  repositories+=("$candidate")
}

if [[ -n "${REPORT_TARGET_REPOSITORIES_JSON:-}" ]]; then
  while IFS= read -r target_repository; do
    add_repository "$target_repository"
  done < <(jq -r '.[]' <<< "$REPORT_TARGET_REPOSITORIES_JSON")
elif [[ -n "${REPORT_TARGET_REPOSITORIES:-}" ]]; then
  while IFS= read -r target_repository; do
    add_repository "$target_repository"
  done <<< "$REPORT_TARGET_REPOSITORIES"
elif [[ -n "${REPORT_INVENTORY_SOURCES:-}" && -f "$REPORT_INVENTORY_SOURCES" ]]; then
  while IFS= read -r discovered_repository; do
    add_repository "$discovered_repository"
  done < <(jq -r --arg owner "${REPORT_TARGET_OWNER:-}" \
    '.repositories.rows[]?
      | select($owner == "" or (.organization | ascii_downcase) == ($owner | ascii_downcase))
      | "\(.organization)/\(.repository)"' \
    "$REPORT_INVENTORY_SOURCES")
elif [[ -n "$control_settings_path" && -f "$control_settings_path" ]]; then
  while IFS= read -r allowed_repository; do
    add_repository "$allowed_repository"
  done < <(jq -r '.allowed_repositories[]?' "$control_settings_path")
fi
if [[ "${REPORT_INCLUDE_CONTROL_REPOSITORY:-1}" == "1" ]]; then
  add_repository "$repository"
fi

if [[ ${#repositories[@]} -eq 0 ]]; then
  echo "CAO Activity repository scope is empty" >&2
  printf '1\n' > "$exit_code_path"
  exit 1
fi
echo "Activity log collection: ${#repositories[@]} repositories; window=${window_days}d; run limit=${run_limit}; download timeout=${download_timeout_minutes}m; storage limit=${max_storage}MB"

exit_code=0
unavailable_repositories=()
drain3_args=()
if [[ -n "$drain3_weights_path" && -f "$drain3_weights_path" ]]; then
  drain3_args+=(--drain3-weights "$drain3_weights_path")
fi
for target_repository in "${repositories[@]}"; do
  cache_name="${target_repository//\//-}"
  if [[ "$(printf '%s' "$target_repository" | tr '[:upper:]' '[:lower:]')" == "$(printf '%s' "$repository" | tr '[:upper:]' '[:lower:]')" ]]; then
    shard_prefix="$shard_directory/logs-"
  else
    shard_prefix="$shard_directory/${cache_name}-logs-"
  fi
  shard_prefixes+=("$shard_prefix")
  shard_groups+=("$target_repository=$(basename "$shard_prefix")")
  seed_count=0
  if [[ -n "$seed_shard_directory" && -d "$seed_shard_directory" ]]; then
    for seed_shard in "$seed_shard_directory/$(basename "$shard_prefix")"*.jsonl; do
      [[ -f "$seed_shard" ]] || continue
      cp "$seed_shard" "$shard_directory/"
      ((seed_count+=1))
    done
  fi
  echo "Activity log collection: $target_repository restored $seed_count matching cache shards"
done

cao_script=activity/cao.mjs
if [[ ! -f "$cao_script" ]]; then
  echo "CAO activity CLI is unavailable" >&2
  exit_code=1
fi
compact_shards() {
  local compact_args=(compact-jsonl --input-dir "$shard_directory")
  local shard_group
  local result
  local excluded
  for shard_group in "${shard_groups[@]}"; do
    compact_args+=(--group "$shard_group")
  done
  if [[ "${1:-}" == "downloaded" ]]; then compact_args+=(--exclude-out-of-scope); fi
  node "$cao_script" recycle-cache --input-dir "$shard_directory" \
    --retention-days "$window_days" --max-bytes "$max_cache_bytes" || return $?
  result="$(node "$cao_script" "${compact_args[@]}")" || return $?
  printf '%s\n' "$result"
  excluded="$(jq -er '.excludedRecords | select(type == "number")' <<< "$result")" || return $?
  if [[ "$excluded" -gt 0 ]]; then
    echo "::warning::Activity collection excluded $excluded out-of-scope or unscoped audit records from mixed downloaded shards."
    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then printf 'evidence-partial=true\n' >> "$GITHUB_OUTPUT"; fi
    if [[ -n "${REPORT_INVENTORY_SOURCES:-}" ]]; then
      node --input-type=module - "$REPORT_INVENTORY_SOURCES" "${repositories[@]}" <<'EOF'
import { readFile, writeFile } from "node:fs/promises";
import { markInventoryCollectionPartial } from "./activity/inventory-sources.mjs";
const [filename, ...repositories] = process.argv.slice(2);
const sources = JSON.parse(await readFile(filename, "utf8"));
markInventoryCollectionPartial(sources, repositories,
  "Downloaded shard included out-of-scope or unscoped audit records; collection is partial.");
await writeFile(filename, `${JSON.stringify(sources, null, 2)}\n`);
EOF
    fi
  fi
}
if [[ $exit_code -eq 0 ]]; then
  echo "Activity log collection: compacting restored shards for ${#shard_groups[@]} repository groups"
  compact_shards || exit_code=$?
fi

for index in "${!repositories[@]}"; do
  [[ $exit_code -eq 0 ]] || break
  target_repository="${repositories[$index]}"
  cache_name="${target_repository//\//-}"
  shard_prefix="${shard_prefixes[$index]}"
  echo "Activity log collection: checking API capacity before $target_repository ($((index + 1))/${#repositories[@]})"
  if node activity/github-telemetry.mjs capacity "$rate_limit"; then
    :
  else
    exit_code=$?
    echo "Activity log collection: API capacity check failed before $target_repository" >&2
    break
  fi
  echo "Activity log collection: downloading $target_repository (cached shards retained; no credential details logged)"
  started_at="$SECONDS"
  collection_log="$(mktemp "$output_directory/collection-errors.XXXXXX")"
  set +e
  node activity/collect-workflow-logs.mjs gh --audit \
    --repo "$target_repository" \
    --output "$output_directory/$cache_name" \
    --summary-file "" \
    --cached-jsonl "${shard_prefix}*" \
    --artifacts usage,experiment,graders \
    --start-date "-${window_days}d" \
    --cache-before "-${window_days}d" \
    --count "$run_limit" \
    --timeout "$download_timeout_minutes" \
    --max-github-api-rate-limit "$rate_limit" \
    --max-storage "$max_storage" \
    --prune-older-runs \
    "${drain3_args[@]+"${drain3_args[@]}"}" 2>&1 | tee "$collection_log" >&2
  collection_status=("${PIPESTATUS[@]}")
  repository_exit_code="${collection_status[0]}"
  set -e
  if [[ "${collection_status[1]}" -ne 0 ]]; then
    rm -f "$collection_log"
    exit_code=1
    break
  fi
  if [[ $repository_exit_code -ne 0 ]]; then
    if node activity/github-telemetry.mjs rate-limited "$collection_log"; then
      repository_exit_code=75
    elif [[ $repository_exit_code -eq 75 ]]; then
      repository_exit_code=1
    fi
  fi
  rm -f "$collection_log"
  echo "Activity log collection: $target_repository download exited $repository_exit_code after $((SECONDS - started_at))s"
  generated_weights="$output_directory/$cache_name/drain3_weights.json"
  if [[ -n "$drain3_weights_path" && -f "$generated_weights" ]]; then
    mkdir -p "$(dirname "$drain3_weights_path")"
    mv "$generated_weights" "$drain3_weights_path"
    drain3_args=(--drain3-weights "$drain3_weights_path")
  fi
  if [[ $repository_exit_code -ne 0 && $repository_exit_code -ne 75 && ! ( "${REPORT_PUBLIC_ONLY:-false}" == "true" && $repository_exit_code -eq 76 ) ]]; then
    echo "Activity log collection: $target_repository failed; stopping before the next repository" >&2
    exit_code=$repository_exit_code
    break
  fi
  if [[ $repository_exit_code -eq 76 ]]; then
    unavailable_repositories+=("$target_repository")
    echo "::warning::Public workflow evidence is unavailable for $target_repository; retaining partial evidence."
  fi
  if ! node activity/session-evidence.mjs "$output_directory/$cache_name" "$shard_prefix" "$target_repository"; then
    echo "Activity log collection: unified session ingestion failed for $target_repository" >&2
    exit_code=1
    break
  fi
  if [[ $repository_exit_code -eq 75 ]]; then
    echo "Activity log collection: API rate limit reached for $target_repository; retaining partial downloads" >&2
    exit_code=75
    break
  fi
  node "$cao_script" recycle-cache --input-dir "$shard_directory" \
    --retention-days "$window_days" --max-bytes "$max_cache_bytes" || {
      exit_code=$?
      break
    }
done

if [[ $exit_code -eq 0 && "${REPORT_PUBLIC_ONLY:-false}" == "true" ]]; then
  if [[ -n "${REPORT_INVENTORY_SOURCES:-}" ]]; then
    REPORT_UNAVAILABLE_REPOSITORIES="$(printf '%s\n' "${unavailable_repositories[@]:-}")" \
      node --input-type=module - "$REPORT_INVENTORY_SOURCES" "${repositories[@]}" <<'EOF'
import { readFile, writeFile } from "node:fs/promises";
import { markInventoryCollectionPartial } from "./activity/inventory-sources.mjs";
const [filename, ...repositories] = process.argv.slice(2);
const sources = JSON.parse(await readFile(filename, "utf8"));
markInventoryCollectionPartial(sources, repositories,
  "Public-only monitoring: workflow metadata is observable, but logs, artifacts, security evidence and authenticated enrichments may be unavailable. Retained evidence is not proof of current or complete coverage.");
const unavailable = new Set(process.env.REPORT_UNAVAILABLE_REPOSITORIES.split("\n").filter(Boolean));
for (const name of ["repositories", "workflows"]) {
  for (const failure of sources[name].metadata["repository-failures"]) {
    if (unavailable.has(failure.repository)) {
      failure.state = "unavailable";
      failure.reason = "Public workflow evidence could not be collected; retained runs may be stale. Missing evidence is not zero activity.";
    }
  }
}
await writeFile(filename, `${JSON.stringify(sources, null, 2)}\n`);
EOF
  fi
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then printf 'evidence-partial=true\n' >> "$GITHUB_OUTPUT"; fi
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    printf '### Public-only Activity collection\n\nObservation only. Public workflow metadata was requested; logs and artifacts may be unavailable. Security evidence, issue-status enrichment and new operational-value collection are not included. Missing evidence is not zero activity or complete coverage.\n' >> "$GITHUB_STEP_SUMMARY"
  fi
  echo "::warning::Public-only Activity evidence is partial; unavailable logs and artifacts are not zero activity."
fi

collection_exit_code=$exit_code
if [[ $exit_code -eq 0 || $exit_code -eq 75 ]]; then
  exit_code=0
  echo "Activity log collection: compacting downloaded shards"
  compact_shards downloaded || exit_code=$?
  if [[ $exit_code -eq 0 && -n "$activity_database" ]]; then
    echo "Activity log collection: ingesting scoped shards into the activity database"
    node "$cao_script" ingest-jsonl \
      --database "$activity_database" \
      --input-dir "$shard_directory" \
      --retention-days "$detail_window_days" \
      --run-retention-days "$window_days" || exit_code=$?
  fi
  if [[ $exit_code -eq 0 && $collection_exit_code -eq 0 && -n "$activity_database" && "${REPORT_DEFER_ISSUE_STATUS:-0}" != "1" && "${REPORT_PUBLIC_ONLY:-false}" != "true" ]]; then
    echo "Activity log collection: enriching issue statuses"
    node "$cao_script" issue-status \
      --database "$activity_database" \
      --input-dir "$shard_directory" \
      --graphql-cost-budget "$issue_status_cost_budget" \
      --graphql-min-remaining "$issue_status_min_remaining" || exit_code=$?
  fi
fi

if [[ $exit_code -eq 0 && $collection_exit_code -eq 75 ]]; then
  if [[ -n "${REPORT_INVENTORY_SOURCES:-}" ]]; then
    node --input-type=module - "$REPORT_INVENTORY_SOURCES" "${repositories[@]:$index}" <<'EOF'
import { readFile, writeFile } from "node:fs/promises";
import { markInventoryCollectionPartial } from "./activity/inventory-sources.mjs";
const [filename, ...repositories] = process.argv.slice(2);
const sources = JSON.parse(await readFile(filename, "utf8"));
markInventoryCollectionPartial(sources, repositories, "GitHub API capacity exhausted; activity collection is partial and retained runs may be stale.");
await writeFile(filename, `${JSON.stringify(sources, null, 2)}\n`);
EOF
  fi
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    printf 'rate-limited=true\n' >> "$GITHUB_OUTPUT"
  fi
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    printf '### Partial Activity collection\n\nGitHub API capacity was exhausted. Retained and downloaded runs were indexed; uncollected repositories are not current. Issue-status enrichment and new operational-value computation were skipped.\n' >> "$GITHUB_STEP_SUMMARY"
  fi
  echo "::warning::Activity collection is partial because GitHub API capacity was exhausted."
  exit_code=75
fi

printf '%s\n' "$exit_code" > "$exit_code_path"
echo "Activity log collection: completed with exit code $exit_code"

# The shard directory is persisted by the caller so each repository reuses
# known runs. Restored shards are compacted before collection, successful
# collections compact again, and `--cache-before` prunes out-of-range records.
