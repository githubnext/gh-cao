package tokenbroker

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/admission"
)

var (
	stableID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	revision = regexp.MustCompile(`^[a-f0-9]{40}$`)
	workflow = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9._-]*/\.github/workflows/[A-Za-z0-9_-][A-Za-z0-9._-]*\.ya?ml@refs/(heads|tags)/[A-Za-z0-9_-]+([./][A-Za-z0-9_-]+)*$`)
)

type workflowRule struct {
	RepositoryID string `json:"repository-id"`
	OwnerID      string `json:"owner-id"`
	WorkflowRef  string `json:"workflow-ref"`
	WorkflowSHA  string `json:"workflow-sha"`
}

func (s *Service) authorized(i admission.Identity) bool {
	if !stableID.MatchString(i.RepositoryID) || !stableID.MatchString(i.OwnerID) ||
		!stableID.MatchString(i.RunID) || !stableID.MatchString(i.RunAttempt) ||
		!stableID.MatchString(i.CheckRunID) || i.EnvironmentPresent || i.OwnerID != s.config.OwnerID ||
		i.Issuer != admission.Issuer || !revision.MatchString(i.WorkflowSHA) ||
		!validWorkflowRef(i.WorkflowRef) ||
		!strings.HasPrefix(i.WorkflowRef, i.Repository+"/.github/workflows/") {
		return false
	}
	_, ref, ok := strings.Cut(i.WorkflowRef, "@")
	owner, repository, _ := strings.Cut(i.Repository, "/")
	legacySubject := "repo:" + i.Repository + ":ref:" + ref
	immutableSubject := "repo:" + owner + "@" + i.OwnerID + "/" + repository + "@" + i.RepositoryID + ":ref:" + ref
	if !ok || i.Ref != ref || (i.Subject != legacySubject && i.Subject != immutableSubject) {
		return false
	}
	switch i.Event {
	case "workflow_dispatch", "schedule", "push":
	default:
		return false
	}
	// Read on every operation. A changed or removed mounted policy immediately
	// denies authority instead of retaining a stale authorization snapshot.
	f, err := os.Open(s.config.PolicyFile)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || uniqueJSON(data) != nil {
		return false
	}
	var root, control, broker, requests map[string]json.RawMessage
	var workflows []json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil ||
		json.Unmarshal(root["control-plane"], &control) != nil || control == nil ||
		json.Unmarshal(control["token-broker"], &broker) != nil || len(broker) != 1 ||
		json.Unmarshal(broker["copilot-requests"], &requests) != nil || len(requests) != 1 ||
		json.Unmarshal(requests["workflows"], &workflows) != nil || len(workflows) > 128 {
		return false
	}
	allowed := false
	seen := make(map[workflowRule]bool, len(workflows))
	for _, raw := range workflows {
		var rule workflowRule
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 ||
			fields["repository-id"] == nil || fields["owner-id"] == nil ||
			fields["workflow-ref"] == nil || fields["workflow-sha"] == nil ||
			json.Unmarshal(raw, &rule) != nil || seen[rule] {
			return false
		}
		seen[rule] = true
		if !stableID.MatchString(rule.RepositoryID) || !stableID.MatchString(rule.OwnerID) ||
			!validWorkflowRef(rule.WorkflowRef) || !revision.MatchString(rule.WorkflowSHA) {
			return false
		}
		if rule.RepositoryID == i.RepositoryID && rule.OwnerID == i.OwnerID &&
			rule.WorkflowRef == i.WorkflowRef && rule.WorkflowSHA == i.WorkflowSHA {
			allowed = true
		}
	}
	return allowed
}

func validWorkflowRef(value string) bool {
	if !workflow.MatchString(value) || strings.Contains(value, "..") {
		return false
	}
	_, ref, _ := strings.Cut(value, "@")
	for _, segment := range strings.Split(ref, "/") {
		if strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	return true
}
