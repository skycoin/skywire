package extsupport

// Build-time optional extension availability flags.
//
// Keep these as constants so disabled surfaces stay completely dormant in
// default builds and hot paths do not pay to probe them dynamically.
const (
	OSCEBWE = false
)
