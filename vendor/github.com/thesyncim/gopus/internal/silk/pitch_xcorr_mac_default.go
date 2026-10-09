//go:build !amd64.v3

package silk

func pitchXcorrMAC32(acc, x, y float32) float32 {
	return acc + noFMA32(x, y)
}
