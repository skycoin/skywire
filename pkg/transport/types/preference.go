package tptypes

import "sync/atomic"

// defaultPreference is the built-in priority order: STCPR > SUDPH > STCP > DMSG.
// Direct types are preferred over DMSG because a dmsg server is an opaque
// intermediary not visible to path planning. Unknown types sort last.
var defaultPreference = []Type{STCPR, SUDPH, STCP, DMSG}

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
		string(STCPR): STCPR,
		string(SUDPH): SUDPH,
		string(STCP):  STCP,
		string(DMSG):  DMSG,
	}
	out := make([]Type, 0, len(s))
	for _, name := range s {
		if t, ok := known[name]; ok {
			out = append(out, t)
		}
	}
	return out
}
