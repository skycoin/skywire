package silk

import (
	"errors"

	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Opus-level voice activity decisions passed to PacketEncoder.Encode
// (VAD_NO_DECISION and VAD_NO_ACTIVITY, silk/define.h). Any other value means
// the Opus layer detected activity.
const (
	VADNoDecision = -1
	VADNoActivity = 0
)

// bitReservoirDecayTimeMs is BITRESERVOIR_DECAY_TIME_MS (silk/tuning_parameters.h).
const bitReservoirDecayTimeMs = 500

// ErrInvalidSampleCount reports an input length Encode cannot code: a prefill
// that is not exactly 10 ms, or a packet that is not a whole number of 10 ms
// blocks within the configured payload size (SILK_ENC_INPUT_INVALID_NO_OF_SAMPLES).
var ErrInvalidSampleCount = errors.New("silk: invalid number of input samples")

// EncControl mirrors silk_EncControlStruct (silk/control.h): the controls the
// Opus encoder sets before each Encode call and the status Encode reports back.
type EncControl struct {
	NChannelsAPI              int32 // I: number of API channels (1 or 2)
	NChannelsInternal         int32 // I: number of coded channels (1 or 2)
	APISampleRate             int32 // I: input sampling rate in Hz
	MaxInternalSampleRate     int32 // I: maximum internal sampling rate in Hz (8000, 12000 or 16000)
	MinInternalSampleRate     int32 // I: minimum internal sampling rate in Hz (8000, 12000 or 16000)
	DesiredInternalSampleRate int32 // I: soft request for the internal sampling rate in Hz
	PayloadSizeMs             int32 // I: packet duration in ms (10, 20, 40 or 60)
	BitRate                   int32 // I: target bitrate in bits/s
	PacketLossPercentage      int32 // I: uplink packet loss in percent (0-100)
	Complexity                int32 // I: complexity (0-10)
	LBRRCoded                 bool  // I: code in-band FEC (LBRR) in this packet
	UseDTX                    bool  // I: discontinuous transmission
	UseCBR                    bool  // I: constant bitrate
	MaxBits                   int32 // I: maximum number of bits for the packet
	ToMono                    bool  // I: last frame before a stereo->mono transition
	OpusCanSwitch             bool  // I: the Opus encoder allows an internal bandwidth switch
	ReducedDependency         bool  // I: code every packet as the first after a reset

	InternalSampleRate        int32 // O: internal sampling rate in Hz
	AllowBandwidthSwitch      bool  // O: low speech activity allows a bandwidth switch
	InWBModeWithoutVariableLP bool  // O: wideband with the variable LP filter idle
	StereoWidthQ14            int32 // O: smoothed stereo width (Q14)
	SwitchReady               bool  // O: ready for the internal bandwidth switch
	SignalType                int32 // O: signal type of the last coded frame
	Offset                    int32 // O: quantization offset of the last coded frame
}

// PacketEncoder is the SILK encoder of one Opus stream (silk_encoder,
// silk/float/structs_FLP.h): the per-channel encoder states, the stereo front
// end and the packet-level rate control that Encode (silk_Encode,
// silk/enc_API.c) drives.
type PacketEncoder struct {
	state                    [2]*Encoder    // state_Fxx
	stereo                   stereoEncState // sStereo
	nBitsUsedLBRR            int32
	nBitsExceeded            int32
	nChannelsAPI             int32
	nChannelsInternal        int32
	nPrevChannelsInternal    int32
	timeSinceSwitchAllowedMs int32
	allowBandwidthSwitch     bool
	prevDecodeOnlyMiddle     bool

	channels int // channel states silk_InitEncoder set up

	buf           []int16 // API-rate int16 input block (buf in silk_Encode)
	stereoScratch stereoLRToMSScratch
}

// NewPacketEncoder creates the SILK encoder for a stream of channels (1 or 2)
// in the state silk_InitEncoder leaves it: no internal sampling rate is set
// until the first Encode call picks one from its controls.
func NewPacketEncoder(channels int) *PacketEncoder {
	s := &PacketEncoder{channels: channels}
	for n := range channels {
		s.state[n] = newEncoder()
	}
	s.initPacketState()
	return s
}

// initPacketState clears the packet-level state the way silk_InitEncoder does.
func (s *PacketEncoder) initPacketState() {
	s.stereo = stereoEncState{}
	s.nBitsUsedLBRR = 0
	s.nBitsExceeded = 0
	s.nChannelsAPI = 1
	s.nChannelsInternal = 1
	s.nPrevChannelsInternal = 0
	s.timeSinceSwitchAllowedMs = 0
	s.allowBandwidthSwitch = false
	s.prevDecodeOnlyMiddle = false
}

// Init resets the encoder as silk_InitEncoder (silk/enc_API.c) does: every
// channel state, the stereo state and the packet-level rate control.
func (s *PacketEncoder) Init() {
	for n := range s.channels {
		s.state[n].reset()
	}
	s.initPacketState()
}

// InDTX reports the SILK half of OPUS_GET_IN_DTX (src/opus_encoder.c): the
// first channel has gone NB_SPEECH_FRAMES_BEFORE_DTX frames without speech,
// and so has the side channel when the last stereo frame coded it.
func (s *PacketEncoder) InDTX(nChannelsInternal int32) bool {
	inDTX := s.state[0].noSpeechCounter >= nbSpeechFramesBeforeDTX
	if inDTX && nChannelsInternal == 2 && !s.prevDecodeOnlyMiddle {
		inDTX = s.state[1].noSpeechCounter >= nbSpeechFramesBeforeDTX
	}
	return inDTX
}

// VariableHPSmth1Q15 returns the smoothed log-domain high-pass cutoff of the
// first channel (state_Fxx[0].sCmn.variable_HP_smth1_Q15), which drives the
// Opus-level hp_cutoff() for VoIP input.
func (s *PacketEncoder) VariableHPSmth1Q15() int32 {
	return s.state[0].variableHPSmth1Q15
}

// Encode is silk_Encode (silk/enc_API.c): it codes nSamplesIn samples per
// channel of interleaved API-rate input (opus_res, float32 in [-1, 1]) as one
// SILK packet into re and returns the payload size in bytes, 0 when every
// channel is in DTX. prefill (1, or 2 to keep the LP transition state) resets
// the channel states and runs 10 ms of input through the analysis buffers
// without coding; re may then be nil. activity is the Opus-level voice activity
// decision (VADNoDecision, VADNoActivity or active).
func (s *PacketEncoder) Encode(ctl *EncControl, samplesIn []float32, nSamplesIn int, re *rangecoding.Encoder, prefill, activity int) (int32, error) {
	return s.encode(ctl, samplesIn, nil, nSamplesIn, re, prefill, activity)
}

func (s *PacketEncoder) encode(ctl *EncControl, samplesIn []float32, samplesResQ8 []int32, nSamplesIn int, re *rangecoding.Encoder, prefill, activity int) (int32, error) {
	nChannelsAPI := int(ctl.NChannelsAPI)
	nChannelsInternal := int(ctl.NChannelsInternal)
	ctl.SwitchReady = false
	if ctl.ReducedDependency {
		for n := range nChannelsAPI {
			s.state[n].firstFrameAfterReset = true
		}
	}
	for n := range nChannelsAPI {
		s.state[n].nFramesEncoded = 0
	}

	if ctl.NChannelsInternal > s.nChannelsInternal {
		// Mono -> stereo transition: init the state of the second channel and
		// the stereo state.
		s.state[1].reset()
		s.stereo.predPrevQ13 = [2]int16{}
		s.stereo.sSide = [2]int16{}
		s.stereo.midSideAmpQ0 = [4]int32{0, 1, 0, 1}
		s.stereo.widthPrevQ14 = 0
		s.stereo.smthWidthQ14 = 1 << 14
	}

	transition := ctl.PayloadSizeMs != s.state[0].packetSizeMs || s.nChannelsInternal != ctl.NChannelsInternal

	s.nChannelsAPI = ctl.NChannelsAPI
	s.nChannelsInternal = ctl.NChannelsInternal

	nBlocksOf10ms := 100 * nSamplesIn / int(ctl.APISampleRate)
	totBlocks := 1
	if nBlocksOf10ms > 1 {
		totBlocks = nBlocksOf10ms >> 1
	}
	currBlock := 0
	var tmpPayloadSizeMs, tmpComplexity int32
	if prefill != 0 {
		// Only accept input length of 10 ms.
		if nBlocksOf10ms != 1 {
			return 0, ErrInvalidSampleCount
		}
		var saveLP LPState
		if prefill == 2 {
			// Save the sampling rate so the bandwidth switching code can keep
			// handling transitions.
			saveLP = s.state[0].lpState
			saveLP.SavedFsKHz = s.state[0].fsKHz
		}
		for n := range nChannelsInternal {
			s.state[n].reset()
			if prefill == 2 {
				s.state[n].lpState = saveLP
			}
		}
		tmpPayloadSizeMs = ctl.PayloadSizeMs
		ctl.PayloadSizeMs = 10
		tmpComplexity = ctl.Complexity
		ctl.Complexity = 0
		for n := range nChannelsInternal {
			s.state[n].controlledSinceLastPayload = false
			s.state[n].prefillFlag = true
		}
	} else if nSamplesIn < 0 || nBlocksOf10ms*int(ctl.APISampleRate) != 100*nSamplesIn ||
		1000*nSamplesIn > int(ctl.PayloadSizeMs)*int(ctl.APISampleRate) {
		// Only accept input lengths that are a multiple of 10 ms, and make sure
		// no more than one packet can be produced.
		return 0, ErrInvalidSampleCount
	}

	for n := range nChannelsInternal {
		// Force the side channel to the same rate as the mid.
		var forceFsKHz int32
		if n == 1 {
			forceFsKHz = s.state[0].fsKHz
		}
		s.state[n].control(ctl, s.allowBandwidthSwitch, forceFsKHz)
		if s.state[n].firstFrameAfterReset || transition {
			for i := range s.state[0].nFramesPerPacket {
				s.state[n].lbrrFlags[i] = 0
			}
		}
		s.state[n].inDTX = s.state[n].useDTX
	}

	// Input buffering/resampling and encoding.
	st0 := s.state[0]
	fsKHz := st0.fsKHz
	nSamplesToBufferMax := 10 * int32(nBlocksOf10ms) * fsKHz
	nSamplesFromInputMax := nSamplesToBufferMax * st0.apiFsHz / (fsKHz * 1000)
	buf := ensureInt16Slice(&s.buf, int(nSamplesFromInputMax))
	var nBytesOut int32
	for {
		currNBitsUsedLBRR := 0
		nSamplesToBuffer := min(st0.frameLength-st0.inputBufIx, nSamplesToBufferMax)
		nSamplesFromInput := int(nSamplesToBuffer * st0.apiFsHz / (fsKHz * 1000))
		in := buf[:nSamplesFromInput]
		switch {
		case nChannelsAPI == 2 && nChannelsInternal == 2:
			st1 := s.state[1]
			id := st0.nFramesEncoded
			for n := range in {
				if silkFixedEncodeBuild && samplesResQ8 != nil {
					in[n] = silkResQ8ToInt16(samplesResQ8[2*n])
				} else {
					in[n] = opusmath.Float32ToInt16(samplesIn[2*n])
				}
			}
			// Make sure to start both resamplers from the same state when
			// switching from mono to stereo.
			if s.nPrevChannelsInternal == 1 && id == 0 {
				st1.resampler.CopyFrom(st0.resampler)
			}
			st0.resampler.Resample(st0.inputBuf[st0.inputBufIx+2:st0.inputBufIx+2+nSamplesToBuffer], in)
			st0.inputBufIx += nSamplesToBuffer

			nSamplesToBuffer1 := min(st1.frameLength-st1.inputBufIx, 10*int32(nBlocksOf10ms)*st1.fsKHz)
			for n := range in {
				if silkFixedEncodeBuild && samplesResQ8 != nil {
					in[n] = silkResQ8ToInt16(samplesResQ8[2*n+1])
				} else {
					in[n] = opusmath.Float32ToInt16(samplesIn[2*n+1])
				}
			}
			st1.resampler.Resample(st1.inputBuf[st1.inputBufIx+2:st1.inputBufIx+2+nSamplesToBuffer1], in)
			st1.inputBufIx += nSamplesToBuffer1
		case nChannelsAPI == 2 && nChannelsInternal == 1:
			// Combine left and right channels before resampling.
			for n := range in {
				var sum int32
				if silkFixedEncodeBuild && samplesResQ8 != nil {
					sum = int32(silkResQ8ToInt16(samplesResQ8[2*n] + samplesResQ8[2*n+1]))
				} else {
					sum = int32(opusmath.Float32ToInt16(samplesIn[2*n] + samplesIn[2*n+1]))
				}
				in[n] = int16(silkRSHIFT_ROUND(sum, 1))
			}
			st0.resampler.Resample(st0.inputBuf[st0.inputBufIx+2:st0.inputBufIx+2+nSamplesToBuffer], in)
			// On the first mono frame, average the results for the two
			// resampler states.
			if s.nPrevChannelsInternal == 2 && st0.nFramesEncoded == 0 {
				st1 := s.state[1]
				st1.resampler.Resample(st1.inputBuf[st1.inputBufIx+2:st1.inputBufIx+2+nSamplesToBuffer], in)
				for n := range st0.frameLength {
					a := int32(st0.inputBuf[st0.inputBufIx+n+2])
					b := int32(st1.inputBuf[st1.inputBufIx+n+2])
					st0.inputBuf[st0.inputBufIx+n+2] = int16(silkRSHIFT(a+b, 1))
				}
			}
			st0.inputBufIx += nSamplesToBuffer
		default:
			for n := range in {
				if silkFixedEncodeBuild && samplesResQ8 != nil {
					in[n] = silkResQ8ToInt16(samplesResQ8[n])
				} else {
					in[n] = opusmath.Float32ToInt16(samplesIn[n])
				}
			}
			st0.resampler.Resample(st0.inputBuf[st0.inputBufIx+2:st0.inputBufIx+2+nSamplesToBuffer], in)
			st0.inputBufIx += nSamplesToBuffer
		}
		if silkFixedEncodeBuild && samplesResQ8 != nil {
			samplesResQ8 = samplesResQ8[nSamplesFromInput*nChannelsAPI:]
		} else {
			samplesIn = samplesIn[nSamplesFromInput*nChannelsAPI:]
		}
		nSamplesIn -= nSamplesFromInput

		// Default.
		s.allowBandwidthSwitch = false

		if st0.inputBufIx < st0.frameLength {
			break
		}

		// Enough data in the input buffer, so encode.
		if st0.nFramesEncoded == 0 && prefill == 0 {
			currNBitsUsedLBRR = s.encodeLBRR(re, nChannelsInternal)
		}

		st0.hpVariableCutoff()

		// Total target bits for the packet.
		nBits := silkDiv32_16(silkMUL(ctl.BitRate, ctl.PayloadSizeMs), 1000)
		// Subtract bits used for LBRR.
		if prefill == 0 {
			// nBitsUsedLBRR is an exponential moving average of the LBRR usage,
			// except that for the first LBRR frame it does no averaging and
			// for the first frame after LBRR it goes back to zero immediately.
			switch {
			case currNBitsUsedLBRR < 10:
				s.nBitsUsedLBRR = 0
			case s.nBitsUsedLBRR < 10:
				s.nBitsUsedLBRR = int32(currNBitsUsedLBRR)
			default:
				s.nBitsUsedLBRR = (s.nBitsUsedLBRR + int32(currNBitsUsedLBRR)) / 2
			}
			nBits -= s.nBitsUsedLBRR
		}
		// Divide by the number of uncoded frames left in the packet.
		nBits = silkDiv32_16(nBits, st0.nFramesPerPacket)
		// Convert to bits/second.
		var targetRateBps int32
		if ctl.PayloadSizeMs == 10 {
			targetRateBps = silkSMULBB(nBits, 100)
		} else {
			targetRateBps = silkSMULBB(nBits, 50)
		}
		// Subtract the fraction of bits in excess of the target in previous
		// frames and packets.
		targetRateBps -= silkDiv32_16(silkMUL(s.nBitsExceeded, 1000), bitReservoirDecayTimeMs)
		if prefill == 0 && st0.nFramesEncoded > 0 {
			// Compare actual vs target bits so far in this packet.
			bitsBalance := int32(re.Tell()) - s.nBitsUsedLBRR - nBits*st0.nFramesEncoded
			targetRateBps -= silkDiv32_16(silkMUL(bitsBalance, 1000), bitReservoirDecayTimeMs)
		}
		// Never exceed the input bitrate.
		targetRateBps = silkLimit32(targetRateBps, ctl.BitRate, 5000)

		// Convert left/right to mid/side.
		var msTargetRatesBps [2]int32
		frameIdx := st0.nFramesEncoded
		if nChannelsInternal == 2 {
			st1 := s.state[1]
			ix, midOnly, rates := silkStereoLRToMS(&s.stereo, st0.inputBuf[:], st1.inputBuf[:],
				targetRateBps, st0.speechActivityQ8, ctl.ToMono, int(fsKHz), int(st0.frameLength), &s.stereoScratch)
			s.stereo.predIx[frameIdx] = ix
			s.stereo.midOnlyFlags[frameIdx] = midOnly
			msTargetRatesBps = rates
			if midOnly == 0 {
				// Reset the side channel encoder memory for the first frame
				// with side coding.
				if s.prevDecodeOnlyMiddle {
					st1.resetAnalysisHistory()
				}
				st1.encodeDoVAD(activity)
			} else {
				st1.vadFlags[frameIdx] = false
			}
			if prefill == 0 {
				stereoEncodePred(re, ix)
				if !st1.vadFlags[frameIdx] {
					stereoEncodeMidOnly(re, midOnly)
				}
			}
		} else {
			// Buffering.
			copy(st0.inputBuf[:2], s.stereo.sMid[:])
			copy(s.stereo.sMid[:], st0.inputBuf[st0.frameLength:st0.frameLength+2])
		}
		st0.encodeDoVAD(activity)

		// Encode.
		for n := range nChannelsInternal {
			st := s.state[n]
			// Handling rate constraints.
			maxBits := ctl.MaxBits
			switch {
			case totBlocks == 2 && currBlock == 0:
				maxBits = maxBits * 3 / 5
			case totBlocks == 3 && currBlock == 0:
				maxBits = maxBits * 2 / 5
			case totBlocks == 3 && currBlock == 1:
				maxBits = maxBits * 3 / 4
			}
			useCBR := ctl.UseCBR && currBlock == totBlocks-1

			var channelRateBps int32
			if nChannelsInternal == 1 {
				channelRateBps = targetRateBps
			} else {
				channelRateBps = msTargetRatesBps[n]
				if n == 0 && msTargetRatesBps[1] > 0 {
					useCBR = false
					// Give mid up to 1/2 of the max bits for that frame.
					maxBits -= ctl.MaxBits / int32(totBlocks*2)
				}
			}

			if channelRateBps > 0 {
				st.controlSNR(int(channelRateBps), int(st.nbSubfr))

				// Use independent coding if no previous frame is available.
				condCoding := codeConditionally
				if st0.nFramesEncoded-int32(n) <= 0 {
					condCoding = codeIndependently
				} else if n > 0 && s.prevDecodeOnlyMiddle {
					// If we skipped a side frame in this packet, we don't
					// need LTP scaling; the LTP state is well-defined.
					condCoding = codeIndependentlyNoLtpScaling
				}
				nBytesOut = st.encodeFrame(re, condCoding, int(maxBits), useCBR)
			}
			st.controlledSinceLastPayload = false
			st.inputBufIx = 0
			st.nFramesEncoded++
		}
		s.prevDecodeOnlyMiddle = s.stereo.midOnlyFlags[st0.nFramesEncoded-1] != 0

		// Insert VAD and FEC flags at beginning of bitstream.
		if nBytesOut > 0 && st0.nFramesEncoded == st0.nFramesPerPacket {
			flags := uint32(0)
			for n := range nChannelsInternal {
				st := s.state[n]
				for i := range st.nFramesPerPacket {
					flags <<= 1
					if st.vadFlags[i] {
						flags |= 1
					}
				}
				flags = flags<<1 | uint32(st.lbrrFlag)
			}
			if prefill == 0 {
				re.PatchInitialBits(flags, uint((st0.nFramesPerPacket+1)*int32(nChannelsInternal)))
			}

			// Return zero bytes if all channels DTXed.
			if st0.inDTX && (nChannelsInternal == 1 || s.state[1].inDTX) {
				nBytesOut = 0
			}

			s.nBitsExceeded += nBytesOut * 8
			s.nBitsExceeded -= silkDiv32_16(silkMUL(ctl.BitRate, ctl.PayloadSizeMs), 1000)
			s.nBitsExceeded = silkLimit32(s.nBitsExceeded, 0, 10000)

			s.updateAllowBandwidthSwitch(st0.speechActivityQ8, ctl.PayloadSizeMs)
		}

		if nSamplesIn == 0 {
			break
		}
		currBlock++
	}

	s.nPrevChannelsInternal = ctl.NChannelsInternal

	ctl.AllowBandwidthSwitch = s.allowBandwidthSwitch
	ctl.InWBModeWithoutVariableLP = st0.fsKHz == 16 && st0.lpState.Mode == 0
	ctl.InternalSampleRate = st0.fsKHz * 1000
	ctl.StereoWidthQ14 = int32(s.stereo.smthWidthQ14)
	if ctl.ToMono {
		ctl.StereoWidthQ14 = 0
	}
	if prefill != 0 {
		ctl.PayloadSizeMs = tmpPayloadSizeMs
		ctl.Complexity = tmpComplexity
		for n := range nChannelsInternal {
			s.state[n].controlledSinceLastPayload = false
			s.state[n].prefillFlag = false
		}
	}
	ctl.SignalType, ctl.Offset = st0.lastEncodedSignalInfo()
	return nBytesOut, nil
}

// silkResQ8ToInt16 ports RES2INT16(a) = SAT16(PSHR32(a, RES_SHIFT)) in
// celt/arch.h for FIXED_POINT+ENABLE_RES24. PSHR32 adds 1<<(RES_SHIFT-1)
// before the arithmetic shift.
func silkResQ8ToInt16(sample int32) int16 {
	value := (sample + 1<<7) >> 8
	if value > 1<<15-1 {
		return 1<<15 - 1
	}
	if value < -1<<15 {
		return -1 << 15
	}
	return int16(value)
}

// updateAllowBandwidthSwitch updates the flag indicating if bandwidth switching
// is allowed at the end of a packet (silk/enc_API.c): the speech activity
// threshold rises with the time since the last switch was allowed.
func (s *PacketEncoder) updateAllowBandwidthSwitch(speechActivityQ8, payloadSizeMs int32) {
	speechActThrForSwitchQ8 := silkSMLAWB(speechActivityDTXThresholdQ8, bandwidthSwitchDelaySlopeQ24Q8, s.timeSinceSwitchAllowedMs)
	if speechActivityQ8 < speechActThrForSwitchQ8 {
		s.allowBandwidthSwitch = true
		s.timeSinceSwitchAllowedMs = 0
		return
	}
	s.allowBandwidthSwitch = false
	s.timeSinceSwitchAllowedMs += payloadSizeMs
}

// encodeLBRR writes the packet start of silk_Encode (silk/enc_API.c): the
// reserved VAD/FEC flag bits and the LBRR data of the previous packet. It
// returns the number of bits the LBRR data used.
func (s *PacketEncoder) encodeLBRR(re *rangecoding.Encoder, nChannelsInternal int) int {
	st0 := s.state[0]
	// Create space at start of payload for VAD and FEC flags.
	nFlagBits := (st0.nFramesPerPacket + 1) * int32(nChannelsInternal)
	iCDF := [2]uint8{uint8(256 - (int32(256) >> nFlagBits)), 0}
	re.EncodeICDF(0, iCDF[:], 8)
	lbrrStart := re.Tell()

	// Encode LBRR flags.
	for n := range nChannelsInternal {
		st := s.state[n]
		lbrrSymbol := 0
		for i := range st.nFramesPerPacket {
			lbrrSymbol |= int(st.lbrrFlags[i]) << i
		}
		st.lbrrFlag = 0
		if lbrrSymbol > 0 {
			st.lbrrFlag = 1
		}
		if lbrrSymbol != 0 && st.nFramesPerPacket > 1 {
			re.EncodeICDF(lbrrSymbol-1, silk_LBRR_flags_iCDF_ptr[st.nFramesPerPacket-2], 8)
		}
	}

	// Code LBRR indices and excitation signals.
	var prevSignalType, prevLagIndex [2]int
	for i := range int(st0.nFramesPerPacket) {
		for n := range nChannelsInternal {
			st := s.state[n]
			if st.lbrrFlags[i] == 0 {
				continue
			}
			if nChannelsInternal == 2 && n == 0 {
				stereoEncodePred(re, s.stereo.predIx[i])
				// For LBRR data there's no need to code the mid-only flag if
				// the side-channel LBRR flag is set.
				if s.state[1].lbrrFlags[i] == 0 {
					stereoEncodeMidOnly(re, s.stereo.midOnlyFlags[i])
				}
			}
			// Use conditional coding if the previous frame is available.
			condCoding := codeIndependently
			if i > 0 && st.lbrrFlags[i-1] != 0 {
				condCoding = codeConditionally
			}
			st.encodeLBRRIndices(re, i, condCoding, &prevSignalType[n], &prevLagIndex[n])
			st.encodeLBRRPulses(re, i)
		}
	}

	// Reset LBRR flags.
	for n := range nChannelsInternal {
		s.state[n].lbrrFlags = [maxFramesPerPacket]int32{}
	}
	return re.Tell() - lbrrStart
}

// encodeDoVAD is silk_encode_do_VAD_FLP (silk/float/encode_frame_FLP.c): it
// runs the voice activity detector on the channel's frame and converts the
// speech activity into the frame's VAD flag and the DTX state. activity is the
// Opus-level decision; when it reports no activity the SILK activity is
// lowered to just under the threshold.
func (e *Encoder) encodeDoVAD(activity int) {
	const activityThreshold = speechActivityDTXThresholdQ8
	frameLength := int(e.frameLength)
	res := silkVADGetSAQ8(&e.vadScratch, &e.vad, e.inputBuf[1:1+frameLength], frameLength, int(e.fsKHz))
	e.speechActivityQ8 = res.speechActivityQ8
	e.inputTiltQ15 = res.inputTiltQ15
	e.inputQualityBandsQ15 = res.inputQualityBandsQ15
	if activity == VADNoActivity && e.speechActivityQ8 >= activityThreshold {
		e.speechActivityQ8 = activityThreshold - 1
	}

	if e.speechActivityQ8 < activityThreshold {
		e.noSpeechCounter++
		if e.noSpeechCounter <= nbSpeechFramesBeforeDTX {
			e.inDTX = false
		} else if e.noSpeechCounter > maxConsecutiveDTX+nbSpeechFramesBeforeDTX {
			e.noSpeechCounter = nbSpeechFramesBeforeDTX
			e.inDTX = false
		}
		e.vadFlags[e.nFramesEncoded] = false
		return
	}
	e.noSpeechCounter = 0
	e.inDTX = false
	e.vadFlags[e.nFramesEncoded] = true
}

// stereoEncodePred is silk_stereo_encode_pred (silk/stereo_encode_pred.c).
func stereoEncodePred(re *rangecoding.Encoder, ix [2][3]int8) {
	n := 5*int(ix[0][2]) + int(ix[1][2])
	re.EncodeICDF(n, silk_stereo_pred_joint_iCDF, 8)
	for i := range 2 {
		re.EncodeICDF(int(ix[i][0]), silk_uniform3_iCDF, 8)
		re.EncodeICDF(int(ix[i][1]), silk_uniform5_iCDF, 8)
	}
}

// stereoEncodeMidOnly is silk_stereo_encode_mid_only
// (silk/stereo_encode_pred.c).
func stereoEncodeMidOnly(re *rangecoding.Encoder, midOnlyFlag int8) {
	re.EncodeICDF(int(midOnlyFlag), silk_stereo_only_code_mid_iCDF, 8)
}
