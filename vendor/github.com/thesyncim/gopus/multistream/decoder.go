package multistream

import (
	"errors"
	"fmt"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/hybrid"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

// Errors for multistream decoder creation and operation.
var (
	// ErrInvalidChannels indicates channels is not in the valid range (1-255).
	ErrInvalidChannels = errors.New("multistream: invalid channel count (must be 1-255)")

	// ErrInvalidStreams indicates streams is not in the valid range (1-255).
	ErrInvalidStreams = errors.New("multistream: invalid stream count (must be 1-255)")

	// ErrInvalidCoupledStreams indicates coupledStreams is invalid (must be 0 to streams).
	ErrInvalidCoupledStreams = errors.New("multistream: invalid coupled streams (must be 0 to streams)")

	// ErrTooManyChannels indicates the total channel count exceeds the maximum.
	ErrTooManyChannels = errors.New("multistream: too many channels (streams + coupled_streams must be <= 255)")

	// ErrInvalidMapping indicates the mapping table is malformed.
	ErrInvalidMapping = errors.New("multistream: invalid mapping table")

	// ErrInvalidProjectionMatrix indicates malformed projection demixing metadata.
	ErrInvalidProjectionMatrix = errors.New("multistream: invalid projection demixing matrix")

	// ErrInvalidStreamIndex indicates an out-of-range per-stream state lookup.
	ErrInvalidStreamIndex = errors.New("multistream: invalid stream index")

	// ErrInvalidGain indicates an invalid decoder gain value.
	ErrInvalidGain = errors.New("multistream: invalid gain (must be -32768 to 32767)")

	// ErrInvalidComplexity indicates an invalid decoder complexity value.
	ErrInvalidComplexity = errors.New("multistream: invalid complexity (must be 0-10)")

	// ErrBufferTooSmall indicates the requested decode frame is smaller than the packet duration.
	ErrBufferTooSmall = errors.New("multistream: output buffer too small")
)

// streamDecoder is an internal interface that wraps the different decoder types.
// This allows the multistream decoder to manage heterogeneous stream decoders uniformly.
type streamDecoder interface {
	// Decode decodes a packet and returns PCM samples as float32.
	// For stereo decoders, samples are interleaved [L0, R0, L1, R1, ...].
	Decode(data []byte, frameSize int) ([]float32, error)

	// DecodeStereo decodes a stereo packet and returns interleaved samples.
	// Only valid for stereo (2-channel) decoders.
	DecodeStereo(data []byte, frameSize int) ([]float32, error)

	// Reset clears decoder state for a new stream.
	Reset()

	// Channels returns the number of channels this decoder produces (1 or 2).
	Channels() int

	// SetIgnoreExtensions toggles libopus-style opaque extension handling.
	SetIgnoreExtensions(bool)
}

const (
	streamModeSILK = iota
	streamModeHybrid
	streamModeCELT
)

type streamTOC struct {
	mode      int
	bandwidth int
	stereo    bool
}

// parseStreamTOC extracts mode/bandwidth/stereo from an Opus TOC byte.
// Bandwidth uses Opus values 0=NB,1=MB,2=WB,3=SWB,4=FB.
func parseStreamTOC(toc byte) streamTOC {
	config := toc >> 3
	stereo := (toc & 0x04) != 0

	switch {
	case config < 4:
		return streamTOC{mode: streamModeSILK, bandwidth: 0, stereo: stereo}
	case config < 8:
		return streamTOC{mode: streamModeSILK, bandwidth: 1, stereo: stereo}
	case config < 12:
		return streamTOC{mode: streamModeSILK, bandwidth: 2, stereo: stereo}
	case config < 14:
		return streamTOC{mode: streamModeHybrid, bandwidth: 3, stereo: stereo}
	case config < 16:
		return streamTOC{mode: streamModeHybrid, bandwidth: 4, stereo: stereo}
	case config < 20:
		return streamTOC{mode: streamModeCELT, bandwidth: 0, stereo: stereo}
	case config < 24:
		return streamTOC{mode: streamModeCELT, bandwidth: 2, stereo: stereo}
	case config < 28:
		return streamTOC{mode: streamModeCELT, bandwidth: 3, stereo: stereo}
	default:
		return streamTOC{mode: streamModeCELT, bandwidth: 4, stereo: stereo}
	}
}

// streamState wraps per-mode decoders and dispatches by packet TOC.
// Each stream owns a full Opus decoder state (SILK/CELT/Hybrid), not just
// hybrid-only state.
type streamState struct {
	sampleRate int32
	channels   int32

	hybridDec *hybrid.Decoder
	celtDec   *celt.Decoder
	silkDec   *silk.Decoder

	lastMode           int32
	lastBandwidth      int32
	lastPacketStereo   bool
	haveDecoded        bool
	prevRedundancy     bool
	lastFrameSize      int32
	lastPacketDuration int32
	lastDataLen        int32
	decodeGainQ8       int32
	ignoreExtensions   bool
	complexity         int32

	// softClipMem is the per-stream soft-clip filter memory (one entry per stream
	// channel), mirroring the per-decoder softclip_mem[2] in libopus
	// opus_decode_native. It is used only on the int16 decode path that requests
	// OPTIONAL_CLIP (the projection int16 demix soft-clips each per-stream output
	// before the mapping-matrix multiply); the float path leaves it cleared.
	softClipMem [2]float32

	streamOSCEFields
	streamFixedFields
}

// softClipStreamOutput applies the libopus opus_pcm_soft_clip to one stream's
// interleaved decoded output in place, advancing the per-stream soft-clip
// memory. It mirrors opus_decode_native's soft_clip step (applied per stream
// before the multistream copy/demix callback). channels is this stream's decoded
// channel count (1 mono, 2 coupled).
func (d *streamState) softClipStreamOutput(samples []float32, frameSize, channels int) {
	if channels < 1 || channels > 2 || frameSize < 1 {
		return
	}
	opusmath.PCMSoftClip(samples, frameSize, channels, d.softClipMem[:channels])
}

// clearSoftClipMem zeroes the per-stream soft-clip memory, matching the
// libopus opus_decode_native behaviour when soft_clip is disabled.
func (d *streamState) clearSoftClipMem() {
	d.softClipMem[0] = 0
	d.softClipMem[1] = 0
}

func newStreamDecoder(sampleRate, channels int) *streamState {
	silkDec := silk.NewDecoder()
	silkDec.SetAPISampleRate(sampleRate)
	celtDec := celt.NewDecoder(channels)
	celtDec.SetDownsample(48000 / sampleRate)
	hybridDec := hybrid.NewDecoderWithSharedDecoders(channels, silkDec, celtDec)
	hybridDec.SetAPISampleRate(sampleRate)
	return &streamState{
		sampleRate:    int32(sampleRate),
		channels:      int32(channels),
		hybridDec:     hybridDec,
		celtDec:       celtDec,
		silkDec:       silkDec,
		lastMode:      streamModeHybrid,
		lastBandwidth: int32(types.BandwidthFullband),
		lastFrameSize: int32(sampleRate / 50),
	}
}

// Decode decodes a packet for mono streams.
func (d *streamState) Decode(data []byte, frameSize int) ([]float32, error) {
	return d.decodePacketToFloat32(data, frameSize)
}

// DecodeStereo decodes a packet for coupled (stereo) streams.
func (d *streamState) DecodeStereo(data []byte, frameSize int) ([]float32, error) {
	return d.decodePacketToFloat32(data, frameSize)
}

// Reset resets decoder state while preserving user-configured gain.
func (d *streamState) Reset() {
	d.hybridDec.Reset()
	d.celtDec.Reset()
	d.silkDec.Reset()
	d.lastMode = streamModeHybrid
	d.lastBandwidth = int32(types.BandwidthFullband)
	d.lastPacketStereo = false
	d.haveDecoded = false
	d.prevRedundancy = false
	d.lastFrameSize = d.sampleRate / 50
	d.lastPacketDuration = 0
	d.lastDataLen = 0
	d.resetOSCEPostfilterState()
}

// SetIgnoreExtensions toggles opaque in-band packet-extension handling for this
// stream's decoder.
func (d *streamState) SetIgnoreExtensions(ignore bool) {
	d.ignoreExtensions = ignore
}

// Channels returns the channel count for this decoder.
func (d *streamState) Channels() int {
	return int(d.channels)
}

// SampleRate returns the decoder sample rate in Hz.
func (d *streamState) SampleRate() int {
	return int(d.sampleRate)
}

// SetGain sets output gain in Q8 dB units (libopus OPUS_SET_GAIN semantics).
func (d *streamState) SetGain(gainQ8 int) error {
	if gainQ8 < -32768 || gainQ8 > 32767 {
		return ErrInvalidGain
	}
	d.decodeGainQ8 = int32(gainQ8)
	return nil
}

// Gain returns the current decoder output gain in Q8 dB units.
func (d *streamState) Gain() int {
	return int(d.decodeGainQ8)
}

// SetPhaseInversionDisabled enables or disables stereo phase inversion in this
// stream's CELT and Hybrid decoders.
func (d *streamState) SetPhaseInversionDisabled(disabled bool) {
	d.celtDec.SetPhaseInversionDisabled(disabled)
	d.hybridDec.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (d *streamState) PhaseInversionDisabled() bool {
	return d.celtDec.PhaseInversionDisabled()
}

// SetComplexity sets this stream's decode complexity (0-10); out-of-range values
// return ErrInvalidComplexity.
func (d *streamState) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return ErrInvalidComplexity
	}
	if err := d.celtDec.SetComplexity(complexity); err != nil {
		return err
	}
	if err := d.hybridDec.SetComplexity(complexity); err != nil {
		return err
	}
	d.complexity = int32(complexity)
	return nil
}

// Complexity returns this stream's decode complexity setting.
func (d *streamState) Complexity() int {
	return int(d.complexity)
}

// Pitch returns the most recent decoded pitch period.
func (d *streamState) Pitch() int {
	if d.lastMode == streamModeCELT {
		return d.celtDec.PostfilterPeriod()
	}
	if d.silkDec.GetLastSignalType() != 2 {
		return 0
	}
	return d.silkDec.GetLagPrev() * streamSilkPitchScale(int(d.lastBandwidth))
}

func streamSilkPitchScale(bandwidth int) int {
	switch types.Bandwidth(bandwidth) {
	case types.BandwidthNarrowband:
		return 6
	case types.BandwidthMediumband:
		return 4
	default:
		return 3
	}
}

// Bandwidth returns the bandwidth of the last successfully decoded packet.
func (d *streamState) Bandwidth() types.Bandwidth {
	return types.Bandwidth(d.lastBandwidth)
}

// LastPacketDuration returns the last decoded packet duration at the decoder API rate.
func (d *streamState) LastPacketDuration() int {
	return int(d.lastPacketDuration)
}

// InDTX reports whether the most recently decoded packet was DTX.
func (d *streamState) InDTX() bool {
	return d.lastDataLen > 0 && d.lastDataLen <= 2
}

// FinalRange returns the final range coder state for the last decoded packet.
func (d *streamState) FinalRange() uint32 {
	if d.lastDataLen <= 1 {
		return 0
	}

	switch d.lastMode {
	case streamModeSILK:
		return d.silkDec.FinalRange()
	case streamModeHybrid:
		return d.hybridDec.FinalRange()
	case streamModeCELT:
		return d.celtDec.FinalRange()
	default:
		return 0
	}
}

func streamDecodeGainLinear(gainQ8 int) float32 {
	if gainQ8 == 0 {
		return 1
	}
	return opusmath.CeltExp2(float32(6.48814081e-4) * float32(gainQ8))
}

func (d *streamState) applyOutputGain32(samples []float32) {
	if d.decodeGainQ8 == 0 {
		return
	}
	gain := streamDecodeGainLinear(int(d.decodeGainQ8))
	for i := range samples {
		samples[i] *= gain
	}
}

func (d *streamState) frameSize48FromAPI(frameSize int) int {
	sampleRate := int(d.sampleRate)
	if sampleRate <= 0 || sampleRate == 48000 {
		return frameSize
	}
	return frameSize * 48000 / sampleRate
}

func (d *streamState) recordDecodedTOC(toc streamTOC) {
	d.lastMode = int32(toc.mode)
	d.lastBandwidth = int32(toc.bandwidth)
	d.lastPacketStereo = toc.stereo
	d.haveDecoded = true
}

func (d *streamState) recordDecodeCall(frameSize, dataLen int) {
	d.lastFrameSize = int32(frameSize)
	d.lastPacketDuration = int32(frameSize)
	d.lastDataLen = int32(dataLen)
}

func (d *streamState) finishDecode(out []float32, err error) ([]float32, error) {
	if err == nil {
		d.applyOutputGain32(out)
	}
	return out, err
}

func (d *streamState) finishDecode32(out []float32, err error) ([]float32, error) {
	if err == nil {
		d.applyOutputGain32(out)
	}
	return out, err
}

func (d *streamState) decodeSILKToFloat32(data []byte, frameSize int, packetStereo bool, opusBandwidth int) ([]float32, error) {
	bw, ok := silk.BandwidthFromOpus(opusBandwidth)
	if !ok {
		return nil, fmt.Errorf("multistream: invalid SILK bandwidth: %d", opusBandwidth)
	}
	if extsupport.OSCERuntime && data != nil {
		restoreOSCELACEHook := d.installOSCELACESilkPostfilterHook(bw, packetStereo)
		defer restoreOSCELACEHook()
	}

	var out32 []float32
	var err error
	switch {
	case packetStereo && d.channels == 2:
		out32, err = d.silkDec.DecodeStereo(data, bw, frameSize, true)
	case packetStereo && d.channels == 1:
		out32, err = d.silkDec.DecodeStereoToMono(data, bw, frameSize, true)
	case !packetStereo && d.channels == 2:
		out32, err = d.silkDec.DecodeMonoToStereo(data, bw, frameSize, true, d.lastPacketStereo)
	default:
		out32, err = d.silkDec.Decode(data, bw, frameSize, true)
	}
	if err != nil {
		return nil, err
	}

	if extsupport.OSCERuntime {
		if data != nil {
			d.applyOSCEPostSilk(out32, frameSize, bw, packetStereo)
		} else {
			d.applyOSCEPLCSilk(out32, frameSize, bw, packetStereo)
		}
	}

	return out32, nil
}

// decodeSILKWithDecoder decodes a SILK-only frame through a shared,
// caller-supplied range decoder (already initialized on the frame bytes) so the
// bits remaining after the SILK payload (the redundancy flag and the SILK<->CELT
// redundant frame) can be read by the caller from the same decoder, exactly as
// opus_decode_frame does. It is the shared-decoder sibling of decodeSILKToFloat32's
// data!=nil branch.
func (d *streamState) decodeSILKWithDecoder(rd *rangecoding.Decoder, frameSize int, packetStereo bool, bw silk.Bandwidth) ([]float32, error) {
	if extsupport.OSCERuntime {
		restoreOSCELACEHook := d.installOSCELACESilkPostfilterHook(bw, packetStereo)
		defer restoreOSCELACEHook()
	}

	channels := int(d.channels)
	var out32 []float32
	var err error
	switch {
	case packetStereo && channels == 2:
		out32 = make([]float32, frameSize*channels)
		_, err = d.silkDec.DecodeStereoWithDecoderInto(rd, bw, frameSize, true, out32)
	case packetStereo && channels == 1:
		out32, err = d.silkDec.DecodeStereoToMonoWithDecoder(rd, bw, frameSize, true)
	case !packetStereo && channels == 2:
		out32 = make([]float32, frameSize*channels)
		_, err = d.silkDec.DecodeMonoToStereoWithDecoderInto(rd, bw, frameSize, true, d.lastPacketStereo, out32)
	default:
		out32 = make([]float32, frameSize*channels)
		_, err = d.silkDec.DecodeWithDecoderInto(rd, bw, frameSize, true, out32)
	}
	if err != nil {
		return nil, err
	}

	if extsupport.OSCERuntime {
		d.applyOSCEPostSilk(out32, frameSize, bw, packetStereo)
	}
	return out32, nil
}

func (d *streamState) decodeFramePayload(frame []byte, frameSize int, toc streamTOC, qextPayload []byte) ([]float32, error) {
	return d.decodeFramePayloadToFloat32(frame, frameSize, toc, qextPayload)
}

func (d *streamState) decodeFramePayloadToFloat32(frame []byte, frameSize int, toc streamTOC, qextPayload []byte) ([]float32, error) {
	var out []float32
	var err error

	// A frame of <= 1 byte is a DTX/PLC frame: libopus opus_decode_frame sets
	// data = NULL for len <= 1 and conceals using the PREVIOUS mode (zeros until
	// any packet has been decoded), regardless of this packet's TOC mode. Decode
	// it as PLC rather than feeding the empty payload to the TOC-mode decoder,
	// which would run concealment in the wrong mode and emit noise instead of the
	// libopus silence. This deliberately does not record the TOC, so prev_mode is
	// left unchanged exactly as libopus leaves it after a concealed frame.
	if len(frame) <= 1 {
		return d.decodePLCToFloat32(frameSize)
	}

	// transSize mirrors libopus IMIN(F5, audiosize): the transition crossfade
	// spans at most 5 ms.
	transSize := frameSize
	if f5 := int(d.sampleRate) / 50 / 2; transSize > f5 {
		transSize = f5
	}

	switch toc.mode {
	case streamModeSILK:
		out, err = d.decodeSILKModeWithTransition(frame, frameSize, transSize, toc)
	case streamModeHybrid:
		if !hybrid.ValidHybridFrameSize(d.frameSize48FromAPI(frameSize)) {
			return nil, fmt.Errorf("multistream: invalid hybrid frame size %d", frameSize)
		}
		out, err = d.decodeHybridModeWithTransition(frame, frameSize, transSize, toc)
	case streamModeCELT:
		out, err = d.decodeCELTModeWithTransition(frame, frameSize, transSize, toc, qextPayload)
	default:
		return nil, ErrInvalidPacket
	}
	if err != nil {
		return nil, err
	}

	if extsupport.OSCERuntime {
		d.markOSCEInactiveIfModeIneligible(toc, nil, frameSize)
	}
	d.recordDecodedTOC(toc)
	return out, nil
}

// decodeCELTModeWithTransition decodes a CELT-only frame, resetting the shared
// CELT decoder on a mode change (libopus OPUS_RESET_STATE) and applying the 5 ms
// pcm_transition crossfade from the previous SILK/Hybrid mode.
func (d *streamState) decodeCELTModeWithTransition(frame []byte, frameSize, transSize int, toc streamTOC, qextPayload []byte) ([]float32, error) {
	celtBW := celt.BandwidthFromOpusConfig(toc.bandwidth)

	ts, err := d.beginModeTransition(toc, transSize)
	if err != nil {
		return nil, err
	}

	// libopus resets the CELT decoder on any mode change that did not come from
	// a redundancy frame before decoding the CELT-only frame.
	if d.haveDecoded && int(d.lastMode) != streamModeCELT && !d.prevRedundancy {
		d.celtDec.Reset()
	}
	d.celtDec.SetBandwidth(celtBW)
	if extsupport.QEXT {
		d.setCELTQEXTPayload(qextPayload)
	}

	channels := int(d.channels)
	out := make([]float32, frameSize*channels)
	if err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(frame, frameSize, toc.stereo, out); err != nil {
		return nil, err
	}

	d.applyModeTransition(&ts, out, frameSize)
	d.prevRedundancy = false
	return out, nil
}

// decodePLCToFloat32 conceals frameSize samples for a lost or degenerate
// (<=1-byte) frame. It mirrors opus_decode_frame's concealment loop
// (src/opus_decoder.c:345): when the requested size exceeds F20 (20 ms) the
// concealment is produced F20 samples at a time, each chunk advancing the
// per-stream concealment state. SILK comfort-noise generation is sized to one
// <=20 ms frame, so an unchunked >20 ms request would otherwise overrun its
// scratch; chunking here matches libopus and keeps every concealer within bounds.
func (d *streamState) decodePLCToFloat32(frameSize int) ([]float32, error) {
	f20 := int(d.sampleRate) / 50
	if f20 <= 0 || frameSize <= f20 {
		return d.decodePLCChunkToFloat32(frameSize)
	}

	channels := int(d.channels)
	out := make([]float32, 0, frameSize*channels)
	remaining := frameSize
	for remaining > 0 {
		chunk := min(remaining, f20)
		decoded, err := d.decodePLCChunkToFloat32(chunk)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded...)
		remaining -= chunk
	}
	return out, nil
}

// decodePLCChunkToFloat32 conceals a single <=F20 frame in the stream's last
// decoded mode.
func (d *streamState) decodePLCChunkToFloat32(frameSize int) ([]float32, error) {
	d.recordDecodeCall(frameSize, 0)

	if !d.haveDecoded {
		return make([]float32, frameSize*int(d.channels)), nil
	}

	switch d.lastMode {
	case streamModeSILK:
		return d.finishDecode32(d.decodeSILKToFloat32(nil, frameSize, d.lastPacketStereo, int(d.lastBandwidth)))
	case streamModeHybrid:
		out, err := d.finishDecode32(d.hybridDec.DecodeToFloat32WithPacketStereo(nil, frameSize, d.lastPacketStereo))
		if extsupport.OSCERuntime && err == nil {
			d.markOSCEInactiveIfModeIneligible(streamTOC{mode: streamModeHybrid, bandwidth: int(d.lastBandwidth), stereo: d.lastPacketStereo}, nil, frameSize)
		}
		return out, err
	case streamModeCELT:
		d.celtDec.SetBandwidth(celt.BandwidthFromOpusConfig(int(d.lastBandwidth)))
		out := make([]float32, frameSize*int(d.channels))
		err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(nil, frameSize, d.lastPacketStereo, out)
		out, err = d.finishDecode32(out, err)
		if extsupport.OSCERuntime && err == nil {
			d.markOSCEInactiveIfModeIneligible(streamTOC{mode: streamModeCELT, bandwidth: int(d.lastBandwidth), stereo: d.lastPacketStereo}, nil, frameSize)
		}
		return out, err
	default:
		return make([]float32, frameSize*int(d.channels)), nil
	}
}

func (d *streamState) decodePacketToFloat32(data []byte, frameSize int) ([]float32, error) {
	if len(data) == 0 {
		return d.decodePLCToFloat32(frameSize)
	}
	if len(data) < 1 {
		return nil, ErrPacketTooShort
	}

	d.recordDecodeCall(frameSize, len(data))

	toc := parseStreamTOC(data[0])
	parsed, err := parseOpusPacket(data, false)
	if err != nil {
		return nil, err
	}

	frameCount := len(parsed.frames)
	if frameCount == 0 {
		return nil, ErrInvalidPacket
	}

	var qextPayloads streamQEXTPayloads
	if extsupport.QEXT && !d.ignoreExtensions && toc.mode == streamModeCELT && len(parsed.padding) > 0 {
		qextPayloads.collect(parsed.padding, parsed.paddingFrameCount, qextPacketExtensionID)
	}

	if frameCount == 1 {
		var qextPayload []byte
		if extsupport.QEXT && !d.ignoreExtensions {
			qextPayload = qextPayloads.frame(0)
		}
		return d.finishDecode32(d.decodeFramePayloadToFloat32(parsed.frames[0], frameSize, toc, qextPayload))
	}
	if frameSize%frameCount != 0 {
		return nil, fmt.Errorf("multistream: frameSize %d not divisible by packet frame count %d", frameSize, frameCount)
	}

	subFrameSize := frameSize / frameCount
	out := make([]float32, 0, frameSize*int(d.channels))
	for i := range frameCount {
		var qextPayload []byte
		if extsupport.QEXT && !d.ignoreExtensions {
			qextPayload = qextPayloads.frame(i)
		}
		frameDecoded, err := d.decodeFramePayloadToFloat32(parsed.frames[i], subFrameSize, toc, qextPayload)
		if err != nil {
			return nil, err
		}
		out = append(out, frameDecoded...)
	}
	return d.finishDecode32(out, nil)
}

// Decoder decodes Opus multistream packets containing multiple elementary streams.
// Each stream is decoded independently and routed to output channels via a mapping table.
//
// Multistream packets are used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 7845 Section 5.1.1
type Decoder struct {
	// sampleRate is the output sample rate (8000, 12000, 16000, 24000, or 48000 Hz).
	sampleRate int32

	// outputChannels is the total number of output channels (1-255).
	outputChannels int

	// streams is the total number of elementary streams (N).
	streams int

	// coupledStreams is the number of coupled (stereo) streams (M).
	// The first M streams produce 2 channels each, the remaining N-M produce 1 channel.
	coupledStreams int

	// mapping is the channel mapping table.
	// mapping[i] indicates which decoded channel feeds output channel i.
	// Values 0 to 2*M-1 are from coupled streams (even=left, odd=right).
	// Values 2*M to N+M-1 are from uncoupled streams.
	// Value 255 indicates a silent channel.
	mapping []byte

	// decoders contains one decoder per stream.
	// First M decoders are stereo (for coupled streams).
	// Remaining N-M decoders are mono (for uncoupled streams).
	decoders []streamDecoder

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State

	// Optional projection demixing matrix in column-major S16 layout.
	projectionDemixing []int16
	projectionCols     int
	projectionScratch  []float32
	softClipMem        []float32
	ignoreExtensions   bool
	dnnBlob            *dnnblob.Blob
	decoderDREDFields
	decoderOSCEFields
	pitchDNNLoaded    bool
	plcModelLoaded    bool
	farganModelLoaded bool

	// Per-call decode scratch reused across Decode calls to reduce the
	// steady-state decode allocation footprint. These slice headers are
	// intra-call scratch that never escape the decoder; their element buffers
	// alias packet data / elementary-decoder-owned outputs that are copied into
	// the channel-mapped result within the same call.
	packetsScratch        [][]byte
	decodedStreamsScratch [][]float32

	// packetParser holds reusable parse/build working buffers, and reframeArena
	// backs the N-1 self-delimited packets reframed to standard form for the
	// elementary decoders. The arena slices coexist across the per-stream decode
	// loop but never escape the decode call.
	packetParser packetScratch
	reframeArena []byte
}

// NewDecoder creates a new multistream decoder.
//
// Parameters:
//   - sampleRate: output sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total output channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//   - mapping: channel mapping table (length must equal channels)
//
// The mapping table determines how decoded audio is routed to output channels:
//   - Values 0 to 2*M-1: from coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: from uncoupled (mono) streams
//   - Value 255: silent channel (output zeros)
//
// Example for 5.1 surround (6 channels, 4 streams, 2 coupled):
//
//	mapping = [0, 4, 1, 2, 3, 5]
//	  Channel 0 (FL): mapping[0]=0 -> coupled stream 0, left
//	  Channel 1 (C):  mapping[1]=4 -> uncoupled stream 2 (2*2+0)
//	  Channel 2 (FR): mapping[2]=1 -> coupled stream 0, right
//	  Channel 3 (RL): mapping[3]=2 -> coupled stream 1, left
//	  Channel 4 (RR): mapping[4]=3 -> coupled stream 1, right
//	  Channel 5 (LFE): mapping[5]=5 -> uncoupled stream 3 (2*2+1)
func NewDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*Decoder, error) {
	// Validate parameters
	if channels < 1 || channels > 255 {
		return nil, ErrInvalidChannels
	}
	if streams < 1 || streams > 255 {
		return nil, ErrInvalidStreams
	}
	if coupledStreams < 0 || coupledStreams > streams {
		return nil, ErrInvalidCoupledStreams
	}
	if streams+coupledStreams > 255 {
		return nil, ErrTooManyChannels
	}
	if len(mapping) != channels {
		return nil, ErrInvalidMapping
	}

	// Validate each mapping entry
	maxMappingValue := streams + coupledStreams
	for i, m := range mapping {
		if m != 255 && int(m) >= maxMappingValue {
			return nil, fmt.Errorf("%w: mapping[%d]=%d exceeds maximum %d", ErrInvalidMapping, i, m, maxMappingValue-1)
		}
	}

	// Create stream decoders
	// First M streams are coupled (stereo), remaining N-M are mono
	decoders := make([]streamDecoder, streams)
	for i := range streams {
		var channels int
		if i < coupledStreams {
			channels = 2 // Coupled stream = stereo
		} else {
			channels = 1 // Uncoupled stream = mono
		}
		decoders[i] = newStreamDecoder(sampleRate, channels)
	}

	// Copy mapping to avoid external mutation
	mappingCopy := make([]byte, len(mapping))
	copy(mappingCopy, mapping)

	return &Decoder{
		sampleRate:     int32(sampleRate),
		outputChannels: channels,
		streams:        streams,
		coupledStreams: coupledStreams,
		mapping:        mappingCopy,
		decoders:       decoders,
		plcState:       plc.NewState(),
		softClipMem:    make([]float32, channels),
	}, nil
}

// Reset clears all decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	for _, dec := range d.decoders {
		dec.Reset()
		dec.SetIgnoreExtensions(d.ignoreExtensions)
	}
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()
	clear(d.softClipMem)
	d.clearDREDPayloadState()
	d.resetDREDRuntimeState()
}

// SetIgnoreExtensions toggles libopus-style opaque packet-extension handling for
// every elementary stream. When true, opaque in-band extensions (including any
// DRED side payloads) carried in packet padding are ignored rather than parsed.
func (d *Decoder) SetIgnoreExtensions(ignore bool) {
	d.ignoreExtensions = ignore
	for _, dec := range d.decoders {
		dec.SetIgnoreExtensions(ignore)
	}
	if ignore {
		d.clearDREDPayloadState()
	}
}

// IgnoreExtensions reports whether opaque packet-extension handling is disabled.
func (d *Decoder) IgnoreExtensions() bool {
	return d.ignoreExtensions
}

func (d *Decoder) firstStreamState() *streamState {
	if len(d.decoders) == 0 {
		return nil
	}
	st, _ := d.decoders[0].(*streamState)
	return st
}

// SetGain sets the decoder output gain in Q8 dB units, applied uniformly to every
// elementary stream (libopus OPUS_SET_GAIN semantics). Valid range is
// [-32768, 32767]; out-of-range values return ErrInvalidGain.
func (d *Decoder) SetGain(gainQ8 int) error {
	if gainQ8 < -32768 || gainQ8 > 32767 {
		return ErrInvalidGain
	}
	for _, dec := range d.decoders {
		if st, ok := dec.(*streamState); ok {
			st.decodeGainQ8 = int32(gainQ8)
		}
	}
	return nil
}

// Gain returns the decoder output gain in Q8 dB units (OPUS_GET_GAIN).
func (d *Decoder) Gain() int {
	if st := d.firstStreamState(); st != nil {
		return st.Gain()
	}
	return 0
}

// SetPhaseInversionDisabled disables (or re-enables) stereo phase inversion for
// every coupled stream, mirroring OPUS_SET_PHASE_INVERSION_DISABLED. Disabling it
// trades stereo quality for better mono downmix behaviour.
func (d *Decoder) SetPhaseInversionDisabled(disabled bool) {
	for _, dec := range d.decoders {
		if st, ok := dec.(*streamState); ok {
			st.SetPhaseInversionDisabled(disabled)
		}
	}
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (d *Decoder) PhaseInversionDisabled() bool {
	if st := d.firstStreamState(); st != nil {
		return st.PhaseInversionDisabled()
	}
	return false
}

// SetComplexity sets the decoder complexity (0-10) for every elementary stream,
// mirroring OPUS_SET_COMPLEXITY. Out-of-range values return ErrInvalidComplexity.
func (d *Decoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return ErrInvalidComplexity
	}
	for _, dec := range d.decoders {
		if st, ok := dec.(*streamState); ok {
			if err := st.SetComplexity(complexity); err != nil {
				return err
			}
		}
	}
	return nil
}

// Complexity returns the decoder complexity setting (OPUS_GET_COMPLEXITY).
func (d *Decoder) Complexity() int {
	if st := d.firstStreamState(); st != nil {
		return st.Complexity()
	}
	return 0
}

// Bandwidth returns the audio bandwidth of the last decoded packet, taken from
// the first stream (OPUS_GET_BANDWIDTH). Defaults to fullband before any decode.
func (d *Decoder) Bandwidth() types.Bandwidth {
	if st := d.firstStreamState(); st != nil {
		return st.Bandwidth()
	}
	return types.BandwidthFullband
}

// LastPacketDuration returns the duration in samples (at the decoder sample rate)
// of the last decoded packet, taken from the first stream
// (OPUS_GET_LAST_PACKET_DURATION).
func (d *Decoder) LastPacketDuration() int {
	if st := d.firstStreamState(); st != nil {
		return st.LastPacketDuration()
	}
	return 0
}

// GetFinalRange returns the XOR of every elementary stream's range-coder final
// state, mirroring libopus opus_multistream_decoder_ctl(OPUS_GET_FINAL_RANGE).
// It is used to verify bit-exact decode against another implementation.
func (d *Decoder) GetFinalRange() uint32 {
	var finalRange uint32
	for _, dec := range d.decoders {
		if st, ok := dec.(*streamState); ok {
			finalRange ^= st.FinalRange()
		}
	}
	return finalRange
}

// FinalRange returns the combined range-coder final state for the last decoded
// packet. It is an alias for GetFinalRange.
func (d *Decoder) FinalRange() uint32 {
	return d.GetFinalRange()
}

// Channels returns the total number of output channels.
func (d *Decoder) Channels() int {
	return d.outputChannels
}

// SampleRate returns the output sample rate in Hz.
func (d *Decoder) SampleRate() int {
	return int(d.sampleRate)
}

// Streams returns the total number of elementary streams.
func (d *Decoder) Streams() int {
	return d.streams
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (d *Decoder) CoupledStreams() int {
	return d.coupledStreams
}

// NewDecoderDefault creates a multistream decoder with default Vorbis-style mapping
// for standard channel configurations (1-8 channels).
//
// This is a convenience function that calls DefaultMapping() to get the appropriate
// streams, coupledStreams, and mapping for the given channel count.
//
// Supported channel counts:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (4 streams, 3 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
func NewDecoderDefault(sampleRate, channels int) (*Decoder, error) {
	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		return nil, err
	}
	return NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
}
