//go:build gopus_fixed_point

package fixedpoint

// Integer KISS-FFT driver ported from libopus celt/kiss_fft.c under FIXED_POINT
// (without ENABLE_QEXT). This wires the radix butterflies in kiss_fft.go into a
// full forward/inverse integer FFT, reproducing compute_twiddles, kf_factor,
// compute_bitrev_table, fft_downshift and opus_fft_impl/opus_fft_c/opus_ifft_c
// bit-for-bit on OPUS_FAST_INT64 targets.
//
// celt_coef == opus_val16 (int16) and COEF_SHIFT == 16 in this build, so the
// FFT scale carried by KissFFTState is an int16 Q15 value.

// maxFactors mirrors libopus MAXFACTORS.
const maxFactors = 8

// KissFFTState is the integer KISS-FFT configuration, matching libopus
// kiss_fft_state in the FIXED_POINT (non-QEXT) build. Twiddles may be shared
// across a family of FFT sizes (a base table plus per-size shifts), exactly as
// the CELT MDCT sub-FFTs share kfft[0]->twiddles.
type KissFFTState struct {
	nfft       int
	scale      int16 // celt_coef, Q15
	scaleShift int
	shift      int // -1 for a standalone (top-level) FFT
	factors    [2 * maxFactors]int16
	bitrev     []int16
	twiddles   []FFTTwiddle
}

// Nfft returns the transform length.
func (st *KissFFTState) Nfft() int { return st.nfft }

// computeTwiddles fills twiddles[0:nfft] exactly as libopus compute_twiddles()
// does in the FIXED_POINT (non-QEXT) path: kf_cexp2(&tw[i], DIV32(SHL32(-i,17),
// nfft)), expanding kf_cexp2 to celt_cos_norm of the phase and phase-32768.
func computeTwiddles(twiddles []FFTTwiddle, nfft int) {
	for i := 0; i < nfft; i++ {
		phase := int32(-i)
		ph := shl32(phase, 17) / int32(nfft)
		twiddles[i].R = CeltCosNorm(ph)
		twiddles[i].I = CeltCosNorm(ph - 32768)
	}
}

// kfFactor reproduces libopus kf_factor(): it populates facbuf as p1,m1,p2,m2,...
// where p[i]*m[i]==m[i-1] and m0==n. It returns false if n cannot be factored
// into radices 2..5.
func kfFactor(n int, facbuf *[2 * maxFactors]int16) bool {
	p := 4
	stages := 0
	nbak := n
	for {
		for n%p != 0 {
			switch p {
			case 4:
				p = 2
			case 2:
				p = 3
			default:
				p += 2
			}
			if p > 32000 || p*p > n {
				p = n
			}
		}
		n /= p
		if p > 5 {
			return false
		}
		facbuf[2*stages] = int16(p)
		if p == 2 && stages > 1 {
			facbuf[2*stages] = 4
			facbuf[2] = 2
		}
		stages++
		if n <= 1 {
			break
		}
	}
	n = nbak
	// Reverse the factor order so the radix-4 stage lands at the end.
	for i := 0; i < stages/2; i++ {
		facbuf[2*i], facbuf[2*(stages-i-1)] = facbuf[2*(stages-i-1)], facbuf[2*i]
	}
	for i := 0; i < stages; i++ {
		n /= int(facbuf[2*i])
		facbuf[2*i+1] = int16(n)
	}
	return true
}

// computeBitrevTable reproduces libopus compute_bitrev_table(): it fills f with
// the bit-reversal permutation derived from the radix factors. fout is the
// running output offset, fpos indexes into f, and facpos indexes into factors.
func computeBitrevTable(fout int, f []int16, fpos, fstride, inStride int, factors []int16, facpos int) {
	p := int(factors[facpos]) // radix
	m := int(factors[facpos+1])
	facpos += 2
	if m == 1 {
		for j := 0; j < p; j++ {
			f[fpos] = int16(fout + j)
			fpos += fstride * inStride
		}
		return
	}
	for j := 0; j < p; j++ {
		computeBitrevTable(fout, f, fpos, fstride*p, inStride, factors, facpos)
		fpos += fstride * inStride
		fout += m
	}
}

// NewKissFFTState builds a standalone integer FFT state for nfft (st->shift==-1,
// owning its own twiddle table), matching opus_fft_alloc(nfft, ..., base=NULL)
// in the FIXED_POINT (non-QEXT) build. It returns nil if nfft is not factorable
// into radices 2..5.
func NewKissFFTState(nfft int) *KissFFTState {
	st := &KissFFTState{nfft: nfft}
	st.scaleShift = int(CeltILog2(int32(nfft)))
	if nfft == 1<<st.scaleShift {
		st.scale = Q15One
	} else {
		st.scale = int16((1073741824 + nfft/2) / nfft >> (15 - st.scaleShift))
	}
	st.shift = -1
	st.twiddles = make([]FFTTwiddle, nfft)
	computeTwiddles(st.twiddles, nfft)
	if !kfFactor(nfft, &st.factors) {
		return nil
	}
	st.bitrev = make([]int16, nfft)
	computeBitrevTable(0, st.bitrev, 0, 1, 1, st.factors[:], 0)
	return st
}

// NewKissFFTStateTwiddles builds an integer FFT state for nfft that shares the
// base state's twiddle table, matching opus_fft_alloc_twiddles(nfft, ...,
// base) in the FIXED_POINT (non-QEXT) build. This is how CELT MDCT sub-FFTs
// (kfft[1..]) reuse kfft[0]'s twiddles with a per-size shift. It returns nil if
// nfft is not factorable into radices 2..5, or no compatible shift exists.
func NewKissFFTStateTwiddles(nfft int, base *KissFFTState) *KissFFTState {
	st := &KissFFTState{nfft: nfft}
	st.scaleShift = int(CeltILog2(int32(nfft)))
	if nfft == 1<<st.scaleShift {
		st.scale = Q15One
	} else {
		st.scale = int16((1073741824 + nfft/2) / nfft >> (15 - st.scaleShift))
	}
	st.twiddles = base.twiddles
	st.shift = 0
	for st.shift < 32 && nfft<<st.shift != base.nfft {
		st.shift++
	}
	if st.shift >= 32 {
		return nil
	}
	if !kfFactor(nfft, &st.factors) {
		return nil
	}
	st.bitrev = make([]int16, nfft)
	computeBitrevTable(0, st.bitrev, 0, 1, 1, st.factors[:], 0)
	return st
}

// Q15One is libopus Q15ONE in the FIXED_POINT build (celt_coef == int16).
const Q15One int16 = 32767

// fftDownshift reproduces libopus fft_downshift(): it shifts the whole nfft-point
// buffer down by min(step, *total) bits, consuming that many from total. The
// shift==1 case uses SHR32 (truncating); larger shifts use PSHR32 (rounding).
func fftDownshift(x []FFTCpx, n int, total *int, step int) {
	shift := step
	if *total < shift {
		shift = *total
	}
	*total -= shift
	if shift == 1 {
		for i := 0; i < n; i++ {
			x[i].R >>= 1
			x[i].I >>= 1
		}
	} else if shift > 0 {
		for i := 0; i < n; i++ {
			x[i].R = pshr32(x[i].R, shift)
			x[i].I = pshr32(x[i].I, shift)
		}
	}
}

// opusFFTImpl reproduces libopus opus_fft_impl(): it walks the radix stages in
// reverse, applying the per-radix downshift then the butterfly, and a final
// downshift of the remaining budget. It operates in place on fout.
func opusFFTImpl(st *KissFFTState, fout []FFTCpx, downshift int) {
	shift := 0
	if st.shift > 0 {
		shift = st.shift
	}

	var fstride [maxFactors + 1]int
	fstride[0] = 1
	L := 0
	var m int
	for {
		p := int(st.factors[2*L])
		m = int(st.factors[2*L+1])
		fstride[L+1] = fstride[L] * p
		L++
		if m == 1 {
			break
		}
	}
	m = int(st.factors[2*L-1])
	for i := L - 1; i >= 0; i-- {
		var m2 int
		if i != 0 {
			m2 = int(st.factors[2*i-1])
		} else {
			m2 = 1
		}
		switch st.factors[2*i] {
		case 2:
			fftDownshift(fout, st.nfft, &downshift, 1)
			KFBfly2(fout, 0, fstride[i])
		case 4:
			fftDownshift(fout, st.nfft, &downshift, 2)
			KFBfly4(fout, 0, st.twiddles, fstride[i]<<shift, m, fstride[i], m2)
		case 3:
			fftDownshift(fout, st.nfft, &downshift, 2)
			KFBfly3(fout, 0, st.twiddles, fstride[i]<<shift, m, fstride[i], m2)
		case 5:
			fftDownshift(fout, st.nfft, &downshift, 3)
			KFBfly5(fout, 0, st.twiddles, fstride[i]<<shift, m, fstride[i], m2)
		}
		m = m2
	}
	fftDownshift(fout, st.nfft, &downshift, downshift)
}

// sMul2 implements libopus S_MUL2(a, b) == MULT16_32_Q16(b, a) on this build:
// (opus_val32)((opus_int64)(opus_val16)b * a >> 16), with b the int16 scale and
// a the int32 sample.
func sMul2(a int32, b int16) int32 {
	return int32((int64(b) * int64(a)) >> 16)
}

// OpusFFT reproduces libopus opus_fft_c(): bit-reverse and scale the input into
// fout, then run the in-place forward transform. fin and fout must be distinct
// and hold at least st.nfft elements.
func OpusFFT(st *KissFFTState, fin, fout []FFTCpx) {
	scale := st.scale
	scaleShift := st.scaleShift - 1
	for i := 0; i < st.nfft; i++ {
		x := fin[i]
		b := st.bitrev[i]
		fout[b].R = sMul2(x.R, scale)
		fout[b].I = sMul2(x.I, scale)
	}
	opusFFTImpl(st, fout, scaleShift)
}

// OpusIFFT reproduces libopus opus_ifft_c(): bit-reverse the input into fout,
// conjugate, run the in-place transform with no downshift, and conjugate back.
// fin and fout must be distinct and hold at least st.nfft elements.
func OpusIFFT(st *KissFFTState, fin, fout []FFTCpx) {
	for i := 0; i < st.nfft; i++ {
		fout[st.bitrev[i]] = fin[i]
	}
	for i := 0; i < st.nfft; i++ {
		fout[i].I = -fout[i].I
	}
	opusFFTImpl(st, fout, 0)
	for i := 0; i < st.nfft; i++ {
		fout[i].I = -fout[i].I
	}
}
