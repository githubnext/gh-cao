package collectnative

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func scanEvidence(ctx context.Context, content []byte, limits Limits, visit func(int, []byte) error) error {
	limits = limits.defaults()
	if len(content) > limits.Bytes {
		return ErrCapacity
	}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, min(4096, limits.LineBytes)), limits.LineBytes)
	line := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line++
		if line > limits.Entries {
			return ErrCapacity
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		if err := visit(line, scanner.Bytes()); err != nil {
			return err
		}
	}
	if scanner.Err() != nil {
		return ErrCapacity
	}
	return ctx.Err()
}

func acquireJobs(ctx context.Context, provider Provider, run Run, fields map[string]any, limits Limits, bytesUsed *int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, supported := provider.(JobProvider)
	if !supported {
		return []string{"job-logs-unavailable"}, nil
	}
	jobs := []map[string]any{}
	annotations := []map[string]any{}
	gaps := []string{}
	seen := map[int64]bool{}
	err := source.Jobs(ctx, run, func(job Job) error {
		if job.Identity != run.Identity || job.ID <= 0 || seen[job.ID] ||
			scalar(job.Fields["id"]) != strconv.FormatInt(job.ID, 10) ||
			scalar(job.Fields["run_id"]) != strconv.FormatInt(run.RunID, 10) ||
			scalar(job.Fields["run_attempt"]) != strconv.Itoa(run.Attempt) {
			return ErrIdentity
		}
		seen[job.ID] = true
		if len(seen) > limits.defaults().Entries {
			return ErrCapacity
		}
		*bytesUsed += len(job.Log)
		if *bytesUsed > limits.defaults().Bytes {
			return ErrCapacity
		}
		detail := map[string]any{"id": job.ID, "run_attempt": run.Attempt}
		for _, name := range []string{"name", "status", "conclusion", "created_at", "started_at", "completed_at"} {
			if value, present := job.Fields[name]; present {
				if value != nil {
					if _, ok := value.(string); !ok {
						return errors.New("invalid native job metadata")
					}
				}
				detail[name] = value
			}
		}
		jobs = append(jobs, detail)
		if job.Gap != "" || len(bytes.TrimSpace(job.Log)) == 0 {
			gaps = append(gaps, "job-log-unavailable")
			return nil
		}
		if !utf8.Valid(job.Log) || bytes.IndexByte(job.Log, 0) >= 0 {
			gaps = append(gaps, "invalid-job-log")
			return nil
		}
		recognized := false
		err := scanEvidence(ctx, job.Log, limits, func(line int, content []byte) error {
			clock, message, found := strings.Cut(string(content), " ")
			if !found || eventClock(clock) == "" {
				return nil
			}
			recognized = true
			// Persist the annotation class and location, never stdout or error
			// text (which may contain prompts, tokens or signed URLs).
			kind, title := "", ""
			switch {
			case strings.HasPrefix(message, "##[error]"), strings.HasPrefix(message, "::error"):
				kind, title = "actions_error", "Actions job reported an error"
			case strings.HasPrefix(message, "##[warning]"), strings.HasPrefix(message, "::warning"):
				kind, title = "actions_warning", "Actions job reported a warning"
			}
			if kind != "" {
				annotations = append(annotations, map[string]any{"code": kind, "title": title, "timestamp": eventClock(clock),
					"job_id": job.ID, "line": line})
				if len(annotations) > limits.defaults().Entries {
					return ErrCapacity
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !recognized {
			gaps = append(gaps, "unsupported-job-log")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		gaps = append(gaps, "job-logs-unavailable")
	}
	fields["job_details"] = jobs
	if len(annotations) > 0 {
		fields["actions_annotations"] = annotations
	}
	return gaps, nil
}

// enrichAudit consumes only exact-attempt artifact members admitted by extract.
// Structured usage summaries are the pinned gh-aw fallback, not a reconstruction
// of agent metrics from Actions runner stdout.
func enrichAudit(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gaps := []string{}
	for _, name := range candidates(files, "aw_info.json") {
		var info map[string]any
		if decode(files[name], &info) != nil {
			continue
		}
		for native, retained := range map[string]string{"engine_id": "engine_id", "engine_name": "engine",
			"agent_version": "agent_version", "cli_version": "cli_version", "model": "model", "agent_runtime": "agent_runtime",
			"firewall_version": "firewall_version", "awmg_version": "gateway_version"} {
			if _, present := fields[retained]; !present {
				if value, ok := info[native].(string); ok {
					fields[retained] = value
				}
			}
		}
		if value, ok := first(info["agent_version"], info["version"]).(string); ok {
			if _, present := fields["engine_version"]; !present {
				fields["engine_version"] = value
			}
			if _, present := fields["agent_version"]; !present {
				fields["agent_version"] = value
			}
		}
		if _, present := fields["firewall_version"]; !present {
			if value, ok := info["awf_version"].(string); ok {
				fields["firewall_version"] = value
			}
		}
	}
	names := []string{}
	for _, name := range candidates(files, "summary.json") {
		if path.Base(path.Dir(name)) == "activity" {
			names = append(names, name)
		}
	}
	switch {
	case len(names) > 1:
		gaps = append(gaps, "ambiguous-audit-summary")
	case len(names) == 1:
		var summary map[string]any
		if decode(files[names[0]], &summary) != nil || summary["schema"] != "usage-activity-summary/v1" {
			gaps = append(gaps, "invalid-audit-summary")
		} else if err := applyAuditSummary(ctx, fields, summary, limits); err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, err
			}
			gaps = append(gaps, "invalid-audit-summary")
		} else {
			for _, name := range []string{"friction", "ledger", "integrity", "steering"} {
				if summary[name] != nil {
					gaps = append(gaps, "legacy-"+name+"-unavailable")
				}
			}
		}
	default:
		gaps = append(gaps, "audit-summary-unavailable")
	}
	if names := candidates(files, "token-usage.jsonl"); len(names) > 1 {
		gaps = append(gaps, "ambiguous-token-usage")
	} else if len(names) == 1 {
		usage, aicMissing, err := legacyTokenUsage(ctx, files[names[0]], identity, limits)
		switch {
		case errors.Is(err, ErrCapacity), ctx.Err() != nil:
			return nil, err
		case err != nil:
			gaps = append(gaps, "invalid-token-usage")
		case usage == nil:
			gaps = append(gaps, "token-usage-unavailable")
		default:
			fields["token_usage_summary"] = usage
			if aicMissing {
				gaps = append(gaps, "legacy-aic-unavailable")
			}
		}
	}
	outputs := candidates(files, "safe-output-items.jsonl")
	if len(outputs) > 1 {
		gaps = append(gaps, "ambiguous-safe-outputs")
	} else if len(outputs) == 1 {
		items := []map[string]any{}
		err := scanEvidence(ctx, files[outputs[0]], limits, func(line int, raw []byte) error {
			var item map[string]any
			if decode(raw, &item) != nil || !matches(item, identity) {
				return ErrIdentity
			}
			safe, err := safeAuditOutput(item)
			if err != nil {
				return err
			}
			safe["sessionPayloadRef"] = fmt.Sprintf("%s#L%d", outputs[0], line)
			items = append(items, safe)
			return nil
		})
		switch {
		case errors.Is(err, ErrCapacity), ctx.Err() != nil:
			return nil, err
		case err != nil:
			gaps = append(gaps, "invalid-safe-outputs")
		default:
			fields["safe_outputs"], fields["safe_items_count"] = items, len(items)
		}
	}
	reportGaps, err := enrichAuditReports(ctx, fields, files, identity, limits)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, reportGaps...)
	start, err := time.Parse(time.RFC3339Nano, text(fields["started_at"]))
	end, endErr := time.Parse(time.RFC3339Nano, text(fields["updated_at"]))
	if err == nil && endErr == nil && !end.Before(start) {
		duration := end.Sub(start)
		fields["action_minutes"] = math.Ceil(duration.Minutes())
		if duration > 0 {
			fields["duration"] = auditDuration(duration)
		}
	}
	// Execution-state and model-startup findings have independent authoritative
	// inputs. Neither makes the remaining upstream rule families complete.
	findings := []any{}
	switch fields["conclusion"] {
	case "failure":
		findings = append(findings, map[string]any{"code": "workflow_failed", "category": "error",
			"severity": "critical", "title": "Workflow Failed"})
	case "timed_out":
		findings = append(findings, map[string]any{"code": "workflow_timeout", "category": "performance",
			"severity": "high", "title": "Workflow Timeout"})
	}
	modelFindings, recommendations, modelGaps, err := modelAuditFindings(ctx, fields, files, identity, limits)
	if err != nil {
		return nil, err
	}
	findings = append(findings, modelFindings...)
	gaps = append(gaps, modelGaps...)
	if len(findings) > 0 {
		audit := object(fields["audit"])
		if audit == nil {
			audit = map[string]any{}
		}
		audit["key_findings"] = findings
		if len(recommendations) > 0 {
			audit["recommendations"] = recommendations
		}
		fields["audit"] = audit
	}
	// A partial finding list cannot establish a zero count for missing classes.
	// The collection gap remains even when supported summary evidence is present.
	return gaps, nil
}

// The cached gh-aw JSONL producer formats run durations, unlike the internal
// JobInfoWithDuration type. Match timeutil.FormatDuration at the pinned revision.
func auditDuration(duration time.Duration) string {
	switch {
	case duration < time.Microsecond:
		return fmt.Sprintf("%dns", duration.Nanoseconds())
	case duration < time.Millisecond:
		return fmt.Sprintf("%dµs", duration.Microseconds())
	case duration < time.Second:
		return fmt.Sprintf("%dms", duration.Milliseconds())
	case duration < time.Minute && math.Round(duration.Seconds()*10)/10 < 60:
		return fmt.Sprintf("%.1fs", math.Round(duration.Seconds()*10)/10)
	case duration < time.Hour && math.Round(duration.Minutes()*10)/10 < 60:
		return fmt.Sprintf("%.1fm", math.Round(duration.Minutes()*10)/10)
	default:
		return fmt.Sprintf("%.1fh", math.Round(duration.Hours()*10)/10)
	}
}

func safeAuditOutput(input map[string]any) (map[string]any, error) {
	if strings.TrimSpace(text(input["type"])) == "" {
		return nil, errors.New("missing safe output type")
	}
	result := map[string]any{}
	for _, field := range []string{"type", "repo", "provider", "timestamp"} {
		if value, ok := input[field].(string); ok {
			result[field] = value
		}
	}
	if value := integer(input["number"]); value != nil {
		if n, ok := number(value); ok && n > 0 {
			result["number"] = value
		}
	}
	if address, err := url.Parse(text(input["url"])); err == nil && address.Scheme == "https" &&
		address.Host != "" && address.User == nil && address.RawQuery == "" && address.Fragment == "" {
		result["url"] = address.String()
	}
	if kind, ok := object(input["target"])["kind"].(string); ok {
		result["target"] = map[string]any{"kind": kind}
	}
	return result, nil
}

func safeCounters(input map[string]any, names ...string) (map[string]any, error) {
	result := map[string]any{}
	for _, name := range names {
		if value, present := input[name]; present {
			if integer(value) == nil || nonnegative(value) == nil {
				return nil, errors.New("invalid audit counter")
			}
			result[name] = value
		}
	}
	return result, nil
}

func applyAuditSummary(ctx context.Context, fields, summary map[string]any, limits Limits) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Build in isolation: a malformed section must not publish half a summary.
	staged := map[string]any{}
	for _, name := range []string{"firewall", "gateway", "skills", "safe_outputs", "working_set"} {
		if summary[name] != nil && object(summary[name]) == nil {
			return errors.New("invalid audit summary section")
		}
	}
	for _, declaration := range [][2]string{{"gateway", "tool_calls"}, {"skills", "items"}, {"safe_outputs", "items"}} {
		if value := object(summary[declaration[0]])[declaration[1]]; value != nil {
			if _, ok := value.([]any); !ok {
				return errors.New("invalid audit summary items")
			}
		}
	}
	count := 0
	step := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > limits.defaults().Entries {
			return ErrCapacity
		}
		return nil
	}
	if firewall := object(summary["firewall"]); firewall != nil {
		result, err := safeCounters(firewall, "total_requests", "allowed_requests", "blocked_requests")
		if err != nil {
			return err
		}
		domains := map[string]any{}
		if firewall["requests_by_domain"] != nil && object(firewall["requests_by_domain"]) == nil {
			return errors.New("invalid firewall domain summary")
		}
		for host, value := range object(firewall["requests_by_domain"]) {
			if err := step(); err != nil {
				return err
			}
			counts, err := safeCounters(object(value), "allowed", "blocked")
			if err != nil || object(value) == nil || len(counts) != 2 || strings.TrimSpace(host) == "" {
				return errors.New("invalid firewall summary")
			}
			domains[host] = counts
		}
		result["requests_by_domain"] = domains
		staged["firewall_analysis"] = result
	}
	if gateway := object(summary["gateway"]); gateway != nil {
		calls := []map[string]any{}
		for _, candidate := range array(gateway["tool_calls"]) {
			if err := step(); err != nil {
				return err
			}
			call := object(candidate)
			retained, err := safeCounters(call, "request_size", "response_size")
			if err != nil || call == nil {
				return errors.New("invalid gateway summary")
			}
			retained["input_size"], retained["output_size"] = retained["request_size"], retained["response_size"]
			delete(retained, "request_size")
			delete(retained, "response_size")
			for native, target := range map[string]string{"tool_call_id": "tool_call_id", "timestamp": "timestamp",
				"server_name": "server_name", "tool_name": "tool_name", "outcome": "status"} {
				if value, ok := call[native].(string); ok {
					retained[target] = value
				}
			}
			calls = append(calls, retained)
		}
		staged["mcp_tool_usage"] = map[string]any{"tool_calls": calls}
	}
	if skills := object(summary["skills"]); skills != nil {
		items := []map[string]any{}
		for _, candidate := range array(skills["items"]) {
			if err := step(); err != nil {
				return err
			}
			skill := object(candidate)
			item, err := safeCounters(skill, "invocation_count", "failed_count")
			if err != nil || strings.TrimSpace(text(skill["name"])) == "" {
				return errors.New("invalid skill summary")
			}
			item["name"], item["status"], item["source"] = skill["name"], "invoked", "usage_summary"
			if clock := eventClock(skill["first_timestamp"]); clock != "" {
				item["timestamp"] = clock
			}
			items = append(items, item)
		}
		staged["skill_activations"] = items
	}
	if outputs := object(summary["safe_outputs"]); outputs != nil {
		counts, err := safeCounters(outputs, "total_items")
		if err != nil {
			return err
		}
		items := []map[string]any{}
		for _, candidate := range array(outputs["items"]) {
			if err := step(); err != nil {
				return err
			}
			safe, err := safeAuditOutput(object(candidate))
			if err != nil {
				return err
			}
			items = append(items, safe)
		}
		staged["safe_outputs"] = items
		if count, present := counts["total_items"]; present {
			staged["safe_items_count"] = count
		}
	}
	if working := object(summary["working_set"]); working != nil {
		retained, err := safeCounters(working, "cumulative_input_tokens", "peak_input_tokens", "rebuild_excess_tokens", "invocations")
		if err != nil || optionalText(working["measurement_state"]) == "" {
			return errors.New("invalid working set summary")
		}
		retained["measurement_state"] = working["measurement_state"]
		if value, present := working["rebuild_factor"]; present {
			if value != nil && nonnegative(value) == nil {
				return errors.New("invalid working set factor")
			}
			retained["rebuild_factor"] = value
		}
		staged["working_set"] = retained
	}
	encoded, err := json.Marshal(staged)
	if err != nil || len(encoded) > limits.defaults().Bytes {
		return ErrCapacity
	}
	for name, value := range staged {
		fields[name] = value
	}
	return nil
}

func legacyTokenUsage(ctx context.Context, content []byte, identity Identity, limits Limits) (map[string]any, bool, error) {
	entries := []map[string]any{}
	seen := map[string]bool{}
	err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
		var entry map[string]any
		if decode(raw, &entry) != nil || entry == nil || !matches(entry, identity) ||
			(entry["event"] != nil && entry["event"] != "token_usage") ||
			(entry["_schema"] != nil && !strings.HasPrefix(text(entry["_schema"]), "token-usage/")) {
			return errors.New("invalid token usage record")
		}
		observed := false
		for _, field := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens"} {
			if value, present := entry[field]; present {
				if integer(value) == nil || nonnegative(value) == nil {
					return errors.New("invalid token usage counter")
				}
				observed = true
			}
		}
		for _, field := range []string{"ai_credits_this_response", "ai_credits_total"} {
			observed = observed || nonnegative(entry[field]) != nil
		}
		if !observed {
			return errors.New("unsupported token usage record")
		}
		if id := text(entry["request_id"]); id != "" {
			if seen[id] {
				return nil
			}
			seen[id] = true
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil || len(entries) == 0 {
		return nil, true, err
	}
	slices.SortStableFunc(entries, func(a, b map[string]any) int {
		left, right := eventClock(a["timestamp"]), eventClock(b["timestamp"])
		if left == "" {
			left = "~"
		}
		if right == "" {
			right = "~"
		}
		return strings.Compare(left, right)
	})
	totals := map[string]any{}
	models := map[string]any{}
	add := func(target map[string]any, key string, value any) {
		n, _ := number(value)
		current, _ := target[key].(float64)
		target[key] = current + n
	}
	aic, priced := 0.0, true
	for _, entry := range entries {
		model := firstText(entry["model"], "unknown")
		if models[model] == nil {
			models[model] = map[string]any{}
		}
		// #nosec G101 -- public usage counter names, not credentials.
		for from, to := range map[string]string{"input_tokens": "total_input_tokens", "output_tokens": "total_output_tokens",
			"cache_read_tokens": "total_cache_read_tokens", "cache_write_tokens": "total_cache_write_tokens"} {
			if value, present := entry[from]; present {
				add(totals, to, value)
				add(object(models[model]), from, value)
			}
		}
		if value, present := entry["reasoning_tokens"]; present {
			add(object(models[model]), "reasoning_tokens", value)
		}
		delta, deltaOK := number(entry["ai_credits_this_response"])
		cumulative, cumulativeOK := number(entry["ai_credits_total"])
		switch {
		case cumulativeOK && cumulative >= 0:
			aic = cumulative
			priced = true
		case deltaOK && delta >= 0:
			aic += delta
		default:
			priced = false
		}
		if deltaOK && delta >= 0 {
			add(object(models[model]), "aic", entry["ai_credits_this_response"])
		}
	}
	totals["by_model"] = models
	if priced {
		totals["total_aic"] = aic
	}
	return totals, !priced, nil
}
