package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func readGitHubJSON(ctx context.Context, client *api.RESTClient, path string) ([]byte, error) {
	response, err := client.RequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, errors.New("GitHub quota request is unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("GitHub quota request failed")
	}
	const maximumBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil || len(data) > maximumBytes {
		return nil, errors.New("GitHub quota response is invalid")
	}
	return data, nil
}

func ghSnapshot(ctx context.Context, host string, transport http.RoundTripper, required ...string) (snapshot, error) {
	resources, err := requiredResources(required)
	if err != nil {
		return snapshot{}, err
	}
	client, err := api.NewRESTClient(api.ClientOptions{
		Host: host, Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Log:           io.Discard, LogIgnoreEnv: true,
		Headers: map[string]string{"X-GitHub-Api-Version": "2026-03-10"},
	})
	if err != nil {
		return snapshot{}, errors.New("GitHub credential is unavailable")
	}
	data, err := readGitHubJSON(ctx, client, "user")
	if err != nil {
		return snapshot{}, err
	}
	var user struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(data, &user); err != nil {
		return snapshot{}, errors.New("GitHub credential identity is invalid")
	}
	userID, err := parseUserID(user.ID)
	if err != nil {
		return snapshot{}, err
	}
	data, err = readGitHubJSON(ctx, client, "rate_limit")
	if err != nil {
		return snapshot{}, err
	}
	return snapshotFromRateLimit(host, userID, data, resources)
}
