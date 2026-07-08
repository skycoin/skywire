package dred

import (
	"github.com/thesyncim/gopus/internal/dnnmath"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// ActivityHistorySize is the length of the encoder voice-activity history at
// 2.5 ms (400 Hz) resolution, mirroring the activity_mem[DRED_MAX_FRAMES*4]
// buffer libopus keeps in src/opus_encoder.c.
const ActivityHistorySize = 4 * MaxFrames

const dredLaplaceFTBits = 15

type dredLatentEncodeScratch struct {
	q        [StateDim]int32
	xq       [StateDim]float32
	delta    [StateDim]float32
	deadzone [StateDim]float32
}

// UpdateActivityHistory mirrors the libopus encoder-side DRED activity-memory
// rollover at 2.5 ms resolution.
func UpdateActivityHistory(dst *[ActivityHistorySize]byte, frameSize, sampleRate int, active bool) {
	if dst == nil || sampleRate <= 0 || frameSize <= 0 {
		return
	}
	n := frameSize * 400 / sampleRate
	if n <= 0 {
		return
	}
	fill := byte(0)
	if active {
		fill = 1
	}
	if n >= len(dst) {
		for i := range dst {
			dst[i] = fill
		}
		return
	}
	copy(dst[n:], dst[:len(dst)-n])
	for i := range n {
		dst[i] = fill
	}
}

func dredVoiceActive(activity []byte, offset int) bool {
	if offset < 0 {
		return false
	}
	start := 8 * offset
	if start >= len(activity) {
		return false
	}
	end := min(start+16, len(activity))
	for i := start; i < end; i++ {
		if activity[i] == 1 {
			return true
		}
	}
	return false
}

func encodeLaplaceP0(enc *rangecoding.Encoder, value int32, p0, decay uint16) {
	signICDF := [3]uint16{32768 - p0, (32768 - p0) / 2, 0}
	symbol := 0
	if value > 0 {
		symbol = 1
	} else if value < 0 {
		symbol = 2
	}
	enc.EncodeICDF16(symbol, signICDF[:], dredLaplaceFTBits)

	if value < 0 {
		value = -value
	}
	if value == 0 {
		return
	}

	icdf := [8]uint16{}
	icdf[0] = uint16(maxInt(7, int(decay)))
	for i := 1; i < 7; i++ {
		icdf[i] = uint16(maxInt(7-i, int((uint32(icdf[i-1])*uint32(decay))>>15)))
	}

	value--
	for {
		symbol := min(int(value), 7)
		enc.EncodeICDF16(symbol, icdf[:], dredLaplaceFTBits)
		value -= 7
		if value < 0 {
			return
		}
	}
}

func quantizeDREDLatents(q []int32, x []float32, scale, dzone, rTable, p0Table []uint8, scratch *dredLatentEncodeScratch) {
	if scratch == nil {
		var local dredLatentEncodeScratch
		scratch = &local
	}
	n := len(x)
	xq := scratch.xq[:n]
	delta := scratch.delta[:n]
	deadzone := scratch.deadzone[:n]

	for i := range n {
		delta[i] = float32(dzone[i]) * (1.0 / 256.0)
		xq[i] = x[i] * float32(scale[i]) * (1.0 / 256.0)
		deadzone[i] = xq[i] / (delta[i] + 0.1)
	}
	dnnmath.TanhVectorApprox(deadzone, deadzone, n)
	for i := range n {
		if rTable[i] == 0 || p0Table[i] == 255 {
			q[i] = 0
			continue
		}
		xqi := xq[i] - delta[i]*deadzone[i]
		q[i] = opusmath.FloorF32ToInt32(0.5 + xqi)
	}
}

func encodeDREDLatents(enc *rangecoding.Encoder, x []float32, scale, dzone, rTable, p0Table []uint8, scratch *dredLatentEncodeScratch) {
	if scratch == nil {
		var local dredLatentEncodeScratch
		scratch = &local
	}
	q := scratch.q[:len(x)]
	quantizeDREDLatents(q, x, scale, dzone, rTable, p0Table, scratch)
	for i, qi := range q {
		if rTable[i] == 0 || p0Table[i] == 255 {
			continue
		}
		encodeLaplaceP0(enc, qi, uint16(p0Table[i])<<7, uint16(rTable[i])<<7)
	}
}

// EncodePayload mirrors libopus dred_encode_silk_frame() for a caller-owned
// DRED history window. dst must not include the temporary experimental prefix.
// It returns the encoded payload length in bytes, or 0 when libopus would
// suppress DRED emission for the provided window.
func EncodePayload(dst []byte, maxChunks, q0, dQ, qmax int32, stateBuffer, latentsBuffer []float32, latentsFill, dredOffset, latentOffset int32, lastExtraDREDOffset *int32, activity []byte) int {
	if len(dst) == 0 || latentsFill <= 0 {
		return 0
	}

	extraDREDOffset := int32(0)
	delayedDRED := false
	if len(activity) > 0 && activity[0] != 0 && lastExtraDREDOffset != nil && *lastExtraDREDOffset > 0 {
		latentOffset = *lastExtraDREDOffset
		delayedDRED = true
		*lastExtraDREDOffset = 0
	}
	for latentOffset < latentsFill && !dredVoiceActive(activity, int(latentOffset)) {
		latentOffset++
		extraDREDOffset++
	}
	if !delayedDRED && lastExtraDREDOffset != nil {
		*lastExtraDREDOffset = extraDREDOffset
	}

	stateIndex := int(latentOffset) * StateDim
	if stateIndex < 0 || stateIndex+StateDim > len(stateBuffer) {
		return 0
	}

	var enc rangecoding.Encoder
	enc.Init(dst)
	enc.EncodeUniform(uint32(q0), 16)
	enc.EncodeUniform(uint32(dQ), 8)

	totalOffset := 16 - (dredOffset - extraDREDOffset*8)
	if totalOffset < 0 {
		return 0
	}
	if totalOffset > 31 {
		enc.EncodeUniform(1, 2)
		enc.EncodeUniform(uint32(totalOffset>>5), 256)
		enc.EncodeUniform(uint32(totalOffset&31), 32)
	} else {
		enc.EncodeUniform(0, 2)
		enc.EncodeUniform(uint32(totalOffset), 32)
	}

	if q0 < 14 && dQ > 0 {
		nvals := 15 - (q0 + 1)
		if qmax >= 15 {
			enc.Encode(0, uint32(nvals), uint32(2*nvals))
		} else {
			fl := nvals + qmax - (q0 + 1)
			fh := nvals + qmax - q0
			enc.Encode(uint32(fl), uint32(fh), uint32(2*nvals))
		}
	}

	var latentScratch dredLatentEncodeScratch
	stateOffset := int(q0) * StateDim
	encodeDREDLatents(
		&enc,
		stateBuffer[stateIndex:stateIndex+StateDim],
		dredStateQuantScalesQ8[stateOffset:stateOffset+StateDim],
		dredStateDeadZoneQ8[stateOffset:stateOffset+StateDim],
		dredStateRQ8[stateOffset:stateOffset+StateDim],
		dredStateP0Q8[stateOffset:stateOffset+StateDim],
		&latentScratch,
	)
	if enc.Tell() > 8*len(dst) {
		return 0
	}

	backup := enc
	dredEncoded := 0
	prevActive := false
	limit := minInt(int(2*maxChunks), int(latentsFill-latentOffset-1))
	header := Header{Q0: q0, DQ: dQ, QMax: qmax}
	for i := 0; i < limit; i += 2 {
		quant := header.QuantizerLevel(i / 2)
		latentIndex := (i + int(latentOffset)) * LatentDim
		if latentIndex < 0 || latentIndex+LatentDim > len(latentsBuffer) {
			return 0
		}
		offset := int(quant) * LatentDim
		encodeDREDLatents(
			&enc,
			latentsBuffer[latentIndex:latentIndex+LatentDim],
			dredLatentQuantScalesQ8[offset:offset+LatentDim],
			dredLatentDeadZoneQ8[offset:offset+LatentDim],
			dredLatentRQ8[offset:offset+LatentDim],
			dredLatentP0Q8[offset:offset+LatentDim],
			&latentScratch,
		)
		if enc.Tell() > 8*len(dst) {
			if i == 0 {
				return 0
			}
			break
		}
		active := dredVoiceActive(activity, i+int(latentOffset))
		if active || prevActive {
			backup = enc
			dredEncoded = i + 2
		}
		prevActive = active
	}

	if dredEncoded == 0 || (dredEncoded <= 2 && extraDREDOffset > 0) {
		return 0
	}

	enc = backup
	used := (enc.Tell() + 7) / 8
	enc.Shrink(uint32(used))
	return len(enc.Done())
}

// EncodeExperimentalPayload mirrors the current libopus temporary DRED payload
// framing by prepending the experimental header in front of EncodePayload().
func EncodeExperimentalPayload(dst []byte, maxChunks, q0, dQ, qmax int32, stateBuffer, latentsBuffer []float32, latentsFill, dredOffset, latentOffset int32, lastExtraDREDOffset *int32, activity []byte) int {
	if len(dst) <= ExperimentalHeaderBytes {
		return 0
	}
	dst[0] = 'D'
	dst[1] = ExperimentalVersion
	n := EncodePayload(dst[ExperimentalHeaderBytes:], maxChunks, q0, dQ, qmax, stateBuffer, latentsBuffer, latentsFill, dredOffset, latentOffset, lastExtraDREDOffset, activity)
	if n == 0 {
		return 0
	}
	return ExperimentalHeaderBytes + n
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
