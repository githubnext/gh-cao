package collectnative

import (
	"fmt"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func (p *Projection) runEvents(input map[string]any, at string) {
	started := firstText(eventClock(first(input["started_at"], input["created_at"])), at)
	closed := at
	if input["status"] == "completed" {
		closed = firstText(eventClock(input["updated_at"]), at)
	}
	sequence := 1000
	emit := func(kind, clock, summary, status, source string, identity any, fields map[string]any) {
		row := model.Row{"runId": p.RunID, "type": kind, "timestamp": clock, "source": source, "summary": summary,
			"payloadRef": "gh-aw-logs-shards#L1", "sourceSequence": sequence}
		if status != "" {
			row["status"] = status
		}
		merge(row, fields)
		id := p.RunID + ":agentic:" + kind + ":" + activityDigest(identity)
		switch kind {
		case "workflow_run_grader":
			row["type"] = "grader.result"
		case "workflow_run_eval":
			row["type"] = "eval.result"
		case "safe_output.created":
			row["type"] = "safe_output.result"
		}
		collection := canonicalKind(text(row["type"]), source, fields)
		if collection == "issues" {
			merge(row, issueFields(text(fields["correlationId"]), fields))
		}
		if source != "agent" || kind != "agent.session" || status != "completed" || summary != "" {
			p.add(collection, id, at, row)
		}
		sequence++
	}
	emit("workflow_run_started", started, joined(input["workflow_name"], input["display_title"]), text(input["status"]), "gh-aw-logs",
		struct {
			Type    string `json:"type"`
			Started string `json:"startedAt"`
		}{"started", started}, nil)
	emit("agent.session", started, joined(input["engine"], input["model"]), text(input["status"]), "agent",
		struct {
			Type   string `json:"type"`
			Engine any    `json:"engine,omitempty"`
			Model  any    `json:"model,omitempty"`
		}{"agent-session", input["engine_id"], input["model"]}, nil)
	if input["status"] == "completed" {
		emit("workflow_run_completed", closed, firstText(input["classification"], input["conclusion"], "completed"),
			firstText(input["conclusion"], input["status"]), "gh-aw-logs", struct {
				Type       string `json:"type"`
				Completed  string `json:"completedAt"`
				Conclusion any    `json:"conclusion,omitempty"`
			}{"completed", closed, input["conclusion"]}, nil)
	}
	if input["conclusion"] == "failure" {
		emit("workflow_run_failed", closed, firstText(input["failure_kind"], "workflow run failed"), "failure", "gh-aw-logs",
			struct {
				Type    string `json:"type"`
				Failure any    `json:"failureKind,omitempty"`
				Errors  any    `json:"errorCount,omitempty"`
			}{"failure", input["failure_kind"], input["error_count"]}, nil)
	}
	if usage := input["token_usage_summary"]; usage != nil {
		total := canonicalNumber(object(usage)["total_aic"])
		summary := "unknown"
		if total != nil {
			summary = fmt.Sprint(total)
		}
		emit("workflow_run_usage", closed, "AIC "+summary, "observed", "gh-aw-logs",
			struct {
				Type  string `json:"type"`
				Usage any    `json:"tokenUsage"`
			}{"usage", usage}, nil)
	}
	for index, candidate := range array(object(input["graders"])["results"]) {
		record := object(candidate)
		status := optionalText(record["status"])
		if status == "" {
			if passed, ok := record["passed"].(bool); ok {
				status = map[bool]string{true: "passed", false: "failed"}[passed]
			}
		}
		fields := map[string]any{"grader": firstText(record["id"], fmt.Sprintf("grader-%d", index+1)),
			"value": canonicalNumber(record["value"]), "baselineValue": canonicalNumber(record["baselineValue"]),
			"deltaFromBaseline": canonicalNumber(record["deltaFromBaseline"])}
		for from, to := range map[string]string{"name": "graderName", "source": "graderSource", "unit": "unit", "direction": "direction",
			"message": "message", "error": "error", "implementation": "implementation", "observation": "observation",
			"metrics": "metrics", "diagnostics": "diagnostics", "sessionPayloadRef": "payloadRef"} {
			if value, present := record[from]; present {
				fields[to] = value
			}
		}
		before := len(p.Collections["audits"])
		emit("workflow_run_grader", closed, firstText(record["name"], record["id"], fmt.Sprintf("Grader %d", index+1)), status, "grader",
			struct {
				Type   string         `json:"type"`
				Index  int            `json:"index"`
				Record map[string]any `json:"record"`
			}{"grader", index, record}, fields)
		if record["sessionPayloadRef"] == nil {
			delete(p.Collections["audits"][before], "payloadRef")
		}
	}
	for index, candidate := range array(input["evals"]) {
		record := object(candidate)
		name, answer := optionalText(record["id"]), strings.ToUpper(optionalText(record["answer"]))
		classified := name != "" && (answer == "YES" || answer == "NO" || answer == "UNKNOWN") && p.evalIdentity(record)
		eventAt := at
		if value, present := record["timestamp"]; present {
			eventAt = eventClock(value)
			classified = classified && eventAt != ""
		}
		fields := map[string]any{"evidenceState": "classified"}
		kind, summary, status := "workflow_run_eval", "Evaluation "+name, map[string]string{"YES": "pass", "NO": "fail", "UNKNOWN": "unavailable"}[answer]
		var identity any = struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Name  string `json:"name"`
		}{"eval", index, name}
		if classified {
			fields["evalId"], fields["answer"] = name, answer
		} else {
			fields["evidenceState"] = "unclassified"
			kind, summary, status, eventAt = "workflow_run_eval_unclassified", "Evaluation evidence is not classified", "unavailable", closed
			identity = struct {
				Type  string `json:"type"`
				Index int    `json:"index"`
			}{"unclassified-eval", index}
		}
		if payload := optionalText(record["sessionPayloadRef"]); payload != "" {
			fields["payloadRef"] = payload
		}
		before := len(p.Collections["audits"])
		emit(kind, eventAt, summary, status, "audit", identity, fields)
		if record["sessionPayloadRef"] == nil {
			delete(p.Collections["audits"][before], "payloadRef")
		}
	}
	for index, candidate := range array(input["safe_outputs"]) {
		record := object(candidate)
		fields := map[string]any{"payloadRef": firstText(record["sessionPayloadRef"], "gh-aw-logs-shards#L1")}
		for from, to := range map[string]string{"url": "correlationId", "type": "safeOutputType"} {
			if value, present := record[from]; present {
				fields[to] = value
			}
		}
		if entity := safeOutputEntity(record); entity != "" {
			fields["githubEntityType"] = entity
		}
		clock := firstText(eventClock(record["timestamp"]), closed)
		summary := joined(firstText(record["type"], "safe output"), record["repo"], record["number"])
		if record["type"] == "report_incomplete" {
			summary = firstText(record["reason"], "Required evidence was unavailable.")
		}
		emit("safe_output.created", clock, summary, "created", "safe-output",
			struct {
				Type   string `json:"type"`
				Index  int    `json:"index"`
				Output any    `json:"outputType,omitempty"`
				URL    any    `json:"url,omitempty"`
			}{"safe-output", index, record["type"], record["url"]}, fields)
	}
	if count, present := input["safe_items_count"]; present {
		emit("workflow_run_safe_outputs", closed, fmt.Sprint(count)+" safe output items", "observed", "gh-aw-logs",
			struct {
				Type  string `json:"type"`
				Count any    `json:"count"`
			}{"safe-outputs", count}, nil)
	}
}

func safeOutputEntity(record map[string]any) string {
	if provider, exists := record["provider"]; exists && !strings.EqualFold(text(provider), "github") {
		return ""
	}
	if kind := optionalText(object(record["target"])["kind"]); kind != "" {
		return strings.ReplaceAll(strings.ToLower(kind), "-", "_")
	}
	fields := map[string]any{"githubEntityType": "issue"}
	if row := issueFields(text(record["url"]), fields); row != nil {
		if strings.Contains(text(record["url"]), "/pull/") {
			return "pull_request"
		}
		return "issue"
	}
	switch strings.ToLower(optionalText(record["type"])) {
	case "create_issue", "update_issue", "close_issue":
		return "issue"
	case "create_pull_request", "update_pull_request", "close_pull_request":
		return "pull_request"
	}
	return ""
}

func joined(values ...any) string {
	var parts []string
	for _, value := range values {
		if s := optionalText(value); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}
