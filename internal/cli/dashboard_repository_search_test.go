package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDashboardRepositoryLookupCancellationStopsGitHubCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX GitHub CLI fixture")
	}
	directory := t.TempDir()
	started := filepath.Join(directory, "started")
	executable := filepath.Join(directory, "gh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf started > \"$CAO_TEST_SEARCH_STARTED\"\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_PATH", executable)
	t.Setenv("CAO_TEST_SEARCH_STARTED", started)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"text":"octo/repo","limit":20}`))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		dashboardRepositorySearch(response, request)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("GitHub lookup did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
		if response.Code != http.StatusBadGateway {
			t.Fatalf("canceled lookup reported success: %d", response.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled lookup did not stop the native GitHub subprocess")
	}
}

func TestDashboardRepositoryLookupIsPrivateReadOnlyAndBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX GitHub CLI fixture")
	}
	directory := t.TempDir()
	argsPath := filepath.Join(directory, "arguments")
	executable := filepath.Join(directory, "gh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAO_TEST_SEARCH_ARGS\"\nprintf '%s' '[{\"fullName\":\"octo/service\",\"description\":\"GitHub suggestion\"}]'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_PATH", executable)
	t.Setenv("CAO_TEST_SEARCH_ARGS", argsPath)
	filename, cleanup, err := startDashboardActionAdapterWithApproval(t.Context(), []string{"octo/control"}, directory,
		func(context.Context, []string) (bool, error) {
			t.Error("read-only search requested approval")
			return false, nil
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ AdapterURL string }
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		body string
		code int
	}{
		{`{"text":"octo/ser","limit":20}`, 200},
		{`{"text":"--limit=1000","limit":2}`, 200},
		{`{"text":"octo/*","limit":20}`, 400},
		{`{"text":"octo","limit":51}`, 400},
		{`{"text":"o","limit":20}`, 400},
		{`{"text":"octo","limit":20,"command":"delete"}`, 400},
	} {
		response, err := http.Post(config.AdapterURL+"/repositories/search", "application/json", strings.NewReader(test.body))
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != test.code || readErr != nil {
			t.Fatalf("lookup got %d: %s %v", response.StatusCode, body, readErr)
		}
		if test.code == 200 {
			args, err := os.ReadFile(argsPath)
			if err != nil || !strings.HasPrefix(string(args), "search\nrepos\n--json\nfullName,description,") {
				t.Fatalf("unexpected lookup argv: %s %v", args, err)
			}
			if strings.Contains(test.body, "octo/ser") && !strings.HasSuffix(string(args), "--limit\n20\n--owner\nocto\n--\nser\n") {
				t.Fatalf("qualified partial repository was not scoped to its owner: %s", args)
			}
			if strings.Contains(test.body, "--limit=1000") && !strings.HasSuffix(string(args), "--limit\n2\n--\n--limit=1000\n") {
				t.Fatalf("query text escaped its positional argument: %s", args)
			}
		}
	}
	base := config.AdapterURL[:strings.LastIndex(config.AdapterURL, "/")]
	response, err := http.Post(base+"/repositories/search", "application/json", strings.NewReader(`{"text":"octo","limit":20}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("lookup was reachable without the native adapter capability")
	}
}
