package collect

import "context"

type TokenProvider interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
}

type TraceCapture interface {
	PrepareCache(context.Context, string, string, string) error
	Capture(context.Context, string, string, string) error
	WithdrawRepository(context.Context, string) error
}
