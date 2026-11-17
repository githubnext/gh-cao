-- Reference DuckDB lowering of the RQA trajectory graders over S1 state rows.
-- Uses only L2 expressions, L3 stages, L4 equality joins on the trace key and
-- L5 `previous`/`row-number` windows; no recurrence matrix or self-join.
-- Placeholders are typed row sets supplied by the evidence test:
--   {{traces}}: trace
--   {{states}}: trace, position, id (NULL when upstream drops the element),
--               first_event_index, first_event_index_kind (absent|integer|other)
--   {{events}}: trace, position, event_index, event_index_kind (absent|integer|other)
WITH
traces AS ({{traces}}),
states AS ({{states}}),
events AS ({{events}}),
state_rows AS (
  SELECT trace, count(*) AS elements,
    bool_or(id IS NOT NULL AND first_event_index_kind = 'other') AS untyped_order
  FROM states GROUP BY trace
),
event_rows AS (
  SELECT trace, count(*) AS elements, bool_or(event_index_kind = 'other') AS untyped_order
  FROM events GROUP BY trace
),
guards AS (
  SELECT t.trace,
    coalesce(s.elements, 0) > 5000 OR coalesce(e.elements, 0) > 5000 AS oversized,
    coalesce(s.untyped_order, false) OR coalesce(e.untyped_order, false) AS untyped_order
  FROM traces t
  LEFT JOIN state_rows s ON s.trace = t.trace
  LEFT JOIN event_rows e ON e.trace = t.trace
),
-- Upstream Map.set keeps the last position for a repeated event index.
event_order AS (
  SELECT trace, event_index, max(position) AS event_position
  FROM events WHERE event_index_kind = 'integer' GROUP BY trace, event_index
),
visits AS (
  SELECT s.trace, s.position, s.id,
    coalesce(e.event_position, s.first_event_index, s.position) AS visit_order
  FROM states s
  LEFT JOIN event_order e ON e.trace = s.trace AND e.event_index = s.first_event_index
  WHERE s.id IS NOT NULL
),
-- (visit_order, position) is unique per trace, reproducing upstream's stable sort.
ordered AS (
  SELECT trace, id,
    row_number() OVER forward AS rn,
    lag(id, 1) OVER forward AS previous1,
    lag(id, 2) OVER forward AS previous2,
    lag(id, 1) OVER backward AS next1,
    row_number() OVER forward - row_number() OVER (PARTITION BY trace, id ORDER BY visit_order, position) AS island
  FROM visits
  WINDOW forward AS (PARTITION BY trace ORDER BY visit_order ASC NULLS LAST, position ASC),
         backward AS (PARTITION BY trace ORDER BY visit_order DESC NULLS FIRST, position DESC)
),
state_counts AS (SELECT trace, id, count(*) AS c FROM ordered GROUP BY trace, id),
visit_terms AS (
  SELECT o.trace,
    CASE WHEN o.previous1 = o.id AND o.next1 = o.id THEN sc.c - 1
         WHEN o.previous1 = o.id OR o.next1 = o.id THEN sc.c - 2
         ELSE 0 END AS vertical_points
  FROM ordered o JOIN state_counts sc ON sc.trace = o.trace AND sc.id = o.id
),
bigrams AS (SELECT trace, count(*) AS c FROM ordered WHERE rn > 1 GROUP BY trace, previous1, id),
trigrams AS (SELECT trace, count(*) AS c FROM ordered WHERE rn > 2 GROUP BY trace, previous2, previous1, id),
islands AS (
  SELECT o.trace, count(*) AS run_length, any_value(sc.c) AS c
  FROM ordered o JOIN state_counts sc ON sc.trace = o.trace AND sc.id = o.id
  GROUP BY o.trace, o.id, o.island
),
state_totals AS (
  SELECT trace, sum(c) AS n, sum(c * (c - 1)) AS recurrent_points FROM state_counts GROUP BY trace
),
bigram_totals AS (SELECT trace, sum(c * (c - 1) // 2) AS pairs FROM bigrams GROUP BY trace),
trigram_totals AS (SELECT trace, sum(c * (c - 1) // 2) AS pairs FROM trigrams GROUP BY trace),
vertical_totals AS (SELECT trace, sum(vertical_points) AS points FROM visit_terms GROUP BY trace),
island_totals AS (
  SELECT trace,
    sum(CASE WHEN run_length >= 2 THEN c - run_length ELSE 0 END + 2 * greatest(0, run_length - 2)) AS lines,
    sum(CASE WHEN run_length >= 2 THEN (c - run_length) * run_length ELSE 0 END
      + CASE WHEN run_length >= 3 THEN 2 * (run_length * (run_length - 1) // 2 - 1) ELSE 0 END) AS length
  FROM islands GROUP BY trace
),
totals AS (
  SELECT t.trace,
    coalesce(s.n, 0) AS n,
    coalesce(s.recurrent_points, 0) AS recurrent_points,
    coalesce(b.pairs, 0) AS bigram_pairs,
    coalesce(r.pairs, 0) AS trigram_pairs,
    coalesce(v.points, 0) AS vertical_points,
    coalesce(i.lines, 0) AS vertical_lines,
    coalesce(i.length, 0) AS vertical_length
  FROM traces t
  LEFT JOIN state_totals s ON s.trace = t.trace
  LEFT JOIN bigram_totals b ON b.trace = t.trace
  LEFT JOIN trigram_totals r ON r.trace = t.trace
  LEFT JOIN vertical_totals v ON v.trace = t.trace
  LEFT JOIN island_totals i ON i.trace = t.trace
),
measures AS (
  SELECT t.trace, g.oversized OR g.untyped_order AS unavailable, t.n,
    CASE WHEN t.n * (t.n - 1) = 0 THEN 0 ELSE t.recurrent_points::DOUBLE / (t.n * (t.n - 1)) END AS rr,
    CASE WHEN t.recurrent_points = 0 THEN 0 ELSE (2 * (2 * t.bigram_pairs - t.trigram_pairs))::DOUBLE / t.recurrent_points END AS det,
    CASE WHEN t.recurrent_points = 0 THEN 0 ELSE t.vertical_points::DOUBLE / t.recurrent_points END AS lam,
    CASE WHEN t.vertical_lines = 0 THEN 0 ELSE t.vertical_length::DOUBLE / t.vertical_lines END AS tt
  FROM totals t JOIN guards g ON g.trace = t.trace
)
SELECT trace,
  CASE WHEN unavailable THEN 'unavailable' WHEN n < 2 THEN 'not-applicable' ELSE 'measured' END AS rr_status,
  CASE WHEN unavailable OR n < 2 THEN NULL ELSE rr END AS rr,
  CASE WHEN unavailable THEN 'unavailable' WHEN n < 4 THEN 'not-applicable' ELSE 'measured' END AS rqa_status,
  CASE WHEN unavailable OR n < 4 THEN NULL ELSE det END AS det,
  CASE WHEN unavailable OR n < 4 THEN NULL ELSE lam END AS lam,
  CASE WHEN unavailable OR n < 4 THEN NULL ELSE tt END AS tt
FROM measures
ORDER BY trace
