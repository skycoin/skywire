package dmsg

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
)

// relayedTestEnv is one dmsg server with two ordinary clients on it: relay,
// which will forward requests on a third key's behalf over its own session, and
// dst, which listens on dstPort. src is a bare keypair that holds no session at
// all — the identity a served desk tab or a service would have in stage 3.
type relayedTestEnv struct {
	dc     disc.APIClient
	srv    *Server
	srvPK  cipher.PubKey
	relay  *Client
	dst    *Client
	dstPK  cipher.PubKey
	srcPK  cipher.PubKey
	srcSK  cipher.SecKey
	dstLis *Listener
}

const relayedDstPort = 80

func newRelayedTestEnv(t *testing.T, conf *ServerConfig) *relayedTestEnv {
	t.Helper()
	dc := disc.NewMock(0)
	srvPK, srvSK := GenKeyPair(t, "relayed-srv")

	srv := NewServer(srvPK, srvSK, dc, conf, nil)
	srv.SetLogger(logging.MustGetLogger("relayed_server"))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	srvEntry := disc.NewServerEntry(srvPK, 0, addr, 10)
	require.NoError(t, srvEntry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	connect := func(name string) (*Client, cipher.PubKey) {
		pk, sk := GenKeyPair(t, name)
		c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
		c.SetLogger(logging.MustGetLogger(name))
		go c.Serve(context.Background()) //nolint:errcheck
		require.Eventually(t, func() bool {
			_, ok := c.Session(srvPK)
			return ok
		}, 15*time.Second, 100*time.Millisecond, "%s never formed a session", name)
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		return c, pk
	}
	relay, _ := connect("relayed-relay")
	dst, dstPK := connect("relayed-dst")
	dstLis, err := dst.Listen(relayedDstPort)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dstLis.Close() }) //nolint:errcheck

	srcPK, srcSK := GenKeyPair(t, "relayed-src")
	return &relayedTestEnv{
		dc:  dc,
		srv: srv, srvPK: srvPK, relay: relay, dst: dst, dstPK: dstPK,
		srcPK: srcPK, srcSK: srcSK, dstLis: dstLis,
	}
}

// relaySession is the relay's own session to the server, viewed the way the
// server-side forwarding code views a session: this is exactly the wrap the
// visor relay acceptor uses to forward with forwardRequest.
func (e *relayedTestEnv) relaySession(t *testing.T) ServerSession {
	t.Helper()
	ses, ok := e.relay.Session(e.srvPK)
	require.True(t, ok)
	return ServerSession{SessionCommon: ses.SessionCommon}
}

// signedRequest builds the request src would send to dst: signed by src, with
// src's half of the client↔client KK handshake. The relay never sees src's key.
func (e *relayedTestEnv) signedRequest(t *testing.T, ipinfo bool) (StreamRequest, SignedObject, *noise.Noise) {
	t.Helper()
	ns, err := noise.New(noise.HandshakeKK, noise.Config{
		LocalPK: e.srcPK, LocalSK: e.srcSK, RemotePK: e.dstPK, Initiator: true,
	})
	require.NoError(t, err)
	msg, err := ns.MakeHandshakeMessage()
	require.NoError(t, err)
	req := StreamRequest{
		Timestamp: time.Now().UnixNano(),
		SrcAddr:   Addr{PK: e.srcPK, Port: 1},
		DstAddr:   Addr{PK: e.dstPK, Port: relayedDstPort},
		NoiseMsg:  msg,
		IPinfo:    ipinfo,
	}
	if ipinfo {
		req.DstAddr = Addr{PK: e.srvPK}
	}
	obj, err := MakeSignedStreamRequest(&req, e.srcSK)
	require.NoError(t, err)
	req, err = obj.ObtainStreamRequest()
	require.NoError(t, err)
	return req, obj, ns
}

// expectRejected writes a request on a fresh stream of the relay's session and
// requires that no response comes back: the server drops a refused request.
func (e *relayedTestEnv) expectRejected(t *testing.T, obj SignedObject) {
	t.Helper()
	ses := e.relaySession(t)
	str, err := ses.sm.yamux.OpenStream()
	require.NoError(t, err)
	defer str.Close() //nolint:errcheck
	require.NoError(t, ses.writeObject(str, obj))
	require.NoError(t, str.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = ses.readObject(str)
	require.Error(t, err, "a refused relayed request must not be answered")
}

// A stream request signed by a key that holds no session, forwarded over
// another client's session, reaches its destination under the SOURCE key and
// carries data end to end; the server counts it against its relay slots.
func TestRelayedRequest_ForwardedOverClientSession(t *testing.T) {
	env := newRelayedTestEnv(t, &ServerConfig{
		MaxSessions: 10, UpdateInterval: 0, AcceptRelayedRequests: true, MaxRelayedStreams: 1,
	})

	accepted := make(chan *Stream, 1)
	go func() {
		s, err := env.dstLis.AcceptStream()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- s
	}()

	req, _, ns := env.signedRequest(t, false)
	relay := env.relaySession(t)
	yStr, respObj, err := relay.forwardRequest(req)
	require.NoError(t, err, "server must accept a relayed request whose signature verifies against its source key")
	defer yStr.Close() //nolint:errcheck
	resp, err := respObj.ObtainStreamResponse()
	require.NoError(t, err)
	require.True(t, resp.Accepted)
	require.NoError(t, ns.ProcessHandshakeMessage(resp.NoiseMsg))

	var s *Stream
	select {
	case s = <-accepted:
		require.NotNil(t, s, "dst failed to accept the relayed stream")
	case <-time.After(10 * time.Second):
		t.Fatal("dst never accepted the relayed stream")
	}
	defer s.Close() //nolint:errcheck
	require.Equal(t, env.srcPK, s.RemoteAddr().(Addr).PK, "the destination must see the SOURCE key, not the relay's")

	// End to end under src's own noise: the relay carries ciphertext only.
	rw := noise.NewReadWriter(yStr, ns)
	_, err = rw.Write([]byte("via relay"))
	require.NoError(t, err)
	buf := make([]byte, 9)
	require.NoError(t, s.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = s.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "via relay", string(buf))

	// Charged to the relay slots for as long as the bridge lives...
	require.Equal(t, int64(1), atomic.LoadInt64(&env.srv.relayedStreams))
	// ...so at MaxRelayedStreams 1 a second relayed request is refused.
	_, obj2, _ := env.signedRequest(t, false)
	env.expectRejected(t, obj2)
}

// With acceptance off, a client session may only carry its own key's requests
// (the pre-existing rule), and an IP request may never be relayed: the address
// it would report is the relay's.
func TestRelayedRequest_Refusals(t *testing.T) {
	strict := newRelayedTestEnv(t, &ServerConfig{MaxSessions: 10, UpdateInterval: 0})
	_, obj, _ := strict.signedRequest(t, false)
	strict.expectRejected(t, obj)

	open := newRelayedTestEnv(t, &ServerConfig{MaxSessions: 10, UpdateInterval: 0, AcceptRelayedRequests: true})
	_, ipObj, _ := open.signedRequest(t, true)
	open.expectRejected(t, ipObj)
}
