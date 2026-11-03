package collect

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

func TestRemoteValueGraphQueryMatchesFrozenReviewedGenerator(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	generator := filepath.Join(root, ".github", "skills", "add-operational-value", "scripts", "github-evidence.mjs")
	code := `import { pathToFileURL } from 'node:url'; const {graphQuery} = await import(pathToFileURL(process.argv[1]));
process.stdout.write(graphQuery("octo/api", [5, 23]));`
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code, generator) // #nosec G204 -- fixed repository generator, not external input.
	query, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read frozen GraphQL generator: %v: %s", err, query)
	}
	if !remoteValueGraphQuery("octo/api", string(query)) {
		t.Fatal("reviewed bounded graphQuery output was not recognized")
	}
	for _, invalid := range []string{
		strings.Replace(string(query), "octo", "other", 1),
		strings.Replace(string(query), "first:100", "first:1000", 1),
		strings.Replace(string(query), "query {", "mutation {", 1),
		strings.Replace(string(query), "n5: issueOrPullRequest(number:5)", "n5: issueOrPullRequest(number:6)", 1),
		string(query) + " rateLimit { cost }",
	} {
		if remoteValueGraphQuery("octo/api", invalid) {
			t.Fatal("unreviewed GraphQL shape was accepted")
		}
	}
}

func TestRemoteValuePublicationRejectsChangedStartingPacket(t *testing.T) {
	initial := model.Row{"id": "proof", "observedAt": "2026-01-01T00:00:00Z", "facts": []any{"one"}}
	same := model.Row{"facts": []any{"one"}, "id": "proof", "observedAt": "2026-01-01T00:00:00Z"}
	if !remoteValuePacketCurrent(initial, same) || !remoteValuePacketCurrent(nil, nil) {
		t.Fatal("unchanged previous proof was rejected")
	}
	for _, current := range []model.Row{nil,
		{"id": "new-proof", "observedAt": "2026-01-02T00:00:00Z", "facts": []any{"one"}},
		{"id": "proof", "observedAt": "2026-01-01T00:00:00Z", "facts": []any{"different"}},
	} {
		if remoteValuePacketCurrent(initial, current) {
			t.Fatalf("stale collector proof was accepted: %v", current)
		}
	}
	if remoteValuePacketCurrent(nil, initial) {
		t.Fatal("concurrently published initial proof was accepted")
	}
}

func TestRemoteValueFailureUsesOnlyBoundedWorkerDiagnostic(t *testing.T) {
	for _, testCase := range []struct {
		name, stderr, reason, message string
	}{
		{
			name: "timeout",
			stderr: "private command output\n" + remoteValueFailurePrefix +
				`{"reasonCode":"timeout","detail":"collector command timed out (SIGTERM)"}` + "\n",
			reason: "timeout", message: "remote Value collector command timed out; previous evidence retained",
		},
		{
			name: "api budget",
			stderr: remoteValueFailurePrefix +
				`{"reasonCode":"api-budget","detail":"collector exhausted its reviewed API request grant"}` + "\n",
			reason:  "api-budget",
			message: "remote Value collector exhausted its reviewed API request grant; previous evidence retained",
		},
		{
			name:   "malformed marker",
			stderr: remoteValueFailurePrefix + `{"reasonCode":"secret-output"}` + "\nprivate command output\n",
			reason: "collector-failed", message: "remote Value collector failed; previous evidence retained",
		},
		{
			name:   "raw stderr",
			stderr: "private command output\n",
			reason: "collector-failed", message: "remote Value collector failed; previous evidence retained",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reason := remoteValueFailureReason([]byte(testCase.stderr))
			if reason != testCase.reason {
				t.Fatalf("reason = %q, want %q", reason, testCase.reason)
			}
			message := remoteValueFailureError(reason).Error()
			if message != testCase.message || strings.Contains(message, "private command output") {
				t.Fatalf("safe diagnostic = %q, want %q", message, testCase.message)
			}
		})
	}
}

func TestRemoteValueRepositoriesReviewedPolicyCacheAndEnrollment(t *testing.T) {
	b := volatileBackfill(t)
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "cao.json")) // #nosec G304 -- fixed checked-in policy fixture.
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(path, policy, 0o600); err != nil { // #nosec G703 -- fixed filename beneath t.TempDir.
		t.Fatal(err)
	}
	projector := Projector{CatalogRoot: root, PolicyPath: path, Enrollment: b.Enrollment,
		RemoteValuePlan: &RemoteValuePlanCache{}}
	if names, err := projector.RemoteValueRepositories(t.Context()); err != nil || len(names) != 0 {
		t.Fatalf("unenrolled registration was admitted: %v, %v", names, err)
	}
	if err := b.Enrollment.AddRepositories(t.Context(), 7, []string{"github/gh-aw"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		names, err := projector.RemoteValueRepositories(t.Context())
		if err != nil || !slices.Equal(names, []string{"github/gh-aw"}) {
			t.Fatalf("reviewed registered repository = %v, %v", names, err)
		}
	}
	if !projector.RemoteValuePlan.loaded || len(projector.RemoteValuePlan.entries) != 1 ||
		projector.RemoteValuePlan.entries[0].APIRequests != 700 {
		t.Fatal("reviewed API-bound registration was not cached")
	}
	disabled := strings.Replace(string(policy), `"api-requests": 700,`, `"api-requests": 700, "enabled": false,`, 1)
	if disabled == string(policy) {
		t.Fatal("fixture registration not found")
	}
	if err := os.WriteFile(path, []byte(disabled), 0o600); err != nil { // #nosec G703 -- fixed filename beneath t.TempDir.
		t.Fatal(err)
	}
	if names, err := projector.RemoteValueRepositories(t.Context()); err != nil || len(names) != 0 {
		t.Fatalf("stale policy cache admitted disabled registration: %v, %v", names, err)
	}
}

func TestRemoteValueTargetsRoundRobinInstallations(t *testing.T) {
	got := roundRobinRemoteValueTargets([]remoteValueTarget{
		{Repository: "octo/a", Installation: 1},
		{Repository: "octo/b", Installation: 1},
		{Repository: "hub/c", Installation: 2},
		{Repository: "hub/d", Installation: 2},
		{Repository: "solo/e", Installation: 3},
	})
	want := []remoteValueTarget{
		{Repository: "octo/a", Installation: 1},
		{Repository: "hub/c", Installation: 2},
		{Repository: "solo/e", Installation: 3},
		{Repository: "octo/b", Installation: 1},
		{Repository: "hub/d", Installation: 2},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("round robin targets=%+v", got)
	}
}

func TestRemoteValueAcquisitionPlanSharesOnlyReviewedCommonGitHubRequirements(t *testing.T) {
	shared := remoteValueRequirement{
		Source: "github", Pool: "core", Primitive: "github.repository-metadata.v1",
		Scope: "repository", Cache: "invocation",
	}
	plan := compileRemoteValueAcquisitionPlan([]remoteValueEntry{
		{Workflow: "one", Acquisition: &remoteValueAcquisition{Requirements: []remoteValueRequirement{
			shared,
			{Source: "git", Pool: "git", Primitive: "git.commit-inventory.v1", Scope: "source", Cache: "immutable"},
		}}},
		{Workflow: "two", Acquisition: &remoteValueAcquisition{Requirements: []remoteValueRequirement{shared}}},
		{Workflow: "legacy"},
	})
	id := remoteValueRequirementID(shared)
	if id != "f2ef7a7b2549d6403c6be2b24bdb1cd38bc71c94589f1bf8c09ebc0c49c7acb5" {
		t.Fatalf("cross-runtime requirement ID = %q", id)
	}
	if len(plan.Requirements) != 2 || len(plan.Shared["one"]) != 1 || len(plan.Shared["two"]) != 1 ||
		plan.Shared["one"][id] != shared || plan.Shared["two"][id] != shared ||
		len(plan.Shared["legacy"]) != 0 {
		t.Fatalf("compiled acquisition plan = %+v", plan)
	}
}

func TestRemoteValueAPIEnforcesEveryResourceAndRequestCap(t *testing.T) {
	b := volatileBackfill(t)
	backend := b.Enrollment.Metadata.(operational.GitHubQuotaStore)
	core, err := githubquota.New(backend, githubquota.Options{SafetyReserve: 1000})
	if err != nil {
		t.Fatal(err)
	}

	search, err := githubquota.New(backend, githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).Unix()
	for _, sample := range []struct {
		resource         string
		limit, remaining int
	}{
		{"core", 5000, 4000}, {"search", 30, 20},
	} {
		if err := core.Observe(t.Context(), githubquota.BucketID{App: "github-app-1", Installation: 7, Resource: sample.resource},
			githubquota.Observation{Limit: sample.limit, Remaining: sample.remaining, ResetAt: time.Unix(reset, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.String())
		if request.Header.Get("Authorization") != "Bearer private-installation-token" {
			t.Error("App token did not remain host-side")
		}
		resource, remaining := "core", "3999"
		if strings.HasPrefix(request.URL.Path, "/search/") {
			resource, remaining = "search", "19"
		}
		writer.Header().Set("X-RateLimit-Resource", resource)
		writer.Header().Set("X-RateLimit-Limit", map[string]string{"core": "5000", "search": "30"}[resource])
		writer.Header().Set("X-RateLimit-Remaining", remaining)
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		writer.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	defer upstream.Close()
	base, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := &remoteValueAPI{client: upstream.Client(), quota: core, searchQuota: search,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-installation-token", max: 2}
	for _, endpoint := range []string{
		"repos/other/repo", "repos/octo/api/../../other", "search/issues?q=repo:other/repo",
		"search/issues?q=repo:octo/api+OR+repo:other/repo",
		"graphql", "https://api.github.com/repos/octo/api",
	} {
		if _, _, _, err := api.request(t.Context(), endpoint); err == nil {
			t.Errorf("unapproved API endpoint %q was forwarded", endpoint)
		}
	}
	if len(requests) != 0 {
		t.Fatalf("rejected endpoint reached GitHub: %v", requests)
	}
	for _, endpoint := range []string{
		"repos/octo/api/actions/runs/42", "search/issues?q=repo:octo/api+label:bug",
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api?endpoint="+url.QueryEscape(endpoint), nil)
		api.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != `{"ok":true}` {
			t.Errorf("reviewed endpoint %q: %d %s", endpoint, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Type") != "application/json" ||
			response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("API bridge allowed upstream content to be interpreted as HTML")
		}
	}
	if _, _, _, err := api.request(t.Context(), "repos/octo/api/issues"); err == nil {
		t.Fatal("reviewed request cap did not stop third API call")
	}
	if len(requests) != 2 {
		t.Fatalf("expected exactly two metered upstream calls, got %v", requests)
	}
	if total, coreRequests, searchRequests, graphQLRequests, _, _, _, _, _ := api.costUsage(); total != 2 || coreRequests != 1 || searchRequests != 1 || graphQLRequests != 0 {
		t.Fatalf("request pools were not retained: total=%d core=%d search=%d graphql=%d",
			total, coreRequests, searchRequests, graphQLRequests)
	}
}

func TestRemoteValueAPICoalescesExactReadsAcrossReviewedCollectors(t *testing.T) {
	b := volatileBackfill(t)
	quota, err := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1000})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := quota.Observe(t.Context(), githubquota.BucketID{App: "github-app-1", Installation: 7},
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.Header().Set("X-RateLimit-Resource", "core")
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(4000-calls))
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"full_name":"octo/api"}`)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	cache := newRemoteValueResponseCache()
	requirement := remoteValueRequirement{
		Source: "github", Pool: "core", Primitive: "github.repository-metadata.v1",
		Scope: "repository", Cache: "invocation",
	}
	requirementID := remoteValueRequirementID(requirement)
	shared := map[string]remoteValueRequirement{requirementID: requirement}
	first := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-token", max: 1, cache: cache, sharedRequirements: shared}
	second := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-token", max: 1, cache: cache, sharedRequirements: shared}
	for _, api := range []*remoteValueAPI{first, second} {
		content, _, status, err := api.request(t.Context(), "repos/octo/api", requirementID)
		if err != nil || status != http.StatusOK || string(content) != `{"full_name":"octo/api"}` {
			t.Fatalf("shared request failed: status=%d content=%s err=%v", status, content, err)
		}
	}
	if calls != 1 {
		t.Fatalf("exact shared read reached GitHub %d times", calls)
	}
	if sent, _ := first.usage(); sent != 1 {
		t.Fatalf("first collector metered requests = %d", sent)
	}
	if sent, _ := second.usage(); sent != 0 {
		t.Fatalf("cached collector metered requests = %d", sent)
	}
	if _, _, _, _, hits, _, _, _, _ := second.costUsage(); hits != 1 {
		t.Fatalf("cached collector did not retain its cache hit: %d", hits)
	}
	other := remoteValueRequirement{
		Source: "github", Pool: "core", Primitive: "github.pull-metadata-files.v1",
		Scope: "outputs", Cache: "invocation",
	}
	otherID := remoteValueRequirementID(other)
	isolated := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-token", max: 1, cache: cache,
		sharedRequirements: map[string]remoteValueRequirement{otherID: other}}
	if _, _, _, err := isolated.request(t.Context(), "repos/octo/api", otherID); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("different reviewed requirements shared one response: calls=%d", calls)
	}
}

func TestRemoteValueReviewedHistoricalAliasAndRedirectEachUseCoreQuota(t *testing.T) {
	b := volatileBackfill(t)
	quota, err := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1000})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := quota.Observe(t.Context(), githubquota.BucketID{App: "github-app-1", Installation: 7},
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var origin string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.Header().Set("X-RateLimit-Resource", "core")
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(4000-calls))
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		if request.URL.Path == "/repos/githubnext/gh-aw" {
			writer.Header().Set("Location", origin+"/repos/github/gh-aw")
			writer.WriteHeader(http.StatusMovedPermanently)
			return
		}
		_, _ = io.WriteString(writer, `{"full_name":"github/gh-aw"}`)
	}))
	defer upstream.Close()
	origin = upstream.URL
	base, _ := url.Parse(upstream.URL)
	api := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "github/gh-aw",
		collector: ".github/value-collectors/daily-file-diet.mjs",
		base:      base, token: "private-token", max: 2}
	if _, _, _, err := api.request(t.Context(), "repos/githubnext/gh-aw"); err != nil || calls != 2 {
		t.Fatalf("frozen alias redirect did not reserve both requests: calls=%d err=%v", calls, err)
	}
	if _, _, _, err := api.request(t.Context(), "repos/githubnext/other"); err == nil {
		t.Fatal("unreviewed alias was granted repository access")
	}
	api.collector = "other-collector.mjs"
	if _, _, _, err := api.request(t.Context(), "repos/githubnext/gh-aw"); err == nil {
		t.Fatal("historical alias escaped its exact reviewed collector")
	}
}

func TestRemoteValueAPIUnknownQuotaFailsClosed(t *testing.T) {
	b := volatileBackfill(t)
	quota, err := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://api.github.com")
	api := &remoteValueAPI{client: http.DefaultClient, quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api", base: base, token: "secret", max: 1}
	_, _, _, err = api.request(t.Context(), "repos/octo/api")
	if !errors.Is(err, githubquota.ErrUnknown) {
		t.Fatalf("unknown shared quota was not denied: %v", err)
	}
}

func TestRemoteValueSearchBootstrapsIndependentSharedBucket(t *testing.T) {
	b := volatileBackfill(t)
	store := b.Enrollment.Metadata.(operational.GitHubQuotaStore)
	core, _ := githubquota.New(store, githubquota.Options{SafetyReserve: 1000})
	search, _ := githubquota.New(store, githubquota.Options{SafetyReserve: 1})
	reset := time.Now().Add(time.Hour).Unix()
	if err := core.Observe(t.Context(), githubquota.BucketID{App: "github-app-1", Installation: 7},
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: time.Unix(reset, 0)}); err != nil {
		t.Fatal(err)
	}
	var endpoints []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		endpoints = append(endpoints, request.URL.Path)
		resource, limit, remaining := "core", "5000", "3999"
		if request.URL.Path == "/search/issues" {
			resource, limit, remaining = "search", "30", "18"
		}
		writer.Header().Set("X-RateLimit-Resource", resource)
		writer.Header().Set("X-RateLimit-Limit", limit)
		writer.Header().Set("X-RateLimit-Remaining", remaining)
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		if request.URL.Path == "/rate_limit" {
			_, _ = io.WriteString(writer, `{"resources":{"search":{"limit":30,"remaining":19,"reset":`+
				strconv.FormatInt(reset, 10)+`}}}`)
		} else {
			_, _ = io.WriteString(writer, `{"items":[]}`)
		}
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	api := &remoteValueAPI{client: upstream.Client(), quota: core, searchQuota: search,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-token", max: 2}
	if _, _, _, err := api.request(t.Context(), "search/issues?q=repo:octo/api+label:bug"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(endpoints, []string{"/rate_limit", "/search/issues"}) {
		t.Fatalf("search was not observed independently from core: %v", endpoints)
	}
	if used, _ := api.usage(); used != 2 {
		t.Fatalf("unmetered quota bootstrap: %d requests", used)
	}
}

func TestRemoteValueGraphQLReservesUpperBoundCommitsObservedCost(t *testing.T) {
	b := volatileBackfill(t)
	quota, err := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1000})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := quota.Observe(t.Context(), githubquota.BucketID{App: "github-app-1", Installation: 7},
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Header.Get("Authorization") != "Bearer private-installation-token" {
			t.Error("GraphQL installation credential escaped the Go proxy")
		}
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		if request.URL.Path == "/rate_limit" {
			writer.Header().Set("X-RateLimit-Resource", "core")
			writer.Header().Set("X-RateLimit-Remaining", "3999")
			_, _ = io.WriteString(writer, `{"resources":{"graphql":{"limit":5000,"remaining":4000,"reset":`+
				strconv.FormatInt(reset.Unix(), 10)+`}}}`)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/graphql" {
			t.Errorf("unexpected GraphQL request: %s %s", request.Method, request.URL.Path)
		}
		var payload struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil ||
			!strings.Contains(payload.Query, " rateLimit { cost limit remaining resetAt } }") {
			t.Errorf("GraphQL cost was not requested alongside reviewed query: %v", err)
		}
		writer.Header().Set("X-RateLimit-Resource", "graphql")
		writer.Header().Set("X-RateLimit-Remaining", "3997")
		_, _ = io.WriteString(writer, `{"data":{"repository":{},"rateLimit":{"cost":3,"remaining":3997,"limit":5000,"resetAt":"`+
			reset.UTC().Format(time.RFC3339)+`"}}}`)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	requirement := remoteValueRequirement{
		Source: "github", Pool: "graphql", Primitive: "github.issue-pull-relationships.v1",
		Scope: "outputs", Cache: "invocation",
	}
	requirementID := remoteValueRequirementID(requirement)
	shared := map[string]remoteValueRequirement{requirementID: requirement}
	cache := newRemoteValueResponseCache()
	api := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-installation-token", max: 2,
		cache: cache, sharedRequirements: shared}
	query := remoteValueExpectedGraphQuery("octo/api", []int{5})
	body, err := api.graphQL(t.Context(), query, requirementID)
	if err != nil || !strings.Contains(string(body), `"cost":3`) {
		t.Fatalf("GraphQL result: %v %s", err, body)
	}
	if !slices.Equal(paths, []string{"/rate_limit", "/graphql"}) {
		t.Fatalf("GraphQL requests bypassed shared quota preflight: %v", paths)
	}
	state, err := quota.State(t.Context(), githubquota.BucketID{App: "github-app-1", Installation: 7, Resource: "graphql"})
	if err != nil || state.Remaining != 3997 || state.Reserved != 0 {
		t.Fatalf("actual GraphQL point cost was not committed: %+v %v", state, err)
	}
	if sent, _ := api.usage(); sent != 2 {
		t.Fatalf("GraphQL preflight cost was not accounted: %d", sent)
	}
	if total, coreRequests, _, graphQLRequests, _, _, points, unverified, _ := api.costUsage(); total != 2 || coreRequests != 1 || graphQLRequests != 1 || points != 3 || unverified != 0 {
		t.Fatalf("actual GraphQL cost was not retained: total=%d core=%d graphql=%d points=%d unverified=%d",
			total, coreRequests, graphQLRequests, points, unverified)
	}
	second := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-installation-token", max: 1,
		cache: cache, sharedRequirements: shared}
	if _, err := second.graphQL(t.Context(), query, requirementID); err != nil {
		t.Fatalf("reviewed GraphQL cache hit failed: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("reviewed GraphQL cache hit reached upstream: %v", paths)
	}
	if sent, _ := second.usage(); sent != 0 {
		t.Fatalf("reviewed GraphQL cache hit consumed request grant: %d", sent)
	}
	if runtime.GOOS == "linux" {
		api.max = 3
		socket, closeAPI, err := api.listen(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer closeAPI()
		script := filepath.Join(t.TempDir(), "gh")
		if err := os.WriteFile(script, []byte(remoteValueGH), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), "node", script, "api", "graphql", "-f", "query="+query) // #nosec G204 -- fixed bridge script and generated test query.
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "GH_TOKEN=remote-value-proxy-only",
			"CAO_REMOTE_VALUE_SOCKET=" + socket}
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), `"cost":3`) || len(paths) != 3 {
			t.Fatalf("GraphQL gh wrapper bypassed metered host request: %v %s paths=%v", err, output, paths)
		}
		command = exec.CommandContext(t.Context(), "node", script, "api", "graphql", "-f", "query="+query) // #nosec G204 -- fixed bridge script and generated test query.
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "GH_TOKEN=remote-value-proxy-only",
			"CAO_REMOTE_VALUE_SOCKET=" + socket, "CAO_VALUE_ACQUISITION_REQUIREMENT=" + requirementID}
		output, err = command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), `"cost":3`) || len(paths) != 3 {
			t.Fatalf("reviewed GraphQL gh wrapper cache miss: %v %s paths=%v", err, output, paths)
		}
	}
}

func TestRemoteValueGraphQLUnknownOrUnverifiableCostFailsClosed(t *testing.T) {
	b := volatileBackfill(t)
	quota, err := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1000})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	bucket := githubquota.BucketID{App: "github-app-1", Installation: 7, Resource: "graphql"}
	base, _ := url.Parse("https://api.github.com")
	api := &remoteValueAPI{client: http.DefaultClient, quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api", base: base, max: 10}
	query := remoteValueExpectedGraphQuery("octo/api", []int{5})
	if _, err := api.graphQL(t.Context(), query); !errors.Is(err, githubquota.ErrUnknown) {
		t.Fatalf("unknown core preflight quota reached upstream: %v", err)
	}
	if err := quota.Observe(t.Context(), bucket,
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	for _, response := range []string{
		`{"data":{"repository":{},"rateLimit":{"cost":501,"remaining":3997,"limit":5000,"resetAt":"` +
			reset.UTC().Format(time.RFC3339) + `"}}}`,
		`{"data":{"repository":{},"rateLimit":{"remaining":3997,"limit":5000,"resetAt":"` +
			reset.UTC().Format(time.RFC3339) + `"}}}`,
	} {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-RateLimit-Resource", "graphql")
			writer.Header().Set("X-RateLimit-Limit", "5000")
			writer.Header().Set("X-RateLimit-Remaining", "3997")
			writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			_, _ = io.WriteString(writer, response)
		}))
		api.base, _ = url.Parse(upstream.URL)
		api.client = upstream.Client()
		if _, err := api.graphQL(t.Context(), query); err == nil {
			t.Fatal("GraphQL unverifiable cost was accepted")
		}
		upstream.Close()
	}
	state, err := quota.State(t.Context(), bucket)
	if err != nil || state.Reserved != 2*remoteValueGraphPoints {
		t.Fatalf("unverifiable cost released a shared reservation: %+v %v", state, err)
	}
	if _, _, _, _, _, _, points, unverified, _ := api.costUsage(); points != 0 || unverified != 2 {
		t.Fatalf("unknown GraphQL point cost was reported as zero: points=%d unverified=%d", points, unverified)
	}
}

func TestRemoteValueArtifactRedirectKeepsInstallationTokenAtAPIHost(t *testing.T) {
	b := volatileBackfill(t)
	quota, err := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1000})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	bucket := githubquota.BucketID{App: "github-app-1", Installation: 7}
	if err := quota.Observe(t.Context(), bucket,
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	artifactCalls := 0
	artifact := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		artifactCalls++
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Error("App credential or cookie leaked into signed artifact download")
		}
		_, _ = io.WriteString(writer, "PK-artifact")
	}))
	defer artifact.Close()
	apiCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		apiCalls++
		if request.Header.Get("Authorization") != "Bearer private-installation-token" {
			t.Error("artifact API request lacked host-only installation credential")
		}
		writer.Header().Set("X-RateLimit-Resource", "core")
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", "3999")
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		writer.Header().Set("Location", artifact.URL+"/signed?signature=private")
		writer.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	api := &remoteValueAPI{client: artifact.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-installation-token", max: 1}
	content, _, status, err := api.request(t.Context(), "repos/octo/api/actions/artifacts/42/zip")
	if err != nil || status != http.StatusOK || string(content) != "PK-artifact" ||
		apiCalls != 1 || artifactCalls != 1 {
		t.Fatalf("signed artifact redirect: status=%d upstream=%d signed=%d err=%v",
			status, apiCalls, artifactCalls, err)
	}
	state, err := quota.State(t.Context(), bucket)
	if err != nil || state.Remaining != 3999 || state.Reserved != 0 {
		t.Fatalf("artifact request was not reconciled: %+v %v", state, err)
	}
	if sent, received := api.usage(); sent != 1 || received < int64(len(content)) {
		t.Fatalf("artifact costs omitted API call or transfer: calls=%d bytes=%d", sent, received)
	}
}

func TestRemoteValueArtifactRedirectRejectsOtherDestinations(t *testing.T) {
	b := volatileBackfill(t)
	quota, _ := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1})
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	bucket := githubquota.BucketID{App: "github-app-1", Installation: 7}
	if err := quota.Observe(t.Context(), bucket,
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	attempted := 0
	for _, destination := range []string{
		"http://127.0.0.1/internal", "https://example.invalid/signed",
		"https://127.0.0.1:443/internal/../admin",
	} {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			attempted++
			writer.Header().Set("X-RateLimit-Resource", "core")
			writer.Header().Set("X-RateLimit-Limit", "5000")
			writer.Header().Set("X-RateLimit-Remaining", "3999")
			writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			writer.Header().Set("Location", destination)
			writer.WriteHeader(http.StatusFound)
		}))
		base, _ := url.Parse(upstream.URL)
		api := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
			app: "github-app-1", installation: 7, repository: "octo/api",
			base: base, token: "private-installation-token", max: 1}
		if _, _, _, err := api.request(t.Context(), "repos/octo/api/actions/artifacts/42/zip"); err == nil {
			t.Errorf("unapproved signed artifact destination %q was followed", destination)
		}
		upstream.Close()
	}
	if attempted != 3 {
		t.Fatalf("only initial governed API requests may be attempted, got %d", attempted)
	}
}

func TestRemoteValueGHWrapperRejectsGraphQLWithoutToken(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "gh")
	if err := os.WriteFile(script, []byte(remoteValueGH), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", script, "api", "graphql", "-f", "query=query{}") // #nosec G204 -- fixed bridge script beneath t.TempDir.
	command.Env = []string{"PATH=" + os.Getenv("PATH"),
		"CAO_REMOTE_VALUE_SOCKET=" + filepath.Join(directory, "missing.sock")}
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "remote Value API unavailable") {
		t.Fatalf("GraphQL request bypassed the unavailable quota proxy: %v %s", err, output)
	}
}

func TestRemoteValueGHWrapperUsesHostQuotaAndKeepsTokenPrivate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("remote Value authenticated API confinement requires Linux")
	}
	b := volatileBackfill(t)
	quota, err := githubquota.New(b.Enrollment.Metadata.(operational.GitHubQuotaStore),
		githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour)
	if err := quota.Observe(t.Context(), githubquota.BucketID{App: "github-app-1", Installation: 7},
		githubquota.Observation{Limit: 5000, Remaining: 4000, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.Header.Get("Authorization") != "Bearer private-installation-token" {
			t.Error("App credential was not used exclusively by the Go proxy")
		}
		writer.Header().Set("X-RateLimit-Resource", "core")
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", "3999")
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		_, _ = io.WriteString(writer, `{"id":42}`)
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	api := &remoteValueAPI{client: upstream.Client(), quota: quota, searchQuota: quota,
		app: "github-app-1", installation: 7, repository: "octo/api",
		base: base, token: "private-installation-token", max: 1}
	directory := t.TempDir()
	socket, closeAPI, err := api.listen(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer closeAPI()
	script := filepath.Join(directory, "gh")
	if err := os.WriteFile(script, []byte(remoteValueGH), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", script, "api", "repos/octo/api") // #nosec G204 -- fixed bridge script beneath t.TempDir.
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GH_TOKEN=remote-value-proxy-only",
		"CAO_REMOTE_VALUE_SOCKET=" + socket}
	content, err := command.CombinedOutput()
	if err != nil || string(content) != `{"id":42}` || calls != 1 {
		t.Fatalf("confined request: output=%s calls=%d err=%v", content, calls, err)
	}
	denied := exec.CommandContext(t.Context(), "node", script, "api", "repos/octo/api") // #nosec G204 -- fixed bridge script beneath t.TempDir.
	denied.Env = command.Env
	content, err = denied.CombinedOutput()
	if err == nil || calls != 1 {
		t.Fatalf("request cap bypassed: output=%s calls=%d err=%v", content, calls, err)
	}
}

func TestRemoteValueDefersWithoutHostedServices(t *testing.T) {
	err := (Projector{}).CollectRemoteValues(t.Context(), "octo/api")
	if !errors.Is(err, ErrRemoteValueDeferred) {
		t.Fatalf("without Postgres or App quota must defer, got %v", err)
	}
}

func TestRemoteValueWorkspaceWithdrawn(t *testing.T) {
	projector := Projector{Lake: Lake{Directory: t.TempDir()}}
	root, err := projector.remoteValueWorkspace(t.Context(), "octo/api", "my-workflow")
	if err != nil {
		t.Fatal(err)
	}
	if err := projector.ForgetRemoteValueWorkspace(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("withdrawn private proof remained: %v", err)
	}
}

func TestRemoteValueWorkspaceRejectsUnconfiguredOrRelativeLake(t *testing.T) {
	for _, directory := range []string{"", ".", "relative/lake"} {
		projector := Projector{Lake: Lake{Directory: directory}}
		if _, err := projector.remoteValueWorkspace(t.Context(), "octo/api", "my-workflow"); err == nil {
			t.Errorf("workspace accepted unsafe lake directory %q", directory)
		}
		if err := projector.ForgetRemoteValueWorkspace(t.Context(), "octo/api"); err == nil {
			t.Errorf("withdrawal scanned unsafe lake directory %q", directory)
		}
	}
}

func TestRemoteValueProofReadConfinesFilesAndByteBudget(t *testing.T) {
	for _, scenario := range []string{"nested", "oversized", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			source := t.TempDir()
			if err := os.Mkdir(filepath.Join(source, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "nested", "proof"), []byte("proof"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "oversized":
				if err := os.WriteFile(filepath.Join(source, "large"), make([]byte, postgresx.ValueProofFileMax+1), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(source, "escape")); err != nil {
					t.Fatal(err)
				}
			}
			files, err := readRemoteValueProof(source)
			if scenario != "nested" {
				if err == nil {
					t.Fatal("unsafe or oversized proof was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 || files[0].Path != "nested/proof" || string(files[0].Content) != "proof" {
				t.Fatalf("nested proof was not preserved: %+v", files)
			}
		})
	}
}
