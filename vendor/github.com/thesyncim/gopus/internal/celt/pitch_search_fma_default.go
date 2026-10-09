//go:build !arm64

package celt

// libopusPitchSearchUsesFMA is false where the selected scalar x86 reference
// compiles the pitch Syy recurrence without fused multiply-add instructions.
const libopusPitchSearchUsesFMA = false
