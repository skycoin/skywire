//go:build gopus_dred || gopus_osce

package extsupport

// DREDRuntime reports whether DRED runtime hooks are compiled in. The
// extra-controls tag enables runtime hooks for parity without changing
// SupportsOptionalExtension.
const DREDRuntime = true
