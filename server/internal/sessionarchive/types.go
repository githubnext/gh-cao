// Package sessionarchive owns private approved evidence independently of dashboard storage.
package sessionarchive

import (
	"errors"
	"time"
)

var (
	ErrBusy      = errors.New("session archive owner or query is busy")
	ErrCapacity  = errors.New("session archive capacity exceeded")
	ErrWithdrawn = errors.New("session archive evidence withdrawn")
	ErrReplay    = errors.New("session archive stale evidence replay rejected")
	ErrClosed    = errors.New("session archive is closed")
	ErrIntegrity = errors.New("session archive integrity check failed")
	ErrLimit     = errors.New("session archive result exceeds limit")
	ErrNotFound  = errors.New("session archive trace not found")
)

type Member struct {
	Namespace, Repository, RunID string
	Attempt                      int64
	ID, Version                  string
	Events                       int64
}

type Projection struct {
	Namespace, Repository, RunID string
	Attempt                      int64
	ID, Version, Day             string
	ArtifactDigest, PolicyDigest string
	CapturedAt                   time.Time
	Gaps                         []string
	Events                       []Event
}

type File struct {
	Path, Digest string
	Bytes, Rows  int64
	Members      []Member
}

type Manifest struct {
	Version    int
	Generation uint64
	CreatedAt  time.Time
	Files      []File
	Members    []Member
	Fences     []Fence
}

type Fence struct {
	Namespace, Repository, RunID string
	Attempt                      int64
	Versions                     []string
	Withdrawn                    bool
}

type Scope struct {
	Namespace    string
	Repositories []string
}

type TimelineRequest struct {
	Repository, RunID string
	Attempt           int64
	Limit             int
}

type Summary struct {
	Repository   string `json:"repository"`
	RunID        string `json:"runId"`
	Attempt      int64  `json:"attempt"`
	ProjectionID string `json:"projectionId"`
	Events       int64  `json:"events"`
}

type Status struct {
	Enabled                    bool
	Generation                 uint64
	ActiveTraces, StagedTraces int
	ArchiveBytes, StagingBytes int64
}
