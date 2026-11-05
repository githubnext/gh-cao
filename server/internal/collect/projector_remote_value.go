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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var ErrRemoteValueDeferred = errors.New("remote Value acquisition deferred")

const remoteValueFailurePrefix = "CAO_WORKFLOW_VALUE_FAILURE "

// CollectScopedRemoteValues preserves the separately selected reviewed Value
// runtime without giving native run acquisition a Node or evidence-lake dependency.
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
// the per-repository lease and durable dirty hint. This method never holds the
// projection lease or an App token in its child process.
func (p Projector) CollectRemoteValues(ctx context.Context, repository string) error {
	name, err := NormalizeRepository(repository)
	if err != nil {
		return err
	}
	if p.Data == nil || !p.Data.Open() || p.RemoteQuota == nil || p.RemoteSearchQuota == nil ||
		p.Tokens == nil || p.Leases == nil || p.Enrollment.Metadata == nil {
		return fmt.Errorf("%w: hosted Postgres, shared quota and App enrollment are required", ErrRemoteValueDeferred)
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
	plan := compileRemoteValueAcquisitionPlan(selected)
	var shared *remoteValueResponseCache
	if len(plan.Shared) > 0 {
		shared = newRemoteValueResponseCache()
	}
	apiBase, err := remoteValueAPIBase(p.GitHubAPIURL)
	if err != nil {
		return err
	}
	for _, entry := range selected {
		cache := (*remoteValueResponseCache)(nil)
		requirements := plan.Shared[entry.Workflow]
		if len(requirements) > 0 {
			cache = shared
		}
		if err := p.collectRemoteValue(
			ctx, name, installation, entry, policyDigest, apiBase, cache, requirements,
		); err != nil {
			return err
		}
	}
	return nil
}

func (p Projector) collectRemoteValue(
	ctx context.Context, repository string, installation int64, entry remoteValueEntry,
	policyDigest [sha256.Size]byte, base *url.URL, cache *remoteValueResponseCache,
	sharedRequirements map[string]remoteValueRequirement,
) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	var previous model.Row
	var restored []postgresx.ValueProofFile
	var err error
	if p.nativeValueScope != nil {
		previous, restored, err = p.Data.Acquisition().ReadValue(ctx, *p.nativeValueScope, entry.Workflow)
	} else {
		previous, err = p.Data.ReadOperationalValue(ctx, repository, entry.Workflow)
	}
	switch {
	case errors.Is(err, postgresx.ErrValueParentMissing):
		return fmt.Errorf("%w: canonical workflow parent has not been projected", ErrRemoteValueDeferred)
	case errors.Is(err, postgresx.ErrValueNotFound):
		previous = nil
	case err != nil:
		return err
	}
	var root string
	if p.nativeValueScope == nil {
		root, err = p.remoteValueWorkspace(ctx, repository, entry.Workflow)
		if err != nil {
			return err
		}
	}
	work, err := os.MkdirTemp(root, "cao-value-invocation-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if p.nativeValueScope != nil {
		if err := writeRemoteValueProof(restored, filepath.Join(work, "latest")); err != nil {
			return err
		}
	} else {
		if err := restoreRemoteValueProof(ctx, p.Data, repository, entry.Workflow, filepath.Join(work, "latest")); err != nil {
			return err
		}
	}
	bin := filepath.Join(work, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(remoteValueGH), 0o700); err != nil { // #nosec G306 -- owner-only executable bridge in a private invocation directory.
		return err
	}
	token, err := p.Tokens.InstallationToken(ctx, installation)
	if err != nil {
		return err
	}
	transport := p.RemoteHTTP
	if transport == nil {
		transport = &http.Client{Timeout: 65 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	api := &remoteValueAPI{
		client: transport, quota: p.RemoteQuota, searchQuota: p.RemoteSearchQuota,
		app: p.QuotaApp, installation: installation, repository: repository, collector: entry.Collector,
		base: base, token: token, max: entry.APIRequests, cache: cache,
		sharedRequirements: sharedRequirements,
		authorize:          p.nativeValueCurrent,
	}
	socket, closeAPI, err := api.listen(ctx)
	if err != nil {
		return err
	}
	defer closeAPI()
	request := struct {
		Root       string          `json:"root"`
		Entry      json.RawMessage `json:"entry"`
		Previous   model.Row       `json:"previous,omitempty"`
		ObservedAt string          `json:"observedAt"`
		Workspace  string          `json:"workspace"`
	}{
		Root: p.CatalogRoot, Entry: entry.Raw, Previous: previous,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Workspace: work,
	}
	input, err := json.Marshal(request)
	if err != nil || len(input) > 8<<20 {
		return errors.New("remote Value invocation exceeds its input budget")
	}
	started := time.Now()
	command := exec.CommandContext(ctx, p.node(), filepath.Join(p.CatalogRoot, "activity", "workflow-values.mjs"), "--worker") // #nosec G204 -- reviewed runtime, private environment.
	command.Dir = p.CatalogRoot
	command.Env = append(collectionEnvironment(""),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+work, "GH_TOKEN=remote-value-proxy-only", "GITHUB_TOKEN=remote-value-proxy-only",
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"CAO_REMOTE_VALUE_SOCKET="+socket)
	command.Stdin = bytes.NewReader(input)
	stdout := &remoteValueOutput{limit: 8 << 20}
	stderr := &remoteValueOutput{limit: 16 << 10}
	failureReason := ""
	defer func() {
		if resultErr != nil && failureReason == "" {
			failureReason = "collector-failed"
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
			GraphQLRequestsUnverified: unverified, OutputBytes: stdout.bytes, TransportBytes: receivedBytes,
			Succeeded: resultErr == nil, FailureReason: failureReason,
		}); costErr != nil {
			resultErr = errors.Join(resultErr, costErr)
		}
	}()
	command.Stdout, command.Stderr = stdout, stderr
	remoteValueProcessGroup(command)
	command.WaitDelay = 5 * time.Second
	if err := p.remoteValueRegistrationCurrent(ctx, repository, installation, entry, policyDigest); err != nil {
		return err
	}
	runErr := command.Run()
	stopRemoteValueProcessGroup(command)
	if runErr != nil || stdout.exceeded || stderr.exceeded {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			failureReason = "timeout"
		case stdout.exceeded || stderr.exceeded:
			failureReason = "output-budget"
		default:
			failureReason = remoteValueFailureReason(stderr.buffer.Bytes())
		}
		return remoteValueFailureError(failureReason)
	}
	if bytes.Equal(bytes.TrimSpace(stdout.buffer.Bytes()), []byte("null")) {
		return nil
	}
	var envelope struct {
		SchemaVersion int       `json:"schema_version"`
		Kind          string    `json:"kind"`
		Packet        model.Row `json:"operational_value_evidence"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.buffer.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil || envelope.SchemaVersion != 2 ||
		envelope.Kind != "operational_value_evidence" || envelope.Packet == nil {
		return errors.New("remote Value collector returned an invalid evidence envelope")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("remote Value collector returned trailing output")
	}
	if !strings.EqualFold(fmt.Sprint(envelope.Packet["repository"]), repository) ||
		envelope.Packet["workflow"] != entry.Workflow {
		return errors.New("remote Value evidence exceeds the reviewed repository or workflow")
	}
	proof, err := readRemoteValueProof(filepath.Join(work, "latest"))
	if err != nil {
		return err
	}
	if p.nativeValueScope != nil {
		if err := p.remoteValueRegistrationCurrent(ctx, repository, installation, entry, policyDigest); err != nil {
			return err
		}
		_, err := p.Data.Acquisition().PublishValue(ctx, *p.nativeValueScope, previous, envelope.Packet, proof, time.Now().UTC())
		return err
	}
	return p.Enrollment.withMutationLock(ctx, func(locked context.Context) error {
		if err := p.remoteValueRegistrationCurrent(locked, repository, installation, entry, policyDigest); err != nil {
			return err
		}
		current, err := p.Data.ReadOperationalValue(locked, repository, entry.Workflow)
		if errors.Is(err, postgresx.ErrValueNotFound) {
			current = nil
		} else if err != nil {
			return err
		}
		if !remoteValuePacketCurrent(previous, current) {
			return fmt.Errorf("%w: canonical remote Value proof changed before publication", ErrRemoteValueDeferred)
		}
		if proof == nil {
			_, err := p.Data.PublishOperationalValue(locked, envelope.Packet)
			return err
		}
		_, err = p.Data.PublishOperationalValueWithProof(
			locked, envelope.Packet, repository, entry.Workflow, proof, time.Now().UTC())
		return err
	})
}

func remoteValueFailureReason(stderr []byte) string {
	for _, line := range bytes.Split(stderr, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte(remoteValueFailurePrefix)) {
			continue
		}
		var diagnostic struct {
			ReasonCode string `json:"reasonCode"`
		}
		if err := json.Unmarshal(bytes.TrimPrefix(line, []byte(remoteValueFailurePrefix)), &diagnostic); err != nil {
			continue
		}
		switch diagnostic.ReasonCode {
		case "api-budget", "budget-unavailable", "timeout", "collector-failed":
			return diagnostic.ReasonCode
		}
	}
	return "collector-failed"
}

func remoteValueFailureError(reason string) error {
	switch reason {
	case "api-budget":
		return errors.New("remote Value collector exhausted its reviewed API request grant; previous evidence retained")
	case "budget-unavailable":
		return errors.New("remote Value collector API budget state was unavailable; previous evidence retained")
	case "timeout":
		return errors.New("remote Value collector command timed out; previous evidence retained")
	case "output-budget":
		return errors.New("remote Value collector exceeded its output budget; previous evidence retained")
	default:
		return errors.New("remote Value collector failed; previous evidence retained")
	}
}

func remoteValuePacketCurrent(start, current model.Row) bool {
	if (start == nil) != (current == nil) {
		return false
	}
	before, beforeErr := json.Marshal(start)
	after, afterErr := json.Marshal(current)
	return beforeErr == nil && afterErr == nil && bytes.Equal(before, after)
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

type remoteValueOutput struct {
	buffer   bytes.Buffer
	limit    int
	bytes    int64
	exceeded bool
}

func (w *remoteValueOutput) Write(content []byte) (int, error) {
	w.bytes += int64(len(content))
	if w.bytes > int64(w.limit) {
		w.exceeded = true
		return 0, errors.New("remote Value output exceeds its byte budget")
	}
	return w.buffer.Write(content)
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

func restoreRemoteValueProof(
	ctx context.Context, store *postgresx.Store, repository, workflow, destination string,
) error {
	files, err := store.ReadValueProof(ctx, repository, workflow)
	if err != nil {
		return err
	}
	return writeRemoteValueProof(files, destination)
}

func writeRemoteValueProof(files []postgresx.ValueProofFile, destination string) error {
	if len(files) == 0 {
		return nil
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, file := range files {
		parent := filepath.Dir(file.Path)
		if parent != "." {
			if err := root.MkdirAll(parent, 0o700); err != nil {
				return err
			}
		}
		if err := root.WriteFile(file.Path, file.Content, 0o600); err != nil {
			return err
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
