package collectnative

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

type auditSubagent struct {
	name, requested, resolved, effort string
	completed, failed                 int
	invoked                           bool
	observed                          map[string]int
	start                             map[string]any
	startCount                        int
}

type auditSubagentScope struct {
	agents      map[string]*auditSubagent
	main        map[string]int
	active      map[string]string
	sequence    map[string]int
	tools       map[string]string
	unavailable []any
	seen        map[string]bool
	engine      string
}

func newAuditSubagentScope() *auditSubagentScope {
	return &auditSubagentScope{agents: map[string]*auditSubagent{}, main: map[string]int{},
		active: map[string]string{}, sequence: map[string]int{}, tools: map[string]string{}, seen: map[string]bool{}}
}

func (s *auditSubagentScope) agent(id string) *auditSubagent {
	if s.agents[id] == nil {
		s.agents[id] = &auditSubagent{observed: map[string]int{}}
	}
	return s.agents[id]
}

func subagentAuditTime(event map[string]any) (time.Time, error) {
	value := event["timestamp"]
	if value == nil || value == "" {
		return time.Time{}, nil
	}
	if raw, ok := value.(string); ok {
		return time.Parse(time.RFC3339Nano, raw)
	}
	n, ok := number(value)
	if !ok || n < 0 || n > 9007199254740991 {
		return time.Time{}, ErrIdentity
	}
	if object(event["provenance"])["timestampUnit"] == "seconds" {
		n *= 1000
	}
	return time.UnixMilli(int64(n)), nil
}

func (s *auditSubagentScope) piIdentity(event, data map[string]any, dispatch bool) string {
	if id := firstText(data["invocationId"], event["agentId"]); id != "" {
		return id
	}
	name := firstText(data["agent"], data["agentName"], "unknown")
	if !dispatch && s.active[name] != "" {
		return s.active[name]
	}
	s.sequence[name]++
	id := fmt.Sprintf("legacy:%s:%d", name, s.sequence[name])
	s.active[name] = id
	return id
}

// Only the state needed for canonical findings is retained; request payloads,
// error messages, prompts, and per-request content never enter the envelope.
func (s *auditSubagentScope) observe(event map[string]any, work *int, limits Limits) error {
	kind, data := text(event["type"]), object(event["data"])
	if data["agentMetrics"] != nil && object(data["agentMetrics"]) == nil {
		return ErrIdentity
	}
	if kind == "session.init" && (data["sourceEngine"] == "pi" || data["sourceEngine"] == "copilot") {
		if text(data["sessionId"]) == "" && text(event["agentId"]) == "" {
			*s = *newAuditSubagentScope()
		}
		s.engine = text(data["sourceEngine"])
	}
	if metrics := object(data["agentMetrics"]); metrics != nil {
		s.main = map[string]int{}
		for _, row := range s.agents {
			row.observed = map[string]int{}
		}
		for id, value := range metrics {
			*work++
			if *work > limits.defaults().Entries {
				return ErrCapacity
			}
			metric := object(value)
			models := object(metric["modelMetrics"])
			if metric == nil || (metric["modelMetrics"] != nil && models == nil) {
				return ErrIdentity
			}
			row := s.agent(id)
			if row.name == "" {
				row.name = firstText(metric["agentDisplayName"], metric["agentName"], id)
			}
			for model, value := range models {
				*work++
				if *work > limits.defaults().Entries {
					return ErrCapacity
				}
				metric := object(value)
				requests := object(metric["requests"])
				if metric == nil || (metric["requests"] != nil && requests == nil) {
					return ErrIdentity
				}
				count := requests["count"]
				if count != nil && (integer(count) == nil || nonnegative(count) == nil) {
					return ErrIdentity
				}
				if id == "main" {
					s.main[model] = engineCounter(count)
				} else {
					row.observed[model] = engineCounter(count)
					if row.requested == "" && len(models) == 1 {
						row.requested = model
					}
				}
			}
		}
	}
	if kind == "subagent.model_unavailable" {
		agent, declared, inherited := identifier(data["agentName"]), modelName(data["declaredModel"]), modelName(data["model"])
		if agent == nil || declared == nil || inherited == nil {
			return ErrIdentity
		}
		key := fmt.Sprintf("%s\x00%s\x00%s", agent, declared, inherited)
		if !s.seen[key] {
			s.seen[key] = true
			s.unavailable = append(s.unavailable, auditFinding("subagent_model_unavailable", "tooling", "medium",
				"Sub-agent Model Unavailable", fmt.Sprintf("Sub-agent %s declared model %s was unavailable; used inherited session model %s", agent, declared, inherited)))
		}
		return nil
	}
	if strings.HasPrefix(kind, "pi.subagent_") {
		id := s.piIdentity(event, data, kind == "pi.subagent_dispatch")
		row := s.agent(id)
		row.invoked = true
		if row.name == "" {
			row.name = firstText(data["agent"], data["agentName"], "unknown")
		}
		switch kind {
		case "pi.subagent_dispatch":
			row.requested, row.resolved = text(data["requestedModel"]), text(data["resolvedModel"])
		case "pi.subagent_event":
			payload := object(data["event"])
			if payload == nil {
				return ErrIdentity
			}
			message := object(payload["message"])
			usage := object(message["usage"])
			failed := message["stopReason"] == "error" || message["stopReason"] == "aborted" || text(message["errorMessage"]) != ""
			if failed && engineCounter(usage["input"])+engineCounter(usage["output"])+engineCounter(usage["cacheRead"])+engineCounter(usage["cacheWrite"]) == 0 {
				return nil
			}
			if model := firstText(message["responseModel"], message["model"]); model != "" {
				row.observed[model]++
			}
		case "pi.subagent_result":
			outcome := text(data["outcome"])
			_, hasError := data["error"]
			if outcome == "failed" || strings.HasPrefix(outcome, "SUBAGENT_") || hasError || text(data["errorMessage"]) != "" {
				row.failed = 1
			} else {
				row.completed = 1
			}
			if s.active[text(data["agent"])] == id {
				delete(s.active, text(data["agent"]))
			}
		}
		return nil
	}
	if s.engine == "pi" {
		if kind == "tool.execution_start" && data["toolName"] == "subagent" {
			if name := text(object(data["input"])["agent"]); name != "" && text(data["toolCallId"]) != "" {
				s.tools[text(data["toolCallId"])] = name
			}
		}
		if kind == "tool.execution_complete" {
			name := s.tools[text(data["toolCallId"])]
			delete(s.tools, text(data["toolCallId"]))
			if id := s.active[name]; name != "" && id != "" {
				if success, ok := data["success"].(bool); ok {
					row := s.agent(id)
					if success {
						row.completed = 1
					} else {
						row.failed = 1
					}
					delete(s.active, name)
				}
			}
		}
	}
	if !slices.Contains([]string{"subagent.started", "subagent.configured", "subagent.completed", "subagent.failed", "subagent.request"}, kind) {
		return nil
	}
	id := firstText(event["agentId"], data["invocationId"], data["toolCallId"])
	if kind == "subagent.request" {
		id = firstText(event["agentId"], data["invocationId"])
	}
	if id == "" {
		if kind == "subagent.request" {
			return ErrIdentity
		}
		return nil
	}
	if kind == "subagent.request" {
		if text(data["model"]) == "" {
			return nil
		}
		for _, key := range []string{"inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens"} {
			if value := data[key]; value != nil && (integer(value) == nil || nonnegative(value) == nil) {
				return ErrIdentity
			}
		}
		if _, err := subagentAuditTime(event); err != nil {
			return err
		}
	}
	row := s.agent(id)
	row.invoked = true
	if name := firstText(data["agentDisplayName"], data["agentName"]); name != "" &&
		(data["agentDisplayName"] != nil || row.name == "") {
		row.name = name
	}
	switch kind {
	case "subagent.started":
		row.startCount++
		row.requested, row.resolved, row.effort = text(data["model"]), text(data["resolvedModel"]), text(data["reasoningEffort"])
		row.start = event
	case "subagent.configured":
		if model := text(data["model"]); model != "" {
			row.resolved = model
		}
		if effort := text(data["reasoningEffort"]); effort != "" {
			row.effort = effort
		}
	case "subagent.request":
		if model := text(data["model"]); model != "" {
			row.observed[model]++
		}
	case "subagent.completed":
		if data["cancelled"] != true || row.completed+row.failed == 0 {
			row.completed++
		}
	case "subagent.failed":
		row.failed++
	}
	return nil
}

func readAuditSubagentScope(ctx context.Context, files map[string][]byte, identity Identity, limits Limits, nativeOnly bool) (*auditSubagentScope, []string, error) {
	names := candidates(files, "aw_session.jsonl")
	names = slices.DeleteFunc(names, excludedAuditPath)
	unified := true
	if nativeOnly || len(names) == 0 {
		names, unified = candidates(files, "agent-session.jsonl"), false
		names = slices.DeleteFunc(names, excludedAuditPath)
	}
	if len(names) > 1 {
		return nil, []string{"ambiguous-subagent-audit-session"}, nil
	}
	scopes, sources := map[string]*auditSubagentScope{}, map[string]string{}
	final, finalTime := "", time.Time{}
	work, header := 0, !unified
	gaps := []string{}
	if len(names) == 1 {
		err := scanEvidence(ctx, files[names[0]], limits, func(_ int, raw []byte) error {
			work++
			if work > limits.defaults().Entries {
				return ErrCapacity
			}
			var event map[string]any
			if decode(raw, &event) != nil || !matches(event, identity) || !matches(object(event["data"]), identity) {
				return ErrIdentity
			}
			kind, data, provenance := text(event["type"]), object(event["data"]), object(event["provenance"])
			if !header {
				if kind != "session.format" || provenance["component"] != "collector" || scalar(data["version"]) != "1" {
					return ErrIdentity
				}
				header = true
				return nil
			}
			if unified && kind == "session.format" {
				return ErrIdentity
			}
			if unified && (provenance["component"] != "agent" || provenance["phase"] != "agent") {
				return nil
			}
			source := firstText(object(provenance["native"])["path"], provenance["path"])
			scope := firstText(sources[source], source)
			if (kind == "session.start" || kind == "session.init") && text(event["agentId"]) == "" {
				if session := text(data["sessionId"]); session != "" {
					scope = source + ":" + session
					sources[source] = scope
				}
				start, err := subagentAuditTime(event)
				if err != nil {
					return err
				}
				if finalTime.IsZero() || (!start.IsZero() && !start.Before(finalTime)) {
					final, finalTime = scope, start
				}
			}
			if scopes[scope] == nil {
				scopes[scope] = newAuditSubagentScope()
				if len(scopes) == 1 {
					final = scope
				}
			}
			return scopes[scope].observe(event, &work, limits)
		})
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, nil, err
			}
			return nil, []string{"invalid-subagent-audit-session"}, nil
		}
		if !header {
			return nil, []string{"invalid-subagent-audit-session"}, nil
		}
	}
	state := scopes[final]
	if state == nil {
		state = newAuditSubagentScope()
	}
	return state, gaps, nil
}

func subagentAuditFindings(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]any, []any, []string, error) {
	state, gaps, err := readAuditSubagentScope(ctx, files, identity, limits, false)
	if err != nil || state == nil {
		return nil, nil, gaps, err
	}
	bridgeGaps, err := bridgeAuditSubagentIdentity(ctx, state, files, identity, limits)
	if err != nil {
		return nil, nil, nil, err
	}
	gaps = append(gaps, bridgeGaps...)
	findings := []any{}
	grouped := map[string]int{}
	for _, row := range state.agents {
		if row.failed > 0 && row.name != "" {
			grouped[row.name+"\x00"+row.requested] += row.failed
		}
	}
	failures := 0
	for _, key := range slices.Sorted(maps.Keys(grouped)) {
		count := grouped[key]
		failures += count
		name, _, _ := strings.Cut(key, "\x00")
		findings = append(findings, auditFinding("subagent_failed", "tooling", "high", "Sub-agent Failed",
			fmt.Sprintf("Sub-agent %s failed %d invocation(s)", name, count)))
	}
	declared, err := declaredSubagentAudit(ctx, fields, files, state, limits)
	if err != nil {
		if !errors.Is(err, ErrIdentity) {
			return nil, nil, nil, err
		}
		gaps = append(gaps, "invalid-subagent-model-declaration")
	}
	findings = append(findings, declared...)
	findings = append(findings, state.unavailable...)
	recommendations := []any{}
	if failures > 0 {
		audit := object(fields["audit"])
		if audit == nil {
			audit = map[string]any{}
			fields["audit"] = audit
		}
		audit["subagent_failure_count"] = failures
		recommendations = append(recommendations, auditRecommendation("high",
			"Review failed sub-agent invocations and verify recovery",
			"A successful parent conclusion does not establish completion of failed delegated work"))
	}
	return findings, recommendations, gaps, nil
}

func declaredSubagentAudit(ctx context.Context, fields map[string]any, files map[string][]byte, state *auditSubagentScope, limits Limits) ([]any, error) {
	names := candidates(files, "aw_info.json")
	if len(names) != 1 {
		return nil, ctx.Err()
	}
	var info map[string]any
	if decode(files[names[0]], &info) != nil {
		return nil, ErrIdentity
	}
	declarations := array(info["sub_agent_models"])
	work := len(declarations)
	for _, value := range declarations {
		work += len(array(object(value)["patterns"]))
	}
	if work > limits.defaults().Entries {
		return nil, ErrCapacity
	}
	resolver := newAuditModelResolver(ctx, declarations, limits)
	type group struct {
		name              string
		invoked           bool
		completed, failed int
		models            map[string]bool
	}
	groups := map[string]*group{}
	for _, row := range state.agents {
		work++
		if work > limits.defaults().Entries {
			return nil, ErrCapacity
		}
		key := row.name + "\x00" + row.requested
		if groups[key] == nil {
			groups[key] = &group{name: row.name, models: map[string]bool{}}
		}
		g := groups[key]
		g.invoked = g.invoked || row.invoked
		g.completed, g.failed = g.completed+row.completed, g.failed+row.failed
		for _, model := range slices.Sorted(maps.Keys(row.observed)) {
			if row.observed[model] > 0 {
				g.models[model] = true
				break
			}
		}
	}
	observed := map[string]string{}
	for model, value := range object(object(fields["token_usage_summary"])["by_model"]) {
		work++
		if work > limits.defaults().Entries {
			return nil, ErrCapacity
		}
		if engineCounter(object(value)["requests"]) > 0 {
			observed[model] = text(object(value)["provider"])
		}
	}
	parents := []string{}
	for model, count := range state.main {
		if count > 0 {
			parents = append(parents, model)
		}
	}
	if len(parents) == 0 && text(info["model"]) != "" {
		parents = append(parents, text(info["model"]))
	}
	findings := []any{}
	for _, value := range declarations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		declaration := object(value)
		name, requested := text(declaration["name"]), text(declaration["model"])
		requested, _, _ = strings.Cut(requested, "?")
		if identifier(name) == nil || modelName(requested) == nil {
			return nil, ErrIdentity
		}
		patterns := []string{}
		for _, pattern := range array(declaration["patterns"]) {
			patterns = append(patterns, text(pattern))
		}
		if strings.EqualFold(requested, "inherit") || strings.EqualFold(requested, "inherited") {
			patterns = parents
		} else if len(patterns) == 0 {
			patterns = []string{requested}
		}
		matchesModel := func(model, provider string) (bool, error) {
			for _, pattern := range patterns {
				match, err := resolver.matches(pattern, model, provider)
				if err != nil || match {
					return match, err
				}
			}
			return false, nil
		}
		invoked, matched, completed, failed := false, false, 0, 0
		for _, row := range groups {
			work++
			if work > limits.defaults().Entries {
				return nil, ErrCapacity
			}
			if !strings.EqualFold(row.name, name) || !row.invoked {
				continue
			}
			invoked = true
			completed += row.completed
			failed += row.failed
			if len(row.models) == 1 {
				for model := range row.models {
					match, err := matchesModel(model, "")
					if err != nil {
						return nil, err
					}
					matched = matched || match
				}
			}
		}
		if !invoked {
			for model, provider := range observed {
				match, err := matchesModel(model, provider)
				if err != nil {
					return nil, err
				}
				matched = matched || match
			}
		}
		if matched || (!invoked && len(observed) == 0) || (invoked && failed > 0 && completed == 0) {
			continue
		}
		findings = append(findings, auditFinding("subagent_model_not_observed", "tooling", "medium",
			"Declared Sub-agent Model Not Observed", fmt.Sprintf("No observed model requests match sub-agent %s model %s", name, requested)))
	}
	return findings, nil
}
