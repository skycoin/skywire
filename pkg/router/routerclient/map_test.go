package routerclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestMakeMap(t *testing.T) {
	logging.SetLevel(logrus.WarnLevel)

	const timeout = time.Second * 5

	type testCase struct {
		okays int             // Number of routers to generate, that can be successfully dialed to.
		fails []time.Duration // Number of routers to generate, that should fail when dialed to.
	}

	cases := []testCase{
		{1, nil},
		{4, nil},
		{7, make([]time.Duration, 5)},
		{8, []time.Duration{0, time.Second, time.Millisecond}},
		{0, make([]time.Duration, 10)},
		{1, []time.Duration{timeout * 2}}, // this should still get canceled from hard deadline
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			var successes = tc.okays
			var fails = len(tc.fails)
			var total = successes + fails

			// Arrange: dialer that dials to remote routers, used for creating router clients
			dialer := newTestDialer(total)

			// Arrange: successful mock router
			okayR := new(router.MockRouter)
			okayR.On("ReserveKeys", mock.Anything).Return([]routing.RouteID{}, nil)

			// Arrange: serve router gateways (via single mock router)
			for i := 0; i < successes; i++ {
				addr := serveRouterRPC(t, okayR)
				dialer.Add(addr, nil)
			}
			for i := 0; i < fails; i++ {
				addr := serveRouterRPC(t, okayR)
				duration := time.Second
				dialer.Add(addr, &duration)
			}

			// Arrange: Ensure MakeMap call has a hard deadline
			ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(timeout))
			defer cancel()

			// Act: MakeMap dials to all routers
			rcM, err := MakeMap(ctx, dialer, dialer.PKs())
			if fails == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			t.Cleanup(func() {
				for _, err := range rcM.CloseAll() {
					assert.NoError(t, err)
				}
			})

			// TODO (darkrengarius): fix, just call another function
			/*if fails == 0 {
				// Assert: (all dials successful) we can successfully interact with the remote router via all clients
				require.NoError(t, err)
				for _, pk := range dialer.PKs() {
					routerC := rcM.Client(pk)
					assert.NotNil(t, routerC)
					_, err := rcM.Client(pk).ReserveIDs(context.Background(), 0)
					assert.NoError(t, err)
				}
			} else {
				// Assert: (some dials fail) should return error and internal clients are all nil
				require.Error(t, err)
				for _, pk := range dialer.PKs() {
					routerC := rcM.Client(pk)
					assert.Nil(t, routerC)
				}
			}*/
		})
	}
}

// serves a router over RPC and returns the bound TCP address
func serveRouterRPC(t *testing.T, r router.Router) (addr string) {
	l, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, l.Close()) })

	rpcS := rpc.NewServer()
	require.NoError(t, rpcS.Register(router.NewRPCGateway(r)))
	go rpcS.Accept(l)

	return l.Addr().String()
}

func pkFromAddr(addr string) (pk cipher.PubKey) {
	h := cipher.SumSHA256([]byte(addr))
	copy(pk[1:], h[:])
	return
}

// testDialer mocks a snet dialer which should dial to a 'pk:port' address
// To achieve this, we use a map that associates pks with a TCP addresses and dial via TCP.
// Ports become irrelevant.
type testDialer struct {
	m        map[cipher.PubKey]string
	failures map[cipher.PubKey]time.Duration
}

func newTestDialer(routers int) *testDialer {
	return &testDialer{
		m:        make(map[cipher.PubKey]string, routers),
		failures: make(map[cipher.PubKey]time.Duration),
	}
}

func (d *testDialer) Add(addr string, failure *time.Duration) cipher.PubKey {
	pk := pkFromAddr(addr)
	d.m[pk] = addr
	if failure != nil {
		d.failures[pk] = *failure
	}
	return pk
}

func (d *testDialer) PKs() []cipher.PubKey {
	out := make([]cipher.PubKey, 0, len(d.m))
	for pk := range d.m {
		out = append(out, pk)
	}
	return out
}

func (d *testDialer) Dial(_ context.Context, remote cipher.PubKey, _ uint16) (net.Conn, error) {
	if wait, ok := d.failures[remote]; ok {
		if wait != 0 {
			time.Sleep(wait)
		}
		return nil, errors.New("test error: failed to dial, as expected")
	}
	return net.Dial("tcp", d.m[remote])
}

func (testDialer) Type() string {
	return dmsg.Type
}
