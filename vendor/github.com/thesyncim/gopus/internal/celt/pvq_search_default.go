//go:build (!arm64 && !amd64) || purego

package celt

// pvqSearchPulseLoop places pulsesLeft pulses using the rate-distortion
// criterion. Go fallback for non-SIMD architectures.
func pvqSearchPulseLoop(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	for i := 0; i < pulsesLeft; i++ {
		yy += 1

		// Find best position
		bestID := 0
		rxy := xy + absX[0]
		ryy := yy + y[0]
		bestNum := rxy * rxy
		bestDen := ryy
		for j := 1; j < n; j++ {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		// Update state
		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return xy, yy
}
