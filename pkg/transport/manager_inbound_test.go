// Package transport pkg/transport/manager_inbound_test.go
package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestSettlementInboundGate: the responder's gate sees the dialer's key and
// label, and a refusal reaches the dialer as a settlement rejection.
func TestSettlementInboundGate(t *testing.T) {
	for _, tc := range []struct {
		label Label
		take  bool
	}{
		{LabelAutomatic, false},
		{LabelUser, true},
	} {
		t.Run(string(tc.label), func(t *testing.T) {
			pkA, skA := cipher.GenerateKeyPair()
			pkB, skB := cipher.GenerateKeyPair()
			cA, cB := net.Pipe()
			tA := &pipeTransport{Conn: cA, lpk: pkA, rpk: pkB, nw: types.SUDPH}
			tB := &pipeTransport{Conn: cB, lpk: pkB, rpk: pkA, nw: types.SUDPH}
			defer cA.Close() //nolint:errcheck
			defer cB.Close() //nolint:errcheck
			dc := NewDiscoveryMock()
			ctx := context.Background()

			var seenPK cipher.PubKey
			var seenLabel Label
			gate := func(remote cipher.PubKey, label Label) bool {
				seenPK, seenLabel = remote, label
				return label != LabelAutomatic
			}

			initErr := make(chan error, 1)
			go func() {
				_, err := MakeSettlementHS(true, logging.MustGetLogger("hs-test"), tc.label).Do(ctx, dc, tA, skA)
				initErr <- err
			}()
			_, respErr := makeSettlementRespHS(gate).Do(ctx, dc, tB, skB)

			select {
			case err := <-initErr:
				if tc.take {
					require.NoError(t, err)
					require.NoError(t, respErr)
				} else {
					require.ErrorContains(t, err, "rejected by remote")
					require.ErrorIs(t, respErr, ErrInboundRefused)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("initiator handshake timed out")
			}
			require.Equal(t, pkA, seenPK)
			require.Equal(t, tc.label, seenLabel)
		})
	}
}
