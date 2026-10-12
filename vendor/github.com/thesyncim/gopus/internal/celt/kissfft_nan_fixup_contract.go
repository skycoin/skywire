//go:build !arm64 && !amd64

package celt

// kissMulSourceNaNFixup reports that a NaN twiddle product takes the
// kissMul*SourceNonFinite path, whose unrounded a*b + c*d the compiler may
// contract on this target.
const kissMulSourceNaNFixup = true
