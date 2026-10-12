//go:build amd64 && !amd64.v3

package celt

// kissMulSourceNaNFixup is false: below GOAMD64=v3 the compiler has no fused
// multiply-add to contract a*b + c*d into, so kissMul*SourceNonFinite rounds
// every product exactly as kissMul*Source does and the non-finite path yields
// the same bits.
const kissMulSourceNaNFixup = false
