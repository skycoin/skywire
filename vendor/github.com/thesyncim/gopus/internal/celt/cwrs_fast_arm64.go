//go:build arm64 && !purego

package celt

//go:noescape
func cwrsiFastCore(n, k int, i uint32, y []int) uint32
