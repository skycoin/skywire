//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const fixedQEXTBuild = true

type celtQEXTState struct {
	enabled      bool
	active       bool
	buffer       []byte
	payloadLen   int
	encoder      rangecoding.Encoder
	oldBandE     []int32
	customMDCT   *QEXTMDCTLookup
	customWindow []int32
}

// SetQEXTEnabled toggles the fixed-point CELT extension coder.
func (e *CELTEncoder) SetQEXTEnabled(enabled bool) { e.qext.enabled = enabled }

func (e *CELTEncoder) qextEnabled() bool { return e.qext.enabled }

// LastQEXTPayload returns the retained side payload from the last completed
// frame. The slice remains valid until the next frame encode.
func (e *CELTEncoder) LastQEXTPayload() []byte {
	if e.qext.payloadLen == 0 {
		return nil
	}
	return e.qext.buffer[:e.qext.payloadLen]
}

func (e *CELTEncoder) clearQEXTPayload() {
	e.qext.active = false
	e.qext.payloadLen = 0
}

func (e *CELTEncoder) initQEXTEncoder(storage int) *rangecoding.Encoder {
	if storage <= 0 {
		return nil
	}
	if cap(e.qext.buffer) < storage {
		e.qext.buffer = make([]byte, storage)
	} else {
		e.qext.buffer = e.qext.buffer[:storage]
	}
	clear(e.qext.buffer)
	e.qext.encoder.Init(e.qext.buffer)
	e.qext.active = true
	return &e.qext.encoder
}

func (e *CELTEncoder) finishQEXTEncoder() uint32 {
	if !e.qext.active {
		return 0
	}
	rangeValue := e.qext.encoder.Range()
	e.qext.encoder.Shrink(uint32(e.qext.encoder.Storage()))
	e.qext.encoder.Done()
	e.qext.payloadLen = e.qext.encoder.Storage()
	e.qext.active = false
	return rangeValue
}

func (e *CELTEncoder) initQEXTState(channels int) {
	e.qext.oldBandE = make([]int32, channels*14)
}

func (e *CELTEncoder) resetQEXTState() {
	e.clearQEXTPayload()
	clear(e.qext.oldBandE)
}

// reserveQEXTBytes ports the ENABLE_QEXT reservation block in
// celt/celt_encoder.c after ordinary VBR sizing. It returns the complete
// reserved region (including the extension ID byte) and its Opus padding
// length. The caller turns the reserved region into an encoder-owned side
// coder after shrinking the main coder.
func (e *CELTEncoder) reserveQEXTBytes(nbCompressedBytes, minAllowed, vbrRate, frameSize, modeFs, shortMdctSize, lm, channels,
	equivRate, lastCodedBands, intensity, tellFrac int, constrainedVBR bool, stereoSaving int16,
	totBoost int, tfEstimate int16, pitchChange bool, maxDepth, temporalVBR int32,
	lfe, hasSurroundMask bool, surroundMasking int32, analysis CELTAnalysisInfo, toneishness int32) (int, int) {
	const qextPacketCap int32 = 1275
	compressed := int32(nbCompressedBytes)
	offset := int32(bitrateToBits(channels*80000, modeFs, frameSize) / 8)
	qextBytes := max32(compressed-qextPacketCap, max32(0, (compressed-offset)*4/5))
	if qextBytes <= 20 {
		return 0, 0
	}

	bitres := int32(bitRes)
	target := (compressed - qextBytes/3) * 8 << uint(bitres)
	if vbrRate == 0 {
		target -= int32(40*channels+20) << uint(bitres)
		tfEstimate2 := min32(1<<14, int32(tfEstimate)*2)
		target = int32(computeVBR(e.eBands, int(target), lm, equivRate, lastCodedBands, channels, intensity,
			constrainedVBR, stereoSaving, totBoost, int16(tfEstimate2), pitchChange,
			maxDepth, temporalVBR, celtNbEBands, shortMdctSize, lfe, hasSurroundMask, surroundMasking, analysis, true))
		target += int32(tellFrac)
	}
	tone := int16(pshr32(toneishness, 14))
	scale := int16(32767 - int32(mult16x16Q15(int32(tone), int32(tone))))
	mainTargetBytes := compressed - target/(8<<uint(bitres))
	qextBytes += mult16x32Q15(scale, mainTargetBytes-qextBytes)
	qextBytes = max32(compressed-qextPacketCap, max32(21, qextBytes))

	paddingBytes := (qextBytes + 253) / 254
	qextBytes = min32(qextBytes, compressed-int32(minAllowed)-paddingBytes-1)
	paddingBytes = (qextBytes + 253) / 254
	if qextBytes <= 20 {
		return 0, 0
	}
	return int(qextBytes), int(paddingBytes)
}
