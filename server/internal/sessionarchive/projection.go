package sessionarchive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func Project(config adxtrace.Config, namespace, repository, runID, day string, attempt int64, artifact []byte, now time.Time) (Projection, error) {
	if err := config.Validate(); err != nil {
		return Projection{}, err
	}
	if !config.Enabled || !slices.Contains(config.Repositories, repository) || !validIdentity(namespace, repository, runID, attempt) {
		return Projection{}, ErrIntegrity
	}
	e, err := adxtrace.Project(config, repository, runID, day, attempt, artifact, now)
	if err != nil {
		return Projection{}, err
	}
	return FromEvidence(namespace, e)
}

// FromEvidence reconstructs the approved logical event without another parser
// or approval policy; transport chunks are verified before joining.
func FromEvidence(namespace string, e adxtrace.Evidence) (Projection, error) {
	if !validIdentity(namespace, e.Repository, e.RunID, e.Attempt) || e.Version == "" {
		return Projection{}, ErrIntegrity
	}
	p := Projection{Namespace: namespace, Repository: e.Repository, RunID: e.RunID, Attempt: e.Attempt,
		ID: e.ID, Version: e.Version, Day: e.Day, ArtifactDigest: e.ArtifactDigest, PolicyDigest: e.PolicyDigest,
		CapturedAt: e.CapturedAt, Gaps: slices.Clone(e.Gaps)}
	parts := e.Tables["TraceContentParts"]
	partIndex := 0
	for i, row := range e.Tables["TraceEvents"] {
		integer := func(name string) int64 {
			n, _ := strconv.ParseInt(fmt.Sprint(row[name]), 10, 64)
			return n
		}
		if integer("Sequence") != int64(i) || integer("PartCount") < 1 {
			return Projection{}, ErrIntegrity
		}
		var content strings.Builder
		for j := int64(0); j < integer("PartCount"); j++ {
			if partIndex >= len(parts) || fmt.Sprint(parts[partIndex]["Sequence"]) != strconv.Itoa(i) ||
				fmt.Sprint(parts[partIndex]["Part"]) != strconv.FormatInt(j, 10) {
				return Projection{}, ErrIntegrity
			}
			text, ok := parts[partIndex]["Content"].(string)
			if !ok {
				return Projection{}, ErrIntegrity
			}
			content.WriteString(text)
			partIndex++
		}
		if hash([]byte(content.String())) != row["ContentDigest"] || !json.Valid([]byte(content.String())) {
			return Projection{}, ErrIntegrity
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return Projection{}, err
		}
		var event Event
		if err := json.Unmarshal(encoded, &event); err != nil {
			return Projection{}, err
		}
		event.Namespace, event.Repository, event.RunID, event.RunAttempt = namespace, e.Repository, e.RunID, e.Attempt
		event.TraceID, event.ProjectionID, event.ContractVersion = e.ID, e.Version, ContractVersion
		event.CompletionDay, event.ArtifactDigest, event.PolicyDigest = e.Day, e.ArtifactDigest, e.PolicyDigest
		event.CapturedAt, event.Gaps, event.PayloadJSON = e.CapturedAt.UTC().Format(time.RFC3339Nano), slices.Clone(e.Gaps), content.String()
		if event.Gaps == nil {
			event.Gaps = []string{}
		}
		p.Events = append(p.Events, event)
	}
	if len(p.Events) == 0 || partIndex != len(parts) {
		return Projection{}, ErrIntegrity
	}
	return p, nil
}

func (p Projection) Member() Member {
	return Member{p.Namespace, p.Repository, p.RunID, p.Attempt, p.ID, p.Version, int64(len(p.Events))}
}
