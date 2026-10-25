package adxtrace

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func decode(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON content")
	}
	return nil
}

type Evidence struct {
	ID, Version, Repository, RunID, Day, ArtifactDigest, PolicyDigest string
	Attempt                                                           int64
	CapturedAt                                                        time.Time
	Gaps                                                              []string
	Tables                                                            map[string][]model.Row
}

var secretKeys = regexp.MustCompile(`(?i)^(authorization|cookie|set-cookie|password|passwd|secret|token|access_token|api_key|private_key|client_secret)$`)
var builtInSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
	regexp.MustCompile(`(?i)Bearer[ \t]+[A-Za-z0-9._~+/-]+=*`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

func approvedValue(value any, patterns []*regexp.Regexp, changed *bool) any {
	switch value := value.(type) {
	case string:
		for _, pattern := range patterns {
			next := pattern.ReplaceAllString(value, "[REDACTED]")
			*changed = *changed || next != value
			value = next
		}
		return value
	case map[string]any:
		for key, child := range value {
			if secretKeys.MatchString(key) {
				value[key], *changed = "[REDACTED]", true
			} else {
				value[key] = approvedValue(child, patterns, changed)
			}
		}
	case []any:
		for i, child := range value {
			value[i] = approvedValue(child, patterns, changed)
		}
	}
	return value
}

func scalar(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	}
	return ""
}

// Project admits only explicitly approved event subtrees. Unknown extensions are
// reported, never copied through a permissive JSON fallback.
func Project(c Config, repository, runID, day string, attempt int64, artifact []byte, now time.Time) (Evidence, error) {
	if !utf8.Valid(artifact) {
		return Evidence{}, errors.New("trace source is not valid UTF-8")
	}
	if int64(len(artifact)) > c.MaxTraceBytes || attempt < 1 || runID == "" {
		return Evidence{}, errors.New("trace source exceeds eligibility budget or has invalid identity")
	}
	e := Evidence{Repository: repository, RunID: runID, Attempt: attempt, Day: day, CapturedAt: now.UTC(),
		ArtifactDigest: digest(artifact), PolicyDigest: policyDigest(c.Policy), Tables: map[string][]model.Row{}, Gaps: []string{}}
	e.ID = digest([]byte(repository + "\x00" + runID + fmt.Sprintf("\x00%d", attempt)))
	patterns := slices.Clone(builtInSecrets)
	for _, pattern := range c.Policy.RedactPatterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return Evidence{}, errors.New("invalid trace redaction policy")
		}
		patterns = append(patterns, compiled)
	}
	scanner := bufio.NewScanner(bytes.NewReader(artifact))
	scanner.Buffer(make([]byte, 4096), int(c.MaxTraceBytes)+1)
	var episode, hop, parentHop string
	sequence, parts := 0, 0
	approvedBytes := 0
	hasFormat, hasCoverage := false, false
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		if sequence >= 32768 {
			return Evidence{}, errors.New("trace exceeds event budget")
		}
		var event map[string]any
		if err := decode(scanner.Bytes(), &event); err != nil {
			return Evidence{}, errors.New("invalid unified trace JSON event")
		}
		kind := scalar(event["type"])
		data, ok := event["data"].(map[string]any)
		if !eventName.MatchString(kind) || !ok {
			return Evidence{}, errors.New("invalid unified trace event envelope")
		}
		if kind == "session.format" && scalar(data["version"]) != "1" {
			return Evidence{}, errors.New("unsupported unified trace format")
		}
		hasFormat = hasFormat || kind == "session.format"
		hasCoverage = hasCoverage || kind == "session.collection"
		fields, admitted := c.Policy.Events[kind]
		if !admitted {
			e.Gaps = append(e.Gaps, "unapproved-event")
		}
		approved := map[string]any{"type": kind, "data": map[string]any{}}
		for key, value := range event {
			switch key {
			case "id", "parentId", "timestamp":
				if value != nil && scalar(value) == "" && value != "" {
					return Evidence{}, errors.New("invalid native event identity or timestamp")
				}
				approved[key] = value
			case "provenance":
				provenance, ok := value.(map[string]any)
				if !ok {
					return Evidence{}, errors.New("invalid trace provenance")
				}
				filtered := map[string]any{}
				for name, field := range provenance {
					if slices.Contains([]string{"component", "phase", "path", "index", "timestampMs"}, name) {
						if scalar(field) == "" && field != "" {
							return Evidence{}, errors.New("invalid trace provenance field")
						}
						filtered[name] = field
					} else {
						e.Gaps = append(e.Gaps, "unapproved-provenance-field")
					}
				}
				approved[key] = filtered
			case "type", "data":
			default:
				e.Gaps = append(e.Gaps, "unapproved-envelope-field")
			}
		}
		filtered := approved["data"].(map[string]any)
		for key, value := range data {
			if admitted && slices.Contains(fields, key) {
				filtered[key] = value
			} else {
				e.Gaps = append(e.Gaps, "unapproved-data-field")
			}
		}
		changed := false
		approvedValue(approved, patterns, &changed)
		if changed {
			e.Gaps = append(e.Gaps, "redacted")
		}
		indexed := func(value any) string {
			result := scalar(value)
			if len(result) > 4096 {
				e.Gaps = append(e.Gaps, "index-field-oversized")
				return ""
			}
			return result
		}
		if kind == "workflow.info" {
			// These are supplied producer episode bindings, not timestamp edges.
			if lineage, ok := filtered["episode"].(map[string]any); ok {
				episode, hop, parentHop = indexed(lineage["episodeId"]), indexed(lineage["hopId"]), indexed(lineage["parentHopId"])
			}
		}
		content, err := json.Marshal(approved)
		if err != nil {
			return Evidence{}, err
		}
		approvedBytes += len(content)
		if int64(approvedBytes) > c.MaxTraceBytes {
			return Evidence{}, errors.New("approved trace exceeds eligibility budget")
		}
		partCount := 0
		for offset := 0; offset < len(content); {
			end := min(len(content), offset+(48<<10))
			for end < len(content) && !utf8.RuneStart(content[end]) {
				end--
			}
			e.Tables["TraceContentParts"] = append(e.Tables["TraceContentParts"], model.Row{
				"Sequence": sequence, "Part": partCount, "Content": string(content[offset:end]),
			})
			partCount++
			offset = end
		}
		e.Tables["TraceEvents"] = append(e.Tables["TraceEvents"], model.Row{
			"Sequence": sequence, "Type": kind, "EventId": indexed(approved["id"]), "ParentId": indexed(approved["parentId"]),
			"EpisodeId": episode, "HopId": hop, "ParentHopId": parentHop,
			"ToolCallId": indexed(filtered["toolCallId"]), "SessionId": indexed(filtered["sessionId"]),
			"Timestamp": indexed(approved["timestamp"]), "PartCount": partCount, "ContentDigest": digest(content),
		})
		sequence++
		parts += partCount
	}
	if err := scanner.Err(); err != nil || sequence == 0 {
		return Evidence{}, errors.New("empty or oversized unified trace")
	}
	if !hasFormat {
		e.Gaps = append(e.Gaps, "producer-format-unattested")
	}
	if !hasCoverage {
		e.Gaps = append(e.Gaps, "source-coverage-unattested")
	}
	slices.Sort(e.Gaps)
	e.Gaps = slices.Compact(e.Gaps)
	// Version hashes include native source, policy and approved contents, but
	// exclude wall-clock acquisition time so repeat collection is idempotent.
	content, err := json.Marshal(e.Tables)
	if err != nil {
		return Evidence{}, err
	}
	e.Version = digest([]byte(e.ArtifactDigest + e.PolicyDigest + digest(content)))
	e.Tables["TraceManifest"] = []model.Row{{
		"Repository": repository, "ArtifactDigest": e.ArtifactDigest, "PolicyDigest": e.PolicyDigest,
		"EventCount": sequence, "PartCount": parts, "ContentDigest": digest(content), "State": "ready",
		"Gaps": e.Gaps, "CapturedAt": e.CapturedAt.Format(time.RFC3339Nano),
	}}
	return e, nil
}

func seal(namespace, id, version string, attempt, revision int64, table string, rows []model.Row) error {
	columns, ok := Tables[table]
	if !ok {
		return errors.New("unknown trace table")
	}
	for _, row := range rows {
		row["Namespace"], row["RunId"], row["RunAttempt"] = namespace, id, attempt
		row["ProjectionId"], row["ContractVersion"] = version, ContractVersion
		if slices.Contains(columns, "SourceRevision") {
			row["SourceRevision"] = revision
		}
		delete(row, "RowDigest")
		content, err := json.Marshal(row)
		if err != nil {
			return err
		}
		row["RowDigest"] = digest(content)
		if len(row) != len(columns) {
			return errors.New("trace row does not match generated contract")
		}
		for key := range row {
			if !slices.Contains(columns, key) {
				return errors.New("unapproved trace column")
			}
		}
	}
	return nil
}

func rowSetDigest(rows []model.Row) string {
	digests := make([]string, 0, len(rows))
	for _, row := range rows {
		digests = append(digests, scalar(row["RowDigest"]))
	}
	slices.Sort(digests)
	return digest([]byte(strings.Join(slices.Compact(digests), "")))
}
