package tptypes

import (
	"sync/atomic"
)

// defaultPreference is the built-in priority order:
//
//	STCPR > QUIC > SUDPH > STCP > WEBRTC > WS > WT > DMSG
//
// STCPR (plain TCP via the address resolver) connects through the widest set of
// networks, so it stays the reliable baseline. QUIC slots above SUDPH: both are
// UDP, but QUIC is encrypted with modern congestion control and gives datagrams,
// whereas SUDPH is raw KCP over hole-punching. The browser-oriented direct types
// (WEBRTC/WS/WT) sit just above DMSG — they ARE direct (the "direct over DMSG"
// rule), but are newer and chiefly serve browser/NAT-constrained peers. DMSG is
// last: a dmsg server is an opaque intermediary not visible to path planning.
// Unknown types sort after everything listed.
var defaultPreference = []Type{STCPR, QUIC, SUDPH, STCP, WEBRTC, WS, WT, DMSG}

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
		string(WS):          WS,
		string(WT):          WT,
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
