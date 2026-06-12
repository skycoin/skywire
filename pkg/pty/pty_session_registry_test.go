// Package pty pkg/pty/pty_session_registry_test.go
package pty

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// newTestSession wraps a fakePty in a registered-ready session. Caller stops it.
func newTestSession(t *testing.T) (*ptySession, *fakePty) {
	t.Helper()
	owner, _ := cipher.GenerateKeyPair()
	fp := newFakePty()
	return newPtySession(newSessionID(), owner, fp), fp
}

func TestSessionRegistry_AddGetRemoveCount(t *testing.T) {
	r := newSessionRegistry()
	require.Equal(t, 0, r.count())

	s, fp := newTestSession(t)
	defer func() { _ = fp.Stop() }()
	r.add(s)
	require.Equal(t, 1, r.count())

	got, ok := r.get(s.id)
	require.True(t, ok)
	require.Same(t, s, got)

	_, ok = r.get("nope")
	require.False(t, ok)

	r.remove(s.id)
	require.Equal(t, 0, r.count())
	_, ok = r.get(s.id)
	require.False(t, ok)
}

func TestSessionRegistry_ReapIdleRespectsTTL(t *testing.T) {
	r := newSessionRegistry()

	// Attached session (a follower present) is never reaped, however old.
	attached, fpA := newTestSession(t)
	defer func() { _ = fpA.Stop() }()
	_ = attached.follow() // never detached
	r.add(attached)

	// Detached session: a follower attached then closed, so lastDetach is set.
	detached, fpD := newTestSession(t)
	defer func() { _ = fpD.Stop() }()
	rdr := detached.follow()
	require.NoError(t, rdr.Close())
	r.add(detached)

	now := time.Now()
	// TTL not yet elapsed: nothing reaped.
	require.Equal(t, 0, r.reap(now, time.Hour))
	require.Equal(t, 2, r.count())

	// TTL elapsed for the detached one (idle since ~now); attached stays.
	require.Equal(t, 1, r.reap(now.Add(2*time.Hour), time.Hour))
	require.Equal(t, 1, r.count())
	_, ok := r.get(detached.id)
	require.False(t, ok, "idle-past-TTL detached session should be reaped")
	_, ok = r.get(attached.id)
	require.True(t, ok, "attached session must not be reaped")
}

func TestSessionRegistry_ReapClosedImmediately(t *testing.T) {
	r := newSessionRegistry()
	s, fp := newTestSession(t)
	r.add(s)

	// Exit the shell: the pump closes the session.
	_ = fp.Stop()
	require.Eventually(t, s.isClosed, time.Second, 5*time.Millisecond)

	// A closed session is reaped even with a huge TTL (it can't be reattached).
	require.Equal(t, 1, r.reap(time.Now(), time.Hour))
	require.Equal(t, 0, r.count())
}

func TestNewSessionID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := newSessionID()
		require.NotEmpty(t, id)
		require.False(t, seen[id], "duplicate session id %q", id)
		seen[id] = true
	}
}

func TestRemotePKCtx_RoundTrips(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	ctx := withRemotePK(context.Background(), pk)
	require.Equal(t, pk, remotePKFromCtx(ctx))

	// Absent → zero PK (the local CLI socket case).
	require.Equal(t, cipher.PubKey{}, remotePKFromCtx(context.Background()))
}
