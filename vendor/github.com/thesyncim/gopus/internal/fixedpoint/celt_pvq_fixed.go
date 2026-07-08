//go:build gopus_fixed_point

package fixedpoint

// This file ports the integer CELT FIXED_POINT PVQ pulse search,
// op_pvq_search_c from celt/vq.c. The search greedily places K unit pulses to
// maximise the projection xy^2/yy of the candidate codeword onto the input
// X, working entirely in the integer domain (no range coder). It is the kernel
// shared by alg_quant; alg_quant/alg_unquant themselves are not ported here as
// they couple to the entropy coder.
//
// Type model (matches celt/arch.h FIXED_POINT): celt_norm and opus_val32 are
// int32, opus_val16 is int16, opus_val64 is int64. NORM_SHIFT is 24. The
// FIXED_POINT macros that cast operands down to int16 before multiplying
// (MULT16_16, MAC16_16, MULT16_16_Q15, MULT16_32_Q16, EXTRACT16) are reproduced
// exactly, including the int16 truncation, because the search relies on X/y
// having been scaled into the int16 range first.

// celtInnerProdNormShift ports celt/vq.c celt_inner_prod_norm_shift: it
// accumulates x[i]*y[i] in a 64-bit sum then shifts right by 2*(NORM_SHIFT-14).
func celtInnerProdNormShift(x, y []int32, n int) int32 {
	var sum int64
	for i := 0; i < n; i++ {
		sum += int64(x[i]) * int64(y[i])
	}
	return int32(sum >> (2 * (normShift - 14)))
}

// normScaledown ports celt/vq.c norm_scaledown: PSHR32 each element when
// shift > 0, otherwise a no-op.
func normScaledown(x []int32, n, shift int) {
	if shift <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		x[i] = pshr32(x[i], shift)
	}
}

// mac16x16 ports MULT16_16/MAC16_16: both factors are first truncated to int16,
// multiplied as int32, and added to the accumulator.
func mac16x16(c, a, b int32) int32 {
	return c + int32(int16(a))*int32(int16(b))
}

// mult16x16 ports MULT16_16: int16 truncation of both factors, int32 product.
func mult16x16(a, b int32) int32 {
	return int32(int16(a)) * int32(int16(b))
}

// mult16x16Q15i32 ports MULT16_16_Q15 over the int32 macro inputs used in the
// search: int16-truncate, multiply, arithmetic shift right by 15.
func mult16x16Q15i32(a, b int32) int32 {
	return (int32(int16(a)) * int32(int16(b))) >> 15
}

// mult16x32Q16 ports MULT16_32_Q16 (OPUS_FAST_INT64 path): the first factor is
// truncated to int16, multiplied by the 32-bit second factor in 64 bits, then
// arithmetic shifted right by 16.
func mult16x32Q16(a, b int32) int32 {
	return int32((int64(int16(a)) * int64(b)) >> 16)
}

// OpPvqSearch ports celt/vq.c op_pvq_search_c (FIXED_POINT path). It searches
// for the K-pulse PVQ codeword that best matches X, writing the integer pulse
// counts (with their original signs restored) into iy and returning yy, the
// squared norm of the codeword (y, internally scaled by 2). X is modified
// in place: it is first scaled down to the int16 working range, then its
// absolute value is taken. iy must have length >= N (alg_quant allocates N+3
// for vectorisation headroom; this search only touches indices [0,N)).
func OpPvqSearch(x []int32, iy []int, k, n int, scratch *celtEncodeScratch) int32 {
	var y []int32
	var signx []bool
	if scratch != nil {
		y = ensureInt32(&scratch.pvqY, n)
		signx = ensureBool(&scratch.pvqSignx, n)
	} else {
		y = make([]int32, n)
		signx = make([]bool, n)
	}

	{
		shift := (int(CeltILog2(1+celtInnerProdNormShift(x, x, n))) + 1) / 2
		shift = imax(0, shift+(normShift-14)-14)
		normScaledown(x, n, shift)
	}

	// Strip the sign and clear the working vectors.
	var sum int32
	for j := 0; j < n; j++ {
		signx[j] = x[j] < 0
		if x[j] < 0 {
			x[j] = -x[j]
		}
		iy[j] = 0
		y[j] = 0
	}

	// xy/best_num are opus_val32; yy/Rxy/Ryy/best_den are opus_val16 (int16),
	// so every assignment to them truncates to int16 exactly as the C does.
	var xy int32
	var yy int16

	pulsesLeft := k

	// Pre-search by projecting on the pyramid when K is large.
	if k > (n >> 1) {
		for j := 0; j < n; j++ {
			sum += x[j]
		}
		// If X is too small, replace it with a single pulse at index 0.
		if sum <= int32(k) {
			x[0] = 1 << 14 // QCONST16(1.f, 14)
			for j := 1; j < n; j++ {
				x[j] = 0
			}
			sum = 1 << 14
		}
		rcp := int32(int16(mult16x32Q16(int32(k), CeltRcp(sum))))
		for j := 0; j < n; j++ {
			// Round towards zero.
			iy[j] = int(mult16x16Q15i32(x[j], rcp))
			y[j] = int32(iy[j])
			yy = int16(mac16x16(int32(yy), y[j], y[j]))
			xy = mac16x16(xy, x[j], y[j])
			y[j] *= 2
			pulsesLeft -= iy[j]
		}
	}

	// On silence the projection may leave too many pulses; dump them into bin 0.
	if pulsesLeft > n+3 {
		tmp := int32(pulsesLeft)
		yy = int16(mac16x16(int32(yy), tmp, tmp))
		yy = int16(mac16x16(int32(yy), tmp, y[0]))
		iy[0] += pulsesLeft
		pulsesLeft = 0
	}

	for i := 0; i < pulsesLeft; i++ {
		rshift := 1 + int(CeltILog2(int32(k-pulsesLeft+i+1)))

		bestID := 0
		// The squared-magnitude term is added unconditionally, so hoist it.
		yy = add16s(yy, 1)

		// Position 0 handled out of the loop to reduce mispredicted branches.
		rxy := int16(shr32(add32(xy, x[0]), rshift))
		ryy := add16s(yy, int16(y[0]))
		rxy = mult16x16q15(rxy, rxy)
		bestDen := ryy
		bestNum := int32(rxy)

		for j := 1; j < n; j++ {
			rxy := int16(shr32(add32(xy, x[j]), rshift))
			ryy := add16s(yy, int16(y[j]))
			rxy = mult16x16q15(rxy, rxy)
			// Compare num/den >= best_num/best_den without division.
			if mult16x16(int32(bestDen), int32(rxy)) > mult16x16(int32(ryy), bestNum) {
				bestDen = ryy
				bestNum = int32(rxy)
				bestID = j
			}
		}

		xy = add32(xy, x[bestID])
		yy = add16s(yy, int16(y[bestID]))

		y[bestID] += 2
		iy[bestID]++
	}

	// Restore the original signs.
	for j := 0; j < n; j++ {
		if signx[j] {
			iy[j] = -iy[j]
		}
	}

	// The C return type is opus_val16; the caller sign-extends it to int32.
	return int32(yy)
}

// shr32 ports SHR32(a, shift): arithmetic right shift, shift >= 0.
func shr32(a int32, shift int) int32 {
	return a >> shift
}
