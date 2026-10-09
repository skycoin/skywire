//go:build !amd64.v3

package celt

func pitchXcorrSSETailMAC32(acc, x, y float32) float32 {
	return noFMA32Add(acc, noFMA32Mul(x, y))
}
