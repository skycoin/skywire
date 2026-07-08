// Package multistream implements the Opus multistream engine: encoding and
// decoding of packets that carry several elementary Opus streams (each a normal
// SILK / CELT / Hybrid stream) which are decoded independently and routed to
// output channels via a channel-mapping table. It is the engine behind surround
// sound (5.1, 7.1, ...), discrete multichannel, and ambisonics/projection audio.
//
// Most applications should reach for the top-level gopus multistream wrappers
// (gopus.NewMultistreamEncoder / gopus.NewMultistreamDecoder and the …Default
// constructors), which forward to this package. Import this package directly for
// the lower-level Encoder / Decoder types and for projection / ambisonics, whose
// constructors live only here.
//
// # Channel mapping families (RFC 7845 Section 5)
//
// A multistream layout is described by three integers plus a mapping table:
//
//   - streams (N):        total number of elementary Opus streams.
//   - coupledStreams (M): number of those streams that are coupled (stereo); the
//     first M streams decode to 2 channels each, the remaining N-M decode to 1
//     channel. The total decoded-channel count is therefore N+M, and the
//     invariant N+M <= 255 always holds.
//   - mapping:            one byte per OUTPUT channel selecting its source
//     decoded channel (see below). len(mapping) == output channels.
//
// Each mapping byte is interpreted as:
//
//   - 0 .. 2*M-1:  a channel of a coupled (stereo) stream; even = left,
//     odd = right of stereo pair index value/2.
//   - 2*M .. N+M-1: a mono (uncoupled) stream.
//   - 255:         a silent output channel (decoded as zeros).
//
// RFC 7845 channel mapping families this package supports:
//
//   - Family 0 (mono/stereo): the degenerate 1- or 2-channel case (1 stream).
//   - Family 1 (Vorbis/surround): 1..8 channels in Vorbis channel order;
//     see DefaultMapping / NewEncoderDefault / NewDecoderDefault.
//   - Family 2 (ambisonics, mono streams): ACN/SN3D ambisonics carried as mono
//     streams plus an optional non-diegetic stereo pair; see ValidateAmbisonics,
//     AmbisonicsMapping, and NewEncoderAmbisonics(.., 2).
//   - Family 3 (ambisonics projection): ACN/SN3D ambisonics carried as maximally
//     coupled stereo streams with a mixing/demixing matrix; see the Projection
//     types below.
//   - Family 255 (discrete): an explicit, arbitrary mapping with no defined
//     channel semantics; constructed directly via NewEncoder / NewDecoder.
//
// # Surround semantics
//
// Surround encoding (family 1, 3..8 channels) splits the requested total bitrate
// across the elementary streams using libopus' surround masking / rate-allocation
// model, applies per-stream LFE and trim decisions, then concatenates the
// per-stream packets with RFC 6716 Appendix B self-delimiting framing (the first
// N-1 streams are self-delimited; the last uses standard framing). Decoding
// reverses this: split, decode each stream, then scatter decoded channels to the
// output via the mapping table.
//
// # Projection semantics (family 3)
//
// Projection encoding mixes the input ambisonics channels through a fixed mixing
// matrix into the coupled/mono elementary streams, so inter-channel energy is
// redistributed for better coding efficiency. The decoder reverses this with a
// demixing matrix obtained from the encoder. gopus does not expose separate
// projection encoder/decoder types as libopus does; instead use
// NewProjectionEncoder / Encoder.GetDemixingMatrix on the encode side and
// NewProjectionDecoder (or NewDecoder + Decoder.SetProjectionDemixingMatrix) on
// the decode side. Projection decoders always use the trivial identity mapping
// [0, 1, ..., channels-1]; the demixing matrix carries the routing. libopus 1.6.1
// (and gopus) provide pre-computed matrices for ambisonics orders 1..5 only.
//
// # Buffer ownership
//
// Constructors copy the caller's mapping (and demixing matrix) defensively, so
// the caller may reuse or mutate those slices afterwards. Encode and the Decode*
// methods allocate and return a fresh output slice on every call that the caller
// fully owns; input slices are read-only and never retained past the call.
//
// # Error conditions
//
// Constructor and CTL errors (ErrInvalidChannels, ErrInvalidStreams,
// ErrInvalidCoupledStreams, ErrTooManyChannels, ErrInvalidMapping,
// ErrInvalidProjectionMatrix, ErrInvalidGain, ErrInvalidComplexity, ...) report
// out-of-range layout or control parameters. Decode-path errors
// (ErrPacketTooShort, ErrInvalidPacket, ErrDurationMismatch, ErrBufferTooSmall,
// ErrInvalidStreamCount) report malformed or inconsistent input packets; the
// decode path is hardened so that no malformed input causes a panic or
// out-of-bounds access — every defect surfaces as one of these errors.
//
// # Build tags
//
// DRED is available only in builds using -tags gopus_dred. OSCE helper surfaces
// remain extra-controls-gated and absent from the default build.
//
// Reference: RFC 7845 Section 5 (Channel Mapping), RFC 6716 Appendix B
// (self-delimiting framing), libopus opus_multistream_* and opus_projection_*.
package multistream
