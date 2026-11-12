package collect

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/logger"
)

var runnerLog = logger.New("cao:collect:runner")

// TokenProvider mints a short-lived installation token for one collection.
type TokenProvider interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
}

type rateLimitProvider interface {
	RateLimit(ctx context.Context, installationID int64) (int, time.Time, error)
}

// TraceCapture receives only already-acquired repository evidence before the
// ordinary lossy projection. It grants no additional acquisition authority.
type TraceCapture interface {
	PrepareCache(context.Context, string, string, string) error
	Capture(context.Context, string, string, string) error
	WithdrawRepository(context.Context, string) error
}

// Runner retains filesystem state used by recovery and withdrawal.
// Acquisition requires NativeWorker; the filesystem collector is retired.
type Runner struct {
	Lake Lake
	// Timeout bounds recovery refreshes.
	Timeout time.Duration
	Traces  TraceCapture
}

// Validate rejects the retired filesystem collector without a fallback.
func (r Runner) Validate() error {
	return fmt.Errorf("%w: filesystem collection is retired; select native acquisition", collectnative.ErrUnsupported)
}

func (r Runner) timeout() time.Duration {
	if r.Timeout <= 0 {
		return 20 * time.Minute
	}
	return r.Timeout
}

// Collect rejects direct calls to the retired acquisition path.
func (r Runner) Collect(ctx context.Context, task Task) error {
	return r.Validate()
}

// collectionEnvironment builds a minimal environment for the subprocess. The
// process environment is not inherited wholesale so ambient credentials cannot
// leak into a collection.
func collectionEnvironment(apiURL string) []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LANG=" + os.Getenv("LANG"),
	}
	if value := os.Getenv("GH_HOST"); value != "" {
		environment = append(environment, "GH_HOST="+value)
	}
	if value := strings.TrimSpace(apiURL); value != "" {
		environment = append(environment, "GITHUB_API_URL="+value)
		return environment
	}
	if value := os.Getenv("GITHUB_API_URL"); value != "" {
		environment = append(environment, "GITHUB_API_URL="+value)
	}
	return environment
}

func summarize(output string) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) > 2000 {
		trimmed = trimmed[len(trimmed)-2000:]
	}
	return trimmed
}
