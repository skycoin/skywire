package celt

// kfBfly4M1CoreScalar is the kf_bfly4 m == 1 stage: n twiddle-free radix-4
// butterflies over consecutive groups of four values, using a fixed-size array
// view for each group.
func kfBfly4M1CoreScalar(fout []kissCpx, n int) {
	if n <= 0 {
		return
	}
	groups := fout[:4*n]
	for i := 0; i < len(groups); i += 4 {
		g := (*[4]kissCpx)(groups[i : i+4])
		a0r, a0i := g[0].r, g[0].i
		a1r, a1i := g[1].r, g[1].i
		a2r, a2i := g[2].r, g[2].i
		a3r, a3i := g[3].r, g[3].i

		s0r := a0r - a2r
		s0i := a0i - a2i
		f0r := a0r + a2r
		f0i := a0i + a2i

		s1r := a1r + a3r
		s1i := a1i + a3i
		f2r := f0r - s1r
		f2i := f0i - s1i
		f0r += s1r
		f0i += s1i

		s1r = a1r - a3r
		s1i = a1i - a3i
		g[0] = kissCpx{f0r, f0i}
		g[1] = kissCpx{s0r + s1i, s0i - s1r}
		g[2] = kissCpx{f2r, f2i}
		g[3] = kissCpx{s0r - s1i, s0i + s1r}
	}
}
