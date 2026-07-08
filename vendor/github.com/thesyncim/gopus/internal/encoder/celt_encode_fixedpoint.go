//go:build gopus_fixed_point

package encoder

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/types"
)

// fixedPointBuild reports whether the gopus_fixed_point integer codec paths are
// compiled in. Golden-fixture tests calibrated against the float SILK encode
// path gate on it (the FIXED_POINT SILK encode produces byte-exact-to-libopus
// payloads that differ from the float golden bytes).
const fixedPointBuild = true

// encoderFixedCELTFields carries the integer (FIXED_POINT) CELT encoder state
// added under the gopus_fixed_point build. It is empty in the default build.
type encoderFixedCELTFields struct {
	fixedCELT       *fixedCELTState
	fixedCELTOut    []byte
	fixedFinalRange uint32
	fixedCELTUsed   bool
}

// fixedCELTFinalRange returns the integer CELT encoder's final range coder state
// when the last frame was produced by the integer path. currentFinalRange uses
// it so the encoder's reported final range matches the integer packet.
func (e *Encoder) fixedCELTFinalRange() (uint32, bool) {
	if e.fixedCELTUsed {
		return e.fixedFinalRange, true
	}
	return 0, false
}

// clearFixedCELTUsed resets the integer-CELT-used flag at the start of each
// packet so a stale value from a previous CELT frame cannot mis-gate the TOC
// frame-size conversion for a subsequent SILK/Hybrid frame.
func (e *Encoder) clearFixedCELTUsed() { e.fixedCELTUsed = false }

// fixedCELTState holds the integer (FIXED_POINT) CELT encoder used under the
// gopus_fixed_point build to produce byte-exact CELT-mode packets. It is created
// lazily and carries all CELT cross-frame state, so once a CELT-mode packet is
// routed to it every subsequent CELT frame must continue through it.
type fixedCELTState struct {
	enc      *fixedpoint.CELTEncoder
	channels int
	pcm16    []int16
	rng      *rangecoding.Encoder
}

// celtFixedUpsample mirrors celt_encoder_init's st->upsample =
// resampling_factor(API sample rate): 1 at 48 kHz and 2/3/4/6 at 24/16/12/8 kHz.
// 0 means an unsupported API rate.
func (e *Encoder) celtFixedUpsample() int {
	switch e.sampleRate {
	case 48000:
		return 1
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	}
	return 0
}

// celtFixedEndBand maps the (already-clamped) effective bandwidth to the CELT
// end band, matching the endband switch in opus_encoder.c celt_encode_with_ec
// setup: NB=13, MB/WB=17, SWB=19, FB=21.
func celtFixedEndBand(bw types.Bandwidth) int {
	switch bw {
	case types.BandwidthNarrowband:
		return 13
	case types.BandwidthMediumband, types.BandwidthWideband:
		return 17
	case types.BandwidthSuperwideband:
		return 19
	case types.BandwidthFullband:
		return 21
	}
	return 21
}

// celtFixedEncodeInScope reports whether the integer CELT encoder can produce a
// byte-exact packet for this frame. Its scope matches
// fixedpoint.CELTEncoder.EncodeWithEC: the static 48 kHz CELT mode at any
// supported API rate (48/24/16/12/8 kHz, where the API-rate frameSize upsamples
// to a valid 2.5/5/10/20 ms 48 kHz core block), start band 0, 1 or 2 channels,
// no hybrid (SILK present), no LFE, no QEXT, no surround energy mask. Anything
// else falls back to the float CELT encoder.
//
// It also requires a pure-CELT stream (restricted-low-delay or forced ModeCELT):
// the integer encoder carries no SILK->CELT transition-prefill state, so a stream
// that can switch modes stays on the float path to avoid a cross-frame state
// mismatch.
func (e *Encoder) celtFixedEncodeInScope(frameSize int) bool {
	if !e.lowDelay && e.mode != ModeCELT {
		return false
	}
	if e.lfe {
		return false
	}
	upsample := e.celtFixedUpsample()
	if upsample == 0 {
		return false
	}
	// The API-rate frameSize must upsample to a valid 48 kHz core block
	// (shortMdctSize<<LM for LM 0..3, i.e. 120/240/480/960).
	const shortMdctSize = 120
	core := frameSize * upsample
	if core <= 0 || core > 960 || core%shortMdctSize != 0 {
		return false
	}
	c := int(e.channels)
	if c != 1 && c != 2 {
		return false
	}
	if extsupport.QEXT && e.qextActive() {
		return false
	}
	if len(e.celtEnergyMask) > 0 {
		return false
	}
	return true
}

// encodeCELTFrameFixed runs the integer CELT encoder for one in-scope frame and
// returns the CELT payload bytes (TOC-excluded), matching the float
// celt.Encoder.EncodeFrame contract. ok is false when the frame is out of the
// integer encoder's scope, in which case the caller must use the float path.
func (e *Encoder) encodeCELTFrameFixed(pcm []opusRes, frameSize, bitrate, maxPayloadBytes int) (out []byte, ok bool, err error) {
	e.fixedCELTUsed = false
	if !e.celtFixedEncodeInScope(frameSize) {
		return nil, false, nil
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels {
		return nil, false, ErrInvalidFrameSize
	}

	st := e.ensureFixedCELT(channels)
	st.enc.SetBandRange(0, celtFixedEndBand(e.effectiveBandwidth()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	switch e.bitrateMode {
	case ModeCBR:
		st.enc.SetVBR(false)
		st.enc.SetConstrainedVBR(false)
	case ModeCVBR:
		st.enc.SetVBR(true)
		st.enc.SetConstrainedVBR(true)
	case ModeVBR:
		st.enc.SetVBR(true)
		st.enc.SetConstrainedVBR(false)
	}

	// Convert the delay-compensated float frame to int16 with the libopus
	// FLOAT2INT16 quantization, matching opus_encode_float -> celt_encode_with_ec.
	if cap(st.pcm16) < len(pcm) {
		st.pcm16 = make([]int16, len(pcm))
	}
	st.pcm16 = st.pcm16[:len(pcm)]
	for i, v := range pcm {
		st.pcm16[i] = opusmath.Float32ToInt16(float32(v))
	}

	// nbCompressedBytes is the output buffer cap; EncodeWithEC self-computes the
	// CBR byte count and clamps below it, and uses it directly as the VBR ceiling.
	nbCompressedBytes := celtPacketSizeCap - 1
	if maxPayloadBytes > 0 && maxPayloadBytes < nbCompressedBytes {
		nbCompressedBytes = maxPayloadBytes
	}

	if cap(st.rng.Buffer()) < nbCompressedBytes {
		buf := make([]byte, nbCompressedBytes)
		st.rng.Init(buf)
	} else {
		buf := st.rng.Buffer()[:nbCompressedBytes]
		for i := range buf {
			buf[i] = 0
		}
		st.rng.Init(buf)
	}

	n := st.enc.EncodeWithEC(st.pcm16, frameSize, st.rng, nbCompressedBytes)
	e.fixedFinalRange = st.rng.Range()
	out = append(e.fixedCELTOut[:0], st.rng.Buffer()[:n]...)
	e.fixedCELTOut = out
	e.fixedCELTUsed = true
	return out, true, nil
}

// LastFixedCELTInput16 returns the int16 PCM frame the integer CELT encoder
// consumed for the most recent frame, for parity tests that drive the same
// samples through the bare FIXED_POINT celt_encode_with_ec oracle.
func (e *Encoder) LastFixedCELTInput16() []int16 {
	if e.fixedCELT == nil {
		return nil
	}
	return e.fixedCELT.pcm16
}

func (e *Encoder) ensureFixedCELT(channels int) *fixedCELTState {
	if e.fixedCELT == nil || e.fixedCELT.channels != channels {
		e.fixedCELT = &fixedCELTState{
			enc:      fixedpoint.NewCELTEncoderRate(channels, int(e.sampleRate)),
			channels: channels,
			rng:      &rangecoding.Encoder{},
		}
	}
	return e.fixedCELT
}

// resetFixedCELT clears the integer CELT cross-frame state, mirroring the float
// celtEncoder.Reset() done on a CELT mode transition. The API-rate upsample is
// preserved by recreating at the encoder's sample rate.
func (e *Encoder) resetFixedCELT() {
	if e.fixedCELT != nil {
		e.fixedCELT.enc = fixedpoint.NewCELTEncoderRate(e.fixedCELT.channels, int(e.sampleRate))
	}
}

const celtPacketSizeCap = 1275
