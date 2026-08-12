// Package transport pkg/transport/transport.go c2-net-transport
// operations.
package transport

import (
	"crypto/sha256"
	"math/big"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// MakeTransportID generates uuid.UUID from pair of keys + type + public
// Generated uuid is:
// - always the same for a given pair
// - GenTransportUUID(keyA,keyB) == GenTransportUUID(keyB, keyA)
func MakeTransportID(keyA, keyB cipher.PubKey, netType types.Type) uuid.UUID {
	tpType := string(netType)
	keys := SortEdges(keyA, keyB)
	b := make([]byte, 33*2+len(tpType))
	i := 0
	i += copy(b[i:], keys[0][:])
	i += copy(b[i:], keys[1][:])
	copy(b[i:], tpType)
	return uuid.NewHash(sha256.New(), uuid.UUID{}, b, 0)
}

// SortEdges sorts keys so that least-significant comes first
func SortEdges(keyA, keyB cipher.PubKey) [2]cipher.PubKey {
	var a, b big.Int
	if a.SetBytes(keyA[:]).Cmp(b.SetBytes(keyB[:])) < 0 {
		return [2]cipher.PubKey{keyA, keyB}
	}
	return [2]cipher.PubKey{keyB, keyA}
}

// TypeFromTransportID determines the transport type by comparing the given
// transport ID against computed IDs for known transport types. A TPID is a
// one-way SHA-256 of (sorted keys ‖ type string) — the type is not stored in a
// recoverable field — so the only way to recover it is to replay MakeTransportID
// for each candidate type (with the endpoint keys) and match. Returns empty
// string if no match is found.
//
// The candidate set is types.Known() (the canonical single source of truth, so
// it never drifts as new transport types are added — the reason the old
// hard-coded {STCPR,SUDPH,STCP,DMSG} list silently returned "" for squicr /
// webrtc / swsr / swtr) plus the legacy pre-rename wire names, since a transport
// an older visor registered under "quic"/"ws"/"wt" was hashed with that string
// and so has a different TPID than its canonical-named equivalent.
func TypeFromTransportID(tpID uuid.UUID, keyA, keyB cipher.PubKey) types.Type {
	for _, t := range types.Known() {
		if MakeTransportID(keyA, keyB, t) == tpID {
			return t
		}
	}
	// Legacy wire names produce distinct IDs; normalize back to canonical so
	// callers see a stable type regardless of which name created the transport.
	for _, t := range []types.Type{types.QUICLegacy, types.QUICLegacy2, types.WSLegacy, types.WTLegacy} {
		if MakeTransportID(keyA, keyB, t) == tpID {
			return types.NormalizeType(t)
		}
	}
	return ""
}
