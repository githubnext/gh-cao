// Package githubapp wraps GitHub App authentication and the small set of
// App-level API calls the optional server ingestion profile needs.
//
// It is a thin layer over go-github and ghinstallation: token minting,
// caching, and transport belong to those libraries. What this package adds is
// the CAO-specific policy around them — enrollment enumeration, webhook
// delivery replay, and per-installation rate-limit governance that is shared
// across workers through Redis.
package githubapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v66/github"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/logger"
)

var appLog = logger.New("cao:githubapp")

// Config describes the read-only GitHub App the collector authenticates as.
type Config struct {
	AppID          int64
	PrivateKeyPEM  []byte
	BaseURL        string
	UploadURL      string
	RequestTimeout time.Duration
	Transport      http.RoundTripper
}

// Validate reports whether the configuration can authenticate at all. It
// deliberately fails closed: a partially configured App is never treated as an
// unauthenticated client.
func (c Config) Validate() error {
	if c.AppID <= 0 {
		return errors.New("GitHub App id is required")
	}
	if len(c.PrivateKeyPEM) == 0 {
		return errors.New("GitHub App private key is required")
	}
	return nil
}

// Client authenticates as the App and mints installation clients on demand.
type Client struct {
	config        Config
	appTransport  *ghinstallation.AppsTransport
	appClient     *github.Client
	mutex         sync.Mutex
	installations map[int64]*installationEntry
	identityMutex sync.Mutex
	identity      *BotIdentity
}

type installationEntry struct {
	transport *ghinstallation.Transport
	client    *github.Client
}

// Installation is one App installation and the account it belongs to.
type Installation struct {
	ID      int64
	Account string
}

// Repository is one enrolled repository.
type Repository struct {
	ID         int64
	FullName   string
	PushedAt   time.Time
	Private    bool
	Visibility string
	Archived   bool
}

// WorkflowRun is the stable run identity needed by historical collection.
type WorkflowRun struct {
	ID        int64
	Attempt   int
	CreatedAt time.Time
}

// GitTreeEntry is one entry returned by the Git tree API.
type GitTreeEntry struct {
	Path string
	OID  string
	Mode string
	Type string
	Size int64
}

// APIResponse carries quota metadata and conditional-response state.
type APIResponse struct {
	Remaining   int
	Reset       time.Time
	RetryAfter  time.Duration
	StatusCode  int
	ETag        string
	NotModified bool
	Truncated   bool
	Secondary   bool
}

// ErrNotFound reports that a requested Git object or ref does not exist.
var ErrNotFound = errors.New("GitHub object was not found")

// Delivery is one App webhook delivery, used for gap recovery.
type Delivery struct {
	ID             int64
	GUID           string
	Event          string
	StatusCode     int
	DeliveredAt    time.Time
	Redelivery     bool
	InstallationID int64
}

// New constructs a client for the configured App.
func New(config Config) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 * time.Second
	}
	baseTransport := config.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	transport, err := ghinstallation.NewAppsTransport(baseTransport, config.AppID, config.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("configure GitHub App transport: %w", err)
	}
	if base := strings.TrimSpace(config.BaseURL); base != "" {
		transport.BaseURL = strings.TrimSuffix(base, "/")
	}
	httpClient := &http.Client{Transport: transport, Timeout: config.RequestTimeout}
	appClient, err := enterpriseClient(httpClient, config)
	if err != nil {
		return nil, err
	}
	return &Client{
		config:        config,
		appTransport:  transport,
		appClient:     appClient,
		installations: map[int64]*installationEntry{},
	}, nil
}

func enterpriseClient(httpClient *http.Client, config Config) (*github.Client, error) {
	client := github.NewClient(httpClient)
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		return client, nil
	}
	upload := strings.TrimSpace(config.UploadURL)
	if upload == "" {
		upload = base
	}
	enterprise, err := client.WithEnterpriseURLs(base, upload)
	if err != nil {
		return nil, fmt.Errorf("configure GitHub base URL: %w", err)
	}
	return enterprise, nil
}

// ListInstallations enumerates every installation of the App. It is used for
// cold start, repository-list reconciliation, and explicit repair.
func (c *Client) ListInstallations(ctx context.Context) ([]Installation, error) {
	options := &github.ListOptions{PerPage: 100}
	var installations []Installation
	for {
		page, response, err := c.appClient.Apps.ListInstallations(ctx, options)
		if err != nil {
			return nil, fmt.Errorf("list GitHub App installations: %w", err)
		}
		for _, installation := range page {
			if !installation.GetSuspendedAt().IsZero() {
				continue
			}
			if installation.GetID() <= 0 {
				return nil, errors.New("GitHub App enumeration returned an invalid installation")
			}
			installations = append(installations, Installation{
				ID:      installation.GetID(),
				Account: installation.GetAccount().GetLogin(),
			})
		}
		if response == nil || response.NextPage == 0 {
			break
		}
		if response.NextPage <= options.Page {
			return nil, errors.New("GitHub App enumeration returned a non-advancing page")
		}
		options.Page = response.NextPage
	}
	appLog.Printf("enumerated installations count=%d", len(installations))
	return installations, nil
}

// ListRepositories enumerates the repositories one installation covers.
func (c *Client) ListRepositories(ctx context.Context, installationID int64) ([]Repository, error) {
	var repositories []Repository
	for page := 1; page != 0; {
		items, next, _, err := c.ListRepositoriesPage(ctx, installationID, page)
		if err != nil {
			return nil, err
		}
		repositories = append(repositories, items...)
		page = next
	}
	appLog.Printf("enumerated installation repositories count=%d", len(repositories))
	return repositories, nil
}

// ListRepositoriesPage leaves pagination and quota admission to the worker.
func (c *Client) ListRepositoriesPage(ctx context.Context, installationID int64, pageNumber int) ([]Repository, int, githubquota.ResponseQuota, error) {
	client, err := c.installationClient(installationID)
	if err != nil {
		return nil, 0, githubquota.ResponseQuota{}, err
	}
	options := &github.ListOptions{PerPage: 100, Page: pageNumber}
	var repositories []Repository
	page, response, err := client.Apps.ListRepos(ctx, options)
	quota := workflowRunResponseQuota(response)
	if err != nil {
		return nil, 0, quota, fmt.Errorf("list installation repositories: %w", err)
	}
	if page != nil {
		for _, repository := range page.Repositories {
			name := repository.GetFullName()
			if name == "" || repository.GetID() <= 0 {
				return nil, 0, quota, errors.New("installation repository is missing its identity")
			}
			repositories = append(repositories, Repository{
				ID:         repository.GetID(),
				FullName:   name,
				PushedAt:   repository.GetPushedAt().Time,
				Private:    repository.GetPrivate(),
				Visibility: repository.GetVisibility(),
				Archived:   repository.GetArchived(),
			})
		}
	}
	next := 0
	if response != nil {
		next = response.NextPage
	}
	return repositories, next, quota, nil
}

// ListWorkflowRuns returns one page of a repository's workflow runs, newest
// first. The caller owns the persisted page cursor so enumeration can resume
// after process restarts.
func (c *Client) ListWorkflowRuns(
	ctx context.Context, installationID int64, repository string, page, perPage int,
) ([]WorkflowRun, int, githubquota.ResponseQuota, error) {
	return c.listWorkflowRuns(ctx, installationID, repository, page, perPage, "")
}

func (c *Client) ListWorkflowRunsWindow(
	ctx context.Context, installationID int64, repository string, page, perPage int, after, before time.Time,
) ([]WorkflowRun, int, githubquota.ResponseQuota, error) {
	if after.IsZero() || !after.Before(before) {
		return nil, 0, githubquota.ResponseQuota{}, errors.New("workflow run window must have increasing bounds")
	}
	created := after.UTC().Format(time.RFC3339) + ".." + before.UTC().Format(time.RFC3339)
	return c.listWorkflowRuns(ctx, installationID, repository, page, perPage, created)
}

func (c *Client) listWorkflowRuns(
	ctx context.Context, installationID int64, repository string, page, perPage int, created string,
) ([]WorkflowRun, int, githubquota.ResponseQuota, error) {
	client, owner, name, err := c.repositoryClient(installationID, repository)
	if err != nil {
		return nil, 0, githubquota.ResponseQuota{}, err
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 100
	}
	runs, response, err := client.Actions.ListRepositoryWorkflowRuns(ctx, owner, name, &github.ListWorkflowRunsOptions{
		ListOptions: github.ListOptions{Page: page, PerPage: perPage},
		Created:     created,
	})
	quota := workflowRunResponseQuota(response)
	if err != nil {
		return nil, 0, quota, fmt.Errorf("list repository workflow runs: %w", err)
	}
	var result []WorkflowRun
	if runs != nil {
		for _, run := range runs.WorkflowRuns {
			if run.GetID() <= 0 {
				continue
			}
			attempt := run.GetRunAttempt()
			if attempt <= 0 {
				attempt = 1
			}
			result = append(result, WorkflowRun{
				ID:        run.GetID(),
				Attempt:   attempt,
				CreatedAt: run.GetCreatedAt().Time,
			})
		}
	}
	nextPage := 0
	if response != nil {
		nextPage = response.NextPage
	}
	return result, nextPage, quota, nil
}

func workflowRunResponseQuota(response *github.Response) githubquota.ResponseQuota {
	if response == nil || response.Response == nil {
		return githubquota.ResponseQuota{}
	}
	quota := githubquota.ParseResponse(
		response.Header, response.StatusCode, time.Now().UTC())
	return quota
}

// QuotaRateLimit reads the core quota observation used to initialize an
// installation bucket before it has any recorded API responses.
func (c *Client) QuotaRateLimit(
	ctx context.Context, installationID int64,
) (githubquota.ResponseQuota, error) {
	client, err := c.installationClient(installationID)
	if err != nil {
		return githubquota.ResponseQuota{}, err
	}
	_, response, err := client.RateLimit.Get(ctx)
	quota := workflowRunResponseQuota(response)
	if err != nil {
		return quota, fmt.Errorf("read installation rate limit: %w", err)
	}
	if response == nil || response.Response == nil {
		return quota, errors.New("rate limit response is missing quota metadata")
	}
	if !quota.HasObservation {
		return quota, errors.New("rate limit response is missing core quota headers")
	}
	return quota, nil
}

// ValidateRepositoryAccess enumerates the App's complete repository scope and
// rejects a public control repository whose credentials can read private
// repositories. The full enumeration is intentional: startup must fail before
// private evidence can enter a publicly operated database.
func (c *Client) ValidateRepositoryAccess(ctx context.Context, controlRepository string) error {
	installations, err := c.ListInstallations(ctx)
	if err != nil {
		return err
	}
	var repositories []Repository
	for _, installation := range installations {
		covered, err := c.ListRepositories(ctx, installation.ID)
		if err != nil {
			return err
		}
		repositories = append(repositories, covered...)
	}
	return ValidateRepositoryVisibility(controlRepository, repositories)
}

// ValidateRepositoryVisibility enforces the public-control repository boundary
// without including private repository names in diagnostics.
func ValidateRepositoryVisibility(controlRepository string, repositories []Repository) error {
	controlRepository = strings.TrimSpace(controlRepository)
	if controlRepository == "" {
		return errors.New("control repository is required for repository visibility validation")
	}
	controlPublic := false
	controlFound := false
	privateCount := 0
	for _, repository := range repositories {
		if strings.EqualFold(repository.FullName, controlRepository) {
			controlFound = true
			controlPublic = repositoryPublic(repository)
		}
		if !repositoryPublic(repository) {
			privateCount++
		}
	}
	if !controlFound {
		return errors.New("control repository visibility could not be verified")
	}
	if controlPublic && privateCount > 0 {
		return fmt.Errorf(
			"public control repository cannot access non-public repositories: non-public repository count=%d",
			privateCount,
		)
	}
	return nil
}

func repositoryPublic(repository Repository) bool {
	if repository.Visibility != "" {
		return repository.Visibility == "public"
	}
	return !repository.Private
}

// InstallationToken mints a token for the collection subprocess. The token is
// never logged and never persisted.
func (c *Client) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	entry, err := c.installation(installationID)
	if err != nil {
		return "", err
	}
	token, err := entry.transport.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("mint installation token: %w", err)
	}
	return token, nil
}

// RateLimit reads the current core rate-limit state for an installation.
func (c *Client) RateLimit(ctx context.Context, installationID int64) (int, time.Time, error) {
	client, err := c.installationClient(installationID)
	if err != nil {
		return 0, time.Time{}, err
	}
	limits, _, err := client.RateLimit.Get(ctx)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read installation rate limit: %w", err)
	}
	if limits == nil || limits.Core == nil {
		return 0, time.Time{}, errors.New("rate limit response is missing core limits")
	}
	return limits.Core.Remaining, limits.Core.Reset.Time, nil
}

// ResolveRef resolves one repository ref, optionally using an ETag for conditional requests.
func (c *Client) ResolveRef(
	ctx context.Context, installationID int64, repository, ref, etag string,
) (string, APIResponse, error) {
	client, owner, name, err := c.repositoryClient(installationID, repository)
	if err != nil {
		return "", APIResponse{}, err
	}
	ref = strings.TrimPrefix(ref, "refs/")
	refParts := strings.Split(ref, "/")
	for index := range refParts {
		refParts[index] = url.PathEscape(refParts[index])
	}
	request, err := client.NewRequest(
		http.MethodGet,
		"repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/git/ref/"+strings.Join(refParts, "/"),
		nil,
	)
	if err != nil {
		return "", APIResponse{}, err
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	reference := new(github.Reference)
	response, err := client.Do(ctx, request, reference)
	state := apiResponse(response, err)
	if state.NotModified {
		return "", state, nil
	}
	if err != nil {
		return "", state, classifyGitHubError("resolve repository ref", err, state)
	}
	oid := reference.GetObject().GetSHA()
	if oid == "" {
		return "", state, errors.New("repository ref is missing an object id")
	}
	return oid, state, nil
}

// Tree reads one recursive Git tree.
func (c *Client) Tree(
	ctx context.Context, installationID int64, repository, oid string,
) ([]GitTreeEntry, APIResponse, error) {
	client, owner, name, err := c.repositoryClient(installationID, repository)
	if err != nil {
		return nil, APIResponse{}, err
	}
	tree, response, err := client.Git.GetTree(ctx, owner, name, oid, true)
	state := apiResponse(response, err)
	if err != nil {
		return nil, state, classifyGitHubError("read repository tree", err, state)
	}
	state.Truncated = tree.GetTruncated()
	entries := make([]GitTreeEntry, 0, len(tree.Entries))
	for _, entry := range tree.Entries {
		entries = append(entries, GitTreeEntry{
			Path: entry.GetPath(),
			OID:  entry.GetSHA(),
			Mode: entry.GetMode(),
			Type: entry.GetType(),
			Size: int64(entry.GetSize()),
		})
	}
	return entries, state, nil
}

// Blob reads one Git blob as raw bytes.
func (c *Client) Blob(
	ctx context.Context, installationID int64, repository, oid string,
) ([]byte, APIResponse, error) {
	client, owner, name, err := c.repositoryClient(installationID, repository)
	if err != nil {
		return nil, APIResponse{}, err
	}
	content, response, err := client.Git.GetBlobRaw(ctx, owner, name, oid)
	state := apiResponse(response, err)
	if err != nil {
		return nil, state, classifyGitHubError("read repository blob", err, state)
	}
	return content, state, nil
}

// ListDeliveries reads App webhook deliveries newest first. Gap recovery walks
// this list back to the last processed delivery instead of sweeping
// repositories.
func (c *Client) ListDeliveries(ctx context.Context, cursor string, limit int) ([]Delivery, string, error) {
	if limit <= 0 {
		limit = 100
	}
	options := &github.ListCursorOptions{PerPage: 100, Cursor: cursor}
	var deliveries []Delivery
	next := cursor
	for len(deliveries) < limit {
		page, response, err := c.appClient.Apps.ListHookDeliveries(ctx, options)
		if err != nil {
			return nil, "", fmt.Errorf("list App webhook deliveries: %w", err)
		}
		for _, delivery := range page {
			deliveries = append(deliveries, Delivery{
				ID:             delivery.GetID(),
				GUID:           delivery.GetGUID(),
				Event:          delivery.GetEvent(),
				StatusCode:     delivery.GetStatusCode(),
				DeliveredAt:    delivery.GetDeliveredAt().Time,
				Redelivery:     delivery.GetRedelivery(),
				InstallationID: delivery.GetInstallationID(),
			})
		}
		if response == nil || response.Cursor == "" {
			next = ""
			break
		}
		next = response.Cursor
		options.Cursor = response.Cursor
	}
	return deliveries, next, nil
}

// Redeliver asks GitHub to send a delivery again. Redelivered events flow
// through the ordinary admission path, including deduplication.
func (c *Client) Redeliver(ctx context.Context, deliveryID int64) error {
	if _, _, err := c.appClient.Apps.RedeliverHookDelivery(ctx, deliveryID); err != nil {
		return fmt.Errorf("redeliver App webhook delivery: %w", err)
	}
	return nil
}

func (c *Client) installationClient(installationID int64) (*github.Client, error) {
	entry, err := c.installation(installationID)
	if err != nil {
		return nil, err
	}
	return entry.client, nil
}

func (c *Client) repositoryClient(
	installationID int64, repository string,
) (*github.Client, string, string, error) {
	owner, name, found := strings.Cut(strings.TrimSpace(repository), "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return nil, "", "", fmt.Errorf("invalid repository reference %q", repository)
	}
	client, err := c.installationClient(installationID)
	return client, owner, name, err
}

func apiResponse(response *github.Response, err error) APIResponse {
	state := APIResponse{}
	if response != nil {
		state.Remaining = response.Rate.Remaining
		state.Reset = response.Rate.Reset.Time
		if response.Response != nil {
			state.StatusCode = response.StatusCode
			state.ETag = response.Header.Get("ETag")
			state.NotModified = response.StatusCode == http.StatusNotModified
		}
	}
	var primary *github.RateLimitError
	if errors.As(err, &primary) {
		state.Remaining = primary.Rate.Remaining
		state.Reset = primary.Rate.Reset.Time
		if primary.Response != nil {
			state.StatusCode = primary.Response.StatusCode
		}
	}
	var tokenErr *ghinstallation.HTTPError
	if errors.As(err, &tokenErr) && tokenErr.Response != nil {
		state = tokenResponse(tokenErr.Response)
	}
	var secondary *github.AbuseRateLimitError
	if errors.As(err, &secondary) {
		state.Secondary = true
		if secondary.Response != nil {
			state.StatusCode = secondary.Response.StatusCode
		}
		if secondary.RetryAfter != nil {
			state.RetryAfter = *secondary.RetryAfter
		}
	}
	return state
}

// tokenResponse reads rate-limit metadata from a failed installation-token
// request. The token is minted inside the transport, so go-github never sees
// the response; without this a rate-limited mint would bypass the governor.
func tokenResponse(response *http.Response) APIResponse {
	state := APIResponse{StatusCode: response.StatusCode}
	if remaining, err := strconv.Atoi(response.Header.Get("X-RateLimit-Remaining")); err == nil {
		state.Remaining = remaining
	}
	if reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && reset > 0 {
		state.Reset = time.Unix(reset, 0).UTC()
	}
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		state.RetryAfter = time.Duration(seconds) * time.Second
	}
	state.Secondary = response.StatusCode == http.StatusForbidden &&
		state.RetryAfter > 0 && response.Header.Get("X-RateLimit-Remaining") != "0"
	return state
}

func classifyGitHubError(operation string, err error, response APIResponse) error {
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (c *Client) installation(installationID int64) (*installationEntry, error) {
	if installationID <= 0 {
		return nil, errors.New("installation id is required")
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if entry, ok := c.installations[installationID]; ok {
		return entry, nil
	}
	transport := ghinstallation.NewFromAppsTransport(c.appTransport, installationID)
	if base := strings.TrimSpace(c.config.BaseURL); base != "" {
		transport.BaseURL = strings.TrimSuffix(base, "/")
	}
	httpClient := &http.Client{Transport: transport, Timeout: c.config.RequestTimeout}
	client, err := enterpriseClient(httpClient, c.config)
	if err != nil {
		return nil, err
	}
	entry := &installationEntry{transport: transport, client: client}
	c.installations[installationID] = entry
	return entry, nil
}
