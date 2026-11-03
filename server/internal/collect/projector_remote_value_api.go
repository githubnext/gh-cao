package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
)

// A frozen bounded graphQuery(repo, numbers) has at most ten issue aliases,
// each with one 100-item timeline and one 100-item closing-issue connection.
// The documented GitHub cost formula gives under 10 points for this shape.
// Reserving 500 points leaves ample headroom if its pricing changes; no other
// query shape is permitted and the returned actual cost must not exceed 500.
const remoteValueGraphPoints = 500
const (
	remoteValueCacheEntries = 512
	remoteValueCacheBytes   = 32 << 20
)

var remoteValueGraphAlias = regexp.MustCompile(`n([1-9][0-9]*): issueOrPullRequest\(number:([1-9][0-9]*)\)`)

func remoteValueGraphQuery(repository, query string) bool {
	matches := remoteValueGraphAlias.FindAllStringSubmatch(query, 11)
	if len(matches) == 0 || len(matches) > 10 || len(query) > 128<<10 {
		return false
	}
	numbers := make([]int, 0, len(matches))
	for _, match := range matches {
		if match[1] != match[2] {
			return false
		}
		number, err := strconv.Atoi(match[1])
		if err != nil || number <= 0 || number > 1<<31-1 {
			return false
		}
		numbers = append(numbers, number)
	}
	return query == remoteValueExpectedGraphQuery(repository, numbers)
}

func remoteValueExpectedGraphQuery(repository string, numbers []int) string {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok {
		return ""
	}
	const reference = "id number url repository { nameWithOwner }"
	timeline := func(pull bool) string {
		merged := ""
		if pull {
			merged = ",MERGED_EVENT"
		}
		extra := ""
		if pull {
			extra = "... on MergedEvent { createdAt }"
		}
		return `timelineItems(first:100,itemTypes:[CLOSED_EVENT,REOPENED_EVENT,CROSS_REFERENCED_EVENT` + merged + `]) {
    nodes { __typename ... on ClosedEvent { createdAt closer { __typename ... on PullRequest { ` + reference + ` } } }
      ... on ReopenedEvent { createdAt } ` + extra + `
      ... on CrossReferencedEvent { createdAt source { __typename
        ... on PullRequest { ` + reference + ` } ... on Issue { ` + reference + ` } } } }
    pageInfo { hasNextPage } }`
	}
	parts := make([]string, 0, len(numbers))
	for _, number := range numbers {
		parts = append(parts, fmt.Sprintf(`n%d: issueOrPullRequest(number:%d) { __typename
      ... on Issue { %s state stateReason createdAt updatedAt closedAt %s }
      ... on PullRequest { %s state createdAt updatedAt closedAt mergedAt headRefOid
        mergeCommit { oid } closingIssuesReferences(first:100) { nodes { %s } pageInfo { hasNextPage } }
        %s } }`, number, number, reference, timeline(false), reference, reference, timeline(true)))
	}
	return "query { repository(owner:" + strconv.Quote(owner) + ",name:" + strconv.Quote(name) + ") {\n    " +
		strings.Join(parts, "\n") + " } }"
}

// The collector never receives an installation credential. Its confined gh
// executable speaks to this private Unix socket; the host reserves each API
// request against the shared installation buckets before forwarding it.
type remoteValueAPI struct {
	client             *http.Client
	quota              *githubquota.Service
	searchQuota        *githubquota.Service
	app                string
	installation       int64
	repository         string
	collector          string
	base               *url.URL
	token              string
	max                int
	cache              *remoteValueResponseCache
	sharedRequirements map[string]remoteValueRequirement
	mu                 sync.Mutex
	used               int
	sent               int
	preflights         int
	graphPoints        int
	graphUnknown       int
	cacheHits          int
	bytes              int64
}

type remoteValueCachedResponse struct {
	content []byte
	headers http.Header
	status  int
}

type remoteValueResponseCache struct {
	mu      sync.Mutex
	entries map[string]remoteValueCachedResponse
	bytes   int
}

func newRemoteValueResponseCache() *remoteValueResponseCache {
	return &remoteValueResponseCache{entries: map[string]remoteValueCachedResponse{}}
}

func (c *remoteValueResponseCache) get(key string) (remoteValueCachedResponse, bool) {
	if c == nil {
		return remoteValueCachedResponse{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	if !ok {
		return remoteValueCachedResponse{}, false
	}
	value.content = bytes.Clone(value.content)
	value.headers = value.headers.Clone()
	return value, true
}

func (c *remoteValueResponseCache) put(key string, value remoteValueCachedResponse) {
	if c == nil || len(value.content) > remoteValueCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists || len(c.entries) >= remoteValueCacheEntries ||
		c.bytes+len(value.content) > remoteValueCacheBytes {
		return
	}
	value.content = bytes.Clone(value.content)
	value.headers = value.headers.Clone()
	c.entries[key] = value
	c.bytes += len(value.content)
}

func (a *remoteValueAPI) usage() (int, int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sent, a.bytes
}

func (a *remoteValueAPI) costUsage() (int, int, int, int, int, int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sent, a.cacheHits, a.preflights, a.graphPoints, a.graphUnknown, a.bytes
}

func (a *remoteValueAPI) recordBytes(count int) {
	a.mu.Lock()
	a.bytes += int64(count)
	a.mu.Unlock()
}

func (a *remoteValueAPI) recordCacheHit() {
	a.mu.Lock()
	a.cacheHits++
	a.mu.Unlock()
}

func (a *remoteValueAPI) sharedCacheKey(requirementID, resource, requestKey string) (string, bool) {
	requirement, ok := a.sharedRequirements[requirementID]
	if !ok || requirement.Source != "github" || requirement.Cache != "invocation" ||
		requirement.Pool != resource {
		return "", false
	}
	return requirementID + "\x00" + requestKey, true
}

func (a *remoteValueAPI) requestClient() http.Client {
	client := *a.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 {
		client.Timeout = 65 * time.Second
	}
	return client
}

func (a *remoteValueAPI) reserve(ctx context.Context, resource string) (githubquota.Reservation, *githubquota.Service, error) {
	a.mu.Lock()
	if a.used >= a.max {
		a.mu.Unlock()
		return githubquota.Reservation{}, nil, errors.New("reviewed remote Value API request limit exhausted")
	}
	a.used++
	a.mu.Unlock()
	service := a.quota
	if resource == "search" {
		service = a.searchQuota
	}
	if service == nil {
		return githubquota.Reservation{}, nil, errors.New("shared remote Value quota unavailable")
	}
	bucket := githubquota.BucketID{
		App: a.app, Installation: a.installation, Resource: resource,
	}
	cost := 1
	if resource == "graphql" {
		cost = remoteValueGraphPoints
	}
	reservation, err := service.Reserve(ctx, bucket, githubquota.ReservationRequest{EstimatedCost: cost})
	if (resource == "search" || resource == "graphql") && errors.Is(err, githubquota.ErrUnknown) {
		if err = a.observeResource(ctx, resource); err != nil {
			return githubquota.Reservation{}, nil, err
		}
		reservation, err = service.Reserve(ctx, bucket, githubquota.ReservationRequest{EstimatedCost: cost})
	}
	return reservation, service, err
}

func (a *remoteValueAPI) observeResource(ctx context.Context, resource string) error {
	if resource != "search" && resource != "graphql" {
		return errors.New("unsupported remote Value quota preflight")
	}
	reservation, service, err := a.reserve(ctx, "core")
	if err != nil {
		return err
	}
	destination := *a.base
	destination.Path = strings.TrimSuffix(destination.Path, "/") + "/rate_limit"
	destination.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, destination.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+a.token)
	request.Header.Set("Accept", "application/vnd.github+json")
	a.mu.Lock()
	a.sent++
	a.preflights++
	a.mu.Unlock()
	client := a.requestClient()
	response, err := client.Do(request)
	if err != nil {
		return errors.New("remote Value quota preflight transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	a.recordBytes(len(content))
	if err != nil || len(content) > 1<<20 {
		return errors.New("remote Value quota preflight exceeded its byte budget")
	}
	quota := githubquota.ParseResponse(response.Header, response.StatusCode, time.Now())
	if !quota.HasObservation || quota.Resource != "core" {
		return errors.New("remote Value quota preflight lacks authoritative core quota")
	}
	quota.Observation.ObservedAt = time.Time{}
	if err := service.Commit(ctx, reservation, quota.Observation); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return errors.New("remote Value quota preflight failed")
	}
	var result struct {
		Resources map[string]struct {
			Limit     int   `json:"limit"`
			Remaining int   `json:"remaining"`
			Reset     int64 `json:"reset"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(content, &result); err != nil ||
		result.Resources[resource].Limit <= 0 || result.Resources[resource].Remaining < 0 ||
		result.Resources[resource].Remaining > result.Resources[resource].Limit || result.Resources[resource].Reset <= 0 {
		return errors.New("remote Value quota preflight is incomplete")
	}
	bucket := result.Resources[resource]
	target := a.quota
	if resource == "search" {
		target = a.searchQuota
	}
	return target.Observe(ctx, githubquota.BucketID{
		App: a.app, Installation: a.installation, Resource: resource,
	}, githubquota.Observation{
		Limit: bucket.Limit, Remaining: bucket.Remaining,
		ResetAt: time.Unix(bucket.Reset, 0).UTC(),
	})
}

func (a *remoteValueAPI) request(
	ctx context.Context, endpoint string, requirementIDs ...string,
) ([]byte, http.Header, int, error) {
	path, err := url.Parse(endpoint)
	if len(endpoint) > 2048 || err != nil || path.IsAbs() || path.Host != "" || path.Fragment != "" || strings.HasPrefix(endpoint, "//") ||
		strings.Contains(path.EscapedPath(), "%") {
		return nil, nil, 0, errors.New("remote Value API endpoint is not a confined relative path")
	}
	segments := strings.Split(strings.TrimPrefix(path.Path, "/"), "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, nil, 0, errors.New("remote Value API endpoint contains an unsafe path segment")
		}
	}
	resource := "core"
	switch {
	case len(segments) >= 3 && segments[0] == "repos" &&
		a.reviewedRepository(segments[1]+"/"+segments[2]):
	case len(segments) == 2 && segments[0] == "search" && segments[1] == "issues":
		queries := path.Query()["q"]
		if len(queries) != 1 || !remoteValueScopedSearch(queries[0], a.repository) {
			return nil, nil, 0, errors.New("remote Value search exceeds its exact repository")
		}
		resource = "search"
	default:
		return nil, nil, 0, errors.New("remote Value API endpoint is outside its enrolled repository")
	}
	requirementID := ""
	if len(requirementIDs) == 1 {
		requirementID = requirementIDs[0]
	}
	cacheKey, cacheable := a.sharedCacheKey(requirementID, resource, "GET\x00"+endpoint)
	if cacheable {
		if cached, ok := a.cache.get(cacheKey); ok {
			a.recordCacheHit()
			return cached.content, cached.headers, cached.status, nil
		}
	}
	reservation, service, err := a.reserve(ctx, resource)
	if err != nil {
		return nil, nil, 0, err
	}
	destination := *a.base
	destination.Path = strings.TrimSuffix(destination.Path, "/") + "/" + strings.TrimPrefix(path.Path, "/")
	destination.RawQuery = path.RawQuery
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, destination.String(), nil)
	if err != nil {
		return nil, nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+a.token)
	request.Header.Set("Accept", "application/vnd.github+json")
	a.mu.Lock()
	a.sent++
	a.mu.Unlock()
	client := a.requestClient()
	response, err := client.Do(request)
	if err != nil {
		// An unknown transport outcome consumes the reservation until its TTL.
		return nil, nil, 0, errors.New("remote Value API transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	a.recordBytes(len(content))
	if err != nil || len(content) > 16<<20 {
		return nil, nil, 0, errors.New("remote Value API response exceeded its byte budget")
	}
	quota := githubquota.ParseResponse(response.Header, response.StatusCode, time.Now())
	if !quota.HasObservation || quota.Resource != resource {
		// Missing or mismatched metadata is not evidence that the reserved
		// request was free. Hold its capacity until expiry and fail closed.
		return nil, nil, 0, errors.New("remote Value API response lacks authoritative resource quota")
	}
	quota.Observation.ObservedAt = time.Time{}
	if err := service.Commit(ctx, reservation, quota.Observation); err != nil {
		return nil, nil, 0, err
	}
	if !quota.ParkUntil.IsZero() {
		if err := service.Park(ctx, reservation.Bucket, quota.ParkUntil, quota.ParkReason); err != nil {
			return nil, nil, 0, err
		}
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if response.StatusCode == http.StatusMovedPermanently && len(segments) >= 3 &&
			!strings.EqualFold(segments[1]+"/"+segments[2], a.repository) {
			// A historical alias may redirect to its current repository.
			// Follow only the same API host and exact registered repository,
			// reserving a second core request rather than letting http.Client
			// silently follow an unmetered authenticated redirect.
			target, err := url.Parse(response.Header.Get("Location"))
			if err == nil && target.Scheme == a.base.Scheme && target.Host == a.base.Host &&
				target.RawQuery == path.RawQuery && target.User == nil && target.Fragment == "" {
				canonical := strings.TrimPrefix(target.Path, strings.TrimSuffix(a.base.Path, "/")+"/")
				if strings.EqualFold(canonical, "repos/"+a.repository) ||
					strings.HasPrefix(strings.ToLower(canonical), "repos/"+a.repository+"/") {
					return a.request(ctx, canonical, requirementID)
				}
			}
		}
		if !remoteValueArtifactZip(path.Path, a.repository) {
			return nil, nil, 0, errors.New("remote Value API redirect is outside the reviewed artifact download")
		}
		content, err := a.downloadArtifact(ctx, response.Header.Get("Location"))
		if err != nil {
			return nil, nil, 0, err
		}
		headers := http.Header{"Content-Type": []string{"application/zip"}}
		if cacheable {
			a.cache.put(cacheKey, remoteValueCachedResponse{content: content, headers: headers, status: http.StatusOK})
		}
		return content, headers, http.StatusOK, nil
	}
	if cacheable && response.StatusCode < http.StatusInternalServerError {
		headers := http.Header{"Content-Type": []string{response.Header.Get("Content-Type")}}
		a.cache.put(cacheKey, remoteValueCachedResponse{
			content: content, headers: headers, status: response.StatusCode,
		})
	}
	return content, response.Header, response.StatusCode, nil
}

func (a *remoteValueAPI) reviewedRepository(repository string) bool {
	if strings.EqualFold(repository, a.repository) {
		return true
	}
	// This source's frozen history explicitly names the original coordinate
	// as an alias. It is not a general grant to read other repositories.
	return a.repository == "github/gh-aw" && a.collector == ".github/value-collectors/daily-file-diet.mjs" &&
		strings.EqualFold(repository, "githubnext/gh-aw")
}

func remoteValueArtifactZip(endpoint, repository string) bool {
	parts := strings.Split(strings.TrimPrefix(endpoint, "/"), "/")
	if len(parts) != 7 || parts[0] != "repos" || !strings.EqualFold(parts[1]+"/"+parts[2], repository) ||
		parts[3] != "actions" || parts[4] != "artifacts" || parts[6] != "zip" {
		return false
	}
	id, err := strconv.ParseUint(parts[5], 10, 64)
	return err == nil && id > 0
}

func (a *remoteValueAPI) downloadArtifact(ctx context.Context, location string) ([]byte, error) {
	target, err := url.Parse(location)
	if err != nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" ||
		target.Hostname() == "" || path.Clean(target.Path) != target.Path ||
		(target.Port() != "" && target.Port() != "443" &&
			(a.base.Scheme != "http" || target.Hostname() != a.base.Hostname())) {
		return nil, errors.New("remote Value artifact redirect is not an approved HTTPS destination")
	}
	host := strings.ToLower(target.Hostname())
	approved := host == "objects.githubusercontent.com" ||
		strings.HasSuffix(host, ".actions.githubusercontent.com") ||
		strings.HasSuffix(host, ".blob.core.windows.net")
	if a.base.Scheme == "http" && (a.base.Hostname() == "127.0.0.1" || a.base.Hostname() == "localhost") {
		approved = approved || host == a.base.Hostname()
	}
	if !approved {
		return nil, errors.New("remote Value artifact redirect is outside the approved download hosts")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	// No App credential, cookie or forwarding headers are sent to signed
	// artifact storage. Redirect chaining is forbidden even with a custom
	// injected HTTP client.
	client := a.requestClient()
	client.Jar = nil
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("remote Value artifact download failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.ContentLength > 16<<20 {
		return nil, errors.New("remote Value artifact download is unavailable or oversized")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	a.recordBytes(len(content))
	if err != nil || len(content) > 16<<20 {
		return nil, errors.New("remote Value artifact download exceeded its byte budget")
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("remote Value artifact download is unavailable")
	}
	return content, nil
}

func (a *remoteValueAPI) graphQL(ctx context.Context, query string, requirementIDs ...string) ([]byte, error) {
	if !remoteValueGraphQuery(a.repository, query) {
		return nil, errors.New("remote Value GraphQL query exceeds the frozen reviewed scope")
	}
	requirementID := ""
	if len(requirementIDs) == 1 {
		requirementID = requirementIDs[0]
	}
	cacheKey, cacheable := a.sharedCacheKey(requirementID, "graphql", "POST\x00graphql\x00"+query)
	if cacheable {
		if cached, ok := a.cache.get(cacheKey); ok {
			a.recordCacheHit()
			return cached.content, nil
		}
	}
	reservation, service, err := a.reserve(ctx, "graphql")
	if err != nil {
		return nil, err
	}
	// Appending the rateLimit field to the single reviewed query lets GitHub
	// report actual cost independently of the response headers used to commit
	// its shared installation bucket.
	withCost := strings.TrimSpace(query)
	withCost = strings.TrimSuffix(withCost, "}") +
		" rateLimit { cost limit remaining resetAt } }"
	body, err := json.Marshal(map[string]string{"query": withCost})
	if err != nil {
		return nil, err
	}
	destination := *a.base
	if strings.HasSuffix(destination.Path, "/api/v3") {
		destination.Path = strings.TrimSuffix(destination.Path, "/api/v3") + "/api/graphql"
	} else {
		destination.Path = strings.TrimSuffix(destination.Path, "/") + "/graphql"
	}
	destination.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, destination.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+a.token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	a.mu.Lock()
	a.sent++
	a.graphUnknown++
	a.mu.Unlock()
	client := a.requestClient()
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("remote Value GraphQL transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	a.recordBytes(len(content))
	if err != nil || len(content) > 16<<20 {
		return nil, errors.New("remote Value GraphQL response exceeded its byte budget")
	}
	quota := githubquota.ParseResponse(response.Header, response.StatusCode, time.Now())
	if !quota.HasObservation || quota.Resource != "graphql" {
		return nil, errors.New("remote Value GraphQL response lacks authoritative quota")
	}
	var result struct {
		Data struct {
			RateLimit struct {
				Cost      int    `json:"cost"`
				Limit     int    `json:"limit"`
				Remaining int    `json:"remaining"`
				ResetAt   string `json:"resetAt"`
			} `json:"rateLimit"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(content, &result); err != nil {
		return nil, errors.New("remote Value GraphQL cost is unavailable")
	}
	cost := result.Data.RateLimit
	reset, resetErr := time.Parse(time.RFC3339, cost.ResetAt)
	if resetErr != nil || cost.Cost < 1 || cost.Cost > reservation.Amount ||
		cost.Limit != quota.Observation.Limit || cost.Remaining != quota.Observation.Remaining ||
		!reset.Equal(quota.Observation.ResetAt) {
		// If cost is missing, higher than the reserved upper bound, or
		// inconsistent with authoritative headers, retain the reservation.
		return nil, errors.New("remote Value GraphQL actual cost is unverified")
	}
	a.mu.Lock()
	a.graphUnknown--
	a.graphPoints += cost.Cost
	a.mu.Unlock()
	quota.Observation.ObservedAt = time.Time{}
	if err := service.Commit(ctx, reservation, quota.Observation); err != nil {
		return nil, err
	}
	if !quota.ParkUntil.IsZero() {
		if err := service.Park(ctx, reservation.Bucket, quota.ParkUntil, quota.ParkReason); err != nil {
			return nil, err
		}
	}
	if response.StatusCode != http.StatusOK || len(result.Errors) != 0 {
		return nil, errors.New("remote Value GraphQL query failed")
	}
	if cacheable {
		a.cache.put(cacheKey, remoteValueCachedResponse{content: content, status: http.StatusOK})
	}
	return content, nil
}

func remoteValueScopedSearch(expression, repository string) bool {
	scopes := 0
	for _, term := range strings.Fields(strings.ToLower(expression)) {
		if term == "or" || term == "and" || term == "not" ||
			strings.HasPrefix(term, "org:") || strings.HasPrefix(term, "user:") ||
			strings.HasPrefix(term, "-repo:") {
			return false
		}
		if strings.HasPrefix(term, "repo:") {
			if term != "repo:"+repository {
				return false
			}
			scopes++
		}
	}
	return scopes == 1
}

func (a *remoteValueAPI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method == http.MethodPost && request.URL.Path == "/graphql" {
		if request.Header.Get("Content-Type") != "application/json" {
			http.Error(writer, "unsupported remote Value GraphQL body", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, (128<<10)+1))
		if err != nil || len(body) > 128<<10 {
			http.Error(writer, "remote Value GraphQL request exceeds its byte budget", http.StatusForbidden)
			return
		}
		var input struct {
			Query string `json:"query"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(writer, "invalid remote Value GraphQL request", http.StatusForbidden)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			http.Error(writer, "invalid remote Value GraphQL request", http.StatusForbidden)
			return
		}
		content, err := a.graphQL(
			request.Context(), input.Query, request.Header.Get("X-CAO-Value-Acquisition-Requirement"),
		)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(content)
		return
	}
	if request.Method != http.MethodGet || request.URL.Path != "/api" {
		http.Error(writer, "unsupported remote Value API request", http.StatusForbidden)
		return
	}
	endpoint := request.URL.Query().Get("endpoint")
	content, headers, status, err := a.request(
		request.Context(), endpoint, request.Header.Get("X-CAO-Value-Acquisition-Requirement"),
	)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if headers.Get("Content-Type") == "application/zip" {
		writer.Header().Set("Content-Type", "application/zip")
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(content) // #nosec G705 -- only JSON or ZIP with nosniff, never upstream-selected HTML.
}

func (a *remoteValueAPI) listen(ctx context.Context) (string, func(), error) {
	if runtime.GOOS != "linux" {
		return "", nil, errors.New("remote Value authenticated API confinement requires Linux abstract Unix sockets")
	}
	nonce, err := operationToken()
	if err != nil {
		return "", nil, err
	}
	name := "cao-remote-value-" + nonce
	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", "@"+name)
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{Handler: a, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return "@" + name, func() {
		_ = server.Close()
		_ = listener.Close()
	}, nil
}

// The CLI supports one REST GET or the reviewed bounded GraphQL query.
// Other flags, pagination and mutation paths cannot escape host admission.
const remoteValueGH = `#!/usr/bin/env node
const http = require('node:http');
const args = process.argv.slice(2);
const graph = args.length === 4 && args[0] === 'api' && args[1] === 'graphql'
  && args[2] === '-f' && args[3].startsWith('query=');
const rest = args.length === 2 && args[0] === 'api' && /^(?:repos\/|search\/)/.test(args[1]);
if (!graph && !rest) {
  process.stderr.write('remote Value gh API command unsupported\n');
  process.exit(1);
}
const body = graph ? JSON.stringify({query: args[3].slice('query='.length)}) : '';
const requirement = process.env.CAO_VALUE_ACQUISITION_REQUIREMENT || '';
const headers = graph ? {'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body)} : {};
if (/^[a-f0-9]{64}$/.test(requirement)) headers['X-CAO-Value-Acquisition-Requirement'] = requirement;
const request = http.request({socketPath: process.env.CAO_REMOTE_VALUE_SOCKET.replace(/^@/, '\0'),
  path: graph ? '/graphql' : '/api?endpoint=' + encodeURIComponent(args[1]),
  method: graph ? 'POST' : 'GET', timeout: 65000,
  headers}, response => {
  if (response.statusCode < 200 || response.statusCode >= 300) {
    process.stderr.write('remote Value API failed (HTTP ' + response.statusCode + ')\n');
    response.resume();
    process.exitCode = 1;
    return;
  }
  response.pipe(process.stdout);
});
request.on('error', () => { process.stderr.write('remote Value API unavailable\n'); process.exitCode = 1; });
request.on('timeout', () => request.destroy());
request.end(body);
`

func remoteValueAPIBase(raw string) (*url.URL, error) {
	if raw == "" {
		raw = "https://api.github.com"
	}
	base, err := url.Parse(raw)
	if err != nil || base.Host == "" || base.User != nil ||
		(base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost"))) {
		return nil, errors.New("remote Value API base must be HTTPS or test loopback")
	}
	return base, nil
}
