package simulator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSetupAzureProxyCannotReachLiveResources(t *testing.T) {
	var localRequests atomic.Int64
	blocked := make(chan string, 1)
	proxy, ca := bootstrapProxyWithRejection(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		localRequests.Add(1)
		_, _ = io.WriteString(w, "local simulator")
	}), func(method, host string) { blocked <- method + " " + host }, setupAzureHost, setupAzureSCM)
	target, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(setupRead(t, ca)) {
		t.Fatal("invalid simulator CA")
	}
	transport := &http.Transport{
		Proxy:           http.ProxyURL(target),
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != target.Host {
				return nil, fmt.Errorf("test client attempted to bypass its loopback proxy")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, host := range []string{"github.com", "api.github.com", setupAzureHost, setupAzureSCM} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != "local simulator" {
			t.Fatalf("%s was not handled by the local simulator", host)
		}
	}
	for _, host := range []string{"management.azure.com", "login.microsoftonline.com", "uploads.github.com", "raw.githubusercontent.com", "169.254.169.254"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			t.Fatalf("proxy accepted an unapproved destination: %s", host)
		}
		select {
		case rejected := <-blocked:
			if rejected != "CONNECT "+host+":443" {
				t.Fatalf("incorrect proxy rejection: %s", rejected)
			}
		default:
			t.Fatalf("request was not rejected by the local proxy: %s", host)
		}
	}
	if localRequests.Load() != 4 {
		t.Fatal("denied destinations reached the simulator's upstream")
	}
}

func TestSetupAzureEnvironmentDoesNotInheritCloudCredentials(t *testing.T) {
	for _, name := range []string{"GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
		"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID",
		"AZURE_FEDERATED_TOKEN_FILE", "AZURE_CONFIG_DIR", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"} {
		t.Setenv(name, "ambient-value-must-not-be-inherited")
	}

	t.Setenv("GH_TOKEN", "ambient-value-must-not-be-inherited")
	environment := bootstrapEnvironment(t, t.TempDir(), "http://127.0.0.1:1", "simulator-ca.pem")
	values := map[string]string{}
	for _, item := range environment {
		name, value, _ := strings.Cut(item, "=")
		values[name] = value
		if value == "ambient-value-must-not-be-inherited" {
			t.Fatalf("simulation inherited ambient cloud configuration: %s", name)
		}
	}
	if values["GH_TOKEN"] != "simulator-token-with-no-external-authority" {
		t.Fatal("simulation must use only its inert GitHub token")
	}
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"} {
		if values[name] != "http://127.0.0.1:1" {
			t.Fatalf("simulation did not fix its local proxy: %s", name)
		}
	}
	if values["NO_PROXY"] != "" || values["no_proxy"] != "" {
		t.Fatal("simulation allows a proxy bypass")
	}
}

func TestSetupAzurePodmanRejectsRemoteEngines(t *testing.T) {
	local := map[string]string{"CONTAINER_HOST": "", "PATH": "/usr/bin"}
	if name := setupPodmanRemoteSetting(func(name string) (string, bool) { value, ok := local[name]; return value, ok }); name != "" {
		t.Fatalf("local Podman environment rejected: %s", name)
	}
	for _, name := range setupPodmanRemoteSettings {
		for _, value := range []string{"unix:///run/podman/podman.sock", "ssh://core@remote.example/run/podman/podman.sock", "machine-default"} {
			remote := map[string]string{name: value}
			if got := setupPodmanRemoteSetting(func(name string) (string, bool) { value, ok := remote[name]; return value, ok }); got != name {
				t.Fatalf("simulation accepted remote Podman setting %s=%s", name, value)
			}
		}
	}
}
