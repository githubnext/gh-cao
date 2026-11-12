package collect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

// NativeValueService is a separately selected execution lane for reviewed
// Value programs. Native acquisition never stages an Activity lake for it.
type NativeValueService struct {
	State                    operational.StateStore
	Leases                   operational.LeaseStore
	Data                     *postgresx.Store
	Enrollment               Enrollment
	CatalogRoot              string
	GitHubAPIURL             string
	Tokens                   TokenProvider
	QuotaApp                 string
	RemoteQuota              *githubquota.Service
	RemoteSearchQuota        *githubquota.Service
	RemoteHTTP               *http.Client
	RemoteValuePlan          *RemoteValuePlanCache
	PolicyPath               string
	MinInterval              time.Duration
	InventoryRepositoryLimit int
	nativeValueScope         *collectnative.EnrollmentScope
}

func (s NativeValueService) minInterval() time.Duration {
	if s.MinInterval <= 0 {
		return 5 * time.Minute
	}
	return s.MinInterval
}

func nativeValueCatalog(root string) (string, error) {
	if root == "" {
		return "", collectnative.ErrUnsupported
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func (s NativeValueService) Validate() error {
	p := s
	if p.Data == nil || !p.Data.Open() || p.State == nil || p.Leases == nil ||
		p.Enrollment.Authority == nil || p.CatalogRoot == "" || p.PolicyPath == "" ||
		p.RemoteQuota == nil || p.RemoteSearchQuota == nil || p.Tokens == nil {
		return collectnative.ErrUnsupported
	}
	return s.ValidateRuntime()
}

// ValidateRuntime checks only the selected Value lane's deployed policy,
// without opening credentials or requiring backend handles.
func (s NativeValueService) ValidateRuntime() error {
	p := s
	if p.CatalogRoot == "" || p.PolicyPath == "" {
		return errors.New("selected native Value execution requires its reviewed catalog and policy")
	}
	root, err := os.Stat(p.CatalogRoot)
	if err != nil {
		return fmt.Errorf("selected native Value execution requires its reviewed catalog: %w", err)
	}
	if !root.IsDir() {
		return errors.New("selected native Value catalog is not a directory")
	}
	info, err := os.Stat(p.PolicyPath)
	if err != nil {
		return fmt.Errorf("selected native Value execution requires its reviewed policy: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("selected native Value policy is not a regular file")
	}
	return nil
}

func (s NativeValueService) InvalidateValue(ctx context.Context, scope collectnative.EnrollmentScope) error {
	return s.Data.Acquisition().InvalidateValue(ctx, scope)
}

func (s NativeValueService) RunValues(ctx context.Context) error {
	if err := s.Validate(); err != nil {
		return err
	}
	root, err := nativeValueCatalog(s.CatalogRoot)
	if err != nil {
		return err
	}
	s.CatalogRoot = root
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	remote := make(chan struct{})
	go func() {
		defer close(remote)
		s.remoteValueLoop(lifetime)
	}()
	defer func() { cancel(); <-remote }()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := s.CollectNativeLocalValues(ctx); err != nil && ctx.Err() == nil {
				workerLog.Printf("native local Value deferred error_type=%T; accepted evidence retained", err)
			}
			timer.Reset(s.minInterval())
		}
	}
}

func (p NativeValueService) collectNativeRemoteValue(ctx context.Context, repository string, now time.Time) error {
	scope, err := p.Enrollment.Authority.CurrentScope(ctx, repository)
	if err != nil {
		return err
	}
	lease := "collect:value-native:" + strings.TrimPrefix(remoteValueHintKey(repository), "collect:value-remote:") +
		":" + strconv.FormatInt(scope.InstallationID, 10) + ":" + strconv.FormatInt(scope.Epoch, 10)
	token, err := operationToken()
	if err != nil {
		return err
	}
	acquired, err := p.Leases.TryLock(ctx, lease, token, 12*time.Minute)
	if err != nil || !acquired {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = p.Leases.Unlock(release, lease, token)
	}()
	acquisition := p.Data.Acquisition()
	hint, err := acquisition.ValueHint(ctx, scope)
	if err != nil {
		return err
	}
	if hint.AttemptDay == now.UTC().Format("2006-01-02") && hint.Dirty == hint.Processed ||
		!hint.RetryAt.IsZero() && now.Before(hint.RetryAt) {
		return nil
	}
	if hint.Dirty == hint.Processed {
		if err := acquisition.InvalidateValue(ctx, scope); err != nil {
			return err
		}
		hint, err = acquisition.ValueHint(ctx, scope)
		if err != nil {
			return err
		}
	}
	run, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	collectErr := p.CollectScopedRemoteValues(run, scope)
	var retry time.Time
	if collectErr != nil {
		retry = now.Add(time.Hour)
	}
	return errors.Join(collectErr, acquisition.CompleteValueHint(ctx, scope, hint.Dirty, now, retry))
}
