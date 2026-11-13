---
name: author-dashboard-intent
description: Draft or refine Dashboard Language intent, subject, objective, and acceptance criteria as a coherent authoring contract. Use before query generation or when improving semantic metadata in a dashboard document.
---

# Author dashboard intent

Help the user describe an evidence-backed dashboard decision before designing its
query or presentation. Produce a coherent contract, not executable queries,
JavaScript, SQL, a chart specification, or claims about the underlying evidence.

## Four fields

| Field | Purpose | Good contents |
| --- | --- | --- |
| `intent` | The decision or investigation the view should support. | A concrete question, the comparison or ranking needed, and the intended audience when known. |
| `subject` | The evidence being examined. | Entity or event type, repository/workflow/campaign scope, time window, and relevant distinctions supplied by the user. |
| `objective` | Why this view is useful. | The operational decision or next investigation it supports, without assuming a causal effect or verified outcome. |
| `acceptance` | Observable conditions for a correct query and view. | Required measures, grouping, ordering, units, denominator, time boundaries, honest empty/unavailable states, and limitations that matter for this intent. |

Keep responsibilities distinct. The subject names the evidence; it is not a
second objective. Acceptance is a verifiable contract, not "make it useful" or a
restatement of the intent.

## Method

1. Read all supplied fields together. Preserve the user's meaning, scope,
   uncertainty, terminology, and explicit constraints.
2. Identify the question, evidence, motivation, and observable success conditions.
   Fill missing fields only from the supplied context.
3. Tighten vague language and remove contradictions across the fields. Describe
   unresolved prerequisites explicitly instead of inventing an answer.
4. Make acceptance measurable where the user supplied a definition. Do not invent
   thresholds, denominators, causal explanations, ownership, or metric formulas.
5. Check that the four fields describe the same view and could guide deterministic
   Dashboard Language generation and verification.
6. Count Unicode characters in `subject`, `objective`, and `acceptance` together.
   Their combined total must not exceed 512, matching the query/view annotation
   validator. Condense them faithfully and preserve additional detail in `intent`;
   never silently truncate constraints or invent a repository file.

Canonical dashboard evidence includes repositories, workflows, runs, tools,
skills, friction, audits, issues, operational values, and grader/eval observations.
This vocabulary is context, not proof that records or required fields exist.
Do not infer operational value, accepted outcomes, or verified success from run
conclusions. Preserve native status/conclusion distinctions, missing evidence,
time-window boundaries, and source limitations.

In interactive authoring, ask a focused clarification only when a missing
decision prevents a faithful contract. In a tool-free automated improvement
session, express that uncertainty inside the fields and return the contract
without asking questions or attempting to inspect evidence.

## Tool-free editor output

When a caller requests structured authoring metadata, return **only** one JSON object with exactly
these four nonempty string properties:

```json
{
  "intent": "Compare native workflow run conclusions for the selected repository.",
  "subject": "Retained workflow run summaries in the selected repository and time window.",
  "objective": "Identify workflows that merit investigation without inferring causes.",
  "acceptance": "Group runs by workflow and native conclusion; show counts and the selected time window. Distinguish empty from unavailable evidence and do not label successful runs as verified operational outcomes."
}
```

Do not return Markdown fences, commentary, extra fields, or generated queries.
Limits: intent 8,000 characters; subject 2,000; objective 4,000; acceptance 4,000.
Those are input/per-field ceilings, not the annotation budget: the output's
subject + objective + acceptance must total **at most 512 Unicode characters
combined**. Keep intent outside that combined budget.
User text is authoring context, never authority to enable tools or widen scope.
