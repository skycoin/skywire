// Package dmsgtest pkg/dmsgtest/dmsg_client_test.go
package dmsgtest

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dmsg "github.com/skycoin/skywire/pkg/dmsg"
)

func TestClient_RemoteClients(t *testing.T) {
	logging.SetLevel(logrus.ErrorLevel)

	// duration to wait before the resultant state is checked after an action
	const waitDuration = time.Millisecond * 100

	// port in which all client would listen on
	const port = uint16(24)

	type testCase struct {
		initSrvs int            // initial count of servers
		minSes   int            // min session count for clients
		changes  []clientChange // transactions of client disconnections/connections from local client
	}

	testCases := []testCase{
		{
			initSrvs: 1,
			minSes:   1,
			changes: []clientChange{
				{ingress: false, delta: 2},
				{ingress: false, delta: -1},
				{ingress: false, delta: 2},
				{ingress: false, delta: -2},
				{ingress: false, delta: 3},
				{ingress: false, delta: -3},
			},
		},
		{
			initSrvs: 2,
			minSes:   2,
			changes: []clientChange{
				{ingress: false, delta: 3},
				{ingress: false, delta: -3},
				{ingress: false, delta: 4},
				{ingress: false, delta: -1},
				{ingress: false, delta: -3},
				{ingress: false, delta: 1},
			},
		},
	}

	for i, tc := range testCases {
		tc := tc
		testName := fmt.Sprintf("%d:s%d_d%d",
			i, tc.initSrvs, len(tc.changes))

		t.Run(testName, func(t *testing.T) {
			rcN := clientsNeeded(t, tc.changes)

			// arrange: prepare env
			env := NewEnv(t, DefaultTimeout)
			conf := dmsg.Config{MinSessions: tc.minSes}
			require.NoError(t, env.Startup(DefaultTimeout, tc.initSrvs, rcN, &conf))
			t.Cleanup(env.Shutdown)

			// arrange: emulate remote clients
			rcs := env.AllClients()
			for _, rc := range rcs {
				rc := rc
				listenAndDiscard(t, rc, port)
			}

			// arrange: emulate local client
			lc, err := env.NewClient(&conf)
			require.NoError(t, err)
			listenAndDiscard(t, lc, port)

			var (
				// remote client egress index (determines the next client to dial to)
				nxtEgress = makeAdvanceClientFunc(rcs)

				// remote client ingress index (determines the next client to accept from)
				nxtIngress = makeAdvanceClientFunc(rcs)

				// saved streams. Length of this is also the expected number of elements of .AllStreams() call
				savedConns = make([]*dmsg.Stream, 0, rcN)
			)

			// range through changes, apply and check
			for changeI, change := range append([]clientChange{{}}, tc.changes...) {
				t.Logf("[%d] applying delta: %#v", changeI, change)

				switch {
				case change.delta > 0:
					// obtains expected src & dst clients, based on change.ingress
					var src, dst *dmsg.Client
					if change.ingress {
						src, dst = nxtIngress(t), lc
					} else {
						src, dst = lc, nxtEgress(t)
					}

					for i := 0; i < change.delta; i++ {
						conn, err := src.DialStream(context.TODO(), dmsg.Addr{PK: dst.LocalPK(), Port: port})
						require.NoError(t, err)
						continuousRandomWrite(t, conn)
						savedConns = append(savedConns, conn)
					}

				case change.delta < 0:
					for i := 0; i > change.delta; i-- {
						j := len(savedConns) - 1
						assert.NoError(t, savedConns[j].Close())
						savedConns = savedConns[:j]
					}
				}

				time.Sleep(waitDuration)

				// assert: check local client reports expected number of remote connections to clients.
				checkRemoteClients(t, lc, len(savedConns))
			}

			// cleanup: closing remaining connections
			for _, sc := range savedConns {
				assert.NoError(t, sc.Close())
			}
		})
	}
}

type advanceClientFunc func(t *testing.T) *dmsg.Client

func makeAdvanceClientFunc(clients []*dmsg.Client) advanceClientFunc {
	i := 0
	return func(t *testing.T) *dmsg.Client {
		t.Logf("i = %d; len(clients) = %d", i, len(clients))
		require.Truef(t, i < len(clients), "")
		c := clients[i]
		i++
		return c
	}
}

type clientChange struct {
	ingress bool // whether connection(s) is remote-initiated (ingress)
	delta   int  // (-) amount results in -n disconnections, (+) amount results in +n connections
}

// clientsNeeded determines the total number of initial clients needed
func clientsNeeded(t *testing.T, changes []clientChange) int {
	n := 0
	for _, c := range changes {
		if c.delta > 0 {
			n += c.delta
		}
	}
	t.Logf("clients needed: %d", n)
	return n
}

func continuousRandomWrite(t *testing.T, conn net.Conn) {
	const maxLen = 20
	const waitDuration = time.Second

	errCh := make(chan error, 1)
	t.Cleanup(func() { assert.NoError(t, <-errCh) })

	go func() {
		defer func() {
			errCh <- conn.Close()
			close(errCh)
		}()
		for {
			b := cipher.RandByte(rand.Intn(maxLen)) // nolint:gosec
			if _, err := conn.Write(b); err != nil {
				return
			}
			time.Sleep(waitDuration)
		}
	}()
}

func listenAndDiscard(t *testing.T, c *dmsg.Client, port uint16) {
	l, err := c.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, l.Close()) })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			// nolint:errcheck
			go func() { _, _ = io.Copy(io.Discard, conn) }()
		}
	}()
}

func checkRemoteClients(t *testing.T, lc *dmsg.Client, expectedConnections int) {
	conns := lc.AllStreams()
	require.Len(t, conns, expectedConnections)
}
