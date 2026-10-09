//go:build !gopus_custom_modes

package celt

type customMDCTState struct{}

func (*customMDCTState) mdctLookup(int) *mdctTransformLookup { return nil }

func (*customMDCTState) modeWindow(overlap int) []float32 { return GetWindowBufferF32(overlap) }
