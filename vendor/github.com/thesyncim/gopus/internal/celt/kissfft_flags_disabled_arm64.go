//go:build arm64

package celt

const kissFFTM1FastPathEnabled = true

const kissFFTDFTFallbackEnabled = false

// The 120-point FFT oracle matches libopus through the regular butterfly path.
const kissFFTCOrder120Enabled = false
