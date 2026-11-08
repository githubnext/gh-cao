package cliegress

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var egressLog = logger.New("cao:cliegress")

const (
	MaxConnections = 32
	IdleTimeout    = 30 * time.Second
	MaxLifetime    = 15 * time.Minute
)

// Serve owns a private Unix listener until cancellation or Close. Only static
// errors are returned to clients; authorities, headers and tunnel bytes are
// never logged. Even hijacked tunnels are closed when the command is stopped.
func Serve(ctx context.Context, listener net.Listener, policy *Policy) func() {
	ctx, cancel := context.WithTimeout(ctx, MaxLifetime)
	p := &proxy{
		policy: policy, lookup: net.DefaultResolver.LookupNetIP,
		dial:  (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		slots: make(chan struct{}, MaxConnections),
	}
	server := &http.Server{
		Handler: p, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: IdleTimeout, MaxHeaderBytes: 8192,
		ErrorLog:    log.New(safeLogWriter{}, "", 0),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	var once sync.Once
	closeProxy := func() {
		once.Do(func() {
			cancel()
			_ = server.Close()
			_ = listener.Close()
		})
	}
	stop := context.AfterFunc(ctx, closeProxy)
	go func() {
		if err := server.Serve(&boundedListener{Listener: listener, slots: make(chan struct{}, MaxConnections)}); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			_, _ = safeLogWriter{}.Write(nil)
		}
		closeProxy()
	}()
	return func() { stop(); closeProxy() }
}

type safeLogWriter struct{}

func (safeLogWriter) Write(p []byte) (int, error) {
	_, err := io.WriteString(os.Stderr, "hosted CLI egress transport failed\n")
	return len(p), err
}

type proxy struct {
	policy *Policy
	lookup lookupFunc
	dial   func(context.Context, string, string) (net.Conn, error)
	slots  chan struct{}
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect || r.URL == nil || r.URL.Host != r.Host ||
		r.URL.User != nil || r.URL.RawQuery != "" || r.URL.Path != "" ||
		r.URL.Scheme != "" || r.URL.Opaque != "" || r.URL.Fragment != "" ||
		r.ContentLength > 0 || len(r.TransferEncoding) != 0 {
		http.Error(w, "HTTPS CONNECT required", http.StatusForbidden)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		egressLog.Printf("tunnel rejected: reason=capacity limit=%d", MaxConnections)
		http.Error(w, "egress capacity unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), MaxLifetime)
	defer cancel()
	resolveCtx, resolveCancel := context.WithTimeout(ctx, 10*time.Second)
	addresses, err := p.policy.destination(resolveCtx, r.Host, p.lookup)
	resolveCancel()
	if err != nil {
		egressLog.Printf("tunnel rejected: reason=destination_denied")
		http.Error(w, "egress destination denied or unavailable", http.StatusForbidden)
		return
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	upstream, err := dialFirst(dialCtx, p.dial, addresses)
	dialCancel()
	if err != nil {
		egressLog.Printf("tunnel rejected: reason=dial_failed addresses=%d", len(addresses))
		http.Error(w, "egress destination unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "egress tunnel unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = client.Close(); _ = upstream.Close() })
	defer stop()
	_ = client.SetDeadline(time.Now().Add(IdleTimeout))
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}
	deadline, _ := ctx.Deadline()
	left := &idleConn{Conn: client, deadline: deadline}
	right := &idleConn{Conn: upstream, deadline: deadline}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(right, io.MultiReader(io.LimitReader(buffered, int64(buffered.Reader.Buffered())), left))
		_ = upstream.Close()
		_ = client.Close()
		close(done)
	}()
	_, _ = io.Copy(left, right)
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

// dialFirst returns the first successful connection from addresses, stopping
// early once ctx ends. The last dial error is returned when every attempt fails.
func dialFirst(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), addresses []string) (net.Conn, error) {
	var conn net.Conn
	var err error
	for _, address := range addresses {
		conn, err = dial(ctx, "tcp", address)
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	return conn, err
}

type boundedListener struct {
	net.Listener
	slots chan struct{}
}

func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &boundedConn{Conn: conn, release: func() { <-l.slots }}, nil
		default:
			_ = conn.Close()
		}
	}
}

type boundedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

type idleConn struct {
	net.Conn
	deadline time.Time
}

func (c *idleConn) nextDeadline() time.Time {
	next := time.Now().Add(IdleTimeout)
	if !c.deadline.IsZero() && c.deadline.Before(next) {
		return c.deadline
	}
	return next
}

func (c *idleConn) Read(p []byte) (int, error) {
	_ = c.SetReadDeadline(c.nextDeadline())
	return c.Conn.Read(p)
}

func (c *idleConn) Write(p []byte) (int, error) {
	_ = c.SetWriteDeadline(c.nextDeadline())
	return c.Conn.Write(p)
}
