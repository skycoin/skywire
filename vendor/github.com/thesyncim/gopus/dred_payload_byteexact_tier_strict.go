//go:build purego || amd64

package gopus

// dredPayloadByteExactTier selects whether the carried-DRED encoder-parity tests
// hold gopus byte-exactly equal to the libopus reference payload.
//
// This is the bit-exact tier: the pure-Go build (-tags purego, any arch) and the
// amd64 SIMD build both track libopus's DRED RDOVAE float feature extraction and
// latent quantization closely enough that the emitted DRED extension bytes match
// exactly. On this tier the parity tests assert full byte equality.
const dredPayloadByteExactTier = true
