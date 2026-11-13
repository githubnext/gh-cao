// Package githubquota is the single CAO-side authority for coordinating
// GitHub API quota across one or more independently metered buckets.
//
// A bucket is one GitHub rate-limit meter, identified by the GitHub App
// identity, the installation, and the rate-limit resource. The package owns
// quota semantics (observation reconciliation, reservation admission,
// parking, and bucket selection); raw operational storage persistence stays in operational.
//
// The service never receives, stores, or logs GitHub access tokens.
package githubquota

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var bucketLog = logger.New("cao:githubquota:bucket")

// ResourceCore is GitHub's primary REST API rate-limit resource.
const ResourceCore = "core"

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// BucketID identifies one independently metered GitHub API quota.
type BucketID struct {
	// App is a stable, non-secret name for the GitHub App identity.
	App string `json:"app"`
	// Installation is the GitHub App installation ID.
	Installation int64 `json:"installation"`
	// Resource is the GitHub rate-limit resource, such as "core".
	Resource string `json:"resource"`
}

// Normalize returns the bucket with a lower-cased App and Resource and the
// core resource applied when Resource is empty.
func (b BucketID) Normalize() BucketID {
	b.App = strings.ToLower(strings.TrimSpace(b.App))
	b.Resource = strings.ToLower(strings.TrimSpace(b.Resource))
	if b.Resource == "" {
		b.Resource = ResourceCore
	}
	return b
}

// bucketRejectionReason classifies why a BucketID failed Validate. It is
// useful for diagnosing malformed bucket identities — for example a
// misconfigured provider or a corrupted storage key — without logging the
// app name, installation ID, or resource value itself.
type bucketRejectionReason string

const (
	bucketRejectionReasonApp          bucketRejectionReason = "app"
	bucketRejectionReasonInstallation bucketRejectionReason = "installation"
	bucketRejectionReasonResource     bucketRejectionReason = "resource"
)

// classifyBucketRejection reports why a bucket identity is not well formed.
// It is a pure function extracted from Validate so the rejection reason is
// testable independently of the caller's logging, and so Validate itself
// stays a thin wrapper over one classification.
func classifyBucketRejection(bucket BucketID) (bucketRejectionReason, bool) {
	if !identifierPattern.MatchString(bucket.App) {
		return bucketRejectionReasonApp, true
	}
	if bucket.Installation <= 0 {
		return bucketRejectionReasonInstallation, true
	}
	if !identifierPattern.MatchString(bucket.Resource) {
		return bucketRejectionReasonResource, true
	}
	return "", false
}

// Validate reports whether the bucket identity is well formed.
func (b BucketID) Validate() error {
	reason, rejected := classifyBucketRejection(b)
	if !rejected {
		return nil
	}
	switch reason {
	case bucketRejectionReasonApp:
		return errors.New("github quota bucket app must contain 1-64 lowercase letters, digits, dots, underscores, or hyphens")
	case bucketRejectionReasonInstallation:
		return errors.New("github quota bucket installation must be positive")
	default:
		return errors.New("github quota bucket resource must contain 1-64 lowercase letters, digits, dots, underscores, or hyphens")
	}
}

// String renders the bucket for logs and operator reports.
func (b BucketID) String() string {
	return fmt.Sprintf("%s/%d/%s", b.App, b.Installation, b.Resource)
}

// storageKey is the persistence identity. App and Resource cannot contain a
// colon, so the encoding is unambiguous.
func (b BucketID) storageKey() string {
	return b.App + ":" + strconv.FormatInt(b.Installation, 10) + ":" + b.Resource
}

func normalizeBucket(bucket BucketID) (BucketID, error) {
	bucket = bucket.Normalize()
	if reason, rejected := classifyBucketRejection(bucket); rejected {
		bucketLog.Printf("rejected bucket identity reason=%s", reason)
		return BucketID{}, bucket.Validate()
	}
	return bucket, nil
}
