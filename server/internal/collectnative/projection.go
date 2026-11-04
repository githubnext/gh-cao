package collectnative

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

//go:embed projection_contract.gen.json
var projectionContract []byte

type ProjectionContract struct {
	Queries       []query.Definition  `json:"queries"`
	RoutingFields map[string]string   `json:"routingFields"`
	PrunedFields  map[string][]string `json:"prunedFields"`
	Transient     []string            `json:"transient"`
}

func CanonicalContract() (ProjectionContract, error) {
	var contract ProjectionContract
	if err := decode(projectionContract, &contract); err != nil {
		return contract, err
	}
	return contract, query.Validate(contract.Queries)
}

// Projection is a bounded, per-attempt canonical publication. Routing remains
// the authoritative Dashboard Language query, executed by the database adapter
// before removing ingestion-only fields and committing these rows.
type Projection struct {
	Collections map[string][]model.Row
	RunID       string
	Identity    Identity
}

func nativeID(kind, coordinate string) string {
	return kind + ":gh-aw-logs:" + model.EncodeCoordinate(coordinate)
}

func optionalText(value any) string {
	text := scalar(value)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

func firstText(values ...any) string {
	for _, value := range values {
		if s := optionalText(value); s != "" {
			return s
		}
	}
	return ""
}

func canonicalNumber(value any) any {
	if finite(value) {
		return value
	}
	return nil
}

func nullableText(value any) any {
	if s := optionalText(value); s != "" {
		return s
	}
	return nil
}

func putText(row model.Row, key string, values ...any) {
	if s := firstText(values...); s != "" {
		row[key] = s
	}
}

func canonicalKind(kind, source string, fields map[string]any) string {
	switch {
	case kind == "firewall.http_access":
		return "domains"
	case kind == "skill.invoked":
		return "skills"
	case kind == "workflow_run_friction":
		return "friction"
	case source == "mcp", kind == "mcp.tool_call", kind == "mcp.rpc.request" && source == "gateway",
		kind == "tool.execution_start", kind == "tool.execution_complete", kind == "mcp.guard.blocked", kind == "mcp.difc.filtered":
		return "tools"
	case kind == "safe_output.result" && (fields["githubEntityType"] == "issue" || fields["githubEntityType"] == "pull_request"):
		if issueFields(text(fields["correlationId"]), fields) != nil {
			return "issues"
		}
	}
	return "audits"
}

func issueFields(link string, fields map[string]any) model.Row {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() != "github.com" {
		return nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || (parts[2] != "issues" && parts[2] != "pull") {
		return nil
	}
	n, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	row := model.Row{"id": fmt.Sprintf("github:issue:%s/%s:%d", strings.ToLower(parts[0]), strings.ToLower(parts[1]), n),
		"owner": parts[0], "repository": parts[1], "repositoryFullName": parts[0] + "/" + parts[1],
		"number": n, "isPullRequest": fields["githubEntityType"] == "pull_request", "url": link}
	for from, to := range map[string]string{"issueState": "state", "issueClosed": "closed", "issueStateReason": "stateReason",
		"issueClosedAt": "closedAt", "issueStatusObservedAt": "statusObservedAt"} {
		if value, present := fields[from]; present {
			row[to] = value
		}
	}
	return row
}

var bashTool = regexp.MustCompile(`(?i)(^|[/.:_-])(bash|shell)($|[/.:_-])`)

func (p *Projection) add(collection, sourceID, at string, row model.Row) {
	if row["id"] == nil {
		kind := strings.TrimSuffix(collection, "s")
		switch collection {
		case "graderObservations":
			kind = "grader-observation"
		case "evalObservations":
			kind = "eval-observation"
		case "experimentAssignments":
			kind = "experiment-assignment"
		}
		row["id"] = nativeID(kind, sourceID)
	}
	row["observedAt"] = at
	row["provenance"] = model.Row{"source": "gh-aw-logs", "sourceId": sourceID, "observedAt": at}
	if collection == "experiments" || collection == "graders" || collection == "evals" {
		row["firstObservedAt"], row["lastObservedAt"] = at, at
	}
	p.Collections[collection] = append(p.Collections[collection], row)
}

func (p *Projection) event(location string, line int, key, at, source, kind string, fields map[string]any) {
	row := model.Row{"runId": p.RunID, "source": source, "type": kind, "payloadRef": fmt.Sprintf("%s#L%d", location, line),
		"sourceSequence": line}
	merge(row, fields)
	collection := canonicalKind(kind, source, fields)
	switch collection {
	case "tools":
		name := firstText(fields["mcpTool"], fields["toolName"], fields["summary"], "unknown")
		row["toolType"], row["name"] = "mcp", name
		if bashTool.MatchString(name) {
			row["toolType"] = "bash"
		}
	case "skills":
		row["name"] = firstText(fields["name"], fields["summary"], "unknown")
		row["invocationCount"], row["failedCount"] = 1, 0
		if n, ok := number(fields["invocationCount"]); ok {
			row["invocationCount"] = max(1, n)
		}
		if n, ok := number(fields["failedCount"]); ok {
			row["failedCount"] = max(0, n)
		}
	case "issues":
		merge(row, issueFields(text(fields["correlationId"]), fields))
	}
	if n, ok := number(row["status"]); ok {
		row["status"] = strconv.FormatFloat(n, 'f', -1, 64)
	}
	sourceID := fmt.Sprintf("%s:%s:%d", p.RunID, location, line)
	if key != "" {
		sourceID += ":" + key
	}
	p.add(collection, sourceID, at, row)
}

// ProjectCanonical consumes only the collector's safe envelope, never raw
// artifact/session bytes. Observation time is recorded upstream or supplied by
// the durable publication, so replay never invents a fresh observation.
func ProjectCanonical(ctx context.Context, evidence Evidence, published time.Time, limits Limits) (Projection, error) {
	if err := ctx.Err(); err != nil {
		return Projection{}, err
	}
	content, _, err := evidence.Bytes()
	if err != nil {
		return Projection{}, err
	}
	limits = limits.defaults()
	if len(content) > limits.Bytes || published.IsZero() {
		return Projection{}, ErrCapacity
	}
	var persisted Evidence
	if err := decode(content, &persisted); err != nil {
		return Projection{}, err
	}
	input := persisted.Run
	i := persisted.Identity
	if repositoryOf(input) != i.Repository || scalar(input["run_id"]) != strconv.FormatInt(i.RunID, 10) ||
		scalar(input["run_attempt"]) != strconv.Itoa(i.Attempt) || !workflowPath.MatchString(text(input["workflow_path"])) {
		return Projection{}, ErrIdentity
	}
	at := eventClock(first(input["updated_at"], input["created_at"]))
	if at == "" {
		at = published.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	session := object(input["cao_unified_session"])
	if session != nil && scalar(session["version"]) != "1" {
		return Projection{}, errors.New("unsupported safe session envelope")
	}
	for _, name := range []string{"experiments", "graders", "evals"} {
		if value, present := session[name]; present {
			input[name] = value
		}
	}
	metadata := object(session["run"])
	for _, name := range []string{"engine_id", "agent_version", "cli_version", "firewall_version", "gateway_version",
		"model", "requested_model", "resolved_model", "safe_outputs", "safe_items_count", "token_usage_summary"} {
		if value, present := metadata[name]; present {
			input[name] = value
		}
	}
	owner, name, _ := strings.Cut(i.Repository, "/")
	sourcePath := strings.TrimSuffix(strings.ToLower(text(input["workflow_path"])), ".lock.yml") + ".md"
	repositoryID := "repository:" + model.EncodeCoordinate(i.Repository)
	workflowID := "workflow:" + model.EncodeCoordinate(i.Repository+":"+sourcePath)
	p := Projection{Collections: map[string][]model.Row{}, RunID: fmt.Sprintf("github:run:%s:%d", i.Repository, i.RunID), Identity: i}
	workflowName := firstText(input["workflow_name"], input["workflow_path"])
	p.add("repositories", "repository:"+i.Repository, at, model.Row{"id": repositoryID, "owner": owner, "name": name, "visibility": "unknown"})
	p.add("workflows", "workflow:"+i.Repository+":path:"+sourcePath, at, model.Row{"id": workflowID, "repositoryId": repositoryID,
		"name": workflowName, "path": sourcePath, "state": "unknown"})
	run := model.Row{"id": p.RunID, "repositoryId": repositoryID, "workflowId": workflowID, "owner": owner, "repository": name,
		"workflowPath": sourcePath, "githubRunId": strconv.FormatInt(i.RunID, 10), "attempt": i.Attempt,
		"title": firstText(input["display_title"], fmt.Sprintf("Run %d", i.RunID)), "event": firstText(input["event"], input["event_name"], "unknown"),
		"status": firstText(input["status"], "unknown"), "conclusion": nullableText(input["conclusion"]),
		"createdAt": nullableText(input["created_at"]), "startedAt": nullableText(input["started_at"]), "completedAt": nil,
		"updatedAt": nullableText(input["updated_at"]), "runLink": nullableText(input["url"]),
		"agentId":      firstText(input["agent_id"], input["engine_id"], "copilot"),
		"agentVersion": nullableText(input["agent_version"]),
		"modelId":      firstText(input["resolved_model"], input["model"], "auto"), "ghAwVersion": nullableText(input["cli_version"]),
		"engine": firstText(input["engine"], "unknown")}
	if input["status"] == "completed" {
		run["completedAt"] = run["updatedAt"]
	}
	for from, to := range map[string]string{"head_branch": "branch", "head_sha": "headSha", "engine_id": "engineId",
		"firewall_version": "firewallVersion", "gateway_version": "gatewayVersion"} {
		putText(run, to, input[from])
	}
	putText(run, "requestedModel", input["requested_model"], input["model"])
	putText(run, "resolvedModel", input["resolved_model"], input["model"])
	usage := object(input["token_usage_summary"])
	for from, to := range map[string]string{"total_aic": "aicTotal", "total_input_tokens": "inputTokens",
		"total_output_tokens": "outputTokens", "total_cache_read_tokens": "cacheReadTokens",
		"total_cache_write_tokens": "cacheWriteTokens", "total_reasoning_tokens": "reasoningTokens"} {
		run[to] = canonicalNumber(usage[from])
	}
	for _, field := range []string{"actionMinutes", "githubApiCalls", "safeItemsCount", "errorCount", "agenticDurationSeconds",
		"firewallAllowedCalls", "firewallBlockedCalls", "mcpToolCalls", "mcpResponseBytes", "operationalGrader",
		"highPriorityAuditItems", "mediumPriorityAuditItems"} {
		run[field] = nil
	}
	run["safeItemsCount"] = canonicalNumber(input["safe_items_count"])
	started, completed := eventClock(first(input["started_at"], input["created_at"])), eventClock(run["completedAt"])
	if started != "" && completed != "" {
		start, _ := time.Parse(time.RFC3339Nano, started)
		end, _ := time.Parse(time.RFC3339Nano, completed)
		run["agenticDurationSeconds"] = max(0, end.Sub(start).Seconds())
	}
	p.add("runs", fmt.Sprintf("run:%d:%d", i.RunID, i.Attempt), at, run)
	events := array(session["events"])
	if len(events)+len(persisted.Gaps) > limits.Entries {
		return Projection{}, ErrCapacity
	}
	for _, candidate := range events {
		if err := ctx.Err(); err != nil {
			return Projection{}, err
		}
		event := object(candidate)
		line, valid := number(event["line"])
		fields := object(event["fields"])
		if !valid || line <= 0 || integer(event["line"]) == nil || fields == nil || text(event["source"]) == "" || text(event["type"]) == "" {
			return Projection{}, errors.New("invalid safe session event")
		}
		if line > float64(limits.Entries) {
			return Projection{}, ErrCapacity
		}
		if value, present := event["timestamp"]; present {
			timestamp := eventClock(value)
			if timestamp == "" {
				return Projection{}, errors.New("invalid safe session event clock")
			}
			fields["timestamp"] = timestamp
		}
		p.event(text(session["path"]), int(line), text(event["key"]), firstText(fields["timestamp"], at),
			text(event["source"]), text(event["type"]), fields)
	}
	if err := p.evaluations(ctx, input, workflowID, at, limits); err != nil {
		return Projection{}, err
	}
	p.runEvents(input, at)
	for _, gap := range persisted.Gaps {
		if !gapCode.MatchString(gap) {
			return Projection{}, errors.New("invalid native evidence gap classification")
		}
		coordinate := fmt.Sprintf("%s:%d:%s", p.RunID, i.Attempt, gap)
		observed := published.UTC().Format("2006-01-02T15:04:05.000Z")
		row := model.Row{"id": "audit:cao-native:" + model.EncodeCoordinate(coordinate), "runId": p.RunID,
			"source": "cao-native", "type": "collection.coverage", "code": gap,
			"status": "unavailable", "summary": "Native collection evidence unavailable"}
		p.add("audits", coordinate, observed, row)
		row["provenance"] = model.Row{"source": "cao-native", "sourceId": coordinate, "observedAt": observed}
	}
	contract, err := CanonicalContract()
	if err != nil {
		return Projection{}, err
	}
	for collection, rows := range p.Collections {
		for _, row := range rows {
			for _, field := range contract.PrunedFields[collection] {
				delete(row, field)
			}
		}
		p.Collections[collection] = convergeRecords(collection, rows)
		// Native storage retains sourceSequence; sequence is reconstructed by
		// the canonical query rather than persisted as a second ordering fact.
		for _, row := range p.Collections[collection] {
			delete(row, "sequence")
		}
		slices.SortFunc(p.Collections[collection], func(a, b model.Row) int {
			return strings.Compare(text(a["id"]), text(b["id"]))
		})
	}
	total, bytes := 0, int64(0)
	for _, rows := range p.Collections {
		total += len(rows)
		bytes += query.EstimateRowsBytes(rows)
	}
	if total > limits.Entries || bytes > int64(limits.Bytes) {
		return Projection{}, ErrCapacity
	}
	return p, ctx.Err()
}

var gapCode = regexp.MustCompile(`^[a-z][a-z0-9-]{0,95}$`)

// Activity's stable IDs hash JavaScript UTF-16 code units, not UTF-8 bytes.
func activityDigest(value any) string {
	content, _ := json.Marshal(value)
	hash := uint32(0x811c9dc5)
	for _, unit := range utf16.Encode([]rune(string(content))) {
		hash = (hash ^ uint32(unit)) * 0x01000193
	}
	return fmt.Sprintf("%08x", hash)
}

func (p *Projection) evaluations(ctx context.Context, input map[string]any, workflow, at string, limits Limits) error {
	assignments := object(object(input["experiments"])["assignments"])
	for name, value := range assignments {
		if optionalText(name) == "" || optionalText(value) == "" {
			return errors.New("invalid experiment assignment")
		}
		experimentID := nativeID("experiment", workflow+":"+name)
		p.add("experiments", workflow+":experiment:"+name, at, model.Row{"id": experimentID, "workflowId": workflow, "name": name, "timestamp": at})
		p.add("experimentAssignments", p.RunID+":experiment:"+name, at, model.Row{"id": nativeID("experiment-assignment", p.RunID+":"+name),
			"runId": p.RunID, "experimentId": experimentID, "variant": value, "timestamp": at})
	}
	if len(assignments)+len(array(input["evals"]))+len(array(object(input["graders"])["results"])) > limits.Entries {
		return ErrCapacity
	}
	for index, candidate := range array(object(input["graders"])["results"]) {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := object(candidate)
		name := optionalText(record["id"])
		if name == "" {
			continue
		}
		resultAt := at
		if value, present := record["timestamp"]; present {
			resultAt = eventClock(value)
			if resultAt == "" {
				continue
			}
		}
		id := nativeID("grader", workflow+":"+name)
		definition := model.Row{"id": id, "workflowId": workflow, "name": name, "sourceGraderId": name,
			"displayName": firstText(record["name"], name), "threshold": canonicalNumber(record["threshold"]), "timestamp": resultAt}
		putText(definition, "unit", record["unit"])
		putText(definition, "direction", record["direction"])
		p.add("graders", workflow+":grader:"+name, resultAt, definition)
		status := optionalText(record["status"])
		if status == "" {
			status = "unavailable"
			if passed, ok := record["passed"].(bool); ok {
				status = map[bool]string{true: "passed", false: "failed"}[passed]
			}
		}
		identity := struct {
			Type   string         `json:"type"`
			Index  int            `json:"index"`
			Record map[string]any `json:"record"`
		}{"grader", index, record}
		row := model.Row{"id": nativeID("grader-observation", fmt.Sprintf("%s:%s:%d", p.RunID, name, index)),
			"runId": p.RunID, "graderId": id, "value": canonicalNumber(record["value"]), "status": status, "timestamp": resultAt,
			"auditId": nativeID("audit", p.RunID+":agentic:workflow_run_grader:"+activityDigest(identity))}
		for from, to := range map[string]string{"included": "included", "source": "graderSource", "metrics": "metrics"} {
			if value, present := record[from]; present {
				row[to] = value
			}
		}
		putText(row, "exclusionReason", record["exclusionReason"], record["exclusion_reason"])
		putText(row, "evaluatorDigest", record["evaluatorDigest"], record["evaluator_digest"])
		p.linkExperiment(row, record, assignments, workflow)
		p.add("graderObservations", fmt.Sprintf("%s:grader:%s:%d", p.RunID, name, index), resultAt, row)
	}
	for index, candidate := range array(input["evals"]) {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := object(candidate)
		name, answer := optionalText(record["id"]), strings.ToUpper(optionalText(record["answer"]))
		if name == "" || !slices.Contains([]string{"YES", "NO", "UNKNOWN"}, answer) {
			continue
		}
		if !p.evalIdentity(record) {
			continue
		}
		resultAt := at
		if value, present := record["timestamp"]; present {
			resultAt = eventClock(value)
			if resultAt == "" {
				continue
			}
		}
		id := nativeID("eval", workflow+":"+name)
		p.add("evals", workflow+":eval:"+name, resultAt, model.Row{"id": id, "workflowId": workflow, "name": name, "sourceEvalId": name, "timestamp": resultAt})
		identity := struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Name  string `json:"name"`
		}{"eval", index, name}
		row := model.Row{"id": nativeID("eval-observation", fmt.Sprintf("%s:%s:%d", p.RunID, name, index)),
			"runId": p.RunID, "evalId": id, "evalResult": answer, "timestamp": resultAt,
			"status":  map[string]string{"YES": "pass", "NO": "fail", "UNKNOWN": "unavailable"}[answer],
			"auditId": nativeID("audit", p.RunID+":agentic:workflow_run_eval:"+activityDigest(identity))}
		p.linkExperiment(row, record, assignments, workflow)
		p.add("evalObservations", fmt.Sprintf("%s:eval:%s:%d", p.RunID, name, index), resultAt, row)
	}
	return nil
}

func (p *Projection) linkExperiment(row model.Row, record, assignments map[string]any, workflow string) {
	name := firstText(record["experimentId"], record["experiment_id"])
	assigned, exists := assignments[name]
	if name != "" && exists && optionalText(assigned) != "" && (record["variant"] == nil || record["variant"] == assigned) {
		row["experimentId"] = nativeID("experiment", workflow+":"+name)
		row["variant"] = assigned
	}
}

func (p *Projection) evalIdentity(record map[string]any) bool {
	for _, field := range []string{"runId", "run_id", "runid"} {
		if value, present := record[field]; present && scalar(value) != strconv.FormatInt(p.Identity.RunID, 10) && scalar(value) != p.RunID {
			return false
		}
	}
	for _, field := range []string{"runAttempt", "run_attempt"} {
		if value, present := record[field]; present && scalar(value) != strconv.Itoa(p.Identity.Attempt) {
			return false
		}
	}
	return true
}

func convergeRecords(collection string, rows []model.Row) []model.Row {
	slices.SortStableFunc(rows, func(a, b model.Row) int {
		if order := strings.Compare(text(a["observedAt"]), text(b["observedAt"])); order != 0 {
			return order
		}
		aSource, _ := a["provenance"].(model.Row)
		bSource, _ := b["provenance"].(model.Row)
		return strings.Compare(text(aSource["sourceId"]), text(bSource["sourceId"]))
	})
	byID := map[string]model.Row{}
	for _, row := range rows {
		id := text(row["id"])
		if previous := byID[id]; previous != nil {
			firstObserved := previous["firstObservedAt"]
			merge(previous, row)
			if firstObserved != nil {
				previous["firstObservedAt"] = firstObserved
			}
		} else {
			byID[id] = row
		}
	}
	result := make([]model.Row, 0, len(byID))
	for _, row := range byID {
		result = append(result, row)
	}
	slices.SortFunc(result, func(a, b model.Row) int { return strings.Compare(text(a["id"]), text(b["id"])) })
	if slices.Contains([]string{"domains", "tools", "skills", "friction", "audits", "issues"}, collection) {
		slices.SortStableFunc(result, func(a, b model.Row) int {
			left, right := numericSequence(a["sourceSequence"]), numericSequence(b["sourceSequence"])
			if a["source"] == b["source"] && left != right {
				return left - right
			}
			return strings.Compare(sequenceClock(a["timestamp"]), sequenceClock(b["timestamp"]))
		})
		for index, row := range result {
			row["sequence"] = index
		}
	}
	return result
}

func numericSequence(value any) int {
	if n, ok := value.(int); ok {
		return n
	}
	n, _ := number(value)
	return int(n)
}

func sequenceClock(value any) string {
	if value == nil {
		return "undefined"
	}
	return fmt.Sprint(value)
}
