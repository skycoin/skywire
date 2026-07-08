//go:build gopus_osce || gopus_dred

package gopus

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/silk"
)

func (d *Decoder) explicitDREDResultForDecode(dred *DRED) internaldred.Result {
	sampleRate := d.dredRuntimeSampleRate()
	if d == nil || dred == nil || !dred.Processed() || sampleRate <= 0 {
		return internaldred.Result{}
	}
	maxDREDSamples := d.maxCachedDREDSamples()
	if maxDREDSamples <= 0 {
		maxDREDSamples = internaldred.MaxLatents * internaldred.LatentSpanSamples(sampleRate)
	}
	return dred.cache.Parsed.ForRequest(internaldred.Request{
		MaxDREDSamples: maxDREDSamples,
		SampleRate:     sampleRate,
	})
}

func (d *Decoder) queueExplicitDREDRecovery(dred *DRED, dredOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	r := d.dredRecoveryState()
	if d == nil || r == nil || dred == nil || !dred.Processed() || frameSizeSamples <= 0 {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if r.dredPLC.Blend() == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(
		&r.dredPLC,
		d.explicitDREDResultForDecode(dred),
		&dred.decoded,
		dredOffsetSamples,
		frameSizeSamples,
		initFrames,
	)
}

func (d *Decoder) decodeExplicitDREDFloat(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	if d == nil || dred == nil || !dred.Processed() {
		return 0, ErrInvalidArgument
	}
	if !d.dredNeuralConcealmentReady() {
		return 0, ErrOptionalExtensionUnavailable
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil {
		return 0, ErrInvalidArgument
	}
	if frameSizeSamples <= 0 {
		return 0, ErrInvalidArgument
	}
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	lossQuantum := sampleRate / 400
	if lossQuantum <= 0 || frameSizeSamples < lossQuantum || frameSizeSamples%lossQuantum != 0 {
		return 0, ErrInvalidArgument
	}
	needed := frameSizeSamples * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	// Explicit DRED queues FEC features before concealment; analysis priming is
	// only for non-FEC neural PLC entry history.
	if d.prevMode == ModeHybrid && channels >= 1 && channels <= 2 {
		return d.decodeExplicitHybridDREDFloat(dred, dredOffsetSamples, pcm[:needed], frameSizeSamples)
	}
	if d.prevMode == ModeSILK && (channels == 1 || channels == 2) {
		// SILK-only previous mode: libopus runs the standard SILK PLC path
		// with lpcnet FEC features queued; the SILK DeepPLC hook produces
		// the 16 kHz neural concealment lowband and SILK upsamples to the
		// output rate. The neural runtime needs its 16 kHz PCM history
		// primed from the prior SILK decode so FARGAN/LPCNet enter with the
		// voiced trajectory rather than zeros.
		//
		// Stereo SILK DRED uses libopus's single LPCNetPLCState path: seed
		// from native SILK channel 0 and let SILK PLC/resampling emit the API
		// channels.
		return d.decodeExplicitSILKDREDFloat(dred, dredOffsetSamples, pcm[:needed], frameSizeSamples)
	}
	return d.decodeExplicitDRED48kNeuralFloat(dred, dredOffsetSamples, pcm[:needed], frameSizeSamples)
}

func (d *Decoder) decodeExplicitDRED48kNeuralFloat(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	if d == nil {
		return 0, ErrInvalidArgument
	}
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	needed := frameSizeSamples * channels
	if frameSizeSamples <= 0 || len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
	// Standalone DRED has already queued its parsed features. Keep cached
	// sidecar scheduling out of this path so it cannot replace the explicit
	// queue with state from the previous decoded packet.
	if d.celtDecoder != nil && !d.celtDecoder.LastPLCFrameWasNeural() {
		d.primeDREDCELTEntryHistory(d.prevMode, false)
	}

	chunkLimit := sampleRate / 25 * 3
	if chunkLimit <= 0 || frameSizeSamples <= chunkLimit {
		if !d.applyDREDNeuralConcealment48kMono(pcm[:needed], frameSizeSamples) {
			return 0, ErrInvalidPacket
		}
	} else {
		remaining := frameSizeSamples
		offset := 0
		for remaining > 0 {
			chunk := nextPLCChunkSamples(sampleRate, d.prevMode, remaining)
			if chunk <= 0 {
				return 0, ErrInvalidPacket
			}
			start := offset * channels
			end := start + chunk*channels
			if end > len(pcm) || !d.applyDREDNeuralConcealment48kMono(pcm[start:end], chunk) {
				packetFrameSize := d.lastFrameSize
				if packetFrameSize <= 0 {
					packetFrameSize = int32(chunk)
				}
				n, err := d.decodePLCChunksInto(pcm[start:needed], remaining, plcDecodeState{
					packetFrameSize:    int(packetFrameSize),
					mode:               d.prevMode,
					bandwidth:          d.lastBandwidth,
					packetStereo:       d.prevPacketStereo,
					useDecoderPLCState: true,
				})
				if err != nil {
					return 0, err
				}
				remaining -= n
				break
			}
			offset += chunk
			remaining -= chunk
		}
		if remaining != 0 {
			return 0, ErrInvalidPacket
		}
	}
	d.applyOutputGain(pcm[:needed])
	d.lastFrameSize = int32(frameSizeSamples)
	d.lastPacketDuration = int32(frameSizeSamples)
	d.lastDataLen = 0
	return frameSizeSamples, nil
}

func (d *Decoder) decodeExplicitHybridDREDFloat(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	if d == nil {
		return 0, ErrInvalidArgument
	}
	d.primeHybridDREDEntryHistory(frameSizeSamples)
	queued := d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
	var lowbandSnapshot *silk.DeepPLCLowbandSnapshot
	cleanupHook := func() {}
	usedHook := func() bool { return false }
	if d.dredNeuralConcealmentAvailable() && d.silkDecoder != nil {
		lowbandSnapshot = d.silkDecoder.SnapshotDeepPLCLowbandMono()
		cleanupHook, usedHook = d.beginHybridDREDLowbandHook()
	}
	defer cleanupHook()
	n, err := d.decodePLCChunksInto(pcm, frameSizeSamples, plcDecodeState{
		packetFrameSize:    frameSizeSamples,
		mode:               d.prevMode,
		bandwidth:          d.lastBandwidth,
		packetStereo:       d.prevPacketStereo,
		useDecoderPLCState: true,
	})
	if err != nil {
		return 0, err
	}
	if usedHook() {
		d.finishActiveDREDRecovery(n)
	} else if queued.NeededFeatureFrames > 0 || d.dredRecoveryState() != nil {
		d.advanceHybridDREDLowbandState(n, lowbandSnapshot)
	}
	d.applyOutputGain(pcm[:n*int(d.channels)])
	d.lastFrameSize = int32(n)
	d.lastPacketDuration = int32(n)
	d.lastDataLen = 0
	return n, nil
}

func (d *Decoder) primeHybridDREDEntryHistory(frameSizeSamples int) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || n.dredRawHistoryUpdated {
		return
	}
	if r.dredPLC.Blend() != 0 || r.dredPLC.FECReadPos() != 0 || r.dredPLC.FECFillPos() != 0 || r.dredPLC.FECSkip() != 0 {
		return
	}
	d.refreshDREDHistoryFromHybridDecoder(frameSizeSamples)
}

// decodeExplicitSILKDREDFloat mirrors libopus opus_decode_native()'s SILK-only
// DRED branch: queue lpcnet FEC features, prime the DeepPLC entry history from
// the prior SILK lowband decode, install the SILK DeepPLC hook so SILK PLC
// pulls neural concealment instead of running its classical periodic/random
// LPC concealment, then run standard SILK PLC via decodePLCChunksInto. SILK
// upsamples the neural lowband to the API sample rate using its existing
// resampler, matching libopus's `silk_Decode(lost_flag=1)` flow.
//
// Stereo SILK DRED still uses one LPCNetPLCState, as in opus_decoder.c. The
// entry history comes from native SILK channel 0, then normal SILK PLC and
// resampling produce the caller-visible channels.
func (d *Decoder) decodeExplicitSILKDREDFloat(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	if d == nil {
		return 0, ErrInvalidArgument
	}
	d.primeExplicitSILKDREDEntryHistory()
	d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
	cleanupHook := func() {}
	usedHook := func() bool { return false }
	if d.dredNeuralConcealmentAvailable() && d.silkDecoder != nil {
		cleanupHook, usedHook = d.beginHybridDREDLowbandHook()
	}
	defer cleanupHook()
	n, err := d.decodePLCChunksInto(pcm, frameSizeSamples, plcDecodeState{
		packetFrameSize:    frameSizeSamples,
		mode:               d.prevMode,
		bandwidth:          d.lastBandwidth,
		packetStereo:       d.prevPacketStereo,
		useDecoderPLCState: true,
	})
	if err != nil {
		return 0, err
	}
	// libopus passes the single LPCNetPLCState only to SILK channel 0. If
	// the lowband hook fired, it already consumed the queued FEC features.
	// Older stereo paths that cannot fire the hook still need an explicit
	// state advance so retained lpcnet/FARGAN continuity matches libopus.
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if usedHook() {
		d.finishActiveDREDRecovery(n)
	} else if channels == 2 && n > 0 && sampleRate > 0 {
		// DRED neural concealment runs at 16 kHz; convert decoder-rate
		// samples to lpcnet 16 kHz sample count. Skip if the conversion
		// is non-integral or yields a non-FrameSize multiple.
		nativeSamples := n * 16000 / sampleRate
		if nativeSamples > 0 && n*16000%sampleRate == 0 {
			_ = d.generateDREDNeuralFrames16k(nil, nativeSamples)
		}
	}
	d.applyOutputGain(pcm[:n*channels])
	d.lastFrameSize = int32(n)
	d.lastPacketDuration = int32(n)
	d.lastDataLen = 0
	return n, nil
}

// decodeCELTNeuralPLCInto runs libopus FRAME_PLC_NEURAL concealment for a lost
// CELT/Hybrid frame on the public Decode(nil) path. libopus celt_decode_lost()
// (celt_decoder.c:727-735) selects FRAME_PLC_NEURAL whenever start==0, the
// lpcnet model is loaded, complexity>=5, plc_duration<80 and !skip_plc -- and it
// runs that pure LPCNet concealment regardless of whether any DRED packet was
// queued. opus_decode(NULL) passes dred==NULL, so the cached-DRED FEC feature
// feed (opus_decoder.c:736) is skipped: fec_fill_pos stays <= fec_read_pos and
// the FRAME_DRED branch is not taken. We mirror that here by priming the neural
// entry history but never queuing cached DRED features, so
// applyDREDNeuralConcealment48kMono runs the neural-PLC (not DRED) branch.
func (d *Decoder) decodeCELTNeuralPLCInto(pcm []float32, frameSizeSamples int, state plcDecodeState) (int, bool, error) {
	if d == nil || (state.mode != ModeCELT && state.mode != ModeHybrid) {
		return 0, false, nil
	}
	if d.channels < 1 || d.channels > 2 || d.celtDecoder == nil || !d.dredNeuralConcealmentAvailable() {
		return 0, false, nil
	}
	channels := int(d.channels)
	needed := frameSizeSamples * channels
	if frameSizeSamples <= 0 || len(pcm) < needed {
		return 0, false, ErrBufferTooSmall
	}
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return 0, false, nil
	}
	sampleRate := int(d.sampleRate)
	chunkLimit := sampleRate / 25 * 3
	if chunkLimit <= 0 || frameSizeSamples <= chunkLimit {
		// Prime the neural entry history (no cached-DRED queue) then run a single
		// neural-PLC concealment frame.
		if !d.celtDecoder.LastPLCFrameWasNeural() {
			d.primeDREDCELTEntryHistory(state.mode, false)
		}
		if !d.applyDREDNeuralConcealment48kMono(pcm[:needed], frameSizeSamples) {
			return 0, false, nil
		}
		return frameSizeSamples, true, nil
	}
	remaining := frameSizeSamples
	offset := 0
	for remaining > 0 {
		chunk := nextPLCChunkSamples(sampleRate, state.mode, remaining)
		if chunk <= 0 {
			return offset, false, nil
		}
		start := offset * channels
		end := start + chunk*channels
		if end > len(pcm) {
			return offset, false, nil
		}
		if !d.celtDecoder.LastPLCFrameWasNeural() {
			d.primeDREDCELTEntryHistory(state.mode, false)
		}
		if !d.applyDREDNeuralConcealment48kMono(pcm[start:end], chunk) {
			return offset, false, nil
		}
		offset += chunk
		remaining -= chunk
	}
	return frameSizeSamples, true, nil
}

func (d *Decoder) decodeSILKNeuralPLCInto(pcm []float32, frameSizeSamples int, state plcDecodeState) (int, bool, error) {
	if d == nil || state.mode != ModeSILK || d.silkDecoder == nil || !d.dredNeuralConcealmentAvailable() {
		return 0, false, nil
	}
	if d.channels < 1 || d.channels > 2 {
		return 0, false, nil
	}
	needed := frameSizeSamples * int(d.channels)
	if frameSizeSamples <= 0 || len(pcm) < needed {
		return 0, false, ErrBufferTooSmall
	}
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return 0, false, nil
	}

	cleanupHook, usedHook := d.beginHybridDREDLowbandHook()
	defer cleanupHook()
	n, err := d.decodePLCChunksInto(pcm[:needed], frameSizeSamples, state)
	if err != nil {
		return n, false, err
	}
	return n, usedHook(), nil
}

// primeExplicitSILKDREDEntryHistory seeds the DRED neural concealment entry
// history from the SILK-only native lowband produced by the previous decode.
// Without this priming, the FARGAN renderer enters concealment with a zeroed
// PCM history and produces near-silent output instead of the voiced trajectory
// libopus emits. Mirrors the Hybrid priming flow but pulls the native PCM via
// silk.Decoder.LatestNativeMono() since the SILK-only path produces a full
// native lowband (not just the Hybrid lowband portion).
func (d *Decoder) primeExplicitSILKDREDEntryHistory() {
	if d == nil || d.silkDecoder == nil {
		return
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || n.dredRawHistoryUpdated {
		return
	}
	if r.dredPLC.Blend() != 0 || r.dredPLC.FECReadPos() != 0 || r.dredPLC.FECFillPos() != 0 || r.dredPLC.FECSkip() != 0 {
		return
	}
	d.refreshDREDHistoryFromSILKDecoder()
}
