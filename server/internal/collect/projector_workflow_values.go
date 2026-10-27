package collect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operationalvalue"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
)

const localValueLockName = "value-local"
const localValueAttemptKey = "collect:value-local-attempt"

var ErrLocalValueBusy = errors.New("a local Value computation is already running")

func (p Projector) refreshWorkflowValueHealth(ctx context.Context) error {
	if p.CatalogRoot == "" || p.PolicyPath == "" || p.Enrollment.Metadata == nil {
		return nil
	}
	repositories, err := p.enrolledRepositories(ctx)
	if err != nil {
		return err
	}
	if len(repositories) == 0 {
		return nil
	}
	return operationalvalue.WriteCollectionHealth(ctx, operationalvalue.Config{
		Root: p.CatalogRoot, NodeBinary: p.node(), Repositories: repositories,
		Environment: collectionEnvironment(p.GitHubAPIURL), WorkerTimeout: 30 * time.Second,
	}, p.PolicyPath, p.Lake.Directory)
}

// CollectLocalValues never acquires a target credential. It evaluates reviewed
// adapters against one repeatable-read canonical PostgreSQL revision, then
// publishes only if the projection, policy, request, enrollment, and database
// revision remain current.
func (p Projector) CollectLocalValues(ctx context.Context) error {
	if p.Leases == nil || p.State == nil || p.CatalogRoot == "" || p.PolicyPath == "" {
		return nil
	}
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
	if p.Data == nil || !p.Data.Open() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	generation, err := p.State.OperationalState(ctx, projectionCompletedKey)
	if err != nil || len(generation) == 0 {
		return err
	}
	attempted, err := p.State.OperationalState(ctx, localValueAttemptKey)
	if err != nil || bytes.Equal(generation, attempted) {
		return err
	}
	// One bounded attempt per published input, not a per-workflow retry loop.
	if err := p.State.SetOperationalState(ctx, localValueAttemptKey, generation); err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "cao-local-value-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	policy, err := readLocalValueFile(ctx, p.PolicyPath, 1<<20)
	if err != nil {
		return err
	}
	policyPath := filepath.Join(workspace, "cao.json")
	if err := os.WriteFile(policyPath, policy, 0o600); err != nil {
		return err
	}
	requestPath := filepath.Join(p.Lake.ShardDirectory(), "workflow-value-local.json")
	revision, err := readLocalValueRevision(ctx, policyPath, requestPath)
	if err != nil {
		return err
	}
	repositories, err := p.enrolledRepositories(ctx)
	if err != nil {
		return err
	}
	plan, err := operationalvalue.ReadLocalValuePlan(ctx, operationalvalue.Config{
		Root: p.CatalogRoot, NodeBinary: p.node(),
		Environment: collectionEnvironment(p.GitHubAPIURL), WorkerTimeout: 30 * time.Second,
	}, policyPath, p.Lake.Directory)
	if err != nil {
		return err
	}
	if len(plan.Repositories) == 0 {
		return nil
	}
	enrolled := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		enrolled[strings.ToLower(repository)] = true
	}
	for _, repository := range plan.Repositories {
		if !enrolled[strings.ToLower(repository)] {
			return errors.New("local Value plan exceeds the exact enrolled repository scope")
		}
	}
	output := filepath.Join(workspace, "operational-values.jsonl")
	config := operationalvalue.Config{
		Root: p.CatalogRoot, NodeBinary: p.node(), Output: output,
		Environment: collectionEnvironment(p.GitHubAPIURL), WorkerTimeout: 2 * time.Minute,
	}
	var databaseRevision int64
	if err := p.Data.WithReadTransaction(ctx, func(readCtx context.Context, reader postgresx.NativeReader) error {
		state, err := reader.State(readCtx)
		if err != nil {
			return err
		}
		databaseRevision = state.Revision
		return operationalvalue.ComputeHostedSnapshot(readCtx, config, policyPath, p.Lake.Directory, output,
			func(queryCtx context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, error) {
				sources, _, err := reader.ExecuteSQLPlanWithOptions(queryCtx, definitions, requested,
					postgresx.SQLExecutionOptions{Repositories: repositories})
				for name, source := range sources {
					if source.Metadata == nil {
						source.Metadata = model.Metadata{}
					}
					source.Metadata["canonical-revision"] = databaseRevision
					sources[name] = source
				}
				return sources, err
			})
	}); err != nil {
		return err
	}
	return p.publishLocalValues(ctx, output, generation, revision, repositories, databaseRevision)
}

func (p Projector) publishLocalValues(
	ctx context.Context,
	output string,
	generation []byte,
	revision localValueRevision,
	repositories []string,
	databaseRevision int64,
) error {
	content, err := readLocalValueFile(ctx, output, 16<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(content) > 16<<20 {
		return errors.New("local Value output is unreadable or exceeds its byte budget")
	}
	publication, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	enrolled := map[string]bool{}
	for _, repository := range repositories {
		enrolled[strings.ToLower(repository)] = true
	}
	if err := operationalvalue.FilterRepositories(output, func(repository string) bool { return enrolled[repository] }); err != nil {
		return err
	}
	content, err = readLocalValueFile(publication, output, 16<<20)
	if err != nil {
		return err
	}
	rows, err := operationalvalue.ReadMeasurements(output)
	if err != nil {
		return err
	}
	return p.Enrollment.withMutationLock(publication, func(locked context.Context) error {
		return p.withProjectionPublicationLease(locked, func(serialized context.Context) error {
			currentGeneration, err := p.State.OperationalState(serialized, projectionCompletedKey)
			if err != nil {
				return err
			}
			currentRevision, err := readLocalValueRevision(serialized, p.PolicyPath,
				filepath.Join(p.Lake.ShardDirectory(), "workflow-value-local.json"))
			if err != nil {
				return err
			}
			currentRepositories, err := p.enrolledRepositories(serialized)
			if err != nil {
				return err
			}
			if !localValuePublicationCurrent(generation, currentGeneration, revision, currentRevision,
				repositories, currentRepositories) {
				return errors.New("local Value publication deferred: its input, policy, or enrollment changed")
			}
			if _, err := p.Data.ReplaceOperationalValues(serialized, databaseRevision, rows); err != nil {
				return err
			}
			previous, err := readLocalValueFile(serialized, p.Lake.operationalValuesPath(), 16<<20)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if bytes.Equal(content, previous) {
				return nil
			}
			if err := WriteFileAtomic(p.Lake.operationalValuesPath(), content); err != nil {
				return err
			}
			// Mirror accepted measurements for cold-start recovery. Computation
			// never reads this file in hosted mode; PostgreSQL is authoritative.
			return p.RequestProjection(serialized)
		})
	})
}

func localValuePublicationCurrent(
	expectedGeneration, currentGeneration []byte,
	expectedRevision, currentRevision localValueRevision,
	expectedRepositories, currentRepositories []string,
) bool {
	return bytes.Equal(expectedGeneration, currentGeneration) &&
		expectedRevision == currentRevision &&
		slices.Equal(expectedRepositories, currentRepositories)
}

type localValueRevision struct {
	policy, request [sha256.Size]byte
}

func readLocalValueRevision(ctx context.Context, policy, request string) (localValueRevision, error) {
	var revision localValueRevision
	for _, input := range []struct {
		filename string
		limit    int64
		optional bool
		digest   *[sha256.Size]byte
	}{
		{policy, 1 << 20, false, &revision.policy},
		{request, 1 << 20, true, &revision.request},
	} {
		content, err := readLocalValueFile(ctx, input.filename, input.limit)
		if err != nil && (!input.optional || !errors.Is(err, os.ErrNotExist)) {
			return revision, err
		}
		*input.digest = sha256.Sum256(content)
	}
	return revision, nil
}

func (p Projector) withProjectionPublicationLease(ctx context.Context, publish func(context.Context) error) error {
	token, err := operationToken()
	if err != nil {
		return err
	}
	for {
		acquired, err := p.Leases.TryLock(ctx, projectionLockName, token, p.lockTTL())
		if err != nil {
			return err
		}
		if acquired {
			defer func() {
				release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				defer cancel()
				_ = p.Leases.Unlock(release, projectionLockName, token)
			}()
			return publish(ctx)
		}
		select {
		case <-ctx.Done():
			return ErrProjectionBusy
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func readLocalValueFile(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, errors.New("local Value input is not a bounded regular file")
	}
	file, err := os.Open(path) // #nosec G304 -- confined snapshot or private output path, size checked before reading.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, errors.New("local Value input grew beyond its byte budget")
	}
	return content, ctx.Err()
}
