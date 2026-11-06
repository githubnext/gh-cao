package collectnative

import "context"

// EnrollmentAuthority is ingestion scope, never workflow execution authority.
// Mutations run in one durable, deployment-wide ownership transaction. The
// callback may enumerate current GitHub access, but cannot commit stale changes
// after its transaction has expired or its process has disconnected.
type EnrollmentAuthority interface {
	ScopeReader
	WithMutation(context.Context, func(context.Context) error) error
	Enroll(context.Context, int64, []string) error
	Withdraw(context.Context, int64, []string) ([]string, error)
	ScanRepositories(context.Context, int64, string, int) ([]string, string, error)
	RepositoryCount(context.Context) (int64, error)
	Installations(context.Context) ([]int64, error)
	HasInstallation(context.Context, int64) (bool, error)
}
