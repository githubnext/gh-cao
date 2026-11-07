---
title: Scatter and bubble charts
description: Dashboard Language scatter clustering and quantitative circle-area encoding.
---

This companion to the [Dashboard Language specification](dashboard-language-specification.md)
defines scatter value encoding. Its normative requirements use the specification's
conformance classes and requirements notation.

## Scatter clustering

Scatter charts may declare `scatter: { cluster: false }` when every observation must remain individually represented, including observations with identical timestamps and values. `scatter` is a mapping allowed only on scatter charts; its only key, `cluster`, is Boolean and defaults to `true`. An explicit `false` disables both clustering and render-time point sampling, preserving the precise timestamp and value of every query-produced observation. Authors should consider the rendering cost of their retained dataset before disabling reduction.

## Value-encoded scatter charts (bubble charts)

A scatter chart may encode a third quantitative measure through `encoding.value`.
This produces a **bubble chart**: circle **area**, not diameter, is proportional
to the positive value, using a zero-based scale. The diameter therefore grows
with the square root of the value. A separate size legend shows representative
values with matching circle-area ratios; an optional `color` legend remains
independent. Titles and units on `value` label the size legend, accessible point
names, and tooltips independently of `y`.

```json
{
  "id": "run-cost-and-duration",
  "mark": "chart",
  "chart": "scatter",
  "data": { "source": "runs", "limit": 100 },
  "encoding": {
    "x": { "field": "started-at", "type": "temporal" },
    "y": { "field": "duration", "type": "quantitative", "title": "Duration" },
    "value": { "field": "aic-total", "type": "quantitative", "title": "AI credits" },
    "color": { "field": "run-conclusion", "type": "nominal" }
  }
}
```

- **DLS-VIEW-049:** Only scatter charts and metrics **MAY** encode `value`.
  Scatter `value` **MUST** explicitly declare `type: quantitative` and reference
  one source field. Bubble chart encodings **MUST** use query-produced values;
  aggregation belongs in `dashboard.queries`, not `x`, `y`, `color`, or `value`.
  View ordering, time bucketing, and limits **MUST** execute through the production
  query boundary, not main-thread source derivation.
- **DLS-VIEW-050:** A value-encoded scatter chart **MUST** scale circle area
  proportionally from zero to its largest positive finite size value and
  **MUST** render a visibly labeled size legend even without `color`. Each plotted
  observation **MUST** expose its formatted size value in its accessible name and
  tooltip. Missing, null, non-numeric, non-finite, zero, and negative size values
  **MUST NOT** become fabricated fixed-size or zero-valued observations; the
  presenter **MUST** report how many cannot be plotted. If none have a usable
  size, it **MUST** show an explicit empty state rather than a size legend.
- **DLS-VIEW-051:** Value-encoded scatter charts **MUST NOT** cluster or sample
  observations, regardless of `scatter.cluster`, because a cluster would invent
  a size measure. They preserve coincident observations and original query
  values. Authors **SHOULD** bound query results with `limit`. Categorical facets
  retain the existing independent-per-panel scale semantics. Scatter charts
  without `value` retain their existing fixed-size markers and reduction defaults.
