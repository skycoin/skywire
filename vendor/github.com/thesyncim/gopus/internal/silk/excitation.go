package silk

// scaleExcitation applies a Q16 gain to an excitation signal for one subframe,
// scaling to Q0 (PCM units) in place. This is a standalone helper used by unit
// tests; the bit-exact decode path applies gains inside silkDecodeCore
// (silk/decode_core.c) rather than calling this.
func scaleExcitation(excitation []int32, gain int32) {
	for i := range excitation {
		// Multiply by Q16 gain and return to Q0.
		excitation[i] = (excitation[i] * gain) >> 16
	}
}
