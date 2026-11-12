package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
	gateway "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewayhost"
)

var version = "dev"
var authorityLog = logger.New("cao:webhook-gateway:authority")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil && !errors.Is(err, context.Canceled) {
		// Never print errors from credential, policy, database, or HTTP libraries.
		fmt.Fprintln(os.Stderr, "webhook gateway stopped: configuration or infrastructure unavailable")
		os.Exit(1)
	}
}

type environmentSecrets struct{ Policies gateway.PolicyProvider }

func (e environmentSecrets) WebhookSecrets(ctx context.Context, id string) ([]byte, []byte, error) {
	currentName, previousName := "CAO_GATEWAY_WEBHOOK_SECRET", "CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET"
	if e.Policies != nil && id != "" {
		policy, err := e.Policies.Load(ctx)
		if err != nil {
			return nil, nil, gateway.ErrUnavailable
		}
		if source, found := policy.ExternalSource(id); found {
			currentName, previousName = source.SecretEnv, source.SecretEnv+"_PREVIOUS"
		}
	}
	current := []byte(os.Getenv(currentName))
	previous := []byte(os.Getenv(previousName))
	if len(current) < 32 || len(current) > 512 || (len(previous) > 0 && (len(previous) < 32 || len(previous) > 512)) {
		return nil, nil, errors.New("gateway webhook secret must contain 32 to 512 bytes")
	}
	return current, previous, nil
}

func loadKey(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("gateway App private key file is required")
	}
	file, err := os.Open(path) // #nosec G304 G703 -- credential path is supplied by the trusted deployment, never webhook input.
	if err != nil {
		return nil, errors.New("gateway App private key is unavailable")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) == 0 || len(data) > 64<<10 {
		return nil, errors.New("gateway App private key is invalid")
	}
	return data, nil
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("cao-webhook-gateway", flag.ContinueOnError)
	role := flags.String("role", "", "receive, dispatch, local, or discover; credentials must be isolated by role")
	var workflows []string
	flags.Func("workflow", "discover only: absolute workflow lock-file path (repeatable)", func(path string) error {
		if len(workflows) >= 1024 {
			return errors.New("gateway discovery workflow limit exceeded")
		}
		workflows = append(workflows, path)
		return nil
	})
	policyPath := flags.String("policy", "", "absolute path to reviewed cao.json")
	listen := flags.String("listen", "127.0.0.1:8081", "receiving HTTP address")
	cert := flags.String("tls-cert", "", "receiving TLS certificate file")
	key := flags.String("tls-key", "", "receiving TLS private key file")
	repository := flags.String("repository", "", "local only: exact OWNER/REPO to poll read-only")
	namespace := flags.String("namespace", "", "local only: exact pre-provisioned PostgreSQL operational namespace")
	events := flags.String("events", "issues,issue_comment,pull_request", "local only: comma-separated repository Events API event families")
	interval := flags.Duration("poll-interval", time.Minute, "local only: minimum GitHub polling interval")
	once := flags.Bool("once", false, "local only: poll once, drain available work, and report simulated outcomes")
	apiURL := flags.String("api-url", "https://api.github.com", "local only: HTTPS GitHub API base URL (numeric loopback HTTP allowed for tests)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*role != "receive" && *role != "dispatch" && *role != "local" && *role != "discover") || flags.NArg() != 0 {
		return errors.New("gateway requires an explicit receive, dispatch, local, or discover role")
	}
	if err := validateRoleCredentials(*role); err != nil {
		return err
	}
	var incompatible bool
	flags.Visit(func(f *flag.Flag) {
		if *role == "discover" {
			incompatible = incompatible || f.Name != "role" && f.Name != "workflow"
			return
		}
		productionOption := f.Name == "policy" || f.Name == "listen" || f.Name == "tls-cert" || f.Name == "tls-key"
		if f.Name == "workflow" || *role == "local" && productionOption || *role != "local" && f.Name != "role" && !productionOption {
			incompatible = true
		}
	})
	if incompatible {
		return errors.New("gateway discovery, local simulation, and production options cannot be combined")
	}
	if *role == "discover" {
		return discoverWorkflows(ctx, workflows, os.Stdout)
	}
	if *role == "local" {
		return runLocal(ctx, localOptions{repository: *repository, namespace: *namespace, events: strings.Split(*events, ","),
			interval: *interval, once: *once, apiURL: *apiURL}, os.Stdout)
	}
	baseLoader := gateway.NewPolicyLoader(*policyPath)
	policy, err := baseLoader.Load(ctx)
	if err != nil {
		return err
	}
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		if err := os.Setenv("OTEL_SERVICE_NAME", "cao-webhook-gateway"); err != nil {
			return errors.New("gateway telemetry resource is unavailable")
		}
	}
	shutdown, err := telemetry.Setup(ctx, version)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = shutdown(shutdownCtx)
	}()
	store, err := postgres.Open(ctx, os.Getenv("CAO_POSTGRES_URL"), policy.Policy.Namespace, postgres.Config{})
	if err != nil {
		return errors.New("gateway operational storage is unavailable")
	}
	defer func() { _ = store.Close() }()
	queue, err := store.OpenWebhookQueue(ctx, "github-webhooks", policy.Policy.Queue)
	if err != nil {
		return err
	}
	registry, ok := queue.(gateway.RegistryStore)
	if !ok {
		return gateway.ErrUnavailable
	}
	loader := webhookgatewayhost.RegistryLoader{Base: baseLoader, Store: registry}
	policy, err = loader.Load(ctx)
	if err != nil {
		return err
	}
	if *role == "receive" {
		return serve(ctx, *listen, *cert, *key, policy, loader, queue,
			webhookgatewayhost.ReceiveAuthority{Loader: loader, Queue: queue, State: store})
	}
	return dispatch(ctx, policy, loader, baseLoader, queue, store)
}

func validateRoleCredentials(role string) error {
	if role != "receive" {
		for _, entry := range os.Environ() {
			name, value, _ := strings.Cut(entry, "=")
			if strings.HasPrefix(name, "CAO_GATEWAY_SOURCE_") && value != "" {
				return errors.New("provider signing credentials belong only in the receiving process")
			}
		}
	}
	if role == "local" || role == "discover" {
		for _, name := range []string{"CAO_GATEWAY_RECEIVE_KEY_FILE", "CAO_GATEWAY_DISPATCH_KEY_FILE",
			"CAO_GATEWAY_WEBHOOK_SECRET", "CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET"} {
			if os.Getenv(name) != "" {
				return errors.New("local simulation and discovery must not receive production gateway credentials")
			}
		}
	}
	if role == "receive" &&
		(os.Getenv("CAO_GATEWAY_RECEIVE_KEY_FILE") != "" || os.Getenv("CAO_GATEWAY_DISPATCH_KEY_FILE") != "") {
		return errors.New("receiving process must not receive App private keys")
	}
	if role == "dispatch" &&
		(os.Getenv("CAO_GATEWAY_WEBHOOK_SECRET") != "" || os.Getenv("CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET") != "") {
		return errors.New("dispatch process must not receive webhook signing secrets")
	}
	return nil
}

func serve(ctx context.Context, address, certificate, privateKey string, policy gateway.PolicySnapshot, loader gateway.PolicyProvider, queue gateway.Queue, authorizer gateway.Authorizer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if certificate == "" || privateKey == "" {
		if certificate != "" || privateKey != "" {
			return errors.New("gateway TLS requires both certificate and private key")
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("plaintext gateway must listen on numeric loopback behind an HTTPS proxy")
		}
	}
	secrets := environmentSecrets{Policies: loader}
	if _, _, err := secrets.WebhookSecrets(ctx, ""); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		current, err := loader.Load(r.Context())
		if err != nil || !current.Revision.Enabled {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if err := queue.ApplyRevision(r.Context(), current.Revision); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		depth, supported := queue.(gateway.QueueDepthProvider)
		if !supported {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if _, err := depth.QueueDepth(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	sources := make(map[string]bool)
	slots := make(chan struct{}, 64)
	for _, source := range policy.Policy.ExternalSources {
		if _, _, err := secrets.WebhookSecrets(ctx, source.ID); err != nil {
			return err
		}
		sources[source.ID] = true
		mux.Handle("/webhooks/"+source.ID, boundedIngress(slots, &gateway.Ingress{
			SourceID: source.ID, External: true, Secrets: secrets, Policies: loader, Queue: queue, Authorizer: authorizer,
		}))
	}
	if policy.Policy.Discovery != nil {
		managed, ok := loader.(webhookgatewayhost.RegistryLoader)
		if !ok {
			return gateway.ErrUnavailable
		}
		sources[gateway.CompilerSourceID] = true
		mux.Handle("/webhooks/"+gateway.CompilerSourceID, boundedIngress(slots, &gateway.Ingress{
			SourceID: gateway.CompilerSourceID, Secrets: secrets, Policies: loader, Queue: queue, Authorizer: authorizer,
			Discovery: webhookgatewayhost.DiscoveryHints{Policies: managed.Base, Registry: managed.Store},
		}))
	}
	for _, subscription := range policy.Subscriptions {
		source := subscription.Source.ID
		if sources[source] {
			continue
		}
		sources[source] = true
		mux.Handle("/webhooks/"+source, boundedIngress(slots, &gateway.Ingress{
			SourceID: source, Secrets: secrets, Policies: loader, Queue: queue, Authorizer: authorizer,
		}))
	}
	server := &http.Server{
		Addr: address, Handler: otelhttp.NewHandler(mux, "cao.webhook-gateway"),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	var err error
	if certificate != "" {
		err = server.ListenAndServeTLS(certificate, privateKey)
	} else {
		err = server.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		<-stopped
		return nil
	}
	return errors.New("gateway HTTP listener is unavailable")
}

func boundedIngress(slots chan struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func dispatch(ctx context.Context, policy gateway.PolicySnapshot, loader, baseLoader gateway.PolicyProvider, queue gateway.Queue, store *postgres.Store) error {
	receiverKey, err := loadKey(os.Getenv("CAO_GATEWAY_RECEIVE_KEY_FILE"))
	if err != nil {
		return err
	}
	dispatchKey, err := loadKey(os.Getenv("CAO_GATEWAY_DISPATCH_KEY_FILE"))
	if err != nil {
		return err
	}
	transport := webhookgatewayhost.BudgetTransport{Limiter: store}
	receiver, err := githubapp.New(githubapp.Config{
		AppID: policy.Policy.ReceivingAppID, PrivateKeyPEM: receiverKey, Transport: transport, RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		return errors.New("gateway receiving identity is unavailable")
	}
	dispatcher, err := githubapp.New(githubapp.Config{
		AppID: policy.Policy.DispatchAppID, PrivateKeyPEM: dispatchKey, Transport: transport, RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		return errors.New("gateway dispatch identity is unavailable")
	}
	if err := receiver.ValidateWebhookGatewayRole(ctx, "receive"); err != nil {
		return err
	}
	if err := dispatcher.ValidateWebhookGatewayRole(ctx, "dispatch"); err != nil {
		return err
	}
	quota, err := githubquota.New(store, githubquota.Options{SafetyReserve: 100})
	if err != nil {
		return err
	}
	authority := webhookgatewayhost.LiveAuthority{Loader: loader, Queue: queue, Receiving: receiver, Dispatch: dispatcher, State: store, Quota: quota}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	if policy.Policy.Discovery != nil {
		registry, ok := queue.(gateway.RegistryStore)
		if !ok {
			return gateway.ErrUnavailable
		}
		discovery := &webhookgatewayhost.DiscoveryWorker{Policies: baseLoader, Registry: registry, GitHub: receiver, Quota: quota}
		workers.Go(func() { _ = discovery.Run(ctx) })
	}
	workers.Go(func() {
		primer := webhookgatewayhost.AuthorityRefresher{Authority: authority}
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			refreshCtx, finish := context.WithTimeout(ctx, 20*time.Second)
			if err := primer.Refresh(refreshCtx); err != nil && ctx.Err() == nil {
				authorityLog.Printf("authorization proof refresh incomplete")
			}
			finish()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	hostname, err := os.Hostname()
	if err != nil {
		cancel()
		workers.Wait()
		return errors.New("gateway worker identity is unavailable")
	}
	for i := range policy.Policy.Workers {
		gate := &webhookgatewayhost.QuotaGate{Service: quota, Reader: dispatcher, AppID: policy.Policy.DispatchAppID,
			Dispatcher: githubapp.WebhookGatewayDispatcher{Client: dispatcher, Receiving: receiver,
				ObserveReceivingQuota: func(ctx context.Context, installation int64, response githubquota.ResponseQuota) error {
					return webhookgatewayhost.ObserveQuota(ctx, quota, strconv.FormatInt(policy.Policy.ReceivingAppID, 10), installation, response)
				},
				ObserveQuota: func(ctx context.Context, installation int64, response githubquota.ResponseQuota) error {
					return webhookgatewayhost.ObserveQuota(ctx, quota, strconv.FormatInt(policy.Policy.DispatchAppID, 10), installation, response)
				}},
		}
		// Opaque bounded worker identity; hostnames never enter metric labels.
		owner := gateway.MetadataDigest([]byte(strings.Join([]string{hostname, strconv.Itoa(os.Getpid()), strconv.Itoa(i)}, ":")))[:32]
		worker := &gateway.Worker{Policies: loader, Queue: queue, Authorizer: authority, Dispatcher: gate, Budget: gate,
			LabelConsumer: webhookgatewayhost.LabelConsumer{Authority: authority, GitHub: receiver,
				Deliveries: store, Receipts: store, Quota: quota, ReceivingApp: policy.Policy.ReceivingAppID},
			Owner: owner, HTTPTimeout: 20 * time.Second, LeaseDuration: time.Minute, BatchWindow: gateway.DispatchBatchWindow}
		workers.Go(func() { _ = worker.Run(ctx) })
	}
	<-ctx.Done()
	workers.Wait()
	return ctx.Err()
}
