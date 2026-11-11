// Package dmsg pkg/dmsg/dmsg/dial_refused_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// refusingServerSession returns a client session to a server with no other client.
func refusingServerSession(t *testing.T) ClientSession {
	t.Helper()
	dc := disc.NewMock(0)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	srvPK, srvSK := cipher.GenerateKeyPair()
	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 20, UpdateInterval: 0}, nil)
	entry := disc.NewServerEntry(srvPK, 0, addr, 20)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	pk, sk := cipher.GenerateKeyPair()
	c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
	c.SetLogger(logging.MustGetLogger("dial-refused"))
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	ses, err := c.EnsureAndObtainSession(context.Background(), srvPK)
	require.NoError(t, err)
	return ses
}

// A dial the server refuses fails with the server's reason, not a bare EOF.
func TestDialRefusedByServerCarriesReason(t *testing.T) {
	ses := refusingServerSession(t)
	absentPK, _ := cipher.GenerateKeyPair()
	cases := []struct {
		name string
		dst  Addr
		want error
	}{
		{"no session for destination", Addr{PK: absentPK, Port: 1001}, ErrReqNoNextSession},
		{"invalid destination port", Addr{PK: absentPK, Port: 0}, ErrReqInvalidDstPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := ses.DialStream(ctx, tc.dst)
			require.Error(t, err)
			require.NotErrorIs(t, err, io.EOF)
			require.True(t, errors.Is(err, tc.want), "got %v, want %v", err, tc.want)
		})
	}
}

// A dialer that did not ask for the server's refusal reads EOF, as a build
// without the field does.
func TestDialRefusedWithoutOptInReadsEOF(t *testing.T) {
	ses := refusingServerSession(t)
	absentPK, _ := cipher.GenerateKeyPair()

	str, req := writeRawRequest(t, ses, Addr{PK: absentPK, Port: 1001}, false)
	require.ErrorIs(t, str.readResponse(req), io.EOF)
}

// A build without AcceptsServerRefusal decodes a request that sets it, and
// the signature over the raw bytes still verifies.
func TestStreamRequestOptInDecodesWithoutTheField(t *testing.T) {
	// The StreamRequest of a build before the field was added.
	type oldStreamRequest struct {
		Timestamp int64
		SrcAddr   Addr
		DstAddr   Addr
		IPinfo    bool
		GeoInfo   bool
		NoiseMsg  []byte
	}
	srcPK, srcSK := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	req := StreamRequest{
		Timestamp:            time.Now().UnixNano(),
		SrcAddr:              Addr{PK: srcPK, Port: 50000},
		DstAddr:              Addr{PK: dstPK, Port: 1001},
		NoiseMsg:             []byte("noise"),
		AcceptsServerRefusal: true,
	}
	obj, err := MakeSignedStreamRequest(&req, srcSK)
	require.NoError(t, err)

	var old oldStreamRequest
	require.NoError(t, decodeGob(&old, obj.Object()))
	require.Equal(t, oldStreamRequest{
		Timestamp: req.Timestamp,
		SrcAddr:   req.SrcAddr,
		DstAddr:   req.DstAddr,
		NoiseMsg:  req.NoiseMsg,
	}, old)
	require.NoError(t, verifySig(old.SrcAddr.PK, obj.Sig(), obj.Object()))

	// Unset, the field adds nothing to the encoding.
	unset := req
	unset.AcceptsServerRefusal = false
	oldBytes, err := encodeGob(&oldStreamRequest{
		Timestamp: req.Timestamp,
		SrcAddr:   req.SrcAddr,
		DstAddr:   req.DstAddr,
		NoiseMsg:  req.NoiseMsg,
	})
	require.NoError(t, err)
	newBytes, err := encodeGob(&unset)
	require.NoError(t, err)
	var fromOld, fromNew StreamRequest
	require.NoError(t, decodeGob(&fromOld, oldBytes))
	require.NoError(t, decodeGob(&fromNew, newBytes))
	require.Equal(t, fromOld, fromNew)
	require.False(t, fromOld.AcceptsServerRefusal)
}

// servedPair returns a dialer and a destination on one server, with the
// dialer's session to that server. The destination serves and has no listener.
func servedPair(t *testing.T) (dialer, dst *Client, ses ClientSession) {
	t.Helper()
	dc := disc.NewMock(0)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	srvPK, srvSK := cipher.GenerateKeyPair()
	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 20, UpdateInterval: 0}, nil)
	entry := disc.NewServerEntry(srvPK, 0, addr, 20)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	connect := func() *Client {
		pk, sk := cipher.GenerateKeyPair()
		c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
		c.SetLogger(logging.MustGetLogger("dial-refused"))
		go c.Serve(ctx)
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		<-c.Ready()
		return c
	}
	dialer, dst = connect(), connect()
	ses, ok := dialer.clientSession(dialer.porter, srvPK)
	require.True(t, ok)
	return dialer, dst, ses
}

// writeRawRequest writes a stream request to dst on a new stream of ses,
// with or without the opt-in to a server refusal.
func writeRawRequest(t *testing.T, ses ClientSession, dst Addr, optIn bool) (*Stream, StreamRequest) {
	t.Helper()
	str, err := newInitiatingStream(&ses)
	require.NoError(t, err)
	t.Cleanup(func() { _ = str.Close() }) //nolint:errcheck
	require.NoError(t, str.SetDeadline(time.Now().Add(HandshakeTimeout)))
	require.NoError(t, str.prepareFields(true, Addr{PK: ses.LocalPK(), Port: 50000}, dst))
	nsMsg, err := str.ns.MakeHandshakeMessage()
	require.NoError(t, err)
	req := StreamRequest{
		Timestamp:            time.Now().UnixNano(),
		SrcAddr:              str.lAddr,
		DstAddr:              str.rAddr,
		NoiseMsg:             nsMsg,
		AcceptsServerRefusal: optIn,
	}
	obj, err := MakeSignedStreamRequest(&req, ses.localSK())
	require.NoError(t, err)
	require.NoError(t, ses.writeObject(str.muxStream(), obj))
	return str, req
}

// A dial to a port the destination does not listen on fails with the
// destination's reason, and the destination's session survives it.
func TestDialRefusedByDestinationCarriesReason(t *testing.T) {
	dialer, dst, ses := servedPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr := Addr{PK: dst.LocalPK(), Port: 1001}

	_, err := ses.DialStream(ctx, addr)
	require.Error(t, err)
	require.NotErrorIs(t, err, io.EOF)
	require.True(t, errors.Is(err, ErrReqNoListener), "got %v", err)

	// The whole dial ladder keeps its own code and carries the reason.
	_, err = dialer.DialStream(ctx, addr)
	require.True(t, errors.Is(err, ErrCannotConnectToDelegated), "got %v", err)
	require.True(t, errors.Is(err, ErrReqNoListener), "got %v", err)

	lis, err := dst.Listen(addr.Port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() }) //nolint:errcheck
	str, err := ses.DialStream(ctx, addr)
	require.NoError(t, err)
	require.NoError(t, str.Close())
}

// The destination's refusal is signed by the destination and is the only
// response on the stream, whether or not the dialer also takes a server's.
func TestDestinationRefusalIsTheOnlyResponse(t *testing.T) {
	_, dst, ses := servedPair(t)
	for _, optIn := range []bool{false, true} {
		str, req := writeRawRequest(t, ses, Addr{PK: dst.LocalPK(), Port: 1001}, optIn)

		obj, err := ses.readObject(str.muxStream())
		require.NoError(t, err, "opt-in %v", optIn)
		require.NoError(t, verifySig(dst.LocalPK(), obj.Sig(), obj.Object()), "opt-in %v", optIn)
		resp, err := obj.ObtainStreamResponse()
		require.NoError(t, err)
		require.False(t, resp.Accepted)
		// The check a dialer built before AcceptsServerRefusal runs.
		require.ErrorIs(t, resp.Verify(req), ErrReqNoListener)

		_, err = ses.readObject(str.muxStream())
		require.ErrorIs(t, err, io.EOF, "opt-in %v", optIn)
	}
}
