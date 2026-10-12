//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// QEXTKissFFTState is one Q31 KISS-FFT configuration from a QEXT CELT mode.
// Static modes share pinned tables; custom modes generate them at construction.
type QEXTKissFFTState struct {
	nfft       int
	scale      int32
	scaleShift int
	shift      int
	factors    [2 * maxFactors]int16
	bitrev     []int16
	twiddles   []QEXTFFTTwiddle
}

// Nfft returns the transform length.
func (st *QEXTKissFFTState) Nfft() int { return st.nfft }

// OpusFFT reproduces opus_fft_c() for a QEXT KISS configuration.
// fin and fout are caller-owned buffers and must each hold nfft samples.
func (st *QEXTKissFFTState) OpusFFT(fin, fout []FFTCpx) {
	for i, rev := range st.bitrev {
		x := fin[i]
		fout[rev] = FFTCpx{R: qextMul(x.R, st.scale), I: qextMul(x.I, st.scale)}
	}
	qextOpusFFTImpl(st, fout, st.scaleShift-1)
}

func qextFFTDownshift(x []FFTCpx, n int, total *int, step int) {
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

// qextOpusFFTImpl mirrors opus_fft_impl() with the ENABLE_QEXT Q31 butterflies.
func qextOpusFFTImpl(st *QEXTKissFFTState, fout []FFTCpx, downshift int) {
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
		m2 := 1
		if i != 0 {
			m2 = int(st.factors[2*i-1])
		}
		switch st.factors[2*i] {
		case 2:
			qextFFTDownshift(fout, st.nfft, &downshift, 1)
			if m == 1 {
				kfBfly2CustomM1(fout, fstride[i])
			} else {
				qextKFBfly2(fout, 0, fstride[i])
			}
		case 4:
			qextFFTDownshift(fout, st.nfft, &downshift, 2)
			qextKFBfly4(fout, 0, st.twiddles, fstride[i]<<shift, m, fstride[i], m2)
		case 3:
			qextFFTDownshift(fout, st.nfft, &downshift, 2)
			qextKFBfly3(fout, 0, st.twiddles, fstride[i]<<shift, m, fstride[i], m2)
		case 5:
			qextFFTDownshift(fout, st.nfft, &downshift, 3)
			qextKFBfly5(fout, 0, st.twiddles, fstride[i]<<shift, m, fstride[i], m2)
		}
		m = m2
	}
	qextFFTDownshift(fout, st.nfft, &downshift, downshift)
}

// QEXTMDCTLookup holds a Q31 MDCT mode table and its sub-FFT states.
type QEXTMDCTLookup struct {
	n        int
	maxshift int
	kfft     []*QEXTKissFFTState
	trig     []int32
	window   []int32
}

// N returns the full (shift==0) MDCT length.
func (l *QEXTMDCTLookup) N() int { return l.n }

// Window returns the mode's overlap window in Q31.
func (l *QEXTMDCTLookup) Window() []int32 { return l.window }

// staticQEXTMDCTLookup48000 owns the immutable ENABLE_QEXT tables from the
// pinned libopus 1.6.1 48000/960 static mode data.
var staticQEXTMDCTLookup48000 = buildStaticQEXTMDCTLookup48000()

// buildStaticQEXTMDCTLookup48000 creates the lookup once during package init.
func buildStaticQEXTMDCTLookup48000() *QEXTMDCTLookup {
	l := &QEXTMDCTLookup{
		n:        staticMDCT48000N,
		maxshift: staticMDCT48000MaxShift,
		kfft:     make([]*QEXTKissFFTState, len(staticMDCT48000KFFT)),
		trig:     staticQEXTMDCT48000Trig[:],
		window:   staticQEXTMDCT48000Window[:],
	}
	for i, base := range staticMDCT48000KFFT {
		l.kfft[i] = &QEXTKissFFTState{
			nfft:       base.nfft,
			scale:      572662306,
			scaleShift: base.scaleShift,
			shift:      base.shift,
			factors:    base.factors,
			bitrev:     base.bitrev,
			twiddles:   staticQEXTMDCT48000Twiddles[:],
		}
	}
	return l
}

// NewStaticQEXTMDCTLookup48000 returns the shared static 48000/960 mode lookup.
func NewStaticQEXTMDCTLookup48000() *QEXTMDCTLookup {
	return staticQEXTMDCTLookup48000
}

// StaticQEXTCELT48000FFTState returns the baked 480-point analysis FFT state
// used by the 48 kHz CELT mode.
func StaticQEXTCELT48000FFTState() *QEXTKissFFTState {
	return staticQEXTMDCTLookup48000.kfft[0]
}
