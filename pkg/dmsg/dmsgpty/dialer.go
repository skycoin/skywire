// Package dmsgpty pkg/dmsg/dmsgpty/dialer.go
//
// StreamDialer abstracts the outbound dial that Host uses to proxy
// CLI requests to a remote dmsgpty endpoint. Pre-refactor, Host
// reached into its bound *dmsg.Client directly (h.dmsgC.DialStream)
// at every call site — pinning the entire dmsgpty stack to dmsg as
// the only outbound transport, even though the wire-level PtyClient
// only needs an io.ReadWriteCloser. This file introduces a small
// interface around just the outbound dial so a transport-aware
// adapter can be swapped in without touching the rest of Host.
//
// Phase 1 (this PR) is abstraction-only: NewHost wires the default
// dmsg adapter, behavior is unchanged, every existing caller keeps
// working. Phase 2 (separate PR) will wire a TransportStreamDialer
// adapter backed by transport.Manager so `cli dmsg pty start` rides
// the visor's already-negotiated transports (skynet routes, stcpr,
// etc.) instead of always opening a fresh dmsg stream.
//
// The listening side (Host.ListenAndServe → h.dmsgC.Listen) and the
// logger plumbing stay on *dmsg.Client; only the proxy-out dial is
// pluggable. dmsgpty-hosts are still reached over dmsg by remote
// peers — only this host's outgoing proxy gets the transport choice.
package dmsgpty

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// StreamDialer is the outbound-dial side of dmsgpty.Host. Implementations
// open a bidirectional stream to (pk, port) on whatever transport they're
// backed by. The returned conn carries the dmsgpty wire protocol that
// NewPtyClient consumes, so any net.Conn-shaped transport (dmsg.Stream,
// skynet RouteGroup, raw TCP, …) is a valid backing.
type StreamDialer interface {
	// DialStream opens an outbound stream to pk:port. The returned conn
	// is owned by the caller — close on cleanup. ctx bounds dial-side
	// work only; in-flight reads/writes on the returned conn are
	// independent of ctx cancellation.
	DialStream(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error)
}

// dmsgDialer is the default StreamDialer used when NewHost wires a
// host without an explicit dialer override. Bridges to dmsg.Client.
// DialStream so existing call sites keep the exact wire+timing
// behavior they had before the refactor.
type dmsgDialer struct {
	c *dmsg.Client
}

// DialStream forwards to the underlying dmsg.Client. Pre-refactor
// behavior is preserved exactly: same address shape, same context
// semantics, same error surface.
func (d dmsgDialer) DialStream(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	return d.c.DialStream(ctx, dmsg.Addr{PK: pk, Port: port})
}
