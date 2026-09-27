// Multistream decode implementation for Opus surround sound.
// This file contains the Decode methods that parse multistream packets,
// decode each elementary stream, and apply channel mapping to produce
// the final interleaved output.

package multistream

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
)

// ensureDecodedStreamsScratch returns a reusable [][]float32 header sized to
// d.streams, clearing any stale per-stream references so a decode failure or
// short stream cannot leak a previous frame's buffer into the channel mapping.
func (d *Decoder) ensureDecodedStreamsScratch() [][]float32 {
	s := d.decodedStreamsScratch
	if cap(s) < d.streams {
		s = make([][]float32, d.streams)
	}
	s = s[:d.streams]
	for i := range s {
		s[i] = nil
	}
	d.decodedStreamsScratch = s
	return s
}

func applyChannelMapping32(decodedStreams [][]float32, mapping []byte, coupledStreams, frameSize, outputChannels int) []float32 {
	output := make([]float32, frameSize*outputChannels)

	for outCh := range outputChannels {
		mappingIdx := mapping[outCh]
		if mappingIdx == 255 {
			continue
		}

		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= len(decodedStreams) {
			continue
		}

		src := decodedStreams[streamIdx]
		srcChannels := streamChannels(streamIdx, coupledStreams)
		for s := range frameSize {
			srcIdx := s*srcChannels + chanInStream
			if srcIdx < len(src) {
				output[s*outputChannels+outCh] = src[srcIdx]
			}
		}
	}

	return output
}

func (d *Decoder) decodeStreamToFloat32(stream int, packet []byte, frameSize int) ([]float32, error) {
	if stream < d.coupledStreams {
		return d.decoders[stream].DecodeStereo(packet, frameSize)
	}
	return d.decoders[stream].Decode(packet, frameSize)
}

// Decode decodes a multistream Opus packet and returns PCM samples.
//
// If data is nil, performs Packet Loss Concealment (PLC) by generating
// concealment audio based on the previous frames' state.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at the decoder sample rate
//
// Returns sample-interleaved float32 samples: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
// where N is the number of output channels.
//
// All elementary streams within the packet must have the same frame duration.
// If durations differ, ErrDurationMismatch is returned.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float32, error) {
	return d.decodeToFloat32(data, frameSize, true, false)
}

// DecodeToInt16 decodes a multistream packet and converts to int16 PCM.
// This is a convenience wrapper for common audio output formats.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at the decoder sample rate
//
// Returns sample-interleaved int16 samples in range [-32768, 32767].
// The output format is: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
func (d *Decoder) DecodeToInt16(data []byte, frameSize int) ([]int16, error) {
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		// libopus opus_projection_decode passes OPTIONAL_CLIP, so each per-stream
		// decoded buffer is soft-clipped before the int16 mapping-matrix multiply.
		// Request that here (the float demix path in DecodeToFloat32 does not).
		samples, err := d.decodeToFloat32(data, frameSize, false, true)
		if err != nil {
			return nil, err
		}
		output := make([]int16, len(samples))
		d.applyProjectionDemixingInt16(output, samples, len(samples)/d.outputChannels)
		return output, nil
	}

	samples, err := d.DecodeToFloat32(data, frameSize)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return float32ToInt16(samples), nil
	}
	return float32ToInt16SoftClip(samples, len(samples)/d.outputChannels, d.outputChannels, d.softClipMem), nil
}

// DecodeToFloat32 decodes a multistream packet and returns float32 PCM.
// This is a convenience wrapper for audio APIs expecting float32.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at the decoder sample rate
//
// Returns sample-interleaved float32 samples in approximate range [-1, 1].
// The output format is: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
func (d *Decoder) DecodeToFloat32(data []byte, frameSize int) ([]float32, error) {
	return d.decodeToFloat32(data, frameSize, true, false)
}

func (d *Decoder) decodeToFloat32(data []byte, frameSize int, applyProjection, perStreamSoftClip bool) ([]float32, error) {
	if extsupport.DREDRuntime && data != nil && len(data) > 0 && d.dredSidecarActive() {
		d.invalidateDREDPayloadState()
	}

	// A nil OR zero-length packet is packet loss: libopus opus_multistream_decode
	// sets do_plc=1 for len==0 (opus_multistream_decoder.c:213), concealing the
	// requested frame size exactly as for a NULL packet.
	if len(data) == 0 {
		output, err := d.decodePLCToFloat32(frameSize, applyProjection, perStreamSoftClip)
		if err == nil && extsupport.DREDRuntime && d.dredSidecarActive() {
			d.markDREDConcealedAll()
		}
		return output, err
	}

	packets, err := parseMultistreamPacketScratch(d.packetsScratch, &d.packetParser, &d.reframeArena, data, d.streams)
	if err != nil {
		return nil, fmt.Errorf("multistream: parse error: %w", err)
	}
	d.packetsScratch = packets

	duration, err := validateStreamDurationsAtRateScratch(&d.packetParser, packets, int(d.sampleRate))
	if err != nil {
		return nil, err
	}
	if duration > frameSize {
		return nil, ErrBufferTooSmall
	}
	decodeFrameSize := duration

	decodedStreams := d.ensureDecodedStreamsScratch()
	for i := 0; i < d.streams; i++ {
		var endDREDCapture func()
		if extsupport.DREDRuntime && d.dredPayloadScannerActive() {
			if st, ok := d.decoders[i].(*streamState); ok && len(packets[i]) > 0 {
				toc := parseStreamTOC(packets[i][0])
				endDREDCapture = d.beginDREDRawMonoGoodFrameCapture(i, st, toc.mode, packets[i])
			}
		}
		decoded, decodeErr := d.decodeStreamToFloat32(i, packets[i], decodeFrameSize)
		if endDREDCapture != nil {
			endDREDCapture()
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("multistream: stream %d decode error: %w", i, decodeErr)
		}
		// libopus opus_decode_native soft-clips each stream's output (sized to the
		// stream's channels) when soft_clip is requested, before the copy/demix
		// callback; otherwise it clears the per-stream soft-clip memory.
		d.applyPerStreamSoftClip(i, decoded, decodeFrameSize, perStreamSoftClip)
		decodedStreams[i] = decoded
	}
	if extsupport.DREDRuntime && d.dredPayloadScannerActive() {
		for i := 0; i < d.streams; i++ {
			d.maybeCacheDREDPayload(i, packets[i])
		}
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		for i := 0; i < d.streams; i++ {
			d.markDREDUpdated(i)
		}
	}

	output := applyChannelMapping32(decodedStreams, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)
	if applyProjection {
		d.applyProjectionDemixing32(output, decodeFrameSize)
	}

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

	return output, nil
}

func (d *Decoder) decodePLCToFloat32(frameSize int, applyProjection, perStreamSoftClip bool) ([]float32, error) {
	fadeFactor := d.plcState.RecordLoss()
	totalSamples := frameSize * d.outputChannels
	if fadeFactor < 0.001 {
		return make([]float32, totalSamples), nil
	}

	maxChunk := int(d.sampleRate) / 50
	if maxChunk > 0 && frameSize > maxChunk {
		output := make([]float32, totalSamples)
		remaining := frameSize
		offset := 0
		for remaining > 0 {
			chunk := min(remaining, maxChunk)
			decoded, err := d.decodePLCChunkToFloat32(chunk, applyProjection, perStreamSoftClip)
			if err != nil {
				return nil, err
			}
			total := chunk * d.outputChannels
			if len(decoded) < total {
				return nil, ErrBufferTooSmall
			}
			copy(output[offset:offset+total], decoded[:total])
			offset += total
			remaining -= chunk
		}
		return output, nil
	}

	return d.decodePLCChunkToFloat32(frameSize, applyProjection, perStreamSoftClip)
}

func (d *Decoder) decodePLCChunkToFloat32(frameSize int, applyProjection, perStreamSoftClip bool) ([]float32, error) {
	decodedStreams := make([][]float32, d.streams)
	for i := 0; i < d.streams; i++ {
		if extsupport.DREDRuntime {
			if decoded, ok, err := d.decodeDREDPLCStream(i, frameSize); err != nil {
				return nil, err
			} else if ok {
				d.applyPerStreamSoftClip(i, decoded, frameSize, perStreamSoftClip)
				decodedStreams[i] = decoded
				continue
			}
		}
		decoded, err := d.decodeStreamToFloat32(i, nil, frameSize)
		if err != nil {
			channels := streamChannels(i, d.coupledStreams)
			decoded = make([]float32, frameSize*channels)
		}
		d.applyPerStreamSoftClip(i, decoded, frameSize, perStreamSoftClip)
		decodedStreams[i] = decoded
	}

	output := applyChannelMapping32(decodedStreams, d.mapping, d.coupledStreams, frameSize, d.outputChannels)
	if applyProjection {
		d.applyProjectionDemixing32(output, frameSize)
	}
	return output, nil
}

// applyPerStreamSoftClip soft-clips stream i's interleaved decoded buffer in
// place when enabled (the int16 OPTIONAL_CLIP path), advancing that stream's
// soft-clip memory; when disabled it clears the memory. This mirrors libopus
// opus_decode_native's per-stream soft_clip step, run before the multistream
// copy/demix callback. A no-op when the stream is not a *streamState (e.g. a
// stub/test decoder).
func (d *Decoder) applyPerStreamSoftClip(i int, decoded []float32, frameSize int, perStreamSoftClip bool) {
	st, ok := d.decoders[i].(*streamState)
	if !ok {
		return
	}
	channels := streamChannels(i, d.coupledStreams)
	if !perStreamSoftClip {
		st.clearSoftClipMem()
		return
	}
	st.softClipStreamOutput(decoded, frameSize, channels)
}

func float32ToInt16(samples []float32) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = opusmath.Float32ToInt16(s)
	}
	return output
}

func float32ToInt16SoftClip(samples []float32, n, channels int, declipMem []float32) []int16 {
	output := make([]int16, len(samples))
	if channels < 1 || n < 1 || len(samples) == 0 || len(declipMem) < channels {
		return output
	}
	total := min(n*channels, len(samples))
	if total <= 0 {
		return output
	}

	tmp := append([]float32(nil), samples[:total]...)
	opusmath.PCMSoftClip(tmp, n, channels, declipMem)
	for i := range total {
		output[i] = opusmath.Float32ToInt16(tmp[i])
	}
	return output
}
