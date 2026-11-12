package collect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var workerLog = logger.New("cao:collect:worker")

// consumerNameSource identifies which input determined a worker's resolved
// consumer name. It is useful for diagnosing a misconfigured deployment
// without logging the name itself.
type consumerNameSource string

const (
	consumerNameSourceExplicit  consumerNameSource = "explicit"
	consumerNameSourceGenerated consumerNameSource = "generated"
)

// resolveConsumerName applies the standard priority for a worker's consumer
// name: an explicit configured value, then a name generated from hostname
// and a random suffix obtained from hostnameFunc and tokenFunc. It is a pure
// function so the resolution logic can be exercised without depending on the
// real hostname or a random token. It returns the resolved name and which
// input supplied it, so callers can log the source without exposing the
// name.
func resolveConsumerName(explicit string, hostnameFunc func() (string, error), tokenFunc func() (string, error)) (string, consumerNameSource) {
	if explicit != "" {
		return explicit, consumerNameSourceExplicit
	}
	host, err := hostnameFunc()
	if err != nil || host == "" {
		host = "worker"
	}
	suffix, err := tokenFunc()
	if err != nil {
		return host, consumerNameSourceGenerated
	}
	return host + "-" + suffix[:8], consumerNameSourceGenerated
}

func consumerName(explicit string) string {
	name, source := resolveConsumerName(explicit, os.Hostname, operationToken)
	workerLog.Printf("worker resolved consumer name source=%s", source)
	return name
}

func sleep(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func operationToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
