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
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var ErrRemoteValueDeferred = errors.New("remote Value acquisition deferred")

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
	apiBase, err := remoteValueAPIBase(p.GitHubAPIURL)
	if err != nil {
		return err
	}
	for _, entry := range selected {
		if err := p.collectRemoteValue(ctx, name, installation, entry, policyDigest, apiBase); err != nil {
			return err
		}
	}
	return nil
}

func (p Projector) collectRemoteValue(
	ctx context.Context, repository string, installation int64, entry remoteValueEntry,
	policyDigest [sha256.Size]byte, base *url.URL,
) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	previous, err := p.Data.ReadOperationalValue(ctx, repository, entry.Workflow)
	switch {
	case errors.Is(err, postgresx.ErrValueParentMissing):
		return fmt.Errorf("%w: canonical workflow parent has not been projected", ErrRemoteValueDeferred)
	case errors.Is(err, postgresx.ErrValueNotFound):
		previous = nil
	case err != nil:
		return err
	}
	root, err := p.remoteValueWorkspace(repository, entry.Workflow)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(root, ".invocation-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if err := copyRemoteValueProof(filepath.Join(root, "latest"), filepath.Join(work, "latest"), 64<<20); err != nil {
		return err
	}
	bin := filepath.Join(work, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(remoteValueGH), 0o700); err != nil {
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
		base: base, token: token, max: entry.APIRequests,
	}
	socket, closeAPI, err := api.listen()
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
	defer func() {
		apiCalls, preflights, points, unverified, receivedBytes := api.costUsage()
		if costErr := appendRemoteValueCost(root, remoteValueCost{
			At: started.UTC(), APIRequests: apiCalls, WallMs: time.Since(started).Milliseconds(),
			PreflightRequests: preflights, GraphQLPointsObserved: points,
			GraphQLRequestsUnverified: unverified, OutputBytes: stdout.bytes, TransportBytes: receivedBytes,
			Succeeded: resultErr == nil,
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
		return errors.New("remote Value collector failed or exceeded its budget; previous evidence retained")
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
		if _, err := p.Data.PublishOperationalValue(locked, envelope.Packet); err != nil {
			return err
		}
		return promoteRemoteValueProof(root, work)
	})
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
			current.Collector == entry.Collector && current.APIRequests == entry.APIRequests {
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

func (p Projector) remoteValueWorkspace(repository, workflow string) (string, error) {
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
	previous, err := readLocalValueFile(context.Background(), identityPath, 4096)
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
func (p Projector) ForgetRemoteValueWorkspace(repository string) error {
	if !filepath.IsAbs(p.Lake.Directory) || strings.TrimSpace(p.Lake.Directory) == "" {
		return errors.New("remote Value requires an absolute private lake directory")
	}
	name, err := NormalizeRepository(repository)
	if err != nil {
		return err
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
		identity, readErr := readLocalValueFile(context.Background(), filepath.Join(directory, "identity.json"), 4096)
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

func copyRemoteValueProof(source, destination string, remaining int64) error {
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("retained remote Value proof is not a directory")
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if !info.Mode().IsRegular() || info.Size() > remaining {
			return errors.New("retained remote Value proof exceeds its confined byte budget")
		}
		remaining -= info.Size()
		content, err := os.ReadFile(path) // #nosec G304 -- walked beneath private workspace, symlinks rejected.
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	})
}

func promoteRemoteValueProof(root, work string) error {
	next := filepath.Join(work, "latest")
	if _, err := os.Lstat(next); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	old := filepath.Join(root, "latest")
	backup := filepath.Join(work, "previous")
	if err := os.Rename(old, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(next, old); err != nil {
		_ = os.Rename(backup, old)
		return err
	}
	return nil
}

type remoteValueCost struct {
	At                        time.Time `json:"at"`
	APIRequests               int       `json:"apiRequests"`
	PreflightRequests         int       `json:"preflightRequests"`
	GraphQLPointsObserved     int       `json:"graphqlPointsObserved"`
	GraphQLRequestsUnverified int       `json:"graphqlRequestsUnverified"`
	WallMs                    int64     `json:"wallMs"`
	OutputBytes               int64     `json:"outputBytes"`
	TransportBytes            int64     `json:"transportBytes"`
	Succeeded                 bool      `json:"succeeded"`
}

func appendRemoteValueCost(root string, record remoteValueCost) error {
	filename := filepath.Join(root, "collection-cost.json")
	history := struct {
		SchemaVersion             int               `json:"schemaVersion"`
		Attempts                  int64             `json:"attempts"`
		APIRequests               int64             `json:"apiRequests"`
		Preflights                int64             `json:"preflightRequests"`
		GraphQLPointsObserved     int64             `json:"graphqlPointsObserved"`
		GraphQLRequestsUnverified int64             `json:"graphqlRequestsUnverified"`
		WallMs                    int64             `json:"wallMs"`
		OutputBytes               int64             `json:"outputBytes"`
		TransportBytes            int64             `json:"transportBytes"`
		Recent                    []remoteValueCost `json:"recent"`
	}{SchemaVersion: 1}
	info, err := os.Lstat(filename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && (!info.Mode().IsRegular() || info.Size() > 128<<10) {
		return errors.New("remote Value cost history exceeds its byte budget")
	}
	if err == nil {
		content, err := os.ReadFile(filename) // #nosec G304 -- bounded regular file in private workspace.
		if err != nil {
			return err
		}
		if err := json.Unmarshal(content, &history); err != nil || history.SchemaVersion != 1 ||
			history.Attempts < 0 || history.APIRequests < 0 || history.Preflights < 0 ||
			history.GraphQLPointsObserved < 0 || history.GraphQLRequestsUnverified < 0 ||
			history.WallMs < 0 || history.OutputBytes < 0 || history.TransportBytes < 0 ||
			len(history.Recent) > 30 {
			return errors.New("remote Value cost history is invalid")
		}
	}
	history.Attempts++
	history.APIRequests += int64(record.APIRequests)
	history.Preflights += int64(record.PreflightRequests)
	history.GraphQLPointsObserved += int64(record.GraphQLPointsObserved)
	history.GraphQLRequestsUnverified += int64(record.GraphQLRequestsUnverified)
	history.WallMs += record.WallMs
	history.OutputBytes += record.OutputBytes
	history.TransportBytes += record.TransportBytes
	history.Recent = append(history.Recent, record)
	if len(history.Recent) > 30 {
		history.Recent = history.Recent[len(history.Recent)-30:]
	}
	content, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return WriteFileAtomic(filename, append(content, '\n'))
}
