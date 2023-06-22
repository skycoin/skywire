// Package dmsg pkg/dmsg/session_common.go
package dmsg

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"

	"github.com/skycoin/dmsg/pkg/noise"
)

// SessionCommon contains the common fields and methods used by a session, whether it be it from the client or server
// perspective.
type SessionCommon struct {
	entity *EntityCommon // back reference
	rPK    cipher.PubKey // remote pk

	netConn net.Conn // underlying net.Conn (TCP connection to the dmsg server)
	ys      *yamux.Session
	ns      *noise.Noise
	nMap    noise.NonceMap
	rMx     sync.Mutex
	wMx     sync.Mutex

	log logrus.FieldLogger
}

// GetConn returns underlying TCP `net.Conn`.
func (sc *SessionCommon) GetConn() net.Conn {
	return sc.netConn
}

// GetDecNonce returns value of DecNonce of underlying `*noise.Noise`.
func (sc *SessionCommon) GetDecNonce() uint64 {
	sc.rMx.Lock()
	defer sc.rMx.Unlock()
	return sc.ns.GetDecNonce()
}

// GetEncNonce returns value of EncNonce of underlying `*noise.Noise`.
func (sc *SessionCommon) GetEncNonce() uint64 {
	sc.wMx.Lock()
	defer sc.wMx.Unlock()
	return sc.ns.GetEncNonce()
}

func (sc *SessionCommon) initClient(entity *EntityCommon, conn net.Conn, rPK cipher.PubKey) error {
	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   entity.pk,
		LocalSK:   entity.sk,
		RemotePK:  rPK,
		Initiator: true,
	})
	if err != nil {
		return err
	}

	rw := noise.NewReadWriter(conn, ns)
	if err := rw.Handshake(time.Second * 5); err != nil {
		return err
	}
	if rw.Buffered() > 0 {
		return ErrSessionHandshakeExtraBytes
	}

	ySes, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		return err
	}

	sc.entity = entity
	sc.rPK = rPK
	sc.netConn = conn
	sc.ys = ySes
	sc.ns = ns
	sc.nMap = make(noise.NonceMap)
	sc.log = entity.log.WithField("session", ns.RemoteStatic())
	return nil
}

func (sc *SessionCommon) initServer(entity *EntityCommon, conn net.Conn) error {
	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   entity.pk,
		LocalSK:   entity.sk,
		Initiator: false,
	})
	if err != nil {
		return err
	}

	rw := noise.NewReadWriter(conn, ns)
	if err := rw.Handshake(time.Second * 5); err != nil {
		return err
	}
	if rw.Buffered() > 0 {
		return ErrSessionHandshakeExtraBytes
	}

	ySes, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		return err
	}

	sc.entity = entity
	sc.rPK = ns.RemoteStatic()
	sc.netConn = conn
	sc.ys = ySes
	sc.ns = ns
	sc.nMap = make(noise.NonceMap)
	sc.log = entity.log.WithField("session", ns.RemoteStatic())
	return nil
}

// writeEncryptedGob encrypts with noise and prefixed with uint16 (2 additional bytes).
func (sc *SessionCommon) writeObject(w io.Writer, obj SignedObject) error {
	sc.wMx.Lock()
	p := sc.ns.EncryptUnsafe(obj)
	sc.wMx.Unlock()
	p = append(make([]byte, 2), p...)
	binary.BigEndian.PutUint16(p, uint16(len(p)-2))
	_, err := w.Write(p)
	return err
}

func (sc *SessionCommon) readObject(r io.Reader) (SignedObject, error) {
	lb := make([]byte, 2)
	if _, err := io.ReadFull(r, lb); err != nil {
		return nil, err
	}
	pb := make([]byte, binary.BigEndian.Uint16(lb))
	if _, err := io.ReadFull(r, pb); err != nil {
		return nil, err
	}

	sc.rMx.Lock()
	if sc.nMap == nil {
		sc.rMx.Unlock()
		return nil, ErrSessionClosed
	}
	obj, err := sc.ns.DecryptWithNonceMap(sc.nMap, pb)
	sc.rMx.Unlock()

	return obj, err
}

func (sc *SessionCommon) localSK() cipher.SecKey { return sc.entity.sk }

// LocalPK returns the local public key of the session.
func (sc *SessionCommon) LocalPK() cipher.PubKey { return sc.entity.pk }

// RemotePK returns the remote public key of the session.
func (sc *SessionCommon) RemotePK() cipher.PubKey { return sc.rPK }

// LocalTCPAddr returns the local address of the underlying TCP connection.
func (sc *SessionCommon) LocalTCPAddr() net.Addr { return sc.netConn.LocalAddr() }

// RemoteTCPAddr returns the remote address of the underlying TCP connection.
func (sc *SessionCommon) RemoteTCPAddr() net.Addr { return sc.netConn.RemoteAddr() }

// Ping obtains the round trip latency of the session.
func (sc *SessionCommon) Ping() (time.Duration, error) { return sc.ys.Ping() }

// Close closes the session.
func (sc *SessionCommon) Close() error {
	if sc == nil {
		return nil
	}
	err := sc.ys.Close()
	sc.rMx.Lock()
	sc.nMap = nil
	sc.rMx.Unlock()
	return err
}
