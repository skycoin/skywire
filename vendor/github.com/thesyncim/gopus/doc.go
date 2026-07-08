// Package gopus implements the Opus audio codec in pure Go.
//
// It targets RFC 6716 compatibility with pinned libopus reference behavior,
// uses caller-owned buffers for the main encode/decode API, and requires no cgo
// dependency.
//
// # Quick Start
//
// Encoding:
//
//	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
//	    SampleRate:  48000,
//	    Channels:    2,
//	    Application: gopus.ApplicationAudio,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	pcm := make([]float32, 960*2) // 20 ms stereo at 48 kHz
//	packet := make([]byte, 4000)
//	nPacket, err := enc.Encode(pcm, packet)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	packet = packet[:nPacket]
//
// Decoding:
//
//	cfg := gopus.DefaultDecoderConfig(48000, 2)
//	dec, err := gopus.NewDecoder(cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
//	n, err := dec.Decode(packet, pcmOut)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	pcmOut = pcmOut[:n*cfg.Channels]
//
// # PCM And Buffers
//
// Float32 samples are normalized to [-1.0, 1.0]. Int16 helpers are available
// for common audio APIs. Stereo and multichannel PCM is interleaved.
//
// Decode output needs room for up to 5760 samples per channel, the default
// 120 ms cap at 48 kHz. A 4000-byte encode buffer is sufficient for any Opus
// packet.
//
// # Packet Loss
//
// Pass nil packet data to Decode to run packet loss concealment:
//
//	if packetLost {
//	    n, err = dec.Decode(nil, pcmOut)
//	} else {
//	    n, err = dec.Decode(packet, pcmOut)
//	}
//
// # Controls And Extensions
//
// Standard Opus controls such as bitrate, complexity, bandwidth, FEC, DTX,
// gain, frame size, packet parsing, and multistream helpers are exposed on the
// top-level types.
//
// Optional extension support is build dependent. Use SupportsOptionalExtension
// before relying on an extension surface, and treat README.md as the support
// matrix source of truth.
//
//	if SupportsOptionalExtension(OptionalExtensionQEXT) {
//	    _ = enc.SetQEXT(true)
//	}
//
// # Multistream
//
// NewMultistreamEncoderDefault and NewMultistreamDecoderDefault support 1-8
// channels with the standard Vorbis-style channel mappings used by Ogg Opus.
// Use the explicit multistream constructors for custom mappings.
//
// # Package Boundaries
//
// The public surface is this top-level gopus package plus four importable
// packages: multistream (surround / ambisonics / projection), container/ogg
// (Ogg Opus read/write), container/red (RFC 2198 RTP RED), and types (shared
// Mode / Bandwidth / Signal enumerations). The top-level package re-exports the
// common multistream constructors, so most applications need only gopus and, if
// they handle files, container/ogg.
//
// The SILK, CELT, Hybrid, range-coder, PLC, and DNN building blocks live under
// internal/ and are not importable; depend on the packages above instead.
//
// Encoder and Decoder instances are not safe for concurrent use.
package gopus
