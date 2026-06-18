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

	// Both cascades are injected source-first; the reverse path is re-oriented
	// to start at the source (see signReserveCascades for the rationale). Each
	// cascade reserves its own path's per-node count.
	if len(biRt.Forward) == 0 {
		return routing.EdgeRules{}, fmt.Errorf("cascade: no forward hops")
	}
	revInject := reverseHopsFromSource(biRt.Reverse)

	// --- Phase 1: Reserve route IDs ---

	fwdSessionID, fwdReservePayload, err := buildReserveWithCounts(cb, biRt.Forward, singlePathReserveCount(biRt.Forward))
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: build fwd reserve: %w", err)
	}

	firstFwdTpID := biRt.Forward[0].TpID

	fwdAck, err := cb.SendCascade(ctx, firstFwdTpID, fwdSessionID, fwdReservePayload, cb.reserveTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve: %w", err)
	}
	if fwdAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve rejected: %s", fwdAck.Error)
	}

	// Reverse path reserve (source-oriented).
	revSessionID, revReservePayload, err := buildReserveWithCounts(cb, revInject, singlePathReserveCount(revInject))
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: build rev reserve: %w", err)
	}

	if len(revInject) == 0 {
		return routing.EdgeRules{}, fmt.Errorf("cascade: no reverse hops")
	}
	firstRevTpID := revInject[0].TpID
	revAck, err := cb.SendCascade(ctx, firstRevTpID, revSessionID, revReservePayload, cb.reserveTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve: %w", err)
	}
	if revAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve rejected: %s", revAck.Error)
	}

	// --- Reconstruct IDReserver from cascade ACKs (reverse re-oriented) ---
	idR, err := newCascadeIDReserver(biRt.Forward, revInject, fwdAck.RouteIDs, revAck.RouteIDs)
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

	// Forward path install — terminal is the responding destination edge.
	fwdInstallPayload, err := cb.BuildInstallMessage(biRt.Forward, fwdSessionID, rulesPerPK, respEdge.Desc)
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

	// Reverse path install (source-oriented); route group already introduced.
	revInstallPayload, err := cb.BuildInstallMessage(revInject, revSessionID, rulesPerPK, routing.RouteDescriptor{})
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

// signReserveCascades builds and signs the forward and reverse reserve
// cascades for a bidirectional route WITHOUT sending them. This is the
// RSN-side half of phase 1: the RSN is a pure signing oracle and never
// dials hops. The returned bytes are injected by the SOURCE over its own
// transports.
//
// cb must be an RSN-side CascadeBuilder (constructed with the RSN's
// rsnPK/rsnSK) so each per-hop layer is signed with the RSN's key.
func signReserveCascades(cb *CascadeBuilder, biRt routing.BidirectionalRoute) (
	fwdSessionID uint64, fwdReserveBytes []byte,
	revSessionID uint64, revReserveBytes []byte,
	err error,
) {
	if len(biRt.Forward) == 0 {
		return 0, nil, 0, nil, fmt.Errorf("cascade: no forward hops")
	}
	if len(biRt.Reverse) == 0 {
		return 0, nil, 0, nil, fmt.Errorf("cascade: no reverse hops")
	}

	// Each cascade is injected by the SOURCE over its own transports, so both
	// must be oriented source-first. The forward path already starts at the
	// source; the reverse path (dst->src) is re-oriented to start at the source
	// (src->...->dst over the same bidirectional transports). Each cascade
	// reserves its OWN path's per-node count (one per appearance); the two sum
	// to the legacy combined rec[pk] that GenerateRules pops (once per
	// direction) — and keeps newCascadeIDReserver's one-ID-per-PK assumption
	// valid.
	fwdSessionID, fwdReserveBytes, err = buildReserveWithCounts(cb, biRt.Forward, singlePathReserveCount(biRt.Forward))
	if err != nil {
		return 0, nil, 0, nil, fmt.Errorf("cascade: build fwd reserve: %w", err)
	}

	revInject := reverseHopsFromSource(biRt.Reverse)
	revSessionID, revReserveBytes, err = buildReserveWithCounts(cb, revInject, singlePathReserveCount(revInject))
	if err != nil {
		return 0, nil, 0, nil, fmt.Errorf("cascade: build rev reserve: %w", err)
	}

	return fwdSessionID, fwdReserveBytes, revSessionID, revReserveBytes, nil
}

// signInstallCascades reconstructs the IDReserver from the route-IDs that
// the SOURCE collected during the reserve phase, recomputes the routing
// rules DETERMINISTICALLY from the route (rules are never trusted from the
// source), then builds and signs the forward and reverse install cascades
// WITHOUT sending them. This is the RSN-side half of phase 2.
//
// It returns the install bytes (for the source to inject over its own
// transports) plus the initiating-edge EdgeRules that the source installs
// locally and returns to the dialer.
func signInstallCascades(
	cb *CascadeBuilder,
	biRt routing.BidirectionalRoute,
	fwdSessionID, revSessionID uint64,
	fwdRouteIDs, revRouteIDs []routing.RouteID,
) (fwdInstallBytes, revInstallBytes []byte, initEdge routing.EdgeRules, err error) {
	fwdRt, revRt := biRt.ForwardAndReverse()

	// The reverse cascade was injected source-first (see signReserveCascades),
	// so its ACK route IDs arrive in source->...->dst order. Reconstruct the
	// IDReserver against the SAME re-oriented reverse path so each ACK ID maps
	// back to the visor that actually reserved it. The rules are recomputed
	// below from this deterministic reserver — we do NOT trust source-supplied
	// rules.
	revInject := reverseHopsFromSource(biRt.Reverse)
	idR, err := newCascadeIDReserver(biRt.Forward, revInject, fwdRouteIDs, revRouteIDs)
	if err != nil {
		return nil, nil, routing.EdgeRules{}, fmt.Errorf("cascade: reconstruct IDs: %w", err)
	}

	fwdRules, revRules, interRules, err := GenerateRules(idR, []routing.Route{fwdRt, revRt})
	if err != nil {
		return nil, nil, routing.EdgeRules{}, fmt.Errorf("cascade: generate rules: %w", err)
	}

	srcPK := biRt.Desc.Src()
	dstPK := biRt.Desc.Dst()
	initEdge = routing.EdgeRules{
		Desc:    revRt.Desc,
		Forward: fwdRules[srcPK.String()][0],
		Reverse: revRules[srcPK.String()][0],
	}
	respEdge := routing.EdgeRules{
		Desc:    fwdRt.Desc,
		Forward: fwdRules[dstPK.String()][0],
		Reverse: revRules[dstPK.String()][0],
	}

	rulesPerPK := collectRulesPerPK(fwdRules, revRules, interRules, initEdge, respEdge)

	// The forward cascade terminates at the responding destination — stamp its
	// EdgeRules descriptor so it creates a route group (IntroduceRules). The
	// reverse cascade terminates at the destination too but the route group is
	// already introduced by the forward terminal, so no edge marking there.
	fwdInstallBytes, err = cb.BuildInstallMessage(biRt.Forward, fwdSessionID, rulesPerPK, respEdge.Desc)
	if err != nil {
		return nil, nil, routing.EdgeRules{}, fmt.Errorf("cascade: build fwd install: %w", err)
	}
	revInstallBytes, err = cb.BuildInstallMessage(revInject, revSessionID, rulesPerPK, routing.RouteDescriptor{})
	if err != nil {
		return nil, nil, routing.EdgeRules{}, fmt.Errorf("cascade: build rev install: %w", err)
	}

	return fwdInstallBytes, revInstallBytes, initEdge, nil
}

// singlePathReserveCount counts how many route IDs each visor must reserve for a
// SINGLE path traversal: one per appearance (the path's source + every hop's To).
// The forward and (re-oriented) reverse cascades each reserve independently, so
// the per-PK total across both equals the legacy NewIDReserver's combined count
// — which is exactly what GenerateRules pops (once per direction). Reserving the
// COMBINED count in each cascade (as the old computeReserveCount did) over-
// reserved and misaligned newCascadeIDReserver's one-ID-per-PK distribution.
func singlePathReserveCount(hops []routing.Hop) map[cipher.PubKey]uint8 {
	counts := make(map[cipher.PubKey]uint8)
	if len(hops) == 0 {
		return counts
	}
	counts[hops[0].From]++
	for _, hop := range hops {
		counts[hop.To]++
	}
	return counts
}

// reverseHopsFromSource re-orients a path so the route's SOURCE — which sits at
// the END of the reverse path (dst->src) — can inject the cascade over its own
// transports. It reverses hop order and swaps each hop's From/To, preserving
// transport IDs (transports are bidirectional), so the result starts at the
// source and traverses the same physical transports outward.
// e.g. reverse path [C->B, B->A] becomes [A->B, B->C].
func reverseHopsFromSource(hops []routing.Hop) []routing.Hop {
	out := make([]routing.Hop, 0, len(hops))
	for i := len(hops) - 1; i >= 0; i-- {
		out = append(out, routing.Hop{
			From: hops[i].To,
			To:   hops[i].From,
			TpID: hops[i].TpID,
		})
	}
	return out
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
