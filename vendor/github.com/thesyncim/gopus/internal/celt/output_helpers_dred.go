//go:build gopus_dred || gopus_osce

package celt

func (d *Decoder) advanceDeemphasisStateMono(samples []float32) {
	n := len(samples)
	if d == nil || d.channels != 1 || n == 0 {
		return
	}
	if d.preemphState[0] == 0 {
		allZero := true
		for i := 0; i < n; i++ {
			if samples[i] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			return
		}
	}
	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	state := d.preemphState[0]
	for i := 0; i < n; i++ {
		tmp := samples[i] + verySmall + state
		state = noFMA32Mul(coef, tmp)
	}
	d.preemphState[0] = state
}
