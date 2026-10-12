package celt

// deemphasisVerySmall is libopus VERY_SMALL (1e-30f), which deemphasis() adds to
// every sample so the IIR state never decays into denormals. It is added on
// every frame, including silence, so a silent frame decodes to
// VERY_SMALL/(1-coef)/CELT_SIG_SCALE rather than exact zero.
const deemphasisVerySmall float32 = 1e-30

// sig2res is libopus SIG2RES in the float build: 1/CELT_SIG_SCALE.
const sig2res float32 = 1.0 / 32768.0

// deemphasisLoopConsts holds deemphasisVerySmall and sig2res as variables. The
// hot deemphasis loops read them once into registers; Go reloads constant
// float operands from memory on every iteration.
var deemphasisLoopConsts = [2]float32{deemphasisVerySmall, sig2res}

// deemphCoefficient returns the first de-emphasis tap (mode->preemph[0]) of the
// active mode. Zero d.deemphCoef selects the 48 kHz PreemphCoef.
func (d *Decoder) deemphCoefficient() float32 {
	if d.deemphCoef != 0 {
		return d.deemphCoef
	}
	return float32(PreemphCoef)
}

// deemphasis mirrors libopus celt/celt_decoder.c deemphasis(). x0 and x1 hold
// the synthesized signal (out_syn) of channels 0 and 1 with sample stride
// xStride; x1 is unused for a mono decoder. n is the frame length at the
// internal rate. pcm receives n/downsample interleaved output frames. With
// accum the output is added onto pcm (libopus celt_accum, used for the Hybrid
// highband and the Hybrid->SILK fade-out frame). pcm may alias x0/x1 when the
// input is interleaved with stride d.channels and downsample is 1.
func (d *Decoder) deemphasis(pcm []float32, x0, x1 []float32, xStride, n, downsample int, accum bool) {
	if n <= 0 {
		return
	}
	downsample = max(downsample, 1)
	channels := int(d.channels)
	coef0 := d.deemphCoefficient()
	if channels == 2 && d.deemphCoef1 == 0 {
		d.preemphState[0], d.preemphState[1] = deemphasisStereo(pcm, x0, x1, xStride, n, downsample, coef0, d.preemphState[0], d.preemphState[1], accum)
		return
	}
	for c := range channels {
		x := x0
		if c == 1 {
			x = x1
		}
		m := d.preemphState[c]
		if d.deemphCoef1 != 0 {
			m = deemphasis2TapChannel(pcm[c:], channels, x, xStride, n, downsample, coef0, d.deemphCoef1, d.deemphCoef3, m, accum)
		} else {
			m = deemphasisChannel(pcm[c:], channels, x, xStride, n, downsample, coef0, m, accum)
		}
		d.preemphState[c] = m
	}
}

// deemphasisInterleaved runs deemphasis over interleaved synthesis samples
// (frameSize frames at the internal rate). When the caller supplied an output
// buffer (directOutPCM) the result goes there and deemphasisInterleaved
// returns nil. Otherwise the samples are de-emphasized in place and returned.
func (d *Decoder) deemphasisInterleaved(samples []float32, frameSize int) []float32 {
	if d.directOutPCM != nil {
		d.deemphasisInterleavedTo(d.directOutPCM, samples, frameSize, d.directOutAccum)
		return nil
	}
	d.deemphasisInterleavedTo(samples, samples, frameSize, false)
	return samples[:frameSize*int(d.channels)]
}

// deemphasisInterleavedTo de-emphasizes interleaved synthesis samples into pcm,
// downsampling when pcm is sized for the API rate.
func (d *Decoder) deemphasisInterleavedTo(pcm, samples []float32, frameSize int, accum bool) {
	x1 := samples
	if d.channels == 2 {
		x1 = samples[1:]
	}
	d.deemphasis(pcm, samples, x1, int(d.channels), frameSize, d.outputDownsample(pcm, frameSize), accum)
}

// outputDownsample returns the deemphasis downsampling factor for writing a
// frameSize-sample internal frame into pcm: 1 when pcm holds the full
// internal-rate frame, otherwise the decoder's API-rate factor.
func (d *Decoder) outputDownsample(pcm []float32, frameSize int) int {
	if len(pcm) >= frameSize*int(d.channels) {
		return 1
	}
	return d.downsampleFactor()
}

// deemphasisChannel is the single-tap (mode->preemph[1] == 0) deemphasis of one
// channel and returns the updated filter memory. The three loops are the
// libopus branches: downsampling (tmp = x + VERY_SMALL + m into scratch, then
// decimate), accumulation (tmp = x + m + VERY_SMALL, y += SIG2RES(tmp)) and the
// plain path (tmp = x + VERY_SMALL + m, y = SIG2RES(tmp)). mul32 keeps
// m = coef*tmp a separately rounded product, as it is its own C statement. The
// downsampling loop emits scratch[j*downsample] as soon as it is computed,
// which yields the same values as libopus' separate decimation pass.
func deemphasisChannel(y []float32, yStride int, x []float32, xStride, n, downsample int, coef, m float32, accum bool) float32 {
	if downsample > 1 {
		nd := n / downsample
		if downsample == 2 && xStride == 1 && yStride == 1 {
			return deemphasisDownsample2(y[:nd], x[:n], coef, m, accum)
		}
		if nd > 0 {
			_ = y[(nd-1)*yStride]
		}
		_ = x[(n-1)*xStride]
		j := 0
		for o := range nd {
			tmp := x[j*xStride] + deemphasisVerySmall + m
			m = mul32(coef, tmp)
			if accum {
				y[o*yStride] = fma32(sig2res, tmp, y[o*yStride])
			} else {
				y[o*yStride] = sig2res * tmp
			}
			j++
			for end := j + downsample - 1; j < end; j++ {
				m = mul32(coef, x[j*xStride]+deemphasisVerySmall+m)
			}
		}
		for ; j < n; j++ {
			m = mul32(coef, x[j*xStride]+deemphasisVerySmall+m)
		}
		return m
	}
	_ = x[(n-1)*xStride]
	_ = y[(n-1)*yStride]
	if accum {
		if xStride == 1 && yStride == 1 {
			x = x[:n:n]
			y = y[:n:n]
			for j := range x {
				tmp := x[j] + m + deemphasisVerySmall
				m = mul32(coef, tmp)
				y[j] = fma32(sig2res, tmp, y[j])
			}
			return m
		}
		for j := range n {
			tmp := x[j*xStride] + m + deemphasisVerySmall
			m = mul32(coef, tmp)
			y[j*yStride] = fma32(sig2res, tmp, y[j*yStride])
		}
		return m
	}
	if xStride == 1 && yStride == 1 {
		x = x[:n:n]
		y = y[:n:n]
		verySmall, scale := deemphasisLoopConsts[0], deemphasisLoopConsts[1]
		for j := range x {
			tmp := x[j] + verySmall + m
			m = mul32(coef, tmp)
			y[j] = scale * tmp
		}
		return m
	}
	for j := range n {
		tmp := x[j*xStride] + deemphasisVerySmall + m
		m = mul32(coef, tmp)
		y[j*yStride] = sig2res * tmp
	}
	return m
}

// deemphasisDownsample2 is the downsample == 2 branch of deemphasisChannel for
// contiguous x and y: y[o] takes the first sample of each input pair, and the
// filter memory runs over every input.
func deemphasisDownsample2(y, x []float32, coef, m float32, accum bool) float32 {
	pairs := x[:2*len(y)]
	if accum {
		for o := range y {
			p := pairs[2*o : 2*o+2 : 2*o+2]
			tmp := p[0] + deemphasisVerySmall + m
			m = mul32(coef, tmp)
			y[o] = fma32(sig2res, tmp, y[o])
			m = mul32(coef, p[1]+deemphasisVerySmall+m)
		}
	} else {
		for o := range y {
			p := pairs[2*o : 2*o+2 : 2*o+2]
			tmp := p[0] + deemphasisVerySmall + m
			m = mul32(coef, tmp)
			y[o] = sig2res * tmp
			m = mul32(coef, p[1]+deemphasisVerySmall+m)
		}
	}
	for _, v := range x[len(pairs):] {
		m = mul32(coef, v+deemphasisVerySmall+m)
	}
	return m
}

// deemphasisStereo runs the single-tap deemphasis of both channels in one
// loop, writing interleaved output into y. libopus does this only for
// deemphasis_stereo_simple() (no downsampling, no accumulation); the other
// branches run each channel on its own. Every channel keeps exactly the
// per-sample operations of deemphasisChannel, so interleaving the two
// independent recurrences changes only instruction scheduling.
func deemphasisStereo(y []float32, x0, x1 []float32, xStride, n, downsample int, coef, m0, m1 float32, accum bool) (float32, float32) {
	_ = x0[(n-1)*xStride]
	_ = x1[(n-1)*xStride]
	if downsample > 1 {
		nd := n / downsample
		if downsample == 2 && xStride == 1 {
			return deemphasisStereoDownsample2(y[:2*nd], x0[:n], x1[:n], coef, m0, m1, accum)
		}
		if nd > 0 {
			_ = y[2*nd-1]
		}
		j := 0
		for o := range nd {
			tmp0 := x0[j*xStride] + deemphasisVerySmall + m0
			tmp1 := x1[j*xStride] + deemphasisVerySmall + m1
			m0 = mul32(coef, tmp0)
			m1 = mul32(coef, tmp1)
			if accum {
				y[2*o] = fma32(sig2res, tmp0, y[2*o])
				y[2*o+1] = fma32(sig2res, tmp1, y[2*o+1])
			} else {
				y[2*o] = sig2res * tmp0
				y[2*o+1] = sig2res * tmp1
			}
			j++
			for end := j + downsample - 1; j < end; j++ {
				m0 = mul32(coef, x0[j*xStride]+deemphasisVerySmall+m0)
				m1 = mul32(coef, x1[j*xStride]+deemphasisVerySmall+m1)
			}
		}
		for ; j < n; j++ {
			m0 = mul32(coef, x0[j*xStride]+deemphasisVerySmall+m0)
			m1 = mul32(coef, x1[j*xStride]+deemphasisVerySmall+m1)
		}
		return m0, m1
	}
	y = y[:2*n]
	if accum {
		for j := range n {
			tmp0 := x0[j*xStride] + m0 + deemphasisVerySmall
			tmp1 := x1[j*xStride] + m1 + deemphasisVerySmall
			m0 = mul32(coef, tmp0)
			m1 = mul32(coef, tmp1)
			y[2*j] = fma32(sig2res, tmp0, y[2*j])
			y[2*j+1] = fma32(sig2res, tmp1, y[2*j+1])
		}
		return m0, m1
	}
	if xStride == 1 {
		x0 = x0[:n:n]
		x1 = x1[:n:n]
		verySmall, scale := deemphasisLoopConsts[0], deemphasisLoopConsts[1]
		for k := 0; k+1 < len(y); k += 2 {
			j := k >> 1
			tmp0 := x0[j] + verySmall + m0
			tmp1 := x1[j] + verySmall + m1
			m0 = mul32(coef, tmp0)
			m1 = mul32(coef, tmp1)
			y[k] = scale * tmp0
			y[k+1] = scale * tmp1
		}
		return m0, m1
	}
	for j := range n {
		tmp0 := x0[j*xStride] + deemphasisVerySmall + m0
		tmp1 := x1[j*xStride] + deemphasisVerySmall + m1
		m0 = mul32(coef, tmp0)
		m1 = mul32(coef, tmp1)
		y[2*j] = sig2res * tmp0
		y[2*j+1] = sig2res * tmp1
	}
	return m0, m1
}

// deemphasisStereoDownsample2 is the downsample == 2 branch of
// deemphasisStereo for contiguous x0 and x1, with the per-channel operations of
// deemphasisDownsample2.
func deemphasisStereoDownsample2(y, x0, x1 []float32, coef, m0, m1 float32, accum bool) (float32, float32) {
	nd := len(y) / 2
	pairs0 := x0[:2*nd]
	pairs1 := x1[:len(pairs0)]
	for o := range nd {
		p0 := pairs0[2*o : 2*o+2 : 2*o+2]
		p1 := pairs1[2*o : 2*o+2 : 2*o+2]
		out := y[2*o : 2*o+2 : 2*o+2]
		tmp0 := p0[0] + deemphasisVerySmall + m0
		tmp1 := p1[0] + deemphasisVerySmall + m1
		m0 = mul32(coef, tmp0)
		m1 = mul32(coef, tmp1)
		if accum {
			out[0] = fma32(sig2res, tmp0, out[0])
			out[1] = fma32(sig2res, tmp1, out[1])
		} else {
			out[0] = sig2res * tmp0
			out[1] = sig2res * tmp1
		}
		m0 = mul32(coef, p0[1]+deemphasisVerySmall+m0)
		m1 = mul32(coef, p1[1]+deemphasisVerySmall+m1)
	}
	tail1 := x1[len(pairs0):]
	for i, v := range x0[len(pairs0):] {
		m0 = mul32(coef, v+deemphasisVerySmall+m0)
		m1 = mul32(coef, tail1[i]+deemphasisVerySmall+m1)
	}
	return m0, m1
}

// deemphasis2TapChannel is the custom/QEXT deemphasis of one channel used when
// mode->preemph[1] != 0:
//
//	tmp     = x[j] + m + VERY_SMALL
//	m       = coef0*tmp - coef1*x[j]
//	scratch = coef3*tmp        (SHL32 is a no-op in the float build)
//
// followed by the SIG2RES decimation of scratch into y.
func deemphasis2TapChannel(y []float32, yStride int, x []float32, xStride, n, downsample int, coef0, coef1, coef3, m float32, accum bool) float32 {
	nd := n / downsample
	out := 0
	for j := range n {
		xj := x[j*xStride]
		tmp := xj + m + deemphasisVerySmall
		m = fma32(coef0, tmp, -mul32(coef1, xj))
		if j%downsample == 0 && out < nd {
			s := mul32(coef3, tmp)
			if accum {
				// libopus stores the deemphasized highband in scratch, then
				// applies SIG2RES before ADD_RES in a separate pass. Keep the
				// scale multiply rounded before the lowband accumulation.
				scaled := float32(sig2res * s)
				y[out*yStride] += scaled
			} else {
				y[out*yStride] = sig2res * s
			}
			out++
		}
	}
	return m
}
