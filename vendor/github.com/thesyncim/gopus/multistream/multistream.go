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
	applyChannelMapping32Into(output, decodedStreams, mapping, coupledStreams, frameSize, outputChannels)
	return output
}

func applyChannelMapping32Into(output []float32, decodedStreams [][]float32, mapping []byte, coupledStreams, frameSize, outputChannels int) {
	clear(output[:frameSize*outputChannels])
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

}

func (d *Decoder) decodeStreamToFloat32(stream int, packet []byte, frameSize int) ([]float32, error) {
	if stream < d.coupledStreams {
		return d.decoders[stream].DecodeStereo(packet, frameSize)
	}
	return d.decoders[stream].Decode(packet, frameSize)
}

// Decode returns a newly allocated interleaved float32 PCM slice. frameSize is
// the maximum number of samples per channel at SampleRate; requests above 120 ms
// are capped. A packet returns its actual duration, which must not exceed
// frameSize. A nil or empty data slice requests PLC for the capped frameSize.
// All elementary streams in a packet must have the same duration.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float32, error) {
	return d.DecodeToFloat32(data, frameSize)
}

// DecodeToInt16 returns newly allocated interleaved signed 16-bit PCM.
// frameSize is the maximum number of samples per channel at SampleRate; requests
// above 120 ms are capped. A packet returns its actual duration, and nil or empty
// data requests PLC for the capped frameSize.
func (d *Decoder) DecodeToInt16(data []byte, frameSize int) ([]int16, error) {
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		if pcm, handled, err := d.decodeFixedProjectionInt16(data, frameSize); err != nil {
			return nil, err
		} else if handled {
			return pcm, nil
		}
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

	if pcm, handled, err := d.decodeFixedOutputInt16(data, frameSize); err != nil {
		return nil, err
	} else if handled {
		return pcm, nil
	}

	samples, err := d.decodeToFloat32(data, frameSize, true, true)
	if err != nil {
		return nil, err
	}

	return float32ToInt16(samples), nil
}

// DecodeToFloat32 returns newly allocated interleaved float32 PCM with values
// approximately in [-1, 1]. frameSize is the maximum number of samples per
// channel at SampleRate; requests above 120 ms are capped. A packet returns its
// actual duration, and nil or empty data requests PLC for the capped frameSize.
func (d *Decoder) DecodeToFloat32(data []byte, frameSize int) ([]float32, error) {
	if frameSize <= 0 {
		return nil, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)
	output := d.outputScratchFor(frameSize * d.outputChannels)
	n, err := d.DecodeIntoFloat32(data, output, frameSize)
	if err != nil {
		return nil, err
	}
	return append([]float32(nil), output[:n*d.outputChannels]...), nil
}

// DecodeIntoFloat32 decodes into output and returns the number of samples per
// channel written. frameSize is the maximum number of samples per channel at
// SampleRate; requests above 120 ms are capped. Only the interleaved prefix for
// the decoded duration is written. It returns ErrBufferTooSmall when the packet
// exceeds frameSize or output is too short.
func (d *Decoder) DecodeIntoFloat32(data []byte, output []float32, frameSize int) (int, error) {
	if n, handled, err := d.decodeFixedOutputFloat32(data, output, frameSize); err != nil {
		return 0, err
	} else if handled {
		return n, nil
	}
	return d.decodeToFloat32Into(data, frameSize, true, false, output)
}

func (d *Decoder) outputScratchFor(n int) []float32 {
	if cap(d.outputScratch) < n {
		d.outputScratch = make([]float32, n)
	}
	return d.outputScratch[:n]
}

func (d *Decoder) decodeToFloat32(data []byte, frameSize int, applyProjection, perStreamSoftClip bool) ([]float32, error) {
	if frameSize <= 0 {
		return nil, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)
	scratch := d.outputScratchFor(frameSize * d.outputChannels)
	n, err := d.decodeToFloat32Into(data, frameSize, applyProjection, perStreamSoftClip, scratch)
	if err != nil {
		return nil, err
	}
	return append([]float32(nil), scratch[:n*d.outputChannels]...), nil
}

func (d *Decoder) decodeToFloat32Into(data []byte, frameSize int, applyProjection, perStreamSoftClip bool, output []float32) (int, error) {
	if frameSize <= 0 {
		return 0, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)

	// A nil OR zero-length packet is packet loss: libopus opus_multistream_decode
	// sets do_plc=1 for len==0 (opus_multistream_decoder.c:213), concealing the
	// requested frame size exactly as for a NULL packet.
	if len(data) == 0 {
		// opus_decode_native rejects PLC sizes that are not a multiple of 2.5 ms
		// before advancing decoder state (opus_decoder.c:733).
		if frameSize%(int(d.sampleRate)/400) != 0 {
			return 0, ErrInvalidPacket
		}
		n, err := d.decodePLCToFloat32Into(frameSize, applyProjection, output)
		if err == nil && extsupport.DREDRuntime && d.dredSidecarActive() {
			d.markDREDConcealedAll()
		}
		return n, err
	}

	packets, err := parseMultistreamPacketScratch(d.packetsScratch, &d.packetParser, &d.reframeArena, data, d.streams)
	if err != nil {
		return 0, fmt.Errorf("multistream: parse error: %w", err)
	}
	d.packetsScratch = packets

	duration, err := validateStreamDurationsAtRateScratch(&d.packetParser, packets, int(d.sampleRate))
	if err != nil {
		return 0, err
	}
	if duration > frameSize {
		return 0, ErrBufferTooSmall
	}
	decodeFrameSize := duration
	needed := decodeFrameSize * d.outputChannels
	if len(output) < needed {
		return 0, ErrBufferTooSmall
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		d.invalidateDREDPayloadState()
	}

	decodedStreams := d.ensureDecodedStreamsScratch()
	for i := 0; i < d.streams; i++ {
		var captureState *streamState
		capturing := false
		if extsupport.DREDRuntime && len(packets[i]) > 0 {
			if st, ok := d.decoders[i].(*streamState); ok {
				toc := parseStreamTOC(packets[i][0])
				captureState = st
				capturing = d.beginDREDRawMonoFrameCapture(i, st, toc.mode, packets[i])
			}
		}
		decoded, decodeErr := d.decodeStreamToFloat32(i, packets[i], decodeFrameSize)
		if capturing {
			d.endDREDRawMonoFrameCapture(i, captureState)
		}
		if decodeErr != nil {
			return 0, fmt.Errorf("multistream: stream %d decode error: %w", i, decodeErr)
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

	output = output[:needed]
	applyChannelMapping32Into(output, decodedStreams, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)
	if applyProjection {
		d.applyProjectionDemixing32(output, decodeFrameSize)
	}

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

	return decodeFrameSize, nil
}

func (d *Decoder) decodePLCToFloat32Into(frameSize int, applyProjection bool, output []float32) (int, error) {
	totalSamples := frameSize * d.outputChannels
	if len(output) < totalSamples {
		return 0, ErrBufferTooSmall
	}
	// Each elementary codec owns its concealment decay and history updates.
	// opus_multistream_decode_native keeps calling it throughout a loss burst.
	_ = d.plcState.RecordLoss()

	maxChunk := int(d.sampleRate) / 50
	if maxChunk > 0 && frameSize > maxChunk {
		remaining := frameSize
		offset := 0
		for remaining > 0 {
			chunk := min(remaining, maxChunk)
			total := chunk * d.outputChannels
			err := d.decodePLCChunkToFloat32Into(chunk, applyProjection, output[offset:offset+total])
			if err != nil {
				return 0, err
			}
			offset += total
			remaining -= chunk
		}
		d.recordCompletedPLCPacket(frameSize)
		return frameSize, nil
	}

	if err := d.decodePLCChunkToFloat32Into(frameSize, applyProjection, output[:totalSamples]); err != nil {
		return 0, err
	}
	d.recordCompletedPLCPacket(frameSize)
	return frameSize, nil
}

// src/opus_decoder.c: opus_decode_native reports pcm_count after the full PLC loop.
func (d *Decoder) recordCompletedPLCPacket(frameSize int) {
	for _, decoder := range d.decoders {
		if st, ok := decoder.(*streamState); ok {
			st.lastPacketDuration = int32(frameSize)
		}
	}
}

func (d *Decoder) decodePLCChunkToFloat32Into(frameSize int, applyProjection bool, output []float32) error {
	// opus_decode_native returns loss output before its soft-clip step. Keep
	// each stream's clipping memory intact for the next received packet.
	decodedStreams := d.ensureDecodedStreamsScratch()
	for i := 0; i < d.streams; i++ {
		st, _ := d.decoders[i].(*streamState)
		capturing := false
		if extsupport.DREDRuntime && st != nil {
			capturing = d.beginDREDRawMonoFrameCapture(i, st, int(st.lastMode), nil)
		}
		if extsupport.DREDRuntime {
			if decoded, ok, err := d.decodeDREDPLCStream(i, frameSize); err != nil {
				if capturing {
					d.endDREDRawMonoFrameCapture(i, st)
				}
				return err
			} else if ok {
				if capturing {
					d.endDREDRawMonoFrameCapture(i, st)
				}
				decodedStreams[i] = decoded
				continue
			}
		}
		decoded, err := d.decodeStreamToFloat32(i, nil, frameSize)
		if capturing {
			d.endDREDRawMonoFrameCapture(i, st)
		}
		if err != nil {
			channels := streamChannels(i, d.coupledStreams)
			decoded = d.silenceScratchFor(frameSize * channels)
		}
		decodedStreams[i] = decoded
	}

	applyChannelMapping32Into(output, decodedStreams, d.mapping, d.coupledStreams, frameSize, d.outputChannels)
	if applyProjection {
		d.applyProjectionDemixing32(output, frameSize)
	}
	return nil
}

func (d *Decoder) silenceScratchFor(n int) []float32 {
	if cap(d.silenceScratch) < n {
		d.silenceScratch = make([]float32, n)
	}
	out := d.silenceScratch[:n]
	clear(out)
	return out
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
