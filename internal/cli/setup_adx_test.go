package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func setupADXTestArgs() []string {
	return []string{"setup", "adx",
		"--endpoint", "https://synthetic.eastus.kusto.windows.net/",
		"--database", "Synthetic", "--tenant-id", "11111111-1111-1111-1111-111111111111",
		"--client-id", "22222222-2222-2222-2222-222222222222",
		"--repository-id", "repo-b", "--repository-id", "repo-a",
		"--data-approved", "--single-replica"}
}

func TestSetupADXOffline(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("CAO_ADX_CLIENT_SECRET", "must-never-appear")
	t.Setenv("CAO_ADX_ENDPOINT", "https://ambient.invalid")
	t.Setenv("CAO_ADX_TRACES_ENABLED", "true")
	t.Setenv("CAO_ADX_TRACE_MINING_ENABLED", "true")
	root, stdout, stderr := testCommand(t, nil, nil)
	root.SetArgs(setupADXTestArgs())
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"CAO_ADX_ENABLED":              "true",
		"CAO_ADX_ENDPOINT":             "https://synthetic.eastus.kusto.windows.net",
		"CAO_ADX_DATABASE":             "Synthetic",
		"CAO_ADX_TENANT_ID":            "11111111-1111-1111-1111-111111111111",
		"CAO_ADX_CLIENT_ID":            "22222222-2222-2222-2222-222222222222",
		"CAO_ADX_REPOSITORY_IDS":       "repo-a,repo-b",
		"CAO_ADX_DATA_APPROVED":        "true",
		"CAO_ADX_NON_PERSONAL_DATA":    "false",
		"CAO_ADX_SINGLE_REPLICA":       "true",
		"CAO_ADX_INTERVAL":             "30s",
		"CAO_ADX_MAX_STORAGE_BYTES":    "50000000000",
		"CAO_ADX_TRACES_ENABLED":       "false",
		"CAO_ADX_TRACE_MINING_ENABLED": "false",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
	if strings.Contains(stdout.String()+stderr.String(), "must-never-appear") ||
		!strings.Contains(stderr.String(), "nothing deployed or verified") ||
		!strings.Contains(stderr.String(), "secret manager") {
		t.Fatal("missing handoff or leaked secret")
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("stdout mode changed the checkout: %v %v", entries, err)
	}
}

func TestSetupADXHelpOffline(t *testing.T) {
	t.Setenv("PATH", "")
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"setup", "adx", "--help"})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--dry-run", "--repository-id", "--data-approved", "--single-replica",
		"CAO_ADX_CLIENT_SECRET", "intentionally omitted", "does not contact Azure/GitHub",
		"cao-dashboard adx schema", "explicitly disables traces and mining"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help omits %q", want)
		}
	}
}

func TestSetupADXRejectsInvalidInputs(t *testing.T) {
	for _, tt := range []struct {
		name, flag, value, want string
	}{
		{"approval", "--data-approved", "--data-approved=false", "acknowledgment"},
		{"replicas", "--single-replica", "--single-replica=false", "acknowledgment"},
		{"http", "--endpoint", "http://synthetic.kusto.windows.net", "--endpoint"},
		{"ingest", "--endpoint", "https://ingest-synthetic.kusto.windows.net", "--endpoint"},
		{"host", "--endpoint", "https://synthetic.kusto.windows.net.evil.test", "--endpoint"},
		{"credentials", "--endpoint", "https://user:password@synthetic.kusto.windows.net", "--endpoint"},
		{"port", "--endpoint", "https://synthetic.kusto.windows.net:443", "--endpoint"},
		{"empty-port", "--endpoint", "https://synthetic.kusto.windows.net:", "--endpoint"},
		{"path", "--endpoint", "https://synthetic.kusto.windows.net/database", "--endpoint"},
		{"query", "--endpoint", "https://synthetic.kusto.windows.net?token=secret", "--endpoint"},
		{"empty-query", "--endpoint", "https://synthetic.kusto.windows.net?", "--endpoint"},
		{"fragment", "--endpoint", "https://synthetic.kusto.windows.net/#secret", "--endpoint"},
		{"encoded-path", "--endpoint", "https://synthetic.kusto.windows.net/%2f", "--endpoint"},
		{"database", "--database", "data; unsafe", "--database"},
		{"database-length", "--database", strings.Repeat("a", 101), "--database"},
		{"tenant", "--tenant-id", strings.Repeat("-", 36), "--tenant-id"},
		{"client", "--client-id", "not-a-guid", "--client-id"},
		{"empty-id", "--repository-id", "", "--repository-id"},
		{"wildcard", "--repository-id", "owner/*", "--repository-id"},
		{"question-mark", "--repository-id", "repo?", "--repository-id"},
		{"csv", "--repository-id", "repo-a,repo-c", "--repository-id"},
		{"control", "--repository-id", "repo\nx", "--repository-id"},
		{"whitespace", "--repository-id", " repo-a ", "--repository-id"},
		{"long-id", "--repository-id", strings.Repeat("a", 513), "--repository-id"},
		{"duplicate-id", "--repository-id", "repo-a", "duplicate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := setupADXTestArgs()
			for i, arg := range args {
				if arg != tt.flag {
					continue
				}
				if strings.Contains(tt.value, "=false") {
					args[i] = tt.value
				} else {
					args[i+1] = tt.value
				}
				break
			}
			path := filepath.Join(t.TempDir(), "settings.json")
			root, stdout, _ := testCommand(t, nil, nil)
			root.SetArgs(append(args, "--output", path))
			err := root.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.want) || stdout.Len() != 0 {
				t.Fatalf("error = %v, stdout = %s; want %q", err, stdout, tt.want)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid input wrote output: %v", err)
			}
		})
	}
	for _, flags := range [][]string{
		{"--interval", "0s"}, {"--interval", "999ms"}, {"--interval", "1h1s"}, {"--interval", "bad"},
		{"--max-storage-bytes", "0"}, {"--max-storage-bytes", "1048575"},
		{"--max-storage-bytes", "1099511627777"}, {"--max-storage-bytes", "bad"},
		{"--client-secret", "never-accept-a-secret"},
	} {
		root, stdout, _ := testCommand(t, nil, nil)
		root.SetArgs(append(setupADXTestArgs(), flags...))
		if err := root.ExecuteContext(t.Context()); err == nil || stdout.Len() != 0 {
			t.Fatalf("accepted invalid flags %q: %v", flags, err)
		}
	}
}

func TestSetupADXScopeBoundsAndApprovals(t *testing.T) {
	for _, count := range []int{1, 1000, 1001} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			args := setupADXTestArgs()
			args = append(args[:len(args)-6], "--non-personal-data", "--single-replica")
			for i := range count {
				args = append(args, "--repository-id", strings.Repeat("x", 512-len(strconv.Itoa(i)))+strconv.Itoa(i))
			}

			root, stdout, _ := testCommand(t, nil, nil)
			root.SetArgs(args)
			err := root.ExecuteContext(t.Context())
			if count > 1000 {
				if err == nil || stdout.Len() != 0 {
					t.Fatal("accepted too many IDs")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]string
			if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
				t.Fatal(err)
			}
			if settings["CAO_ADX_NON_PERSONAL_DATA"] != "true" || settings["CAO_ADX_DATA_APPROVED"] != "false" {
				t.Fatal("alternative approval was not preserved")
			}
		})
	}
}

func TestSetupADXRequiresExplicitSettings(t *testing.T) {
	for _, name := range []string{"--endpoint", "--database", "--tenant-id", "--client-id", "--repository-id"} {
		t.Run(name, func(t *testing.T) {
			args := setupADXTestArgs()
			filtered := []string{}
			for i := 0; i < len(args); i++ {
				if args[i] == name {
					i++
					continue
				}
				filtered = append(filtered, args[i])
			}
			root, stdout, _ := testCommand(t, nil, nil)
			root.SetArgs(filtered)
			if err := root.ExecuteContext(t.Context()); err == nil || stdout.Len() != 0 {
				t.Fatalf("accepted missing %s: %v", name, err)
			}
		})
	}
	root, _, _ := testCommand(t, nil, nil)
	root.SetArgs(append(setupADXTestArgs(), "extra"))
	if err := root.ExecuteContext(t.Context()); err == nil {
		t.Fatal("accepted positional argument")
	}
}

func TestSetupADXOutputFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	for _, dryRun := range []bool{true, false} {
		root, stdout, _ := testCommand(t, nil, nil)
		args := append(setupADXTestArgs(), "--output", path, "--interval", "1s", "--max-storage-bytes", "1048576")
		if dryRun {
			args = append(args, "--dry-run")
		}
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if dryRun {
			if !os.IsNotExist(err) {
				t.Fatalf("dry run created output: %v", err)
			}
			continue
		}
		if err != nil || string(data) != stdout.String() {
			t.Fatalf("saved output differs: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("output is not private: %v", err)
		}
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{path, link, dangling, dir, filepath.Join(dir, "absent", "file")} {
		root, stdout, _ := testCommand(t, nil, nil)
		root.SetArgs(append(setupADXTestArgs(), "--output", output))
		if err := root.ExecuteContext(t.Context()); err == nil || stdout.Len() != 0 {
			t.Fatalf("accepted unsafe output %s: %v", output, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatal("existing output was modified")
	}
}
