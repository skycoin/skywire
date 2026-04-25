package tptypes

// TypePreference returns a sort priority for the given transport type.
// Lower values are preferred for data routes. The ordering is:
// STCPR (0) > SUDPH (1) > STCP (2) > DMSG (3). Direct types are
// preferred over DMSG because a dmsg server is an opaque intermediary
// not visible to path planning. Unknown types sort last.
func TypePreference(t Type) int {
	switch t {
	case STCPR:
		return 0
	case SUDPH:
		return 1
	case STCP:
		return 2
	case DMSG:
		return 3
	default:
		return 4
	}
}
