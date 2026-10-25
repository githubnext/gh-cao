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
	Merged      bool       `json:"merged"`
	MergedAt    *time.Time `json:"merged_at"`
	ClosedBy    *struct {
		Type string `json:"type"`
	} `json:"closed_by"`
}

type Outcome struct {
	Repository  string    `json:"repository"`
	Number      int64     `json:"number"`
	PullRequest bool      `json:"pullRequest"`
	URL         string    `json:"url"`
	State       string    `json:"state"`
	StateReason string    `json:"stateReason,omitempty"`
	Merged      bool      `json:"merged"`
	CloseActor  string    `json:"closeActor,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ObservedAt  time.Time `json:"observedAt"`
	Source      string    `json:"source"`
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
		(o.Merged && (!o.PullRequest || o.State != "closed")) {
		return errors.New("inconsistent outcome observation")
	}
	return nil
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
	switch {
	case o.PullRequest && o.Merged:
		return "accepted", "merged"
	case o.PullRequest && o.State == "closed":
		return "rejected", "closed_without_merge"
	case o.State == "closed" && o.StateReason == "completed":
		return "accepted", "completed"
	case o.State == "closed" && o.StateReason == "not_planned" && o.CloseActor == "Bot":
		return "lifecycle", "lifecycle"
	case o.State == "closed" && o.StateReason == "not_planned" && o.CloseActor == "User":
		return "rejected", "closed_not_planned"
	case o.State == "open":
		return "pending", "open"
	default:
		return "unknown", "missing_resolution_evidence"
	}
}

func (o Outcome) Audit(issueID, runID string) model.Row {
	status, signal := o.Evaluation()
	return model.Row{
		"id":    "audit:server-mining:outcome:" + model.EncodeCoordinate(issueID),
		"runId": runID, "source": Source, "type": "outcome.observed",
		"correlationId": issueID, "targetRepository": o.Repository,
		"status": status, "code": signal, "decision": o.State,
		"scope": "selected_outputs", "evidenceState": "partial",
		"observedAt": o.ObservedAt, "timestamp": o.UpdatedAt,
		"summary": o.Source,
	}
}
