package simulator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cgi" // #nosec G504 -- Go 1.27; local Git fixtures only, never a public CGI host.
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCAOBootstrapIntegration(t *testing.T) {
	if os.Getenv("CAO_BOOTSTRAP_INTEGRATION") != "1" {
		t.Skip("run npm run test:integration:bootstrap to exercise the real installer")
	}
	if runtime.GOOS == "windows" {
		t.Fatal("bootstrap integration requires Bash on Linux or macOS")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	catalog, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	const release = "v0.0.1"
	binary := filepath.Join(root, "gh-cao")
	bootstrapRun(t, ctx, catalog, nil, "go", "build", "-mod=readonly", "-ldflags=-X main.version="+release, "-o", binary, "./cmd/gh-cao")
	api := NewBootstrapAPI(release, bootstrapReleaseAssets(t, binary))
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitExecPath := strings.TrimSpace(bootstrapRun(t, ctx, root, nil, "git", "--exec-path"))
	api.Git = &cgi.Handler{
		Path: git, Args: []string{"http-backend"}, Root: "/",
		Env: []string{"GIT_PROJECT_ROOT=" + filepath.Join(root, "remotes"), "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_EXEC_PATH=" + gitExecPath},
	}
	t.Cleanup(func() {
		if t.Failed() {
			api.mu.Lock()
			defer api.mu.Unlock()
			t.Logf("simulator requests: %v; unsupported: %v", api.Requests, api.Unhandled)
		}
	})
	proxy, ca := bootstrapProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "raw.githubusercontent.com" {
			if r.Method != http.MethodGet || r.URL.Path != "/github/gh-aw/main/install-gh-aw.sh" {
				t.Errorf("unexpected upstream installer request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			api.mu.Lock()
			api.Requests = append(api.Requests, r.Method+" "+r.URL.Path)
			api.mu.Unlock()
			_, _ = w.Write([]byte(bootstrapAWInstaller))
			return
		}
		api.ServeHTTP(w, r)
	}), "raw.githubusercontent.com")
	environment := bootstrapEnvironment(t, root, proxy, ca)
	bootstrapInstallAW(t, environment)
	remote := filepath.Join(root, "remotes", "platform", "control.git")
	bootstrapRun(t, ctx, root, environment, "git", "init", "--bare", "--initial-branch=main", remote)
	bootstrapRun(t, ctx, root, environment, "git", "--git-dir="+remote, "config", "http.receivepack", "true")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	bootstrapRun(t, ctx, workspace, environment, "bash", filepath.Join(catalog, "install-gh-cao.sh"), catalog, "--create-repo", "platform/control")
	consumer := filepath.Join(workspace, "control")
	bootstrapAssertRepository(t, api, true)
	for _, name := range []string{
		".github/workflows/cao-activity.yml", ".github/workflows/cao-dashboard.yml",
		".github/workflows/cao-validate.yml", ".github/workflows/shared/control.mjs",
		".github/actions/setup-cao-runtime/action.yml", "activity/cao.mjs", ".github/workflows/shared/materialize-cao.mjs",
	} {
		if info, err := os.Stat(filepath.Join(consumer, name)); err != nil || info.Size() == 0 {
			t.Fatalf("missing or empty installed resource %s: %v", name, err)
		}
	}
	policyFile := filepath.Join(consumer, ".github", "workflows", "cao.json")
	policyBefore, err := os.ReadFile(policyFile) // #nosec G304 -- fixed installed filename in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	// Private repositories require interactive visibility/credential consent.
	command := exec.CommandContext(ctx, "gh", "cao", "setup", "--json")
	command.Dir, command.Env = consumer, environment
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "requires a public control repository") {
		t.Fatalf("private non-interactive setup did not fail closed: %v\n%s", err, output)
	}
	after, err := os.ReadFile(policyFile) // #nosec G304 -- same installed fixture policy.
	if err != nil || string(after) != string(policyBefore) {
		t.Fatalf("failed private setup changed policy: %v", err)
	}
	bootstrapAssertRepository(t, api, true)
	// Explicitly select the documented public-monitoring profile on fixture
	// data only, then exercise the installer's --setup path and idempotency.
	bootstrapRun(t, ctx, consumer, environment, "gh", "api", "-X", "PATCH", "repos/platform/control", "-F", "private=false")
	bootstrapRun(t, ctx, consumer, environment, "bash", filepath.Join(catalog, "install-gh-cao.sh"), catalog, "--setup")
	bootstrapAssertRepository(t, api, false)
	bootstrapAssertPolicy(t, policyFile)
	configured, err := os.ReadFile(policyFile) // #nosec G304 -- fixed installed filename in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRun(t, ctx, consumer, environment, "gh", "cao", "setup", "--json")
	repeated, err := os.ReadFile(policyFile) // #nosec G304 -- fixed installed filename in t.TempDir.
	if err != nil || string(repeated) != string(configured) {
		t.Fatalf("repeated setup changed policy: %v", err)
	}
	bootstrapRun(t, ctx, consumer, environment, "git", "add", ".")
	bootstrapRun(t, ctx, consumer, environment, "git", "-c", "user.name=CAO simulator", "-c", "user.email=simulator@example.invalid",
		"commit", "-qm", "Bootstrap simulated CAO control plane\n\nCo-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>")
	bootstrapRun(t, ctx, consumer, environment, "git", "push", "--set-upstream", "origin", "HEAD:main")
	published := bootstrapRun(t, ctx, root, environment, "git", "--git-dir="+remote, "show", "main:.github/workflows/cao.json")
	if strings.TrimSpace(published) != strings.TrimSpace(string(configured)) {
		t.Fatal("published control-plane policy differs from verified local policy")
	}
	for _, workflow := range []string{"cao-activity.yml", "cao-dashboard.yml", "cao-validate.yml"} {
		bootstrapRun(t, ctx, root, environment, "git", "--git-dir="+remote, "show", "main:.github/workflows/"+workflow)
	}
	cloned := filepath.Join(root, "verified-clone")
	bootstrapRun(t, ctx, root, environment, "gh", "repo", "clone", "platform/control", cloned)
	clonedPolicy, err := os.ReadFile(filepath.Join(cloned, ".github", "workflows", "cao.json")) // #nosec G304 -- fixed cloned filename in t.TempDir.
	if err != nil || string(clonedPolicy) != string(configured) {
		t.Fatalf("fresh clone did not receive the deployed policy: %v", err)
	}
	testBootstrapInstallerCompatibility(t, ctx, catalog, consumer, environment, api)
	api.mu.Lock()
	api.Assets["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  gh-cao-" + runtime.GOOS + "-" + runtime.GOARCH + "\n")
	api.mu.Unlock()
	corrupt := exec.CommandContext(ctx, "bash", filepath.Join(catalog, "install-gh-cao.sh"), catalog, "--create-repo", "platform/corrupt") // #nosec G204 -- current checkout's installer and fixed test arguments.
	corrupt.Dir, corrupt.Env = workspace, environment
	output, err = corrupt.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "checksum verification failed") {
		t.Fatalf("installer accepted a corrupted release: %v\n%s", err, output)
	}
	bootstrapAssertRepository(t, api, false)
	bootstrapRun(t, ctx, consumer, environment, "gh", "cao", "--version")
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.Unhandled) != 0 {
		t.Fatalf("bootstrap used unsupported simulator routes: %v", api.Unhandled)
	}
	for _, required := range []string{
		"GET /repos/platform/control", "POST /graphql",
		"GET /repos/githubnext/gh-cao/releases", "GET /repos/githubnext/gh-cao/releases/tags/" + release,
		"GET /githubnext/gh-cao/releases/download/" + release + "/checksums.txt",
		"GET /repos/platform/control/actions/variables",
		"GET /repos/platform/control/actions/organization-variables",
		"POST /platform/control.git/git-receive-pack", "POST /platform/control.git/git-upload-pack",
	} {
		if !slices.Contains(api.Requests, required) {
			t.Errorf("bootstrap did not exercise %s", required)
		}
	}
	for _, request := range api.Requests {
		if request == "POST /repos/platform/control/actions/variables" ||
			strings.HasPrefix(request, "PATCH /repos/platform/control/actions/variables/") {
			t.Errorf("fresh setup unnecessarily configured authentication: %s", request)
		}
		if strings.HasSuffix(request, "/pages") {
			t.Errorf("operational bootstrap used Pages: %s", request)
		}
	}
	t.Logf("real installer, release verification, repository creation, setup and Git publication completed through %d simulated requests", len(api.Requests))
}

func bootstrapReleaseAssets(t *testing.T, binary string) map[string][]byte {
	t.Helper()
	executable := setupRead(t, binary)
	assets := map[string][]byte{}
	for _, platform := range []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64.exe", "windows-arm64.exe"} {
		assets["gh-cao-"+platform] = []byte("unused platform fixture")
	}
	assets["gh-cao-"+runtime.GOOS+"-"+runtime.GOARCH] = executable
	var checksums strings.Builder
	for name, content := range assets {
		fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(content), name)
	}
	assets["checksums.txt"] = []byte(checksums.String())
	return assets
}

func bootstrapRun(t *testing.T, ctx context.Context, directory string, environment []string, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...) // #nosec G204,G702 -- test-owned executable/argument arrays without shell interpolation, never API input.
	command.Dir = directory
	if environment != nil {
		command.Env = environment
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func bootstrapEnvironment(t *testing.T, root, proxy, ca string) []string {
	t.Helper()
	gitExecPath, err := exec.CommandContext(t.Context(), "git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(root, "config")
	data := filepath.Join(root, "data")
	for _, directory := range []string{home, config, data} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Do not inherit user tokens, git configuration, credential helpers,
	// extension installations, proxies, or Actions credentials. Disable CLI
	// telemetry before it can start background requests outside the allowlist.
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + config, "XDG_DATA_HOME=" + data,
		"GH_CONFIG_DIR=" + filepath.Join(config, "gh"), "GH_HOST=github.com",
		"GH_TOKEN=simulator-token-with-no-external-authority", "GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1",
		"GH_AW_DISABLE_UPDATE_CHECK=1",
		"DO_NOT_TRACK=1", "GH_TELEMETRY=0",
		"HTTPS_PROXY=" + proxy, "HTTP_PROXY=" + proxy, "ALL_PROXY=" + proxy, "NO_PROXY=",
		"https_proxy=" + proxy, "http_proxy=" + proxy, "all_proxy=" + proxy, "no_proxy=",
		"SSL_CERT_FILE=" + ca, "CURL_CA_BUNDLE=" + ca, "GIT_SSL_CAINFO=" + ca,
		"NODE_EXTRA_CA_CERTS=" + ca,
		"CI=1", "NO_COLOR=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_EXEC_PATH=" + strings.TrimSpace(string(gitExecPath)),
		"GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=https:file", "GH_GIT_PROTOCOL=https",
	}
}

func bootstrapInstallAW(t *testing.T, environment []string) {
	t.Helper()
	source := os.Getenv("CAO_BOOTSTRAP_GH_AW_BINARY")
	if source == "" {
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			data = filepath.Join(home, ".local", "share")
		}
		source = filepath.Join(data, "gh", "extensions", "gh-aw", "gh-aw")
	}
	content, err := os.ReadFile(source) // #nosec G304,G703 -- explicitly selected local gh-aw executable prerequisite.
	if err != nil {
		t.Fatalf("install gh-aw first or set CAO_BOOTSTRAP_GH_AW_BINARY: %v", err)
	}
	for _, item := range environment {
		if directory, ok := strings.CutPrefix(item, "XDG_DATA_HOME="); ok {
			destination := filepath.Join(directory, "gh", "extensions", "gh-aw", "gh-aw")
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destination, content, 0o700); err != nil { // #nosec G306,G703 -- test-owned executable under isolated XDG_DATA_HOME.
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing isolated extension directory")
}

func bootstrapAssertRepository(t *testing.T, api *BootstrapAPI, private bool) {
	t.Helper()
	api.mu.Lock()
	defer api.mu.Unlock()
	repo := api.Repositories["platform/control"]
	if len(api.Repositories) != 1 || repo == nil || repo.Private != private {
		t.Fatalf("unexpected control repository state: %+v", repo)
	}
	if len(repo.Variables) != 0 {
		t.Fatal("built-in token monitoring unexpectedly configured Actions variables")
	}
}

func bootstrapAssertPolicy(t *testing.T, file string) {
	t.Helper()
	content, err := os.ReadFile(file) // #nosec G304 -- test-owned installed policy.
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Version int `json:"version"`
		Control struct {
			Scope struct {
				Owners       []string `json:"allowed-owners"`
				Repositories []string `json:"allowed-repositories"`
			} `json:"scope"`
			Activity struct {
				Repositories []string `json:"repositories"`
			} `json:"activity"`
			Campaigns  map[string]any `json:"campaigns"`
			Workflows  map[string]any `json:"workflows"`
			Operations map[string]any `json:"operations"`
		} `json:"control-plane"`
	}
	if err := json.Unmarshal(content, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Version != 1 || !reflect.DeepEqual(policy.Control.Scope.Owners, []string{"platform"}) ||
		!reflect.DeepEqual(policy.Control.Scope.Repositories, []string{"platform/control"}) ||
		!reflect.DeepEqual(policy.Control.Activity.Repositories, []string{"platform/control"}) ||
		policy.Control.Campaigns == nil || len(policy.Control.Campaigns) != 0 ||
		len(policy.Control.Workflows) != 0 || len(policy.Control.Operations) != 0 {
		t.Fatalf("bootstrap policy must remain exact-scope and monitoring-only: %s", content)
	}
}

// The real gh and curl still address github.com and api.github.com. CONNECT
// terminates only at this simulator; no upstream connection or DNS is possible.
func bootstrapProxy(t *testing.T, handler http.Handler, additionalHosts ...string) (proxyURL, certificateFile string) {
	t.Helper()
	return bootstrapProxyWithRejection(t, handler, func(method, host string) {
		t.Errorf("blocked unexpected bootstrap destination: %s %s", method, host)
	}, additionalHosts...)
}

func bootstrapProxyWithRejection(t *testing.T, handler http.Handler, rejected func(string, string), additionalHosts ...string) (proxyURL, certificateFile string) {
	t.Helper()
	hosts := append([]string{"github.com", "api.github.com"}, additionalHosts...)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CAO bootstrap simulator"},
		DNSNames:  hosts,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certificate, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewUnstartedServer(handler)
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || !slices.ContainsFunc(hosts, func(host string) bool { return r.Host == host+":443" }) {
			rejected(r.Method, r.Host)
			http.Error(w, "simulator destination not allowed", http.StatusForbidden)
			return
		}
		local, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(r.Context(), "tcp", target.Host)
		if err != nil {
			t.Error(err)
			http.Error(w, "simulator unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = local.Close() }()
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("proxy does not support CONNECT")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		client, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = client.Close() }()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			t.Error(err)
			return
		}
		if err := buffered.Flush(); err != nil {
			t.Error(err)
			return
		}
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(local, buffered)
			_ = local.Close()
			close(done)
		}()
		_, _ = io.Copy(client, local)
		_ = client.Close()
		<-done
	}))
	t.Cleanup(proxy.Close)
	certificateFile = filepath.Join(t.TempDir(), "simulator-ca.pem")
	if err := os.WriteFile(certificateFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return proxy.URL, certificateFile
}
