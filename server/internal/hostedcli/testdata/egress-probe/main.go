// A credential-free probe used only by the opt-in real Podman boundary test.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	err := probe(ctx)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("network-none: direct TCP/UDP/DNS denied; CONNECT allowed/denied; git/gh reached GitHub")
}

func probe(ctx context.Context) error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("interface inventory unavailable")
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback == 0 {
			return fmt.Errorf("non-loopback interface present")
		}
	}
	for _, address := range []string{"1.1.1.1:443", "[2606:4700:4700::1111]:443", "10.88.0.1:443", "172.17.0.1:443", "169.254.169.254:80"} {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return fmt.Errorf("direct TCP bypass succeeded")
		}
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "udp", "1.1.1.1:53")
	if err == nil {
		defer func() { _ = conn.Close() }()
		if _, err := conn.Write([]byte("blocked-egress-probe")); err == nil {
			return fmt.Errorf("direct UDP bypass sent a packet")
		}
	}
	for _, address := range []string{"8.8.8.8:53", "10.88.0.1:53", "127.0.0.11:53"} {
		resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}}
		dnsCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := resolver.LookupHost(dnsCtx, "api.github.com")
		cancel()
		if err == nil {
			return fmt.Errorf("direct DNS bypass succeeded")
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, target := range []string{"https://example.com", "https://169.254.169.254", "https://127.0.0.1"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return fmt.Errorf("invalid probe request")
		}
		resp, err := client.Do(request)
		if err == nil {
			_ = resp.Body.Close()
			return fmt.Errorf("unapproved CONNECT succeeded")
		}
		if !strings.Contains(err.Error(), "Forbidden") {
			return fmt.Errorf("unapproved CONNECT did not reach policy denial")
		}
	}
	// The read-only socket mount must not have a writable workspace alias.
	if err := os.WriteFile("/workspace/.cao-egress/relay", []byte("replace"), 0o600); err == nil {
		return fmt.Errorf("workspace alias permits relay replacement")
	}
	if err := os.WriteFile("/cao-egress/relay", []byte("replace"), 0o600); err == nil {
		return fmt.Errorf("relay mount is writable")
	}
	git := exec.CommandContext(ctx, "git", "ls-remote", "https://github.com/github/gitignore", "HEAD")
	if output, err := git.Output(); err != nil || !strings.Contains(string(output), "HEAD") {
		return fmt.Errorf("public GitHub git read through CONNECT failed")
	}
	gh := exec.CommandContext(ctx, "gh", "api", "--hostname", "github.com", "/user")
	gh.Env = append(os.Environ(), "GH_TOKEN=egress-test-not-a-credential")
	output, err := gh.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "HTTP 401") {
		return fmt.Errorf("gh did not reach GitHub's unauthenticated rejection")
	}
	return nil
}
