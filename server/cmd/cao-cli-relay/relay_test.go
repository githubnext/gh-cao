package main

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestStartRelayForwardsToSocket(t *testing.T) {
	socket := shortSocketPath(t)
	upstream, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upstream.Close() }()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(conn, conn)
	}()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listener, err := startRelay(ctx, cancel, "127.0.0.1:0", socket)
	if err != nil {
		t.Fatalf("startRelay: %v", err)
	}
	defer func() { _ = listener.Close() }()

	client, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(client, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo = %q, %v; want ping", got, err)
	}
	if ctx.Err() != nil {
		t.Fatal("healthy relay cancelled the workload context")
	}
}

func TestStartRelayRejectsOccupiedAddress(t *testing.T) {
	occupied, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listener, err := startRelay(ctx, cancel, occupied.Addr().String(), shortSocketPath(t))
	if err == nil {
		_ = listener.Close()
		t.Fatal("expected listen failure on occupied address")
	}
	if listener != nil {
		t.Fatalf("listener = %v on failure, want nil", listener)
	}
	if ctx.Err() != nil {
		t.Fatal("listen failure must not cancel the context; run reports it directly")
	}
}

func TestStartRelayCancelsWorkloadWhenRelayFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listener, err := startRelay(ctx, cancel, "127.0.0.1:0", shortSocketPath(t))
	if err != nil {
		t.Fatalf("startRelay: %v", err)
	}
	// Closing the listener makes Accept fail while the context is still live.
	_ = listener.Close()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("unexpected relay failure did not cancel the workload context")
	}
}

func TestStartRelayStopsQuietlyOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	listener, err := startRelay(ctx, func() {}, "127.0.0.1:0", shortSocketPath(t))
	if err != nil {
		t.Fatalf("startRelay: %v", err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(t.Context(), "tcp4", listener.Addr().String())
		if err != nil {
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting after cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
