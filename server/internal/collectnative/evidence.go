// Package collectnative acquires bounded run-attempt evidence without Activity,
// a subprocess, or a filesystem. It does not activate canonical dashboard data.
package collectnative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
)

var (
	ErrCapacity    = errors.New("native acquisition resource budget exceeded")
	ErrIdentity    = errors.New("native acquisition evidence identity mismatch")
	ErrUnsupported = errors.New("native acquisition cannot activate the legacy filesystem projector or trace destinations")
	ErrClaimLost   = errors.New("native acquisition claim expired or superseded")
	ErrBusy        = errors.New("native acquisition attempt is already claimed")
)

var coordinate = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9._-]+$`)
var workflowPath = regexp.MustCompile(`^\.github/workflows/[A-Za-z0-9_.-]+\.lock\.yml$`)
var artifactIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type Identity struct {
	Repository     string `json:"repository"`
	InstallationID int64  `json:"installationId"`
	RunID          int64  `json:"runId"`
	Attempt        int    `json:"attempt"`
}

func (i Identity) Validate() error {
	if len(i.Repository) > 256 || !coordinate.MatchString(i.Repository) || i.InstallationID <= 0 || i.RunID <= 0 || i.Attempt <= 0 {
		return ErrIdentity
	}
	return nil
}

// Limits are hard ceilings, not successful truncation thresholds.
type Limits struct {
	Bytes, LineBytes, Entries, Runs, Pages, Artifacts int
}

func (l Limits) defaults() Limits {
	if l.Bytes <= 0 {
		l.Bytes = 32 << 20
	}
	if l.LineBytes <= 0 {
		l.LineBytes = 4 << 20
	}
	if l.Entries <= 0 {
		l.Entries = 32768
	}
	if l.Runs <= 0 {
		l.Runs = 10000
	}
	if l.Pages <= 0 {
		l.Pages = 100
	}
	if l.Artifacts <= 0 {
		l.Artifacts = 100
	}
	return l
}

// Artifact is an already acquired, exact-attempt archive. Paths are logical
// archive names and must never be interpreted as filesystem locations.
type Artifact struct {
	ID    int64
	Name  string
	Files map[string][]byte
}

type Run struct {
	Identity
	WorkflowPath string
	Fields       map[string]any
}

// Provider owns native, credential-scoped acquisition. Visit is synchronous;
// callers release one archive before acquiring the next.
type Provider interface {
	Runs(context.Context, string, int64, time.Time, func(Run) error) error
	Artifacts(context.Context, Run, func(Artifact) error) error
}

// Evidence contains only safe projections, never raw sessions or artifact data.
// Gaps are stable classifications, not raw upstream errors.
type Evidence struct {
	Version  int              `json:"version"`
	Identity Identity         `json:"identity"`
	Run      map[string]any   `json:"run"`
	Gaps     []string         `json:"gaps"`
	Contexts []mining.Context `json:"contexts,omitempty"`
}

func (e Evidence) Bytes() ([]byte, string, error) {
	if e.Version != 1 || e.Identity.Validate() != nil {
		return nil, "", ErrIdentity
	}
	content, err := json.Marshal(e)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(content)
	return content, hex.EncodeToString(sum[:]), nil
}

func decode(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing evidence content")
	}
	return nil
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func scalar(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case json.Number:
		return string(value)
	}
	return ""
}

func number(value any) (float64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

func matches(data map[string]any, i Identity) bool {
	for _, field := range []string{"runId", "run_id", "runid"} {
		if value, present := data[field]; present && scalar(value) != strconv.FormatInt(i.RunID, 10) {
			return false
		}
	}
	for _, field := range []string{"runAttempt", "run_attempt"} {
		if value, present := data[field]; present && scalar(value) != strconv.Itoa(i.Attempt) {
			return false
		}
	}
	return true
}

func repositoryOf(run map[string]any) string {
	repository := scalar(run["repository"])
	if !strings.Contains(repository, "/") {
		repository = scalar(run["organization"]) + "/" + repository
	}
	return strings.ToLower(repository)
}
