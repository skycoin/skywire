package network

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestQUICListenerBoundsPendingConns opens many QUIC conns that never open a
// stream. The listener must stop accepting once maxPendingQUICConns wait, so
// quic-go refuses the rest instead of every conn parking a goroutine.
func TestQUICListenerBoundsPendingConns(t *testing.T) {
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()
	srvCert, err := newQUICCertificate(srvPK, srvSK)
	require.NoError(t, err)
	cliCert, err := newQUICCertificate(cliPK, cliSK)
	require.NoError(t, err)
	qconf := &quic.Config{HandshakeIdleTimeout: 2 * time.Second}

	srvUDP := quicTestUDP(t)
	defer srvUDP.Close() //nolint:errcheck
	qlis, err := quic.Listen(srvUDP, quicTLSConfig(srvCert, nil, nil), qconf)
	require.NoError(t, err)
	lis := newQuicListener(qlis, logging.MustGetLogger("quic-bound-test"))
	defer lis.Close() //nolint:errcheck

	cliUDP := quicTestUDP(t)
	defer cliUDP.Close() //nolint:errcheck
	tr := &quic.Transport{Conn: cliUDP}
	defer tr.Close() //nolint:errcheck

	const dials = 4 * maxPendingQUICConns
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range dials {
		wg.Go(func() {
			qc, err := tr.Dial(ctx, srvUDP.LocalAddr(), quicTLSConfig(cliCert, &srvPK, nil), qconf)
			if err != nil {
				return
			}
			defer qc.CloseWithError(0, "") //nolint:errcheck
			// A conn the server accepted is answered when the server stops it;
			// count those that stay up past the handshake.
			select {
			case <-qc.Context().Done():
			case <-time.After(time.Second):
				accepted.Add(1)
				<-ctx.Done()
			}
		})
	}
	wg.Wait()
	t.Logf("%d of %d conns stayed up", accepted.Load(), dials)
	require.Less(t, accepted.Load(), int64(dials/2))
}
