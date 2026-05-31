// Package network network_more_test.go: unit tests for the transport
// accessors, listener, generic client, address-resolver dial logic, and
// the small helpers in this package.
package network

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/porter"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func testLog() *logging.Logger { return logging.MustGetLogger("network_test") }

func keyPair(t *testing.T) (cipher.PubKey, cipher.SecKey) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	return pk, sk
}

// ---- isPrivateIP ----------------------------------------------------------

func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.32.0.1", false},
		{"192.168.1.1", true},
		{"100.64.0.1", true},   // CGNAT
		{"100.128.0.1", false}, // above CGNAT range
		{"127.0.0.1", true},    // loopback
		{"169.254.0.1", true},  // link-local
		{"8.8.8.8", false},     // public v4
		{"fd00::1", true},      // ULA v6
		{"fc00::1", true},      // ULA v6
		{"2001:db8::1", false}, // public v6
		{"::1", true},          // v6 loopback
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			require.Equal(t, tc.want, isPrivateIP(net.ParseIP(tc.ip)))
		})
	}
	require.False(t, isPrivateIP(nil))
}

// ---- transport accessors --------------------------------------------------

func TestTransportAccessors(t *testing.T) {
	cl, sv := net.Pipe()
	defer sv.Close() //nolint:errcheck

	lPK, _ := keyPair(t)
	rPK, _ := keyPair(t)
	freed := false
	tp := &transport{
		Conn:          cl,
		lAddr:         dmsg.Addr{PK: lPK, Port: 11},
		rAddr:         dmsg.Addr{PK: rPK, Port: 22},
		transportType: types.STCP,
		freePort:      func() { freed = true },
	}

	require.Equal(t, lPK, tp.LocalPK())
	require.Equal(t, rPK, tp.RemotePK())
	require.Equal(t, uint16(11), tp.LocalPort())
	require.Equal(t, uint16(22), tp.RemotePort())
	require.Equal(t, types.STCP, tp.Network())
	require.Equal(t, dmsg.Addr{PK: lPK, Port: 11}, tp.LocalAddr())
	require.Equal(t, dmsg.Addr{PK: rPK, Port: 22}, tp.RemoteAddr())
	require.Equal(t, cl.LocalAddr(), tp.LocalRawAddr())
	require.Equal(t, cl.RemoteAddr(), tp.RemoteRawAddr())

	require.NoError(t, tp.Close())
	require.True(t, freed, "freePort should be invoked on Close")
}

func TestTransportClose_NoFreePort(t *testing.T) {
	cl, sv := net.Pipe()
	defer sv.Close()           //nolint:errcheck
	tp := &transport{Conn: cl} // freePort nil
	require.NoError(t, tp.Close())
}

// ---- doHandshake ----------------------------------------------------------

func TestDoHandshake(t *testing.T) {
	lPK, _ := keyPair(t)
	rPK, _ := keyPair(t)
	lAddr := dmsg.Addr{PK: lPK, Port: 1}
	rAddr := dmsg.Addr{PK: rPK, Port: 2}

	t.Run("success", func(t *testing.T) {
		cl, sv := net.Pipe()
		defer sv.Close() //nolint:errcheck

		hs := func(_ net.Conn, _ time.Time) (dmsg.Addr, dmsg.Addr, error) {
			return lAddr, rAddr, nil
		}
		tp, err := DoHandshake(cl, hs, types.STCPR, testLog())
		require.NoError(t, err)
		require.Equal(t, lAddr, tp.LocalAddr())
		require.Equal(t, rAddr, tp.RemoteAddr())
		require.Equal(t, types.STCPR, tp.Network())
	})

	t.Run("handshake error closes conn", func(t *testing.T) {
		cl, sv := net.Pipe()
		defer sv.Close() //nolint:errcheck

		hsErr := errors.New("handshake failed")
		hs := func(_ net.Conn, _ time.Time) (dmsg.Addr, dmsg.Addr, error) {
			return dmsg.Addr{}, dmsg.Addr{}, hsErr
		}
		_, err := DoHandshakeWithTimeout(cl, hs, types.STCP, time.Second, testLog())
		require.ErrorIs(t, err, hsErr)
		// Conn was closed by doHandshake: a write should now fail.
		_, werr := cl.Write([]byte("x"))
		require.Error(t, werr)
	})
}

// ---- debugConn ------------------------------------------------------------

func TestDebugConn(t *testing.T) {
	cl, sv := net.Pipe()
	defer cl.Close() //nolint:errcheck

	dc := newDebugConn(cl)
	require.Equal(t, "(no data captured)", dc.capturedData())

	go func() {
		sv.Write([]byte("hello-world")) //nolint:errcheck
		sv.Close()                      //nolint:errcheck
	}()

	buf := make([]byte, 64)
	n, _ := dc.Read(buf)
	require.Equal(t, "hello-world", string(buf[:n]))

	captured := dc.capturedData()
	require.Contains(t, captured, "hex=")
	require.Contains(t, captured, "ascii=hello-world")
}

// ---- EncryptConn ----------------------------------------------------------

func TestEncryptConn_HandshakeError(t *testing.T) {
	lPK, lSK := keyPair(t)
	rPK, _ := keyPair(t)

	cl, sv := net.Pipe()
	// Close the remote end so the noise handshake write/read fails fast
	// instead of blocking for encryptHSTimout.
	require.NoError(t, sv.Close())

	cfg := noise.Config{LocalPK: lPK, LocalSK: lSK, RemotePK: rPK, Initiator: true}
	_, err := EncryptConn(cfg, cl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "noise handshake")
}

// ---- listener -------------------------------------------------------------

func TestListener(t *testing.T) {
	pk, _ := keyPair(t)
	lAddr := dmsg.Addr{PK: pk, Port: 7}
	freed := false
	lis := newListener(lAddr, func() { freed = true }, types.STCPR)

	require.Equal(t, lAddr, lis.Addr())
	require.Equal(t, pk, lis.PK())
	require.Equal(t, uint16(7), lis.Port())
	require.Equal(t, types.STCPR, lis.Network())

	// introduce delivers a transport that AcceptTransport returns.
	cl, sv := net.Pipe()
	defer sv.Close() //nolint:errcheck
	tp := &transport{Conn: cl, lAddr: lAddr}
	require.NoError(t, lis.introduce(tp))

	got, err := lis.AcceptTransport()
	require.NoError(t, err)
	require.Equal(t, tp, got)

	// Accept (net.Listener form) also pulls from the same channel.
	cl2, sv2 := net.Pipe()
	defer sv2.Close() //nolint:errcheck
	require.NoError(t, lis.introduce(&transport{Conn: cl2, lAddr: lAddr}))
	conn, err := lis.Accept()
	require.NoError(t, err)
	require.NotNil(t, conn)

	// Close frees the port and makes AcceptTransport return a closed-pipe error.
	require.NoError(t, lis.Close())
	require.True(t, freed)
	_, err = lis.AcceptTransport()
	require.ErrorIs(t, err, io.ErrClosedPipe)

	// introduce after close is rejected.
	cl3, sv3 := net.Pipe()
	defer sv3.Close() //nolint:errcheck
	require.ErrorIs(t, lis.introduce(&transport{Conn: cl3, lAddr: lAddr}), io.ErrClosedPipe)
}

// ---- generic client -------------------------------------------------------

func newTestGenericClient(t *testing.T, netType types.Type) *genericClient {
	t.Helper()
	pk, sk := keyPair(t)
	return &genericClient{
		lPK:           pk,
		lSK:           sk,
		netType:       netType,
		log:           testLog(),
		porter:        porter.New(porter.MinEphemeral),
		listeners:     make(map[uint16]*listener),
		listenStarted: make(chan struct{}),
		done:          make(chan struct{}),
	}
}

func TestGenericClient_Basics(t *testing.T) {
	c := newTestGenericClient(t, types.STCP)
	require.Equal(t, types.STCP, c.Type())
	require.NotEqual(t, cipher.PubKey{}, c.PK())
	require.NotEqual(t, cipher.SecKey{}, c.SK())
	require.False(t, c.isClosed())
}

func TestGenericClient_Listen(t *testing.T) {
	c := newTestGenericClient(t, types.STCPR)

	lis, err := c.Listen(8080)
	require.NoError(t, err)
	require.NotNil(t, lis)

	// getListener / checkListener resolve the registered port.
	gl, err := c.getListener(8080)
	require.NoError(t, err)
	require.Equal(t, lis, gl)
	require.NoError(t, c.checkListener(8080))

	// Unknown port.
	_, err = c.getListener(9999)
	require.Error(t, err)
	require.Error(t, c.checkListener(9999))

	// Re-listening on the same port is rejected (porter reserved it).
	_, err = c.Listen(8080)
	require.ErrorIs(t, err, ErrPortOccupied)
}

func TestGenericClient_ListenAfterClose(t *testing.T) {
	c := newTestGenericClient(t, types.STCP)
	require.NoError(t, c.Close())
	require.True(t, c.isClosed())

	_, err := c.Listen(1234)
	require.ErrorIs(t, err, io.ErrClosedPipe)

	// Close is idempotent.
	require.NoError(t, c.Close())
}

func TestGenericClient_LocalAddrNotListening(t *testing.T) {
	c := newTestGenericClient(t, types.STCP)
	// Unblock LocalAddr's <-listenStarted, then close so it reports
	// ErrNotListening rather than returning a nil listener's Addr.
	close(c.listenStarted)
	require.NoError(t, c.Close())

	_, err := c.LocalAddr()
	require.ErrorIs(t, err, ErrNotListening)
}

func TestGenericClient_CloseClosesListeners(t *testing.T) {
	c := newTestGenericClient(t, types.STCPR)
	lis, err := c.Listen(4321)
	require.NoError(t, err)

	require.NoError(t, c.Close())
	// The listener was closed by Close(); AcceptTransport returns EOF.
	_, err = lis.AcceptTransport()
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

// ---- ClientFactory.MakeClient ---------------------------------------------

func TestMakeClient(t *testing.T) {
	pk, sk := keyPair(t)
	f := &ClientFactory{
		PK:      pk,
		SK:      sk,
		PKTable: stcp.NewTable(nil),
	}

	cases := []struct {
		netType types.Type
		want    types.Type
	}{
		{types.STCP, types.STCP},
		{types.STCPR, types.STCPR},
		{types.SUDPH, types.SUDPH},
		{types.DMSG, types.DMSG},
	}
	for _, tc := range cases {
		t.Run(string(tc.netType), func(t *testing.T) {
			c, err := f.MakeClient(tc.netType, 0)
			require.NoError(t, err)
			require.Equal(t, tc.want, c.Type())
		})
	}

	t.Run("unknown type", func(t *testing.T) {
		_, err := f.MakeClient(types.Type("bogus"), 0)
		require.Error(t, err)
	})
}

func TestStcpClient_StartServe(t *testing.T) {
	pk, sk := keyPair(t)
	f := &ClientFactory{PK: pk, SK: sk, ListenAddr: "127.0.0.1:0", PKTable: stcp.NewTable(nil)}
	c, err := f.MakeClient(types.STCP, 0)
	require.NoError(t, err)

	require.NoError(t, c.Start())
	// LocalAddr blocks until serve() has bound the listener.
	addr, err := c.LocalAddr()
	require.NoError(t, err)
	require.NotNil(t, addr)

	// Starting again is rejected.
	require.ErrorIs(t, c.Start(), ErrAlreadyListening)

	require.NoError(t, c.Close())
}

func TestStcprClient_StartServe(t *testing.T) {
	pk, sk := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("BindSTCPR", mock.Anything, mock.Anything).Return(nil)

	f := &ClientFactory{PK: pk, SK: sk, ARClient: ar}
	c, err := f.MakeClient(types.STCPR, 0)
	require.NoError(t, err)

	require.NoError(t, c.Start())
	addr, err := c.LocalAddr() // unblocks once the bind + accept loop is up
	require.NoError(t, err)
	require.NotNil(t, addr)

	require.NoError(t, c.Close())
}

func TestStcp_DialAcceptEndToEnd(t *testing.T) {
	pkA, skA := keyPair(t)
	pkB, skB := keyPair(t)
	eb := appevent.NewBroadcaster(logging.MustGetLogger("eb"), time.Second)

	// A: listens on an ephemeral local address.
	fA := &ClientFactory{PK: pkA, SK: skA, ListenAddr: "127.0.0.1:0", PKTable: stcp.NewTable(nil), EB: eb}
	clientA, err := fA.MakeClient(types.STCP, 0)
	require.NoError(t, err)
	require.NoError(t, clientA.Start())
	addrA, err := clientA.LocalAddr()
	require.NoError(t, err)
	lis, err := clientA.Listen(9)
	require.NoError(t, err)

	// B: knows A's address via its PK table.
	fB := &ClientFactory{
		PK: pkB, SK: skB, EB: eb,
		PKTable: stcp.NewTable(map[cipher.PubKey]string{pkA: addrA.String()}),
	}
	clientB, err := fB.MakeClient(types.STCP, 0)
	require.NoError(t, err)

	acceptCh := make(chan Transport, 1)
	go func() {
		if tp, aerr := lis.AcceptTransport(); aerr == nil {
			acceptCh <- tp
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// B dials A: this drives Dial -> initTransport -> wrapTransport ->
	// doHandshake -> encrypt, and on A's side the accept loop runs the
	// responder handshake and introduces the transport to the listener.
	dialed, err := clientB.Dial(ctx, pkA, 9)
	require.NoError(t, err)
	require.Equal(t, pkA, dialed.RemotePK())
	require.Equal(t, uint16(9), dialed.RemotePort())

	var accepted Transport
	select {
	case accepted = <-acceptCh:
	case <-time.After(20 * time.Second):
		t.Fatal("listener did not accept the dialed transport")
	}
	require.Equal(t, pkB, accepted.RemotePK())

	// Data flows across the encrypted transport.
	go func() { _, _ = dialed.Write([]byte("hi")) }()
	buf := make([]byte, 2)
	_ = accepted.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := accepted.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "hi", string(buf[:n]))

	require.NoError(t, dialed.Close())
	require.NoError(t, accepted.Close())
	require.NoError(t, lis.Close())
	require.NoError(t, clientA.Close())
	require.NoError(t, clientB.Close())
}

func TestStcprClient_DialClosed(t *testing.T) {
	pk, sk := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	f := &ClientFactory{PK: pk, SK: sk, ARClient: ar}
	c, err := f.MakeClient(types.STCPR, 0)
	require.NoError(t, err)
	require.NoError(t, c.Close())

	remote, _ := keyPair(t)
	_, err = c.Dial(context.Background(), remote, 1)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestStcpClient_Dial(t *testing.T) {
	pk, sk := keyPair(t)
	f := &ClientFactory{PK: pk, SK: sk, PKTable: stcp.NewTable(nil)}
	c, err := f.MakeClient(types.STCP, 0)
	require.NoError(t, err)

	remote, _ := keyPair(t)

	// PK not in table -> entry-not-found.
	_, err = c.Dial(context.Background(), remote, 1)
	require.ErrorIs(t, err, ErrStcpEntryNotFound)

	// After close -> closed pipe.
	require.NoError(t, c.Close())
	_, err = c.Dial(context.Background(), remote, 1)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

// ---- resolvedClient.dialVisor ---------------------------------------------

func newTestResolvedClient(netType types.Type, lPK cipher.PubKey, ar addrresolver.APIClient) *resolvedClient {
	gc := &genericClient{lPK: lPK, netType: netType, log: testLog()}
	return &resolvedClient{genericClient: gc, ar: ar}
}

// recordDial returns a dialFunc that records addresses and yields conns per
// a supplied per-address result map (nil error => success).
func recordDial(dialed *[]string, fail map[string]error) dialFunc {
	return func(_ context.Context, addr string) (net.Conn, error) {
		*dialed = append(*dialed, addr)
		if err, ok := fail[addr]; ok && err != nil {
			return nil, err
		}
		return fakeConn{}, nil
	}
}

func TestDialVisor_ResolveError(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("Resolve", mock.Anything, string(types.STCPR), remote).
		Return(addrresolver.VisorData{}, errors.New("boom"))

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	_, err := c.dialVisor(context.Background(), remote, recordDial(&dialed, nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve PK")
	require.Empty(t, dialed)
}

func TestDialVisor_LocalLAN(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("")
	ar.On("Resolve", mock.Anything, mock.Anything, remote).Return(addrresolver.VisorData{
		RemoteAddr: "1.2.3.4:5",
		IsLocal:    true,
		LocalAddresses: addrresolver.LocalAddresses{
			Port:      "5",
			Addresses: []string{"127.0.0.1", "192.168.1.2"}, // loopback skipped (not self)
		},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	conn, err := c.dialVisor(context.Background(), remote, recordDial(&dialed, nil))
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"192.168.1.2:5"}, dialed) // loopback skipped, LAN dialed
}

func TestDialVisor_SelfConnection(t *testing.T) {
	lPK, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("")
	ar.On("Resolve", mock.Anything, mock.Anything, lPK).Return(addrresolver.VisorData{
		RemoteAddr: "1.2.3.4:5",
		LocalAddresses: addrresolver.LocalAddresses{
			Port:      "5",
			Addresses: []string{"127.0.0.1"}, // loopback used for self-connection
		},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	_, err := c.dialVisor(context.Background(), lPK, recordDial(&dialed, nil))
	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1:5"}, dialed)
}

func TestDialVisor_SamePublicIP(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("9.9.9.9")
	ar.On("Resolve", mock.Anything, mock.Anything, remote).Return(addrresolver.VisorData{
		RemoteAddr: "9.9.9.9:5",
		LocalAddresses: addrresolver.LocalAddresses{
			Port:      "5",
			Addresses: []string{"192.168.0.5"},
		},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	_, err := c.dialVisor(context.Background(), remote, recordDial(&dialed, nil))
	require.NoError(t, err)
	require.Equal(t, []string{"192.168.0.5:5"}, dialed)
}

func TestDialVisor_DualStackHappyEyeballs(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("")
	ar.On("Resolve", mock.Anything, mock.Anything, remote).Return(addrresolver.VisorData{
		RemoteAddr:     "1.2.3.4",
		RemoteAddrV6:   "2001:db8::1",
		LocalAddresses: addrresolver.LocalAddresses{Port: "5"},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	_, err := c.dialVisor(context.Background(), remote, recordDial(&dialed, nil))
	require.NoError(t, err)
	// v6 first (happy eyeballs), and it succeeds so v4 is not tried.
	require.Equal(t, []string{"[2001:db8::1]:5"}, dialed)
}

func TestDialVisor_V4Only(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("")
	ar.On("Resolve", mock.Anything, mock.Anything, remote).Return(addrresolver.VisorData{
		RemoteAddr:     "1.2.3.4",
		LocalAddresses: addrresolver.LocalAddresses{Port: "5"},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	_, err := c.dialVisor(context.Background(), remote, recordDial(&dialed, nil))
	require.NoError(t, err)
	require.Equal(t, []string{"1.2.3.4:5"}, dialed)
}

func TestDialVisor_V6Only(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("")
	ar.On("Resolve", mock.Anything, mock.Anything, remote).Return(addrresolver.VisorData{
		RemoteAddrV6:   "2001:db8::1",
		LocalAddresses: addrresolver.LocalAddresses{Port: "5"},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	_, err := c.dialVisor(context.Background(), remote, recordDial(&dialed, nil))
	require.NoError(t, err)
	require.Equal(t, []string{"[2001:db8::1]:5"}, dialed)
}

func TestDialVisor_NeitherAddr(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("")
	ar.On("Resolve", mock.Anything, mock.Anything, remote).Return(addrresolver.VisorData{
		LocalAddresses: addrresolver.LocalAddresses{Port: "5"},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	_, err := c.dialVisor(context.Background(), remote, recordDial(&dialed, nil))
	require.Error(t, err)
	require.Contains(t, err.Error(), "neither RemoteAddr")
	require.Empty(t, dialed)
}

// ---- misc helpers / error paths -------------------------------------------

func TestNewListenerExported(t *testing.T) {
	pk, _ := keyPair(t)
	lis := NewListener(dmsg.Addr{PK: pk, Port: 3}, func() {}, types.SUDPH)
	require.Equal(t, uint16(3), lis.Port())
	require.Equal(t, types.SUDPH, lis.Network())
	require.NoError(t, lis.Close())
}

func TestAcceptTransport_Closed(t *testing.T) {
	c := newTestGenericClient(t, types.STCP)
	require.NoError(t, c.Close())
	require.ErrorIs(t, c.acceptTransport(), io.ErrClosedPipe)
}

func TestWrapTransport_HandshakeError(t *testing.T) {
	c := newTestGenericClient(t, types.STCP)
	cl, sv := net.Pipe()
	defer sv.Close() //nolint:errcheck

	onCloseCalled := false
	hsErr := errors.New("hs boom")
	hs := func(_ net.Conn, _ time.Time) (dmsg.Addr, dmsg.Addr, error) {
		return dmsg.Addr{}, dmsg.Addr{}, hsErr
	}
	_, err := c.wrapTransport(cl, hs, true, func() { onCloseCalled = true })
	require.ErrorIs(t, err, hsErr)
	require.True(t, onCloseCalled)
}

func TestGetStunDetails_AllOffline(t *testing.T) {
	// An unresolvable server address fails fast; GetStunDetails should
	// log the failures and return a (zero-valued) StunDetails rather than
	// panicking or blocking.
	log := testLog()
	details := GetStunDetails([]string{"256.256.256.256:3478"}, log)
	require.NotNil(t, details)
}

func TestDialVisor_LANFailsFallsBackToPublic(t *testing.T) {
	lPK, _ := keyPair(t)
	remote, _ := keyPair(t)
	ar := &addrresolver.MockAPIClient{}
	ar.On("LocalPublicIP").Return("")
	ar.On("Resolve", mock.Anything, mock.Anything, remote).Return(addrresolver.VisorData{
		RemoteAddr: "1.2.3.4",
		IsLocal:    true,
		LocalAddresses: addrresolver.LocalAddresses{
			Port:      "5",
			Addresses: []string{"192.168.1.2"},
		},
	}, nil)

	c := newTestResolvedClient(types.STCPR, lPK, ar)
	var dialed []string
	conn, err := c.dialVisor(context.Background(), remote, recordDial(&dialed,
		map[string]error{"192.168.1.2:5": errors.New("LAN unreachable")}))
	require.NoError(t, err)
	require.NotNil(t, conn)
	// LAN attempt first, then the public v4 fallback.
	require.Equal(t, []string{"192.168.1.2:5", "1.2.3.4:5"}, dialed)
}
