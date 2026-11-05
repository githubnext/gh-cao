package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

// NativeValueService is a separately selected execution lane for existing
// reviewed Value programs. Only that lane needs Node/catalog/policy assets;
// native acquisition never stages an Activity lake for it.
type NativeValueService struct {
	Projector Projector
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
	p := s.Projector
	if p.Data == nil || !p.Data.Open() || p.State == nil || p.Leases == nil ||
		p.Enrollment.Authority == nil || p.CatalogRoot == "" || p.PolicyPath == "" ||
		p.RemoteQuota == nil || p.RemoteSearchQuota == nil || p.Tokens == nil {
		return collectnative.ErrUnsupported
	}
	return s.ValidateRuntime()
}

// ValidateRuntime checks only the selected Value lane's deployed dependencies,
// without opening credentials, executing programs or requiring backend handles.
func (s NativeValueService) ValidateRuntime() error {
	p := s.Projector
	if p.CatalogRoot == "" || p.PolicyPath == "" {
		return errors.New("selected native Value execution requires its reviewed catalog and policy")
	}
	if _, err := exec.LookPath(p.node()); err != nil {
		return fmt.Errorf("selected native Value execution requires Node: %w", err)
	}
	for _, path := range []string{
		p.PolicyPath,
		filepath.Join(p.CatalogRoot, "activity", "local-values.mjs"),
		filepath.Join(p.CatalogRoot, "activity", "workflow-values.mjs"),
		filepath.Join(p.CatalogRoot, ".github", "workflows", "shared", "policy.mjs"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("selected native Value execution requires deployed policy/catalog assets: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("selected native Value policy/catalog asset is not a regular file")
		}
	}
	return nil
}

func (s NativeValueService) InvalidateValue(ctx context.Context, scope collectnative.EnrollmentScope) error {
	return s.Projector.Data.Acquisition().InvalidateValue(ctx, scope)
}

func (s NativeValueService) RunValues(ctx context.Context) error {
	if err := s.Validate(); err != nil {
		return err
	}
	root, err := nativeValueCatalog(s.Projector.CatalogRoot)
	if err != nil {
		return err
	}
	s.Projector.CatalogRoot = root
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	remote := make(chan struct{})
	go func() {
		defer close(remote)
		(Worker{Projector: s.Projector}).remoteValueLoop(lifetime)
	}()
	defer func() { cancel(); <-remote }()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := s.Projector.CollectNativeLocalValues(ctx); err != nil && ctx.Err() == nil {
				workerLog.Printf("native local Value deferred error_type=%T; accepted evidence retained", err)
			}
			timer.Reset(s.Projector.minInterval())
		}
	}
}

func (w Worker) collectNativeRemoteValue(ctx context.Context, repository string, now time.Time) error {
	p := w.Projector
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
