package operationalvalue

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
)

type WorkflowPolicySnapshot struct {
	Document      json.RawMessage        `json:"document"`
	Registrations []WorkflowRegistration `json:"registrations"`
}

// ResolveWorkflowPolicy binds registrations to the same validated, composed
// document. Callers must resolve again before publication rather than caching
// authority by the entry profile's bytes alone.
func ResolveWorkflowPolicy(ctx context.Context, config Config, policy string) (WorkflowPolicySnapshot, error) {
	const resolve = `
import { pathToFileURL } from 'node:url';
const { loadPolicyFile } = await import(pathToFileURL(process.argv[1]).href);
const { workflowValueRegistrations } = await import(pathToFileURL(process.argv[2]).href);
const document = loadPolicyFile(process.argv[3]);
const entries = document['control-plane']?.campaigns?.activity?.['workflow-values'] ?? [];
const registrations = workflowValueRegistrations(document, entries.map(entry => entry.repository));
process.stdout.write(JSON.stringify({ document, registrations }));
`
	content, err := runLocalRuntimeOutput(ctx, config, []string{
		"--input-type=module", "--eval", resolve,
		filepath.Join(config.Root, ".github", "workflows", "shared", "policy.mjs"),
		filepath.Join(config.Root, "activity", "workflow-values.mjs"), policy,
	})
	if err != nil {
		return WorkflowPolicySnapshot{}, err
	}
	var snapshot WorkflowPolicySnapshot
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return WorkflowPolicySnapshot{}, err
	}
	if len(snapshot.Document) == 0 || !json.Valid(snapshot.Document) || len(snapshot.Registrations) > 1000 {
		return WorkflowPolicySnapshot{}, errors.New("reviewed Value policy snapshot exceeds its contract")
	}
	return snapshot, nil
}
