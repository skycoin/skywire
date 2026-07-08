//go:build !arm64 || purego

package celt

func kfBfly4M1Core(fout []kissCpx, n int) {
	total := n << 2
	_ = fout[total-1] // BCE hint for base+0..3 accesses.
	for i := 0; i < total; i += 4 {
		a0r, a0i := fout[i].r, fout[i].i
		a1r, a1i := fout[i+1].r, fout[i+1].i
		a2r, a2i := fout[i+2].r, fout[i+2].i
		a3r, a3i := fout[i+3].r, fout[i+3].i

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
		f1r := s0r + s1i
		f1i := s0i - s1r
		f3r := s0r - s1i
		f3i := s0i + s1r

		fout[i].r, fout[i].i = f0r, f0i
		fout[i+1].r, fout[i+1].i = f1r, f1i
		fout[i+2].r, fout[i+2].i = f2r, f2i
		fout[i+3].r, fout[i+3].i = f3r, f3i
	}
}
