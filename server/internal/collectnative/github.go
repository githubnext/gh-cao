package collectnative

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
)

type TokenProvider interface {
	InstallationToken(context.Context, int64) (string, error)
}

// GitHub acquires directly from the Actions REST API. Quota must already have an
// authoritative installation observation; unknown capacity fails closed.
type GitHub struct {
	BaseURL string
	HTTP    *http.Client
	Tokens  TokenProvider
	Quota   *githubquota.Service
	App     string
	Limits  Limits
}

func (g GitHub) base() (*url.URL, error) {
	value := g.BaseURL
	if value == "" {
		value = "https://api.github.com/"
	}
	base, err := url.Parse(value)
	if err != nil || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Host == "" {
		return nil, errors.New("invalid native acquisition API endpoint")
	}
	address := net.ParseIP(base.Hostname())
	if base.Scheme != "https" && (base.Scheme != "http" || address == nil || !address.IsLoopback()) {
		return nil, errors.New("native acquisition requires HTTPS or numeric loopback")
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	return base, nil
}

func (g GitHub) client() *http.Client {
	client := http.Client{Timeout: time.Minute}
	if g.HTTP != nil {
		client = *g.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 {
		client.Timeout = time.Minute
	}
	return &client
}

func (g GitHub) get(ctx context.Context, installation int64, endpoint string) ([]byte, http.Header, int, error) {
	base, err := g.base()
	if err != nil {
		return nil, nil, 0, err
	}
	if g.Tokens == nil || g.Quota == nil || g.App == "" || installation <= 0 {
		return nil, nil, 0, errors.New("native acquisition requires scoped credentials and shared quota")
	}
	relative, err := url.Parse(endpoint)
	if err != nil || relative.IsAbs() || relative.Host != "" || strings.HasPrefix(endpoint, "/") {
		return nil, nil, 0, ErrIdentity
	}
	reservation, err := g.Quota.Reserve(ctx, githubquota.BucketID{
		App: g.App, Installation: installation, Resource: githubquota.ResourceCore,
	}, githubquota.ReservationRequest{EstimatedCost: 1})
	if err != nil {
		return nil, nil, 0, err
	}
	token, err := g.Tokens.InstallationToken(ctx, installation)
	if err != nil {
		return nil, nil, 0, errors.Join(err, g.Quota.Release(ctx, reservation))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.ResolveReference(relative).String(), nil)
	if err != nil {
		return nil, nil, 0, errors.Join(ErrIdentity, g.Quota.Release(ctx, reservation))
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := g.client().Do(request)
	if err != nil {
		// An uncertain network outcome keeps its reservation until expiry.
		if ctx.Err() != nil {
			return nil, nil, 0, ctx.Err()
		}
		return nil, nil, 0, errors.New("native acquisition API transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	quota, err := g.Quota.RecordResponse(ctx, g.App, installation, response.Header, response.StatusCode)
	if err != nil {
		return nil, nil, 0, err
	}
	if quota.HasObservation {
		if err := g.Quota.Commit(ctx, reservation, quota.Observation); err != nil {
			return nil, nil, 0, err
		}
	}
	if response.StatusCode == http.StatusFound {
		return nil, response.Header.Clone(), response.StatusCode, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, nil, response.StatusCode, fmt.Errorf("native acquisition API status %d", response.StatusCode)
	}
	content, err := readBounded(response.Body, g.Limits.defaults().Bytes)
	return content, response.Header.Clone(), response.StatusCode, err
}

func readBounded(reader io.Reader, limit int) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, errors.New("native acquisition response read failed")
	}
	if len(content) > limit {
		return nil, ErrCapacity
	}
	return content, nil
}

func (g GitHub) Attempt(ctx context.Context, identity Identity) (Run, error) {
	if err := identity.Validate(); err != nil {
		return Run{}, err
	}
	content, _, _, err := g.get(ctx, identity.InstallationID, fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d",
		identity.Repository, identity.RunID, identity.Attempt))
	if err != nil {
		return Run{}, err
	}
	var fields map[string]any
	if err := decode(content, &fields); err != nil {
		return Run{}, errors.New("invalid native run attempt")
	}
	run, err := githubRun(fields, identity.Repository, identity.InstallationID)
	if err != nil || run.Identity != identity {
		return Run{}, ErrIdentity
	}
	return run, nil
}

func githubRun(fields map[string]any, repository string, installation int64) (Run, error) {
	runID, idErr := strconv.ParseInt(scalar(fields["id"]), 10, 64)
	attempt, attemptErr := strconv.Atoi(scalar(fields["run_attempt"]))
	identity := Identity{repository, installation, runID, attempt}
	name := scalar(fields["path"])
	if idErr != nil || attemptErr != nil || identity.Validate() != nil ||
		!workflowPath.MatchString(name) || strings.ToLower(scalar(object(fields["repository"])["full_name"])) != repository {
		return Run{}, ErrIdentity
	}
	return Run{Identity: identity, WorkflowPath: name, Fields: fields}, nil
}

func (g GitHub) Runs(ctx context.Context, repository string, installation int64, since time.Time, visit func(Run) error) error {
	if !coordinate.MatchString(repository) || installation <= 0 {
		return ErrIdentity
	}
	limits := g.Limits.defaults()
	var workflows []int64
	for page := 1; ; page++ {
		if page > limits.Pages {
			return ErrCapacity
		}
		content, _, _, err := g.get(ctx, installation, fmt.Sprintf("repos/%s/actions/workflows?per_page=100&page=%d", repository, page))
		if err != nil {
			return err
		}
		var result struct {
			Workflows []struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			} `json:"workflows"`
		}
		if err := decode(content, &result); err != nil || result.Workflows == nil {
			return errors.New("invalid native workflow listing")
		}
		for _, workflow := range result.Workflows {
			if !strings.HasSuffix(workflow.Path, ".lock.yml") {
				continue
			}
			if !workflowPath.MatchString(workflow.Path) || workflow.ID <= 0 {
				return ErrIdentity
			}
			workflows = append(workflows, workflow.ID)
			if len(workflows) > limits.Runs {
				return ErrCapacity
			}
		}
		if len(result.Workflows) < 100 {
			break
		}
	}
	slices.Sort(workflows)
	seen := map[int64]bool{}
	for _, workflow := range slices.Compact(workflows) {
		for page := 1; ; page++ {
			if page > limits.Pages {
				return ErrCapacity
			}
			endpoint := fmt.Sprintf("repos/%s/actions/workflows/%d/runs?per_page=100&page=%d", repository, workflow, page)
			if !since.IsZero() {
				endpoint += "&created=" + url.QueryEscape(">="+since.UTC().Format(time.RFC3339))
			}
			content, _, _, err := g.get(ctx, installation, endpoint)
			if err != nil {
				return err
			}
			var result struct {
				Runs []map[string]any `json:"workflow_runs"`
			}
			if err := decode(content, &result); err != nil || result.Runs == nil {
				return errors.New("invalid native run listing")
			}
			for _, fields := range result.Runs {
				run, err := githubRun(fields, repository, installation)
				if err != nil {
					return err
				}
				if seen[run.RunID] {
					continue
				}
				seen[run.RunID] = true
				if len(seen) > limits.Runs {
					return ErrCapacity
				}
				if err := visit(run); err != nil {
					return err
				}
			}
			if len(result.Runs) < 100 {
				break
			}
		}
	}
	return nil
}

func artifactName(name string) bool {
	if len(name) > 256 || !artifactIdentifier.MatchString(name) {
		return false
	}
	for _, suffix := range []string{"usage", "experiment", "experiments", "graders", "evals"} {
		if name == suffix || strings.HasSuffix(name, "-"+suffix) {
			return true
		}
	}
	return false
}

func (g GitHub) Artifacts(ctx context.Context, run Run, visit func(Artifact) error) error {
	if run.Validate() != nil {
		return ErrIdentity
	}
	limits := g.Limits.defaults()
	count, acquired := 0, 0
	for page := 1; ; page++ {
		if page > limits.Pages {
			return ErrCapacity
		}
		content, _, _, err := g.get(ctx, run.InstallationID, fmt.Sprintf("repos/%s/actions/runs/%d/artifacts?per_page=100&page=%d", run.Repository, run.RunID, page))
		if err != nil {
			return err
		}
		var result struct {
			Artifacts []struct {
				ID          int64  `json:"id"`
				Name        string `json:"name"`
				Expired     bool   `json:"expired"`
				WorkflowRun struct {
					ID int64 `json:"id"`
				} `json:"workflow_run"`
			} `json:"artifacts"`
		}
		if err := decode(content, &result); err != nil || result.Artifacts == nil {
			return errors.New("invalid native artifact listing")
		}
		for _, artifact := range result.Artifacts {
			count++
			if count > limits.Artifacts {
				return ErrCapacity
			}
			if !artifactName(artifact.Name) || artifact.Expired {
				continue
			}
			if artifact.ID <= 0 || artifact.WorkflowRun.ID != run.RunID {
				return ErrIdentity
			}
			archive, err := g.archive(ctx, run, artifact.ID)
			if err != nil {
				return err
			}
			acquired += len(archive)
			if acquired > limits.Bytes {
				return ErrCapacity
			}
			files, size, err := unpack(ctx, archive, limits)
			acquired += size
			if err != nil {
				return err
			}
			if acquired > limits.Bytes {
				return ErrCapacity
			}
			if err := visit(Artifact{ID: artifact.ID, Name: artifact.Name, Files: files}); err != nil {
				return err
			}
		}
		if len(result.Artifacts) < 100 {
			return nil
		}
	}
}

func (g GitHub) archive(ctx context.Context, run Run, id int64) ([]byte, error) {
	content, headers, status, err := g.get(ctx, run.InstallationID, fmt.Sprintf("repos/%s/actions/artifacts/%d/zip", run.Repository, id))
	if err != nil || status == http.StatusOK {
		return content, err
	}
	location, err := url.Parse(headers.Get("Location"))
	if err != nil || location.Scheme != "https" || location.Host == "" || location.User != nil || location.Fragment != "" {
		return nil, errors.New("invalid native artifact download redirect")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, ErrIdentity
	}
	// Signed object storage receives no installation credentials. Redirects are
	// not followed, and transport errors must not expose the signed URL.
	response, err := g.client().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("native artifact download transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("native artifact download status %d", response.StatusCode)
	}
	return readBounded(response.Body, g.Limits.defaults().Bytes)
}

func unpack(ctx context.Context, archive []byte, limits Limits) (map[string][]byte, int, error) {
	// Reject excessive central-directory cardinality before archive/zip allocates
	// per-member objects. ZIP64 is intentionally unsupported for bounded evidence.
	end := bytes.LastIndex(archive, []byte{'P', 'K', 5, 6})
	if end < 0 || len(archive)-end < 22 || int(binary.LittleEndian.Uint16(archive[end+20:])) != len(archive)-end-22 {
		return nil, 0, errors.New("invalid native artifact ZIP directory")
	}
	members := int(binary.LittleEndian.Uint16(archive[end+10:]))
	if members == 65535 || members > limits.Entries {
		return nil, 0, ErrCapacity
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, 0, errors.New("invalid native artifact ZIP")
	}
	if len(reader.File) > limits.Entries {
		return nil, 0, ErrCapacity
	}
	files := map[string][]byte{}
	names := map[string]bool{}
	total := 0
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		name := strings.TrimSuffix(file.Name, "/")
		if !fs.ValidPath(name) || strings.Contains(name, "\\") || file.Mode()&fs.ModeSymlink != 0 || names[name] {
			return nil, 0, errors.New("unsafe or duplicate native artifact member")
		}
		names[name] = true
		if file.FileInfo().IsDir() {
			continue
		}
		if !file.Mode().IsRegular() {
			return nil, 0, errors.New("non-regular native artifact member")
		}
		remaining := limits.Bytes - total
		if remaining < 0 || file.UncompressedSize64 > uint64(remaining) {
			return nil, 0, ErrCapacity
		}
		stream, err := file.Open()
		if err != nil {
			return nil, 0, errors.New("open native artifact member failed")
		}
		value, readErr := readBounded(stream, limits.Bytes-total)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			return nil, 0, errors.Join(readErr, closeErr)
		}
		total += len(value)
		switch path.Base(name) {
		case "aw_info.json", "aw_session.jsonl", "assignments.json", "grader_results.json", "grader_manifest.json", "evals.jsonl":
			files[name] = value
		}
	}
	return files, total, nil
}
