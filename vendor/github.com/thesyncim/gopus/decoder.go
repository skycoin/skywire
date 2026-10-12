package gopus

import (
	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/hybrid"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

const (
	defaultMaxPacketSamples = 5760
	defaultMaxPacketBytes   = 1500
)

// DecoderConfig sets the sample rate, channel count, and packet limits for a
// Decoder.
type DecoderConfig struct {
	// SampleRate must be 8000, 12000, 16000, 24000, or 48000 Hz.
	// Builds with gopus_qext also accept 96000 Hz.
	SampleRate int
	// Channels must be 1 (mono) or 2 (stereo).
	Channels int
	// MaxPacketSamples caps the maximum decoded samples per channel per packet.
	// Zero selects 5,760 samples, or 120 ms at 48 kHz. The limit must fit
	// the decoder work buffers; [NewDecoder] rejects overflowing sizes.
	MaxPacketSamples int
	// MaxPacketBytes caps the maximum Opus packet size in bytes.
	// Zero selects 1,500 bytes.
	MaxPacketBytes int
}

// DefaultDecoderConfig returns a DecoderConfig with default packet limits for
// the given stream format. It allows up to 5,760 decoded samples per channel
// and 1,500 packet bytes; set MaxPacketSamples or MaxPacketBytes to raise a
// limit.
func DefaultDecoderConfig(sampleRate, channels int) DecoderConfig {
	return DecoderConfig{
		SampleRate:       sampleRate,
		Channels:         channels,
		MaxPacketSamples: defaultMaxPacketSamples,
		MaxPacketBytes:   defaultMaxPacketBytes,
	}
}

// Decoder decodes Opus packets into PCM samples. It retains stream state and is
// not safe for concurrent use; use one Decoder per stream. Construct it with
// [NewDecoder]; the zero value is not ready for use.
type Decoder struct {
	silkDecoder      *silk.Decoder   // SILK-only mode decoder
	celtDecoder      *celt.Decoder   // CELT-only mode decoder
	hybridDecoder    *hybrid.Decoder // Hybrid mode decoder
	sampleRate       int32
	channels         int32
	maxPacketSamples int
	maxPacketBytes   int
	// scratchF32 backs the five fixed float32 decode work buffers below with one
	// contiguous allocation; see NewDecoder.
	scratchF32         arena.Bump[float32]
	scratchPCM         []float32
	scratchTransition  []float32
	scratchRedundant   []float32
	scratchSilkPLC     []float32 // SILK PLC concealment output (one chunk at API rate)
	lastFrameSize      int32
	lastPacketDuration int32
	prevMode           Mode // Track last mode for PLC
	lastPacketMode     Mode // Track last packet mode (libopus st->mode) for decode_fec gating
	lastBandwidth      Bandwidth
	prevRedundancy     bool
	prevPacketStereo   bool
	haveDecoded        bool
	bandwidthKnown     bool   // true once a non-PLC packet has been decoded (gates Bandwidth() vs 0)
	redundantRng       uint32 // Range from redundancy decoding, XORed with final range
	lastDataLen        int32  // Length of last packet data
	mainDecodeRng      uint32 // Final range from main decode (before any redundancy processing)
	decodeGainQ8       int    // Output gain in Q8 dB (libopus OPUS_SET_GAIN semantics)
	ignoreExtensions   bool   // libopus OPUS_SET_IGNORE_EXTENSIONS semantics
	complexity         int32  // libopus decoder complexity, default 0

	// FEC (Forward Error Correction) state
	// Stages the following packet's LBRR payload to recover the missing audio.
	fecData       []byte    // Stored packet data containing LBRR for FEC recovery
	fecMode       Mode      // Mode of the packet containing LBRR
	fecBandwidth  Bandwidth // Bandwidth of the packet containing LBRR
	fecStereo     bool      // Whether the packet was stereo
	fecFrameSize  int       // Frame size of the packet containing LBRR
	fecFrameCount int       // Number of frames in packet
	hasFEC        bool      // True if fecData contains valid LBRR data
	scratchFEC    []float32 // Scratch buffer for FEC decode

	// Scratch range decoder to avoid per-frame heap allocations
	scratchRangeDecoder rangecoding.Decoder

	// Soft clipping memory (float decode uses none; int16 decode uses this)
	softClipMem [2]float32
	dnnBlob     *dnnblob.Blob
	decoderDREDFields
	decoderOSCEFields
	decoderHD96kFields
	decoderFixedFields
	fixedQEXT decoderFixedQEXTFields

	// Decoder-side DNN readiness mirrors the validated model families retained
	// by OPUS_SET_DNN_BLOB; optional paths require their corresponding models.
	pitchDNNLoaded    bool
	plcModelLoaded    bool
	farganModelLoaded bool
}

// NewDecoder returns a Decoder configured by cfg. It returns an error if the
// sample rate, channel count, or packet limits are invalid.
func NewDecoder(cfg DecoderConfig) (*Decoder, error) {
	if !validSampleRate(cfg.SampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if cfg.Channels < 1 || cfg.Channels > 2 {
		return nil, ErrInvalidChannels
	}

	maxPacketSamples := cfg.MaxPacketSamples
	if maxPacketSamples == 0 {
		maxPacketSamples = defaultMaxPacketSamples
	}
	if maxPacketSamples < 1 {
		return nil, ErrInvalidMaxPacketSamples
	}

	maxPacketBytes := cfg.MaxPacketBytes
	if maxPacketBytes == 0 {
		maxPacketBytes = defaultMaxPacketBytes
	}
	if maxPacketBytes < 1 {
		return nil, ErrInvalidMaxPacketBytes
	}

	// The contiguous float32 arena holds three packet-sized buffers and two
	// transition buffers. Bound its element count by the largest length whose
	// byte size fits in int before multiplying or constructing decoder state.
	transitionSamples := max(48000, cfg.SampleRate) / 200 // 5 ms in the largest configured CELT geometry.
	transLen := transitionSamples * cfg.Channels
	maxFloatElements := int(^uint(0)>>1) / 4
	if maxPacketSamples > (maxFloatElements-2*transLen)/(3*cfg.Channels) {
		return nil, ErrInvalidMaxPacketSamples
	}

	// Under gopus_qext, 96 kHz requests route through the 48 kHz internal
	// pipeline with 2x upsampling at the output boundary.
	// C ref: opus_decoder.c opus_decoder_init() ENABLE_QEXT (Fs != 96000) gate.
	internalRate := cfg.SampleRate
	if cfg.SampleRate == 96000 {
		internalRate = 48000
	}

	silkDec := silk.NewDecoder()
	silkDec.SetAPISampleRate(internalRate)
	celtDec := celt.NewDecoder(cfg.Channels)
	if err := celtDec.SetAPISampleRate(internalRate); err != nil {
		return nil, err
	}
	hybridDec := hybrid.NewDecoderWithSharedDecoders(cfg.Channels, silkDec, celtDec)
	hybridDec.SetAPISampleRate(internalRate)

	d := &Decoder{
		silkDecoder:      silkDec,
		celtDecoder:      celtDec,
		hybridDecoder:    hybridDec,
		sampleRate:       int32(internalRate),
		channels:         int32(cfg.Channels),
		maxPacketSamples: maxPacketSamples,
		maxPacketBytes:   maxPacketBytes,
		lastFrameSize:    int32(internalRate / 50), // Default 20ms at the internal rate
		prevMode:         ModeHybrid,               // Default for PLC until first decode
		lastPacketMode:   ModeHybrid,
		lastBandwidth:    BandwidthFullband,
		fecData:          make([]byte, maxPacketBytes),
	}
	// Back the five fixed float32 decode work buffers with one contiguous arena.
	pcmLen := maxPacketSamples * cfg.Channels
	d.scratchF32.Ensure(3*pcmLen + 2*transLen)
	d.scratchPCM = d.scratchF32.AllocN(pcmLen)
	d.scratchTransition = d.scratchF32.AllocN(transLen)
	d.scratchRedundant = d.scratchF32.AllocN(transLen)
	d.scratchSilkPLC = d.scratchF32.AllocN(pcmLen)
	d.scratchFEC = d.scratchF32.AllocN(pcmLen)
	if cfg.SampleRate == 96000 {
		init96kDecoder(d)
	}
	d.initOSCELossHook()
	return d, nil
}
