package collectnative

import (
	"context"
	"errors"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
)

// Detection accounting is deliberately separate from agent usage. Only verdict
// booleans and allowlisted outcomes survive the transient evidence boundary.
func detectionAuditFindings(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]any, []string, error) {
	limits = limits.defaults()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(files) > limits.Entries {
		return nil, nil, ErrCapacity
	}
	gaps := []string{}
	var unified map[string]any
	names := candidates(files, "aw_session.jsonl")
	if len(names) > 1 {
		gaps = append(gaps, "ambiguous-detection-session")
	} else if len(names) == 1 {
		var err error
		unified, err = detectionSession(ctx, files[names[0]], identity, limits)
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, nil, err
			}
			gaps = append(gaps, "invalid-detection-session")
		}
	}
	var result map[string]any
	var verdict map[string]any
	verdictConflict := false
	notStarted := false
	usageResults := 0
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		base := path.Base(name)
		if base != "execution.json" && base != "detection_result.json" && base != "detection.log" {
			continue
		}
		if excludedAuditPath(name) {
			continue
		}
		if len(files[name]) > limits.defaults().Bytes {
			return nil, nil, ErrCapacity
		}
		var record map[string]any
		if base != "detection.log" {
			if decode(files[name], &record) != nil || record == nil || !matches(record, identity) {
				gaps = append(gaps, "invalid-detection-evidence")
				continue
			}
		}
		if base == "execution.json" {
			if path.Base(path.Dir(name)) == "detection" && scalar(record["version"]) == "1" &&
				record["component"] == "detection" && record["state"] == "not_started" {
				notStarted = true
			}
			continue
		}
		if base == "detection_result.json" && path.Base(path.Dir(name)) == "detection" &&
			usageArtifactPath(name) && validDetectionResult(text(record["job_result"])) {
			usageResults++
			result = record
		}
		observe := func(next map[string]any) {
			if !completeDetectionVerdict(next, false) {
				return
			}
			next = detectionVerdict(next, false)
			if verdict != nil && !reflect.DeepEqual(verdict, next) {
				verdictConflict = true
			}
			verdict = next
		}
		if base == "detection_result.json" {
			observe(record)
		} else {
			var staged map[string]any
			conflict := false
			err := scanEvidence(ctx, files[name], limits, func(_ int, raw []byte) error {
				_, content, found := strings.Cut(string(raw), "THREAT_DETECTION_RESULT:")
				if !found {
					return nil
				}
				var next map[string]any
				if decode([]byte(content), &next) != nil {
					return errors.New("invalid detection verdict log")
				}
				if !completeDetectionVerdict(next, false) {
					return nil
				}
				next = detectionVerdict(next, false)
				if staged != nil && !reflect.DeepEqual(staged, next) {
					conflict = true
				}
				staged = next
				return nil
			})
			if err != nil {
				if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
					return nil, nil, err
				}
				gaps = append(gaps, "invalid-detection-log")
				continue
			}
			if conflict {
				verdictConflict = true
			} else if staged != nil {
				observe(staged)
			}
		}
	}
	if usageResults > 1 {
		result = nil
		gaps = append(gaps, "ambiguous-detection-result")
	}
	if verdictConflict {
		verdict = nil
		gaps = append(gaps, "conflicting-detection-verdict")
	}
	if unified != nil {
		// A unified verdict is authoritative even when incomplete; do not
		// borrow missing flags from an unrelated legacy verdict.
		verdict = nil
		if completeDetectionVerdict(unified, true) {
			verdict = detectionVerdict(unified, true)
		} else if unified["promptInjection"] != nil || unified["secretLeak"] != nil || unified["maliciousPatch"] != nil {
			gaps = append(gaps, "incomplete-detection-verdict")
		}
		merged := map[string]any{}
		for _, field := range []string{"job_result", "conclusion", "reason"} {
			merged[field] = result[field]
		}
		for from, to := range map[string]string{"jobResult": "job_result", "conclusion": "conclusion", "reason": "reason"} {
			if value, ok := unified[from].(string); ok {
				merged[to] = value
			}
		}
		result = merged
	}
	failed, skipped := false, false
	for _, candidate := range array(fields["job_details"]) {
		job := object(candidate)
		name := strings.ToLower(strings.TrimSpace(text(job["name"])))
		name = strings.NewReplacer(" ", "_", "-", "_").Replace(name)
		if name == "detection" {
			switch strings.ToLower(strings.TrimSpace(text(job["conclusion"]))) {
			case "failure":
				failed = true
			case "skipped":
				skipped = true
			}
		}
	}
	hasResult := validDetectionResult(text(result["job_result"]))
	skipped = skipped || (hasResult && result["job_result"] == "skipped")
	notStarted = notStarted && !skipped
	resultFailed := hasResult && (result["job_result"] == "failure" || result["job_result"] == "cancelled" ||
		result["conclusion"] == "failure" || result["conclusion"] == "warning")
	findings := []any{}
	if failed || notStarted || resultFailed {
		title, description := "Threat Detection Job Failed", "The threat-detection job failed before producing a threat verdict"
		if notStarted && !failed {
			description = "Usage artifact evidence indicates the threat-detection job did not start"
		} else if verdict != nil {
			description = "The threat-detection job concluded with failure"
		}
		if resultFailed && !failed && result["conclusion"] == "warning" {
			title, description = "Threat Detection Warning", "Threat detection reported a warning"
		}
		if resultFailed {
			switch result["reason"] {
			case "agent_failure", "parse_error", "threat_detected":
				description += " (" + text(result["reason"]) + ")"
			}
		}
		findings = append(findings, auditFinding("threat_detection_job_failed", "security", "high", title, description))
	}
	kinds := []string{}
	for _, flag := range [][2]string{{"prompt_injection", "prompt injection"}, {"secret_leak", "secret leak"}, {"malicious_patch", "malicious patch"}} {
		if verdict[flag[0]] == true {
			kinds = append(kinds, flag[1])
		}
	}
	if len(kinds) > 0 || (hasResult && result["reason"] == "threat_detected") {
		description := "Threat detection identified a threat, but no specific threat category was available"
		if len(kinds) > 0 {
			description = "Threat detection identified: " + strings.Join(kinds, ", ")
		}
		findings = append(findings, auditFinding("threat_detected", "security", "critical", "Security Threat Detected", description))
	}
	return findings, gaps, nil
}

func validDetectionResult(value string) bool {
	return value == "success" || value == "failure" || value == "cancelled" || value == "skipped"
}

func detectionSession(ctx context.Context, content []byte, identity Identity, limits Limits) (map[string]any, error) {
	header := false
	var result map[string]any
	err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
		var event map[string]any
		if decode(raw, &event) != nil || event == nil || !matches(event, identity) || !matches(object(event["data"]), identity) {
			return errors.New("invalid detection session")
		}
		data, provenance := object(event["data"]), object(event["provenance"])
		if !header {
			if event["type"] != "session.format" || provenance["component"] != "collector" || scalar(data["version"]) != "1" {
				return errors.New("invalid detection session header")
			}
			header = true
			return nil
		}
		if event["type"] == "session.format" && provenance["component"] == "collector" {
			return errors.New("duplicate detection session header")
		}
		if event["type"] != "detection.result" || provenance["component"] != "detection" || provenance["phase"] != "detection" {
			return nil
		}
		if data == nil {
			return errors.New("invalid detection result")
		}
		safe := map[string]any{}
		for _, name := range []string{"jobResult", "conclusion", "reason", "promptInjection", "secretLeak", "maliciousPatch"} {
			value, present := data[name]
			if !present || value == nil {
				continue
			}
			if name == "jobResult" || name == "conclusion" || name == "reason" {
				if _, ok := value.(string); !ok {
					return errors.New("invalid detection outcome")
				}
			} else if _, ok := value.(bool); !ok {
				return errors.New("invalid detection verdict")
			}
			safe[name] = value
		}
		if result != nil && !reflect.DeepEqual(result, safe) {
			return errors.New("conflicting detection results")
		}
		result = safe
		return nil
	})
	if err == nil && !header {
		err = errors.New("missing detection header")
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func completeDetectionVerdict(record map[string]any, unified bool) bool {
	for _, names := range [][2]string{{"prompt_injection", "promptInjection"}, {"secret_leak", "secretLeak"}, {"malicious_patch", "maliciousPatch"}} {
		key := names[0]
		if unified {
			key = names[1]
		}
		if _, ok := record[key].(bool); !ok {
			return false
		}
	}
	return true
}

func detectionVerdict(record map[string]any, unified bool) map[string]any {
	result := map[string]any{}
	for _, names := range [][2]string{{"prompt_injection", "promptInjection"}, {"secret_leak", "secretLeak"}, {"malicious_patch", "maliciousPatch"}} {
		key := names[0]
		if unified {
			key = names[1]
		}
		result[names[0]] = record[key]
	}
	return result
}

func usageArtifactPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == "usage" || strings.HasSuffix(part, "-usage") {
			return true
		}
	}
	return false
}

func excludedAuditPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == "workflow-logs" || part == "base" || strings.HasPrefix(part, "baseline-") {
			return true
		}
	}
	return false
}
