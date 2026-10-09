// Package celt implements the CELT transform-coding layer in RFC 6716 Section
// 4.3. It analyzes and reconstructs spectral frames using an MDCT, predictive
// band-energy coding, pulse-vector quantization, and overlap-add synthesis.
//
// [Encoder] and [Decoder] retain per-stream prediction, overlap, filter, and
// concealment state. Keep one instance per stream and serialize access to it.
// [Decoder.DecodeFrame] initializes a range decoder for a CELT payload.
// [Decoder.AccumulateFrameHybridWithPacketStereo] is the Hybrid entry point:
// it continues from the range decoder after SILK and adds CELT output to the
// caller's low-band PCM. The encoder can use a range coder supplied with
// [Encoder.SetRangeEncoder].
//
// At 48 kHz, CELT frames contain 120, 240, 480, or 960 samples. In Hybrid mode,
// CELT starts at band 17; SILK carries the lower-frequency signal. The decoder
// also handles packet loss by evolving its band-energy and overlap history.
package celt
