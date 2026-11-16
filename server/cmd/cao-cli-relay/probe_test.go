package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortSocketPath avoids the platform's unix socket path length limit that long
// t.TempDir paths can exceed.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "relay")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "p.sock")
}

func TestProbeEgressSocketAcceptsListener(t *testing.T) {
	socket := shortSocketPath(t)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan struct{})
	go func() {
		if conn, err := listener.Accept(); err == nil {
			_ = conn.Close()
		}
		close(accepted)
	}()

	if err := probeEgressSocket(t.Context(), socket); err != nil {
		t.Fatalf("probe of live socket: %v", err)
	}
	<-accepted
}

func TestProbeEgressSocketMissingSocket(t *testing.T) {
	if err := probeEgressSocket(t.Context(), shortSocketPath(t)); err == nil {
		t.Fatal("expected an error for a socket that does not exist")
	}
}

func TestProbeEgressSocketNotASocket(t *testing.T) {
	path := shortSocketPath(t)
	if err := os.WriteFile(path, []byte("regular file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := probeEgressSocket(t.Context(), path); err == nil {
		t.Fatal("expected an error for a regular file")
	}
}

func TestProbeEgressSocketCancelledContext(t *testing.T) {
	socket := shortSocketPath(t)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := probeEgressSocket(ctx, socket); err == nil {
		t.Fatal("expected an error when the context is already cancelled")
	}
}
