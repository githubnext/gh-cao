package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

type setupGitKey struct{}
type setupGitFunc func(*cobra.Command, string, ...string) (string, error)

func setupGit(cmd *cobra.Command, index string, args ...string) (string, error) {
	if run, ok := cmd.Context().Value(setupGitKey{}).(setupGitFunc); ok {
		return run(cmd, index, args...)
	}
	return setupGitExec(cmd, index, args...)
}

func setupGitExec(cmd *cobra.Command, index string, args ...string) (string, error) {
	child := exec.CommandContext(cmd.Context(), "git", append([]string{"--no-pager", "--literal-pathspecs", "-c", "color.ui=false"}, args...)...)
	child.Stdin = cmd.InOrStdin()
	if index != "" {
		child.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
	}
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", fmt.Errorf("git %s failed: %s: %w", args[0], message, err)
		}
		return "", fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return stdout.String(), nil
}

func setupPublishHead(cmd *cobra.Command) (string, error) {
	head, err := setupGit(cmd, "", "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil // An unborn branch is valid for a fresh control repository.
		}
		return "", err
	}
	return strings.TrimSpace(head), nil
}

func setupPublishRemoteHead(cmd *cobra.Command, branch, pushURL string) (string, error) {
	output, err := setupGit(cmd, "", "ls-remote", "--heads", strings.TrimSpace(pushURL), "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if output == "" {
		return "", nil
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || !commitPattern.MatchString(fields[0]) || fields[1] != "refs/heads/"+branch {
		return "", errors.New("unable to verify the remote default-branch revision")
	}
	return fields[0], nil
}

func setupPublishRemote(raw, repository string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		host, path, ok := strings.Cut(raw, ":")
		if ok && strings.Contains(host, "@") {
			raw = "ssh://" + host + "/" + path
		}
	}
	remote, err := url.Parse(raw)
	if err != nil || remote == nil || (remote.Scheme != "https" && remote.Scheme != "ssh") ||
		remote.Hostname() == "" || remote.RawQuery != "" || remote.Fragment != "" ||
		!strings.EqualFold(strings.TrimSuffix(strings.TrimRight(remote.Path, "/"), ".git"), "/"+repository) {
		return nil, errors.New("origin must have one HTTPS or SSH push URL for the selected control repository")
	}
	if remote.User != nil {
		if _, password := remote.User.Password(); password || remote.Scheme != "ssh" {
			return nil, errors.New("origin must not contain embedded HTTPS credentials or an SSH password")
		}
	}
	return remote, nil
}

func setupPublishBlocked(cmd *cobra.Command, reason string) (map[string]any, error) {
	err := nativeSetupNote(cmd, "Publication not offered: %s.\nFiles remain local; review, commit and push manually.\n", reason)
	return map[string]any{"status": "blocked", "reason": reason}, err
}

func setupPublishSummary(stat string, styled bool) string {
	summary := strings.TrimSpace(stat)
	if !styled {
		return summary
	}
	return regexp.MustCompile(`\d+ (?:insertions?\(\+\)|deletions?\(-\))`).ReplaceAllStringFunc(summary, func(change string) string {
		color := "\x1b[32m"
		if strings.HasSuffix(change, "(-)") {
			color = "\x1b[31m"
		}
		return color + change + "\x1b[0m"
	})
}

func setupPublishOutput(publication map[string]any, styled bool) string {
	message := "Monitoring configuration pushed. Actions pending; setup is not waiting for workflow completion.\n"
	link := "%s: %s\n"
	if styled {
		message = "\n\x1b[1;32mMonitoring configuration pushed.\x1b[0m Actions pending; setup is not waiting for workflow completion.\n"
		link = "\x1b[1;36m%s:\x1b[0m \x1b[4;34m%s\x1b[0m\n"
	}
	message += fmt.Sprintf(link, "Actions", publication["actionsURL"])
	message += "Use Actions operational artifacts with the local or cloud dashboard.\n"
	return message
}

func setupPublish(cmd *cobra.Command, gh ghExecutor, prompt nativeSetupPrompt, repository string, files []string) (result map[string]any, err error) {
	return publishCAOChanges(cmd, gh, prompt, repository, files,
		"Commit and push these CAO monitoring changes?", "Set up CAO monitoring",
		"Pushing starts monitoring in Actions; setup will not wait for workflow completion.")
}

func publishCAOChanges(cmd *cobra.Command, gh ghExecutor, prompt nativeSetupPrompt, repository string, files []string, question, commitMessage, notice string) (result map[string]any, err error) {
	result = map[string]any{"status": "not-requested"}
	if prompt == nil || os.Getenv("CI") != "" {
		return result, nil
	}
	if err := nativeSetupNote(cmd, "Preparing change summary...\n"); err != nil {
		return nil, err
	}
	staged, err := setupGit(cmd, "", "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	if staged != "" {
		return setupPublishBlocked(cmd, "the Git index already contains staged changes; unstage or publish them separately")
	}
	remotes, err := setupGit(cmd, "", "remote")
	if err != nil {
		return nil, err
	}
	if !slices.Contains(strings.Fields(remotes), "origin") {
		return setupPublishBlocked(cmd, "no origin remote is configured")
	}
	pushURL, err := setupGit(cmd, "", "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return nil, err
	}
	if len(strings.Split(strings.TrimSpace(pushURL), "\n")) != 1 {
		return setupPublishBlocked(cmd, "origin has multiple push destinations")
	}
	remote, err := setupPublishRemote(pushURL, repository)
	if err != nil {
		return setupPublishBlocked(cmd, err.Error())
	}
	var target struct {
		Repository string `json:"full_name"`
		URL        string `json:"html_url"`
		Branch     string `json:"default_branch"`
	}
	if err := nativeSetupJSON(cmd, gh, &target, "api", "--hostname", remote.Hostname(), "repos/"+repository); err != nil {
		return nil, err
	}
	site, parseErr := url.Parse(target.URL)
	if parseErr != nil || site == nil || site.Scheme != "https" || site.User != nil || site.RawQuery != "" || site.Fragment != "" ||
		!strings.EqualFold(site.Hostname(), remote.Hostname()) || !strings.EqualFold(site.Path, "/"+repository) ||
		!strings.EqualFold(target.Repository, repository) || target.Branch == "" {
		return nil, errors.New("unable to verify the control repository's default branch and web URL")
	}
	branch, err := setupGit(cmd, "", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return setupPublishBlocked(cmd, "checkout is detached; publish from the control repository's default branch")
		}
		return nil, err
	}
	if strings.TrimSpace(branch) != target.Branch {
		return setupPublishBlocked(cmd, "checkout is not on the control repository's default branch; use your normal pull-request process")
	}
	head, err := setupPublishHead(cmd)
	if err != nil {
		return nil, err
	}
	remoteHead, err := setupPublishRemoteHead(cmd, target.Branch, pushURL)
	if err != nil {
		return nil, err
	}
	if head != remoteHead {
		return setupPublishBlocked(cmd, "local and remote default-branch revisions differ; synchronize or publish existing commits separately")
	}
	directory, err := os.MkdirTemp("", "cao-setup-review-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	index := filepath.Join(directory, "index")
	base := "HEAD"
	if head == "" {
		base = "--empty"
	}
	if _, err := setupGit(cmd, index, "read-tree", base); err != nil {
		return nil, err
	}
	files = nativeSetupUnique(files, false)
	for _, filename := range files {
		if _, err := setupDestination(filename); err != nil {
			return nil, err
		}
	}
	if _, err := setupGit(cmd, index, append([]string{"add", "--"}, files...)...); err != nil {
		return nil, err
	}
	// A rerun must not publish policy alone while a previously prepared runtime is uncommitted.
	required := append(append([]string{}, setupRuntimeFiles...), nativeRuntimeRequiredFiles...)
	if _, err := setupGit(cmd, index, append([]string{"ls-files", "--error-unmatch", "--"}, required...)...); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return setupPublishBlocked(cmd, "runtime files from an earlier setup are not committed; review and publish the complete installation manually")
		}
		return nil, err
	}
	changed, err := setupGit(cmd, index, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	if changed == "" {
		return map[string]any{"status": "unchanged"}, nil
	}
	paths := strings.Split(strings.TrimSuffix(changed, "\x00"), "\x00")
	tree, err := setupGit(cmd, index, "write-tree")
	if err != nil {
		return nil, err
	}
	stat, err := setupGit(cmd, index, "diff", "--cached", "--shortstat")
	if err != nil {
		return nil, err
	}
	diff, err := setupGit(cmd, index, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--binary")
	if err != nil {
		return nil, err
	}
	review := filepath.Join(directory, "cao-changes.diff")
	if err := os.WriteFile(review, []byte(diff), 0o600); err != nil {
		return nil, err
	}
	if err := nativeSetupNote(cmd, "CAO changes for %s on %s:\n%s\nFull diff (available until this prompt finishes): %s\n%s\n", repository, target.Branch, setupPublishSummary(stat, nativeSetupStyled(cmd)), review, notice); err != nil {
		return nil, err
	}
	approved, err := prompt.Confirm(question, false)
	if errors.Is(err, errNativeSetupCancelled) || (err == nil && !approved) {
		return map[string]any{"status": "declined"}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := setupPublishUnchanged(cmd, index, head, remoteHead, target.Branch, tree, pushURL, files); err != nil {
		return nil, err
	}
	if _, err := setupGit(cmd, "", append([]string{"add", "--"}, paths...)...); err != nil {
		return nil, err
	}
	stagedTree, err := setupGit(cmd, "", "write-tree")
	if err != nil {
		return nil, err
	}
	if stagedTree != tree {
		return nil, errors.New("the Git index changed after review; files remain staged, but setup did not commit or push")
	}
	if _, err := setupGit(cmd, "", "commit", "-m", commitMessage); err != nil {
		return nil, fmt.Errorf("CAO commit failed; inspect staged files before retrying publication: %w", err)
	}
	committedTree, err := setupGit(cmd, "", "rev-parse", "HEAD^{tree}")
	if err != nil {
		return nil, err
	}
	if committedTree != tree {
		return nil, errors.New("committed files differ from the reviewed changes; local commit retained, but setup did not push")
	}
	commit, err := setupPublishHead(cmd)
	if err != nil {
		return nil, err
	}
	parents, err := setupGit(cmd, "", "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		return nil, err
	}
	expectedParents := commit
	if head != "" {
		expectedParents += " " + head
	}
	currentBranch, err := setupGit(cmd, "", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(parents) != expectedParents || strings.TrimSpace(currentBranch) != target.Branch {
		return nil, errors.New("commit ancestry or branch changed during publication; local commit retained, but setup did not push")
	}
	currentURL, err := setupGit(cmd, "", "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return nil, err
	}
	if currentURL != pushURL {
		return nil, errors.New("origin changed during publication; local commit retained, but setup did not push")
	}
	if _, err := setupGit(cmd, "", "-c", "remote.origin.mirror=false", "push", "--no-follow-tags", "--recurse-submodules=no", "--set-upstream", "origin", "HEAD:refs/heads/"+target.Branch); err != nil {
		return nil, fmt.Errorf("CAO changes committed as %s, but push failed; the local commit is retained. Follow your repository's publication or pull-request process; branch protections were not bypassed: %w", commit, err)
	}
	result = map[string]any{
		"status": "published", "commit": commit, "actionsStatus": "pending",
		"actionsURL": target.URL + "/actions",
		"repository": repository, "branch": target.Branch,
	}
	return result, nil
}

func setupPublishUnchanged(cmd *cobra.Command, index, head, remoteHead, branch, tree, pushURL string, files []string) error {
	staged, err := setupGit(cmd, "", "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return err
	}
	currentHead, err := setupPublishHead(cmd)
	if err != nil {
		return err
	}
	currentBranch, err := setupGit(cmd, "", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return err
	}
	currentURL, err := setupGit(cmd, "", "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	if currentURL != pushURL {
		return errors.New("origin changed after review; setup did not commit or push")
	}
	currentRemote, err := setupPublishRemoteHead(cmd, branch, pushURL)
	if err != nil {
		return err
	}
	if staged != "" || currentHead != head || currentRemote != remoteHead || strings.TrimSpace(currentBranch) != branch {
		return errors.New("the Git state changed after review; setup did not commit or push")
	}
	for _, filename := range files {
		if _, err := setupDestination(filename); err != nil {
			return err
		}
	}
	if _, err := setupGit(cmd, index, append([]string{"add", "--"}, files...)...); err != nil {
		return err
	}
	currentTree, err := setupGit(cmd, index, "write-tree")
	if err != nil {
		return err
	}
	if currentTree != tree {
		return errors.New("CAO files changed after review; setup did not commit or push")
	}
	return nil
}
