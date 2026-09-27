//go:build !arm64 || !purego

package celt

// mdctEncodeFMA32 delegates to mdctFMA32 on every target where the encoder
// and the IMDCT decoder share the same fused-multiply-add semantics; only
// arm64 purego diverges (see the sibling _arm64_purego file).
func mdctEncodeFMA32(a, b, c float32) float32 { return mdctFMA32(a, b, c) }
