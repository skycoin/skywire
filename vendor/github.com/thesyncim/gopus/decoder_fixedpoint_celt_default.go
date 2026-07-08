//go:build !gopus_fixed_point

package gopus

import "github.com/thesyncim/gopus/internal/celt"

// celtDecodeFixedAPIRate is a no-op in the default (float) build: it never
// handles the CELT-only decode, so the caller falls through to the float CELT
// decoder. It exists only to keep the dispatch in
// decodeOpusFrameIntoWithStatePolicyAndQEXT build-tag agnostic.
func (d *Decoder) celtDecodeFixedAPIRate(_ []byte, _ int, _ bool, _ celt.CELTBandwidth, _ []float32) (bool, error) {
	return false, nil
}

// celtDecodeLostFixedAPIRate is a no-op in the default build: CELT packet loss
// always uses the float concealment + conversion there.
func (d *Decoder) celtDecodeLostFixedAPIRate(_ int) bool { return false }

// resetFixedCELT is a no-op in the default build.
func (d *Decoder) resetFixedCELT() {}

// prepareFixedHybrid / finishFixedHybrid are no-ops in the default build: the
// Hybrid int16/int24 wrappers always use the float conversion there.
func (d *Decoder) prepareFixedHybrid(_ []byte, _ celt.CELTBandwidth, _ bool) bool { return false }
func (d *Decoder) finishFixedHybrid() error                                       { return nil }

// armFixedHybridLost / finishFixedHybridLost are no-ops in the default build:
// lost hybrid frames always use the float PLC + conversion there.
func (d *Decoder) armFixedHybridLost(_ int, _ bool) bool { return false }
func (d *Decoder) finishFixedHybridLost(_ int) bool      { return false }

// The integer Hybrid redundancy / transition helpers are no-ops in the default
// build; the int16/int24 wrappers there always use the float conversion for
// redundancy / transition frames.
func (d *Decoder) fixedDecodeRedundantCELT(_ []byte, _ celt.CELTBandwidth, _ bool) {}
func (d *Decoder) fixedDecodeTransitionPLC(_ int)                                  {}
func (d *Decoder) fixedApplyRedundancySilkToCelt(_, _ int)                         {}
func (d *Decoder) fixedApplyRedundancyCeltToSilk(_, _ int)                         {}
func (d *Decoder) fixedApplyTransition(_, _, _ int)                                {}
func (d *Decoder) fixedClearHybridFrame()                                          {}
func (d *Decoder) fixedSnapshotHandled() bool                                      { return false }
func (d *Decoder) fixedRestoreHandled(_ bool)                                      {}
func (d *Decoder) fixedSuppressCELTPLC(_ bool) bool                                { return false }
func (d *Decoder) fixedCELTPLCHookSuppressed() bool                                { return false }

// The integer-output accumulation helpers are no-ops in the default build; the
// int16/int24 wrappers there always use the float conversion.
func (d *Decoder) beginFixedPacket()          {}
func (d *Decoder) endFixedPacket()            {}
func (d *Decoder) markFixedUnhandled()        {}
func (d *Decoder) fixedInt16Ready(_ int) bool { return false }
func (d *Decoder) fixedApplyDecodeGain(_ int) {}

// finishInt16Output / finishInt24Output always use the shared float conversion
// in the default build.
func (d *Decoder) finishInt16Output(pcm []int16, scratch []float32, n, channels int) bool {
	softClipAndFloat32ToInt16(pcm, scratch, n, channels, d.softClipMem[:])
	return false
}

func (d *Decoder) finishInt24Output(pcm []int32, scratch []float32, n, channels int) bool {
	float32ToInt24Slice(pcm, scratch, n, channels)
	return false
}

// fixedInt16PLCOutput / fixedInt24PLCOutput never handle concealment in the
// default build; the int16/int24 PLC wrappers always use the float fallback.
func (d *Decoder) fixedInt16PLCOutput(_ []int16, _, _ int) bool { return false }
func (d *Decoder) fixedInt24PLCOutput(_ []int32, _, _ int) bool { return false }
