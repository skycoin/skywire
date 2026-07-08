//go:build gopus_dred || gopus_osce

package encoder

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

const maxDREDPCM16k = 1920

type dredEncoderModels struct {
	encoder *rdovae.EncoderModel
	pitch   *lpcnetplc.PitchDNNModel
}

func (m dredEncoderModels) loaded() bool {
	return m.encoder != nil && m.pitch != nil
}

type dredEncoderRuntime struct {
	generator           internaldred.LatentGenerator
	resampleMem         [internaldred.ResamplingOrder + 1]float32
	scaledPCM16k        [maxDREDPCM16k]float32
	latentsBuffer       [internaldred.MaxFrames * rdovae.LatentDim]float32
	stateBuffer         [internaldred.MaxFrames * rdovae.StateDim]float32
	packetSnapshot      dredEncoderPacketSnapshot
	activity            [internaldred.ActivityHistorySize]byte
	latestLatents       [rdovae.LatentDim]float32
	latestState         [rdovae.StateDim]float32
	latentsFill         int32
	dredOffset          int32
	latentOffset        int32
	lastExtraDREDOffset int32
	payload             [internaldred.MaxDataSize]byte
	emitted             int32
}

type dredEncoderPacketSnapshot struct {
	valid               bool
	latentsBuffer       [internaldred.MaxFrames * rdovae.LatentDim]float32
	stateBuffer         [internaldred.MaxFrames * rdovae.StateDim]float32
	activity            [internaldred.ActivityHistorySize]byte
	latentsFill         int32
	dredOffset          int32
	latentOffset        int32
	lastExtraDREDOffset int32
}

type dredEncoderExtras struct {
	duration int32
	models   dredEncoderModels
	runtime  *dredEncoderRuntime
}

func (e *Encoder) ensureDREDExtras() *dredEncoderExtras {
	if !extsupport.DREDRuntime {
		return nil
	}
	if e.dred == nil {
		e.dred = &dredEncoderExtras{}
	}
	return e.dred
}

func (e *Encoder) pruneDREDExtrasIfDormant() {
	if e.dred == nil {
		return
	}
	if e.dred.duration == 0 && !e.dred.models.loaded() {
		e.dred = nil
	}
}

func (e *Encoder) dredModelsLoaded() bool {
	return extsupport.DREDRuntime && e.dred != nil && e.dred.models.loaded()
}

func (e *Encoder) dredEncodingActive() bool {
	return extsupport.DREDRuntime && e.dnnBlob != nil && e.dred != nil && e.dred.duration > 0 && e.dred.models.loaded()
}

func (e *Encoder) resetDREDControls() {
	if !extsupport.DREDRuntime || e.dred == nil {
		return
	}
	e.dred.duration = 0
	e.dred.runtime = nil
	e.pruneDREDExtrasIfDormant()
}

func (e *Encoder) clearInactiveDREDHistory() {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.runtime == nil {
		return
	}
	runtime := e.dred.runtime
	runtime.latentsFill = 0
	runtime.activity = [internaldred.ActivityHistorySize]byte{}
	runtime.packetSnapshot.valid = false
}

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob for optional extension
// paths. A nil blob clears the retained model.
func (e *Encoder) SetDNNBlob(blob *dnnblob.Blob) {
	if !extsupport.DREDRuntime {
		e.dnnBlob = blob
		e.pruneDREDExtrasIfDormant()
		return
	}
	if blob == nil {
		e.dnnBlob = nil
		if e.dred != nil {
			e.dred.models = dredEncoderModels{}
			e.dred.runtime = nil
		}
		e.pruneDREDExtrasIfDormant()
		return
	}
	encModel, err := rdovae.LoadEncoder(blob)
	if err != nil {
		if e.dnnBlob == nil {
			e.dnnBlob = blob
		}
		return
	}
	pitchModel, err := lpcnetplc.LoadPitchDNNModel(blob)
	if err != nil {
		if e.dnnBlob == nil {
			e.dnnBlob = blob
		}
		return
	}
	extra := e.ensureDREDExtras()
	if extra == nil {
		return
	}
	if extra.runtime != nil {
		if err := extra.runtime.generator.SetDNNBlobPreservingState(blob); err != nil {
			return
		}
	}
	e.dnnBlob = blob
	extra.models.encoder = encModel
	extra.models.pitch = pitchModel
}

func (e *Encoder) ensureActiveDREDRuntime() *dredEncoderRuntime {
	if !e.dredEncodingActive() {
		return nil
	}
	if e.channels != 1 && e.channels != 2 {
		return nil
	}
	if e.dred.runtime != nil {
		return e.dred.runtime
	}
	runtime := &dredEncoderRuntime{}
	if err := runtime.generator.SetDNNBlob(e.dnnBlob); err != nil {
		return nil
	}
	e.dred.runtime = runtime
	return runtime
}

func (e *Encoder) processDREDLatents(framePCM []opusRes, extraDelay int) int {
	return e.processDREDLatentsWithActivity(framePCM, extraDelay, e.currentDREDActivity(framePCM))
}

func (e *Encoder) processDREDLatentsWithActivity(framePCM []opusRes, extraDelay int, active bool) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	runtime := e.ensureActiveDREDRuntime()
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	if runtime == nil || len(framePCM) == 0 || len(framePCM)%channels != 0 {
		return 0
	}
	samples16k := e.convertDREDFrameTo16k(runtime, framePCM)
	if samples16k == 0 {
		return 0
	}
	extraDelay16k := int32(extraDelay * 16000 / sampleRate)
	emitted := runtime.generator.Process16k(e.dred.models.encoder, runtime.scaledPCM16k[:samples16k], extraDelay16k, func(latents, state []float32) {
		copy(runtime.latentsBuffer[rdovae.LatentDim:], runtime.latentsBuffer[:(internaldred.MaxFrames-1)*rdovae.LatentDim])
		copy(runtime.stateBuffer[rdovae.StateDim:], runtime.stateBuffer[:(internaldred.MaxFrames-1)*rdovae.StateDim])
		copy(runtime.latentsBuffer[:rdovae.LatentDim], latents[:rdovae.LatentDim])
		copy(runtime.stateBuffer[:rdovae.StateDim], state[:rdovae.StateDim])
		copy(runtime.latestLatents[:], latents[:rdovae.LatentDim])
		copy(runtime.latestState[:], state[:rdovae.StateDim])
		if runtime.latentsFill < internaldred.NumRedundancyFrames {
			runtime.latentsFill++
		}
		runtime.emitted++
	})
	runtime.dredOffset = runtime.generator.DREDOffset()
	runtime.latentOffset = runtime.generator.LatentOffset()
	internaldred.UpdateActivityHistory(&runtime.activity, len(framePCM)/channels, sampleRate, active)
	return emitted
}

func (e *Encoder) backfillDREDActivityForFrame(frameSize int, active bool) {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.runtime == nil || e.sampleRate <= 0 {
		return
	}
	frameActivity := frameSize * 400 / int(e.sampleRate)
	if frameActivity <= 0 {
		return
	}
	if frameActivity > len(e.dred.runtime.activity) {
		frameActivity = len(e.dred.runtime.activity)
	}
	var v byte
	if active {
		v = 1
	}
	for i := 0; i < frameActivity; i++ {
		e.dred.runtime.activity[i] = v
	}
}

func (e *Encoder) processDREDLatentsForPacket(framePCM []opusRes, frameSize, extraDelay int, mode Mode) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	if mode == ModeSILK && frameSize > 2880 {
		var encFrameSize int
		switch frameSize {
		case 3840:
			encFrameSize = 1920
		case 4800:
			encFrameSize = 960
		case 5760:
			encFrameSize = 2880
		default:
			return e.processDREDLatents(framePCM, extraDelay)
		}
		channels := int(e.channels)
		if channels <= 0 {
			return 0
		}
		frameSamples := frameSize * channels
		if frameSamples <= 0 || len(framePCM) < frameSamples {
			return 0
		}
		e.clearDREDPacketSnapshot()
		frameStride := encFrameSize * channels
		emitted := 0
		for start := 0; start < frameSamples; start += frameStride {
			end := start + frameStride
			if end > frameSamples {
				return emitted
			}
			emitted += e.processDREDLatents(framePCM[start:end], extraDelay)
			if start == 0 {
				e.snapshotDREDPacketState()
			}
		}
		return emitted
	}
	if mode != ModeSILK && frameSize > 960 && frameSize%960 == 0 {
		channels := int(e.channels)
		if channels <= 0 {
			return 0
		}
		frameSamples := frameSize * channels
		if frameSamples <= 0 || len(framePCM) < frameSamples {
			return 0
		}
		e.clearDREDPacketSnapshot()
		frameStride := 960 * channels
		emitted := 0
		for start := 0; start < frameSamples; start += frameStride {
			end := start + frameStride
			if end > frameSamples {
				return emitted
			}
			emitted += e.processDREDLatents(framePCM[start:end], extraDelay)
			if start == 0 {
				e.snapshotDREDPacketState()
			}
		}
		return emitted
	}
	return e.processDREDLatents(framePCM, extraDelay)
}

func (e *Encoder) snapshotDREDPacketState() {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.runtime == nil {
		return
	}
	runtime := e.dred.runtime
	snapshot := &runtime.packetSnapshot
	copy(snapshot.latentsBuffer[:], runtime.latentsBuffer[:])
	copy(snapshot.stateBuffer[:], runtime.stateBuffer[:])
	copy(snapshot.activity[:], runtime.activity[:])
	snapshot.latentsFill = runtime.latentsFill
	snapshot.dredOffset = runtime.dredOffset
	snapshot.latentOffset = runtime.latentOffset
	snapshot.lastExtraDREDOffset = runtime.lastExtraDREDOffset
	snapshot.valid = true
}

func (e *Encoder) clearDREDPacketSnapshot() {
	if !extsupport.DREDRuntime || e.dred == nil || e.dred.runtime == nil {
		return
	}
	e.dred.runtime.packetSnapshot.valid = false
}

func (e *Encoder) convertDREDFrameTo16k(runtime *dredEncoderRuntime, framePCM []opusRes) int {
	if !extsupport.DREDRuntime {
		return 0
	}
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	if runtime == nil || len(framePCM) == 0 || len(framePCM)%channels != 0 {
		return 0
	}
	frameSize16k := len(framePCM) / channels * 16000 / sampleRate
	if frameSize16k <= 0 || frameSize16k > len(runtime.scaledPCM16k) {
		return 0
	}

	input := framePCM
	out := 0
	for remaining16k := frameSize16k; remaining16k > 0; {
		processSize16k := internaldred.DFrameSize
		if processSize16k > remaining16k {
			processSize16k = remaining16k
		}
		processSize := processSize16k * sampleRate / 16000
		processSamples := processSize * channels
		if processSamples <= 0 || processSamples > len(input) {
			return 0
		}
		n := internaldred.ConvertTo16kMonoFloat32(runtime.scaledPCM16k[out:], &runtime.resampleMem, input[:processSamples], sampleRate, channels)
		if n != processSize16k {
			return 0
		}
		out += n
		// Match libopus dred_compute_latents() pcm advancement: it does
		// `pcm += process_size` regardless of channel count, which under-advances
		// the interleaved-stereo pointer by a factor of `channels` per iteration.
		// Faithfully replicate that here so the DRED encoder sees the same input
		// window libopus does on stereo 40 ms / 60 ms multi-iter Process16k calls.
		// See tmp_check/opus-1.6.1/dnn/dred_encoder.c:240.
		input = input[processSize:]
		remaining16k -= processSize16k
	}
	return out
}
