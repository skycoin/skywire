package dmsg

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func mkPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// TestPickIdleSessionsToReap covers the idle-session reaper's decision policy:
// only streamless sessions that stayed idle for >= idleChecksToReap consecutive
// checks are reaped, never below the MinSessions floor, and busy/unknown sessions
// are kept. The idleStreak map is advanced across calls.
func TestPickIdleSessionsToReap(t *testing.T) {
	const min = 2
	const thresh = 2

	// Two idle + one busy, floor 2. Surplus = 1. First call: streaks reach 1
	// (< thresh) → nothing yet.
	a, b, c := mkPK(t), mkPK(t), mkPK(t)
	streams := map[cipher.PubKey]int{a: 0, b: 0, c: 3}
	streak := map[cipher.PubKey]int{}

	require.Empty(t, pickIdleSessionsToReap(streams, streak, min, thresh), "first check: below streak threshold")
	require.Equal(t, 1, streak[a])
	require.Equal(t, 1, streak[b])
	require.Equal(t, 0, streak[c], "busy session streak stays 0")

	// Second call: idle streaks reach 2 (== thresh). 3 sessions, floor 2 →
	// surplus 1 → exactly ONE idle session reaped (the busy one is never a
	// candidate, and the floor is respected).
	reap := pickIdleSessionsToReap(streams, streak, min, thresh)
	require.Len(t, reap, 1, "reap only the surplus above MinSessions")
	require.NotEqual(t, c, reap[0], "never reap a busy session")
	_, streakKept := streak[reap[0]]
	require.False(t, streakKept, "a reaped server's idle streak is cleared so a re-dialed session gets a fresh grace period")

	// A busy session resets its streak.
	streams2 := map[cipher.PubKey]int{a: 0, b: 5}
	streak2 := map[cipher.PubKey]int{a: 1, b: 3}
	pickIdleSessionsToReap(streams2, streak2, min, thresh)
	require.Equal(t, 0, streak2[b], "streak reset when the session has streams")

	// Never reap below the floor: total == min → nothing, even if all idle.
	streams3 := map[cipher.PubKey]int{a: 0, b: 0}
	streak3 := map[cipher.PubKey]int{a: 9, b: 9}
	require.Empty(t, pickIdleSessionsToReap(streams3, streak3, min, thresh), "at the floor, keep all")

	// Unknown stream count (-1, e.g. quic) is treated as busy → never reaped,
	// and doesn't accrue an idle streak.
	streams4 := map[cipher.PubKey]int{a: -1, b: -1, c: -1}
	streak4 := map[cipher.PubKey]int{}
	require.Empty(t, pickIdleSessionsToReap(streams4, streak4, 1, 1), "unknown counts are busy")
	for _, v := range streak4 {
		require.Equal(t, 0, v, "unknown/busy sessions never accrue an idle streak")
	}

	// Vanished sessions are pruned from the streak map.
	gone := mkPK(t)
	streak5 := map[cipher.PubKey]int{gone: 5, a: 1}
	pickIdleSessionsToReap(map[cipher.PubKey]int{a: 0}, streak5, 1, 5)
	_, stillThere := streak5[gone]
	require.False(t, stillThere, "streak pruned for a session no longer present")
}

// TestReapedSessionFlag pins the marker the reaper sets before closing a
// session. The serve goroutine's exit path reads it to skip the errCh wake and
// the lost-server note that would otherwise re-dial the server just trimmed —
// observed live as one reap and one re-dial of the same server every minute.
func TestReapedSessionFlag(t *testing.T) {
	var nilSes *SessionCommon
	require.False(t, nilSes.wasReaped(), "a nil session is never reaped")

	sc := &SessionCommon{}
	require.False(t, sc.wasReaped(), "fresh session is not reaped")
	sc.markReaped()
	require.True(t, sc.wasReaped(), "flag sticks once set")
}
