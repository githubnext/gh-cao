package simulator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

const setupAzuriteRelayScript = `
const socket = require("node:net").connect(Number(process.argv[1]), "127.0.0.1");
socket.on("error", () => process.exit(1));
socket.on("close", () => process.stdin.destroy());
process.stdin.pipe(socket);
socket.pipe(process.stdout);
`

func startSetupAzuriteRelay(t *testing.T, ctx context.Context, command func(context.Context) *exec.Cmd) string {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0") // #nosec G102 -- numeric loopback only.
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	var workers sync.WaitGroup
	workers.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Go(func() {
				defer func() { _ = connection.Close() }()
				stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
				defer stop()
				process := command(ctx)
				process.Stdin = connection
				process.WaitDelay = time.Second
				output, err := process.StdoutPipe()
				if err != nil {
					t.Error(err)
					return
				}
				if err := process.Start(); err != nil {
					if ctx.Err() == nil {
						t.Logf("Azurite loopback relay could not start: %v", err)
					}
					return
				}
				_, _ = io.Copy(connection, output)
				_ = connection.Close()
				if err := process.Wait(); err != nil && ctx.Err() == nil {
					t.Logf("Azurite loopback relay exited: %v", err)
				}
			})
		}
	})
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		workers.Wait()
		stop()
	})
	return listener.Addr().String()
}

func TestSetupAzureContainerRelay(t *testing.T) {
	if os.Getenv("CAO_SETUP_AZURE_INTEGRATION") != "1" {
		t.Skip("run npm run test:integration:setup-azure to exercise the container-loopback relay")
	}
	body := bytes.Repeat([]byte{0, '\r', '\n', 0xff}, 4096)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPut || !bytes.Equal(content, body) {
			t.Error("relay changed the upstream request")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(content)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	address := startSetupAzuriteRelay(t, ctx, func(ctx context.Context) *exec.Cmd {
		command := exec.CommandContext(ctx, "node", "-e", setupAzuriteRelayScript, target.Port()) // #nosec G204 -- fixed relay script and test-owned loopback port.
		command.Env = setupAzureToolEnvironment("PATH")
		return command
	})
	transport, err := NewLocalTransport("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	store := &azureBlobStore{endpoint: "http://" + address, key: []byte("test-only-key"), client: &http.Client{Transport: transport, Timeout: 5 * time.Second}}
	for range 3 {
		content, err := store.request(ctx, http.MethodPut, "/setup/blob", body)
		if err != nil || !bytes.Equal(content, body) {
			t.Fatalf("loopback relay round trip: %v", err)
		}
	}
	upstream.Close()
	transport.CloseIdleConnections()
	if _, err := store.request(ctx, http.MethodPut, "/setup/blob", body); err == nil {
		t.Fatal("relay accepted a request with no container listener")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("relay did not close its connection when the container command failed")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	cancel()
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var timeout net.Error
	if _, err := connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("cancellation left a relay connection open")
	} else if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("cancellation did not promptly close the relay connection")
	}
}
