//go:build gopus_qext

package celt

// HD96kMode is the native 96 kHz CELT mode used by Opus HD / QEXT. It mirrors
// libopus's static mode96000_1920_240 (opus_custom_mode_create(96000, 1920)),
// the only 96 kHz mode in the QEXT-enabled static_mode_list.
//
// Relative to the 48 kHz fullband mode (mode48000_960_120):
//   - shortMdctSize, overlap, and the long-block MDCT length all double:
//     overlap 120->240, shortMdctSize 120->240, long MDCT N 1920->3840.
//   - eBands and logN are unchanged: the pseudo-critical band layout (eBand5ms)
//     and per-band logN are shared, so 96 kHz simply spreads the same 21 bands
//     over twice as many MDCT bins.
//   - preemph differs (96 kHz coefficients).
//   - nbShortMdcts (8) and maxLM (3) are unchanged.
//
// The MDCT twiddle and window tables are the doubled-length variants; both are
// computed here from the same closed forms libopus uses in clt_mdct_init and
// modes.c, and are byte/numeric-verified against the libopus qext oracle in
// mode_hd96k_qext_test.go.
type HD96kMode struct {
	Fs            int
	Overlap       int
	NbEBands      int
	EffEBands     int
	MaxLM         int
	NbShortMdcts  int
	ShortMdctSize int
	Preemph       [4]float32
	EBands        []int16
	LogN          []int16
	Window        []float32
	// MdctN is the long-block MDCT length (frame_size*2 = 3840 for 20 ms).
	MdctN int
	// MdctMaxShift is the largest short-block shift (3 => smallest block is N>>3).
	MdctMaxShift int
	// MdctTrig is the concatenation of the per-shift trig segments, exactly as
	// libopus lays out mdct_lookup.trig: segment s has N(s)/2 entries with
	// N(s) = MdctN >> s, for s in [0, MdctMaxShift].
	MdctTrig []float32
}

// hd96kEBands mirrors libopus eBand5ms (shared with the 48 kHz mode).
var hd96kEBands = func() []int16 {
	b := make([]int16, len(EBands))
	for i, v := range EBands {
		b[i] = int16(v)
	}
	return b
}()

// hd96kLogN mirrors libopus logN400 (shared with the 48 kHz mode).
var hd96kLogN = func() []int16 {
	b := make([]int16, len(LogN))
	for i, v := range LogN {
		b[i] = int16(v)
	}
	return b
}()

// hd96kWindow is the overlap=240 Vorbis window (libopus window240).
var hd96kWindow = func() []float32 {
	w := make([]float32, 240)
	for i := range w {
		w[i] = VorbisWindow(i, 240)
	}
	return w
}()

// hd96kMdctTrig builds the concatenated MDCT twiddle table for the native
// 96 kHz mode (libopus mdct_twiddles1920[3600]). For each shift s the segment
// holds N>>s>>1 cosine values trig[i] = cos(2*pi*(i+0.125)/(N>>s)), matching
// clt_mdct_init's per-shift loop and gopus's buildMDCTTrigF32.
var hd96kMdctTrig = func() []float32 {
	const n = 3840
	const maxShift = 3
	total := n - ((n / 2) >> maxShift)
	trig := make([]float32, 0, total)
	for shift := 0; shift <= maxShift; shift++ {
		trig = append(trig, buildMDCTTrigF32(n>>shift)...)
	}
	return trig
}()

// NewHD96kMode returns the native 96 kHz CELT mode definition.
func NewHD96kMode() HD96kMode {
	return HD96kMode{
		Fs:            96000,
		Overlap:       240,
		NbEBands:      MaxBands,
		EffEBands:     MaxBands,
		MaxLM:         3,
		NbShortMdcts:  8,
		ShortMdctSize: 240,
		Preemph:       [4]float32{0.9230041504, 0.2200012207, 1.5128347184, 0.6610107422},
		EBands:        hd96kEBands,
		LogN:          hd96kLogN,
		Window:        hd96kWindow,
		MdctN:         3840,
		MdctMaxShift:  3,
		MdctTrig:      hd96kMdctTrig,
	}
}
