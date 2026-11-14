// Package githubwebhook defines the pluggable service that fans verified GitHub
// webhook deliveries out to registered listeners.
//
// The package is transport independent: an ingress adapter (such as the
// dashboard server's HTTP route) authenticates the delivery and then calls
// Service.Dispatch. Listeners never see unverified input, and registering a
// listener grants it no credentials, policy, or write authority.
package githubwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Delivery is one authenticated GitHub webhook delivery.
type Delivery struct {
	Delivery string
	Event    string
	Payload  json.RawMessage
}

// Outcome is a listener's acknowledgement of one delivery.
//
// Fields are merged into the ingress acknowledgement. Duplicate reports that
// the listener had already processed this delivery identifier.
type Outcome struct {
	Duplicate bool
	Fields    map[string]any
}

// Listener receives authenticated deliveries. Implementations must be
// idempotent by Delivery.Delivery: a delivery is redelivered whenever any
// registered listener fails it, so successful listeners can observe it again.
type Listener interface {
	HandleGitHubWebhook(context.Context, Delivery) (Outcome, error)
}

// ListenerFunc adapts a function to Listener.
type ListenerFunc func(context.Context, Delivery) (Outcome, error)

// HandleGitHubWebhook calls f.
func (f ListenerFunc) HandleGitHubWebhook(ctx context.Context, delivery Delivery) (Outcome, error) {
	return f(ctx, delivery)
}

// Registration is the handle returned by Register.
type Registration interface {
	// Unregister removes this registration. It is idempotent and never removes
	// a different listener later registered under the same name.
	Unregister()
}

// Service is the pluggable GitHub webhook service contract. Alternative
// implementations (for example a queue-backed or remote fan-out) can replace
// the in-process Registry without changing ingress adapters or listeners.
type Service interface {
	// Register adds a named listener. Names are unique; registering a name that
	// is already present fails with ErrDuplicateListener.
	Register(name string, listener Listener) (Registration, error)
	// Unregister removes the named listener and reports whether it existed.
	Unregister(name string) bool
	// Listeners returns registered listener names in dispatch order.
	Listeners() []string
	// Dispatch delivers to every registered listener. It fails with
	// ErrNoListeners when none is registered.
	Dispatch(context.Context, Delivery) (Outcome, error)
}

var (
	ErrNoListeners       = errors.New("github webhook: no listeners are registered")
	ErrDuplicateListener = errors.New("github webhook: listener name is already registered")
	ErrInvalidListener   = errors.New("github webhook: listener name and implementation are required")
)

// ListenerError attributes a dispatch failure to one listener.
type ListenerError struct {
	Name string
	Err  error
}

func (e *ListenerError) Error() string {
	return fmt.Sprintf("github webhook listener %q: %v", e.Name, e.Err)
}

func (e *ListenerError) Unwrap() error { return e.Err }

// Registry is the default in-process Service. Listeners run sequentially in
// registration order against a snapshot taken when Dispatch starts, so
// concurrent Register/Unregister calls affect only later deliveries.
type Registry struct {
	mu        sync.RWMutex
	next      uint64
	listeners []registered
}

type registered struct {
	id       uint64
	name     string
	listener Listener
}

var _ Service = (*Registry)(nil)

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(name string, listener Listener) (Registration, error) {
	name = strings.TrimSpace(name)
	if name == "" || listener == nil {
		return nil, ErrInvalidListener
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.listeners {
		if existing.name == name {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateListener, name)
		}
	}
	r.next++
	r.listeners = append(r.listeners, registered{id: r.next, name: name, listener: listener})
	return &registration{registry: r, id: r.next}, nil
}

func (r *Registry) Unregister(name string) bool {
	return r.remove(func(entry registered) bool { return entry.name == name })
}

func (r *Registry) Listeners() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.listeners))
	for i, entry := range r.listeners {
		names[i] = entry.name
	}
	return names
}

// Dispatch delivers to every listener even when an earlier one fails, so each
// listener observes the delivery at least once; failures are joined. The
// merged Outcome is a duplicate only when every listener reports one, and
// earlier listeners win field-name collisions.
func (r *Registry) Dispatch(ctx context.Context, delivery Delivery) (Outcome, error) {
	r.mu.RLock()
	snapshot := append([]registered(nil), r.listeners...)
	r.mu.RUnlock()
	if len(snapshot) == 0 {
		return Outcome{}, ErrNoListeners
	}
	merged := Outcome{Duplicate: true}
	var failures []error
	for _, entry := range snapshot {
		outcome, err := entry.listener.HandleGitHubWebhook(ctx, delivery)
		if err != nil {
			failures = append(failures, &ListenerError{Name: entry.name, Err: err})
			continue
		}
		merged.Duplicate = merged.Duplicate && outcome.Duplicate
		for key, value := range outcome.Fields {
			if merged.Fields == nil {
				merged.Fields = make(map[string]any, len(outcome.Fields))
			}
			if _, exists := merged.Fields[key]; !exists {
				merged.Fields[key] = value
			}
		}
	}
	if len(failures) > 0 {
		return Outcome{}, errors.Join(failures...)
	}
	if _, reported := merged.Fields["duplicate"]; reported {
		merged.Fields["duplicate"] = merged.Duplicate
	}
	return merged, nil
}

func (r *Registry) remove(match func(registered) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, entry := range r.listeners {
		if match(entry) {
			r.listeners = append(r.listeners[:i:i], r.listeners[i+1:]...)
			return true
		}
	}
	return false
}

type registration struct {
	registry *Registry
	id       uint64
	once     sync.Once
}

func (r *registration) Unregister() {
	r.once.Do(func() {
		r.registry.remove(func(entry registered) bool { return entry.id == r.id })
	})
}
