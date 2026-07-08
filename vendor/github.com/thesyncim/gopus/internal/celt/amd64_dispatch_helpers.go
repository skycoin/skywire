//go:build amd64

package celt

func pvqSearchPulseLoopGeneric(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	for i := 0; i < pulsesLeft; i++ {
		yy += 1

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

		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return xy, yy
}

func toneLPCCorrGeneric(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	for i := 0; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+delay]
		r02 += xi * x[i+delay2]
	}
	return
}
