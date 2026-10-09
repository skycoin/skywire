//go:build gopus_osce || gopus_dred

package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
)

// ErrDREDModelNotLoaded reports that the tag-gated standalone DRED
// parse/process path has not been armed with a DRED decoder model blob yet.
var ErrDREDModelNotLoaded = errors.New("gopus: DRED decoder model not loaded")

// DREDRequest supplies the limits used to evaluate cached DRED data.
// MaxDREDSamples is a sample limit at SampleRate; SampleRate is in hertz. The
// request bounds usable feature frames and 40 ms latent chunks.
type DREDRequest = internaldred.Request

// DREDAvailability summarizes request-bounded DRED coverage. FeatureFrames
// counts 10 ms feature frames and MaxLatents counts 40 ms latent chunks;
// OffsetSamples, EndSamples, and AvailableSamples are sample counts at the
// request sample rate. Availability is also bounded by the parsed payload.
type DREDAvailability = internaldred.Availability

// DREDFeatureWindow describes the 10 ms feature indexes considered for a
// concealment request. FeatureOffsetBase and MaxFeatureIndex are inclusive
// indexes; RecoverableFeatureFrames counts requested indexes within the parsed
// payload, while MissingPositiveFrames counts requested indexes beyond it.
type DREDFeatureWindow = internaldred.FeatureWindow

// DREDParsed retains the parsed header and number of payload latent chunks
// available before model-backed processing. Each payload latent chunk spans
// 40 ms.
type DREDParsed = internaldred.Parsed

// DREDResult bundles a DRED request and parsed payload metadata with the
// coverage available under that request.
type DREDResult = internaldred.Result

// DREDProcessStage reports how far a retained DRED packet has progressed through
// the standalone DRED wrapper.
type DREDProcessStage int

const (
	// DREDProcessStageEmpty indicates no retained DRED payload.
	DREDProcessStageEmpty DREDProcessStage = iota
	// DREDProcessStageDeferred indicates Parse retained metadata with deferred
	// processing still pending.
	DREDProcessStageDeferred
	// DREDProcessStageProcessed indicates Process has finalized the retained state.
	DREDProcessStageProcessed
)

// DREDDecoder retains the DRED-specific RDOVAE model and processing state for
// the standalone Parse/Process API. It is available with -tags gopus_dred or
// -tags gopus_osce; its model is separate from the Decoder's PLC models.
type DREDDecoder struct {
	dnnBlob     *dnnblob.Blob
	model       *rdovae.Decoder
	modelLoaded bool
	processor   rdovae.Processor
}

// NewDREDDecoder constructs an empty standalone DRED decoder. Call
// SetDNNBlob before Parse.
func NewDREDDecoder() *DREDDecoder {
	return &DREDDecoder{}
}

// SetDNNBlob copies and validates a standalone DRED decoder model blob. A
// successful call replaces the retained model and resets its processing state;
// an invalid blob leaves the currently loaded model unchanged.
func (d *DREDDecoder) SetDNNBlob(data []byte) error {
	if d == nil {
		return ErrInvalidArgument
	}
	if data == nil {
		return ErrInvalidArgument
	}
	blob, err := dnnblob.Clone(data)
	if err != nil {
		return ErrInvalidArgument
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		return ErrInvalidArgument
	}
	model, err := rdovae.LoadDecoder(blob)
	if err != nil {
		return ErrInvalidArgument
	}
	d.dnnBlob = blob
	d.model = model
	d.modelLoaded = true
	d.processor = rdovae.Processor{}
	return nil
}

// ModelLoaded reports whether a DRED decoder model blob is currently retained.
func (d *DREDDecoder) ModelLoaded() bool {
	return d != nil && d.modelLoaded
}

// DRED mirrors the retained standalone OpusDRED packet state used by the
// tag-gated DRED parse/process wrapper.
type DRED struct {
	data         [internaldred.MaxDataSize]byte
	cache        internaldred.Cache
	decoded      internaldred.Decoded
	processStage DREDProcessStage
}

// NewDRED constructs an empty standalone DRED state wrapper.
func NewDRED() *DRED {
	return &DRED{}
}

// Clear removes the retained payload, parsed metadata, latent/state/feature
// output, and process stage. It does not change the model on DREDDecoder.
func (d *DRED) Clear() {
	if d == nil {
		return
	}
	d.cache.Clear()
	d.decoded.Clear()
	d.processStage = 0
}

// Empty reports whether this DRED state is nil or retains no payload.
func (d *DRED) Empty() bool {
	return d == nil || d.cache.Empty()
}

// Len reports the retained payload size in bytes.
func (d *DRED) Len() int {
	if d == nil {
		return 0
	}
	return d.cache.Len
}

// Parsed returns the retained low-cost DRED metadata.
func (d *DRED) Parsed() DREDParsed {
	if d == nil {
		return DREDParsed{}
	}
	return d.cache.Parsed
}

// ProcessStage reports the retained standalone DRED lifecycle stage.
func (d *DRED) ProcessStage() DREDProcessStage {
	if d == nil {
		return DREDProcessStageEmpty
	}
	return d.processStage
}

// RawProcessStage reports the underlying libopus-shaped process stage:
// `-1` before/without a valid parse, `1` after deferred parse, and `2` once
// processed features have been materialized.
func (d *DRED) RawProcessStage() int {
	if d == nil || d.processStage == DREDProcessStageEmpty {
		return -1
	}
	return int(d.processStage)
}

// NeedsProcessing reports whether Parse deferred standalone DRED processing.
func (d *DRED) NeedsProcessing() bool {
	return d != nil && d.processStage == DREDProcessStageDeferred
}

// Processed reports whether the retained standalone DRED state has been
// finalized through Process.
func (d *DRED) Processed() bool {
	return d != nil && d.processStage == DREDProcessStageProcessed
}

// LatentCount reports how many request-bounded latent vectors are retained from
// the last successful Parse call.
func (d *DRED) LatentCount() int {
	if d == nil {
		return 0
	}
	return d.decoded.NbLatents
}

// FillState copies the retained 50-float RDOVAE state into dst and returns
// the number of floats written. It copies a prefix when dst is short and returns
// zero for an empty state.
func (d *DRED) FillState(dst []float32) int {
	if d == nil || d.Empty() {
		return 0
	}
	return d.decoded.FillState(dst)
}

// FillLatents copies retained request-bounded latent records into dst and
// returns the number of floats written. Each record has 25 latent values
// followed by its quantizer-level value; a short dst receives a prefix.
func (d *DRED) FillLatents(dst []float32) int {
	if d == nil || d.Empty() {
		return 0
	}
	return d.decoded.FillLatents(dst)
}

// FeatureCount reports how many processed DRED feature values are retained from
// the last successful Process call.
func (d *DRED) FeatureCount() int {
	if d == nil || d.processStage != DREDProcessStageProcessed {
		return 0
	}
	return d.decoded.NbLatents * 4 * internaldred.NumFeatures
}

// FillFeatures copies retained processed feature frames into dst and returns
// the number of floats written. Each 10 ms frame has 20 float features; a short
// dst receives a prefix, and deferred or empty states write nothing.
func (d *DRED) FillFeatures(dst []float32) int {
	if d == nil || d.processStage != DREDProcessStageProcessed {
		return 0
	}
	return d.decoded.FillFeatures(dst)
}

// Result evaluates the retained DRED payload against an opus_dred_parse()-style
// request. It returns a zero result when no payload is retained.
func (d *DRED) Result(maxDredSamples, sampleRate int) DREDResult {
	if d == nil {
		return DREDResult{}
	}
	return d.cache.Result(DREDRequest{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     sampleRate,
	})
}

// Availability reports the request-bounded retained DRED coverage.
func (d *DRED) Availability(maxDredSamples, sampleRate int) DREDAvailability {
	return d.Result(maxDredSamples, sampleRate).Availability
}

// MaxAvailableSamples mirrors opus_dred_parse()'s positive sample-count result
// for the retained DRED state and request.
func (d *DRED) MaxAvailableSamples(maxDredSamples, sampleRate int) int {
	return d.Result(maxDredSamples, sampleRate).MaxAvailableSamples()
}

// FillQuantizerLevels writes the request-bounded retained DRED quantizer
// schedule into dst and returns the number of entries written.
func (d *DRED) FillQuantizerLevels(dst []int32, maxDredSamples, sampleRate int) int {
	return d.Result(maxDredSamples, sampleRate).FillQuantizerLevels(dst)
}

// FeatureWindow reports the retained 10 ms DRED feature-index window for a
// concealment request. decodeOffsetSamples and frameSizeSamples use samples at
// sampleRate; initFrames is the number of pre-roll feature frames.
func (d *DRED) FeatureWindow(maxDredSamples, sampleRate, decodeOffsetSamples, frameSizeSamples, initFrames int) DREDFeatureWindow {
	result := d.Result(maxDredSamples, sampleRate)
	if d != nil && d.processStage == DREDProcessStageProcessed {
		return internaldred.ProcessedFeatureWindow(result, &d.decoded, decodeOffsetSamples, frameSizeSamples, initFrames)
	}
	return result.FeatureWindow(decodeOffsetSamples, frameSizeSamples, initFrames)
}

// Parse finds and retains the temporary DRED packet extension from packet and
// returns available and trailing-silence sample counts at sampleRate. It
// requires a loaded DRED decoder model. If packet has no supported DRED
// extension, Parse clears dst and returns zero counts without an error. With
// deferProcessing true, parsed state and latents are retained for a later
// Process call; otherwise model-derived features are produced before Parse
// returns.
//
// The API is available with -tags gopus_dred or -tags gopus_osce.
func (d *DREDDecoder) Parse(dst *DRED, packet []byte, maxDredSamples, sampleRate int, deferProcessing bool) (availableSamples, dredEnd int, err error) {
	if d == nil || dst == nil || sampleRate <= 0 || maxDredSamples < 0 {
		return 0, 0, ErrInvalidArgument
	}
	if !d.modelLoaded {
		return 0, 0, ErrDREDModelNotLoaded
	}
	dst.Clear()
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		dst.Clear()
		return 0, 0, nil
	}
	if err := dst.cache.Store(dst.data[:], payload, frameOffset); err != nil {
		return 0, 0, ErrInvalidPacket
	}
	minFeatureFrames := internaldred.RequestedFeatureFrames(maxDredSamples, sampleRate)
	if _, err := dst.decoded.Decode(payload, frameOffset, minFeatureFrames); err != nil {
		dst.Clear()
		return 0, 0, ErrInvalidPacket
	}
	dst.processStage = DREDProcessStageDeferred
	if !deferProcessing {
		if err := d.Process(dst, dst); err != nil {
			return 0, 0, err
		}
	}
	result := dst.Result(maxDredSamples, sampleRate)
	return result.Availability.AvailableSamples, result.Availability.EndSamples, nil
}

// Process finalizes a deferred DRED state by running the RDOVAE decoder and
// retaining the derived feature frames. src may equal dst; otherwise Process
// copies the retained state to dst. Processing an already processed state is
// idempotent. The DRED decoder model must be loaded.
func (d *DREDDecoder) Process(src, dst *DRED) error {
	if d == nil || src == nil || dst == nil {
		return ErrInvalidArgument
	}
	if !d.modelLoaded {
		return ErrDREDModelNotLoaded
	}
	if src.processStage != DREDProcessStageDeferred && src.processStage != DREDProcessStageProcessed {
		return ErrInvalidArgument
	}
	if src != dst {
		*dst = *src
	}
	if dst.processStage != DREDProcessStageProcessed {
		d.model.DecodeAllWithProcessor(&d.processor, dst.decoded.Features[:], dst.decoded.State[:], dst.decoded.Latents[:], dst.decoded.NbLatents)
	}
	dst.processStage = DREDProcessStageProcessed
	return nil
}

// DecodeDRED conceals frameSizeSamples per channel into interleaved float32
// pcm using a processed DRED payload and the receiver's decoder history.
// dredOffsetSamples and frameSizeSamples use samples at the Decoder API rate;
// the frame size must be a positive multiple of 2.5 ms, and
// pcm must hold frameSizeSamples*Channels elements. It returns samples per
// channel; it reports ErrOptionalExtensionUnavailable when the receiver's
// DRED neural runtime is not ready.
func (d *Decoder) DecodeDRED(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	return d.decodeExplicitDREDFloat(dred, dredOffsetSamples, pcm, frameSizeSamples)
}

// DecodeDREDInt24 conceals frameSizeSamples per channel into interleaved
// 24-bit-scale PCM stored in int32, using a processed DRED payload and the
// receiver's decoder history. Values are right-justified; the nominal signed
// 24-bit range is [-8388608, 8388607], but libopus RES2INT24
// (src/celt/arch.h) does not soft-clip or clamp to that range; a +1.0 sample
// maps to 8388608. dredOffsetSamples and frameSizeSamples use samples at the
// Decoder API rate; the frame size must be a positive multiple of 2.5 ms, and pcm
// must hold frameSizeSamples*Channels elements. It returns samples per channel.
func (d *Decoder) DecodeDREDInt24(dred *DRED, dredOffsetSamples int, pcm []int32, frameSizeSamples int) (int, error) {
	if frameSizeSamples <= 0 {
		return 0, ErrInvalidArgument
	}
	channels := int(d.channels)
	needed := frameSizeSamples * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	d.ensureScratchPCM(needed)
	n, err := d.decodeExplicitDREDFloat(dred, dredOffsetSamples, d.scratchPCM[:needed], frameSizeSamples)
	if err != nil {
		return 0, err
	}
	float32ToInt24Slice(pcm, d.scratchPCM, n, channels)
	return n, nil
}
