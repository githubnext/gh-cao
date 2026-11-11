# CAO CLI in an agentic workflow

Use this reference only when the workflow imports
`.github/workflows/shared/activity-cache.md`.

The agent runtime is JavaScript under `activity/` and `dashboard/`. Invoke it
with Node.js; never build, install, or invoke the native `gh cao` extension or
run the operator's `install-gh-cao.sh` inside a workflow as a runtime dependency.

1. Check `$RUNNER_TEMP/cao-activity/gh-aw-logs.sqlite`. A missing or empty file
   is a cache miss, not an error. Fall back to bounded read-only GitHub or
   agentic-workflow tools.
2. Resolve `activity/cao.mjs` from the installed campaign source. If it is
   absent, skip every CAO command; never invoke `node` with an empty script path.
3. Pass the cache database explicitly:

   ```bash
   db="$RUNNER_TEMP/cao-activity/gh-aw-logs.sqlite"
   node activity/cao.mjs gh runs \
     --database "$db" \
     --repo OWNER/REPOSITORY \
     --workflow WORKFLOW \
     --status failure \
     --since YYYY-MM-DD \
     --until YYYY-MM-DD
   ```

Do not download another snapshot inside the run. Validate cache scope,
freshness, time window, and completeness before treating results as
authoritative.
The public Pages demo is synthetic and provides no operational SQLite or agent
summary. It is not a cache-miss recovery source. For separately authorized
artifact preparation, follow [Agent analysis](https://githubnext.github.io/gh-cao/agent-analysis/);
do not widen the worker's acquisition scope or credentials.
