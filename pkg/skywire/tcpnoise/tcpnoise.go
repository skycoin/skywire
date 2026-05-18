// Package tcpnoise — noise-XK over raw TCP, factored out of
// pkg/dmsg/dmsgpty so apps other than dmsgpty (skychat first, then
// app skynet / app skysocks etc. per #2706) can build TCP-direct
// transports on the same primitive without dragging in a dmsgpty
// dependency.
//
// The handshake is XK (one-side static, mutual auth via static
// public keys). Initiator pins the responder's PK before dial;
// responder learns the initiator's PK from the handshake. The
// resulting net.Conn surfaces decrypted bytes via Read/Write —
// callers see plaintext frames and don't touch the noise layer.
//
// Threat model: same as dmsgpty's --via tcp path. Endpoint
// confidentiality + integrity rely on the operator's PK pinning
// (--whitelist on accept side, explicit PK in the dial URL on
// initiator side). No discovery; both sides agree out-of-band on
// host:port and the remote PK.
package tcpnoise

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
)

// DialTimeout bounds the underlying TCP dial.
const DialTimeout = 10 * time.Second

// HandshakeTimeout bounds the noise XK handshake. Generous because
// the first noise round-trip can stall on TCP fast-open quirks +
// app-cold-start latency; once the handshake completes per-stream
// timeouts at the application layer take over.
const HandshakeTimeout = 10 * time.Second

// Dial opens a TCP connection to addr, runs an XK noise handshake
// pinning rPK as the responder, and returns the noise-wrapped
// net.Conn ready for application framing.
//
// localPK / localSK is the initiator's identity. Standard skywire
// convention: use the visor's SK so the PK matches what peers see
// over dmsg / skynet (allowlists keep working). Ephemeral keypairs
// also work for one-off use but will fail PK-pinned remotes.
func Dial(
	ctx context.Context,
	addr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	rPK cipher.PubKey,
) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: DialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcpnoise: dial %s: %w", addr, err)
	}

	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   localPK,
		LocalSK:   localSK,
		RemotePK:  rPK,
		Initiator: true,
	})
	if err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("tcpnoise: noise init: %w", err)
	}
	rw := noise.NewReadWriter(conn, ns)
	if err := rw.Handshake(HandshakeTimeout); err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("tcpnoise: handshake (server pinned to %s): %w", rPK, err)
	}
	return &Conn{Conn: conn, rw: rw, rPK: rPK}, nil
}

// Accept runs the responder side of the XK handshake on raw — a
// connection already accepted from a net.Listener. Returns the
// noise-wrapped conn AND the remote PK learned during the
// handshake. The caller is expected to consult a whitelist on the
// returned PK before serving any application traffic over the conn.
//
// On handshake failure, the raw conn is closed and an error is
// returned. The caller should NOT use raw afterwards.
func Accept(
	raw net.Conn,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
) (net.Conn, cipher.PubKey, error) {
	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   localPK,
		LocalSK:   localSK,
		Initiator: false,
	})
	if err != nil {
		_ = raw.Close() //nolint:errcheck,gosec
		return nil, cipher.PubKey{}, fmt.Errorf("tcpnoise: noise init: %w", err)
	}
	rw := noise.NewReadWriter(raw, ns)
	if err := rw.Handshake(HandshakeTimeout); err != nil {
		_ = raw.Close() //nolint:errcheck,gosec
		return nil, cipher.PubKey{}, fmt.Errorf("tcpnoise: handshake: %w", err)
	}
	rPK := rw.RemoteStatic()
	return &Conn{Conn: raw, rw: rw, rPK: rPK}, rPK, nil
}

// Conn is a net.Conn adapter over a noise.ReadWriter. The embedded
// net.Conn supplies LocalAddr / Close / Set*Deadline; Read / Write
// override to go through the noise framing for confidentiality +
// integrity. rPK is the peer's static PK pinned during the
// handshake — exposed via RemotePK for app-side whitelist / logging.
type Conn struct {
	net.Conn // for LocalAddr / Close / Set*Deadline
	rw       *noise.ReadWriter
	rPK      cipher.PubKey
}

// Read pulls plaintext bytes from the noise layer.
func (c *Conn) Read(p []byte) (int, error) { return c.rw.Read(p) }

// Write seals plaintext bytes via the noise layer.
func (c *Conn) Write(p []byte) (int, error) { return c.rw.Write(p) }

// RemotePK is the peer's static public key learned via the
// handshake (responder side) or pinned at dial time (initiator
// side). Callers use this to authorize the peer against a
// whitelist before serving application traffic.
func (c *Conn) RemotePK() cipher.PubKey { return c.rPK }
