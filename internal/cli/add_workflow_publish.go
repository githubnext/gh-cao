package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/github/gh-aw/pkg/parser"
	"github.com/spf13/cobra"
)

func workflowAddSnapshot(cmd *cobra.Command) (map[string]string, error) {
	dirty, err := setupGit(cmd, "", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".github", ".gitattributes")
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	records := strings.Split(strings.TrimSuffix(dirty, "\x00"), "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		if record == "" {
			continue
		}
		if len(record) < 4 {
			return nil, errors.New("unable to read Git working-tree status")
		}
		name := record[3:]
		if strings.ContainsAny(record[:2], "RC") {
			return nil, errors.New("finish pending renames or copies before guided workflow installation")
		}
		info, err := os.Lstat(name)
		var content []byte
		switch {
		case errors.Is(err, os.ErrNotExist):
			content = []byte("deleted")
		case err != nil:
			return nil, err
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(name)
			if err != nil {
				return nil, err
			}
			content = []byte(target)
		case info.Mode().IsRegular():
			content, err = os.ReadFile(name)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported working-tree entry: %s", name)
		}
		result[name] = fmt.Sprintf("%s:%x", record[:2], sha256.Sum256(content))
	}
	return result, nil
}

func workflowAddFiles(before, after map[string]string, name string) ([]string, string) {
	var files []string
	for filename, state := range after {
		if before[filename] == state {
			continue
		}
		if _, dirty := before[filename]; dirty {
			return nil, "installation changed a file with pre-existing edits: " + filename
		}
		if filename != ".gitattributes" && !strings.HasPrefix(filename, ".github/") {
			return nil, "changes outside the workflow installation require manual review: " + filename
		}
		files = append(files, filename)
	}
	if len(files) == 0 {
		return nil, ""
	}
	for filename := range before {
		if strings.HasPrefix(filename, ".github/workflows/shared/") || filename == ".github/workflows/cao-runtime.yml" {
			return nil, "shared runtime already had uncommitted changes: " + filename
		}
	}
	for _, filename := range []string{policyPath, ".github/workflows/" + name + ".md", ".github/workflows/" + name + ".lock.yml"} {
		if _, dirty := before[filename]; dirty {
			return nil, "workflow or policy already had uncommitted changes: " + filename
		}
		files = append(files, filename)
	}
	slices.Sort(files)
	return slices.Compact(files), ""
}

func finishWorkflowAdd(cmd *cobra.Command, gh ghExecutor, prompt nativeSetupPrompt, name string, before map[string]string, result map[string]any) error {
	after, err := workflowAddSnapshot(cmd)
	if err != nil {
		return err
	}
	files, blocked := workflowAddFiles(before, after, name)
	if blocked != "" {
		result["publication"], err = setupPublishBlocked(cmd, blocked)
		return err
	}
	if len(files) == 0 {
		result["publication"] = map[string]any{"status": "unchanged"}
		return nil
	}
	repository, err := nativeSetupGH(cmd, gh, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return err
	}
	if !repositoryPattern.MatchString(repository) {
		return errors.New("unable to determine control repository for publication")
	}
	publication, err := publishCAOChanges(cmd, gh, prompt, repository, files,
		"Commit and push these CAO workflow changes?", "Add CAO workflow "+name,
		"Pushing publishes the workflow and policy; scheduled or event triggers may start work. A first manual run requires separate approval.")
	if err != nil {
		return err
	}
	result["publication"] = publication
	if publication["status"] != "published" {
		return nil
	}
	firstRun, err := offerWorkflowFirstRun(cmd, gh, prompt, name, publication)
	result["firstRun"] = firstRun
	if err != nil {
		return fmt.Errorf("workflow published, but its first run was not completed: %w", err)
	}
	return err
}

func offerWorkflowFirstRun(cmd *cobra.Command, gh ghExecutor, prompt nativeSetupPrompt, name string, publication map[string]any) (map[string]any, error) {
	declined := map[string]any{"status": "not-requested"}
	repository, _ := publication["repository"].(string)
	commit, _ := publication["commit"].(string)
	branch, _ := publication["branch"].(string)
	if !repositoryPattern.MatchString(repository) || !commitPattern.MatchString(commit) || branch == "" {
		return nil, errors.New("first run requires a verified published workflow revision")
	}
	pushURL, err := setupGit(cmd, "", "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return nil, err
	}
	remote, err := setupPublishRemote(pushURL, repository)
	if err != nil {
		return nil, err
	}
	head, err := setupPublishRemoteHead(cmd, branch, pushURL)
	if err != nil {
		return nil, err
	}
	if head != commit {
		return nil, errors.New("remote branch changed after publication; no first run requested")
	}
	var workflow struct {
		Path  string `json:"path"`
		State string `json:"state"`
	}
	if err := nativeSetupJSON(cmd, gh, &workflow, "api", "--hostname", remote.Hostname(), "repos/"+repository+"/actions/workflows/"+name+".lock.yml"); err != nil {
		return nil, fmt.Errorf("published workflow is not available for a first run yet; no run requested: %w", err)
	}
	if workflow.Path != ".github/workflows/"+name+".lock.yml" || workflow.State != "active" {
		return nil, errors.New("published workflow is not active at its expected path; no run requested")
	}
	content, err := setupGit(cmd, "", "show", commit+":.github/workflows/"+name+".md")
	if err != nil {
		return nil, err
	}
	frontmatter, err := parser.ExtractFrontmatterFromContent(content)
	if err != nil {
		return nil, err
	}
	triggers, _ := frontmatter.Frontmatter["on"].(map[string]any)
	dispatch, manual := triggers["workflow_dispatch"]
	if !manual {
		err := nativeSetupNote(cmd, "No first run offered: the workflow does not declare workflow_dispatch.\n")
		return map[string]any{"status": "unavailable"}, err
	}
	config, _ := dispatch.(map[string]any)
	inputs, _ := config["inputs"].(map[string]any)
	if _, explicitReview := inputs["safe_output_mode"]; !explicitReview {
		err := nativeSetupNote(cmd, "No first run offered: this workflow has no explicit safe_output_mode input; review and run it manually.\n")
		return map[string]any{"status": "unavailable"}, err
	}
	values := map[string]string{}
	var keys []string
	for key := range inputs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		definition, _ := inputs[key].(map[string]any)
		switch key {
		case "safe_output_mode":
			values[key] = "review"
		case "safe_output_repo":
			values[key] = repository
		case "max_repos":
			values[key] = "1"
		default:
			if definition["required"] != true && key != "target_repo" {
				continue
			}
			fallback := ""
			if value, exists := definition["default"]; exists {
				fallback = fmt.Sprint(value)
			}
			value, err := prompt.Text("First run input "+key, fallback)
			if errors.Is(err, errNativeSetupCancelled) {
				return declined, nil
			}
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("first run requires input %s; no run requested", key)
			}
			values[key] = value
		}
	}
	policyContent, err := setupGit(cmd, "", "show", commit+":"+policyPath)
	if err != nil {
		return nil, err
	}
	policy, err := nativeDecodeJSON([]byte(policyContent))
	if err != nil {
		return nil, err
	}
	document, ok := policy.(map[string]any)
	if !ok {
		return nil, errors.New("published CAO policy must be an object")
	}
	if err := workflowFirstRunPolicy(document, name, repository, values["target_repo"]); err != nil {
		return nil, err
	}
	if err := nativeSetupNote(cmd, "First run: %s at %s, review mode, target %s, outputs in %s. This can spend AI credits and create outputs.\n", name, commit, values["target_repo"], repository); err != nil {
		return nil, err
	}
	approved, err := prompt.Confirm("Run this published workflow now?", false)
	if errors.Is(err, errNativeSetupCancelled) || err == nil && !approved {
		return declined, nil
	}
	if err != nil {
		return nil, err
	}
	head, err = setupPublishRemoteHead(cmd, branch, pushURL)
	if err != nil {
		return nil, err
	}
	if head != commit {
		return nil, errors.New("remote branch changed after publication; no first run requested")
	}
	args := []string{"aw", "run", ".github/workflows/" + name + ".md", "--ref", branch, "--json"}
	for _, key := range keys {
		if value, exists := values[key]; exists {
			args = append(args, "--raw-field", key+"="+value)
		}
	}
	stdout, stderr, err := gh(cmd.Context(), args...)
	if err != nil {
		return nil, commandError("first run failed or dispatch result is unknown; inspect Actions before retrying", stderr, err)
	}
	runID, err := workflowActivatedRunID(stdout, stderr, remote.Hostname(), repository)
	if err != nil {
		return nil, err
	}
	var run struct {
		HeadSHA string `json:"head_sha"`
		Path    string `json:"path"`
		Event   string `json:"event"`
		URL     string `json:"html_url"`
	}
	if err := nativeSetupJSON(cmd, gh, &run, "api", "--hostname", remote.Hostname(), fmt.Sprintf("repos/%s/actions/runs/%d", repository, runID)); err != nil {
		return nil, fmt.Errorf("first run was requested but its revision could not be verified; inspect Actions before retrying: %w", err)
	}
	if run.HeadSHA != commit || strings.SplitN(run.Path, "@", 2)[0] != workflow.Path || run.Event != "workflow_dispatch" {
		return nil, errors.New("first run does not match the reviewed published revision; inspect Actions immediately; no retry was attempted")
	}
	return map[string]any{"status": "requested", "commit": commit, "runID": runID, "runURL": run.URL}, nil
}

func workflowActivatedRunID(stdout, stderr, host, repository string) (int64, error) {
	var dispatched []struct {
		Status string `json:"status"`
	}
	unknown := errors.New("first run dispatch result is unknown; inspect Actions before retrying")
	if err := json.Unmarshal([]byte(stdout), &dispatched); err != nil || len(dispatched) != 1 || dispatched[0].Status != "triggered" {
		return 0, unknown
	}
	// gh-aw's JSON summary does not carry the activated run ID. Only trust the
	// native dispatch URL, before gh-aw can print a latest-run tracking fallback.
	native, _, marked := strings.Cut(stderr, "Triggered workflow:")
	pattern := regexp.MustCompile(regexp.QuoteMeta("https://"+host+"/"+repository+"/actions/runs/") + `([1-9][0-9]*)\b`)
	matches := pattern.FindAllStringSubmatch(native, -1)
	if !marked || len(matches) != 1 {
		return 0, unknown
	}
	runID, err := strconv.ParseInt(matches[0][1], 10, 64)
	if err != nil {
		return 0, unknown
	}
	return runID, nil
}

func workflowFirstRunPolicy(policy map[string]any, name, repository, target string) error {
	if err := nativeValidatePolicy(policy); err != nil {
		return err
	}
	control, _ := policy["control-plane"].(map[string]any)
	workflows, _ := control["workflows"].(map[string]any)
	entry, ok := workflows[name].(map[string]any)
	if !ok || entry["enabled"] == false {
		return errors.New("standalone workflow is unregistered or disabled; no first run requested")
	}
	if target != "" && !repositoryPattern.MatchString(target) {
		return errors.New("first run target_repo must be an exact OWNER/REPO")
	}
	scope, _ := control["scope"].(map[string]any)
	for _, coordinate := range []string{repository, target} {
		if coordinate == "" {
			continue
		}
		for key, identity := range map[string]string{"allowed-owners": strings.SplitN(coordinate, "/", 2)[0], "allowed-repositories": coordinate} {
			values, exists := scope[key].([]any)
			if !exists || !slices.ContainsFunc(values, func(value any) bool {
				return strings.EqualFold(fmt.Sprint(value), identity)
			}) {
				return fmt.Errorf("%s is outside explicit CAO scope; no first run requested", coordinate)
			}
		}
	}
	return nil
}
