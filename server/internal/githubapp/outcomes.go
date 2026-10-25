package githubapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/mining"
)

// ReadOutcome makes exactly one read of an already attributed object. No
// comments, timelines, reviews, search, or cross-repository discovery is needed.
func (c *Client) ReadOutcome(ctx context.Context, installationID int64, target mining.Target) (mining.Outcome, githubquota.ResponseQuota, error) {
	expectedHost := "github.com"
	if c.config.BaseURL != "" {
		base, err := url.Parse(c.config.BaseURL)
		if err != nil {
			return mining.Outcome{}, githubquota.ResponseQuota{}, err
		}
		expectedHost = strings.TrimPrefix(base.Hostname(), "api.")
	}
	objectURL, err := url.Parse(target.URL)
	if err != nil || !strings.EqualFold(objectURL.Hostname(), expectedHost) {
		return mining.Outcome{}, githubquota.ResponseQuota{}, errors.New("outcome target is outside the configured GitHub host")
	}
	client, owner, repository, err := c.repositoryClient(installationID, target.Repository)
	if err != nil {
		return mining.Outcome{}, githubquota.ResponseQuota{}, err
	}
	category := "issues"
	if target.PullRequest {
		category = "pulls"
	}
	request, err := client.NewRequest(http.MethodGet, fmt.Sprintf("repos/%s/%s/%s/%d",
		url.PathEscape(owner), url.PathEscape(repository), category, target.Number), nil)
	if err != nil {
		return mining.Outcome{}, githubquota.ResponseQuota{}, err
	}
	var object mining.Object
	response, err := client.Do(ctx, request, &object)
	quota := workflowRunResponseQuota(response)
	if err != nil {
		return mining.Outcome{}, quota, fmt.Errorf("read outcome object: %w", err)
	}
	if object.URL != target.URL || object.Number != target.Number {
		return mining.Outcome{}, quota, errors.New("outcome API response identity mismatch")
	}
	outcome := mining.Outcome{
		Repository: target.Repository, Number: target.Number, PullRequest: target.PullRequest,
		URL: object.URL, State: object.State, StateReason: object.StateReason,
		Merged: object.Merged || object.MergedAt != nil, UpdatedAt: object.UpdatedAt,
		ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Source: "github-api",
	}
	if object.ClosedBy != nil {
		outcome.CloseActor = object.ClosedBy.Type
	}
	return outcome, quota, outcome.Validate()
}
