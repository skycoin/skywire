// Package hybrid implements the Hybrid decoder for Opus.
// Hybrid mode combines SILK (low frequencies, 0-8kHz) with CELT (high frequencies, 8-20kHz)
// for super-wideband and fullband speech at medium bitrates.
//
// Reference: RFC 6716 Section 3.2 (Hybrid mode)
package hybrid

import (
	"errors"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// Constants for Hybrid mode
const (
	// HybridCELTStartBand is the first CELT band decoded in hybrid mode.
	// Bands 0-16 are covered by SILK; CELT only decodes bands 17-21.
	HybridCELTStartBand = 17

	// SilkCELTDelay is the libopus SILK/CELT delay in samples at 48 kHz.
	SilkCELTDelay = celt.SilkCELTDelay
)

// Errors for Hybrid decoding
var (
	// ErrInvalidFrameSize indicates a frame size invalid for hybrid mode.
	// Hybrid only supports 10ms (480 samples) and 20ms (960 samples) frames.
	ErrInvalidFrameSize = errors.New("hybrid: invalid frame size (only 10ms/20ms supported)")

	// ErrDecodeFailed indicates a frame decode error.
	ErrDecodeFailed = errors.New("hybrid: frame decode failed")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("hybrid: nil range decoder")
)

// Decoder decodes Hybrid-mode Opus frames (SILK + CELT combined).
// Hybrid mode uses SILK for 0-8kHz and CELT for 8-20kHz.
//
// The decoder coordinates two sub-decoders:
// - SILK: Decodes low-frequency content at WB (16kHz), upsampled to 48kHz
// - CELT: Decodes high-frequency content (bands 17-21) at 48kHz
//
// SILK alignment is handled by the shared SILK resampler state before summing with CELT.
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
	scratchCELT48        []float32
	scratchCELTAPI       []float32

	// fixedHighband, when set, drives the FIXED_POINT integer CELT highband
	// decode for the in-flight integer-output (DecodeInt16 / DecodeInt24) packet.
	// It is nil on the float Decode path and in the default build, so the float
	// hybrid decode is unchanged. When set, decodeFrameWithHookFloat32 captures
	// the resampled int16 SILK lowband and, once the shared range decoder is
	// positioned at the CELT start band, hands a clone of it (plus the SILK
	// lowband) to the hook for the integer accum decode.
	fixedHighband    FixedHybridHighband
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
	DecodeHybridHighband(silkInt16 []int16, filled int, rd *rangecoding.Decoder, frameSizeAPI, frameSize48 int, packetStereo bool)
}

// SetFixedHighband installs (or clears, with nil) the integer hybrid highband
// hook for the next decode. It is a no-op in effect on the float path because
// the root decoder only sets it while an integer-output packet is active.
func (d *Decoder) SetFixedHighband(h FixedHybridHighband) {
	d.fixedHighband = h
}

// NewDecoder creates a new Hybrid decoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
//
// The decoder initializes:
// - SILK decoder in WB (wideband, 16kHz) mode (always WB for hybrid)
// - CELT decoder for high-frequency bands
func NewDecoder(channels int) *Decoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	// Max frame: 960 samples (20ms at 48kHz) * 2 channels = 1920
	maxSamples := 960 * channels

	return &Decoder{
		silkDecoder: silk.NewDecoder(),
		celtDecoder: celt.NewDecoder(channels),

		channels:      int32(channels),
		apiSampleRate: 48000,
		plcState:      plc.NewState(),

		// Pre-allocate scratch buffers for zero-alloc decode path
		scratchSilkUpsampled: make([]float32, maxSamples),
		scratchCELT48:        make([]float32, maxSamples),
		scratchCELTAPI:       make([]float32, maxSamples),
	}
}

// NewDecoderWithSharedDecoders creates a Hybrid decoder that reuses external SILK/CELT decoders.
// This is useful for sharing decoder state across Opus modes.
func NewDecoderWithSharedDecoders(channels int, silkDec *silk.Decoder, celtDec *celt.Decoder) *Decoder {
	d := NewDecoder(channels)
	if silkDec != nil {
		d.silkDecoder = silkDec
	}
	if celtDec != nil {
		d.celtDecoder = celtDec
	}
	return d
}

// SetAPISampleRate sets the public decoder rate used for hybrid PCM output.
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

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// For hybrid mode, this returns the CELT decoder's final range since CELT encodes last.
func (d *Decoder) FinalRange() uint32 {
	return d.celtDecoder.FinalRange()
}

// ValidHybridFrameSize returns true if the frame size is valid for hybrid mode.
// Hybrid only supports 10ms (480 samples) and 20ms (960 samples) at 48kHz.
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

func (d *Decoder) downsampleFrame48ToAPI(dst, src []float32, frameSize int) {
	channels := int(d.channels)
	apiSampleRate := int(d.apiSampleRate)
	if apiSampleRate == 48000 {
		copy(dst[:frameSize*channels], src[:frameSize*channels])
		return
	}
	factor := 48000 / apiSampleRate
	for i := range frameSize {
		srcBase := i * factor * channels
		dstBase := i * channels
		for c := range channels {
			dst[dstBase+c] = src[srcBase+c]
		}
	}
}

// decodeFrame decodes a single hybrid frame using a shared range decoder.
// This is the core decoding function that coordinates SILK and CELT.
//
// Parameters:
//   - rd: Range decoder (shared between SILK and CELT)
//   - frameSize: Expected output samples at 48kHz (480 or 960)
//   - stereo: True for stereo decoding
//
// Returns: PCM samples as float32 slice at 48kHz
func (d *Decoder) decodeFrame(rd *rangecoding.Decoder, frameSize int, packetStereo bool) ([]float32, error) {
	return d.decodeFrameWithHook(rd, frameSize, packetStereo, nil)
}

// decodeFrameWithHook decodes a single hybrid frame and allows a hook after SILK decode.
func (d *Decoder) decodeFrameWithHook(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) error) ([]float32, error) {
	return d.decodeFrameWithHookFloat32(rd, frameSize, packetStereo, afterSilk, nil)
}

// DecodeWithDecoderHookToFloat32 decodes a hybrid frame and writes the final
// 48 kHz output directly into out.
func (d *Decoder) DecodeWithDecoderHookToFloat32(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) error, out []float32) error {
	channels := int(d.channels)
	if len(out) < frameSize*channels {
		return ErrDecodeFailed
	}
	_, err := d.decodeFrameWithHookFloat32(rd, frameSize, packetStereo, afterSilk, out[:frameSize*channels])
	return err
}

func (d *Decoder) decodeFrameWithHookFloat32(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) error, out []float32) ([]float32, error) {
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

	// Use scratch buffer for SILK upsampled output
	channels := int(d.channels)
	totalSamples := frameSizeAPI * channels
	silkUpsampled := d.ensureSilkUpsampled(totalSamples)

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
				nL = leftResampler.ProcessIntoBoth(resamplerInput, scratchF32L, i16L)
			} else {
				nL = leftResampler.ProcessInto(resamplerInput, scratchF32L)
			}
			for i := 0; i < nL && i < totalSamples; i++ {
				silkUpsampled[i] = scratchF32L[i]
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
		var nL int
		if captureFixed {
			nL = leftResampler.ProcessInt16IntoBoth(resamplerInput, scratchF32L, i16L)
		} else {
			nL = leftResampler.ProcessInt16Into(resamplerInput, scratchF32L)
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
			for i := 0; i < nL && i < totalSamples; i++ {
				silkUpsampled[i] = scratchF32L[i]
			}
			filledSilkSamples = min(nL, totalSamples)
			if captureFixed {
				d.filledSilkInt16 = copyInterleaveMono(d.scratchSilkInt16, i16L, filledSilkSamples)
			}
		}
	}
	if filledSilkSamples < totalSamples {
		clear(silkUpsampled[filledSilkSamples:totalSamples])
	}

	if afterSilk != nil {
		if err := afterSilk(rd); err != nil {
			return nil, err
		}
	}

	// Drive the FIXED_POINT integer CELT highband from a clone of the shared range
	// decoder, now positioned at the CELT start band (after any hybrid redundancy
	// flags and with storage shrunk by afterSilk). The clone lets the integer
	// decode consume the bitstream independently of the float CELT decode below.
	if captureFixed {
		rdClone := *rd
		d.fixedHighband.DecodeHybridHighband(d.scratchSilkInt16, d.filledSilkInt16, &rdClone, frameSizeAPI, frameSize48, packetStereo)
	}

	// Step 3: Use SILK output directly
	// The delay compensation is handled internally by the SILK resampler,
	// matching libopus behavior where SILK outputs at API rate with proper alignment.

	// Step 2: Decode CELT layer (8-20kHz, bands 17-21 only)
	// CELT reads from the same range decoder (SILK already consumed its portion).
	celtAPI, err := d.decodeCELTHybridToAPI(rd, frameSizeAPI, frameSize48, packetStereo)
	if err != nil {
		return nil, err
	}

	if len(out) < totalSamples {
		out = make([]float32, totalSamples)
	} else {
		out = out[:totalSamples]
	}
	combineHybridBands(out, celtAPI, silkUpsampled, totalSamples)

	d.prevPacketStereo = packetStereo
	return out, nil
}

// combineHybridBands sums the CELT highband (already deemphasised and scaled by
// 1/CELT_SIG_SCALE) onto the SILK lowband to form the final hybrid PCM.
//
// This mirrors the libopus float build, where opus_decoder.c writes the SILK
// output into pcm and then celt_decode_with_ec_dred is called with celt_accum=1
// (opus_decoder.c:370,607). The accumulation happens inside CELT's deemphasis()
// as `y[j*C] = ADD_RES(y[j*C], SIG2RES(tmp))` (celt/celt_decoder.c:379), where for
// the float build SIG2RES(a)=(1/CELT_SIG_SCALE)*a and ADD_RES(a,b)=a+b
// (celt/arch.h:373,379). gopus computes SIG2RES(tmp) inside applyDeemphasisAndScale
// (scale=1/32768) and the add here is float32-commutative, so silk+celt and
// celt+silk are bit-identical.
func combineHybridBands(out, celtAPI, silkUpsampled []float32, totalSamples int) {
	i := 0
	for ; i+3 < totalSamples; i += 4 {
		out[i] = celtAPI[i] + silkUpsampled[i]
		out[i+1] = celtAPI[i+1] + silkUpsampled[i+1]
		out[i+2] = celtAPI[i+2] + silkUpsampled[i+2]
		out[i+3] = celtAPI[i+3] + silkUpsampled[i+3]
	}
	for ; i < totalSamples; i++ {
		out[i] = celtAPI[i] + silkUpsampled[i]
	}
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

func (d *Decoder) decodeCELTHybridToAPI(rd *rangecoding.Decoder, frameSizeAPI, frameSize48 int, packetStereo bool) ([]float32, error) {
	channels := int(d.channels)
	needed48 := frameSize48 * channels
	if cap(d.scratchCELT48) < needed48 {
		d.scratchCELT48 = make([]float32, needed48)
	}
	celt48 := d.scratchCELT48[:needed48]
	if err := d.celtDecoder.DecodeFrameHybridWithPacketStereoToFloat32(rd, frameSize48, packetStereo, celt48); err != nil {
		return nil, err
	}
	neededAPI := frameSizeAPI * channels
	if d.apiSampleRate == 48000 {
		return celt48[:neededAPI], nil
	}
	if cap(d.scratchCELTAPI) < neededAPI {
		d.scratchCELTAPI = make([]float32, neededAPI)
	}
	celtAPI := d.scratchCELTAPI[:neededAPI]
	d.downsampleFrame48ToAPI(celtAPI, celt48, frameSizeAPI)
	return celtAPI, nil
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
