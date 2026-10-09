// Package silk implements the SILK speech layer in RFC 6716 Section 4.2. It
// codes and reconstructs predictive speech frames at 8, 12, or 16 kHz, including
// stereo prediction, in-band FEC (LBRR), resampling, and speech concealment.
//
// [Encoder] and [Decoder] retain predictor, quantizer, and resampler history
// across frames. Keep their state with one stream and serialize access. Hybrid
// mode reuses the stream's SILK decoder across mode transitions and passes one
// range decoder through SILK, then CELT; [Decoder.DecodeFrameRaw] is the
// lower-level SILK entry point for that path.
//
// Fixed-point builds preserve SILK's Q-format widths, shifts, saturation, and
// state updates on identical inputs. Floating-point kernels are compared with
// the matching libopus build and target. Exact guarantees and exercised cases
// are recorded in `reports/validation.md#coverage`. The matching C sources are
// in libopus 1.6.1's `silk/` directory, especially `dec_API.c`, `decode_frame.c`,
// `decode_core.c`, and `PLC.c`.
//
// Most callers should use the top-level gopus encoder and decoder APIs. The
// package surface is an internal codec interface and may change before v1.
package silk
