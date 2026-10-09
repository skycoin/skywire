//go:build !gopus_fixed_point

package encoder

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type encoderFixedOuterQ8Fields struct{}

func newFixedOuterQ8Fields() encoderFixedOuterQ8Fields { return encoderFixedOuterQ8Fields{} }
func (e *Encoder) resetFixedOuterQ8()                  {}
func (e *Encoder) fixedHighBandGainQ15(_ int32) int16  { return 1<<15 - 1 }
func (e *Encoder) fadeFixedHighBand(_ int16)           {}

func (e *Encoder) fixedSILKInputQ8(_ int) []int32     { return nil }
func (e *Encoder) fixedHybridCELTPCMQ8(_ int) []int32 { return nil }
func (e *Encoder) fixedFrameSliceQ8(_, _ int) []int32 { return nil }
func (e *Encoder) stageFixedSILKPrefill(_ bool)       {}
func (e *Encoder) captureFixedCELTTransitionPrefill() {}
func (e *Encoder) fixedSILKPrefillInputQ8(_ int) []int32 {
	return nil
}
func (e *Encoder) fixedCELTTransitionPrefillInputQ8(_ int) []int32 {
	return nil
}
func (e *Encoder) consumeFixedSILKPrefill() {}
func (e *Encoder) consumeFixedCELTPrefill() {}

func (e *Encoder) encodeSILKPrefill(prefill, activity int) error {
	_, err := e.silk.Encode(&e.silkMode, e.scratchSilkPrefill, int(e.sampleRate)/100, nil, prefill, activity)
	return err
}

func (e *Encoder) encodeSILKFrame(pcm []opusRes, frameSize int, re *rangecoding.Encoder, activity int) (int32, error) {
	return e.silk.Encode(&e.silkMode, pcm, frameSize, re, 0, activity)
}
