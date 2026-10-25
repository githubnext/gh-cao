package adxtrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func Namespace(endpoint, database string) string {
	sum := sha256.Sum256([]byte(endpoint + "\x00" + database))
	return hex.EncodeToString(sum[:])
}

func Withdraw(ctx context.Context, config Config, namespace, repository string) error {
	s, err := newSpool(ctx, config, namespace)
	if err != nil {
		return err
	}
	return s.withdraw(ctx, repository)
}

func ExportDatasetFile(ctx context.Context, config Config, namespace, path string) (string, error) {
	content, err := readPrivateFile(path, 1<<20)
	if err != nil {
		return "", errors.New("dataset specification must be private and bounded")
	}
	return ExportDataset(ctx, config, namespace, content)
}

type DatasetEntry struct {
	TraceID, TraceVersion, TaskGroup, Split, QualityLabel, LabelEvidence string
	MiningVersion                                                        string          `json:",omitempty"`
	Outcome                                                              *DatasetOutcome `json:",omitempty"`
}

type DatasetOutcome struct {
	OutputID, AuditID, Disposition, RuleVersion, Cutoff string
}

type DatasetSpec struct {
	ID, Transform, Evaluator string
	ToolSchemaEvidence       string
	TrainingApproved         bool
	ToolSchemas              []map[string]any
	Entries                  []DatasetEntry
}

type dataset struct {
	ID, Name, File, Digest, State, Delivered string
	References                               map[string]string
	GroupSplits, Bindings                    map[string]string
	Rows                                     []model.Row
}

func Inspect(ctx context.Context, config Config, namespace string) (Status, error) {
	s, err := newSpool(ctx, config, namespace)
	if err != nil {
		return Status{}, err
	}
	status := Status{Stage: "waiting"}
	err = s.locked(ctx, func(_ *os.Root, state *ledger) error {
		status.Frames = len(state.Frames)
		for _, r := range state.Records {
			if r.State == "eligible" {
				status.Eligible++
			} else {
				status.Gaps++
			}
			if r.Delivered == "" {
				status.Pending++
			}
			if r.Mining != nil && r.Mining.Delivered == "" {
				status.Pending++
			}
			if r.Mining != nil && (r.Mining.State != "ready" || !time.Now().Before(r.Mining.FreshUntil)) {
				status.Gaps++
			}
		}
		status.Degraded = status.Gaps > 0
		return nil
	})
	return status, err
}

// ExportDataset creates a private immutable native-trajectory dataset. It does
// not train or replay anything and requires separately supplied quality evidence.
func ExportDataset(ctx context.Context, config Config, namespace string, specification []byte) (string, error) {
	return exportDatasetAt(ctx, config, namespace, specification, time.Now().UTC())
}

func exportDatasetAt(ctx context.Context, config Config, namespace string, specification []byte, now time.Time) (string, error) {
	if len(specification) > 1<<20 {
		return "", errors.New("dataset specification exceeds budget")
	}
	var spec DatasetSpec
	if err := decode(specification, &spec); err != nil || !spec.TrainingApproved ||
		spec.ID == "" || spec.Transform == "" || spec.Evaluator == "" || spec.ToolSchemaEvidence == "" ||
		len(spec.Entries) == 0 || len(spec.Entries) > 1000 || len(spec.ToolSchemas) == 0 {
		return "", errors.New("dataset requires training-use approval, transform/evaluator, tool schemas and labelled entries")
	}
	tools := map[string]bool{}
	for _, schema := range spec.ToolSchemas {
		name := scalar(schema["name"])
		parameters, valid := schema["parameters"].(map[string]any)
		if name == "" || tools[name] || !valid || parameters["type"] != "object" {
			return "", errors.New("dataset tools require unique names and parameter schemas")
		}
		tools[name] = true
	}
	s, err := newSpool(ctx, config, namespace)
	if err != nil {
		return "", err
	}
	var path string
	err = s.locked(ctx, func(root *os.Root, state *ledger) error {
		specContent, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		id := digest(specContent)
		if old := state.Datasets[id]; old != nil {
			if old.State != "ready" {
				return errors.New("dataset has been invalidated")
			}
			for traceID := range old.References {
				r := state.Records[traceID]
				if config.MiningEnabled && (r == nil || r.Mining == nil || r.Mining.State != "ready" || r.Mining.Delivered == "" || !now.Before(r.Mining.FreshUntil)) {
					return errors.New("dataset mining evidence is no longer current")
				}
			}
			path = config.Directory + string(os.PathSeparator) + old.File
			return nil
		}
		groups, bindings, seen := map[string]string{}, map[string]string{}, map[string]bool{}
		for _, previous := range state.Datasets {
			if previous.Name != spec.ID {
				continue
			}
			for group, split := range previous.GroupSplits {
				groups[group] = split
			}
			for key, group := range previous.Bindings {
				bindings[key] = group
			}
		}
		d := &dataset{ID: id, Name: spec.ID, State: "ready", References: map[string]string{}, GroupSplits: groups, Bindings: bindings}
		var content []byte
		for _, entry := range spec.Entries {
			r := state.Records[entry.TraceID]
			if r == nil || r.State != "eligible" || r.Delivered == "" || r.Version != entry.TraceVersion ||
				entry.TaskGroup == "" || entry.QualityLabel == "" || entry.LabelEvidence == "" || seen[entry.TraceID] ||
				!slices.Contains([]string{"train", "validation", "test"}, entry.Split) {
				return errors.New("dataset entry lacks current ready evidence, labels, grouping or an explicit split")
			}
			if split := groups[entry.TaskGroup]; split != "" && split != entry.Split {
				return errors.New("task-group split leakage")
			}
			groups[entry.TaskGroup], seen[entry.TraceID] = entry.Split, true
			e, err := readEvidence(root, r, config.MaxTraceBytes)
			if err != nil {
				return err
			}
			events, err := reconstruct(e)
			if err != nil {
				return err
			}
			// Run attempts and observed episodes cannot be assigned conflicting
			// groups, even when the caller gave them different task labels.
			keys := []string{"run:" + r.Repository + "/" + r.RunID}
			for _, row := range e.Tables["TraceEvents"] {
				if episode := scalar(row["EpisodeId"]); episode != "" {
					keys = append(keys, "episode:"+episode)
				}
			}
			var mined *miningEvidence
			var outcome model.Row
			if config.MiningEnabled {
				if r.Mining == nil || r.Mining.State != "ready" || r.Mining.Delivered == "" || entry.MiningVersion != r.Mining.Version || !now.Before(r.Mining.FreshUntil) {
					return errors.New("dataset requires an explicit current verified mining version")
				}
				mining, err := readMining(root, r.Mining)
				if err != nil {
					return err
				}
				mined = &mining
				for _, episode := range mining.Tables["TraceEpisodes"] {
					keys = append(keys, "episode:"+scalar(episode["EpisodeId"]))
				}
				if entry.Outcome != nil {
					outcome, err = datasetOutcome(entry.Outcome, mining, r, now)
					if err != nil {
						return err
					}
					keys = append(keys, "output:"+mining.CanonicalNamespace+":"+scalar(outcome["OutputUrl"]))
				}
			} else if entry.MiningVersion != "" || entry.Outcome != nil {
				return errors.New("dataset outcome/mining lineage requires explicitly enabled mining")
			}
			for _, key := range keys {
				if group := bindings[key]; group != "" && group != entry.TaskGroup {
					return errors.New("related trace split/group leakage")
				}
				bindings[key] = entry.TaskGroup
			}
			var messages []map[string]any
			calls := map[string]bool{}
			seenCalls := map[string]bool{}
			users, assistants := 0, 0
			for sequence, event := range events {
				kind := scalar(event["type"])
				data, _ := event["data"].(map[string]any)
				role := ""
				switch kind {
				case "prompt.system":
					if _, ok := data["content"]; !ok {
						return errors.New("training message lacks approved content")
					}
					role = "system"
				case "prompt.user", "user.message":
					if _, ok := data["content"]; !ok {
						return errors.New("training message lacks approved content")
					}
					users++
					role = "user"
				case "assistant.message":
					if channel := scalar(data["channel"]); channel == "" || channel == "final" {
						if _, ok := data["content"]; !ok {
							return errors.New("training message lacks approved content")
						}
						assistants++
						role = "assistant"
					}
				case "tool.execution_start":
					if !tools[scalar(data["toolName"])] {
						return errors.New("training tool call lacks a bound tool schema")
					}
					call := scalar(data["toolCallId"])
					if call == "" || seenCalls[call] {
						return errors.New("training tool call lacks a unique native correlation")
					}
					if _, ok := data["input"]; !ok {
						return errors.New("training tool call lacks approved input")
					}
					calls[call] = true
					seenCalls[call] = true
					role = "assistant-tool-call"
				case "tool.execution_complete":
					call := scalar(data["toolCallId"])
					if call == "" || !calls[call] {
						return errors.New("training tool result lacks a matching call")
					}
					delete(calls, call)
					_, output := data["output"]
					_, result := data["result"]
					_, failure := data["error"]
					if !output && !result && !failure {
						return errors.New("training tool result lacks approved content")
					}
					role = "tool-result"
				}
				if role != "" {
					projected, err := trainingEvent(event, role)
					if errors.Is(err, errReasoningOnly) {
						continue
					}
					if err != nil {
						return err
					}
					messages = append(messages, map[string]any{"role": role, "sourceSequence": sequence, "event": projected})
				}
			}
			if len(messages) == 0 || users == 0 || assistants == 0 || len(calls) != 0 || messages[len(messages)-1]["role"] != "assistant" {
				return errors.New("trace has no complete approved training trajectory")
			}
			schemaJSON, err := json.Marshal(spec.ToolSchemas)
			if err != nil {
				return err
			}
			row := map[string]any{"datasetId": id, "name": spec.ID, "traceId": r.ID, "traceVersion": r.Version,
				"artifactDigest": r.ArtifactDigest, "policyDigest": r.PolicyDigest, "transform": spec.Transform,
				"roleMapping": "unified-text-events-v1", "evaluator": spec.Evaluator, "qualityLabel": entry.QualityLabel,
				"labelEvidence": entry.LabelEvidence, "taskGroup": entry.TaskGroup, "split": entry.Split,
				"tools": spec.ToolSchemas, "toolSchemaDigest": digest(schemaJSON), "toolSchemaEvidence": spec.ToolSchemaEvidence,
				"messages": messages, "gaps": e.Gaps}
			if mined != nil {
				row["mining"] = map[string]any{"version": entry.MiningVersion, "canonicalNamespace": mined.CanonicalNamespace,
					"canonicalRunId": mined.CanonicalRunID, "canonicalRevision": mined.CanonicalRevision,
					"capturedAt": mined.CapturedAt, "episodes": mined.Tables["TraceEpisodes"], "gaps": mined.Gaps}
			}
			if outcome != nil {
				row["outcome"] = outcome
				row["outcomeCutoff"] = entry.Outcome.Cutoff
			}
			line, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if int64(len(content)+len(line)+1) > min(config.MaxSpoolBytes/4, 64<<20) {
				return ErrCapacity
			}
			content = append(content, line...)
			content = append(content, '\n')
			d.References[r.ID] = r.Version
			metadata := []model.Row{{"DatasetId": id, "Transform": spec.Transform, "Split": entry.Split,
				"TaskGroup": entry.TaskGroup, "Evaluator": spec.Evaluator, "QualityLabel": entry.QualityLabel,
				"State": "ready", "MemberCount": len(spec.Entries), "DatasetDigest": "",
				"MiningVersion": entry.MiningVersion, "OutcomeId": "", "Disposition": "", "LabelRuleVersion": "", "LabelCutoff": ""}}
			if outcome != nil {
				metadata[0]["OutcomeId"], metadata[0]["Disposition"] = scalar(outcome["OutputId"]), scalar(outcome["Status"])
				metadata[0]["LabelRuleVersion"], metadata[0]["LabelCutoff"] = entry.Outcome.RuleVersion, entry.Outcome.Cutoff
			}
			state.Revision++
			if err := seal(namespace, r.ID, r.Version, r.Attempt, state.Revision, "TraceDatasets", metadata); err != nil {
				return err
			}
			d.Rows = append(d.Rows, metadata...)
		}
		d.Digest, d.File = digest(content), id+".dataset"
		for _, row := range d.Rows {
			row["DatasetDigest"] = d.Digest
			if err := seal(namespace, scalar(row["RunId"]), scalar(row["ProjectionId"]),
				int64(integer(row["RunAttempt"])), int64(integer(row["SourceRevision"])), "TraceDatasets", []model.Row{row}); err != nil {
				return err
			}
		}
		if err := s.reserve(root, int64(len(content))); err != nil {
			return err
		}
		if err := atomicWrite(root, d.File, content); err != nil {
			return err
		}
		state.Datasets[id] = d
		if err := s.save(root, state); err != nil {
			return err
		}
		path = config.Directory + string(os.PathSeparator) + d.File
		return nil
	})
	return path, err
}

func datasetOutcome(binding *DatasetOutcome, e miningEvidence, r *record, now time.Time) (model.Row, error) {
	cutoff, err := time.Parse(time.RFC3339Nano, binding.Cutoff)
	if err != nil || cutoff.After(now) || cutoff.Before(e.CapturedAt) || binding.RuleVersion != outcomeLabelVersion ||
		!slices.Contains([]string{"accepted", "rejected"}, binding.Disposition) {
		return nil, errors.New("outcome label requires an observed as-of cutoff and a supported reviewed disposition rule")
	}
	for _, row := range e.Tables["TraceOutcomes"] {
		if scalar(row["OutputId"]) != binding.OutputID {
			continue
		}
		observed, observedErr := time.Parse(time.RFC3339Nano, scalar(row["ObservedAt"]))
		updated, updatedErr := time.Parse(time.RFC3339Nano, scalar(row["ObjectUpdatedAt"]))
		if binding.AuditID == "" || scalar(row["AuditId"]) != binding.AuditID || scalar(row["Status"]) != binding.Disposition ||
			integer(row["ProducingAttempt"]) != int(r.Attempt) || observedErr != nil || updatedErr != nil ||
			observed.After(cutoff) || updated.After(cutoff) {
			return nil, errors.New("outcome label lacks exact attempt, independent disposition or cutoff evidence")
		}
		return row, nil
	}
	return nil, errors.New("outcome label has no retained output")
}

func reconstruct(e Evidence) ([]map[string]any, error) {
	parts := map[string][]model.Row{}
	for _, row := range e.Tables["TraceContentParts"] {
		sequence := scalarNumber(row["Sequence"])
		parts[sequence] = append(parts[sequence], row)
	}
	var events []map[string]any
	for _, metadata := range e.Tables["TraceEvents"] {
		sequence := scalarNumber(metadata["Sequence"])
		rows := parts[sequence]
		if len(rows) != integer(metadata["PartCount"]) {
			return nil, errors.New("trace content parts are incomplete")
		}
		slices.SortFunc(rows, func(a, b model.Row) int { return integer(a["Part"]) - integer(b["Part"]) })
		var content []byte
		for i, row := range rows {
			if integer(row["Part"]) != i {
				return nil, errors.New("trace content parts are not contiguous")
			}
			content = append(content, scalar(row["Content"])...)
		}
		if digest(content) != scalar(metadata["ContentDigest"]) {
			return nil, errors.New("trace event integrity mismatch")
		}
		var event map[string]any
		if err := decode(content, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

var errReasoningOnly = errors.New("reasoning-only training event excluded")

func trainingEvent(event map[string]any, role string) (map[string]any, error) {
	source, _ := event["data"].(map[string]any)
	data := map[string]any{}
	fields := []string{"content", "channel", "sessionId"}
	switch role {
	case "assistant-tool-call":
		fields = []string{"toolName", "toolCallId", "input", "sessionId"}
	case "tool-result":
		fields = []string{"toolName", "toolCallId", "output", "result", "error", "success", "sessionId"}
	}
	for _, key := range fields {
		if value, exists := source[key]; exists {
			data[key] = value
		}
	}
	if role != "assistant-tool-call" && role != "tool-result" {
		switch content := data["content"].(type) {
		case string:
			if content == "" {
				return nil, errors.New("training text content is empty")
			}
		case []any:
			filtered := make([]any, 0, len(content))
			for _, block := range content {
				part, ok := block.(map[string]any)
				if !ok {
					return nil, errors.New("unsupported training content block")
				}
				kind := scalar(part["type"])
				if slices.Contains([]string{"reasoning", "thinking", "analysis", "redacted_thinking"}, kind) {
					continue
				}
				text, ok := part["text"].(string)
				if !slices.Contains([]string{"text", "input_text", "output_text"}, kind) || !ok || text == "" {
					return nil, errors.New("training export requires supported text blocks")
				}
				filtered = append(filtered, map[string]any{"type": kind, "text": text})
			}
			if len(filtered) == 0 {
				if role == "assistant" {
					return nil, errReasoningOnly
				}
				return nil, errors.New("training message has no supported text")
			}
			data["content"] = filtered
		default:
			return nil, errors.New("training export requires text content")
		}
	}
	projected := map[string]any{"data": data}
	for _, key := range []string{"id", "parentId", "type", "timestamp", "provenance"} {
		if value, exists := event[key]; exists {
			projected[key] = value
		}
	}
	return projected, nil
}

func integer(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		n, _ := strconv.Atoi(string(v))
		return n
	}
	return -1
}

func scalarNumber(value any) string { return strconv.Itoa(integer(value)) }

func invalidateDatasets(state *ledger, traceID string) []string {
	var files []string
	for _, d := range state.Datasets {
		if _, ok := d.References[traceID]; !ok || d.State != "ready" {
			continue
		}
		d.State, d.Delivered = "invalidated", ""
		if d.File != "" {
			files = append(files, d.File)
			d.File = ""
		}
		for _, row := range d.Rows {
			row["State"] = "invalidated"
			state.Revision++
			row["SourceRevision"] = state.Revision
		}
	}
	return files
}
