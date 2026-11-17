// Package cao exposes reviewed source contracts to the native operator.
package cao

import "embed"

// OperatorSources contains production policy, plan and evidence semantics.
// Native execution never evaluates JavaScript downloaded from a repository.
//
//go:embed .github/workflows/shared/authority.mjs .github/workflows/shared/policy.mjs .github/workflows/shared/value-acquisition.mjs modernization/engagement.mjs modernization/github.mjs modernization/actions.mjs all:modernization/squad/.squad/agents
var OperatorSources embed.FS
