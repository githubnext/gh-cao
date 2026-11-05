package collectnative

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
)

var auditReportName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$`)

func enrichAuditReports(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]string, error) {
	gaps := []string{}
	for _, source := range []struct {
		name string
		gap  string
		read func([]byte) error
	}{
		{"agent_output.json", "agent-reports", func(content []byte) error {
			reports, err := agentAuditReports(ctx, content, identity, limits)
			if err != nil {
				return err
			}
			fields["audit"] = reports
			fields["missing_tool_count"] = len(array(reports["missing_tools"]))
			fields["missing_data_count"] = len(array(reports["missing_data"]))
			if len(array(reports["noops"])) > 0 {
				gaps = append(gaps, "agent-report-text-unavailable")
			}
			return nil
		}},
		{"github_rate_limits.jsonl", "github-api-usage", func(content []byte) error {
			count, available := 0, false
			err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
				var entry map[string]any
				if decode(raw, &entry) != nil || !matches(entry, identity) {
					return errors.New("invalid native GitHub usage record")
				}
				switch entry["source"] {
				case "response_headers":
					count++
					available = true
				case "rate_limit_api":
					available = true
				default:
					return errors.New("unsupported native GitHub usage record")
				}
				return nil
			})
			if err != nil {
				return err
			}
			if !available {
				return errors.New("missing native GitHub usage evidence")
			}
			// Snapshot probes are not workflow API calls. Do not mix this
			// immutable workflow accounting with the collector's own quotas.
			fields["github_api_calls"] = count
			return nil
		}},
	} {
		names := candidates(files, source.name)
		switch {
		case len(names) > 1:
			gaps = append(gaps, "ambiguous-"+source.gap)
		case len(names) == 1:
			if err := source.read(files[names[0]]); err != nil {
				if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
					return nil, err
				}
				gaps = append(gaps, "invalid-"+source.gap)
			}
		}
	}
	names := candidates(files, "aw_session.jsonl")
	if len(names) == 1 {
		sessionGaps, err := auditSessionSourceGaps(ctx, files[names[0]], identity, limits)
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, err
			}
			gaps = append(gaps, "invalid-audit-session")
		} else {
			gaps = append(gaps, sessionGaps...)
		}
	}
	return gaps, nil
}

func agentAuditReports(ctx context.Context, content []byte, identity Identity, limits Limits) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits = limits.defaults()
	if len(content) > limits.Bytes {
		return nil, ErrCapacity
	}
	var document struct {
		Items []json.RawMessage `json:"items"`
	}
	if decode(content, &document) != nil || document.Items == nil {
		return nil, errors.New("invalid native agent reports")
	}
	if len(document.Items) > limits.Entries {
		return nil, ErrCapacity
	}
	reports := map[string]any{"missing_tools": []any{}, "missing_data": []any{}, "noops": []any{}}
	for _, raw := range document.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(raw) > limits.LineBytes {
			return nil, ErrCapacity
		}
		var item map[string]any
		if decode(raw, &item) != nil || !matches(item, identity) {
			return nil, errors.New("invalid native agent report")
		}
		var key, field string
		switch item["type"] {
		case "report_missing_tool", "missing_tool":
			key, field = "missing_tools", "tool"
		case "report_missing_data", "missing_data":
			key, field = "missing_data", "data_type"
		case "noop":
			key = "noops"
		default:
			continue
		}
		retained := map[string]any{}
		if field != "" {
			if !auditReportName.MatchString(text(item[field])) {
				return nil, errors.New("unsupported native report identifier")
			}
			retained[field] = item[field]
		}
		if timestamp := eventClock(item["timestamp"]); timestamp != "" {
			retained["timestamp"] = timestamp
		}
		// Reports may quote arbitrary prompts in reason/context/alternatives
		// and noop messages. Preserve occurrence, not those free-text bodies.
		reports[key] = append(array(reports[key]), retained)
	}
	return reports, nil
}

func auditSessionSourceGaps(ctx context.Context, content []byte, identity Identity, limits Limits) ([]string, error) {
	gaps := []string{}
	err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
		var event map[string]any
		if decode(raw, &event) != nil || event == nil || !matches(event, identity) || !matches(object(event["data"]), identity) {
			return errors.New("invalid native audit session")
		}
		if event["type"] == "detection.result" {
			gaps = append(gaps, "threat-detection-audit-unavailable")
		}
		return nil
	})
	return gaps, err
}
