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
