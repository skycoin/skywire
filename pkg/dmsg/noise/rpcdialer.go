//go:build !tinygo

// Package noise pkg/noise/rpcdialer.go
//
// RPCClientDialer is split out of net.go behind //go:build !tinygo because it
// imports net/rpc, which transitively pulls net/http — broken on the TinyGo js
// target. It is a server-side redial helper (a visor exposing an RPC server to a
// remote), never used by a dmsg client, so excluding it from TinyGo builds costs
// nothing. See docs/design/tinygo-dmsg-client.md.
package noise

import (
	"net"
	"net/rpc"
	"sync"
	"time"

	"github.com/flynn/noise"
)

// RPCClientDialer attempts to redial to a remotely served RPCClient.
// It exposes an RPCServer to the remote server.
// The connection is encrypted via noise.
type RPCClientDialer struct {
	config  Config
	pattern noise.HandshakePattern
	addr    string
	conn    net.Conn
	mu      sync.Mutex
	done    chan struct{} // nil: loop is not running, non-nil: loop is running.
}

// NewRPCClientDialer creates a new RPCClientDialer.
func NewRPCClientDialer(addr string, pattern noise.HandshakePattern, config Config) *RPCClientDialer {
	return &RPCClientDialer{config: config, pattern: pattern, addr: addr}
}

// Run repeatedly dials to remote until a successful connection is established.
// It exposes a RPC Server.
// It will return if Close is called or crypto fails.
func (d *RPCClientDialer) Run(srv *rpc.Server, retry time.Duration) error {
	if ok := d.setDone(); !ok {
		return ErrAlreadyServing
	}
	for {
		if err := d.establishConn(); err != nil {
			// Only return if not network error.
			if _, ok := err.(net.Error); !ok {
				return err
			}
		} else {
			// Only serve when then dial succeeds.
			srv.ServeConn(d.conn)
			d.setConn(nil)
		}
		select {
		case <-d.done:
			d.clearDone()
			return nil
		case <-time.After(retry):
		}
	}
}

// Close closes the handler.
func (d *RPCClientDialer) Close() (err error) {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	if d.done != nil {
		close(d.done)
	}
	if d.conn != nil {
		err = d.conn.Close()
	}
	d.mu.Unlock()
	return
}

// This operation should be atomic, hence protected by mutex.
func (d *RPCClientDialer) establishConn() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	conn, err := net.Dial("tcp", d.addr)
	if err != nil {
		return err
	}
	ns, err := New(d.pattern, d.config)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return err
	}
	wrappedConn, err := WrapConn(conn, ns, time.Second*5)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return err
	}
	d.conn = wrappedConn
	return nil
}

func (d *RPCClientDialer) setConn(conn net.Conn) {
	d.mu.Lock()
	d.conn = conn
	d.mu.Unlock()
}

func (d *RPCClientDialer) setDone() (ok bool) {
	d.mu.Lock()
	if ok = d.done == nil; ok {
		d.done = make(chan struct{})
	}
	d.mu.Unlock()
	return
}

func (d *RPCClientDialer) clearDone() {
	d.mu.Lock()
	d.done = nil
	d.mu.Unlock()
}
