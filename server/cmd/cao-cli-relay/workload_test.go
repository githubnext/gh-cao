package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/cliegress"
)

func TestRunWorkloadForwardsStreamsAndRelayEnvironment(t *testing.T) {
	var stdout, stderr bytes.Buffer
	script := `read line; echo "in=$line"; echo "proxy=$HTTP_PROXY"; echo "no=[$NO_PROXY]" >&2; echo "keep=$KEEP"`
	code := runWorkload(t.Context(), []string{"sh", "-c", script},
		[]string{"PATH=/usr/bin:/bin", "KEEP=yes", "HTTP_PROXY=http://evil:1", "NO_PROXY=example.com"},
		strings.NewReader("hello\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr.String())
	}
	for _, want := range []string{"in=hello", "proxy=http://" + cliegress.RelayAddress, "keep=yes"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q missing %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "evil") {
		t.Errorf("inherited proxy leaked: %q", stdout.String())
	}
	if got := strings.TrimSpace(stderr.String()); got != "no=[]" {
		t.Errorf("stderr = %q, want %q", got, "no=[]")
	}
}

func TestRunWorkloadPropagatesExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWorkload(t.Context(), []string{"sh", "-c", "exit 3"}, []string{"PATH=/usr/bin:/bin"}, strings.NewReader(""), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestRunWorkloadReportsStartFailureAndCancellation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWorkload(t.Context(), []string{"/nonexistent/cao-relay-workload", "arg"}, nil, strings.NewReader(""), &stdout, &stderr)
	if code != 125 {
		t.Fatalf("missing executable = %d, want 125", code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code = runWorkload(ctx, []string{"sleep", "5"}, []string{"PATH=/usr/bin:/bin"}, strings.NewReader(""), &stdout, &stderr)
	if code != 125 {
		t.Fatalf("cancelled workload = %d, want 125", code)
	}
}
