// Package transport — pkg/transport/manager_accept_nolock_test.go: the
// settlement handshake of an incoming transport must not run under tm.mx.
//
// Seen live on prod01 (2026-09-28): a peer opened a dmsg transport three times
// in a row and never sent its settlement entry. acceptTransport held the
// manager's write lock through each 20s handshake timeout, so for 59s every
// other transport on the relay stopped being read ("readCh full for 30s") and
// the lock watchdog fired. This test hands the manager one such silent dialer
// and checks that a reader of the transport map is not stuck behind it.
package transport

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// oneShotListener is a network.Listener that yields the transports pushed into
// it and then blocks, standing in for a real carrier listener.
type oneShotListener struct {
	ch   chan network.Transport
	once sync.Once
	pk   cipher.PubKey
	nw   types.Type
}

func (l *oneShotListener) AcceptTransport() (network.Transport, error) {
	tp, ok := <-l.ch
	if !ok {
		return nil, io.ErrClosedPipe
	}
	return tp, nil
}
func (l *oneShotListener) Accept() (net.Conn, error) { return l.AcceptTransport() }
func (l *oneShotListener) Close() error {
	l.once.Do(func() { close(l.ch) })
	return nil
}
func (l *oneShotListener) Addr() net.Addr      { return &net.TCPAddr{} }
func (l *oneShotListener) PK() cipher.PubKey   { return l.pk }
func (l *oneShotListener) Port() uint16        { return 0 }
func (l *oneShotListener) Network() types.Type { return l.nw }

// TestAcceptHandshakeDoesNotHoldManagerLock: while the responder side of the
// settlement handshake waits on a dialer that never sends its entry, other
// callers must still get through tm.mx.
func TestAcceptHandshakeDoesNotHoldManagerLock(t *testing.T) {
	tm := newTestManager(t)
	tm.Conf.LogStore = InMemoryTransportLogStore()

	const nw = types.STCPR
	client := &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: nw}
	tm.mx.Lock()
	tm.netClients[nw] = client
	tm.mx.Unlock()

	// The dialer: its Read blocks forever, so receiveAndVerifyEntry never
	// returns and the handshake runs to its 20s deadline (or ctx cancel).
	silent := newBlockingTransport(tm.Conf.PubKey, mustPK(t), nw)
	lis := &oneShotListener{ch: make(chan network.Transport, 1), pk: tm.Conf.PubKey, nw: nw}
	lis.ch <- silent

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		require.NoError(t, silent.Close())
		require.NoError(t, lis.Close())
	}()

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- tm.acceptTransport(ctx, lis) }()

	// The managed transport is installed in the map before the handshake
	// starts; once it is there the handshake is in progress.
	require.Eventually(t, func() bool { return tm.TransportCount() == 1 }, 2*time.Second, 10*time.Millisecond)

	// A reader of the map must not wait for the silent dialer. Before the fix
	// this blocked for the whole 20s handshake timeout.
	readDone := make(chan struct{})
	go func() {
		tm.WalkTransports(func(*ManagedTransport) bool { return true })
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("transport map reader blocked behind an in-progress settlement handshake: acceptTransport holds tm.mx across the handshake")
	}

	// The accept itself is still pending on the silent dialer.
	select {
	case err := <-acceptDone:
		t.Fatalf("acceptTransport returned early: %v", err)
	default:
	}

	// Canceling the accept context ends the handshake; the failed accept must
	// close the dialer's connection (no CLOSE_WAIT leak).
	cancel()
	select {
	case err := <-acceptDone:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("acceptTransport did not return after its context was canceled")
	}
	select {
	case <-silent.done:
	default:
		t.Fatal("failed accept left the incoming connection open")
	}
}
