//go:build amd64 && !purego

package celt

import "github.com/thesyncim/gopus/internal/cpufeat"

var amd64UseAVX2FMA = cpufeat.AMD64.HasAVX2 && cpufeat.AMD64.HasFMA

// Use AVX2/FMA only at libopus x86 float-build dispatch points with matching
// source helpers.
var amd64UsePitchXcorrAVX2FMA = amd64UseAVX2FMA

// Keep unrelated CELT prefilter/header-analysis helpers independent of host
// AVX/FMA exposure until the matching libopus helper is ported.
var amd64UseAbsSumAVX2FMA = false
var amd64UsePrefilterAVX2FMA = false
var amd64UsePitchAutocorrAVX2FMA = false
var amd64UseToneLPCCorrAVX2FMA = false

//go:noescape
func pvqSearchPulseLoopAVX(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32)

func pvqSearchPulseLoop(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	if amd64UseAVX2FMA {
		return pvqSearchPulseLoopAVX(absX, y, iy, xy, yy, n, pulsesLeft)
	}
	return pvqSearchPulseLoopGeneric(absX, y, iy, xy, yy, n, pulsesLeft)
}

//go:noescape
func toneLPCCorrAVXFMA(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)

func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if amd64UseToneLPCCorrAVX2FMA && amd64UseAVX2FMA {
		return toneLPCCorrAVXFMA(x, cnt, delay, delay2)
	}
	return toneLPCCorrGeneric(x, cnt, delay, delay2)
}
