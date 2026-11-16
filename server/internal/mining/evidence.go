// Package mining preserves recorded gh-aw lineage and independently observed
// GitHub dispositions. Neither workflow success nor object existence is acceptance.
package mining

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const Source = "server-mining"
const OutcomeRuleVersion = AdoptionRegistryVersion

type Context struct {
	EpisodeID      string `json:"episode_id"`
	HopID          string `json:"hop_id"`
	ParentHopID    string `json:"parent_hop_id"`
	Repo           string `json:"repo"`
	RunID          string `json:"run_id"`
	WorkflowID     string `json:"workflow_id"`
	RootRepo       string `json:"root_repo"`
	RootRunID      string `json:"root_run_id"`
	RootWorkflowID string `json:"root_workflow_id"`
}

// Merge enriches compact recorded contexts without resolving contradictory facts.
func (c Context) Merge(next Context) (Context, error) {
	for _, field := range []struct {
		current *string
		next    string
	}{
		{&c.EpisodeID, next.EpisodeID}, {&c.HopID, next.HopID},
		{&c.ParentHopID, next.ParentHopID}, {&c.Repo, next.Repo},
		{&c.RunID, next.RunID}, {&c.WorkflowID, next.WorkflowID},
		{&c.RootRepo, next.RootRepo}, {&c.RootRunID, next.RootRunID},
		{&c.RootWorkflowID, next.RootWorkflowID},
	} {
		if *field.current != "" && field.next != "" && *field.current != field.next {
			return Context{}, errors.New("conflicting recorded episode context")
		}
		if *field.current == "" {
			*field.current = field.next
		}
	}
	return c, nil
}

type Membership struct {
	Repository string    `json:"repository"`
	RunID      string    `json:"runId"`
	Attempt    int       `json:"attempt"`
	Context    Context   `json:"context"`
	ObservedAt time.Time `json:"observedAt"`
}

type Object struct {
	URL         string     `json:"html_url"`
	Number      int64      `json:"number"`
	State       string     `json:"state"`
	StateReason string     `json:"state_reason"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Merged      bool       `json:"merged"`
	MergedAt    *time.Time `json:"merged_at"`
	ClosedBy    *struct {
		Type string `json:"type"`
	} `json:"closed_by"`
	MergedBy *struct {
		Type string `json:"type"`
	} `json:"merged_by"`
}

type Outcome struct {
	Repository    string     `json:"repository"`
	Number        int64      `json:"number"`
	PullRequest   bool       `json:"pullRequest"`
	URL           string     `json:"url"`
	State         string     `json:"state"`
	StateReason   string     `json:"stateReason,omitempty"`
	Merged        bool       `json:"merged"`
	CloseActor    string     `json:"closeActor,omitempty"`
	DispositionAt *time.Time `json:"dispositionAt,omitempty"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	ObservedAt    time.Time  `json:"observedAt"`
	Source        string     `json:"source"`
}

// Target accepts only exact issue/PR URLs; it never follows a payload-supplied
// endpoint. The configured GitHub client owns the API host.
type Target struct {
	URL         string
	Repository  string
	Number      int64
	PullRequest bool
}

func ParseTarget(raw string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Port() != "" {
		return Target{}, errors.New("invalid outcome target URL")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 4 || (parts[2] != "issues" && parts[2] != "pull") {
		return Target{}, errors.New("outcome target must identify an issue or pull request")
	}
	for _, part := range parts[:2] {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, " \t\r\n\\%") {
			return Target{}, errors.New("invalid outcome repository")
		}
	}
	number, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || number <= 0 || strconv.FormatInt(number, 10) != parts[3] {
		return Target{}, errors.New("invalid outcome object number")
	}
	return Target{URL: raw, Repository: strings.ToLower(parts[0] + "/" + parts[1]),
		Number: number, PullRequest: parts[2] == "pull"}, nil
}

func (o Outcome) Validate() error {
	target, err := ParseTarget(o.URL)
	if err != nil {
		return err
	}
	if target.Repository != o.Repository || target.Number != o.Number || target.PullRequest != o.PullRequest ||
		(o.State != "open" && o.State != "closed") || o.UpdatedAt.IsZero() || o.ObservedAt.IsZero() ||
		(o.Source != "github-webhook" && o.Source != "github-api") ||
		(o.Merged && (!o.PullRequest || o.State != "closed")) ||
		(o.DispositionAt != nil && (o.DispositionAt.IsZero() || o.State != "closed" || o.DispositionAt.After(o.UpdatedAt))) {
		return errors.New("inconsistent outcome observation")
	}
	return nil
}

// Merge keeps the original observation clock on replay and refuses to choose
// between contradictory dispositions at the same upstream update clock.
func (o Outcome) Merge(next Outcome) (Outcome, error) {
	if err := o.Validate(); err != nil {
		return Outcome{}, err
	}
	if err := next.Validate(); err != nil {
		return Outcome{}, err
	}
	if o.Repository != next.Repository || o.URL != next.URL || o.PullRequest != next.PullRequest {
		return Outcome{}, errors.New("outcome observation ownership mismatch")
	}
	if next.UpdatedAt.Before(o.UpdatedAt) {
		return o, nil
	}
	if next.UpdatedAt.After(o.UpdatedAt) {
		if o.State == next.State && o.StateReason == next.StateReason && o.Merged == next.Merged {
			if o.CloseActor != "" && next.CloseActor != "" && o.CloseActor != next.CloseActor {
				return Outcome{}, errors.New("conflicting disposition actor")
			}
			if next.CloseActor == "" {
				next.CloseActor = o.CloseActor
			}
			if next.DispositionAt == nil {
				next.DispositionAt = copiedTime(o.DispositionAt)
			}
		}
		return next, nil
	}
	if o.State != next.State || o.StateReason != next.StateReason || o.Merged != next.Merged ||
		(o.CloseActor != "" && next.CloseActor != "" && o.CloseActor != next.CloseActor) ||
		(o.DispositionAt != nil && next.DispositionAt != nil && !o.DispositionAt.Equal(*next.DispositionAt)) {
		return Outcome{}, errors.New("conflicting same-time outcome observations")
	}
	merged := o
	if merged.CloseActor == "" {
		merged.CloseActor = next.CloseActor
	}
	if merged.DispositionAt == nil {
		merged.DispositionAt = copiedTime(next.DispositionAt)
	}
	if next.Source == "github-api" && next.ObservedAt.After(merged.ObservedAt) {
		merged.ObservedAt = next.ObservedAt
		merged.Source = next.Source
	}
	return merged, nil
}

func (m Membership) Validate() error {
	if m.Repository == "" || m.Attempt < 1 || m.ObservedAt.IsZero() ||
		strings.TrimSpace(m.Context.EpisodeID) == "" || len(m.Context.EpisodeID) > 2048 {
		return errors.New("invalid recorded episode membership")
	}
	id, err := strconv.ParseInt(m.RunID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != m.RunID {
		return errors.New("invalid episode run identity")
	}
	return nil
}

func (m Membership) Audits() []model.Row {
	runID := "github:run:" + m.Repository + ":" + m.RunID
	base := "audit:server-mining:" + model.EncodeCoordinate(fmt.Sprintf("%s:%d:%s", runID, m.Attempt, m.Context.HopID))
	row := model.Row{
		"id": base + ":membership", "runId": runID, "source": Source,
		"type": "episode.membership", "correlationId": m.Context.EpisodeID,
		"scope": "selected_runs", "evidenceState": "partial",
		"observedAt": m.ObservedAt, "claimRunAttempt": m.Attempt,
	}
	for key, value := range map[string]string{
		"name": m.Context.HopID, "code": m.Context.ParentHopID,
		"claimRunId": m.Context.RunID, "targetRepository": m.Context.Repo,
		"targetWorkflowPath": m.Context.WorkflowID,
	} {
		if value != "" {
			row[key] = value
		}
	}
	rows := []model.Row{row}
	if m.Context.RootRunID != "" || m.Context.RootRepo != "" || m.Context.RootWorkflowID != "" {
		rows = append(rows, model.Row{
			"id": base + ":root", "runId": runID, "source": Source, "type": "episode.root",
			"correlationId": m.Context.EpisodeID, "claimRunId": m.Context.RootRunID,
			"targetRepository": m.Context.RootRepo, "targetWorkflowPath": m.Context.RootWorkflowID,
			"observedAt": m.ObservedAt, "scope": "selected_runs", "evidenceState": "partial",
		})
	}
	return rows
}

func (o Outcome) Evaluation() (status, signal string) {
	outputType := "create_issue"
	if o.PullRequest {
		outputType = "create_pull_request"
	}
	evaluation := o.EvaluateSafeOutput(outputType)
	return evaluation.Status, evaluation.Signal
}

func (o Outcome) Audit(issueID, runID string) model.Row {
	outputType := "create_issue"
	if o.PullRequest {
		outputType = "create_pull_request"
	}
	return o.AuditForSafeOutput(issueID, runID, outputType)
}

func (o Outcome) AuditForSafeOutput(issueID, runID, outputType string) model.Row {
	evaluation := o.EvaluateSafeOutput(outputType)
	eventAt := o.UpdatedAt
	if evaluation.AcceptedAt != nil {
		eventAt = *evaluation.AcceptedAt
	}
	return model.Row{
		"id":    "audit:server-mining:outcome:" + model.EncodeCoordinate(issueID),
		"runId": runID, "source": Source, "type": "outcome.observed",
		"correlationId": issueID, "targetRepository": o.Repository,
		"payloadRef": o.URL, "name": OutcomeRuleVersion,
		"status": evaluation.Status, "code": evaluation.Signal, "decision": o.State,
		"scope": "selected_outputs", "evidenceState": "partial",
		"observedAt": o.ObservedAt, "timestamp": eventAt,
		"summary": o.Source,
	}
}
