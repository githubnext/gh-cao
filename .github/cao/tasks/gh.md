# Use gh

The GitHub CLI supplies authenticated, read-only inspection that CAO and gh-aw
commands build on. It is never an authority source.

## Read-only inspection

| Need | Command |
| --- | --- |
| Confirm the active account and host | `gh auth status` |
| Confirm repository access and default branch | `gh repo view OWNER/REPO --json nameWithOwner,defaultBranchRef,visibility` |
| List or inspect workflow runs | `gh run list --workflow FILE`, `gh run view RUN_ID --log-failed` |
| Download a run artifact such as `cao-validation` | `gh run download RUN_ID --name NAME --dir DIR` |
| Read an issue or pull request | `gh issue view`, `gh pr view`, `gh pr checks` |
| Read other REST or GraphQL state | `gh api` with `GET` requests only |

Record immutable URLs, run IDs, and commit SHAs for any finding.

## Boundaries

- Treat `gh` reach as credential reach, not policy. Inventory scope, live mode,
  and dispatch come only from reviewed `.github/workflows/cao.json`.
- Do not run mutating `gh` commands (`gh repo create`, `gh secret set`,
  `gh workflow run`, `gh pr merge`, non-`GET` `gh api`) without explicit
  operator approval for that exact command, repository, and effect. Prefer the
  owning `gh cao` or `gh aw` command, which validates the change.
- Never print, echo, or persist tokens; do not pass `gh auth token` output into
  files, workflow inputs, dispatch envelopes, commits, or chat.
- Use `gh cao` for CAO configuration and queries ([gh cao](gh-cao.md)) and
  `gh aw` for workflow compilation and logs ([gh aw](gh-aw.md)).
