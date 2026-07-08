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

// DREDRequest mirrors the low-cost opus_dred_parse() request parameters that
// affect how much cached redundancy is usable.
type DREDRequest = internaldred.Request

// DREDAvailability summarizes the request-bounded DRED coverage available from
// a parsed payload.
type DREDAvailability = internaldred.Availability

// DREDFeatureWindow mirrors the feature-offset window libopus derives from a
// parsed DRED result and a concealment request.
type DREDFeatureWindow = internaldred.FeatureWindow

// DREDParsed is the retained low-cost DRED metadata parsed from an Opus packet.
type DREDParsed = internaldred.Parsed

// DREDResult bundles parsed DRED metadata with request-bounded availability.
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

// DREDDecoder mirrors libopus's standalone OpusDREDDecoder control lifetime for
// the tag-gated DRED parse/process surface.
type DREDDecoder struct {
	dnnBlob     *dnnblob.Blob
	model       *rdovae.Decoder
	modelLoaded bool
	processor   rdovae.Processor
}

// NewDREDDecoder constructs a tag-gated standalone DRED decoder wrapper.
func NewDREDDecoder() *DREDDecoder {
	return &DREDDecoder{}
}

// SetDNNBlob loads and validates a standalone DRED decoder model blob, matching
// the libopus OpusDREDDecoder DNN-blob control lifetime.
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

// Clear resets the retained DRED payload and process stage.
func (d *DRED) Clear() {
	if d == nil {
		return
	}
	d.cache.Clear()
	d.decoded.Clear()
	d.processStage = 0
}

// Empty reports whether any DRED payload is currently retained.
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

// FillState copies the retained DRED decoder state into dst and returns the
// number of floats written.
func (d *DRED) FillState(dst []float32) int {
	if d == nil || d.Empty() {
		return 0
	}
	return d.decoded.FillState(dst)
}

// FillLatents copies the retained request-bounded DRED latent vectors into dst
// and returns the number of floats written.
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

// FillFeatures copies the retained processed DRED feature frames into dst and
// returns the number of floats written.
func (d *DRED) FillFeatures(dst []float32) int {
	if d == nil || d.processStage != DREDProcessStageProcessed {
		return 0
	}
	return d.decoded.FillFeatures(dst)
}

// Result evaluates the retained DRED payload against an opus_dred_parse()-style
// request.
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

// FeatureWindow reports the retained DRED feature-offset window for a given
// concealment request.
func (d *DRED) FeatureWindow(maxDredSamples, sampleRate, decodeOffsetSamples, frameSizeSamples, initFrames int) DREDFeatureWindow {
	result := d.Result(maxDredSamples, sampleRate)
	if d != nil && d.processStage == DREDProcessStageProcessed {
		return internaldred.ProcessedFeatureWindow(result, &d.decoded, decodeOffsetSamples, frameSizeSamples, initFrames)
	}
	return result.FeatureWindow(decodeOffsetSamples, frameSizeSamples, initFrames)
}

// Parse finds and retains the temporary DRED packet extension from packet and
// returns the request-bounded available and trailing-silence sample counts.
//
// This tag-gated wrapper covers the supported standalone parse, deferred
// process, and feature-retention surface. It does not claim broad DRED
// audio-path parity beyond the green seams documented for the current release.
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

// Process finalizes a deferred standalone DRED state by running the pure-Go
// RDOVAE decoder and retaining the derived DRED feature frames.
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

// DecodeDRED decodes a processed standalone DRED payload into pcm using the
// receiver Decoder state as the concealment context.
func (d *Decoder) DecodeDRED(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	return d.decodeExplicitDREDFloat(dred, dredOffsetSamples, pcm, frameSizeSamples)
}

// DecodeDREDInt24 decodes a processed standalone DRED payload into 24-bit PCM
// samples stored in int32, using the receiver Decoder state as the concealment
// context. Each element carries a signed 24-bit value in the range
// [-8388608, 8388607] (= ±2^23), right-justified in int32 — matching libopus
// opus_decoder_dred_decode24() (src/opus_decoder.c): the float DRED decode path
// followed by RES2INT24 (arch.h) on each sample.
//
// pcm must hold at least frameSizeSamples*channels elements.
// Returns the number of samples per channel decoded, or an error.
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
