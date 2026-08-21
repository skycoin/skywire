// Package network pkg/transport/network/conn_details_test.go c2-net-transport
package network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// addrStub is a minimal net.Addr for the conn stubs below.
type addrStub struct{ s string }

func (a addrStub) Network() string { return "stub" }
func (a addrStub) String() string  { return a.s }

// connStub satisfies net.Conn enough for ConnDetails to read its
// Local/RemoteAddr. Only the two address methods are exercised.
type connStub struct {
	net.Conn
	local, remote net.Addr
}

func (c connStub) LocalAddr() net.Addr  { return c.local }
func (c connStub) RemoteAddr() net.Addr { return c.remote }

// rawStub is a connStub that also contributes type-specific details,
// standing in for the quicStreamConn / dcConn rawConnDetailer path.
type rawStub struct {
	connStub
	alpn string
	cert string
	// overrideRemote, when set, is written into ConnDetails.RemoteAddr
	// to model webrtc's ICE-candidate override of the placeholder addr.
	overrideRemote string
}

func (r rawStub) rawConnDetails(d *ConnDetails) {
	d.ALPN = r.alpn
	d.TLSCertSHA256 = r.cert
	if r.overrideRemote != "" {
		d.RemoteAddr = r.overrideRemote
	}
}

func TestArBackedType(t *testing.T) {
	for _, tt := range []struct {
		typ  types.Type
		want bool
	}{
		{types.STCPR, true},
		{types.SUDPH, true},
		{types.QUIC, true}, // squicr
		{types.STCP, false},
		{types.DMSG, false},
	} {
		assert.Equalf(t, tt.want, arBackedType(tt.typ), "arBackedType(%s)", tt.typ)
	}
}

func TestTransportConnDetails_AddrsAndARFlag(t *testing.T) {
	stub := connStub{
		local:  addrStub{"10.0.0.1:1000"},
		remote: addrStub{"1.2.3.4:5000"},
	}
	tp := &transport{Conn: stub, transportType: types.STCPR}

	d := tp.ConnDetails()
	assert.Equal(t, "1.2.3.4:5000", d.RemoteAddr)
	assert.Equal(t, "10.0.0.1:1000", d.LocalAddr)
	assert.True(t, d.ARBackedType, "stcpr is an AR-backed carrier")
	assert.Empty(t, d.DmsgServerPK)
	assert.Empty(t, d.TLSCertSHA256)
}

func TestTransportConnDetails_RawContributor(t *testing.T) {
	raw := rawStub{
		connStub:       connStub{local: addrStub{"[::]:0"}, remote: addrStub{"9.9.9.9:443"}},
		alpn:           "skywire-quic-1",
		cert:           "deadbeef",
		overrideRemote: "9.9.9.9:443",
	}
	// c.Conn provides the base addrs; c.rawConn provides the extra details.
	tp := &transport{Conn: raw.connStub, rawConn: raw, transportType: types.QUIC}

	d := tp.ConnDetails()
	require.Equal(t, "9.9.9.9:443", d.RemoteAddr)
	assert.Equal(t, "skywire-quic-1", d.ALPN)
	assert.Equal(t, "deadbeef", d.TLSCertSHA256)
	assert.True(t, d.ARBackedType)
}

// TestTransportConnDetails_NoRawContributor ensures a transport whose
// rawConn does not implement rawConnDetailer still reports base details
// without panicking (the stcp/stcpr/sudph common case).
func TestTransportConnDetails_NoRawContributor(t *testing.T) {
	stub := connStub{local: addrStub{"127.0.0.1:1"}, remote: addrStub{"8.8.8.8:2"}}
	tp := &transport{Conn: stub, rawConn: stub, transportType: types.STCP}

	d := tp.ConnDetails()
	assert.Equal(t, "8.8.8.8:2", d.RemoteAddr)
	assert.False(t, d.ARBackedType)
	assert.Empty(t, d.ALPN)
}
