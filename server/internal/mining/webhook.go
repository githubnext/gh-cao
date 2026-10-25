package mining

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func ParseWebhook(event string, payload []byte, observedAt time.Time) (*Outcome, error) {
	if event != "issues" && event != "pull_request" {
		return nil, errors.New("unsupported outcome webhook event")
	}
	var envelope struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			Type string `json:"type"`
		} `json:"sender"`
		Issue       Object `json:"issue"`
		PullRequest Object `json:"pull_request"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	object := envelope.Issue
	if event == "pull_request" {
		object = envelope.PullRequest
	}
	outcome := Outcome{
		Repository: strings.ToLower(envelope.Repository.FullName),
		Number:     object.Number, PullRequest: event == "pull_request",
		URL: object.URL, State: object.State, StateReason: object.StateReason,
		Merged:    object.Merged || object.MergedAt != nil,
		UpdatedAt: object.UpdatedAt, ObservedAt: observedAt.UTC().Truncate(time.Microsecond),
		Source: "github-webhook",
	}
	if object.ClosedBy != nil {
		outcome.CloseActor = object.ClosedBy.Type
	} else if envelope.Action == "closed" {
		outcome.CloseActor = envelope.Sender.Type
	}
	if envelope.Action == "closed" && outcome.State != "closed" ||
		(envelope.Action == "opened" || envelope.Action == "reopened") && outcome.State != "open" {
		return nil, errors.New("webhook action disagrees with object disposition")
	}
	if err := outcome.Validate(); err != nil {
		return nil, err
	}
	return &outcome, nil
}
