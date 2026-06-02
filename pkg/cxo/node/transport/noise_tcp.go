package transport

import (
	"fmt"
	"net"
	"time"

	"github.com/skycoin/noise"

	"github.com/skycoin/skywire/pkg/cipher"
	skynoise "github.com/skycoin/skywire/pkg/dmsg/noise"
)

// noiseHandshakeTimeout bounds the XX handshake on a CXO TCP conn.
const noiseHandshakeTimeout = 30 * time.Second

// noiseConn is a net.Conn whose Read/Write are encrypted by a Noise
// ReadWriter. The embedded net.Conn supplies addrs / deadlines / Close;
// only Read/Write are overridden to go through the cipher.
type noiseConn struct {
	net.Conn
	rw  *skynoise.ReadWriter
	rPK cipher.PubKey
}

func (c *noiseConn) Read(p []byte) (int, error)  { return c.rw.Read(p) }
func (c *noiseConn) Write(p []byte) (int, error) { return c.rw.Write(p) }

// RemotePK returns the peer's static public key, learned during the XX
// handshake.
func (c *noiseConn) RemotePK() cipher.PubKey { return c.rPK }

// wrapNoiseXX runs a Noise_XX handshake over raw and returns an
// encrypted net.Conn. XX is mutual-authentication with no pre-pinned
// remote key: both sides transmit AND learn each other's static key
// during the handshake. That matches the CXO transport's "dial by
// address, learn the PK during the handshake" model, so it works for
// every caller (swarm, rpc, subscriber) without threading a remote PK
// through Connect. It uses the SAME canonical noise package
// (secp256k1 / ChaCha20-Poly1305 / SHA-256, replay-windowed nonces)
// that dmsg, tcpnoise, and the skywire transports use — so CXO-TCP is
// no longer the one plaintext transport.
func wrapNoiseXX(raw net.Conn, localPK cipher.PubKey, localSK cipher.SecKey, initiator bool) (*noiseConn, error) {
	ns, err := skynoise.New(noise.HandshakeXX, skynoise.Config{
		LocalPK:   localPK,
		LocalSK:   localSK,
		Initiator: initiator,
	})
	if err != nil {
		return nil, fmt.Errorf("cxo-tcp noise init: %w", err)
	}
	rw := skynoise.NewReadWriter(raw, ns)
	if err := rw.Handshake(noiseHandshakeTimeout); err != nil {
		return nil, fmt.Errorf("cxo-tcp noise handshake: %w", err)
	}
	return &noiseConn{Conn: raw, rw: rw, rPK: rw.RemoteStatic()}, nil
}
