// Package sessionexport defines bounded, source-neutral canonical session snapshots.
package sessionexport

import (
	"context"
	"errors"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const (
	MaxRows  = 1024
	MaxBytes = 256 << 10
)

var ErrTooLarge = errors.New("canonical session exceeds export budget")

type Session struct {
	Namespace string
	Revision  int64
	Run       model.Row
	Facts     map[string][]model.Row
	Quality   map[string]model.Metadata
}

// Reader returns the next completed run in exact repository scope. An empty
// Run ends the current sweep; Revision still identifies the observed snapshot.
type Reader interface {
	ReadSession(context.Context, Request) (Session, error)
}

type Request struct {
	RepositoryIDs     []string
	After             string
	Fields            map[string][]string
	CompletedRevision int64
}
