-- Reference DuckDB (RE2) lowering of the closed upstream keyword predicates:
--   retries:          /\bretry(?:ing)?\b/i            (grader_session.cjs)
--   policy-near-miss: /\b(check|verify|...)\b/i       (trajectory_graders.cjs)
-- JavaScript non-Unicode \b and /i are ASCII-only, while RE2 (?i) and Rust
-- regex \b and \w are not, so word boundaries and case folding are spelled
-- out as ASCII classes. The same pattern text is valid for KQL `matches regex`.
-- {{texts}}: id, text
WITH texts AS ({{texts}})
SELECT id,
  regexp_matches(text, '(^|[^A-Za-z0-9_])[rR][eE][tT][rR][yY]([iI][nN][gG])?([^A-Za-z0-9_]|$)') AS retries,
  regexp_matches(text, '(^|[^A-Za-z0-9_])([cC][hH][eE][cC][kK]|[vV][eE][rR][iI][fF][yY]|[vV][eE][rR][iI][fF][iI][cC][aA][tT][iI][oO][nN]|[pP][oO][lL][iI][cC][yY]|[aA][pP][pP][rR][oO][vV][aA][lL]|[aA][pP][pP][rR][oO][vV][eE]|[gG][uU][aA][rR][dD]|[cC][oO][nN][fF][iI][rR][mM]|[aA][uU][tT][hH][oO][rR][iI][zZ][eE]|[aA][uU][tT][hH][oO][rR][iI][zZ][aA][tT][iI][oO][nN])([^A-Za-z0-9_]|$)') AS guard
FROM texts
ORDER BY id
