//go:build !gopus_fixed_point

package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// fixedPointBuild is false in the default (float) build.
const fixedPointBuild = false

// encoderFixedCELTFields is empty in the default (float) build, keeping the
// Encoder struct byte-unchanged.
type encoderFixedCELTFields struct{}

// encodeCELTFrameFixed never handles a frame in the default build, so the CELT
// frame seam always uses the float celt.Encoder. This keeps the dispatch in
// encodeCELTFrameWithBitrateMaxPayloadAndDRED build-tag agnostic.
func (e *Encoder) encodeCELTFrameFixed(_ []opusRes, _, _, _ int, _ bool) ([]byte, bool, error) {
	return nil, false, nil
}

func (e *Encoder) encodeHybridCELTFrameFixed(_ []int32, _, _, _ int, _ *rangecoding.Encoder, _ bool) ([]byte, bool, error) {
	return nil, false, nil
}

func (e *Encoder) encodeRedundantCELTFrameFixed(_ []int32, _, _, _ int, _, _ bool) ([]byte, uint32, bool, error) {
	return nil, 0, false, nil
}

func (e *Encoder) prefillCELTFrameFixed(_ []int32, _, _, _, _ int, _ int32) bool { return false }

// resetFixedCELT is a no-op in the default build.
func (e *Encoder) resetFixedCELT() {}

func (e *Encoder) resetFixedCELTState() {}

func (e *Encoder) syncFixedCELTEnergyMask() {}

// SetCELTEnergyMaskQ24 applies a fixed Q24 mask through the float CELT control
// when the fixed-point encoder is not compiled in.
func (e *Encoder) SetCELTEnergyMaskQ24(mask []int32) {
	if len(mask) == 0 {
		e.SetCELTEnergyMask(nil)
		return
	}
	needed := int(e.channels) * celt.MaxBands
	if len(mask) < needed {
		panic("CELT energy mask is shorter than channels*21")
	}
	if cap(e.celtEnergyMask) < needed {
		e.celtEnergyMask = make([]float32, needed)
	} else {
		e.celtEnergyMask = e.celtEnergyMask[:needed]
	}
	for i, value := range mask[:needed] {
		e.celtEnergyMask[i] = float32(value) * (1.0 / float32(1<<24))
	}
	e.syncCELTEnergyMask()
}

func (e *Encoder) setFixedCELTLFE(_ bool) {}

func (e *Encoder) fixedSilkSurroundRateOffset(_ int32) (int32, bool) { return 0, false }

func (e *Encoder) fixedStereoWidthForMode(_ int) (opusVal16, bool) { return 0, false }
func (e *Encoder) fixedModeThreshold(_ int32) (int32, bool)        { return 0, false }

// fixedCELTFinalRange never reports an integer range in the default build.
func (e *Encoder) fixedCELTFinalRange() (uint32, bool) { return 0, false }

func (e *Encoder) fixedQEXTPayloadIfUsed() ([]byte, bool) { return nil, false }

// clearFixedCELTUsed is a no-op in the default build.
func (e *Encoder) clearFixedCELTUsed() {}

func (e *Encoder) prepareFixedInputRes(_ []float32) {}
func (e *Encoder) clearFixedInputRes()              {}
func (e *Encoder) preprocessFixedInputRes(_ int)    {}
func (e *Encoder) prepareFixedCELTPCM(_ int)        {}
func (e *Encoder) advanceFixedInputCursor(_ int)    {}
func (e *Encoder) updateFixedDelayBuffer(_ int)     {}
func (e *Encoder) applyFixedStereoWidth(_ int16)    {}
