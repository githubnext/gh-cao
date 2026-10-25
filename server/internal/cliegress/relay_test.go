package cliegress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRelayUnixTransportAndCancellation(t *testing.T) {
	directory, err := os.MkdirTemp("", "ce-relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	socket := filepath.Join(directory, "proxy.sock")
	unixListener, err := (&net.ListenConfig{}).Listen(ctx, "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ParsePolicy([]byte(`{"hosts":["api.github.com"]}`))
	if err != nil {
		t.Fatal(err)
	}
	closeProxy := Serve(ctx, unixListener, policy)
	defer closeProxy()
	tcpListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tcpListener.Close() }()
	done := make(chan error, 1)
	go func() { done <- Relay(ctx, tcpListener, socket) }()
	client, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", tcpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprint(client, "CONNECT denied.example:443 HTTP/1.1\r\nHost: denied.example:443\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("Unix relay did not reach the command's policy")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relay shutdown failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay outlived cancellation")
	}
}

func TestBoundedListenerRejectsExtraConnections(t *testing.T) {
	ctx := t.Context()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	bounded := &boundedListener{Listener: listener, slots: make(chan struct{}, 1)}
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := bounded.Accept()
		accepted <- conn
		_, _ = bounded.Accept()
	}()
	first, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	conn := <-accepted
	defer func() { _ = conn.Close() }()
	extra, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = extra.Close() }()
	_ = extra.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := extra.Read(make([]byte, 1)); err == nil || n != 0 {
		t.Fatal("extra accepted connection was not closed")
	} else {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatal("connection was left open until the test deadline")
		}
	}
}

func TestRelayDeadline(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := relayDeadline(t.Context(), now); !got.Equal(now.Add(MaxLifetime)) {
		t.Fatalf("no context deadline: got %v", got)
	}
	sooner := now.Add(time.Minute)
	soonCtx, cancel := context.WithDeadline(t.Context(), sooner)
	defer cancel()
	if got := relayDeadline(soonCtx, now); !got.Equal(sooner) {
		t.Fatalf("sooner context deadline: got %v", got)
	}
	later := now.Add(2 * MaxLifetime)
	lateCtx, cancelLate := context.WithDeadline(t.Context(), later)
	defer cancelLate()
	if got := relayDeadline(lateCtx, now); !got.Equal(now.Add(MaxLifetime)) {
		t.Fatalf("later context deadline: got %v", got)
	}
}

func TestRelayReturnsErrorWhenListenerFails(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if err := Relay(t.Context(), listener, "unused.sock"); err == nil {
		t.Fatal("expected accept error from closed listener")
	}
}

func TestRelayCancellationIsClean(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Relay(ctx, listener, "unused.sock"); err != nil {
		t.Fatalf("cancelled relay should return nil, got %v", err)
	}
}
