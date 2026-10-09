//go:build !amd64.v3 || gopus_fixed_point

package celt

func renormalizeEnergy(x []celtNorm) float32 {
	var energy float32
	for i := range x {
		v := float32(x[i])
		energy = celtFloatMulAdd(v, v, energy)
	}
	return energy
}
