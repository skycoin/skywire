//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes && (!goexperiment.simd || nosimd || purego)

package celt

// stereoIthetaNonStereoEnergy matches the scalar celt_inner_prod_norm_shift
// loop in pinned libopus 1.6.1 celt/vq.c:65-71, called by stereo_itheta at
// :741-742 when stereo is false. GCC 13.3 with x86-64-v3 rounds each square
// before adding it to its channel sum.
func stereoIthetaNonStereoEnergy(x, y []celtNorm) (float32, float32) {
	n := min(len(x), len(y))
	x, y = x[:n:n], y[:n:n]
	var emid, eside float32
	for i := range x {
		xv, yv := float32(x[i]), float32(y[i])
		emid = noFMA32Add(emid, noFMA32Mul(xv, xv))
		eside = noFMA32Add(eside, noFMA32Mul(yv, yv))
	}
	return emid, eside
}
