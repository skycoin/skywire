// Multistream encoder implementation for Opus surround sound.
// This file contains the Encoder struct and NewEncoder function for encoding
// multi-channel audio into multistream Opus packets.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1

package multistream

import (
	"errors"
	"fmt"
	"math"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/types"
)

// ErrInvalidInput indicates the input samples have incorrect length.
var ErrInvalidInput = errors.New("multistream: invalid input length")

// ErrInvalidLayout indicates the channel mapping has an invalid layout.
// For coupled streams, both left and right channels must be mapped.
var ErrInvalidLayout = errors.New("multistream: invalid layout - coupled stream missing left or right channel")

// ErrInvalidLSBDepth indicates the LSB depth is outside the valid range (8-24).
var ErrInvalidLSBDepth = errors.New("multistream: invalid LSB depth (must be 8-24)")

// Encoder encodes multi-channel audio into Opus multistream packets.
// Each elementary stream is encoded independently using a Phase 8 unified Encoder,
// then combined with self-delimiting framing per RFC 6716 Appendix B.
//
// Multistream packets are used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 7845 Section 5.1.1
type Encoder struct {
	// sampleRate is the input sample rate (8000, 12000, 16000, 24000, or 48000 Hz).
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

	// Optional projection-family mixing matrix coefficients (column-major S16).
	projectionMixing []int16
	projectionCols   int
	projectionRows   int
	projectionFrame  []float32

	// projectionDemixingGain stores the gain field from the internal demixing matrix,
	// matching OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN.
	// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
	projectionDemixingGain int

	// streamBitrates stores per-stream rates computed by allocation policy.
	streamBitrates []int

	// streamSurroundTrim stores per-stream surround trim derived from surround masks.
	streamSurroundTrim []float32

	// streamEnergyMask stores per-stream CELT energy masks (max 42 values/stream).
	streamEnergyMask []float32

	// surroundBandSMR stores per-channel surround masks (21 bands per channel).
	surroundBandSMR []float32

	// surroundWindowMem mirrors libopus surround_analysis overlap history (per channel).
	surroundWindowMem []float32

	// surroundPreemphMem mirrors libopus surround_analysis preemphasis memory (per channel).
	surroundPreemphMem []float32

	// surroundInputScratch holds per-channel overlap+frame analysis input.
	surroundInputScratch []float32

	// surroundBandScratch stores temporary per-band energies for one channel.
	surroundBandScratch [surroundBands]float32

	// surroundAnalysisEncoder computes CELT band energies for surround analysis.
	surroundAnalysisEncoder *celt.Encoder

	// Per-call encode scratch reused across Encode calls to reduce the
	// steady-state encode allocation footprint. These slice headers and their
	// element buffers are intra-call scratch consumed before the assembled
	// packet is produced; they never escape the encoder. The assembled output
	// bytes are freshly allocated because the caller may retain them.
	streamInputScratch   [][]float32 // routed per-stream input buffers
	analysisInputScratch [][]float32 // routed per-stream analysis buffers (distinct length)
	streamPacketsScratch [][]byte    // per-stream encoded packets
	assembleScratch      [][]byte    // self-delimited framing slices for assembly

	// packetParser holds reusable parse/build working buffers and assembleArena
	// backs the self-delimited reframing of the first N-1 stream packets during
	// assembly. The arena slices coexist until the final packet copy but never
	// escape the assemble call.
	packetParser  packetScratch
	assembleArena []byte
}

const surroundBands = 21

var surroundEBands = [surroundBands + 1]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 13, 15, 17, 20, 23, 27, 32, 38, 46, 56, 69,
}

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

// NewEncoder creates a new multistream encoder.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total input channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//   - mapping: channel mapping table (length must equal channels)
//
// The mapping table determines how input audio is routed to stream encoders:
//   - Values 0 to 2*M-1: to coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: to uncoupled (mono) streams
//   - Value 255: silent channel (input ignored)
//
// Example for 5.1 surround (6 channels, 4 streams, 2 coupled):
//
//	mapping = [0, 4, 1, 2, 3, 5]
//	  Input 0 (FL): mapping[0]=0 -> coupled stream 0, left
//	  Input 1 (C):  mapping[1]=4 -> uncoupled stream 2 (2*2+0)
//	  Input 2 (FR): mapping[2]=1 -> coupled stream 0, right
//	  Input 3 (RL): mapping[3]=2 -> coupled stream 1, left
//	  Input 4 (RR): mapping[4]=3 -> coupled stream 1, right
//	  Input 5 (LFE): mapping[5]=5 -> uncoupled stream 3 (2*2+1)
func NewEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*Encoder, error) {
	// Validation exactly mirrors decoder
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
		sampleRate:              int32(sampleRate),
		inputChannels:           channels,
		streams:                 streams,
		coupledStreams:          coupledStreams,
		mapping:                 mappingCopy,
		encoders:                encoders,
		bitrate:                 256000, // Default 256 kbps total
		mappingFamily:           mappingFamily,
		lfeStream:               lfeStream,
		streamBitrates:          make([]int, streams),
		streamSurroundTrim:      make([]float32, streams),
		streamEnergyMask:        make([]float32, streams*2*surroundBands),
		surroundBandSMR:         make([]float32, channels*surroundBands),
		surroundWindowMem:       make([]float32, channels*celt.Overlap),
		surroundPreemphMem:      make([]float32, channels),
		surroundAnalysisEncoder: celt.NewEncoder(1),
	}
	enc.applyLFEFlags()
	return enc, nil
}

// NewEncoderDefault creates a multistream encoder with default Vorbis-style mapping
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

// NewEncoderAmbisonics creates a new multistream encoder for ambisonics audio.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total input channels (valid ambisonics count: 1, 4, 6, 9, 11, 16, 18, 25, 27...)
//   - mappingFamily: 2 for ACN/SN3D (mostly mono), 3 for projection (paired stereo)
//
// Valid ambisonics channel counts are (order+1)^2 or (order+1)^2 + 2:
//   - Order 0: 1 channel (or 3 with non-diegetic)
//   - Order 1 (FOA): 4 channels (or 6 with non-diegetic)
//   - Order 2 (SOA): 9 channels (or 11 with non-diegetic)
//   - Order 3 (TOA): 16 channels (or 18 with non-diegetic)
//
// For mapping family 2:
//   - ACN channel ordering with SN3D normalization
//   - All ambisonics channels are mono streams
//   - Optional non-diegetic stereo pair as one coupled stream
//
// For mapping family 3:
//   - Projection-based encoding
//   - Channels are paired into stereo coupled streams
//   - streams = (channels+1)/2, coupled = channels/2
//   - libopus parity support is limited to orders 1..5
//     (valid channels: 4, 6, 9, 11, 16, 18, 25, 27, 36, 38)
//
// Reference: RFC 7845 Section 5.1.1.2, libopus opus_multistream_encoder.c
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

// Reset clears all encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	for i, enc := range e.encoders {
		enc.Reset()
		enc.SetLFE(i == e.lfeStream)
		enc.SetCELTSurroundTrim(0)
		if i < len(e.streamSurroundTrim) {
			e.streamSurroundTrim[i] = 0
		}
	}
	if len(e.surroundBandSMR) > 0 {
		clear(e.surroundBandSMR)
	}
	if len(e.streamEnergyMask) > 0 {
		clear(e.streamEnergyMask)
	}
	if len(e.surroundWindowMem) > 0 {
		clear(e.surroundWindowMem)
	}
	if len(e.surroundPreemphMem) > 0 {
		clear(e.surroundPreemphMem)
	}
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

// Bitrate returns the total bitrate in bits per second.
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

func clampFloat32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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

func (e *Encoder) surroundBandwidth(frameSize int) types.Bandwidth {
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	totalRate := e.bitrateForAllocation(frameSize)
	equivRate := totalRate
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

func surroundTrimFromMask(maskL, maskR []float32) float32 {
	if len(maskL) < surroundBands {
		return 0
	}
	channels := 1
	if len(maskR) >= surroundBands {
		channels = 2
	}
	maskEnd := min(17, surroundBands)

	maskAvg := float32(0)
	count := 0
	diff := float32(0)
	for c := 0; c < channels; c++ {
		mask := maskL
		if c == 1 {
			mask = maskR
		}
		for i := 0; i < maskEnd; i++ {
			m := clampFloat32(mask[i], -2.0, 0.25)
			if m > 0 {
				m *= 0.5
			}
			width := surroundEBands[i+1] - surroundEBands[i]
			maskAvg += m * float32(width)
			count += width
			diff += m * float32(1+2*i-maskEnd)
		}
	}
	if count <= 0 {
		return 0
	}
	maskAvg = maskAvg/float32(count) + 0.2
	_ = maskAvg

	denom := float32(channels * (maskEnd - 1) * (maskEnd + 1) * maskEnd)
	if denom <= 0 {
		return 0
	}
	diff = diff * 6.0 / denom
	diff *= 0.5
	diff = clampFloat32(diff, -0.031, 0.031)
	return 64.0 * diff
}

func (e *Encoder) inputChannelForMapping(mappingIdx byte) int {
	for i, v := range e.mapping {
		if v == mappingIdx {
			return i
		}
	}
	return -1
}

func (e *Encoder) ensureSurroundInputScratch(size int) []float32 {
	if cap(e.surroundInputScratch) < size {
		e.surroundInputScratch = make([]float32, size)
	}
	return e.surroundInputScratch[:size]
}

func (e *Encoder) computeSurroundBandSMR(pcm []float32, frameSize int, bandSMR []float32) bool {
	if frameSize <= 0 || e.inputChannels < 3 || e.inputChannels > 8 {
		return false
	}
	if len(pcm) < frameSize*e.inputChannels {
		return false
	}
	if len(bandSMR) < e.inputChannels*surroundBands {
		return false
	}

	var pos [8]int
	if !channelPositions(e.inputChannels, pos[:]) {
		return false
	}

	upsample := resamplingFactor(int(e.sampleRate))
	if upsample <= 0 {
		return false
	}
	analysisFrameSize := frameSize * upsample
	freqSize, ok := surroundAnalysisFreqSize(analysisFrameSize)
	if !ok || analysisFrameSize%freqSize != 0 {
		return false
	}
	nbFrames := analysisFrameSize / freqSize
	overlap := celt.Overlap

	if cap(e.surroundWindowMem) < e.inputChannels*overlap {
		e.surroundWindowMem = make([]float32, e.inputChannels*overlap)
	}
	if cap(e.surroundPreemphMem) < e.inputChannels {
		e.surroundPreemphMem = make([]float32, e.inputChannels)
	}
	if e.surroundAnalysisEncoder == nil {
		e.surroundAnalysisEncoder = celt.NewEncoder(1)
	}

	in := e.ensureSurroundInputScratch(overlap + analysisFrameSize)

	var maskLogE [3][surroundBands]float32
	for c := range 3 {
		for i := range surroundBands {
			maskLogE[c][i] = -28.0
		}
	}

	for ch := 0; ch < e.inputChannels; ch++ {
		copy(in[:overlap], e.surroundWindowMem[ch*overlap:(ch+1)*overlap])
		clear(in[overlap:])

		for i := range frameSize {
			in[overlap+i*upsample] = pcm[i*e.inputChannels+ch] * float32(celt.CELTSigScale)
		}

		m := e.surroundPreemphMem[ch]
		for i := range analysisFrameSize {
			x := in[overlap+i]
			in[overlap+i] = x - m
			m = celt.PreemphCoef * x
		}
		e.surroundPreemphMem[ch] = m

		for i := range surroundBands {
			e.surroundBandScratch[i] = float32(math.Inf(-1))
		}

		for frame := range nbFrames {
			start := frame * freqSize
			end := start + freqSize + overlap
			coeffs := celt.MDCTForwardWithOverlapFloat32(in[start:end], overlap)
			if upsample != 1 {
				bound := min(freqSize/upsample, len(coeffs))
				for i := 0; i < bound; i++ {
					coeffs[i] *= float32(upsample)
				}
				for i := bound; i < len(coeffs); i++ {
					coeffs[i] = 0
				}
			}

			var tmp [surroundBands]float32
			e.surroundAnalysisEncoder.ComputeBandEnergiesFloat32Into(coeffs, surroundBands, freqSize, tmp[:])
			for i := range surroundBands {
				if tmp[i] > e.surroundBandScratch[i] {
					e.surroundBandScratch[i] = tmp[i]
				}
			}
		}

		for i := 1; i < surroundBands; i++ {
			if e.surroundBandScratch[i-1]-1.0 > e.surroundBandScratch[i] {
				e.surroundBandScratch[i] = e.surroundBandScratch[i-1] - 1.0
			}
		}
		for i := surroundBands - 2; i >= 0; i-- {
			if e.surroundBandScratch[i+1]-2.0 > e.surroundBandScratch[i] {
				e.surroundBandScratch[i] = e.surroundBandScratch[i+1] - 2.0
			}
		}

		copy(bandSMR[ch*surroundBands:(ch+1)*surroundBands], e.surroundBandScratch[:])

		switch pos[ch] {
		case 1:
			for i := range surroundBands {
				maskLogE[0][i] = logSum32(maskLogE[0][i], e.surroundBandScratch[i])
			}
		case 3:
			for i := range surroundBands {
				maskLogE[2][i] = logSum32(maskLogE[2][i], e.surroundBandScratch[i])
			}
		case 2:
			for i := range surroundBands {
				maskLogE[0][i] = logSum32(maskLogE[0][i], e.surroundBandScratch[i]-0.5)
				maskLogE[2][i] = logSum32(maskLogE[2][i], e.surroundBandScratch[i]-0.5)
			}
		}

		copy(e.surroundWindowMem[ch*overlap:(ch+1)*overlap], in[analysisFrameSize:analysisFrameSize+overlap])
	}

	for i := range surroundBands {
		maskLogE[1][i] = min(maskLogE[0][i], maskLogE[2][i])
	}
	channelOffset := 0.5 * opusmath.CeltLog2(2.0/float32(e.inputChannels-1))
	for c := range 3 {
		for i := range surroundBands {
			maskLogE[c][i] += channelOffset
		}
	}

	for ch := 0; ch < e.inputChannels; ch++ {
		row := bandSMR[ch*surroundBands : (ch+1)*surroundBands]
		if pos[ch] == 0 {
			clear(row)
			continue
		}
		mask := maskLogE[pos[ch]-1][:]
		for i := range surroundBands {
			row[i] -= mask[i]
		}
	}

	return true
}

func (e *Encoder) updateSurroundTrimFromPCM(pcm []float32, frameSize int) bool {
	if cap(e.streamSurroundTrim) < e.streams {
		e.streamSurroundTrim = make([]float32, e.streams)
	}
	trim := e.streamSurroundTrim[:e.streams]
	for i := range trim {
		trim[i] = 0
	}
	if !e.isSurroundMapping() {
		return false
	}

	needed := e.inputChannels * surroundBands
	if cap(e.surroundBandSMR) < needed {
		e.surroundBandSMR = make([]float32, needed)
	}
	bandSMR := e.surroundBandSMR[:needed]
	clear(bandSMR)
	if !e.computeSurroundBandSMR(pcm, frameSize, bandSMR) {
		return false
	}

	for s := 0; s < e.streams; s++ {
		if s == e.lfeStream {
			trim[s] = 0
			continue
		}
		if s < e.coupledStreams {
			left := e.inputChannelForMapping(byte(2 * s))
			right := e.inputChannelForMapping(byte(2*s + 1))
			if left < 0 || right < 0 {
				continue
			}
			trim[s] = surroundTrimFromMask(
				bandSMR[left*surroundBands:(left+1)*surroundBands],
				bandSMR[right*surroundBands:(right+1)*surroundBands],
			)
		} else {
			mappingIdx := byte(2*e.coupledStreams + (s - e.coupledStreams))
			mono := e.inputChannelForMapping(mappingIdx)
			if mono < 0 {
				continue
			}
			trim[s] = surroundTrimFromMask(
				bandSMR[mono*surroundBands:(mono+1)*surroundBands],
				nil,
			)
		}
	}
	return true
}

// multistreamCVBRBoundScale computes a constrained-VBR burst scale that keeps
// aggregate multistream packet bursts within the Opus 1275-byte packet cap.
// A scale of 1 keeps libopus single-stream behavior (~2x base burst ceiling).
func multistreamCVBRBoundScale(totalBitrate, sampleRate, frameSize int) float32 {
	if totalBitrate <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 1.0
	}
	targetBytes := (totalBitrate * frameSize) / (8 * sampleRate)
	if targetBytes <= 0 {
		return 1.0
	}
	const maxPacketBytes = 1275
	// Reserve a small framing margin for self-delimited multistream headers and
	// per-stream TOC/entropy tail variance.
	const framingMarginBytes = 16
	maxBurstBytes := max(maxPacketBytes-framingMarginBytes, 1)
	burstMultiple := float32(maxBurstBytes) / float32(targetBytes)
	if burstMultiple >= 2.0 {
		return 1.0
	}
	if burstMultiple <= 1.0 {
		return 0.0
	}
	return burstMultiple - 1.0
}

func (e *Encoder) applyPerStreamPolicy(frameSize int, pcm []float32) {
	rates := e.allocateRates(frameSize)
	hasSurroundMask := false
	if e.isSurroundMapping() {
		hasSurroundMask = e.updateSurroundTrimFromPCM(pcm, frameSize)
	}
	var streamMasks []float32
	if hasSurroundMask {
		needed := e.streams * 2 * surroundBands
		if cap(e.streamEnergyMask) < needed {
			e.streamEnergyMask = make([]float32, needed)
		}
		streamMasks = e.streamEnergyMask[:needed]
		clear(streamMasks)
	}
	cvbrBoundScale := float32(1.0)
	if len(e.encoders) > 0 && e.encoders[0].GetBitrateMode() == encoder.ModeCVBR {
		cvbrBoundScale = multistreamCVBRBoundScale(e.totalBitrateForAllocation(frameSize), int(e.sampleRate), frameSize)
	}

	surroundBandwidth := e.surroundBandwidth(frameSize)
	for i := 0; i < e.streams; i++ {
		enc := e.encoders[i]
		enc.SetAllocatedBitrate(rates[i])
		enc.SetLFE(i == e.lfeStream)
		enc.SetCELTCVBRBoundScale(cvbrBoundScale)
		enc.SetCELTPayloadCeilingActive(true)

		switch {
		case e.isSurroundMapping():
			enc.SetBandwidth(surroundBandwidth)
			if i < e.coupledStreams {
				// Preserve surround image parity with libopus on coupled streams.
				enc.SetMode(encoder.ModeCELT)
				enc.SetForceChannels(2)
			}
			if i < len(e.streamSurroundTrim) {
				enc.SetCELTSurroundTrim(e.streamSurroundTrim[i])
			} else {
				enc.SetCELTSurroundTrim(0)
			}
			enc.SetCELTEnergyMask(nil)
			if hasSurroundMask && i != e.lfeStream {
				base := i * 2 * surroundBands
				if i < e.coupledStreams {
					left := e.inputChannelForMapping(byte(2 * i))
					right := e.inputChannelForMapping(byte(2*i + 1))
					if left >= 0 && right >= 0 {
						dst := streamMasks[base : base+2*surroundBands]
						copy(dst[:surroundBands], e.surroundBandSMR[left*surroundBands:(left+1)*surroundBands])
						copy(dst[surroundBands:], e.surroundBandSMR[right*surroundBands:(right+1)*surroundBands])
						setStreamCELTEnergyMask(enc, dst)
					}
				} else {
					mappingIdx := byte(2*e.coupledStreams + (i - e.coupledStreams))
					mono := e.inputChannelForMapping(mappingIdx)
					if mono >= 0 {
						dst := streamMasks[base : base+surroundBands]
						copy(dst, e.surroundBandSMR[mono*surroundBands:(mono+1)*surroundBands])
						setStreamCELTEnergyMask(enc, dst)
					}
				}
			}
		case e.isAmbisonicsMapping():
			enc.SetMode(encoder.ModeCELT)
			enc.SetCELTSurroundTrim(0)
			enc.SetCELTEnergyMask(nil)
		default:
			enc.SetCELTSurroundTrim(0)
			enc.SetCELTEnergyMask(nil)
		}
	}
}

func setStreamCELTEnergyMask(enc *encoder.Encoder, mask []float32) {
	if enc == nil {
		return
	}
	if len(mask) == 0 {
		enc.SetCELTEnergyMask(nil)
		return
	}
	n := min(len(mask), 2*surroundBands)
	enc.SetCELTEnergyMask(mask[:n])
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

// GetDemixingMatrix returns the serialized demixing matrix for this projection encoder,
// matching the output of OPUS_PROJECTION_GET_DEMIXING_MATRIX_REQUEST.
//
// The returned bytes are S16LE-encoded, row-major over:
//   - rows = streams + coupled_streams  (nb_input_streams)
//   - cols = channels                   (nb_output_streams)
//
// Returns nil if this encoder is not mapping family 3, or if no defaults are
// available for the configured channel count.
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_REQUEST
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

// DemixingMatrixSize returns the byte size of the demixing matrix,
// matching OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE_REQUEST.
//
// Returns 0 if this encoder is not mapping family 3.
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE_REQUEST
func (e *Encoder) DemixingMatrixSize() int {
	if e.mappingFamily != 3 {
		return 0
	}
	return ProjectionDemixingMatrixSize(e.inputChannels, e.streams, e.coupledStreams)
}

// Encode encodes multi-channel PCM samples to an Opus multistream packet.
//
// Parameters:
//   - pcm: input samples as float32, sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
//   - frameSize: number of samples per channel (must be valid for Opus: 120, 240, 480, 960, 1920, 2880)
//
// Returns:
//   - The encoded multistream packet
//   - nil, nil if DTX is active for all streams (all returned 1-byte TOC-only packets)
//   - error if encoding fails
//
// The encoding process:
//  1. Routes input channels to stream buffers via mapping table
//  2. Encodes each stream independently using the unified encoder
//  3. Assembles packets with self-delimiting framing per RFC 6716 Appendix B
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1
func (e *Encoder) Encode(pcm []float32, frameSize int) ([]byte, error) {
	return e.EncodeWithAnalysis(pcm, frameSize, pcm)
}

// EncodeWithAnalysis encodes the selected frame while letting child encoders
// analyze the full caller frame selected by expert-frame-duration controls.
func (e *Encoder) EncodeWithAnalysis(pcm []float32, frameSize int, analysisPCM []float32) ([]byte, error) {
	return e.EncodeFloat32WithAnalysis(pcm, frameSize, analysisPCM)
}

// EncodeFloat32 encodes libopus float-build PCM samples to an Opus multistream packet.
func (e *Encoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error) {
	return e.Encode(pcm, frameSize)
}

// EncodeFloat32WithAnalysis encodes the selected frame while letting child
// encoders analyze the full caller frame selected by expert-frame-duration
// controls. PCM routing and projection mixing stay in the libopus float domain.
//
// The top-level packet budget defaults to the libopus per-stream maximum
// (maxOpusFrameBytes per stream), matching the recommended 4000-byte-per-stream
// caller buffer. Use EncodeFloat32WithAnalysisMaxBytes to pass an explicit
// caller buffer size, which libopus threads into the per-stream curr_max
// budgeting (opus_multistream_encoder.c opus_multistream_encode_native()).
func (e *Encoder) EncodeFloat32WithAnalysis(pcm []float32, frameSize int, analysisPCM []float32) ([]byte, error) {
	return e.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, analysisPCM, maxOpusFrameBytes*e.streams)
}

// EncodeFloat32WithAnalysisMaxBytes encodes one frame with an explicit caller
// packet budget. maxDataBytes is the total output buffer size; libopus uses it
// as the top-level max_data_bytes and derives each stream's curr_max from it
// (opus_multistream_encoder.c opus_multistream_encode_native(), lines 1016-1024).
func (e *Encoder) EncodeFloat32WithAnalysisMaxBytes(pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	// Validate input length
	expectedLen := frameSize * e.inputChannels
	if len(pcm) != expectedLen {
		return nil, fmt.Errorf("%w: got %d samples, expected %d (frameSize=%d, channels=%d)",
			ErrInvalidInput, len(pcm), expectedLen, frameSize, e.inputChannels)
	}
	if analysisPCM == nil {
		analysisPCM = pcm
	}
	if len(analysisPCM) < expectedLen || len(analysisPCM)%e.inputChannels != 0 {
		return nil, fmt.Errorf("%w: got %d analysis samples for frameSize=%d channels=%d",
			ErrInvalidInput, len(analysisPCM), frameSize, e.inputChannels)
	}

	// Mirror libopus per-stream rate/control policy ahead of stream encodes.
	e.applyPerStreamPolicy(frameSize, pcm)

	// For CBR, libopus shrinks the total caller budget to the bitrate-implied
	// packet size before deriving each stream's curr_max
	// (opus_multistream_encoder.c opus_multistream_encode_native(), lines
	// 918-928). rate_sum is the sum of the per-stream allocation that feeds the
	// OPUS_AUTO branch.
	fs := int(e.sampleRate)
	if !e.VBR() {
		smallestPacket := e.streams*2 - 1
		if frameSize > 0 && fs/frameSize == 10 {
			smallestPacket += e.streams
		}
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

	// Route input channels to stream buffers
	streamBuffers := e.routeInputToStreams(e.streamInputScratch, pcm, frameSize)
	e.streamInputScratch = streamBuffers
	analysisStreamBuffers := streamBuffers
	if len(analysisPCM) != len(pcm) {
		analysisFrameSize := len(analysisPCM) / e.inputChannels
		analysisStreamBuffers = e.routeInputToStreams(e.analysisInputScratch, analysisPCM, analysisFrameSize)
		e.analysisInputScratch = analysisStreamBuffers
	}

	// Encode each stream.
	//
	// libopus opus_multistream_encoder.c opus_multistream_encode_native() sizes
	// each stream's max_data_bytes (curr_max) from the remaining caller budget
	// before handing it to opus_encode_native(), which in turn feeds the CELT
	// nb_compr_bytes / SILK maxBits rate-control loops (opus_encoder.c). Passing a
	// fixed per-stream cap instead of curr_max diverges from libopus on the hybrid
	// VBR path, where CELT picks its per-frame size from within nb_compr_bytes.
	//
	//   curr_max = max_data_bytes - tot_size;                 (line 1016)
	//   curr_max -= IMAX(0,2*(nb_streams-s-1)-1);             (line 1018, reserve)
	//   if (Fs/frame_size == 10) curr_max -= nb_streams-s-1;  (line 1020-1021)
	//   curr_max = IMIN(curr_max, MS_FRAME_TMP);              (line 1022)
	//   if (s != nb_streams-1) curr_max -= curr_max>253?2:1;  (line 1024)
	//
	// tot_size accumulates the self-delimited size of the already-emitted streams,
	// matching opus_repacketizer_out_range_impl()'s returned len (line 1048).
	streamPackets := e.streamPacketsScratch
	if cap(streamPackets) < e.streams {
		streamPackets = make([][]byte, e.streams)
	}
	streamPackets = streamPackets[:e.streams]
	e.streamPacketsScratch = streamPackets
	allDTX := true
	totSize := 0
	hundredMs := frameSize > 0 && int(e.sampleRate)/frameSize == 10

	for i := 0; i < e.streams; i++ {
		enc := e.encoders[i]

		currMax := maxDataBytes - totSize
		// Reserve one byte for the last stream and two for the others.
		if r := 2*(e.streams-i-1) - 1; r > 0 {
			currMax -= r
		}
		// For 100 ms, reserve an extra byte per stream for the ToC.
		if hundredMs {
			currMax -= e.streams - i - 1
		}
		if currMax > msFrameTmp {
			currMax = msFrameTmp
		}
		// Repacketizer adds one or two bytes for self-delimited frames.
		if i != e.streams-1 {
			if currMax > 253 {
				currMax -= 2
			} else {
				currMax--
			}
		}
		// For CBR, the last stream gets exactly the remaining budget so the
		// total packet matches the requested constant rate
		// (opus_multistream_encoder.c lines 1025-1026).
		if !e.VBR() && i == e.streams-1 {
			enc.SetAllocatedBitrate(bitsToBitrate(currMax*8, fs, frameSize))
		}

		packet, err := enc.EncodeFloat32WithAnalysisMaxBytes(streamBuffers[i], frameSize, analysisStreamBuffers[i], currMax)
		if err != nil {
			return nil, fmt.Errorf("stream %d encode failed: %w", i, err)
		}

		if packet == nil {
			streamPackets[i] = []byte{}
		} else {
			streamPackets[i] = packet
			// DTX packets are 1-byte TOC-only; full packets are >1 byte
			if len(packet) > 1 {
				allDTX = false
			}
			// tot_size tracks the self-delimited size for non-last streams.
			if i != e.streams-1 {
				totSize += len(packet) + frameLengthBytes(len(packet))
			} else {
				totSize += len(packet)
			}
		}
	}

	// If all streams are DTX (1-byte TOC or nil), return nil to signal silence
	if allDTX {
		return nil, nil
	}

	// Assemble multistream packet with RFC 6716 Appendix B framing.
	packet, err := e.assembleMultistreamPacket(streamPackets)
	if err != nil {
		return nil, err
	}
	return packet, nil
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

// SetForceChannels sets forced channel count on all stream encoders.
func (e *Encoder) SetForceChannels(channels int) {
	for _, enc := range e.encoders {
		enc.SetForceChannels(channels)
	}
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

// Lookahead returns the encoder's algorithmic delay in samples at 48kHz.
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
