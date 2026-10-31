package treestore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cxo/node"
)

func onlyConn(t *testing.T, s *Subscriber) *node.Conn {
	t.Helper()
	cs := s.cxoNode.Connections()
	require.Len(t, cs, 1)
	return cs[0]
}

func kickWatchdog(s *Subscriber) {
	s.reconnectMu.Lock()
	kick := s.reconnectKick
	s.reconnectMu.Unlock()
	kick <- struct{}{}
}

// A repeated fill-break kick must replace the publisher conn, since the
// cached one delivered Roots but not their objects. A first kick keeps it.
func TestReconnectWatchdog_RepeatedFillBreakDropsConn(t *testing.T) {
	rig := newReattachRig(t, 2)
	member, owner := rig.visors[0], rig.visors[1]
	rig.attachSubsTo(t, member)
	sub := member.subs[owner.pk]

	first := onlyConn(t, sub)
	sub.fillBreakStreak.Store(1)
	before := sub.reconnectAttempts.Load()
	kickWatchdog(sub)
	waitFor(t, 5*time.Second, "first kick handled", func() bool { return sub.reconnectAttempts.Load() > before })
	require.Same(t, first, onlyConn(t, sub), "a first kick must reuse the conn")

	sub.fillBreakStreak.Store(2)
	before = sub.reconnectAttempts.Load()
	kickWatchdog(sub)
	waitFor(t, 10*time.Second, "fresh conn after repeated break", func() bool {
		cs := sub.cxoNode.Connections()
		return sub.reconnectAttempts.Load() > before && len(cs) == 1 && cs[0] != first
	})
}
