package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

const (
	githubResourceFreshFor = 5 * time.Minute
	githubResourceKeepFor  = postgresx.GitHubResourceKeepFor
	githubResourceMaxBytes = 64 << 10
	githubUpstreamMaxBytes = 1 << 20
	githubResourceEntries  = postgresx.GitHubResourceEntries
)

type githubRepositoryResource struct {
	FullName        string  `json:"full_name"`
	Description     *string `json:"description"`
	HTMLURL         string  `json:"html_url"`
	DefaultBranch   string  `json:"default_branch"`
	StargazersCount int64   `json:"stargazers_count"`
	ForksCount      int64   `json:"forks_count"`
	OpenIssuesCount int64   `json:"open_issues_count"`
	Archived        bool    `json:"archived"`
	Visibility      string  `json:"visibility"`
}

type githubReleaseResource struct {
	TagName     string  `json:"tag_name"`
	Name        *string `json:"name"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt *string `json:"published_at"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
}

type githubResourceEntry struct {
	body         []byte
	etag         string
	upstreamETag string
	validated    time.Time
}

type githubResourceStore interface {
	ReadGitHubResource(context.Context, string) (postgresx.GitHubResource, bool, error)
	WriteGitHubResource(context.Context, string, postgresx.GitHubResource) error
	DeleteGitHubResource(context.Context, string) error
}

type githubResourceCache struct {
	store  githubResourceStore
	flight singleflight.Group
	// These dependencies are overridden only by focused package tests.
	localToken func(context.Context) (string, error)
	transport  http.RoundTripper
}

func (c *githubResourceCache) load(ctx context.Context, key string) (githubResourceEntry, bool, error) {
	if c.store == nil {
		return githubResourceEntry{}, false, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
	}
	value, found, err := c.store.ReadGitHubResource(ctx, key)
	return githubResourceEntry{body: value.Body, etag: value.ETag, upstreamETag: value.UpstreamETag, validated: value.Validated}, found, err
}

func (c *githubResourceCache) save(ctx context.Context, key string, entry githubResourceEntry) error {
	return c.store.WriteGitHubResource(ctx, key, postgresx.GitHubResource{
		Body: entry.body, ETag: entry.etag, UpstreamETag: entry.upstreamETag, Validated: entry.validated,
	})
}

type githubResourceError struct {
	status    int
	retry     time.Time
	denyStale bool
}

type githubResourceCredentials struct {
	token  string
	base   string
	app    string
	client *http.Client
}

func (e *githubResourceError) Error() string { return "GitHub resource is unavailable" }

func (a *App) gitHubRepositoryResource(w http.ResponseWriter, r *http.Request) {
	a.gitHubResource(w, r, false)
}

func (a *App) gitHubReleaseResource(w http.ResponseWriter, r *http.Request) {
	a.gitHubResource(w, r, true)
}

//nolint:contextcheck // Shared bounded revalidation must survive cancellation of its first subscriber.
func (a *App) gitHubResource(w http.ResponseWriter, r *http.Request, release bool) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(r.Context(), w, http.StatusMethodNotAllowed, "GitHub resources are read-only")
		return
	}
	w.Header().Add("Vary", "Authorization")
	w.Header().Add("Vary", "Cookie")
	repository, err := collect.NormalizeRepository(r.PathValue("owner") + "/" + r.PathValue("repo"))
	if err != nil || len(repository) > 256 || r.URL.RawQuery != "" {
		writeError(r.Context(), w, http.StatusBadRequest, "invalid GitHub resource request")
		return
	}
	installation, err := a.githubResourceScope(r.Context(), repository)
	if err != nil {
		writeGitHubResourceError(r.Context(), w, err)
		return
	}
	credentialCtx, cancelCredentials := context.WithTimeout(r.Context(), 15*time.Second)
	credentials, err := a.githubResourceCredentials(credentialCtx, installation)
	cancelCredentials()
	if err != nil {
		writeGitHubResourceError(r.Context(), w, err)
		return
	}
	path := "repos/" + repository
	if release {
		path += "/releases/latest"
	}
	key := githubResourceKey(installation, path, r.Header, credentials.token)
	entry, ok, err := a.githubResources.load(r.Context(), key)
	if err != nil {
		writeGitHubResourceError(r.Context(), w, err)
		return
	}
	stale := false
	var staleError error
	if !ok || time.Since(entry.validated) >= githubResourceFreshFor {
		result := a.githubResources.flight.DoChan(key, func() (any, error) {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Second)
			defer cancel()
			cached, found, err := a.githubResources.load(ctx, key)
			if err != nil {
				return nil, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
			}
			if found && time.Since(cached.validated) < githubResourceFreshFor {
				return cached, nil
			}
			return a.fetchGitHubResource(ctx, credentials, installation, path, key, release)
		})
		select {
		case <-r.Context().Done():
			return
		case fetched := <-result:
			if fetched.Err != nil {
				if !ok || !githubResourceTransientError(fetched.Err) {
					writeGitHubResourceError(r.Context(), w, fetched.Err)
					return
				}
				stale = true
				staleError = fetched.Err
			} else {
				entry = fetched.Val.(githubResourceEntry)
			}
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=300, stale-while-revalidate=86400")
	w.Header().Set("Age", strconv.Itoa(max(0, int(time.Since(entry.validated).Seconds()))))
	w.Header().Set("ETag", entry.etag)
	if stale {
		w.Header().Set("Cache-Control", "private, max-age=0, stale-while-revalidate=86400")
		w.Header().Set("Warning", `110 - "Response is stale"`)
		writeGitHubResourceRetryAfter(w, staleError)
	}
	if !stale && githubETagMatches(r.Header.Get("If-None-Match"), entry.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(entry.body)
}

func githubResourceKey(installation int64, path string, headers http.Header, credential string) string {
	identity := sha256.Sum256([]byte(headers.Get("Authorization") + "\x00" + headers.Get("Cookie") + "\x00" + credential))
	return strconv.FormatInt(installation, 10) + ":" + path + ":" + hex.EncodeToString(identity[:])
}

func (a *App) githubLocalResourceSession() string {
	if a.accessToken == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("cao-github-resource-session\x00" + a.accessToken))
	return hex.EncodeToString(sum[:])
}

func (a *App) githubResourceScope(ctx context.Context, repository string) (int64, error) {
	if a.config.HostProfile.Name == "local" && a.config.HostProfile.Authentication == HostAuthenticationBearer {
		if strings.EqualFold(repository, a.config.ActionsRepository) ||
			(a.config.Collector != nil && strings.EqualFold(repository, a.config.Collector.ControlRepository)) {
			return 1, nil
		}
		return 0, &githubResourceError{status: http.StatusForbidden}
	}
	collector := a.Collector()
	if collector == nil || collector.client == nil || a.config.Collector == nil || a.config.Collector.AdmitOnly {
		return 0, &githubResourceError{status: http.StatusServiceUnavailable}
	}
	installation, err := collector.enrollment.InstallationFor(ctx, repository)
	if err != nil {
		return 0, &githubResourceError{status: http.StatusServiceUnavailable}
	}
	if installation <= 0 {
		return 0, &githubResourceError{status: http.StatusForbidden}
	}
	return installation, nil
}

func localGitHubResourceToken(ctx context.Context, executable string) (string, error) {
	command := exec.CommandContext(ctx, executable, "auth", "token", "--hostname", "github.com") // #nosec G204 -- The owning local canvas selects its CLI; browser requests cannot set executable paths or arguments.
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "", &githubResourceError{status: http.StatusServiceUnavailable}
	}
	return strings.TrimSpace(string(output)), nil
}

func (a *App) githubResourceCredentials(ctx context.Context, installation int64) (githubResourceCredentials, error) {
	base, app := "https://api.github.com", "local-cli"
	transport := http.DefaultTransport
	var token string
	var err error
	if a.config.HostProfile.Name == "local" && a.config.HostProfile.Authentication == HostAuthenticationBearer {
		resolve := a.githubResources.localToken
		if resolve == nil {
			executable := "gh"
			if a.config.Canvas != nil && a.config.Canvas.GitHubCLI != "" {
				executable = a.config.Canvas.GitHubCLI
			}
			resolve = func(ctx context.Context) (string, error) { return localGitHubResourceToken(ctx, executable) }
		}
		token, err = resolve(ctx)
	} else {
		collector := a.Collector()
		if collector == nil || collector.client == nil || a.config.Collector == nil {
			return githubResourceCredentials{}, &githubResourceError{status: http.StatusServiceUnavailable}
		}
		token, err = collector.client.InstallationToken(ctx, installation)
		app = "github-app-" + strconv.FormatInt(a.config.Collector.AppID, 10)
		if a.config.Collector.BaseURL != "" {
			base = strings.TrimSuffix(a.config.Collector.BaseURL, "/")
		}
		if a.config.Collector.Transport != nil {
			transport = a.config.Collector.Transport
		}
	}
	if err != nil || strings.TrimSpace(token) == "" {
		return githubResourceCredentials{}, &githubResourceError{status: http.StatusServiceUnavailable}
	}
	if a.githubResources.transport != nil {
		transport = a.githubResources.transport
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return githubResourceCredentials{token: token, base: base, app: app, client: client}, nil
}

func githubResourceRequest(ctx context.Context, client *http.Client, base, token, path, etag string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	return client.Do(request)
}

func (a *App) reserveGitHubResource(ctx context.Context, client *http.Client, token, base string, bucket githubquota.BucketID) (githubquota.Reservation, error) {
	if a.quota == nil {
		return githubquota.Reservation{}, &githubResourceError{status: http.StatusServiceUnavailable}
	}
	reservation, err := a.quota.Reserve(ctx, bucket, githubquota.ReservationRequest{EstimatedCost: 1})
	if !errors.Is(err, githubquota.ErrUnknown) {
		return reservation, err
	}
	// Serialize cold-bucket probes as well as resource reads.
	_, err, _ = a.githubResources.flight.Do("quota:"+bucket.String(), func() (any, error) {
		state, stateErr := a.quota.State(ctx, bucket)
		if stateErr != nil || state.Status != githubquota.StatusUnknown {
			return nil, stateErr
		}
		response, requestErr := githubResourceRequest(ctx, client, base, token, "rate_limit", "")
		if requestErr != nil {
			return nil, requestErr
		}
		defer func() { _ = response.Body.Close() }()
		quota, recordErr := a.quota.RecordResponse(ctx, bucket.App, bucket.Installation, response.Header, response.StatusCode)
		if recordErr != nil {
			return nil, recordErr
		}
		if limited := githubResourceRateLimit(response.StatusCode, quota); limited != nil {
			return nil, limited
		}
		if response.StatusCode != http.StatusOK || !quota.HasObservation {
			return nil, &githubResourceError{status: http.StatusServiceUnavailable}
		}
		return nil, nil
	})
	if err != nil {
		return githubquota.Reservation{}, err
	}
	return a.quota.Reserve(ctx, bucket, githubquota.ReservationRequest{EstimatedCost: 1})
}

func (a *App) fetchGitHubResource(ctx context.Context, credentials githubResourceCredentials, installation int64, path, key string, release bool) (githubResourceEntry, error) {
	token, base, app, client := credentials.token, credentials.base, credentials.app, credentials.client
	bucket := githubquota.BucketID{App: app, Installation: installation, Resource: githubquota.ResourceCore}
	reservation, err := a.reserveGitHubResource(ctx, client, token, base, bucket)
	if err != nil {
		return githubResourceEntry{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = a.quota.Release(cleanup, reservation)
	}()
	cached, exists, err := a.githubResources.load(ctx, key)
	if err != nil {
		return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
	}
	response, err := githubResourceRequest(ctx, client, base, token, path, cached.upstreamETag)
	if err != nil {
		return githubResourceEntry{}, err
	}
	defer func() { _ = response.Body.Close() }()
	quota, err := a.quota.RecordResponse(ctx, app, installation, response.Header, response.StatusCode)
	if err != nil {
		return githubResourceEntry{}, err
	}
	if limited := githubResourceRateLimit(response.StatusCode, quota); limited != nil {
		return githubResourceEntry{}, limited
	}
	if response.StatusCode == http.StatusNotFound {
		if err := a.githubResources.store.DeleteGitHubResource(ctx, key); err != nil {
			return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
		}
		return githubResourceEntry{}, &githubResourceError{status: http.StatusNotFound}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		if err := a.githubResources.store.DeleteGitHubResource(ctx, key); err != nil {
			return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
		}
		return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
	}
	if response.StatusCode == http.StatusNotModified && exists {
		cached.validated = time.Now()
		if etag := response.Header.Get("ETag"); etag != "" {
			cached.upstreamETag = etag
		}
		if err := a.githubResources.save(ctx, key, cached); err != nil {
			return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
		}
		return cached, nil
	}
	if response.StatusCode != http.StatusOK {
		return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, githubUpstreamMaxBytes+1))
	if err != nil || len(body) > githubUpstreamMaxBytes {
		return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable}
	}
	normalized, err := normalizeGitHubResource(body, release)
	if err != nil || len(normalized) > githubResourceMaxBytes {
		if err == nil {
			err = &githubResourceError{status: http.StatusServiceUnavailable}
		}
		return githubResourceEntry{}, err
	}
	sum := sha256.Sum256(normalized)
	entry := githubResourceEntry{body: normalized, etag: `"` + hex.EncodeToString(sum[:]) + `"`,
		upstreamETag: response.Header.Get("ETag"), validated: time.Now()}
	if err := a.githubResources.save(ctx, key, entry); err != nil {
		return githubResourceEntry{}, &githubResourceError{status: http.StatusServiceUnavailable, denyStale: true}
	}
	return entry, nil
}

func normalizeGitHubResource(body []byte, release bool) ([]byte, error) {
	if release {
		var value githubReleaseResource
		if err := json.Unmarshal(body, &value); err != nil || value.TagName == "" || value.HTMLURL == "" {
			return nil, &githubResourceError{status: http.StatusServiceUnavailable}
		}
		return json.Marshal(value)
	}
	var value githubRepositoryResource
	if err := json.Unmarshal(body, &value); err != nil || value.FullName == "" || value.HTMLURL == "" {
		return nil, &githubResourceError{status: http.StatusServiceUnavailable}
	}
	return json.Marshal(value)
}

func githubETagMatches(header, etag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

func githubResourceRateLimit(status int, quota githubquota.ResponseQuota) error {
	if status != http.StatusTooManyRequests &&
		(status != http.StatusForbidden || (quota.ParkUntil.IsZero() && (!quota.HasObservation || quota.Observation.Remaining > 0))) {
		return nil
	}
	retry := quota.ParkUntil
	if retry.IsZero() && quota.HasObservation {
		retry = quota.Observation.ResetAt
	}
	return &githubResourceError{status: http.StatusTooManyRequests, retry: retry}
}

func githubResourceTransientError(err error) bool {
	var resourceError *githubResourceError
	if errors.As(err, &resourceError) {
		return !resourceError.denyStale && (resourceError.status == http.StatusServiceUnavailable || resourceError.status == http.StatusTooManyRequests)
	}
	return true
}

func writeGitHubResourceError(ctx context.Context, w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	var resourceError *githubResourceError
	var unavailable *githubquota.UnavailableError
	switch {
	case errors.As(err, &resourceError):
		status = resourceError.status
	case errors.As(err, &unavailable):
		if unavailable.Status == githubquota.StatusExhausted || unavailable.Status == githubquota.StatusParked {
			status = http.StatusTooManyRequests
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		writeGitHubResourceRetryAfter(w, err)
	}
	writeError(ctx, w, status, "GitHub resource is unavailable")
}

func writeGitHubResourceRetryAfter(w http.ResponseWriter, err error) {
	retry := time.Now().Add(time.Minute)
	var resourceError *githubResourceError
	var unavailable *githubquota.UnavailableError
	switch {
	case errors.As(err, &resourceError):
		if !resourceError.retry.IsZero() {
			retry = resourceError.retry
		}
	case errors.As(err, &unavailable):
		if !unavailable.RetryAt.IsZero() {
			retry = unavailable.RetryAt
		}
	}
	seconds := max(1, int(time.Until(retry).Seconds()+1))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}
