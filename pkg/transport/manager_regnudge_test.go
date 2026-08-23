// Package transport — pkg/transport/manager_regnudge_test.go: regression guard
// for the outbound re-register nudge (#4122).
//
// The bug: a dial-only visor (a browser/wasm visor never accepts inbound
// transports) never hit the transport manager's re-register NUDGE, which was
// sent only from the transport-ACCEPT path. So after such a visor dialed OUT,
// its CXO tp-list snapshot was published only on the 90s re-register ticker —
// leaving it unroutable ("transport not found") for up to 90s, which for a
// short-lived tab is effectively forever.
//
// The fix (manager.go, saveTransportInternal): after a successful OUTBOUND
// transport save, send on tm.regNudge (non-blocking), symmetric with the accept
// path. The re-register loop debounces ~5s and publishes the tp-list snapshot.
//
// This test asserts the user-visible OUTCOME rather than poking the unexported
// channel directly: with a CXO leaf publisher installed, a successful outbound
// SaveTransport causes the publisher's Put("tp-list", …) to fire PROMPTLY —
// well inside the 90s ticker window. Before the fix, no publish would occur
// until that 90s ticker; the generous 10s timeout below would then fail.
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

// blockingTransport is a network.Transport whose Read blocks until Close, so a
// transport installed by a successful SaveTransport stays "serving" for the
// duration of the test instead of tearing down on an immediate EOF (which would
// remove it from tm.tps and could itself trigger a deletion-path publish,
// muddying the assertion). Its non-Read surface mirrors memTransport.
type blockingTransport struct {
	done     chan struct{}
	once     sync.Once
	lpk, rpk cipher.PubKey
	nw       types.Type
}

func newBlockingTransport(lpk, rpk cipher.PubKey, nw types.Type) *blockingTransport {
	return &blockingTransport{done: make(chan struct{}), lpk: lpk, rpk: rpk, nw: nw}
}

func (t *blockingTransport) Read([]byte) (int, error) {
	<-t.done // block until closed, then report EOF
	return 0, io.EOF
}
func (t *blockingTransport) Write(p []byte) (int, error) {
	select {
	case <-t.done:
		return 0, net.ErrClosed
	default:
		return len(p), nil
	}
}
func (t *blockingTransport) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}
func (t *blockingTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (t *blockingTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (t *blockingTransport) SetDeadline(time.Time) error      { return nil }
func (t *blockingTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *blockingTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *blockingTransport) LocalPK() cipher.PubKey           { return t.lpk }
func (t *blockingTransport) RemotePK() cipher.PubKey          { return t.rpk }
func (t *blockingTransport) LocalPort() uint16                { return 0 }
func (t *blockingTransport) RemotePort() uint16               { return 0 }
func (t *blockingTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (t *blockingTransport) RemoteRawAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 5000}
}
func (t *blockingTransport) Network() types.Type { return t.nw }

// dialingClient is a network.Client whose Dial always succeeds, handing back the
// same long-lived blockingTransport. It is enough to drive saveTransportInternal
// through a real OUTBOUND save: with SaveTransportNoRegister the manager swaps in
// a noop discovery client, which skips the settlement handshake — so a working
// Dial is all that's needed for the outbound path (and its nudge) to run.
type dialingClient struct {
	pk  cipher.PubKey
	sk  cipher.SecKey
	typ types.Type
	tp  network.Transport
}

func (c *dialingClient) Dial(context.Context, cipher.PubKey, uint16) (network.Transport, error) {
	return c.tp, nil
}
func (c *dialingClient) Start() error { return nil }
func (c *dialingClient) Listen(uint16) (network.Listener, error) {
	return nil, io.ErrClosedPipe
}
func (c *dialingClient) LocalAddr() (net.Addr, error) { return &net.TCPAddr{}, nil }
func (c *dialingClient) PK() cipher.PubKey            { return c.pk }
func (c *dialingClient) SK() cipher.SecKey            { return c.sk }
func (c *dialingClient) Close() error                 { return nil }
func (c *dialingClient) Type() types.Type             { return c.typ }

// nudgeLeafPub records tp-list publishes and signals on tpListCh the first time
// the snapshot leaf (tpdListPath) is Put — the observable effect of the nudge.
type nudgeLeafPub struct {
	mu       sync.Mutex
	putPaths []string
	tpListCh chan struct{}
}

func newNudgeLeafPub() *nudgeLeafPub {
	return &nudgeLeafPub{tpListCh: make(chan struct{}, 1)}
}

func (p *nudgeLeafPub) Put(path string, _ []byte) error {
	p.mu.Lock()
	p.putPaths = append(p.putPaths, path)
	p.mu.Unlock()
	if path == tpdListPath {
		select {
		case p.tpListCh <- struct{}{}:
		default:
		}
	}
	return nil
}
func (p *nudgeLeafPub) Delete(string) error { return nil }

// TestOutboundSaveNudgesReRegister is the #4122 regression guard: a successful
// OUTBOUND SaveTransport must nudge the re-register loop so the tp-list snapshot
// is published promptly, not only on the 90s ticker.
func TestOutboundSaveNudgesReRegister(t *testing.T) {
	tm := newTestManager(t)
	// saveTransportInternal builds the ManagedTransport with tm.Conf.LogStore;
	// the bare test manager has none, so give it an in-memory store.
	tm.Conf.LogStore = InMemoryTransportLogStore()

	const nw = types.STCPR
	client := &dialingClient{
		pk:  tm.Conf.PubKey,
		sk:  tm.Conf.SecKey,
		typ: nw,
		tp:  newBlockingTransport(tm.Conf.PubKey, mustPK(t), nw),
	}
	tm.mx.Lock()
	tm.netClients[nw] = client
	tm.mx.Unlock()

	// Install the CXO leaf publisher so re-registration takes the CXO snapshot
	// path (publishTPDList) instead of the HTTP fallback.
	pub := newNudgeLeafPub()
	tm.SetTPDLeafPublisher(pub)

	// Start the manager's background loops (runReRegisterTransports lives here).
	// Teardown cancels the context (the loops select on ctx.Done) and closes the
	// underlying transport to unblock the serving read loop. We deliberately do
	// NOT call tm.Close(): it waits on the serve WaitGroup while holding tm.mx,
	// which can wedge a unit test; cancelling the context stops every loop and
	// the leaked goroutines exit with the process.
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		client.tp.Close()
	}()
	tm.Serve(ctx)

	// A dial-only visor: dial OUT to a peer. NoRegister keeps this a pure unit
	// test (noop discovery → no settlement handshake, no live TPD) while still
	// running the real saveTransportInternal outbound path — including the nudge.
	remote := mustPK(t)
	mTp, err := tm.SaveTransportNoRegister(ctx, remote, nw, LabelUser)
	require.NoError(t, err)
	require.NotNil(t, mTp)

	// The re-register loop debounces ~5s before publishing. A tp-list Put well
	// inside 10s proves the outbound nudge fired. Before #4122 the first publish
	// would wait the full 90s ticker and this would time out.
	select {
	case <-pub.tpListCh:
		// success: prompt snapshot publish triggered by the outbound nudge.
	case <-time.After(10 * time.Second):
		t.Fatal("no tp-list snapshot published within 10s of an outbound SaveTransport: " +
			"the re-register nudge did not fire (regression of #4122; before the fix a " +
			"dial-only visor waited the full 90s re-register ticker)")
	}

	// The published leaf must be the transport-list snapshot path.
	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Contains(t, pub.putPaths, tpdListPath)
}
