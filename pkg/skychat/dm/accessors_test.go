// Package dm pkg/skychat/dm/accessors_test.go
//
// The /status-facing accessors and the network predicate that the controller's
// send/receive tests never reach directly.
//
// Conns and Peers back the chat-app's /status output, so they are what an
// operator reads when diagnosing "is anyone actually connected"; NoteInbound is
// how transports that bypass the framed read loop (the CXO feed path) keep
// those counters honest.
package dm

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// dialOnlyClient is a Client whose Dial always succeeds, handing back one end
// of a net.Pipe with the peer end drained. Enough to populate the conn cache
// the accessors report on, without standing up the full memHub fixture.
type dialOnlyClient struct {
	mu      sync.Mutex
	closers []net.Conn
}

func (d *dialOnlyClient) Listen(appnet.Type, routing.Port) (net.Listener, error) {
	return nil, errors.New("dialOnlyClient: Listen unsupported")
}

func (d *dialOnlyClient) Dial(appnet.Addr) (net.Conn, error) {
	local, remote := net.Pipe()
	d.mu.Lock()
	d.closers = append(d.closers, local, remote)
	d.mu.Unlock()
	// net.Pipe is unbuffered: drain the peer end or the first write blocks.
	go func() {
		for {
			if _, err := message.ReadFrame(remote); err != nil {
				return
			}
		}
	}()
	return local, nil
}

func (d *dialOnlyClient) closeAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.closers {
		_ = c.Close() //nolint:errcheck
	}
}

func TestHasNetwork(t *testing.T) {
	c := New(Config{Networks: []appnet.Type{appnet.TypeSkynet}})
	if !c.hasNetwork(appnet.TypeSkynet) {
		t.Error("a configured network should be reported")
	}
	if c.hasNetwork(appnet.TypeDmsg) {
		t.Error("an unconfigured network must not be reported")
	}

	both := New(Config{Networks: []appnet.Type{appnet.TypeSkynet, appnet.TypeDmsg}})
	if !both.hasNetwork(appnet.TypeSkynet) || !both.hasNetwork(appnet.TypeDmsg) {
		t.Error("both configured networks should be reported")
	}

	// A controller with no networks reports none — this is what makes the
	// standalone/TCP-direct path fall through the send loop cleanly.
	if New(Config{}).hasNetwork(appnet.TypeSkynet) {
		t.Error("a controller with no networks must report none")
	}
}

// TestConnsAndPeers drives the accessors through the real conn cache: Serve
// registers a conn keyed by its appnet.Addr, so the counters move without any
// reaching into unexported state.
func TestConnsAndPeers(t *testing.T) {
	cc := &dialOnlyClient{}
	c := New(Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	t.Cleanup(func() {
		_ = c.Close() //nolint:errcheck
		cc.closeAll()
	})

	if n := c.Conns(); n != 0 {
		t.Errorf("fresh controller Conns() = %d, want 0", n)
	}
	if p := c.Peers(); len(p) != 0 {
		t.Errorf("fresh controller Peers() = %v, want empty", p)
	}

	peer, _ := cipher.GenerateKeyPair()
	if err := c.SendRaw(context.Background(), peer, []byte("warm the conn")); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}

	if n := c.Conns(); n != 1 {
		t.Errorf("Conns() = %d, want 1 after a dial", n)
	}
	peers := c.Peers()
	if len(peers) != 1 || peers[0] != peer.Hex() {
		t.Errorf("Peers() = %v, want [%s]", peers, peer.Hex())
	}
	if !c.HasConn(peer) {
		t.Error("HasConn should agree with Peers")
	}

	// Close drops the cache, so /status stops advertising dead peers.
	_ = c.Close() //nolint:errcheck
	if n := c.Conns(); n != 0 {
		t.Errorf("after Close, Conns() = %d, want 0", n)
	}
}

// TestNoteInbound — transports that don't ride the framed read loop (the CXO
// feed) call this so /status counters stay accurate for them too.
func TestNoteInbound(t *testing.T) {
	c := New(Config{})

	before := c.Stats()
	if before.InboundMsgs != 0 {
		t.Fatalf("fresh controller already reports %d inbound", before.InboundMsgs)
	}

	c.NoteInbound()
	c.NoteInbound()

	got := c.Stats()
	if got.InboundMsgs != 2 {
		t.Errorf("InboundMsgs = %d after two NoteInbound calls, want 2", got.InboundMsgs)
	}
	if got.LastRxAt.IsZero() {
		t.Error("NoteInbound should stamp LastRxAt so /status shows recent activity")
	}
}
