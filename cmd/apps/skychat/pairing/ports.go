// Package pairing — cmd/apps/skychat/pairing/ports.go: deterministic
// DMSG port allocation for chat-pair feeds.
//
// Each ordered pair of visor public keys (Alice, Bob) maps to a stable
// publisher port + subscriber port. Both sides of the pair compute the
// same numbers from public information alone, so no out-of-band port
// negotiation is needed.
//
// Port plan:
//
//   - Publisher port (where Alice's pair-with-Bob feed listens, and
//     where Bob's subscriber dials Alice): in [PubBase, PubBase+PubSpan).
//   - Subscriber port (where Alice's subscriber-to-Bob CXO node binds
//     its inbound listener, since EnableDMSG always Listens): in
//     [SubBase, SubBase+PubSpan), offset by PubSpan from the publisher.
//
// The two ranges don't overlap, and they sit clear of the system DMSG
// port table (everything <300 + reserved ranges around port 80 / 100 /
// 136). Collisions with the reserved set are still possible at the
// boundaries; ResolvePublisherPort walks forward to the next free
// port in PubBase..PubBase+PubSpan and panics on full saturation
// (50000 deterministic slots is far more than any realistic chat-pair
// count, so saturation here is an indication of either a bug or a
// hostile workload).
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
	PubBase uint16 = 10000
	PubSpan uint16 = 25000
	SubBase uint16 = 35000
)

// ReservedPorts is the set of DMSG ports already bound by visor
// subsystems. The pair-feed allocator avoids these so a publisher can
// always Listen and a subscriber's local listen can't shadow a system
// service. Mirrors pkg/visor/cxo_user_feeds.go reservedDmsgPorts; kept
// in sync manually because dragging in pkg/visor here would create an
// import cycle (skychat is invoked by the launcher inside the visor).
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

// PairPorts is the pair of DMSG ports a single side of a chat pair
// uses. Both Alice and Bob compute the same PairPorts struct from the
// same (alice_pk, bob_pk) input.
type PairPorts struct {
	Publisher  uint16
	Subscriber uint16
}

// ComputePairPorts returns the deterministic publisher + subscriber
// ports for the pair (a, b). Symmetric: ComputePairPorts(a, b) ==
// ComputePairPorts(b, a). The caller must already have decided that
// the pair should exist; ComputePairPorts has no side effects.
//
// Returns an error only when every port slot in PubBase..PubBase+PubSpan
// is also in ReservedPorts (impossible with the current static
// reserved table, but checked so future expansions of that table fail
// loudly rather than infinite-loop).
func ComputePairPorts(a, b cipher.PubKey) (PairPorts, error) {
	pub, err := publisherPort(a, b, ReservedPorts())
	if err != nil {
		return PairPorts{}, err
	}
	return PairPorts{
		Publisher:  pub,
		Subscriber: pub + PubSpan,
	}, nil
}

// publisherPort hashes the (min, max) of (a, b) and walks forward
// past any reserved-port collision to the next free slot. Exported
// for tests via ComputePairPorts; kept internal here so the
// reserved-port table is the single configuration knob.
func publisherPort(a, b cipher.PubKey, reserved map[uint16]struct{}) (uint16, error) {
	lo, hi := orderedPair(a, b)
	h := sha256.New()
	h.Write(lo[:]) //nolint:errcheck
	h.Write(hi[:]) //nolint:errcheck
	sum := h.Sum(nil)
	// Use the first 4 bytes of the hash; mod into the publisher span.
	raw := binary.BigEndian.Uint32(sum[:4])
	candidate := PubBase + uint16(raw%uint32(PubSpan))

	// Walk forward through the publisher span until we find a non-
	// reserved slot. Both ends must do the same walk for the result
	// to stay symmetric.
	for offset := uint16(0); offset < PubSpan; offset++ {
		port := PubBase + ((candidate-PubBase)+offset)%PubSpan
		if _, isReserved := reserved[port]; !isReserved {
			// Also avoid the corresponding subscriber slot if it's
			// reserved — keeps Pair.Open simple by guaranteeing both
			// slots are free.
			if _, subReserved := reserved[port+PubSpan]; !subReserved {
				return port, nil
			}
		}
	}
	return 0, fmt.Errorf("pairing: no free publisher port in [%d, %d)", PubBase, PubBase+PubSpan)
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
