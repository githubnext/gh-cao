package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var eventsLog = logger.New("cao:collect:events")

// IntentKind classifies what an admitted webhook asks the collector to do.
type IntentKind string

const (
	// IntentCollect requests collection of one repository.
	IntentCollect IntentKind = "collect"
	// IntentIssueStatus refreshes the enrolled repository after an issue event.
	IntentIssueStatus     IntentKind = "issue-status"
	IntentRepository      IntentKind = "repository"
	IntentWorkflowRefresh IntentKind = "workflow-refresh"
	// IntentEnroll adds repositories to the enrollment set.
	IntentEnroll IntentKind = "enroll"
	// IntentUnenroll removes repositories from the enrollment set.
	IntentUnenroll IntentKind = "unenroll"
	// IntentRemoveInstallation removes an installation and all its
	// repositories.
	IntentRemoveInstallation IntentKind = "remove-installation"
	// IntentIgnore is an event the collector does not act on.
	IntentIgnore IntentKind = "ignore"
)

// Intent is the admitted meaning of one webhook delivery.
type Intent struct {
	Kind           IntentKind
	Repository     string
	Repositories   []string
	InstallationID int64
	Reason         string
	Issue          operational.IssueUpdate
	RepositoryID   int64
	Lifecycle      string
}

type webhookEnvelope struct {
	Action  string `json:"action"`
	Ref     string `json:"ref"`
	Deleted bool   `json:"deleted"`
	Size    int    `json:"size"`
	Commits []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Removed  []string `json:"removed"`
	} `json:"commits"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	WorkflowRun struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"workflow_run"`
	Issue struct {
		Number      int64           `json:"number"`
		State       string          `json:"state"`
		StateReason *string         `json:"state_reason"`
		ClosedAt    *string         `json:"closed_at"`
		UpdatedAt   string          `json:"updated_at"`
		HTMLURL     string          `json:"html_url"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	Repositories []struct {
		FullName string `json:"full_name"`
	} `json:"repositories"`
	RepositoriesAdded []struct {
		FullName string `json:"full_name"`
	} `json:"repositories_added"`
	RepositoriesRemoved []struct {
		FullName string `json:"full_name"`
	} `json:"repositories_removed"`
}

// ParseEvent maps a verified webhook delivery to a collector intent.
//
// Parsing never infers enrollment: it reports what the payload says, and
// admission decides whether that repository is in scope.
func ParseEvent(event string, payload []byte) (Intent, error) {
	var envelope webhookEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Intent{}, fmt.Errorf("parse webhook payload: %w", err)
	}
	trimmedEvent := strings.TrimSpace(event)
	var intent Intent
	var err error
	switch trimmedEvent {
	case "push":
		intent = parsePushEvent(envelope)
	case "workflow_run":
		intent, err = parseWorkflowRunEvent(envelope)
	case "issues":
		intent = parseIssueEvent(envelope)
	case "repository":
		intent = parseRepositoryEvent(envelope)
	case "installation":
		intent = parseInstallationEvent(envelope)
	case "installation_repositories":
		intent = parseInstallationRepositoriesEvent(envelope)
	default:
		intent = Intent{Kind: IntentIgnore}
	}

	if err != nil {
		return Intent{}, err
	}
	// One classification per delivery is a meaningful state transition worth
	// observing, and this is never called from a polling or retry loop, so
	// logging every delivery cannot flood the log.
	eventsLog.Printf("classified webhook event=%s kind=%s", trimmedEvent, intent.Kind)
	return intent, nil
}

func parsePushEvent(envelope webhookEnvelope) Intent {
	if envelope.Deleted || envelope.Repository.DefaultBranch == "" ||
		envelope.Ref != "refs/heads/"+envelope.Repository.DefaultBranch ||
		envelope.Installation.ID <= 0 {
		return Intent{Kind: IntentIgnore}
	}
	repository, err := NormalizeRepository(envelope.Repository.FullName)
	if err != nil {
		return Intent{Kind: IntentIgnore}
	}
	// GitHub truncates the commits array on large pushes. Refresh conservatively
	// when file coverage is incomplete; never fetch commits in the receiver.
	changed := envelope.Size > len(envelope.Commits) || len(envelope.Commits) >= 2048
	for _, commit := range envelope.Commits {
		for _, paths := range [][]string{commit.Added, commit.Modified, commit.Removed} {
			for _, path := range paths {
				filename, underWorkflows := strings.CutPrefix(path, ".github/workflows/")
				changed = changed || (underWorkflows && strings.HasSuffix(filename, ".lock.yml") &&
					!strings.Contains(filename, "/"))
			}
		}
	}
	if !changed {
		return Intent{Kind: IntentIgnore}
	}
	return Intent{Kind: IntentWorkflowRefresh, Repository: repository,
		InstallationID: envelope.Installation.ID, Reason: "push.workflows"}
}

func parseRepositoryEvent(envelope webhookEnvelope) Intent {
	state := ""
	switch envelope.Action {
	case "created", "unarchived", "renamed", "transferred":
		state = "active"
	case "archived":
		state = "archived"
	case "deleted":
		state = "deleted"
	}
	if state == "" || envelope.Installation.ID <= 0 || envelope.Repository.ID <= 0 {
		return Intent{Kind: IntentIgnore}
	}
	repository, err := NormalizeRepository(envelope.Repository.FullName)
	if err != nil {
		return Intent{Kind: IntentIgnore}
	}
	return Intent{Kind: IntentRepository, Repository: repository, RepositoryID: envelope.Repository.ID,
		InstallationID: envelope.Installation.ID, Lifecycle: state, Reason: "repository." + envelope.Action}
}

func parseIssueEvent(envelope webhookEnvelope) Intent {
	switch envelope.Action {
	case "opened", "reopened", "closed", "edited", "assigned", "unassigned",
		"labeled", "unlabeled", "milestoned", "demilestoned", "locked", "unlocked":
	default:
		return Intent{Kind: IntentIgnore}
	}
	issue := envelope.Issue
	if envelope.Repository.FullName == "" || issue.Number <= 0 || issue.UpdatedAt == "" ||
		len(issue.PullRequest) != 0 || envelope.Installation.ID <= 0 {
		return Intent{Kind: IntentIgnore}
	}
	repository, err := NormalizeRepository(envelope.Repository.FullName)
	if err != nil {
		return Intent{Kind: IntentIgnore}
	}
	link, err := url.Parse(issue.HTMLURL)
	if err != nil || link.Scheme != "https" || link.Hostname() == "" ||
		!strings.EqualFold(link.Path, fmt.Sprintf("/%s/issues/%d", repository, issue.Number)) ||
		link.RawQuery != "" || link.Fragment != "" || link.User != nil {
		return Intent{Kind: IntentIgnore}
	}
	observed, err := time.Parse(time.RFC3339Nano, issue.UpdatedAt)
	if err != nil {
		return Intent{Kind: IntentIgnore}
	}
	state := strings.ToUpper(issue.State)
	if state != "OPEN" && state != "CLOSED" ||
		(envelope.Action == "closed" && state != "CLOSED") ||
		((envelope.Action == "opened" || envelope.Action == "reopened") && state != "OPEN") {
		return Intent{Kind: IntentIgnore}
	}
	update := operational.IssueUpdate{
		Repository: repository, InstallationID: envelope.Installation.ID,
		ID:    fmt.Sprintf("github:issue:%s:%d", repository, issue.Number),
		State: state, ObservedAt: observed.UTC().Format("2006-01-02T15:04:05.000000000Z"),
	}
	if state == "CLOSED" {
		if issue.StateReason != nil {
			update.StateReason = *issue.StateReason
		}
		if issue.ClosedAt != nil {
			closed, err := time.Parse(time.RFC3339Nano, *issue.ClosedAt)
			if err != nil {
				return Intent{Kind: IntentIgnore}
			}
			update.ClosedAt = closed.UTC().Format(time.RFC3339Nano)
		}
	}
	return Intent{Kind: IntentIssueStatus, Repository: repository,
		InstallationID: envelope.Installation.ID, Issue: update}
}

// parseWorkflowRunEvent maps a "workflow_run" delivery to a collection intent.
// It is a pure function so this classification's edge cases (an
// action other than "completed", a missing repository) are testable without
// constructing a full webhook payload.
func parseWorkflowRunEvent(envelope webhookEnvelope) (Intent, error) {
	if envelope.Action != "completed" {
		return Intent{Kind: IntentIgnore}, nil
	}
	if envelope.Repository.FullName == "" {
		return Intent{}, errors.New("workflow_run payload is missing a repository")
	}
	return Intent{
		Kind:           IntentCollect,
		Repository:     envelope.Repository.FullName,
		InstallationID: envelope.Installation.ID,
		Reason:         "workflow_run",
	}, nil
}

// parseInstallationEvent maps an "installation" delivery to an enrollment or
// removal intent. It is a pure function so this classification's action
// dispatch is testable independently of json decoding.
func parseInstallationEvent(envelope webhookEnvelope) Intent {
	installationID := envelope.Installation.ID
	switch envelope.Action {
	case "created", "new_permissions_accepted", "unsuspend":
		return Intent{
			Kind:           IntentEnroll,
			Repositories:   names(envelope.Repositories),
			InstallationID: installationID,
			Reason:         "installation." + envelope.Action,
		}
	case "deleted", "suspend":
		return Intent{
			Kind:           IntentRemoveInstallation,
			InstallationID: installationID,
			Reason:         "installation." + envelope.Action,
		}
	default:
		return Intent{Kind: IntentIgnore}
	}
}

// parseInstallationRepositoriesEvent maps an "installation_repositories"
// delivery to an enrollment or unenrollment intent. It is a pure function so
// this classification's action dispatch is testable independently of json
// decoding.
func parseInstallationRepositoriesEvent(envelope webhookEnvelope) Intent {
	installationID := envelope.Installation.ID
	switch envelope.Action {
	case "added":
		return Intent{
			Kind:           IntentEnroll,
			Repositories:   names(envelope.RepositoriesAdded),
			InstallationID: installationID,
			Reason:         "installation_repositories.added",
		}
	case "removed":
		return Intent{
			Kind:           IntentUnenroll,
			Repositories:   names(envelope.RepositoriesRemoved),
			InstallationID: installationID,
			Reason:         "installation_repositories.removed",
		}
	default:
		return Intent{Kind: IntentIgnore}
	}
}

func names(entries []struct {
	FullName string `json:"full_name"`
}) []string {
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.FullName != "" {
			values = append(values, entry.FullName)
		}
	}
	return values
}

// ErrNotEnrolled reports a delivery for a repository outside ingestion scope.
var ErrNotEnrolled = errors.New("repository is not enrolled")

var ErrDeliveryInProgress = errors.New("webhook delivery admission is already in progress")

// Admitter durably queues work without changing enrollment or projecting.
// It performs no projection, takes no global lease, and never blocks one
// repository behind another.
type Admitter struct {
	Enrollment Enrollment
	Queue      Queue
	// Projection requests a projection after erasure, so the canonical
	// database stops reporting repositories that left ingestion scope.
	Projection ProjectionRequester
}

// ProjectionRequester marks the lake as changed. Projector satisfies it.
type ProjectionRequester interface {
	RequestProjection(ctx context.Context) error
}

// Admission describes what a delivery did, for the webhook response.
type Admission struct {
	Kind      IntentKind `json:"kind"`
	Enqueued  bool       `json:"enqueued"`
	Duplicate bool       `json:"duplicate,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	// ErasureQueued counts repositories with durable erasure work because they
	// left ingestion scope.
	ErasureQueued int `json:"erasureQueued,omitempty"`
}

// Admit applies one verified delivery.
func (a Admitter) Admit(ctx context.Context, event string, payload []byte) (Admission, error) {
	intent, err := ParseEvent(event, payload)
	if err != nil {
		return Admission{}, err
	}
	if isScopeIntent(intent.Kind) {
		return a.queueScope(ctx, intent, "", 0)
	}
	return a.admitIntent(ctx, intent)
}

// AdmitDelivery commits delivery deduplication with durable admission. Queue
// append, repository debounce, and delivery identity are one Redis operation.
func (a Admitter) AdmitDelivery(
	ctx context.Context,
	event string,
	payload []byte,
	delivery string,
	deliveryTTL time.Duration,
) (admission Admission, err error) {
	ctx, finish := startRefreshTelemetry(ctx, "admission", "webhook")
	defer func() { finish(admissionOutcome(admission, err), err) }()
	intent, err := ParseEvent(event, payload)
	if err != nil {
		return Admission{}, err
	}
	if isScopeIntent(intent.Kind) {
		return a.queueScope(ctx, intent, delivery, deliveryTTL)
	}
	if intent.Kind == IntentIssueStatus || intent.Kind == IntentRepository || intent.Kind == IntentWorkflowRefresh {
		return a.admitIssue(ctx, intent, delivery, deliveryTTL)
	}
	if intent.Kind != IntentCollect {
		reservation, err := a.Queue.Deliveries.ReserveDelivery(ctx, delivery, 30*time.Second)
		if err != nil {
			return Admission{}, err
		}

		switch reservation {
		case operational.DeliveryAlreadyCommitted:
			return Admission{Kind: intent.Kind, Duplicate: true}, nil
		case operational.DeliveryReserved:
		case operational.DeliveryInProgress:
			return Admission{}, ErrDeliveryInProgress
		}
		defer func() {
			release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = a.Queue.Deliveries.ReleaseDeliveryReservation(release, delivery)
		}()
		admission, err := a.admitIntent(ctx, intent)
		if err != nil {
			return Admission{}, err
		}
		fresh, err := a.Queue.Deliveries.RememberDelivery(ctx, delivery, deliveryTTL)
		if err != nil {
			return Admission{}, err
		}
		admission.Duplicate = !fresh
		return admission, nil
	}
	installationID, err := a.resolveCollectInstallation(ctx, intent)
	if err != nil {
		return Admission{}, err
	}
	enqueued, duplicate, err := a.Queue.EnqueueDelivery(ctx, Task{
		Repository:     intent.Repository,
		InstallationID: installationID,
		Reason:         intent.Reason,
	}, delivery, deliveryTTL)
	if err != nil {
		return Admission{}, err
	}
	return Admission{Kind: IntentCollect, Enqueued: enqueued, Duplicate: duplicate}, nil
}

func (a Admitter) admitIssue(ctx context.Context, intent Intent, delivery string, ttl time.Duration) (Admission, error) {
	enrolled, err := a.Enrollment.Enrolled(ctx, intent.Repository)
	if err != nil {
		return Admission{}, err
	}
	owner, err := a.Enrollment.InstallationFor(ctx, intent.Repository)
	if err != nil {
		return Admission{}, err
	}
	if !enrolled || owner != intent.InstallationID {
		if intent.Kind == IntentRepository && !enrolled {
			known, err := a.Enrollment.KnownInstallation(ctx, intent.InstallationID)
			if err != nil {
				return Admission{}, err
			}
			if known {
				owner = intent.InstallationID
			} else {
				return Admission{Kind: IntentIgnore, Reason: "not-enrolled"}, nil
			}
		} else {
			return Admission{Kind: IntentIgnore, Reason: "not-enrolled"}, nil
		}
	}
	task := Task{
		Repository:       intent.Repository,
		InstallationID:   owner,
		Reason:           intent.Reason,
		RepositoryID:     intent.RepositoryID,
		Lifecycle:        intent.Lifecycle,
		InventoryRefresh: intent.Kind == IntentWorkflowRefresh,
	}
	if task.Reason == "" {
		task.Reason = "issue-status"
	}
	if delivery == "" {
		enqueued, err := a.Queue.Enqueue(ctx, task)
		if err != nil {
			return Admission{}, err
		}
		return Admission{Kind: intent.Kind, Enqueued: enqueued}, nil
	}
	enqueued, duplicate, err := a.Queue.EnqueueDelivery(ctx, task, delivery, ttl)
	if err != nil {
		return Admission{}, err
	}
	return Admission{Kind: intent.Kind, Enqueued: enqueued, Duplicate: duplicate}, nil
}

func isScopeIntent(kind IntentKind) bool {
	return kind == IntentEnroll || kind == IntentUnenroll || kind == IntentRemoveInstallation
}

func (a Admitter) queueScope(ctx context.Context, intent Intent, delivery string, ttl time.Duration) (Admission, error) {
	if intent.InstallationID <= 0 {
		return Admission{}, errors.New("scope refresh requires an installation")
	}
	for index, repository := range intent.Repositories {
		name, err := NormalizeRepository(repository)
		if err != nil {
			return Admission{}, err
		}
		intent.Repositories[index] = name
	}
	task := Task{
		Repository:     "installation/" + strconv.FormatInt(intent.InstallationID, 10),
		InstallationID: intent.InstallationID, Reason: intent.Reason,
		Scope: &ScopeChange{Kind: intent.Kind, Repositories: intent.Repositories},
	}
	if intent.Kind == IntentEnroll {
		// A coalesced task enumerates current coverage, not the first payload's
		// repository list, so later additions in the same burst cannot be lost.
		task.Scope.Repositories = nil
		task.NotBefore = time.Now().UTC().Add(2 * time.Second)
	}
	if delivery == "" {
		enqueued, err := a.Queue.Enqueue(ctx, task)
		return Admission{Kind: intent.Kind, Enqueued: enqueued}, err
	}
	enqueued, duplicate, err := a.Queue.EnqueueDelivery(ctx, task, delivery, ttl)
	return Admission{Kind: intent.Kind, Enqueued: enqueued, Duplicate: duplicate}, err
}

// resolveCollectInstallation determines the installation that must own a
// collect intent's enqueued task. It centralizes the enrollment check and
// installation lookup shared by AdmitDelivery and admitIntent's IntentCollect
// branch, so the two entry points cannot drift into different admission
// rules for the same intent kind.
func (a Admitter) resolveCollectInstallation(ctx context.Context, intent Intent) (int64, error) {
	enrolled, err := a.Enrollment.Enrolled(ctx, intent.Repository)
	if err != nil {
		return 0, err
	}
	installationID := intent.InstallationID
	if enrolled && installationID == 0 {
		installationID, err = a.Enrollment.InstallationFor(ctx, intent.Repository)
		if err != nil {
			return 0, err
		}
	}
	resolved, ok := decideCollectInstallation(enrolled, installationID)
	if !ok {
		// Either the repository is out of scope, or enrollment covers it but
		// no installation maps to it; either way this is a meaningful
		// admission-time gap worth observing, and it is not called from a
		// polling or retry loop.
		eventsLog.Printf("collect intent refused enrolled=%t", enrolled)
		return 0, ErrNotEnrolled
	}
	return resolved, nil
}

// decideCollectInstallation applies the admission rule for a collect intent:
// the repository must be enrolled, and an installation must be known for it.
// It is a pure function so this rule is testable without a Redis-backed
// Enrollment store.
func decideCollectInstallation(enrolled bool, installationID int64) (int64, bool) {
	if !enrolled || installationID == 0 {
		return 0, false
	}
	return installationID, true
}

func (a Admitter) queueErasures(ctx context.Context, installationID int64, repositories []string) error {
	for _, repository := range repositories {
		if _, err := a.Queue.Enqueue(ctx, Task{
			Repository:     repository,
			InstallationID: installationID,
			Reason:         "scope-withdrawn",
			Erase:          true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a Admitter) admitIntent(ctx context.Context, intent Intent) (Admission, error) {
	switch intent.Kind {
	case IntentIgnore:
		return Admission{Kind: IntentIgnore}, nil
	case IntentEnroll:
		if err := a.Enrollment.AddRepositories(ctx, intent.InstallationID, intent.Repositories); err != nil {
			return Admission{}, err
		}
		return Admission{Kind: IntentEnroll}, nil
	case IntentUnenroll:
		removed, err := a.Enrollment.RemoveRepositoriesBefore(
			ctx, intent.InstallationID, intent.Repositories,
			func(ctx context.Context, repositories []string) error {
				return a.queueErasures(ctx, intent.InstallationID, repositories)
			},
		)
		if err != nil {
			return Admission{}, err
		}
		erased, err := a.erase(ctx, removed)
		if err != nil {
			return Admission{}, err
		}
		return Admission{Kind: IntentUnenroll, ErasureQueued: erased}, nil
	case IntentRemoveInstallation:
		removed, err := a.Enrollment.RemoveInstallationBefore(
			ctx, intent.InstallationID,
			func(ctx context.Context, repositories []string) error {
				return a.queueErasures(ctx, intent.InstallationID, repositories)
			},
		)
		if err != nil {
			return Admission{}, err
		}
		erased, err := a.erase(ctx, removed)
		if err != nil {
			return Admission{}, err
		}
		return Admission{Kind: IntentRemoveInstallation, ErasureQueued: erased}, nil
	case IntentCollect:
		installationID, err := a.resolveCollectInstallation(ctx, intent)
		if err != nil {
			return Admission{}, err
		}
		enqueued, err := a.Queue.Enqueue(ctx, Task{
			Repository:     intent.Repository,
			InstallationID: installationID,
			Reason:         intent.Reason,
		})
		if err != nil {
			return Admission{}, err
		}
		return Admission{Kind: IntentCollect, Enqueued: enqueued}, nil
	case IntentIssueStatus, IntentRepository, IntentWorkflowRefresh:
		return a.admitIssue(ctx, intent, "", 0)
	default:
		return Admission{Kind: IntentIgnore}, nil
	}
}

// erase requests projection after durable erasure work has been queued.
func (a Admitter) erase(ctx context.Context, repositories []string) (int, error) {
	if len(repositories) == 0 {
		return 0, nil
	}
	if a.Projection != nil {
		if err := a.Projection.RequestProjection(ctx); err != nil {
			return 0, err
		}
	}
	return len(repositories), nil
}
