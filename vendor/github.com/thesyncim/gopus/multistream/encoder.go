// Multistream encoder implementation for Opus surround sound.
// This file contains the Encoder struct and NewEncoder function for encoding
// multi-channel audio into multistream Opus packets.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1

package multistream

import (
	"errors"
	"fmt"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/types"
)

// ErrInvalidInput indicates an unsupported frame size or malformed PCM shape.
var ErrInvalidInput = errors.New("multistream: invalid input length")

// ErrInvalidForceChannels indicates that a forced channel count is invalid for
// the stream where the control is applied.
var ErrInvalidForceChannels = errors.New("multistream: invalid force channels")

// ErrInvalidLayout indicates the channel mapping has an invalid layout.
// For coupled streams, both left and right channels must be mapped.
var ErrInvalidLayout = errors.New("multistream: invalid layout - coupled stream missing left or right channel")

// ErrInvalidLSBDepth indicates the LSB depth is outside the valid range (8-24).
var ErrInvalidLSBDepth = errors.New("multistream: invalid LSB depth (must be 8-24)")

// Encoder encodes interleaved PCM as an Opus packet containing multiple streams.
// It routes input channels through its mapping table and is not safe for
// concurrent use.
type Encoder struct {
	// sampleRate is the input sample rate (8000, 12000, 16000, 24000, or 48000 Hz;
	// 96000 Hz is available in gopus_qext builds).
	sampleRate int32

	// inputChannels is the total number of input channels (1-255).
	inputChannels int

	// streams is the total number of elementary streams (N).
	streams int

	// coupledStreams is the number of coupled (stereo) streams (M).
	// The first M encoders produce stereo output, the remaining N-M produce mono.
	coupledStreams int

	// mapping is the channel mapping table.
	// mapping[i] indicates which stream channel receives input channel i.
	// Values 0 to 2*M-1 are for coupled streams (even=left, odd=right).
	// Values 2*M to N+M-1 are for uncoupled streams.
	// Value 255 indicates a silent input channel (ignored).
	mapping []byte

	// encoders contains one encoder per stream.
	// First M encoders are stereo (for coupled streams).
	// Remaining N-M encoders are mono (for uncoupled streams).
	encoders []*encoder.Encoder

	// dnnBlob retains a validated USE_WEIGHTS_FILE blob and is propagated to all
	// stream encoders when present.
	dnnBlob *dnnblob.Blob

	// bitrate is the total bitrate in bits per second, distributed across streams.
	bitrate int

	// mappingFamily indicates the channel mapping family used:
	//   0: RTP mapping (mono or stereo only)
	//   1: Vorbis-style mapping (1-8 channels)
	//   2: Ambisonics ACN/SN3D (mostly mono streams)
	//   3: Ambisonics with projection (paired stereo streams)
	//   255: Discrete channels (no predefined mapping)
	mappingFamily int

	// lfeStream is the stream index that carries LFE, or -1 when absent.
	lfeStream int

	// restrictedSilk mirrors st->application == OPUS_APPLICATION_RESTRICTED_SILK,
	// which skips surround_analysis() and the energy masks.
	restrictedSilk bool

	// Optional projection-family mixing matrix coefficients (column-major S16).
	projectionMixing []int16
	projectionCols   int
	projectionRows   int
	projectionShortResFields

	// projectionDemixingGain stores the gain field from the internal demixing matrix,
	// matching OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN.
	// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
	projectionDemixingGain int

	// streamBitrates stores per-stream rates computed by allocation policy.
	streamBitrates []int

	// surroundAnalysis owns per-channel history and typed scratch matching the
	// active libopus float or FIXED_POINT build.
	surroundAnalysis surroundAnalysisState

	// Per-call encode scratch reused across Encode calls so the steady-state
	// encode path is allocation-free.
	streamInputScratch   [][]float32 // routed per-stream input buffers
	analysisInputScratch [][]float32 // routed per-stream analysis buffers (distinct length)
	int16Scratch         []float32   // opus_res view of 16-bit analysis input
	int16CodedScratch    []float32   // opus_res view of 16-bit coded input when it is not the analysis prefix

	// packetParser holds reusable parse/build working buffers for the
	// self-delimited reframing of the first N-1 stream packets.
	packetParser packetScratch
}

const surroundBands = 21

func mappingEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func inferMappingFamily(channels, streams, coupledStreams int, mapping []byte) int {
	if channels >= 1 && channels <= 8 {
		ds, dc, dm, err := DefaultMapping(channels)
		if err == nil && ds == streams && dc == coupledStreams && mappingEqual(dm, mapping) {
			return 1
		}
	}

	if channels > 0 {
		ds, dc, err := ValidateAmbisonics(channels)
		if err == nil && ds == streams && dc == coupledStreams {
			if dm, derr := AmbisonicsMapping(channels); derr == nil && mappingEqual(dm, mapping) {
				return 2
			}
		}

		ds, dc, err = ValidateAmbisonicsFamily3(channels)
		if err == nil && ds == streams && dc == coupledStreams {
			if dm, derr := AmbisonicsMappingFamily3(channels); derr == nil && mappingEqual(dm, mapping) {
				return 3
			}
		}
	}

	if streams == channels && coupledStreams == 0 {
		discrete := true
		for i := range channels {
			if mapping[i] != byte(i) {
				discrete = false
				break
			}
		}
		if discrete {
			return 255
		}
	}

	return 0
}

func inferLFEStream(mappingFamily, channels, streams int) int {
	if mappingFamily == 1 && channels >= 6 {
		return streams - 1
	}
	return -1
}

// NewEncoder returns a multistream encoder for channels of interleaved PCM.
// sampleRate must be 8, 12, 16, 24, or 48 kHz; 96 kHz is available with
// gopus_qext. channels and streams must be in 1..255, coupledStreams in
// 0..streams, and streams+coupledStreams at most 255. mapping has one entry per
// input channel: 0..2*coupledStreams-1 selects a coupled stream channel,
// 2*coupledStreams..streams+coupledStreams-1 selects a mono stream, and 255
// leaves that input channel unused. Each coupled stream must map both channels,
// and each mono stream must be mapped at least once. The mapping is copied. The
// initial total bitrate is 256000 bits per second.
func NewEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*Encoder, error) {
	// Validation exactly mirrors decoder
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
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

	// Validate layout: ensure every encoder stream has at least one input.
	if err := validateEncoderLayout(mapping, streams, coupledStreams); err != nil {
		return nil, err
	}

	// Create stream encoders
	// First M encoders are stereo (coupled), remaining N-M are mono
	encoders := make([]*encoder.Encoder, streams)
	for i := range streams {
		var chans int
		if i < coupledStreams {
			chans = 2 // Coupled stream = stereo
		} else {
			chans = 1 // Uncoupled stream = mono
		}
		streamEnc := encoder.NewEncoder(sampleRate, chans)
		// Match libopus multistream defaults: VBR enabled with constraint on.
		streamEnc.SetVBRConstraint(true)
		encoders[i] = streamEnc
	}

	// Copy mapping to avoid external mutation
	mappingCopy := make([]byte, len(mapping))
	copy(mappingCopy, mapping)

	mappingFamily := inferMappingFamily(channels, streams, coupledStreams, mappingCopy)
	lfeStream := inferLFEStream(mappingFamily, channels, streams)

	enc := &Encoder{
		sampleRate:       int32(sampleRate),
		inputChannels:    channels,
		streams:          streams,
		coupledStreams:   coupledStreams,
		mapping:          mappingCopy,
		encoders:         encoders,
		bitrate:          256000, // Default 256 kbps total
		mappingFamily:    mappingFamily,
		lfeStream:        lfeStream,
		streamBitrates:   make([]int, streams),
		surroundAnalysis: newSurroundAnalysisState(channels, streams),
	}
	enc.applyLFEFlags()
	return enc, nil
}

// NewEncoderDefault returns an encoder with the Vorbis mapping for 1–8 input
// channels. It returns an error for an unsupported sample rate or channel count.
func NewEncoderDefault(sampleRate, channels int) (*Encoder, error) {
	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		return nil, err
	}
	enc, err := NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}
	enc.mappingFamily = 1 // Vorbis-style mapping
	enc.lfeStream = inferLFEStream(enc.mappingFamily, channels, streams)
	enc.applyLFEFlags()
	return enc, nil
}

// NewEncoderAmbisonics returns a multistream encoder with ACN/SN3D mapping.
// Family 2 accepts valid ambisonics counts through 227 channels; family 3
// projection supports 4, 6, 9, 11, 16, 18, 25, 27, 36, or 38 channels. Other
// families return ErrInvalidMappingFamily, and unsupported family 3 orders
// return ErrProjectionOrderUnsupported.
func NewEncoderAmbisonics(sampleRate, channels, mappingFamily int) (*Encoder, error) {
	var streams, coupledStreams int
	var mapping []byte
	var err error

	switch mappingFamily {
	case 2:
		streams, coupledStreams, err = ValidateAmbisonics(channels)
		if err != nil {
			return nil, err
		}
		mapping, err = AmbisonicsMapping(channels)
		if err != nil {
			return nil, err
		}
	case 3:
		streams, coupledStreams, err = ValidateAmbisonicsFamily3(channels)
		if err != nil {
			return nil, err
		}
		mapping, err = AmbisonicsMappingFamily3(channels)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidMappingFamily
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}
	enc.mappingFamily = mappingFamily
	enc.lfeStream = -1
	enc.applyLFEFlags()
	if mappingFamily == 3 {
		if err := enc.initProjectionMixingDefaults(); err != nil {
			return nil, err
		}
	}
	return enc, nil
}

// Reset clears codec and surround-analysis history for a new stream. It keeps
// the encoder's sample rate, channel mapping, and projection matrix.
func (e *Encoder) Reset() {
	for i, enc := range e.encoders {
		enc.Reset()
		enc.SetLFE(i == e.lfeStream)
	}
	e.surroundAnalysis.reset()
}

func (e *Encoder) applyLFEFlags() {
	for i, enc := range e.encoders {
		enc.SetLFE(i == e.lfeStream)
	}
}

// Channels returns the total number of input channels.
func (e *Encoder) Channels() int {
	return e.inputChannels
}

// SampleRate returns the input sample rate in Hz.
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// Streams returns the total number of elementary streams.
func (e *Encoder) Streams() int {
	return e.streams
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (e *Encoder) CoupledStreams() int {
	return e.coupledStreams
}

// MappingFamily returns the channel mapping family used by this encoder.
//
// Mapping families:
//   - 0: RTP mapping (mono or stereo only)
//   - 1: Vorbis-style mapping (1-8 channels)
//   - 2: Ambisonics ACN/SN3D (mostly mono streams)
//   - 3: Ambisonics with projection (paired stereo streams)
//   - 255: Discrete channels (no predefined mapping)
func (e *Encoder) MappingFamily() int {
	return e.mappingFamily
}

// SetBitrate sets the total bitrate in bits per second.
// The bitrate is distributed across streams with coupled streams getting
// proportionally more bits than mono streams.
//
// Distribution formula:
//   - Coupled streams: 3 units (e.g., 96 kbps at typical settings)
//   - Mono streams: 2 units (e.g., 64 kbps at typical settings)
func (e *Encoder) SetBitrate(totalBitrate int) {
	e.bitrate = clampTotalBitrate(totalBitrate, e.inputChannels)
	rates := e.allocateRates(960)
	for i := 0; i < e.streams && i < len(rates); i++ {
		e.encoders[i].SetAllocatedBitrate(rates[i])
	}
}

// Bitrate returns the configured aggregate target, retaining the automatic
// and maximum bitrate sentinels. Libopus OPUS_GET_BITRATE sums the current
// per-stream targets instead.
func (e *Encoder) Bitrate() int {
	return e.bitrate
}

func clampTotalBitrate(bitrate, channels int) int {
	if bitrate == encoder.BitrateAuto || bitrate == encoder.BitrateMax {
		return bitrate
	}
	if channels < 1 {
		channels = 1
	}
	minBitrate := encoder.MinBitrate * channels
	if bitrate < minBitrate {
		return minBitrate
	}
	maxBitrate := encoder.MaxBitrate * channels
	if bitrate > maxBitrate {
		return maxBitrate
	}
	return bitrate
}

// SetMode sets the base mode for all stream encoders.
func (e *Encoder) SetMode(mode encoder.Mode) {
	for _, enc := range e.encoders {
		enc.SetMode(mode)
	}
}

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob and propagates it to all
// child stream encoders. A nil blob clears the retained model.
func (e *Encoder) SetDNNBlob(blob *dnnblob.Blob) {
	e.dnnBlob = blob
	for _, enc := range e.encoders {
		enc.SetDNNBlob(blob)
	}
}

// DNNBlobLoaded reports whether a validated model blob is retained.
func (e *Encoder) DNNBlobLoaded() bool {
	return e.dnnBlob != nil
}

// Mode returns the base mode from the first stream encoder.
func (e *Encoder) Mode() encoder.Mode {
	if len(e.encoders) > 0 {
		return e.encoders[0].Mode()
	}
	return encoder.ModeAuto
}

// SetLowDelay toggles low-delay application behavior for all stream encoders.
func (e *Encoder) SetLowDelay(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetLowDelay(enabled)
	}
}

// LowDelay reports low-delay application behavior from the first stream encoder.
func (e *Encoder) LowDelay() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].LowDelay()
	}
	return false
}

// SetVoIPApplication toggles VoIP application bias for all stream encoders.
func (e *Encoder) SetVoIPApplication(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetVoIPApplication(enabled)
	}
}

// VoIPApplication reports VoIP application bias from the first stream encoder.
func (e *Encoder) VoIPApplication() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].VoIPApplication()
	}
	return false
}

// SetRestrictedSilkApplication toggles restricted-SILK application behavior.
func (e *Encoder) SetRestrictedSilkApplication(enabled bool) {
	e.restrictedSilk = enabled
	for _, enc := range e.encoders {
		enc.SetRestrictedSilkApplication(enabled)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var surroundLogSumDiffTable = [...]float32{
	0.5000000, 0.2924813, 0.1609640, 0.0849625,
	0.0437314, 0.0221971, 0.0111839, 0.0056136,
	0.0028123, 0, 0, 0, 0, 0, 0, 0, 0,
}

func logSum32(a, b float32) float32 {
	maxVal := b
	diff := b - a
	if a > b {
		maxVal = a
		diff = a - b
	}
	if !(diff < 8.0) {
		return maxVal
	}
	low := int(2.0 * diff)
	frac := 2.0*diff - float32(low)
	return maxVal + surroundLogSumDiffTable[low] + frac*(surroundLogSumDiffTable[low+1]-surroundLogSumDiffTable[low])
}

func (e *Encoder) isSurroundMapping() bool {
	return e.mappingFamily == 1 && e.inputChannels > 2
}

// isAmbisonicsMapping reports the MAPPING_TYPE_AMBISONICS behaviour: forced
// CELT-only per-stream encoders and the ambisonics_rate_allocation() bit split.
//
// Only mapping family 2 takes this path. Family 3 (projection) initializes its
// internal multistream encoder with MAPPING_TYPE_NONE
// (opus_projection_encoder.c -> opus_multistream_encoder_init), so it uses the
// generic surround_rate_allocation() and lets each per-stream encoder pick its
// own mode; the projection mixing matrix carries the spatial image instead.
func (e *Encoder) isAmbisonicsMapping() bool {
	return e.mappingFamily == 2
}

func (e *Encoder) bitrateForAllocation(frameSize int) int {
	if e.bitrate > 0 {
		return e.bitrate
	}
	if frameSize <= 0 {
		frameSize = 960
	}
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	nbLFE := 0
	if e.lfeStream >= 0 {
		nbLFE = 1
	}
	nbUncoupled := max(e.streams-e.coupledStreams-nbLFE, 0)
	nbNormal := 2*e.coupledStreams + nbUncoupled
	if e.bitrate == encoder.BitrateMax {
		return nbNormal*750000 + nbLFE*128000
	}
	channelOffset := 40 * maxInt(50, fs/frameSize)
	return nbNormal*(channelOffset+fs+10000) + 8000*nbLFE
}

func (e *Encoder) ambisonicsBitrateForAllocation(frameSize int) int {
	if e.bitrate > 0 {
		return e.bitrate
	}
	if frameSize <= 0 {
		frameSize = 960
	}
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	nbChannels := e.streams + e.coupledStreams
	if e.bitrate == encoder.BitrateMax {
		return nbChannels * 750000
	}
	return (e.coupledStreams+e.streams)*(fs+60*fs/frameSize) + e.streams*15000
}

func (e *Encoder) totalBitrateForAllocation(frameSize int) int {
	if e.isAmbisonicsMapping() {
		return e.ambisonicsBitrateForAllocation(frameSize)
	}
	return e.bitrateForAllocation(frameSize)
}

func (e *Encoder) allocateSurroundRates(rates []int, frameSize int) {
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	if frameSize <= 0 {
		frameSize = 960
	}

	nbLFE := 0
	if e.lfeStream >= 0 {
		nbLFE = 1
	}
	nbCoupled := e.coupledStreams
	nbUncoupled := max(e.streams-nbCoupled-nbLFE, 0)
	nbNormal := 2*nbCoupled + nbUncoupled

	bitrate := e.bitrateForAllocation(frameSize)
	if nbNormal <= 0 {
		per := bitrate / maxInt(1, e.streams)
		per = maxInt(per, 500)
		for i := 0; i < e.streams; i++ {
			rates[i] = per
		}
		return
	}

	channelOffset := 40 * maxInt(50, fs/frameSize)
	lfeOffset := minInt(bitrate/20, 3000) + 15*maxInt(50, fs/frameSize)
	if nbLFE == 0 {
		lfeOffset = 0
	}

	streamOffset := (bitrate - channelOffset*nbNormal - lfeOffset*nbLFE) / nbNormal / 2
	streamOffset = maxInt(0, minInt(20000, streamOffset))

	const coupledRatio = 512
	const lfeRatio = 32
	total := (nbUncoupled << 8) + coupledRatio*nbCoupled + nbLFE*lfeRatio
	if total <= 0 {
		total = 1
	}
	numerator := bitrate - lfeOffset*nbLFE - streamOffset*(nbCoupled+nbUncoupled) - channelOffset*nbNormal
	channelRate := (256 * numerator) / total

	for i := 0; i < e.streams; i++ {
		var rate int
		if i < e.coupledStreams {
			rate = 2*channelOffset + maxInt(0, streamOffset+((channelRate*coupledRatio)>>8))
		} else if i != e.lfeStream {
			rate = channelOffset + maxInt(0, streamOffset+channelRate)
		} else {
			rate = maxInt(0, lfeOffset+((channelRate*lfeRatio)>>8))
		}
		rates[i] = maxInt(rate, 500)
	}
}

// bitsToBitrate mirrors libopus celt.h bits_to_bitrate(): the bitrate implied
// by a per-frame bit budget at the configured sample rate.
func bitsToBitrate(bits, fs, frameSize int) int {
	if frameSize <= 0 || fs <= 0 {
		return 0
	}
	return bits * (6 * fs / frameSize) / 6
}

// bitrateToBits mirrors libopus celt.h bitrate_to_bits(): the number of bits a
// given bitrate yields for one frame at the configured sample rate.
func bitrateToBits(bitrate, fs, frameSize int) int {
	if frameSize <= 0 || fs <= 0 {
		return 0
	}
	return bitrate * 6 / (6 * fs / frameSize)
}

// allocatedRateSum returns the sum of the per-stream allocation, matching the
// rate_sum returned by libopus rate_allocation() (each rate floored at 500).
func (e *Encoder) allocatedRateSum(frameSize int) int {
	rates := e.allocateRates(frameSize)
	sum := 0
	for i := 0; i < e.streams && i < len(rates); i++ {
		sum += rates[i]
	}
	return sum
}

func (e *Encoder) allocateRates(frameSize int) []int {
	if frameSize <= 0 {
		frameSize = 960
	}
	if cap(e.streamBitrates) < e.streams {
		e.streamBitrates = make([]int, e.streams)
	}
	rates := e.streamBitrates[:e.streams]

	switch {
	case e.isAmbisonicsMapping():
		totalRate := e.ambisonicsBitrateForAllocation(frameSize)
		per := totalRate / maxInt(1, e.streams)
		per = maxInt(per, 500)
		for i := 0; i < e.streams; i++ {
			rates[i] = per
		}
	default:
		e.allocateSurroundRates(rates, frameSize)
	}

	return rates
}

// surroundBandwidth is the per-stream OPUS_SET_BANDWIDTH of a surround
// layout. libopus derives it from st->bitrate_bps itself, so OPUS_AUTO and
// OPUS_BITRATE_MAX (negative) select narrowband
// (src/opus_multistream_encoder.c:941-957).
func (e *Encoder) surroundBandwidth(frameSize int) types.Bandwidth {
	fs := int(e.sampleRate)
	equivRate := e.bitrate
	if frameSize > 0 && frameSize*50 < fs {
		equivRate -= 60 * (fs/frameSize - 50) * e.inputChannels
	}
	if equivRate > 10000*e.inputChannels {
		return types.BandwidthFullband
	}
	if equivRate > 7000*e.inputChannels {
		return types.BandwidthSuperwideband
	}
	if equivRate > 5000*e.inputChannels {
		return types.BandwidthWideband
	}
	return types.BandwidthNarrowband
}

func channelPositions(channels int, pos []int) bool {
	if len(pos) < channels {
		return false
	}
	for i := range channels {
		pos[i] = 0
	}
	switch channels {
	case 4:
		pos[0], pos[1], pos[2], pos[3] = 1, 3, 1, 3
	case 3, 5, 6:
		pos[0], pos[1], pos[2], pos[3], pos[4] = 1, 2, 3, 1, 3
		if channels == 6 {
			pos[5] = 0
		}
	case 7:
		pos[0], pos[1], pos[2], pos[3], pos[4], pos[5], pos[6] = 1, 2, 3, 1, 3, 2, 0
	case 8:
		pos[0], pos[1], pos[2], pos[3], pos[4], pos[5], pos[6], pos[7] = 1, 2, 3, 1, 3, 1, 3, 0
	default:
		return false
	}
	return true
}

func resamplingFactor(rate int) int {
	switch rate {
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
	default:
		return 0
	}
}

func surroundAnalysisFreqSize(frameSize int) (int, bool) {
	switch frameSize {
	case 120, 240, 480, 960:
		return frameSize, true
	default:
		if frameSize > 0 && frameSize%960 == 0 {
			return 960, true
		}
		return 0, false
	}
}

// applyPerStreamPolicy runs surround_analysis() and the per-stream
// OPUS_SET_* controls of opus_multistream_encode_native()
// (src/opus_multistream_encoder.c:897-962 and 976-1014).
func (e *Encoder) applyPerStreamPolicy(frameSize int, input encodeInput) {
	rates := e.allocateRates(frameSize)
	surround := e.isSurroundMapping()
	e.surroundAnalysis.applyEnergyMasks(e, frameSize, input)
	surroundBandwidth := e.surroundBandwidth(frameSize)
	for i := 0; i < e.streams; i++ {
		enc := e.encoders[i]
		enc.SetAllocatedBitrate(rates[i])

		switch {
		case surround:
			enc.SetBandwidth(surroundBandwidth)
			if i < e.coupledStreams {
				// To preserve the spatial image, force stereo CELT on coupled
				// streams.
				enc.SetMode(encoder.ModeCELT)
				enc.SetForceChannels(2)
			}
		case e.isAmbisonicsMapping():
			enc.SetMode(encoder.ModeCELT)
		}
	}
}

func (e *Encoder) initProjectionMixingDefaults() error {
	matrix, ok := defaultProjectionMixingMatrix(e.inputChannels, e.streams, e.coupledStreams)
	if !ok {
		return fmt.Errorf("multistream: missing projection mixing defaults for channels=%d streams=%d coupled=%d",
			e.inputChannels, e.streams, e.coupledStreams)
	}

	needed := len(matrix)
	if cap(e.projectionMixing) < needed {
		e.projectionMixing = make([]int16, needed)
	}
	coeffs := e.projectionMixing[:needed]
	copy(coeffs, matrix)

	e.projectionRows = e.inputChannels
	e.projectionCols = e.inputChannels

	// Retain the demixing gain for the matching order.
	// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
	// OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN_REQUEST
	if def, defOK := projectionDemixingDefaults[e.inputChannels]; defOK {
		e.projectionDemixingGain = def.gain
	}
	return nil
}

// GetDemixingMatrix returns a copy of this projection encoder's demixing matrix
// as S16LE bytes in column-major order. Each column contains one input stream
// channel's coefficients for all output channels. It returns nil when this is
// not a family 3 encoder or no matrix is available.
func (e *Encoder) GetDemixingMatrix() []byte {
	if e.mappingFamily != 3 {
		return nil
	}
	b, ok := defaultProjectionDemixingMatrixBytes(e.inputChannels, e.streams, e.coupledStreams)
	if !ok {
		return nil
	}
	return b
}

// DemixingMatrixGain returns the gain field of the internal demixing matrix,
// matching OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN_REQUEST.
//
// Returns 0 if this encoder is not mapping family 3.
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN_REQUEST
func (e *Encoder) DemixingMatrixGain() int {
	if e.mappingFamily != 3 {
		return 0
	}
	return e.projectionDemixingGain
}

// DemixingMatrixSize returns the serialized demixing matrix size in bytes, or
// zero when this is not a family 3 encoder.
func (e *Encoder) DemixingMatrixSize() int {
	if e.mappingFamily != 3 {
		return 0
	}
	return ProjectionDemixingMatrixSize(e.inputChannels, e.streams, e.coupledStreams)
}

// Encode encodes frameSize samples per channel of interleaved float32 PCM into
// out and returns the packet length. len(out) is the maximum packet size in
// bytes. pcm must contain exactly frameSize*Channels() samples. A too-small
// output buffer returns ErrBufferTooSmall.
func (e *Encoder) Encode(pcm []float32, frameSize int, out []byte) (int, error) {
	return e.EncodeWithAnalysis(pcm, frameSize, pcm, out)
}

// EncodeWithAnalysis encodes frameSize samples per channel from pcm while
// analysisPCM supplies the samples used for tonality analysis. pcm must contain
// exactly frameSize*Channels() samples; analysisPCM must contain at least that
// many whole interleaved samples.
func (e *Encoder) EncodeWithAnalysis(pcm []float32, frameSize int, analysisPCM []float32, out []byte) (int, error) {
	return e.encodeNative(encodeInput{f32: pcm}, frameSize, analysisPCM, out)
}

// EncodeInt16 encodes frameSize samples per channel of interleaved int16 PCM
// into out and returns the packet length. pcm must contain exactly
// frameSize*Channels() samples; len(out) is the maximum packet size in bytes.
func (e *Encoder) EncodeInt16(pcm []int16, frameSize int, out []byte) (int, error) {
	return e.EncodeInt16WithAnalysis(pcm, frameSize, pcm, out)
}

// EncodeInt16WithAnalysis encodes pcm while analysisPCM supplies the interleaved
// samples used for tonality analysis. pcm must contain exactly
// frameSize*Channels() samples, and analysisPCM must contain at least that many
// whole samples.
func (e *Encoder) EncodeInt16WithAnalysis(pcm []int16, frameSize int, analysisPCM []int16, out []byte) (int, error) {
	if err := e.validateNativeFrameSize(frameSize); err != nil {
		return 0, err
	}
	if len(analysisPCM) < len(pcm) {
		return 0, fmt.Errorf("%w: got %d analysis samples for %d samples", ErrInvalidInput, len(analysisPCM), len(pcm))
	}
	// INT16TORES is exact in the float build, so the converted analysis frame
	// also carries the coded samples; downmix_int() reads the same values.
	if cap(e.int16Scratch) < len(analysisPCM) {
		e.int16Scratch = make([]float32, len(analysisPCM))
	}
	analysis := e.int16Scratch[:len(analysisPCM)]
	for i, v := range analysisPCM {
		analysis[i] = float32(v) * (1.0 / 32768)
	}
	coded := analysis
	if !sameInt16Prefix(pcm, analysisPCM) {
		if cap(e.int16CodedScratch) < len(pcm) {
			e.int16CodedScratch = make([]float32, len(pcm))
		}
		coded = e.int16CodedScratch[:len(pcm)]
		for i, v := range pcm {
			coded[i] = float32(v) * (1.0 / 32768)
		}
	}
	return e.encodeNative(encodeInput{f32: coded[:len(pcm)], i16: pcm}, frameSize, analysis, out)
}

// sameInt16Prefix reports whether pcm is the leading part of analysisPCM.
func sameInt16Prefix(pcm, analysisPCM []int16) bool {
	return len(pcm) == 0 || (len(analysisPCM) >= len(pcm) && &pcm[0] == &analysisPCM[0])
}

// encodeInput is the caller frame handed to opus_multistream_encode_native():
// f32 always holds the opus_res samples, and i16 holds the original 16-bit
// samples when the caller used the int16 entry point, for the projection
// encoder's integer mixing (mapping_matrix_multiply_channel_in_short).
type encodeInput struct {
	f32 []float32
	i16 []int16
}

// encodeNative ports opus_multistream_encode_native()
// (src/opus_multistream_encoder.c:846-1053).
func (e *Encoder) encodeNative(in encodeInput, frameSize int, analysisPCM []float32, out []byte) (int, error) {
	if err := e.validateNativeFrameSize(frameSize); err != nil {
		return 0, err
	}
	expectedLen, ok := checkedInterleavedSampleCount(frameSize, e.inputChannels)
	if !ok || len(in.f32) != expectedLen {
		return 0, fmt.Errorf("%w: got %d samples, expected %d (frameSize=%d, channels=%d)",
			ErrInvalidInput, len(in.f32), expectedLen, frameSize, e.inputChannels)
	}
	if analysisPCM == nil {
		analysisPCM = in.f32
	}
	if len(analysisPCM) < expectedLen || len(analysisPCM)%e.inputChannels != 0 {
		return 0, fmt.Errorf("%w: got %d analysis samples for frameSize=%d channels=%d",
			ErrInvalidInput, len(analysisPCM), frameSize, e.inputChannels)
	}
	maxDataBytes := len(out)
	// libopus rejects an undersized caller buffer before surround analysis or
	// per-stream rate changes, so a rejected call leaves encoder state intact.
	// The 100 ms framing carries one extra ToC byte per stream.
	fs := int(e.sampleRate)
	smallestPacket := e.streams*2 - 1
	if fs/frameSize == 10 {
		smallestPacket += e.streams
	}
	if maxDataBytes < smallestPacket {
		return 0, ErrBufferTooSmall
	}
	if in.i16 != nil {
		for _, enc := range e.encoders {
			enc.ReserveShortEncodeScratch(frameSize, maxDataBytes)
		}
	}

	// Surround analysis and the per-stream OPUS_SET_* controls
	// (lines 897-962).
	e.applyPerStreamPolicy(frameSize, in)

	// For CBR, libopus shrinks the total caller budget to the bitrate-implied
	// packet size before deriving each stream's curr_max (lines 918-928).
	// rate_sum is the sum of the per-stream allocation that feeds the OPUS_AUTO
	// branch.
	vbr := e.VBR()
	if !vbr {
		switch e.bitrate {
		case encoder.BitrateAuto:
			rateSum := e.allocatedRateSum(frameSize)
			maxDataBytes = minInt(maxDataBytes, (bitrateToBits(rateSum, fs, frameSize)+4)/8)
		case encoder.BitrateMax:
			// No shrinking: keep the full caller budget.
		default:
			maxDataBytes = minInt(maxDataBytes, maxInt(smallestPacket, (bitrateToBits(e.bitrate, fs, frameSize)+4)/8))
		}
	}

	// copy_channel_in: each stream reads its left/right (or mono) input
	// channel, through the mixing matrix for the projection encoder.
	streamBuffers := e.routeInputToStreams(e.streamInputScratch, in, frameSize)
	e.streamInputScratch = streamBuffers
	analysisStreamBuffers := streamBuffers
	if e.mappingFamily == 3 || len(analysisPCM) != len(in.f32) || &analysisPCM[0] != &in.f32[0] {
		// opus_encode_native() runs the tonality analysis on the caller PCM
		// through downmix() with the stream's c1/c2 input channels, so the
		// projection encoder's analysis sees the unmixed input.
		analysisFrameSize := len(analysisPCM) / e.inputChannels
		analysisStreamBuffers = e.routeChannels(e.analysisInputScratch, analysisPCM, analysisFrameSize)
		e.analysisInputScratch = analysisStreamBuffers
	}

	// Each stream's max_data_bytes (curr_max) comes from the remaining caller
	// budget (lines 1015-1024):
	//
	//   curr_max = max_data_bytes - tot_size;
	//   curr_max -= IMAX(0,2*(nb_streams-s-1)-1);
	//   if (Fs/frame_size == 10) curr_max -= nb_streams-s-1;
	//   curr_max = IMIN(curr_max, MS_FRAME_TMP);
	//   if (s != nb_streams-1) curr_max -= curr_max>253?2:1;
	//
	// and the repacketizer writes the stream straight into the caller buffer,
	// self-delimited for all but the last stream (lines 1039-1048).
	totSize := 0
	hundredMs := fs/frameSize == 10
	for i := 0; i < e.streams; i++ {
		enc := e.encoders[i]

		currMax := maxDataBytes - totSize
		if r := 2*(e.streams-i-1) - 1; r > 0 {
			currMax -= r
		}
		if hundredMs {
			currMax -= e.streams - i - 1
		}
		currMax = minInt(currMax, msFrameTmp)
		last := i == e.streams-1
		if !last {
			if currMax > 253 {
				currMax -= 2
			} else {
				currMax--
			}
		}
		if !vbr && last {
			enc.SetAllocatedBitrate(bitsToBitrate(currMax*8, fs, frameSize))
		}

		packet, err := e.encodeStream(enc, i, streamBuffers[i], frameSize, analysisStreamBuffers[i], currMax, in.i16 != nil)
		if err != nil {
			return 0, fmt.Errorf("stream %d encode failed: %w", i, err)
		}
		// opus_multistream_encode_native pads the final CBR stream to the
		// remaining packet budget, including TOC-only DTX frames.
		var n int
		if last && !vbr && len(packet) < maxDataBytes-totSize {
			n, err = padStreamPacketInto(&e.packetParser, out[totSize:maxDataBytes], packet)
		} else {
			n, err = writeStreamPacket(&e.packetParser, out[totSize:maxDataBytes], packet, last)
		}
		if err != nil {
			return 0, fmt.Errorf("stream %d framing failed: %w", i, err)
		}
		totSize += n
	}
	return totSize, nil
}

// validateNativeFrameSize matches libopus frame_size_select() with
// OPUS_FRAMESIZE_ARG. Multistream encoding rejects unsupported durations
// before checking the output budget or entering stream analysis.
func (e *Encoder) validateNativeFrameSize(frameSize int) error {
	if !validNativeFrameSize(int(e.sampleRate), frameSize, e.restrictedSilk) {
		return fmt.Errorf("%w: unsupported frame size %d at %d Hz", ErrInvalidInput, frameSize, e.sampleRate)
	}
	return nil
}

// validNativeFrameSize follows src/opus_encoder.c frame_size_select() for the
// multistream encoder's OPUS_FRAMESIZE_ARG setting.
func validNativeFrameSize(sampleRate, frameSize int, restrictedSilk bool) bool {
	if sampleRate <= 0 || frameSize < sampleRate/400 {
		return false
	}
	if restrictedSilk && frameSize < sampleRate/100 {
		return false
	}

	for _, supported := range [...]int{
		sampleRate / 400,
		sampleRate / 200,
		sampleRate / 100,
		sampleRate / 50,
		sampleRate / 25,
		3 * sampleRate / 50,
		4 * sampleRate / 50,
		5 * sampleRate / 50,
		6 * sampleRate / 50,
	} {
		if frameSize == supported {
			return true
		}
	}
	return false
}

// checkedInterleavedSampleCount guards the public frameSize*channels shape
// before the Go API multiplies dimensions that arrive as machine-sized ints.
func checkedInterleavedSampleCount(frameSize, channels int) (int, bool) {
	if frameSize < 0 || channels <= 0 {
		return 0, false
	}
	maxInt := int(^uint(0) >> 1)
	if frameSize > maxInt/channels {
		return 0, false
	}
	return frameSize * channels, true
}

// encodeStream runs one elementary opus_encode_native() call. The 16-bit entry
// points pass lsb_depth 16, which libopus applies as IMIN(16, st->lsb_depth)
// for this call only; the float entry points pass MAX_ENCODING_DEPTH.
func (e *Encoder) encodeStream(enc *encoder.Encoder, stream int, pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int, shortInput bool) ([]byte, error) {
	if shortInput {
		if e.mappingFamily == 3 && len(e.projectionMixing) > 0 {
			return e.encodeProjectionShortStream(enc, stream, pcm, frameSize, analysisPCM, maxDataBytes)
		}
		return enc.EncodeShortMixedWithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxDataBytes)
	}
	return enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxDataBytes)
}

// SetComplexity sets encoder complexity (0-10) for all stream encoders.
// Higher values use more CPU for better quality.
// Default is 10 (maximum quality).
func (e *Encoder) SetComplexity(complexity int) {
	for _, enc := range e.encoders {
		enc.SetComplexity(complexity)
	}
}

// Complexity returns the complexity setting of the first encoder.
// All stream encoders use the same complexity.
func (e *Encoder) Complexity() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].Complexity()
	}
	return 10 // Default
}

// SetBitrateMode sets the bitrate mode for all stream encoders.
func (e *Encoder) SetBitrateMode(mode encoder.BitrateMode) {
	for _, enc := range e.encoders {
		enc.SetBitrateMode(mode)
	}
}

// BitrateMode returns the current bitrate mode (from first stream encoder).
func (e *Encoder) BitrateMode() encoder.BitrateMode {
	if len(e.encoders) > 0 {
		return e.encoders[0].GetBitrateMode()
	}
	return encoder.ModeCVBR
}

// SetVBR enables or disables VBR mode for all stream encoders.
func (e *Encoder) SetVBR(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetVBR(enabled)
	}
}

// VBR reports whether VBR mode is enabled.
func (e *Encoder) VBR() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].VBR()
	}
	return true
}

// SetVBRConstraint enables or disables constrained VBR mode.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	for _, enc := range e.encoders {
		enc.SetVBRConstraint(constrained)
	}
}

// VBRConstraint reports whether constrained VBR is enabled.
func (e *Encoder) VBRConstraint() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].VBRConstraint()
	}
	return true
}

// SetFEC enables or disables in-band Forward Error Correction for all streams.
// When enabled, encoders include LBRR data for loss recovery.
func (e *Encoder) SetFEC(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetFEC(enabled)
	}
}

// SetInBandFEC sets the in-band FEC configuration for all streams.
func (e *Encoder) SetInBandFEC(config int) error {
	for _, enc := range e.encoders {
		if err := enc.SetInBandFEC(config); err != nil {
			return err
		}
	}
	return nil
}

// FECEnabled returns whether FEC is enabled (from first encoder).
func (e *Encoder) FECEnabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].FECEnabled()
	}
	return false
}

// InBandFEC returns the in-band FEC configuration from the first stream.
func (e *Encoder) InBandFEC() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].InBandFEC()
	}
	return 0
}

// SetPacketLoss sets the expected packet loss percentage (0-100) for all streams.
// This affects FEC behavior and bitrate allocation.
func (e *Encoder) SetPacketLoss(lossPercent int) {
	for _, enc := range e.encoders {
		enc.SetPacketLoss(lossPercent)
	}
}

// PacketLoss returns the expected packet loss percentage (from first encoder).
func (e *Encoder) PacketLoss() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].PacketLoss()
	}
	return 0
}

// SetDTX enables or disables Discontinuous Transmission for all streams.
// When enabled, packets are suppressed during silence.
func (e *Encoder) SetDTX(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetDTX(enabled)
	}
}

// DTXEnabled returns whether DTX is enabled (from first encoder).
func (e *Encoder) DTXEnabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].DTXEnabled()
	}
	return false
}

// SetBandwidth sets the target bandwidth for all stream encoders.
func (e *Encoder) SetBandwidth(bw types.Bandwidth) {
	for _, enc := range e.encoders {
		enc.SetBandwidth(bw)
	}
}

// SetBandwidthAuto restores automatic bandwidth selection on all stream encoders.
func (e *Encoder) SetBandwidthAuto() {
	for _, enc := range e.encoders {
		enc.SetBandwidthAuto()
	}
}

// Bandwidth returns the target bandwidth from the first stream encoder.
func (e *Encoder) Bandwidth() types.Bandwidth {
	if len(e.encoders) > 0 {
		return e.encoders[0].Bandwidth()
	}
	return types.BandwidthFullband
}

// SetForceChannels applies the forced channel count to stream encoders in
// order. A mono stream rejects a value of 2 after earlier coupled streams have
// already accepted it, matching opus_multistream_encoder_ctl.
func (e *Encoder) SetForceChannels(channels int) error {
	for i, enc := range e.encoders {
		streamChannels := 1
		if i < e.coupledStreams {
			streamChannels = 2
		}
		if channels != -1 && (channels < 1 || channels > streamChannels) {
			return ErrInvalidForceChannels
		}
		enc.SetForceChannels(channels)
	}
	return nil
}

// ForceChannels returns forced channel count from the first stream encoder.
func (e *Encoder) ForceChannels() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].ForceChannels()
	}
	return -1
}

// SetPredictionDisabled toggles inter-frame prediction for all stream encoders.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	for _, enc := range e.encoders {
		enc.SetPredictionDisabled(disabled)
	}
}

// PredictionDisabled reports whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].PredictionDisabled()
	}
	return false
}

// SetPhaseInversionDisabled toggles stereo phase inversion on all stream encoders.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	for _, enc := range e.encoders {
		enc.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].PhaseInversionDisabled()
	}
	return false
}

// GetFinalRange returns the final range coder state for all streams.
// The values from all streams are XOR combined to produce a single verification value.
// This matches libopus OPUS_GET_FINAL_RANGE for multistream encoders.
// Must be called after Encode() to get a meaningful value.
func (e *Encoder) GetFinalRange() uint32 {
	var combined uint32
	for _, enc := range e.encoders {
		combined ^= enc.FinalRange()
	}
	return combined
}

// Lookahead returns the encoder's algorithmic delay in samples at SampleRate.
// This includes both CELT delay compensation and mode-specific delay.
// For multistream, all stream encoders have the same lookahead.
// Reference: libopus OPUS_GET_LOOKAHEAD
func (e *Encoder) Lookahead() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].Lookahead()
	}
	// Default: 2.5ms base + 130 samples delay compensation
	return int(e.sampleRate)/400 + 130
}

// Signal returns the current signal type hint (from first encoder).
// All stream encoders share the same signal type setting.
func (e *Encoder) Signal() types.Signal {
	if len(e.encoders) > 0 {
		return e.encoders[0].SignalType()
	}
	return types.SignalAuto
}

// SetSignal sets the signal type hint for all stream encoders.
// SignalVoice biases toward SILK mode, SignalMusic toward CELT mode.
func (e *Encoder) SetSignal(signal types.Signal) {
	for _, enc := range e.encoders {
		enc.SetSignalType(signal)
	}
}

// SetMaxBandwidth sets the maximum bandwidth limit for all stream encoders.
// The actual bandwidth will be clamped to this limit.
func (e *Encoder) SetMaxBandwidth(bw types.Bandwidth) {
	for _, enc := range e.encoders {
		enc.SetMaxBandwidth(bw)
	}
}

// MaxBandwidth returns the maximum bandwidth limit (from first encoder).
// All stream encoders share the same max bandwidth setting.
func (e *Encoder) MaxBandwidth() types.Bandwidth {
	if len(e.encoders) > 0 {
		return e.encoders[0].MaxBandwidth()
	}
	return types.BandwidthFullband
}

// SetLSBDepth sets the input signal's LSB depth for all stream encoders.
// Valid range is 8-24 bits. This affects DTX sensitivity.
func (e *Encoder) SetLSBDepth(depth int) error {
	if depth < 8 || depth > 24 {
		return ErrInvalidLSBDepth
	}
	for _, enc := range e.encoders {
		enc.SetLSBDepth(depth)
	}
	return nil
}

// LSBDepth returns the current LSB depth setting (from first encoder).
// All stream encoders share the same LSB depth setting.
func (e *Encoder) LSBDepth() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].LSBDepth()
	}
	return 24 // Default
}

// validateEncoderLayout mirrors libopus validate_encoder_layout.
func validateEncoderLayout(mapping []byte, streams, coupledStreams int) error {
	if streams+coupledStreams > len(mapping) {
		return fmt.Errorf("%w: streams + coupledStreams exceeds channels", ErrInvalidLayout)
	}

	mapped := make([]bool, streams+coupledStreams)

	for _, m := range mapping {
		if m == 255 {
			continue
		}

		idx := int(m)
		if idx >= 0 && idx < len(mapped) {
			mapped[idx] = true
		}
	}

	for s := range streams {
		if s < coupledStreams {
			if !mapped[2*s] || !mapped[2*s+1] {
				return fmt.Errorf("%w: coupled stream %d", ErrInvalidLayout, s)
			}
			continue
		}
		if !mapped[s+coupledStreams] {
			return fmt.Errorf("%w: uncoupled stream %d", ErrInvalidLayout, s)
		}
	}

	return nil
}
