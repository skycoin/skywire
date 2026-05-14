// Package dmsgpty pkg/dmsg/dmsgpty/host_tcp.go — direct-TCP entry
// point for the dmsgpty / dmsgscp / dmsgcat protocols, with an XK
// noise handshake gating the dmsgpty whitelist.
//
// The dmsg-overlay path (ListenAndServe on a dmsg port) is the
// primary entry. This file adds a parallel listener that operators
// can enable via Dmsgpty.TCPListen in the visor config. The
// handshake model intentionally mirrors ssh's:
//
//   - The CLIENT pins the SERVER's PK (passed as RemotePK on the
//     XK-Initiator side). A wrong PK fails the handshake — the
//     equivalent of "host key check failed".
//   - The SERVER learns the CLIENT's PK from the completed
//     handshake (noise.RemoteStatic()). That PK is then checked
//     against the dmsgpty Whitelist — the equivalent of
//     authorized_keys but indexed by visor PK instead of an SSH
//     pubkey.
//
// The Stream that comes out of the noise wrapper satisfies net.Conn
// and is fed to the same serveConn path that the dmsg-overlay
// listener uses, so dmsgpty / dmsgscp / dmsgcat handlers are
// transport-agnostic at the protocol layer.
package dmsgpty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
)

// tcpHandshakeTimeout bounds the XK noise handshake on each accepted
// TCP connection. Operator-facing dial latency is bounded; a stuck
// handshake (client never speaks, or attacker probes the port) costs
// at most one timeout's worth of accept-loop pacing.
const tcpHandshakeTimeout = 10 * time.Second

// ListenAndServeTCP brings up a raw-TCP entry point for the dmsgpty
// protocol on addr (net.Listen format — `:2022`, `0.0.0.0:2022`,
// etc.). Each accepted connection runs an XK noise handshake with
// the visor's identity keypair before being authorized against the
// dmsgpty whitelist and handed to the standard dmsgpty mux.
//
// Blocks until ctx is canceled or the listener fails permanently.
// Returns the underlying listen error in the latter case; ctx-driven
// shutdown returns nil.
//
// Whitelist sharing: the wl injected at NewHost time is the same wl
// the dmsg-overlay path uses. There is no separate TCP ACL by design
// — trusting a peer for dmsg-pty implicitly trusts them over the
// TCP entry point, since the protocol semantics are identical.
func (h *Host) ListenAndServeTCP(ctx context.Context, addr string, localPK cipher.PubKey, localSK cipher.SecKey) error {
	if addr == "" {
		return errors.New("dmsgpty: ListenAndServeTCP: empty addr")
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dmsgpty: tcp listen %s: %w", addr, err)
	}

	log := logging.MustGetLogger("dmsg_pty_tcp")
	// dmsgC is nil in --no-dmsg mode (standalone TCP-only host); skip
	// the master-logger lookup in that case rather than NPE-ing.
	if h.dmsgC != nil {
		if masterLogger := h.dmsgC.MasterLogger(); masterLogger != nil {
			log = masterLogger.PackageLogger("dmsg_pty_tcp")
		}
	}
	log.WithField("addr", lis.Addr().String()).Info("Direct-TCP dmsgpty listener bound.")

	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()

	mux := dmsgEndpoints(h)
	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
				log.Debug("dmsgpty-tcp: cleanly stopped serving.")
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() { //nolint:staticcheck // Temporary is the canonical accept-error pattern
				log.WithError(err).Warn("dmsgpty-tcp: temporary accept error, continuing...")
				time.Sleep(50 * time.Millisecond)
				continue
			}
			log.WithError(err).Error("dmsgpty-tcp: permanent accept error.")
			return err
		}
		go h.handleTCPConn(ctx, log, &mux, conn, localPK, localSK)
	}
}

// handleTCPConn runs the per-connection noise + authorize + serve
// pipeline. Each accepted conn becomes its own goroutine so a stuck
// handshake (slow client, attacker probing the port) doesn't block
// the accept loop.
func (h *Host) handleTCPConn(
	ctx context.Context,
	parentLog logrus.FieldLogger,
	mux *hostMux,
	conn net.Conn,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
) {
	h.serveTCPConn(ctx, parentLog, mux, conn, localPK, localSK)
}

// ServeAcceptedTCP runs the per-connection noise + authorize + serve
// pipeline on a TCP conn that the caller has already accepted (e.g.
// from a systemd-socket-activation handoff: net.FileConn on FD 0).
//
// Synchronous and one-shot: returns when the session ends. Designed
// for the "one dmsgpty-host process per active session" model that
// matches sshd's fork-per-session behavior — the parent socket-
// activated unit can be restarted without disturbing this process's
// running session.
//
// localPK / localSK identify the server side of the noise handshake.
// In --sk-from-visor mode these come from skywire-config.json's
// top-level sk/pk fields so the host shares the visor's identity
// without contending for a dmsg-discovery entry.
//
// Logging: uses a fresh package logger if h has no dmsg.Client master
// logger (the standalone --no-dmsg path); otherwise reuses the
// master logger for parity with ListenAndServeTCP.
func (h *Host) ServeAcceptedTCP(
	ctx context.Context,
	conn net.Conn,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
) {
	log := logging.MustGetLogger("dmsg_pty_tcp")
	if h.dmsgC != nil {
		if masterLogger := h.dmsgC.MasterLogger(); masterLogger != nil {
			log = masterLogger.PackageLogger("dmsg_pty_tcp")
		}
	}
	mux := dmsgEndpoints(h)
	h.serveTCPConn(ctx, log, &mux, conn, localPK, localSK)
}

// serveTCPConn is the shared per-conn pipeline used by both the
// listener-driven accept loop (ListenAndServeTCP -> handleTCPConn)
// and the externally-accepted single-conn entry point
// (ServeAcceptedTCP). Pulled out to keep the noise/authorize logic
// in one place.
func (h *Host) serveTCPConn(
	ctx context.Context,
	parentLog logrus.FieldLogger,
	mux *hostMux,
	conn net.Conn,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
) {
	defer func() { _ = conn.Close() }() //nolint:errcheck

	log := parentLog.WithField("remote_addr", conn.RemoteAddr().String())

	// XK with Initiator=false: we (server) know our own static, and
	// learn the client's static via the handshake. Pre-handshake
	// deadlines bound the cost of an attacker holding a TCP slot open
	// without speaking; once the handshake completes the per-stream
	// timeouts of the dmsgpty handlers take over.
	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   localPK,
		LocalSK:   localSK,
		Initiator: false,
	})
	if err != nil {
		log.WithError(err).Warn("dmsgpty-tcp: noise init failed.")
		return
	}
	rw := noise.NewReadWriter(conn, ns)
	if err := rw.Handshake(tcpHandshakeTimeout); err != nil {
		log.WithError(err).Debug("dmsgpty-tcp: noise handshake failed.")
		return
	}
	if rw.Buffered() > 0 {
		log.Warn("dmsgpty-tcp: noise handshake left buffered bytes — abort.")
		return
	}
	rPK := rw.RemoteStatic()
	log = log.WithField("remote_pk", rPK.String())

	if !h.authorize(log, rPK) {
		// authorize logs the rejection itself; close the wrapped
		// conn so the client gets a read EOF.
		return
	}

	log = log.WithField("conn_id", atomic.AddInt32(&h.connN, 1))
	defer atomic.AddInt32(&h.connN, -1)
	log.Debug("dmsgpty-tcp: stream accepted.")

	nc := &noiseNetConn{Conn: conn, rw: rw, rPK: rPK}
	h.serveConn(ctx, log, mux, nc)
}

// noiseNetConn adapts a noise.ReadWriter back to net.Conn so the
// existing dmsgpty mux can consume it identically to a dmsg.Stream.
// Read / Write go through the noise layer for encryption + framing;
// LocalAddr / RemoteAddr / Close / SetDeadline pass through to the
// underlying TCP conn unchanged. RemoteAddr returns a synthetic
// dmsg-shaped address so any downstream code that pattern-matches
// on `dmsg.Addr` / `appnet.Addr` for PK extraction can keep its
// existing extraction path.
type noiseNetConn struct {
	net.Conn // for LocalAddr / Close / Set*Deadline. Read/Write below override.
	rw       *noise.ReadWriter
	rPK      cipher.PubKey
}

// Read pulls plaintext bytes from the noise layer.
func (c *noiseNetConn) Read(p []byte) (int, error) { return c.rw.Read(p) }

// Write pushes plaintext bytes through the noise layer.
func (c *noiseNetConn) Write(p []byte) (int, error) { return c.rw.Write(p) }

// RemoteAddr returns a TCPNoiseAddr carrying the handshake-derived
// peer PK alongside the underlying TCP address. Callers that need
// the PK can type-assert; callers that just want a net.Addr get the
// usual ip:port string.
func (c *noiseNetConn) RemoteAddr() net.Addr {
	return TCPNoiseAddr{TCP: c.Conn.RemoteAddr(), PK: c.rPK}
}

// TCPNoiseAddr is the RemoteAddr type returned from a dmsgpty-tcp
// accepted connection. The underlying TCP address is preserved so
// log lines + audit trails look familiar; PK is the peer's
// noise-handshake-verified static key.
type TCPNoiseAddr struct {
	TCP net.Addr
	PK  cipher.PubKey
}

// Network returns the underlying TCP network ("tcp" / "tcp4" / "tcp6").
func (a TCPNoiseAddr) Network() string { return a.TCP.Network() }

// String returns a `<pk>@<ip:port>` form so log lines uniquely
// identify the peer without the reader having to cross-reference the
// dmsg discovery.
func (a TCPNoiseAddr) String() string {
	return fmt.Sprintf("%s@%s", a.PK.String(), a.TCP.String())
}
