//go:build gopus_dred || gopus_osce

package encoder

import (
	"math/bits"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/types"
)

var dredBitsTable = [...]float32{73.2, 68.1, 62.5, 57.0, 51.5, 45.7, 39.9, 32.4, 26.4, 20.4, 16.3, 13.0, 9.3, 8.2, 7.2, 6.4}

type dredEmissionPlan struct {
	q0           int32
	dQ           int32
	qmax         int32
	targetChunks int32
	bitrate      int32
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func dredBitrateToBits(bitrate, sampleRate, frameSize int) int {
	if bitrate <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 0
	}
	unitsPerFrame := 6 * sampleRate / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	return bitrate * 6 / unitsPerFrame
}

func dredBitsToBitrate(bitCount, sampleRate, frameSize int) int {
	if bitCount <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 0
	}
	unitsPerFrame := 6 * sampleRate / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	return bitCount * unitsPerFrame / 6
}

func estimateDREDBits(q0, dQ, qmax, duration, targetBits int) (int, int) {
	bitsUsed := float32(8 * (3 + internaldred.ExperimentalHeaderBytes))
	bitsUsed += 50.0 + dredBitsTable[q0]

	dredChunks := (duration + 5) / 4
	if dredChunks > internaldred.NumRedundancyFrames/2 {
		dredChunks = internaldred.NumRedundancyFrames / 2
	}
	targetChunks := 0
	header := internaldred.Header{Q0: int32(q0), DQ: int32(dQ), QMax: int32(qmax)}
	for i := 0; i < dredChunks; i++ {
		q := header.QuantizerLevel(i)
		bitsUsed += dredBitsTable[q]
		if bitsUsed < float32(targetBits) {
			targetChunks = i + 1
		}
	}
	return int(opusmath.FloorHalfPlusF32ToInt32(bitsUsed)), targetChunks
}

func (e *Encoder) computeDREDEmissionPlan(frameSize int) (dredEmissionPlan, bool) {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.duration <= 0 || !e.dredModelsLoaded() || e.bitrate <= 0 || e.sampleRate <= 0 || frameSize <= 0 {
		return dredEmissionPlan{}, false
	}

	packetLoss := e.packetLoss
	if packetLoss < 0 {
		packetLoss = 0
	}
	if packetLoss > 100 {
		packetLoss = 100
	}

	var dredFrac float32
	bitrateOffset := int32(12000)
	if e.fecEnabled {
		dredFrac = minFloat32(0.7, 3.0*float32(packetLoss)/100.0)
		bitrateOffset = 20000
	} else if packetLoss > 5 {
		dredFrac = minFloat32(0.8, 0.55+float32(packetLoss)/100.0)
	} else {
		dredFrac = 12.0 * float32(packetLoss) / 100.0
	}

	frameRateScale := float32(frameSize*50) / float32(e.sampleRate)
	dredFrac = dredFrac / (dredFrac + (1.0-dredFrac)*frameRateScale)

	rateBudget := e.bitrate - bitrateOffset
	if rateBudget < 1 {
		rateBudget = 1
	}
	q0 := 51 - 3*bits.Len(uint(rateBudget))
	if q0 < 4 {
		q0 = 4
	}
	if q0 > 15 {
		q0 = 15
	}
	dQ := 5
	if e.bitrate-bitrateOffset > 36000 {
		dQ = 3
	}
	qmax := 15
	targetDREDBitrate := int(dredFrac * float32(e.bitrate-bitrateOffset))
	if targetDREDBitrate < 0 {
		targetDREDBitrate = 0
	}
	maxBits, targetChunks := estimateDREDBits(q0, dQ, qmax, int(e.dred.duration), dredBitrateToBits(targetDREDBitrate, int(e.sampleRate), frameSize))
	if targetChunks < 2 {
		return dredEmissionPlan{}, false
	}
	dredBitrate := dredBitsToBitrate(maxBits, int(e.sampleRate), frameSize)
	if targetDREDBitrate < dredBitrate {
		dredBitrate = targetDREDBitrate
	}
	if dredBitrate <= 0 {
		return dredEmissionPlan{}, false
	}
	return dredEmissionPlan{
		q0:           int32(q0),
		dQ:           int32(dQ),
		qmax:         int32(qmax),
		targetChunks: int32(targetChunks),
		bitrate:      int32(dredBitrate),
	}, true
}

func maxDREDChunks(duration, targetChunks int, capToTarget bool) int {
	maxChunks := (duration + 5) / 4
	if maxChunks > internaldred.NumRedundancyFrames/2 {
		maxChunks = internaldred.NumRedundancyFrames / 2
	}
	if capToTarget && targetChunks > 0 && maxChunks > targetChunks {
		maxChunks = targetChunks
	}
	return maxChunks
}

func packetExtensionPaddingAmount(extID, extDataLen int) int {
	if extDataLen <= 0 {
		return 0
	}
	extLen := 0
	if extID >= 3 && extID <= 127 {
		if extID < 32 {
			if extDataLen <= 1 {
				extLen = 1 + extDataLen
			}
		} else {
			extLen = 1 + extDataLen
		}
	}
	if extLen <= 0 {
		return 0
	}
	return extLen + (extLen+253)/254
}

func (e *Encoder) previewDREDExperimentalPayloadLength(maxChunks, q0, dQ, qmax int32) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil || runtime.latentsFill <= 0 {
		return 0
	}
	lastExtra := runtime.lastExtraDREDOffset
	return internaldred.EncodeExperimentalPayload(
		runtime.payload[:],
		maxChunks,
		q0,
		dQ,
		qmax,
		runtime.stateBuffer[:],
		runtime.latentsBuffer[:],
		runtime.latentsFill,
		runtime.dredOffset,
		runtime.latentOffset,
		&lastExtra,
		runtime.activity[:],
	)
}

func (e *Encoder) hybridDREDPrimaryBudget(originalBitrate, frameSize int, plan dredEmissionPlan) int {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.duration <= 0 || plan.targetChunks < 1 {
		return 0
	}
	maxChunks := maxDREDChunks(int(e.dred.duration), int(plan.targetChunks), e.bitrateMode != ModeCBR)
	if maxChunks < 1 {
		return 0
	}
	payloadLen := e.previewDREDExperimentalPayloadLength(int32(maxChunks), plan.q0, plan.dQ, plan.qmax)
	if payloadLen == 0 {
		return 0
	}
	targetSize := e.targetBytesForBitrate(originalBitrate, frameSize)
	paddingAmount := packetExtensionPaddingAmount(internaldred.ExtensionID, payloadLen)
	// maxPacketBytes is the primary packet budget including its TOC. The hybrid
	// VBR path applies the final range shrink after this soft budget, so leave
	// the code-3 count byte to the packet-extension framing step.
	budget := targetSize - paddingAmount
	if e.channels > 1 {
		budget -= 4 * (int(e.channels) - 1)
	}
	if budget < 2 {
		return 2
	}
	return budget
}

func packetExtensionsLengthExcludingDRED(extensions []packetExtension) int {
	total := 0
	for _, ext := range extensions {
		total += packetExtensionDataLength(ext.ID, len(ext.Data), false)
	}
	return total
}

func (e *Encoder) maybeBuildSingleFrameDREDPacket(frameData []byte, actualMode Mode, packetBW types.Bandwidth, frameSize int, stereo bool, prefixExtensions []packetExtension) ([]byte, bool, error) {
	if !extsupport.DREDRuntime {
		return nil, false, nil
	}
	plan, ok := e.computeDREDEmissionPlan(frameSize)
	if !ok {
		return nil, false, nil
	}

	targetSize := e.targetBytesForBitrate(int(e.bitrate), frameSize)
	baseLen := 1 + len(frameData)
	withPadding := e.bitrateMode == ModeCBR
	// In CBR we must fit DRED within the bitrate-based target; in VBR/CVBR the
	// primary CELT/Hybrid encoder may exceed that target and DRED still goes
	// into the spare scratch buffer space, matching libopus opus_packet_pad_impl
	// behavior against orig_max_data_bytes.
	if withPadding && targetSize < baseLen+1 {
		return nil, false, nil
	}

	prefixExtLen := packetExtensionsLengthExcludingDRED(prefixExtensions)
	dredBytesLeft := targetSize - baseLen - 3 - prefixExtLen
	if !withPadding {
		dredBytesLeft = len(e.scratchPacket) - baseLen - 3 - prefixExtLen
	}
	if dredBytesLeft > internaldred.MaxDataSize {
		dredBytesLeft = internaldred.MaxDataSize
	}
	continuationOverheadInput := dredBytesLeft + 1 + internaldred.ExperimentalHeaderBytes
	if prefixExtLen > 0 {
		continuationOverheadInput += prefixExtLen
	}
	dredBytesLeft -= continuationOverheadInput / 255
	if dredBytesLeft < internaldred.MinBytes+internaldred.ExperimentalHeaderBytes {
		return nil, false, nil
	}

	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil {
		return nil, false, nil
	}

	maxChunks := maxDREDChunks(int(e.dred.duration), int(plan.targetChunks), e.bitrateMode != ModeCBR)
	if maxChunks < 1 {
		return nil, false, nil
	}

	n := e.buildDREDExperimentalPayload(runtime.payload[:dredBytesLeft], int32(maxChunks), plan.q0, plan.dQ, plan.qmax)
	if n == 0 {
		return nil, false, nil
	}

	var extensions [4]packetExtension
	extensions[0] = packetExtension{ID: internaldred.ExtensionID, Data: runtime.payload[:n], Frame: 0}
	copiedPrefix := copy(extensions[1:], prefixExtensions)
	if copiedPrefix != len(prefixExtensions) {
		return nil, false, ErrInvalidConfig
	}
	extCount := 1 + copiedPrefix

	packetLen, err := buildPacketWithExtensionsInto(
		e.scratchPacket,
		frameData,
		modeToTypes(actualMode),
		packetBW,
		e.packetTOCFrameSize(frameSize),
		stereo,
		extensions[:extCount],
		targetSize,
		withPadding,
	)
	if err != nil {
		return nil, false, err
	}
	return e.scratchPacket[:packetLen], true, nil
}

func (e *Encoder) maybeBuildMultiFrameDREDPacket(frames [][]byte, actualMode Mode, packetBW types.Bandwidth, packetFrameSize, packetTOCFrameSize, firstFrameMaxBytes int, stereo bool, vbr bool, prefixExtensions []packetExtension) ([]byte, bool, error) {
	if !extsupport.DREDRuntime {
		return nil, false, nil
	}
	plan, ok := e.computeDREDEmissionPlan(packetFrameSize)
	if !ok {
		return nil, false, nil
	}

	targetSize := e.targetBytesForBitrate(int(e.bitrate), packetFrameSize)
	baseLen := 2
	if vbr {
		for i := 0; i < len(frames)-1; i++ {
			baseLen += frameLengthBytes(len(frames[i]))
		}
	}
	for _, frame := range frames {
		baseLen += len(frame)
	}
	withPadding := e.bitrateMode == ModeCBR
	// In CBR we must fit DRED within the bitrate-based target; in VBR/CVBR the
	// primary encoder may exceed that target and DRED still goes into the spare
	// scratch buffer space, matching libopus opus_packet_pad_impl behavior.
	if withPadding && targetSize < baseLen+1 {
		return nil, false, nil
	}

	prefixExtLen := packetExtensionsLengthExcludingDRED(prefixExtensions)
	dredBytesLeft := 0
	if firstFrameMaxBytes > 0 && len(frames) > 0 {
		dredBytesLeft = firstFrameMaxBytes - (1 + len(frames[0])) - 3 - prefixExtLen
	} else {
		dredBytesLeft = targetSize - baseLen - 3 - prefixExtLen
	}
	if !withPadding && firstFrameMaxBytes <= 0 {
		dredBytesLeft = len(e.scratchPacket) - baseLen - 3 - prefixExtLen
	}
	if dredBytesLeft > internaldred.MaxDataSize {
		dredBytesLeft = internaldred.MaxDataSize
	}
	continuationOverheadInput := dredBytesLeft + 1 + internaldred.ExperimentalHeaderBytes
	if prefixExtLen > 0 {
		continuationOverheadInput += prefixExtLen
	}
	dredBytesLeft -= continuationOverheadInput / 255
	if dredBytesLeft < internaldred.MinBytes+internaldred.ExperimentalHeaderBytes {
		return nil, false, nil
	}

	runtime := e.ensureActiveDREDRuntime()
	if runtime == nil {
		return nil, false, nil
	}
	if runtime.packetSnapshot.valid {
		defer func() {
			runtime.packetSnapshot.valid = false
		}()
	}

	maxChunks := maxDREDChunks(int(e.dred.duration), int(plan.targetChunks), e.bitrateMode != ModeCBR)
	if maxChunks < 1 {
		return nil, false, nil
	}

	n := e.buildDREDExperimentalPayloadForPacket(runtime.payload[:dredBytesLeft], int32(maxChunks), plan.q0, plan.dQ, plan.qmax)
	if n == 0 {
		return nil, false, nil
	}

	var extensions [10]packetExtension
	extensions[0] = packetExtension{ID: internaldred.ExtensionID, Data: runtime.payload[:n], Frame: 0}
	copiedPrefix := copy(extensions[1:], prefixExtensions)
	if copiedPrefix != len(prefixExtensions) {
		return nil, false, ErrInvalidConfig
	}
	extCount := 1 + copiedPrefix

	packetLen, err := buildMultiFramePacketWithExtensionsInto(
		e.scratchPacket,
		frames,
		modeToTypes(actualMode),
		packetBW,
		packetTOCFrameSize,
		stereo,
		vbr,
		extensions[:extCount],
		targetSize,
		withPadding,
	)
	if err != nil {
		return nil, false, err
	}
	return e.scratchPacket[:packetLen], true, nil
}
