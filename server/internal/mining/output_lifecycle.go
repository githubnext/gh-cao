package mining

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const OutputLifecycleRetentionDays = 365

func lifecycleTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func OutputLifecycleID(runID, outputType, targetURL, evidenceID string) string {
	identity := evidenceID
	if (outputType == "create_issue" || outputType == "create_pull_request") && targetURL != "" {
		identity = targetURL
	}
	return "output-lifecycle:" + base64.RawURLEncoding.EncodeToString(
		[]byte(runID+":"+outputType+":"+identity),
	)
}

func safeLifecycleURL(value string) bool {
	if value == "" {
		return true
	}
	address, err := url.Parse(value)
	return err == nil && address.Scheme == "https" && address.Hostname() != "" &&
		address.User == nil && address.RawQuery == "" && address.Fragment == "" &&
		address.RawPath == "" && address.Port() == ""
}

func lifecycleTargetRepository(value string) string {
	address, err := url.Parse(value)
	if err != nil || !strings.EqualFold(address.Hostname(), "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(address.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(parts[0] + "/" + parts[1])
}

func OutputLifecycleRow(
	sourceID string,
	repositoryID string,
	workflowID string,
	runID string,
	outputType string,
	title string,
	targetRepository string,
	targetURL string,
	deliveredAt time.Time,
	observedAt time.Time,
	deliverySource string,
) (model.Row, error) {
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(repositoryID) == "" ||
		strings.TrimSpace(workflowID) == "" || strings.TrimSpace(runID) == "" ||
		strings.TrimSpace(outputType) == "" || strings.TrimSpace(deliverySource) == "" ||
		deliveredAt.IsZero() || observedAt.IsZero() || !safeLifecycleURL(targetURL) {
		return nil, errors.New("invalid safe-output lifecycle delivery")
	}
	deliveredAt = deliveredAt.UTC()
	observedAt = observedAt.UTC()
	evaluation := EvaluateSafeOutputDelivery(outputType, deliveredAt)
	row := model.Row{
		"id": OutputLifecycleID(runID, outputType, targetURL, sourceID), "repositoryId": repositoryID, "workflowId": workflowID,
		"runId": runID, "safeOutputType": outputType,
		"deliveryState": "delivered", "deliverySignal": "safe_output.result",
		"deliveredAt": lifecycleTimestamp(deliveredAt), "deliveryObservedAt": lifecycleTimestamp(observedAt),
		"deliverySource": deliverySource, "deliveryEvidenceId": sourceID,
		"adoptionState": evaluation.Status, "adoptionSignal": evaluation.Signal,
		"adoptionObservedAt": lifecycleTimestamp(observedAt), "adoptionSource": "adoption-registry",
		"ruleVersion": AdoptionRegistryVersion, "survivalState": "unknown",
		"observedAt": lifecycleTimestamp(observedAt),
	}
	if evaluation.AcceptanceKind == "fallback_delivery" {
		row["adoptionSource"] = "delivery-rule"
	}
	if evaluation.AcceptedAt != nil {
		row["adoptionAt"] = lifecycleTimestamp(*evaluation.AcceptedAt)
	}
	if title = strings.TrimSpace(title); title != "" {
		row["title"] = title
	}
	if targetRepository = strings.ToLower(strings.TrimSpace(targetRepository)); targetRepository != "" {
		row["targetRepository"] = targetRepository
	} else if targetRepository = lifecycleTargetRepository(targetURL); targetRepository != "" {
		row["targetRepository"] = targetRepository
	}
	if targetURL != "" {
		row["targetUrl"] = targetURL
	}
	return row, nil
}

func ApplyOutcomeToLifecycle(previous model.Row, outcome Outcome) (model.Row, error) {
	if err := outcome.Validate(); err != nil {
		return nil, err
	}
	outputType, _ := previous["safeOutputType"].(string)
	targetURL, _ := previous["targetUrl"].(string)
	if targetURL != outcome.URL ||
		(outputType != "create_issue" && outputType != "create_pull_request") ||
		(outputType == "create_pull_request") != outcome.PullRequest {
		return nil, errors.New("outcome does not match safe-output lifecycle")
	}
	evaluation := outcome.EvaluateSafeOutput(outputType)
	next := model.Row{}
	for key, value := range previous {
		next[key] = value
	}
	previousState, _ := previous["adoptionState"].(string)
	next["adoptionState"] = evaluation.Status
	next["adoptionSignal"] = evaluation.Signal
	next["adoptionObservedAt"] = outcome.ObservedAt.UTC()
	next["adoptionSource"] = outcome.Source
	next["observedAt"] = outcome.ObservedAt.UTC()
	if evaluation.AcceptedAt != nil {
		next["adoptionAt"] = *evaluation.AcceptedAt
	} else {
		delete(next, "adoptionAt")
	}
	if evaluation.Status == "accepted" {
		next["survivalState"] = "observed"
		next["survivalSignal"] = "resource_still_accepted"
		next["survivalObservedAt"] = outcome.ObservedAt.UTC()
	}
	if previousState == "accepted" && evaluation.Status != "accepted" {
		signal := "RESOURCE_REJECTED_OR_REVERSED"
		if outcome.State == "open" {
			signal = "ISS_REOPENED"
			if outcome.PullRequest {
				signal = "PR_REOPENED"
			}
		}
		next["reversalSignal"] = signal
		next["reversedAt"] = outcome.UpdatedAt.UTC()
		next["survivalState"] = "reversed"
		next["survivalSignal"] = signal
		next["survivalObservedAt"] = outcome.ObservedAt.UTC()
	}
	return next, nil
}

func lifecycleTime(row model.Row, field string) (time.Time, bool) {
	value, ok := row[field]
	if !ok || value == nil {
		return time.Time{}, false
	}
	switch value := value.(type) {
	case time.Time:
		return value.UTC(), !value.IsZero()
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, value)
		return parsed.UTC(), err == nil && !parsed.IsZero()
	default:
		return time.Time{}, false
	}
}

func adoptionAuthority(source any) int {
	switch fmt.Sprint(source) {
	case "github-api":
		return 3
	case "github-webhook":
		return 2
	case "delivery-rule", "adoption-registry":
		return 1
	default:
		return 0
	}
}

func sameLifecycleFields(left, right model.Row, fields ...string) bool {
	for _, field := range fields {
		if left[field] != nil && right[field] != nil && fmt.Sprint(left[field]) != fmt.Sprint(right[field]) {
			leftTime, leftOK := lifecycleTime(left, field)
			rightTime, rightOK := lifecycleTime(right, field)
			if !leftOK || !rightOK || !leftTime.Equal(rightTime) {
				return false
			}
		}
	}
	return true
}

func MergeOutputLifecycle(previous, incoming model.Row) (model.Row, error) {
	if previous == nil {
		return incoming, nil
	}
	for _, field := range []string{
		"id", "repositoryId", "workflowId", "runId", "safeOutputType",
		"deliveryState", "deliverySignal", "deliverySource",
		"deliveryEvidenceId", "ruleVersion",
	} {
		if !sameLifecycleFields(previous, incoming, field) {
			return nil, fmt.Errorf("conflicting output lifecycle %s", field)
		}
	}
	for _, field := range []string{"targetRepository", "targetUrl"} {
		if !sameLifecycleFields(previous, incoming, field) {
			return nil, fmt.Errorf("conflicting output lifecycle %s", field)
		}
	}
	merged := model.Row{}
	for key, value := range previous {
		merged[key] = value
	}
	for key, value := range incoming {
		merged[key] = value
	}
	previousAdoptionAt, _ := lifecycleTime(previous, "adoptionObservedAt")
	incomingAdoptionAt, _ := lifecycleTime(incoming, "adoptionObservedAt")
	previousAuthority, incomingAuthority := adoptionAuthority(previous["adoptionSource"]), adoptionAuthority(incoming["adoptionSource"])
	previousAdoptionWins := previousAuthority > incomingAuthority ||
		previousAuthority == incomingAuthority && previousAdoptionAt.After(incomingAdoptionAt)
	if previousAuthority == incomingAuthority && previousAdoptionAt.Equal(incomingAdoptionAt) &&
		!sameLifecycleFields(previous, incoming, "adoptionState", "adoptionSignal", "adoptionAt", "adoptionSource") {
		return nil, errors.New("conflicting equal-clock output adoption evidence")
	}
	if previousAdoptionWins {
		for _, field := range []string{"adoptionState", "adoptionSignal", "adoptionAt", "adoptionObservedAt", "adoptionSource"} {
			if value, ok := previous[field]; ok {
				merged[field] = value
			} else {
				delete(merged, field)
			}
		}
	}
	for _, fields := range [][]string{
		{"survivalState", "survivalSignal", "survivalObservedAt"},
		{"reversalSignal", "reversedAt"},
	} {
		clock := fields[len(fields)-1]
		previousAt, previousOK := lifecycleTime(previous, clock)
		incomingAt, incomingOK := lifecycleTime(incoming, clock)
		if previousOK && incomingOK && previousAt.Equal(incomingAt) && !sameLifecycleFields(previous, incoming, fields...) {
			return nil, fmt.Errorf("conflicting equal-clock output %s evidence", clock)
		}
		if previousOK && (!incomingOK || previousAt.After(incomingAt)) {
			for _, field := range fields {
				if value, ok := previous[field]; ok {
					merged[field] = value
				} else {
					delete(merged, field)
				}
			}
		}
	}
	if previousAt, ok := lifecycleTime(previous, "deliveryObservedAt"); ok {
		if incomingAt, incomingOK := lifecycleTime(incoming, "deliveryObservedAt"); !incomingOK || previousAt.Before(incomingAt) {
			merged["deliveryObservedAt"] = previousAt
		}
	}
	if previousAt, ok := lifecycleTime(previous, "deliveredAt"); ok {
		if incomingAt, incomingOK := lifecycleTime(incoming, "deliveredAt"); !incomingOK || previousAt.Before(incomingAt) {
			merged["deliveredAt"] = previousAt
		}
	}
	if previousAt, ok := lifecycleTime(previous, "observedAt"); ok {
		if incomingAt, incomingOK := lifecycleTime(incoming, "observedAt"); !incomingOK || previousAt.After(incomingAt) {
			merged["observedAt"] = previousAt
		}
	}
	return merged, nil
}
