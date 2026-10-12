//go:build gopus_qext

package celt

// ENABLE_QEXT selects Q30 theta gains in libopus bands.c for every band,
// including main bands with no secondary range coder.
const celtQEXTFloatMath = true
