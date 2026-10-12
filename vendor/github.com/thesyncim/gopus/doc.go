// Package gopus implements Opus audio encoding and decoding in pure Go.
// It supports the SILK, CELT, and Hybrid modes defined by RFC 6716 and RFC 8251
// without requiring cgo or an external codec library.
//
// # Streams and samples
//
// [Encoder] and [Decoder] process mono or stereo streams. Use [MultistreamEncoder]
// and [MultistreamDecoder] for multichannel layouts. Codec instances retain
// stream history and are not safe for concurrent use. Each independent stream
// needs its own instance. Reset clears stream history; each Reset method
// documents which controls it preserves.
//
// PCM is interleaved: each successive group of samples contains one sample per
// channel. Float32 methods use normalized full scale; integer input methods use
// signed 16-bit samples or right-justified 24-bit samples in int32 values.
// [Decoder.DecodeInt24] writes int32 values at 24-bit PCM scale without clipping
// to the signed 24-bit range. Frame sizes and decoded sample counts are measured per
// channel at the configured sample rate. Encoders return packet byte counts.
//
// # Buffers and packet loss
//
// Encode and Decode methods write into caller-provided buffers. Reusing codec
// instances and adequately sized buffers avoids steady-state allocations in
// the covered hot paths; constructors and initial warmup can allocate. A decode
// buffer sized to [DecoderConfig].MaxPacketSamples times Channels holds the
// largest packet allowed by that configuration. Only the returned sample count
// times Channels elements contain decoded output.
//
// An empty packet passed to [Decoder.Decode] requests packet-loss concealment.
// The output buffer length selects the requested duration, except that a full
// configured-size buffer uses the last decoded packet's duration when available.
// To recover a missing packet from the next packet's in-band FEC, call
// [Decoder.DecodeWithFEC] with fec set to true, then call Decode with the same
// packet to decode its primary audio. The decoder conceals the loss if FEC is
// unavailable. Native 96 kHz FEC requests always use concealment and ignore
// the supplied packet.
//
// [Reader] and [Writer] adapt packet sources and sinks to little-endian PCM byte
// streams. They require packet-aware transport interfaces; they do not frame a
// byte stream of concatenated Opus packets. [ParsePacket] inspects packet framing,
// and [Repacketizer] combines or separates compatible frames without re-encoding.
//
// # Build configuration
//
// Go 1.27 or later is required. Ordinary builds use scalar Go kernels.
// GOEXPERIMENT=simd enables Go SIMD kernels where implemented, with runtime CPU
// checks and scalar fallbacks. The nosimd and purego build tags force scalar
// kernels even when the experiment is enabled.
//
// Optional extensions require their matching build tags: gopus_qext for QEXT and
// native 96 kHz, gopus_dred for DRED, gopus_osce for OSCE and deep PLC, and
// gopus_custom_modes for Opus Custom. The gopus_fixed_point tag selects the
// integer codec pipeline. [SupportsOptionalExtension] reports the supported API
// surface of the current build; compiled extra controls may still report false.
// DRED controls and standalone recovery APIs also compile with gopus_osce,
// while the supported DRED probe requires gopus_dred.
package gopus
