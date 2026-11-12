package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	ghterm "github.com/cli/go-gh/v2/pkg/term"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Streaming commands are separate from ghExecutor so credentials are never
// passed on the command line or captured in diagnostic output.
type nativeSetupExecKey struct{}
type nativeSetupExecFunc func(*cobra.Command, string, io.Reader, ...string) (string, error)

func nativeSetupExec(cmd *cobra.Command, name string, input io.Reader, args ...string) (string, error) {
	if run, ok := cmd.Context().Value(nativeSetupExecKey{}).(nativeSetupExecFunc); ok {
		return run(cmd, name, input, args...)
	}
	child := exec.CommandContext(cmd.Context(), name, args...)
	child.Stdin = input
	if input == nil {
		child.Stdin = cmd.InOrStdin()
	}
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = cmd.ErrOrStderr()
	if name == "gh" && len(args) > 0 && args[0] == "secret" {
		child.Stdout = cmd.ErrOrStderr()
	}
	if err := child.Run(); err != nil {
		return "", fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	return output.String(), nil
}

func nativeSetupFlag(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

func nativeSetupBool(cmd *cobra.Command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

func nativeSetupGH(cmd *cobra.Command, gh ghExecutor, args ...string) (string, error) {
	out, stderr, err := gh(cmd.Context(), args...)
	if err != nil {
		return "", commandError("gh "+strings.Join(args, " ")+" failed", stderr, err)
	}
	return strings.TrimSpace(out), nil
}

func nativeSetupJSON(cmd *cobra.Command, gh ghExecutor, target any, args ...string) error {
	out, err := nativeSetupGH(cmd, gh, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), target); err != nil {
		return fmt.Errorf("gh %s returned invalid JSON", strings.Join(args, " "))
	}
	return nil
}

func nativeSetupReadPolicy(path string) (map[string]any, error) {
	if path == policyPath {
		return readPolicy()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var policy map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("%s contains invalid JSON: %w", path, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s must contain one JSON object", path)
	}
	version, ok := policy["version"].(json.Number)
	number, numberErr := version.Float64()
	if policy == nil || !ok || numberErr != nil || number != 1 {
		return nil, fmt.Errorf("%s must declare version 1", path)
	}
	if value, exists := policy["control-plane"]; exists {
		control, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s control-plane must be an object", path)
		}
		for _, key := range []string{"campaigns", "workflows", "operations"} {
			if value, exists := control[key]; exists {
				if _, ok := value.(map[string]any); !ok {
					return nil, fmt.Errorf("%s control-plane.%s must be an object", path, key)
				}
			}
		}
	}
	return policy, nil
}

func nativeSetupMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func nativeSetupStrings(value any) ([]string, bool) {
	if values, ok := value.([]string); ok {
		return values, true
	}
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		result[i] = text
	}
	return result, true
}

func nativeSetupUnique(values []string, insensitive bool) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		key := value
		if insensitive {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

type nativeSetupRepository struct {
	Repository string `json:"nameWithOwner"`
	Visibility string `json:"visibility"`
}

var nativeSetupCoordinate = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var nativeSetupRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.*-]+/[A-Za-z0-9_.*-]+$`)

func nativeSetupInspect(cmd *cobra.Command, gh ghExecutor, repo string) (nativeSetupRepository, error) {
	var result nativeSetupRepository
	err := nativeSetupJSON(cmd, gh, &result, "repo", "view", repo, "--json", "nameWithOwner,visibility")
	if err != nil {
		return result, err
	}
	if !nativeSetupCoordinate.MatchString(result.Repository) || result.Visibility == "" {
		return result, fmt.Errorf("unable to inspect %s: incomplete repository data", repo)
	}
	result.Visibility = strings.ToLower(result.Visibility)
	return result, nil
}

var errNativeSetupCancelled = errors.New("setup cancelled")

type nativeSetupStageError struct {
	cause       error
	stage       string
	policySaved bool
}

func (e *nativeSetupStageError) Error() string {
	if e.policySaved {
		return fmt.Sprintf("%s\nConfiguration was saved; run gh cao doctor and review the installed files.", e.cause)
	}
	return fmt.Sprintf("%s\nSetup stopped during %s; the local policy was not changed. Runtime files may have been prepared, and credentials may already have changed; inspect them before retrying.", e.cause, e.stage)
}

func (e *nativeSetupStageError) Unwrap() error { return e.cause }

type nativeSetupPrompt interface {
	Text(string, string) (string, error)
	Confirm(string, bool) (bool, error)
}

type nativeSetupPromptKey struct{}
type nativeSetupTerminalPrompt struct {
	reader *bufio.Reader
	output io.Writer
	styled bool
}

func (p *nativeSetupTerminalPrompt) Text(label, fallback string) (string, error) {
	suffix := ""
	if fallback != "" {
		suffix = " [" + fallback + "]"
	}
	prefix := ""
	if p.styled {
		prefix = "\x1b[36m?\x1b[0m "
		if suffix != "" {
			suffix = "\x1b[2m" + suffix + "\x1b[0m"
		}
	}
	if _, err := fmt.Fprintf(p.output, "%s%s%s: ", prefix, label, suffix); err != nil {
		return "", err
	}
	answer, err := p.reader.ReadString('\n')
	if err != nil {
		return "", errNativeSetupCancelled
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = fallback
	}
	return answer, nil
}

func (p *nativeSetupTerminalPrompt) Confirm(label string, defaultYes bool) (bool, error) {
	fallback := "n"
	if defaultYes {
		fallback = "y"
	}
	answer, err := p.Text(label+" (y/n)", fallback)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), err
}

func nativeSetupInteractive(cmd *cobra.Command) bool {
	input, inOK := cmd.InOrStdin().(*os.File)
	output, outOK := cmd.ErrOrStderr().(*os.File)
	return inOK && outOK && term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd())) && os.Getenv("CI") == ""
}

func nativeSetupGetPrompt(cmd *cobra.Command) nativeSetupPrompt {
	if prompt, ok := cmd.Context().Value(nativeSetupPromptKey{}).(nativeSetupPrompt); ok {
		return prompt
	}
	if !nativeSetupInteractive(cmd) {
		return nil
	}
	return &nativeSetupTerminalPrompt{
		reader: bufio.NewReader(cmd.InOrStdin()), output: cmd.ErrOrStderr(),
		styled: nativeSetupStyled(cmd),
	}
}

func nativeSetupStyled(cmd *cobra.Command) bool {
	return nativeSetupColorEnabled(cmd.ErrOrStderr())
}

func nativeSetupColorEnabled(writer io.Writer) bool {
	output, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(output.Fd())) &&
		os.Getenv("NO_COLOR") == "" && os.Getenv("ACCESSIBLE") == "" && os.Getenv("CI") == "" &&
		ghterm.FromEnv().IsColorEnabled()
}

func nativeSetup(cmd *cobra.Command, gh ghExecutor) error {
	result, err := nativeSetupControl(cmd, gh, nativeSetupGetPrompt(cmd))
	if errors.Is(err, errNativeSetupCancelled) {
		var staged *nativeSetupStageError
		if errors.As(err, &staged) {
			if writeErr := nativeSetupNote(cmd, "%s\n", staged); writeErr != nil {
				return writeErr
			}
		}
		result = map[string]any{"command": "setup", "cancelled": true}
		err = nil
	}
	if err != nil {
		return err
	}
	if nativeSetupBool(cmd, "json") {
		return printJSON(cmd, result)
	}
	if result["cancelled"] == true {
		return nil
	}
	publication := nativeSetupMap(result["publication"])
	if publication["status"] == "published" {
		_, err = io.WriteString(cmd.OutOrStdout(), setupPublishOutput(publication, nativeSetupColorEnabled(cmd.OutOrStdout())))
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), "Monitoring configured locally. No commit or push was performed.\nNext: gh cao doctor, then review, commit and push the installed files.\nActions produces operational artifacts for the local or cloud dashboard.\nOptional automation: https://githubnext.github.io/gh-cao/operation-patterns/\n")
	return err
}

func nativeSetupBoundedPolicy(policy map[string]any, repo string) error {
	control := nativeSetupMap(policy["control-plane"])
	campaigns := nativeSetupMap(control["campaigns"])
	if campaigns == nil || len(campaigns) != 0 {
		return errors.New("non-interactive setup requires a bare control plane with no declared campaigns")
	}
	if value, exists := control["workflows"]; exists {
		if workflows := nativeSetupMap(value); workflows == nil || len(workflows) != 0 {
			return errors.New("non-interactive setup requires a bare control plane with no declared standalone workflows")
		}
	}
	if value, exists := control["operations"]; exists {
		if operations := nativeSetupMap(value); operations == nil || len(operations) != 0 {
			return errors.New("non-interactive setup requires a bare control plane with no declared operations")
		}
	}
	exact := func(value any, expected string) bool {
		values, ok := nativeSetupStrings(value)
		return ok && len(values) == 1 && strings.EqualFold(values[0], expected)
	}
	if raw, exists := control["scope"]; exists {
		scope := nativeSetupMap(raw)
		if scope == nil || len(scope) != 2 || !exact(scope["allowed-owners"], strings.Split(repo, "/")[0]) || !exact(scope["allowed-repositories"], repo) {
			return errors.New("non-interactive setup refuses to replace existing repository scope; run setup from an interactive terminal to review it")
		}
	}
	if raw, exists := control["activity"]; exists {
		activity := nativeSetupMap(raw)
		if activity == nil || !exact(activity["repositories"], repo) {
			return errors.New("non-interactive setup refuses to replace existing Activity scope; run setup from an interactive terminal to review it")
		}
	}
	return nil
}

func nativeSetupControl(cmd *cobra.Command, gh ghExecutor, prompt nativeSetupPrompt) (result map[string]any, err error) {
	if err := setupCheckoutRoot(cmd.Context()); err != nil {
		return nil, err
	}
	requested, _ := cmd.Flags().GetStringArray("repository")
	publicOnly := len(requested) > 0
	if publicOnly {
		prompt = nil
	}
	if prompt == nil {
		if err := nativeSetupNote(cmd, "Applying non-interactive setup defaults for the current public control repository.\n"); err != nil {
			return nil, err
		}
	}
	if _, err = nativeSetupGH(cmd, gh, "auth", "status"); err != nil {
		return nil, fmt.Errorf("GitHub CLI authentication check failed; run gh auth login: %w", err)
	}
	repo, err := nativeSetupGH(cmd, gh, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return nil, err
	}
	if !nativeSetupCoordinate.MatchString(repo) {
		return nil, errors.New("unable to determine control repository; run setup from a GitHub repository checkout")
	}
	controlRepo, err := nativeSetupInspect(cmd, gh, repo)
	if err != nil {
		return nil, err
	}
	repo = controlRepo.Repository
	if _, err := setupDestination(policyPath); err != nil {
		return nil, err
	}
	policy, err := readPolicy()
	createPolicy := errors.Is(err, os.ErrNotExist)
	if createPolicy {
		if _, statErr := os.Lstat(policyPath); !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("setup refuses an unreadable existing policy: %s", policyPath)
		}
		policy, err = initialPolicy(repo, ""), nil
	}
	if err != nil {
		return nil, fmt.Errorf("CAO policy is invalid; fix the existing policy before rerunning setup: %w", err)
	}
	control := nativeSetupMap(policy["control-plane"])
	if prompt == nil && nativeSetupMap(control["activity"])["public-only"] == true {
		publicOnly = true
	}
	campaigns := nativeSetupMap(control["campaigns"])
	if campaigns == nil || len(campaigns) != 0 {
		return nil, errors.New("setup only configures a bare control plane with no declared campaigns")
	}
	if value, exists := control["workflows"]; exists {
		if workflows := nativeSetupMap(value); workflows == nil || len(workflows) != 0 {
			return nil, errors.New("setup only configures a bare control plane with no declared standalone workflows")
		}
	}
	if value, exists := control["operations"]; exists {
		if operations := nativeSetupMap(value); operations == nil || len(operations) != 0 {
			return nil, errors.New("setup only configures a bare control plane with no declared operations")
		}
	}
	owner := strings.Split(controlRepo.Repository, "/")[0]
	repositories := []string{controlRepo.Repository}
	owners := []string{owner}
	profile := "workflow-token"
	var authOptions nativeSetupAuthOptions
	if prompt == nil {
		if controlRepo.Visibility != "public" {
			return nil, fmt.Errorf("non-interactive setup requires a public control repository; %s is %s", repo, controlRepo.Visibility)
		}
		if err = nativeSetupBoundedPolicy(policy, controlRepo.Repository); err != nil && (!publicOnly || nativeSetupMap(control["activity"])["public-only"] != true) {
			return nil, err
		}
		if publicOnly {
			if len(requested) == 0 && nativeSetupMap(control["activity"])["public-only"] == true {
				requested, _ = nativeSetupStrings(nativeSetupMap(control["activity"])["repositories"])
			}
			inspected, e := nativeSetupExpand(cmd, gh, controlRepo, strings.Join(requested, ","), true)
			if e != nil {
				return nil, e
			}
			repositories, owners = []string{}, []string{}
			for _, candidate := range inspected {
				if candidate.Visibility != "public" {
					return nil, fmt.Errorf("public-only monitoring cannot include %s (%s)", candidate.Repository, candidate.Visibility)
				}
				repositories = append(repositories, candidate.Repository)
				owners = append(owners, strings.Split(candidate.Repository, "/")[0])
			}
			owners = nativeSetupUnique(owners, true)
			profile = "public-only"
			if err := nativeSetupNote(cmd, "Public-only monitoring scope: %s\nNo App/PAT or agent execution.\n", strings.Join(repositories, ", ")); err != nil {
				return nil, err
			}
		}
	} else {
		if err := nativeSetupNote(cmd, "Control: %s (%s)\nMonitoring only; no workflows run.\n", repo, controlRepo.Visibility); err != nil {
			return nil, err
		}
		answer, e := prompt.Text("Repositories (comma-separated; * saves exact names)", repo)
		if e != nil {
			return nil, e
		}
		inspected, e := nativeSetupExpand(cmd, gh, controlRepo, answer)
		if e != nil {
			return nil, e
		}
		repositories, owners = []string{}, []string{}
		for _, inspectedRepo := range inspected {
			if controlRepo.Visibility == "public" && !strings.EqualFold(inspectedRepo.Repository, repo) && inspectedRepo.Visibility != "public" {
				return nil, errors.New("a public control repository cannot be configured for non-public repository evidence; use a private control repository")
			}
			repositories = append(repositories, inspectedRepo.Repository)
			owners = append(owners, strings.Split(inspectedRepo.Repository, "/")[0])
		}
		owners = nativeSetupUnique(owners, false)
		appProfile := "github-app"
		if len(owners) != 1 {
			appProfile = "enterprise-app"
		}
		publicTargets := controlRepo.Visibility == "public" || controlRepo.Visibility == "private" || controlRepo.Visibility == "internal"
		for _, candidate := range inspected {
			if !strings.EqualFold(candidate.Repository, repo) && candidate.Visibility != "public" {
				publicTargets = false
			}
		}
		if publicTargets {
			profile = "public-only"
			if err := nativeSetupNote(cmd, "Monitoring the control repository and public targets: built-in Actions token; no App/PAT. Restricted evidence is partial.\n"); err != nil {
				return nil, err
			}
		} else {
			profile, e = prompt.Text("Authentication ("+appProfile+", approved-app, token)", appProfile)
			if e != nil {
				return nil, e
			}
			if profile != appProfile && profile != "approved-app" && profile != "token" {
				return nil, fmt.Errorf("authentication profile is not available for this repository scope: %s", profile)
			}
		}
		authOptions = nativeSetupAuthOptions{repo: repo, policy: policyPath, write: []string{repo}, expires: "30"}
		if profile == "approved-app" || profile == "enterprise-app" {
			if authOptions.readClient, err = prompt.Text("Read App client ID", ""); err != nil {
				return nil, err
			}
			if authOptions.writeClient, err = prompt.Text("Write App client ID", ""); err != nil {
				return nil, err
			}
			if authOptions.readClient == "" || authOptions.writeClient == "" {
				return nil, errors.New("existing App setup requires both client IDs")
			}
		}
		writeScope := repo
		if profile == "public-only" {
			writeScope = "none (monitoring only)"
		}
		if err := nativeSetupNote(cmd, "Read: %s\nWrite: %s\nAuth: %s\nSetting up CAO runtime and policy, and credentials if needed.\n", strings.Join(repositories, ", "), writeScope, profile); err != nil {
			return nil, err
		}
		apply, e := prompt.Confirm("Apply this setup?", false)
		if e != nil {
			return nil, e
		}
		if !apply {
			return map[string]any{"command": "setup", "cancelled": true}, nil
		}
	}
	stage := "runtime preparation"
	policySaved := false
	defer func() {
		if err != nil {
			err = &nativeSetupStageError{cause: err, stage: stage, policySaved: policySaved}
		}
	}()
	files, err := setupRuntime(cmd, gh)
	if err != nil {
		return nil, err
	}
	if createPolicy {
		policy["gh-aw-version"], err = nativeVersion(cmd.Context(), gh)
		if err != nil {
			return nil, err
		}
	}
	stage = "credentials"
	authentication := map[string]any{"mode": "workflow-token", "repository": repo}
	if prompt == nil || profile == "public-only" {
		if err = nativeSetupWorkflowToken(cmd, gh, repo); err != nil {
			return nil, err
		}
	} else {
		if err := nativeSetupNote(cmd, "Configuring credentials...\n"); err != nil {
			return nil, err
		}
		// Pass the intended policy in memory: no secret or expanded policy needs
		// to be written to a global temporary directory.
		originalScope := control["scope"]
		control["scope"] = map[string]any{"allowed-owners": owners, "allowed-repositories": repositories}
		method := profile
		if method == "approved-app" {
			method = "github-app"
		}
		authentication, err = nativeSetupAuthenticate(cmd, gh, method, authOptions, policy)
		control["scope"] = originalScope
		if err != nil {
			return nil, err
		}
	}
	stage = "policy"
	if err := nativeSetupNote(cmd, "Saving CAO policy...\n"); err != nil {
		return nil, err
	}
	control["scope"] = map[string]any{"allowed-owners": owners, "allowed-repositories": repositories}
	if prompt == nil || profile == "public-only" {
		activity := nativeSetupMap(control["activity"])
		if activity == nil {
			activity = map[string]any{}
		}
		activity["repositories"] = repositories
		if profile == "public-only" {
			activity["public-only"] = true
		}
		control["activity"] = activity
	} else if activity := nativeSetupMap(control["activity"]); activity["public-only"] == true {
		activity["repositories"] = repositories
		delete(activity, "public-only")
	}
	if err = writePolicy(policy, createPolicy); err != nil {
		return nil, err
	}
	policySaved = true
	result = map[string]any{
		"command": "setup", "controlRepository": repo, "visibility": controlRepo.Visibility,
		"repositories": repositories, "profile": profile, "authentication": authentication,
		"campaigns": []string{},
	}
	stage = "optional publication"
	result["publication"], err = setupPublish(cmd, gh, prompt, repo, append(files, policyPath))
	if err != nil {
		return result, err
	}
	if prompt == nil {
		result["nonInteractive"] = true
		if err := nativeSetupNote(cmd, "Non-interactive setup applied to %s.\nRead and Activity scope: %s\nAuthentication: repository GITHUB_TOKEN (workflow-token)\nEvidence: Actions operational artifacts for the local or cloud dashboard\nCampaigns: none\n", repo, strings.Join(repositories, ", ")); err != nil {
			// The policy has been saved at this point; do not imply rollback.
			return result, nativeErrorf("Configuration was saved, but completion could not be displayed; run gh cao doctor and review the installed files")
		}
	}
	return result, nil
}

func nativeSetupMatches(pattern, repo string) bool {
	expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
	matched, _ := regexp.MatchString("(?i)"+expression, repo)
	return matched
}

func nativeSetupExpand(cmd *cobra.Command, gh ghExecutor, control nativeSetupRepository, answer string, publicOnly ...bool) ([]nativeSetupRepository, error) {
	result := []nativeSetupRepository{control}
	var organizations []string
	organizationsLoaded := false
	cache := map[string][]nativeSetupRepository{}
	for _, pattern := range strings.Split(answer, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if pattern == "*" {
			pattern = "*/*"
		}
		if !nativeSetupRepositoryPattern.MatchString(pattern) {
			return nil, fmt.Errorf("repository must use owner/repository format with optional * wildcards: %s", pattern)
		}
		if len(publicOnly) > 0 && publicOnly[0] && strings.Contains(strings.Split(pattern, "/")[0], "*") {
			return nil, errors.New("public-only quickstart accepts exact OWNER/REPO or OWNER/* patterns")
		}
		if !strings.Contains(pattern, "*") {
			if !strings.EqualFold(pattern, control.Repository) {
				repo, err := nativeSetupInspect(cmd, gh, pattern)
				if err != nil {
					return nil, err
				}
				result = append(result, repo)
			}
			continue
		}
		owner := strings.Split(pattern, "/")[0]
		owners := []string{owner}
		if strings.Contains(owner, "*") {
			if !organizationsLoaded {
				for page := 1; page <= 10; page++ {
					var entries []struct{ Login string }
					if err := nativeSetupJSON(cmd, gh, &entries, "api", fmt.Sprintf("/user/orgs?per_page=100&page=%d", page)); err != nil {
						return nil, err
					}
					if entries == nil {
						return nil, errors.New("GitHub returned invalid organization data")
					}
					for _, entry := range entries {
						if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(entry.Login) {
							return nil, errors.New("GitHub returned incomplete organization data")
						}
						organizations = append(organizations, entry.Login)
					}
					if len(organizations) >= 1000 {
						return nil, errors.New("organization list reached 1000; use a narrower pattern")
					}
					if len(entries) < 100 {
						break
					}
				}
				organizationsLoaded = true
			}
			owners = nil
			for _, candidate := range organizations {
				if nativeSetupMatches(owner+"/*", candidate+"/*") {
					owners = append(owners, candidate)
				}
			}
			if len(owners) == 0 {
				return nil, fmt.Errorf("organization pattern %s matched no accessible organizations", owner)
			}
		}
		matches := []nativeSetupRepository{}
		for _, owner := range owners {
			key := strings.ToLower(owner)
			repos, cached := cache[key]
			if !cached {
				if err := nativeSetupJSON(cmd, gh, &repos, "repo", "list", owner, "--json", "nameWithOwner,visibility", "--limit", "1000"); err != nil {
					return nil, err
				}
				if repos == nil || len(repos) >= 1000 {
					return nil, fmt.Errorf("unable to expand wildcard for %s: invalid repository data or repository list reached 1000; enter exact repository names", owner)
				}
				for i := range repos {
					if !nativeSetupCoordinate.MatchString(repos[i].Repository) || !strings.EqualFold(strings.Split(repos[i].Repository, "/")[0], owner) || repos[i].Visibility == "" {
						return nil, errors.New("gh repo list returned incomplete repository data")
					}
					repos[i].Visibility = strings.ToLower(repos[i].Visibility)
				}
				cache[key] = repos
			}
			for _, repo := range repos {
				if nativeSetupMatches(pattern, repo.Repository) && (len(publicOnly) == 0 || !publicOnly[0] || repo.Visibility == "public") {
					matches = append(matches, repo)
				}
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("repository pattern %s matched no accessible repositories", pattern)
		}
		sort.Slice(matches, func(i, j int) bool { return matches[i].Repository < matches[j].Repository })
		result = append(result, matches...)
	}
	seen := map[string]bool{}
	unique := []nativeSetupRepository{}
	for _, repo := range result {
		key := strings.ToLower(repo.Repository)
		if !seen[key] {
			unique = append(unique, repo)
			seen[key] = true
		}
	}
	return unique, nil
}

func nativeSetupNote(cmd *cobra.Command, format string, args ...any) error {
	if nativeSetupStyled(cmd) {
		message := strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")
		for _, line := range strings.Split(message, "\n") {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "\x1b[36m>\x1b[0m \x1b[2m%s\x1b[0m\n", line); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := fmt.Fprintf(cmd.ErrOrStderr(), format, args...)
	return err
}
