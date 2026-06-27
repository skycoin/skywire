// Package wasmrpc bridges a browser wasm-visor's net/rpc server to local
// `skywire cli` clients.
//
// A wasm-visor runs in a browser tab and cannot open a TCP listener, so the
// connection is inverted: the tab dials the host over a single WebSocket, both
// ends wrap that one connection as a yamux session, and the HOST opens a fresh
// yamux stream per local `skywire cli` connection. The tab accepts each stream
// into its rpc.Server.ServeConn. So:
//
//	skywire --rpc 127.0.0.1:<port> visor info   # drives one specific tab
//
// and N tabs => N bridges on N ephemeral ports — driving multiple wasm-visors
// with the ordinary CLI. The host end is Bridge (this file); the tab end is
// ServeTab (tab.go), which the wasm-visor calls.
package wasmrpc

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
)

// Bridge fronts ONE tab: a yamux session over the tab's WebSocket connection,
// plus a local TCP listener whose every accepted connection is piped to a fresh
// yamux stream (the tab serves net/rpc over it).
type Bridge struct {
	session *yamux.Session
	lis     net.Listener
	log     func(string)
	once    sync.Once
}

// NewBridge wraps tabConn (the host side of the tab's WebSocket, as a net.Conn)
// in a yamux SERVER and opens a local TCP listener on 127.0.0.1:<port> (port 0 =
// an OS-assigned ephemeral port). Accepted cli connections are each piped to a
// new yamux stream. Close tears it all down.
func NewBridge(tabConn net.Conn, port int, log func(string)) (*Bridge, error) {
	sess, err := yamux.Server(tabConn, yamux.DefaultConfig())
	if err != nil {
		return nil, fmt.Errorf("wasmrpc: yamux server: %w", err)
	}
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		_ = sess.Close() //nolint:errcheck
		return nil, fmt.Errorf("wasmrpc: rpc listen: %w", err)
	}
	b := &Bridge{session: sess, lis: lis, log: log}
	go b.acceptLoop()
	return b, nil
}

// Addr is the local address `skywire cli --rpc` should target for this tab.
func (b *Bridge) Addr() string { return b.lis.Addr().String() }

func (b *Bridge) logf(format string, args ...interface{}) {
	if b.log != nil {
		b.log(fmt.Sprintf(format, args...))
	}
}

func (b *Bridge) acceptLoop() {
	for {
		cliConn, err := b.lis.Accept()
		if err != nil {
			return // listener closed
		}
		go b.pipe(cliConn)
	}
}

// pipe shuttles one cli connection to a new yamux stream and back. A yamux
// stream is a clean per-RPC-connection channel to the tab, so the tab's
// rpc.Server.ServeConn sees exactly one client per stream.
func (b *Bridge) pipe(cliConn net.Conn) {
	defer cliConn.Close() //nolint:errcheck
	stream, err := b.session.Open()
	if err != nil {
		b.logf("open stream: %v", err)
		return
	}
	defer stream.Close() //nolint:errcheck
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(stream, cliConn); done <- struct{}{} }() //nolint:errcheck
	go func() { _, _ = io.Copy(cliConn, stream); done <- struct{}{} }() //nolint:errcheck
	<-done                                                              // one side closed → tear down the other via the defers
}

// Close shuts the listener and the yamux session (idempotent).
func (b *Bridge) Close() error {
	var err error
	b.once.Do(func() {
		err = b.lis.Close()
		_ = b.session.Close() //nolint:errcheck
	})
	return err
}
