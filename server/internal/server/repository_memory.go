package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/repositorymemory"
)

var repositoryMemoryLog = logger.New("cao:server:repository-memory")
var errRepositoryMemoryIntegrityFailure = errors.New("repository-memory file failed integrity validation")

func (a *App) repositoryMemoryDirectory() string {
	if a.config.Canvas != nil {
		return a.config.Canvas.MemoryDirectory
	}
	return a.config.SourceDirectory
}

func (a *App) repositoryMemoryCampaign(response http.ResponseWriter, request *http.Request) {
	campaignID := request.PathValue("campaign")
	if !repositorymemory.ValidCampaign(campaignID) {
		writeError(request.Context(), response, http.StatusBadRequest, "repository-memory campaign is invalid")
		return
	}
	var campaign repositorymemory.Campaign
	campaign, err := repositoryMemorySnapshot(request.Context(), a.repositoryMemoryDirectory(), campaignID)
	if err != nil && a.memory != nil &&
		errors.Is(err, errCanonicalEntityNotFound) {
		resolved, resolveErr := a.memory.Campaign(request.Context(), campaignID)
		if resolveErr != nil {
			writeRepositoryMemoryError(request.Context(), response, resolveErr)
			return
		}
		if resolved == nil {
			writeJSON(response, http.StatusOK, nil)
			return
		}
		campaign = *resolved
		err = nil
	}
	if errors.Is(err, errCanonicalEntityNotFound) {
		writeJSON(response, http.StatusOK, nil)
		return
	}
	if err != nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "repository memory is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, campaign)
}

func (a *App) repositoryMemoryContent(response http.ResponseWriter, request *http.Request) {
	campaignID := request.PathValue("campaign")
	filePath := request.URL.Query().Get("path")
	if !repositorymemory.ValidCampaign(campaignID) || !repositorymemory.ValidPath(filePath) {
		writeError(request.Context(), response, http.StatusBadRequest, "repository-memory file path is invalid")
		return
	}
	var content []byte
	err := func(ctx context.Context) error {
		campaign, err := repositoryMemorySnapshot(ctx, a.repositoryMemoryDirectory(), campaignID)
		if err != nil {
			return err
		}
		var selected *repositorymemory.File
		for index := range campaign.Files {
			if campaign.Files[index].Path == filePath {
				selected = &campaign.Files[index]
				break
			}
		}
		if selected == nil {
			return repositorymemory.ErrNotFound
		}
		snapshot, err := repositorymemory.Load(a.repositoryMemoryDirectory())
		if err != nil {
			return err
		}
		value, ok := snapshot.Files[repositorymemory.FileKey(campaignID, filePath)]
		if !ok {
			return errors.New("repository-memory file content is invalid")
		}
		content = value
		if reason, ok := repositoryMemoryIntegrityFailure(content, *selected); !ok {
			repositoryMemoryLog.PrintfContext(ctx, "repository-memory file failed integrity validation reason=%s", reason)
			return errRepositoryMemoryIntegrityFailure
		}
		return nil
	}(request.Context())
	if err != nil && a.memory != nil &&
		errors.Is(err, errCanonicalEntityNotFound) {
		content, resolveErr := a.memory.Content(request.Context(), campaignID, filePath)
		if errors.Is(resolveErr, repositorymemory.ErrNotFound) ||
			(resolveErr == nil && content == nil) {
			writeError(request.Context(), response, http.StatusNotFound, "repository-memory file was not found")
			return
		}
		if resolveErr != nil {
			writeRepositoryMemoryError(request.Context(), response, resolveErr)
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"content": string(content)})
		return
	}
	if errors.Is(err, errCanonicalEntityNotFound) {
		writeError(request.Context(), response, http.StatusNotFound, "repository-memory branch was not found")
		return
	}
	if errors.Is(err, repositorymemory.ErrNotFound) {
		writeError(request.Context(), response, http.StatusNotFound, "repository-memory file was not found")
		return
	}
	if errors.Is(err, errRepositoryMemoryIntegrityFailure) {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "repository-memory file failed integrity validation")
		return
	}
	if err != nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "repository memory is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"content": string(content)})
}

// repositoryMemoryIntegrityReason names why repositoryMemoryIntegrityFailure
// rejected a fetched file, stable across message wording changes so it is
// useful to log without exposing the file's path or content.
type repositoryMemoryIntegrityReason string

const (
	repositoryMemoryIntegrityReasonSizeMismatch   repositoryMemoryIntegrityReason = "size-mismatch"
	repositoryMemoryIntegrityReasonSHA256Mismatch repositoryMemoryIntegrityReason = "sha256-mismatch"
)

// repositoryMemoryIntegrityFailure validates fetched content against the
// manifest's recorded size and, when present, SHA-256 checksum. It is a pure
// function extracted from repositoryMemoryContent so the size-mismatch and
// checksum-mismatch rejection paths are independently testable without a
// source document. ok is false when content fails validation; reason then
// names which check failed.
func repositoryMemoryIntegrityFailure(content []byte, expected repositorymemory.File) (reason repositoryMemoryIntegrityReason, ok bool) {
	if int64(len(content)) != expected.Size {
		return repositoryMemoryIntegrityReasonSizeMismatch, false
	}
	if expected.SHA256 != "" {
		sum := sha256.Sum256(content)
		if !strings.EqualFold(expected.SHA256, hex.EncodeToString(sum[:])) {
			return repositoryMemoryIntegrityReasonSHA256Mismatch, false
		}
	}
	return "", true
}

func repositoryMemorySnapshot(
	ctx context.Context, directory, campaignID string,
) (repositorymemory.Campaign, error) {
	if err := ctx.Err(); err != nil {
		return repositorymemory.Campaign{}, err
	}
	if directory == "" {
		return repositorymemory.Campaign{}, errCanonicalEntityNotFound
	}
	snapshot, err := repositorymemory.Load(directory)
	if err != nil {
		return repositorymemory.Campaign{}, err
	}
	var manifest repositorymemory.Manifest
	if err := json.Unmarshal(snapshot.Manifest, &manifest); err != nil || manifest.Version != 1 {
		return repositorymemory.Campaign{}, errors.New("repository-memory manifest is invalid")
	}
	for _, campaign := range manifest.Campaigns {
		if campaign.Campaign == campaignID {
			return campaign, nil
		}
	}
	return repositorymemory.Campaign{}, errCanonicalEntityNotFound
}

func writeRepositoryMemoryError(ctx context.Context, response http.ResponseWriter, err error) {
	if retryAfter, throttled := repositorymemory.RetryAfterSeconds(err); throttled {
		response.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		writeError(ctx, response, http.StatusTooManyRequests, "repository memory is temporarily throttled")
		return
	}
	writeError(ctx, response, http.StatusServiceUnavailable, "repository memory is unavailable")
}
