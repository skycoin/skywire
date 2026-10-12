//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

// plcLPCReflectionSumOrdered matches the two float _celt_lpc accumulation
// paths in celt/celt_lpc.c. The v3 scalar reference rounds each product and
// addition. The selected x86 SIMD reference accumulates complete four-term
// groups from rounded products, then contracts its final zero-to-three terms.
func plcLPCReflectionSumOrdered(lpc, ac []float32, i int) float32 {
	if !libopusFloatInnerProdUsesSSEOrder {
		return plcLPCReflectionSum(lpc, ac, i)
	}

	prefix := i &^ 3
	rr := float32(0)
	for j := 0; j < prefix; j++ {
		product := mul32(lpc[j], ac[i-j])
		rr = add32(rr, product)
	}
	for j := prefix; j < i; j++ {
		rr = plcLPCFMA32(lpc[j], ac[i-j], rr)
	}
	return rr
}

// celt/celt_lpc.c::_celt_lpc evaluates r*r, then error*(r*r), then subtracts
// that rounded product from error in the pinned float build.
func plcLPCErrorPowerUpdate32(r, errorPower float32) float32 {
	square := mul32(r, r)
	decay := mul32(square, errorPower)
	return sub32(errorPower, decay)
}

// celt/celt_lpc.c::_celt_lpc uses scalar FMAs for the final SIMD remainder.
// Keep this register boundary for the pinned GCC 13.3 x86-v3 float caller:
// stack-backed inline operands otherwise lower to separate MULSS and ADDSS.
//
//go:noinline
func plcLPCFMA32(a, b, c float32) float32 {
	return a*b + c
}
