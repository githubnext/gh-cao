package tokenbroker

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMalformedPolicyAlwaysDenies(t *testing.T) {
	tests := []string{
		`{}`, `null`, `[]`, `{`,
		`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":null}}}}`,
		`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[],"workflows":[]}}}}`,
		`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[null]}}}}`,
		strings.Repeat(" ", (1<<20)+1),
	}
	f := newFixture(t)
	for _, body := range tests {
		if err := os.WriteFile(f.policy, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if w := f.call(exchangeBody); w.Code != 403 || f.keyReads != 0 {
			t.Fatal("malformed policy failed open")
		}
	}
}

func TestPolicyRuleRejectsUnsafeAuthority(t *testing.T) {
	f := newFixture(t)
	for _, edit := range []func(map[string]string){
		func(r map[string]string) { r["repository-id"] = "*" },
		func(r map[string]string) { r["repository-id"] = "0" },
		func(r map[string]string) { r["repository-id"] = "01" },
		func(r map[string]string) { r["owner-id"] = strings.Repeat("1", 21) },
		func(r map[string]string) { r["workflow-sha"] = strings.Repeat("A", 40) },
		func(r map[string]string) {
			r["workflow-ref"] = "acme/project/.github/workflows/../triage.lock.yml@refs/heads/main"
		},
		func(r map[string]string) {
			r["workflow-ref"] = "acme/project/.github/workflows/triage.lock.yml@refs/heads/feature..evil"
		},
		func(r map[string]string) {
			r["workflow-ref"] = "acme/project/.github/workflows/*.lock.yml@refs/heads/main"
		},
		func(r map[string]string) { r["extra-authority"] = "ignored" },
	} {
		rule := map[string]string{
			"repository-id": f.identity.RepositoryID, "owner-id": f.identity.OwnerID,
			"workflow-ref": f.identity.WorkflowRef, "workflow-sha": f.identity.WorkflowSHA,
		}
		edit(rule)
		data, _ := json.Marshal(map[string]any{"control-plane": map[string]any{"token-broker": map[string]any{"copilot-requests": map[string]any{"workflows": []any{rule}}}}})
		if err := os.WriteFile(f.policy, data, 0600); err != nil {
			t.Fatal(err)
		}
		if w := f.call(exchangeBody); w.Code != 403 || f.keyReads != 0 {
			t.Fatal("unsafe policy rule authorized issuance")
		}
	}
}

func TestExactTagRefProfile(t *testing.T) {
	f := newFixture(t)
	f.identity.Ref = "refs/tags/release-1.0"
	f.identity.Subject = "repo:acme/project:ref:" + f.identity.Ref
	f.identity.WorkflowRef = "acme/project/.github/workflows/triage.lock.yml@" + f.identity.Ref
	f.identity.Event = "push"
	f.writePolicy(t, f.identity)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal("enrolled exact safe tag denied")
	}
}

func TestBoundedContextsAndTenantLimits(t *testing.T) {
	f := newFixture(t)
	for n := range maxContexts {
		f.service.contexts[strconv.Itoa(n)] = &execution{until: f.now.Add(time.Hour)}
	}
	if w := f.call(exchangeBody); w.Code != 429 || f.keyReads != 0 {
		t.Fatal("context capacity did not fail closed")
	}
	clear(f.service.contexts)
	f.service.tenantWindow = f.now
	f.service.tenantMints = 60
	if w := f.call(exchangeBody); w.Code != 429 || f.keyReads != 0 {
		t.Fatal("tenant mint rate was not bounded")
	}
	f.service.tenantMints = 0
	state := f.service.contexts[executionKey(f.identity)]
	state.tokens = make([]heldToken, maxTokens)
	for n := range state.tokens {
		state.tokens[n] = heldToken{sealed: f.service.seal(executionKey(f.identity), "test"), expires: f.now.Add(time.Hour)}
	}
	if w := f.call(exchangeBody); w.Code != 429 || f.keyReads != 0 {
		t.Fatal("per-context credential capacity did not fail closed")
	}
}

func TestPolicyRuleCountAndDuplicatesFailClosed(t *testing.T) {
	f := newFixture(t)
	for _, count := range []int{2, 129} {
		rules := make([]workflowRule, count)
		for n := range rules {
			rules[n] = workflowRule{
				RepositoryID: f.identity.RepositoryID, OwnerID: f.identity.OwnerID,
				WorkflowRef: f.identity.WorkflowRef, WorkflowSHA: f.identity.WorkflowSHA,
			}
			if count > 128 && n != 0 {
				rules[n].RepositoryID = strconv.Itoa(n + 1)
			}
		}
		data, _ := json.Marshal(map[string]any{"control-plane": map[string]any{"token-broker": map[string]any{"copilot-requests": map[string]any{"workflows": rules}}}})
		if err := os.WriteFile(f.policy, data, 0600); err != nil {
			t.Fatal(err)
		}
		if w := f.call(exchangeBody); w.Code != 403 || f.keyReads != 0 {
			t.Fatal("duplicate or over-capacity policy authorized issuance")
		}
	}
}

func TestWorkflowRefMatchesSharedPolicyContract(t *testing.T) {
	for _, value := range []string{
		"acme/project/.github/workflows/triage.yml@refs/heads/main",
		"acme/project/.github/workflows/triage.yaml@refs/heads/feature/topic",
		"acme/project/.github/workflows/_triage.lock.yml@refs/tags/release/1.2",
		"acme/project/.github/workflows/-triage.yml@refs/heads/feature-1",
		"acme/project/.github/workflows/triage.lock.yaml@refs/tags/a.locked",
	} {
		if !validWorkflowRef(value) {
			t.Errorf("shared-contract valid ref rejected: %s", value)
		}
	}
	for _, value := range []string{
		"acme/project/.github/workflows/triage.yml@refs/heads/foo.lock",
		"acme/project/.github/workflows/triage.yml@refs/tags/foo.lock/v1",
		"acme/project/.github/workflows/triage.yml@refs/heads/main.",
		"acme/project/.github/workflows/triage.yml@refs/heads/main//foo",
		"acme/project/.github/workflows/triage.yml@refs/heads/main..foo",
		"acme/project/.github/workflows/.triage.yml@refs/heads/main",
		"acme/project/.github/workflows/triage.yml@refs/heads/main\n",
	} {
		if validWorkflowRef(value) {
			t.Errorf("shared-contract invalid ref accepted: %s", value)
		}
	}
	f := newFixture(t)
	f.identity.Ref = "refs/heads/feature/topic"
	f.identity.Subject = "repo:acme/project:ref:" + f.identity.Ref
	f.identity.WorkflowRef = "acme/project/.github/workflows/_triage.yaml@" + f.identity.Ref
	f.writePolicy(t, f.identity)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal("schema-valid enrolled YAML branch was denied")
	}
}

func TestBrokerPolicyRejectsUnknownAndCaseVariantFields(t *testing.T) {
	f := newFixture(t)
	rule := map[string]string{
		"repository-id": f.identity.RepositoryID, "owner-id": f.identity.OwnerID,
		"workflow-ref": f.identity.WorkflowRef, "workflow-sha": f.identity.WorkflowSHA,
	}
	for _, change := range []func(map[string]any, map[string]any, map[string]string){
		func(b, _ map[string]any, _ map[string]string) { b["extra"] = true },
		func(_, r map[string]any, _ map[string]string) { r["extra"] = true },
		func(b, _ map[string]any, _ map[string]string) {
			b["Copilot-Requests"] = b["copilot-requests"]
			delete(b, "copilot-requests")
		},
		func(_, r map[string]any, _ map[string]string) {
			r["Workflows"] = r["workflows"]
			delete(r, "workflows")
		},
		func(_, _ map[string]any, r map[string]string) {
			r["Repository-ID"] = r["repository-id"]
			delete(r, "repository-id")
		},
	} {
		entry := make(map[string]string, len(rule))
		for key, value := range rule {
			entry[key] = value
		}
		requests := map[string]any{"workflows": []any{entry}}
		broker := map[string]any{"copilot-requests": requests}
		change(broker, requests, entry)
		data, _ := json.Marshal(map[string]any{"control-plane": map[string]any{"token-broker": broker}})
		if err := os.WriteFile(f.policy, data, 0600); err != nil {
			t.Fatal(err)
		}
		if w := f.call(exchangeBody); w.Code != 403 || f.keyReads != 0 {
			t.Fatal("unknown or case-variant authority fields failed open")
		}
	}
}
