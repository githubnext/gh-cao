package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestParseRedisMaxBytesEnv(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  int64
		valid bool
	}{
		{"valid positive", "200000000", 200_000_000, true},
		{"smallest positive", "1", 1, true},
		{"empty", "", 0, false},
		{"zero", "0", 0, false},
		{"negative", "-1", 0, false},
		{"non-numeric", "200MB", 0, false},
		{"overflow", "9223372036854775808", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseRedisMaxBytesEnv(test.value)
			if test.valid {
				if err != nil || got != test.want {
					t.Fatalf("parseRedisMaxBytesEnv(%q) = %d, %v; want %d, nil", test.value, got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseRedisMaxBytesEnv(%q) accepted invalid input, got %d", test.value, got)
			}
		})
	}
}

func TestRedisMaxBytesConfiguration(t *testing.T) {
	t.Setenv("CAO_REDIS_MAX_BYTES", "")
	if err := os.Unsetenv("CAO_REDIS_MAX_BYTES"); err != nil {
		t.Fatal(err)
	}
	if maximum, err := RedisMaxBytesFromEnv(0); err != nil || maximum != 200_000_000 {
		t.Fatalf("default budget: %d, %v", maximum, err)
	}
	t.Setenv("CAO_REDIS_MAX_BYTES", "200000000")
	if maximum, err := RedisMaxBytesFromEnv(0); err != nil || maximum != 200_000_000 {
		t.Fatalf("default-sized budget: %d, %v", maximum, err)
	}
	for _, invalid := range []string{"", "0", "-1", "200MB", "9223372036854775808"} {
		t.Setenv("CAO_REDIS_MAX_BYTES", invalid)
		if _, err := RedisMaxBytesFromEnv(0); err == nil {
			t.Fatalf("invalid environment budget accepted: %q", invalid)
		}
		if maximum, err := RedisMaxBytesFromEnv(4096); err != nil || maximum != 4096 {
			t.Fatalf("environment overrides explicit config: %d, %v", maximum, err)
		}
	}
	if _, err := RedisMaxBytesFromEnv(-1); err == nil {
		t.Fatal("negative configuration accepted")
	}
}

func TestServerAppliesRedisMemoryConfiguration(t *testing.T) {
	t.Setenv("CAO_REDIS_MAX_BYTES", "8000000")
	t.Setenv("CAO_QUERY_CACHE_DISABLED", "false")
	t.Setenv("CAO_QUERY_CACHE_MAX_BYTES", "67108864")
	t.Setenv("CAO_QUERY_CACHE_MAX_RESULT_BYTES", "1048576")
	for _, explicit := range []int64{0, 4_000_000} {
		store := redisx.NewStore(redisMaintenanceClient{}, "memory-config")
		app, err := New(t.Context(), store, Config{
			Database: &postgresx.Store{}, Listen: "127.0.0.1:8443",
			SiteDirectory: t.TempDir(), AccessToken: strings.Repeat("x", 32),
			RedisMaxBytes: explicit,
		})
		if err != nil {
			t.Fatal(err)
		}
		expected := explicit
		if expected == 0 {
			expected = 8_000_000
		}
		if app.config.RedisMaxBytes != expected || store.MaxMemoryBytes() != expected {
			t.Fatalf("server/store budget mismatch: app=%d store=%d expected=%d",
				app.config.RedisMaxBytes, store.MaxMemoryBytes(), expected)
		}
	}
}

type redisMaintenanceClient struct {
	err   error
	calls chan time.Duration
}

func (client redisMaintenanceClient) Do(ctx context.Context, command ...string) (any, error) {
	if deadline, ok := ctx.Deadline(); ok && client.calls != nil {
		select {
		case client.calls <- time.Until(deadline):
		default:
		}
	}
	if client.err != nil {
		return nil, client.err
	}
	if command[0] == "PING" {
		return "PONG", nil
	}
	if command[0] == "INFO" {
		return cacheTestMemoryInfo, nil
	}
	if command[0] == "EVAL" && strings.Contains(command[1], `return {probe("INFO"`) {
		return cacheTestCapabilityReply(), nil
	}
	return []any{nil, int64(0), int64(0), int64(0), int64(0)}, nil
}

func (redisMaintenanceClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, errors.New("unexpected maintenance pipeline")
}

func maintenanceApp(client redisx.CommandClient, output io.Writer) *App {
	return &App{services: redisx.NewStore(client, "maintenance").OperationalServices(), config: Config{
		QueryCache: QueryCacheConfig{MaxBytes: 64 << 20},
		Logger:     log.New(output, "", 0),
	},
	}
}

func TestRedisMaintenanceStartsAndStopsWithService(t *testing.T) {
	client := redisMaintenanceClient{calls: make(chan time.Duration, 8)}
	app := maintenanceApp(client, io.Discard)
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case remaining := <-client.calls:
		if remaining <= 0 || remaining > redisMaintenanceTimeout {
			t.Fatalf("unbounded startup maintenance: %s", remaining)
		}
	default:
		t.Fatal("startup did not run Redis maintenance")
	}
	stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("maintenance outlived service stop: %v", err)
	}
}

func TestRedisMaintenancePeriodicAndCancellation(t *testing.T) {
	client := redisMaintenanceClient{calls: make(chan time.Duration, 8)}
	app := maintenanceApp(client, io.Discard)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.runRedisMaintenance(ctx, time.Millisecond)
	}()
	select {
	case <-client.calls:
	case <-time.After(time.Second):
		t.Fatal("periodic maintenance did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop on cancellation")
	}
}

func TestRedisMaintenanceFailuresAreExplicitAndSanitized(t *testing.T) {
	var output bytes.Buffer
	app := maintenanceApp(redisMaintenanceClient{err: errors.New("private backend error")}, &output)
	if err := app.Start(t.Context()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("startup error was hidden or exposed backend details: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	app.runRedisMaintenance(ctx, time.Millisecond)
	if !strings.Contains(output.String(), "Operational cache maintenance failed") || strings.Contains(output.String(), "private") {
		t.Fatalf("periodic error was hidden or exposed backend details: %q", output.String())
	}
	if app.startContext != nil {
		t.Fatal("failed startup became active")
	}
}

func TestRedisMemoryPressureLogExplainsNodeWideScopeAndSafeRemediation(t *testing.T) {
	var output bytes.Buffer
	app := maintenanceApp(redisPressureMaintenanceClient{}, &output)
	err := app.maintainRedisCaches(t.Context())
	if !errors.Is(err, redisx.ErrMemoryPressure) {
		t.Fatalf("maintenance error = %v, want memory pressure", err)
	}
	message := output.String()
	for _, expected := range []string{
		"Operational cache maintenance pressure",
		"used_bytes=510554256",
		"budget_bytes=200000000",
		"disposable_cache_entries_evicted=0",
		"inspect_operational_backend_capacity",
		"preserve_protected_state",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("pressure log %q does not contain %q", message, expected)
		}
	}
	if strings.Contains(err.Error(), "reduce operational state") ||
		!strings.Contains(err.Error(), "preserve noeviction") ||
		!strings.Contains(err.Error(), "do not delete protected state") {
		t.Fatalf("pressure error does not preserve protected state: %v", err)
	}
}

type redisPressureMaintenanceClient struct{}

func (redisPressureMaintenanceClient) Do(_ context.Context, command ...string) (any, error) {
	switch command[0] {
	case "INFO":
		return "# Memory\r\nused_memory:510554256\r\nmaxmemory:0\r\n", nil
	case "EVAL":
		if strings.Contains(command[1], `return {probe("INFO"`) {
			return []any{
				[]any{int64(0), "ERR command is not allowed in scripts"},
				[]any{int64(1), nil},
			}, nil
		}
		return nil, errors.New("unexpected maintenance script")
	case "DEL":
		return int64(0), nil
	case "SCAN":
		return []any{"0", []any{}}, nil
	default:
		return nil, errors.New("unexpected Redis command")
	}
}

func (redisPressureMaintenanceClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, errors.New("unexpected maintenance pipeline")
}

type managedRedisMaintenanceClient struct{}

func (managedRedisMaintenanceClient) Do(_ context.Context, command ...string) (any, error) {
	switch command[0] {
	case "PING":
		return "PONG", nil
	case "INFO":
		return "used_memory:1024\r\n", nil
	case "EVAL":
		return []any{
			[]any{int64(0), "ERR command is not allowed in scripts"},
			[]any{int64(1), nil},
		}, nil
	case "DEL":
		return int64(0), nil
	default:
		return nil, errors.New("unexpected managed maintenance command")
	}
}

func (managedRedisMaintenanceClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, errors.New("unexpected managed maintenance pipeline")
}

func TestRedisMaintenanceReportsUnsupportedCachingWithoutBlockingStartup(t *testing.T) {
	var output bytes.Buffer
	app := maintenanceApp(redisMaintenanceClient{}, &output)
	app.services = redisx.NewStore(managedRedisMaintenanceClient{}, "managed-startup").OperationalServices()
	if err := app.Start(t.Context()); err != nil {
		t.Fatalf("optional cache capabilities blocked protected storage startup: %v", err)
	}
	stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	health, err := app.services.Health.Health(t.Context())
	if err != nil || !health.CacheDisabled {
		t.Fatal("unsupported managed-provider caches remained enabled")
	}
	if !strings.Contains(output.String(), "disposable caching disabled") || !strings.Contains(output.String(), "protected storage remains enabled") {
		t.Fatalf("cache degradation was not reported explicitly: %q", output.String())
	}
}
