package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var modernizationPreviewID = regexp.MustCompile(`^[a-f0-9]{48}$`)

type modernizationUnconfirmedError struct{ message string }

func (e *modernizationUnconfirmedError) Error() string { return e.message }

type modernizationReview struct {
	Repository string         `json:"repository"`
	Expires    time.Time      `json:"expires"`
	Binding    string         `json:"binding"`
	Result     map[string]any `json:"result"`
	Plan       map[string]any `json:"plan"`
}

func modernizationPreviewDirectory() (string, error) {
	directory := os.Getenv("CAO_MODERNIZATION_PREVIEW_DIR")
	if directory == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		directory = filepath.Join(cache, "gh-cao", "modernization-previews")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("modernization previews require a private native directory")
	}
	return directory, nil
}

func modernizationReviewBinding(state *modernizationState, result map[string]any, plan map[string]any) (string, error) {
	content, err := json.Marshal(map[string]any{"sha": state.sha, "policy": state.document, "result": result, "plan": plan})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func modernizationSavePreview(repository string, state *modernizationState, result map[string]any, plan map[string]any) error {
	directory, err := modernizationPreviewDirectory()
	if err != nil {
		return err
	}
	binding, err := modernizationReviewBinding(state, result, plan)
	if err != nil {
		return err
	}
	identity := make([]byte, 24)
	if _, err := rand.Read(identity); err != nil {
		return err
	}
	id := hex.EncodeToString(identity)
	expires := time.Now().UTC().Add(5 * time.Minute)
	review := modernizationReview{Repository: repository, Expires: expires, Binding: binding, Result: result, Plan: plan}
	content, err := json.Marshal(review)
	if err != nil {
		return err
	}
	filename := filepath.Join(directory, id+".json")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err := modernizationSyncDirectory(directory); err != nil {
		return err
	}
	// Review contents above exclude these transport-only identity fields.
	result["previewId"], result["expiresAt"] = id, expires.Format(time.RFC3339)
	return nil
}

func modernizationExecute(ctx context.Context, deps modernizationDeps, repository, id string) (map[string]any, error) {
	if !modernizationPreviewID.MatchString(id) {
		return nil, errors.New("execution requires an exact native preview identity")
	}
	directory, err := modernizationPreviewDirectory()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(directory, id+".used")); err == nil {
		return nil, errors.New("modernization preview was already used; no automatic retry is permitted")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	filename := filepath.Join(directory, id+".json")
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, errors.New("modernization preview is unavailable or already consumed")
	}
	var review modernizationReview
	if err := json.Unmarshal(content, &review); err != nil {
		return nil, err
	}
	if review.Repository != repository || time.Now().After(review.Expires) {
		return nil, errors.New("modernization preview is expired or belongs to another control repository")
	}
	state, err := modernizationLoad(ctx, deps, repository, false)
	if err != nil {
		return nil, err
	}
	action, mode := nativeText(review.Result["action"]), nativeText(review.Result["mode"])
	fresh, err := modernizationPreview(ctx, deps, state, action, mode, review.Plan, false)
	if err != nil {
		return nil, err
	}
	binding, err := modernizationReviewBinding(state, fresh, review.Plan)
	if err != nil {
		return nil, err
	}
	if fresh["canExecute"] != true || binding != review.Binding {
		return nil, errors.New("modernization plan, policy, control revision, scope or task evidence changed; review a fresh preview")
	}
	// Exclusive persistent claim precedes the first remote mutation. Any uncertain
	// write remains consumed: neither queue retry nor process restart replays it.
	claim, err := os.OpenFile(filepath.Join(directory, id+".used"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, errors.New("modernization preview was already used; no automatic retry is permitted")
	}
	if err := errors.Join(claim.Sync(), claim.Close()); err != nil {
		return nil, err
	}
	if err := modernizationSyncDirectory(directory); err != nil {
		return nil, err
	}
	result := map[string]any{
		"schema": "cao-modernization-operator/v1", "kind": "execution", "status": "accepted",
		"repository": repository, "controlRepository": repository, "controlSha": state.sha,
		"action": action, "accepted": true,
	}

	if action == "approve-plan" {
		if err := modernizationCleanCheckout(ctx, deps, state.sha); err != nil {
			return nil, err
		}
		plan, err := json.MarshalIndent(review.Plan, "", "  ")
		if err != nil {
			return nil, err
		}
		query := `mutation($input:CreateCommitOnBranchInput!){createCommitOnBranch(input:$input){commit{url oid}}}`
		payload := map[string]any{"query": query, "variables": map[string]any{"input": map[string]any{
			"branch":          map[string]any{"repositoryNameWithOwner": repository, "branchName": state.branch},
			"expectedHeadOid": state.sha,
			"message":         map[string]any{"headline": "Approve reviewed modernization engagement", "body": "Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>"},
			"fileChanges": map[string]any{"additions": []map[string]string{{
				"path": modernizationPlanPath, "contents": base64.StdEncoding.EncodeToString(append(plan, '\n')),
			}}},
		}}}
		output, err := modernizationWrite(ctx, deps, "graphql", payload)
		if err != nil {
			return nil, err
		}
		var response struct {
			Data struct {
				CreateCommitOnBranch struct {
					Commit struct{ URL, OID string }
				}
			}
			Errors []any
		}
		if err := json.Unmarshal([]byte(output), &response); err != nil || len(response.Errors) != 0 ||
			!nativeCommitSHA.MatchString(response.Data.CreateCommitOnBranch.Commit.OID) ||
			!strings.HasPrefix(response.Data.CreateCommitOnBranch.Commit.URL, "https://github.com/"+repository+"/commit/") {
			return nil, &modernizationUnconfirmedError{"approval outcome is unconfirmed; preview consumed, inspect control repository before further work"}
		}
		result["approvalCommitUrl"] = response.Data.CreateCommitOnBranch.Commit.URL
		return result, nil
	}
	workflow := nativeText(fresh["workflow"])
	inputs := map[string]string{
		"expected_control_sha": state.sha, "safe_output_mode": mode, "safe_output_repo": repository,
		"max_repos": fmt.Sprint(len(state.repositories)),
	}
	if workflow == "modernization-system" {
		inputs["target_repos"] = strings.Join(state.repositories, ",")
	} else {
		inputs["expected_batch_digest"] = nativeText(fresh["batchDigest"])
	}
	if _, err := modernizationWrite(ctx, deps, "repos/"+repository+"/actions/workflows/"+workflow+".lock.yml/dispatches",
		map[string]any{"ref": state.branch, "inputs": inputs}); err != nil {
		return nil, err
	}
	result["workflowUrl"] = "https://github.com/" + repository + "/actions/workflows/" + workflow + ".lock.yml"
	result["status"] = "dispatch-accepted"
	return result, nil
}

func modernizationSyncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func modernizationWrite(ctx context.Context, deps modernizationDeps, endpoint string, payload any) (string, error) {
	file, err := os.CreateTemp("", ".cao-modernization-request-*.json")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := json.NewEncoder(file).Encode(payload); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	output, _, err := deps.gh(ctx, "api", "--hostname", "github.com", "--method", "POST", endpoint, "--input", file.Name())
	if err != nil {
		return "", &modernizationUnconfirmedError{"modernization mutation was not confirmed; preview consumed, inspect remote state before making a new request"}
	}
	return output, nil
}
