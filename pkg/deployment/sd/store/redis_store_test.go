package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestChunkKeys verifies the MGET batching math: every key appears exactly once,
// in order, no batch exceeds the size, and edge cases (empty, <size, ==size,
// exact multiple, off-by-one, non-positive size) behave.
func TestChunkKeys(t *testing.T) {
	mk := func(n int) []string {
		ks := make([]string, n)
		for i := range ks {
			ks[i] = fmt.Sprintf("k%d", i)
		}
		return ks
	}

	cases := []struct {
		name       string
		n, size    int
		wantChunks int
	}{
		{"empty", 0, 256, 0},
		{"single", 1, 256, 1},
		{"under", 100, 256, 1},
		{"exactly_size", 256, 256, 1},
		{"one_over", 257, 256, 2},
		{"exact_multiple", 512, 256, 2},
		{"many", 1000, 256, 4},
		{"size_one", 5, 1, 5},
		{"nonpositive_size", 10, 0, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys := mk(c.n)
			chunks := chunkKeys(keys, c.size)
			assert.Len(t, chunks, c.wantChunks)

			// Reassembling the chunks must reproduce the input exactly (order
			// preserved, nothing dropped or duplicated) — that's what keeps the
			// concatenated MGET result positionally aligned with the keys.
			var flat []string
			for _, ch := range chunks {
				assert.LessOrEqual(t, len(ch), maxBatch(c.size), "no chunk exceeds size")
				assert.NotEmpty(t, ch, "no empty chunks")
				flat = append(flat, ch...)
			}
			if c.n == 0 {
				assert.Empty(t, flat)
			} else {
				assert.Equal(t, keys, flat)
			}
		})
	}
}

// maxBatch is the largest allowed chunk for a given size (a non-positive size
// means "single batch", so any length is allowed).
func maxBatch(size int) int {
	if size <= 0 {
		return 1 << 30
	}
	return size
}
