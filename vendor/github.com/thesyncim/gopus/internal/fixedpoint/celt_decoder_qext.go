//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	qextCELTDecodeBufferSize48 = 2048
	qextCELTDecodeBufferSize96 = 4096
	qextCELTMaxQEXTBands       = 14
	qextCELTOverlapMax         = 320
)

// QEXTCELTDecoder is the fixed-point ENABLE_QEXT CELT decoder state for the
// native 48 kHz and 96 kHz modes. Its Q31 synthesis and QEXT energy history are
// separate from CELTDecoder because those types follow the non-QEXT Q15 mode.
type QEXTCELTDecoder struct {
	channels            int
	sampleRate          int
	downsample          int
	shortMDCTSize       int
	overlap             int
	decodeBufSize       int
	maxLM               int
	nbEBands            int
	effEBands           int
	start               int
	end                 int
	maxFrameSize        int
	qextMaxBands        int
	deemph0             int16
	deemph1             int16
	deemph3             int16
	mdct                *QEXTMDCTLookup
	window              []int32
	eBands              []int16
	logN                []int16
	qextEdges           []int16
	qextLogN            []int16
	customTables        fixedCustomTables
	decodeMem           []int32
	oldBandE            []int32
	oldLogE             []int32
	oldLogE2            []int32
	backgroundLogE      []int32
	qextOldBandE        []int32
	preemphMem          []int32
	mdctScratch         QEXTMDCTScratch
	freq                []int32
	baseBands           celtDecodeBandsScratch
	qextBands           celtDecodeBandsScratch
	decodeAlloc         celt.CELTDecodeAllocation
	extraPulses         [celt.MaxCustomBands + qextCELTMaxQEXTBands]int32
	extraQuant          [celt.MaxCustomBands + qextCELTMaxQEXTBands]int32
	tfZero              [qextCELTMaxQEXTBands]int32
	postfilterPeriod    int32
	postfilterPeriodOld int32
	postfilterGain      int16
	postfilterGainOld   int16
	postfilterTapset    int32
	postfilterTapsetOld int32
	disableInv          bool
	lossDuration        int32
	plcDuration         int32
	lastFrameType       int32
	lastPitchIndex      int32
	skipPLC             bool
	prefilterAndFold    bool
	plcLPC              [2 * celtLPCOrder]int16
	plcWindow           []int16
	rng                 uint32
	lastRes             []int32
	decodeRows          [2][]int32
	synthesisRows       [2][]int32
	extDec              rangecoding.Decoder
	dummyDec            rangecoding.Decoder
}

// NewQEXTCELTDecoder allocates a fixed-point QEXT decoder for an Opus API
// sample rate and one or two output channels. Rates below 48 kHz use the native
// 48 kHz CELT mode and emit the requested downsampled output rate.
func NewQEXTCELTDecoder(channels, sampleRate int) (*QEXTCELTDecoder, error) {
	if channels < 1 || channels > 2 {
		return nil, fmt.Errorf("QEXT CELT channels must be 1 or 2, got %d", channels)
	}
	coreSampleRate := sampleRate
	downsample := 1
	switch sampleRate {
	case 8000:
		downsample, coreSampleRate = 6, 48000
	case 12000:
		downsample, coreSampleRate = 4, 48000
	case 16000:
		downsample, coreSampleRate = 3, 48000
	case 24000:
		downsample, coreSampleRate = 2, 48000
	case 48000, 96000:
	default:
		return nil, fmt.Errorf("unsupported QEXT CELT sample rate %d", sampleRate)
	}
	var shortMDCTSize, overlap, decodeBufSize int
	var mdct *QEXTMDCTLookup
	var window []int32
	switch coreSampleRate {
	case 48000:
		shortMDCTSize, overlap, decodeBufSize = 120, 120, qextCELTDecodeBufferSize48
		mdct, window = NewStaticQEXTMDCTLookup48000(), staticQEXTMDCT48000Window[:]
	case 96000:
		shortMDCTSize, overlap, decodeBufSize = 240, 240, qextCELTDecodeBufferSize96
		mdct, window = NewStaticQEXTMDCTLookup96000(), staticQEXTMDCT96000Window[:]
	default:
		return nil, fmt.Errorf("unsupported QEXT CELT sample rate %d", sampleRate)
	}
	_, qextEdges, qextLogN, qextMaxBands, ok := fixedQEXTBandMode(coreSampleRate, shortMDCTSize)
	if !ok {
		return nil, fmt.Errorf("unsupported native QEXT CELT mode %d/%d", sampleRate, shortMDCTSize)
	}
	deemph0, deemph1, deemph3 := int16(27853), int16(0), int16(8192)
	if coreSampleRate == 96000 {
		// celt/static_modes_fixed.h mode96000_1920_240 preemph.
		deemph0, deemph1, deemph3 = 30245, 7209, 5415
	}
	return newQEXTCELTDecoderState(channels, coreSampleRate, downsample,
		shortMDCTSize, overlap, decodeBufSize, celtMaxLM, celt.MaxBands, celt.MaxBands,
		staticMDCT48000EBands[:], staticMDCT48000LogN[:], qextEdges, qextLogN,
		qextMaxBands, mdct, window, nil, deemph0, deemph1, deemph3)
}

func newQEXTCELTDecoderState(channels, sampleRate, downsample, shortMDCTSize, overlap,
	decodeBufSize, maxLM, nbEBands, effEBands int, eBands, logN, qextEdges, qextLogN []int16,
	qextMaxBands int, mdct *QEXTMDCTLookup, window []int32, customTables fixedCustomTables,
	deemph0, deemph1, deemph3 int16,
) (*QEXTCELTDecoder, error) {
	if channels < 1 || channels > 2 || sampleRate <= 0 || downsample < 1 || shortMDCTSize <= 0 ||
		overlap < 0 || overlap > qextCELTOverlapMax || decodeBufSize < 1 || maxLM < 0 || maxLM > celtMaxLM ||
		nbEBands < 1 || nbEBands > celt.MaxCustomBands || effEBands < 1 || effEBands > nbEBands ||
		len(eBands) < nbEBands+1 || len(logN) < nbEBands || len(window) < overlap || mdct == nil ||
		shortMDCTSize<<maxLM > decodeBufSize || qextMaxBands < 0 || qextMaxBands > qextCELTMaxQEXTBands {
		return nil, fmt.Errorf("unsupported QEXT CELT geometry %d/%d/%d", sampleRate, shortMDCTSize, nbEBands)
	}
	d := &QEXTCELTDecoder{
		channels: channels, sampleRate: sampleRate, downsample: downsample,
		skipPLC:       true,
		shortMDCTSize: shortMDCTSize, overlap: overlap, decodeBufSize: decodeBufSize,
		maxLM: maxLM, nbEBands: nbEBands, effEBands: effEBands,
		start: 0, end: effEBands, maxFrameSize: shortMDCTSize << maxLM,
		qextMaxBands: qextMaxBands, deemph0: deemph0, deemph1: deemph1, deemph3: deemph3,
		disableInv: channels == 1, mdct: mdct, window: window, eBands: eBands, logN: logN,
		qextEdges: qextEdges, qextLogN: qextLogN, customTables: customTables,
		plcWindow: make([]int16, overlap),
	}
	for i := 0; i < overlap; i++ {
		d.plcWindow[i] = int16(window[i] >> 16)
	}
	memSize := decodeBufSize + overlap
	d.decodeMem = make([]int32, channels*memSize)
	d.oldBandE = make([]int32, 2*nbEBands)
	d.oldLogE = make([]int32, 2*nbEBands)
	d.oldLogE2 = make([]int32, 2*nbEBands)
	d.backgroundLogE = make([]int32, 2*nbEBands)
	d.qextOldBandE = make([]int32, 2*qextCELTMaxQEXTBands)
	d.preemphMem = make([]int32, channels)
	d.freq = make([]int32, d.maxFrameSize)
	d.mdctScratch.fft = make([]FFTCpx, mdct.n/4)
	for i := range d.oldLogE {
		d.oldLogE[i] = -gconst(28)
		d.oldLogE2[i] = -gconst(28)
	}
	for _, bands := range []*celtDecodeBandsScratch{&d.baseBands, &d.qextBands} {
		bands.x = make([]int32, 2*d.maxFrameSize)
		bands.norm = make([]int32, 2*(d.maxFrameSize+d.maxFrameSize/2))
		bands.lowband = make([]int32, celtMaxBandWidth)
	}
	d.dummyDec.Init(nil)
	return d, nil
}

// SetBandRange selects the main-mode start/end band range used by the next
// received CELT frame.
func (d *QEXTCELTDecoder) SetBandRange(start, end int) {
	d.start = start
	d.end = end
}

// SetStartBand selects the next frame's CELT start band while preserving the
// current end band, matching CELT_SET_START_BAND on transition/PLC frames.
func (d *QEXTCELTDecoder) SetStartBand(start int) {
	d.start = start
}

// SetPhaseInversionDisabled controls the stereo phase inversion in CELT band
// decoding. Mono decoders start with phase inversion disabled, matching
// celt_decoder.c; Reset preserves this control.
func (d *QEXTCELTDecoder) SetPhaseInversionDisabled(disabled bool) {
	d.disableInv = disabled
}

// LastRes returns the raw int24 opus_res output from the last successful frame.
// It aliases the caller's output buffer and remains valid until the next call.
func (d *QEXTCELTDecoder) LastRes() []int32 { return d.lastRes }

// FinalRange returns the main range XOR side range when a QEXT payload is
// present, matching celt_decode_with_ec_dred's final range state.
func (d *QEXTCELTDecoder) FinalRange() uint32 { return d.rng }

// Reset clears decoder history while retaining allocated scratch and mode.
func (d *QEXTCELTDecoder) Reset() {
	clear(d.decodeMem)
	clear(d.oldBandE)
	clear(d.backgroundLogE)
	clear(d.qextOldBandE)
	clear(d.preemphMem)
	for i := range d.oldLogE {
		d.oldLogE[i] = -gconst(28)
		d.oldLogE2[i] = -gconst(28)
	}
	d.postfilterPeriod = 0
	d.postfilterPeriodOld = 0
	d.postfilterGain = 0
	d.postfilterGainOld = 0
	d.postfilterTapset = 0
	d.postfilterTapsetOld = 0
	d.lossDuration = 0
	d.plcDuration = 0
	d.lastFrameType = frameNone
	d.lastPitchIndex = 0
	d.skipPLC = true
	d.prefilterAndFold = false
	clear(d.plcLPC[:])
	d.rng = 0
	d.lastRes = nil
}

// DecodeFrameWithEC decodes one received CELT frame from a main range coder
// that the caller has already initialized and positioned after outer packet
// parsing. dataLen is the effective main CELT payload size used for tell
// validation. qextPayload contains only the side range-coded bytes, without its
// packet extension identifier. A main body of length zero or one follows the
// CELT PLC path and ignores both coders. out is caller-owned interleaved raw
// int24 opus_res storage. The return value is the per-channel sample count, or
// a negative Opus status (-1 bad argument, -2 short output, -3 coder overrun).
func (d *QEXTCELTDecoder) DecodeFrameWithEC(main *rangecoding.Decoder, dataLen, frameSize, codedChannels int, qextPayload []byte, out []int32) int {
	return d.decodeFrameWithEC(main, dataLen, frameSize, codedChannels, qextPayload, out, false)
}

// DecodeHybridAccumWithEC decodes a QEXT CELT highband from the shared main
// range coder and adds its fixed-point deemphasis output to the existing SILK
// opus_res samples in accum. main must already be positioned after SILK parsing
// and any redundancy flags. dataLen is the main CELT storage length after any
// trailing redundancy bytes have been excluded.
func (d *QEXTCELTDecoder) DecodeHybridAccumWithEC(main *rangecoding.Decoder, dataLen, frameSize, codedChannels int, qextPayload []byte, accum []int32) int {
	return d.decodeFrameWithEC(main, dataLen, frameSize, codedChannels, qextPayload, accum, true)
}

func (d *QEXTCELTDecoder) decodeFrameWithEC(main *rangecoding.Decoder, dataLen, frameSize, codedChannels int, qextPayload []byte, out []int32, accum bool) int {
	if dataLen < 0 || d.start < 0 || d.start >= d.end || d.end > d.nbEBands {
		return -1
	}
	if dataLen <= 1 {
		if accum {
			return d.decodeLost(frameSize, out, true)
		}
		return d.DecodeLost(frameSize, out)
	}
	if main == nil || codedChannels < 1 || codedChannels > 2 {
		return -1
	}
	lm := -1
	for candidate := 0; candidate <= d.maxLM; candidate++ {
		if d.shortMDCTSize<<candidate == frameSize {
			lm = candidate
			break
		}
	}
	if lm < 0 || frameSize%d.downsample != 0 {
		return -2
	}
	// libopus clears skip_plc only when a received frame begins after a run of
	// no losses. It keeps the flag through the first received frame after PLC so
	// an immediately following loss continues with noise concealment.
	if d.lossDuration == 0 {
		d.skipPLC = false
	}
	if codedChannels == 1 {
		// celt_decode_with_ec (celt/celt_decoder.c) merges the two energy
		// histories when the packet has one coded channel. Noise PLC updates only
		// oldBandE[0], so the second history can retain the pre-loss value until
		// this received frame.
		for band := 0; band < d.nbEBands; band++ {
			second := d.nbEBands + band
			d.oldBandE[band] = max32(d.oldBandE[band], d.oldBandE[second])
		}
	}
	apiFrameSize := frameSize / d.downsample
	if len(out) < d.channels*apiFrameSize {
		return -2
	}

	cc := d.channels
	N := frameSize
	M := 1 << lm
	memSize := d.decodeBufSize + d.overlap
	decodeMem := d.decodeRows[:cc]
	outSyn := d.synthesisRows[:cc]
	base := d.decodeBufSize - N
	for c := 0; c < cc; c++ {
		decodeMem[c] = d.decodeMem[c*memSize : (c+1)*memSize]
		outSyn[c] = decodeMem[c][base:]
	}

	totalBits := dataLen * 8
	tell := main.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = main.DecodeBit(15) == 1
	}
	if silence {
		tell = totalBits
		main.SkipToTell(totalBits)
	}

	postfilterPitch := 0
	postfilterTapset := 0
	var postfilterGain int16
	if d.start == 0 && tell+16 <= totalBits {
		if main.DecodeBit(1) == 1 {
			octave := int(main.DecodeUniform(6))
			postfilterPitch = (16 << octave) + int(main.DecodeRawBits(uint(4+octave))) - 1
			qg := int(main.DecodeRawBits(3))
			if main.Tell()+2 <= totalBits {
				postfilterTapset = main.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = int16(3072 * (qg + 1))
		}
		tell = main.Tell()
	}
	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = main.DecodeBit(3) == 1
		tell = main.Tell()
	}
	shortBlocks := 0
	if transient {
		shortBlocks = M
	}
	intraEnergy := false
	if tell+3 <= totalBits {
		intraEnergy = main.DecodeBit(3) == 1
	}
	if !intraEnergy && d.lossDuration != 0 {
		missing := d.lossDuration >> lm
		if missing > 10 {
			missing = 10
		}
		var safety int32
		switch lm {
		case 0:
			safety = gconst15
		case 1:
			safety = gconst05
		}
		for c := 0; c < 2; c++ {
			for band := d.start; band < d.end; band++ {
				idx := c*d.nbEBands + band
				if d.oldBandE[idx] < max32(d.oldLogE[idx], d.oldLogE2[idx]) {
					E0 := d.oldBandE[idx]
					E1 := d.oldLogE[idx]
					E2 := d.oldLogE2[idx]
					slope := max32(E1-E0, half32(E2-E0))
					slope = min32(slope, gconst(2))
					E0 -= max32(0, int32(1+missing)*slope)
					d.oldBandE[idx] = max32(-gconst(20), E0)
				} else {
					d.oldBandE[idx] = min32(min32(d.oldBandE[idx], d.oldLogE[idx]), d.oldLogE2[idx])
				}
				d.oldBandE[idx] -= safety
			}
		}
	}

	UnquantCoarseEnergy(main, d.oldBandE, d.start, d.end, d.nbEBands, codedChannels, lm, intraEnergy)
	if d.customTables != nil {
		d.decodeAlloc = d.customTables.DecodeCELTAllocation(main, totalBits, d.start, d.end, lm, codedChannels, transient)
	} else {
		d.decodeAlloc = celt.DecodeCELTAllocation(main, totalBits, d.start, d.end, lm, codedChannels, transient)
	}
	alloc := &d.decodeAlloc
	fineQuant := alloc.FineQuant[:d.nbEBands]
	finePriority := alloc.FinePriority[:d.nbEBands]
	UnquantFineEnergy(main, d.oldBandE, d.start, d.end, d.nbEBands, codedChannels, nil, fineQuant)

	// The side coder exists in every QEXT build, including when runtime QEXT is
	// absent. Its presence selects Q31 angle gains in the QEXT band kernels.
	d.extDec.Init(qextPayload)
	qextTotalBits := len(qextPayload) * (8 << bitRes)
	qextEnd := 0
	qextIntensity := 0
	qextDualStereo := 0
	qextActive := len(qextPayload) != 0 && d.end == d.effEBands
	if qextActive && d.qextMaxBands == 0 {
		return -1
	}
	if qextActive {
		header := celt.QEXTDecodeHeaderExport(&d.extDec, codedChannels, len(qextPayload))
		// The bitstream can signal bands beyond the mode spectrum.
		// quant_all_bands consumes them into normalization scratch.
		qextEnd = header.EndBands
		qextIntensity = imin(header.Intensity, qextEnd)
		if codedChannels == 2 && header.DualStereo && qextIntensity != 0 {
			qextDualStereo = 1
		}
		qextIntraEnergy := false
		if d.extDec.Tell()+3 <= d.extDec.StorageBits() {
			qextIntraEnergy = d.extDec.DecodeBit(3) == 1
		}
		UnquantCoarseEnergy(&d.extDec, d.qextOldBandE, 0, qextEnd, qextCELTMaxQEXTBands, codedChannels, lm, qextIntraEnergy)
	}
	qextBitsQ3 := qextTotalBits - main.TellFrac() - 1
	if qextBitsQ3 < 0 {
		qextBitsQ3 = 0
	}
	if !d.decodeQEXTExtraAllocation(qextEnd, qextBitsQ3, codedChannels, lm) {
		return -1
	}
	if len(qextPayload) != 0 {
		UnquantFineEnergy(&d.extDec, d.oldBandE, d.start, d.end, d.nbEBands,
			codedChannels, fineQuant, d.extraQuant[:])
	}

	moveLen := d.decodeBufSize - N + d.overlap
	for c := 0; c < cc; c++ {
		copy(decodeMem[c][:moveLen], decodeMem[c][N:N+moveLen])
	}

	seed := d.rng
	totalBitsQ3 := dataLen*(8<<bitRes) - alloc.AntiCollapseRsv
	qextState := QEXTBandDecodeState{Decoder: &d.extDec, ExtraPulses: d.extraPulses[:], TotalBitsQ3: qextTotalBits, Caps: alloc.Caps[:d.nbEBands]}
	_, _, collapse := quantAllBandsDecodeMode(main, codedChannels, N, lm, d.start, d.end,
		alloc.Pulses[:d.nbEBands], alloc.TFRes[:d.nbEBands], shortBlocks, alloc.Spread, alloc.DualStereo, alloc.Intensity,
		totalBitsQ3, alloc.Balance, alloc.CodedBands, d.disableInv, &seed,
		d.eBands, d.logN, d.nbEBands, false, false, true, &qextState, d.customTables, &d.baseBands)
	X := d.baseBands.x[:codedChannels*N]

	if qextEnd > 0 {
		extBalance := qextTotalBits - d.extDec.TellFrac()
		fineQ3 := 0
		if qextEnd > 1 {
			fineQ3 = codedChannels * int(d.extraQuant[d.nbEBands+1]<<bitRes)
		}
		for i := 0; i < qextEnd; i++ {
			extBalance -= int(d.extraPulses[d.nbEBands+i]) + fineQ3
		}
		UnquantFineEnergy(&d.extDec, d.qextOldBandE, 0, qextEnd, qextCELTMaxQEXTBands,
			codedChannels, nil, d.extraQuant[d.nbEBands:d.nbEBands+qextEnd])
		for i := 0; i < qextEnd; i++ {
			d.tfZero[i] = 0
		}
		_, _, _ = quantAllQEXTExtraBandsDecode(&d.extDec, codedChannels, N, lm, qextEnd,
			d.extraPulses[d.nbEBands:d.nbEBands+qextEnd], d.tfZero[:], shortBlocks, alloc.Spread,
			qextDualStereo, qextIntensity, qextTotalBits, extBalance, d.disableInv, &seed,
			d.qextEdges, d.qextLogN, &d.qextBands)
		first := int(d.qextEdges[0]) * M
		last := imin(int(d.qextEdges[qextEnd])*M, N)
		for c := 0; c < codedChannels; c++ {
			copy(X[c*N+first:c*N+last], d.qextBands.x[c*N+first:c*N+last])
		}
	}

	antiCollapseOn := false
	if alloc.AntiCollapseRsv > 0 {
		antiCollapseOn = main.DecodeRawBits(1) == 1
	}
	finalBandE := d.oldBandE
	if len(qextPayload) != 0 {
		finalBandE = nil
	}
	UnquantEnergyFinalise(main, finalBandE, d.start, d.end, d.nbEBands,
		codedChannels, fineQuant, finePriority, totalBits-main.Tell())
	if antiCollapseOn {
		AntiCollapse(X, collapse, lm, codedChannels, N, d.start, d.end,
			d.oldBandE, d.oldLogE, d.oldLogE2, alloc.Pulses[:d.nbEBands], d.eBands, d.nbEBands, seed, false)
	}
	if silence {
		for i := 0; i < codedChannels*d.nbEBands; i++ {
			d.oldBandE[i] = -gconst(28)
		}
	}
	if d.prefilterAndFold {
		d.prefilterAndFoldQEXT(N, decodeMem)
	}

	d.synthesisQEXT(X, N, codedChannels, cc, lm, transient, silence, qextEnd, outSyn)
	d.applyQEXTCombFilter(decodeMem, N, lm, postfilterPitch, postfilterGain, postfilterTapset)
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = int32(postfilterPitch)
	d.postfilterGain = postfilterGain
	d.postfilterTapset = int32(postfilterTapset)
	if lm != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}
	if codedChannels == 1 {
		copy(d.oldBandE[d.nbEBands:2*d.nbEBands], d.oldBandE[:d.nbEBands])
	}
	if !transient {
		copy(d.oldLogE2, d.oldLogE[:2*d.nbEBands])
		copy(d.oldLogE, d.oldBandE[:2*d.nbEBands])
	} else {
		for i := range d.oldLogE {
			d.oldLogE[i] = min32(d.oldLogE[i], d.oldBandE[i])
		}
	}
	maxBackground := gconst001 * int32(imin(160, int(d.lossDuration)+M))
	for i := range d.backgroundLogE {
		d.backgroundLogE[i] = min32(d.backgroundLogE[i]+maxBackground, d.oldBandE[i])
	}
	for c := 0; c < 2; c++ {
		for i := 0; i < d.start; i++ {
			d.oldBandE[c*d.nbEBands+i] = 0
			d.oldLogE[c*d.nbEBands+i] = -gconst(28)
			d.oldLogE2[c*d.nbEBands+i] = -gconst(28)
		}
		for i := d.end; i < d.nbEBands; i++ {
			d.oldBandE[c*d.nbEBands+i] = 0
			d.oldLogE[c*d.nbEBands+i] = -gconst(28)
			d.oldLogE2[c*d.nbEBands+i] = -gconst(28)
		}
	}
	if len(qextPayload) != 0 {
		d.rng = main.Range() ^ d.extDec.Range()
	} else {
		d.rng = main.Range()
	}
	d.lossDuration = 0
	d.plcDuration = 0
	d.lastFrameType = frameNormal
	d.prefilterAndFold = false
	d.lastRes = out[:cc*apiFrameSize]
	deemphasisQEXT(outSyn, d.lastRes, frameSize, cc, d.downsample, d.preemphMem, accum,
		d.deemph0, d.deemph1, d.deemph3)
	if main.Tell() > totalBits || (len(qextPayload) != 0 && d.extDec.Tell() > qextTotalBits/(1<<bitRes)) {
		return -3
	}
	return apiFrameSize
}
