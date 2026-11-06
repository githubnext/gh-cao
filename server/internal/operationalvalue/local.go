package operationalvalue

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

const hostedQueryLimit = 32

type QueryExecutor func(context.Context, []query.Definition, []string) (map[string]model.Source, error)

type LocalValuePlan struct {
	Repositories        []string `json:"repositories"`
	DisabledCampaigns   []string `json:"disabledCampaigns"`
	DisabledWorkflows   []string `json:"disabledWorkflows"`
	DisabledCollections []string `json:"disabledCollections"`
}

func WriteCollectionHealth(ctx context.Context, config Config, policy, snapshot string) error {
	document, reviewed, err := loadValuePolicy(ctx, policy)
	if err != nil {
		return err
	}
	shards := filepath.Join(snapshot, "gh-aw-logs-shards")
	if err := os.MkdirAll(shards, 0o700); err != nil {
		return err
	}
	var inventory struct {
		Repositories model.Source `json:"repositories"`
		Workflows    model.Source `json:"workflows"`
	}
	content, err := readBoundedValueFile(filepath.Join(snapshot, "inventory-sources.json"), 16<<20)
	if err != nil || json.Unmarshal(content, &inventory) != nil {
		return errors.New("invalid canonical Value inventory")
	}
	type target struct {
		Repository string
		Workflow   string
		Collector  string
		Enabled    *bool
		Evidence   json.RawMessage
	}
	targets := map[string]target{}
	include := func(entry target) {
		if valuePolicyInScope(&reviewed, config.Repositories, entry.Repository) {
			targets[strings.ToLower(entry.Repository)+":"+entry.Workflow] = entry
		}
	}
	previous := map[string]model.Row{}
	healthFile := filepath.Join(shards, "workflow-values-collections.jsonl")
	if content, err := readBoundedValueFile(healthFile, 16<<20); err == nil {
		for _, line := range bytes.Split(content, []byte{'\n'}) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var envelope struct {
				SchemaVersion int       `json:"schema_version"`
				Kind          string    `json:"kind"`
				Record        model.Row `json:"workflow_value_collection"`
			}
			if json.Unmarshal(line, &envelope) != nil || envelope.SchemaVersion != 2 || envelope.Kind != "workflow_value_collection" ||
				!valueRepositoryPattern.MatchString(textField(envelope.Record, "repository")) ||
				!valueWorkflowPattern.MatchString(textField(envelope.Record, "workflow")) {
				return errors.New("invalid retained Value health")
			}
			repository, workflow := textField(envelope.Record, "repository"), textField(envelope.Record, "workflow")
			include(target{Repository: repository, Workflow: workflow})
			previous[strings.ToLower(repository)+":"+workflow] = envelope.Record
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if inventory.Repositories.Metadata["availability"] != "unavailable" &&
		inventory.Workflows.Metadata["availability"] != "unavailable" {
		repositories := map[string]bool{}
		for _, row := range inventory.Repositories.Rows {
			owner, name := textField(row, "organization"), textField(row, "repository")
			if owner == "" {
				owner = textField(row, "owner")
				name = textField(row, "name")
			}
			repositories[strings.ToLower(owner+"/"+name)] = true
		}
		for _, row := range inventory.Workflows.Rows {
			path := textField(row, "workflow")
			if path == "" {
				path = textField(row, "path")
			}
			if strings.HasPrefix(path, ".github/workflows/") && strings.HasSuffix(path, ".md") {
				workflow := strings.TrimSuffix(strings.TrimPrefix(path, ".github/workflows/"), ".md")
				repository := textField(row, "organization") + "/" + textField(row, "repository")
				if valueWorkflowPattern.MatchString(workflow) && repositories[strings.ToLower(repository)] {
					include(target{Repository: repository, Workflow: workflow})
				}
			}
		}
	}
	activity := reviewed.Control.Campaigns["activity"]
	for _, entry := range activity.Values {
		include(target{Repository: entry.Repository, Workflow: entry.Workflow,
			Collector: entry.Collector, Enabled: entry.Enabled, Evidence: entry.Evidence})
	}
	if len(targets) > 10000 {
		return errors.New("reviewed Value collection inventory exceeds its row budget")
	}
	now := time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
	digest := valuePolicyDigest(document)
	disabled := activity.Enabled != nil && !*activity.Enabled
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var health, packets bytes.Buffer
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := targets[key]
		state, reason, registration := "unconfigured", "unconfigured", "unconfigured"
		if entry.Enabled != nil && !*entry.Enabled {
			state, reason, registration = "disabled", "disabled", "disabled"
		} else if entry.Collector != "" {
			registration = "enabled"
			if disabled {
				state, reason = "disabled", "activity-disabled"
			} else {
				state, reason = "deferred", "shared-budget-unavailable"
			}
		}
		record := model.Row{
			"schemaVersion": 1,
			"id":            "workflow-value-collection:" + url.QueryEscape(strings.ToLower(entry.Repository)+":"+entry.Workflow),
			"repository":    strings.ToLower(entry.Repository), "workflow": entry.Workflow,
			"sourcePath":   ".github/workflows/" + entry.Workflow + ".md",
			"policyDigest": digest, "observedAt": now,
			"registration": registration, "state": state, "reasonCode": reason,
			"reason": valueHealthReason[reason],
		}
		if entry.Collector != "" {
			record["collector"] = entry.Collector
		}
		if retained := previous[key]; retained != nil && retained["lastAttemptAt"] != nil {
			record["lastAttemptAt"], record["lastAttemptOutcome"] = retained["lastAttemptAt"], retained["lastAttemptOutcome"]
			at, err := time.Parse(time.RFC3339Nano, textField(retained, "lastAttemptAt"))
			if err != nil {
				return errors.New("invalid retained Value attempt time")
			}
			if registration == "enabled" && !disabled && retained["policyDigest"] == digest &&
				!at.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				previousState, previousReason := textField(retained, "state"), textField(retained, "reasonCode")
				if previousState == "available" || previousState == "failed" {
					record["state"], record["reasonCode"], record["reason"] =
						previousState, previousReason, collectionReason(previousReason)
				}
			}
		}
		if err := json.NewEncoder(&health).Encode(map[string]any{
			"schema_version": 2, "kind": "workflow_value_collection", "workflow_value_collection": record,
		}); err != nil {
			return err
		}
		if len(entry.Evidence) != 0 && !disabled && registration == "enabled" {
			packet, err := readReviewedValuePacket(config.Root, entry)
			if err != nil {
				return err
			}
			if err := json.NewEncoder(&packets).Encode(map[string]any{
				"schema_version": 2, "kind": "operational_value_evidence", "operational_value_evidence": packet,
			}); err != nil {
				return err
			}
		}
	}
	if health.Len() > 16<<20 || packets.Len() > 16<<20 {
		return errors.New("hosted Value preparation exceeds its byte budget")
	}
	eligible := make([]string, 0, len(config.Repositories))
	for _, repository := range config.Repositories {
		if valuePolicyInScope(&reviewed, config.Repositories, repository) {
			eligible = append(eligible, repository)
		}
	}
	request, err := json.Marshal([]any{map[string]any{"schemaVersion": 1, "snapshot": "hosted",
		"policyDigest": digest, "eligible": !disabled,
		"reason":       map[bool]string{true: "activity-disabled", false: "shared-budget-unavailable"}[disabled],
		"repositories": eligible}})
	if err != nil {
		return err
	}
	for _, output := range []struct {
		name    string
		content []byte
	}{
		{healthFile, health.Bytes()},
		{filepath.Join(shards, "workflow-values.jsonl"), packets.Bytes()},
		{filepath.Join(shards, "workflow-value-local.json"), request},
	} {
		if err := os.WriteFile(output.name, output.content, 0o600); err != nil { // #nosec G304 -- private hosted transport names.
			return err
		}
	}
	return nil
}

var valueHealthReason = map[string]string{
	"unconfigured":              "No collector is registered for this workflow in the reviewed policy.",
	"disabled":                  "The reviewed registration explicitly disables future collection; retained evidence is unchanged.",
	"activity-disabled":         "Activity collection is disabled in the reviewed policy.",
	"shared-budget-unavailable": "Automatic remote acquisition is deferred until shared API request budgets can be enforced. Activity retains priority.",
	"captured":                  "Accepted evidence is available at its original capture and cutoff dates.",
	"not-due":                   "The frozen cadence is already covered by retained evidence; no new evidence was acquired.",
	"api-budget":                "GitHub API capacity is exhausted or reserved for ordinary Activity collection.",
	"timeout":                   "The collector exceeded its time budget; previous accepted evidence is retained.",
	"collector-failed":          "The collector failed; previous accepted evidence is retained.",
	"invalid-evidence":          "The collector returned invalid or incomplete evidence; previous accepted evidence is retained.",
	"batch-budget":              "The collection batch exhausted its bounded time or output budget.",
}

func collectionReason(reason string) string { return valueHealthReason[reason] }

// CollectionAttempt updates only the actual-attempt clock and its decision.
// Callers publish this with the accepted packet under the same enrollment
// epoch; a cost record alone is not evidence that a capture succeeded.
func CollectionAttempt(previous model.Row, at time.Time, state, reason string) (model.Row, error) {
	if previous == nil || at.IsZero() || (state != "available" && state != "failed") {
		return nil, errors.New("invalid Value collection attempt")
	}
	switch state + ":" + reason {
	case "available:captured", "available:not-due",
		"failed:api-budget", "failed:timeout", "failed:collector-failed",
		"failed:invalid-evidence", "failed:batch-budget":
	default:
		return nil, errors.New("invalid Value collection attempt reason")
	}
	if previous["registration"] != "enabled" || !valueRepositoryPattern.MatchString(textField(previous, "repository")) ||
		!valueWorkflowPattern.MatchString(textField(previous, "workflow")) ||
		!sha256Fingerprint.MatchString(textField(previous, "policyDigest")) {
		return nil, errors.New("Value collection attempt has no reviewed registration")
	}
	at = at.UTC().Truncate(time.Millisecond)
	if previousAt := textField(previous, "lastAttemptAt"); previousAt != "" {
		prior, err := time.Parse(time.RFC3339Nano, previousAt)
		if err != nil || at.Before(prior) {
			return nil, errors.New("Value collection attempt precedes retained attempt")
		}
	}
	record := make(model.Row, len(previous))
	for key, value := range previous {
		record[key] = value
	}
	record["observedAt"] = optimizerTime(time.Now())
	record["state"], record["reasonCode"], record["reason"] = state, reason, collectionReason(reason)
	record["lastAttemptAt"], record["lastAttemptOutcome"] = optimizerTime(at), state
	return record, nil
}

func textField(row model.Row, key string) string {
	text, _ := row[key].(string)
	return text
}

func readReviewedValuePacket(root string, entry struct {
	Repository string
	Workflow   string
	Collector  string
	Enabled    *bool
	Evidence   json.RawMessage
}) (model.Row, error) {
	var evidence struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(entry.Evidence, &evidence); err != nil {
		return nil, err
	}
	path := root
	for _, component := range strings.Split(evidence.Path, "/") {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("reviewed Value evidence is missing or symlinked")
		}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, errors.New("reviewed Value evidence is not a bounded regular file")
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- confined reviewed evidence file with SHA-256 pin.
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".gz") {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()
		raw, err = io.ReadAll(io.LimitReader(reader, (8<<20)+1))
		if err != nil {
			return nil, err
		}
	}
	hash := sha256.Sum256(raw)
	if len(raw) > 8<<20 || fmt.Sprintf("%x", hash) != evidence.SHA256 {
		return nil, errors.New("reviewed Value evidence digest mismatch")
	}
	var envelope struct {
		SchemaVersion int       `json:"schema_version"`
		Kind          string    `json:"kind"`
		Packet        model.Row `json:"operational_value_evidence"`
	}
	var envelopeFields map[string]json.RawMessage
	if json.Unmarshal(raw, &envelopeFields) != nil || len(envelopeFields) != 3 ||
		json.Unmarshal(raw, &envelope) != nil || envelope.SchemaVersion != 2 ||
		envelope.Kind != "operational_value_evidence" ||
		!strings.EqualFold(textField(envelope.Packet, "repository"), entry.Repository) ||
		textField(envelope.Packet, "workflow") != entry.Workflow ||
		textField(envelope.Packet, "sourcePath") != ".github/workflows/"+entry.Workflow+".md" {
		return nil, errors.New("reviewed Value evidence identity mismatch")
	}
	capture, err := time.Parse(time.RFC3339Nano, textField(envelope.Packet, "observedAt"))
	if err != nil || capture.After(time.Now()) {
		return nil, errors.New("reviewed Value evidence has invalid capture time")
	}
	return envelope.Packet, nil
}

func ReadLocalValuePlan(ctx context.Context, config Config, policy, snapshot string) (LocalValuePlan, error) {
	document, reviewed, err := loadValuePolicy(ctx, policy)
	if err != nil {
		return LocalValuePlan{}, err
	}
	plan := LocalValuePlan{Repositories: []string{}, DisabledCampaigns: []string{},
		DisabledWorkflows: []string{}, DisabledCollections: []string{}}
	content, err := readBoundedValueFile(filepath.Join(snapshot, "gh-aw-logs-shards", "workflow-value-local.json"), 16<<20)
	if errors.Is(err, os.ErrNotExist) {
		return plan, nil
	}
	if err != nil {
		return plan, errors.New("invalid local Value request")
	}
	var rawRequests []json.RawMessage
	if json.Unmarshal(content, &rawRequests) != nil || len(rawRequests) > 1000 {
		return plan, errors.New("invalid local Value requests")
	}
	var requests []struct {
		SchemaVersion int      `json:"schemaVersion"`
		Snapshot      string   `json:"snapshot"`
		PolicyDigest  string   `json:"policyDigest"`
		Eligible      bool     `json:"eligible"`
		Repositories  []string `json:"repositories"`
	}
	if json.Unmarshal(content, &requests) != nil {
		return plan, errors.New("invalid local Value requests")
	}
	repositories := map[string]bool{}
	for i, request := range requests {
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawRequests[i], &fields) != nil ||
			string(fields["eligible"]) != "true" && string(fields["eligible"]) != "false" {
			return plan, errors.New("invalid local Value eligibility request")
		}
		if request.SchemaVersion != 1 || request.Repositories == nil || len(request.Repositories) > 10000 {
			return plan, errors.New("invalid local Value request")
		}
		if !request.Eligible || request.Snapshot != "hosted" || request.PolicyDigest != valuePolicyDigest(document) {
			continue
		}
		for _, repository := range request.Repositories {
			if !valueRepositoryPattern.MatchString(repository) {
				return plan, errors.New("invalid local Value repository request")
			}
			if valuePolicyInScope(&reviewed, request.Repositories, repository) {
				repositories[strings.ToLower(repository)] = true
			}
		}
	}
	for name, campaign := range reviewed.Control.Campaigns {
		if campaign.Enabled != nil && !*campaign.Enabled {
			plan.DisabledCampaigns = append(plan.DisabledCampaigns, name)
		}
	}
	for name, workflow := range reviewed.Control.Workflows {
		if workflow.Enabled != nil && !*workflow.Enabled {
			plan.DisabledWorkflows = append(plan.DisabledWorkflows, name)
		}
	}
	for _, entry := range reviewed.Control.Campaigns["activity"].Values {
		if entry.Enabled != nil && !*entry.Enabled {
			plan.DisabledCollections = append(plan.DisabledCollections, strings.ToLower(entry.Repository)+":"+entry.Workflow)
		}
	}
	if activity := reviewed.Control.Campaigns["activity"]; activity.Enabled == nil || *activity.Enabled {
		for repository := range repositories {
			plan.Repositories = append(plan.Repositories, repository)
		}
	}
	sort.Strings(plan.Repositories)
	sort.Strings(plan.DisabledCampaigns)
	sort.Strings(plan.DisabledWorkflows)
	return plan, nil
}

func ComputeHostedSnapshot(
	ctx context.Context,
	config Config,
	policy, snapshot, output string,
	execute QueryExecutor,
) error {
	if execute == nil {
		return errors.New("hosted local Value requires the canonical PostgreSQL query boundary")
	}
	plan, err := ReadLocalValuePlan(ctx, config, policy, snapshot)
	if err != nil {
		return err
	}
	if len(plan.Repositories) == 0 {
		return os.WriteFile(output, nil, 0o600)
	}
	if err := verifyHostedLocalAdapters(config.Root, plan.DisabledCampaigns); err != nil {
		return err
	}
	_, reviewed, err := loadValuePolicy(ctx, policy)
	if err != nil {
		return err
	}
	optimizationEnabled := !slices.Contains(plan.DisabledCampaigns, "optimization") &&
		!slices.Contains(plan.DisabledWorkflows, "optimization-token-optimizer")
	// This is the one reviewed local-only adapter. Its implementation and
	// fingerprint are pinned against the independent JS oracle in tests.
	const evaluatorDigest = "5932b3eb53899fd949e92c6219d562ea853808cfb54d152e0c279ba41e48eacb"
	const adoptionAt = "2026-09-15T23:30:36Z"
	adoption, _ := time.Parse(time.RFC3339, adoptionAt)
	supported := map[string]bool{
		"github/gh-aw": true, "github/gh-aw-actions": true, "github/gh-aw-firewall": true,
		"github/gh-aw-mcpg": true, "github/gh-aw-threat-detection": true, "githubnext/gh-cao": true,
	}
	observed := time.Now().UTC().Truncate(time.Millisecond)
	if !config.ObservedAt.IsZero() {
		observed = config.ObservedAt.UTC().Truncate(time.Millisecond)
	}
	type window struct {
		repository string
		start, end time.Time
	}
	windows := make([]window, 0, len(plan.Repositories))
	for _, repository := range plan.Repositories {
		if !optimizationEnabled {
			break
		}
		if !supported[repository] ||
			slices.Contains(plan.DisabledCollections, repository+":optimization-token-optimizer") {
			continue
		}
		end := adoption.Add(time.Duration(math.Floor(observed.Sub(adoption).Hours()/24)) * 24 * time.Hour)
		windows = append(windows, window{repository, end.Add(-7 * 24 * time.Hour), end})
	}
	if len(windows) > hostedQueryLimit {
		return errors.New("hosted local Value exceeded its canonical query budget")
	}
	metricFields := []struct {
		id, name, unit string
		role           string
	}{
		{"aic-per-successful-run", "AI Credit per successful run", "aic-per-run", "primary"},
		{"failure-rate-percent", "Failure rate", "percent", "diagnostic"},
		{"cancellation-rate-percent", "Cancellation rate", "percent", "diagnostic"},
	}
	var result bytes.Buffer
	for _, selected := range windows {
		if err := ctx.Err(); err != nil {
			return err
		}
		byConclusion := func(values ...string) *query.Filter {
			items := make([]any, len(values))
			for i, value := range values {
				items[i] = value
			}
			return &query.Filter{Predicates: []query.Predicate{{Field: "conclusion", In: items}}}
		}
		success := byConclusion("success")
		definition := query.Definition{
			Name: "optimization-repository-efficiency", From: "runs",
			Joins: []query.Join{{Source: "workflows", Type: "inner",
				On:     []query.JoinKey{{Left: "workflowId", Right: "id"}},
				Fields: []query.SelectedField{{Field: "path", As: "declaredWorkflowPath"}}}},
			Filter: &query.Filter{Predicates: []query.Predicate{
				{Field: "startedAt", GTE: selected.start.UTC().Format(time.RFC3339Nano)},
				{Field: "startedAt", LT: selected.end.UTC().Format(time.RFC3339Nano)},
				{Field: "declaredWorkflowPath", Includes: ".md"},
				{Field: "owner", Equals: strings.Split(selected.repository, "/")[0]},
				{Field: "repository", Equals: strings.Split(selected.repository, "/")[1]},
			}},
			Aggregate: &query.Aggregate{
				By: []string{"owner", "repository", "declaredWorkflowPath"},
				Values: []query.AggregateValue{
					{Field: "githubRunId", As: "concludedRuns", Reducer: "count", Filter: byConclusion("success", "failure", "cancelled", "timed_out", "startup_failure", "action_required", "neutral", "skipped", "stale")},
					{Field: "githubRunId", As: "successfulRuns", Reducer: "count", Filter: success},
					{Field: "aicTotal", As: "successfulRunsWithAic", Reducer: "count", Filter: success},
					{Field: "aicTotal", As: "aicPerSuccessfulRun", Reducer: "mean", Filter: success},
					{Field: "githubRunId", As: "failedRuns", Reducer: "count", Filter: byConclusion("failure", "timed_out", "startup_failure")},
					{Field: "githubRunId", As: "cancelledRuns", Reducer: "count", Filter: byConclusion("cancelled")},
				},
			},
		}
		sources, err := execute(ctx, []query.Definition{definition}, []string{definition.Name})
		if err != nil {
			return fmt.Errorf("hosted canonical Value query: %w", err)
		}
		source, ok := sources[definition.Name]
		if !ok || source.Metadata["availability"] != "available" {
			return errors.New("hosted canonical Value query is unavailable")
		}
		revision, ok := source.Metadata["canonical-revision"]
		if !ok {
			return errors.New("hosted canonical Value query did not identify its revision")
		}
		provenance := []struct {
			Repository string `json:"repository"`
			Kind       string `json:"kind"`
			Ref        string `json:"ref"`
		}{{selected.repository, "cao-canonical-query-sqlite-sha256",
			valuePolicyDigest([]byte(fmt.Sprint(revision)))}}
		eligibleWorkflows := []string{}
		var successes, failures, cancelled, concluded int
		var aicTotal float64
		for _, row := range source.Rows {
			path := textField(row, "declaredWorkflowPath")
			if strings.Contains(path, "/optimization-") || strings.HasSuffix(path, "/optimization.md") {
				continue
			}
			total, totalOK := safeValueCount(row["concludedRuns"])
			successCount, successOK := safeValueCount(row["successfulRuns"])
			withAIC, aicOK := safeValueCount(row["successfulRunsWithAic"])
			failedCount, failedOK := safeValueCount(row["failedRuns"])
			cancelCount, cancelOK := safeValueCount(row["cancelledRuns"])
			mean, meanOK := safeValueNumber(row["aicPerSuccessfulRun"])
			if path == "" || !totalOK || !successOK || !aicOK || !failedOK ||
				!cancelOK || !meanOK || mean < 0 || total < 3 || successCount == 0 || withAIC != successCount {
				continue
			}
			eligibleWorkflows = append(eligibleWorkflows, selected.repository+"/"+path)
			successes += successCount
			concluded += total
			failures += failedCount
			cancelled += cancelCount
			aicTotal += mean * float64(successCount)
		}
		sort.Strings(eligibleWorkflows)
		populationJSON, err := json.Marshal(eligibleWorkflows)
		if err != nil {
			return err
		}
		const opportunity = "A declared non-Optimization agentic workflow in the authorized target repository with at least three completed runs, at least one successful run, and complete successful-run AI Credit coverage in the observation window."
		filters := []string{
			"Measure runs in the authorized target repository, never the control repository as a proxy.",
			"Include declared agentic workflow sources and exclude the Optimization campaign itself.",
			"Require at least three completed runs and at least one successful run in the observation window.",
			"Require authoritative AI Credit for every successful run in the observation window.",
			"Measure native AI Credit per successful run, failed-run percentage, and cancelled-run percentage without normalizing them to a synthetic score.",
		}
		const collection = "Execute one bounded canonical Activity query over target-repository runs in the observation window, grouped by declared workflow. Apply the identical native formulas before and after adoption because AI Credit and run conclusions predate the Optimization workflow. Aggregate only eligible workflow rows into native repository measures. No eligible workflow or incomplete AI Credit evidence is missing."
		type evidenceWindow struct {
			Start          string `json:"start"`
			End            string `json:"end"`
			ObservedAt     string `json:"observedAt"`
			DurationDays   int    `json:"durationDays"`
			CadenceDays    int    `json:"cadenceDays"`
			MaturationDays int    `json:"maturationDays"`
		}
		type evidenceRecord struct {
			Key                              string         `json:"key"`
			Repositories                     []string       `json:"repositories"`
			Opportunity                      string         `json:"opportunity"`
			Filters                          []string       `json:"filters"`
			Collection                       string         `json:"collection"`
			Window                           evidenceWindow `json:"window"`
			MaturityStatus                   string         `json:"maturityStatus"`
			Dubious                          bool           `json:"dubious"`
			CoverageStatus                   string         `json:"coverageStatus"`
			MissingReason                    string         `json:"missingReason"`
			EligibleCount                    int            `json:"eligibleCount"`
			PopulationDigest                 string         `json:"populationDigest"`
			EligibleWorkflowCount            int            `json:"eligibleWorkflowCount"`
			SuccessfulRunCount               int            `json:"successfulRunCount"`
			SuccessfulRunAicTotal            float64        `json:"successfulRunAicTotal"`
			ConcludedRunCount                int            `json:"concludedRunCount"`
			FailedRunCount                   int            `json:"failedRunCount"`
			CancelledRunCount                int            `json:"cancelledRunCount"`
			FailedRunPercentagePointTotal    int            `json:"failedRunPercentagePointTotal"`
			CancelledRunPercentagePointTotal int            `json:"cancelledRunPercentagePointTotal"`
		}
		status := "matured"
		if selected.end.Sub(selected.start) < 7*24*time.Hour {
			status = "interim"
		}
		missingReason := "source-window-coverage-unverified"
		if len(eligibleWorkflows) == 0 {
			missingReason = "no-eligible-opportunities"
		}
		evidence := evidenceRecord{
			"target-repository-agentic-workflow-efficiency", []string{selected.repository},
			opportunity, filters, collection,
			evidenceWindow{optimizerTime(selected.start), optimizerTime(selected.end),
				optimizerTime(observed), 7, 1, 0},
			status, status == "interim", "unknown", missingReason,
			len(eligibleWorkflows), valuePolicyDigest(populationJSON), len(eligibleWorkflows),
			successes, optimizerRound(aicTotal), concluded, failures, cancelled, failures * 100, cancelled * 100,
		}
		evidenceJSON, err := json.Marshal(struct {
			Evidence   evidenceRecord `json:"evidence"`
			Provenance any            `json:"provenance"`
		}{evidence, provenance})
		if err != nil {
			return err
		}
		for _, metric := range metricFields {
			var value any
			var numerator, denominator float64
			switch metric.id {
			case "aic-per-successful-run":
				numerator, denominator = evidence.SuccessfulRunAicTotal, float64(successes)
			case "failure-rate-percent":
				numerator, denominator = float64(failures*100), float64(concluded)
			case "cancellation-rate-percent":
				numerator, denominator = float64(cancelled*100), float64(concluded)
			}
			if len(eligibleWorkflows) != 0 && denominator > 0 {
				value = optimizerRound(numerator / denominator)
			}
			maturity := status
			if value == nil {
				maturity = "unavailable"
			}
			record := map[string]any{
				"timestamp": optimizerTime(observed), "repository": selected.repository,
				"campaign": "optimization", "campaign_id": "campaign:optimization",
				"value_id": "optimization-token-optimizer." + metric.id, "value": value,
				"metric_role": metric.role, "metric_name": metric.name, "metric_unit": metric.unit,
				"metric_direction": "decrease", "maturity_status": maturity,
				"adoption_at": adoptionAt, "evaluation_mode": "baseline-comparable",
				"workflow_slug": "optimization-token-optimizer", "workflow_name": "Optimization / Token Optimizer",
				"evaluator_digest": evaluatorDigest, "cadence_days": 1,
				"window_start": optimizerTime(selected.start), "window_end": optimizerTime(selected.end),
				"measurement_kind": "interval", "coverage_status": "unknown", "missing_reason": missingReason,
				"eligible_count": len(eligibleWorkflows), "population_digest": evidence.PopulationDigest,
				"evidence_digest": valuePolicyDigest(evidenceJSON),
			}
			if value != nil {
				record["rollup_numerator"], record["rollup_denominator"] = numerator, denominator
			}
			if err := json.NewEncoder(&result).Encode(map[string]any{
				"schema_version": 2, "kind": "operational_value", "operational_value": record,
			}); err != nil {
				return err
			}
		}
	}
	if reviewedDependabotLocalValue(reviewed, plan) {
		for _, repository := range plan.Repositories {
			if repository == "github/gh-aw" {
				if err := writeDependabotValue(ctx, &result, observed,
					reviewedDependabotEvidence(reviewed), execute); err != nil {
					return err
				}
			}
		}
	}
	if result.Len() > 16<<20 {
		return errors.New("hosted local Value output exceeds its byte budget")
	}
	return os.WriteFile(output, result.Bytes(), 0o600) // #nosec G304 -- private hosted output path.
}

func reviewedDependabotLocalValue(reviewed valuePolicy, plan LocalValuePlan) bool {
	if slices.Contains(plan.DisabledCampaigns, "dependabot") ||
		slices.Contains(plan.DisabledWorkflows, "dependabot-update-planner") {
		return false
	}
	campaign, ok := reviewed.Control.Campaigns["dependabot"]
	if !ok {
		return false
	}
	worker, ok := campaign.Workers["update-planner"]
	if !ok || worker.Workflow != "dependabot-update-planner" ||
		worker.Enabled != nil && !*worker.Enabled {
		return false
	}
	target, ok := campaign.Targets["github/gh-aw"]
	return ok && (target.Mode == "review" || target.Mode == "live")
}

func writeUnavailableDependabotValue(output *bytes.Buffer, observed time.Time) error {
	const adoptionAt = "2026-09-17T08:48:00Z"
	adoption, _ := time.Parse(time.RFC3339, adoptionAt)
	cadence := 7 * 24 * time.Hour
	if observed.Before(adoption.Add(cadence)) {
		return nil
	}
	end := adoption.Add(time.Duration(math.Floor(observed.Sub(adoption).Hours()/cadence.Hours())) * cadence)
	for _, metric := range dependabotMetrics {
		record := map[string]any{
			"timestamp": optimizerTime(observed), "repository": "github/gh-aw",
			"campaign": "dependabot", "campaign_id": "campaign:dependabot",
			"value_id": "dependabot-update-planner." + metric.id, "value": nil,
			"metric_role": metric.role, "metric_name": metric.name, "metric_unit": metric.unit,
			"metric_direction": "decrease", "maturity_status": "unavailable",
			"adoption_at": adoptionAt, "evaluation_mode": "baseline-comparable",
			"workflow_slug": "dependabot-update-planner", "workflow_name": "Dependabot / Update Planner",
			"evaluator_digest": dependabotEvaluator,
			"cadence_days":     7, "window_start": optimizerTime(end.Add(-cadence)),
			"window_end": optimizerTime(end), "measurement_kind": "snapshot",
			"coverage_status": "missing", "missing_reason": "remote-acquisition-unavailable",
		}
		if err := json.NewEncoder(output).Encode(map[string]any{
			"schema_version": 2, "kind": "operational_value", "operational_value": record,
		}); err != nil {
			return err
		}
	}
	return nil
}

func verifyHostedLocalAdapters(root string, disabled []string) error {
	directories, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(directories) > 256 {
		return errors.New("hosted local Value catalog exceeds its campaign budget")
	}
	for _, directory := range directories {
		if !directory.IsDir() || directory.Name() == "activity" ||
			slices.Contains(disabled, directory.Name()) {
			continue
		}
		wrapper := filepath.Join(root, directory.Name(), "operational-value.mjs")
		wrapperBytes, wrapperErr := readBoundedValueFile(wrapper, 1<<20)
		if errors.Is(wrapperErr, os.ErrNotExist) {
			continue
		}
		if wrapperErr != nil {
			return wrapperErr
		}
		if fmt.Sprintf("%x", sha256.Sum256(wrapperBytes)) != "771ab1363f035e771684fc334c38dfeeee1855d691758055d4637373d3617416" {
			return errors.New("hosted local Value wrapper differs from its reviewed Go implementation")
		}
		if directory.Name() != "optimization" && directory.Name() != "dependabot" {
			return errors.New("hosted local Value campaign is not supported by the Go evaluator")
		}
		path := filepath.Join(root, directory.Name(), "operational-value")
		modules, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, module := range modules {
			if !strings.HasSuffix(module.Name(), ".mjs") {
				continue
			}
			expected := "4c69f080d1e7c1d07ee2201585bacefbdbbba652d75163f7bcaed19d294e1546"
			name := "optimization-token-optimizer.mjs"
			if directory.Name() == "dependabot" {
				expected, name = "bb95c37d4e5a7204f5587f1b7732603f88da9fdf7e50933492bc6af90c97d0b9",
					"dependabot-update-planner.mjs"
			}
			if module.Name() != name || !module.Type().IsRegular() {
				return errors.New("hosted local Value adapter is not supported by the Go evaluator")
			}
			content, err := readBoundedValueFile(filepath.Join(path, module.Name()), 1<<20)
			if err != nil ||
				fmt.Sprintf("%x", sha256.Sum256(content)) != expected {
				return errors.New("hosted local Value adapter differs from its reviewed Go implementation")
			}
		}
	}
	return nil
}

func optimizerTime(value time.Time) string {
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func optimizerRound(value float64) float64 {
	return math.Floor(value*1e9+0.5) / 1e9
}

func safeValueNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if ok {
		result, err := number.Float64()
		return result, err == nil && !math.IsNaN(result) && !math.IsInf(result, 0)
	}
	switch number := value.(type) {
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case int64:
		return float64(number), true
	case int:
		return float64(number), true
	}
	return 0, false
}

func safeValueCount(value any) (int, bool) {
	number, ok := safeValueNumber(value)
	return int(number), ok && number >= 0 && number <= math.MaxInt32 && math.Trunc(number) == number
}
