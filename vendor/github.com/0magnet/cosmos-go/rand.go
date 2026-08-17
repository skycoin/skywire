//go:build js && wasm

package cosmos

// splitmix64-based deterministic PRNG, replacing the `random` npm package.
// The exact sequence differs from the original (it only adds tiny jitter to
// initial positions), but a fixed seed still yields a reproducible layout.
type rng struct {
	state uint64
}

func newRng(seed uint64) *rng {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &rng{state: seed}
}

func seedFromString(s string) uint64 {
	var h uint64 = 14695981039346656037 // FNV-1a
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func (r *rng) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// float returns a uniformly distributed value in [min, max).
func (r *rng) float(min, max float64) float64 {
	f := float64(r.next()>>11) / (1 << 53)
	return min + f*(max-min)
}
