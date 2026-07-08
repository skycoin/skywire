// Package silk implements the SILK speech layer of Opus (RFC 6716 Section 4.2),
// the low-level codec used by gopus for the SILK and Hybrid Opus modes.
//
// Most applications should use the top-level gopus encoder/decoder APIs rather
// than this package directly. The symbols exported here are advanced
// implementation details and may change before the first release.
//
// # Relationship to libopus
//
// This package is a behaviour-for-behaviour Go port of the reference SILK
// implementation in libopus 1.6.1 (the silk/ directory of the libopus source
// tree). The fixed-point decode path is bit-exact with libopus: identical
// range-coded input produces identical PCM, including the saturating
// fixed-point arithmetic, Q-format scaling and per-subframe state carried
// across frames. Where a Go file mirrors a specific libopus translation unit,
// its doc comments name that source file (for example silk/decode_core.c or
// silk/NLSF2A.c) so the two can be diffed.
//
// # Decoder
//
// The decode path is structured as in libopus:
//
//   - Decode / DecodeStereo (silk.go) — public per-packet entry points,
//     mirroring silk/dec_API.c silk_Decode. They parse the SILK header
//     (VAD + LBRR flags), decode each 20 ms frame, run mid/side to left/right
//     stereo unmixing, and resample from the internal SILK rate (8/12/16 kHz)
//     to the decoder API rate.
//   - decodeFrameCoreInto (frame_decode_helpers.go) — one SILK frame, mirroring
//     silk/decode_frame.c silk_decode_frame: decode indices, decode pulses,
//     dequantize parameters, run the LTP/LPC synthesis core.
//   - silkDecodeIndices / silkDecodeParameters / silkDecodeCore /
//     silkDecodePulses (libopus_decode.go) — port silk/decode_indices.c,
//     silk/decode_parameters.c, silk/decode_core.c and silk/decode_pulses.c.
//   - LBRR / Forward Error Correction (lbrr_decode.go) — DecodeFEC mirrors
//     silk_Decode with lostFlag = FLAG_DECODE_LBRR.
//   - Packet Loss Concealment (silk.go, cng.go, plc_glue.go, plc package) —
//     ports silk/PLC.c and silk/CNG.c; invoked when Decode is called with nil
//     data for a lost packet.
//   - Resampler (resample_libopus.go) — ports silk/resampler*.c.
//
// All public decode entry points are written to tolerate malformed, truncated
// or otherwise hostile input without panicking: the range decoder keeps
// returning symbols past the end of the buffer, so out-of-range indices are
// bounded inside the SILK decoder. Valid input remains bit-exact with libopus;
// the decode-path fuzz targets in this package assert the no-panic contract.
package silk
