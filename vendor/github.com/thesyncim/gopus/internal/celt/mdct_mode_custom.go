//go:build gopus_custom_modes

package celt

// CustomMDCTTables owns the immutable transforms of a nonstandard CELT mode.
// Its shorter FFTs share the largest FFT's twiddles, as in clt_mdct_init.
type CustomMDCTTables struct {
	blocks [4]mdctTransformLookup
}

// NewCustomMDCTTables constructs the dynamic mode tables once. Standard Opus
// modes use their frozen tables and do not need a CustomMDCTTables value.
func NewCustomMDCTTables(frameSize, maxLM int, window []float32) *CustomMDCTTables {
	if frameSize <= 0 || maxLM < 0 || maxLM >= 4 {
		return nil
	}
	tables := &CustomMDCTTables{}
	window = append([]float32(nil), window...)
	var base *kissFFTState
	for shift := 0; shift <= maxLM; shift++ {
		n := (2 * frameSize) >> shift
		fft := newDynamicKissFFTState(n/4, base)
		if fft == nil || len(fft.bitrev) != n/4 {
			return nil
		}
		if base == nil {
			base = fft
		}
		tables.blocks[shift] = mdctTransformLookup{n: n, trig: buildMDCTTrigF32(n), window: window, fft: fft}
	}
	return tables
}

type customMDCTState struct {
	customTransforms *CustomMDCTTables
}

func (s *customMDCTState) mdctLookup(n int) *mdctTransformLookup {
	if s.customTransforms != nil {
		for i := range s.customTransforms.blocks {
			block := &s.customTransforms.blocks[i]
			if block.n == n {
				return block
			}
		}
	}
	return nil
}

func (s *customMDCTState) modeWindow(overlap int) []float32 {
	if s.customTransforms != nil {
		window := s.customTransforms.blocks[0].window
		if len(window) == overlap {
			return window
		}
	}
	return GetWindowBufferF32(overlap)
}
