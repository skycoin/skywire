// Package store pkg/dmsg/discovery/store/entry_stale_test.go c1-net-dmsg
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func TestServerEntryIsStale(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) int64 { return now.Add(d).UnixNano() }

	tt := []struct {
		name  string
		entry *disc.Entry
		stale bool
	}{
		{"nil entry", nil, false},
		{"client-only entry is never a stale server",
			&disc.Entry{Client: &disc.Client{}, Timestamp: at(-time.Hour)}, false},
		{"zero timestamp is never stale",
			&disc.Entry{Server: &disc.Server{}, Timestamp: 0}, false},
		{"fresh registration",
			&disc.Entry{Server: &disc.Server{}, Timestamp: at(-30 * time.Second)}, false},
		{"just inside the bound",
			&disc.Entry{Server: &disc.Server{}, Timestamp: at(-serverEntryStaleAfter + time.Second)}, false},
		{"past the bound",
			&disc.Entry{Server: &disc.Server{}, Timestamp: at(-serverEntryStaleAfter - time.Second)}, true},
		{"the production case: frozen for 20 minutes",
			&disc.Entry{Server: &disc.Server{}, Timestamp: at(-20 * time.Minute)}, true},
		{"a dual entry whose registration froze is stale as a server",
			&disc.Entry{Client: &disc.Client{}, Server: &disc.Server{}, Timestamp: at(-20 * time.Minute)}, true},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.stale, serverEntryIsStale(tc.entry, now))
		})
	}
}

// Entry.Timestamp is written in two units in this codebase — disc.PutEntry
// stamps UnixNano, several constructors and tests use Unix seconds. Reading a
// seconds value as nanoseconds dates the entry to 1970, which would withdraw a
// live server from discovery permanently.
func TestServerEntryStaleAcceptsBothTimestampUnits(t *testing.T) {
	now := time.Now()
	srv := func(ts int64) *disc.Entry {
		return &disc.Entry{Server: &disc.Server{}, Timestamp: ts}
	}

	t.Run("fresh in nanoseconds", func(t *testing.T) {
		require.False(t, serverEntryIsStale(srv(now.UnixNano()), now))
	})
	t.Run("fresh in seconds", func(t *testing.T) {
		require.False(t, serverEntryIsStale(srv(now.Unix()), now))
	})
	t.Run("stale in nanoseconds", func(t *testing.T) {
		require.True(t, serverEntryIsStale(srv(now.Add(-time.Hour).UnixNano()), now))
	})
	t.Run("stale in seconds", func(t *testing.T) {
		require.True(t, serverEntryIsStale(srv(now.Add(-time.Hour).Unix()), now))
	})
	t.Run("the unit boundary is not a date any entry can carry", func(t *testing.T) {
		// Just under the ceiling read as seconds is the year 33658 — far in
		// the future, so definitively not stale either way.
		require.False(t, serverEntryIsStale(srv(secondsTimestampCeiling-1), now))
	})
}

// Folding a dmsg server into its visor puts both halves on ONE Redis key, so
// the server's short TTL silently became the visor's CLIENT registration TTL
// too — sixty minutes down to two. This pins the rule that only a server-ONLY
// entry takes the short expiry.
func TestDualEntryKeepsTheClientTTL(t *testing.T) {
	short := func(e *disc.Entry) bool { return e.Server != nil && e.Client == nil }

	require.True(t, short(&disc.Entry{Server: &disc.Server{}}),
		"a server-only entry must expire on the server cadence")
	require.False(t, short(&disc.Entry{Client: &disc.Client{}}),
		"a client-only entry keeps the client TTL")
	require.False(t, short(&disc.Entry{Client: &disc.Client{}, Server: &disc.Server{}}),
		"a dual entry must keep the CLIENT TTL: its server half is retired by "+
			"serverEntryIsStale, not by the key expiring under its client half")
}
