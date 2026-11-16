-- Reference DuckDB lowering of the declarative tool-call reconciliation plan
-- for tool-failure-count, tool-success-rate and trajectory-efficiency, over
-- promoted archive columns. Each CTE names the construct it needs: L2 row
-- compute, L3 stages, L4 equality joins, L5 `row-number` windows with
-- `groupby` (counts of earlier rows of another class are row-number
-- differences), L6 field-state tests and S-level text `first`. No recursion.
--
-- Rules, matching upstream createSessionLookups whenever its FIFO pointer
-- never skips a call:
--   * Only agent-phase rows take part.
--   * Resume: within each [path, layer] invocation ordered by attempt, a
--     resume attempt maps to the first attempt of its island; islands are
--     numbered by the count of non-resume attempts so far (gaps-and-islands,
--     not transitive closure). The mapping applies only to rows with a
--     non-empty SessionId; across invocations the last mapping of an id wins.
--     harness.retry attempt results and startup attempt scopes are outside
--     the fixture (S1 arrays, Phase 5).
--   * Starts are calls. A completion whose (scope, ToolCallId) appeared on an
--     earlier row re-merges into that call: the latest earlier start, else the
--     call of the first completion with that id.
--   * Every other completion uses the (scope, ToolName) queue. Once the queue
--     has a start, the k-th such completion completes the k-th start when that
--     start precedes it, otherwise it is an orphan call. This reproduces the
--     pointer, including overshoot, but not its skips over completed or
--     ID-mismatched calls: those need sequential state (G3).
--   * Outcome fields merge last-writer-wins per present field.
--   * Gateway rows (mcp.tool_call and tools/call RPCs) group by server, id
--     (or event index) and attempt scope; unnamed rows count only after a
--     named row of the same key; requests do not complete. A gateway call
--     whose id and resolved attempt match a native id is dropped.
-- Placeholders are typed row sets supplied by the evidence test:
--   {{traces}}: trace
--   {{events}}: trace, Sequence and the promoted columns below; strings are
--               '' when absent, ErrorState is absent|null|value.
WITH
traces AS ({{traces}}),
events AS ({{events}}),
agent AS (SELECT * FROM events WHERE Phase = 'agent'),
-- Attempt events dedup per invocation and attempt id: the last event's data
-- at the first event's position (upstream Map replacement).
attempt_events AS (
  SELECT trace, Path, Layer, DataAttemptId,
    max_by(DataAttempt, Sequence) AS DataAttempt, max_by(Strategy, Sequence) AS Strategy, min(Sequence) AS Sequence
  FROM agent
  WHERE Type = 'harness.attempt' AND Component = 'harness' AND Layer IN ('harness', 'startup')
    AND DataAttempt >= 1 AND DataAttemptId <> ''
  GROUP BY trace, Path, Layer, DataAttemptId
),
-- L5 + L2: island number = count of non-resume attempts up to this row.
attempts AS (
  SELECT trace, Sequence, Path, Layer, DataAttempt, DataAttemptId, Strategy = 'resume' AS resume,
    row_number() OVER (PARTITION BY trace, Path, Layer ORDER BY DataAttempt, Sequence) AS rn_all,
    row_number() OVER (PARTITION BY trace, Path, Layer, Strategy = 'resume' ORDER BY DataAttempt, Sequence) AS rn_class
  FROM attempt_events
),
islands AS (
  SELECT *, CASE WHEN resume THEN rn_all - rn_class ELSE rn_class END AS island,
    row_number() OVER (PARTITION BY trace, Path, Layer, CASE WHEN resume THEN rn_all - rn_class ELSE rn_class END
      ORDER BY DataAttempt, Sequence) AS island_rn
  FROM attempts
),
-- L4: the first attempt of each island is its root.
rooted AS (
  SELECT i.trace, i.DataAttemptId, i.DataAttempt, i.Sequence, r.DataAttemptId AS root
  FROM islands i JOIN islands r
    ON r.trace = i.trace AND r.Path = i.Path AND r.Layer = i.Layer AND r.island = i.island AND r.island_rn = 1
),
-- S-level text `first` ordered by attempt descending: the last mapping wins.
attempt_roots AS (
  SELECT trace, DataAttemptId, max_by(root, (DataAttempt, Sequence)) AS root FROM rooted GROUP BY trace, DataAttemptId
),
scoped AS (
  SELECT e.*, CASE WHEN e.ProvenanceAttemptId = '' THEN ''
      WHEN e.SessionId <> '' THEN coalesce(r.root, e.ProvenanceAttemptId)
      ELSE e.ProvenanceAttemptId END AS attempt
  FROM agent e LEFT JOIN attempt_roots r ON r.trace = e.trace AND r.DataAttemptId = e.ProvenanceAttemptId
),
native AS (
  SELECT *, Type = 'tool.execution_start' AS is_start,
    row_number() OVER (PARTITION BY trace, SessionId, AgentId, attempt, ToolCallId ORDER BY Sequence) AS id_all,
    row_number() OVER (PARTITION BY trace, SessionId, AgentId, attempt, ToolCallId, Type ORDER BY Sequence) AS id_class
  FROM scoped WHERE Type IN ('tool.execution_start', 'tool.execution_complete')
),
-- A completion is ID-matched when its non-empty id appeared on an earlier row.
fifo_rows AS (
  SELECT *,
    row_number() OVER (PARTITION BY trace, SessionId, AgentId, attempt, ToolName ORDER BY Sequence) AS name_all,
    row_number() OVER (PARTITION BY trace, SessionId, AgentId, attempt, ToolName, is_start ORDER BY Sequence) AS name_class
  FROM native WHERE is_start OR ToolCallId = '' OR id_all = 1
),
-- The pointer exists once the queue has a start; only completions after that
-- advance it. The k-th such completion takes the k-th start if it is queued.
fifo_queued AS (
  SELECT *,
    row_number() OVER (PARTITION BY trace, SessionId, AgentId, attempt, ToolName ORDER BY Sequence) AS k
  FROM fifo_rows WHERE NOT is_start AND name_all > name_class
),
fifo_targets AS (
  SELECT c.trace, c.Sequence,
    CASE WHEN q.k <= c.name_all - c.name_class THEN 'S' || s.Sequence ELSE 'C' || c.Sequence END AS call
  FROM fifo_rows c
  LEFT JOIN fifo_queued q ON q.trace = c.trace AND q.Sequence = c.Sequence
  LEFT JOIN fifo_rows s ON s.trace = c.trace AND s.is_start AND s.SessionId = c.SessionId AND s.AgentId = c.AgentId
    AND s.attempt = c.attempt AND s.ToolName = c.ToolName AND s.name_class = q.k
  WHERE NOT c.is_start
),
matched_targets AS (
  SELECT c.trace, c.Sequence,
    CASE WHEN c.id_all > c.id_class THEN 'S' || s.Sequence ELSE f.call END AS call
  FROM native c
  LEFT JOIN native s ON s.trace = c.trace AND s.is_start AND s.SessionId = c.SessionId AND s.AgentId = c.AgentId
    AND s.attempt = c.attempt AND s.ToolCallId = c.ToolCallId AND s.id_class = c.id_all - c.id_class
  LEFT JOIN native first ON first.trace = c.trace AND NOT first.is_start AND first.SessionId = c.SessionId
    AND first.AgentId = c.AgentId AND first.attempt = c.attempt AND first.ToolCallId = c.ToolCallId AND first.id_class = 1
  LEFT JOIN fifo_targets f ON f.trace = first.trace AND f.Sequence = first.Sequence
  WHERE NOT c.is_start AND c.ToolCallId <> '' AND c.id_all > 1
),
completions AS (
  SELECT n.*, coalesce(f.call, m.call) AS call
  FROM native n
  LEFT JOIN fifo_targets f ON f.trace = n.trace AND f.Sequence = n.Sequence
  LEFT JOIN matched_targets m ON m.trace = n.trace AND m.Sequence = n.Sequence
  WHERE NOT n.is_start
),
native_calls AS (
  SELECT trace, 'S' || Sequence AS call, ToolName AS name FROM native WHERE is_start
  UNION ALL
  SELECT trace, call, ToolName FROM completions WHERE call = 'C' || Sequence
),
-- L6 + S-level `first`: the last present value of each outcome field.
native_outcomes AS (
  SELECT c.trace, c.call, c.name, count(m.Sequence) > 0 AS completed,
    max_by(m.Success, m.Sequence) FILTER (WHERE m.Success IS NOT NULL) AS success,
    max_by(m.IsError, m.Sequence) FILTER (WHERE m.IsError IS NOT NULL) AS is_error,
    max_by(m.Status, m.Sequence) FILTER (WHERE m.Status <> '') AS status,
    max_by(m.ErrorState, m.Sequence) FILTER (WHERE m.ErrorState <> 'absent') AS error
  FROM native_calls c LEFT JOIN completions m ON m.trace = c.trace AND m.call = c.call
  GROUP BY c.trace, c.call, c.name
),
native_ids AS (
  SELECT DISTINCT trace, ToolCallId AS id, attempt FROM native WHERE ToolCallId <> ''
),
gateway_rows AS (
  SELECT *,
    CASE WHEN ToolCallId <> '' THEN to_json(ToolCallId) WHEN RpcIdString <> '' THEN to_json(RpcIdString)
      WHEN RpcIdNumber IS NOT NULL THEN to_json(RpcIdNumber) WHEN RequestIdString <> '' THEN to_json(RequestIdString)
      WHEN RequestIdNumber IS NOT NULL THEN to_json(RequestIdNumber) END AS id
  FROM scoped
  WHERE Type IN ('mcp.tool_call', 'mcp.rpc.request', 'mcp.rpc.response') AND Method IN ('', 'tools/call')
),
gateway_keyed AS (
  SELECT *, CAST(coalesce(id, to_json(Sequence)) AS VARCHAR) AS gateway_id,
    CASE WHEN ProvenanceAttemptId <> '' THEN SessionId ELSE '' END AS key_session,
    CASE WHEN ProvenanceAttemptId <> '' THEN AgentId ELSE '' END AS key_agent
  FROM gateway_rows WHERE ToolName <> '' OR id IS NOT NULL
),
gateway_ranked AS (
  SELECT *,
    row_number() OVER (PARTITION BY trace, ServerName, gateway_id, attempt, key_session, key_agent ORDER BY Sequence) AS key_all,
    row_number() OVER (PARTITION BY trace, ServerName, gateway_id, attempt, key_session, key_agent, ToolName <> '' ORDER BY Sequence) AS key_class
  FROM gateway_keyed
),
gateway_merged AS (
  SELECT * FROM gateway_ranked WHERE ToolName <> '' OR key_all > key_class
),
gateway_calls AS (
  SELECT trace, ServerName, gateway_id, attempt, key_session, key_agent,
    max_by(ToolName, Sequence) FILTER (WHERE ToolName <> '') AS name,
    min_by(attempt, Sequence) AS call_attempt,
    bool_or(Type <> 'mcp.rpc.request') AS completed,
    max_by(Success, Sequence) FILTER (WHERE Type <> 'mcp.rpc.request' AND Success IS NOT NULL) AS success,
    max_by(IsError, Sequence) FILTER (WHERE Type <> 'mcp.rpc.request' AND IsError IS NOT NULL) AS is_error,
    max_by(Status, Sequence) FILTER (WHERE Type <> 'mcp.rpc.request' AND Status <> '') AS status,
    max_by(ErrorState, Sequence) FILTER (WHERE Type <> 'mcp.rpc.request' AND ErrorState <> 'absent') AS error,
    coalesce(
      max_by(ToolCallId, Sequence) FILTER (WHERE Type <> 'mcp.rpc.request' AND ToolCallId <> ''),
      max_by(coalesce(nullif(RpcIdString, ''), CAST(RpcIdNumber AS VARCHAR)), Sequence)
        FILTER (WHERE Type <> 'mcp.rpc.request' AND (RpcIdString <> '' OR RpcIdNumber IS NOT NULL)),
      max_by(coalesce(nullif(RequestIdString, ''), CAST(RequestIdNumber AS VARCHAR)), Sequence)
        FILTER (WHERE Type <> 'mcp.rpc.request' AND (RequestIdString <> '' OR RequestIdNumber IS NOT NULL))
    ) AS merged_id
  FROM gateway_merged
  GROUP BY trace, ServerName, gateway_id, attempt, key_session, key_agent
),
calls AS (
  SELECT trace, name, completed, success, is_error, status, error FROM native_outcomes
  UNION ALL
  SELECT g.trace, g.name, g.completed, g.success, g.is_error, g.status, g.error
  FROM gateway_calls g
  WHERE g.merged_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM native_ids n WHERE n.trace = g.trace AND n.id = g.merged_id AND n.attempt = g.call_attempt)
),
graded AS (
  SELECT trace, count(*) AS tool_calls,
    count(*) FILTER (WHERE NOT completed OR success = false OR is_error = true
      OR status IN ('error', 'failure') OR error = 'value') AS failed,
    count(DISTINCT name) AS names
  FROM calls GROUP BY trace
)
SELECT t.trace,
  CAST(coalesce(g.failed, 0) AS DOUBLE) AS failure_count,
  CASE WHEN coalesce(g.tool_calls, 0) = 0 THEN 1::DOUBLE ELSE (g.tool_calls - g.failed) / g.tool_calls END AS success_rate,
  CASE WHEN coalesce(g.tool_calls, 0) = 0 THEN 1::DOUBLE ELSE g.names / g.tool_calls END AS efficiency
FROM traces t LEFT JOIN graded g ON g.trace = t.trace
