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

	str, err := newInitiatingStream(&ses)
	require.NoError(t, err)
	t.Cleanup(func() { _ = str.Close() }) //nolint:errcheck
	require.NoError(t, str.SetDeadline(time.Now().Add(HandshakeTimeout)))
	require.NoError(t, str.prepareFields(true, Addr{PK: ses.LocalPK(), Port: 50000}, Addr{PK: absentPK, Port: 1001}))
	nsMsg, err := str.ns.MakeHandshakeMessage()
	require.NoError(t, err)
	req := StreamRequest{
		Timestamp: time.Now().UnixNano(),
		SrcAddr:   str.lAddr,
		DstAddr:   str.rAddr,
		NoiseMsg:  nsMsg,
	}
	obj, err := MakeSignedStreamRequest(&req, ses.localSK())
	require.NoError(t, err)
	require.NoError(t, ses.writeObject(str.muxStream(), obj))

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
