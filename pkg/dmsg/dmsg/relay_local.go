// Package dmsg pkg/dmsg/dmsg/relay_local.go
//
// The LOCAL half of the relay (see client_relay.go). AcceptRelaySession takes a
// plain net.Conn and keeps the attached peer's own identity, so the pipe under
// it is free: today the visor feeds it from skynet (a skywire route to
// skyenv.DmsgRelayPort), and here it is fed from a unix socket or a loopback
// listener on the same host.
//
// What that buys is a STANDALONE process — its own keypair, its own binary, no
// router, no visor — holding a dmsg identity without holding dmsg server
// sessions. The dmsgweb proxy is the motivating case: it runs under a key the
// survey whitelist knows, so the key must NOT rotate, and every restart of it
// costs the deployment a fresh set of server sessions plus a discovery entry
// churn. Attached locally it keeps the key, holds exactly one session (to the
// visor beside it), publishes no entry, and its streams are forwarded over the
// VISOR's sessions and transports — which is what the visor is already paying
// for.
//
// The wire under the socket is identical to the skynet one: a hello preamble so
// the attaching process can learn which key to run the Noise XK handshake
// against, then the same Noise+yamux dmsg session. Nothing above the pipe knows
// the difference, which is the point — the carrier stays CarrierSkynet and the
// session behaves as any other relay attachment (DialStream's phase 0,
// RelayOnly's "exactly one session", the relay-slot accounting).
//
// AUTHORIZATION. Attaching here is a real grant: the attached process dials
// dmsg under ITS OWN key but over the visor's sessions, and is charged to the
// visor's relay slots. Two independent gates, and the acceptor is opt-in:
//
//   - The listener itself. A unix socket's file permissions are the gate — mode
//     0600 means "a process running as the visor's user", which is already a
//     process that could read the visor's own secret key off disk, so the grant
//     adds nothing it did not have. This is why the unix socket is the default
//     and why it is created 0600.
//   - The allow callback, same shape as the skynet acceptor's. The Noise XK
//     handshake PROVES the attacher holds the secret key for the PK it claims,
//     so an allow that checks a configured key list is cryptographic
//     authentication and not a hint. A listener with no filesystem gate
//     (loopback TCP) must therefore be paired with a non-empty key list; the
//     visor refuses to bind one otherwise.
//
// There is deliberately no third option: no network-facing acceptor, no
// "anyone on localhost" TCP default.
package dmsg

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// The local-attach hello: the ONLY bytes on the socket that are not the dmsg
// session itself. It exists because the attaching side must know the acceptor's
// public key BEFORE it can speak dmsg at all — the session handshake is Noise
// XK, whose initiator needs the responder's static key up front. Without the
// preamble every attaching tool would have to learn the visor's PK out of band
// (parse skywire-config.json, or hold RPC credentials just to ask), and would
// get an opaque handshake failure when that guess went stale. With it the
// socket path is the whole configuration.
//
// It is a local-only framing. The skynet acceptor sends nothing of the kind:
// there the PK is what the route was set up for, so it is already known.
const (
	localRelayMagic       = "DRLY"
	localRelayHelloV1     = 1
	localRelayHelloLen    = len(localRelayMagic) + 1 + len(cipher.PubKey{}) + 2
	localRelayHelloIOTime = 10 * time.Second
)

// ErrLocalRelayHello is returned when the bytes at the head of a local relay
// conn are not a hello this build understands — most often because the socket
// path points at something that is not a dmsg relay acceptor at all.
var ErrLocalRelayHello = errors.New("dmsg: not a local dmsg relay acceptor")

// writeLocalRelayHello writes the acceptor's identity preamble: magic, version,
// the acceptor's public key, and the dmsg port its relay is published on (so
// the attaching side can synthesize the same skynet://<pk>:<port> address the
// route-carried attach would have used, and logs/`visor state` read alike).
func writeLocalRelayHello(w io.Writer, pk cipher.PubKey, port uint16) error {
	b := make([]byte, 0, localRelayHelloLen)
	b = append(b, localRelayMagic...)
	b = append(b, localRelayHelloV1)
	b = append(b, pk[:]...)
	b = binary.BigEndian.AppendUint16(b, port)
	_, err := w.Write(b)
	return err
}

// readLocalRelayHello reads the preamble written by writeLocalRelayHello.
func readLocalRelayHello(r io.Reader) (cipher.PubKey, uint16, error) {
	var pk cipher.PubKey
	b := make([]byte, localRelayHelloLen)
	if _, err := io.ReadFull(r, b); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return pk, 0, fmt.Errorf("%w: short hello", ErrLocalRelayHello)
		}
		return pk, 0, err
	}
	if string(b[:len(localRelayMagic)]) != localRelayMagic {
		return pk, 0, fmt.Errorf("%w: bad magic", ErrLocalRelayHello)
	}
	if v := b[len(localRelayMagic)]; v != localRelayHelloV1 {
		return pk, 0, fmt.Errorf("%w: unsupported hello version %d", ErrLocalRelayHello, v)
	}
	off := len(localRelayMagic) + 1
	copy(pk[:], b[off:off+len(pk)])
	if pk.Null() {
		return pk, 0, fmt.Errorf("%w: null public key", ErrLocalRelayHello)
	}
	port := binary.BigEndian.Uint16(b[off+len(pk):])
	return pk, port, nil
}

// ServeLocalRelay accepts local attach conns on lis and serves each as a relay
// session on this client, exactly as the skynet acceptor does — the pipe is the
// only difference. It blocks until lis fails or ctx is done, so callers run it
// in their own goroutine; lis is closed on return.
//
// port is what the hello advertises: the dmsg port this visor's relay is
// published on (skyenv.DmsgRelayPort), so an attached peer nominates the same
// address whichever pipe it came in over.
//
// allow is the SAME callback shape the skynet acceptor passes to
// AcceptRelaySession, and carries the same meaning: it is consulted after the
// Noise handshake has proven the peer's key, and refusing closes the conn. A nil
// allow admits any key that can open lis — correct ONLY when the listener
// itself is the gate (a 0600 unix socket). See this file's package comment.
func (ce *Client) ServeLocalRelay(ctx context.Context, lis net.Listener, port uint16, allow func(cipher.PubKey) bool) error {
	defer lis.Close() //nolint:errcheck

	// Unblock the Accept below on cancellation: a unix listener has no other
	// way out, and leaving the goroutine parked would hold the socket file
	// past the visor's shutdown and make the NEXT start fail to bind it.
	go func() {
		select {
		case <-ctx.Done():
			_ = lis.Close() //nolint:errcheck
		case <-ce.done:
			_ = lis.Close() //nolint:errcheck
		}
	}()

	for {
		conn, err := lis.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosed(ce.done) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go func(conn net.Conn) {
			// The hello is bounded: a local process that connects and then
			// never reads would otherwise pin this goroutine (and a relay
			// slot's worth of attention) forever.
			_ = conn.SetWriteDeadline(time.Now().Add(localRelayHelloIOTime)) //nolint:errcheck
			if err := writeLocalRelayHello(conn, ce.pk, port); err != nil {
				ce.log.WithError(err).Debug("dmsg local relay: hello write failed")
				_ = conn.Close() //nolint:errcheck
				return
			}
			// Clear it again: the session that follows manages its own
			// deadlines, and a stale write deadline would kill it mid-stream.
			_ = conn.SetWriteDeadline(time.Time{}) //nolint:errcheck
			if err := ce.AcceptRelaySession(ctx, conn, allow); err != nil {
				ce.log.WithError(err).Debug("dmsg local relay session ended with error")
			}
		}(conn)
	}
}

// LocalRelayDialer dials the local relay acceptor at network/addr ("unix" +
// socket path, or "tcp" + a loopback address). It is the raw dial, hello and
// all: see DialLocalRelay and AttachLocalRelay for the two things callers
// actually do with it.
type LocalRelayDialer struct {
	Network string
	Addr    string
}

// DialLocalRelay opens one conn to the acceptor and reads its hello, returning
// the conn positioned at the first byte of the dmsg session along with the
// acceptor's key and relay port.
func (d LocalRelayDialer) DialLocalRelay(ctx context.Context, expect *cipher.PubKey) (net.Conn, cipher.PubKey, uint16, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, d.Network, d.Addr)
	if err != nil {
		return nil, cipher.PubKey{}, 0, err
	}
	deadline := time.Now().Add(localRelayHelloIOTime)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetReadDeadline(deadline) //nolint:errcheck
	pk, port, err := readLocalRelayHello(conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, cipher.PubKey{}, 0, fmt.Errorf("dmsg: local relay %s://%s: %w", d.Network, d.Addr, err)
	}
	_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	// A mismatch here means the socket now fronts a DIFFERENT visor than the
	// one this client nominated — a reinstall, or a socket path reused by
	// another instance. Failing loudly beats handing the conn to a Noise XK
	// handshake keyed to the old PK, which fails with nothing to read from.
	if expect != nil && pk != *expect {
		_ = conn.Close() //nolint:errcheck
		return nil, cipher.PubKey{}, 0, fmt.Errorf("dmsg: local relay %s://%s is %s, expected %s", d.Network, d.Addr, pk, *expect)
	}
	return conn, pk, port, nil
}

// PubKey probes the acceptor for its public key and relay port, then hangs up.
// A caller needs both before it can nominate the relay (SetRelayPeers takes a
// key, and the Noise XK session handshake needs it too).
func (d LocalRelayDialer) PubKey(ctx context.Context) (cipher.PubKey, uint16, error) {
	conn, pk, port, err := d.DialLocalRelay(ctx, nil)
	if err != nil {
		return cipher.PubKey{}, 0, err
	}
	_ = conn.Close() //nolint:errcheck
	return pk, port, nil
}

// SessionDialer returns a Config.SessionDialer that serves the skynet carrier
// from this local socket instead of from a skywire route. The address it is
// handed is the usual "skynet://<pk>:<port>", and the PK in it is checked
// against the acceptor's hello — so a client that nominated visor A cannot be
// silently attached to visor B by a swapped socket.
//
// It is the whole client-side counterpart: a standalone process installs this
// (SetSessionDialer), nominates the visor (SetRelayPeers), and the ordinary
// serve loop does the rest.
func (d LocalRelayDialer) SessionDialer() SessionDialer {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != CarrierSkynet {
			return nil, fmt.Errorf("dmsg local relay dialer: unsupported carrier %q", network)
		}
		pk, _, err := ParseSkynetAddr(addr)
		if err != nil {
			return nil, err
		}
		conn, _, _, err := d.DialLocalRelay(ctx, &pk)
		return conn, err
	}
}

// AttachLocalRelay points this client at the local relay acceptor at
// network/addr: it probes the acceptor for its identity, installs the session
// dialer that serves the skynet carrier from that socket, and nominates the
// acceptor as this client's relay. It returns the acceptor's public key.
//
// It does NOT change this client's identity, and deliberately cannot: the key
// is fixed at NewClient, the acceptor learns it from the session handshake, and
// every stream this client dials afterwards is signed with it and arrives at
// the destination under it. That is the whole reason to attach rather than to
// run a second client — a key the deployment already knows (a whitelisted
// survey key, a service PK in someone's config) keeps working.
//
// Pair it with Config.RelayOnly to get the intended end state: one session, to
// the visor, and no discovery entry at all. Without RelayOnly the client also
// keeps MinSessions server sessions and publishes an entry naming them, which
// is a legitimate but different thing to want.
func (ce *Client) AttachLocalRelay(ctx context.Context, network, addr string) (cipher.PubKey, error) {
	d := LocalRelayDialer{Network: network, Addr: addr}
	pk, port, err := d.PubKey(ctx)
	if err != nil {
		return cipher.PubKey{}, err
	}
	if pk == ce.pk {
		// SetRelayPeers drops self-nominations silently, which would leave the
		// caller with a client that never attaches and no reason why.
		return pk, fmt.Errorf("dmsg: local relay %s://%s has this client's own key %s", network, addr, pk)
	}
	ce.SetSessionDialer(d.SessionDialer())
	ce.SetRelayPeers([]cipher.PubKey{pk}, port)
	return pk, nil
}
