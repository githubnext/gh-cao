package collect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operationalvalue"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
)

const nativeLocalValueCompletedKey = "collect:native-value-local-completed"

type nativeLocalValueProgress struct {
	Revision    int64             `json:"revision"`
	Policy      [sha256.Size]byte `json:"policy"`
	CompletedAt time.Time         `json:"completedAt"`
}

// CollectNativeLocalValues is the optional Node/catalog Value coexistence lane.
// PostgreSQL owns input, accepted evidence and publication. Scratch contains
// only bounded per-invocation transport files and is never a recovery source.
func (p Projector) CollectNativeLocalValues(ctx context.Context) error {
	if p.CatalogRoot == "" && p.PolicyPath == "" {
		return nil
	}
	if p.CatalogRoot == "" || p.PolicyPath == "" || p.State == nil || p.Leases == nil ||
		p.Data == nil || !p.Data.Open() || p.Enrollment.Authority == nil {
		return collectnative.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	token, err := operationToken()
	if err != nil {
		return err
	}
	acquired, err := p.Leases.TryLock(ctx, localValueLockName, token, 3*time.Minute)
	if err != nil {
		return err
	}
	if !acquired {
		return ErrLocalValueBusy
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = p.Leases.Unlock(release, localValueLockName, token)
	}()
	policy, err := p.resolvedNativeValuePolicy(ctx)
	if err != nil {
		return err
	}
	state, err := p.Data.State(ctx)
	if err != nil {
		return err
	}
	progress := nativeLocalValueProgress{Revision: state.Revision, Policy: sha256.Sum256(policy)}
	completed, err := p.State.OperationalState(ctx, nativeLocalValueCompletedKey)
	if err != nil {
		return err
	}
	var previous nativeLocalValueProgress
	if len(completed) != 0 {
		if err := json.Unmarshal(completed, &previous); err != nil {
			return errors.New("invalid durable native local Value progress")
		}
		if previous.Revision == progress.Revision && previous.Policy == progress.Policy &&
			!previous.CompletedAt.IsZero() && time.Since(previous.CompletedAt) >= 0 &&
			time.Since(previous.CompletedAt) < p.minInterval() {
			return nil
		}
	}
	scopes, err := p.nativeLocalValueScopes(ctx)
	if err != nil || len(scopes) == 0 {
		return err
	}
	repositories := make([]string, len(scopes))
	for i, scope := range scopes {
		repositories[i] = scope.Repository
	}
	inputs, err := p.Data.ReadNativeValueInputs(ctx, repositories)
	if err != nil {
		return err
	}
	workspace, err := os.MkdirTemp(".", ".native-local-value-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return err
	}
	policyPath := filepath.Join(workspace, "cao.json")
	if err := os.WriteFile(policyPath, policy, 0o600); err != nil {
		return err
	}
	if err := writeNativeValueInputs(workspace, inputs); err != nil {
		return err
	}
	root, err := filepath.Abs(p.CatalogRoot)
	if err != nil {
		return err
	}
	config := operationalvalue.Config{Root: root, NodeBinary: p.node(), Repositories: repositories,
		Environment: collectionEnvironment(p.GitHubAPIURL), WorkerTimeout: 30 * time.Second}
	if err := operationalvalue.WriteCollectionHealth(ctx, config, policyPath, workspace); err != nil {
		return err
	}
	health, err := readNativeValueEnvelopes(ctx, filepath.Join(workspace, "gh-aw-logs-shards", "workflow-values-collections.jsonl"),
		"workflow_value_collection")
	if err != nil {
		return err
	}
	packets, err := readNativeValueEnvelopes(ctx, filepath.Join(workspace, "gh-aw-logs-shards", "workflow-values.jsonl"),
		"operational_value_evidence")
	if err != nil {
		return err
	}
	if err := p.nativeLocalPolicyCurrent(ctx, policy); err != nil {
		return err
	}
	revision, err := p.Data.PublishNativeValuePreparation(ctx, inputs.Revision, scopes, health, packets)
	if err != nil {
		return err
	}
	plan, err := operationalvalue.ReadLocalValuePlan(ctx, config, policyPath, workspace)
	if err != nil {
		return err
	}
	for _, repository := range plan.Repositories {
		if !slices.Contains(repositories, repository) {
			return errors.New("native local Value plan exceeds exact enrolled repository scope")
		}
	}
	if len(plan.Repositories) != 0 {
		revision, err = p.computeNativeLocalValues(ctx, config, policyPath, workspace, policy, repositories, plan.Repositories, revision)
		if err != nil {
			return err
		}
	}
	progress.Revision = revision
	progress.CompletedAt = time.Now().UTC()
	content, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	// Failed or interrupted work never consumes the input. Successful retry is
	// safe even if this checkpoint write fails: canonical publication is CAS.
	return p.State.SetOperationalState(ctx, nativeLocalValueCompletedKey, content)
}

func (p Projector) nativeLocalPolicyCurrent(ctx context.Context, expected []byte) error {
	current, err := p.resolvedNativeValuePolicy(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, current) {
		return errors.New("native local Value publication deferred: reviewed policy changed")
	}
	return nil
}

func (p Projector) nativeLocalValueScopes(ctx context.Context) ([]collectnative.EnrollmentScope, error) {
	installations, err := p.Enrollment.Authority.Installations(ctx)
	if err != nil {
		return nil, err
	}
	index := map[string]collectnative.EnrollmentScope{}
	for _, installation := range installations {
		cursor := ""
		for {
			repositories, next, err := p.Enrollment.Authority.ScanRepositories(ctx, installation, cursor, 256)
			if err != nil {
				return nil, err
			}
			for _, repository := range repositories {
				scope, err := p.Enrollment.Authority.CurrentScope(ctx, repository)
				if err != nil {
					return nil, err
				}
				if scope.InstallationID != installation {
					return nil, collectnative.ErrClaimLost
				}
				index[repository] = scope
				if len(index) > 10000 {
					return nil, collectnative.ErrCapacity
				}
				if p.InventoryRepositoryLimit > 0 && len(index) > p.InventoryRepositoryLimit {
					return nil, collectnative.ErrCapacity
				}
			}
			if next == "" {
				break
			}
			if next <= cursor {
				return nil, errors.New("native Value enrollment scan did not advance")
			}
			cursor = next
		}
	}
	repositories := make([]string, 0, len(index))
	for repository := range index {
		repositories = append(repositories, repository)
	}
	slices.Sort(repositories)
	scopes := make([]collectnative.EnrollmentScope, len(repositories))
	for i, repository := range repositories {
		scopes[i] = index[repository]
	}
	return scopes, nil
}

func (p Projector) computeNativeLocalValues(
	ctx context.Context, config operationalvalue.Config, policyPath, workspace string, policy []byte,
	enrolled, selected []string, expectedRevision int64,
) (int64, error) {
	output := filepath.Join(workspace, "operational-values.jsonl")
	config.WorkerTimeout = 2 * time.Minute
	var retained []model.Row
	retainedScope := make([]string, 0, len(enrolled))
	for _, repository := range enrolled {
		if !slices.Contains(selected, repository) {
			retainedScope = append(retainedScope, repository)
		}
	}
	err := p.Data.WithReadTransaction(ctx, func(readCtx context.Context, reader postgresx.NativeReader) error {
		state, err := reader.State(readCtx)
		if err != nil {
			return err
		}
		if state.Revision != expectedRevision {
			return postgresx.ErrOperationalValueRevisionStale
		}
		// Disabled/out-of-policy repositories keep accepted history. The runtime
		// receives only its reviewed plan scope, never all ingestion enrollment.
		if len(retainedScope) != 0 {
			cutoff := time.Now().UTC().Add(-operationalvalue.DefaultRetention).Format(time.RFC3339Nano)
			sources, _, err := reader.ExecuteSQLPlanWithOptions(readCtx, []query.Definition{{
				Name: "retained", From: "$operationalValues",
				Filter: &query.Filter{Predicates: []query.Predicate{{Field: "timestamp", GTE: cutoff}}},
			}}, []string{"retained"}, postgresx.SQLExecutionOptions{Repositories: retainedScope})
			if err != nil {
				return err
			}
			retained = sources["retained"].Rows
		}
		return operationalvalue.ComputeHostedSnapshot(readCtx, config, policyPath, workspace, output,
			func(queryCtx context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, error) {
				sources, _, err := reader.ExecuteSQLPlanWithOptions(queryCtx, nativeLocalValueDefinitions(definitions), requested,
					postgresx.SQLExecutionOptions{Repositories: selected})
				for name, source := range sources {
					if source.Metadata == nil {
						source.Metadata = model.Metadata{}
					}
					source.Metadata["canonical-revision"] = expectedRevision
					sources[name] = source
				}
				return sources, err
			})
	})
	if err != nil {
		return 0, err
	}
	if _, err := readLocalValueFile(ctx, output, 16<<20); err != nil {
		return 0, err
	}
	rows, err := operationalvalue.ReadMeasurements(output)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if !slices.Contains(selected, strings.ToLower(textValue(row["repository"]))) {
			return 0, errors.New("native local Value output exceeds reviewed scope")
		}
		if len(retained)+len(rows) > query.MaxOutputRows {
			return 0, errors.New("native local Value combined history exceeds its row budget")
		}
	}
	if err := p.nativeLocalPolicyCurrent(ctx, policy); err != nil {
		return 0, err
	}
	state, err := p.Data.ReplaceOperationalValues(ctx, expectedRevision, append(retained, rows...))
	return state.Revision, err
}

func textValue(value any) string {
	text, _ := value.(string)
	return text
}

// Activity's canonical query contract names collections without the dashboard
// document's "$" root marker. Translate only generated canonical collection
// names; preserve named query dependencies and reject unknown/runtime sources
// through the production SQL resolver.
func nativeLocalValueDefinitions(definitions []query.Definition) []query.Definition {
	names := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		names[definition.Name] = true
	}
	root := func(name string) string {
		if !names[name] && postgresx.IsCanonicalCollection(name) {
			return "$" + name
		}
		return name
	}
	result := slices.Clone(definitions)
	for i := range result {
		result[i].From = root(result[i].From)
		result[i].Union = slices.Clone(result[i].Union)
		for j := range result[i].Union {
			result[i].Union[j] = root(result[i].Union[j])
		}
		result[i].Joins = slices.Clone(result[i].Joins)
		for j := range result[i].Joins {
			result[i].Joins[j].Source = root(result[i].Joins[j].Source)
		}
	}
	return result
}
