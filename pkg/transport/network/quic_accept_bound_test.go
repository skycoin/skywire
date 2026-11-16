package network

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestQUICListenerBoundsPendingConns opens QUIC conns that never open a
// stream. Each accepted one holds its slot for quicStreamAcceptTimeout, so the
// listener must take exactly its cap off quic-go once every dial has settled.
func TestQUICListenerBoundsPendingConns(t *testing.T) {
	const pending, dials = 4, 12 // dials stay within quic-go's accept queue

	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()
	srvCert, err := newQUICCertificate(srvPK, srvSK)
	require.NoError(t, err)
	cliCert, err := newQUICCertificate(cliPK, cliSK)
	require.NoError(t, err)
	qconf := &quic.Config{HandshakeIdleTimeout: 20 * time.Second}

	srvUDP := quicTestUDP(t)
	defer srvUDP.Close() //nolint:errcheck
	qlis, err := quic.Listen(srvUDP, quicTLSConfig(srvCert, nil, nil), qconf)
	require.NoError(t, err)
	lis := newQuicListenerCap(qlis, logging.MustGetLogger("quic-bound-test"), pending)
	defer lis.Close() //nolint:errcheck

	cliUDP := quicTestUDP(t)
	defer cliUDP.Close() //nolint:errcheck
	tr := &quic.Transport{Conn: cliUDP}
	defer tr.Close() //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	var held, dialed sync.WaitGroup
	defer held.Wait()
	defer cancel()
	dialed.Add(dials)
	for range dials {
		held.Go(func() {
			qc, err := tr.Dial(ctx, srvUDP.LocalAddr(), quicTLSConfig(cliCert, &srvPK, nil), qconf)
			dialed.Done()
			if err != nil {
				return
			}
			<-ctx.Done()
			qc.CloseWithError(0, "") //nolint:errcheck,gosec
		})
	}

	dialed.Wait()
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int64(pending), lis.accepted.Load())
}
