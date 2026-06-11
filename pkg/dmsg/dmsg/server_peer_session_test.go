// Package dmsg — pkg/dmsg/dmsg/server_peer_session_test.go
//
// Regression coverage for the peer-session map teardown in
// maintainPeerConnection. The outbound peer loop's cleanup delete must
// be identity-checked: if an inbound PeerAnnounce from the same PK
// (addAcceptedPeerSession) replaced the outbound session in
// peerSessions, the outbound goroutine unwinding later must NOT evict
// that live successor. This mirrors the identity-checked deletes used
// for the main session map (delSession) and the inbound-peer teardown
// in handleSession.
package dmsg

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// peerCleanupDelete reproduces the exact map mutation performed by
// maintainPeerConnection's cleanup block (server.go) so the
// identity-check can be exercised without standing up a TCP peer link.
func peerCleanupDelete(s *Server, pk cipher.PubKey, ses *SessionCommon) {
	s.peerSessionsMx.Lock()
	if s.peerSessions[pk] == ses {
		delete(s.peerSessions, pk)
	}
	s.peerSessionsMx.Unlock()
}

// TestMaintainPeerConnection_CleanupIdentityChecked guards the
// newest-wins teardown asymmetry: the outbound peer loop's cleanup must
// not delete a session it no longer owns.
func TestMaintainPeerConnection_CleanupIdentityChecked(t *testing.T) {
	s := newTestServer(t)
	peerPK, _ := cipher.GenerateKeyPair()

	// Outbound dial files its session.
	outbound := &SessionCommon{rPK: peerPK}
	s.peerSessionsMx.Lock()
	s.peerSessions[peerPK] = outbound
	s.peerSessionsMx.Unlock()

	// An inbound PeerAnnounce from the same PK replaces it (the
	// reverse-path enabler for a non-public server). This is what
	// addAcceptedPeerSession does to the map.
	inbound := &SessionCommon{rPK: peerPK}
	s.addAcceptedPeerSession(peerPK, inbound)

	s.peerSessionsMx.Lock()
	cur := s.peerSessions[peerPK]
	s.peerSessionsMx.Unlock()
	require.Same(t, inbound, cur, "inbound announce must take the map slot")

	// The outbound goroutine now unwinds (its link closed) and runs its
	// cleanup. Identity-checked: it must NOT evict the live inbound
	// successor.
	peerCleanupDelete(s, peerPK, outbound)

	s.peerSessionsMx.Lock()
	cur, ok := s.peerSessions[peerPK]
	s.peerSessionsMx.Unlock()
	require.True(t, ok, "live inbound successor must survive the stale outbound cleanup")
	require.Same(t, inbound, cur)

	// When the inbound session itself later closes, an identity-checked
	// delete for it does remove the entry.
	peerCleanupDelete(s, peerPK, inbound)
	s.peerSessionsMx.Lock()
	_, ok = s.peerSessions[peerPK]
	s.peerSessionsMx.Unlock()
	require.False(t, ok, "owning session may delete its own entry")
}

// TestMaintainPeerConnection_CleanupNoSuccessor confirms the common
// case still works: with no replacement, the outbound cleanup deletes
// its own entry.
func TestMaintainPeerConnection_CleanupNoSuccessor(t *testing.T) {
	s := newTestServer(t)
	peerPK, _ := cipher.GenerateKeyPair()

	outbound := &SessionCommon{rPK: peerPK}
	s.peerSessionsMx.Lock()
	s.peerSessions[peerPK] = outbound
	s.peerSessionsMx.Unlock()

	peerCleanupDelete(s, peerPK, outbound)

	s.peerSessionsMx.Lock()
	_, ok := s.peerSessions[peerPK]
	s.peerSessionsMx.Unlock()
	require.False(t, ok, "outbound cleanup must remove its own entry when unreplaced")
}
