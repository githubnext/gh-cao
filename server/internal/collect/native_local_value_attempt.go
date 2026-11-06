package collect

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operationalvalue"
)

// RecordNativeValueAttempt publishes a real collector outcome independently
// of cost accounting. The canonical writer checks the exact enrollment epoch
// and revision, so a withdrawn or changed target cannot regain health.
func (p Projector) RecordNativeValueAttempt(
	ctx context.Context, repository, workflow string, attemptedAt time.Time, state, reason string,
) error {
	if p.Data == nil || !p.Data.Open() || p.Enrollment.Authority == nil || p.PolicyPath == "" {
		return collectnative.ErrUnsupported
	}
	repository = strings.ToLower(repository)
	policy, err := p.resolvedNativeValuePolicy(ctx)
	if err != nil {
		return err
	}
	root, err := p.nativeLocalValueRoot()
	if err != nil {
		return err
	}
	snapshot, err := operationalvalue.ResolveWorkflowPolicy(ctx,
		operationalvalue.Config{Root: root}, p.PolicyPath)
	if err != nil {
		return err
	}
	var selected *operationalvalue.WorkflowRegistration
	for i := range snapshot.Registrations {
		registration := &snapshot.Registrations[i]
		if strings.EqualFold(registration.Repository, repository) && registration.Workflow == workflow {
			selected = registration
			break
		}
	}
	if selected == nil || selected.Collector == "" {
		return errors.New("Value attempt lacks an enabled reviewed registration")
	}
	scope, err := p.Enrollment.Authority.CurrentScope(ctx, repository)
	if err != nil {
		return err
	}
	inputs, err := p.Data.ReadNativeValueInputs(ctx, []string{repository})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(policy)
	record := model.Row{
		"schemaVersion": 1,
		"id":            "workflow-value-collection:" + url.QueryEscape(repository+":"+workflow),
		"repository":    repository, "workflow": workflow,
		"sourcePath":   ".github/workflows/" + workflow + ".md",
		"policyDigest": fmt.Sprintf("%x", digest),
		"observedAt":   attemptedAt.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z"),
		"collector":    selected.Collector, "registration": "enabled",
		"state": "deferred", "reasonCode": "shared-budget-unavailable",
		"reason": "Automatic remote acquisition is deferred until shared API request budgets can be enforced. Activity retains priority.",
	}
	for _, previous := range inputs.Collections {
		if previous["id"] == record["id"] {
			if previous["policyDigest"] == record["policyDigest"] {
				record = previous
			} else if previous["lastAttemptAt"] != nil {
				record["lastAttemptAt"], record["lastAttemptOutcome"] =
					previous["lastAttemptAt"], previous["lastAttemptOutcome"]
			}
			break
		}
	}
	record, err = operationalvalue.CollectionAttempt(record, attemptedAt, state, reason)
	if err != nil {
		return err
	}
	if err := p.nativeLocalPolicyCurrent(ctx, policy); err != nil {
		return err
	}
	_, err = p.Data.PublishNativeValuePreparation(ctx, inputs.Revision, []collectnative.EnrollmentScope{scope},
		[]model.Row{record}, nil)
	return err
}
