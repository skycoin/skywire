package node

import (
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// TestHeadDelConnDrainsBroadcastWhileWaiting reproduces the feeds↔head cycle
// seen on dmsg-discovery 2026-09-29: the head loop is parked in
// createFiller → broadcastRoot (waiting for the feeds loop to drain brorq)
// while the feeds loop deletes a closed connection from that head. Before the
// fix nodeHead.delConn only offered delcq, so neither side ever moved.
func TestHeadDelConnDrainsBroadcastWhileWaiting(t *testing.T) {
	fs := &nodeFeeds{
		brorq:  make(chan connRoot),
		closeq: make(chan struct{}),
		fs:     map[cipher.PubKey]*nodeFeed{},
	}
	nh := &nodeHead{
		n:      &nodeFeed{fs: fs},
		delcq:  make(chan *Conn),
		closeq: make(chan struct{}),
	}

	// The head loop: broadcast a Root for a live conn (parks on brorq, which
	// only the feeds loop drains), then serve delcq.
	live := &Conn{closeq: make(chan struct{})}
	gone := &Conn{closeq: make(chan struct{})}
	headDone := make(chan struct{})
	go func() {
		defer close(headDone)
		fs.broadcastRoot(connRoot{c: live, r: &registry.Root{}})
		if got := <-nh.delcq; got != gone {
			t.Errorf("head received %p, want the deleted conn %p", got, gone)
		}
	}()

	// The feeds loop: delete another conn from this head.
	done := make(chan struct{})
	go func() {
		nh.delConn(gone)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("nodeHead.delConn deadlocked against a head parked in broadcastRoot")
	}
	<-headDone
}
