---
title: Dashboard Language CLI Action Selectors
description: Declarative repository selection, wildcard enrollment, and bounded command-service lookup requirements.
---

This section extends the [Dashboard Language Specification](./dashboard-language-specification.md)
with the contract for view-placed CLI action selectors.

## Configuration

A view-placed `operate` CLI action may declare a reusable `selector` dialog
configuration: `label`, optional `placeholder` and `allow-patterns`, `debounce-ms` (100–2000),
`query: { command: "gh search repos", fields: [...], limit: 1–50 }`, and
`mapping: { value, label, description?, target }`. Mapping fields must be selected
by the query; `target` must name one command template token. For example:

```json
{
  "id": "add-repository",
  "level": "operate",
  "label": "Add repository",
  "icon": "plus",
  "placement": "view",
  "command": "gh cao repos add {{repository}}",
  "selector": {
    "label": "Repository",
    "debounce-ms": 300,
    "query": { "command": "gh search repos", "fields": ["fullName", "description"], "limit": 20 },
    "mapping": { "value": "fullName", "label": "fullName", "description": "description", "target": "repository" }
  }
}
```

## Repository patterns

With `allow-patterns: true`, `mapping.target` must be `repository-pattern`.
The selector additionally offers a typed exact name or `owner/repository` pattern
containing `*` for explicit selection, without issuing a search for that pattern.
Command rendering quotes wildcard values as one literal argument. Ordinary
`repository` tokens remain exact-only. Enrollment resolves current accessible
matches to exact policy entries, rejects incomplete discovery or more than 100
matches, and does not enroll future matches or grant credential access.

## Lookup and interaction

Selectors render only when a command service is explicitly available. Declaring
one does not authorize lookup or enrollment, and the native maintenance service
does not execute `gh cao repos add`. No canvas execution or search server is
provided. A service that supports lookup must expose bounded read-only GitHub
search, not a query over canonical dashboard evidence. It resolves the query
and mapping from the declared action ID;
clients cannot supply a command, fields, or mapping. Search input is a separate
argv value, never shell-interpolated. These transient external choices do not
become dashboard sources. Changing input immediately invalidates prior selection
and cancels stale lookups. Closing or detaching the dialog cancels pending work.
The presenter must label the textbox, announce loading, empty, and failed lookup
states, and keep results in a viewport-bounded, independently scrollable list.
Queries return at most their declared `limit`, even when hundreds of repositories
match. At that limit, the presenter tells users to refine their search rather
than implying that every match is shown. Slow lookups retain an explicit loading
state and, after two seconds, explain that users can continue typing or cancel.
The server bounds each GitHub CLI lookup to 15 seconds. Search input and
cancellation remain available while waiting; abandoned lookups and their
slow-search timers must not update later searches. Presenters support keyboard
selection and disable execution until a mapped value
passes command-template validation. Selection never executes a command; fresh,
exact per-invocation approval and the selected service's authorization remain
mandatory. Unsupported operations must fail closed rather than use a browser
callback or another executor.
