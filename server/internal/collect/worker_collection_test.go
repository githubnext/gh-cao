package collect

import (
	"errors"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func TestWorkerRejectsRetiredCollectionBeforeQueueAccess(t *testing.T) {
	if err := (Worker{}).Run(t.Context()); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("filesystem worker did not fail closed: %v", err)
	}
}

func TestWorkerSelectsNativeWithoutFilesystemFallback(t *testing.T) {
	worker := Worker{Native: &NativeWorker{}}
	if err := worker.Run(t.Context()); !errors.Is(err, collectnative.ErrUnsupported) || strings.Contains(err.Error(), "filesystem") {
		t.Fatalf("native configuration error was lost or fell back to filesystem: %v", err)
	}
}
