//go:build gopus_osce

package extsupport

// OSCEBWERuntime reports whether OSCE BWE controls/helpers are compiled in.
// The extra-controls tag enables runtime hooks for parity without
// changing SupportsOptionalExtension.
const (
	OSCEBWERuntime = true
	OSCERuntime    = true
)
