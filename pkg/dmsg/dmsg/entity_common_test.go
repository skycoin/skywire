package dmsg

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// newTestEntity builds a bare EntityCommon sufficient to exercise the
// session-map lifecycle (setSession/delSession) without any network I/O.
func newTestEntity() *EntityCommon {
	return &EntityCommon{
		sessions:   make(map[cipher.PubKey]*SessionCommon),
		sessionsMx: new(sync.Mutex),
	}
}

func testSession(pk cipher.PubKey) *SessionCommon {
	// A bare SessionCommon: RemotePK reads rPK, and Close is nil-safe
	// (no smux/yamux set), so no network resources are required.
	return &SessionCommon{rPK: pk}
}

// TestSetSession_NewestWins is the regression guard for the dmsg multihop
// failure: a reconnecting session for a PK that already has one must REPLACE
// the stale predecessor (newest-session-wins), not be rejected. Keeping the
// stale session left the visor "connected" to a server that could no longer
// bridge an inbound stream — dialers hung for HandshakeTimeout and route
// setup failed with "dmsg error 202".
func TestSetSession_NewestWins(t *testing.T) {
	c := newTestEntity()
	pk, _ := cipher.GenerateKeyPair()

	s1 := testSession(pk)
	require.True(t, c.setSession(context.Background(), s1))
	got, ok := c.session(pk)
	require.True(t, ok)
	require.Same(t, s1, got)

	// Reconnect: a second session for the SAME pk must take over.
	s2 := testSession(pk)
	require.True(t, c.setSession(context.Background(), s2))
	got, ok = c.session(pk)
	require.True(t, ok)
	require.Same(t, s2, got, "newest session must replace the stale one")

	// Installing the identical session again is a no-op, still present.
	require.True(t, c.setSession(context.Background(), s2))
	got, _ = c.session(pk)
	require.Same(t, s2, got)
}

// TestDelSession_IdentityChecked guards the other half of newest-session-wins:
// when an evicted stale session's serve goroutine unwinds and calls
// delSession, it must NOT delete the live replacement that already took its
// map slot. Only an identity match (or a nil session) may delete.
func TestDelSession_IdentityChecked(t *testing.T) {
	c := newTestEntity()
	pk, _ := cipher.GenerateKeyPair()

	s1 := testSession(pk)
	s2 := testSession(pk)
	require.True(t, c.setSession(context.Background(), s1))
	require.True(t, c.setSession(context.Background(), s2)) // s2 replaces s1

	// The stale s1's goroutine unwinds and tries to delete by PK — must be
	// a no-op because the map now holds s2, not s1.
	c.delSession(context.Background(), pk, s1)
	got, ok := c.session(pk)
	require.True(t, ok, "stale predecessor delete must not evict the live session")
	require.Same(t, s2, got)

	// The live session deleting itself does remove it.
	c.delSession(context.Background(), pk, s2)
	_, ok = c.session(pk)
	require.False(t, ok)

	// A nil session deletes unconditionally (legacy callers).
	s3 := testSession(pk)
	require.True(t, c.setSession(context.Background(), s3))
	c.delSession(context.Background(), pk, nil)
	_, ok = c.session(pk)
	require.False(t, ok)
}
