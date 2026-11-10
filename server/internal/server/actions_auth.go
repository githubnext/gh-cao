package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var actionsAuthLog = logger.New("cao:server:actions-auth")

const actionsGitHubAPIVersion = "2026-03-10"

type githubPermissionProbe struct {
	name string
	path string
}

// parseActionsRepository splits a GITHUB_REPOSITORY-style "owner/name" value
// into its owner and repository name, rejecting anything that isn't exactly
// two non-blank segments. It is a pure function so this boundary's
// validation behavior is testable without an HTTP round trip.
func parseActionsRepository(repository string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(repository), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", errors.New("GitHub Actions MCP authentication requires GITHUB_REPOSITORY in owner/repository form")
	}
	return owner, name, nil
}

// resolveGitHubAPIBaseURL applies the standard default for the GitHub API
// base URL: an explicit rawURL, trimmed and with any trailing slash removed,
// falling back to the public GitHub API when rawURL is blank. It is a pure
// function so the permission check's URL resolution is testable without
// making a request.
func resolveGitHubAPIBaseURL(rawURL string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("GitHub Actions MCP API URL is invalid")
	}
	return baseURL, nil
}

func verifyGitHubActionsPermissions(ctx context.Context, config Config, token string) error {
	owner, name, err := parseActionsRepository(config.ActionsRepository)
	if err != nil {
		return err
	}
	baseURL, err := resolveGitHubAPIBaseURL(config.GitHubAPIURL)
	if err != nil {
		return err
	}
	client := config.ActionsHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	repositoryPath := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	probes := []githubPermissionProbe{
		{name: "actions", path: repositoryPath + "/actions/runs?per_page=1"},
		{name: "contents", path: repositoryPath + "/contents?per_page=1"},
		{name: "issues", path: repositoryPath + "/issues?per_page=1"},
		{name: "pull-requests", path: repositoryPath + "/pulls?per_page=1"},
	}
	for _, probe := range probes {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+probe.path, nil)
		if err != nil {
			return errors.New("GitHub Actions MCP permission check could not be created")
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-GitHub-Api-Version", actionsGitHubAPIVersion)
		request.Header.Set("User-Agent", "gh-cao-mcp")
		response, err := client.Do(request)
		if err != nil {
			actionsAuthLog.PrintfContext(ctx, "permission probe request failed probe=%s", probe.name)
			return fmt.Errorf("GitHub Actions MCP %s permission check failed", probe.name)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			actionsAuthLog.PrintfContext(ctx, "permission probe denied probe=%s status=%d", probe.name, response.StatusCode)
			return fmt.Errorf("GitHub Actions token requires %s: read permission for MCP access", probe.name)
		}
	}
	return nil
}
