// Package collect implements the optional server-side, webhook-driven
// acquisition profile described in specs/server-ingestion.md.
//
// The profile is off unless an operator configures it, and it is an
// alternative to — never a layer on top of — the GitHub Actions profile. It
// reuses the Actions profile's collection CLI, shard format, and canonical
// projection rather than reimplementing any of them.
package collect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var enrollmentLog = logger.New("cao:collect:enrollment")

const (
	installationsKey        = "collect:installations"
	repositoriesKey         = "collect:repositories"
	repositoryInstallations = "collect:repository-installation"
	enrollmentMutationLock  = "collect:enrollment-mutation"
	enrollmentLockTTL       = 30 * time.Second
)

var ErrEnrollmentMutationBusy = errors.New("enrollment mutation is already in progress")

// Enrollment is the durable set of installations and repositories the App
// covers. In this profile enrollment is ingestion scope; it is never authority
// to act on a repository, and it is never derived from cao.json.
type Enrollment struct {
	Metadata operational.CollectionMetadata
	Leases   operational.LeaseStore
	// Authority selects native durable ingestion ownership. Metadata remains
	// an operational index/checkpoint adapter, not native scope authority.
	Authority collectnative.EnrollmentAuthority
}

// Coverage summarizes enrollment for status reporting.
type Coverage struct {
	Installations int64 `json:"installations"`
	Repositories  int64 `json:"repositories"`
}

// repositoryRejectionReason classifies why NormalizeRepository rejected an
// "owner/name" reference. It is useful for diagnosing malformed enrollment
// input — for example a webhook payload or GitHub App enumeration result
// carrying an unexpected reference shape — without logging the reference
// value itself.
type repositoryRejectionReason string

const (
	repositoryRejectionReasonMalformed      repositoryRejectionReason = "malformed"
	repositoryRejectionReasonInvalidSegment repositoryRejectionReason = "invalid-segment"
)

// classifyRepositoryRejection reports why an "owner/name" reference already
// split into owner and name is not well-formed. It is a pure function
// extracted from NormalizeRepository so the rejection reason is testable
// independently of the caller's logging.
func classifyRepositoryRejection(owner, name string, found bool) (repositoryRejectionReason, bool) {
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return repositoryRejectionReasonMalformed, true
	}
	if !validRepositorySegment(owner) || !validRepositorySegment(name) {
		return repositoryRejectionReasonInvalidSegment, true
	}
	return "", false
}

// NormalizeRepository canonicalizes an "owner/name" reference, rejecting
// anything that is not a well-formed repository.
func NormalizeRepository(value string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	owner, name, found := strings.Cut(trimmed, "/")
	if reason, rejected := classifyRepositoryRejection(owner, name, found); rejected {
		enrollmentLog.Printf("rejected repository reference reason=%s", reason)
		return "", fmt.Errorf("invalid repository reference %q", value)
	}
	return owner + "/" + name, nil
}

func validRepositorySegment(value string) bool {
	// A segment that is "." or ".." would traverse out of the evidence lake
	// once it is joined into a shard path, and a leading "-" would be read as
	// a flag by the collection subprocess.
	if value == "" || value == "." || value == ".." {
		return false
	}
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '-', character == '_', character == '.':
		default:
			return false
		}
	}
	return true
}

func (e Enrollment) withMutationLock(
	ctx context.Context, mutate func(context.Context) error,
) error {
	if e.Authority != nil {
		if err := e.Authority.WithMutation(ctx, mutate); errors.Is(err, collectnative.ErrBusy) {
			return ErrEnrollmentMutationBusy
		} else {
			return err
		}
	}
	token, err := operationToken()
	if err != nil {
		return err
	}
	acquired, err := e.Leases.TryLock(ctx, enrollmentMutationLock, token, enrollmentLockTTL)
	if err != nil {
		return err
	}
	if !acquired {
		return ErrEnrollmentMutationBusy
	}

	operationCtx, cancelOperation := context.WithCancel(ctx)
	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	lockLost := make(chan error, 1)
	go func() {
		defer close(renewalDone)
		ticker := time.NewTicker(enrollmentLockTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenewal:
				return
			case <-operationCtx.Done():
				return
			case <-ticker.C:
				renewalCtx, cancel := context.WithTimeout(operationCtx, 5*time.Second)
				renewed, renewErr := e.renewMutationLock(renewalCtx, token)
				cancel()
				if renewErr != nil || !renewed {
					if renewErr == nil {
						renewErr = ErrEnrollmentMutationBusy
					}
					select {
					case lockLost <- renewErr:
					default:
					}
					cancelOperation()
					return
				}
			}
		}
	}()

	mutationErr := mutate(operationCtx)
	close(stopRenewal)
	<-renewalDone
	cancelOperation()
	if mutationErr == nil {
		select {
		case mutationErr = <-lockLost:
		default:
		}
	}
	unlockCtx, cancelUnlock := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancelUnlock()
	if err := e.Leases.Unlock(unlockCtx, enrollmentMutationLock, token); mutationErr == nil {
		mutationErr = err
	}
	return mutationErr
}

func (e Enrollment) renewMutationLock(ctx context.Context, token string) (bool, error) {
	return e.Leases.RenewLock(ctx, enrollmentMutationLock, token, enrollmentLockTTL)
}

// AddRepositories records repositories covered by one installation.
func (e Enrollment) AddRepositories(ctx context.Context, installationID int64, repositories []string) error {
	if installationID <= 0 {
		return errors.New("installation id is required")
	}
	if len(repositories) == 0 {
		return nil
	}
	return e.withMutationLock(ctx, func(ctx context.Context) error {
		return e.addRepositories(ctx, installationID, repositories)
	})
}

func (e Enrollment) addRepositories(ctx context.Context, installationID int64, repositories []string) error {
	normalized := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		name, err := NormalizeRepository(repository)
		if err != nil {
			return err
		}
		normalized = append(normalized, name)
	}
	if _, err := e.Metadata.AddMembers(ctx, installationsKey, strconv.FormatInt(installationID, 10)); err != nil {
		return err
	}
	if _, err := e.Metadata.AddMembers(ctx, repositoriesKey, normalized...); err != nil {
		return err
	}
	if _, err := e.Metadata.AddMembers(ctx, installationRepositoriesKey(installationID), normalized...); err != nil {
		return err
	}
	transferred := 0
	for start := 0; start < len(normalized); start += 256 {
		batch := normalized[start:min(start+256, len(normalized))]
		count, err := e.Metadata.TransferOwners(ctx, repositoryInstallations,
			"collect:installation:", ":repositories", installationID, batch)
		if err != nil {
			return err
		}
		transferred += count
	}
	if e.Authority != nil {
		if err := e.Authority.Enroll(ctx, installationID, normalized); err != nil {
			return err
		}
	}
	enrollmentLog.Printf("enrolled repositories installation=%d count=%d transferred=%d",
		installationID, len(normalized), transferred)
	return nil
}

// repositoryTransfer decides whether a repository's recorded owner in Redis
// differs from the installation now claiming it. It returns the previous
// installation id and whether the caller must move the repository out of
// that installation's set; an empty or unparsable previous owner, or one that
// already matches installationID, needs no transfer.
func repositoryTransfer(previous string, installationID int64) (previousInstallation int64, transferred bool) {
	if previous == "" || previous == strconv.FormatInt(installationID, 10) {
		return 0, false
	}
	identifier, err := strconv.ParseInt(previous, 10, 64)
	if err != nil || identifier <= 0 {
		return 0, false
	}
	return identifier, true
}

// RemoveRepositories drops repositories from enrollment and reports the
// normalized names it removed, so the caller can erase their retained
// evidence.
//
// Removal is scoped to the installation that currently covers a repository. A
// stale event from an installation that no longer covers it clears only that
// installation's own membership, so a repository transferred to another
// installation keeps its enrollment and its evidence.
func (e Enrollment) RemoveRepositories(
	ctx context.Context, installationID int64, repositories []string) ([]string, error) {
	return e.RemoveRepositoriesBefore(ctx, installationID, repositories, nil)
}

// RemoveRepositoriesBefore runs prepare with the normalized removal set while
// enrollment mutations are serialized, before changing membership.
func (e Enrollment) RemoveRepositoriesBefore(
	ctx context.Context,
	installationID int64,
	repositories []string,
	prepare func(context.Context, []string) error,
) ([]string, error) {
	removed := make([]string, 0, len(repositories))
	err := e.withMutationLock(ctx, func(ctx context.Context) error {
		normalized := make([]string, 0, len(repositories))
		for _, repository := range repositories {
			name, err := NormalizeRepository(repository)
			if err != nil {
				return err
			}
			normalized = append(normalized, name)
		}
		if prepare != nil {
			if err := prepare(ctx, normalized); err != nil {
				return err
			}
		}
		var err error
		removed, err = e.removeRepositories(ctx, installationID, normalized)
		return err
	})
	return removed, err
}

func (e Enrollment) removeRepositories(
	ctx context.Context, installationID int64, repositories []string,
) ([]string, error) {
	if e.Authority != nil {
		return e.removeNativeRepositories(ctx, installationID, repositories)
	}
	removed := make([]string, 0, len(repositories))
	stale := 0
	for _, repository := range repositories {
		name := repository
		if installationID > 0 {
			if err := e.Metadata.RemoveMembers(ctx, installationRepositoriesKey(installationID), name); err != nil {
				return removed, err
			}
		}
		owner, err := e.Metadata.ReadAttribute(ctx, repositoryInstallations, name)
		if err != nil {
			return removed, err
		}
		if staleRemoval(owner, installationID) {
			stale++
			continue
		}
		if err := e.Metadata.RemoveMembers(ctx, repositoriesKey, name); err != nil {
			return removed, err
		}
		if err := e.Metadata.DeleteAttribute(ctx, repositoryInstallations, name); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	if stale > 0 {
		// A stale event from an installation that no longer covers a
		// repository must not erase evidence the current owner still needs;
		// the count is worth surfacing since it explains a removal request
		// that removed fewer repositories than requested.
		enrollmentLog.Printf("skipped stale removals installation=%d skipped=%d", installationID, stale)
	}
	return removed, nil
}

// staleRemoval reports whether a repository's recorded owner differs from the
// installation requesting removal. It returns true when removal must be
// skipped: an unowned repository, a request from an unknown installation, or
// a request that matches the recorded owner all proceed with removal.
func staleRemoval(owner string, installationID int64) bool {
	return owner != "" && installationID > 0 && owner != strconv.FormatInt(installationID, 10)
}

// RemoveInstallation drops an installation and every repository it covered,
// reporting those repositories so their retained evidence can be erased.
func (e Enrollment) RemoveInstallation(ctx context.Context, installationID int64) ([]string, error) {
	return e.RemoveInstallationBefore(ctx, installationID, nil)
}

// RemoveInstallationBefore prepares durable follow-up work from the exact
// membership snapshot that is removed, while enrollment mutations are locked.
func (e Enrollment) RemoveInstallationBefore(
	ctx context.Context, installationID int64, prepare func(context.Context, []string) error,
) ([]string, error) {
	var removed []string
	err := e.withMutationLock(ctx, func(ctx context.Context) error {
		repositories, err := e.RepositoriesForInstallation(ctx, installationID)
		if err != nil {
			return err
		}
		if prepare != nil {
			if err := prepare(ctx, repositories); err != nil {
				return err
			}
		}
		removed, err = e.removeRepositories(ctx, installationID, repositories)
		if err != nil {
			return err
		}
		if err := e.Metadata.Clear(ctx, installationRepositoriesKey(installationID)); err != nil {
			return err
		}
		enrollmentLog.Printf("removed installation=%d repositories=%d", installationID, len(removed))
		return e.Metadata.RemoveMembers(ctx, installationsKey, strconv.FormatInt(installationID, 10))
	})
	return removed, err
}

// RepositoriesForInstallation returns the current repository membership without
// changing it, so erasure work can be durably queued before membership removal.
func (e Enrollment) RepositoriesForInstallation(ctx context.Context, installationID int64) ([]string, error) {
	var names []string
	cursor := ""
	for {
		var repositories []string
		var next string
		var err error
		if e.Authority != nil {
			repositories, next, err = e.Authority.ScanRepositories(ctx, installationID, cursor, 500)
		} else {
			repositories, next, err = e.Metadata.ScanMembers(ctx, installationRepositoriesKey(installationID), cursor, 500)
		}
		if err != nil {
			return nil, err
		}
		names = append(names, repositories...)
		if next == "0" || next == "" {
			break
		}
		cursor = next
	}
	sort.Strings(names)
	return names, nil
}

// Enrolled reports whether a repository is in scope. Admission fails closed:
// an unknown repository is rejected rather than inferred from the payload.
func (e Enrollment) Enrolled(ctx context.Context, repository string) (bool, error) {
	name, err := NormalizeRepository(repository)
	if err != nil {
		return false, err
	}
	if e.Authority != nil {
		installation, err := e.InstallationFor(ctx, name)
		return installation > 0, err
	}
	return e.Metadata.HasMember(ctx, repositoriesKey, name)
}

// InstallationFor reports which installation covers a repository.
func (e Enrollment) InstallationFor(ctx context.Context, repository string) (int64, error) {
	name, err := NormalizeRepository(repository)
	if err != nil {
		return 0, err
	}
	if e.Authority != nil {
		scope, err := e.Authority.CurrentScope(ctx, name)
		if errors.Is(err, collectnative.ErrIdentity) {
			return 0, nil
		}
		return scope.InstallationID, err
	}
	value, err := e.Metadata.ReadAttribute(ctx, repositoryInstallations, name)
	if err != nil || value == "" {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

// Coverage reports enrollment size, published as source metadata so partial
// coverage is visible rather than rendered as a complete zero.
func (e Enrollment) Coverage(ctx context.Context) (Coverage, error) {
	installations, err := e.Metadata.MemberCount(ctx, installationsKey)
	if err != nil {
		return Coverage{}, err
	}
	var repositories int64
	if e.Authority != nil {
		identifiers, scanErr := e.Installations(ctx)
		if scanErr != nil {
			return Coverage{}, scanErr
		}
		installations = int64(len(identifiers))
		repositories, err = e.Authority.RepositoryCount(ctx)
	} else {
		repositories, err = e.Metadata.MemberCount(ctx, repositoriesKey)
	}
	if err != nil {
		return Coverage{}, err
	}
	return Coverage{Installations: installations, Repositories: repositories}, nil
}

// Installations enumerates known installation identifiers.
func (e Enrollment) Installations(ctx context.Context) ([]int64, error) {
	cursor := ""
	var identifiers []int64
	if e.Authority != nil {
		var err error
		identifiers, err = e.Authority.Installations(ctx)
		if err != nil {
			return nil, err
		}
	}
	for {
		members, next, err := e.Metadata.ScanMembers(ctx, installationsKey, cursor, 500)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			identifier, err := strconv.ParseInt(member, 10, 64)
			if err != nil {
				continue
			}
			identifiers = append(identifiers, identifier)
		}
		if next == "0" || next == "" {
			sort.Slice(identifiers, func(i, j int) bool { return identifiers[i] < identifiers[j] })
			return slices.Compact(identifiers), nil
		}
		cursor = next
	}
}

func (e Enrollment) KnownInstallation(ctx context.Context, installation int64) (bool, error) {
	if installation <= 0 {
		return false, errors.New("invalid installation identifier")
	}
	if e.Authority != nil {
		known, err := e.Authority.HasInstallation(ctx, installation)
		if err != nil || known {
			return known, err
		}
	}
	return e.Metadata.HasMember(ctx, installationsKey, strconv.FormatInt(installation, 10))
}

// ScanRepositories walks enrolled repositories by cursor so cold start over a
// very large enrollment set is resumable and never blocks Redis.
func (e Enrollment) ScanRepositories(ctx context.Context, cursor string, count int) ([]string, string, error) {
	if e.Authority != nil {
		if cursor == "0" {
			cursor = ""
		}
		names, next, err := e.Authority.ScanRepositories(ctx, 0, cursor, count)
		if next == "" {
			next = "0"
		}
		return names, next, err
	}
	return e.Metadata.ScanMembers(ctx, repositoriesKey, cursor, count)
}

func (e Enrollment) removeNativeRepositories(ctx context.Context, installationID int64, repositories []string) ([]string, error) {
	removed, err := e.Authority.Withdraw(ctx, installationID, repositories)
	if err != nil {
		return nil, err
	}
	for _, name := range repositories {
		if installationID > 0 {
			if err := e.Metadata.RemoveMembers(ctx, installationRepositoriesKey(installationID), name); err != nil {
				return nil, err
			}
		}
	}
	for _, name := range removed {
		if err := e.Metadata.RemoveMembers(ctx, repositoriesKey, name); err != nil {
			return nil, err
		}
		if err := e.Metadata.DeleteAttribute(ctx, repositoryInstallations, name); err != nil {
			return nil, err
		}
	}
	return removed, nil
}

func installationRepositoriesKey(installationID int64) string {
	return "collect:installation:" + strconv.FormatInt(installationID, 10) + ":repositories"
}
