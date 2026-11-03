package collect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operationalvalue"
)

// RemoteValuePlanCache shares a reviewed policy snapshot between webhook
// admissions. A policy change invalidates it on the next call; the cache never
// grants authority without the separate live enrollment check.
type RemoteValuePlanCache struct {
	mu      sync.Mutex
	digest  [sha256.Size]byte
	entries []remoteValueEntry
	loaded  bool
}

type remoteValueEntry struct {
	Repository  string                  `json:"repository"`
	Workflow    string                  `json:"workflow"`
	Collector   string                  `json:"collector"`
	APIRequests int                     `json:"api-requests"`
	Enabled     *bool                   `json:"enabled"`
	Acquisition *remoteValueAcquisition `json:"acquisition,omitempty"`
	Raw         json.RawMessage         `json:"-"`
}

type remoteValueAcquisition struct {
	Requirements []remoteValueRequirement `json:"requirements"`
}

type remoteValueRequirement struct {
	Source    string `json:"source"`
	Pool      string `json:"pool"`
	Primitive string `json:"primitive"`
	Scope     string `json:"scope"`
	Cache     string `json:"cache"`
}

type remoteValueAcquisitionPlan struct {
	Requirements []remoteValueRequirement
	Shared       map[string]map[string]remoteValueRequirement
}

func remoteValueRequirementKey(requirement remoteValueRequirement) string {
	return strings.Join([]string{
		requirement.Cache, requirement.Pool, requirement.Primitive, requirement.Scope, requirement.Source,
	}, "\x00")
}

func remoteValueRequirementID(requirement remoteValueRequirement) string {
	digest := sha256.Sum256([]byte(remoteValueRequirementKey(requirement)))
	return fmt.Sprintf("%x", digest)
}

func compileRemoteValueAcquisitionPlan(entries []remoteValueEntry) remoteValueAcquisitionPlan {
	type planned struct {
		requirement remoteValueRequirement
		workflows   map[string]bool
	}
	plannedRequirements := map[string]*planned{}
	for _, entry := range entries {
		if entry.Acquisition == nil {
			continue
		}
		for _, requirement := range entry.Acquisition.Requirements {
			key := remoteValueRequirementKey(requirement)
			current := plannedRequirements[key]
			if current == nil {
				current = &planned{requirement: requirement, workflows: map[string]bool{}}
				plannedRequirements[key] = current
			}
			current.workflows[entry.Workflow] = true
		}
	}
	keys := make([]string, 0, len(plannedRequirements))
	for key := range plannedRequirements {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	plan := remoteValueAcquisitionPlan{Shared: map[string]map[string]remoteValueRequirement{}}
	for _, key := range keys {
		current := plannedRequirements[key]
		plan.Requirements = append(plan.Requirements, current.requirement)
		if current.requirement.Source == "github" && current.requirement.Cache == "invocation" &&
			len(current.workflows) > 1 {
			id := remoteValueRequirementID(current.requirement)
			for workflow := range current.workflows {
				if plan.Shared[workflow] == nil {
					plan.Shared[workflow] = map[string]remoteValueRequirement{}
				}
				plan.Shared[workflow][id] = current.requirement
			}
		}
	}
	return plan
}

func remoteValueAcquisitionEqual(left, right *remoteValueAcquisition) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func (p Projector) remoteValuePlan(ctx context.Context) ([]remoteValueEntry, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if p.PolicyPath == "" || p.CatalogRoot == "" {
		return nil, zero, errors.New("reviewed remote Value policy and catalog are required")
	}
	content, err := readLocalValueFile(ctx, p.PolicyPath, 1<<20)
	if err != nil {
		return nil, zero, err
	}
	digest := sha256.Sum256(content)
	cache := p.RemoteValuePlan
	if cache != nil {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.loaded && cache.digest == digest {
			return slices.Clone(cache.entries), digest, nil
		}
	}
	// The production policy runtime validates the entire document and applies
	// Activity enablement, global scope and explicit Activity repository scope.
	registrations, err := operationalvalue.WorkflowRegistrations(ctx, operationalvalue.Config{
		Root: p.CatalogRoot, NodeBinary: p.node(),
		Environment: collectionEnvironment(p.GitHubAPIURL), WorkerTimeout: 30 * time.Second,
	}, p.PolicyPath)
	if err != nil {
		return nil, zero, err
	}
	if len(registrations) > 1000 {
		return nil, zero, errors.New("reviewed workflow Value registration limit exceeded")
	}
	var document struct {
		Control struct {
			Campaigns struct {
				Activity struct {
					Values []json.RawMessage `json:"workflow-values"`
				} `json:"activity"`
			} `json:"campaigns"`
		} `json:"control-plane"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil {
		return nil, zero, fmt.Errorf("decode reviewed remote Value policy: %w", err)
	}
	raw := make(map[string]remoteValueEntry, len(document.Control.Campaigns.Activity.Values))
	for _, value := range document.Control.Campaigns.Activity.Values {
		var entry remoteValueEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			return nil, zero, err
		}
		entry.Raw = value
		key := strings.ToLower(entry.Repository) + ":" + entry.Workflow
		if _, exists := raw[key]; exists {
			return nil, zero, errors.New("duplicate reviewed remote Value registration")
		}
		raw[key] = entry
	}
	entries := make([]remoteValueEntry, 0, len(registrations))
	for _, registration := range registrations {
		key := strings.ToLower(registration.Repository) + ":" + registration.Workflow
		entry, ok := raw[key]
		if !ok || entry.Collector != registration.Collector || entry.Enabled != nil && !*entry.Enabled {
			return nil, zero, errors.New("reviewed remote Value registration changed during resolution")
		}
		if entry.APIRequests > 0 {
			entries = append(entries, entry)
		}
	}
	// A policy edit during the runtime subprocess must not cache the plan
	// under a digest belonging to the older policy.
	current, err := readLocalValueFile(ctx, p.PolicyPath, 1<<20)
	if err != nil {
		return nil, zero, err
	}
	if sha256.Sum256(current) != digest {
		return nil, zero, errors.New("reviewed remote Value policy changed during resolution")
	}
	if cache != nil {
		cache.entries, cache.digest, cache.loaded = slices.Clone(entries), digest, true
	}
	return entries, digest, nil
}

// RemoteValueRepositories returns only reviewed, enabled, API-bound remote
// registrations that remain enrolled. It never acquires an App token or calls
// GitHub, and the policy runtime subprocess runs only when policy bytes change.
func (p Projector) RemoteValueRepositories(ctx context.Context) ([]string, error) {
	entries, _, err := p.remoteValuePlan(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		name, err := NormalizeRepository(entry.Repository)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		enrolled, err := p.Enrollment.Enrolled(ctx, name)
		if err != nil {
			return nil, err
		}
		if enrolled {
			names = append(names, name)
			seen[name] = true
		}
	}
	slices.Sort(names)
	return names, nil
}
