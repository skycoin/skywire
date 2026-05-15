// Package appnet pkg/app/appnet/packet.go: UDP / datagram app API.
// Stage 5 of #2607.
//
// Apps that want UDP semantics over skywire (faithful loss, no
// head-of-line blocking on reorder, per-datagram AEAD) call
// DialPacket / DialPacketContext to get a DatagramPacketConn. The
// returned conn satisfies net.PacketConn plus a MaxPayload()
// method so callers can avoid the opaque-EMSGSIZE footgun that
// plain net.UDPConn presents.
//
// Networker implementations OPT INTO datagram support by also
// implementing PacketNetworker. The narrow opt-in interface
// (separate from the existing Networker contract) lets
// implementations that don't yet support datagrams stay valid —
// callers get ErrPacketNetworkerNotSupported on DialPacket.
//
// Stage 5 scope: API surface + the top-level DialPacket /
// DialPacketContext entry points + a stub that surfaces
// "implementation in progress" until the route-setup integration
// lands. The route-setup wiring (which constructs the per-route
// DatagramRouteGroup from a Noise handshake, installs ciphers via
// SetCiphers, and registers Handle dispatch with the router) is a
// follow-up because it touches a significant amount of the
// existing DialRoutes machinery and is worth its own focused PR.

package appnet

import (
	"context"
	"errors"
	"net"
)

// ErrPacketNetworkerNotSupported is returned by DialPacket when
// the resolved Networker for addr.Net does not implement
// PacketNetworker. Stage 5 ships the API; implementations land in
// the route-setup-integration follow-up.
var ErrPacketNetworkerNotSupported = errors.New("appnet: networker does not support packet dialing")

// DatagramPacketConn extends net.PacketConn with the operations
// apps need to use UDP-over-skywire without surprises:
//
//   - MaxPayload returns the largest plaintext datagram WriteTo
//     will accept. App code that consults this before sending
//     never sees the opaque EMSGSIZE behavior plain net.UDPConn
//     presents (where the OS-level MTU is invisible until a send
//     fails). Reflects the Stage 3 AEAD overhead — 24 bytes
//     subtracted from the raw wire cap.
//
//   - InboundDropped surfaces the count of inbound datagrams the
//     conn dropped because its receive queue was full (slow-
//     consumer signal). Apps that care about packet loss can
//     poll this to distinguish "lost in transit" from "delivered
//     but dropped at our end."
//
//   - AEADAuthFailures surfaces the count of inbound datagrams
//     that failed authentication (forgery / replay / corrupted).
//     A rising counter under normal traffic is an attack signal.
type DatagramPacketConn interface {
	net.PacketConn

	// MaxPayload is the largest plaintext datagram WriteTo accepts.
	MaxPayload() int

	// InboundDropped is the count of inbound datagrams dropped
	// since conn construction due to a full receive queue.
	InboundDropped() uint64

	// AEADAuthFailures is the count of inbound datagrams dropped
	// since conn construction due to AEAD authentication failure.
	AEADAuthFailures() uint64
}

// PacketNetworker is the opt-in interface a Networker implements
// when it supports datagram-shaped dialing. Separate from the core
// Networker contract so implementations that don't yet support
// datagrams stay valid — callers get ErrPacketNetworkerNotSupported
// when they hit one of those.
type PacketNetworker interface {
	// DialPacketContext opens a datagram conn to addr. Canceling
	// ctx during the dial returns ctx.Err. The returned conn is
	// safe for concurrent use by multiple goroutines.
	DialPacketContext(ctx context.Context, addr Addr) (DatagramPacketConn, error)
}

// DialPacket opens a UDP-shaped conn to addr using a background
// context.
func DialPacket(addr Addr) (DatagramPacketConn, error) {
	return DialPacketContext(context.Background(), addr)
}

// DialPacketContext opens a UDP-shaped conn to addr. The networker
// for addr.Net must implement PacketNetworker; otherwise
// ErrPacketNetworkerNotSupported is returned.
//
// On success the returned conn is loss-tolerant, possibly-
// unordered, no head-of-line blocking on reorder. Each datagram
// is independently encrypted + authenticated + replay-protected
// (per-datagram ChaCha20-Poly1305-IETF + RFC 6479 sliding window;
// see pkg/router/datagram_crypto.go).
func DialPacketContext(ctx context.Context, addr Addr) (DatagramPacketConn, error) {
	n, err := ResolveNetworker(addr.Net)
	if err != nil {
		return nil, err
	}
	pn, ok := n.(PacketNetworker)
	if !ok {
		return nil, ErrPacketNetworkerNotSupported
	}
	return pn.DialPacketContext(ctx, addr)
}
