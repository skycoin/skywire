// Package rangecoding implements the Opus range coder: the entropy
// encoder/decoder defined in RFC 6716 Section 4.1 and ported, bit for bit, from
// libopus celt/entenc.c (encoder) and celt/entdec.c (decoder), with constants
// from celt/mfrngcod.h and celt/entcode.h.
//
// # Overview
//
// The range coder is the innermost layer of the Opus bitstream: every SILK and
// CELT symbol is ultimately encoded through it. It is an arithmetic coder
// operating on a 32-bit range register (rng) and value register (val),
// renormalizing one 8-bit symbol at a time (EC_SYM_BITS). Two independent bit
// streams share a single packet buffer: the range-coded symbols grow forward
// from the start of the buffer, while "raw" bits (ec_enc_bits/ec_dec_bits) grow
// backward from the end. ec_tell/ec_tell_frac report the combined position of
// both streams.
//
//   - [Encoder] mirrors libopus ec_enc: Encode/EncodeBin/EncodeBit/EncodeICDF*
//     for range-coded symbols, EncodeRawBits for the end stream, EncodeUniform
//     for uniformly distributed integers (ec_enc_uint), and Done to finalize.
//   - [Decoder] mirrors libopus ec_dec and is the exact symmetric inverse:
//     Decode+Update, DecodeBin, DecodeBit, the DecodeICDF* family, DecodeRawBits,
//     and DecodeUniform.
//
// Encoder and decoder are exact inverses: feeding the output of an [Encoder]
// into a freshly-initialized [Decoder] reproduces the original symbol sequence,
// and ec_tell stays in lockstep on both sides. This roundtrip property is
// exercised by the package's fuzz targets and characterization tests.
//
// # Bit-exactness
//
// This is the crown-jewel bit-exact layer of gopus. The encoder must produce
// byte-for-byte identical output to libopus for the same symbol sequence, and
// the decoder must consume libopus output identically, because any divergence
// here desynchronizes the entire packet. To preserve this, the implementation
// reproduces libopus integer arithmetic exactly:
//
//   - The state registers rng, val, ext, offs, end_offs, end_window, and
//     storage are unsigned 32-bit (opus_uint32). Their wrap-around and
//     truncation behavior under shifts and multiplies is load-bearing.
//   - rem, nend_bits, nbits_total, and error are C int in libopus and are kept
//     as int32 here; in particular rem uses -1 as a "no buffered byte" sentinel,
//     so it must be signed.
//   - Renormalization, carry propagation (the deferred 0xFF run), and the
//     decoder's (EC_SYM_MAX &^ sym) reconstruction follow libopus exactly.
//
// See the [state_type_parity_test.go] reflection test, which asserts these
// field widths match the libopus ec_ctx struct. Changing any width or arithmetic
// step risks breaking parity and must not be done casually.
//
// # Scope
//
// Most applications should use the top-level gopus APIs rather than importing
// this package directly. It is exposed primarily so the SILK and CELT codec
// layers (and bit-exact differential tests against libopus) can drive the
// entropy coder, and the surface may change before the first release.
package rangecoding
