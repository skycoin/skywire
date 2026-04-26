// Package router pkg/router/cascade_route.go
//
// createRouteGroupCascade implements the two-phase cascade protocol
// for route setup: reserve IDs from each hop, then install rules.
// Falls back to DMSG-based setup on failure.
package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// createRouteGroupCascade implements the cascade route setup protocol.
// It performs two phases:
//  1. Reserve: cascade reserve messages along forward and reverse paths,
//     collecting route IDs from each hop.
//  2. Install: compute routing rules using the reserved IDs, then cascade
//     install messages to install rules on each hop.
//
// The cascade messages flow over the route's own transports using route ID 0,
// eliminating the need for DMSG connections to each hop.
func createRouteGroupCascade(
	ctx context.Context,
	log logrus.FieldLogger,
	cb *CascadeBuilder,
	biRt routing.BidirectionalRoute,
) (routing.EdgeRules, error) {
	log.Info("Attempting cascade route setup")

	fwdRt, revRt := biRt.ForwardAndReverse()

	// Determine how many route IDs each hop needs.
	// Same logic as NewIDReserver: count occurrences of each PK across both paths.
	reserveCount := computeReserveCount(biRt.Forward, biRt.Reverse)

	// --- Phase 1: Reserve route IDs ---

	// Build per-hop reserve messages with the correct reserveN for each hop.
	fwdSessionID, fwdReservePayload, err := buildReserveWithCounts(cb, biRt.Forward, reserveCount)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: build fwd reserve: %w", err)
	}

	// Find the transport connecting us to the first hop.
	if len(biRt.Forward) == 0 {
		return routing.EdgeRules{}, fmt.Errorf("cascade: no forward hops")
	}
	firstFwdTpID := biRt.Forward[0].TpID

	fwdAck, err := cb.SendCascade(ctx, firstFwdTpID, fwdSessionID, fwdReservePayload, cb.reserveTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve: %w", err)
	}
	if fwdAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve rejected: %s", fwdAck.Error)
	}

	// Reverse path reserve.
	revSessionID, revReservePayload, err := buildReserveWithCounts(cb, biRt.Reverse, reserveCount)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: build rev reserve: %w", err)
	}

	firstRevTpID := biRt.Reverse[0].TpID
	revAck, err := cb.SendCascade(ctx, firstRevTpID, revSessionID, revReservePayload, cb.reserveTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve: %w", err)
	}
	if revAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve rejected: %s", revAck.Error)
	}

	// --- Reconstruct IDReserver from cascade ACKs ---
	idR, err := newCascadeIDReserver(biRt.Forward, biRt.Reverse, fwdAck.RouteIDs, revAck.RouteIDs)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: reconstruct IDs: %w", err)
	}

	// --- Generate rules (same as DMSG path) ---
	fwdRules, revRules, interRules, err := GenerateRules(idR, []routing.Route{fwdRt, revRt})
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: generate rules: %w", err)
	}

	srcPK := biRt.Desc.Src()
	dstPK := biRt.Desc.Dst()
	initEdge := routing.EdgeRules{
		Desc:    revRt.Desc,
		Forward: fwdRules[srcPK.String()][0],
		Reverse: revRules[srcPK.String()][0],
	}
	respEdge := routing.EdgeRules{
		Desc:    fwdRt.Desc,
		Forward: fwdRules[dstPK.String()][0],
		Reverse: revRules[dstPK.String()][0],
	}

	// --- Phase 2: Install rules ---

	// Collect all rules per PK for the cascade install.
	rulesPerPK := collectRulesPerPK(fwdRules, revRules, interRules, initEdge, respEdge)

	// Forward path install.
	fwdInstallPayload, err := cb.BuildInstallMessage(biRt.Forward, fwdSessionID, rulesPerPK)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: build fwd install: %w", err)
	}
	fwdInstAck, err := cb.SendCascade(ctx, firstFwdTpID, fwdSessionID, fwdInstallPayload, cb.installTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd install: %w", err)
	}
	if fwdInstAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd install rejected: %s", fwdInstAck.Error)
	}

	// Reverse path install.
	revInstallPayload, err := cb.BuildInstallMessage(biRt.Reverse, revSessionID, rulesPerPK)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: build rev install: %w", err)
	}
	revInstAck, err := cb.SendCascade(ctx, firstRevTpID, revSessionID, revInstallPayload, cb.installTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev install: %w", err)
	}
	if revInstAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev install rejected: %s", revInstAck.Error)
	}

	log.Info("Cascade route setup succeeded")
	return initEdge, nil
}

// computeReserveCount determines how many route IDs each PK needs across both paths.
func computeReserveCount(forward, reverse []routing.Hop) map[cipher.PubKey]uint8 {
	counts := make(map[cipher.PubKey]uint8)
	for _, hops := range [][]routing.Hop{forward, reverse} {
		if len(hops) == 0 {
			continue
		}
		counts[hops[0].From]++
		for _, hop := range hops {
			counts[hop.To]++
		}
	}
	return counts
}

// buildReserveWithCounts builds a nested reserve cascade with per-hop reserveN.
func buildReserveWithCounts(cb *CascadeBuilder, hops []routing.Hop, counts map[cipher.PubKey]uint8) (uint64, []byte, error) {
	if len(hops) == 0 {
		return 0, nil, fmt.Errorf("no hops")
	}

	sessionID := cb.nextSessionID()
	var nonce uint64

	// Build target list: source + each To
	type hopTarget struct {
		pk        cipher.PubKey
		relayTpID [16]byte
	}
	targets := make([]hopTarget, 0, len(hops)+1)

	// First target is the source (hops[0].From)
	var firstRelay [16]byte
	copy(firstRelay[:], hops[0].TpID[:])
	targets = append(targets, hopTarget{pk: hops[0].From, relayTpID: firstRelay})

	for i, hop := range hops {
		var relay [16]byte
		if i < len(hops)-1 {
			copy(relay[:], hops[i+1].TpID[:])
		}
		targets = append(targets, hopTarget{pk: hop.To, relayTpID: relay})
	}

	// Build nested from last to first.
	var innerPayload []byte
	for i := len(targets) - 1; i >= 0; i-- {
		nonce++
		reserveN := counts[targets[i].pk]
		if reserveN == 0 {
			reserveN = 1
		}

		var relayID [16]byte
		copy(relayID[:], targets[i].relayTpID[:])

		msg := &routing.CascadeSetup{
			Phase:     routing.CascadePhaseReserve,
			SessionID: sessionID,
			RSNPK:     cb.rsnPK,
			Nonce:     nonce,
			ReserveN:  reserveN,
			Payload:   innerPayload,
		}
		copy(msg.RelayTpID[:], relayID[:])

		if err := msg.Sign(targets[i].pk, cb.rsnSK); err != nil {
			return 0, nil, fmt.Errorf("sign hop %d: %w", i, err)
		}
		data, err := msg.Marshal()
		if err != nil {
			return 0, nil, fmt.Errorf("marshal hop %d: %w", i, err)
		}
		innerPayload = data
	}

	return sessionID, innerPayload, nil
}

// cascadeIDReserver implements IDReserver for the cascade protocol.
// It's populated from the cascade ACK route IDs instead of dialing each hop.
type cascadeIDReserver struct {
	ids map[cipher.PubKey][]routing.RouteID
}

func newCascadeIDReserver(forward, reverse []routing.Hop, fwdIDs, revIDs []routing.RouteID) (*cascadeIDReserver, error) {
	r := &cascadeIDReserver{ids: make(map[cipher.PubKey][]routing.RouteID)}

	// Distribute forward path IDs to PKs in order.
	fwdPKs := hopPKs(forward)
	fwdIdx := 0
	for _, pk := range fwdPKs {
		if fwdIdx >= len(fwdIDs) {
			return nil, fmt.Errorf("not enough forward route IDs")
		}
		r.ids[pk] = append(r.ids[pk], fwdIDs[fwdIdx])
		fwdIdx++
	}

	// Distribute reverse path IDs.
	revPKs := hopPKs(reverse)
	revIdx := 0
	for _, pk := range revPKs {
		if revIdx >= len(revIDs) {
			return nil, fmt.Errorf("not enough reverse route IDs")
		}
		r.ids[pk] = append(r.ids[pk], revIDs[revIdx])
		revIdx++
	}

	return r, nil
}

func hopPKs(hops []routing.Hop) []cipher.PubKey {
	if len(hops) == 0 {
		return nil
	}
	pks := []cipher.PubKey{hops[0].From}
	for _, h := range hops {
		pks = append(pks, h.To)
	}
	return pks
}

func (r *cascadeIDReserver) ReserveIDs(_ context.Context) error { return nil } // already reserved
func (r *cascadeIDReserver) TotalIDs() int {
	total := 0
	for _, ids := range r.ids {
		total += len(ids)
	}
	return total
}

func (r *cascadeIDReserver) PopID(pk cipher.PubKey) (routing.RouteID, bool) {
	ids, ok := r.ids[pk]
	if !ok || len(ids) == 0 {
		return 0, false
	}
	r.ids[pk] = ids[1:]
	return ids[0], true
}

func (r *cascadeIDReserver) Client(_ cipher.PubKey) *Client { return nil }
func (r *cascadeIDReserver) Close() error                   { return nil }
func (r *cascadeIDReserver) ReturnToPool(_ *ClientPool)     {}
func (r *cascadeIDReserver) String() string                 { return "cascadeIDReserver" }

// collectRulesPerPK gathers all rules that should be installed on each visor PK.
func collectRulesPerPK(fwdRules, revRules, interRules RulesMap, initEdge, respEdge routing.EdgeRules) map[cipher.PubKey][]routing.Rule {
	result := make(map[cipher.PubKey][]routing.Rule)

	// Add initiating edge rules.
	srcPK := initEdge.Desc.DstPK() // initEdge.Desc is the reverse desc, so DstPK = source
	result[srcPK] = append(result[srcPK], initEdge.Forward, initEdge.Reverse)

	// Add responding edge rules.
	dstPK := respEdge.Desc.DstPK()
	result[dstPK] = append(result[dstPK], respEdge.Forward, respEdge.Reverse)

	// Add intermediary rules.
	for addr, rules := range interRules {
		pk := cascadePKFromAddr(addr)
		result[pk] = append(result[pk], rules...)
	}

	// Also add forward/reverse rules for non-edge hops (they get forward rules too).
	for addr, rules := range fwdRules {
		pk := cascadePKFromAddr(addr)
		if pk != srcPK && pk != dstPK {
			result[pk] = append(result[pk], rules...)
		}
	}
	for addr, rules := range revRules {
		pk := cascadePKFromAddr(addr)
		if pk != srcPK && pk != dstPK {
			result[pk] = append(result[pk], rules...)
		}
	}

	return result
}

// cascadePKFromAddr extracts the PK from a "pk:port" address string.
func cascadePKFromAddr(addr string) cipher.PubKey {
	pkStr := addr
	if idx := strings.Index(addr, ":"); idx >= 0 {
		pkStr = addr[:idx]
	}
	var pk cipher.PubKey
	if err := pk.Set(pkStr); err != nil {
		return cipher.PubKey{}
	}
	return pk
}
