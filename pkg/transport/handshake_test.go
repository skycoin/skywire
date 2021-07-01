package transport_test

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet/snettest"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestSettlementHS(t *testing.T) {
	tpDisc := transport.NewDiscoveryMock()

	keys := snettest.GenKeyPairs(2)
	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	// TEST: Perform a handshake between two snet.Network instances.
	t.Run("Do", func(t *testing.T) {
		lis1, err := nEnv.Nets[1].Listen(dmsg.Type, skyenv.DmsgTransportPort)
		require.NoError(t, err)

		errCh1 := make(chan error, 1)
		go func() {
			defer close(errCh1)
			conn1, err := lis1.AcceptConn()
			if err != nil {
				errCh1 <- err
				return
			}
			errCh1 <- transport.MakeSettlementHS(false).Do(context.TODO(), tpDisc, conn1, keys[1].SK)
		}()

		const entryTimeout = 5 * time.Second
		start := time.Now()

		// Wait until entry is set.
		// TODO: Implement more elegant solution.
		for {
			if time.Since(start) > entryTimeout {
				t.Fatal("Entry in Dmsg Discovery is not set within expected time")
			}

			if _, err := nEnv.DmsgD.Entry(context.TODO(), keys[1].PK); err == nil {
				break
			}
		}

		conn0, err := nEnv.Nets[0].Dial(context.TODO(), dmsg.Type, keys[1].PK, skyenv.DmsgTransportPort)
		require.NoError(t, err)
		require.NoError(t, transport.MakeSettlementHS(true).Do(context.TODO(), tpDisc, conn0, keys[0].SK))

		require.NoError(t, <-errCh1)
	})
}

// TODO(evanlinjin): This will need further testing.
