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

// CollectNativeLocalValues is the Go-native hosted Value preparation lane.
// PostgreSQL owns input, accepted evidence and publication. Scratch contains
// only bounded per-invocation transport files and is never a recovery source.
func (p NativeValueService) CollectNativeLocalValues(ctx context.Context) error {
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
	root, err := p.nativeLocalValueRoot()
	if err != nil {
		return err
	}
	workspace, err := os.MkdirTemp(root, ".native-local-value-")
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
	config := operationalvalue.Config{Root: root, Repositories: repositories}
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

func (p NativeValueService) nativeLocalPolicyCurrent(ctx context.Context, expected []byte) error {
	current, err := p.resolvedNativeValuePolicy(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, current) {
		return errors.New("native local Value publication deferred: reviewed policy changed")
	}
	return nil
}

func (p NativeValueService) nativeLocalValueScopes(ctx context.Context) ([]collectnative.EnrollmentScope, error) {
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

func (p NativeValueService) computeNativeLocalValues(
	ctx context.Context, config operationalvalue.Config, policyPath, workspace string, policy []byte,
	enrolled, selected []string, expectedRevision int64,
) (int64, error) {
	output := filepath.Join(workspace, "operational-values.jsonl")
	var retained []model.Row
	err := p.Data.WithReadTransaction(ctx, func(readCtx context.Context, reader postgresx.NativeReader) error {
		state, err := reader.State(readCtx)
		if err != nil {
			return err
		}
		if state.Revision != expectedRevision {
			return postgresx.ErrOperationalValueRevisionStale
		}
		// The evaluator writes only new observations. Preserve previous accepted
		// history for all enrolled repositories, including the selected scope.
		if len(enrolled) != 0 {
			cutoff := time.Now().UTC().Add(-operationalvalue.DefaultRetention).Format(time.RFC3339Nano)
			sources, _, err := reader.ExecuteSQLPlanWithOptions(readCtx, []query.Definition{{
				Name: "retained", From: "$operationalValues",
				Filter: &query.Filter{Predicates: []query.Predicate{{Field: "timestamp", GTE: cutoff}}},
			}}, []string{"retained"}, postgresx.SQLExecutionOptions{Repositories: enrolled})
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
	kept := nativeLocalValueRetainedRows(retained, rows)
	if err := p.nativeLocalPolicyCurrent(ctx, policy); err != nil {
		return 0, err
	}
	state, err := p.Data.ReplaceOperationalValues(ctx, expectedRevision, append(kept, rows...))
	return state.Revision, err
}

// A repeated evaluation replaces the same frozen interval. The legacy
// Dependabot missing placeholder predates its evaluator digest; only a
// complete numeric observation at that exact cutoff may supersede it.
func nativeLocalValueRetainedRows(retained, rows []model.Row) []model.Row {
	updated := make(map[string]bool, len(rows))
	key := func(row model.Row) string {
		return strings.Join([]string{textValue(row["campaign"]), textValue(row["repository"]),
			textValue(row["valueId"]), textValue(row["evaluator-digest"]),
			textValue(row["window-start"]), textValue(row["window-end"])}, "\x00")
	}
	for _, row := range rows {
		updated[key(row)] = true
	}
	legacyReplacements := map[string]bool{}
	for _, row := range rows {
		if nativeLocalValueCompleteDependabot(row) {
			legacyReplacements[nativeLocalValueWindowKey(row)] = true
		}
	}
	kept := retained[:0]
	for _, row := range retained {
		if !updated[key(row)] && (!nativeLocalValueLegacyDependabotMissing(row) ||
			!legacyReplacements[nativeLocalValueWindowKey(row)]) {
			kept = append(kept, row)
		}
	}
	return kept
}

func nativeLocalValueWindowKey(row model.Row) string {
	cutoff := func(field string) string {
		value := textValue(row[field])
		at, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || !strings.HasSuffix(value, "Z") {
			return value
		}
		return at.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{textValue(row["campaign"]), textValue(row["repository"]),
		textValue(row["valueId"]), cutoff("window-start"), cutoff("window-end")}, "\x00")
}

func nativeLocalValueDependabotMetric(row model.Row) bool {
	if row["campaign"] != "dependabot" || row["repository"] != dependabotValueRepository ||
		row["measurement-kind"] != "snapshot" {
		return false
	}
	for _, metric := range dependabotValueMetrics {
		if row["valueId"] == dependabotValueWorkflow+"."+metric.id {
			return true
		}
	}
	return false
}

func nativeLocalValueCompleteDependabot(row model.Row) bool {
	_, numeric := row["value"].(json.Number)
	return nativeLocalValueDependabotMetric(row) && numeric &&
		row["coverage-status"] == "complete" && row["maturity-status"] == "matured" &&
		row["missing-reason"] == nil && textValue(row["evaluator-digest"]) != "" &&
		textValue(row["window-start"]) != "" && textValue(row["window-end"]) != ""
}

func nativeLocalValueLegacyDependabotMissing(row model.Row) bool {
	return nativeLocalValueDependabotMetric(row) && row["value"] == nil &&
		row["coverage-status"] == "missing" && row["maturity-status"] == "unavailable" &&
		row["missing-reason"] == "remote-acquisition-unavailable" &&
		textValue(row["evaluator-digest"]) == "" &&
		textValue(row["window-start"]) != "" && textValue(row["window-end"]) != ""
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
