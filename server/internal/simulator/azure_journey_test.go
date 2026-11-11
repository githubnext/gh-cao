package simulator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestCAOAzureInstallJourneyIntegration(t *testing.T) {
	testCAOSetupAzureIntegration(t, "private farm journey")
}

// Replays the private hosted branch of bradygaster/gh-aw-cao#26. Human
// approvals are fixture inputs, not authority to contact either cloud.
func setupAzurePrivateFarm(t *testing.T, ctx context.Context, f *setupFixture, catalog, binary string) {
	t.Helper()
	assets := bootstrapReleaseAssets(t, binary)
	f.api.mu.Lock()
	f.api.Assets = assets
	for _, repository := range f.api.Repositories {
		repository.Private = true
	}
	f.api.mu.Unlock()
	for _, item := range f.env {
		if data, ok := strings.CutPrefix(item, "XDG_DATA_HOME="); ok {
			if err := os.Remove(filepath.Join(data, "gh/extensions/gh-cao/gh-cao")); err != nil {
				t.Fatal(err)
			}
		}
	}
	installerEnv := setupAzureEnvironment(f.env, map[string]string{"PATH": os.Getenv("PATH")})
	bootstrapRun(t, ctx, f.checkout, installerEnv, "bash", filepath.Join(catalog, "install-gh-cao.sh"), catalog)
	for _, name := range []string{"activity/cao.mjs", ".github/workflows/cao-activity.yml", ".github/workflows/cao-validate.yml"} {
		if len(setupRead(t, filepath.Join(f.checkout, name))) == 0 {
			t.Fatalf("installer omitted %s", name)
		}
	}
	// Hosted setup uses reviewed policy, not the public monitoring shortcut.
	f.control()["scope"] = map[string]any{"allowed-owners": []string{"platform"},
		"allowed-repositories": []string{"platform/control", "platform/alpha", "platform/zeta"}}
	f.control()["activity"] = map[string]any{"repositories": []string{"platform/control", "platform/alpha", "platform/zeta"},
		"public-only": false}
	setupSaveFixturePolicy(t, f)
	output := bootstrapRun(t, ctx, f.checkout, f.env, "gh", "cao", "setup", "auth", "github-app", "--dry-run", "--no-open")
	var apps map[string]any
	if err := json.Unmarshal([]byte(output), &apps); err != nil {
		t.Fatal(err)
	}
	setupAssertJSON(t, apps["installationTargets"], map[string]any{
		"read":  []map[string]any{{"owner": "platform", "repositories": []string{"alpha", "control", "zeta"}}},
		"write": []map[string]any{{"owner": "platform", "repositories": []string{"control"}}},
	})
	setupAssertGitHubAppPlans(t, apps, false, "", "", "github.com")
	// Model the owner completing selected-repository installation and storing
	// PEMs outside the agent. Secret-name presence is not working-key proof.
	f.api.mu.Lock()
	f.api.Repositories["platform/control"].Secrets = []string{"GH_AW_GITHUB_READ_APP_PRIVATE_KEY", "GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY"}
	f.api.mu.Unlock()
	for name, value := range map[string]string{
		"GH_AW_GITHUB_READ_APP_ID": "1001", "GH_AW_GITHUB_WRITE_APP_ID": "1002", "GH_AW_GITHUB_AUTH_MODE": "app",
	} {
		bootstrapRun(t, ctx, f.checkout, f.env, "gh", "variable", "set", name, "--body", value)
		if got := strings.TrimSpace(bootstrapRun(t, ctx, f.checkout, f.env, "gh", "variable", "get", name)); got != value {
			t.Fatalf("App variable %s was not configured on the control repository", name)
		}
	}
	remote := filepath.Join(f.root, "published-control.git")
	bootstrapRun(t, ctx, f.root, f.env, "git", "init", "--bare", "--quiet", "--initial-branch=main", remote)
	bootstrapRun(t, ctx, f.checkout, f.env, "git", "add", ".github", "activity")
	bootstrapRun(t, ctx, f.checkout, f.env, "git", "-c", "user.name=CAO simulator", "-c", "user.email=simulator@example.invalid",
		"commit", "-qm", "Publish reviewed private control plane")
	bootstrapRun(t, ctx, f.checkout, f.env, "git", "push", remote, "HEAD:main")
	published := bootstrapRun(t, ctx, f.root, f.env, "git", "--git-dir="+remote, "show", "main:.github/workflows/cao.json")
	if !bytes.Equal(bytes.TrimSpace([]byte(published)), bytes.TrimSpace(setupRead(t, f.policyPath()))) {
		t.Fatal("published policy differs from reviewed private farm authority")
	}
	f.api.mu.Lock()
	defer f.api.mu.Unlock()
	if len(f.api.Unhandled) != 0 {
		t.Fatalf("installer used unsupported GitHub routes: %v", f.api.Unhandled)
	}
	for _, request := range f.api.Requests {
		if strings.Contains(request, "/pages") {
			t.Fatalf("private hosted journey contacted Pages: %s", request)
		}
	}
}

func setupAzureFarmSnapshot(t *testing.T, attempts int) string {
	t.Helper()
	directory := t.TempDir()
	var shard bytes.Buffer
	encoder := json.NewEncoder(&shard)
	write := func(value any) {
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	write(map[string]any{"kind": "metadata", "schemaVersion": model.SchemaVersion, "ingestionVersion": 3, "phase": "runs", "records": 5 + 2*attempts})
	record := func(collection string, row model.Row) {
		write(map[string]any{"kind": "record", "collection": collection, "record": row})
	}
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	for _, name := range []string{"control", "alpha", "zeta"} {
		record("repositories", model.Row{"id": "repository:platform/" + name, "owner": "platform", "name": name,
			"visibility": "private", "rolloutMode": "review", "observedAt": now})
	}
	for _, name := range []string{"alpha", "zeta"} {
		record("workflows", model.Row{"id": "workflow:platform/" + name + ":build", "repositoryId": "repository:platform/" + name,
			"path": ".github/workflows/build.yml", "name": "Build", "state": "active", "observedAt": now})
		for attempt := 1; attempt <= attempts; attempt++ {
			record("runs", model.Row{"id": fmt.Sprintf("run:%s-%d", name, attempt),
				"repositoryId": "repository:platform/" + name, "workflowId": "workflow:platform/" + name + ":build",
				"owner": "platform", "repository": name, "githubRunId": fmt.Sprintf("%s-%d", name, attempt),
				"attempt": 1, "status": "completed", "conclusion": "success", "createdAt": now, "updatedAt": now,
				"startedAt": now, "completedAt": now, "observedAt": now})
		}
	}
	name := "gh-aw-logs-runs/farm.jsonl"
	setupWrite(t, filepath.Join(directory, name), shard.Bytes())
	setupWrite(t, filepath.Join(directory, "inventory-sources.json"), []byte("{}"))
	manifest, err := json.Marshal(map[string]string{name: fmt.Sprintf("%x", sha256.Sum256(shard.Bytes()))})
	if err != nil {
		t.Fatal(err)
	}
	setupWrite(t, filepath.Join(directory, "payload-hashes.json"), manifest)
	return directory
}

type setupAzureOAuth struct {
	mu       sync.Mutex
	codes    map[string]string
	requests []string
	revoked  []string
}

func newSetupAzureOAuth() *setupAzureOAuth {
	return &setupAzureOAuth{codes: map[string]string{}}
}

func (a *setupAzureOAuth) serveHTTP(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/login/oauth/") && r.URL.Path != "/user/memberships/orgs/platform" &&
		!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer journey-") &&
		r.URL.Path != "/applications/Iv1.caosetupsim/token" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/login/oauth/authorize":
		query := r.URL.Query()
		if query.Get("client_id") != "Iv1.caosetupsim" || query.Get("redirect_uri") != "https://"+setupAzureHost+"/auth/callback" || query.Get("state") == "" {
			http.Error(w, "incorrect OAuth registration or state", http.StatusBadRequest)
			return true
		}
		code := fmt.Sprintf("code-%d", len(a.requests))
		a.codes[code] = query.Get("simulated_user")
		http.Redirect(w, r, "https://"+setupAzureHost+"/auth/callback?code="+code+"&state="+url.QueryEscape(query.Get("state")), http.StatusFound)
	case r.Method == http.MethodPost && r.URL.Path == "/login/oauth/access_token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid token request", http.StatusBadRequest)
			return true
		}
		login, exists := a.codes[r.Form.Get("code")]
		if !exists || r.Form.Get("client_id") != "Iv1.caosetupsim" ||
			r.Form.Get("client_secret") != setupAzureSecrets()["CAO_SIM_CLIENT_SECRET"] ||
			r.Form.Get("redirect_uri") != "https://"+setupAzureHost+"/auth/callback" {
			http.Error(w, "unregistered OAuth client or code", http.StatusUnauthorized)
			return true
		}
		delete(a.codes, r.Form.Get("code"))
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "journey-" + login, "token_type": "bearer", "expires_in": 3600})
	case r.Method == http.MethodGet && r.URL.Path == "/user":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 123, "login": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer journey-")})
	case r.Method == http.MethodGet && r.URL.Path == "/user/memberships/orgs/platform":
		state := "pending"
		if r.Header.Get("Authorization") == "Bearer journey-member" {
			state = "active"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"state": state})
	case r.Method == http.MethodDelete && r.URL.Path == "/applications/Iv1.caosetupsim/token":
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid revocation", http.StatusBadRequest)
			return true
		}
		a.revoked = append(a.revoked, body["access_token"])
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported simulated OAuth request", http.StatusNotFound)
	}
	return true
}

func setupAzureBrowser(t *testing.T, proxy, ca string) *http.Client {
	t.Helper()
	endpoint, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(setupRead(t, ca)) {
		t.Fatal("invalid simulator CA")
	}
	transport := &http.Transport{Proxy: http.ProxyURL(endpoint),
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != endpoint.Host {
				return nil, fmt.Errorf("browser attempted to bypass its loopback proxy")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}}
	t.Cleanup(transport.CloseIdleConnections)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: transport, Jar: jar, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func setupAzureBrowserRequest(t *testing.T, ctx context.Context, client *http.Client, method, target, body string, status int) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range client.Jar.Cookies(request.URL) {
		if cookie.Name == "cao_csrf" {
			request.Header.Set("X-CSRF-Token", cookie.Value)
		}
	}
	response, err := client.Do(request) // #nosec G704 -- transport permits only the test-owned loopback proxy.
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d: %s (%v)", method, request.URL.Path, response.StatusCode, status, content, err)
	}
	assertSetupAzureNoSecrets(t, content)
	return content, response.Header
}

func assertSetupAzureFarmJourney(t *testing.T, ctx context.Context, functions *setupAzureFunctions, oauth *setupAzureOAuth, proxy, ca string) {
	t.Helper()
	const origin = "https://" + setupAzureHost
	const query = `{"sourceNames":["$repositories","$runs"]}`
	member := setupAzureBrowser(t, proxy, ca)
	setupAzureBrowserRequest(t, ctx, member, http.MethodPost, origin+"/api/v1/query", query, http.StatusUnauthorized)
	login := func(client *http.Client, user string, status int) {
		_, headers := setupAzureBrowserRequest(t, ctx, client, http.MethodGet, origin+"/auth/login", "", http.StatusFound)
		authorization, err := url.Parse(headers.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		parameters := authorization.Query()
		parameters.Set("simulated_user", user)
		authorization.RawQuery = parameters.Encode()
		_, headers = setupAzureBrowserRequest(t, ctx, client, http.MethodGet, authorization.String(), "", http.StatusFound)
		setupAzureBrowserRequest(t, ctx, client, http.MethodGet, headers.Get("Location"), "", status)
	}
	login(member, "member", http.StatusFound)
	page, _ := setupAzureBrowserRequest(t, ctx, member, http.MethodGet, origin+"/", "", http.StatusOK)
	if !bytes.Contains(page, []byte("server-http")) {
		t.Fatal("authenticated dashboard did not select the hosted backend")
	}
	read := func(wantRuns int) int64 {
		content, _ := setupAzureBrowserRequest(t, ctx, member, http.MethodPost, origin+"/api/v1/query", query, http.StatusOK)
		var result struct {
			Revision int64
			Sources  map[string]model.Source
		}
		if err := json.Unmarshal(content, &result); err != nil {
			t.Fatal(err)
		}
		repositories := make([]string, 0, len(result.Sources["$repositories"].Rows))
		for _, row := range result.Sources["$repositories"].Rows {
			repositories = append(repositories, row["owner"].(string)+"/"+row["name"].(string))
			if row["visibility"] != "private" {
				t.Fatal("farm repository lost private visibility")
			}
		}
		slices.Sort(repositories)
		if !slices.Equal(repositories, []string{"platform/alpha", "platform/control", "platform/zeta"}) || len(result.Sources["$runs"].Rows) != wantRuns {
			t.Fatalf("hosted query did not return the exact farm and %d runs: %s", wantRuns, content)
		}
		ids := make([]string, 0, wantRuns)
		for _, row := range result.Sources["$runs"].Rows {
			if row["owner"] != "platform" || (row["repository"] != "alpha" && row["repository"] != "zeta") {
				t.Fatalf("hosted run is outside the farm: %s", content)
			}
			ids = append(ids, row["id"].(string))
		}
		slices.Sort(ids)
		expected := make([]string, 0, wantRuns)
		for _, name := range []string{"alpha", "zeta"} {
			for attempt := 1; attempt <= wantRuns/2; attempt++ {
				expected = append(expected, fmt.Sprintf("run:%s-%d", name, attempt))
			}
		}
		if !slices.Equal(ids, expected) {
			t.Fatalf("hosted query returned incorrect run identities: %v", ids)
		}
		return result.Revision
	}
	previous := read(2)
	setupAzureBrowserRequest(t, ctx, member, http.MethodGet, origin+"/api/admin/collection/status", "", http.StatusForbidden)
	outsider := setupAzureBrowser(t, proxy, ca)
	login(outsider, "outsider", http.StatusForbidden)
	setupAzureBrowserRequest(t, ctx, outsider, http.MethodPost, origin+"/api/v1/query", query, http.StatusUnauthorized)
	// Exercise the chosen controlled-snapshot refresh branch, not just initial
	// readiness. This deliberately does not pretend a collector was installed.
	snapshot := setupAzureFarmSnapshot(t, 2)
	bootstrapRun(t, ctx, functions.directory, functions.env, functions.binary, "ingest", "--source", snapshot,
		"--redis-namespace", "azure-dashboard", "--database-queries", filepath.Join(functions.directory, "database-queries.json"))
	setupAzureBrowserRequest(t, ctx, member, http.MethodPost, origin+"/api/v1/refresh", "{}", http.StatusOK)
	if read(4) <= previous {
		t.Fatal("new snapshot did not advance the hosted data revision")
	}
	oauth.mu.Lock()
	defer oauth.mu.Unlock()
	if !slices.Contains(oauth.revoked, "journey-outsider") {
		t.Fatal("denied sign-in did not revoke its simulated provider credential")
	}
	t.Log("private installer -> reviewed App scope -> snapshot deploy -> OAuth login/denial -> PostgreSQL farm query -> refreshed evidence; no Pages or live services")
}
