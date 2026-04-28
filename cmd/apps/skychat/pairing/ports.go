// Package pairing — cmd/apps/skychat/pairing/ports.go: deterministic
// DMSG port allocation for chat-pair feeds.
//
// Each unordered pair of visor public keys (Alice, Bob) maps to a
// stable DMSG port. Both sides of the pair compute the same number
// from public information alone, so no out-of-band port negotiation
// is needed.
//
// Port plan: one port per pair, in [PortBase, PortBase+PortSpan).
// Each side's CXO node listens on this single port and hosts both
// roles — Alice publishes her own pair-with-Bob feed and subscribes
// to Bob's pair-with-Alice feed on the same node, with Bob's node
// doing the symmetric thing on the same port number on his side.
// One port suffices because publisher and subscriber operate on
// distinct feed PKs (each side's own PK), so the CXO node multiplexes
// them on the single DMSG conn.
//
// The range sits clear of the system DMSG port table (everything
// <300 plus reserved ports around 80 / 100 / 136). Collisions with
// the reserved set are walked past so a pair can always Listen.
package pairing

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Port-allocation tunables. Public constants so callers (e.g. the
// visor's CXO user-feed registry, which needs to reject overlapping
// port allocations) can validate against the same numbers.
const (
	PortBase uint16 = 10000
	PortSpan uint16 = 50000
)

// ReservedPorts is the set of DMSG ports already bound by visor
// subsystems. The pair-feed allocator avoids these so a publisher can
// always Listen. Mirrors pkg/visor/cxo_user_feeds.go reservedDmsgPorts;
// kept in sync manually because dragging in pkg/visor here would create
// an import cycle (skychat is invoked by the launcher inside the visor).
func ReservedPorts() map[uint16]struct{} {
	return map[uint16]struct{}{
		skyenv.DmsgCtrlPort:                  {},
		skyenv.DmsgPingPort:                  {},
		skyenv.DmsgPtyPort:                   {},
		skyenv.DmsgSetupPort:                 {},
		skyenv.DmsgHypervisorPort:            {},
		skyenv.DmsgTransportSetupPort:        {},
		skyenv.DmsgTransportSetupServicePort: {},
		skyenv.DmsgGRPCPort:                  {},
		skyenv.DmsgCXOPort:                   {},
		80:                                   {}, // dmsg-http
		skyenv.DmsgDHTPort:                   {},
		skyenv.DmsgAwaitSetupPort:            {},
	}
}

// ComputePairPort returns the deterministic DMSG port for the chat
// pair (a, b). Symmetric: ComputePairPort(a, b) == ComputePairPort(b, a).
//
// Returns an error only when every port slot in the span is also in
// ReservedPorts (impossible with the current static table, but
// checked so future expansions of that table fail loudly rather than
// infinite-loop).
func ComputePairPort(a, b cipher.PubKey) (uint16, error) {
	return pairPort(a, b, ReservedPorts())
}

// pairPort hashes the (min, max) of (a, b) and walks forward past any
// reserved-port collision to the next free slot. Both ends do the
// same walk so the result stays symmetric.
func pairPort(a, b cipher.PubKey, reserved map[uint16]struct{}) (uint16, error) {
	lo, hi := orderedPair(a, b)
	h := sha256.New()
	h.Write(lo[:]) //nolint:errcheck
	h.Write(hi[:]) //nolint:errcheck
	sum := h.Sum(nil)
	raw := binary.BigEndian.Uint32(sum[:4])
	candidate := PortBase + uint16(raw%uint32(PortSpan))

	for offset := uint16(0); offset < PortSpan; offset++ {
		port := PortBase + ((candidate-PortBase)+offset)%PortSpan
		if _, isReserved := reserved[port]; !isReserved {
			return port, nil
		}
	}
	return 0, fmt.Errorf("pairing: no free port in [%d, %d)", PortBase, PortBase+PortSpan)
}

// orderedPair returns (a, b) sorted byte-lexicographically so the
// hash input is canonical regardless of which side computes it.
func orderedPair(a, b cipher.PubKey) (cipher.PubKey, cipher.PubKey) {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return a, b
		}
		if a[i] > b[i] {
			return b, a
		}
	}
	return a, b
}
