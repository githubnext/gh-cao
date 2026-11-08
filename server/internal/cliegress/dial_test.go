package cliegress

import (
	"context"
	"net"
	"testing"
)

func closedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestDialFirstFallsBackToLaterAddress(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	dial := (&net.Dialer{}).DialContext
	conn, err := dialFirst(context.Background(), dial, []string{closedAddress(t), listener.Addr().String()})
	if err != nil {
		t.Fatalf("dialFirst error = %v", err)
	}
	if got := conn.RemoteAddr().String(); got != listener.Addr().String() {
		t.Fatalf("connected to %s", got)
	}
	_ = conn.Close()
}

func TestDialFirstReturnsErrorWhenAllFail(t *testing.T) {
	dial := (&net.Dialer{}).DialContext
	conn, err := dialFirst(context.Background(), dial, []string{closedAddress(t), closedAddress(t)})
	if conn != nil || err == nil {
		t.Fatalf("conn=%v err=%v", conn, err)
	}
}

func TestDialFirstStopsWhenContextEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dial := (&net.Dialer{}).DialContext
	conn, err := dialFirst(ctx, dial, []string{closedAddress(t), closedAddress(t)})
	if conn != nil || err == nil {
		t.Fatalf("conn=%v err=%v", conn, err)
	}
}

func TestDialFirstWithNoAddresses(t *testing.T) {
	conn, err := dialFirst(context.Background(), (&net.Dialer{}).DialContext, nil)
	if conn != nil || err != nil {
		t.Fatalf("conn=%v err=%v", conn, err)
	}
}
