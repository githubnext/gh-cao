package operational

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// CLI command storage does not approve commands, acquire credentials, spend
// GitHub quota, or execute subprocesses. Those remain the caller's authority.
type CLICommandQueues interface {
	OpenCLICommandQueue(context.Context, string, CLICommandQueueOptions) (CLICommandQueue, error)
}

// A caller must abort its executor if ownership cannot be renewed. A canceled
// or timed-out command retains its execution claim until the subprocess exits
// and Settle succeeds. Losing a lease never proves subprocess termination.
type CLICommandQueue interface {
	Acquire(context.Context, string, time.Duration) (CLICommandLease, error)
	Renew(context.Context, CLICommandLease, time.Duration) (CLICommandLease, error)
	Release(context.Context, CLICommandLease) error
	AcknowledgeRecovery(context.Context, CLICommandLease) error
	Enqueue(context.Context, CLICommandInvocation, string, int64) (CLICommand, error)
	Claim(context.Context, CLICommandLease) (*CLICommand, error)
	Finish(context.Context, CLICommandLease, int64, CLICommandResult) error
	AppendOutput(context.Context, CLICommandLease, int64, string, string) (CLICommandOutput, error)
	Settle(context.Context, CLICommandLease, int64, *int) error
	Cancel(context.Context, int64) (CLICommand, error)
	Detail(context.Context, int64) (CLICommand, error)
	List(context.Context, CLICommandFilter) (CLICommandPage, error)
	ListBefore(context.Context, CLICommandCursorFilter) (CLICommandCursorPage, error)
	Output(context.Context, int64, int64, int) ([]CLICommandOutput, error)
	Retain(context.Context) (int64, error)
}

type CLICommandStatus string

const (
	CLICommandQueued    CLICommandStatus = "queued"
	CLICommandRunning   CLICommandStatus = "running"
	CLICommandCompleted CLICommandStatus = "completed"
	CLICommandFailed    CLICommandStatus = "failed"
	CLICommandCanceled  CLICommandStatus = "canceled"
	CLICommandTimedOut  CLICommandStatus = "timed-out"
)

func (s CLICommandStatus) Terminal() bool {
	return s == CLICommandCompleted || s == CLICommandFailed || s == CLICommandCanceled || s == CLICommandTimedOut
}

var (
	ErrCLICommandNotFound = errors.New("CLI command not found")
	ErrCLIQueueRecovery   = errors.New("CLI queue recovery requires operator reconciliation")
	ErrCLICommandInput    = errors.New("invalid CLI command input")
)

type CLICommandQueueOptions struct {
	MaxConcurrency, MaxJobs, MaxInputBytes             int
	MaxChunkBytes, MaxOutputBytes, MaxQueueOutputBytes int
	MaxOutputChunks, RetentionDays                     int
	Timeout                                            time.Duration
}

func (o CLICommandQueueOptions) Resolve() (CLICommandQueueOptions, error) {
	for _, field := range []struct {
		value             *int
		fallback, maximum int
	}{
		{&o.MaxConcurrency, 4, 32}, {&o.MaxJobs, 10000, 10000},
		{&o.MaxInputBytes, 1 << 20, 1 << 20}, {&o.MaxChunkBytes, 16 << 10, 16 << 10},
		{&o.MaxOutputBytes, 1 << 20, 1 << 20}, {&o.MaxQueueOutputBytes, 64 << 20, 64 << 20},
		{&o.MaxOutputChunks, 4096, 4096}, {&o.RetentionDays, 30, 3650},
	} {
		if *field.value == 0 {
			*field.value = field.fallback
		}
		if *field.value < 1 || *field.value > field.maximum {
			return o, ErrCLICommandInput
		}
	}
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Minute
	}
	if o.Timeout < time.Millisecond || o.Timeout > 15*time.Minute || o.RetentionDays < 7 {
		return o, ErrCLICommandInput
	}
	return o, nil
}

type CLICommandInvocation struct {
	ActionID  string            `json:"actionId"`
	Command   string            `json:"command"`
	Arguments map[string]bool   `json:"arguments,omitempty"`
	Values    map[string]string `json:"values,omitempty"`
	Input     *string           `json:"input,omitempty"`
	// Actor is the attribution login at admission; ActorID is the immutable
	// GitHub account ID that must match the live permission response.
	// Repository is the single owner/name target of the execution credential.
	Actor      string `json:"actor,omitempty"`
	ActorID    int64  `json:"actorId,omitempty"`
	Repository string `json:"repository,omitempty"`
	// Origin is the in-dashboard hash route where the action was approved.
	// It is navigation context only and never affects admission or execution.
	Origin string `json:"origin,omitempty"`
}

var cliSecret = regexp.MustCompile(`(?i)\b(?:gh[pousr]_[a-z0-9_]+|github_pat_[a-z0-9_]+|AKIA[A-Z0-9]{16})|-----BEGIN .*PRIVATE KEY-----|\bBearer\s+\S+|(?:token|secret|password|credential|authorization|private[-_]?key)\s*["']?\s*[:=]\s*["']?\S+|--[\w-]*(?:token|password|secret|credential)\b`)
var cliSecretKey = regexp.MustCompile(`(?i)token|secret|password|credential|authorization|private[-_]?key`)
var cliIdempotency = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var cliActor = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(?:\[bot\])?$`)
var cliRepository = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
var cliOrigin = regexp.MustCompile(`^#page-[a-z0-9][a-z0-9-]{0,127}(?:\?[A-Za-z0-9._~%&=+:,@/!*'()-]{0,1000})?$`)

// ValidCLICommandOrigin reports whether origin is empty or a bounded
// in-dashboard hash route that cannot name another document or scheme.
func ValidCLICommandOrigin(origin string) bool {
	return origin == "" || cliOrigin.MatchString(origin)
}

// RedactCLICommandText replaces credential-shaped substrings so subprocess
// output can cross the storage boundary that ValidateCLICommandText guards.
func RedactCLICommandText(text string) string {
	text = strings.ToValidUTF8(strings.ReplaceAll(text, "\x00", ""), "\uFFFD")
	text = cliSecret.ReplaceAllString(text, "[REDACTED]")
	if ValidateCLICommandText(text) != nil {
		// Redaction markers can splice fragments into a new match; fail closed.
		return "[REDACTED]"
	}
	return text
}

// Callers must also reject credential values known only to their execution
// environment and frame/redact output before crossing this storage boundary.
func ValidateCLICommandText(text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || cliSecret.MatchString(strings.ReplaceAll(text, "[REDACTED]", "")) {
		return ErrCLICommandInput
	}
	return nil
}

func (i CLICommandInvocation) Encode(maxBytes int) ([]byte, error) {
	if stage, err := i.validate(); err != nil {
		validationLog.Printf("CLI command invocation rejected stage=%s", stage)
		return nil, err
	}
	data, err := json.Marshal(i)
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		validationLog.Printf("CLI command invocation rejected stage=%s bytes=%d max=%d", cliRejectSize, len(data), maxBytes)
		return nil, ErrCLICommandInput
	}
	return data, nil
}

// Stages at which an invocation can be rejected; they name the check that
// failed without carrying any invocation content.
const (
	cliRejectShape     = "shape"
	cliRejectSecretKey = "secret-key"
	cliRejectText      = "text"
	cliRejectSize      = "size"
)

// validate checks an invocation's shape and rejects credential-like content,
// returning the stage that failed so callers can diagnose without exposing
// the rejected text.
func (i CLICommandInvocation) validate() (string, error) {
	if i.ActionID == "" || len(i.ActionID) > 256 || strings.TrimSpace(i.Command) == "" ||
		(i.Input != nil && utf8.RuneCountInString(*i.Input) > 100000) ||
		(i.Actor != "" && !cliActor.MatchString(i.Actor)) ||
		i.ActorID < 0 || (i.Actor != "" && i.ActorID == 0) || (i.Actor == "" && i.ActorID != 0) ||
		(i.Repository != "" && !cliRepository.MatchString(i.Repository)) || !ValidCLICommandOrigin(i.Origin) {
		return cliRejectShape, ErrCLICommandInput
	}
	for key, value := range i.Values {
		if cliSecretKey.MatchString(key) && value != "" {
			return cliRejectSecretKey, ErrCLICommandInput
		}
	}
	for key := range i.Arguments {
		if err := ValidateCLICommandText(key); err != nil {
			return cliRejectText, err
		}
	}
	for _, text := range []string{i.ActionID, i.Command, i.Origin} {
		if err := ValidateCLICommandText(text); err != nil {
			return cliRejectText, err
		}
	}
	for key, value := range i.Values {
		if ValidateCLICommandText(key) != nil || ValidateCLICommandText(value) != nil {
			return cliRejectText, ErrCLICommandInput
		}
	}
	if i.Input != nil && ValidateCLICommandText(*i.Input) != nil {
		return cliRejectText, ErrCLICommandInput
	}
	return "", nil
}

func ValidateCLICommandKey(key string) error {
	if key != "" && (!cliIdempotency.MatchString(key) || cliSecret.MatchString(key)) {
		return ErrCLICommandInput
	}
	return nil
}

type CLICommandLease struct {
	Owner     string
	Fence     int64
	ExpiresAt time.Time
}

type CLICommand struct {
	ID int64
	CLICommandInvocation
	Status                            CLICommandStatus
	CreatedAt                         time.Time
	StartedAt, CompletedAt, Deadline  *time.Time
	CompletionWeek                    string
	IdempotencyKey                    string
	RetryOf                           int64
	ExitCode                          *int
	Error                             string
	OutputBytes, OutputChunks         int
	OutputTruncated, ExecutionClaimed bool
}

type CLICommandResult struct {
	Status   CLICommandStatus
	ExitCode *int
	Error    string
}

type CLICommandOutput struct {
	Sequence  int64
	Stream    string
	Data      string
	Truncated bool
}

type CLICommandFilter struct {
	Statuses      []CLICommandStatus
	Limit, Offset int
}

type CLICommandCursorFilter struct {
	Statuses []CLICommandStatus
	BeforeID int64
	Limit    int
}

type CLICommandCursorPage struct {
	Commands        []CLICommand
	HasMore         bool
	Revision        int64
	RecoveryBlocked bool
}

func (f CLICommandCursorFilter) Validate() error {
	if f.Limit < 1 || f.Limit > 500 || f.BeforeID < 0 || len(f.Statuses) > 6 {
		return ErrCLICommandInput
	}
	for _, status := range f.Statuses {
		if status != CLICommandQueued && status != CLICommandRunning && !status.Terminal() {
			return fmt.Errorf("CLI command status: %w", ErrCLICommandInput)
		}
	}
	return nil
}

func (f CLICommandFilter) Validate() error {
	if f.Limit < 1 || f.Limit > 500 || f.Offset < 0 || f.Offset > 1000000 || len(f.Statuses) > 6 {
		return ErrCLICommandInput
	}
	for _, status := range f.Statuses {
		if status != CLICommandQueued && status != CLICommandRunning && !status.Terminal() {
			return fmt.Errorf("CLI command status: %w", ErrCLICommandInput)
		}
	}
	return nil
}

type CLICommandPage struct {
	Commands        []CLICommand
	Total, Running  int
	Revision        int64
	RecoveryBlocked bool
}
