# CAO CLI in an agentic workflow

Use this reference only when an operational snapshot was explicitly prepared
for the authorized workflow.

The agent runtime is JavaScript under `activity/` and `dashboard/`. Invoke it
with Node.js; never build, install, or invoke the native `gh cao` extension or
run the operator's `install-gh-cao.sh` inside a workflow as a runtime dependency.

1. Check `.cao/gh-aw-logs.sqlite`. A missing or empty file
   means operational evidence is unavailable. Use only separately authorized,
   bounded read-only evidence tools; do not infer a snapshot or discover artifacts.
2. Resolve `gh cao` from the installed campaign source. If it is
   absent, skip every CAO command; never invoke `node` with an empty script path.
3. Pass the snapshot database explicitly:

   ```bash
   db=".cao/gh-aw-logs.sqlite"
   gh cao gh runs \
     --database "$db" \
     --repo OWNER/REPOSITORY \
     --workflow WORKFLOW \
     --status failure \
     --since YYYY-MM-DD \
     --until YYYY-MM-DD
   ```

Do not download another snapshot inside the run. Validate snapshot scope,
freshness, time window, and completeness before treating results as
authoritative.
The public Pages demo is synthetic and provides no operational SQLite or agent
summary. It is not a missing-evidence recovery source. For separately authorized
artifact preparation, follow [Agent analysis](https://githubnext.github.io/gh-cao/agent-analysis/);
do not widen the worker's acquisition scope or credentials.
