package doctor

import (
	"os"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collect"
)

func fakeStat(present map[string]bool) func(string) (os.FileInfo, error) {
	return func(path string) (os.FileInfo, error) {
		if present[path] {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
}

func TestClassifyQueueBacklogReportsDeadLettersBeforeAnythingElse(t *testing.T) {
	// A backlog near the max length must not mask a dead-letter warning; dead
	// letters are checked first regardless of depth.
	classification := classifyQueueBacklog(900, 10, 3, 1000)
	if classification.status != StatusWarn {
		t.Fatalf("status = %v, want %v", classification.status, StatusWarn)
	}
	if classification.reason != queueBacklogReasonDeadLetters {
		t.Fatalf("reason = %v, want %v", classification.reason, queueBacklogReasonDeadLetters)
	}
	if classification.remedy == "" {
		t.Fatal("expected a remedy for dead-lettered tasks")
	}
}

func TestClassifyQueueBacklogWarnsWithinTwentyPercentOfMaxLength(t *testing.T) {
	classification := classifyQueueBacklog(800, 5, 0, 1000)
	if classification.status != StatusWarn {
		t.Fatalf("status = %v, want %v", classification.status, StatusWarn)
	}
	if classification.reason != queueBacklogReasonNearMaxLength {
		t.Fatalf("reason = %v, want %v", classification.reason, queueBacklogReasonNearMaxLength)
	}
}

func TestClassifyQueueBacklogPassesBelowTheWarningThreshold(t *testing.T) {
	classification := classifyQueueBacklog(799, 5, 0, 1000)
	if classification.status != StatusPass {
		t.Fatalf("status = %v, want %v", classification.status, StatusPass)
	}
	if classification.reason != queueBacklogReasonHealthy {
		t.Fatalf("reason = %v, want %v", classification.reason, queueBacklogReasonHealthy)
	}
	if classification.remedy != "" {
		t.Fatalf("expected no remedy for a healthy queue, got %q", classification.remedy)
	}
}

func TestClassifyQueueBacklogPassesWhenNoMaximumIsConfigured(t *testing.T) {
	// maximum <= 0 means unbounded, so the near-max-length threshold must
	// never trigger regardless of depth.
	classification := classifyQueueBacklog(1_000_000, 5, 0, 0)
	if classification.status != StatusPass {
		t.Fatalf("status = %v, want %v", classification.status, StatusPass)
	}
	if classification.reason != queueBacklogReasonHealthy {
		t.Fatalf("reason = %v, want %v", classification.reason, queueBacklogReasonHealthy)
	}
}

func TestResolvePrivateKeySourceReportsAbsentWhenNothingIsConfigured(t *testing.T) {
	result := resolvePrivateKeySource(fakeStat(nil), "", "", "")
	if result.label != "absent" || result.present || result.err != nil {
		t.Fatalf("result = %+v, want label=absent present=false err=nil", result)
	}
	if result.reason != privateKeySourceReasonAbsent {
		t.Fatalf("reason = %v, want %v", result.reason, privateKeySourceReasonAbsent)
	}
}

func TestResolvePrivateKeySourcePrefersFileOverInline(t *testing.T) {
	stat := func(string) (os.FileInfo, error) {
		return fakeRegularFileInfo{}, nil
	}
	result := resolvePrivateKeySource(stat, "/etc/cao/key.pem", "base64-value", "inline-value")
	if result.label != "file (configured)" || !result.present || result.err != nil {
		t.Fatalf("result = %+v, want label=file (configured) present=true err=nil", result)
	}
	if result.reason != privateKeySourceReasonFileConfigured {
		t.Fatalf("reason = %v, want %v", result.reason, privateKeySourceReasonFileConfigured)
	}
}

func TestResolvePrivateKeySourceReportsUnreadableFile(t *testing.T) {
	result := resolvePrivateKeySource(fakeStat(nil), "/etc/cao/missing.pem", "", "")
	if result.label != "file (unreadable)" || result.present || result.err == nil {
		t.Fatalf("result = %+v, want label=file (unreadable) present=false non-nil err", result)
	}
	if result.reason != privateKeySourceReasonFileUnreadable {
		t.Fatalf("reason = %v, want %v", result.reason, privateKeySourceReasonFileUnreadable)
	}
}

func TestResolvePrivateKeySourceRejectsADirectory(t *testing.T) {
	stat := func(string) (os.FileInfo, error) {
		return fakeDirInfo{}, nil
	}
	result := resolvePrivateKeySource(stat, "/etc/cao/keys", "", "")
	if result.label != "file (not a file)" || result.present || result.err == nil {
		t.Fatalf("result = %+v, want label=file (not a file) present=false non-nil err", result)
	}
	if result.reason != privateKeySourceReasonFileNotAFile {
		t.Fatalf("reason = %v, want %v", result.reason, privateKeySourceReasonFileNotAFile)
	}
}

func TestResolvePrivateKeySourceReportsInlineWhenNoFileIsConfigured(t *testing.T) {
	result := resolvePrivateKeySource(fakeStat(nil), "", "", "inline-value")
	if result.label != "inline environment variable (configured)" || !result.present || result.err != nil {
		t.Fatalf("result = %+v, want label=inline environment variable (configured) present=true err=nil", result)
	}
	if result.reason != privateKeySourceReasonInlineConfigured {
		t.Fatalf("reason = %v, want %v", result.reason, privateKeySourceReasonInlineConfigured)
	}
}

func TestResolvePrivateKeySourceReportsBase64BeforeInline(t *testing.T) {
	result := resolvePrivateKeySource(fakeStat(nil), "", "base64-value", "inline-value")
	if result.label != "base64 environment variable (configured)" || !result.present || result.err != nil {
		t.Fatalf("result = %+v, want label=base64 environment variable (configured) present=true err=nil", result)
	}
	if result.reason != privateKeySourceReasonBase64Configured {
		t.Fatalf("reason = %v, want %v", result.reason, privateKeySourceReasonBase64Configured)
	}
}

// fakeDirInfo and fakeRegularFileInfo are minimal os.FileInfo stand-ins whose
// IsDir reports a fixed value, so resolvePrivateKeySource's directory
// rejection and regular-file acceptance are testable without creating a real
// file or directory on disk.
type fakeDirInfo struct{ os.FileInfo }

func (fakeDirInfo) IsDir() bool { return true }

type fakeRegularFileInfo struct{ os.FileInfo }

func (fakeRegularFileInfo) IsDir() bool { return false }

func TestClassifyCollectionSettingsFailsWithoutWebhookSecretWhenAdmitOnly(t *testing.T) {
	classification := classifyCollectionSettings(false, true, 12345)
	if classification.status != StatusFail {
		t.Fatalf("status = %v, want %v", classification.status, StatusFail)
	}
	if classification.reason != collectionSettingsReasonNoSecretAdmitOnly {
		t.Fatalf("reason = %v, want %v", classification.reason, collectionSettingsReasonNoSecretAdmitOnly)
	}
	if classification.remedy == "" {
		t.Fatal("expected a non-empty remedy")
	}
}

func TestClassifyCollectionSettingsWarnsWithoutWebhookSecretWhenNotAdmitOnly(t *testing.T) {
	classification := classifyCollectionSettings(false, false, 12345)
	if classification.status != StatusWarn {
		t.Fatalf("status = %v, want %v", classification.status, StatusWarn)
	}
	if classification.reason != collectionSettingsReasonNoSecretWorker {
		t.Fatalf("reason = %v, want %v", classification.reason, collectionSettingsReasonNoSecretWorker)
	}
}

func TestClassifyCollectionSettingsPassesAdmitOnlyWithWebhookSecret(t *testing.T) {
	classification := classifyCollectionSettings(true, true, 12345)
	if classification.status != StatusPass {
		t.Fatalf("status = %v, want %v", classification.status, StatusPass)
	}
	if classification.reason != collectionSettingsReasonAdmitOnly {
		t.Fatalf("reason = %v, want %v", classification.reason, collectionSettingsReasonAdmitOnly)
	}
	if classification.remedy != "" {
		t.Fatalf("remedy = %q, want empty", classification.remedy)
	}
}

func TestClassifyCollectionSettingsPassesFullCollectionWithWebhookSecret(t *testing.T) {
	classification := classifyCollectionSettings(true, false, 12345)
	if classification.status != StatusPass {
		t.Fatalf("status = %v, want %v", classification.status, StatusPass)
	}
	if classification.reason != collectionSettingsReasonConfigured {
		t.Fatalf("reason = %v, want %v", classification.reason, collectionSettingsReasonConfigured)
	}
	if !strings.Contains(classification.summary, "12345") {
		t.Fatalf("summary = %q, want it to mention the App ID", classification.summary)
	}
}

func TestClassifyBackfillStateReportsErrorBeforeAnythingElse(t *testing.T) {
	// An idle phase alongside a reported error must not mask the error
	// classification; the error is checked first regardless of phase.
	classification := classifyBackfillState(collect.BackfillState{Phase: "idle", Error: "installation enumeration failed"})
	if classification.status != StatusFail {
		t.Fatalf("status = %v, want %v", classification.status, StatusFail)
	}
	if classification.reason != backfillReasonErrored {
		t.Fatalf("reason = %v, want %v", classification.reason, backfillReasonErrored)
	}
	if classification.remedy == "" {
		t.Fatal("expected a remedy for an errored cold start")
	}
}

func TestClassifyBackfillStateWarnsWhenNeverRun(t *testing.T) {
	classification := classifyBackfillState(collect.BackfillState{Phase: "idle"})
	if classification.status != StatusWarn {
		t.Fatalf("status = %v, want %v", classification.status, StatusWarn)
	}
	if classification.reason != backfillReasonNeverRun {
		t.Fatalf("reason = %v, want %v", classification.reason, backfillReasonNeverRun)
	}
	if classification.remedy == "" {
		t.Fatal("expected a remedy for a namespace that has never cold-started")
	}
}

func TestClassifyBackfillStateWarnsWhenIncomplete(t *testing.T) {
	classification := classifyBackfillState(collect.BackfillState{Phase: "collecting"})
	if classification.status != StatusWarn {
		t.Fatalf("status = %v, want %v", classification.status, StatusWarn)
	}
	if classification.reason != backfillReasonIncomplete {
		t.Fatalf("reason = %v, want %v", classification.reason, backfillReasonIncomplete)
	}
	if classification.remedy == "" {
		t.Fatal("expected a remedy for an incomplete cold start")
	}
}

func TestClassifyBackfillStatePassesWhenComplete(t *testing.T) {
	classification := classifyBackfillState(collect.BackfillState{Phase: "complete"})
	if classification.status != StatusPass {
		t.Fatalf("status = %v, want %v", classification.status, StatusPass)
	}
	if classification.reason != backfillReasonComplete {
		t.Fatalf("reason = %v, want %v", classification.reason, backfillReasonComplete)
	}
	if classification.remedy != "" {
		t.Fatalf("expected no remedy for a completed cold start, got %q", classification.remedy)
	}
}

func TestClassifyBudgetHeadroomWarnsOnParkedBeforeLow(t *testing.T) {
	// A parked installation alongside a low one must not mask the parked
	// classification; parked is checked first, matching the prior inline
	// priority.
	rateLimits := []collect.Headroom{
		{InstallationID: 1, Remaining: 50, ParkedUntil: "2026-01-01T00:00:00Z"},
		{InstallationID: 2, Remaining: 5},
	}
	classification := classifyBudgetHeadroom(rateLimits, 10)
	if classification.status != StatusWarn {
		t.Fatalf("status = %v, want %v", classification.status, StatusWarn)
	}
	if classification.reason != budgetHeadroomReasonParked {
		t.Fatalf("reason = %v, want %v", classification.reason, budgetHeadroomReasonParked)
	}
	if !strings.Contains(classification.summary, "1 installations are parked") {
		t.Fatalf("summary = %q, want it to mention the parked count", classification.summary)
	}
	if classification.remedy == "" {
		t.Fatal("expected a remedy for a parked installation")
	}
}

func TestClassifyBudgetHeadroomWarnsWhenAtOrBelowFloor(t *testing.T) {
	rateLimits := []collect.Headroom{
		{InstallationID: 1, Remaining: 10},
		{InstallationID: 2, Remaining: 500},
	}
	classification := classifyBudgetHeadroom(rateLimits, 10)
	if classification.status != StatusWarn {
		t.Fatalf("status = %v, want %v", classification.status, StatusWarn)
	}
	if classification.reason != budgetHeadroomReasonLow {
		t.Fatalf("reason = %v, want %v", classification.reason, budgetHeadroomReasonLow)
	}
	if !strings.Contains(classification.summary, "1 installations are at or below") {
		t.Fatalf("summary = %q, want it to mention the low count", classification.summary)
	}
}

func TestClassifyBudgetHeadroomIgnoresFloorWhenUnconfigured(t *testing.T) {
	// A floor of zero means no configured floor; remaining headroom must
	// never be classified as low regardless of how small it is.
	rateLimits := []collect.Headroom{{InstallationID: 1, Remaining: 0}}
	classification := classifyBudgetHeadroom(rateLimits, 0)
	if classification.status != StatusPass {
		t.Fatalf("status = %v, want %v", classification.status, StatusPass)
	}
	if classification.reason != budgetHeadroomReasonHealthy {
		t.Fatalf("reason = %v, want %v", classification.reason, budgetHeadroomReasonHealthy)
	}
}

func TestClassifyBudgetHeadroomPassesWhenHealthy(t *testing.T) {
	rateLimits := []collect.Headroom{
		{InstallationID: 1, Remaining: 4000},
		{InstallationID: 2, Remaining: 3000},
	}
	classification := classifyBudgetHeadroom(rateLimits, 10)
	if classification.status != StatusPass {
		t.Fatalf("status = %v, want %v", classification.status, StatusPass)
	}
	if classification.reason != budgetHeadroomReasonHealthy {
		t.Fatalf("reason = %v, want %v", classification.reason, budgetHeadroomReasonHealthy)
	}
	if classification.remedy != "" {
		t.Fatalf("expected no remedy for healthy headroom, got %q", classification.remedy)
	}
	if !strings.Contains(classification.summary, "2 installations") {
		t.Fatalf("summary = %q, want it to mention the installation count", classification.summary)
	}
}
