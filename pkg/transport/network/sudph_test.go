// Package network sudph_test.go: unit tests for the non-network slices of
// the SUDPH client — Start/listen/serve guards and cleanup, the Dial/dial
// availability guards, dialWithTimeout context handling, and Close. These
// deliberately avoid real UDP hole-punching / kcp traffic, which is
// integration territory.
package network

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/porter"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func newTestSudph(t *testing.T, ar addrresolver.APIClient) *sudphClient {
	t.Helper()
	pk, sk := keyPair(t)
	gc := &genericClient{
		lPK:           pk,
		lSK:           sk,
		netType:       types.SUDPH,
		log:           testLog(),
		porter:        porter.New(porter.MinEphemeral),
		listeners:     make(map[uint16]*listener),
		listenStarted: make(chan struct{}),
		done:          make(chan struct{}),
	}
	return &sudphClient{resolvedClient: &resolvedClient{genericClient: gc, ar: ar}}
}

func TestSudphClient_DialGuards(t *testing.T) {
	t.Run("not available when listen never ran", func(t *testing.T) {
		c := newTestSudph(t, &addrresolver.MockAPIClient{})
		remote, _ := keyPair(t)
		_, err := c.Dial(context.Background(), remote, 1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "SUDPH not available")
	})

	t.Run("closed client", func(t *testing.T) {
		c := newTestSudph(t, &addrresolver.MockAPIClient{})
		require.NoError(t, c.Close())
		remote, _ := keyPair(t)
		_, err := c.Dial(context.Background(), remote, 1)
		require.ErrorIs(t, err, io.ErrClosedPipe)
	})
}

func TestSudphClient_DialRaw(t *testing.T) {
	t.Run("nil filter", func(t *testing.T) {
		c := newTestSudph(t, &addrresolver.MockAPIClient{})
		_, err := c.dial("1.2.3.4:5")
		require.Error(t, err)
		require.Contains(t, err.Error(), "packet filter not initialized")
	})

	t.Run("bad remote address", func(t *testing.T) {
		c := newTestSudph(t, &addrresolver.MockAPIClient{})
		// Give it a real packet filter so dial gets past the nil guard and
		// fails on UDP address resolution instead.
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		require.NoError(t, err)
		defer pc.Close() //nolint:errcheck
		c.filter = pfilter.NewPacketFilter(pc)
		c.filter.Start()

		_, err = c.dial("missing-port")
		require.Error(t, err)
		require.Contains(t, err.Error(), "ResolveUDPAddr")
	})
}

func TestSudphClient_DialWithTimeout_CtxDone(t *testing.T) {
	c := newTestSudph(t, &addrresolver.MockAPIClient{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	_, err := c.dialWithTimeout(ctx, "1.2.3.4:5")
	require.Error(t, err)
}

func TestSudphClient_StartAlreadyListening(t *testing.T) {
	c := newTestSudph(t, &addrresolver.MockAPIClient{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	c.connListener = lis

	require.ErrorIs(t, c.Start(), ErrAlreadyListening)
}

func TestSudphClient_Close_Fresh(t *testing.T) {
	c := newTestSudph(t, &addrresolver.MockAPIClient{})
	// No packet listener / visors conn set yet — Close should be a clean
	// no-op over the nil fields.
	require.NoError(t, c.Close())
}

func TestSudphClient_ListenBindError(t *testing.T) {
	ar := &addrresolver.MockAPIClient{}
	// BindSUDPH fails (e.g. AR has no UDP target). listen() must release
	// the UDP listener and nil out its filter/conn fields.
	ar.On("BindSUDPH", mock.Anything, mock.Anything).
		Return(nil, errors.New("no udp target"))

	c := newTestSudph(t, ar)
	_, err := c.listen()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no udp target")

	// Cleanup nilled the transient fields so a later Dial reports
	// unavailable rather than nil-deref'ing.
	require.Nil(t, c.filter)
	require.Nil(t, c.packetListener)
	require.Nil(t, c.sudphVisorsConn)
}

func TestSudphClient_ServeBindError(t *testing.T) {
	ar := &addrresolver.MockAPIClient{}
	ar.On("BindSUDPH", mock.Anything, mock.Anything).
		Return(nil, errors.New("no udp target"))

	c := newTestSudph(t, ar)
	// serve() calls listen(), hits the bind error, logs and returns without
	// starting the accept loop. It should not panic or block.
	c.serve()
	require.Nil(t, c.connListener)
}

func TestSudphClient_MakeBindHandshake(t *testing.T) {
	c := newTestSudph(t, &addrresolver.MockAPIClient{})
	hs := c.makeBindHandshake()
	require.NotNil(t, hs)
}
