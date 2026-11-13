package repositorymemory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/logger"
)

var remoteLog = logger.New("cao:repositorymemory:remote")

const (
	remoteCampaignTTL  = 5 * time.Minute
	remoteFileTTL      = time.Hour
	remoteLockTTL      = 2 * time.Minute
	remoteOperationTTL = 90 * time.Second
)

var ErrNotFound = errors.New("repository memory was not found")

// RemoteCache is the operational storage used by lazy GitHub resolution.
type RemoteCache interface {
	CachedRepositoryMemoryCampaign(context.Context, string) ([]byte, error)
	CacheRepositoryMemoryCampaign(context.Context, string, []byte, time.Duration) error
	CachedRepositoryMemoryFile(context.Context, string, string, string) ([]byte, error)
	CacheRepositoryMemoryFile(context.Context, string, string, string, []byte, time.Duration) error
	TryLock(context.Context, string, string, time.Duration) (bool, error)
	Unlock(context.Context, string, string) error
}

// InstallationResolver maps the configured control repository to its App installation.
type InstallationResolver interface {
	InstallationFor(context.Context, string) (int64, error)
}

// GitHubSource is the bounded Git data API used to resolve memory branches.
type GitHubSource interface {
	ResolveRef(context.Context, int64, string, string, string) (string, githubapp.APIResponse, error)
	Tree(context.Context, int64, string, string) ([]githubapp.GitTreeEntry, githubapp.APIResponse, error)
	Blob(context.Context, int64, string, string) ([]byte, githubapp.APIResponse, error)
	RateLimit(context.Context, int64) (int, time.Time, error)
}

// Governor shares GitHub rate-limit state across server instances.
type Governor interface {
	Reserve(context.Context, int64) (int, error)
	Observe(context.Context, int64, int, time.Time) error
	Park(context.Context, int64, time.Time) error
	Headroom(context.Context, int64) (int, time.Time, error)
}

// RemoteResolver lazily resolves memory from GitHub and caches it in operational storage.
type RemoteResolver struct {
	Cache             RemoteCache
	Installations     InstallationResolver
	Source            GitHubSource
	Governor          Governor
	ControlRepository string
}

// ThrottledError reports when GitHub access is rate-limited or already in flight.
type ThrottledError struct {
	RetryAfter time.Duration
}

func (e *ThrottledError) Error() string {
	return "repository memory is temporarily throttled"
}

type cachedCampaign struct {
	Campaign *Campaign `json:"campaign"`
	ETag     string    `json:"etag,omitempty"`
}

// Campaign returns one cached campaign manifest, fetching it from GitHub on a miss.
func (r *RemoteResolver) Campaign(ctx context.Context, campaignID string) (*Campaign, error) {
	cached, ok, err := r.cachedCampaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if ok && cached.Campaign == nil {
		return cached.Campaign, nil
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	lockName := remoteLockName("campaign", campaignID)
	acquired, err := r.Cache.TryLock(ctx, lockName, token, remoteLockTTL)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, &ThrottledError{RetryAfter: time.Second}
	}
	defer func() {
		_ = r.Cache.Unlock(context.WithoutCancel(ctx), lockName, token)
	}()
	ctx, cancel := context.WithTimeout(ctx, remoteOperationTTL)
	defer cancel()
	cached, ok, err = r.cachedCampaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if ok && cached.Campaign == nil {
		return cached.Campaign, nil
	}
	installationID, err := r.Installations.InstallationFor(ctx, r.ControlRepository)
	if err != nil || installationID <= 0 {
		return nil, errors.New("control repository GitHub App installation is unavailable")
	}
	etag := ""
	if ok {
		etag = cached.ETag
	}
	if err := r.reserve(ctx, installationID); err != nil {
		return nil, err
	}
	commit, response, err := r.Source.ResolveRef(
		ctx, installationID, r.ControlRepository, "heads/memory/"+campaignID, etag)
	if err = r.observe(ctx, installationID, response, err); errors.Is(err, githubapp.ErrNotFound) {
		return nil, r.cacheCampaign(ctx, campaignID, nil, "")
	} else if err != nil {
		return nil, err
	}
	if response.NotModified {
		if !ok || cached.Campaign == nil {
			return nil, errors.New("repository memory ref was not modified without a cached campaign")
		}
		if err := r.cacheCampaign(ctx, campaignID, cached.Campaign, cached.ETag); err != nil {
			return nil, err
		}
		return cached.Campaign, nil
	}
	if ok && cached.Campaign != nil && cached.Campaign.Commit == commit {
		if err := r.cacheCampaign(ctx, campaignID, cached.Campaign, response.ETag); err != nil {
			return nil, err
		}
		return cached.Campaign, nil
	}
	refETag := response.ETag
	if err := r.reserve(ctx, installationID); err != nil {
		return nil, err
	}
	entries, response, err := r.Source.Tree(ctx, installationID, r.ControlRepository, commit)
	if err = r.observe(ctx, installationID, response, err); err != nil {
		return nil, err
	}
	campaign := buildRemoteCampaign(campaignID, commit, entries, response.Truncated)
	remoteLog.Printf("built remote campaign entries=%d admitted=%d omitted=%d",
		len(entries), len(campaign.Files), totalOmitted(campaign.Omitted))
	if err := r.cacheCampaign(ctx, campaignID, campaign, refETag); err != nil {
		return nil, err
	}
	return campaign, nil
}

// Content returns one cached file, fetching its immutable blob from GitHub on a miss.
func (r *RemoteResolver) Content(ctx context.Context, campaignID, filePath string) ([]byte, error) {
	campaign, err := r.Campaign(ctx, campaignID)
	if err != nil || campaign == nil {
		return nil, err
	}
	var selected *File
	for index := range campaign.Files {
		if campaign.Files[index].Path == filePath {
			selected = &campaign.Files[index]
			break
		}
	}
	if selected == nil {
		return nil, ErrNotFound
	}
	content, err := r.Cache.CachedRepositoryMemoryFile(ctx, campaignID, campaign.Commit, filePath)
	if err == nil && content != nil {
		return validateRemoteContent(*selected, content)
	}
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	lockName := remoteLockName("file", campaignID, campaign.Commit, filePath)
	acquired, err := r.Cache.TryLock(ctx, lockName, token, remoteLockTTL)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, &ThrottledError{RetryAfter: time.Second}
	}
	defer func() {
		_ = r.Cache.Unlock(context.WithoutCancel(ctx), lockName, token)
	}()
	ctx, cancel := context.WithTimeout(ctx, remoteOperationTTL)
	defer cancel()
	content, err = r.Cache.CachedRepositoryMemoryFile(ctx, campaignID, campaign.Commit, filePath)
	if err == nil && content != nil {
		return validateRemoteContent(*selected, content)
	}
	if err != nil {
		return nil, err
	}
	installationID, err := r.Installations.InstallationFor(ctx, r.ControlRepository)
	if err != nil || installationID <= 0 {
		return nil, errors.New("control repository GitHub App installation is unavailable")
	}
	if err := r.reserve(ctx, installationID); err != nil {
		return nil, err
	}
	content, response, err := r.Source.Blob(
		ctx, installationID, r.ControlRepository, selected.OID)
	if err = r.observe(ctx, installationID, response, err); err != nil {
		return nil, err
	}
	content, err = validateRemoteContent(*selected, content)
	if err != nil {
		return nil, err
	}
	if err := r.Cache.CacheRepositoryMemoryFile(
		ctx, campaignID, campaign.Commit, filePath, content, remoteFileTTL); err != nil {
		return nil, err
	}
	return content, nil
}

func (r *RemoteResolver) cachedCampaign(ctx context.Context, campaignID string) (*cachedCampaign, bool, error) {
	content, err := r.Cache.CachedRepositoryMemoryCampaign(ctx, campaignID)
	if err != nil || content == nil {
		return nil, false, err
	}
	var cached cachedCampaign
	if err := json.Unmarshal(content, &cached); err != nil {
		return nil, false, err
	}
	return &cached, true, nil
}

func (r *RemoteResolver) cacheCampaign(
	ctx context.Context, campaignID string, campaign *Campaign, etag string,
) error {
	content, err := json.Marshal(cachedCampaign{Campaign: campaign, ETag: etag})
	if err != nil {
		return err
	}
	return r.Cache.CacheRepositoryMemoryCampaign(ctx, campaignID, content, remoteCampaignTTL)
}

func (r *RemoteResolver) reserve(ctx context.Context, installationID int64) error {
	if _, err := r.Governor.Reserve(ctx, installationID); err != nil {
		if errors.Is(err, githubapp.ErrBudgetUnknown) {
			remaining, reset, rateErr := r.Source.RateLimit(ctx, installationID)
			if rateErr != nil {
				return rateErr
			}
			if observeErr := r.Governor.Observe(ctx, installationID, remaining, reset); observeErr != nil {
				return observeErr
			}
			_, err = r.Governor.Reserve(ctx, installationID)
			if err == nil {
				return nil
			}
		}
		if !errors.Is(err, githubapp.ErrBudgetExhausted) &&
			!errors.Is(err, githubapp.ErrInstallationParked) {
			return err
		}
		_, parkedTo, headroomErr := r.Governor.Headroom(ctx, installationID)
		if headroomErr != nil {
			return headroomErr
		}
		retryAfter := time.Minute
		if delay := time.Until(parkedTo); delay > 0 {
			retryAfter = delay
		}
		return &ThrottledError{RetryAfter: retryAfter}
	}
	return nil
}

func (r *RemoteResolver) observe(
	ctx context.Context, installationID int64, response githubapp.APIResponse, requestErr error,
) error {
	if !response.Reset.IsZero() || response.Remaining > 0 {
		if err := r.Governor.Observe(ctx, installationID, response.Remaining, response.Reset); err != nil {
			return err
		}
	}
	var retryAt time.Time
	switch {
	case response.RetryAfter > 0:
		retryAt = time.Now().UTC().Add(response.RetryAfter)
	case response.Remaining == 0 && response.Reset.After(time.Now()):
		retryAt = response.Reset
	case response.StatusCode == 429:
		retryAt = time.Now().UTC().Add(time.Minute)
	case response.Secondary:
		retryAt = time.Now().UTC().Add(time.Minute)
	}
	if !retryAt.IsZero() {
		if err := r.Governor.Park(ctx, installationID, retryAt); err != nil {
			return err
		}
		if requestErr != nil {
			return &ThrottledError{RetryAfter: max(time.Until(retryAt), time.Second)}
		}
	}
	return requestErr
}

// totalOmitted sums every Omissions counter, so a caller can report how many
// tree entries were dropped for any reason without listing each category.
func totalOmitted(omitted Omissions) int {
	return omitted.FileLimit + omitted.FileSize + omitted.TotalSize + omitted.Extension +
		omitted.Nesting + omitted.UnsafePath + omitted.InvalidContent + omitted.UnsupportedType
}

func buildRemoteCampaign(
	campaignID, commit string, entries []githubapp.GitTreeEntry, truncated bool,
) *Campaign {
	campaign := &Campaign{
		Campaign: campaignID,
		Branch:   "memory/" + campaignID,
		Commit:   commit,
		Files:    []File{},
	}
	if truncated {
		campaign.Omitted.FileLimit++
	}
	var totalSize int64
	for _, entry := range entries {
		switch {
		case entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755"):
			campaign.Omitted.UnsupportedType++
		case !ValidPath(entry.Path):
			classifyRemotePath(entry.Path, &campaign.Omitted)
		case entry.Size < 0 || entry.Size > MaxFileSize:
			campaign.Omitted.FileSize++
		case len(campaign.Files) >= MaxFileCount:
			campaign.Omitted.FileLimit++
		case entry.Size > MaxTotalSize-totalSize:
			campaign.Omitted.TotalSize++
		default:
			totalSize += entry.Size
			campaign.Files = append(campaign.Files, File{
				Path: entry.Path, OID: entry.OID, Size: entry.Size,
			})
		}
	}
	return campaign
}

func classifyRemotePath(value string, omitted *Omissions) {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		omitted.UnsafePath++
		return
	}
	segments := strings.Split(value, "/")
	if len(segments)-1 > MaxNesting {
		omitted.Nesting++
		return
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			omitted.UnsafePath++
			return
		}
	}
	omitted.Extension++
}

func validateRemoteContent(file File, content []byte) ([]byte, error) {
	if int64(len(content)) != file.Size || len(content) > MaxFileSize {
		return nil, errors.New("repository-memory file failed size validation")
	}
	if !utf8.Valid(content) {
		return nil, errors.New("repository-memory file is not valid UTF-8 text")
	}
	if file.SHA256 != "" {
		sum := sha256.Sum256(content)
		if !strings.EqualFold(file.SHA256, hex.EncodeToString(sum[:])) {
			return nil, errors.New("repository-memory file failed hash validation")
		}
	}
	return content, nil
}

func randomToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate repository-memory lock token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func remoteLockName(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "repository-memory:" + hex.EncodeToString(sum[:])
}

// RetryAfterSeconds returns a bounded HTTP Retry-After value.
func RetryAfterSeconds(err error) (int, bool) {
	var throttled *ThrottledError
	if !errors.As(err, &throttled) {
		return 0, false
	}
	return max(1, int(math.Ceil(throttled.RetryAfter.Seconds()))), true
}
