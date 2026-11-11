package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type setupPublishPrompt struct {
	approve bool
	cancel  bool
	review  func()
	asked   bool
}

func (p *setupPublishPrompt) Text(string, string) (string, error) { return "org/control", nil }

func (p *setupPublishPrompt) Confirm(label string, defaultYes bool) (bool, error) {
	if label != "Commit and push these CAO monitoring changes?" {
		return true, nil
	}
	if defaultYes {
		panic("publication must default to No")
	}
	p.asked = true
	if p.review != nil {
		p.review()
	}
	if p.cancel {
		return false, errNativeSetupCancelled
	}
	return p.approve, nil
}

func setupPublishFixture(t *testing.T, existing bool) (*cobra.Command, ghExecutor, string, []string) {
	t.Helper()
	t.Setenv("CI", "")
	nativeSetupTestDirectory(t)
	cmd := nativeSetupTestCommand()
	git := func(args ...string) string {
		t.Helper()
		output, err := setupGitExec(cmd, "", args...)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	git("init", "--bare", "--quiet", "--initial-branch=main", remote)
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.name", "CAO setup test")
	git("config", "user.email", "cao-setup@example.test")
	git("config", "commit.gpgsign", "false")
	git("remote", "add", "origin", remote)
	files := []string{policyPath}
	if existing {
		nativeSetupTestSave(t, nativeSetupTestPolicy())
		git("add", ".github", "activity", "dashboard")
		git("commit", "--quiet", "-m", "Existing monitoring")
		git("push", "--quiet", "--set-upstream", "origin", "main")
		writeTestFile(t, policyPath, "{\n  \"version\": 1,\n  \"test-change\": true\n}\n")
	}
	// Git transport is a real local bare repository; only GitHub identity is mocked.
	cmd.SetContext(context.WithValue(cmd.Context(), setupGitKey{}, setupGitFunc(func(cmd *cobra.Command, index string, args ...string) (string, error) {
		if strings.Join(args, " ") == "remote get-url --push --all origin" {
			return "https://github.com/org/control.git\n", nil
		}
		if len(args) == 4 && args[0] == "ls-remote" {
			if args[2] != "https://github.com/org/control.git" {
				t.Fatal("remote revision must be checked at the verified push destination")
			}
			args[2] = remote
		}
		return setupGitExec(cmd, index, args...)
	})))
	var calls []string
	bootstrap := setupBootstrapGH(t, "", &calls)
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		switch strings.Join(args, " ") {
		case "api --hostname github.com repos/org/control":
			return `{"full_name":"org/control","html_url":"https://github.com/org/control","default_branch":"main"}`, "", nil
		}
		return bootstrap(ctx, args...)
	}
	return cmd, gh, remote, files
}

func setupPublishTestGit(t *testing.T, cmd *cobra.Command, args ...string) string {
	t.Helper()
	output, err := setupGitExec(cmd, "", args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(output)
}

func TestSetupPublicationSummary(t *testing.T) {
	for _, test := range []struct {
		stat, want string
	}{
		{" 2 files changed, 10 insertions(+), 3 deletions(-)\n", "2 files changed, \x1b[32m10 insertions(+)\x1b[0m, \x1b[31m3 deletions(-)\x1b[0m"},
		{" 1 file changed, 1 insertion(+)\n", "1 file changed, \x1b[32m1 insertion(+)\x1b[0m"},
		{" 1 file changed, 1 deletion(-)\n", "1 file changed, \x1b[31m1 deletion(-)\x1b[0m"},
		{" 1 file changed, 0 insertions(+), 0 deletions(-)\n", "1 file changed, \x1b[32m0 insertions(+)\x1b[0m, \x1b[31m0 deletions(-)\x1b[0m"},
	} {
		if got := setupPublishSummary(test.stat, true); got != test.want {
			t.Fatalf("styled summary = %q, want %q", got, test.want)
		}
		if got := setupPublishSummary(test.stat, false); got != strings.TrimSpace(test.stat) {
			t.Fatalf("plain summary contains styling: %q", got)
		}
	}
}

func TestSetupPublishesAllPreparedComponentsWithoutUnrelatedFiles(t *testing.T) {
	cmd, gh, remote, _ := setupPublishFixture(t, false)
	for name, content := range map[string]string{
		"notes.txt": "unrelated", "AGENTS.md": "consumer instructions",
		"dashboard/consumer.txt": "not runtime-owned", ".github/workflows/consumer.yml": "unrelated workflow",
	} {
		writeTestFile(t, name, content)
	}
	var reviewFile string
	prompt := &setupPublishPrompt{approve: true}
	prompt.review = func() {
		output := cmd.ErrOrStderr().(*bytes.Buffer).String()
		if !regexp.MustCompile(`CAO changes for org/control on main:\n\d+ files changed, \d+ insertions?\(\+\)\nFull diff`).MatchString(output) {
			t.Fatalf("expected a one-line change summary followed by the full-diff path: %s", output)
		}
		for _, verbose := range []string{"Policy diff:", "diff --git", " | ", "\x1b["} {
			if strings.Contains(output, verbose) {
				t.Fatalf("plain publication output contains verbose diff or styling %q", verbose)
			}
		}
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "Full diff (available until this prompt finishes): ") {
				reviewFile = strings.TrimPrefix(line, "Full diff (available until this prompt finishes): ")
			}
		}
		diff, err := os.ReadFile(reviewFile)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"activity/cao.mjs", "dashboard/site/package.json", "cao-activity.yml", "cao-dashboard.yml", "cao-validate.yml", policyPath} {
			if !strings.Contains(string(diff), name) {
				t.Fatalf("review omitted %s", name)
			}
		}
		for _, name := range []string{"notes.txt", "AGENTS.md", "consumer.txt", "consumer.yml"} {
			if strings.Contains(string(diff), name) {
				t.Fatalf("review included unrelated file %s", name)
			}
		}
		if staged := setupPublishTestGit(t, cmd, "diff", "--cached", "--name-only"); staged != "" {
			t.Fatalf("review changed the real index: %s", staged)
		}
	}
	result, err := nativeSetupControl(cmd, gh, prompt)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeSetupMap(result["publication"])
	if !prompt.asked || publication["status"] != "published" || publication["actionsStatus"] != "pending" ||
		publication["actionsURL"] != "https://github.com/org/control/actions" {
		t.Fatalf("publication result: %#v", publication)
	}
	localHead := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
	if remoteHead := setupPublishTestGit(t, cmd, "--git-dir="+remote, "rev-parse", "refs/heads/main"); remoteHead != localHead {
		t.Fatalf("push did not reach local fixture remote: %s != %s", remoteHead, localHead)
	}
	tracked := setupPublishTestGit(t, cmd, "ls-files")
	for _, name := range append(append([]string{}, setupRuntimeFiles...), nativeRuntimeRequiredFiles...) {
		if !strings.Contains("\n"+tracked+"\n", "\n"+name+"\n") {
			t.Fatalf("prepared component not committed: %s", name)
		}
	}
	for _, name := range []string{"notes.txt", "AGENTS.md", "dashboard/consumer.txt", ".github/workflows/consumer.yml"} {
		if strings.Contains(tracked, name) {
			t.Fatalf("unrelated file committed: %s", name)
		}
	}
	if _, err := os.Stat(reviewFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary review diff was not cleaned up")
	}
}

func TestNativeSetupPublicationOutput(t *testing.T) {
	for _, structured := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "JSON"}[structured], func(t *testing.T) {
			cmd, gh, _, _ := setupPublishFixture(t, false)
			cmd.Flags().Bool("json", structured, "")
			cmd.SetContext(context.WithValue(cmd.Context(), nativeSetupPromptKey{}, nativeSetupPrompt(&setupPublishPrompt{approve: true})))
			if err := nativeSetup(cmd, gh); err != nil {
				t.Fatal(err)
			}
			output := cmd.OutOrStdout().(*bytes.Buffer).String()
			if strings.Contains(output, "\x1b[") {
				t.Fatal("redirected setup output must not contain terminal styling")
			}
			for _, obsolete := range []string{"Pages", "pages", "dashboardURL", "deploymentStatus", ".github.io"} {
				if strings.Contains(output, obsolete) {
					t.Fatalf("publication retains obsolete Pages output %q: %s", obsolete, output)
				}
			}
			if structured {
				var result map[string]any
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					t.Fatal(err)
				}
				publication := nativeSetupMap(result["publication"])
				if publication["status"] != "published" || publication["actionsStatus"] != "pending" {
					t.Fatalf("misleading JSON result: %s", output)
				}
			} else if !strings.Contains(output, "Actions pending") || !strings.Contains(output, "https://github.com/org/control/actions") ||
				strings.Contains(output, "No commit or push was performed") {
				t.Fatalf("misleading text result: %s", output)
			}
		})
	}
}

func TestSetupPublicationLinkStyling(t *testing.T) {
	publication := map[string]any{"actionsURL": "https://github.com/org/control/actions"}
	for _, styled := range []bool{false, true} {
		output := setupPublishOutput(publication, styled)
		want := "Actions:"
		if styled {
			want = "\x1b[1;36m" + want + "\x1b[0m"
		}
		if !strings.Contains(output, want) {
			t.Fatalf("missing label %q in output %q", want, output)
		}
		for _, value := range publication {
			link := value.(string)
			if styled {
				link = "\x1b[4;34m" + link + "\x1b[0m"
			}
			if !strings.Contains(output, link) {
				t.Fatalf("missing link %q in output %q", link, output)
			}
		}
		if !strings.Contains(output, "Actions operational artifacts with the local or cloud dashboard") {
			t.Fatal("output must direct operators to real-data dashboards")
		}
		if styled {
			if !strings.HasPrefix(output, "\n\x1b[1;32mMonitoring configuration pushed.\x1b[0m") {
				t.Fatal("styled success must be highlighted and separated from the prompt")
			}
		} else if strings.Contains(output, "\x1b[") {
			t.Fatal("plain output must not contain terminal styling")
		}
		if !strings.Contains(output, "Actions pending; setup is not waiting for workflow completion.") {
			t.Fatal("link styling must preserve pending Actions status")
		}
	}
}

func TestSetupPublicationDeclineAndCancellationLeaveIndexUntouched(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "declined", true: "cancelled"}[cancel], func(t *testing.T) {
			cmd, gh, _, files := setupPublishFixture(t, true)
			before := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
			prompt := &setupPublishPrompt{cancel: cancel}
			result, err := setupPublish(cmd, gh, prompt, "org/control", files)
			if err != nil || result["status"] != "declined" || !prompt.asked {
				t.Fatalf("result=%#v error=%v", result, err)
			}
			if setupPublishTestGit(t, cmd, "rev-parse", "HEAD") != before || setupPublishTestGit(t, cmd, "diff", "--cached", "--name-only") != "" {
				t.Fatal("declining publication changed HEAD or the index")
			}
		})
	}
}

func TestSetupPublicationNeverCommitsUnattended(t *testing.T) {
	for _, ci := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "CI"}[ci], func(t *testing.T) {
			cmd := nativeSetupTestCommand()
			cmd.SetContext(context.WithValue(cmd.Context(), setupGitKey{}, setupGitFunc(func(*cobra.Command, string, ...string) (string, error) {
				t.Fatal("unattended publication invoked Git")
				return "", nil
			})))
			var prompt nativeSetupPrompt
			if ci {
				t.Setenv("CI", "true")
				prompt = &setupPublishPrompt{approve: true}
			}
			result, err := setupPublish(cmd, nil, prompt, "org/control", nil)
			if err != nil || result["status"] != "not-requested" {
				t.Fatalf("result=%#v error=%v", result, err)
			}
		})
	}
}

func TestSetupPublicationBlocksUnsafeGitState(t *testing.T) {
	for _, state := range []string{"staged", "ahead", "behind", "feature branch", "detached", "uncommitted runtime"} {
		t.Run(state, func(t *testing.T) {
			cmd, gh, _, files := setupPublishFixture(t, true)
			switch state {
			case "staged":
				writeTestFile(t, "unrelated.txt", "unrelated")
				setupPublishTestGit(t, cmd, "add", "unrelated.txt")
			case "ahead":
				writeTestFile(t, "unrelated.txt", "unrelated")
				setupPublishTestGit(t, cmd, "add", "unrelated.txt")
				setupPublishTestGit(t, cmd, "commit", "-m", "Unrelated local commit")
			case "behind":
				tree := setupPublishTestGit(t, cmd, "rev-parse", "HEAD^{tree}")
				parent := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
				remoteCommit := setupPublishTestGit(t, cmd, "commit-tree", tree, "-p", parent, "-m", "Remote-only update")
				setupPublishTestGit(t, cmd, "push", "origin", remoteCommit+":refs/heads/main")
			case "feature branch":
				setupPublishTestGit(t, cmd, "switch", "-c", "feature")
			case "detached":
				setupPublishTestGit(t, cmd, "switch", "--detach")
			case "uncommitted runtime":
				setupPublishTestGit(t, cmd, "rm", "--cached", "--", "activity/cao.mjs")
				setupPublishTestGit(t, cmd, "commit", "-m", "Remove runtime")
				setupPublishTestGit(t, cmd, "push", "origin", "main")
			}
			head := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
			index := setupPublishTestGit(t, cmd, "write-tree")
			prompt := &setupPublishPrompt{approve: true}
			result, err := setupPublish(cmd, gh, prompt, "org/control", files)
			if err != nil || result["status"] != "blocked" || prompt.asked {
				t.Fatalf("result=%#v error=%v asked=%v", result, err, prompt.asked)
			}
			if head != setupPublishTestGit(t, cmd, "rev-parse", "HEAD") || index != setupPublishTestGit(t, cmd, "write-tree") {
				t.Fatal("blocked publication changed HEAD or staging")
			}
		})
	}
}

func TestSetupPublicationCommitsOnlyCAODelta(t *testing.T) {
	cmd, gh, remote, files := setupPublishFixture(t, true)
	writeTestFile(t, "README.md", "Original README")
	setupPublishTestGit(t, cmd, "add", "README.md")
	setupPublishTestGit(t, cmd, "commit", "-m", "Existing documentation")
	setupPublishTestGit(t, cmd, "push", "origin", "main")
	writeTestFile(t, "README.md", "Unrelated unstaged documentation edit")
	result, err := setupPublish(cmd, gh, &setupPublishPrompt{approve: true}, "org/control", files)
	if err != nil || result["status"] != "published" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if changed := setupPublishTestGit(t, cmd, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); changed != policyPath {
		t.Fatalf("publication included files outside its CAO delta: %s", changed)
	}
	if setupPublishTestGit(t, cmd, "diff", "--name-only") != "README.md" {
		t.Fatal("unrelated unstaged changes were not preserved")
	}
	if setupPublishTestGit(t, cmd, "--git-dir="+remote, "rev-parse", "main") != result["commit"] {
		t.Fatal("CAO delta was not pushed")
	}
	unchanged, err := setupPublish(cmd, gh, &setupPublishPrompt{approve: true}, "org/control", files)
	if err != nil || unchanged["status"] != "unchanged" {
		t.Fatalf("idempotent publication result=%#v error=%v", unchanged, err)
	}
}

func TestSetupPublicationRejectsChangesDuringReview(t *testing.T) {
	for _, state := range []string{"file", "index", "branch", "origin"} {
		t.Run(state, func(t *testing.T) {
			cmd, gh, _, files := setupPublishFixture(t, true)
			before := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
			originChanged := false
			run := cmd.Context().Value(setupGitKey{}).(setupGitFunc)
			cmd.SetContext(context.WithValue(cmd.Context(), setupGitKey{}, setupGitFunc(func(cmd *cobra.Command, index string, args ...string) (string, error) {
				if originChanged && strings.Join(args, " ") == "remote get-url --push --all origin" {
					return "https://github.com/other/repository.git\n", nil
				}
				return run(cmd, index, args...)
			})))
			prompt := &setupPublishPrompt{approve: true, review: func() {
				switch state {
				case "file":
					writeTestFile(t, policyPath, "changed after review")
				case "index":
					writeTestFile(t, "unrelated.txt", "unrelated")
					setupPublishTestGit(t, cmd, "add", "unrelated.txt")
				case "branch":
					setupPublishTestGit(t, cmd, "switch", "-c", "different")
				case "origin":
					originChanged = true
				}
			}}
			_, err := setupPublish(cmd, gh, prompt, "org/control", files)
			if err == nil || !strings.Contains(err.Error(), "changed after review") {
				t.Fatalf("changed %s accepted: %v", state, err)
			}
			if setupPublishTestGit(t, cmd, "rev-parse", "HEAD") != before {
				t.Fatal("changed review created a commit")
			}
		})
	}
}

func TestSetupPublicationReportsCommitAndProtectedPushFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture Git shell hooks require a POSIX shell")
	}
	for _, stage := range []string{"commit", "push"} {
		t.Run(stage, func(t *testing.T) {
			cmd, gh, remote, files := setupPublishFixture(t, true)
			before := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
			hook := ".git/hooks/pre-commit"
			if stage == "push" {
				hook = filepath.Join(remote, "hooks", "pre-receive")
			}
			writeTestFile(t, hook, "#!/bin/sh\necho 'Protected by fixture policy' >&2\nexit 1\n")
			if err := os.Chmod(hook, 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := setupPublish(cmd, gh, &setupPublishPrompt{approve: true}, "org/control", files)
			if err == nil || !strings.Contains(err.Error(), stage+" failed") {
				t.Fatalf("failed %s not reported: %v", stage, err)
			}
			local := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
			if stage == "commit" && local != before {
				t.Fatal("rejected commit changed HEAD")
			}
			if stage == "push" && (local == before || !strings.Contains(err.Error(), "local commit is retained")) {
				t.Fatal("push failure lost the local commit or recovery guidance")
			}
			if setupPublishTestGit(t, cmd, "--git-dir="+remote, "rev-parse", "main") != before {
				t.Fatal("protected remote was changed")
			}
		})
	}
}

func TestSetupPublicationRemoteIdentity(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/org/control.git", "git@github.com:org/control.git", "ssh://git@github.com/org/control.git",
	} {
		if _, err := setupPublishRemote(remote, "org/control"); err != nil {
			t.Fatalf("valid origin rejected: %s: %v", remote, err)
		}

	}
	for _, remote := range []string{
		"https://github.com/other/control.git", "https://token@github.com/org/control.git", "/tmp/other-repository",
		"https://github.com/org/control.git\nhttps://github.com/other/control.git", "ssh://git:password@github.com/org/control.git",
	} {
		if _, err := setupPublishRemote(remote, "org/control"); err == nil {
			t.Fatal("invalid origin accepted")
		}
	}
}

func TestSetupPublicationDoesNotPushHookChangedCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture Git shell hooks require a POSIX shell")
	}
	for _, stage := range []string{"tree", "ancestry", "branch", "origin"} {
		t.Run(stage, func(t *testing.T) {
			cmd, gh, remote, files := setupPublishFixture(t, true)
			before := setupPublishTestGit(t, cmd, "rev-parse", "HEAD")
			switch stage {
			case "tree":
				writeTestFile(t, ".git/hooks/pre-commit", "#!/bin/sh\nprintf unrelated > unrelated.txt\ngit add unrelated.txt\n")
				if err := os.Chmod(".git/hooks/pre-commit", 0o755); err != nil {
					t.Fatal(err)
				}
			case "ancestry", "branch":
				hook := "#!/bin/sh\ngit switch --quiet -c hook-branch\n"
				if stage == "ancestry" {
					hook = "#!/bin/sh\ngit -c core.hooksPath=/dev/null commit --quiet --allow-empty -m 'Additional hook commit'\n"
				}
				writeTestFile(t, ".git/hooks/post-commit", hook)
				if err := os.Chmod(".git/hooks/post-commit", 0o755); err != nil {
					t.Fatal(err)
				}
			case "origin":
				run := cmd.Context().Value(setupGitKey{}).(setupGitFunc)
				committed := false
				cmd.SetContext(context.WithValue(cmd.Context(), setupGitKey{}, setupGitFunc(func(cmd *cobra.Command, index string, args ...string) (string, error) {
					if committed && strings.Join(args, " ") == "remote get-url --push --all origin" {
						return "https://github.com/other/repository.git\n", nil
					}
					output, err := run(cmd, index, args...)
					if args[0] == "commit" {
						committed = true
					}
					return output, err
				})))
			}
			_, err := setupPublish(cmd, gh, &setupPublishPrompt{approve: true}, "org/control", files)
			if err == nil || !strings.Contains(err.Error(), "local commit retained") {
				t.Fatalf("%s mutation not reported: %v", stage, err)
			}
			if setupPublishTestGit(t, cmd, "--git-dir="+remote, "rev-parse", "main") != before {
				t.Fatal("a commit changed by hooks was pushed")
			}
		})
	}
}
