package dmsgsrv

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// silentConn is a setup stream whose peer never sends a request.
type silentConn struct {
	net.Conn
	remote dmsg.Addr
}

func (c silentConn) RemoteAddr() net.Addr { return c.remote }

// floodListener hands out silent setup streams as fast as they are accepted.
type floodListener struct {
	accepted atomic.Int64
	mu       sync.Mutex
	peers    []net.Conn
	done     chan struct{}
}

func (l *floodListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	default:
	}
	l.accepted.Add(1)
	srv, cli := net.Pipe()
	l.mu.Lock()
	l.peers = append(l.peers, cli)
	l.mu.Unlock()
	pk, _ := cipher.GenerateKeyPair()
	return silentConn{Conn: srv, remote: dmsg.Addr{PK: pk, Port: 36}}, nil
}

func (l *floodListener) Close() error   { return nil }
func (l *floodListener) Addr() net.Addr { return dmsg.Addr{} }

// TestServeSetupConnsIsBounded floods the setup port with streams that never
// send a request. The slot must be taken before a stream is accepted, so the
// accepted streams stay at the cap instead of every one parking a goroutine.
func TestServeSetupConnsIsBounded(t *testing.T) {
	lis := &floodListener{done: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveSetupConns(ctx, lis, nil, testLog())
	}()

	time.Sleep(300 * time.Millisecond)
	if n := lis.accepted.Load(); n > maxConcurrentSetups+1 {
		t.Errorf("accepted %d silent setup streams, want at most %d", n, maxConcurrentSetups+1)
	}

	cancel()
	close(lis.done)
	lis.mu.Lock()
	for _, c := range lis.peers {
		c.Close() //nolint:errcheck,gosec
	}
	lis.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serveSetupConns did not return")
	}
}
