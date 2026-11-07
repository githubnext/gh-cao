package collect

import (
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- Git repository object IDs use SHA-1 by design.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	fileDietHistoricalRevision = "0782148c0af21f4f1d10b7cb5e81b571ed58c463"
	fileDietHistoricalPath     = "docs/operational-value/proof/file-diet-lifetime"
	fileDietGitOutputLimit     = 128 << 20
	fileDietGitWorkspaceLimit  = 512 << 20
)

// Git reads only public immutable objects from the two reviewed coordinates.
// No installation credential or ambient Git configuration crosses this boundary.
type nativeFileDietGit struct {
	binary string
	dir    string
}

func newNativeFileDietGit(ctx context.Context, workspace, repository string) (*nativeFileDietGit, error) {
	if repository != dailyFileDietRepository && repository != "githubnext/gh-cao" {
		return nil, errors.New("unreviewed File Diet git repository")
	}
	binary, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("reviewed File Diet immutable Git transport is unavailable")
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return nil, err
	}
	git := &nativeFileDietGit{binary: binary, dir: filepath.Join(workspace, "git")}
	if _, err := git.run(ctx, nil, "init", "--bare", "--quiet", git.dir); err != nil {
		return nil, err
	}
	if _, err := git.run(ctx, nil, "remote", "add", "origin", "https://github.com/"+repository+".git"); err != nil {
		return nil, err
	}
	for _, args := range [][]string{
		{"config", "remote.origin.promisor", "true"},
		{"config", "remote.origin.partialclonefilter", "blob:none"},
		{"config", "core.hooksPath", "/dev/null"},
	} {
		if _, err := git.run(ctx, nil, args...); err != nil {
			return nil, err
		}
	}
	return git, nil
}

func (g *nativeFileDietGit) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, 240*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, g.binary, append([]string{"--git-dir", g.dir}, args...)...) // #nosec G204 -- pinned executable, bounded reviewed immutable object operations.
	command.Env = []string{
		"PATH=" + filepath.Dir(g.binary) + ":/usr/bin:/bin",
		"HOME=" + filepath.Dir(g.dir),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false", "GIT_SSH_COMMAND=false",
		"GIT_OPTIONAL_LOCKS=0", "GIT_LFS_SKIP_SMUDGE=1",
		"GIT_HTTP_MAX_REQUESTS=1", "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=protocol.file.allow", "GIT_CONFIG_VALUE_0=never",
	}
	command.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	command.Stdout = &boundedGitOutput{writer: &stdout, remaining: fileDietGitOutputLimit}
	command.Stderr = &boundedGitOutput{writer: io.Discard, remaining: 16 << 10}
	remoteValueProcessGroup(command)
	command.WaitDelay = 5 * time.Second
	err := command.Run()
	stopRemoteValueProcessGroup(command)
	if err != nil {
		if runCtx.Err() != nil {
			return nil, errors.New("reviewed File Diet Git acquisition timed out")
		}
		return nil, errors.New("reviewed File Diet Git acquisition failed")
	}
	return stdout.Bytes(), nil
}

type boundedGitOutput struct {
	writer    io.Writer
	remaining int
}

func (w *boundedGitOutput) Write(value []byte) (int, error) {
	if len(value) > w.remaining {
		return 0, errors.New("reviewed File Diet Git output budget exceeded")
	}
	w.remaining -= len(value)
	return w.writer.Write(value)
}

func validFileDietSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func (g *nativeFileDietGit) fetch(ctx context.Context, revisions []string, blobs bool) error {
	values := slices.Clone(revisions)
	slices.Sort(values)
	values = slices.Compact(values)
	for _, value := range values {
		if !validFileDietSHA(value) {
			return errors.New("unreviewed immutable Git object identity")
		}
	}
	limit := 100
	if blobs {
		limit = 50
	}
	for start := 0; start < len(values); start += limit {
		end := min(start+limit, len(values))
		args := []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head"}
		if !blobs {
			args = append(args, "--depth=1", "--filter=blob:none")
		}
		args = append(args, "origin")
		if blobs {
			args = append(args, "--stdin")
			if _, err := g.run(ctx, []byte(strings.Join(values[start:end], "\n")+"\n"), args...); err != nil {
				return err
			}
		} else {
			if _, err := g.run(ctx, nil, append(args, values[start:end]...)...); err != nil {
				return err
			}
		}
		if err := fileDietGitWorkspaceWithinBudget(g.dir); err != nil {
			return err
		}
	}
	return nil
}

func fileDietGitWorkspaceWithinBudget(root string) error {
	var total int64
	files := 0
	return filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("reviewed File Diet Git workspace contains a symlink")
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		files++
		if total > fileDietGitWorkspaceLimit || files > 100000 {
			return errors.New("reviewed File Diet Git workspace exceeds its byte or file budget")
		}
		return nil
	})
}

type fileDietTreeEntry struct {
	mode string
	kind string
	sha  string
	path string
}

func parseFileDietTree(content []byte, prefix string, max int) ([]fileDietTreeEntry, error) {
	rows := bytes.Split(bytes.TrimSuffix(content, []byte{0}), []byte{0})
	if len(rows) > max {
		return nil, errors.New("immutable Git tree exceeds File Diet budget")
	}
	result := make([]fileDietTreeEntry, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		header, path, ok := bytes.Cut(row, []byte{'\t'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 || !validFileDietSHA(fields[2]) ||
			strings.ContainsRune(string(path), 0) || !strings.HasPrefix(string(path), prefix) {
			return nil, errors.New("invalid immutable File Diet Git tree")
		}
		result = append(result, fileDietTreeEntry{mode: fields[0], kind: fields[1], sha: fields[2], path: string(path)})
	}
	return result, nil
}

func (g *nativeFileDietGit) tree(ctx context.Context, commit, path string, limit int) ([]fileDietTreeEntry, error) {
	if !validFileDietSHA(commit) || path != "pkg" && path != fileDietHistoricalPath {
		return nil, errors.New("unreviewed File Diet Git tree scope")
	}
	output, err := g.run(ctx, nil, "ls-tree", "-r", "-z", commit, "--", path)
	if err != nil {
		return nil, err
	}
	return parseFileDietTree(output, path+"/", limit)
}

func (g *nativeFileDietGit) firstParent(ctx context.Context, commit string) (string, error) {
	if !validFileDietSHA(commit) {
		return "", errors.New("invalid immutable merge revision")
	}
	output, err := g.run(ctx, nil, "cat-file", "-p", commit)
	if err != nil {
		return "", err
	}
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if parent, ok := bytes.CutPrefix(line, []byte("parent ")); ok && validFileDietSHA(string(parent)) {
			return string(parent), nil
		}
		if len(line) == 0 {
			break
		}
	}
	return "", errors.New("accepted revision lacks an actual first parent")
}

func (g *nativeFileDietGit) blob(ctx context.Context, sha string, maxBytes int) ([]byte, error) {
	if !validFileDietSHA(sha) {
		return nil, errors.New("invalid immutable blob identity")
	}
	output, err := g.run(ctx, []byte(sha+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	header, rest, ok := bytes.Cut(output, []byte{'\n'})
	fields := strings.Fields(string(header))
	if !ok || len(fields) != 3 || fields[0] != sha || fields[1] != "blob" {
		return nil, errors.New("immutable Git blob identity mismatch")
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil || size < 0 || size > maxBytes || len(rest) != size+1 || rest[size] != '\n' {
		return nil, errors.New("immutable File Diet blob exceeds its byte budget")
	}
	content := rest[:size]
	digest := sha1.New() // #nosec G401 -- Git object identity uses SHA-1, not a security signature.
	_, _ = fmt.Fprintf(digest, "blob %d\x00", size)
	_, _ = digest.Write(content)
	if fmt.Sprintf("%x", digest.Sum(nil)) != sha {
		return nil, errors.New("native Git blob digest mismatch")
	}
	return content, nil
}

func (g *nativeFileDietGit) measureBlobs(ctx context.Context, shas []string) (map[string][2]int, error) {
	if len(shas) == 0 || len(shas) > 50 {
		return nil, errors.New("reviewed File Diet blob batch exceeds its request budget")
	}
	for _, sha := range shas {
		if !validFileDietSHA(sha) {
			return nil, errors.New("invalid immutable blob identity")
		}
	}
	output, err := g.run(ctx, []byte(strings.Join(shas, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	return parseFileDietBlobMeasurements(output, shas)
}

func parseFileDietBlobMeasurements(output []byte, shas []string) (map[string][2]int, error) {
	measurements := make(map[string][2]int, len(shas))
	cursor := 0
	for _, sha := range shas {
		if !validFileDietSHA(sha) {
			return nil, errors.New("invalid immutable blob identity")
		}
		end := bytes.IndexByte(output[cursor:], '\n')
		if end < 0 {
			return nil, errors.New("malformed immutable Git blob header")
		}
		fields := strings.Fields(string(output[cursor : cursor+end]))
		if len(fields) != 3 || fields[0] != sha || fields[1] != "blob" {
			return nil, errors.New("immutable Git blob identity mismatch")
		}
		size, err := strconv.Atoi(fields[2])
		cursor += end + 1
		if err != nil || size < 0 || size >= fileDietGitOutputLimit ||
			size+1 > len(output)-cursor || output[cursor+size] != '\n' {
			return nil, errors.New("immutable File Diet blob exceeds its batch byte budget")
		}
		content := output[cursor : cursor+size]
		digest := sha1.New() // #nosec G401 -- Git object identity uses SHA-1, not a security signature.
		_, _ = fmt.Fprintf(digest, "blob %d\x00", size)
		_, _ = digest.Write(content)
		if fmt.Sprintf("%x", digest.Sum(nil)) != sha {
			return nil, errors.New("native Git blob digest mismatch")
		}
		measurements[sha] = [2]int{bytes.Count(content, []byte{'\n'}), size}
		cursor += size + 1
	}
	if cursor != len(output) {
		return nil, errors.New("unexpected trailing immutable Git blob content")
	}
	return measurements, nil
}

func fileDietGitWorkspace(root string) (string, error) {
	if root == "" {
		return "", errors.New("reviewed File Diet requires a private catalog workspace")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("reviewed File Diet catalog workspace is unsafe")
	}
	return os.MkdirTemp(root, ".cao-file-diet-")
}
