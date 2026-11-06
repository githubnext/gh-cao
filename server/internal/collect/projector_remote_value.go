package collect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var ErrRemoteValueDeferred = errors.New("remote Value acquisition deferred")

// CollectScopedRemoteValues uses the reviewed Go acquisition under one exact
// enrollment epoch, independently of native run acquisition.
func (p Projector) CollectScopedRemoteValues(ctx context.Context, scope collectnative.EnrollmentScope) error {
	if p.Enrollment.Authority == nil || p.Data == nil {
		return collectnative.ErrUnsupported
	}
	p.nativeValueScope = &scope
	if err := p.nativeValueCurrent(ctx); err != nil {
		return err
	}
	root, err := nativeValueCatalog(p.CatalogRoot)
	if err != nil {
		return err
	}
	p.CatalogRoot = root
	return p.CollectRemoteValues(ctx, scope.Repository)
}

func (p Projector) nativeValueCurrent(ctx context.Context) error {
	if p.nativeValueScope == nil {
		return nil
	}
	current, err := p.Enrollment.Authority.CurrentScope(ctx, p.nativeValueScope.Repository)
	if err != nil {
		return err
	}
	if current != *p.nativeValueScope {
		return collectnative.ErrClaimLost
	}
	return nil
}

// CollectRemoteValues works on one exact enrolled repository. The caller owns
// the per-repository lease and durable dirty hint.
func (p Projector) CollectRemoteValues(ctx context.Context, repository string) error {
	name, err := NormalizeRepository(repository)
	if err != nil {
		return err
	}
	if p.Data == nil || !p.Data.Open() || p.RemoteQuota == nil || p.RemoteSearchQuota == nil ||
		p.Tokens == nil || p.Leases == nil || p.Enrollment.Metadata == nil {
		return fmt.Errorf("%w: hosted Postgres, shared quota and App enrollment are required", ErrRemoteValueDeferred)
	}
	if p.nativeValueScope == nil {
		return fmt.Errorf("%w: remote Value requires an exact native enrollment epoch", ErrRemoteValueDeferred)
	}
	entries, policyDigest, err := p.remoteValuePlan(ctx)
	if err != nil {
		return err
	}
	selected := make([]remoteValueEntry, 0)
	for _, entry := range entries {
		if strings.EqualFold(entry.Repository, name) {
			selected = append(selected, entry)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	valid, attempts := supportedRemoteValueEntries(selected)
	if len(valid) == 0 {
		return attempts
	}
	enrolled, err := p.Enrollment.Enrolled(ctx, name)
	if err != nil {
		return err
	}
	installation, err := p.Enrollment.InstallationFor(ctx, name)
	if err != nil {
		return err
	}
	if !enrolled || installation <= 0 {
		return fmt.Errorf("%w: remote Value repository is no longer enrolled", ErrRemoteValueDeferred)
	}
	plan := compileRemoteValueAcquisitionPlan(valid)
	var shared *remoteValueResponseCache
	if len(plan.Shared) > 0 {
		shared = newRemoteValueResponseCache()
	}
	apiBase, err := remoteValueAPIBase(p.GitHubAPIURL)
	if err != nil {
		return err
	}
	for _, entry := range valid {
		cache := (*remoteValueResponseCache)(nil)
		requirements := plan.Shared[entry.Workflow]
		if len(requirements) > 0 {
			cache = shared
		}
		if err := p.collectRemoteValue(
			ctx, name, installation, entry, policyDigest, apiBase, cache, requirements,
		); err != nil {
			attempts = errors.Join(attempts, err)
		}
	}
	return attempts
}

func supportedRemoteValueEntries(entries []remoteValueEntry) ([]remoteValueEntry, error) {
	valid := make([]remoteValueEntry, 0, len(entries))
	var failures error
	for _, entry := range entries {
		if err := reviewedRemoteValue(entry); err != nil {
			failures = errors.Join(failures, fmt.Errorf("%s: %w", entry.Workflow, err))
			continue
		}
		valid = append(valid, entry)
	}
	return valid, failures
}

func (p Projector) collectRemoteValue(
	ctx context.Context, repository string, installation int64, entry remoteValueEntry,
	policyDigest [sha256.Size]byte, base *url.URL, cache *remoteValueResponseCache,
	sharedRequirements map[string]remoteValueRequirement,
) (resultErr error) {
	if entry.Workflow == dependabotValueWorkflow {
		return p.collectDependabotValue(ctx, repository, installation, entry, policyDigest, base, cache, sharedRequirements)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	if err := reviewedRemoteValue(entry); err != nil {
		return err
	}
	if err := p.remoteValueRegistrationCurrent(ctx, repository, installation, entry, policyDigest); err != nil {
		return err
	}
	previous, restored, err := p.Data.Acquisition().ReadValue(ctx, *p.nativeValueScope, entry.Workflow)
	switch {
	case errors.Is(err, postgresx.ErrValueParentMissing):
		return fmt.Errorf("%w: canonical workflow parent has not been projected", ErrRemoteValueDeferred)
	case errors.Is(err, postgresx.ErrValueNotFound):
		previous = nil
	case err != nil:
		return err
	}
	started := time.Now()
	failureReason, outputBytes := "", int64(0)
	transport := p.RemoteHTTP
	if transport == nil {
		transport = &http.Client{Timeout: 65 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	api := &remoteValueAPI{
		client: transport, quota: p.RemoteQuota, searchQuota: p.RemoteSearchQuota,
		app: p.QuotaApp, installation: installation, repository: repository, collector: entry.Collector,
		base: base, max: entry.APIRequests, cache: cache,
		sharedRequirements: sharedRequirements,
		authorize:          p.nativeValueCurrent,
	}
	defer func() {
		if resultErr != nil && failureReason == "" {
			failureReason = nativeRemoteValueFailure(ctx, resultErr)
		}
		apiCalls, coreRequests, searchRequests, graphQLRequests, cacheHits,
			preflights, points, unverified, receivedBytes := api.costUsage()
		costCtx, stopCost := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stopCost()
		if costErr := p.Data.AppendValueCollectionCost(costCtx, postgresx.ValueCollectionCost{
			At: started.UTC(), APIRequests: apiCalls, CacheHits: cacheHits,
			Installation: installation, Repository: repository, Workflow: entry.Workflow, Collector: entry.Collector,
			CoreRequests: coreRequests, SearchRequests: searchRequests, GraphQLRequests: graphQLRequests,
			WallMS:            time.Since(started).Milliseconds(),
			PreflightRequests: preflights, GraphQLPointsObserved: points,
			GraphQLRequestsUnverified: unverified, OutputBytes: outputBytes, TransportBytes: receivedBytes,
			Succeeded: resultErr == nil, FailureReason: failureReason,
		}); costErr != nil {
			resultErr = errors.Join(resultErr, costErr)
		}
		if p.nativeValueScope != nil {
			state, reason := "available", "captured"
			if resultErr != nil {
				state, reason = "failed", nativeRemoteValueFailure(ctx, resultErr)
			} else if outputBytes == 4 {
				reason = "not-due"
			}
			if healthErr := p.RecordNativeValueAttempt(costCtx, repository, entry.Workflow,
				started, state, reason); healthErr != nil {
				resultErr = errors.Join(resultErr, healthErr)
			}
		}
	}()
	api.token, err = p.Tokens.InstallationToken(ctx, installation)
	if err != nil {
		return err
	}
	observedAt := time.Now().UTC().Truncate(time.Millisecond)
	packet, proof, skipped, err := p.acquireNativeDailyFileDiet(ctx, entry, api, previous, restored, observedAt)
	if err != nil {
		return err
	}
	if skipped {
		outputBytes = 4
		return nil
	}
	output, err := json.Marshal(struct {
		SchemaVersion int       `json:"schema_version"`
		Kind          string    `json:"kind"`
		Packet        model.Row `json:"operational_value_evidence"`
	}{2, "operational_value_evidence", packet})
	if err != nil {
		return fmt.Errorf("encode File Diet evidence: %w", err)
	}
	outputBytes = int64(len(output))
	if outputBytes > 8<<20 {
		return errors.New("file Diet output exceeds its evidence byte budget")
	}
	if err := p.remoteValueRegistrationCurrent(ctx, repository, installation, entry, policyDigest); err != nil {
		return err
	}
	_, err = p.Data.Acquisition().PublishValue(ctx, *p.nativeValueScope, previous, packet, proof, time.Now().UTC())
	return err
}

func nativeRemoteValueFailure(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timeout"
	case strings.Contains(err.Error(), "request limit exhausted"),
		strings.Contains(err.Error(), "budget exhausted"):
		return "api-budget"
	case strings.Contains(err.Error(), "quota unavailable"),
		strings.Contains(err.Error(), "shared remote Value quota"):
		return "batch-budget"
	case errors.Is(err, postgresx.ErrValueInvalid), errors.Is(err, postgresx.ErrValueConflict),
		strings.Contains(err.Error(), "evidence packet"), strings.Contains(err.Error(), "File Diet proof"):
		return "invalid-evidence"
	default:
		return "collector-failed"
	}
}

func (p Projector) remoteValueRegistrationCurrent(
	ctx context.Context, repository string, installation int64, entry remoteValueEntry, digest [sha256.Size]byte,
) error {
	if err := p.nativeValueCurrent(ctx); err != nil {
		return err
	}
	enrolled, err := p.Enrollment.Enrolled(ctx, repository)
	if err != nil {
		return err
	}
	owner, err := p.Enrollment.InstallationFor(ctx, repository)
	if err != nil {
		return err
	}
	entries, currentDigest, err := p.remoteValuePlan(ctx)
	if err != nil {
		return err
	}
	for _, current := range entries {
		if enrolled && owner == installation && currentDigest == digest &&
			strings.EqualFold(current.Repository, repository) && current.Workflow == entry.Workflow &&
			current.Collector == entry.Collector && current.APIRequests == entry.APIRequests &&
			remoteValueAcquisitionEqual(current.Acquisition, entry.Acquisition) {
			return nil
		}
	}
	return fmt.Errorf("%w: reviewed registration or enrollment changed", ErrRemoteValueDeferred)
}

func (p Projector) remoteValueWorkspace(ctx context.Context, repository, workflow string) (string, error) {
	path, err := p.remoteValueWorkspacePath(repository, workflow)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(path)
	for _, directory := range []string{p.Lake.Directory, parent, path} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", err
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("remote Value workspace is not a private directory")
		}
		if directory != p.Lake.Directory && info.Mode().Perm()&0o077 != 0 {
			return "", errors.New("remote Value workspace is accessible to other users")
		}
	}
	identity, err := json.Marshal(struct {
		Repository string `json:"repository"`
		Workflow   string `json:"workflow"`
	}{repository, workflow})
	if err != nil {
		return "", err
	}
	identityPath := filepath.Join(path, "identity.json")
	previous, err := readLocalValueFile(ctx, identityPath, 4096)
	if errors.Is(err, os.ErrNotExist) {
		if err := WriteFileAtomic(identityPath, identity); err != nil {
			return "", err
		}
	} else if err != nil || !bytes.Equal(previous, identity) {
		return "", errors.New("remote Value workspace identity mismatch")
	}
	return path, nil
}

func (p Projector) remoteValueWorkspacePath(repository, workflow string) (string, error) {
	if !filepath.IsAbs(p.Lake.Directory) || strings.TrimSpace(p.Lake.Directory) == "" {
		return "", errors.New("remote Value requires an absolute private lake directory")
	}
	if _, err := NormalizeRepository(repository); err != nil || workflow == "" || strings.ContainsAny(workflow, "/\\.") {
		return "", errors.New("invalid remote Value workspace identity")
	}
	digest := sha256.Sum256([]byte(repository + ":" + workflow))
	parent := filepath.Join(p.Lake.Directory, ".remote-value-workspaces")
	return filepath.Join(parent, hex.EncodeToString(digest[:])), nil
}

// ForgetRemoteValueWorkspace removes durable private proofs after enrollment
// withdrawal. Publication independently rechecks enrollment under its mutation
// lock, so a withdrawn repository cannot publish after this cleanup.
func (p Projector) ForgetRemoteValueWorkspace(ctx context.Context, repository string) error {
	if !filepath.IsAbs(p.Lake.Directory) || strings.TrimSpace(p.Lake.Directory) == "" {
		return errors.New("remote Value requires an absolute private lake directory")
	}
	name, err := NormalizeRepository(repository)
	if err != nil {
		return err
	}
	if p.Data != nil && p.Data.Open() {
		if err := p.Data.DeleteValueProofs(ctx, name); err != nil {
			return err
		}
	}
	parent := filepath.Join(p.Lake.Directory, ".remote-value-workspaces")
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Workspaces use a one-way digest. Derive only identities that were
	// reviewed, plus any previously reviewed policy may have removed: those
	// cannot safely be rediscovered by scanning opaque directories. For the
	// latter, use the repository manifest in each workspace instead.
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(parent, entry.Name())
		identity, readErr := readLocalValueFile(ctx, filepath.Join(directory, "identity.json"), 4096)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		var metadata struct{ Repository, Workflow string }
		if err := json.Unmarshal(identity, &metadata); err != nil {
			return err
		}
		if metadata.Repository == name {
			expected, err := p.remoteValueWorkspacePath(name, metadata.Workflow)
			if err != nil || expected != directory {
				return errors.New("remote Value workspace identity mismatch")
			}
			if err := os.RemoveAll(directory); err != nil {
				return err
			}
		}
	}
	return nil
}

func readRemoteValueProof(directory string) ([]postgresx.ValueProofFile, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("retained remote Value proof is not a directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	files := []postgresx.ValueProofFile{}
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, err error) error {
		if err != nil || relative == "." {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || len(files) >= postgresx.ValueProofMaxFiles {
			return errors.New("retained remote Value proof exceeds its confined file budget")
		}
		file, err := root.Open(relative)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > postgresx.ValueProofFileMax {
			return errors.New("retained remote Value proof exceeds its confined byte budget")
		}
		content, err := io.ReadAll(io.LimitReader(file, postgresx.ValueProofFileMax+1))
		if err != nil || len(content) > postgresx.ValueProofFileMax {
			return errors.New("retained remote Value proof exceeds its confined byte budget")
		}
		total += int64(len(content))
		if total > postgresx.ValueProofMaxBytes {
			return errors.New("retained remote Value proof exceeds its confined byte budget")
		}
		files = append(files, postgresx.ValueProofFile{Path: relative, Content: content})
		return nil
	})
	return files, err
}
