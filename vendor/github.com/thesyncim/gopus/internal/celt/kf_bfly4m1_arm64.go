//go:build arm64 && !purego

package celt

//go:noescape
func kfBfly4M1Core(fout []kissCpx, n int)
