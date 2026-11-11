package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/logger"
)

var brokerLog = logger.New("cao:tokenbroker")

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

func (s *Service) authorizedIdentity(i admission.Identity) bool {
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
	return true
}

func (s *Service) authorized(ctx context.Context, i admission.Identity) bool {
	if !s.authorizedIdentity(i) {
		return false
	}
	// Read on every operation. A changed or removed mounted policy immediately
	// denies authority instead of retaining a stale authorization snapshot.
	data, err := readPolicyFile(s.config.PolicyFile)
	if err != nil {
		brokerLog.PrintfContext(ctx, "policy denied: reason=unreadable")
		return false
	}
	rules, err := parseWorkflowRules(data)
	if err != nil {
		brokerLog.PrintfContext(ctx, "policy denied: reason=invalid_policy")
		return false
	}
	if !ruleAllows(rules, i) {
		brokerLog.PrintfContext(ctx, "policy denied: reason=no_matching_rule rules=%d", len(rules))
		return false
	}
	return true
}

var errPolicy = errors.New("invalid token broker policy")

// readPolicyFile reads the mounted policy, rejecting files above the 1 MiB bound.
func readPolicyFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-configured mounted policy path, never request input.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errPolicy
	}
	return data, nil
}

// parseWorkflowRules strictly decodes the copilot-requests workflow rules and
// fails closed on duplicate members, unknown shapes, duplicates, or unsafe values.
func parseWorkflowRules(data []byte) ([]workflowRule, error) {
	if uniqueJSON(data) != nil {
		return nil, errPolicy
	}
	var root, control, broker, requests map[string]json.RawMessage
	var workflows []json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil ||
		json.Unmarshal(root["control-plane"], &control) != nil || control == nil ||
		json.Unmarshal(control["token-broker"], &broker) != nil || len(broker) != 1 ||
		json.Unmarshal(broker["copilot-requests"], &requests) != nil || len(requests) != 1 ||
		json.Unmarshal(requests["workflows"], &workflows) != nil || len(workflows) > 128 {
		return nil, errPolicy
	}
	rules := make([]workflowRule, 0, len(workflows))
	seen := make(map[workflowRule]bool, len(workflows))
	for _, raw := range workflows {
		var rule workflowRule
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 ||
			fields["repository-id"] == nil || fields["owner-id"] == nil ||
			fields["workflow-ref"] == nil || fields["workflow-sha"] == nil ||
			json.Unmarshal(raw, &rule) != nil || seen[rule] {
			return nil, errPolicy
		}
		seen[rule] = true
		if !stableID.MatchString(rule.RepositoryID) || !stableID.MatchString(rule.OwnerID) ||
			!validWorkflowRef(rule.WorkflowRef) || !revision.MatchString(rule.WorkflowSHA) {
			return nil, errPolicy
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// ruleAllows reports whether an exact rule matches the verified identity.
func ruleAllows(rules []workflowRule, i admission.Identity) bool {
	for _, rule := range rules {
		if rule.RepositoryID == i.RepositoryID && rule.OwnerID == i.OwnerID &&
			rule.WorkflowRef == i.WorkflowRef && rule.WorkflowSHA == i.WorkflowSHA {
			return true
		}
	}
	return false
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
