// Package arfeed — arfeed_test.go pins the properties a Preview reader depends
// on: every level is a dense, index-addressable run of names derived from the
// public key alone, those names actually spread across the key space, and a
// bucket body round-trips through the version-framed codec.
package arfeed

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestBucketIndicesMatchSortedPositions is the invariant the whole design
// rests on: Indices(pk)[lvl] must equal the position of Segments(pk)[lvl] in
// that level's sorted name list, because that is the index the reader hands to
// registry.Refs.ValueByIndex instead of scanning the level.
func TestBucketIndicesMatchSortedPositions(t *testing.T) {
	// BucketPathAt enumerates the tree in the order treestore sorts it. Check
	// that each LEVEL is a dense ascending run of Fanout names, then that a
	// key's computed indices address its own bucket.
	seen := make(map[string]bool, BucketCount)
	for i := 0; i < BucketCount; i++ {
		path := BucketPathAt(i)
		segs := strings.Split(path, "/")
		require.Len(t, segs, Levels)
		for _, s := range segs {
			require.Len(t, s, 1)
		}
		require.False(t, seen[path], "duplicate bucket path %s", path)
		seen[path] = true
	}
	require.Len(t, seen, BucketCount)

	for i := 0; i < 500; i++ {
		pk, _ := cipher.GenerateKeyPair()
		segs, indices := Segments(pk), Indices(pk)
		require.Len(t, segs, Levels)
		for lvl := range segs {
			require.GreaterOrEqual(t, indices[lvl], 0)
			require.Less(t, indices[lvl], Fanout)
			// The index must be the position of this segment among the Fanout
			// names sorted ascending — that is the whole basis for addressing
			// a child by index instead of scanning for it.
			require.Equal(t, segs[lvl], fmt.Sprintf("%x", indices[lvl]))
		}
		require.True(t, seen[BucketPath(pk)], "%s addresses no published bucket", pk.Hex())
	}
}

// TestBucketsSpreadAcrossKeySpace guards the offset. A skywire public key is a
// compressed secp256k1 point, so its leading byte is always 0x02 or 0x03:
// naming a bucket after hex[0:2] puts the entire network into two buckets and
// silently turns every "one indexed fetch" into a whole-network download. This
// is not hypothetical — it is what the first version of this file did, and the
// 18 KB bucket leaf is what exposed it.
func TestBucketsSpreadAcrossKeySpace(t *testing.T) {
	const n = 4000
	occupied := make(map[string]int)
	for i := 0; i < n; i++ {
		pk, _ := cipher.GenerateKeyPair()
		occupied[BucketPath(pk)]++
	}
	require.Greater(t, len(occupied), BucketCount*3/4,
		"bucket names must spread across the key space, got only %d distinct of %d",
		len(occupied), BucketCount)

	worst := 0
	for _, c := range occupied {
		if c > worst {
			worst = c
		}
	}
	// Uniform bucketing puts ~n/256 in a bucket; allow generous slack for the
	// tail of the multinomial while still failing hard on a degenerate split.
	require.Less(t, worst, 4*n/BucketCount+20,
		"worst-case bucket occupancy %d suggests the bucket key is not uniform", worst)
}

func TestBucketRoundTrip(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	peers := map[cipher.PubKey]*PeerBindings{
		pkA: {STCPR: &addrresolver.VisorData{RemoteAddr: "1.2.3.4:5000"}},
		pkB: {SUDPH: &addrresolver.VisorData{RemoteAddr: "5.6.7.8:6000", RemoteAddrV6: "[2001:db8::1]:6000"}},
	}
	blob, err := EncodeBucket(peers)
	require.NoError(t, err)

	got, err := DecodeBucket(blob)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "1.2.3.4:5000", got[pkA.Hex()].Get(types.STCPR).RemoteAddr)
	require.Nil(t, got[pkA.Hex()].Get(types.SUDPH))
	require.Equal(t, "[2001:db8::1]:6000", got[pkB.Hex()].Get(types.SUDPH).RemoteAddrV6)
}

// TestEncodeBucketIsDeterministic matters because CXO is content-addressed: an
// unchanged bucket must re-encode to identical bytes so a republish is a wire
// no-op rather than a fresh object every 30 seconds.
func TestEncodeBucketIsDeterministic(t *testing.T) {
	peers := map[cipher.PubKey]*PeerBindings{}
	for i := 0; i < 32; i++ {
		pk, _ := cipher.GenerateKeyPair()
		peers[pk] = &PeerBindings{
			STCPR: &addrresolver.VisorData{RemoteAddr: "10.0.0.1:1"},
			WT:    &addrresolver.VisorData{RemoteAddr: "10.0.0.2:2"},
		}
	}
	first, err := EncodeBucket(peers)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		again, err := EncodeBucket(peers)
		require.NoError(t, err)
		require.Equal(t, first, again, "re-encode of unchanged content must be byte-identical")
	}
}

func TestDecodeBucketRejectsUnknownVersion(t *testing.T) {
	blob, err := EncodeBucket(nil)
	require.NoError(t, err)
	blob[0] = Version + 1
	_, err = DecodeBucket(blob)
	require.ErrorIs(t, err, ErrBadVersion)
}

func TestEmptyBucketDecodesToNothing(t *testing.T) {
	blob, err := EncodeBucket(nil)
	require.NoError(t, err)
	got, err := DecodeBucket(blob)
	require.NoError(t, err)
	require.Empty(t, got)
}

// A record with no binding at all is dropped rather than published as an empty
// object, so a reader never sees a peer it cannot dial.
func TestEmptyRecordsAreOmitted(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	blob, err := EncodeBucket(map[cipher.PubKey]*PeerBindings{pk: {}})
	require.NoError(t, err)
	got, err := DecodeBucket(blob)
	require.NoError(t, err)
	require.Empty(t, got)
}
