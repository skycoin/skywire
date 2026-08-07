// Package tptypes pkg/transport/types/preference.go c2-net-transport
package tptypes

import (
	"sort"
	"sync/atomic"
)

// defaultPreference is the built-in priority order:
//
//	STCPR > QUIC > SUDPH > STCP > WT > WS > WEBRTC > DMSG
//
// STCPR (plain TCP via the address resolver) connects through the widest set of
// networks, so it stays the reliable baseline. QUIC slots above SUDPH: both are
// UDP, but QUIC is encrypted with modern congestion control and gives datagrams,
// whereas SUDPH is raw KCP over hole-punching. The browser-oriented carriers sit
// just above DMSG, and within that group the DIRECT ones outrank the relayed one:
// WT (WebTransport/QUIC, browser-dialable to a reachable endpoint) then WS
// (WebSocket, insecure-page-only) are true point-to-point links, so they must be
// preferred over WEBRTC — which reaches ANY visor but only via NAT-traversal
// (ICE) with dmsg-carried signaling, i.e. heavier and last-direct-resort. A
// browser edge should therefore make WEBRTC only to peers no direct carrier can
// reach, matching the public-autoconnect's WT-first/WebRTC-fallback design. DMSG
// is last: a dmsg server is an opaque intermediary not visible to path planning.
// Unknown types sort after everything listed.
var defaultPreference = []Type{STCPR, QUIC, SUDPH, STCP, WT, WS, WEBRTC, DMSG}

// preferenceOrder is the active order, settable via SetPreferenceOrder.
// Stored atomically so reads are lock-free in hot path code.
var preferenceOrder atomic.Pointer[[]Type]

// TypePreference returns a sort priority for the given transport type.
// Lower values are preferred. Unknown types sort last.
func TypePreference(t Type) int {
	order := preferenceOrder.Load()
	if order == nil {
		order = &defaultPreference
	}
	for i, ot := range *order {
		if ot == t {
			return i
		}
	}
	return len(*order)
}

// PreferenceOrder returns the active order — the one SetPreferenceOrder
// installed, or the built-in default when none was. The result is a copy;
// mutating it does not affect the active order. Callers surfacing the
// setting (the router-settings API, a UI) use this rather than assuming
// the default.
func PreferenceOrder() []Type {
	order := preferenceOrder.Load()
	if order == nil {
		return append([]Type(nil), defaultPreference...)
	}
	return append([]Type(nil), (*order)...)
}

// PreferredOrder returns candidates sorted by TypePreference, most-preferred
// first, without modifying the input. It is the single way to answer "which
// of these types do we try first" — whether picking between transports that
// already exist or deciding which one to create — so one configured order
// governs every such decision. Types absent from the active order keep their
// relative position at the end (the sort is stable).
func PreferredOrder(candidates ...Type) []Type {
	out := make([]Type, len(candidates))
	copy(out, candidates)
	sort.SliceStable(out, func(i, j int) bool {
		return TypePreference(out[i]) < TypePreference(out[j])
	})
	return out
}

// SetPreferenceOrder overrides the active transport-type preference order.
// Pass nil or an empty slice to revert to the built-in default. Unknown
// or unspecified types implicitly sort after listed types.
func SetPreferenceOrder(order []Type) {
	if len(order) == 0 {
		preferenceOrder.Store(nil)
		return
	}
	cp := make([]Type, len(order))
	copy(cp, order)
	preferenceOrder.Store(&cp)
}

// ParsePreferenceOrder parses a slice of strings (e.g. from config) into
// a slice of Type. Unknown strings are skipped silently — callers can
// detect this by comparing input/output lengths.
func ParsePreferenceOrder(s []string) []Type {
	known := map[string]Type{
		string(STCPR):       STCPR,
		string(QUIC):        QUIC, // "squicr"
		string(QUICLegacy):  QUIC, // "quic"  — back-compat alias
		string(QUICLegacy2): QUIC, // "squic" — back-compat alias
		string(SUDPH):       SUDPH,
		string(STCP):        STCP,
		string(WEBRTC):      WEBRTC,
		string(WS):          WS, // "swsr"
		string(WSLegacy):    WS, // "ws"    — back-compat alias
		string(WT):          WT, // "swtr"
		string(WTLegacy):    WT, // "wt"    — back-compat alias
		string(DMSG):        DMSG,
	}
	out := make([]Type, 0, len(s))
	for _, name := range s {
		if t, ok := known[name]; ok {
			out = append(out, t)
		}
	}
	return out
}
