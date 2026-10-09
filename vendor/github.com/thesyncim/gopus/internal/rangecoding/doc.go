// Package rangecoding implements the Opus range coder in RFC 6716 Section 4.1.
// It ports libopus 1.6.1's `celt/entenc.c`, `celt/entdec.c`, and `celt/entcode.c`.
//
// Range-coded symbols are written forward from the start of the packet buffer.
// Raw bits use a second stream packed backward from the end. [Encoder.Done]
// finalizes both streams; [Decoder.DecodeRawBits] reads the raw-bit stream.
// [Encoder.Tell] and [Decoder.Tell] report the combined bit position, while
// [Encoder.TellFrac] and [Decoder.TellFrac] report it in eighth-bit units.
//
// Initialize an [Encoder] with a caller-owned output buffer and a [Decoder]
// with the packet bytes. The coder retains those slices for the duration of
// use, so callers must not mutate or reuse them while encoding or decoding.
// Each coder also retains mutable range and carry state; use one per active
// packet and serialize access.
//
// Integer widths, wraparound, renormalization, carry propagation, and symbol
// reconstruction determine the encoded bytes and final range. Exact guarantees
// are scoped to the matched reference configurations listed in
// `reports/validation.md#coverage`.
package rangecoding
