// Package clitp cmd/skywire-cli/commands/tp/tp-disc_test.go c4-vis-cli
package clitp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func mkEntry(a, b cipher.PubKey, t string) *transport.Entry {
	return &transport.Entry{Edges: [2]cipher.PubKey{a, b}, Type: types.Type(t)}
}

// TestAggregateVisorTransports covers the per-key summary aggregation behind
// `tp disc -s --pk <pk>`: transports where pk is one of the two edges are
// counted by type, and the distinct PEER visors (the other edge) are collected.
func TestAggregateVisorTransports(t *testing.T) {
	me, _ := cipher.GenerateKeyPair()
	p1, _ := cipher.GenerateKeyPair()
	p2, _ := cipher.GenerateKeyPair()

	// me↔p1 stcpr, p1↔me stcpr (peer p1 twice), me↔p2 sudph.
	entries := []*transport.Entry{
		mkEntry(me, p1, "stcpr"),
		mkEntry(p1, me, "stcpr"),
		mkEntry(me, p2, "sudph"),
	}

	byType, peers := aggregateVisorTransports(entries, me)
	require.Equal(t, 2, byType["stcpr"])
	require.Equal(t, 1, byType["sudph"])
	require.Len(t, byType, 2)
	// Two distinct peers: p1 and p2 (p1 appears on two transports).
	require.Len(t, peers, 2)
	require.Contains(t, peers, p1)
	require.Contains(t, peers, p2)
}

func TestAggregateVisorTransportsEmpty(t *testing.T) {
	me, _ := cipher.GenerateKeyPair()
	byType, peers := aggregateVisorTransports(nil, me)
	require.Empty(t, byType)
	require.Empty(t, peers)
}

// TestCollectKeysByTypeNetworkWide covers `tp disc --type <type>`: both edges
// of every transport of the requested type are collected, de-duplicated and
// sorted; other types are ignored.
func TestCollectKeysByTypeNetworkWide(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()

	entries := []*transport.Entry{
		mkEntry(a, b, "webrtc"),
		mkEntry(b, c, "webrtc"),
		mkEntry(a, c, "stcpr"), // different type, ignored
	}

	pks := collectKeysByType(entries, "webrtc", cipher.PubKey{}, false)
	require.Len(t, pks, 3) // a, b, c
	require.Contains(t, pks, a.Hex())
	require.Contains(t, pks, b.Hex())
	require.Contains(t, pks, c.Hex())
	require.NotContains(t, pks, "") // stcpr edge not folded in via empty key

	// No transports of a type => empty result, not an error.
	require.Empty(t, collectKeysByType(entries, "sudph", cipher.PubKey{}, false))
}

// TestCollectKeysByTypeFiltered covers `tp disc --type <type> --pk <pk>`: only
// the PEER edge of the filter visor's transports of that type is collected.
func TestCollectKeysByTypeFiltered(t *testing.T) {
	me, _ := cipher.GenerateKeyPair()
	p1, _ := cipher.GenerateKeyPair()
	p2, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	entries := []*transport.Entry{
		mkEntry(me, p1, "webrtc"),
		mkEntry(p2, me, "webrtc"),
		mkEntry(me, p1, "stcpr"),     // wrong type
		mkEntry(p1, other, "webrtc"), // doesn't involve me
	}

	pks := collectKeysByType(entries, "webrtc", me, true)
	require.ElementsMatch(t, []string{p1.Hex(), p2.Hex()}, pks)
	require.NotContains(t, pks, me.Hex())    // never includes the filter itself
	require.NotContains(t, pks, other.Hex()) // not a peer of me on webrtc
}
