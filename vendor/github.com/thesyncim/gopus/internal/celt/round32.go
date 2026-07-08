package celt

// round32 forces x to float32 precision. Go's arm64 backend may contract a*b+c
// into a single FMADD (one rounding), which diverges from scalar libopus (two
// roundings); wrapping the product as round32(a*b) materializes it at float32
// precision so the surrounding add/sub cannot fuse, matching the scalar
// reference on every build. It is the cheap barrier — an FMUL+FADD pair rather
// than the FMUL+FMOV+FMOV+FADD of a Float32bits round-trip — and a no-op on
// amd64 and the purego oracle, which do not contract FP. Keep this tiny; its
// fusion-defeating codegen is guarded by TestRound32DefeatsFusion.
func round32(x float32) float32 {
	return float32(x)
}

// mulAdd32Ref, mulSub32Ref, and subMul32Ref are the scalar-C-semantics
// multiply-accumulate forms: the product rounds to float32 before the add/sub,
// so no FMADD/FMSUB forms across the boundary.
func mulAdd32Ref(a, b, c float32) float32 { return round32(a*b) + c }

func mulSub32Ref(a, b, c float32) float32 { return round32(a*b) - c }

func subMul32Ref(c, a, b float32) float32 { return c - round32(a*b) }

// fma32 computes a*b+c and lets the arm64 backend contract it into a single
// FMADD where the code explicitly wants fusion (the kiss-FFT twiddle hot path);
// amd64 does not contract FP. mul32/add32/sub32 are the non-fused-intent
// primitives: routing through round32 keeps a materialized product from fusing
// with a surrounding op, matching scalar libopus on every build at FMUL+FADD
// cost, and a no-op on amd64/purego where there is no contraction.
func fma32(a, b, c float32) float32 { return a*b + c }

func mul32(a, b float32) float32 { return round32(a * b) }

func add32(a, b float32) float32 { return round32(a + b) }

func sub32(a, b float32) float32 { return round32(a - b) }
