// Package dmsgdisc reconcile_test.go: unit tests for mergeServersByPK, the pure
// static-config ∪ live-registry reconciliation that keeps the discovery's
// dmsg-server session set current (registered address wins over drifted config).
package dmsgdisc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func srvEntry(t *testing.T, addr string) *disc.Entry {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return &disc.Entry{Static: pk, Server: &disc.Server{Address: addr}}
}

func withPK(pk cipher.PubKey, addr string) *disc.Entry {
	return &disc.Entry{Static: pk, Server: &disc.Server{Address: addr}}
}

func addrOf(set []*disc.Entry, pk cipher.PubKey) string {
	for _, e := range set {
		if e.Static == pk {
			return e.Server.Address
		}
	}
	return ""
}

func TestMergeServersByPK(t *testing.T) {
	drifted := srvEntry(t, "1.1.1.1:30081") // same PK, stale static address
	staticOnly := srvEntry(t, "2.2.2.2:30082")
	registeredOnly := srvEntry(t, "3.3.3.3:30083")

	static := []*disc.Entry{
		withPK(drifted.Static, "1.1.1.1:30081"), // stale port in config
		staticOnly,
	}
	registered := []*disc.Entry{
		withPK(drifted.Static, "1.1.1.1:30088"), // server re-registered at :30088
		registeredOnly,
	}

	out := mergeServersByPK(static, registered)

	// union of all distinct PKs, deduped
	require.Len(t, out, 3)
	// drift self-heals: registered (current) address wins over the stale config
	require.Equal(t, "1.1.1.1:30088", addrOf(out, drifted.Static))
	// a static-only server (not yet registered) is retained — cold-start floor
	require.Equal(t, "2.2.2.2:30082", addrOf(out, staticOnly.Static))
	// a registered-only server (registered after cold-start) is picked up
	require.Equal(t, "3.3.3.3:30083", addrOf(out, registeredOnly.Static))
}

func TestMergeServersByPK_EmptyRegistry(t *testing.T) {
	// Registry empty / unreachable (cold-start, or fetch error → nil): fall back
	// to exactly the static seed set so bootstrap still works.
	s1 := srvEntry(t, "1.1.1.1:30081")
	s2 := srvEntry(t, "2.2.2.2:30082")
	out := mergeServersByPK([]*disc.Entry{s1, s2}, nil)
	require.Len(t, out, 2)
	require.Equal(t, "1.1.1.1:30081", addrOf(out, s1.Static))
}

func TestMergeServersByPK_SkipsNilAndServerless(t *testing.T) {
	good := srvEntry(t, "1.1.1.1:30081")
	out := mergeServersByPK(
		[]*disc.Entry{nil, good},
		[]*disc.Entry{nil, {Static: cipher.PubKey{}, Server: nil}}, // serverless registry entry ignored
	)
	require.Len(t, out, 1)
	require.Equal(t, "1.1.1.1:30081", addrOf(out, good.Static))
}
