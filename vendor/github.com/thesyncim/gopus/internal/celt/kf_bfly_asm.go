//go:build (arm64 || amd64) && !purego

package celt

// kfBfly5Inner implements the radix-5 butterfly inner loop in assembly.
// It handles the full double loop (outer i, inner u) with inlined arithmetic,
// eliminating all function call overhead from kissMulAddSource/kissMulSubSource/kissAdd/kissSub.
// ya = w[fstride*m], yb = w[fstride*2*m] are computed internally.
//
//go:noescape
func kfBfly5Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)

// kfBfly3Inner implements the radix-3 butterfly inner loop in assembly.
// epi3 = w[fstride*m] is computed internally.
//
//go:noescape
func kfBfly3Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)

// kfBfly4Inner implements the radix-4 butterfly inner loop in assembly.
//
//go:noescape
func kfBfly4Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
