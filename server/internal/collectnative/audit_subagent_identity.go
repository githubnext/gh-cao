package collectnative

import (
	"context"
	"errors"
	"strings"
)

// Pinned token_usage_subagent_identity.go links SDK accounting only through a
// unique native start, never through an agent name or a guessed model.
func bridgeAuditSubagentIdentity(ctx context.Context, state *auditSubagentScope, files map[string][]byte, identity Identity, limits Limits) ([]string, error) {
	if len(candidates(files, "aw_session.jsonl")) == 0 || len(candidates(files, "agent-session.jsonl")) == 0 {
		return nil, nil
	}
	native, gaps, err := readAuditSubagentScope(ctx, files, identity, limits, true)
	if err != nil || native == nil || len(gaps) != 0 {
		return gaps, err
	}
	type start struct {
		id  string
		row *auditSubagent
	}
	nativeStarts, unifiedStarts := map[string][]start{}, map[string][]start{}
	for id, row := range native.agents {
		if row.start != nil && row.startCount == 1 && text(row.start["agentId"]) != "" {
			tool := text(object(row.start["data"])["toolCallId"])
			if tool != "" {
				nativeStarts[tool] = append(nativeStarts[tool], start{id, row})
			}
		}
	}
	for id, row := range state.agents {
		if row.start != nil && row.startCount == 1 && text(row.start["agentId"]) == "" {
			tool := text(object(row.start["data"])["toolCallId"])
			if tool != "" {
				unifiedStarts[tool] = append(unifiedStarts[tool], start{id, row})
			}
		}
	}
	type link struct {
		from, to string
		effort   string
	}
	links, claims := []link{}, map[string]int{}
	for tool, unified := range unifiedStarts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw := nativeStarts[tool]
		if len(unified) != 1 || len(raw) != 1 {
			continue
		}
		from, to := unified[0], raw[0]
		if from.id == to.id || len(from.row.observed) != 0 {
			continue
		}
		target := state.agents[to.id]
		if target != nil && (target.invoked || target.completed != 0 || target.failed != 0 ||
			target.effort != "" || target.resolved != "") {
			continue
		}
		nativeData, data := object(to.row.start["data"]), object(from.row.start["data"])
		conflict := false
		for _, key := range []string{"agentName", "agentDisplayName"} {
			left, right := text(data[key]), text(nativeData[key])
			if left != "" && right != "" && left != right {
				conflict = true
			}
		}
		if target != nil && target.name != "" && target.name != firstText(data["agentName"], data["agentDisplayName"]) &&
			target.name != text(data["agentDisplayName"]) {
			conflict = true
		}
		left, leftErr := subagentAuditTime(from.row.start)
		right, rightErr := subagentAuditTime(to.row.start)
		if leftErr != nil || rightErr != nil || (!left.IsZero() && !right.IsZero() && !left.Equal(right)) {
			conflict = true
		}
		provenance := object(object(from.row.start["provenance"])["native"])
		if name := text(provenance["persistedPath"]); name != "" &&
			strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./") != "agent-session.jsonl" {
			conflict = true
		}
		index := provenance["index"]
		nativeIndex := object(to.row.start["provenance"])["index"]
		if index != nil && nativeIndex != nil && scalar(index) != scalar(nativeIndex) {
			conflict = true
		}
		if conflict {
			continue
		}
		claims[to.id]++
		links = append(links, link{from.id, to.id, to.row.effort})
	}
	for _, link := range links {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if claims[link.to] != 1 {
			continue
		}
		source, target := state.agents[link.from], state.agents[link.to]
		if source == nil {
			return nil, errors.New("invalid subagent identity bridge")
		}
		merged := *source
		if target != nil {
			merged.observed = target.observed
			merged.name = firstText(merged.name, target.name)
			merged.requested = firstText(merged.requested, target.requested)
			merged.resolved = firstText(merged.resolved, target.resolved)
		}
		merged.effort = firstText(merged.effort, link.effort)
		state.agents[link.to] = &merged
		delete(state.agents, link.from)
	}
	return nil, nil
}
