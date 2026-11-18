---
title: Dashboard Language List Selection
description: Lazy entity-card selection, exact-target batch commands, and local Updates maintenance requirements.
---

This section extends the [Dashboard Language Specification](./dashboard-language-specification.md)
with the normative contract for lazy list selection and Updates maintenance.

## Configuration and selection

A lazy entity-card list may declare `list.selection`:

```json
{
  "key": ["repository-coordinate", "workflow"],
  "commands": ["update-workflow-source", "enable-workflow", "disable-workflow"]
}
```

`key` is a non-empty sequence of unique source field names whose non-empty string
or finite numeric values identify each result uniquely. `commands` is a required,
non-empty sequence of unique action IDs from the referenced card template's
`actions`, in toolbar order. Context and `when` conditions are reused unchanged;
selection must not invent a command mapping or widen row eligibility.

Like GitHub issue lists, each rendered card always has an independently operable
checkbox; no separate selection toggle is required. Shift-click selects or
deselects the inclusive range between the last clicked checkbox and the current
one, including cards appended since the previous click. The header checkbox,
**Select all loaded items**, selects only
the cards already rendered, not unseen buffered rows or later pages. Its mixed
state indicates a partial selection of loaded cards; unchecking the checked
header clears the entire selection, including offscreen query results. View
filters, selection controls, and commands share a single non-wrapping header row.
Narrow viewports scroll that row horizontally rather than stacking controls;
dropdown panels remain visible outside the scrolling row. When all
loaded items are checked, a compact **Select all N** control selects the entire
remaining filtered query (or **Select all results** if no total is available).
It follows the existing view-bound canonical continuation, retaining route,
global and local filters, search, ordering, and authored query limits. It does
not issue an unscoped query, derive source rows in JavaScript, or render every
selected card. Loading more cards does not implicitly select them unless they
were already included in full-query selection. Individual deselection and
**Clear selection** remain available.

The command split button appears only while at least one item is selected and
defaults to the first declared command. Its dropdown changes the selected command
without running it; the primary button opens that command's exact-target batch approval
dialog, including boolean arguments, capability checks, and independent queue
admission. A command is disabled if any selected row fails its row-action
condition or lacks valid context. Unknown/unavailable evidence, invalid or
duplicate keys, failed/stale continuation, and mismatched totals fail closed;
partial evidence warns that only the explicitly listed targets are included.
Full-query selection is applied atomically after every page succeeds. A failure
preserves the prior explicit selection and exposes a retryable error; it never
approves the loaded page as if it were the full query. Clearing selection hides
bulk commands but keeps checkboxes visible. Source refresh or a changed query context creates a new
selection lifetime, discarding stale selected rows and approvals. Detaching the
view ignores late selection results.

## Updates maintenance

Local repository maintenance provides separate Repository, Workflow, and
Campaign pages under the **Updates** navigation section, each with its own
navigation entry and evidence-backed update indicator.
Each page is a full-view lazy card list over bounded query pages (10,000+
repositories); scrolling or **Load more** fetches more. Per-row and selected-batch
maintenance keep exact targets and approval. Selection exposes Update, Enable,
and Disable through the same declared row commands. Workflow enablement does not
change CAO policy, rollout, or live authority. Native enable/disable remains
limited to the explicitly selected local repository scope; managed update scope
does not extend it, and the hosted executor's allowlist is unchanged.
A break operation uses the existing disable command, not a new
CLI verb: gh-aw disable cancels in-progress runs before disabling future runs,
whereas campaign policy disable leaves active runs running. Exact repository
and workflow targets require fresh approval independently of update evidence.
Updates/upgrades still require pull-request publication in the native adapter;
the disable exception does not expand the hosted execution allowlist.
