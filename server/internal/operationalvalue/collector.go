// Package operationalvalue implements native Value evaluation and the
// operational-value publication contract shared with the Activity CLI.
package operationalvalue

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultRetention keeps compact outcome observations independent of run detail.
	DefaultRetention = 365 * 24 * time.Hour
	day              = 24 * time.Hour
	maxWorkerOutput  = 16 * 1024 * 1024
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)
	identifierPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	slugPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// Config describes native Value evaluation.
type Config struct {
	Root         string
	ObservedAt   time.Time
	Repositories []string
}

type definition struct {
	WorkflowSlug   string
	AdoptedAt      time.Time
	EvaluationMode string
	CadenceDays    float64
	Repositories   []string
	ValueIDs       []string
}

type definitionRecord struct {
	Kind           string   `json:"kind"`
	WorkflowSlug   string   `json:"workflowSlug"`
	AdoptedAt      string   `json:"adoptedAt"`
	EvaluationMode string   `json:"evaluationMode"`
	CadenceDays    float64  `json:"cadenceDays"`
	Repositories   []string `json:"repositories"`
	ValueIDs       []string `json:"valueIds"`
}

type valueRecord struct {
	WindowStart       *string  `json:"windowStart"`
	WindowEnd         *string  `json:"windowEnd"`
	MeasurementKind   *string  `json:"measurementKind"`
	CoverageStatus    *string  `json:"coverageStatus"`
	MissingReason     *string  `json:"missingReason"`
	EligibleCount     *float64 `json:"eligibleCount"`
	PopulationDigest  *string  `json:"populationDigest"`
	EvidenceDigest    *string  `json:"evidenceDigest"`
	CadenceDays       *float64 `json:"cadenceDays"`
	EvaluatorDigest   *string  `json:"evaluatorDigest"`
	Timestamp         string   `json:"timestamp"`
	Repository        string   `json:"repository"`
	ValueID           string   `json:"valueId"`
	Value             *float64 `json:"value"`
	MetricRole        *string  `json:"metricRole"`
	MetricName        *string  `json:"metricName"`
	MetricUnit        *string  `json:"metricUnit"`
	MetricDirection   *string  `json:"metricDirection"`
	MaturityStatus    *string  `json:"maturityStatus"`
	AdoptionAt        *string  `json:"adoptionAt"`
	EvaluationMode    *string  `json:"evaluationMode"`
	WorkflowSlug      *string  `json:"workflowSlug"`
	WorkflowName      *string  `json:"workflowName"`
	RollupNumerator   *float64 `json:"rollupNumerator"`
	RollupDenominator *float64 `json:"rollupDenominator"`
}

type parsedOutput struct {
	definitions []definition
	values      []valueRecord
	deferred    []string
}

type envelope struct {
	SchemaVersion    int              `json:"schema_version"`
	Kind             string           `json:"kind"`
	OperationalValue operationalValue `json:"operational_value"`
}

type operationalValue struct {
	WindowStart       *string  `json:"window_start,omitempty"`
	WindowEnd         *string  `json:"window_end,omitempty"`
	MeasurementKind   *string  `json:"measurement_kind,omitempty"`
	CoverageStatus    *string  `json:"coverage_status,omitempty"`
	MissingReason     *string  `json:"missing_reason,omitempty"`
	EligibleCount     *float64 `json:"eligible_count,omitempty"`
	PopulationDigest  *string  `json:"population_digest,omitempty"`
	EvidenceDigest    *string  `json:"evidence_digest,omitempty"`
	CadenceDays       *float64 `json:"cadence_days,omitempty"`
	EvaluatorDigest   *string  `json:"evaluator_digest,omitempty"`
	Timestamp         string   `json:"timestamp"`
	Repository        string   `json:"repository"`
	Campaign          string   `json:"campaign"`
	CampaignID        string   `json:"campaign_id"`
	ValueID           string   `json:"value_id"`
	Value             *float64 `json:"value"`
	MetricRole        string   `json:"metric_role"`
	MetricName        string   `json:"metric_name"`
	MetricUnit        *string  `json:"metric_unit,omitempty"`
	MetricDirection   string   `json:"metric_direction"`
	MaturityStatus    string   `json:"maturity_status"`
	AdoptionAt        *string  `json:"adoption_at,omitempty"`
	EvaluationMode    *string  `json:"evaluation_mode,omitempty"`
	WorkflowSlug      *string  `json:"workflow_slug,omitempty"`
	WorkflowName      *string  `json:"workflow_name,omitempty"`
	RollupNumerator   *float64 `json:"rollup_numerator,omitempty"`
	RollupDenominator *float64 `json:"rollup_denominator,omitempty"`
}

// persistValues combines this run's freshly collected values with the
// previously retained shard, retiring any retained value whose campaign no
// longer declares it active and resolving duplicates by cadence bucket or
// exact key. It is a pure function so the retire-then-merge decision is
// testable independently of the temp-file rename writeEnvelopes performs.
func persistValues(
	retained, values []envelope, activeIDs map[string]map[string]struct{}, definitions map[string]definition,
) []envelope {
	currentRetained := retireValues(retained, activeIDs)
	return mergeValues(append(currentRetained, values...), definitions)
}

func parseOutput(content []byte, source string, repositories []string) (parsedOutput, error) {
	var output parsedOutput
	allowed := map[string]struct{}{}
	for _, repository := range repositories {
		allowed[strings.ToLower(repository)] = struct{}{}
	}
	defined := map[string]struct{}{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
			return output, fmt.Errorf("%s:%d emitted invalid JSON: %w", source, line, err)
		}
		if header.Kind == "operational_value_deferred" {
			var record struct {
				WorkflowSlug string `json:"workflowSlug"`
				Reason       string `json:"reason"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil ||
				!slugPattern.MatchString(record.WorkflowSlug) || strings.TrimSpace(record.Reason) == "" || len(record.Reason) > 1024 {
				return output, errors.New("invalid operational value deferral")
			}
			output.deferred = append(output.deferred, record.WorkflowSlug+": "+record.Reason)
			continue
		}
		if header.Kind == "operational_value_definition" {
			var record definitionRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				return output, err
			}
			item, err := validateDefinition(record, source, line, defined)
			if err != nil {
				return output, err
			}
			output.definitions = append(output.definitions, item)
			continue
		}
		var record valueRecord
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &fields); err != nil {
			return output, err
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return output, err
		}
		if err := validateValue(&record, fields, source, line, allowed); err != nil {
			return output, err
		}
		output.values = append(output.values, record)
	}
	if err := scanner.Err(); err != nil {
		return output, err
	}
	return output, nil
}

func validateDefinition(
	record definitionRecord, source string, line int, defined map[string]struct{},
) (definition, error) {
	adoptedAt, err := parseTimestamp(record.AdoptedAt)
	valid := err == nil &&
		slugPattern.MatchString(record.WorkflowSlug) &&
		(record.EvaluationMode == "baseline-comparable" || record.EvaluationMode == "attainment-only") &&
		!math.IsNaN(record.CadenceDays) && !math.IsInf(record.CadenceDays, 0) && record.CadenceDays > 0 &&
		len(record.Repositories) > 0 && len(record.ValueIDs) > 0
	for _, repository := range record.Repositories {
		valid = valid && repositoryPattern.MatchString(repository)
	}
	for _, valueID := range record.ValueIDs {
		valid = valid && identifierPattern.MatchString(valueID) &&
			strings.HasPrefix(valueID, record.WorkflowSlug+".")
		if _, exists := defined[valueID]; exists {
			return definition{}, fmt.Errorf("%s:%d emitted a duplicate operational value definition ID: %s",
				source, line, valueID)
		}
		defined[valueID] = struct{}{}
	}
	if !valid {
		return definition{}, fmt.Errorf("%s:%d must emit a valid operational value definition", source, line)
	}
	repositories := uniqueLower(record.Repositories)
	return definition{
		WorkflowSlug: record.WorkflowSlug, AdoptedAt: adoptedAt,
		EvaluationMode: record.EvaluationMode, CadenceDays: record.CadenceDays,
		Repositories: repositories, ValueIDs: append([]string{}, record.ValueIDs...),
	}, nil
}

func validateValue(
	record *valueRecord,
	fields map[string]json.RawMessage,
	source string,
	line int,
	allowed map[string]struct{},
) error {
	timestamp, err := parseTimestamp(record.Timestamp)
	if err != nil {
		return fmt.Errorf("%s:%d.timestamp must be an ISO-8601 timestamp", source, line)
	}
	record.Timestamp = formatTimestamp(timestamp)
	if !repositoryPattern.MatchString(record.Repository) {
		return fmt.Errorf("%s:%d.repository must identify a requested repository", source, line)
	}
	if _, ok := allowed[strings.ToLower(record.Repository)]; !ok {
		return fmt.Errorf("%s:%d.repository must identify a requested repository", source, line)
	}
	if !identifierPattern.MatchString(record.ValueID) {
		return fmt.Errorf("%s:%d.valueId must be a lowercase metric identifier", source, line)
	}
	if err := validatePeriod(record, fields, timestamp, source, line); err != nil {
		return err
	}
	unavailable := fieldIsNull(fields, "value") && record.WindowStart != nil &&
		record.MissingReason != nil && record.MaturityStatus != nil && *record.MaturityStatus == "unavailable"
	if !unavailable && (record.Value == nil || math.IsNaN(*record.Value) || math.IsInf(*record.Value, 0)) {
		return fmt.Errorf("%s:%d.value must be a finite number", source, line)
	}
	if fieldIsNull(fields, "cadenceDays") || (record.CadenceDays != nil &&
		(math.IsNaN(*record.CadenceDays) || math.IsInf(*record.CadenceDays, 0) || *record.CadenceDays <= 0)) {
		return fmt.Errorf("%s:%d.cadenceDays must be a positive finite number", source, line)
	}
	if record.MetricRole != nil && *record.MetricRole != "primary" && *record.MetricRole != "diagnostic" {
		return fmt.Errorf("%s:%d.metricRole must be primary or diagnostic", source, line)
	}
	if record.MetricName != nil {
		name := strings.TrimSpace(*record.MetricName)
		if name == "" {
			return fmt.Errorf("%s:%d.metricName must be a non-empty string", source, line)
		}
		record.MetricName = &name
	}
	if fieldIsNull(fields, "metricUnit") ||
		(record.MetricUnit != nil && strings.TrimSpace(*record.MetricUnit) == "") {
		return fmt.Errorf("%s:%d.metricUnit must be a non-empty string", source, line)
	}
	if record.MetricUnit != nil {
		trimmed := strings.TrimSpace(*record.MetricUnit)
		record.MetricUnit = &trimmed
	}
	if record.MetricDirection != nil && !oneOf(*record.MetricDirection, "increase", "decrease", "maintain", "target") {
		return fmt.Errorf("%s:%d.metricDirection is invalid", source, line)
	}
	if record.MaturityStatus != nil && !oneOf(*record.MaturityStatus, "matured", "interim", "unavailable") {
		return fmt.Errorf("%s:%d.maturityStatus is invalid", source, line)
	}
	if fieldIsNull(fields, "adoptionAt") {
		return fmt.Errorf("%s:%d.adoptionAt must be an ISO-8601 timestamp", source, line)
	}
	if record.AdoptionAt != nil {
		value, err := parseTimestamp(*record.AdoptionAt)
		if err != nil {
			return fmt.Errorf("%s:%d.adoptionAt must be an ISO-8601 timestamp", source, line)
		}
		canonical := formatTimestamp(value)
		record.AdoptionAt = &canonical
	}
	if fieldIsNull(fields, "evaluationMode") ||
		(record.EvaluationMode != nil &&
			!oneOf(*record.EvaluationMode, "baseline-comparable", "attainment-only")) {
		return fmt.Errorf("%s:%d.evaluationMode is invalid", source, line)
	}
	if fieldIsNull(fields, "evaluatorDigest") ||
		(record.EvaluatorDigest != nil && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(*record.EvaluatorDigest)) {
		return fmt.Errorf("%s:%d.evaluatorDigest must be a SHA-256 fingerprint", source, line)
	}
	for name, value := range map[string]*string{
		"workflowSlug": record.WorkflowSlug, "workflowName": record.WorkflowName,
	} {
		if fieldIsNull(fields, name) {
			return fmt.Errorf("%s:%d.%s must be a non-empty string", source, line, name)
		}
		if value != nil {
			trimmed := strings.TrimSpace(*value)
			if trimmed == "" {
				return fmt.Errorf("%s:%d.%s must be a non-empty string", source, line, name)
			}
			*value = trimmed
		}
	}
	_, hasNumerator := fields["rollupNumerator"]
	_, hasDenominator := fields["rollupDenominator"]
	if hasNumerator != hasDenominator ||
		(record.Value == nil && hasNumerator) ||
		(hasNumerator &&
			(record.RollupNumerator == nil || record.RollupDenominator == nil ||
				!finiteNonNegative(*record.RollupNumerator) ||
				!finitePositive(*record.RollupDenominator))) {
		return fmt.Errorf("%s:%d must emit a valid rollup numerator and denominator together", source, line)
	}
	return nil
}

func validatePeriod(record *valueRecord, fields map[string]json.RawMessage, observed time.Time, source string, line int) error {
	hasPeriod := false
	for _, name := range []string{"windowStart", "windowEnd", "measurementKind", "coverageStatus"} {
		_, present := fields[name]
		hasPeriod = hasPeriod || present
	}
	if hasPeriod {
		if record.WindowStart == nil || record.WindowEnd == nil || record.MeasurementKind == nil || record.CoverageStatus == nil {
			return fmt.Errorf("%s:%d must emit a valid measurement period and coverage", source, line)
		}
		start, startErr := parseTimestamp(*record.WindowStart)
		end, endErr := parseTimestamp(*record.WindowEnd)
		if startErr != nil || endErr != nil || !start.Before(end) || end.After(observed) ||
			!oneOf(*record.MeasurementKind, "interval", "snapshot") ||
			!oneOf(*record.CoverageStatus, "complete", "partial", "missing", "unknown") {
			return fmt.Errorf("%s:%d must emit a valid measurement period and coverage", source, line)
		}
		*record.WindowStart, *record.WindowEnd = formatTimestamp(start), formatTimestamp(end)
	}
	for name, value := range map[string]*string{"populationDigest": record.PopulationDigest, "evidenceDigest": record.EvidenceDigest} {
		if fieldIsNull(fields, name) || (value != nil && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(*value)) {
			return fmt.Errorf("%s:%d.%s must be a SHA-256 fingerprint", source, line, name)
		}
	}
	if fieldIsNull(fields, "eligibleCount") || (record.EligibleCount != nil &&
		(!finiteNonNegative(*record.EligibleCount) || math.Trunc(*record.EligibleCount) != *record.EligibleCount || *record.EligibleCount > 9007199254740991)) {
		return fmt.Errorf("%s:%d.eligibleCount must be a non-negative safe integer", source, line)
	}
	if fieldIsNull(fields, "missingReason") || (record.MissingReason != nil && strings.TrimSpace(*record.MissingReason) == "") {
		return fmt.Errorf("%s:%d.missingReason must be a non-empty string", source, line)
	}
	if record.MissingReason != nil {
		*record.MissingReason = strings.TrimSpace(*record.MissingReason)
	}
	return nil
}

func readRetained(path string, cutoff time.Time) ([]envelope, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path) // #nosec G304 -- operator-configured evidence-lake path.
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var retained []envelope
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var item envelope
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, err
		}
		if item.SchemaVersion != 2 || item.Kind != "operational_value" {
			return nil, fmt.Errorf("%s:%d is not an operational value envelope", path, line)
		}
		timestamp, err := parseTimestamp(item.OperationalValue.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("%s:%d has an invalid operational value timestamp", path, line)
		}
		if (cutoff.IsZero() || !timestamp.Before(cutoff)) &&
			strings.TrimSpace(item.OperationalValue.Campaign) != "" {
			retained = append(retained, item)
		}
	}
	return retained, scanner.Err()
}

func retireValues(values []envelope, active map[string]map[string]struct{}) []envelope {
	output := values[:0]
	for _, item := range values {
		ids, found := active[item.OperationalValue.Campaign]
		if !found {
			output = append(output, item)
			continue
		}
		if _, exists := ids[item.OperationalValue.ValueID]; exists {
			output = append(output, item)
		}
	}
	return output
}

func mergeValues(values []envelope, definitions map[string]definition) []envelope {
	index := map[string]int{}
	merged := make([]envelope, 0, len(values))
	for _, item := range values {
		key := valueKey(item.OperationalValue)
		if definition, ok := definitions[definitionKey(
			item.OperationalValue.Campaign, item.OperationalValue.ValueID,
		)]; ok {
			key = cadenceKey(item.OperationalValue, definition)
		}
		if position, exists := index[key]; exists {
			current, _ := parseTimestamp(merged[position].OperationalValue.Timestamp)
			candidate, _ := parseTimestamp(item.OperationalValue.Timestamp)
			if !candidate.Before(current) {
				merged[position] = item
			}
			continue
		}
		index[key] = len(merged)
		merged = append(merged, item)
	}
	return merged
}

func writeEnvelopes(path string, values []envelope) error {
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	encoder.SetEscapeHTML(false)
	for _, item := range values {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return writeContent(path, content.Bytes())
}

func writeContent(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".operational-values-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func valueKey(value operationalValue) string {
	return strings.ToLower(strings.TrimSpace(value.Campaign)) + "\x00" +
		strings.ToLower(value.Repository) + "\x00" + value.ValueID + "\x00" + value.Timestamp
}

func definitionKey(campaign, valueID string) string {
	return strings.ToLower(campaign) + "\x00" + valueID
}

func cadenceKey(value operationalValue, item definition) string {
	if value.WindowStart != nil && value.WindowEnd != nil && value.MeasurementKind != nil {
		digest := "unversioned"
		if value.EvaluatorDigest != nil {
			digest = *value.EvaluatorDigest
		}
		return strings.ToLower(value.Campaign) + "\x00" + strings.ToLower(value.Repository) +
			"\x00" + value.ValueID + "\x00" + digest + "\x00" + *value.MeasurementKind +
			"\x00" + *value.WindowStart + "\x00" + *value.WindowEnd
	}
	cadence := time.Duration(item.CadenceDays * float64(day))
	timestamp, _ := parseTimestamp(value.Timestamp)
	bucket := math.Ceil(float64(timestamp.Sub(item.AdoptedAt)) / float64(cadence))
	end := item.AdoptedAt.Add(time.Duration(bucket) * cadence)
	return strings.ToLower(value.Campaign) + "\x00" + strings.ToLower(value.Repository) +
		"\x00" + value.ValueID + "\x00" + formatTimestamp(end)
}

func normalizeRepositories(repositories []string) ([]string, error) {
	seen := map[string]string{}
	for _, repository := range repositories {
		if !repositoryPattern.MatchString(repository) {
			return nil, fmt.Errorf("invalid repository: %s", repository)
		}
		key := strings.ToLower(repository)
		if _, exists := seen[key]; !exists {
			seen[key] = repository
		}
	}
	output := make([]string, 0, len(seen))
	for _, repository := range seen {
		output = append(output, repository)
	}
	sort.Slice(output, func(i, j int) bool {
		return strings.ToLower(output[i]) < strings.ToLower(output[j])
	})
	return output, nil
}

func uniqueLower(values []string) []string {
	seen := map[string]struct{}{}
	output := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(value)
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			output = append(output, value)
		}
	}
	return output
}

func parseTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func finitePositive(value float64) bool {
	return finiteNonNegative(value) && value > 0
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func fieldIsNull(fields map[string]json.RawMessage, name string) bool {
	value, exists := fields[name]
	return exists && bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}
