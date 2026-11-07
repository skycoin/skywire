package hybrid

import (
	"errors"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// Constants for Hybrid mode
const (
	// HybridCELTStartBand is the first CELT band decoded in hybrid mode.
	// Bands 0-16 are covered by SILK; CELT starts at band 17 and stops at the
	// configured bandwidth limit.
	HybridCELTStartBand = 17

	// SilkCELTDelay is the libopus SILK/CELT delay in samples at 48 kHz.
	SilkCELTDelay = celt.SilkCELTDelay
)

// Errors for Hybrid decoding
var (
	// ErrInvalidFrameSize indicates a frame size unsupported by Hybrid decoding.
	// Encoded Hybrid frames use 10 ms or 20 ms (480 or 960 samples in the 48 kHz
	// codec domain); PLC also accepts 2.5 ms and 5 ms concealment frames.
	ErrInvalidFrameSize = errors.New("hybrid: invalid frame size (only 10ms/20ms supported)")

	// ErrDecodeFailed indicates a frame decode error.
	ErrDecodeFailed = errors.New("hybrid: frame decode failed")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("hybrid: nil range decoder")
)

// Decoder decodes Hybrid frames with a SILK low band and CELT high bands.
// It keeps separate child decoder state, passes the packet's shared range
// decoder through both layers, and combines their PCM at the configured API
// rate. A Decoder and its child state belong to one stream; it is not safe for
// concurrent calls.
type Decoder struct {
	// Sub-decoders
	silkDecoder *silk.Decoder
	celtDecoder *celt.Decoder

	// Note: Resamplers are NOT stored here. We use the SILK decoder's built-in
	// resamplers via GetResampler() and GetResamplerRightChannel() to ensure
	// resampler state persists across SILK-only <-> Hybrid mode transitions.
	// This matches libopus behavior where the silk_DecControl.resampler_state
	// is shared across all decoding modes.

	// Track previous packet stereo flag for transition handling.
	prevPacketStereo bool

	// Channel count (1 for mono, 2 for stereo), matching libopus C int state width.
	channels int32
	// Decoder API sample rate. Hybrid CELT stays packet-domain internally but
	// emits API-rate PCM, matching libopus OpusDecoder Fs.
	apiSampleRate int32

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	// Max frame size is 960 samples at 48kHz (20ms), stereo needs 960*2 = 1920 samples.
	scratchSilkUpsampled []float32 // SILK upsampled output (max 960*2 for stereo 20ms)
	plcMonoScratch       []float32 // mono SILK PLC before stereo duplication

	// fixedHighband, when set, drives the FIXED_POINT integer CELT highband
	// decode for the in-flight integer-output (DecodeInt16 / DecodeInt24) packet.
	// It is nil on the float Decode path and in the default build, so the float
	// hybrid decode is unchanged. When set, decodeFrameWithHookFloat32 captures
	// the resampled int16 SILK lowband and, once the shared range decoder is
	// positioned at the CELT start band, hands a clone of it (plus the SILK
	// lowband) to the hook for the integer accum decode.
	fixedHighband    FixedHybridHighband
	fixedRangeClone  rangecoding.Decoder
	scratchSilkInt16 []int16
	scratchSilkL     []int16
	scratchSilkR     []int16
	filledSilkInt16  int
}

// FixedHybridHighband receives the data needed to run the FIXED_POINT integer
// CELT highband decode for a hybrid frame, mirroring the libopus
// opus_decode_frame hybrid path (start_band=17, celt_accum=1 onto the SILK
// opus_res lowband). It is implemented in the gopus_fixed_point build by the
// root decoder.
type FixedHybridHighband interface {
	// DecodeHybridHighband is called after the SILK lowband has been decoded and
	// resampled and the shared range decoder rd has been positioned at the CELT
	// start band (after any hybrid redundancy-flag bits, with storage shrunk to
	// exclude trailing redundancy bytes). silkInt16 holds the resampled int16
	// SILK lowband output interleaved by channel (length filled samples). rd is a
	// clone of the shared decoder safe to consume independently of the float CELT
	// decode. frameSizeAPI / frameSize48 are the per-channel API-rate and 48k-core
	// sample counts.
	DecodeHybridHighband(silkInt16 []int16, filled int, rd *rangecoding.Decoder, dataLen, frameSizeAPI, frameSize48 int, packetStereo bool)
}

// SetFixedHighband installs (or clears, with nil) the integer hybrid highband
// hook for the next decode. It is a no-op in effect on the float path because
// the root decoder only sets it while an integer-output packet is active.
func (d *Decoder) SetFixedHighband(h FixedHybridHighband) {
	d.fixedHighband = h
}

// NewDecoder creates a Hybrid decoder for one or two channels. Values below one
// select mono; values above two select stereo. The API sample rate defaults to
// 48 kHz, and Hybrid frames use the wideband SILK decoder plus CELT high bands.
func NewDecoder(channels int) *Decoder {
	return NewDecoderWithSharedDecoders(channels, nil, nil)
}

// NewDecoderWithSharedDecoders creates a Hybrid decoder using the supplied SILK
// and CELT decoders where non-nil. The caller configures those child decoders
// for the same channel count and stream, then shares them across mode decoders
// to preserve their histories.
func NewDecoderWithSharedDecoders(channels int, silkDec *silk.Decoder, celtDec *celt.Decoder) *Decoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	// Initial scratch covers a 20 ms frame for the configured channel count
	// at 48 kHz. QEXT 96 kHz output grows it on demand.
	maxSamples := 960 * channels

	if silkDec == nil {
		silkDec = silk.NewDecoder()
	}
	if celtDec == nil {
		celtDec = celt.NewDecoder(channels)
	}

	return &Decoder{
		silkDecoder: silkDec,
		celtDecoder: celtDec,

		channels:      int32(channels),
		apiSampleRate: 48000,
		plcState:      plc.NewState(),

		// Pre-allocate scratch buffers for zero-alloc decode path
		scratchSilkUpsampled: make([]float32, maxSamples),
	}
}

// SetAPISampleRate sets the output rate for Hybrid PCM. Rates of 8, 12, 16, 24,
// and 48 kHz update the SILK resampler and CELT downsample factor. With QEXT,
// 96 kHz selects native CELT mode, which the parent decoder configures. Without
// QEXT, a 96 kHz request leaves the current rate unchanged. Other values select
// 48 kHz.
func (d *Decoder) SetAPISampleRate(sampleRate int) {
	switch sampleRate {
	case 8000, 12000, 16000, 24000, 48000:
		d.apiSampleRate = int32(sampleRate)
		if d.silkDecoder != nil {
			d.silkDecoder.SetAPISampleRate(sampleRate)
		}
		if d.celtDecoder != nil {
			d.celtDecoder.SetDownsample(48000 / sampleRate)
		}
	case 96000:
		if extsupport.QEXT {
			d.apiSampleRate = 96000
			if d.silkDecoder != nil {
				d.silkDecoder.SetAPISampleRate(96000)
			}
			// The shared CELT decoder is configured for its native 96 kHz
			// mode by the top-level Opus decoder after this rate is selected.
		}
	default:
		d.apiSampleRate = 48000
		if d.silkDecoder != nil {
			d.silkDecoder.SetAPISampleRate(48000)
		}
		if d.celtDecoder != nil {
			d.celtDecoder.SetDownsample(1)
		}
	}
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	// Reset sub-decoders
	d.silkDecoder.Reset()
	d.celtDecoder.Reset()

	d.prevPacketStereo = false
	if d.plcState != nil {
		d.plcState.Reset()
	}
}

// SetPrevPacketStereo synchronizes the previous packet stereo flag.
// This is used when Hybrid decoding is driven by an external Opus decoder.
func (d *Decoder) SetPrevPacketStereo(stereo bool) {
	d.prevPacketStereo = stereo
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return int(d.channels)
}

// SetBandwidth sets the CELT bandwidth for hybrid decoding.
func (d *Decoder) SetBandwidth(bw celt.CELTBandwidth) {
	d.celtDecoder.SetBandwidth(bw)
}

// SetPhaseInversionDisabled toggles stereo phase inversion for the CELT layer.
func (d *Decoder) SetPhaseInversionDisabled(disabled bool) {
	d.celtDecoder.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled reports whether the CELT layer disables stereo phase inversion.
func (d *Decoder) PhaseInversionDisabled() bool {
	return d.celtDecoder.PhaseInversionDisabled()
}

// SetComplexity sets CELT decoder complexity for the Hybrid highband layer.
func (d *Decoder) SetComplexity(complexity int) error {
	return d.celtDecoder.SetComplexity(complexity)
}

// Complexity returns the Hybrid highband CELT decoder complexity setting.
func (d *Decoder) Complexity() int {
	return d.celtDecoder.Complexity()
}

// RecordPLCLoss advances Hybrid PLC loss cadence and returns the fade factor.
// This is used when recovering a lost frame via SILK LBRR while CELT still
// needs concealment for the same frame (decode_fec path).
func (d *Decoder) RecordPLCLoss() float32 {
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	return d.plcState.RecordLoss()
}

// FinalRange returns the final range coder state after decoding a Hybrid frame.
// It reports the CELT range because CELT consumes the range decoder after SILK.
func (d *Decoder) FinalRange() uint32 {
	return d.celtDecoder.FinalRange()
}

// ValidHybridFrameSize reports whether frameSize is 480 or 960 samples at
// 48 kHz, the supported 10 ms and 20 ms encoded Hybrid frame sizes.
func ValidHybridFrameSize(frameSize int) bool {
	return frameSize == 480 || frameSize == 960
}

func (d *Decoder) frameSize48FromAPI(frameSize int) int {
	apiSampleRate := int(d.apiSampleRate)
	if apiSampleRate <= 0 || apiSampleRate == 48000 {
		return frameSize
	}
	return frameSize * 48000 / apiSampleRate
}

// decodeFrame decodes one Hybrid frame from a range decoder shared by SILK and
// CELT. frameSize is per-channel samples at the configured API rate; the frame
// must represent 10 ms or 20 ms in the 48 kHz codec domain.
func (d *Decoder) decodeFrame(rd *rangecoding.Decoder, frameSize int, packetStereo bool) ([]float32, error) {
	return d.decodeFrameWithHook(rd, frameSize, packetStereo, nil)
}

// decodeFrameWithHook decodes a single hybrid frame and allows a hook after SILK decode.
func (d *Decoder) decodeFrameWithHook(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) (int, error)) ([]float32, error) {
	return d.decodeFrameWithHookFloat32(rd, frameSize, packetStereo, afterSilk, nil)
}

// DecodeWithDecoderHookToFloat32 decodes a Hybrid frame and writes its
// interleaved float32 output at the configured API rate into out. frameSize is
// the per-channel sample count; out must hold frameSize times Channels samples.
func (d *Decoder) DecodeWithDecoderHookToFloat32(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) (int, error), out []float32) error {
	channels := int(d.channels)
	if len(out) < frameSize*channels {
		return ErrDecodeFailed
	}
	_, err := d.decodeFrameWithHookFloat32(rd, frameSize, packetStereo, afterSilk, out[:frameSize*channels])
	return err
}

func (d *Decoder) decodeFrameWithHookFloat32(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) (int, error), out []float32) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	frameSizeAPI := frameSize
	frameSize48 := d.frameSize48FromAPI(frameSizeAPI)
	if !ValidHybridFrameSize(frameSize48) {
		return nil, ErrInvalidFrameSize
	}

	// Determine SILK frame duration from 48kHz frame size
	// 480 samples at 48kHz = 10ms, 960 samples = 20ms
	silkDuration := silk.Frame10ms
	if frameSize48 == 960 {
		silkDuration = silk.Frame20ms
	}

	// SILK sample count at 16kHz (WB)
	// 10ms: 160 samples at 16kHz
	// 20ms: 320 samples at 16kHz
	silkSamples := frameSize48 / 3 // 48kHz -> 16kHz = divide by 3

	monoToStereo := packetStereo && !d.prevPacketStereo
	stereoToMono := d.silkDecoder.ShouldUseStereoToMonoHistory(silk.BandwidthWideband, !packetStereo && d.prevPacketStereo)
	if monoToStereo {
		// Reset side-channel state to match libopus mono->stereo transition.
		d.silkDecoder.ResetSideChannel()
		// Copy left resampler state to right resampler on mono->stereo transition.
		// This ensures the right channel has proper history for smooth transition.
		// Resetting would cause zeros at the start of the right channel output.
		leftResampler := d.silkDecoder.GetResampler(silk.BandwidthWideband)
		rightResampler := d.silkDecoder.GetResamplerRightChannel(silk.BandwidthWideband)
		if rightResampler != nil && leftResampler != nil {
			rightResampler.CopyFrom(leftResampler)
		}
	}

	// Step 1: Decode SILK layer (0-8kHz at 16kHz native rate)
	// SILK reads from the shared range decoder first.
	// Use SILK decoder's resamplers for state continuity between SILK-only and Hybrid modes.
	//
	// IMPORTANT: Notify the SILK decoder that we're using WB bandwidth.
	// This ensures proper resampler state management when transitioning between
	// SILK-only (NB/MB/WB) and Hybrid (always WB) modes.
	// Without this, the prevBandwidth tracking gets out of sync, causing
	// resamplers to not be reset when returning to SILK-only mode.
	d.silkDecoder.NotifyBandwidthChange(silk.BandwidthWideband)
	leftResampler := d.silkDecoder.GetResampler(silk.BandwidthWideband)
	rightResampler := d.silkDecoder.GetResamplerRightChannel(silk.BandwidthWideband)

	// The resampled SILK lowband goes straight into out, which the CELT
	// highband then accumulates onto.
	channels := int(d.channels)
	totalSamples := frameSizeAPI * channels
	if len(out) < totalSamples {
		out = make([]float32, totalSamples)
	} else {
		out = out[:totalSamples]
	}
	silkUpsampled := out

	// Scratch buffer for resampler output (float32)
	scratchF32L := d.silkDecoder.GetResamplerScratch(frameSizeAPI)
	scratchF32R := d.silkDecoder.GetResamplerScratchR(frameSizeAPI)

	// When the FIXED_POINT integer hybrid path is active, capture the resampled
	// int16 SILK lowband (the pre-INT16TORES value libopus' silk_Decode emits)
	// interleaved by channel, so the integer CELT highband can accumulate onto
	// the exact opus_res lowband. The per-channel int16 resampler output is
	// produced for free alongside the float32 conversion.
	captureFixed := d.fixedHighband != nil
	var i16L, i16R []int16
	if captureFixed {
		i16L, i16R = d.fixedSilkInt16Scratch(frameSizeAPI)
		d.filledSilkInt16 = 0
	}

	filledSilkSamples := 0
	if packetStereo {
		if d.channels == 1 {
			mid, err := d.silkDecoder.DecodeStereoFrameToMono(
				rd,
				silk.BandwidthWideband, // Always WB for hybrid
				silkDuration,
				true,
			)
			if err != nil {
				return nil, err
			}
			resamplerInput := d.silkDecoder.BuildMonoResamplerInput(mid)
			var nL int
			if captureFixed {
				nL = leftResampler.ProcessIntoBoth(resamplerInput, silkUpsampled, i16L)
			} else {
				nL = leftResampler.ProcessInto(resamplerInput, silkUpsampled)
			}
			filledSilkSamples = min(nL, totalSamples)
			if captureFixed {
				d.filledSilkInt16 = copyInterleaveMono(d.scratchSilkInt16, i16L, filledSilkSamples)
			}
		} else {
			silkOutputL, silkOutputR, ok := d.silkDecoder.GetStereoInt16Scratch(silkSamples)
			if !ok {
				return nil, ErrDecodeFailed
			}
			nNative, err := d.silkDecoder.DecodeStereoFrameInt16Into(
				rd,
				silk.BandwidthWideband, // Always WB for hybrid
				silkDuration,
				true,
				silkOutputL,
				silkOutputR,
			)
			if err != nil {
				return nil, err
			}
			var outL, outR []int16
			direct := false
			if !captureFixed {
				outL, outR, direct = silk.ResampleStereoInt16(leftResampler, rightResampler, silkOutputL[:nNative], silkOutputR[:nNative])
			}
			if direct {
				n := min(len(outL), len(outR), totalSamples/2)
				silk.InterleaveInt16AsFloat32(silkUpsampled, outL[:n], outR[:n])
				filledSilkSamples = 2 * n
			} else {
				var nL, nR int
				if captureFixed {
					nL = leftResampler.ProcessInt16IntoBoth(silkOutputL[:nNative], scratchF32L, i16L)
					nR = rightResampler.ProcessInt16IntoBoth(silkOutputR[:nNative], scratchF32R, i16R)
				} else {
					nL = leftResampler.ProcessInt16Into(silkOutputL[:nNative], scratchF32L)
					nR = rightResampler.ProcessInt16Into(silkOutputR[:nNative], scratchF32R)
				}
				n := min(nR, nL)
				for i := 0; i < n && i*2+1 < totalSamples; i++ {
					silkUpsampled[i*2] = scratchF32L[i]
					silkUpsampled[i*2+1] = scratchF32R[i]
				}
				filledSilkSamples = min(n*2, totalSamples)
				if captureFixed {
					d.filledSilkInt16 = copyInterleaveStereo(d.scratchSilkInt16, i16L, i16R, filledSilkSamples)
				}
			}
		}
	} else {
		// Use int16-native SILK decode/resampler path for hot hybrid decode.
		silkOutput, err := d.silkDecoder.DecodeFrameRawInt16(
			rd,
			silk.BandwidthWideband,
			silkDuration,
			true,
		)
		if err != nil {
			return nil, err
		}
		resamplerInput := d.silkDecoder.BuildMonoResamplerInputInt16(silkOutput)
		dst := scratchF32L
		if d.channels == 1 {
			dst = silkUpsampled
		}
		var nL int
		if captureFixed {
			nL = leftResampler.ProcessInt16IntoBoth(resamplerInput, dst, i16L)
		} else {
			nL = leftResampler.ProcessInt16Into(resamplerInput, dst)
		}
		if d.channels == 2 {
			if stereoToMono {
				var nR int
				if captureFixed {
					nR = rightResampler.ProcessInt16IntoBoth(resamplerInput, scratchF32R, i16R)
				} else {
					nR = rightResampler.ProcessInt16Into(resamplerInput, scratchF32R)
				}
				n := min(nR, nL)
				for i := 0; i < n && i*2+1 < totalSamples; i++ {
					silkUpsampled[i*2] = scratchF32L[i]
					silkUpsampled[i*2+1] = scratchF32R[i]
				}
				filledSilkSamples = min(n*2, totalSamples)
				if captureFixed {
					d.filledSilkInt16 = copyInterleaveStereo(d.scratchSilkInt16, i16L, i16R, filledSilkSamples)
				}
			} else {
				for i := 0; i < nL && i*2+1 < totalSamples; i++ {
					val := scratchF32L[i]
					silkUpsampled[i*2] = val
					silkUpsampled[i*2+1] = val
				}
				filledSilkSamples = min(nL*2, totalSamples)
				if captureFixed {
					d.filledSilkInt16 = copyInterleaveStereoDup(d.scratchSilkInt16, i16L, filledSilkSamples)
				}
			}
		} else {
			filledSilkSamples = min(nL, totalSamples)
			if captureFixed {
				d.filledSilkInt16 = copyInterleaveMono(d.scratchSilkInt16, i16L, filledSilkSamples)
			}
		}
	}
	if filledSilkSamples < totalSamples {
		clear(silkUpsampled[filledSilkSamples:totalSamples])
	}

	dataLen := rd.StorageBits() / 8
	if afterSilk != nil {
		var err error
		dataLen, err = afterSilk(rd)
		if err != nil {
			return nil, err
		}
	}

	// Drive the FIXED_POINT integer CELT highband from a clone of the shared range
	// decoder, now positioned at the CELT start band (after any hybrid redundancy
	// flags). The hook supplies the logical main length separately: malformed
	// redundancy can set it to zero without shrinking entropy storage. The clone
	// lets the integer
	// decode consume the bitstream independently of the float CELT decode below.
	if captureFixed {
		d.fixedRangeClone = *rd
		d.fixedHighband.DecodeHybridHighband(d.scratchSilkInt16, d.filledSilkInt16, &d.fixedRangeClone, dataLen, frameSizeAPI, frameSize48, packetStereo)
		d.fixedRangeClone = rangecoding.Decoder{}
	}

	// Step 3: Use SILK output directly
	// The delay compensation is handled internally by the SILK resampler,
	// matching libopus behavior where SILK outputs at API rate with proper alignment.

	// Step 2: Decode CELT from band 17 (8 kHz) through the active bandwidth.
	// CELT reads from the same range decoder (SILK already consumed its portion)
	// and accumulates its highband onto the SILK lowband inside deemphasis, as
	// opus_decode_frame's celt_decode_with_ec(..., celt_accum=1) does.
	celtFrameSize := frameSize48
	if d.apiSampleRate == 96000 {
		celtFrameSize = frameSizeAPI
	}
	if err := d.celtDecoder.AccumulateFrameHybridWithPacketStereo(rd, dataLen, celtFrameSize, packetStereo, out); err != nil {
		return nil, err
	}

	d.prevPacketStereo = packetStereo
	return out, nil
}

// fixedSilkInt16Scratch returns per-channel int16 resampler-output scratch
// buffers (left, right) sized for frameSizeAPI API-rate samples, and ensures
// d.scratchSilkInt16 can hold the interleaved result for all channels.
func (d *Decoder) fixedSilkInt16Scratch(frameSizeAPI int) (left, right []int16) {
	channels := int(d.channels)
	if cap(d.scratchSilkInt16) < frameSizeAPI*channels {
		d.scratchSilkInt16 = make([]int16, frameSizeAPI*channels)
	}
	d.scratchSilkInt16 = d.scratchSilkInt16[:frameSizeAPI*channels]
	if cap(d.scratchSilkL) < frameSizeAPI {
		d.scratchSilkL = make([]int16, frameSizeAPI)
	}
	d.scratchSilkL = d.scratchSilkL[:frameSizeAPI]
	if channels == 2 {
		if cap(d.scratchSilkR) < frameSizeAPI {
			d.scratchSilkR = make([]int16, frameSizeAPI)
		}
		d.scratchSilkR = d.scratchSilkR[:frameSizeAPI]
		return d.scratchSilkL, d.scratchSilkR
	}
	return d.scratchSilkL, nil
}

// copyInterleaveMono writes the first filled mono samples from src into dst and
// returns filled.
func copyInterleaveMono(dst, src []int16, filled int) int {
	if filled > len(src) {
		filled = len(src)
	}
	if filled > len(dst) {
		filled = len(dst)
	}
	copy(dst[:filled], src[:filled])
	return filled
}

// copyInterleaveStereo interleaves filled (total, L+R) samples from the
// per-channel left/right slices into dst and returns filled.
func copyInterleaveStereo(dst, left, right []int16, filled int) int {
	n := filled / 2
	for i := range n {
		if i >= len(left) || i >= len(right) || 2*i+1 >= len(dst) {
			return 2 * i
		}
		dst[2*i] = left[i]
		dst[2*i+1] = right[i]
	}
	return 2 * n
}

// copyInterleaveStereoDup interleaves a duplicated-mono lowband (left used for
// both channels) into dst for filled total samples and returns filled.
func copyInterleaveStereoDup(dst, left []int16, filled int) int {
	n := filled / 2
	for i := range n {
		if i >= len(left) || 2*i+1 >= len(dst) {
			return 2 * i
		}
		dst[2*i] = left[i]
		dst[2*i+1] = left[i]
	}
	return 2 * n
}

// ensureSilkUpsampled returns a pre-allocated buffer for SILK upsampled output.
func (d *Decoder) ensureSilkUpsampled(n int) []float32 {
	if cap(d.scratchSilkUpsampled) < n {
		d.scratchSilkUpsampled = make([]float32, n)
	} else {
		d.scratchSilkUpsampled = d.scratchSilkUpsampled[:n]
	}
	return d.scratchSilkUpsampled
}

// upsample3x upsamples SILK output from 16kHz to 48kHz using linear interpolation.
// Retained for test helpers.
func upsample3x(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	output := make([]float32, len(samples)*3)

	for i := range samples {
		curr := samples[i]
		var next float32
		if i+1 < len(samples) {
			next = samples[i+1]
		} else {
			next = curr
		}

		output[i*3+0] = curr
		output[i*3+1] = curr*2/3 + next*1/3
		output[i*3+2] = curr*1/3 + next*2/3
	}

	return output
}
