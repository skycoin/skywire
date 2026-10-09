//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const fixedQEXTBuild = false

type celtQEXTState struct {
	oldBandE []int32
}

func (e *CELTEncoder) SetQEXTEnabled(bool)                      {}
func (e *CELTEncoder) qextEnabled() bool                        { return false }
func (e *CELTEncoder) LastQEXTPayload() []byte                  { return nil }
func (e *CELTEncoder) clearQEXTPayload()                        {}
func (e *CELTEncoder) initQEXTEncoder(int) *rangecoding.Encoder { return nil }
func (e *CELTEncoder) finishQEXTEncoder() uint32                { return 0 }
func (e *CELTEncoder) initQEXTState(int)                        {}
func (e *CELTEncoder) resetQEXTState()                          {}
func (e *CELTEncoder) reserveQEXTBytes(nbCompressedBytes, minAllowed, vbrRate, frameSize, modeFs, shortMdctSize, lm, channels,
	equivRate, lastCodedBands, intensity, tellFrac int, constrainedVBR bool, stereoSaving int16,
	totBoost int, tfEstimate int16, pitchChange bool, maxDepth, temporalVBR int32,
	lfe, hasSurroundMask bool, surroundMasking int32, analysis CELTAnalysisInfo, toneishness int32) (int, int) {
	return 0, 0
}

func computeQEXTExtraAllocationFixed(start, end, qextEnd int, totalQ3 int32, channels, lm int,
	mainLogE, qextLogE []int32, mainLogN, qextLogN []int16, qextEdges []int16,
	mode *celtBandGeometry, toneFreq int16, toneishness int32, enc *rangecoding.Encoder,
	extraPulses, extraQuant []int32) {
}
