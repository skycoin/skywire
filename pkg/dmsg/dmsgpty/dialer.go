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
// Phase 1 (#2671): NewHost wires the default dmsg adapter, behavior
// is unchanged. Phase 2 (this PR): MultiDialer composes strategies,
// visor init goes through NewHostWithDialer with a single-strategy
// chain (still dmsg-only). Phase 3 (next): a transport-aware adapter
// backed by SkywireNetworker / transport.Manager prepends to the
// chain so `cli dmsg pty start` rides the visor's already-negotiated
// transports first and falls through to dmsg only on miss.
//
// The listening side (Host.ListenAndServe → h.dmsgC.Listen) and the
// logger plumbing stay on *dmsg.Client; only the proxy-out dial is
// pluggable. dmsgpty-hosts are still reached over dmsg by remote
// peers — only this host's outgoing proxy gets the transport choice.
package dmsgpty

import (
	"context"
	"errors"
	"fmt"
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

// NewDmsgDialer constructs the dmsg-backed StreamDialer used by
// NewHost's default wiring and by callers that want to compose it
// into a MultiDialer alongside non-dmsg strategies. Exposed (vs
// keeping dmsgDialer private) so the visor's init_dmsg.go can build
// the same wrapping that NewHost uses internally — both call sites
// produce identical wire behavior.
func NewDmsgDialer(c *dmsg.Client) StreamDialer {
	return dmsgDialer{c: c}
}

// DialStream forwards to the underlying dmsg.Client. Pre-refactor
// behavior is preserved exactly: same address shape, same context
// semantics, same error surface.
func (d dmsgDialer) DialStream(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	return d.c.DialStream(ctx, dmsg.Addr{PK: pk, Port: port})
}

// MultiDialer tries each StreamDialer in order and returns the first
// success. Errors are accumulated; if every strategy fails, the
// joined error surfaces so operators can see which transports were
// attempted. Empty list errors immediately rather than panicking.
//
// Strategy order is caller-controlled — typically transport-aware
// adapters first (skynet routes / stcpr) with a dmsg fallback last
// so working dmsg behavior survives a misconfigured upstream
// strategy. Each strategy gets the full ctx; a strategy that hangs
// will block fallback until ctx fires, so callers should bound ctx
// per overall attempt, not per strategy.
type MultiDialer []StreamDialer

// DialStream walks the strategies in order. Returns the first conn
// to succeed; otherwise joins every strategy's error so the caller
// gets the full attempt list for diagnostics.
func (m MultiDialer) DialStream(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	if len(m) == 0 {
		return nil, fmt.Errorf("dmsgpty: MultiDialer has no strategies")
	}
	var errs []error
	for i, d := range m {
		conn, err := d.DialStream(ctx, pk, port)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Errorf("strategy %d: %w", i, err))
	}
	return nil, errors.Join(errs...)
}
