// Package dmsg pkg/dmsg/dmsg/server_peer_mutual_test.go c2-dmsg-core
package dmsg

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// announceFailHook counts "Failed to announce as peer." warnings — the
// symptom of the mutual-peer flap.
type announceFailHook struct{ n atomic.Int64 }

func (h *announceFailHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h *announceFailHook) Fire(e *logrus.Entry) error {
	if e.Message == "Failed to announce as peer." {
		h.n.Add(1)
	}
	return nil
}

// Two servers that each list the other as a peer must end up with a live,
// acknowledged peer session in BOTH directions, with every announcement
// accepted. Before the fix, the accepting side pre-marked the dialer as a
// peer and then skipped its announcement, which fell through to the
// StreamRequest parser, failed and closed the session; the dialer redialed
// at once and the pair flapped (the old code still converged eventually
// once one side's dial happened to land on a non-flapping window, which is
// why the announce-failure count — not just eventual presence — is asserted).
func TestPeerServers_MutualConfigAnnounceAccepted(t *testing.T) {
	dc := disc.NewMock(0)
	type srv struct {
		pk   cipher.PubKey
		sk   cipher.SecKey
		lis  net.Listener
		addr string
	}
	mk := func(name string) srv {
		pk, sk := GenKeyPair(t, name)
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		return srv{pk: pk, sk: sk, lis: lis, addr: lis.Addr().String()}
	}
	a, b := mk("peer-a"), mk("peer-b")
	confA := DefaultServerConfig()
	confA.Peers = []PeerEntry{{PK: b.pk, Addr: b.addr}}
	confB := DefaultServerConfig()
	confB.Peers = []PeerEntry{{PK: a.pk, Addr: a.addr}}

	hook := &announceFailHook{}
	mkLog := func(name string) *logging.Logger {
		l := logrus.New()
		l.SetLevel(logrus.DebugLevel)
		l.AddHook(hook)
		return &logging.Logger{FieldLogger: l.WithField("_module", name)}
	}
	srvA := NewServer(a.pk, a.sk, dc, confA, nil)
	srvA.SetLogger(mkLog("peer-a"))
	srvB := NewServer(b.pk, b.sk, dc, confB, nil)
	srvB.SetLogger(mkLog("peer-b"))
	go func() { _ = srvA.Serve(a.lis, a.addr) }()            //nolint:errcheck
	go func() { _ = srvB.Serve(b.lis, b.addr) }()            //nolint:errcheck
	t.Cleanup(func() { _ = srvA.Close(); _ = srvB.Close() }) //nolint:errcheck

	hasPeer := func(s *Server, pk cipher.PubKey) bool {
		s.peerSessionsMx.Lock()
		defer s.peerSessionsMx.Unlock()
		ses, ok := s.peerSessions[pk]
		return ok && ses != nil
	}
	require.Eventually(t, func() bool { return hasPeer(srvA, b.pk) && hasPeer(srvB, a.pk) },
		15*time.Second, 100*time.Millisecond, "both sides must hold a peer session")

	// Stability: the sessions must still be the same objects a few seconds on
	// (no close-and-redial churn), and no announcement may have been refused.
	snap := func(s *Server, pk cipher.PubKey) *SessionCommon {
		s.peerSessionsMx.Lock()
		defer s.peerSessionsMx.Unlock()
		return s.peerSessions[pk]
	}
	ab, ba := snap(srvA, b.pk), snap(srvB, a.pk)
	time.Sleep(3 * time.Second)
	require.Same(t, ab, snap(srvA, b.pk), "A's session to B must not have been replaced")
	require.Same(t, ba, snap(srvB, a.pk), "B's session to A must not have been replaced")
	require.Zero(t, hook.n.Load(), "no peer announcement may fail between mutually configured servers")
}
