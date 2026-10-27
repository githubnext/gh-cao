package operationalvalue

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const maxHostedMeasurements = 100_000

var sha256Fingerprint = regexp.MustCompile(`^[0-9a-f]{64}$`)

type measurementEnvelope struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	Value         map[string]any `json:"operational_value"`
}

func ReadMeasurements(path string) ([]model.Row, error) {
	content, err := os.ReadFile(path) // #nosec G304 -- private output path created by the hosted local Value runtime.
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	rows := make([]model.Row, 0)
	for line := 1; scanner.Scan(); line++ {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		if len(rows) >= maxHostedMeasurements {
			return nil, errors.New("hosted local Value exceeded its measurement budget")
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.UseNumber()
		var envelope measurementEnvelope
		if err := decoder.Decode(&envelope); err != nil {
			return nil, fmt.Errorf("decode hosted local Value line %d: %w", line, err)
		}
		row, err := canonicalMeasurement(envelope)
		if err != nil {
			return nil, fmt.Errorf("hosted local Value line %d: %w", line, err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func canonicalMeasurement(envelope measurementEnvelope) (model.Row, error) {
	if envelope.SchemaVersion != 2 || envelope.Kind != "operational_value" || envelope.Value == nil {
		return nil, errors.New("invalid operational Value envelope")
	}
	required := func(field string) (string, error) {
		value, ok := envelope.Value[field].(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return "", fmt.Errorf("%s is required", field)
		}
		return value, nil
	}
	repository, err := required("repository")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, errors.New("repository must be an owner/name coordinate")
	}
	timestamp, err := required("timestamp")
	if err != nil {
		return nil, err
	}
	instant, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return nil, errors.New("timestamp must be RFC 3339")
	}
	timestamp = canonicalMeasurementTimestamp(instant)
	valueID, err := required("value_id")
	if err != nil {
		return nil, err
	}
	period, err := canonicalMeasurementPeriod(envelope.Value, instant)
	if err != nil {
		return nil, err
	}
	metric, hasMetric := finiteMeasurementNumber(envelope.Value["value"])
	unavailable := hasOwn(envelope.Value, "value") && envelope.Value["value"] == nil &&
		period["window-start"] != nil && period["missing-reason"] != nil &&
		optionalMeasurementString(envelope.Value["maturity_status"], "matured") == "unavailable"
	if !hasMetric && !unavailable {
		return nil, errors.New("value must be a finite number or valid unavailable measurement")
	}
	var metricValue any = metric
	if !hasMetric {
		metricValue = nil
	}
	coordinate := strings.ToLower(repository)
	campaign, _ := envelope.Value["campaign"].(string)
	campaign = strings.TrimSpace(campaign)
	identity := strings.ToLower(campaign) + ":" + coordinate + ":" + strings.ToLower(valueID) + ":" + timestamp
	row := model.Row{
		"id":                          "operational-value:" + model.EncodeCoordinate(identity),
		"repositoryId":                "repository:" + model.EncodeCoordinate(coordinate),
		"repository":                  repository,
		"valueId":                     valueID,
		"value":                       metricValue,
		"operational-value-role":      optionalMeasurementString(envelope.Value["metric_role"], "primary"),
		"operational-value-name":      optionalMeasurementString(envelope.Value["metric_name"], valueID),
		"operational-value-direction": optionalMeasurementString(envelope.Value["metric_direction"], "increase"),
		"maturity-status":             optionalMeasurementString(envelope.Value["maturity_status"], "matured"),
		"timestamp":                   timestamp,
	}
	for field, value := range period {
		row[field] = value
	}
	if campaign != "" {
		row["campaign"] = campaign
		if campaignID, ok := envelope.Value["campaign_id"].(string); ok && strings.TrimSpace(campaignID) != "" {
			row["campaignId"] = strings.TrimSpace(campaignID)
		} else {
			row["campaignId"] = "campaign:" + campaign
		}
	}
	for transport, canonical := range map[string]string{
		"metric_unit":     "operational-value-unit",
		"adoption_at":     "adoption-at",
		"evaluation_mode": "evaluation-mode",
		"workflow_slug":   "workflow-slug",
		"workflow_name":   "workflow-name",
	} {
		if value, exists := envelope.Value[transport]; exists {
			row[canonical] = value
		}
	}
	if value, exists := envelope.Value["evaluator_digest"]; exists {
		digest, ok := value.(string)
		if !ok || !sha256Fingerprint.MatchString(digest) {
			return nil, errors.New("evaluator_digest must be a SHA-256 fingerprint")
		}
		row["evaluator-digest"] = digest
	}
	if value, exists := envelope.Value["cadence_days"]; exists {
		cadence, ok := finiteMeasurementNumber(value)
		number, parseErr := strconv.ParseFloat(string(cadence), 64)
		if !ok || parseErr != nil || number <= 0 {
			return nil, errors.New("cadence_days must be a positive finite number")
		}
		row["cadence-days"] = cadence
	}
	numeratorValue, hasNumerator := envelope.Value["rollup_numerator"]
	denominatorValue, hasDenominator := envelope.Value["rollup_denominator"]
	if hasNumerator != hasDenominator || (!hasMetric && hasNumerator) {
		return nil, errors.New("rollup numerator and denominator must be present together")
	}
	if hasNumerator {
		numerator, numeratorOK := finiteMeasurementNumber(numeratorValue)
		denominator, denominatorOK := finiteMeasurementNumber(denominatorValue)
		numeratorNumber, numeratorErr := strconv.ParseFloat(string(numerator), 64)
		denominatorNumber, denominatorErr := strconv.ParseFloat(string(denominator), 64)
		if !numeratorOK || !denominatorOK || numeratorErr != nil || denominatorErr != nil ||
			numeratorNumber < 0 || denominatorNumber <= 0 {
			return nil, errors.New("invalid rollup numerator or denominator")
		}
		row["rollup-numerator"], row["rollup-denominator"] = numerator, denominator
	}
	return row, nil
}

func canonicalMeasurementPeriod(value map[string]any, observedAt time.Time) (model.Row, error) {
	row := model.Row{}
	hasPeriod := false
	for _, field := range []string{"window_start", "window_end", "measurement_kind", "coverage_status"} {
		hasPeriod = hasPeriod || hasOwn(value, field)
	}
	if hasPeriod {
		startText, startOK := value["window_start"].(string)
		endText, endOK := value["window_end"].(string)
		start, startErr := time.Parse(time.RFC3339Nano, startText)
		end, endErr := time.Parse(time.RFC3339Nano, endText)
		kind, kindOK := value["measurement_kind"].(string)
		coverage, coverageOK := value["coverage_status"].(string)
		if !startOK || !endOK || startErr != nil || endErr != nil ||
			!start.Before(end) || end.After(observedAt) ||
			!kindOK || (kind != "interval" && kind != "snapshot") ||
			!coverageOK || !slicesContains([]string{"complete", "partial", "missing", "unknown"}, coverage) {
			return nil, errors.New("invalid measurement period or coverage")
		}
		row["window-start"] = canonicalMeasurementTimestamp(start)
		row["window-end"] = canonicalMeasurementTimestamp(end)
		row["measurement-kind"] = kind
		row["coverage-status"] = coverage
	}
	for transport, canonical := range map[string]string{
		"population_digest": "population-digest",
		"evidence_digest":   "evidence-digest",
	} {
		if digest, exists := value[transport]; exists {
			text, ok := digest.(string)
			if !ok || !sha256Fingerprint.MatchString(text) {
				return nil, fmt.Errorf("%s must be a SHA-256 fingerprint", transport)
			}
			row[canonical] = text
		}
	}
	if count, exists := value["eligible_count"]; exists {
		number, ok := finiteMeasurementNumber(count)
		parsed, parseErr := strconv.ParseFloat(string(number), 64)
		if !ok || parseErr != nil || parsed < 0 || parsed > 1<<53-1 || math.Trunc(parsed) != parsed {
			return nil, errors.New("eligible_count must be a non-negative safe integer")
		}
		row["eligible-count"] = int64(parsed)
	}
	if reason, exists := value["missing_reason"]; exists {
		text, ok := reason.(string)
		text = strings.TrimSpace(text)
		if !ok || text == "" {
			return nil, errors.New("missing_reason must be a nonempty string")
		}
		row["missing-reason"] = text
	}
	return row, nil
}

func finiteMeasurementNumber(value any) (json.Number, bool) {
	var text string
	switch number := value.(type) {
	case json.Number:
		text = string(number)
	case string:
		text = strings.TrimSpace(number)
	default:
		return "", false
	}
	parsed, err := strconv.ParseFloat(text, 64)
	return json.Number(text), err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func canonicalMeasurementTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func hasOwn(value map[string]any, field string) bool {
	_, exists := value[field]
	return exists
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func optionalMeasurementString(value any, fallback string) string {
	if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	return fallback
}
