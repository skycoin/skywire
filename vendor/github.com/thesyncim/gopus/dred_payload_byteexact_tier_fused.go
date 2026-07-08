//go:build !purego && !amd64

package gopus

// dredPayloadByteExactTier is false on the fused/SIMD non-amd64 build (the
// default arm64 NEON build): DRED RDOVAE feature extraction is float, and the
// quality-gated fused kernels round differently than the scalar reference across
// Apple-Silicon/clang NEON revisions. A 1-ULP feature drift selects a different
// latent quantization, so the emitted DRED extension bytes are not guaranteed to
// match the libopus reference byte-for-byte across arm64 runners.
//
// Byte-exact DRED oracles therefore belong on the bit-exact tier (amd64 and the
// purego build); on this tier the carried-DRED encoder-parity tests assert the
// weaker but still meaningful invariant that the emitted payload parses, carries
// the same chunk/latent structure as libopus, and round-trips through the DRED
// decoder.
const dredPayloadByteExactTier = false
