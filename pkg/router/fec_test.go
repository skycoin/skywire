package router

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestGF256Field checks the core field invariants the coder relies on.
func TestGF256Field(t *testing.T) {
	// a * inv(a) == 1 for all non-zero a.
	for a := 1; a < 256; a++ {
		if got := gfMul(byte(a), gfInv(byte(a))); got != 1 {
			t.Fatalf("a=%d: a*inv(a)=%d, want 1", a, got)
		}
	}
	// multiply by 0 is 0; multiply by 1 is identity.
	for a := 0; a < 256; a++ {
		if gfMul(byte(a), 0) != 0 || gfMul(0, byte(a)) != 0 {
			t.Fatalf("a=%d: mul by 0 != 0", a)
		}
		if gfMul(byte(a), 1) != byte(a) {
			t.Fatalf("a=%d: mul by 1 != a", a)
		}
	}
	// distributivity spot check: a*(b^c) == a*b ^ a*c.
	for _, tc := range [][3]byte{{3, 7, 200}, {255, 128, 1}, {17, 17, 42}} {
		a, b, c := tc[0], tc[1], tc[2]
		if gfMul(a, b^c) != (gfMul(a, b) ^ gfMul(a, c)) {
			t.Fatalf("distributivity failed for %v", tc)
		}
	}
}

// decodeWithErasures is a helper: encode data, drop the given slot indices, decode.
func decodeWithErasures(t *testing.T, c *fecBlockCoder, data [][]byte, erase []int) [][]byte {
	t.Helper()
	repair, err := c.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	n := c.k + c.r
	recv := make([][]byte, n)
	present := make([]bool, n)
	for i := 0; i < c.k; i++ {
		recv[i] = data[i]
		present[i] = true
	}
	for i := 0; i < c.r; i++ {
		recv[c.k+i] = repair[i]
		present[c.k+i] = true
	}
	for _, e := range erase {
		recv[e] = nil
		present[e] = false
	}
	out, err := c.Decode(recv, present)
	if err != nil {
		t.Fatalf("Decode (erase %v): %v", erase, err)
	}
	return out
}

func mkData(k, symLen int, seed int64) [][]byte {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // fixed seed: these tests need reproducible data, not entropy
	data := make([][]byte, k)
	for i := range data {
		data[i] = make([]byte, symLen)
		_, _ = rng.Read(data[i]) // math/rand.Read never returns an error
	}
	return data
}

func eq(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestFECRecoversAllErasurePatterns: for several (k,r), erasing EVERY subset of up
// to r of the k+r symbols must recover the original k data symbols exactly.
func TestFECRecoversAllErasurePatterns(t *testing.T) {
	cases := []struct{ k, r int }{{4, 2}, {6, 3}, {3, 3}, {1, 1}, {8, 4}}
	symLen := 37 // deliberately not a power of two
	for _, tc := range cases {
		c := newFECBlockCoder(tc.k, tc.r, symLen)
		if c == nil {
			t.Fatalf("newFECBlockCoder(%d,%d) nil", tc.k, tc.r)
		}
		data := mkData(tc.k, symLen, int64(tc.k*100+tc.r))
		n := tc.k + tc.r
		// enumerate all erasure subsets of size 0..r
		var subsets func(start, need int, cur []int)
		subsets = func(start, need int, cur []int) {
			if need == 0 {
				out := decodeWithErasures(t, c, data, cur)
				if !eq(out, data) {
					t.Fatalf("k=%d r=%d erase=%v: decode mismatch", tc.k, tc.r, cur)
				}
				return
			}
			for i := start; i <= n-need; i++ {
				subsets(i+1, need-1, append(cur, i))
			}
		}
		for e := 0; e <= tc.r; e++ {
			subsets(0, e, nil)
		}
	}
}

// TestFECErasureBeyondRFails: erasing more than r symbols must NOT silently return
// wrong data — Decode must error (not enough symbols).
func TestFECErasureBeyondRFails(t *testing.T) {
	c := newFECBlockCoder(4, 2, 16)
	data := mkData(4, 16, 7)
	repair, err := c.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	recv := make([][]byte, 6)
	present := make([]bool, 6)
	for i := 0; i < 4; i++ {
		recv[i], present[i] = data[i], true
	}
	for i := 0; i < 2; i++ {
		recv[4+i], present[4+i] = repair[i], true
	}
	// erase 3 (> r=2)
	for _, e := range []int{0, 1, 4} {
		recv[e], present[e] = nil, false
	}
	if _, err := c.Decode(recv, present); err == nil {
		t.Fatal("expected error decoding with >r erasures, got nil")
	}
}

// TestFECSystematicFastPath: with all data present, Decode returns the data
// untouched (systematic, zero reconstruction).
func TestFECSystematicFastPath(t *testing.T) {
	c := newFECBlockCoder(5, 3, 24)
	data := mkData(5, 24, 99)
	repair, err := c.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	recv := make([][]byte, 8)
	present := make([]bool, 8)
	for i := 0; i < 5; i++ {
		recv[i], present[i] = data[i], true
	}
	// repair symbols all missing — data still fully present
	_ = repair
	out, err := c.Decode(recv, present)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !eq(out, data) {
		t.Fatal("systematic fast-path mismatch")
	}
}

// TestFECFuzz: random data + random erasure patterns of size <= r always recover.
func TestFECFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(12345)) //nolint:gosec // fixed seed: the fuzz loop must replay identically on failure
	for iter := 0; iter < 400; iter++ {
		k := 1 + rng.Intn(12)
		r := rng.Intn(6)
		symLen := 1 + rng.Intn(64)
		c := newFECBlockCoder(k, r, symLen)
		if c == nil {
			continue
		}
		data := mkData(k, symLen, int64(iter))
		n := k + r
		// choose a random erasure count 0..r and random positions
		ec := rng.Intn(r + 1)
		perm := rng.Perm(n)[:ec]
		out := decodeWithErasures(t, c, data, perm)
		if !eq(out, data) {
			t.Fatalf("fuzz iter=%d k=%d r=%d erase=%v: mismatch", iter, k, r, perm)
		}
	}
}

func TestFECInvalidDims(t *testing.T) {
	if newFECBlockCoder(0, 2, 16) != nil || newFECBlockCoder(200, 100, 16) != nil ||
		newFECBlockCoder(4, 2, 0) != nil {
		t.Fatal("expected nil for invalid dimensions")
	}
}
