package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	cao "github.com/githubnext/gh-cao"
)

const modernizationResultPrefix = "CAO_MODERNIZATION_RESULT:"
const modernizationPlanPath = ".github/cao/modernization.json"

type modernizationDeps struct {
	gh  ghExecutor
	git func(context.Context, ...string) (string, error)
}

func modernizationCommand(gh ghExecutor) *cobra.Command {
	root := &cobra.Command{Use: "modernization", Short: "Review and operate one explicitly selected modernization control repository"}
	deps := modernizationDeps{gh: gh, git: func(ctx context.Context, args ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", args...)
		command.Env = dashboardDisableEnvironment(os.Environ())
		content, err := command.Output()
		return strings.TrimSpace(string(content)), err
	}}
	for _, operation := range []string{"status", "preview", "execute"} {
		command := &cobra.Command{Use: operation, Args: cobra.NoArgs}
		command.Flags().String("repo", "", "Exact selected control repository")
		if operation == "preview" {
			command.Flags().String("action", "", "assess, approve-plan, run, or verify")
			command.Flags().String("mode", "review", "Requested ceiling: review or live")
		}
		if operation == "execute" {
			command.Flags().String("preview", "", "Unexpired single-use native preview identity")
		}
		command.RunE = func(cmd *cobra.Command, _ []string) error {
			repository, _ := cmd.Flags().GetString("repo")
			action, _ := cmd.Flags().GetString("action")
			mode, _ := cmd.Flags().GetString("mode")
			preview, _ := cmd.Flags().GetString("preview")
			var plan map[string]any
			if operation == "preview" && action == "approve-plan" {
				content, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 400001))
				if err != nil {
					return err
				}
				if len(content) > 400000 {
					return errors.New("reviewed plan input exceeds the command input bound")
				}
				value, err := nativeDecodeJSON(content)
				if err != nil {
					return err
				}
				var ok bool
				plan, ok = value.(map[string]any)
				if !ok {
					return errors.New("reviewed plan must be a JSON object")
				}
			}
			result, err := modernizationOperation(cmd.Context(), deps, repository, operation, action, mode, preview, plan)
			if err != nil {
				result = map[string]any{
					"schema": "cao-modernization-operator/v1", "kind": operation, "status": "unavailable",
					"controlRepository": repository, "repository": repository, "accepted": false,
					"reason": err.Error(),
				}
				var unconfirmed *modernizationUnconfirmedError
				if errors.As(err, &unconfirmed) {
					result["status"] = "unconfirmed"
					delete(result, "accepted")
				}
			}
			content, encodeErr := json.Marshal(result)
			if encodeErr == nil {
				_, encodeErr = fmt.Fprintln(cmd.OutOrStdout(), modernizationResultPrefix+string(content))
			}
			return errors.Join(err, encodeErr)
		}
		root.AddCommand(command)
	}
	return root
}

type modernizationState struct {
	result         map[string]any
	document       map[string]any
	approved       map[string]any
	candidates     []map[string]any
	repositories   []string
	metadata       map[string]any
	sha            string
	branch         string
	runtimeChecked bool
	runtimeError   error
	specialists    []string
}

func (d modernizationDeps) readJSON(ctx context.Context, endpoint string, target any) error {
	content, _, err := d.gh(ctx, "api", "--hostname", "github.com", endpoint)
	if err != nil {
		return fmt.Errorf("required GitHub read failed for the selected modernization control: %w", err)
	}
	if len(content) > 2<<20 {
		return errors.New("modernization response exceeds the read bound")
	}
	return json.Unmarshal([]byte(content), target)
}

func (d modernizationDeps) file(ctx context.Context, repository, revision, path string) ([]byte, bool, error) {
	var tree struct {
		Tree      []struct{ Path, Type string }
		Truncated bool
	}
	// A directory inventory distinguishes absence from permission/API failures.
	if err := d.readJSON(ctx, "repos/"+repository+"/git/trees/"+url.PathEscape(revision)+"?recursive=1", &tree); err != nil {
		return nil, false, err
	}
	if tree.Truncated {
		return nil, false, errors.New("modernization control file inventory is incomplete")
	}
	found := false
	for _, entry := range tree.Tree {
		if entry.Path == path && entry.Type == "blob" {
			found = true
		}
	}
	if !found {
		return nil, false, nil
	}
	var response struct {
		Content, Encoding string
		Size              int
	}
	if err := d.readJSON(ctx, "repos/"+repository+"/contents/"+path+"?ref="+url.QueryEscape(revision), &response); err != nil {
		return nil, false, err
	}
	if response.Encoding != "base64" || response.Size > 524288 {
		return nil, false, errors.New("modernization requires bounded regular repository files")
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	return content, true, err
}

func modernizationOperation(ctx context.Context, deps modernizationDeps, repository, operation, action, mode, preview string, plan map[string]any) (map[string]any, error) {
	repository = strings.ToLower(repository)
	if !repositoryPattern.MatchString(repository) || strings.Contains(repository, "..") || !nativeRepositoryOperationsAllowed(repository) {
		return nil, errors.New("modernization requires an exact permitted control repository")
	}
	if operation == "execute" {
		return modernizationExecute(ctx, deps, repository, preview)
	}
	if operation != "status" && operation != "preview" {
		return nil, errors.New("unsupported modernization operation")
	}
	if operation == "preview" && (!slices.Contains([]string{"assess", "approve-plan", "run", "verify"}, action) ||
		!slices.Contains([]string{"review", "live"}, mode) || (action != "run" && mode != "review") ||
		(action == "approve-plan") != (plan != nil)) {
		return nil, errors.New("invalid modernization action, mode, or concrete plan input")
	}
	state, err := modernizationLoad(ctx, deps, repository, operation == "status")
	if err != nil {
		return nil, err
	}
	if operation == "status" {
		return state.result, nil
	}
	return modernizationPreview(ctx, deps, state, action, mode, plan, true)
}

func modernizationLoad(ctx context.Context, deps modernizationDeps, repository string, capabilities bool) (*modernizationState, error) {
	origin, err := deps.git(ctx, "remote", "get-url", "origin")
	if err != nil {
		return nil, errors.New("modernization requires the owning native control checkout")
	}
	expected := "https://github.com/" + repository
	if !strings.EqualFold(strings.TrimSuffix(origin, ".git"), expected) &&
		!strings.EqualFold(strings.TrimSuffix(origin, ".git"), "git@github.com:"+repository) &&
		!strings.EqualFold(strings.TrimSuffix(origin, ".git"), "ssh://git@github.com/"+repository) {
		return nil, errors.New("selected control repository does not match the native checkout")
	}
	state := &modernizationState{metadata: map[string]any{}}
	var actor struct {
		Login, Type string
		ID          int64
	}
	if err := deps.readJSON(ctx, "user", &actor); err != nil {
		return nil, err
	}
	if actor.Type != "User" || actor.Login == "" || actor.ID <= 0 {
		return nil, errors.New("modernization requires the current authenticated GitHub user")
	}
	if err := deps.readJSON(ctx, "repos/"+repository, &state.metadata); err != nil {
		return nil, err
	}
	permissions, _ := state.metadata["permissions"].(map[string]any)
	if !strings.EqualFold(nativeText(state.metadata["full_name"]), repository) || permissions["push"] != true ||
		state.metadata["archived"] != false || state.metadata["disabled"] != false {
		return nil, errors.New("current GitHub user requires write access to the exact active control repository")
	}
	state.branch = nativeText(state.metadata["default_branch"])
	var head struct{ SHA string }
	if state.branch == "" {
		return nil, errors.New("control default branch is unavailable")
	}
	if err := deps.readJSON(ctx, "repos/"+repository+"/commits/"+url.PathEscape(state.branch), &head); err != nil {
		return nil, err
	}
	if !nativeCommitSHA.MatchString(head.SHA) {
		return nil, errors.New("control revision is not an exact commit")
	}
	state.sha = head.SHA
	policy, exists, err := deps.file(ctx, repository, state.sha, policyPath)
	if err != nil || !exists {
		return nil, errors.Join(err, errors.New("reviewed control policy is required"))
	}
	request := func(string, map[string]any) (map[string]any, error) {
		return nil, errors.New("policy validation cannot make network requests")
	}
	if err := modernizationEvaluate(ctx, string(policy), request, "policy.parsePolicy(input)", &state.document); err != nil {
		return nil, err
	}
	control, _ := state.document["control-plane"].(map[string]any)
	scope, _ := control["scope"].(map[string]any)
	// No organization discovery, wildcard expansion, or canonical-data authority.
	for _, value := range nativeArray(scope["allowed-repositories"]) {
		name := strings.ToLower(nativeText(value))
		if !repositoryPattern.MatchString(name) || strings.Contains(name, "..") || !nativeRepositoryOperationsAllowed(name) {
			return nil, errors.New("modernization requires a bounded exact reviewed repository scope")
		}
		state.repositories = append(state.repositories, name)
	}
	if len(state.repositories) == 0 || len(state.repositories) > 10 {
		return nil, errors.New("modernization requires 1 through 10 exact reviewed repositories")
	}
	identities := map[float64]bool{}
	for _, name := range state.repositories {
		var metadata map[string]any
		if err := deps.readJSON(ctx, "repos/"+name, &metadata); err != nil {
			return nil, err
		}
		id, _ := metadata["id"].(float64)
		if !strings.EqualFold(nativeText(metadata["full_name"]), name) || metadata["archived"] != false || metadata["disabled"] != false ||
			id <= 0 || identities[id] {
			return nil, errors.New("complete modernization scope contains a redirected or unavailable repository")
		}
		identities[id] = true
		state.candidates = append(state.candidates, metadata)
	}
	slices.SortFunc(state.candidates, func(a, b map[string]any) int {
		left, _ := a["id"].(float64)
		right, _ := b["id"].(float64)
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return strings.Compare(nativeText(a["full_name"]), nativeText(b["full_name"]))
	})
	content, exists, err := deps.file(ctx, repository, state.sha, modernizationPlanPath)
	if err != nil {
		return nil, err
	}
	if exists {
		value, err := nativeDecodeJSON(content)
		if err != nil {
			return nil, err
		}
		state.approved, _ = value.(map[string]any)
		if state.approved == nil {
			return nil, errors.New("approved modernization plan is not an object")
		}
	}
	state.result = map[string]any{
		"schema": "cao-modernization-operator/v1", "kind": "status", "status": "available",
		"repository": repository, "controlRepository": repository, "controlSha": state.sha,
		"actor": actor.Login, "actorId": actor.ID,
		"repositories": state.repositories, "plan": state.approved, "planDigest": nil,
		"tasks": []any{}, "evidence": map[string]any{},
		"links": map[string]string{"issuesUrl": expected + "/issues", "pullRequestsUrl": expected + "/pulls", "workflowUrl": expected + "/actions/workflows/modernization.lock.yml"},
	}
	if state.approved != nil {
		var digest string
		if err := modernizationEvaluate(ctx, state.approved, request, "engagement.planDigest(input)", &digest); err != nil {
			return nil, err
		}
		state.result["planDigest"] = digest
		tasks := []map[string]any{}
		for _, item := range nativeArray(state.approved["tasks"]) {
			task, _ := item.(map[string]any)
			tasks = append(tasks, map[string]any{"id": task["id"], "repository": task["repository"], "title": task["title"], "phase": "unavailable", "state": "unavailable"})
		}
		state.result["tasks"] = tasks
	}
	if !capabilities {
		return state, nil
	}
	authorized := map[string]any{}
	for _, action := range []string{"assess", "approve-plan", "run", "verify"} {
		candidate := state.approved
		if action == "approve-plan" {
			candidate = nil
		}
		result, err := modernizationPreview(ctx, deps, state, action, "review", candidate, false)
		if err != nil {
			authorized[action] = map[string]any{"authorized": false, "reason": err.Error()}
			continue
		}
		authorized[action] = map[string]any{"authorized": result["canExecute"], "reason": result["reason"]}
		if action == "run" && result["statusTasks"] != nil {
			state.result["tasks"], state.result["evidence"], state.result["planDigest"] = result["statusTasks"], result["evidence"], result["planDigest"]
		}
	}
	state.result["capabilities"] = authorized
	return state, nil
}

func modernizationPreview(ctx context.Context, deps modernizationDeps, state *modernizationState, action, mode string, plan map[string]any, persist bool) (map[string]any, error) {
	repository := nativeText(state.result["controlRepository"])
	result := map[string]any{
		"schema": "cao-modernization-operator/v1", "kind": "preview", "status": "unavailable",
		"repository": repository, "controlRepository": repository, "controlSha": state.sha,
		"action": action, "mode": mode, "planDigest": nil, "repositories": state.repositories,
		"actor": state.result["actor"], "actorId": state.result["actorId"],
		"tasks": []any{}, "effects": []string{}, "warnings": []string{},
		"canExecute": false, "reason": "", "evidence": map[string]any{},
	}
	if action != "approve-plan" {
		plan = state.approved
	}
	// Policy/plan errors are domain unavailability, not successful no-op previews.
	fail := func(err error) (map[string]any, error) {
		result["reason"] = err.Error()
		return result, nil
	}
	standalone := action == "assess" || action == "verify"
	worker := action == "run"
	var resolved map[string]any
	expression := `policy.effectivePolicy(input.document,{controlRepository:input.repository,
role:input.standalone?"standalone":"orchestrator",workflowName:input.standalone?"modernization-system":"",
campaignName:input.standalone?"":"modernization",requestedMode:input.mode,eventName:"workflow_dispatch"})`
	denyNetwork := func(string, map[string]any) (map[string]any, error) {
		return nil, errors.New("network unavailable in policy evaluation")
	}
	if err := modernizationEvaluate(ctx, map[string]any{
		"document": state.document, "repository": repository, "standalone": standalone, "mode": mode,
	}, denyNetwork, expression, &resolved); err != nil {
		return fail(err)
	}
	if resolved["authorized"] != true {
		return fail(fmt.Errorf("reviewed policy: %s", nativeText(resolved["reason"])))
	}
	maximum, _ := resolved["max_repositories"].(float64)
	if len(state.repositories) > int(maximum) {
		return fail(errors.New("the complete engagement exceeds the reviewed repository cap"))
	}
	control, _ := state.document["control-plane"].(map[string]any)
	// Non-default inventory partitions would select a different complete system.
	if inventory, ok := control["inventory"].(map[string]any); ok {
		for key, value := range inventory {
			number, _ := value.(float64)
			if (key == "cell-count" && number != 1) || (key == "cell-index" && number != 0) ||
				(key == "batch-index" && number != 0) || (key == "batch-size" && number < float64(len(state.repositories))) ||
				(key == "max-scan-repositories" && number < float64(len(state.repositories))) {
				return fail(errors.New("reviewed inventory partition cannot admit the complete modernization system"))
			}
		}
	}
	rollout, _ := resolved["rollout_percent"].(float64)
	if standalone && rollout < 100 {
		return fail(errors.New("system assessment/verification requires the complete scope inside the checked-in rollout"))
	}
	candidates := make([]map[string]any, 0, len(state.candidates))
	for _, metadata := range state.candidates {
		name := nativeText(metadata["full_name"])
		var check any
		if err := modernizationEvaluate(ctx, map[string]any{"name": name, "resolved": resolved}, denyNetwork,
			`(authority.validateTargetScope(input.name,{owners:input.resolved.allowed_owners,repositories:input.resolved.allowed_repositories}),true)`, &check); err != nil {
			return fail(err)
		}
		effective := mode
		targets, _ := resolved["target_policies"].(map[string]any)
		target, _ := targets[strings.ToLower(name)].(map[string]any)
		if target["mode"] == "review" {
			effective = "review"
		}
		destination := repository
		if effective == "live" {
			destination = name
		}
		if mode == "live" && effective != "live" {
			return fail(errors.New("requested live batch exceeds a checked-in target ceiling"))
		}
		candidate := map[string]any{}
		for key, value := range metadata {
			candidate[key] = value
		}
		candidate["safe_output_mode"], candidate["safe_output_repo"] = effective, destination
		candidates = append(candidates, candidate)
	}
	if standalone {
		var scope any
		if err := modernizationEvaluate(ctx, map[string]any{
			"precompute": map[string]any{"authorized": true, "control_role": "standalone", "workflow": "modernization-system",
				"candidate_repositories": candidates, "effective_max_repos": int(maximum)},
			"repositories": strings.Join(state.repositories, ","),
		}, denyNetwork, `engagement.systemReadScope(input.precompute,input.repositories,"app")`, &scope); err != nil {
			return fail(err)
		}
	}
	if action == "assess" && state.approved != nil {
		return fail(errors.New("an approved plan exists; use verification or explicitly review a replacement plan"))
	}
	if action == "verify" && plan == nil || worker && plan == nil {
		return fail(errors.New("a concrete approved modernization plan is required"))
	}
	roster, err := cao.OperatorSources.ReadDir("modernization/squad/.squad/agents")
	if err != nil {
		return nil, err
	}
	var specialists []string
	for _, entry := range roster {
		if entry.IsDir() {
			specialists = append(specialists, entry.Name())
		}
	}
	if plan != nil {
		if state.specialists == nil {
			var entries []struct{ Name, Type string }
			if err := deps.readJSON(ctx, "repos/"+repository+"/contents/modernization/squad/.squad/agents?ref="+url.QueryEscape(state.sha), &entries); err != nil {
				return fail(err)
			}
			state.specialists = []string{}
			for _, entry := range entries {
				if entry.Type == "dir" && slices.Contains(specialists, entry.Name) {
					state.specialists = append(state.specialists, entry.Name)
				}
			}
		}
		var digest string
		if err := modernizationEvaluate(ctx, map[string]any{"plan": plan, "repositories": state.repositories, "specialists": state.specialists}, denyNetwork,
			`engagement.planDigest(engagement.validatePlan(input.plan,input.repositories,input.specialists))`, &digest); err != nil {
			return fail(err)
		}
		result["planDigest"] = digest
		planRepositories := []string{}
		for _, entry := range nativeArray(plan["repositories"]) {
			value, _ := entry.(map[string]any)
			planRepositories = append(planRepositories, nativeText(value["repository"]))
		}
		result["repositories"] = planRepositories
		if action == "verify" && len(planRepositories) != len(state.repositories) {
			return fail(errors.New("verification requires the approved plan's complete admitted system scope"))
		}
	}
	if action == "approve-plan" {
		if err := modernizationCleanCheckout(ctx, deps, state.sha); err != nil {
			return fail(err)
		}
		if plan == nil {
			// Status advertises the capability; approval preview requires input.
			result["canExecute"], result["status"] = true, "available"
			return result, nil
		}
		if result["planDigest"] != nil && state.approved != nil {
			var oldDigest string
			if err := modernizationEvaluate(ctx, state.approved, denyNetwork, "engagement.planDigest(input)", &oldDigest); err != nil {
				return fail(err)
			}
			if oldDigest == result["planDigest"] {
				return fail(errors.New("this exact plan is already approved"))
			}
			result["warnings"] = []string{"Replacement plan outputs use a new digest; prior task issues and pull requests are not adopted."}
		}
		result["effects"] = []string{"Create one control-repository engagement commit using expected-head compare-and-swap; no policy, installation, merge, or deployment changes."}
	} else {
		if !state.runtimeChecked {
			state.runtimeChecked = true
			for _, path := range []string{"modernization/engagement.mjs", "modernization/github.mjs", "modernization/actions.mjs"} {
				expected, err := cao.OperatorSources.ReadFile(path)
				if err != nil {
					return nil, err
				}
				actual, exists, err := deps.file(ctx, repository, state.sha, path)
				if err != nil {
					state.runtimeError = err
					break
				}
				if !exists || !bytes.Equal(expected, actual) {
					state.runtimeError = errors.New("installed modernization runtime differs from this native operator; update the reviewed package and CLI together")
					break
				}
			}
		}
		if state.runtimeError != nil {
			return fail(state.runtimeError)
		}
		workflow := "modernization"
		if standalone {
			workflow = "modernization-system"
		}
		source, exists, err := deps.file(ctx, repository, state.sha, ".github/workflows/"+workflow+".md")
		if err != nil {
			return fail(err)
		}
		if !exists || !modernizationWorkflowFenced(source, false) {
			return fail(errors.New("installed workflow lacks the reviewed-SHA dispatch fence; update the reviewed package first"))
		}
		lock, exists, err := deps.file(ctx, repository, state.sha, ".github/workflows/"+workflow+".lock.yml")
		if err != nil {
			return fail(err)
		}
		if !exists || !modernizationWorkflowFenced(lock, true) {
			return fail(errors.New("installed generated workflow lacks the reviewed-SHA dispatch fence"))
		}
		if worker && (!strings.Contains(string(source), "expected_batch_digest") || !strings.Contains(string(lock), "expected_batch_digest")) {
			return fail(errors.New("installed workflow lacks reviewed task-batch binding"))
		}
		var workflowMetadata struct{ State string }
		if err := deps.readJSON(ctx, "repos/"+repository+"/actions/workflows/"+workflow+".lock.yml", &workflowMetadata); err != nil {
			return fail(err)
		}
		if workflowMetadata.State != "active" {
			return fail(errors.New("the exact modernization workflow is not active"))
		}
		if worker {
			workers, _ := resolved["worker_policies"].(map[string]any)
			task, _ := workers["modernization-task"].(map[string]any)
			if task["enabled"] != true || mode == "live" && task["max_mode"] != nil && task["max_mode"] != "live" {
				return fail(errors.New("modernization-task worker is disabled or its mode ceiling forbids the batch"))
			}
			var taskWorkflow struct{ State string }
			if err := deps.readJSON(ctx, "repos/"+repository+"/actions/workflows/modernization-task.lock.yml", &taskWorkflow); err != nil {
				return fail(err)
			}
			if taskWorkflow.State != "active" {
				return fail(errors.New("modernization task workflow is not active"))
			}
			allowed := append(slices.Clone(state.repositories), repository)
			request := func(route string, parameters map[string]any) (map[string]any, error) {
				return modernizationAPIRequest(ctx, deps.gh, allowed, route, parameters)
			}
			var evidence map[string]any
			if err := modernizationEvaluate(ctx, map[string]any{
				"plan": plan, "digest": result["planDigest"], "candidates": candidates, "repository": repository,
			}, request, "await evidence.collectEvidence(input.plan,input.digest,input.candidates,client,client,input.repository)", &evidence); err != nil {
				return fail(err)
			}
			limit := min(3, int(maximum))
			percentCap := max(1, int((float64(len(candidates))*rollout+99)/100))
			limit = min(limit, percentCap)
			var readiness map[string]any
			if err := modernizationEvaluate(ctx, map[string]any{"plan": plan, "evidence": evidence, "candidates": candidates, "maximum": limit}, denyNetwork,
				"engagement.readyTasks(input.plan,input.evidence,input.candidates,input.maximum)", &readiness); err != nil {
				return fail(err)
			}
			tasks := []map[string]any{}
			for _, entry := range nativeArray(readiness["ready"]) {
				ready, _ := entry.(map[string]any)
				for _, item := range nativeArray(plan["tasks"]) {
					task, _ := item.(map[string]any)
					if task["id"] == ready["task"] {
						tasks = append(tasks, map[string]any{"id": task["id"], "repository": task["repository"], "title": task["title"], "phase": ready["phase"], "state": "ready"})
					}
				}
			}
			result["tasks"], result["evidence"], result["readiness"] = tasks, evidence, readiness
			statusTasks := []map[string]any{}
			for _, item := range nativeArray(plan["tasks"]) {
				task, _ := item.(map[string]any)
				entry, _ := evidence[nativeText(task["id"])].(map[string]any)
				phase, status := "issue", "blocked"
				if entry["issue"] != nil || entry["active_pull"] == true || entry["complete"] == true {
					phase = "implement"
				}
				if entry["complete"] == true {
					status = "complete"
				} else if entry["active_pull"] == true {
					status = "in-review"
				} else if entry["closed_issue"] == true {
					status = "closed-issue"
				}
				for _, ready := range tasks {
					if ready["id"] == task["id"] {
						status = "ready"
					}
				}
				statusTasks = append(statusTasks, map[string]any{
					"id": task["id"], "repository": task["repository"], "title": task["title"], "phase": phase, "state": status,
					"issueUrl": entry["issue_url"], "pullRequestUrl": entry["pull_url"],
				})
			}
			result["statusTasks"] = statusTasks
			var batchDigest string
			if err := modernizationEvaluate(ctx, readiness["ready"], denyNetwork, "engagement.planDigest(input)", &batchDigest); err != nil {
				return fail(err)
			}
			result["batchDigest"] = batchDigest
			if len(tasks) == 0 {
				return fail(errors.New("no tasks are ready from independently collected issue, pull-request, review and check evidence"))
			}
		}
		result["workflow"] = workflow
		result["effects"] = []string{"Dispatch the exact reviewed workflow with its expected control SHA; acceptance does not mean work completed."}
		result["warnings"] = append(result["warnings"].([]string), "If the branch advances, pre-activation rejects the dispatch. Workflow admission rechecks policy and credentials.")
	}
	result["canExecute"], result["status"] = true, "available"
	if persist {
		if err := modernizationSavePreview(repository, state, result, plan); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func modernizationCleanCheckout(ctx context.Context, deps modernizationDeps, sha string) error {
	status, err := deps.git(ctx, "status", "--porcelain", "--untracked-files=all")
	if err != nil || status != "" {
		return errors.New("plan approval requires a clean control checkout; unrelated local work will not be overwritten")
	}
	conflict, err := deps.git(ctx, "status", "--porcelain", "--untracked-files=all", "--ignored=matching", "--", modernizationPlanPath)
	if err != nil || conflict != "" {
		return errors.New("the local engagement path contains uncommitted or ignored work; approval will not overwrite it")
	}
	head, err := deps.git(ctx, "rev-parse", "HEAD")
	if err != nil || head != sha {
		return errors.New("control checkout does not match the reviewed remote head")
	}
	return nil
}
