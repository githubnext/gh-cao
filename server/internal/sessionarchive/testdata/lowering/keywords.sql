-- DuckDB (RE2) evidence for the closed upstream keyword predicates:
--   retries:          /\bretry(?:ing)?\b/i            (grader_session.cjs)
--   policy-near-miss: /\b(check|verify|...)\b/i       (trajectory_graders.cjs)
-- JavaScript non-Unicode \b and /i are ASCII-only, while RE2 (?i) and Rust
-- regex \b and \w are not, so the production keyword-match lowering spells
-- word boundaries and case folding out as ASCII classes (KeywordPattern).
-- {{texts}}: id, text; {{retries}}, {{guard}}: production keyword-match SQL.
WITH texts AS ({{texts}})
SELECT id,
  {{retries}} AS retries,
  {{guard}} AS guard
FROM texts
ORDER BY id
