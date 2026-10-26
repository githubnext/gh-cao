package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSetupAdmissionConfiguration(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		calls := 0
		root, stdout, _ := testCommand(t, func(_ context.Context, args ...string) (string, string, error) {
			calls++
			if calls == 1 {
				return "[]", "", nil
			}
			if args[0] != "api" || strings.Contains(strings.Join(args, " "), "secret") {
				t.Fatal("admission attempted credential provisioning")
			}
			return "", "", nil
		}, nil)
		args := []string{"setup", "admission", "--repo", "acme/project", "--url", "https://cao.example/api/admission", "--json"}
		if dryRun {
			args = append(args, "--dry-run")
		}
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		var report admissionCLIReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		expectedCalls := 4
		if dryRun {
			expectedCalls = 1
		}
		if report.Command != "setup admission" || report.Applied == dryRun || report.Settings.OnOutage != "block" || calls != expectedCalls {
			t.Fatalf("setup admission diverged: %+v calls=%d", report, calls)
		}
	}
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"setup", "admission", "--help"})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "No PAT") || !strings.Contains(stdout.String(), "--dry-run") {
		t.Fatal("setup admission help omits secret boundary or preview flag")
	}
}

func TestAdmissionSetupRemoved(t *testing.T) {
	for _, flags := range [][]string{
		{},
		{"--repo", "acme/project", "--url", "https://cao.example/api/admission", "--json"},
	} {
		root, _, _ := testCommand(t, func(context.Context, ...string) (string, string, error) {
			t.Fatal("removed admission setup command invoked GitHub")
			return "", "", nil
		}, nil)
		root.SetArgs(append([]string{"admission", "setup"}, flags...))
		if err := root.ExecuteContext(t.Context()); err == nil {
			t.Fatal("removed admission setup command accepted")
		}
	}
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"admission", "--help"})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "\n  setup ") || !strings.Contains(stdout.String(), "gh cao setup admission") {
		t.Fatalf("admission help should direct setup to the canonical command: %s", stdout)
	}
}

func TestSetupCommandHierarchy(t *testing.T) {
	root, _, _ := testCommand(t, nil, nil)
	for _, tt := range []struct {
		path, want []string
	}{
		{[]string{"setup"}, []string{"admission", "adx", "auth"}},
		{[]string{"setup", "auth"}, []string{"enterprise-app", "github-app", "hosted-cli", "token"}},
		{[]string{"admission"}, []string{"disable", "status"}},
	} {
		cmd, remaining, err := root.Find(tt.path)
		if err != nil || len(remaining) != 0 {
			t.Fatalf("find %q: %v, remaining %q", tt.path, err, remaining)
		}
		var names []string
		for _, child := range cmd.Commands() {
			names = append(names, child.Name())
		}
		slices.Sort(names)
		if !reflect.DeepEqual(names, tt.want) {
			t.Fatalf("%q subcommands = %v, want %v", tt.path, names, tt.want)
		}
	}
}

func TestSetupGitHubAppDryRunIsNative(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	help, stdout, _ := testCommand(t, nil, nil)
	help.SetArgs([]string{"setup", "auth", "github-app", "--help"})
	if err := help.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gh cao setup auth github-app", "--dry-run", "--read-client-id", "--write-client-id", "--write-repository", "preselect", "100 repositories", "Configure", "GHES"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("GitHub App help omits %q: %s", want, stdout)
		}
	}
	for _, host := range []string{"github.com", "contoso-aw.ghe.com", "github.example.test"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("GH_HOST", host)
			policy := nativeSetupTestPolicy()
			scope := nativeSetupMap(nativeSetupMap(policy["control-plane"])["scope"])
			scope["allowed-repositories"] = []string{"org/control", "org/evidence"}
			for _, reuse := range []bool{false, true} {
				t.Run(fmt.Sprintf("reuse=%t", reuse), func(t *testing.T) {
					if reuse {
						scope["allowed-repositories"] = []string{"org/control", "other/evidence"}
						scope["allowed-owners"] = []string{"org", "other"}
					}
					data, err := json.Marshal(policy)
					if err != nil {
						t.Fatal(err)
					}
					writeTestFile(t, policyPath, string(data))
					gh := func(context.Context, ...string) (string, string, error) {
						t.Fatal("GitHub App dry run must not invoke gh or mutate credentials")
						return "", "", nil
					}
					root, stdout, stderr := testCommand(t, gh, nativeOperator(gh))
					args := []string{"setup", "auth", "github-app", "--repo", "org/control", "--write-repository", "org/reviews", "--dry-run"}
					if reuse {
						args = append(args, "--read-client-id", "Iv1.read", "--write-client-id", "Iv1.write")
					}
					root.SetArgs(args)
					if err := root.ExecuteContext(t.Context()); err != nil {
						t.Fatal(err)
					}
					var result struct {
						Repo                string
						InstallationTargets map[string][]nativeSetupOwnerGroup
						Apps                []struct {
							Role, ClientID, Variable, Secret string
							Manifest                         map[string]any
						}
					}
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if result.Repo != "org/control" || len(result.Apps) != 2 || stderr.Len() != 0 {
						t.Fatalf("unexpected dry-run result: %s; stderr=%s", stdout, stderr)
					}
					read := []nativeSetupOwnerGroup{{Owner: "org", Repositories: []string{"control", "evidence"}}}
					if reuse {
						read = []nativeSetupOwnerGroup{{Owner: "org", Repositories: []string{"control"}}, {Owner: "other", Repositories: []string{"evidence"}}}
					}
					wantTargets := map[string][]nativeSetupOwnerGroup{
						"read": read, "write": {{Owner: "org", Repositories: []string{"reviews"}}},
					}
					if !reflect.DeepEqual(result.InstallationTargets, wantTargets) {
						t.Fatalf("installation scope = %#v, want %#v", result.InstallationTargets, wantTargets)
					}
					for _, app := range result.Apps {
						prefix := "GH_AW_GITHUB_" + strings.ToUpper(app.Role) + "_APP_"
						if app.Variable != prefix+"ID" || app.Secret != prefix+"PRIVATE_KEY" {
							t.Fatalf("wrong credential profile: %#v", app)
						}
						if reuse {
							if app.ClientID != "Iv1."+app.Role || app.Manifest != nil {
								t.Fatalf("reuse must not register new Apps: %#v", app)
							}
							continue
						}
						if app.Manifest["name"] != "cao-org-control-"+app.Role || app.Manifest["url"] != "https://"+host+"/org/control" ||
							app.Manifest["public"] != false || app.Manifest["redirect_url"] != "http://127.0.0.1:0/callback" {
							t.Fatalf("unexpected private App manifest: %#v", app.Manifest)
						}
					}
				})
			}
		})
	}
}

func TestSetupGitHubAppRejectsInvalidSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	policy := nativeSetupTestPolicy()
	scope := nativeSetupMap(nativeSetupMap(policy["control-plane"])["scope"])
	scope["allowed-owners"] = []string{"org", "other"}
	scope["allowed-repositories"] = []string{"org/control", "other/evidence"}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, policyPath, string(data))
	for _, tt := range []struct {
		flags []string
		want  string
	}{
		{nil, "private organization-owned Apps cannot be installed on other"},
		{[]string{"--read-client-id", "Iv1.read"}, "must be supplied together"},
		{[]string{"--write-client-id", "Iv1.write"}, "must be supplied together"},
		{[]string{"--read-client-id", "Iv1.same", "--write-client-id", "Iv1.same"}, "must be distinct"},
		{[]string{"--read-client-id", "Iv1.read", "--write-client-id", "Iv1.write", "--force"}, "cannot be combined"},
		{[]string{"--read-client-id", "Iv1.read", "--write-client-id", "Iv1.write", "--read-app-name", "new-read"}, "cannot be combined"},
		{[]string{"--read-client-id", "Iv1.read", "--write-client-id", "Iv1.write", "--write-app-name", "new-write"}, "cannot be combined"},
	} {
		t.Run(strings.Join(tt.flags, " "), func(t *testing.T) {
			gh := func(context.Context, ...string) (string, string, error) {
				t.Fatal("invalid App selection must not invoke gh")
				return "", "", nil
			}
			root, stdout, _ := testCommand(t, gh, nativeOperator(gh))
			root.SetArgs(append([]string{"setup", "auth", "github-app", "--repo", "org/control", "--dry-run"}, tt.flags...))
			if err := root.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid App selection produced output: %s", stdout)
			}
		})
	}
}

func TestSetupAuthenticationIsNotRootCommand(t *testing.T) {
	for _, args := range [][]string{
		{"setup-auth"}, {"setup-auth", "token", "--dry-run"},
	} {
		root, stdout, stderr := testCommand(t, nil, nil)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Fatalf("%q: expected root-level authentication to be rejected", args)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
		}
	}
}

func TestSetupPreservesRuntimeIOContextAndErrors(t *testing.T) {
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "setup"))
	defer cancel()
	for _, args := range [][]string{
		{"setup", "--json"},
		{"setup", "auth", "github-app"},
		{"setup", "auth", "enterprise-app"},
		{"setup", "auth", "hosted-cli"},
		{"setup", "auth", "token"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			want := errors.New("setup stopped")
			called := false
			root, stdout, stderr := testCommand(t, nil, func(cmd *cobra.Command, _ []string) error {
				called = true
				if cmd.Context() != ctx || cmd.Context().Value(key{}) != "setup" {
					t.Fatal("setup context changed")
				}
				input, err := io.ReadAll(cmd.InOrStdin())
				if err != nil || string(input) != "terminal input" {
					t.Fatalf("stdin changed: %q, %v", input, err)
				}
				if _, err := io.WriteString(cmd.OutOrStdout(), "result"); err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(cmd.ErrOrStderr(), "instructions"); err != nil {
					t.Fatal(err)
				}
				return want
			})
			root.SetIn(strings.NewReader("terminal input"))
			root.SetArgs(args)
			if err := root.ExecuteContext(ctx); !errors.Is(err, want) {
				t.Fatalf("runtime error changed: %v", err)
			}
			if !called || stdout.String() != "result" || stderr.String() != "instructions" {
				t.Fatalf("runtime streams changed: stdout=%q stderr=%q called=%v", stdout, stderr, called)
			}
		})
	}
}
