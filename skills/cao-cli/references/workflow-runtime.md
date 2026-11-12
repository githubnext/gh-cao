# CAO CLI in an agentic workflow

Use this reference only when an operational snapshot was explicitly prepared
for the authorized workflow.

The obsolete Activity JavaScript CLI is retired. Query prepared snapshots with
an already available native `gh cao`. Never build or install the `gh cao`
extension or run the operator's `install-gh-cao.sh` inside a workflow as a
missing-runtime or missing-evidence fallback.

1. Require `CAO_POSTGRES_URL` and its exact provisioned namespace. Missing canonical data
   means operational evidence is unavailable. Use only separately authorized,
   bounded read-only evidence tools; do not infer a snapshot or discover artifacts.
2. Check that the native `gh cao` extension is already available. If it is
   absent, skip every CAO command and use only separately authorized bounded
   read-only tools.
3. Use the authorized PostgreSQL connection; queries never start containers:

   ```bash
   gh cao gh runs \
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
