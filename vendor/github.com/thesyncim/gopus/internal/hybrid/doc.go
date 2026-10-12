// Package hybrid implements the Opus Hybrid-mode decoder. Hybrid frames combine
// a wideband SILK low band with CELT bands starting at band 17 and ending at
// the active bandwidth limit, using one range decoder for both layers.
//
// [Decoder] uses separate SILK and CELT decoder state for one stream. It
// resamples SILK to the configured API rate and combines it with CELT output
// configured for that same rate. Serialize access to a decoder. This
// implementation accepts 10 ms and 20 ms encoded Hybrid frames.
package hybrid
