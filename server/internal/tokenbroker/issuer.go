package tokenbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"

	"github.com/githubnext/gh-cao/server/internal/admission"
)

const (
	githubAPI          = "https://api.github.com"
	githubAPIBodyLimit = 256 << 10
)

var errUpstream = errors.New("credential issuer unavailable")

func (s *Service) issue(ctx context.Context, identity admission.Identity) (issued credential, issueErr error) {
	ctx, observed := observe(ctx, spanIssue, s.telemetry.token)
	defer func() {
		result := outcomeSuccess
		if issueErr != nil {
			result = outcomeUnavailable
		}
		observed.finish(operationIssue, result)
	}()
	pem, err := s.keyReader.PrivateKey(ctx)
	if err != nil {
		return credential{}, errUpstream
	}
	defer clear(pem)
	appTransport, err := ghinstallation.NewAppsTransport(s.client.Transport, s.config.AppID, pem)
	if err != nil {
		return credential{}, errUpstream
	}
	client := *s.client
	client.Transport = appTransport
	base := githubAPI + "/app/installations/" + strconv.FormatInt(s.config.InstallationID, 10)
	body, err := upstream(ctx, &client, http.MethodGet, base, "", nil, http.StatusOK)
	if err != nil {
		return credential{}, err
	}
	var installation struct {
		AppID   int64 `json:"app_id"`
		Account struct {
			ID   json.Number `json:"id"`
			Type string      `json:"type"`
		} `json:"account"`
		SuspendedAt json.RawMessage   `json:"suspended_at"`
		Permissions map[string]string `json:"permissions"`
	}
	if json.Unmarshal(body, &installation) != nil || installation.AppID != s.config.AppID ||
		installation.Account.Type != "Organization" || installation.Account.ID.String() != identity.OwnerID ||
		installation.Account.ID.String() != s.config.OwnerID || string(installation.SuspendedAt) != "null" ||
		installation.Permissions["copilot_requests"] != "read" {
		return credential{}, errUpstream
	}
	repoID, err := strconv.ParseUint(identity.RepositoryID, 10, 64)
	if err != nil {
		return credential{}, errUpstream
	}
	payload, _ := json.Marshal(struct {
		Permissions   map[string]string `json:"permissions"`
		RepositoryIDs []uint64          `json:"repository_ids"`
	}{map[string]string{"copilot_requests": "read"}, []uint64{repoID}})
	body, err = upstream(ctx, &client, http.MethodPost, base+"/access_tokens", "", payload, http.StatusCreated)
	if err != nil {
		return credential{}, err
	}
	var result struct {
		Token        string            `json:"token"`
		ExpiresAt    string            `json:"expires_at"`
		Permissions  map[string]string `json:"permissions"`
		Repositories []struct {
			ID    json.Number `json:"id"`
			Owner struct {
				ID json.Number `json:"id"`
			} `json:"owner"`
		} `json:"repositories"`
	}
	if json.Unmarshal(body, &result) != nil {
		return credential{Token: result.Token}, errUpstream
	}
	expiry, parseErr := time.Parse(time.RFC3339, result.ExpiresAt)
	token := credential{Token: result.Token, ExpiresAt: expiry}
	now := s.now()
	if parseErr != nil || !safeToken(result.Token) ||
		!expiry.After(now.Add(time.Minute)) || expiry.After(now.Add(time.Hour)) ||
		result.Permissions["copilot_requests"] != "read" || len(result.Repositories) != 1 ||
		result.Repositories[0].ID.String() != identity.RepositoryID ||
		result.Repositories[0].Owner.ID.String() != identity.OwnerID {
		return token, errUpstream
	}
	for permission, level := range result.Permissions {
		if permission != "copilot_requests" && (permission != "metadata" || level != "read") {
			return token, errUpstream
		}
	}
	return token, nil
}

func upstream(ctx context.Context, client *http.Client, method, endpoint, token string, payload []byte, expected int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, errUpstream
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		if !safeToken(token) {
			return nil, errUpstream
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errUpstream
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, githubAPIBodyLimit+1))
	if err != nil || len(body) > githubAPIBodyLimit {
		return nil, errUpstream
	}
	if res.StatusCode != expected {
		// Revocation can succeed remotely even if its response is lost. A
		// subsequent 401 from this fixed endpoint proves the held credential
		// no longer authenticates, so cleanup is already complete.
		if method == http.MethodDelete && endpoint == githubAPI+"/installation/token" &&
			token != "" && expected == http.StatusNoContent && res.StatusCode == http.StatusUnauthorized {
			return nil, nil
		}
		return nil, errUpstream
	}
	if expected != http.StatusNoContent && uniqueJSON(body) != nil {
		return nil, errUpstream
	}
	return body, nil
}

func (s *Service) revokeToken(ctx context.Context, token string) error {
	_, err := upstream(ctx, s.client, http.MethodDelete, githubAPI+"/installation/token", token, nil, http.StatusNoContent)
	return err
}
